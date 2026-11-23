//go:build envtest

package handlers

import (
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
