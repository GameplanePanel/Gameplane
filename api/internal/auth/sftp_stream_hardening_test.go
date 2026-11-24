package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSFTPSessionStream(t *testing.T) {
	always := func(*http.Request) bool { return true }
	never := func(*http.Request) bool { return false }
	plain := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/servers/mc/files/download", nil)
	withCaller := func(c SFTPCaller) *http.Request {
		return plain.WithContext(WithSFTPCaller(plain.Context(), c))
	}
	for name, tc := range map[string]struct {
		req      *http.Request
		isStream func(*http.Request) bool
		want     bool
	}{
		"browser stream":          {plain, always, false},
		"sftp session stream":     {withCaller(SFTPCaller{Cluster: "local", SessionID: 7}), always, true},
		"sftp session non-stream": {withCaller(SFTPCaller{Cluster: "local", SessionID: 7}), never, false},
		"sftp service token":      {withCaller(SFTPCaller{Cluster: "local", ServiceTokenID: 3}), always, false},
	} {
		if got := SFTPSessionStream(tc.isStream)(tc.req); got != tc.want {
			t.Errorf("%s: SFTPSessionStream = %v, want %v", name, got, tc.want)
		}
	}
}

// A running SFTP transfer counts as use of its session: the stream re-check slides the session's
// expiry, so a long transfer is not cut when the original 15-minute lifetime runs out.
func TestRevalidateStreams_SFTPTransferSlidesSession(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	raw, _, err := s.IssueSFTPSession(ctx, sftpUserID(t, s, "alice"), "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}
	_, sess, err := s.LookupSFTPSessionToken(ctx, raw)
	if err != nil {
		t.Fatalf("LookupSFTPSessionToken: %v", err)
	}
	// Leave the session with 30 seconds to live and clear the per-process slide throttle.
	if err := s.SlideSFTPSession(ctx, sess.ID, time.Now().Add(30*time.Second)); err != nil {
		t.Fatalf("SlideSFTPSession: %v", err)
	}
	sftpLastSlide.Delete(sess.ID)
	t.Cleanup(func() { sftpLastSlide.Delete(sess.ID) })

	handler := s.RevalidateStreams(
		func(*http.Request) bool { return true },
		10*time.Millisecond,
		func(context.Context, int64) bool { return true },
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			w.WriteHeader(http.StatusGatewayTimeout)
		case <-time.After(100 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		}
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, sftpStreamRequest(t, s, raw))
	if rec.Code != http.StatusOK {
		t.Fatalf("transfer status = %d, want 200", rec.Code)
	}

	_, after, err := s.LookupSFTPSessionToken(ctx, raw)
	if err != nil {
		t.Fatalf("LookupSFTPSessionToken after transfer: %v", err)
	}
	if left := time.Until(after.ExpiresAt); left < 10*time.Minute {
		t.Fatalf("session has %v left after a running transfer, want it slid to about 15m", left)
	}
}
