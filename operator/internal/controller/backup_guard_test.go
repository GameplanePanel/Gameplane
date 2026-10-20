package controller

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/agent"
)

type guardQuiescer struct {
	quiesced, unquiesced int
	quiesceErr           error
}

func (q *guardQuiescer) Quiesce(context.Context, string, string) error {
	q.quiesced++
	return q.quiesceErr
}
func (q *guardQuiescer) Unquiesce(context.Context, string, string) error { q.unquiesced++; return nil }

func guardBackup(name string) *gameplanev1alpha1.Backup {
	return &gameplanev1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: types.UID(name + "-uid"), Finalizers: []string{gameplanev1alpha1.BackupFinalizer, gameplanev1alpha1.BackupSnapshotFinalizer}}, Spec: gameplanev1alpha1.BackupSpec{ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "server"}, RepoRef: &gameplanev1alpha1.SecretKeySelector{Name: "repo"}, Quiesce: true}}
}

func guardFixture(t *testing.T, backups ...*gameplanev1alpha1.Backup) (*BackupReconciler, *guardQuiescer) {
	t.Helper()
	s := scrapeScheme(t)
	if err := snapshotv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	objects := []client.Object{&gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "ns", UID: "server-uid"}, Spec: gameplanev1alpha1.GameServerSpec{TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "template"}}}, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"}, Data: map[string][]byte{"repo": []byte("repo"), "password": []byte("password")}}}
	for _, b := range backups {
		objects = append(objects, b)
	}
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).WithStatusSubresource(&gameplanev1alpha1.Backup{}, &gameplanev1alpha1.GameServer{}, &batchv1.Job{}, &snapshotv1.VolumeSnapshot{}).Build()
	q := &guardQuiescer{}
	return &BackupReconciler{Client: cl, Scheme: s, AgentClient: q, LogReader: scrapeLogReader{body: `{"message_type":"summary","snapshot_id":"abc123"}`}}, q
}

func guardPass(t *testing.T, r *BackupReconciler, name string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
}

func guardGetBackup(t *testing.T, r *BackupReconciler, name string) *gameplanev1alpha1.Backup {
	t.Helper()
	b := &gameplanev1alpha1.Backup{}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: name}, b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBackupGuardSerializesAcrossBackupsAndRestart(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"), guardBackup("second"))
	guardPass(t, r, "first")
	// A new reconciler instance must see the persisted guard.
	r = &BackupReconciler{Client: r.Client, Scheme: r.Scheme, AgentClient: q, LogReader: r.LogReader}
	guardPass(t, r, "second")
	var jobs batchv1.JobList
	if err := r.List(context.Background(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 || q.quiesced != 1 || q.unquiesced != 0 {
		t.Fatalf("overlapping protected copies: jobs=%d, agent=%+v", len(jobs.Items), q)
	}
	job := &jobs.Items[0]
	job.Status.Succeeded = 1
	if err := r.Status().Update(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	guardPass(t, r, "second")
	if q.quiesced != 2 || q.unquiesced != 1 {
		t.Fatalf("second did not acquire after first cleanup: %+v", q)
	}
}

func TestBackupGuardDeleteWaitsForTerminatingWorker(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"), guardBackup("second"))
	guardPass(t, r, "first")
	var job batchv1.Job
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "first"}, &job); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "ns", Labels: job.Spec.Template.Labels, Finalizers: []string{"test/hold"}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	b := guardGetBackup(t, r, "first")
	if err := r.Delete(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	guardPass(t, r, "second")
	if q.unquiesced != 0 || q.quiesced != 1 {
		t.Fatalf("released while worker terminating: %+v", q)
	}
	if err := r.Get(context.Background(), client.ObjectKeyFromObject(pod), pod); err != nil {
		t.Fatal(err)
	}
	pod.Finalizers = nil
	if err := r.Update(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	guardPass(t, r, "second")
	if q.unquiesced != 1 || q.quiesced != 2 {
		t.Fatalf("cleanup did not progress after drain: %+v", q)
	}
}

func TestBackupSnapshotMissingAPIFailsBeforeQuiesce(t *testing.T) {
	b := guardBackup("snapshot")
	b.Spec.Strategy = "volume-snapshot"
	r, q := guardFixture(t, b)
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*snapshotv1.VolumeSnapshot); ok {
			return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "snapshot.storage.k8s.io", Kind: "VolumeSnapshot"}}
		}
		return cl.Get(ctx, key, obj, opts...)
	}})
	guardPass(t, r, "snapshot")
	if got := guardGetBackup(t, r, "snapshot"); got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || got.Status.Message == "" {
		t.Fatalf("missing API not surfaced: %+v", got.Status)
	}
	if q.quiesced != 0 {
		t.Fatalf("quiesced without snapshot API: %+v", q)
	}
}

func TestBackupSnapshotCreateErrorRestoresSaving(t *testing.T) {
	for _, createErr := range []error{apierrors.NewForbidden(schema.GroupResource{Resource: "volumesnapshots"}, "snapshot", errors.New("denied")), errors.New("timeout creating snapshot")} {
		t.Run(createErr.Error(), func(t *testing.T) {
			b := guardBackup("snapshot")
			b.Spec.Strategy = "volume-snapshot"
			r, q := guardFixture(t, b)
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*snapshotv1.VolumeSnapshot); ok {
					return createErr
				}
				return cl.Create(ctx, obj, opts...)
			}})
			guardPass(t, r, "snapshot")
			if got := guardGetBackup(t, r, "snapshot"); got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
				t.Fatalf("creation error not terminal: %+v", got.Status)
			}
			if q.quiesced != 1 || q.unquiesced != 1 {
				t.Fatalf("saving not restored: %+v", q)
			}
		})
	}
}

func TestBackupQuiesceFailureStillAttemptsCleanup(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"))
	q.quiesceErr = errors.New("flush failed after save-off")
	_, _ = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "first"}})
	q.quiesceErr = nil
	guardPass(t, r, "first")
	if q.unquiesced != 1 || guardGetBackup(t, r, "first").Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Fatalf("partial quiesce left saving disabled: %+v", q)
	}
}

func TestBackupGuardRolloutWaitsForLegacyWorkersAndCleanup(t *testing.T) {
	legacy := guardBackup("legacy")
	legacy.Annotations = map[string]string{annoQuiesceAttempted: "true"}
	r, q := guardFixture(t, legacy, guardBackup("new"))
	guardPass(t, r, "legacy")
	guardPass(t, r, "new")
	var jobs batchv1.JobList
	if err := r.List(context.Background(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 || jobs.Items[0].Name != "legacy" || q.quiesced != 0 {
		t.Fatalf("started new copy before legacy drain: jobs=%v agent=%+v", jobs.Items, q)
	}
	jobs.Items[0].Status.Succeeded = 1
	if err := r.Status().Update(context.Background(), &jobs.Items[0]); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "legacy")
	guardPass(t, r, "new")
	if q.unquiesced != 1 || q.quiesced != 1 {
		t.Fatalf("legacy handoff did not progress: %+v", q)
	}
}

func TestBackupGuardOrphanRecoveryWaitsForWorkers(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"), guardBackup("second"))
	guardPass(t, r, "first")
	b := guardGetBackup(t, r, "first")
	b.Finalizers = nil
	if err := r.Update(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	var job batchv1.Job
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "first"}, &job); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan", Namespace: "ns", Labels: job.Spec.Template.Labels}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "second")
	if q.quiesced != 1 || q.unquiesced != 0 {
		t.Fatalf("reclaimed before orphan drain: %+v", q)
	}
	if err := r.Delete(context.Background(), pod); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "second")
	if q.quiesced != 2 || q.unquiesced != 1 {
		t.Fatalf("orphan owner not recovered: %+v", q)
	}
}

func TestBackupGuardLosingOrphanCASDoesNotCallSaveOn(t *testing.T) {
	r, q := guardFixture(t, guardBackup("second"))
	var gs gameplanev1alpha1.GameServer
	key := types.NamespacedName{Namespace: "ns", Name: "server"}
	if err := r.Get(context.Background(), key, &gs); err != nil {
		t.Fatal(err)
	}
	gs.Annotations = map[string]string{backupGuardAnnotation: backupIdentityJSON("deleted", "deleted-uid")}
	if err := r.Update(context.Background(), &gs); err != nil {
		t.Fatal(err)
	}
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
		if _, ok := obj.(*gameplanev1alpha1.GameServer); ok {
			return apierrors.NewConflict(schema.GroupResource{Resource: "gameservers"}, obj.GetName(), errors.New("another contender acquired"))
		}
		return cl.Update(ctx, obj, opts...)
	}})
	guardPass(t, r, "second")
	if q.quiesced != 0 || q.unquiesced != 0 {
		t.Fatalf("loser called agent before owning recovery: %+v", q)
	}
}

func TestBackupGuardCleanupSkipsReplacedTarget(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"))
	guardPass(t, r, "first")
	key := types.NamespacedName{Namespace: "ns", Name: "server"}
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(context.Background(), key, &gs); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(context.Background(), &gs); err != nil {
		t.Fatal(err)
	}
	gs.ResourceVersion = ""
	gs.UID = "replacement"
	gs.Annotations = map[string]string{backupGuardAnnotation: backupIdentityJSON("another", "another-uid")}
	if err := r.Create(context.Background(), &gs); err != nil {
		t.Fatal(err)
	}
	b := guardGetBackup(t, r, "first")
	b.Status.Phase = gameplanev1alpha1.BackupPhaseFailed
	if err := r.Status().Update(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	var job batchv1.Job
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "first"}, &job); err != nil {
		t.Fatal(err)
	}
	job.Status.Succeeded = 1
	if err := r.Status().Update(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	if q.unquiesced != 0 {
		t.Fatal("called save-on against replacement")
	}
	if err := r.Get(context.Background(), key, &gs); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[backupGuardAnnotation] != backupIdentityJSON("another", "another-uid") {
		t.Fatal("cleared replacement owner")
	}
}

func TestBackupGuardReleasesUnsupportedAndRawBackups(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("disabled-%t", disabled), func(t *testing.T) {
			r, q := guardFixture(t, guardBackup("first"), guardBackup("second"))
			q.quiesceErr = agent.ErrUnsupported
			if disabled {
				r.AgentClient = nil
			}
			guardPass(t, r, "first")
			var job batchv1.Job
			if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "first"}, &job); err != nil {
				t.Fatal(err)
			}
			job.Status.Succeeded = 1
			if err := r.Status().Update(context.Background(), &job); err != nil {
				t.Fatal(err)
			}
			guardPass(t, r, "first")
			guardPass(t, r, "second")
			if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "second"}, &job); err != nil {
				t.Fatalf("guard not released: %v", err)
			}
		})
	}
}

func TestBackupGuardRetainsCleanupWhenAgentClientRemoved(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"), guardBackup("second"))
	guardPass(t, r, "first")
	var job batchv1.Job
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "first"}, &job); err != nil {
		t.Fatal(err)
	}
	job.Status.Succeeded = 1
	if err := r.Status().Update(context.Background(), &job); err != nil {
		t.Fatal(err)
	}
	r.AgentClient = nil
	guardPass(t, r, "first")
	guardPass(t, r, "second")
	var jobs batchv1.JobList
	if err := r.List(context.Background(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 1 {
		t.Fatal("released quiesced world without an agent client")
	}
	r.AgentClient = q
	guardPass(t, r, "first")
	guardPass(t, r, "second")
	if q.unquiesced != 1 || q.quiesced != 2 {
		t.Fatalf("cleanup did not recover: %+v", q)
	}
}

func TestBackupQuiesceIntentSurvivesPostAgentWriteFailure(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"))
	base := r.Client
	r.Client = interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
		if b, ok := obj.(*gameplanev1alpha1.Backup); ok && b.Annotations[annoQuiesceAttempted] == "true" {
			return errors.New("write failed after save-off")
		}
		return cl.Update(ctx, obj, opts...)
	}})
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: "first"}})
	if err == nil {
		t.Fatal("expected post-agent write failure")
	}
	r.Client = base
	b := guardGetBackup(t, r, "first")
	if b.Annotations[annoQuiesceAttempted] != "pending" {
		t.Fatal("cleanup intent was not durable before save-off")
	}
	if err := r.Delete(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	if q.unquiesced != 1 {
		t.Fatalf("lost cleanup after write failure: %+v", q)
	}
}

func TestBackupGuardSerializesSnapshotAndRestic(t *testing.T) {
	b := guardBackup("snapshot")
	b.Spec.Strategy = "volume-snapshot"
	r, q := guardFixture(t, b, guardBackup("restic"))
	guardPass(t, r, "snapshot")
	guardPass(t, r, "restic")
	var jobs batchv1.JobList
	if err := r.List(context.Background(), &jobs); err != nil {
		t.Fatal(err)
	}
	if len(jobs.Items) != 0 || q.quiesced != 1 {
		t.Fatalf("restic copied during snapshot: jobs=%d agent=%+v", len(jobs.Items), q)
	}
	var vs snapshotv1.VolumeSnapshot
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "snapshot"}, &vs); err != nil {
		t.Fatal(err)
	}
	ready := true
	vs.Status = &snapshotv1.VolumeSnapshotStatus{ReadyToUse: &ready}
	if err := r.Status().Update(context.Background(), &vs); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "snapshot")
	guardPass(t, r, "restic")
	if q.unquiesced != 1 || q.quiesced != 2 {
		t.Fatalf("cross-strategy handoff failed: %+v", q)
	}
}

func TestBackupSnapshotAPIDisappearsAfterQuiesce(t *testing.T) {
	b := guardBackup("snapshot")
	b.Spec.Strategy = "volume-snapshot"
	r, q := guardFixture(t, b)
	guardPass(t, r, "snapshot")
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*snapshotv1.VolumeSnapshot); ok {
			return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "snapshot.storage.k8s.io", Kind: "VolumeSnapshot"}}
		}
		return cl.Get(ctx, key, obj, opts...)
	}})
	guardPass(t, r, "snapshot")
	if q.quiesced != 1 || q.unquiesced != 1 || guardGetBackup(t, r, "snapshot").Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Fatalf("API removal lost cleanup: %+v", q)
	}
}

func forceDeleteGuardBackup(t *testing.T, r *BackupReconciler, name string) {
	t.Helper()
	b := guardGetBackup(t, r, name)
	b.Finalizers = nil
	if err := r.Update(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(context.Background(), b); err != nil {
		t.Fatal(err)
	}
}

func TestBackupGuardRecoveryLineageSurvivesRepeatedOwnerDeletion(t *testing.T) {
	for _, normalDelete := range []bool{false, true} {
		t.Run(fmt.Sprintf("normal-delete-%t", normalDelete), func(t *testing.T) {
			r, q := guardFixture(t, guardBackup("a"), guardBackup("b"), guardBackup("c"))
			guardPass(t, r, "a")
			var job batchv1.Job
			if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "a"}, &job); err != nil {
				t.Fatal(err)
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "worker-a", Namespace: "ns", Labels: job.Spec.Template.Labels}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
			if err := r.Create(context.Background(), pod); err != nil {
				t.Fatal(err)
			}
			forceDeleteGuardBackup(t, r, "a")
			guardPass(t, r, "b")
			if normalDelete {
				b := guardGetBackup(t, r, "b")
				if err := r.Delete(context.Background(), b); err != nil {
					t.Fatal(err)
				}
				guardPass(t, r, "b")
			} else {
				forceDeleteGuardBackup(t, r, "b")
			}
			guardPass(t, r, "c")
			if q.quiesced != 1 || q.unquiesced != 0 {
				t.Fatalf("lost A's live worker through B: %+v", q)
			}
			if err := r.Delete(context.Background(), pod); err != nil {
				t.Fatal(err)
			}
			if normalDelete {
				guardPass(t, r, "b")
			}
			guardPass(t, r, "c")
			if q.quiesced != 2 || q.unquiesced != 1 {
				t.Fatalf("chain did not hand off after A drained: %+v", q)
			}
		})
	}
}

func TestBackupGuardPendingSnapshotCleanupWaitsForCapture(t *testing.T) {
	for _, mode := range []string{"delete", "orphan", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			a := guardBackup("snapshot")
			a.Spec.Strategy = "volume-snapshot"
			if mode == "legacy" {
				a.Annotations = map[string]string{annoQuiesceAttempted: "true"}
			}
			r, q := guardFixture(t, a, guardBackup("next"))
			guardPass(t, r, "snapshot")
			switch mode {
			case "orphan":
				forceDeleteGuardBackup(t, r, "snapshot")
			case "delete":
				a := guardGetBackup(t, r, "snapshot")
				if err := r.Delete(context.Background(), a); err != nil {
					t.Fatal(err)
				}
				guardPass(t, r, "snapshot")
			}
			guardPass(t, r, "next")
			if q.unquiesced != 0 {
				t.Fatalf("save-on during pending CSI capture: %+v", q)
			}
			var jobs batchv1.JobList
			if err := r.List(context.Background(), &jobs); err != nil {
				t.Fatal(err)
			}
			if len(jobs.Items) != 0 {
				t.Fatal("started next copy during pending CSI capture")
			}
			var vs snapshotv1.VolumeSnapshot
			if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "snapshot"}, &vs); err != nil {
				t.Fatal(err)
			}
			ready := true
			vs.Status = &snapshotv1.VolumeSnapshotStatus{ReadyToUse: &ready}
			if err := r.Status().Update(context.Background(), &vs); err != nil {
				t.Fatal(err)
			}
			if mode != "orphan" {
				guardPass(t, r, "snapshot")
			}
			guardPass(t, r, "next")
			if q.unquiesced != 1 {
				t.Fatalf("CSI cleanup did not progress after capture: %+v", q)
			}
			if err := r.List(context.Background(), &jobs); err != nil {
				t.Fatal(err)
			}
			if len(jobs.Items) != 1 {
				t.Fatal("next copy did not start after CSI capture")
			}
		})
	}
}

func TestBackupSnapshotAmbiguousCreateStillCleansUp(t *testing.T) {
	b := guardBackup("snapshot")
	b.Spec.Strategy = "volume-snapshot"
	r, q := guardFixture(t, b)
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
		if err := cl.Create(ctx, obj, opts...); err != nil {
			return err
		}
		if _, ok := obj.(*snapshotv1.VolumeSnapshot); ok {
			return errors.New("response lost after successful snapshot creation")
		}
		return nil
	}})
	guardPass(t, r, "snapshot")
	if q.quiesced != 1 || q.unquiesced != 1 || guardGetBackup(t, r, "snapshot").Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Fatalf("ambiguous create lost abort cleanup: %+v", q)
	}
	var vs snapshotv1.VolumeSnapshot
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "snapshot"}, &vs); err != nil {
		t.Fatal(err)
	}
	if vs.Status != nil && vs.Status.ReadyToUse != nil && *vs.Status.ReadyToUse {
		t.Fatal("expected still-pending ambiguous capture")
	}
}

func TestBackupSnapshotForbiddenAPIAfterQuiesceStillCleansUp(t *testing.T) {
	b := guardBackup("snapshot")
	b.Spec.Strategy = "volume-snapshot"
	r, q := guardFixture(t, b)
	guardPass(t, r, "snapshot")
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*snapshotv1.VolumeSnapshot); ok {
			return apierrors.NewForbidden(schema.GroupResource{Resource: "volumesnapshots"}, key.Name, errors.New("permission revoked"))
		}
		return cl.Get(ctx, key, obj, opts...)
	}})
	guardPass(t, r, "snapshot")
	if q.unquiesced != 1 || guardGetBackup(t, r, "snapshot").Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Fatalf("revoked API permission trapped cleanup: %+v", q)
	}
}

func TestBackupResticCleanupWithoutSnapshotScheme(t *testing.T) {
	b := quiescedBackup()
	b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
	b.Status.SnapshotID = "abc123"
	q := &scrapeQuiescer{}
	r := newScrapeReconciler(t, b, nil, time.Hour, q)
	r.Scheme = nil
	res, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)})
	if err != nil || res.RequeueAfter > 0 || q.unquiesced != 1 {
		t.Fatalf("unrelated CSI registration blocked restic cleanup: result=%+v err=%v calls=%d", res, err, q.unquiesced)
	}
}

func TestBackupSnapshotAbortSurvivesInterruptedStatusWrite(t *testing.T) {
	b := guardBackup("snapshot")
	b.Spec.Strategy = "volume-snapshot"
	b.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
	b.Annotations = map[string]string{backupSnapshotAbortAnnotation: "true", annoQuiesceAttempted: "true"}
	r, q := guardFixture(t, b)
	guardPass(t, r, "snapshot")
	if q.unquiesced != 1 || guardGetBackup(t, r, "snapshot").Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Fatalf("resumed an aborted capture after interrupted failure status: %+v", q)
	}
	var vs snapshotv1.VolumeSnapshot
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "snapshot"}, &vs); !apierrors.IsNotFound(err) {
		t.Fatalf("created a snapshot after durable abort: %v", err)
	}
}
