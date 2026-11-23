//go:build envtest

package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// sftpEnvtestServer serves the user routes (MountUsers, with the self-service and admin SFTP routes) behind the
// real rbac middleware. Every request is made as a user holding perms on the default cluster.
func sftpEnvtestServer(t *testing.T, store *db.Store, sessions *auth.SessionStore, perms ...string) *httptest.Server {
	t.Helper()
	set := map[string]struct{}{}
	for _, p := range perms {
		set[p] = struct{}{}
	}
	caller := &auth.User{ID: 1, Username: "admin", Role: "admin", Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {"*": set},
	}}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), caller)))
		})
	})
	r.Use(rbac.Middleware(nil))
	MountUsers(r, store, sessions, kube.NewRegistry(scope.DefaultCluster))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// sftpEnvtestCall sends one request to srv and returns the status code and body.
func sftpEnvtestCall(t *testing.T, srv *httptest.Server, method, path string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(b)
}

// sftpEnvtestUser inserts a user with a unique name and returns its id.
func sftpEnvtestUser(t *testing.T, store *db.Store) int64 {
	t.Helper()
	name := fmt.Sprintf("sftpenv%d", sftpSeq.Add(1))
	if _, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?, ?, ?, ?, ?)`,
		name, name, name+"@example.com", "viewer", "x"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var id int64
	if err := store.DB.QueryRowContext(t.Context(), `SELECT id FROM users WHERE username = ?`, name).Scan(&id); err != nil {
		t.Fatalf("user id: %v", err)
	}
	return id
}

// sftpEnvtestLive counts uid's unrevoked sessions under credential.
func sftpEnvtestLive(t *testing.T, store *db.Store, uid int64, credential string) int {
	t.Helper()
	var n int
	if err := store.DB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM sftp_sessions WHERE user_id = ? AND credential = ? AND revoked_at IS NULL`,
		uid, credential).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestSFTPEnvtest_AdminListAndRevokeEndLiveSessions(t *testing.T) {
	store := newTestStore(t)
	sessions := auth.NewSessionStore(store)
	srv := sftpEnvtestServer(t, store, sessions, "users:read", "users:manage")
	uid := sftpEnvtestUser(t, store)

	key, err := sessions.AddSFTPKey(t.Context(), uid, "laptop", sftpTestEd25519Line(t))
	if err != nil {
		t.Fatalf("add key: %v", err)
	}
	if _, err := sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 28); err != nil {
		t.Fatalf("password: %v", err)
	}
	keyCred := fmt.Sprintf("key:%d", key.ID)
	for _, cred := range []string{keyCred, "password"} {
		digest := fmt.Sprintf("envtest-digest-%d", sftpSeq.Add(1))
		if _, err := sessions.InsertSFTPSession(t.Context(), uid, scope.DefaultCluster, cred, digest, 0, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("insert session %s: %v", cred, err)
		}
	}

	// The admin view lists the key and the password, never the hash.
	code, body := sftpEnvtestCall(t, srv, http.MethodGet, fmt.Sprintf("/users/%d/sftp", uid))
	if code != http.StatusOK {
		t.Fatalf("admin GET: status %d body %s", code, body)
	}
	if strings.Contains(body, "hash") || strings.Contains(body, "publicKey") {
		t.Errorf("admin view leaks secret material: %s", body)
	}
	var view sftpAdminView
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(view.Keys) != 1 || view.Keys[0].Fingerprint != key.Fingerprint || view.Password == nil || view.Password.Length != 28 {
		t.Fatalf("admin view = %+v", view)
	}

	// Revoking the key ends only the key's sessions.
	if code, body := sftpEnvtestCall(t, srv, http.MethodDelete, fmt.Sprintf("/users/%d/sftp/keys/%d", uid, key.ID)); code != http.StatusNoContent {
		t.Fatalf("admin key revoke: status %d body %s", code, body)
	}
	if n := sftpEnvtestLive(t, store, uid, keyCred); n != 0 {
		t.Errorf("%d live key sessions after admin revoke, want 0", n)
	}
	if n := sftpEnvtestLive(t, store, uid, "password"); n != 1 {
		t.Errorf("%d live password sessions after key revoke, want 1", n)
	}

	// Revoking the password ends its sessions and the next admin view has no password.
	if code, body := sftpEnvtestCall(t, srv, http.MethodDelete, fmt.Sprintf("/users/%d/sftp/password", uid)); code != http.StatusNoContent {
		t.Fatalf("admin password revoke: status %d body %s", code, body)
	}
	if n := sftpEnvtestLive(t, store, uid, "password"); n != 0 {
		t.Errorf("%d live password sessions after admin revoke, want 0", n)
	}
	code, body = sftpEnvtestCall(t, srv, http.MethodGet, fmt.Sprintf("/users/%d/sftp", uid))
	if code != http.StatusOK || !strings.Contains(body, `"password":null`) {
		t.Errorf("admin GET after revokes: status %d body %s", code, body)
	}
}

func TestSFTPEnvtest_AdminRoutesNeedUsersManage(t *testing.T) {
	store := newTestStore(t)
	sessions := auth.NewSessionStore(store)
	uid := sftpEnvtestUser(t, store)
	key, err := sessions.AddSFTPKey(t.Context(), uid, "laptop", sftpTestEd25519Line(t))
	if err != nil {
		t.Fatalf("add key: %v", err)
	}

	reader := sftpEnvtestServer(t, store, sessions, "users:read")
	if code, _ := sftpEnvtestCall(t, reader, http.MethodGet, fmt.Sprintf("/users/%d/sftp", uid)); code != http.StatusForbidden {
		t.Errorf("users:read GET admin view: status %d, want 403", code)
	}
	if code, _ := sftpEnvtestCall(t, reader, http.MethodDelete, fmt.Sprintf("/users/%d/sftp/keys/%d", uid, key.ID)); code != http.StatusForbidden {
		t.Errorf("users:read revokes a key: status %d, want 403", code)
	}
	keys, err := sessions.ListSFTPKeys(t.Context(), uid)
	if err != nil || len(keys) != 1 {
		t.Fatalf("key removed by a refused request: %d keys (err %v)", len(keys), err)
	}

	manager := sftpEnvtestServer(t, store, sessions, "users:read", "users:manage")
	if code, _ := sftpEnvtestCall(t, manager, http.MethodGet, fmt.Sprintf("/users/%d/sftp", uid)); code != http.StatusOK {
		t.Errorf("users:manage GET admin view: status %d, want 200", code)
	}
	if code, _ := sftpEnvtestCall(t, manager, http.MethodDelete, fmt.Sprintf("/users/%d/sftp/keys/%d", uid, key.ID)); code != http.StatusNoContent {
		t.Errorf("users:manage revokes a key: status %d, want 204", code)
	}
}

func TestSFTPEnvtest_AdminUnknownUserAndKey(t *testing.T) {
	store := newTestStore(t)
	sessions := auth.NewSessionStore(store)
	srv := sftpEnvtestServer(t, store, sessions, "users:read", "users:manage")
	uid := sftpEnvtestUser(t, store)

	if code, _ := sftpEnvtestCall(t, srv, http.MethodGet, fmt.Sprintf("/users/%d/sftp", sftpAdminMissingUser)); code != http.StatusNotFound {
		t.Errorf("unknown user GET: status %d, want 404", code)
	}
	if code, _ := sftpEnvtestCall(t, srv, http.MethodDelete, fmt.Sprintf("/users/%d/sftp/password", uid)); code != http.StatusNotFound {
		t.Errorf("password revoke without a password: status %d, want 404", code)
	}
	if code, _ := sftpEnvtestCall(t, srv, http.MethodDelete, fmt.Sprintf("/users/%d/sftp/keys/99", uid)); code != http.StatusNotFound {
		t.Errorf("unknown key revoke: status %d, want 404", code)
	}
}

// sftpEnvtestSelfServer serves the caller's own SFTP routes (/users/me/sftp*) as user uid. ctxFn, when set,
// marks every request as an MCP-token or SFTP-session caller before the routes run.
func sftpEnvtestSelfServer(t *testing.T, store *db.Store, sessions *auth.SessionStore, uid int64, ctxFn func(context.Context) context.Context) *httptest.Server {
	t.Helper()
	caller := &auth.User{ID: uid, Username: "self", Role: "viewer", Perms: map[string]map[string]map[string]struct{}{}}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithUser(req.Context(), caller)
			if ctxFn != nil {
				ctx = ctxFn(ctx)
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(rbac.Middleware(nil))
	MountUsers(r, store, sessions, kube.NewRegistry(scope.DefaultCluster))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// sftpEnvtestSend sends one request with a JSON body (or none when body is "") and returns the status and body.
func sftpEnvtestSend(t *testing.T, srv *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(b)
}

// sftpEnvtestEpoch returns users.session_epoch for uid.
func sftpEnvtestEpoch(t *testing.T, store *db.Store, uid int64) int64 {
	t.Helper()
	var ep int64
	if err := store.DB.QueryRowContext(t.Context(), `SELECT session_epoch FROM users WHERE id = ?`, uid).Scan(&ep); err != nil {
		t.Fatalf("session epoch: %v", err)
	}
	return ep
}

func TestSFTPEnvtest_UserAddsAndRemovesOwnKeys(t *testing.T) {
	store := newTestStore(t)
	sessions := auth.NewSessionStore(store)
	uid := sftpEnvtestUser(t, store)
	srv := sftpEnvtestSelfServer(t, store, sessions, uid, nil)

	body, err := json.Marshal(map[string]string{"name": "laptop", "publicKey": sftpTestEd25519Line(t)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	code, resp := sftpEnvtestSend(t, srv, http.MethodPost, "/users/me/sftp/keys", string(body))
	if code != http.StatusCreated {
		t.Fatalf("add key: status %d body %s", code, resp)
	}
	var key auth.SFTPKey
	if err := json.Unmarshal([]byte(resp), &key); err != nil || key.ID == 0 || key.Fingerprint == "" {
		t.Fatalf("added key = %+v, %v", key, err)
	}
	keys, err := sessions.ListSFTPKeys(t.Context(), uid)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys after add = %d, %v; want 1", len(keys), err)
	}

	if code, body := sftpEnvtestSend(t, srv, http.MethodDelete, fmt.Sprintf("/users/me/sftp/keys/%d", key.ID), ""); code != http.StatusNoContent {
		t.Fatalf("remove key: status %d body %s", code, body)
	}
	keys, err = sessions.ListSFTPKeys(t.Context(), uid)
	if err != nil || len(keys) != 0 {
		t.Fatalf("keys after remove = %d, %v; want 0", len(keys), err)
	}
}

func TestSFTPEnvtest_DuplicateKeyAcrossUsersRefused(t *testing.T) {
	store := newTestStore(t)
	sessions := auth.NewSessionStore(store)
	first := sftpEnvtestUser(t, store)
	second := sftpEnvtestUser(t, store)
	firstName, err := sessions.SFTPUsername(t.Context(), first)
	if err != nil {
		t.Fatalf("username: %v", err)
	}
	line := sftpTestEd25519Line(t)
	body, err := json.Marshal(map[string]string{"name": "shared", "publicKey": line})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if code, resp := sftpEnvtestSend(t, sftpEnvtestSelfServer(t, store, sessions, first, nil), http.MethodPost, "/users/me/sftp/keys", string(body)); code != http.StatusCreated {
		t.Fatalf("first user add: status %d body %s", code, resp)
	}
	code, resp := sftpEnvtestSend(t, sftpEnvtestSelfServer(t, store, sessions, second, nil), http.MethodPost, "/users/me/sftp/keys", string(body))
	if code != http.StatusConflict {
		t.Fatalf("second user add of the same key: status %d body %s, want 409", code, resp)
	}
	if strings.Contains(resp, firstName) {
		t.Errorf("duplicate refusal names the other user: %s", resp)
	}
	if keys, err := sessions.ListSFTPKeys(t.Context(), second); err != nil || len(keys) != 0 {
		t.Errorf("second user keys = %d, %v; want none", len(keys), err)
	}
}

func TestSFTPEnvtest_PasswordShownOnceAndRegenerateInvalidatesOld(t *testing.T) {
	store := newTestStore(t)
	sessions := auth.NewSessionStore(store)
	uid := sftpEnvtestUser(t, store)
	srv := sftpEnvtestSelfServer(t, store, sessions, uid, nil)
	username, err := sessions.SFTPUsername(t.Context(), uid)
	if err != nil {
		t.Fatalf("username: %v", err)
	}

	code, resp := sftpEnvtestSend(t, srv, http.MethodPost, "/users/me/sftp/password", `{"length":28}`)
	if code != http.StatusOK {
		t.Fatalf("generate: status %d body %s", code, resp)
	}
	var first struct {
		Password string `json:"password"`
		Length   int    `json:"length"`
	}
	if err := json.Unmarshal([]byte(resp), &first); err != nil || first.Password == "" || first.Length != 28 {
		t.Fatalf("generate response = %+v, %v", first, err)
	}

	// The clear text is returned once: the profile read carries the password's metadata, never the secret.
	code, profile := sftpEnvtestCall(t, srv, http.MethodGet, "/users/me/sftp")
	if code != http.StatusOK || strings.Contains(profile, first.Password) {
		t.Fatalf("profile read after generate: status %d, leaks password: %v", code, strings.Contains(profile, first.Password))
	}
	if !strings.Contains(profile, `"length":28`) {
		t.Errorf("profile read lacks password metadata: %s", profile)
	}
	if _, _, ok, err := sessions.VerifySFTPPassword(t.Context(), username, first.Password); err != nil || !ok {
		t.Fatalf("first password does not verify: ok=%v err=%v", ok, err)
	}

	// Regenerating replaces the password: the old one stops verifying and the new one does.
	code, resp = sftpEnvtestSend(t, srv, http.MethodPost, "/users/me/sftp/password", `{"length":24}`)
	if code != http.StatusOK {
		t.Fatalf("regenerate: status %d body %s", code, resp)
	}
	var second struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal([]byte(resp), &second); err != nil || second.Password == "" || second.Password == first.Password {
		t.Fatalf("regenerate response = %+v, %v", second, err)
	}
	if _, _, ok, _ := sessions.VerifySFTPPassword(t.Context(), username, first.Password); ok {
		t.Error("old password still verifies after regenerate")
	}
	if _, _, ok, err := sessions.VerifySFTPPassword(t.Context(), username, second.Password); err != nil || !ok {
		t.Errorf("new password does not verify: ok=%v err=%v", ok, err)
	}
}

func TestSFTPEnvtest_PasswordResetAndUserDeleteRevokeSessions(t *testing.T) {
	store := newTestStore(t)
	sessions := auth.NewSessionStore(store)
	srv := sftpEnvtestServer(t, store, sessions, "users:read", "users:manage")

	// Password reset: an SFTP session opened before it stops resolving, because the reset bumps session_epoch.
	uid := sftpEnvtestUser(t, store)
	raw, _, err := sessions.IssueSFTPSession(t.Context(), uid, scope.DefaultCluster, "password", sftpEnvtestEpoch(t, store, uid))
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	if _, _, err := sessions.LookupSFTPSessionToken(t.Context(), raw); err != nil {
		t.Fatalf("session before reset does not resolve: %v", err)
	}
	if code, body := sftpEnvtestSend(t, srv, http.MethodPost, fmt.Sprintf("/users/%d/reset-password", uid), `{"password":"reset-pass-123456"}`); code != http.StatusNoContent {
		t.Fatalf("reset password: status %d body %s", code, body)
	}
	if _, _, err := sessions.LookupSFTPSessionToken(t.Context(), raw); err == nil {
		t.Error("SFTP session still resolves after the password reset")
	}

	// User removal (the account is the only "disable" the user table has): every SFTP session row goes.
	gone := sftpEnvtestUser(t, store)
	rawGone, _, err := sessions.IssueSFTPSession(t.Context(), gone, scope.DefaultCluster, "password", sftpEnvtestEpoch(t, store, gone))
	if err != nil {
		t.Fatalf("issue session for removal: %v", err)
	}
	if n := sftpEnvtestLive(t, store, gone, "password"); n != 1 {
		t.Fatalf("%d live sessions before removal, want 1", n)
	}
	if code, body := sftpEnvtestSend(t, srv, http.MethodDelete, fmt.Sprintf("/users/%d", gone), ""); code != http.StatusNoContent {
		t.Fatalf("delete user: status %d body %s", code, body)
	}
	if n := sftpEnvtestLive(t, store, gone, "password"); n != 0 {
		t.Errorf("%d live sessions after user removal, want 0", n)
	}
	if _, _, err := sessions.LookupSFTPSessionToken(t.Context(), rawGone); err == nil {
		t.Error("SFTP session still resolves after user removal")
	}
}

func TestSFTPEnvtest_SessionAndMCPTokenCallersRefused(t *testing.T) {
	callers := map[string]func(context.Context) context.Context{
		"mcp token": func(ctx context.Context) context.Context { return auth.WithMCPToken(ctx, 7) },
		"sftp session": func(ctx context.Context) context.Context {
			return auth.WithSFTPCaller(ctx, auth.SFTPCaller{Cluster: scope.DefaultCluster, SessionID: 9})
		},
	}
	for name, withCaller := range callers {
		t.Run(name, func(t *testing.T) {
			store := newTestStore(t)
			sessions := auth.NewSessionStore(store)
			uid := sftpEnvtestUser(t, store)
			srv := sftpEnvtestSelfServer(t, store, sessions, uid, withCaller)

			if code, body := sftpEnvtestSend(t, srv, http.MethodGet, "/users/me/sftp", ""); code != http.StatusForbidden {
				t.Errorf("profile read: status %d body %s, want 403", code, body)
			}
			if code, body := sftpEnvtestSend(t, srv, http.MethodPost, "/users/me/sftp/keys", fmt.Sprintf(`{"name":"x","publicKey":%q}`, sftpTestEd25519Line(t))); code != http.StatusForbidden {
				t.Errorf("add key: status %d body %s, want 403", code, body)
			}
			if code, body := sftpEnvtestSend(t, srv, http.MethodPost, "/users/me/sftp/password", `{"length":24}`); code != http.StatusForbidden {
				t.Errorf("generate password: status %d body %s, want 403", code, body)
			}
			if keys, err := sessions.ListSFTPKeys(t.Context(), uid); err != nil || len(keys) != 0 {
				t.Errorf("refused key add left %d keys (err %v)", len(keys), err)
			}
			if _, found, err := sessions.GetSFTPPasswordInfo(t.Context(), uid); err != nil || found {
				t.Errorf("refused password generate left a password (found %v, err %v)", found, err)
			}
		})
	}
}
