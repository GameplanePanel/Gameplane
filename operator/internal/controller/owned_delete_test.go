package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestDeleteIfControlledBy_RemovesOnlyObjectsTheServerControls(t *testing.T) {
	scheme := testScheme(t)
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "test-server", Namespace: "default", UID: "uid-1"},
	}

	t.Run("deletes controlled Secret", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "secret-1", Namespace: "default"},
		}
		controllerutil.SetControllerReference(gs, secret, scheme)

		r := &GameServerReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(gs, secret).Build(),
			Scheme: scheme,
		}

		err := r.deleteIfControlledBy(context.Background(), gs, secret)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Verify the Secret is deleted
		result := &corev1.Secret{}
		err = r.Get(context.Background(), types.NamespacedName{Name: "secret-1", Namespace: "default"}, result)
		if err == nil {
			t.Error("Secret should be deleted but still exists")
		}
	})

	t.Run("leaves unowned Secret", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret-2",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind:       "GameServer",
						Name:       "other-server",
						UID:        "uid-2",
						Controller: ownerBoolPtr(true),
					},
				},
			},
		}

		r := &GameServerReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(gs, secret).Build(),
			Scheme: scheme,
		}

		err := r.deleteIfControlledBy(context.Background(), gs, secret)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Verify the Secret still exists
		result := &corev1.Secret{}
		err = r.Get(context.Background(), types.NamespacedName{Name: "secret-2", Namespace: "default"}, result)
		if err != nil {
			t.Error("Secret should still exist")
		}
	})

	t.Run("leaves same-named Secret controlled by a different UID", func(t *testing.T) {
		// Regression: a namesake object whose OwnerReference has
		// the right Kind+Name but a DIFFERENT UID (e.g. a stale reference
		// left over from a deleted-and-recreated GameServer, or a crafted
		// collision) must not be treated as owned.
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "secret-3",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind:       "GameServer",
						Name:       gs.Name,
						UID:        "uid-stale",
						Controller: ownerBoolPtr(true),
					},
				},
			},
		}

		r := &GameServerReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(gs, secret).Build(),
			Scheme: scheme,
		}

		err := r.deleteIfControlledBy(context.Background(), gs, secret)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		result := &corev1.Secret{}
		err = r.Get(context.Background(), types.NamespacedName{Name: "secret-3", Namespace: "default"}, result)
		if err != nil {
			t.Error("Secret owned by a different UID should still exist")
		}
	})

	t.Run("ignores missing Secret", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "missing-secret", Namespace: "default"},
		}

		r := &GameServerReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(gs).Build(),
			Scheme: scheme,
		}

		err := r.deleteIfControlledBy(context.Background(), gs, secret)
		if err != nil {
			t.Errorf("unexpected error for missing object: %v", err)
		}
	})

	t.Run("leaves BackupSchedule even if named by controller", func(t *testing.T) {
		bs := &gameplanev1alpha1.BackupSchedule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-server-auto",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{
					{
						Kind:       "GameServer",
						Name:       "test-server",
						UID:        "uid-1",
						Controller: ownerBoolPtr(true),
					},
				},
			},
		}

		r := &GameServerReconciler{
			Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(gs, bs).Build(),
			Scheme: scheme,
		}

		// Attempting to delete a different GVR should not fail, but
		// metav1.IsControlledBy checks Kind, so it only deletes
		// GameServer-controlled objects of the object's own kind
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-server-auto", Namespace: "default"},
		}
		err := r.deleteIfControlledBy(context.Background(), gs, secret)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Verify BackupSchedule still exists
		result := &gameplanev1alpha1.BackupSchedule{}
		err = r.Get(context.Background(), types.NamespacedName{Name: "test-server-auto", Namespace: "default"}, result)
		if err != nil {
			t.Error("BackupSchedule should still exist")
		}
	})
}

// TestDeleteIfControlledBy_DeleteIsUIDPreconditioned is a regression test:
// the Delete call must carry a UID precondition matching the object read
// during the ownership check, so a replacement object created at the same
// name between that read and the Delete reaching the API server is refused
// (a real API server rejects a UID-precondition mismatch) instead of being
// deleted just because it shares the name.
func TestDeleteIfControlledBy_DeleteIsUIDPreconditioned(t *testing.T) {
	scheme := testScheme(t)
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "test-server", Namespace: "default", UID: "uid-1"},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "secret-1", Namespace: "default", UID: "uid-secret-1"},
	}
	controllerutil.SetControllerReference(gs, secret, scheme)

	var gotUID *types.UID
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gs, secret).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				do := client.DeleteOptions{}
				do.ApplyOptions(opts)
				if do.Preconditions != nil {
					gotUID = do.Preconditions.UID
				}
				return c.Delete(ctx, obj, opts...)
			},
		}).Build()

	r := &GameServerReconciler{Client: cl, Scheme: scheme}

	if err := r.deleteIfControlledBy(context.Background(), gs, secret); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotUID == nil {
		t.Fatal("Delete was called without a UID precondition")
	}
	if *gotUID != secret.UID {
		t.Errorf("Delete UID precondition = %q, want %q", *gotUID, secret.UID)
	}
}
