package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestPlanOwnedRefCopies_MissingSourceIsTransientNotTerminal is a
// regression test: a referenced Secret that Get reports NotFound (whether
// genuinely absent or just not yet visible in the informer cache) must
// surface as a plain error, not collapse into "not owned" (errRefNotOwned),
// so the caller requeues instead of permanently failing the Restore.
func TestPlanOwnedRefCopies_MissingSourceIsTransientNotTerminal(t *testing.T) {
	ctx := context.Background()
	s := scrapeScheme(t)

	orig := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "orig", Namespace: "ns", UID: "uid-orig"},
		Spec: gameplanev1alpha1.GameServerSpec{
			Env: []corev1.EnvVar{{
				Name: "V",
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "not-yet-cached-secret"},
					Key:                  "key",
				}},
			}},
		},
	}
	restored := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "restored", Namespace: "ns", UID: "uid-restored"},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(orig, restored).Build()
	r := &RestoreReconciler{Client: cl, Scheme: s}

	_, err := r.planOwnedRefCopies(ctx, orig, restored)
	if err == nil {
		t.Fatal("want an error for a missing referenced Secret, got nil")
	}
	if errors.Is(err, errRefNotOwned) {
		t.Errorf("missing (possibly not-yet-cached) source must not be treated as an ownership refusal, got %v", err)
	}
}

// TestEnsureOwnedRefCopies_RejectsSourceReplacedSinceOwnershipCheck is a
// regression test: ownership is validated during planning, but the source
// object could be replaced with an unowned object at the same name before
// the copy is actually made. ensureOwnedRefCopies must re-check ownership
// against the object it is about to copy, not trust the plan alone.
func TestEnsureOwnedRefCopies_RejectsSourceReplacedSinceOwnershipCheck(t *testing.T) {
	ctx := context.Background()
	s := scrapeScheme(t)

	orig := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "orig", Namespace: "ns", UID: "uid-orig"},
	}
	restored := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "restored", Namespace: "ns", UID: "uid-restored"},
	}

	// A Secret named the same as what was planned, but NOT owned by orig
	// (e.g. it was deleted and replaced between planning and the copy).
	unownedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "replaced-secret", Namespace: "ns"},
		Data:       map[string][]byte{"key": []byte("attacker-controlled")},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(orig, restored, unownedSecret).Build()
	r := &RestoreReconciler{Client: cl, Scheme: s}

	copies := []refCopy{{Kind: secretRefKind, OrigName: "replaced-secret", CopyName: "restored-ref-copy"}}
	err := r.ensureOwnedRefCopies(ctx, orig, restored, copies)
	if err == nil {
		t.Fatal("want an error copying an unowned replacement, got nil")
	}
	if !errors.Is(err, errRefNotOwned) {
		t.Errorf("want errRefNotOwned, got %v", err)
	}

	var cp corev1.Secret
	getErr := cl.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "restored-ref-copy"}, &cp)
	if getErr == nil {
		t.Error("copy Secret should not have been created from an unowned source")
	}
}

// TestEnsureOwnedRefCopies_ConfigMapCopiesBinaryData is a regression test:
// a new ConfigMap copy must inherit BinaryData as well as Data, or entries
// under BinaryData silently disappear from the restored server's copy.
func TestEnsureOwnedRefCopies_ConfigMapCopiesBinaryData(t *testing.T) {
	ctx := context.Background()
	s := scrapeScheme(t)

	restored := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "restored", Namespace: "ns", UID: "uid-restored"},
	}
	orig := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "orig", Namespace: "ns", UID: "uid-orig"},
	}
	src := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: "src-cm", Namespace: "ns",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: gameplanev1alpha1.GroupVersion.String(),
				Kind:       "GameServer", Name: orig.Name, UID: orig.UID,
				Controller: ownerBoolPtr(true),
			}},
		},
		Data:       map[string]string{"text.txt": "hello"},
		BinaryData: map[string][]byte{"blob.bin": {0x00, 0x01, 0x02}},
	}

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(orig, restored, src).Build()
	r := &RestoreReconciler{Client: cl, Scheme: s}

	copies := []refCopy{{Kind: configMapRefKind, OrigName: "src-cm", CopyName: "dst-cm"}}
	if err := r.ensureOwnedRefCopies(ctx, orig, restored, copies); err != nil {
		t.Fatalf("ensureOwnedRefCopies: %v", err)
	}

	var dst corev1.ConfigMap
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "dst-cm"}, &dst); err != nil {
		t.Fatalf("get copy ConfigMap: %v", err)
	}
	if dst.Data["text.txt"] != "hello" {
		t.Errorf("copy Data = %v, want text.txt=hello", dst.Data)
	}
	if string(dst.BinaryData["blob.bin"]) != "\x00\x01\x02" {
		t.Errorf("copy BinaryData missing blob.bin, got %v", dst.BinaryData)
	}
}

// TestEnsureOwnedRefCopies_ExistingCopyMustBeControlledByRestored is a
// regression test: an object already sitting at the copy name that carries
// only a plain (non-controller) OwnerReference to the restored server must
// not be accepted as the restored server's copy, or the restore would
// proceed with contents the restored server does not control. A copy that
// the restored server does control is accepted as-is.
func TestEnsureOwnedRefCopies_ExistingCopyMustBeControlledByRestored(t *testing.T) {
	orig := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "orig", Namespace: "ns", UID: "uid-orig"},
	}
	restored := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "restored", Namespace: "ns", UID: "uid-restored"},
	}
	ownerRef := func(gs *gameplanev1alpha1.GameServer, controller bool) []metav1.OwnerReference {
		return []metav1.OwnerReference{{
			APIVersion: gameplanev1alpha1.GroupVersion.String(),
			Kind:       "GameServer", Name: gs.Name, UID: gs.UID,
			Controller: ownerBoolPtr(controller),
		}}
	}
	// A non-zero CreationTimestamp marks the destination as pre-existing
	// (the fake client does not set one itself).
	existing := func(name string, controller bool) metav1.ObjectMeta {
		return metav1.ObjectMeta{
			Name: name, Namespace: "ns",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
			OwnerReferences:   ownerRef(restored, controller),
		}
	}

	for _, controller := range []bool{false, true} {
		t.Run(fmt.Sprintf("controller=%v", controller), func(t *testing.T) {
			ctx := context.Background()
			s := scrapeScheme(t)
			srcSec := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "src-sec", Namespace: "ns", OwnerReferences: ownerRef(orig, true)},
				Data:       map[string][]byte{"key": []byte("fresh")},
			}
			srcCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "src-cm", Namespace: "ns", OwnerReferences: ownerRef(orig, true)},
				Data:       map[string]string{"key": "fresh"},
			}
			dstSec := &corev1.Secret{
				ObjectMeta: existing("dst-sec", controller),
				Data:       map[string][]byte{"key": []byte("stale")},
			}
			dstCM := &corev1.ConfigMap{
				ObjectMeta: existing("dst-cm", controller),
				Data:       map[string]string{"key": "stale"},
			}
			cl := fake.NewClientBuilder().WithScheme(s).
				WithObjects(orig, restored, srcSec, srcCM, dstSec, dstCM).Build()
			r := &RestoreReconciler{Client: cl, Scheme: s}

			for _, cp := range []refCopy{
				{Kind: secretRefKind, OrigName: "src-sec", CopyName: "dst-sec"},
				{Kind: configMapRefKind, OrigName: "src-cm", CopyName: "dst-cm"},
			} {
				err := r.ensureOwnedRefCopies(ctx, orig, restored, []refCopy{cp})
				if controller {
					if err != nil {
						t.Errorf("%s %q: a copy controlled by the restored server must be accepted, got %v", cp.Kind, cp.CopyName, err)
					}
				} else if !errors.Is(err, errRefNotOwned) {
					t.Errorf("%s %q: want errRefNotOwned for a copy with only a non-controller OwnerReference, got %v", cp.Kind, cp.CopyName, err)
				}
			}

			// Either way the pre-existing copy is left untouched.
			var gotSec corev1.Secret
			if err := cl.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "dst-sec"}, &gotSec); err != nil {
				t.Fatalf("get dst Secret: %v", err)
			}
			if got := string(gotSec.Data["key"]); got != "stale" {
				t.Errorf("dst Secret data = %q, want %q (untouched)", got, "stale")
			}
			var gotCM corev1.ConfigMap
			if err := cl.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "dst-cm"}, &gotCM); err != nil {
				t.Fatalf("get dst ConfigMap: %v", err)
			}
			if got := gotCM.Data["key"]; got != "stale" {
				t.Errorf("dst ConfigMap data = %q, want %q (untouched)", got, "stale")
			}
		})
	}
}

// TestReconcileVolumeSnapshotRestore_DeadlineBoundsMissingRefBeforeCreate is
// a regression test: a missing referenced Secret requeues (it may simply not
// be cached yet), but before the restored server is created that requeue
// must still be bounded by the restore deadline instead of repeating
// forever.
func TestReconcileVolumeSnapshotRestore_DeadlineBoundsMissingRefBeforeCreate(t *testing.T) {
	ctx := context.Background()
	s := scrapeScheme(t)

	orig := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "orig", Namespace: "ns", UID: "uid-orig"},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "tmpl"},
			Env: []corev1.EnvVar{{
				Name: "V",
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "missing-secret"},
					Key:                  "key",
				}},
			}},
		},
	}
	backup := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns"},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: orig.Name},
			Strategy:  "volume-snapshot",
		},
		Status: gameplanev1alpha1.BackupStatus{
			SnapshotID:                "snap-1",
			VolumeSnapshotContentName: "snapcontent-b",
		},
	}
	restore := &gameplanev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: "rs", Namespace: "ns"},
		Spec: gameplanev1alpha1.RestoreSpec{
			BackupRef: gameplanev1alpha1.LocalObjectRef{Name: "b"},
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "restored"},
		},
		Status: gameplanev1alpha1.RestoreStatus{
			SnapshotID: "snap-1",
			StartTime:  &metav1.Time{Time: time.Now().Add(-(VolumeSnapshotRestoreDeadline + time.Minute))},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(orig, backup, restore).
		WithStatusSubresource(&gameplanev1alpha1.Restore{}, &gameplanev1alpha1.Backup{}).
		Build()
	r := &RestoreReconciler{Client: cl, Scheme: s}

	var rs gameplanev1alpha1.Restore
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "rs"}, &rs); err != nil {
		t.Fatalf("get restore: %v", err)
	}
	var b gameplanev1alpha1.Backup
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "b"}, &b); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	if _, err := r.reconcileVolumeSnapshotRestore(ctx, &rs, &b); err != nil {
		t.Fatalf("reconcileVolumeSnapshotRestore: want the Restore failed (nil error), got %v", err)
	}

	var after gameplanev1alpha1.Restore
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "rs"}, &after); err != nil {
		t.Fatalf("get restore after pass: %v", err)
	}
	if after.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
		t.Fatalf("phase = %q, want Failed once the deadline has passed before the restored server exists", after.Status.Phase)
	}
	notFound := apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "missing-secret")
	wantMsg := fmt.Sprintf("restore did not complete within 10m0s: check ownership of Secret %q: %v", "missing-secret", notFound)
	if after.Status.Message != wantMsg {
		t.Errorf("message = %q, want %q", after.Status.Message, wantMsg)
	}

	var gs gameplanev1alpha1.GameServer
	if err := cl.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "restored"}, &gs); !apierrors.IsNotFound(err) {
		t.Errorf("restored server must not be created when the restore fails before Create, got err=%v", err)
	}
}

// TestAwaitRestoredServer_DeadlineAppliesDespiteRecurringCopyError is a
// regression test: a restored server stuck on a recurring transient
// reference-copy error must still hit the documented deadline instead of
// polling forever, because the early return on a transient ensureRestoredRefs
// error used to skip the deadline check entirely.
func TestAwaitRestoredServer_DeadlineAppliesDespiteRecurringCopyError(t *testing.T) {
	ctx := context.Background()
	s := scrapeScheme(t)

	orig := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "orig", Namespace: "ns", UID: "uid-orig"},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "tmpl"},
			Env: []corev1.EnvVar{{
				Name: "V",
				ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "missing-secret"},
					Key:                  "key",
				}},
			}},
		},
	}
	restored := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "restored", Namespace: "ns", UID: "uid-restored"},
	}
	backup := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "ns"},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: orig.Name},
			Strategy:  "volume-snapshot",
		},
	}
	// missing-secret does not exist and the persisted copy plan names it, so
	// ensureOwnedRefCopies fails transiently (a NotFound reading the source,
	// not an ownership refusal) on every pass, exactly like a source object
	// that never shows up in the cache.
	restore := &gameplanev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{
			Name: "rs", Namespace: "ns",
			Annotations: map[string]string{
				"restore.gameplane.local/copy-plan": `[{"kind":"Secret","origName":"missing-secret","copyName":"restored-ref-x"}]`,
			},
		},
		Status: gameplanev1alpha1.RestoreStatus{
			StartTime: &metav1.Time{Time: time.Now().Add(-(VolumeSnapshotRestoreDeadline + time.Minute))},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(orig, restored, backup, restore).
		WithStatusSubresource(&gameplanev1alpha1.Restore{}).
		Build()
	r := &RestoreReconciler{Client: cl, Scheme: s}

	// The pass under test must hit a transient copy error, not an ownership
	// refusal, or the deadline path below is never exercised.
	plan := []refCopy{{Kind: secretRefKind, OrigName: "missing-secret", CopyName: "restored-ref-x"}}
	if cerr := r.ensureOwnedRefCopies(ctx, orig, restored, plan); cerr == nil || errors.Is(cerr, errRefNotOwned) {
		t.Fatalf("ensureOwnedRefCopies: want a transient (non-ownership) error for the missing source, got %v", cerr)
	}

	_, err := r.awaitRestoredServer(ctx, restore, backup, restored)
	if err != nil {
		t.Fatalf("awaitRestoredServer: unexpected error: %v", err)
	}

	var after gameplanev1alpha1.Restore
	if gerr := cl.Get(ctx, types.NamespacedName{Namespace: "ns", Name: "rs"}, &after); gerr != nil {
		t.Fatalf("get restore: %v", gerr)
	}
	if after.Status.Phase != gameplanev1alpha1.RestorePhaseFailed {
		t.Errorf("phase = %q, want Failed once the deadline is exceeded despite a recurring transient copy error", after.Status.Phase)
	}
}
