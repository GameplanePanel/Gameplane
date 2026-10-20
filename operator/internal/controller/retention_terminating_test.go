package controller

import (
	"context"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// A Backup that retention already deleted stays Succeeded while its snapshot
// finalizer runs. trimBackups must not count it as a kept or candidate Backup:
// doing so would shift the keep window and delete a Backup that should stay.
func TestTrimBackups_IgnoresTerminatingBackups(t *testing.T) {
	mk := func(name string, age time.Duration, terminating bool) *gameplanev1alpha1.Backup {
		ct := metav1.NewTime(time.Now().Add(-age))
		b := &gameplanev1alpha1.Backup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: "ns",
				Labels:    map[string]string{"gameplane.local/backup-schedule": "sched"},
			},
			Status: gameplanev1alpha1.BackupStatus{
				Phase:          gameplanev1alpha1.BackupPhaseSucceeded,
				CompletionTime: &ct,
			},
		}
		if terminating {
			now := metav1.Now()
			b.DeletionTimestamp = &now
			b.Finalizers = []string{gameplanev1alpha1.BackupSnapshotFinalizer}
		}
		return b
	}
	// "newest" is already being deleted. With keepLast=1 the Backup to keep is
	// therefore "mid", and only "old" may be deleted.
	newest := mk("newest", time.Hour, true)
	mid := mk("mid", 2*time.Hour, false)
	old := mk("old", 3*time.Hour, false)

	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(newest, mid, old).Build()
	r := &BackupScheduleReconciler{Client: cl, Scheme: s}
	sched := &gameplanev1alpha1.BackupSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "sched", Namespace: "ns"},
		Spec: gameplanev1alpha1.BackupScheduleSpec{
			Retention: &gameplanev1alpha1.BackupRetention{KeepLast: 1},
		},
	}

	if err := r.trimBackups(context.Background(), sched); err != nil {
		t.Fatalf("trimBackups: %v", err)
	}

	exists := func(name string) bool {
		var got gameplanev1alpha1.Backup
		err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: name}, &got)
		if err != nil && !apierrors.IsNotFound(err) {
			t.Fatalf("get %s: %v", name, err)
		}
		return err == nil
	}
	if !exists("mid") {
		t.Error("mid was deleted; a terminating Backup must not take its place in the keep window")
	}
	if exists("old") {
		t.Error("old still exists, want it trimmed")
	}
	if !exists("newest") {
		t.Error("terminating Backup newest vanished, want it left to its finalizer")
	}
}
