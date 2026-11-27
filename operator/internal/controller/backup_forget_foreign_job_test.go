package controller

import (
	"context"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// A Job at the Backup's copy-Job name that another Backup controls (a short
// name equal to a long name's shortened form) is never deleted or awaited.
func TestFinalizeSnapshot_LeavesForeignControlledBackupJob(t *testing.T) {
	b := forgetBackup()
	b.UID = "b1-uid"
	b.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
	b.Status.SnapshotID = ""
	foreign := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns", OwnerReferences: []metav1.OwnerReference{{
			APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "Backup", Name: "other",
			UID: "other-uid", Controller: ownerBoolPtr(true),
		}}},
		Status: batchv1.JobStatus{Active: 1},
	}
	r, _ := newForgetReconciler(t, b, forgetRepoSecret(), foreign)

	if _, err := r.Reconcile(context.Background(), forgetRequest()); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := getJobNamed(t, r, "b1"); !ok {
		t.Fatal("foreign-controlled backup job was deleted")
	}
}

// The forget Job's backup label stays within the 63-character label limit
// for a Backup name longer than that.
func TestRunForgetJob_LongBackupNameLabelFits(t *testing.T) {
	b := forgetBackup()
	b.Name = strings.Repeat("a", 80)
	r, _ := newForgetReconciler(t, b, forgetRepoSecret())

	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "ns", Name: b.Name}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	job, ok := getJobNamed(t, r, forgetJobName(b.Name))
	if !ok {
		t.Fatal("forget job not created")
	}
	want := backupJobName(b.Name)
	if len(want) > 63 {
		t.Fatalf("label value %q longer than 63", want)
	}
	if got := job.Labels[forgetBackupLabel]; got != want {
		t.Fatalf("job label = %q, want %q", got, want)
	}
	if got := job.Spec.Template.Labels[forgetBackupLabel]; got != want {
		t.Fatalf("pod label = %q, want %q", got, want)
	}
}
