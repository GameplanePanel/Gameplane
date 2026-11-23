package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// okHandler records the request it saw and answers 200.
type okHandler struct {
	called  bool
	cluster string
	user    string
	caller  SFTPCaller
	hasUser bool
	hasCall bool
}

func (h *okHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.called = true
	h.cluster = req.URL.Query().Get("cluster")
	if u := UserFromContext(req.Context()); u != nil {
		h.user, h.hasUser = u.Username, true
	}
	h.caller, h.hasCall = SFTPCallerFromContext(req.Context())
	w.WriteHeader(http.StatusOK)
}

func sessionCaller(ctx context.Context) context.Context {
	return WithSFTPCaller(ctx, SFTPCaller{Cluster: "local", SessionID: 7})
}

func TestSFTPSessionGuard_AllowedRoutes(t *testing.T) {
	s := NewSessionStore(nil)
	cases := []struct{ method, path string }{
		{http.MethodGet, "/sftp/v1/servers"},
		{http.MethodGet, "/servers/a/files/list"},
		{http.MethodGet, "/servers/a/files/stat"},
		{http.MethodGet, "/servers/a/files/download"},
		{http.MethodPost, "/servers/a/files/mkdir"},
		{http.MethodPost, "/servers/a/files/rename"},
		{http.MethodDelete, "/servers/a/files/delete"},
		{http.MethodPut, "/servers/a/files/stream"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			next := &okHandler{}
			h := s.SFTPSessionGuard(next)
			req := httptest.NewRequestWithContext(t.Context(), c.method, c.path, nil).WithContext(sessionCaller(context.Background()))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if !next.called || rec.Code != http.StatusOK {
				t.Fatalf("allowed route refused: called=%v code=%d", next.called, rec.Code)
			}
		})
	}
}

func TestSFTPSessionGuard_RefusedRoutes(t *testing.T) {
	s := NewSessionStore(nil)
	cases := []struct {
		name, method, path string
		upgrade            bool
	}{
		{"post list", http.MethodPost, "/servers/a/files/list", false},
		{"get delete", http.MethodGet, "/servers/a/files/delete", false},
		{"put mkdir", http.MethodPut, "/servers/a/files/mkdir", false},
		{"upload", http.MethodPost, "/servers/x/files/upload", false},
		{"archive get", http.MethodGet, "/servers/x/files/archive", false},
		{"archive post", http.MethodPost, "/servers/x/files/archive", false},
		{"users me", http.MethodGet, "/users/me", false},
		{"sftp login", http.MethodPost, "/sftp/v1/login", false},
		{"server action", http.MethodPost, "/servers/a:start", false},
		{"upgrade header", http.MethodGet, "/servers/a/files/list", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			next := &okHandler{}
			h := s.SFTPSessionGuard(next)
			req := httptest.NewRequestWithContext(t.Context(), c.method, c.path, nil).WithContext(sessionCaller(context.Background()))
			if c.upgrade {
				req.Header.Set("Upgrade", "websocket")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if next.called || rec.Code != http.StatusForbidden {
				t.Fatalf("refused request got through: called=%v code=%d", next.called, rec.Code)
			}
		})
	}
}

func TestSFTPSessionGuard_ForcesCluster(t *testing.T) {
	s := NewSessionStore(nil)
	for _, target := range []string{"/servers/a/files/list", "/servers/a/files/list?cluster=other"} {
		next := &okHandler{}
		h := s.SFTPSessionGuard(next)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil).WithContext(sessionCaller(context.Background()))
		h.ServeHTTP(httptest.NewRecorder(), req)
		if next.cluster != "local" {
			t.Fatalf("%s: cluster = %q, want local", target, next.cluster)
		}
	}
}

func TestSFTPSessionGuard_PassesOtherCallers(t *testing.T) {
	s := NewSessionStore(nil)
	cases := map[string]context.Context{
		"no caller":     context.Background(),
		"service token": WithSFTPCaller(context.Background(), SFTPCaller{Cluster: "local", ServiceTokenID: 3}),
	}
	for name, ctx := range cases {
		t.Run(name, func(t *testing.T) {
			next := &okHandler{}
			h := s.SFTPSessionGuard(next)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/users/me", nil).WithContext(ctx)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if !next.called || rec.Code != http.StatusOK {
				t.Fatalf("request refused: called=%v code=%d", next.called, rec.Code)
			}
		})
	}
}

func TestSFTPAuthenticate_SessionTokenAndFailures(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	alice := sftpUserID(t, s, "alice")

	revokedToken, _, err := s.IssueSFTPSession(ctx, alice, "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}
	if err := s.RevokeSFTPSessions(ctx, st.DB, alice, "password"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	token, _, err := s.IssueSFTPSession(ctx, alice, "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}

	okNext := &okHandler{}
	req := bearerRequest(t, "/servers/a/files/list", token)
	rec := httptest.NewRecorder()
	s.Authenticate(okNext).ServeHTTP(rec, req)
	if !okNext.called || okNext.user != "alice" || !okNext.hasCall || okNext.caller.Cluster != "local" || okNext.caller.SessionID == 0 {
		t.Fatalf("valid session not served as alice: %+v", okNext)
	}

	// Revoked, garbage and service-token bearers all get the same generic 401.
	var want string
	for i, bad := range []string{revokedToken, "gpsftp_garbage", "gpsftps_" + token[len(SFTPSessionTokenPrefix):]} {
		next := &okHandler{}
		rec := httptest.NewRecorder()
		s.Authenticate(next).ServeHTTP(rec, bearerRequest(t, "/servers/a/files/list", bad))
		if next.called || rec.Code != http.StatusUnauthorized {
			t.Fatalf("case %d: called=%v code=%d", i, next.called, rec.Code)
		}
		body, _ := io.ReadAll(rec.Body)
		if i == 0 {
			want = string(body)
		} else if string(body) != want {
			t.Fatalf("case %d body %q differs from %q", i, body, want)
		}
	}
}

func TestSFTPServiceAuth_OnlyServiceTokens(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	alice := sftpUserID(t, s, "alice")

	_, svcRaw, err := s.CreateSFTPServiceToken(ctx, "local")
	if err != nil {
		t.Fatalf("CreateSFTPServiceToken: %v", err)
	}
	sessRaw, _, err := s.IssueSFTPSession(ctx, alice, "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}

	next := &okHandler{}
	rec := httptest.NewRecorder()
	s.SFTPServiceAuth(next).ServeHTTP(rec, bearerRequest(t, "/sftp/v1/login", svcRaw))
	if !next.called || rec.Code != http.StatusOK || !next.hasCall || next.caller.Cluster != "local" || next.caller.ServiceTokenID == 0 {
		t.Fatalf("service token refused: called=%v code=%d caller=%+v", next.called, rec.Code, next.caller)
	}

	for name, auth := range map[string]string{"session token": sessRaw, "none": ""} {
		t.Run(name, func(t *testing.T) {
			next := &okHandler{}
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sftp/v1/login", nil)
			if auth != "" {
				req.Header.Set("Authorization", "Bearer "+auth)
			}
			s.SFTPServiceAuth(next).ServeHTTP(rec, req)
			if next.called || rec.Code != http.StatusUnauthorized {
				t.Fatalf("called=%v code=%d", next.called, rec.Code)
			}
		})
	}
}

// bearerRequest builds a cookie-less request carrying a bearer token.
func bearerRequest(t *testing.T, path, token string) *http.Request {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}
