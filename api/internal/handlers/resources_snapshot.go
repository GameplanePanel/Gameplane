package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

var volumeSnapshotGVR = schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshots"}

var errSnapshotSourceUnavailable = errors.New("snapshot data source verification unavailable")

func writeServerValidationError(w http.ResponseWriter, req *http.Request, err error) {
	code := http.StatusForbidden
	if errors.Is(err, errTemplateRefImmutable) {
		code = http.StatusConflict
	}
	if errors.Is(err, errSnapshotSourceUnavailable) {
		code = http.StatusServiceUnavailable
	}
	httperr.WriteCode(w, req, code, err)
}

func validateSnapshotDataSource(ctx context.Context, k *kube.Client, cluster, namespace string, desired, live *unstructured.Unstructured) error {
	source, _, err := unstructured.NestedFieldNoCopy(desired.Object, "spec", "storage", "dataSource")
	if err != nil {
		return fmt.Errorf("invalid spec.storage.dataSource: %w", err)
	}
	var previous any
	if live != nil {
		previous, _, err = unstructured.NestedFieldNoCopy(live.Object, "spec", "storage", "dataSource")
		if err != nil {
			return fmt.Errorf("read existing spec.storage.dataSource: %w", err)
		}
	}
	if reflect.DeepEqual(source, previous) {
		return nil
	}
	user := auth.UserFromContext(ctx)
	if !user.Can("servers:write", true, cluster, namespace) || !user.Can("backups:restore", true, cluster, namespace) {
		return errors.New("changing spec.storage.dataSource requires servers:write and backups:restore permissions")
	}
	if source == nil {
		return nil
	}
	fields, ok := source.(map[string]any)
	if !ok {
		return errors.New("spec.storage.dataSource must reference a Backup-owned VolumeSnapshot")
	}
	kind, _ := fields["kind"].(string)
	name, _ := fields["name"].(string)
	// Match the CRD default when clients omit kind on a new source.
	if kind == "" && fields["kind"] == nil {
		kind = "VolumeSnapshot"
	}
	if kind != "VolumeSnapshot" || name == "" {
		return errors.New("spec.storage.dataSource must name a VolumeSnapshot")
	}
	snapshot, err := k.Dynamic.Resource(volumeSnapshotGVR).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return errors.New("data source VolumeSnapshot does not exist in the selected namespace")
		}
		return fmt.Errorf("%w: read VolumeSnapshot: %w", errSnapshotSourceUnavailable, err)
	}
	if snapshot.GetDeletionTimestamp() != nil {
		return errors.New("data source VolumeSnapshot is being deleted")
	}
	owner := metav1.GetControllerOf(snapshot)
	if owner == nil || owner.APIVersion != "gameplane.local/v1alpha1" || owner.Kind != "Backup" || owner.Name == "" || owner.UID == "" {
		return errors.New("data source VolumeSnapshot must be controlled by a Backup in the same namespace")
	}
	backup, err := k.Dynamic.Resource(kube.GVRs["backups"]).Namespace(namespace).Get(ctx, owner.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return errors.New("data source VolumeSnapshot has no live Backup owner in the selected namespace")
		}
		return fmt.Errorf("%w: read Backup: %w", errSnapshotSourceUnavailable, err)
	}
	if backup.GetUID() != owner.UID || backup.GetDeletionTimestamp() != nil {
		return errors.New("data source VolumeSnapshot does not belong to the current Backup UID")
	}
	// A failed or cancelled CSI capture may remain while the storage driver
	// reclaims it. Late readyToUse must not turn that capture into a restore
	// source after the original game has resumed saving.
	const aborted = "backup.gameplane.local/snapshot-aborted"
	if snapshot.GetAnnotations()[aborted] == "true" || backup.GetAnnotations()[aborted] == "true" {
		return errors.New("data source VolumeSnapshot capture was aborted")
	}
	phase, _, _ := unstructured.NestedString(backup.Object, "status", "phase")
	snapshotID, _, _ := unstructured.NestedString(backup.Object, "status", "snapshotID")
	ready, _, _ := unstructured.NestedBool(snapshot.Object, "status", "readyToUse")
	if phase != "Succeeded" || snapshotID != snapshot.GetName() || !ready {
		return errors.New("data source VolumeSnapshot must be a completed, ready Backup capture")
	}
	return nil
}
