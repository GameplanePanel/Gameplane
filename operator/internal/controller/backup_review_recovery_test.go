package controller

import (
	"testing"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestBackupMissingTemplateAfterQuiesceFailsAndReleases(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"))
	guardPass(t, r, "first")
	// Model a restart after durable preparation but before the copy exists.
	if err := r.Delete(t.Context(), &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "first", Namespace: "ns"}}); err != nil {
		t.Fatal(err)
	}
	setMissingBackupTemplate(t, r)
	guardPass(t, r, "first")
	b := guardGetBackup(t, r, "first")
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || q.unquiesced != 1 {
		t.Fatalf("missing template stranded capture: phase=%s, unquiesced=%d", b.Status.Phase, q.unquiesced)
	}
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(b), &job); !apierrors.IsNotFound(err) {
		t.Fatalf("started a copy without its template: %v", err)
	}
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[lifecycleOperationAnnotation] != "" || gs.Annotations[backupGuardAnnotation] != "" {
		t.Fatal("failed capture retained lifecycle ownership")
	}
}

func TestBackupMissingTemplateStillObservesJobAndWaitsForDrain(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"))
	guardPass(t, r, "first")
	setMissingBackupTemplate(t, r)
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "first"}, &job); err != nil {
		t.Fatal(err)
	}
	job.Status.Active = 1
	if err := r.Status().Update(t.Context(), &job); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	if b := guardGetBackup(t, r, "first"); b.Status.Phase != gameplanev1alpha1.BackupPhaseRunning {
		t.Fatalf("existing job not observed: %s", b.Status.Phase)
	}
	worker := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "copy-worker", Namespace: "ns", Labels: job.Spec.Template.Labels}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	job.Status.Active, job.Status.Succeeded = 0, 1
	if err := r.Status().Update(t.Context(), &job); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	if b := guardGetBackup(t, r, "first"); b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded || q.unquiesced != 0 {
		t.Fatalf("copy lost completion or released before drain: phase=%s, unquiesced=%d", b.Status.Phase, q.unquiesced)
	}
	if err := r.Delete(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	if q.unquiesced != 1 {
		t.Fatalf("drained copy did not restore saving: %d", q.unquiesced)
	}
}

func setMissingBackupTemplate(t *testing.T, r *BackupReconciler) {
	t.Helper()
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
		t.Fatal(err)
	}
	gs.Annotations[effectiveTemplateAnnotation] = "missing-snapshot"
	if err := r.Update(t.Context(), &gs); err != nil {
		t.Fatal(err)
	}
}

func TestBackupForeignWorkerWaitsWithoutQuiescingOrInventingHistory(t *testing.T) {
	for _, strategy := range []string{"restic-snapshot", "volume-snapshot"} {
		t.Run(strategy, func(t *testing.T) {
			b := guardBackup("first")
			b.Spec.Strategy = strategy
			r, q := guardFixture(t, b)
			old := b.DeepCopy()
			old.UID = "previous-backup-uid"
			meta := metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(old, gameplanev1alpha1.GroupVersion.WithKind("Backup"))}}
			var worker client.Object = &batchv1.Job{ObjectMeta: meta}
			if strategy == "volume-snapshot" {
				worker = &snapshotv1.VolumeSnapshot{ObjectMeta: meta}
			}
			if err := r.Create(t.Context(), worker); err != nil {
				t.Fatal(err)
			}
			res, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)})
			if err != nil || res.RequeueAfter <= 0 {
				t.Fatalf("foreign worker must requeue: result=%+v, err=%v", res, err)
			}
			b = guardGetBackup(t, r, "first")
			if b.Status.CaptureManifest != nil || b.Status.Phase == gameplanev1alpha1.BackupPhaseFailed || b.Annotations[backupTargetAnnotation] != "" || q.quiesced != 0 {
				t.Fatalf("foreign worker admitted or failed replacement capture: status=%+v, annotations=%v, quiesced=%d", b.Status, b.Annotations, q.quiesced)
			}
			if err := r.Delete(t.Context(), worker); err != nil {
				t.Fatal(err)
			}
			guardPass(t, r, "first")
			b = guardGetBackup(t, r, "first")
			if b.Status.CaptureManifest == nil || b.Status.CaptureManifest.State != "Recorded" || q.quiesced != 1 {
				t.Fatalf("new capture did not resume with its own metadata: status=%+v, quiesced=%d", b.Status, q.quiesced)
			}
		})
	}
}

func TestBackupForeignReplacementFailsAndDrainsOwnedWorkers(t *testing.T) {
	r, q := guardFixture(t, guardBackup("first"))
	guardPass(t, r, "first")
	b := guardGetBackup(t, r, "first")
	worker := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "original-copy", Namespace: b.Namespace, Labels: map[string]string{backupUIDLabel: string(b.UID)}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	if err := r.Create(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(t.Context(), &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace}}); err != nil {
		t.Fatal(err)
	}
	foreign := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace, UID: "foreign-job", OwnerReferences: []metav1.OwnerReference{{APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "Backup", Name: b.Name, UID: "foreign-backup", Controller: ownerBoolPtr(true)}}}}
	if err := r.Create(t.Context(), foreign); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	b = guardGetBackup(t, r, "first")
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || q.unquiesced != 0 {
		t.Fatalf("replacement did not fail while retaining live-worker protection: status=%+v, agent=%+v", b.Status, q)
	}
	if err := r.Delete(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	guardPass(t, r, "first")
	if q.unquiesced != 1 {
		t.Fatalf("replacement stranded save-off after own workers drained: %+v", q)
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(foreign), foreign); err != nil || foreign.UID != "foreign-job" {
		t.Fatalf("cleanup changed a foreign worker: %v", err)
	}
}
