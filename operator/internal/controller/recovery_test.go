package controller

import (
	v1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
)

// Runtime execution is CI-only; these regression sources precede implementation.
func TestRecoveryRejectsUnsafeRoots(t *testing.T) {
	for _, root := range []string{"/", "/data", "/volumes/../data", "/volumes/a/b", "/volumes/*", "/volumes/extra;echo"} {
		t.Run(root, func(t *testing.T) {
			m := &v1.BackupCaptureManifest{State: "Recorded", FormatVersion: 2, VolumePolicy: "all-data", Volumes: []v1.BackupVolumeCoverage{{ClaimName: "s-data", ClaimUID: "uid", LogicalName: "primary", SnapshotRoot: root, Included: true, Classification: "data", MountPaths: []string{"/data"}}}}
			if err := validateRecoveryManifest(m); err == nil {
				t.Fatal("unsafe root admitted")
			}
		})
	}
}
func TestRecoveryRejectsDuplicateClaims(t *testing.T) {
	m := &v1.BackupCaptureManifest{State: "Recorded", FormatVersion: 2, VolumePolicy: "all-data", Volumes: []v1.BackupVolumeCoverage{
		{ClaimName: "s-data", ClaimUID: "uid", LogicalName: "primary", SnapshotRoot: "/volumes/primary", Included: true, Classification: "data", MountPaths: []string{"/data"}},
		{ClaimName: "s-data", ClaimUID: "uid", LogicalName: "extra-world", SnapshotRoot: "/volumes/extra-world", Included: true, Classification: "data", MountPaths: []string{"/world"}},
	}}
	if err := validateRecoveryManifest(m); err == nil {
		t.Fatal("duplicate claim admitted")
	}
}
func TestRecoveryWorkersUseDisjointExplicitRoots(t *testing.T) {
	b := &v1.Backup{Spec: v1.BackupSpec{RepoRef: &v1.SecretKeySelector{Name: "repo"}, VolumePolicy: "all-data"}, Status: v1.BackupStatus{CaptureManifest: &v1.BackupCaptureManifest{Volumes: []v1.BackupVolumeCoverage{
		{ClaimName: "world", LogicalName: "primary", SnapshotRoot: "/volumes/primary", Included: true},
		{ClaimName: "mods", LogicalName: "mods-v1", SnapshotRoot: "/volumes/mods-v1", Included: true},
		{ClaimName: "cache", LogicalName: "extra-cache", RecoveryPrerequisite: "rebuild", Included: false},
	}}}}
	spec := (&BackupReconciler{}).buildBackupPodSpec(b, &v1.GameTemplate{})
	if strings.Contains(strings.Join(spec.Containers[0].Args, " "), "*") {
		t.Fatal("wildcard capture")
	}
	seen := map[string]bool{}
	for _, m := range spec.Containers[0].VolumeMounts {
		if m.Name != "cache" {
			if !m.ReadOnly {
				t.Fatal("writable capture")
			}
			seen[m.MountPath] = true
		}
	}
	if !seen["/volumes/primary"] || !seen["/volumes/mods-v1"] || seen["/data"] {
		t.Fatalf("unexpected mounts: %v", seen)
	}
	for _, v := range spec.Volumes {
		if v.PersistentVolumeClaim != nil && v.PersistentVolumeClaim.ClaimName == "cache" {
			t.Fatal("rebuildable mounted")
		}
	}
}
func TestStoppedBackupPowerBaseline(t *testing.T) {
	for _, tc := range []struct {
		name    string
		suspend bool
		idle    string
		running bool
	}{{"running", false, "", true}, {"suspended", true, "", false}, {"idle", false, "2026-01-01", false}} {
		t.Run(tc.name, func(t *testing.T) {
			gs := &v1.GameServer{ObjectMeta: metav1.ObjectMeta{Generation: 4, Annotations: map[string]string{IdleAsleepSinceAnnotation: tc.idle}}, Spec: v1.GameServerSpec{Suspend: tc.suspend}}
			debt := newStoppedBackup(gs)
			if debt.WasRunning != tc.running {
				t.Fatal("incorrect admitted power")
			}
			gs.Spec.Suspend = true
			if stoppedBackupMayResume(gs, debt) {
				t.Fatal("overrode user suspension")
			}
			gs.Spec.Suspend = false
			gs.Annotations[IdleAsleepSinceAnnotation] = "later"
			if stoppedBackupMayResume(gs, debt) {
				t.Fatal("overrode idle intent")
			}
		})
	}
}
func TestSelectedClaimDrainMatchesForeignClaims(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "foreign", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "external-world"}}}}}}
	if !podMountsRecoveryClaims(pod, []v1.BackupVolumeCoverage{{ClaimName: "external-world", Included: true}}) {
		t.Fatal("foreign claim writer ignored")
	}
}
