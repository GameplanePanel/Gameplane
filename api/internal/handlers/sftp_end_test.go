package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// sftpEndRevokedAt returns the revoked_at column of the session behind raw.
func sftpEndRevokedAt(t *testing.T, env sftpLoginEnv, raw string) sql.NullString {
	t.Helper()
	var v sql.NullString
	if err := env.store.DB.QueryRowContext(t.Context(),
		`SELECT revoked_at FROM sftp_sessions WHERE digest = ?`, auth.SFTPTokenDigest(raw)).Scan(&v); err != nil {
		t.Fatalf("read session: %v", err)
	}
	return v
}

func sftpEndBody(t *testing.T, tokens []string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"sessionTokens": tokens})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

func TestSFTPEndUnit_RevokesOwnClusterAndIgnoresForeign(t *testing.T) {
	env := newSFTPLoginEnv(t)
	_, uid := env.seedUser(t, "x")
	own, _, err := env.sessions.IssueSFTPSession(t.Context(), uid, scope.DefaultCluster, "password", 0)
	if err != nil {
		t.Fatalf("issue own: %v", err)
	}
	foreign, _, err := env.sessions.IssueSFTPSession(t.Context(), uid, "other-cluster", "password", 0)
	if err != nil {
		t.Fatalf("issue foreign: %v", err)
	}
	h := sftpLoginRouter(env)
	body := sftpEndBody(t, []string{own, foreign, "plain-token", auth.SFTPSessionTokenPrefix + "unknown", own})
	rr := sftpUnitRawPost(t, env, h, "/sftp/v1/sessions/end", body)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if !sftpEndRevokedAt(t, env, own).Valid {
		t.Fatal("own-cluster session was not revoked")
	}
	if sftpEndRevokedAt(t, env, foreign).Valid {
		t.Fatal("foreign-cluster session must stay live")
	}
}

func TestSFTPEndUnit_EmptyListIsNoContent(t *testing.T) {
	env := newSFTPLoginEnv(t)
	rr := sftpUnitRawPost(t, env, sftpLoginRouter(env), "/sftp/v1/sessions/end", `{"sessionTokens":[]}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPEndUnit_InvalidJSON(t *testing.T) {
	env := newSFTPLoginEnv(t)
	rr := sftpUnitRawPost(t, env, sftpLoginRouter(env), "/sftp/v1/sessions/end", "{not json")
	if rr.Code != http.StatusBadRequest || sftpProblemID(t, rr) != "server.common.invalid_json" {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPEndUnit_RejectsMissingOrBadServiceToken(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpLoginRouter(env)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/sftp/v1/sessions/end", strings.NewReader(`{"sessionTokens":[]}`))
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status %d body %s", rr.Code, rr.Body.String())
	}

	// Called without the service-auth middleware there is no caller in the context.
	lh := &sftpLoginHandler{sessions: env.sessions, store: env.store, files: env.files, aud: env.aud}
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", strings.NewReader("{}"))
	req.Header.Set("Accept", "application/problem+json")
	rr = httptest.NewRecorder()
	lh.end(rr, req)
	if rr.Code != http.StatusUnauthorized || sftpProblemID(t, rr) != "server.common.unauthenticated" {
		t.Fatalf("no caller: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPEndUnit_OversizeList(t *testing.T) {
	env := newSFTPLoginEnv(t)
	tokens := make([]string, sftpCheckMaxTokens+1)
	for i := range tokens {
		tokens[i] = auth.SFTPSessionTokenPrefix + "x"
	}
	rr := sftpUnitRawPost(t, env, sftpLoginRouter(env), "/sftp/v1/sessions/end", sftpEndBody(t, tokens))
	if rr.Code != http.StatusBadRequest || sftpProblemID(t, rr) != "server.bad_request" {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPEndUnit_StoreFailure(t *testing.T) {
	env := newSFTPLoginEnv(t)
	deadStore := newTestStore(t)
	deadSessions := auth.NewSessionStore(deadStore)
	if err := deadStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	h := &sftpLoginHandler{sessions: deadSessions, store: deadStore, files: env.files, aud: env.aud}
	req := httptest.NewRequestWithContext(auth.WithSFTPCaller(t.Context(), auth.SFTPCaller{Cluster: scope.DefaultCluster}),
		http.MethodPost, "/sftp/v1/sessions/end", strings.NewReader(sftpEndBody(t, []string{auth.SFTPSessionTokenPrefix + "abc"})))
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.end(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPEndUnit_StoreRejectsOversizeDirectly(t *testing.T) {
	env := newSFTPLoginEnv(t)
	digests := make([]string, auth.SFTPMaxEndDigests+1)
	if err := env.sessions.EndSFTPSessions(t.Context(), scope.DefaultCluster, digests); err == nil {
		t.Fatal("expected an error for an oversize digest list")
	}
}
