package controller

import (
	"testing"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestBackupAdmittedForeignWorkerAbortsAndDrainsOnlyItsOwnWorkers(t *testing.T) {
	for _, strategy := range []string{"restic-snapshot", "volume-snapshot"} {
		t.Run(strategy, func(t *testing.T) {
			b := guardBackup("first")
			b.Spec.Strategy = strategy
			r, q := guardFixtureWorkers(t, false, b)
			guardPass(t, r, b.Name)
			var ownPod *corev1.Pod
			if strategy == "restic-snapshot" {
				ownPod = budgetReadyWorker(t, r, b.Name, "original-worker")
				guardPass(t, r, b.Name)
			} else {
				ownPod = &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "original-worker", Namespace: "ns", Labels: map[string]string{backupUIDLabel: string(b.UID)}},
					Status: corev1.PodStatus{Phase: corev1.PodRunning}}
				if err := r.Create(t.Context(), ownPod); err != nil {
					t.Fatal(err)
				}
			}
			b = guardGetBackup(t, r, b.Name)
			if q.quiesced != 1 || (strategy == "restic-snapshot" && b.Annotations[backupAuthorizedWorkerAnnotation] == "") {
				t.Fatalf("fixture never admitted its capture: %+v %+v", b, q)
			}
			original, err := r.captureWorker(t.Context(), b)
			if err != nil || original == nil {
				t.Fatalf("fixture worker missing: %v", err)
			}
			if err := r.Delete(t.Context(), original); err != nil {
				t.Fatal(err)
			}
			foreignBackup := b.DeepCopy()
			foreignBackup.UID = "foreign-backup"
			meta := metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace, UID: "foreign-worker",
				OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(foreignBackup, gameplanev1alpha1.GroupVersion.WithKind("Backup"))}}
			var foreign client.Object = &batchv1.Job{ObjectMeta: meta}
			if strategy == "volume-snapshot" {
				foreign = &snapshotv1.VolumeSnapshot{ObjectMeta: meta}
			}
			if err := r.Create(t.Context(), foreign); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)}); err != nil {
				t.Fatal(err)
			}
			b = guardGetBackup(t, r, b.Name)
			if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || q.unquiesced != 0 {
				t.Fatalf("replacement did not abort or released before own worker drain: %+v %+v", b.Status, q)
			}
			if strategy == "restic-snapshot" && b.Annotations[backupCopyAbortAnnotation] == "" || strategy == "volume-snapshot" && b.Annotations[backupSnapshotAbortAnnotation] != "true" {
				t.Fatal("replacement abort was not durable")
			}
			var gs gameplanev1alpha1.GameServer
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
				t.Fatal(err)
			}
			if gs.Annotations[lifecycleOperationAnnotation] == "" {
				t.Fatal("replacement released the owner before its original worker drained")
			}
			if err := r.Delete(t.Context(), ownPod); err != nil {
				t.Fatal(err)
			}
			guardPass(t, r, b.Name)
			if q.unquiesced != 1 {
				t.Fatalf("drained original capture did not restore saving: %+v", q)
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(&gs), &gs); err != nil {
				t.Fatal(err)
			}
			if gs.Annotations[lifecycleOperationAnnotation] != "" || gs.Annotations[backupGuardAnnotation] != "" {
				t.Fatal("drained original capture retained its lifecycle owner")
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(foreign), foreign); err != nil || foreign.GetUID() != "foreign-worker" || foreign.GetDeletionTimestamp() != nil {
				t.Fatalf("replacement cleanup modified the foreign resource: %v %+v", err, foreign)
			}
		})
	}
}
