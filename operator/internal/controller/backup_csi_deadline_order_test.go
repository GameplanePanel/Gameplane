package controller

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestBackupExpiredCSIDeadlinePrecedesUnavailableWorkloadFence(t *testing.T) {
	b := guardBackup("first")
	b.Spec.Strategy = "volume-snapshot"
	r, q := guardFixture(t, b)
	guardPass(t, r, b.Name)
	b = guardGetBackup(t, r, b.Name)
	if q.quiesced != 1 || b.Annotations[backupCSIDeadlineAnnotation] == "" {
		t.Fatalf("fixture never began a bounded CSI capture: %+v %+v", b, q)
	}
	b.Annotations[backupCSIDeadlineAnnotation] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	if err := r.Update(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(t.Context(), &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "ns"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)}); err != nil {
		t.Fatal(err)
	}
	b = guardGetBackup(t, r, b.Name)
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || b.Annotations[backupSnapshotAbortAnnotation] != "true" || b.Annotations[backupSourceStartupDeadlineAnnotation] != "" || q.unquiesced != 1 {
		t.Fatalf("expired CSI capture waited for a new source budget: %+v %+v", b, q)
	}
	var gs gameplanev1alpha1.GameServer
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[lifecycleOperationAnnotation] != "" || gs.Annotations[backupGuardAnnotation] != "" {
		t.Fatal("expired CSI capture retained its owner after save-on")
	}
}
