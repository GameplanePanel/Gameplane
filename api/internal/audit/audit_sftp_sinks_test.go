package audit

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
)

// newSinkAuditor returns an Auditor with the stdout, webhook and S3 sinks attached (workers not started,
// so queued events stay readable on the sinks' channels).
func newSinkAuditor(t *testing.T) (*Auditor, *bytes.Buffer, *WebhookSink, *S3Sink) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	hook := NewWebhookSink("http://127.0.0.1:1/hook", "")
	s3, err := NewS3Sink(S3Config{Endpoint: "127.0.0.1:1", Bucket: "audit", AccessKey: "a", SecretKey: "s", Insecure: true})
	if err != nil {
		t.Fatalf("NewS3Sink: %v", err)
	}
	a := New(newStore(t), WithStdoutSink(logger), WithWebhookSink(hook), WithS3Sink(s3))
	return a, &logs, hook, s3
}

func TestMiddlewareSFTPSourceReachesAllSinks(t *testing.T) {
	a, logs, hook, s3 := newSinkAuditor(t)
	h := Middleware(a)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.SetActor(r.Context(), "alice")
		auth.SetSource(r.Context(), "sftp")
		auth.SetAuditTarget(r.Context(), "local/gameplane-games/srv /world")
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/servers/srv/files/stream", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(logs.String(), "source=sftp") {
		t.Errorf("stdout sink line lacks source: %s", logs.String())
	}
	for name, got := range map[string]Event{"webhook": <-hook.ch, "s3": <-s3.ch} {
		if got.Source != "sftp" || got.Actor != "alice" || got.Target != "local/gameplane-games/srv /world" {
			t.Errorf("%s event = %+v", name, got)
		}
	}
	var source string
	if err := a.db.DB.QueryRowContext(t.Context(), `SELECT source FROM audit_events`).Scan(&source); err != nil || source != "sftp" {
		t.Errorf("stored source = %q, %v", source, err)
	}
}

func TestMiddlewareDashboardRowFansOutWithoutSource(t *testing.T) {
	a, logs, hook, s3 := newSinkAuditor(t)
	h := Middleware(a)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/servers/srv/start", nil))
	if strings.Contains(logs.String(), "source=") {
		t.Errorf("dashboard row logged a source: %s", logs.String())
	}
	if got := <-hook.ch; got.Source != "" {
		t.Errorf("webhook source = %q", got.Source)
	}
	if got := <-s3.ch; got.Source != "" {
		t.Errorf("s3 source = %q", got.Source)
	}
}

func TestWriteSyncSFTPCallerFansOutToSinks(t *testing.T) {
	a, logs, hook, s3 := newSinkAuditor(t)
	ctx := auth.WithSFTPCaller(t.Context(), auth.SFTPCaller{Cluster: "local", SessionID: 3})
	if err := a.WriteSync(ctx, http.MethodPost, "/sftp/v1/login", "local", "", http.StatusOK); err != nil {
		t.Fatalf("WriteSync: %v", err)
	}
	if !strings.Contains(logs.String(), "source=sftp") || !strings.Contains(logs.String(), "actor=anonymous") {
		t.Errorf("stdout sink line = %s", logs.String())
	}
	if got := <-hook.ch; got.Source != "sftp" || got.Actor != "anonymous" {
		t.Errorf("webhook event = %+v", got)
	}
	if got := <-s3.ch; got.Source != "sftp" {
		t.Errorf("s3 event = %+v", got)
	}
}

func TestWriteSyncAsNamesActorAndDefaultsAnonymous(t *testing.T) {
	a, logs, hook, s3 := newSinkAuditor(t)
	// No SFTP caller: the row keeps an empty source and the logger line omits it.
	if err := a.WriteSyncAs(t.Context(), "bob", http.MethodPost, "/x", "t", "reason", http.StatusForbidden); err != nil {
		t.Fatalf("WriteSyncAs: %v", err)
	}
	if err := a.WriteSyncAs(t.Context(), "", http.MethodPost, "/y", "t", "", http.StatusOK); err != nil {
		t.Fatalf("WriteSyncAs anonymous: %v", err)
	}
	if strings.Contains(logs.String(), "source=") {
		t.Errorf("non-SFTP rows logged a source: %s", logs.String())
	}
	if got := <-hook.ch; got.Actor != "bob" || got.Reason != "reason" {
		t.Errorf("first webhook event = %+v", got)
	}
	if got := <-hook.ch; got.Actor != "anonymous" {
		t.Errorf("second webhook event = %+v", got)
	}
	<-s3.ch
	<-s3.ch
}

func TestWriteSyncReportsDatabaseFailure(t *testing.T) {
	a := New(newStore(t))
	if err := a.db.DB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := a.WriteSync(t.Context(), http.MethodPost, "/x", "", "", http.StatusOK); err == nil {
		t.Fatal("WriteSync on a closed database succeeded")
	}
}
