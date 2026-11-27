package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// A Job at this Backup's Job name that another Backup controls is never
// mirrored: its snapshot id would be recorded on the wrong Backup.
func TestRetrySnapshotScrape_IgnoresForeignControlledJob(t *testing.T) {
	b := quiescedBackup()
	b.UID = "b1-uid"
	b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
	foreign := succeededJob(time.Second)
	foreign.OwnerReferences = []metav1.OwnerReference{{
		APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "Backup", Name: "other",
		UID: "other-uid", Controller: ownerBoolPtr(true),
	}}
	q := &scrapeQuiescer{}
	r := newLiveScrapeReconciler(t, b,
		scrapeLogReader{body: `{"message_type":"summary","snapshot_id":"snap-other","total_bytes_processed":1}`},
		time.Hour, q)
	if err := r.Create(context.Background(), foreign); err != nil {
		t.Fatalf("create foreign job: %v", err)
	}

	if _, err := r.retrySnapshotScrape(context.Background(), b); err != nil {
		t.Fatalf("retrySnapshotScrape: %v", err)
	}
	if b.Status.SnapshotID == "snap-other" {
		t.Fatal("recorded the foreign Job's snapshot id")
	}
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Errorf("phase = %q, want Failed (own Job gone)", b.Status.Phase)
	}
}
