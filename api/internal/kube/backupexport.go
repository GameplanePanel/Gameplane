package kube

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	unstructuredpkg "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// As in capture.go, these types mirror the operator's CRDs field-for-field
// (same JSON tags) so the dynamic client can convert in both directions
// without importing the operator module. Mirrors of
// operator/api/v1alpha1/backupexport_types.go and backup_types.go.

// GVRBackupExport addresses the BackupExport CRD (spec 041).
var GVRBackupExport = schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "backupexports"}

// GVRBackup addresses the Backup CRD.
var GVRBackup = schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "backups"}

// BackupExportClusterAnnotation records the cluster name a BackupExport was
// requested against. The operator copies it into the archive manifest's
// cluster field. Must match the constant in operator/internal/controller.
const BackupExportClusterAnnotation = "gameplane.io/cluster-name"

// BackupExportPhase mirrors operator/api/v1alpha1.BackupExportPhase.
type BackupExportPhase string

// BackupExportPhase constants mirror operator/api/v1alpha1's BackupExportPhase* consts.
const (
	BackupExportPhasePending   BackupExportPhase = "Pending"
	BackupExportPhaseReady     BackupExportPhase = "Ready"
	BackupExportPhaseCompleted BackupExportPhase = "Completed"
	BackupExportPhaseFailed    BackupExportPhase = "Failed"
	BackupExportPhaseExpired   BackupExportPhase = "Expired"
)

// BackupExportSpec mirrors operator/api/v1alpha1.BackupExportSpec.
type BackupExportSpec struct {
	// BackupRef is the source Backup in the same namespace.
	BackupRef corev1.LocalObjectReference `json:"backupRef"`
	// BackupUID pins the Backup identity.
	BackupUID string `json:"backupUID"`
	// Format of the archive (tar.gz or zip).
	Format string `json:"format"`
	// TTLSeconds bounds the export's life.
	TTLSeconds *int32 `json:"ttlSeconds,omitempty"`
}

// BackupExportStatus mirrors operator/api/v1alpha1.BackupExportStatus (only
// the fields the API reads).
type BackupExportStatus struct {
	// Phase is the lifecycle phase of the export.
	Phase BackupExportPhase `json:"phase,omitempty"`
	// ServiceName is the ClusterIP Service the gateway dials.
	ServiceName string `json:"serviceName,omitempty"`
	// SnapshotID is the restic snapshot being exported.
	SnapshotID string `json:"snapshotID,omitempty"`
	// Message is a human-readable reason when the export failed or expired.
	Message string `json:"message,omitempty"`
}

// BackupExport mirrors operator/api/v1alpha1.BackupExport.
type BackupExport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupExportSpec   `json:"spec,omitempty"`
	Status BackupExportStatus `json:"status,omitempty"`
}

// BackupSpec mirrors the subset of operator/api/v1alpha1.BackupSpec the
// export path reads.
type BackupSpec struct {
	// ServerRef is the GameServer whose data the Backup captured.
	ServerRef corev1.LocalObjectReference `json:"serverRef"`
	// Strategy controls how the data was captured.
	Strategy string `json:"strategy,omitempty"`
}

// BackupStatus mirrors the subset of operator/api/v1alpha1.BackupStatus the
// API reads.
type BackupStatus struct {
	// Phase is the lifecycle phase of the Backup job.
	Phase string `json:"phase,omitempty"`
	// SnapshotID is the restic snapshot ID (or CSI VolumeSnapshot name).
	SnapshotID string `json:"snapshotID,omitempty"`
	// Size is the logical size reported by the driver.
	Size *resource.Quantity `json:"size,omitempty"`
}

// Backup mirrors operator/api/v1alpha1.Backup (only the fields the API needs).
type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupSpec   `json:"spec,omitempty"`
	Status BackupStatus `json:"status,omitempty"`
}

// backupExportNamePrefixMax bounds the Backup name copied into an export's
// generateName so the generated name stays well under the DNS-1123 limit.
const backupExportNamePrefixMax = 40

// CreateBackupExport creates a BackupExport for the named Backup, owned by it
// through a non-controlling ownerReference so the export is garbage-collected
// with the Backup. The apiserver assigns the name from generateName. The
// object returned is what the apiserver persisted. cluster is stamped into the
// BackupExportClusterAnnotation annotation.
func (c *Client) CreateBackupExport(ctx context.Context, ns, backupName, backupUID, format, cluster string, ttlSeconds *int32) (*BackupExport, error) {
	prefix := backupName
	if len(prefix) > backupExportNamePrefixMax {
		prefix = prefix[:backupExportNamePrefixMax]
	}
	controller := false
	blockOwnerDeletion := false
	exp := &BackupExport{
		// TypeMeta must be explicit for unstructured creates; see the
		// comment on CreateNetworkCapture.
		TypeMeta: metav1.TypeMeta{
			APIVersion: "gameplane.local/v1alpha1",
			Kind:       "BackupExport",
		},
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: prefix + "-export-",
			Namespace:    ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         "gameplane.local/v1alpha1",
				Kind:               "Backup",
				Name:               backupName,
				UID:                types.UID(backupUID),
				Controller:         &controller,
				BlockOwnerDeletion: &blockOwnerDeletion,
			}},
			Annotations: map[string]string{BackupExportClusterAnnotation: cluster},
		},
		Spec: BackupExportSpec{
			BackupRef:  corev1.LocalObjectReference{Name: backupName},
			BackupUID:  backupUID,
			Format:     format,
			TTLSeconds: ttlSeconds,
		},
	}
	raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(exp)
	if err != nil {
		return nil, fmt.Errorf("create backup export for %s: convert to unstructured: %w", backupName, err)
	}
	created, err := c.Dynamic.Resource(GVRBackupExport).Namespace(ns).
		Create(ctx, &unstructuredpkg.Unstructured{Object: raw}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create backup export for %s: %w", backupName, err)
	}
	result := &BackupExport{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(created.Object, result); err != nil {
		return nil, fmt.Errorf("create backup export for %s: convert from unstructured: %w", backupName, err)
	}
	return result, nil
}

// GetBackupExport fetches a BackupExport by namespace and name. Returns nil,
// nil when it does not exist.
func (c *Client) GetBackupExport(ctx context.Context, ns, name string) (*BackupExport, error) {
	u, err := c.Dynamic.Resource(GVRBackupExport).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get backup export %s: %w", name, err)
	}
	exp := &BackupExport{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, exp); err != nil {
		return nil, fmt.Errorf("get backup export %s: convert from unstructured: %w", name, err)
	}
	return exp, nil
}

// GetBackup fetches a Backup by namespace and name. Returns nil, nil when it
// does not exist.
func (c *Client) GetBackup(ctx context.Context, ns, name string) (*Backup, error) {
	u, err := c.Dynamic.Resource(GVRBackup).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get backup %s: %w", name, err)
	}
	b := &Backup{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, b); err != nil {
		return nil, fmt.Errorf("get backup %s: convert from unstructured: %w", name, err)
	}
	return b, nil
}

// DeleteBackupExport deletes a BackupExport by namespace and name. A missing
// export is not an error.
func (c *Client) DeleteBackupExport(ctx context.Context, ns, name string) error {
	if err := c.Dynamic.Resource(GVRBackupExport).Namespace(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete backup export %s: %w", name, err)
		}
	}
	return nil
}

// CountActiveBackupExports counts the BackupExports in a namespace that still
// hold or will hold a worker: phase empty (not yet reconciled), Pending or
// Ready. Completed, Failed and Expired exports are not counted.
func (c *Client) CountActiveBackupExports(ctx context.Context, ns string) (int, error) {
	ul, err := c.Dynamic.Resource(GVRBackupExport).Namespace(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("list backup exports in %s: %w", ns, err)
	}
	active := 0
	for _, u := range ul.Items {
		exp := &BackupExport{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, exp); err != nil {
			return 0, fmt.Errorf("list backup exports in %s: convert from unstructured: %w", ns, err)
		}
		switch exp.Status.Phase {
		case "", BackupExportPhasePending, BackupExportPhaseReady:
			active++
		}
	}
	return active, nil
}
