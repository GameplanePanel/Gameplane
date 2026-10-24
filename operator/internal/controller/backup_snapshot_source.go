package controller

import (
	"context"
	"encoding/json"
	"fmt"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// A raw GameServer dataSource must not revive an aborted operator capture while
// a CSI driver still exposes its object. Run only before creating a new PVC:
// existing volumes remain usable after retention deletes their source Backup.
// External snapshots without a Gameplane Backup owner keep the direct-CRD
// administrator contract; API authorization is enforced separately.
func validateBackupSnapshotSource(ctx context.Context, reader client.Reader, namespace, name string) error {
	var snapshot snapshotv1.VolumeSnapshot
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &snapshot); err != nil {
		return fmt.Errorf("resolve snapshot source: %w", err)
	}
	if snapshot.Annotations[backupSnapshotAbortAnnotation] == "true" {
		return fmt.Errorf("snapshot %s was aborted and cannot seed a volume", name)
	}
	owner := metav1.GetControllerOf(&snapshot)
	if owner == nil || owner.APIVersion != gameplanev1alpha1.GroupVersion.String() || owner.Kind != "Backup" {
		return nil
	}
	var backup gameplanev1alpha1.Backup
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: owner.Name}, &backup); err != nil {
		return fmt.Errorf("resolve snapshot Backup owner: %w", err)
	}
	if owner.UID == "" || owner.UID != backup.UID || !backup.DeletionTimestamp.IsZero() || !snapshot.DeletionTimestamp.IsZero() ||
		backup.Annotations[backupSnapshotAbortAnnotation] == "true" || backup.Annotations[backupCopyAbortAnnotation] != "" ||
		backup.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded || backup.Status.SnapshotID != snapshot.Name ||
		snapshot.Status == nil || snapshot.Status.ReadyToUse == nil || !*snapshot.Status.ReadyToUse {
		return fmt.Errorf("snapshot %s is not a completed, valid capture of its current Backup owner", name)
	}
	if value := backup.Annotations[backupSnapshotIdentityAnnotation]; value != "" {
		var id backupSnapshotIdentity
		if err := json.Unmarshal([]byte(value), &id); err != nil || id.UID == "" || id.UID != snapshot.UID {
			return fmt.Errorf("snapshot %s differs from the recorded capture UID", name)
		}
	}
	return nil
}
