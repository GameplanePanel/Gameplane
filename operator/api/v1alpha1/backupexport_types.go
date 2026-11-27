package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupExportPhase is the high-level state of one backup download.
// +kubebuilder:validation:Enum=Pending;Ready;Completed;Failed;Expired
type BackupExportPhase string

// BackupExportPhase constants.
const (
	BackupExportPhasePending   BackupExportPhase = "Pending"
	BackupExportPhaseReady     BackupExportPhase = "Ready"
	BackupExportPhaseCompleted BackupExportPhase = "Completed"
	BackupExportPhaseFailed    BackupExportPhase = "Failed"
	BackupExportPhaseExpired   BackupExportPhase = "Expired"
)

// BackupExportSpec is the desired state of one download of a restic Backup
// as a .tar.gz or .zip archive (spec 041). The API creates it and deletes it
// when the transfer ends; the operator runs the export worker.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
type BackupExportSpec struct {
	// BackupRef is the source Backup in the same namespace.
	BackupRef LocalObjectRef `json:"backupRef"`
	// BackupUID pins the Backup identity; a Backup recreated under the same
	// name is refused.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	BackupUID string `json:"backupUID"`
	// Format of the archive.
	// +kubebuilder:validation:Enum=tar.gz;zip
	Format string `json:"format"`
	// TTLSeconds bounds the export's life. Defaults to the operator's
	// --backup-export-ttl when unset.
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=86400
	// +optional
	TTLSeconds *int32 `json:"ttlSeconds,omitempty"`
}

// BackupExportStatus is the observed state of a BackupExport.
type BackupExportStatus struct {
	// +optional
	Phase BackupExportPhase `json:"phase,omitempty"`
	// ServiceName is the ClusterIP Service the API or gateway dials. It is
	// also the TLS server name of the worker certificate.
	// +optional
	ServiceName string `json:"serviceName,omitempty"`
	// SnapshotID is the restic snapshot being exported.
	// +optional
	SnapshotID string `json:"snapshotID,omitempty"`
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	// BytesSent is reported by the worker when the transfer ends.
	// +optional
	BytesSent *resource.Quantity `json:"bytesSent,omitempty"`
	// Message is a human-readable reason when Phase=Failed or Expired.
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=bkx
// +kubebuilder:printcolumn:name="Backup",type=string,JSONPath=`.spec.backupRef.name`
// +kubebuilder:printcolumn:name="Format",type=string,JSONPath=`.spec.format`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:subresource:status

// BackupExport is one download of a Backup as an archive.
type BackupExport struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupExportSpec   `json:"spec,omitempty"`
	Status BackupExportStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BackupExportList is a list of BackupExports.
type BackupExportList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupExport `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BackupExport{}, &BackupExportList{})
}
