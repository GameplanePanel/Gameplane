package controller

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// scrapeLogReader is a BackupLogReader stub: it returns body as the restic
// pod logs, or err if set.
type scrapeLogReader struct {
	body string
	err  error
}

func (s scrapeLogReader) BackupLogs(_ context.Context, _, _ string) (io.ReadCloser, error) {
	if s.err != nil {
		return nil, s.err
	}
	return io.NopCloser(strings.NewReader(s.body)), nil
}

// scrapeQuiescer records whether the post-backup unquiesce actually ran.
// unquiesceErr, when set, simulates an unreachable agent.
type scrapeQuiescer struct {
	unquiesced   int
	unquiesceErr error
}

func (q *scrapeQuiescer) Quiesce(_ context.Context, _, _ string) error { return nil }

func (q *scrapeQuiescer) Unquiesce(_ context.Context, _, _ string) error {
	if q.unquiesceErr != nil {
		return q.unquiesceErr
	}
	q.unquiesced++
	return nil
}

func scrapeScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := gameplanev1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("gameplane scheme: %v", err)
	}
	if err := batchv1.AddToScheme(s); err != nil {
		t.Fatalf("batch scheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("core scheme: %v", err)
	}
	return s
}

// quiescedBackup is a Backup whose world was quiesced (auto-save off) and whose
// matching unquiesce has not run yet.
func quiescedBackup() *gameplanev1alpha1.Backup {
	b := scrapeBackup()
	b.Annotations = map[string]string{annoQuiesceAttempted: "true"}
	return b
}

func scrapeBackup() *gameplanev1alpha1.Backup {
	b := &gameplanev1alpha1.Backup{}
	b.Name = "b1"
	b.Namespace = "ns"
	return b
}

// succeededJob is a restic Job that finished successfully completedAgo in the
// past — the reconciler reads CompletionTime to bound the snapshot-id scrape.
func succeededJob(completedAgo time.Duration) *batchv1.Job {
	ct := metav1.NewTime(time.Now().Add(-completedAgo))
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "b1", Namespace: "ns"},
		Status:     batchv1.JobStatus{Succeeded: 1, CompletionTime: &ct},
	}
}

func newScrapeReconciler(
	t *testing.T, b *gameplanev1alpha1.Backup, lr BackupLogReader,
	grace time.Duration, agent AgentQuiescer,
) *BackupReconciler {
	t.Helper()
	s := scrapeScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(b).
		WithStatusSubresource(&gameplanev1alpha1.Backup{}).
		Build()
	r := &BackupReconciler{Client: cl, Scheme: s, LogReader: lr, SnapshotScrapeGracePeriod: grace}
	if agent != nil {
		r.AgentClient = agent
	}
	return r
}

// A successful Job whose logs carry a summary line resolves to Succeeded with
// the snapshot id + size populated.
func TestMirrorJobStatus_ScrapesSnapshotID(t *testing.T) {
	b := scrapeBackup()
	r := newScrapeReconciler(t, b,
		scrapeLogReader{body: `{"message_type":"summary","snapshot_id":"snap-abc","total_bytes_processed":2048}`},
		time.Hour, nil)

	if _, err := r.mirrorJobStatus(context.Background(), b, succeededJob(time.Second)); err != nil {
		t.Fatalf("mirrorJobStatus: %v", err)
	}
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded {
		t.Errorf("phase = %q, want Succeeded", b.Status.Phase)
	}
	if b.Status.SnapshotID != "snap-abc" {
		t.Errorf("snapshotID = %q, want snap-abc", b.Status.SnapshotID)
	}
	if b.Status.Size == nil || b.Status.Size.Value() != 2048 {
		t.Errorf("size = %v, want 2048", b.Status.Size)
	}
}

// While the snapshot id can't yet be read but the grace period hasn't lapsed,
// the Backup stays Succeeded (unquiesce has run) and requeues to retry.
func TestMirrorJobStatus_RetriesWhenSnapshotUnavailable(t *testing.T) {
	b := scrapeBackup()
	// Empty logs => ErrNoResticSummary; generous grace so we retry rather than fail.
	r := newScrapeReconciler(t, b, scrapeLogReader{body: ""}, time.Hour, nil)

	res, err := r.mirrorJobStatus(context.Background(), b, succeededJob(time.Second))
	if err != nil {
		t.Fatalf("mirrorJobStatus: %v", err)
	}
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded {
		t.Errorf("phase = %q, want Succeeded (retrying)", b.Status.Phase)
	}
	if b.Status.SnapshotID != "" {
		t.Errorf("snapshotID = %q, want empty", b.Status.SnapshotID)
	}
	if res.RequeueAfter == 0 {
		t.Errorf("expected a requeue to retry the scrape, got %+v", res)
	}
}

// Once the grace period since Job completion lapses without a snapshot id, the
// Backup is failed rather than left at a misleading Succeeded.
func TestMirrorJobStatus_FailsWhenSnapshotUnavailablePastGrace(t *testing.T) {
	b := scrapeBackup()
	r := newScrapeReconciler(t, b, scrapeLogReader{body: ""}, time.Millisecond, nil)

	if _, err := r.mirrorJobStatus(context.Background(), b, succeededJob(time.Minute)); err != nil {
		t.Fatalf("mirrorJobStatus: %v", err)
	}
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Errorf("phase = %q, want Failed", b.Status.Phase)
	}
	if b.Status.SnapshotID != "" {
		t.Errorf("snapshotID = %q, want empty", b.Status.SnapshotID)
	}
	if b.Status.Message == "" {
		t.Errorf("expected a failure message explaining the missing snapshot id")
	}
}

// Failing an unrestorable Backup must first release the quiesced world. If the
// operator was down longer than the grace period, the very first pass that sees
// the Job Succeeded also sees the grace lapsed — and once the Backup is Failed
// the reconcile guard short-circuits, so a skipped unquiesce is never retried
// and the game sits with auto-save off forever.
func TestMirrorJobStatus_UnquiescesBeforeFailingPastGrace(t *testing.T) {
	b := quiescedBackup()
	q := &scrapeQuiescer{}
	r := newScrapeReconciler(t, b, scrapeLogReader{body: ""}, time.Millisecond, q)

	if _, err := r.mirrorJobStatus(context.Background(), b, succeededJob(time.Minute)); err != nil {
		t.Fatalf("mirrorJobStatus: %v", err)
	}
	if q.unquiesced != 1 {
		t.Errorf("Unquiesce called %d times, want exactly 1 before the Backup goes terminal", q.unquiesced)
	}
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Errorf("phase = %q, want Failed", b.Status.Phase)
	}
}

// If the world can't be released yet (agent unreachable), don't go terminal —
// requeue and keep retrying, or the unquiesce would be abandoned.
func TestMirrorJobStatus_DoesNotFailWhileUnquiesceIsPending(t *testing.T) {
	b := quiescedBackup()
	q := &scrapeQuiescer{unquiesceErr: errors.New("agent unreachable")}
	r := newScrapeReconciler(t, b, scrapeLogReader{body: ""}, time.Millisecond, q)

	res, err := r.mirrorJobStatus(context.Background(), b, succeededJob(time.Minute))
	if err != nil {
		t.Fatalf("mirrorJobStatus: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Errorf("expected a requeue to retry the unquiesce, got %+v", res)
	}
	if b.Status.Phase == gameplanev1alpha1.BackupPhaseFailed {
		t.Error("Backup went terminal while its world was still quiesced")
	}
}

// When the Job is gone its pod logs went with it, so the snapshot id can never
// be recovered: fail immediately (after unquiescing) rather than re-creating the
// Job and silently running a second backup.
func TestRetrySnapshotScrape_FailsWhenJobIsGone(t *testing.T) {
	b := quiescedBackup()
	b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
	q := &scrapeQuiescer{}
	r := newScrapeReconciler(t, b, scrapeLogReader{body: ""}, time.Hour, q)

	if _, err := r.retrySnapshotScrape(context.Background(), b); err != nil {
		t.Fatalf("retrySnapshotScrape: %v", err)
	}
	if q.unquiesced != 1 {
		t.Errorf("Unquiesce called %d times, want 1", q.unquiesced)
	}
	if b.Status.Phase != gameplanev1alpha1.BackupPhaseFailed {
		t.Errorf("phase = %q, want Failed (job gone, snapshot id unrecoverable)", b.Status.Phase)
	}
}
