package controller

import (
	"testing"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestBuildRestorePodSpec_UsesResticDeleteFlag covers F-049: without
// `--delete`, restic's restore leaves files created after the snapshot in
// place, so a restore doesn't actually make the volume match the snapshot.
func TestBuildRestorePodSpec_UsesResticDeleteFlag(t *testing.T) {
	r := &RestoreReconciler{}
	rs := &gameplanev1alpha1.Restore{}
	rs.Name = "rs-1"
	rs.Namespace = "ns"
	rs.Spec.ServerRef = gameplanev1alpha1.LocalObjectRef{Name: "smp"}
	rs.Status.SnapshotID = "snap-1"

	src := &gameplanev1alpha1.Backup{}
	src.Spec.RepoRef = &gameplanev1alpha1.SecretKeySelector{Name: "repo"}
	tmpl := &gameplanev1alpha1.GameTemplate{}

	ps := r.buildRestorePodSpec(rs, src, tmpl)
	if len(ps.Containers) != 1 {
		t.Fatalf("containers = %d, want 1", len(ps.Containers))
	}
	args := ps.Containers[0].Args
	found := false
	for _, a := range args {
		if a == "--delete" {
			found = true
		}
	}
	if !found {
		t.Errorf("restore args = %v, want --delete present so post-snapshot files are removed", args)
	}
	if len(args) < 2 || args[0] != "restore" || args[1] != rs.Status.SnapshotID {
		t.Errorf("restore args = %v, want to start with [restore %s]", args, rs.Status.SnapshotID)
	}
}
