package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestVolumeSnapshotRestore_TransientErrorsRequeueNotFail drives the
// volume-snapshot restore through the window right after the restored
// GameServer is created, where the informer cache has not yet seen it and
// writes can hit transient API errors. None of that may mark the Restore
// Failed: each pass must requeue, and once the errors clear the copy is
// made and the restored server's references point at it.
func TestVolumeSnapshotRestore_TransientErrorsRequeueNotFail(t *testing.T) {
	const (
		ns           = "ns"
		origName     = "smp"
		restoredName = "smp-restored"
		secretName   = "smp-secret"
	)
	ctx := context.Background()
	s := scrapeScheme(t)

	orig := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: origName, Namespace: ns, UID: "uid-orig"},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "tmpl"},
			Env: []corev1.EnvVar{{
				Name: "SECRET_VAL",
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  "key",
				}},
			}},
		},
	}
	isController := true
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName, Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gameplanev1alpha1.GroupVersion.String(),
				Kind:       "GameServer",
				Name:       origName,
				UID:        orig.UID,
				Controller: &isController,
			}},
		},
		Data: map[string][]byte{"key": []byte("orig-value")},
	}
	backup := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "smp-vs", Namespace: ns},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: origName},
			Strategy:  "volume-snapshot",
		},
		Status: gameplanev1alpha1.BackupStatus{
			SnapshotID:                "snap-1",
			VolumeSnapshotContentName: "snapcontent-smp-vs",
		},
	}
	restore := &gameplanev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: ns},
		Spec: gameplanev1alpha1.RestoreSpec{
			BackupRef: gameplanev1alpha1.LocalObjectRef{Name: "smp-vs"},
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: restoredName},
		},
		Status: gameplanev1alpha1.RestoreStatus{SnapshotID: "snap-1"},
	}

	// staleRestored simulates a cache that has not observed the restored
	// GameServer yet; failSecretCreates simulates transient write errors.
	var (
		restoredCreated   bool
		staleRestored     = true
		failSecretCreates = 2
	)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(orig, secret, backup, restore).
		WithStatusSubresource(&gameplanev1alpha1.Restore{}, &gameplanev1alpha1.Backup{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*gameplanev1alpha1.GameServer); ok &&
					key.Name == restoredName && restoredCreated && staleRestored {
					return apierrors.NewNotFound(schema.GroupResource{Group: "gameplane.local", Resource: "gameservers"}, key.Name)
				}
				return c.Get(ctx, key, obj, opts...)
			},
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				switch o := obj.(type) {
				case *gameplanev1alpha1.GameServer:
					if o.Name == restoredName {
						restoredCreated = true
					}
				case *corev1.Secret:
					if failSecretCreates > 0 {
						failSecretCreates--
						return apierrors.NewServerTimeout(schema.GroupResource{Resource: "secrets"}, "create", 1)
					}
				}
				return c.Create(ctx, obj, opts...)
			},
		}).
		Build()
	r := &RestoreReconciler{Client: cl, Scheme: s}

	pass := func(label string) error {
		t.Helper()
		var rs gameplanev1alpha1.Restore
		if err := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: "rs"}, &rs); err != nil {
			t.Fatalf("%s: get restore: %v", label, err)
		}
		var b gameplanev1alpha1.Backup
		if err := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: "smp-vs"}, &b); err != nil {
			t.Fatalf("%s: get backup: %v", label, err)
		}
		_, err := r.reconcileVolumeSnapshotRestore(ctx, &rs, &b)
		var after gameplanev1alpha1.Restore
		if gerr := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: "rs"}, &after); gerr != nil {
			t.Fatalf("%s: get restore after pass: %v", label, gerr)
		}
		if after.Status.Phase == gameplanev1alpha1.RestorePhaseFailed {
			t.Fatalf("%s: restore marked Failed on a transient error: %s", label, after.Status.Message)
		}
		return err
	}

	// Pass 1: the server is created, the copy write fails transiently.
	if err := pass("create pass"); err == nil {
		t.Fatal("create pass: want the transient copy error returned for requeue, got nil")
	}
	// Pass 2: the cache still misses the new server; the create path must
	// see AlreadyExists and requeue.
	if err := pass("stale-cache pass"); err != nil {
		t.Fatalf("stale-cache pass: want requeue without error, got %v", err)
	}
	// Pass 3: cache caught up; the await path re-ensures the copy but the
	// write still fails transiently.
	staleRestored = false
	if err := pass("await pass with write error"); err == nil {
		t.Fatal("await pass: want the transient copy error returned for requeue, got nil")
	}
	// Pass 4: errors cleared; the copy is made.
	if err := pass("await pass"); err != nil {
		t.Fatalf("await pass: unexpected error: %v", err)
	}

	copyName := restoredRefName(restoredName, secretName)
	var restored gameplanev1alpha1.GameServer
	if err := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: restoredName}, &restored); err != nil {
		t.Fatalf("get restored server: %v", err)
	}
	if got := restored.Spec.Env[0].ValueFrom.SecretKeyRef.Name; got != copyName {
		t.Errorf("restored env secret ref = %q, want copy %q", got, copyName)
	}
	var cp corev1.Secret
	if err := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: copyName}, &cp); err != nil {
		t.Fatalf("get copied secret: %v", err)
	}
	if !metav1.IsControlledBy(&cp, &restored) {
		t.Error("copied secret is not controller-owned by the restored server")
	}
	var rs gameplanev1alpha1.Restore
	if err := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: "rs"}, &rs); err != nil {
		t.Fatalf("get restore: %v", err)
	}
	if rs.Status.StartTime == nil {
		t.Error("restore StartTime not recorded, so the start deadline would never apply")
	}
}

// TestFailOrRequeue_OnlyOwnershipIsTerminal checks that only an ownership
// refusal marks the Restore Failed; any other error is handed back for a
// requeue with the Restore left untouched.
func TestFailOrRequeue_OnlyOwnershipIsTerminal(t *testing.T) {
	ctx := context.Background()
	s := scrapeScheme(t)
	rs := &gameplanev1alpha1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"}}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(rs).
		WithStatusSubresource(&gameplanev1alpha1.Restore{}).Build()
	r := &RestoreReconciler{Client: cl, Scheme: s}

	transient := apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, "x", errors.New("changed"))
	if _, err := r.failOrRequeue(ctx, rs, fmt.Errorf("ensure copy: %w", transient)); err == nil {
		t.Fatal("transient error: want it returned for requeue, got nil")
	}
	if rs.Status.Phase == gameplanev1alpha1.RestorePhaseFailed {
		t.Fatal("transient error marked the Restore Failed")
	}

	if _, err := r.failOrRequeue(ctx, rs, fmt.Errorf("secret %q: %w", "x", errRefNotOwned)); err != nil {
		t.Fatalf("ownership error: unexpected error %v", err)
	}
	var got gameplanev1alpha1.Restore
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "rs"}, &got); err != nil {
		t.Fatalf("get restore: %v", err)
	}
	if got.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
		t.Errorf("ownership error: phase = %q, want Failed", got.Status.Phase)
	}
}

// TestRewriteRefs_PointsEnvAndTunnelAtCopies checks the pre-Create rewrite
// covers env Secret and ConfigMap refs and the tunnel credentials Secret,
// and leaves unrelated references alone.
func TestRewriteRefs_PointsEnvAndTunnelAtCopies(t *testing.T) {
	spec := gameplanev1alpha1.GameServerSpec{
		Env: []corev1.EnvVar{
			{Name: "A", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "sec"}, Key: "k"}}},
			{Name: "B", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "cm"}, Key: "k"}}},
			{Name: "C", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "sec"}, Key: "k"}}},
			{Name: "D", Value: "plain"},
		},
	}
	spec.Networking.Tunnel = &gameplanev1alpha1.GameServerTunnel{
		CredentialsSecretRef: &gameplanev1alpha1.SecretNameRef{Name: "tun"},
	}
	rewriteRefs(&spec, []refCopy{
		{Kind: secretRefKind, OrigName: "sec", CopyName: "sec-copy"},
		{Kind: configMapRefKind, OrigName: "cm", CopyName: "cm-copy"},
		{Kind: secretRefKind, OrigName: "tun", CopyName: "tun-copy"},
	})
	if got := spec.Env[0].ValueFrom.SecretKeyRef.Name; got != "sec-copy" {
		t.Errorf("env secret ref = %q, want sec-copy", got)
	}
	if got := spec.Env[1].ValueFrom.ConfigMapKeyRef.Name; got != "cm-copy" {
		t.Errorf("env configmap ref = %q, want cm-copy", got)
	}
	if got := spec.Env[2].ValueFrom.ConfigMapKeyRef.Name; got != "sec" {
		t.Errorf("configmap ref sharing a Secret copy's name = %q, want it unchanged", got)
	}
	if got := spec.Networking.Tunnel.CredentialsSecretRef.Name; got != "tun-copy" {
		t.Errorf("tunnel credentials ref = %q, want tun-copy", got)
	}
}

// TestReconcileVolumeSnapshotRestore_PersistsCopyPlan is a regression test
// for the "restore.gameplane.local/copy-plan" annotation: a successful
// create pass must actually write it to the live Restore (client.MergeFrom
// snapshots its argument by reference, so taking that snapshot after the
// annotation is already set silently computes an empty patch and never
// writes anything), and the persisted JSON must round-trip through
// refCopy's exported fields back into the same plan.
func TestReconcileVolumeSnapshotRestore_PersistsCopyPlan(t *testing.T) {
	ctx := context.Background()
	s := scrapeScheme(t)

	const (
		ns           = "ns"
		origName     = "orig"
		restoredName = "restored"
		secretName   = "owned-secret"
	)

	orig := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: origName, Namespace: ns, UID: "uid-orig"},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "tmpl"},
			Env: []corev1.EnvVar{{
				Name: "V",
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  "key",
				}},
			}},
		},
	}
	isController := true
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName, Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gameplanev1alpha1.GroupVersion.String(),
				Kind:       "GameServer",
				Name:       origName,
				UID:        orig.UID,
				Controller: &isController,
			}},
		},
		Data: map[string][]byte{"key": []byte("v")},
	}
	backup := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: ns},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: origName},
			Strategy:  "volume-snapshot",
		},
		Status: gameplanev1alpha1.BackupStatus{
			SnapshotID:                "snap-1",
			VolumeSnapshotContentName: "snapcontent-b",
		},
	}
	restore := &gameplanev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: ns},
		Spec: gameplanev1alpha1.RestoreSpec{
			BackupRef: gameplanev1alpha1.LocalObjectRef{Name: "b"},
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: restoredName},
		},
		Status: gameplanev1alpha1.RestoreStatus{SnapshotID: "snap-1"},
	}

	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(orig, secret, backup, restore).
		WithStatusSubresource(&gameplanev1alpha1.Restore{}, &gameplanev1alpha1.Backup{}).
		Build()
	r := &RestoreReconciler{Client: cl, Scheme: s}

	var rs gameplanev1alpha1.Restore
	if err := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: "rs"}, &rs); err != nil {
		t.Fatalf("get restore: %v", err)
	}
	var b gameplanev1alpha1.Backup
	if err := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: "b"}, &b); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	if _, err := r.reconcileVolumeSnapshotRestore(ctx, &rs, &b); err != nil {
		t.Fatalf("reconcileVolumeSnapshotRestore: %v", err)
	}

	var after gameplanev1alpha1.Restore
	if err := cl.Get(ctx, types.NamespacedName{Namespace: ns, Name: "rs"}, &after); err != nil {
		t.Fatalf("get restore after pass: %v", err)
	}
	planJSON := after.Annotations["restore.gameplane.local/copy-plan"]
	if planJSON == "" {
		t.Fatal("copy-plan annotation was not persisted on the live Restore")
	}
	var plan []refCopy
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		t.Fatalf("unmarshal persisted copy plan: %v", err)
	}
	want := []refCopy{{Kind: secretRefKind, OrigName: secretName, CopyName: restoredRefName(restoredName, secretName)}}
	if !reflect.DeepEqual(plan, want) {
		t.Errorf("persisted copy plan = %+v, want %+v", plan, want)
	}
}
