package controller

import (
	"context"
	"fmt"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestWipePreservesForeignJobs(t *testing.T) {
	for _, pending := range []bool{false, true} {
		for _, succeeded := range []bool{false, true} {
			for _, owner := range []string{"none", "backup", "old-server"} {
				t.Run(fmt.Sprintf("pending=%t/succeeded=%t/owner=%s", pending, succeeded, owner), func(t *testing.T) {
					scheme := wipeScheme(t)
					gs := wipeGameServer(true, "")
					if pending {
						gs.Annotations[WipeRequestedAnnotation] = "token"
					}
					job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-wipe", Namespace: gs.Namespace, UID: "foreign-job", Labels: map[string]string{wipeTokenLabel: "token"}}}
					if succeeded {
						job.Status.Succeeded = 1
					}
					switch owner {
					case "backup":
						backup := &gameplanev1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Name: job.Name, Namespace: job.Namespace, UID: "backup"}}
						if err := controllerutil.SetControllerReference(backup, job, scheme); err != nil {
							t.Fatal(err)
						}
					case "old-server":
						old := gs.DeepCopy()
						old.UID = "previous-incarnation"
						if err := controllerutil.SetControllerReference(old, job, scheme); err != nil {
							t.Fatal(err)
						}
					}
					c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gs, job).Build()
					r := &GameServerReconciler{Client: c, APIReader: c, Scheme: scheme}
					err := r.reconcileWipe(t.Context(), gs, &gameplanev1alpha1.GameTemplate{})
					if pending && err == nil {
						t.Fatal("pending wipe did not report foreign Job collision")
					}
					if !pending && err != nil {
						t.Fatal(err)
					}
					var retained batchv1.Job
					if err := c.Get(t.Context(), client.ObjectKeyFromObject(job), &retained); err != nil {
						t.Fatalf("foreign Job removed: %v", err)
					}
					var current gameplanev1alpha1.GameServer
					if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &current); err != nil {
						t.Fatal(err)
					}
					if current.Annotations[WipeCompletedAnnotation] != "" || !current.Spec.Suspend {
						t.Fatal("foreign Job acknowledged wipe or resumed server")
					}
				})
			}
		}
	}
}

func TestWipeCleanupPreservesJobReplacedDuringDelete(t *testing.T) {
	scheme := wipeScheme(t)
	gs := wipeGameServer(false, "")
	gs.Annotations[wipeGuardAnnotation] = wipeOrphanGuard
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-wipe", Namespace: gs.Namespace, UID: "owned-job"}}
	if err := controllerutil.SetControllerReference(gs, job, scheme); err != nil {
		t.Fatal(err)
	}
	var preconditioned bool
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gs, job).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			options := &client.DeleteOptions{}
			options.ApplyOptions(opts)
			preconditioned = options.Preconditions != nil && options.Preconditions.UID != nil && *options.Preconditions.UID == job.UID
			if err := c.Delete(ctx, obj); err != nil {
				return err
			}
			replacement := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: job.Name, Namespace: job.Namespace, UID: "replacement"}}
			if err := c.Create(ctx, replacement); err != nil {
				return err
			}
			// The fake client does not enforce UID preconditions; emulate the API server.
			if options.Preconditions != nil && options.Preconditions.UID != nil && *options.Preconditions.UID != replacement.UID {
				return apierrors.NewConflict(batchv1.Resource("jobs"), replacement.Name, fmt.Errorf("UID precondition %s does not match replacement UID %s", *options.Preconditions.UID, replacement.UID))
			}
			return c.Delete(ctx, obj, opts...)
		},
	}).Build()
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: scheme}
	if err := r.reconcileWipe(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}); !apierrors.IsConflict(err) {
		t.Fatalf("replacement delete error = %v, want conflict", err)
	}
	if !preconditioned {
		t.Fatal("wipe cleanup omitted the verified Job UID")
	}
	var retained batchv1.Job
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(job), &retained); err != nil {
		t.Fatal(err)
	}
	if retained.UID != "replacement" {
		t.Fatalf("remaining Job UID=%s", retained.UID)
	}
}
