package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// sftpAdminMissingUser is an id no test user is ever given (sftpSeq stays far below it).
const sftpAdminMissingUser int64 = 987654321

type sftpAdminKeyView struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Type        string     `json:"type"`
	Fingerprint string     `json:"fingerprint"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
}

type sftpAdminPasswordView struct {
	Length     int        `json:"length"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

type sftpAdminView struct {
	UserID   int64                  `json:"userId"`
	Username string                 `json:"username"`
	Keys     []sftpAdminKeyView     `json:"keys"`
	Password *sftpAdminPasswordView `json:"password"`
}

// sftpAdminRouter mounts the SFTP routes for a caller who holds users:manage. sftpCaller marks the
// caller as an SFTP session, which every admin route must refuse.
func sftpAdminRouter(sessions *auth.SessionStore, store *db.Store, sftpCaller bool) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithUser(req.Context(), &auth.User{ID: 1, Username: "admin", Role: "admin"})
			if sftpCaller {
				ctx = auth.WithSFTPCaller(ctx, auth.SFTPCaller{Cluster: scope.DefaultCluster, SessionID: 9})
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	mountSFTPRoutes(r, sessions, store)
	return r
}

// sftpAdminSession inserts a live SFTP session for uid under credential.
func sftpAdminSession(t *testing.T, env sftpLoginEnv, uid int64, credential string) {
	t.Helper()
	digest := fmt.Sprintf("admin-digest-%d", sftpSeq.Add(1))
	if _, err := env.sessions.InsertSFTPSession(t.Context(), uid, scope.DefaultCluster, credential, digest, 0, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("insert session %s: %v", credential, err)
	}
}

// sftpAdminLive counts uid's unrevoked sessions under credential.
func sftpAdminLive(t *testing.T, env sftpLoginEnv, uid int64, credential string) int {
	t.Helper()
	var n int
	if err := env.store.DB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM sftp_sessions WHERE user_id = ? AND credential = ? AND revoked_at IS NULL`,
		uid, credential).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestSFTPAdmin_GetListsKeysAndPasswordWithoutSecrets(t *testing.T) {
	env := newSFTPLoginEnv(t)
	name, uid := env.seedUser(t, "x")
	h := sftpAdminRouter(env.sessions, env.store, false)

	// A user with nothing registered has empty lists and no password.
	rr := sftpSelfDo(t, h, http.MethodGet, fmt.Sprintf("/%d/sftp", uid), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("empty GET: status %d body %s", rr.Code, rr.Body.String())
	}
	var empty sftpAdminView
	if err := json.Unmarshal(rr.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if empty.Keys == nil || len(empty.Keys) != 0 || empty.Password != nil {
		t.Fatalf("empty view = %+v, want no keys and no password", empty)
	}

	key, err := env.sessions.AddSFTPKey(t.Context(), uid, "laptop", sftpTestEd25519Line(t))
	if err != nil {
		t.Fatalf("add key: %v", err)
	}
	pw, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24)
	if err != nil {
		t.Fatalf("password: %v", err)
	}

	rr = sftpSelfDo(t, h, http.MethodGet, fmt.Sprintf("/%d/sftp", uid), "")
	if rr.Code != http.StatusOK {
		t.Fatalf("GET: status %d body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, secret := range []string{pw, "publicKey", "hash", "argon2"} {
		if strings.Contains(body, secret) {
			t.Errorf("admin view leaks %q: %s", secret, body)
		}
	}
	var view sftpAdminView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if view.UserID != uid || view.Username != name {
		t.Errorf("identity = %d/%q, want %d/%q", view.UserID, view.Username, uid, name)
	}
	if len(view.Keys) != 1 {
		t.Fatalf("keys = %+v, want one", view.Keys)
	}
	k := view.Keys[0]
	if k.ID != key.ID || k.Name != "laptop" || k.Type != "ssh-ed25519" || k.Fingerprint != key.Fingerprint || k.CreatedAt.IsZero() {
		t.Errorf("key view = %+v, want id %d, name laptop, type ssh-ed25519, fingerprint %s", k, key.ID, key.Fingerprint)
	}
	if view.Password == nil || view.Password.Length != 24 || view.Password.CreatedAt.IsZero() || view.Password.LastUsedAt != nil {
		t.Errorf("password view = %+v, want length 24, created, never used", view.Password)
	}
}

func TestSFTPAdmin_UnknownUserIsNotFound(t *testing.T) {
	env := newSFTPLoginEnv(t)
	h := sftpAdminRouter(env.sessions, env.store, false)
	base := fmt.Sprintf("/%d", sftpAdminMissingUser)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, base + "/sftp"},
		{http.MethodDelete, base + "/sftp/keys/5"},
		{http.MethodDelete, base + "/sftp/password"},
	} {
		rr := sftpSelfDo(t, h, rt.method, rt.path, "")
		if rr.Code != http.StatusNotFound || sftpProblemID(t, rr) != "server.common.user_not_found" {
			t.Errorf("%s %s: status %d body %s, want 404 user_not_found", rt.method, rt.path, rr.Code, rr.Body.String())
		}
	}
}

func TestSFTPAdmin_KeyAndPasswordLookupsAreScopedToTheUser(t *testing.T) {
	env := newSFTPLoginEnv(t)
	_, aliceID := env.seedUser(t, "x")
	_, bobID := env.seedUser(t, "x")
	h := sftpAdminRouter(env.sessions, env.store, false)

	bobKey, err := env.sessions.AddSFTPKey(t.Context(), bobID, "bob-laptop", sftpTestEd25519Line(t))
	if err != nil {
		t.Fatalf("add bob key: %v", err)
	}

	// Alice cannot delete Bob's key through her own id, and gets the same answer as for an unknown key.
	rr := sftpSelfDo(t, h, http.MethodDelete, fmt.Sprintf("/%d/sftp/keys/%d", aliceID, bobKey.ID), "")
	if rr.Code != http.StatusNotFound || sftpProblemID(t, rr) != "server.sftp.key_not_found" {
		t.Fatalf("cross-user key delete: status %d body %s", rr.Code, rr.Body.String())
	}
	rr = sftpSelfDo(t, h, http.MethodDelete, fmt.Sprintf("/%d/sftp/keys/%d", aliceID, 424242), "")
	if rr.Code != http.StatusNotFound || sftpProblemID(t, rr) != "server.sftp.key_not_found" {
		t.Fatalf("unknown key delete: status %d body %s", rr.Code, rr.Body.String())
	}
	keys, err := env.sessions.ListSFTPKeys(t.Context(), bobID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("bob keys after refused delete = %d (err %v), want 1", len(keys), err)
	}

	// Alice has no password to revoke.
	rr = sftpSelfDo(t, h, http.MethodDelete, fmt.Sprintf("/%d/sftp/password", aliceID), "")
	if rr.Code != http.StatusNotFound || sftpProblemID(t, rr) != "server.sftp.password_not_found" {
		t.Fatalf("missing password revoke: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPAdmin_RevokeKeyRevokesItsSessionsOnly(t *testing.T) {
	env := newSFTPLoginEnv(t)
	_, uid := env.seedUser(t, "x")
	h := sftpAdminRouter(env.sessions, env.store, false)

	key, err := env.sessions.AddSFTPKey(t.Context(), uid, "laptop", sftpTestEd25519Line(t))
	if err != nil {
		t.Fatalf("add key: %v", err)
	}
	if _, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24); err != nil {
		t.Fatalf("password: %v", err)
	}
	keyCred := fmt.Sprintf("key:%d", key.ID)
	sftpAdminSession(t, env, uid, keyCred)
	sftpAdminSession(t, env, uid, "password")

	rr := sftpSelfDo(t, h, http.MethodDelete, fmt.Sprintf("/%d/sftp/keys/%d", uid, key.ID), "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("admin key delete: status %d body %s", rr.Code, rr.Body.String())
	}
	if n := sftpAdminLive(t, env, uid, keyCred); n != 0 {
		t.Errorf("%d live key sessions after revoke, want 0", n)
	}
	if n := sftpAdminLive(t, env, uid, "password"); n != 1 {
		t.Errorf("%d live password sessions after key revoke, want 1", n)
	}
	keys, err := env.sessions.ListSFTPKeys(t.Context(), uid)
	if err != nil || len(keys) != 0 {
		t.Errorf("keys after revoke = %d (err %v), want 0", len(keys), err)
	}
}

func TestSFTPAdmin_RevokePasswordRevokesItsSessionsOnly(t *testing.T) {
	env := newSFTPLoginEnv(t)
	_, uid := env.seedUser(t, "x")
	h := sftpAdminRouter(env.sessions, env.store, false)

	key, err := env.sessions.AddSFTPKey(t.Context(), uid, "laptop", sftpTestEd25519Line(t))
	if err != nil {
		t.Fatalf("add key: %v", err)
	}
	if _, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24); err != nil {
		t.Fatalf("password: %v", err)
	}
	keyCred := fmt.Sprintf("key:%d", key.ID)
	sftpAdminSession(t, env, uid, "password")
	sftpAdminSession(t, env, uid, keyCred)

	rr := sftpSelfDo(t, h, http.MethodDelete, fmt.Sprintf("/%d/sftp/password", uid), "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("admin password revoke: status %d body %s", rr.Code, rr.Body.String())
	}
	if n := sftpAdminLive(t, env, uid, "password"); n != 0 {
		t.Errorf("%d live password sessions after revoke, want 0", n)
	}
	if n := sftpAdminLive(t, env, uid, keyCred); n != 1 {
		t.Errorf("%d live key sessions after password revoke, want 1", n)
	}

	// The admin view now shows no password, and a second revoke is a 404.
	rr = sftpSelfDo(t, h, http.MethodGet, fmt.Sprintf("/%d/sftp", uid), "")
	var view sftpAdminView
	if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil || rr.Code != http.StatusOK || view.Password != nil {
		t.Errorf("GET after revoke: status %d password %+v (err %v)", rr.Code, view.Password, err)
	}
	rr = sftpSelfDo(t, h, http.MethodDelete, fmt.Sprintf("/%d/sftp/password", uid), "")
	if rr.Code != http.StatusNotFound || sftpProblemID(t, rr) != "server.sftp.password_not_found" {
		t.Errorf("second revoke: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestSFTPAdmin_SFTPSessionCallerRefused(t *testing.T) {
	env := newSFTPLoginEnv(t)
	_, uid := env.seedUser(t, "x")
	key, err := env.sessions.AddSFTPKey(t.Context(), uid, "laptop", sftpTestEd25519Line(t))
	if err != nil {
		t.Fatalf("add key: %v", err)
	}
	if _, err := env.sessions.GenerateAndStoreSFTPPassword(t.Context(), uid, 24); err != nil {
		t.Fatalf("password: %v", err)
	}
	h := sftpAdminRouter(env.sessions, env.store, true)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, fmt.Sprintf("/%d/sftp", uid)},
		{http.MethodDelete, fmt.Sprintf("/%d/sftp/keys/%d", uid, key.ID)},
		{http.MethodDelete, fmt.Sprintf("/%d/sftp/password", uid)},
	} {
		if rr := sftpSelfDo(t, h, rt.method, rt.path, ""); rr.Code != http.StatusForbidden {
			t.Errorf("%s %s as sftp caller: status %d body %s, want 403", rt.method, rt.path, rr.Code, rr.Body.String())
		}
	}
	// Nothing was revoked by the refused calls.
	keys, err := env.sessions.ListSFTPKeys(t.Context(), uid)
	if err != nil || len(keys) != 1 {
		t.Errorf("keys after refused calls = %d (err %v), want 1", len(keys), err)
	}
}

func TestSFTPAdmin_StoreFailuresAreServerErrors(t *testing.T) {
	deadStore := newTestStore(t)
	sessions := auth.NewSessionStore(deadStore)
	if err := deadStore.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	h := sftpAdminRouter(sessions, deadStore, false)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/3/sftp"},
		{http.MethodDelete, "/3/sftp/keys/5"},
		{http.MethodDelete, "/3/sftp/password"},
	} {
		if rr := sftpSelfDo(t, h, rt.method, rt.path, ""); rr.Code < http.StatusInternalServerError {
			t.Errorf("%s %s on a dead store: status %d, want 5xx", rt.method, rt.path, rr.Code)
		}
	}
}

func TestSFTPAdmin_KeyAlgorithm(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5", "ssh-ed25519"},
		{"ecdsa-sha2-nistp256 AAAAE2VjZHNh", "ecdsa-sha2-nistp256"},
		{"", ""},
		{"   ", ""},
		{"  rsa-sha2-512   AAAAB3NzaC1yc2E  ", "rsa-sha2-512"},
	}
	for _, c := range cases {
		if got := sftpKeyAlgorithm(c.in); got != c.want {
			t.Errorf("sftpKeyAlgorithm(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
