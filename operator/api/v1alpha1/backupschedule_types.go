package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupScheduleSpec defines a recurring backup policy for a GameServer.
// +kubebuilder:validation:XValidation:rule="self.strategy == 'volume-snapshot' || has(self.repoRef)",message="repoRef is required for the restic-snapshot strategy"
// +kubebuilder:validation:XValidation:rule="self.strategy != 'volume-snapshot' || ((!has(self.powerPolicy) || self.powerPolicy == 'preserve') && (!has(self.volumePolicy) || self.volumePolicy == 'primary-only'))",message="stopped and all-data policies require restic-snapshot"
type BackupScheduleSpec struct {
	// PowerPolicy preserves power by default; stop-copy-restart temporarily stops under lifecycle ownership.
	// +kubebuilder:validation:Enum=preserve;stop-copy-restart
	// +optional
	PowerPolicy string `json:"powerPolicy,omitempty"`
	// VolumePolicy captures primary-only by default; all-data includes classified persistent data.
	// +kubebuilder:validation:Enum=primary-only;all-data
	// +optional
	VolumePolicy string `json:"volumePolicy,omitempty"`

	// ServerRef is the GameServer to back up on this schedule.
	ServerRef LocalObjectRef `json:"serverRef"`

	// Schedule is a standard cron expression in the cluster's timezone.
	// Five-field form (no seconds). The Pattern is a structural guard
	// against typos like "every-night"; the controller uses
	// robfig/cron/v3 to parse for actual validity and surfaces a
	// failed condition on any expression that's well-formed but
	// uninterpretable. The optional sixth token tolerates the
	// seconds-prefix dialect.
	// +kubebuilder:validation:MinLength=9
	// +kubebuilder:validation:Pattern=`^\S+\s+\S+\s+\S+\s+\S+\s+\S+(\s+\S+)?$`
	Schedule string `json:"schedule"`

	// RepoRef is the restic repository credentials Secret. Required for the
	// restic-snapshot strategy; omit for volume-snapshot (CSI snapshots need
	// no restic repo).
	// +optional
	RepoRef *SecretKeySelector `json:"repoRef,omitempty"`

	// Strategy mirrors BackupSpec.Strategy.
	// +kubebuilder:validation:Enum=restic-snapshot;volume-snapshot
	// +kubebuilder:default=restic-snapshot
	// +optional
	Strategy string `json:"strategy,omitempty"`

	// Quiesce mirrors BackupSpec.Quiesce.
	//
	// No `omitempty`: the CRD default is true, so an `omitempty` tag would
	// let the typed client silently drop an explicit `false` on the wire
	// (Go's zero value for bool), and the apiserver would re-apply the
	// `true` default — silently re-enabling quiesce for a schedule that
	// explicitly disabled it (backupschedule_controller.go's fire() copies
	// this value into each Backup it creates via the typed client). Do not
	// add it back.
	// +kubebuilder:default=true
	// +optional
	Quiesce bool `json:"quiesce"`

	// Retention controls which past backups are kept. Unset means keep
	// everything (not recommended in production).
	// +optional
	Retention *BackupRetention `json:"retention,omitempty"`

	// Suspend pauses the schedule without deleting it.
	// +kubebuilder:default=false
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// ConcurrencyPolicy controls what happens when a new backup is due
	// while a previous one is still running.
	// +kubebuilder:validation:Enum=Allow;Forbid;Replace
	// +kubebuilder:default=Forbid
	// +optional
	ConcurrencyPolicy string `json:"concurrencyPolicy,omitempty"`

	// StartingDeadlineSeconds caps how late a missed schedule can start.
	// Matches CronJob semantics.
	// +optional
	StartingDeadlineSeconds *int64 `json:"startingDeadlineSeconds,omitempty"`
}

// BackupRetention follows restic's keep-* policy vocabulary. All fields
// are optional; setting none of them disables retention trimming.
type BackupRetention struct {
	// +optional
	KeepLast int32 `json:"keepLast,omitempty"`
	// +optional
	KeepHourly int32 `json:"keepHourly,omitempty"`
	// +optional
	KeepDaily int32 `json:"keepDaily,omitempty"`
	// +optional
	KeepWeekly int32 `json:"keepWeekly,omitempty"`
	// +optional
	KeepMonthly int32 `json:"keepMonthly,omitempty"`
	// +optional
	KeepYearly int32 `json:"keepYearly,omitempty"`
}

// BackupScheduleStatus is the observed state of a BackupSchedule.
type BackupScheduleStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +optional
	LastScheduleTime *metav1.Time `json:"lastScheduleTime,omitempty"`

	// +optional
	LastSuccessfulTime *metav1.Time `json:"lastSuccessfulTime,omitempty"`

	// NextScheduleTime is computed from Spec.Schedule and surfaced in
	// the dashboard.
	// +optional
	NextScheduleTime *metav1.Time `json:"nextScheduleTime,omitempty"`

	// Active lists in-flight Backup objects owned by this schedule.
	// +optional
	Active []corev1.ObjectReference `json:"active,omitempty"`

	// Conditions carry detailed state transitions.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=bks
// +kubebuilder:printcolumn:name="Server",type=string,JSONPath=`.spec.serverRef.name`
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="Suspended",type=boolean,JSONPath=`.spec.suspend`
// +kubebuilder:printcolumn:name="Last",type=date,JSONPath=`.status.lastSuccessfulTime`
// +kubebuilder:printcolumn:name="Next",type=string,JSONPath=`.status.nextScheduleTime`
// +kubebuilder:subresource:status

// BackupSchedule describes a recurring backup policy for a single
// GameServer. The operator creates Backup objects according to the
// schedule and prunes them according to the retention policy.
type BackupSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BackupScheduleSpec   `json:"spec,omitempty"`
	Status BackupScheduleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BackupScheduleList is a list of BackupSchedules.
type BackupScheduleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BackupSchedule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BackupSchedule{}, &BackupScheduleList{})
}
