package controller

import (
	"encoding/json"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Exercise the public dispatch after target binding. Calling the CSI helper
// directly misses a regression that diverted later passes into Restic restore.
func TestVolumeSnapshotRestoreReconcileKeepsBoundStrategy(t *testing.T) {
	for _, state := range []string{"running", "repair-reference", "deadline"} {
		t.Run(state, func(t *testing.T) {
			scheme := scrapeScheme(t)
			if err := appsv1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			orig := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "ns", UID: "source-uid"}}
			target := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "ns", UID: "target-uid", Annotations: map[string]string{annoRestoreCreatedBy: "restore", restoreGuardAnnotation: restoreIdentityJSON("restore", "restore-uid")}}, Spec: gameplanev1alpha1.GameServerSpec{TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "template"}}}
			backup := &gameplanev1alpha1.Backup{ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: "ns", UID: "backup-uid"}, Spec: gameplanev1alpha1.BackupSpec{ServerRef: gameplanev1alpha1.LocalObjectRef{Name: orig.Name}, Strategy: "volume-snapshot"}, Status: gameplanev1alpha1.BackupStatus{Phase: gameplanev1alpha1.BackupPhaseSucceeded, SnapshotID: "snapshot", VolumeSnapshotContentName: "content"}}
			started := metav1.Now()
			rs := &gameplanev1alpha1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "restore", Namespace: "ns", UID: "restore-uid", Finalizers: []string{restoreCleanupFinalizer}, Annotations: map[string]string{restoreTargetAnnotation: restoreIdentityJSON(target.Name, target.UID)}}, Spec: gameplanev1alpha1.RestoreSpec{BackupRef: gameplanev1alpha1.LocalObjectRef{Name: backup.Name}, ServerRef: gameplanev1alpha1.LocalObjectRef{Name: target.Name}}, Status: gameplanev1alpha1.RestoreStatus{Phase: gameplanev1alpha1.RestorePhaseRunning, SnapshotID: "snapshot", StartTime: &started}}
			objects := []client.Object{orig, target, backup, rs}
			want := gameplanev1alpha1.RestorePhaseRunning
			switch state {
			case "running":
				target.Status.Phase = gameplanev1alpha1.GameServerPhaseRunning
				want = gameplanev1alpha1.RestorePhaseSucceeded
			case "repair-reference":
				plan, err := json.Marshal([]refCopy{{Kind: secretRefKind, OrigName: "source-secret", CopyName: "target-secret"}})
				if err != nil {
					t.Fatal(err)
				}
				rs.Annotations["restore.gameplane.local/copy-plan"] = string(plan)
				target.Spec.Env = []corev1.EnvVar{{Name: "SECRET", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "target-secret"}, Key: "key"}}}}
				objects = append(objects, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "source-secret", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(orig, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))}}, Data: map[string][]byte{"key": []byte("preserved-value")}})
			case "deadline":
				started = metav1.NewTime(time.Now().Add(-VolumeSnapshotRestoreDeadline - time.Minute))
				want = gameplanev1alpha1.RestorePhaseFailed
			}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(rs, target, backup).Build()
			r := &RestoreReconciler{Client: cl, APIReader: cl, Scheme: scheme}
			if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(rs)}); err != nil {
				t.Fatal(err)
			}
			var got gameplanev1alpha1.Restore
			if err := cl.Get(t.Context(), client.ObjectKeyFromObject(rs), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status.Phase != want {
				t.Fatalf("bound CSI restore phase = %s, want %s: %s", got.Status.Phase, want, got.Status.Message)
			}
			if state == "repair-reference" {
				var copied corev1.Secret
				if err := cl.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "target-secret"}, &copied); err != nil {
					t.Fatalf("bound CSI restore did not repair reference: %v", err)
				}
				if !metav1.IsControlledBy(&copied, target) || string(copied.Data["key"]) != "preserved-value" {
					t.Fatal("repaired reference lost ownership or data")
				}
			}
			var jobs batchv1.JobList
			if err := cl.List(t.Context(), &jobs); err != nil {
				t.Fatal(err)
			}
			if len(jobs.Items) != 0 {
				t.Fatal("CSI restore dispatched into Restic Job creation")
			}
		})
	}
}
