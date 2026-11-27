package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RestorePhase is a high-level state for a Restore job.
// +kubebuilder:validation:Enum=Pending;Receiving;AwaitingConfirmation;Suspending;Running;Resuming;Succeeded;Failed
type RestorePhase string

// RestorePhase constants define the possible values for a Restore's phase.
const (
	RestorePhasePending RestorePhase = "Pending"
	// RestorePhaseReceiving: an upload restore is receiving and checking its archive (spec 041).
	RestorePhaseReceiving RestorePhase = "Receiving"
	// RestorePhaseAwaitingConfirmation: an upload restore passed its checks and waits for the user.
	RestorePhaseAwaitingConfirmation RestorePhase = "AwaitingConfirmation"
	RestorePhaseSuspending           RestorePhase = "Suspending"
	RestorePhaseRunning              RestorePhase = "Running"
	RestorePhaseResuming             RestorePhase = "Resuming"
	RestorePhaseSucceeded            RestorePhase = "Succeeded"
	RestorePhaseFailed               RestorePhase = "Failed"
)

// RestoreSpec is the desired state of a one-shot restore.
// +kubebuilder:validation:XValidation:rule="has(self.backupRef) != has(self.upload)",message="exactly one of backupRef and upload is required"
// +kubebuilder:validation:XValidation:rule="has(self.backupRef) == has(oldSelf.backupRef) && has(self.upload) == has(oldSelf.upload)",message="restore source cannot change"
type RestoreSpec struct {
	// BackupRef is the source Backup. Exactly one of backupRef and upload is set.
	// Must live in the same namespace
	// and have status.phase=Succeeded with a non-empty snapshotID.
	// +optional
	BackupRef *LocalObjectRef `json:"backupRef,omitempty"`

	// ServerRef is the target GameServer.
	//
	// For restic-snapshot backups the target must already exist; it is
	// suspended and its data volume is overwritten in place (it may be a
	// pre-created empty server, restoring into a fresh server).
	//
	// For volume-snapshot backups the target must NOT already exist: the
	// restore provisions a brand-new GameServer under this name, copying
	// the original server's spec and seeding its data volume from the CSI
	// snapshot. The original server is never touched.
	ServerRef LocalObjectRef `json:"serverRef"`

	// Upload restores the target from an uploaded archive instead of a Backup
	// (spec 041). The archive is checked on a temporary staging volume and
	// copied over the target only after the user confirms; it is not kept.
	// +optional
	Upload *RestoreUpload `json:"upload,omitempty"`
}

// RestoreUpload describes an uploaded archive (spec 041 OD-3 to OD-5).
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.confirmed) || !oldSelf.confirmed || (has(self.confirmed) && self.confirmed)",message="confirmed cannot be cleared"
// +kubebuilder:validation:XValidation:rule="self.fileName == oldSelf.fileName && self.format == oldSelf.format && self.declaredBytes == oldSelf.declaredBytes && (has(self.confirmedTemplate) == has(oldSelf.confirmedTemplate)) && (!has(self.confirmedTemplate) || self.confirmedTemplate == oldSelf.confirmedTemplate)",message="upload fields other than confirmed are immutable"
type RestoreUpload struct {
	// FileName as uploaded, for audit and display only.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	FileName string `json:"fileName"`
	// +kubebuilder:validation:Enum=tar.gz;zip
	Format string `json:"format"`
	// DeclaredBytes is the Content-Length the API accepted.
	// +kubebuilder:validation:Minimum=1
	DeclaredBytes int64 `json:"declaredBytes"`
	// ConfirmedTemplate is the GameTemplate the user said a plain archive
	// (no Gameplane manifest) is for (OD-4). Required for plain archives.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	ConfirmedTemplate string `json:"confirmedTemplate,omitempty"`
	// Confirmed is set by the user after reviewing status.upload. It can
	// only change from false to true.
	// +optional
	Confirmed bool `json:"confirmed,omitempty"`
}

// ArchiveManifestSummary is the part of an archive manifest shown before
// confirmation.
type ArchiveManifestSummary struct {
	// +optional
	FormatVersion int32 `json:"formatVersion,omitempty"`
	// +optional
	Cluster string `json:"cluster,omitempty"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// +optional
	ServerName string `json:"serverName,omitempty"`
	// +optional
	ServerUID string `json:"serverUID,omitempty"`
	// +optional
	TemplateName string `json:"templateName,omitempty"`
	// +optional
	TemplateRevision string `json:"templateRevision,omitempty"`
	// +optional
	GameVersion string `json:"gameVersion,omitempty"`
	// +optional
	CapturedAt *metav1.Time `json:"capturedAt,omitempty"`
	// +optional
	Volumes []string `json:"volumes,omitempty"`
}

// RestoreUploadStatus reports the receive and check result of an upload restore.
type RestoreUploadStatus struct {
	// Kind is "gameplane" (manifest found) or "plain".
	// +optional
	Kind string `json:"kind,omitempty"`
	// +optional
	Manifest *ArchiveManifestSummary `json:"manifest,omitempty"`
	// +optional
	UnpackedBytes int64 `json:"unpackedBytes,omitempty"`
	// StagingClaimName is the temporary PVC (OD-7); deleted when the restore ends.
	// +optional
	StagingClaimName string `json:"stagingClaimName,omitempty"`
	// ReceiverService is the ClusterIP Service the API or gateway streams the upload to.
	// +optional
	ReceiverService string `json:"receiverService,omitempty"`
	// ReceivedAt is when the whole file reached the staging volume.
	// +optional
	ReceivedAt *metav1.Time `json:"receivedAt,omitempty"`
	// Warnings are shown before confirmation (older game version, zip permission reset).
	// +optional
	Warnings []string `json:"warnings,omitempty"`
}

// RestoreRecoveryPlan pins a validated target mapping before any power mutation.
type RestoreRecoveryPlan struct {
	// Admitted power intent is informational; cleanup never overwrites newer user intent.
	AdmittedSuspend bool `json:"admittedSuspend"`
	// +optional
	AdmittedIdle       string `json:"admittedIdle,omitempty"`
	AdmittedGeneration int64  `json:"admittedGeneration"`

	BackupUID        string                 `json:"backupUID"`
	ManifestDigest   string                 `json:"manifestDigest"`
	TargetUID        string                 `json:"targetUID"`
	TemplateRevision string                 `json:"templateRevision"`
	GameVersion      string                 `json:"gameVersion"`
	Volumes          []BackupVolumeCoverage `json:"volumes"`
}

// RestoreStatus is the observed state of a Restore.
type RestoreStatus struct {
	// +optional
	RecoveryPlan *RestoreRecoveryPlan `json:"recoveryPlan,omitempty"`

	// Upload is set for upload restores (spec 041).
	// +optional
	Upload *RestoreUploadStatus `json:"upload,omitempty"`

	// +optional
	Phase RestorePhase `json:"phase,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// SnapshotID is the restic snapshot used. Resolved from the
	// referenced Backup at Pending → Suspending transition and pinned
	// here so retention can't surprise us mid-run.
	// +optional
	SnapshotID string `json:"snapshotID,omitempty"`

	// StartTime / CompletionTime bracket the actual restore Job.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

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
// +kubebuilder:resource:shortName=rs
// +kubebuilder:printcolumn:name="Backup",type=string,JSONPath=`.spec.backupRef.name`
// +kubebuilder:printcolumn:name="Server",type=string,JSONPath=`.spec.serverRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Completed",type=date,JSONPath=`.status.completionTime`
// +kubebuilder:subresource:status

// Restore represents a one-shot restore of a Backup's snapshot into a
// GameServer's data volume. The target server is suspended for the
// duration of the restore Job and resumed on success.
type Restore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RestoreSpec   `json:"spec,omitempty"`
	Status RestoreStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RestoreList is a list of Restores.
type RestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Restore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Restore{}, &RestoreList{})
}
