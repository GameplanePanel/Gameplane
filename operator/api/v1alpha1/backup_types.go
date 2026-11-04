package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupPhase is a high-level state for a Backup job.
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed
type BackupPhase string

// BackupPhase constants define the possible values for a Backup's phase.
const (
	BackupPhasePending   BackupPhase = "Pending"
	BackupPhaseRunning   BackupPhase = "Running"
	BackupPhaseSucceeded BackupPhase = "Succeeded"
	BackupPhaseFailed    BackupPhase = "Failed"
)

// BackupFinalizer blocks deletion until copy workers have drained and any
// matching unquiesce has completed. Every Backup carries it, including raw
// copies and CSI snapshots, so deletion cannot release shared lifecycle
// ownership while a copy is still using the target. See finalizeDelete.
const BackupFinalizer = "gameplane.local/backup-finalizer"

// BackupSnapshotFinalizer blocks the deletion of a restic-strategy Backup until
// the operator has run `restic forget --prune` for the Backup's snapshot (or has
// given up after a bounded wait and left the snapshot in the repository). Without
// it, deleting a Backup — by hand, through the API, or via BackupSchedule
// retention — orphans its snapshot forever. Added to every Backup that has a
// repoRef and a non-volume-snapshot strategy; volume-snapshot Backups need none
// because their VolumeSnapshot is garbage-collected through its owner reference.
// See backup_forget.go's finalizeSnapshot.
const BackupSnapshotFinalizer = "gameplane.local/backup-snapshot-finalizer"

// BackupSpec is the desired state of a one-shot backup.
// +kubebuilder:validation:XValidation:rule="self.strategy == 'volume-snapshot' || has(self.repoRef)",message="repoRef is required for the restic-snapshot strategy"
type BackupSpec struct {
	// ServerRef is the GameServer whose data volume should be backed up.
	ServerRef LocalObjectRef `json:"serverRef"`

	// RepoRef points at a Secret containing the restic repository URL
	// and credentials. See docs for the expected key layout. Required for
	// the restic-snapshot strategy; ignored (and may be omitted) for
	// volume-snapshot, which captures a CSI snapshot instead of using restic.
	// +optional
	RepoRef *SecretKeySelector `json:"repoRef,omitempty"`

	// Strategy controls how the data is captured.
	// - "restic-snapshot" (default): run restic against the PVC mount
	// - "volume-snapshot": use a CSI VolumeSnapshot instead of restic
	// +kubebuilder:validation:Enum=restic-snapshot;volume-snapshot
	// +kubebuilder:default=restic-snapshot
	// +optional
	Strategy string `json:"strategy,omitempty"`

	// VolumeSnapshotClassName names the VolumeSnapshotClass to use when
	// Strategy=volume-snapshot. Empty selects the cluster's default
	// snapshot class. Ignored for restic-snapshot.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	VolumeSnapshotClassName *string `json:"volumeSnapshotClassName,omitempty"`

	// Quiesce, when true, asks the agent to flush/pause the game before
	// the snapshot (via RCON "save-all" or game-specific hook) and
	// resume after. Requires an agent that supports the game.
	//
	// No `omitempty`: the CRD default is true, so an `omitempty` tag would
	// let the typed client silently drop an explicit `false` on the wire
	// (Go's zero value for bool), and the apiserver would re-apply the
	// `true` default — silently re-enabling quiesce for a user/schedule
	// that explicitly disabled it. Do not add it back.
	// +kubebuilder:default=true
	// +optional
	Quiesce bool `json:"quiesce"`

	// Tags are attached to the restic snapshot for filtering.
	// +optional
	Tags []string `json:"tags,omitempty"`
}

// LocalObjectRef references a namespaced object by name only. Assumed
// to live in the same namespace as the referring object.
type LocalObjectRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// BackupStatus is the observed state of a Backup.
type BackupStatus struct {
	// +optional
	Phase BackupPhase `json:"phase,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// StartTime / CompletionTime bracket the actual backup run.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// SnapshotID is the restic snapshot ID (or CSI VolumeSnapshot name)
	// for use in restore operations.
	// +optional
	SnapshotID string `json:"snapshotID,omitempty"`

	// VolumeSnapshotContentName is the cluster-scoped VolumeSnapshotContent
	// bound to status.snapshotID, recorded once the VolumeSnapshot reports
	// readyToUse. Restore checks it to confirm the snapshot actually bound
	// before standing up a new server. Empty for restic-snapshot backups.
	// +optional
	VolumeSnapshotContentName string `json:"volumeSnapshotContentName,omitempty"`

	// Size is the on-disk size of the snapshot, as reported by the
	// backup driver. Not always exact for incremental snapshots.
	// +optional
	Size *resource.Quantity `json:"size,omitempty"`

	// Message is a human-readable reason when Phase=Failed.
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions carry detailed state transitions.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=bk
// +kubebuilder:printcolumn:name="Server",type=string,JSONPath=`.spec.serverRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=`.status.size`
// +kubebuilder:printcolumn:name="Completed",type=date,JSONPath=`.status.completionTime`
// +kubebuilder:subresource:status

// Backup represents a one-shot backup of a GameServer's data volume.
// Recurring backups are expressed as BackupSchedules, which create
// Backup objects over time.
type Backup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupSpec   `json:"spec,omitempty"`
	Status BackupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BackupList is a list of Backups.
type BackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Backup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Backup{}, &BackupList{})
}
