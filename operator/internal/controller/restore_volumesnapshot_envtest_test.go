//go:build envtest

package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestVolumeSnapshotRestore_RefsCopiedWithNewNamesAndOwned is a
// regression test for reference copying: a volume-snapshot restore must
// not let the restored server inherit references it doesn't own. The
// original server's owned Secret is copied under a new name, controller-
// owned by the restored server, and the restored server's env is rewritten
// to point at the copy.
//
// It also covers the awaitRestoredServer original-server lookup: the
// original server must be re-resolved via the source Backup's ServerRef,
// not via rs.Spec.BackupRef.Name, so ensureRestoredRefs keeps running on
// every pass — checked here by deleting the copy and expecting it to
// reappear on the next reconcile.
func TestVolumeSnapshotRestore_RefsCopiedWithNewNamesAndOwned(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	orig := buildGameServer(ns, "smp", "seed-template")
	if err := k8sClient.Create(context.Background(), orig); err != nil {
		t.Fatalf("create original gameserver: %v", err)
	}
	deleteCleanup(t, orig)

	origSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "smp-owned-secret", Namespace: ns},
		Data:       map[string][]byte{"key": []byte("orig-value")},
	}
	if err := controllerutil.SetControllerReference(orig, origSecret, scheme); err != nil {
		t.Fatalf("set controller ref: %v", err)
	}
	if err := k8sClient.Create(context.Background(), origSecret); err != nil {
		t.Fatalf("create original secret: %v", err)
	}
	deleteCleanup(t, origSecret)

	if err := retryUpdateGameServer(t, ns, "smp", func(gs *gameplanev1alpha1.GameServer) {
		gs.Spec.Env = []corev1.EnvVar{{
			Name: "SECRET_VAL",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: origSecret.Name},
					Key:                  "key",
				},
			},
		}}
	}); err != nil {
		t.Fatalf("set env on original gameserver: %v", err)
	}

	if err := k8sClient.Create(context.Background(), buildVolumeSnapshotBackup(ns, "smp-vs", "smp")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	deleteCleanup(t, &gameplanev1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "smp-vs", Namespace: ns}})
	markBackupSucceededVolumeSnapshot(t, ns, "smp-vs", "snap-1", "snapcontent-smp-vs")

	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-1", "smp-vs", "smp-restored")); err != nil {
		t.Fatalf("create restore: %v", err)
	}
	deleteCleanup(t, &gameplanev1alpha1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "rs-1", Namespace: ns}})

	var restored *gameplanev1alpha1.GameServer
	var copyName string
	eventually(t, func() (bool, string) {
		var gs gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: "smp-restored"}, &gs); err != nil {
			return false, "restored gameserver not yet created: " + err.Error()
		}
		if len(gs.Spec.Env) != 1 || gs.Spec.Env[0].ValueFrom == nil || gs.Spec.Env[0].ValueFrom.SecretKeyRef == nil {
			return false, "restored gameserver env not yet rewritten"
		}
		copyName = gs.Spec.Env[0].ValueFrom.SecretKeyRef.Name
		if copyName == origSecret.Name {
			return false, "restored env still references the original secret name"
		}
		restored = &gs
		return true, ""
	})

	// The copy must exist, carry the original data, and be owned by the
	// restored server (not the original). The copy is made after the
	// restored server is created (it needs that server's UID), so wait for it.
	var copySecret corev1.Secret
	eventually(t, func() (bool, string) {
		if err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: copyName}, &copySecret); err != nil {
			return false, "copied secret " + copyName + " not yet created: " + err.Error()
		}
		return true, ""
	})
	if string(copySecret.Data["key"]) != "orig-value" {
		t.Errorf("copied secret data = %q, want %q", copySecret.Data["key"], "orig-value")
	}
	if !metav1.IsControlledBy(&copySecret, restored) {
		t.Error("copied secret is not controller-owned by the restored gameserver")
	}

	// Prove ensureRestoredRefs keeps re-running on every awaitRestoredServer
	// pass: delete the copy and
	// expect the controller to recreate it without any external nudge,
	// since the restored server never reaches Running in envtest (no
	// kubelet) and so keeps polling.
	if err := k8sClient.Delete(context.Background(), &copySecret); err != nil {
		t.Fatalf("delete copy secret to force re-ensure: %v", err)
	}
	eventually(t, func() (bool, string) {
		var again corev1.Secret
		err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: copyName}, &again)
		return err == nil, "copy secret was not re-created by ensureRestoredRefs on a later pass"
	})
}

// TestVolumeSnapshotRestore_FailsOnUnownedRef is a regression test
// for the failure path: if the original server references a Secret it
// does not own, the restore must refuse rather than silently copying (or
// worse, inheriting) an object it has no ownership claim over.
func TestVolumeSnapshotRestore_FailsOnUnownedRef(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	orig := buildGameServer(ns, "smp2", "seed-template")
	if err := k8sClient.Create(context.Background(), orig); err != nil {
		t.Fatalf("create original gameserver: %v", err)
	}
	deleteCleanup(t, orig)

	// A Secret orig references but does NOT own (no OwnerReference at all).
	foreignSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "unowned-secret", Namespace: ns},
		Data:       map[string][]byte{"key": []byte("value")},
	}
	if err := k8sClient.Create(context.Background(), foreignSecret); err != nil {
		t.Fatalf("create foreign secret: %v", err)
	}
	deleteCleanup(t, foreignSecret)

	if err := retryUpdateGameServer(t, ns, "smp2", func(gs *gameplanev1alpha1.GameServer) {
		gs.Spec.Env = []corev1.EnvVar{{
			Name: "SECRET_VAL",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: foreignSecret.Name},
					Key:                  "key",
				},
			},
		}}
	}); err != nil {
		t.Fatalf("set env on original gameserver: %v", err)
	}

	if err := k8sClient.Create(context.Background(), buildVolumeSnapshotBackup(ns, "smp2-vs", "smp2")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	deleteCleanup(t, &gameplanev1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "smp2-vs", Namespace: ns}})
	markBackupSucceededVolumeSnapshot(t, ns, "smp2-vs", "snap-2", "snapcontent-smp2-vs")

	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-2", "smp2-vs", "smp2-restored")); err != nil {
		t.Fatalf("create restore: %v", err)
	}
	deleteCleanup(t, &gameplanev1alpha1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "rs-2", Namespace: ns}})

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-2")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		if r.Status.Message == "" {
			return false, "Failed but no message"
		}
		return true, ""
	})

	// The restored server must never have been created — a failed
	// ownership check must refuse before Create, not after.
	var gs gameplanev1alpha1.GameServer
	err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: ns, Name: "smp2-restored"}, &gs)
	if err == nil {
		t.Error("restored gameserver should not have been created when a ref is unowned")
	}
}

// TestVolumeSnapshotRestore_FailsAfterDeadline is the deadline regression
// test: a restored server that never reaches Running must not poll
// forever — awaitRestoredServer must fail the Restore once
// VolumeSnapshotRestoreDeadline has elapsed since rs.Status.StartTime.
//
// Waiting out the real 10-minute deadline would make this test far too
// slow; instead it backdates StartTime, which is exactly what elapses in
// production once enough real wall-clock time has passed.
func TestVolumeSnapshotRestore_FailsAfterDeadline(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withRestoreReconciler())

	orig := buildGameServer(ns, "smp3", "seed-template")
	if err := k8sClient.Create(context.Background(), orig); err != nil {
		t.Fatalf("create original gameserver: %v", err)
	}
	deleteCleanup(t, orig)

	if err := k8sClient.Create(context.Background(), buildVolumeSnapshotBackup(ns, "smp3-vs", "smp3")); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	deleteCleanup(t, &gameplanev1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "smp3-vs", Namespace: ns}})
	markBackupSucceededVolumeSnapshot(t, ns, "smp3-vs", "snap-3", "snapcontent-smp3-vs")

	if err := k8sClient.Create(context.Background(), buildRestore(ns, "rs-3", "smp3-vs", "smp3-restored")); err != nil {
		t.Fatalf("create restore: %v", err)
	}
	deleteCleanup(t, &gameplanev1alpha1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "rs-3", Namespace: ns}})

	// Wait for StartTime to be recorded (it is set on the pass that creates
	// the restored server, just before Create) — envtest runs no kubelet,
	// so the server never reaches Running and the Restore just keeps
	// polling from here.
	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-3")
		return r.Status.StartTime != nil, "waiting for restore StartTime to be recorded"
	})

	// Backdate StartTime past the deadline.
	if err := retry3(func() error {
		r := getRestore(t, ns, "rs-3")
		past := metav1.NewTime(time.Now().Add(-(VolumeSnapshotRestoreDeadline + time.Minute)))
		r.Status.StartTime = &past
		return k8sClient.Status().Update(context.Background(), r)
	}); err != nil {
		t.Fatalf("backdate restore start time: %v", err)
	}

	eventually(t, func() (bool, string) {
		r := getRestore(t, ns, "rs-3")
		if r.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
			return false, describeRestoreStatus(r)
		}
		if r.Status.Message == "" {
			return false, "Failed but no deadline message"
		}
		return true, ""
	})
}

// retryUpdateGameServer retries a GameServer Spec mutation on conflict —
// the reconciler updates the object concurrently in these tests.
func retryUpdateGameServer(t *testing.T, ns, name string, mutate func(*gameplanev1alpha1.GameServer)) error {
	t.Helper()
	return retry3(func() error {
		gs := getGameServer(t, ns, name)
		mutate(gs)
		return k8sClient.Update(context.Background(), gs)
	})
}

// retry3 retries fn up to 3 times, which is enough to ride out the
// occasional envtest resourceVersion conflict without pulling in
// client-go's retry package for a one-off status update in these tests.
func retry3(fn func() error) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = fn(); err == nil {
			return nil
		}
	}
	return err
}
