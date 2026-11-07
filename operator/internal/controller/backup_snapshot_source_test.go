package controller

import (
	"encoding/json"
	"testing"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestSnapshotPVCRefusesIncompleteAbortedAndReplacedCapture(t *testing.T) {
	for _, mode := range []string{"valid", "pending", "running", "failed", "abort-before-status", "copy-aborted", "snapshot-aborted", "owner-replaced", "snapshot-replaced", "not-ready", "external"} {
		t.Run(mode, func(t *testing.T) {
			b := guardBackup("snapshot")
			b.Spec.Strategy = "volume-snapshot"
			b.Status.Phase, b.Status.SnapshotID = gameplanev1alpha1.BackupPhaseSucceeded, b.Name
			identity, err := json.Marshal(backupSnapshotIdentity{UID: "snapshot-uid"})
			if err != nil {
				t.Fatal(err)
			}
			b.Annotations = map[string]string{backupSnapshotIdentityAnnotation: string(identity)}
			vs := &snapshotv1.VolumeSnapshot{ObjectMeta: metav1.ObjectMeta{Name: b.Name, Namespace: b.Namespace, UID: "snapshot-uid", OwnerReferences: []metav1.OwnerReference{{APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "Backup", Name: b.Name, UID: b.UID, Controller: ownerBoolPtr(true)}}}, Status: &snapshotv1.VolumeSnapshotStatus{ReadyToUse: ownerBoolPtr(true)}}
			switch mode {
			case "pending":
				b.Status.Phase = gameplanev1alpha1.BackupPhasePending
			case "running":
				b.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
			case "failed":
				b.Status.Phase = gameplanev1alpha1.BackupPhaseFailed
			case "abort-before-status":
				b.Annotations[backupSnapshotAbortAnnotation] = "true"
			case "copy-aborted":
				b.Annotations[backupCopyAbortAnnotation] = "cancelled"
			case "snapshot-aborted":
				vs.Annotations = map[string]string{backupSnapshotAbortAnnotation: "true"}
			case "owner-replaced":
				vs.OwnerReferences[0].UID = "previous-backup"
			case "snapshot-replaced":
				vs.UID = "replacement-snapshot"
			case "not-ready":
				vs.Status = nil
			case "external":
				vs.OwnerReferences = nil
				vs.Status = nil
			}
			r, _ := guardFixture(t, b)
			if err := r.Create(t.Context(), vs); err != nil {
				t.Fatal(err)
			}
			gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "restored", Namespace: b.Namespace, UID: "restored-uid"}, Spec: gameplanev1alpha1.GameServerSpec{Storage: &gameplanev1alpha1.GameStorageSpec{DataSource: &gameplanev1alpha1.GameDataSource{Kind: "VolumeSnapshot", Name: b.Name}}}}
			gr := &GameServerReconciler{Client: r.Client, APIReader: r.Client, Scheme: r.Scheme}
			err = gr.reconcilePVC(t.Context(), gs, &gameplanev1alpha1.GameTemplate{})
			allowed := mode == "valid" || mode == "external"
			if (err == nil) != allowed {
				t.Fatalf("PVC source validation mode=%s err=%v", mode, err)
			}
			var pvc corev1.PersistentVolumeClaim
			getErr := r.Get(t.Context(), client.ObjectKey{Namespace: b.Namespace, Name: "restored-data"}, &pvc)
			if !allowed && !apierrors.IsNotFound(getErr) {
				t.Fatalf("invalid source created PVC: %v", getErr)
			}
		})
	}
}
