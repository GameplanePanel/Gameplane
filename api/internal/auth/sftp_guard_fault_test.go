package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func sshMarshal(v any) []byte { return ssh.Marshal(v) }

func sftpBase64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// sftpAuditRecorder collects SFTP audit calls.
type sftpAuditRecorder struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *sftpAuditRecorder) record(_ context.Context, method, path, _, reason string, status int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, method+" "+path+" "+reason+" "+http.StatusText(status))
	return r.err
}

func (r *sftpAuditRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func sftpBadBearer(t *testing.T, path, addr string) *http.Request {
	t.Helper()
	req := bearerRequest(t, path, "gpsftp_"+strings.Repeat("z", sftpTokenBodyLen))
	req.RemoteAddr = addr
	return req
}

func TestSFTPGuard_RefusalIsAuditedAndAuditErrorsAreSwallowed(t *testing.T) {
	s, _ := sftpFault(t.Context(), t)
	rec := &sftpAuditRecorder{}
	s.AttachSFTPAudit(rec.record)

	next := &okHandler{}
	w := httptest.NewRecorder()
	s.Authenticate(next).ServeHTTP(w, sftpBadBearer(t, "/servers/a/files/list", "192.0.2.10:4000"))
	if next.called || w.Code != http.StatusUnauthorized || rec.count() != 1 {
		t.Fatalf("called=%v code=%d audit calls=%d", next.called, w.Code, rec.count())
	}

	// A failing audit writer only logs: the client still gets the generic 401.
	rec.err = context.DeadlineExceeded
	w = httptest.NewRecorder()
	s.Authenticate(next).ServeHTTP(w, sftpBadBearer(t, "/servers/a/files/list", "192.0.2.11:4000"))
	if w.Code != http.StatusUnauthorized || rec.count() != 2 {
		t.Fatalf("code=%d audit calls=%d", w.Code, rec.count())
	}
}

func TestSFTPGuard_BadTokenFloodIsRateLimited(t *testing.T) {
	s, _ := sftpFault(t.Context(), t)
	next := &okHandler{}
	limited := false
	for i := 0; i < 20 && !limited; i++ {
		w := httptest.NewRecorder()
		s.Authenticate(next).ServeHTTP(w, sftpBadBearer(t, "/servers/a/files/list", "192.0.2.20:4000"))
		if w.Code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Fatal("bad session tokens from one address were never rate limited")
	}

	limited = false
	for i := 0; i < 20 && !limited; i++ {
		req := bearerRequest(t, "/sftp/v1/login", "gpsftps_"+strings.Repeat("y", sftpTokenBodyLen))
		req.RemoteAddr = "192.0.2.21:4000"
		w := httptest.NewRecorder()
		s.SFTPServiceAuth(next).ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Fatal("bad service tokens from one address were never rate limited")
	}
	if next.called {
		t.Fatal("next handler ran for a bad token")
	}
}

func TestSFTPGuard_ValidSessionPastItsBudgetIsRateLimited(t *testing.T) {
	ctx := t.Context()
	s, uid := sftpFault(t.Context(), t)
	token, _, err := s.IssueSFTPSession(ctx, uid, "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}
	_, sess, err := s.LookupSFTPSessionToken(ctx, token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	key := fmt.Sprint(sess.ID)
	for i := 0; i < 1000; i++ {
		SFTPSessionLimiter.Allow(key)
	}
	next := &okHandler{}
	limited := false
	for i := 0; i < 50 && !limited; i++ {
		w := httptest.NewRecorder()
		s.Authenticate(next).ServeHTTP(w, bearerRequest(t, "/servers/a/files/list", token))
		limited = w.Code == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("session over its request budget was never refused")
	}
}

func TestSFTPGuard_DatabaseFailureAnswers503(t *testing.T) {
	ctx := t.Context()
	s, uid := sftpFault(t.Context(), t)
	token, _, err := s.IssueSFTPSession(ctx, uid, "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}
	// Loading the user's permissions fails after the session row matched.
	mustExecSFTP(t.Context(), t, s, `DROP TABLE user_role_bindings`)
	next := &okHandler{}
	w := httptest.NewRecorder()
	s.Authenticate(next).ServeHTTP(w, bearerRequest(t, "/servers/a/files/list", token))
	if next.called || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("session: called=%v code=%d, want 503", next.called, w.Code)
	}

	s2, _ := sftpFault(t.Context(), t, sftpCloseDB)
	w = httptest.NewRecorder()
	req := bearerRequest(t, "/sftp/v1/login", "gpsftps_"+strings.Repeat("x", sftpTokenBodyLen))
	req.RemoteAddr = "192.0.2.30:4000"
	s2.SFTPServiceAuth(next).ServeHTTP(w, req)
	if next.called || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("service: called=%v code=%d, want 503", next.called, w.Code)
	}
}

func TestActorHolder_NilReceiverIsEmpty(t *testing.T) {
	var h *ActorHolder
	if h.Source() != "" || h.Target() != "" {
		t.Fatalf("nil holder returned %q / %q", h.Source(), h.Target())
	}
}
