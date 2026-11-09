package controller

import (
	"path"
	"strings"
	"testing"

	v1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// Restic restores metadata on every ancestor of selected files, even when the
// ancestor is not itself included. Selecting the /volumes subtree makes that
// ancestor the metadata-free restore target instead of a read-only image path.
func TestRecoveryRestoreScopesArchiveSubtreeToMountedClaims(t *testing.T) {
	volumes := []v1.BackupVolumeCoverage{
		{ClaimName: "world", LogicalName: "primary", SnapshotRoot: "/volumes/primary", Included: true},
		{ClaimName: "mods", LogicalName: "mods-v1", SnapshotRoot: "/volumes/mods-v1", Included: true},
		{ClaimName: "cache", LogicalName: "extra-cache", Included: false},
	}
	rs := &v1.Restore{Status: v1.RestoreStatus{SnapshotID: "0123456789abcdef", RecoveryPlan: &v1.RestoreRecoveryPlan{Volumes: volumes}}}
	src := &v1.Backup{Spec: v1.BackupSpec{RepoRef: &v1.SecretKeySelector{Name: "repo"}}}
	spec := (&RestoreReconciler{}).buildRestorePodSpec(rs, src, &v1.GameTemplate{})
	worker := spec.Containers[0]
	if worker.SecurityContext.ReadOnlyRootFilesystem == nil || !*worker.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatal("restore weakened container root filesystem protection")
	}
	if worker.Args[1] != rs.Status.SnapshotID+":/volumes" {
		t.Fatalf("restore would apply archive ancestor metadata: %v", worker.Args)
	}
	target := ""
	includes := []string{}
	deleteEnabled := false
	for i, arg := range worker.Args {
		switch arg {
		case "--target":
			target = worker.Args[i+1]
		case "--include":
			includes = append(includes, worker.Args[i+1])
		case "--delete":
			deleteEnabled = true
		}
	}
	if target != "/volumes" || len(includes) != 2 || !deleteEnabled {
		t.Fatalf("incorrect restore scope: %v", worker.Args)
	}
	mounted := map[string]bool{}
	for _, mount := range worker.VolumeMounts {
		if mount.MountPath != "/tmp" {
			mounted[mount.MountPath] = !mount.ReadOnly
		}
	}
	for _, include := range includes {
		if strings.ContainsAny(include, "*?[") || !mounted[path.Join(target, include)] {
			t.Fatalf("include %q does not resolve to an exact writable PVC: %v", include, mounted)
		}
	}
	for _, volume := range spec.Volumes {
		if volume.PersistentVolumeClaim != nil && volume.PersistentVolumeClaim.ClaimName == "cache" {
			t.Fatal("restore mounted excluded rebuildable claim")
		}
	}
}
