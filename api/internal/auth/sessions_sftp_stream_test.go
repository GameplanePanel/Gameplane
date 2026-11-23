package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// sftpStreamRequest returns a transfer request authenticated the way serveSFTPSession leaves it: the
// SFTP bearer header, no session cookie, and the session's user and SFTP caller on the context.
func sftpStreamRequest(t *testing.T, s *SessionStore, raw string) *http.Request {
	t.Helper()
	u, sess, err := s.LookupSFTPSessionToken(context.Background(), raw)
	if err != nil {
		t.Fatalf("LookupSFTPSessionToken: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/servers/mc/files/download", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	ctx := WithSFTPCaller(WithUser(req.Context(), u), SFTPCaller{Cluster: "local", SessionID: sess.ID})
	return req.WithContext(ctx)
}

func TestRevalidateStreams_SFTPCallerWithoutCookieStreams(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	raw, _, err := s.IssueSFTPSession(ctx, sftpUserID(t, s, "alice"), "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}

	ranToEnd := false
	handler := s.RevalidateStreams(
		func(*http.Request) bool { return true },
		10*time.Millisecond,
		func(context.Context, int64) bool { return true },
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Several revalidation ticks pass while the transfer runs; it must not be cancelled.
		select {
		case <-r.Context().Done():
			w.WriteHeader(http.StatusGatewayTimeout)
			return
		case <-time.After(150 * time.Millisecond):
		}
		ranToEnd = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sftpStreamRequest(t, s, raw))
	if !ranToEnd {
		t.Fatalf("SFTP transfer was cancelled mid-stream (status %d)", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an SFTP transfer without a cookie", rec.Code)
	}
}

func TestRevalidateStreams_RevokedSFTPSessionCancelsStream(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	raw, _, err := s.IssueSFTPSession(ctx, sftpUserID(t, s, "alice"), "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}

	started := make(chan struct{})
	cancelled := make(chan struct{})
	handler := s.RevalidateStreams(
		func(*http.Request) bool { return true },
		10*time.Millisecond,
		func(context.Context, int64) bool { return true },
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))

	req := sftpStreamRequest(t, s, raw)
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("stream handler did not start")
	}
	if err := s.RevokeSFTPSessionToken(ctx, raw); err != nil {
		t.Fatalf("RevokeSFTPSessionToken: %v", err)
	}

	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("revoking the SFTP session did not cancel the stream context")
	}
	<-done
}
