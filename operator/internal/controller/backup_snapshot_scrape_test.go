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
	"sigs.k8s.io/controller-runtime/pkg/client"

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

func (q *scrapeQuiescer) CheckPod(context.Context, string, string, string) error { return nil }
func (q *scrapeQuiescer) QuiescePod(ctx context.Context, namespace, server, _ string) error {
	return q.Quiesce(ctx, namespace, server)
}
func (q *scrapeQuiescer) UnquiescePod(ctx context.Context, namespace, server, _ string) error {
	return q.Unquiesce(ctx, namespace, server)
}

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
	b.Annotations = map[string]string{
		annoQuiesceAttempted:        "true",
		backupPreparationAnnotation: `{"outcome":"live","targetUID":"server-uid","workloadUID":"workload-uid","podUID":"game-pod-uid"}`,
	}
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
	grace time.Duration, agent AgentQuiescer, extra ...client.Object,
) *BackupReconciler {
	t.Helper()
	r := newScrapeReconcilerWithObjects(t, b, agent, extra...)
	r.LogReader = lr
	r.SnapshotScrapeGracePeriod = grace
	return r
}

// A live cleanup test must observe the same pod that the Backup recorded.
// Otherwise the reconciler must withhold save-on or retire the original process.
func newLiveScrapeReconciler(
	t *testing.T, b *gameplanev1alpha1.Backup, lr BackupLogReader,
	grace time.Duration, agent AgentQuiescer,
) *BackupReconciler {
	t.Helper()
	b.Spec.ServerRef.Name = "server"
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: b.Namespace, UID: "server-uid"}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "server-0", Namespace: b.Namespace, UID: "game-pod-uid"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{backupRunningGameStatus()}},
	}
	return newScrapeReconciler(t, b, lr, grace, agent, gs, pod)
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

func TestMirrorJobStatusPackedBytes(t *testing.T) {
	for _, tc := range []struct {
		field string
		want  int64
	}{
		{"", -1}, {`,"data_added":121`, -1}, {`,"data_added_packed":0`, 0},
		{`,"data_added_packed":317`, 317}, {`,"data_added_packed":-1`, -1},
	} {
		b := scrapeBackup()
		body := `{"message_type":"summary","snapshot_id":"abc","total_bytes_processed":2048` + tc.field + "}"
		r := newScrapeReconciler(t, b, scrapeLogReader{body: body}, time.Hour, nil)
		if _, err := r.mirrorJobStatus(context.Background(), b, succeededJob(time.Second)); err != nil {
			t.Fatal(err)
		}
		if b.Status.Size == nil || b.Status.Size.Value() != 2048 {
			t.Fatalf("logical size = %v", b.Status.Size)
		}
		got := b.Status.BytesAdded
		if tc.want < 0 && got != nil || tc.want >= 0 && (got == nil || got.Value() != tc.want) {
			t.Errorf("summary %s: added %v, want %d (negative means unknown)", body, got, tc.want)
		}
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
	r := newLiveScrapeReconciler(t, b, scrapeLogReader{body: ""}, time.Millisecond, q)

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
	r := newLiveScrapeReconciler(t, b, scrapeLogReader{body: ""}, time.Millisecond, q)

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
	r := newLiveScrapeReconciler(t, b, scrapeLogReader{body: ""}, time.Hour, q)

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
