package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// testClusterLister is a minimal ClusterLister for tests.
type testClusterLister struct{}

func (testClusterLister) IDs() []string {
	return []string{scope.DefaultCluster}
}

// newUsersServer wires MountUsers behind a test middleware that injects
// `caller` as the authenticated user. Mirrors what sessions.Authenticate
// does in production, without paying for the cookie round-trip.
func newUsersServer(t *testing.T, caller *auth.User) (*httptest.Server, *db.Store, *auth.SessionStore) {
	t.Helper()
	store := newTestStore(t)
	sessions := auth.NewSessionStore(store)
	r := chi.NewRouter()
	if caller != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), caller)))
			})
		})
	}
	MountUsers(r, store, sessions, testClusterLister{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, store, sessions
}

func seedUser(t *testing.T, store *db.Store, username, role, password string) int64 {
	t.Helper()
	auth.SetFastHashParams(t)
	var hash any
	if password != "" {
		h, err := auth.HashPassword(t.Context(), password)
		if err != nil {
			t.Fatalf("hash: %v", err)
		}
		hash = h
	}
	res, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?,?,?,?,?)`,
		username, username, username+"@example.com", role, hash)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// allowTeamA permits the "team-a" namespace for the duration of a test,
// restoring the default allow-list afterwards. Cluster-dimension binding
// tests need it because they grant roles in "team-a".
func allowTeamA(t *testing.T) {
	t.Helper()
	saved := scope.AllowedNamespaces
	t.Cleanup(func() { scope.AllowedNamespaces = saved })
	scope.AllowedNamespaces = []string{scope.DefaultNamespace, "team-a"}
}

func sessionCount(t *testing.T, store *db.Store, userID int64) int {
	t.Helper()
	var n int
	if err := store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("session count: %v", err)
	}
	return n
}

func TestUsers_CreateValidatesPassword(t *testing.T) {
	srv, _, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	status, body := doReq(t, "POST", srv.URL+"/users", map[string]any{
		"username": "alice",
		"password": "short",
		"role":     "operator",
	})
	if status != 400 {
		t.Fatalf("want 400, got %d body=%s", status, body)
	}
}

func TestUsers_CreatePersistsAndLists(t *testing.T) {
	srv, _, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	in := map[string]any{
		"username":    "alice",
		"displayName": "Alice",
		"email":       "alice@example.com",
		"password":    "longenoughpw1",
		"role":        "operator",
	}
	status, body := doReq(t, "POST", srv.URL+"/users", in)
	if status != 200 {
		t.Fatalf("create want 200 got %d body=%s", status, body)
	}
	var created userDTO
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Provider != "local" {
		t.Fatalf("provider = %q want local", created.Provider)
	}

	status, body = doReq(t, "GET", srv.URL+"/users", nil)
	if status != 200 {
		t.Fatalf("list want 200 got %d", status)
	}
	var listed []userDTO
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 || listed[0].Username != "alice" {
		t.Fatalf("unexpected list: %+v", listed)
	}
}

// POST /users must return createdAt as RFC 3339 like GET and PATCH do,
// not the empty string the DTO zero value produced before.
func TestUsers_CreateReturnsRFC3339CreatedAt(t *testing.T) {
	srv, _, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	status, body := doReq(t, "POST", srv.URL+"/users", map[string]any{
		"username": "carol",
		"password": "longenoughpw1",
		"role":     "viewer",
	})
	if status != 200 {
		t.Fatalf("create want 200 got %d body=%s", status, body)
	}
	var created userDTO
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, created.CreatedAt); err != nil {
		t.Fatalf("createdAt %q is not RFC3339: %v", created.CreatedAt, err)
	}

	// The create response must agree with what the list reports.
	status, body = doReq(t, "GET", srv.URL+"/users", nil)
	if status != 200 {
		t.Fatalf("list want 200 got %d", status)
	}
	var listed []userDTO
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 || listed[0].CreatedAt != created.CreatedAt {
		t.Fatalf("list createdAt = %+v, want %q", listed, created.CreatedAt)
	}
}

// Creating a user mirrors their primary role into a cluster-wide ("*")
// role binding — without it the user would resolve to no permissions.
func TestUsers_CreateBindsClusterRole(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	status, body := doReq(t, "POST", srv.URL+"/users", map[string]any{
		"username": "ivy",
		"password": "longenoughpw1",
		"role":     "operator",
	})
	if status != 200 {
		t.Fatalf("create want 200 got %d body=%s", status, body)
	}
	var created userDTO
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var role string
	err := store.DB.QueryRowContext(t.Context(),
		`SELECT role_name FROM user_role_bindings WHERE user_id = ? AND cluster = ? AND namespace = '*'`,
		created.ID, scope.DefaultCluster).Scan(&role)
	if err != nil || role != "operator" {
		t.Fatalf("cluster binding = %q err=%v, want operator", role, err)
	}
}

// A custom role can be assigned to a user once it exists in the roles table.
func TestUsers_CreateAcceptsCustomRole(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	if _, err := store.DB.ExecContext(t.Context(), `INSERT INTO roles(name, builtin) VALUES ('support', 0)`); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	status, _ := doReq(t, "POST", srv.URL+"/users", map[string]any{
		"username": "jo",
		"password": "longenoughpw1",
		"role":     "support",
	})
	if status != 200 {
		t.Fatalf("create with custom role want 200 got %d", status)
	}
}

// Deleting the only user who can manage users is refused.
func TestUsers_DeleteLastManagerRejected(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 999, Role: "admin"})
	id := seedUser(t, store, "onlyadmin", "admin", "longenoughpw1")
	status, body := doReq(t, "DELETE", srv.URL+"/users/"+strconv.FormatInt(id, 10), nil)
	if status != 400 {
		t.Fatalf("want 400 deleting last manager got %d body=%s", status, body)
	}
}

// Per-namespace role bindings: add, list, reject bad namespace, delete.
func TestUsers_RoleBindings(t *testing.T) {
	saved := scope.AllowedNamespaces
	t.Cleanup(func() { scope.AllowedNamespaces = saved })
	scope.AllowedNamespaces = []string{scope.DefaultNamespace, "team-a"}

	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	id := seedUser(t, store, "ned", "viewer", "longenoughpw1")
	base := srv.URL + "/users/" + strconv.FormatInt(id, 10) + "/bindings"

	// Cluster-wide ("*") is reserved for the primary role.
	if status, _ := doReq(t, "POST", base, map[string]any{"roleName": "operator", "namespace": "*"}); status != 400 {
		t.Fatalf("'*' namespace want 400 got %d", status)
	}
	// Unknown namespace rejected.
	if status, _ := doReq(t, "POST", base, map[string]any{"roleName": "operator", "namespace": "nope"}); status != 400 {
		t.Fatalf("unknown ns want 400 got %d", status)
	}
	// Unknown role rejected.
	if status, _ := doReq(t, "POST", base, map[string]any{"roleName": "ghost", "namespace": "team-a"}); status != 400 {
		t.Fatalf("unknown role want 400 got %d", status)
	}
	// Valid grant.
	if status, body := doReq(t, "POST", base, map[string]any{"roleName": "operator", "namespace": "team-a"}); status != 201 {
		t.Fatalf("add binding want 201 got %d body=%s", status, body)
	}
	// Duplicate rejected.
	if status, _ := doReq(t, "POST", base, map[string]any{"roleName": "operator", "namespace": "team-a"}); status != 409 {
		t.Fatalf("duplicate binding want 409 got %d", status)
	}
	// List shows it.
	status, body := doReq(t, "GET", base, nil)
	if status != 200 {
		t.Fatalf("list want 200 got %d", status)
	}
	var bindings []bindingDTO
	if err := json.Unmarshal(body, &bindings); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(bindings) != 1 || bindings[0].RoleName != "operator" || bindings[0].Namespace != "team-a" {
		t.Fatalf("unexpected bindings: %+v", bindings)
	}
	// Delete it.
	if status, _ := doReq(t, "DELETE", base+"/operator/team-a", nil); status != 204 {
		t.Fatalf("delete binding want 204 got %d", status)
	}
	if status, _ := doReq(t, "DELETE", base+"/operator/team-a", nil); status != 404 {
		t.Fatalf("delete missing binding want 404 got %d", status)
	}
}

func TestUsers_PatchEditsAllFields(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 999, Role: "admin"})
	id := seedUser(t, store, "bob", "viewer", "longenoughpw1")

	status, body := doReq(t, "PATCH", srv.URL+"/users/"+strconv.FormatInt(id, 10), map[string]any{
		"displayName": "Robert",
		"email":       "robert@example.com",
		"role":        "operator",
	})
	if status != 200 {
		t.Fatalf("patch want 200 got %d body=%s", status, body)
	}
	var got userDTO
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.DisplayName != "Robert" || got.Email != "robert@example.com" || got.Role != "operator" {
		t.Fatalf("update did not stick: %+v", got)
	}
}

func TestUsers_PatchRejectsInvalidRole(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 999, Role: "admin"})
	id := seedUser(t, store, "carol", "viewer", "longenoughpw1")
	status, _ := doReq(t, "PATCH", srv.URL+"/users/"+strconv.FormatInt(id, 10), map[string]any{
		"role": "superadmin",
	})
	if status != 400 {
		t.Fatalf("want 400 got %d", status)
	}
}

// PATCH /users/{id} where {id} is the caller's own id and the role
// drops below admin must 400 — otherwise a slip locks the cluster out
// of admin operations.
func TestUsers_PatchSelfDemoteRejected(t *testing.T) {
	store := newTestStore(t)
	id := seedUser(t, store, "evan", "admin", "longenoughpw1")
	sessions := auth.NewSessionStore(store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: id, Role: "admin"})))
		})
	})
	MountUsers(r, store, sessions, testClusterLister{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	status, body := doReq(t, "PATCH", srv.URL+"/users/"+strconv.FormatInt(id, 10), map[string]any{
		"role": "viewer",
	})
	if status != 400 {
		t.Fatalf("want 400 self-demote got %d body=%s", status, body)
	}
}

// Role change on someone else clears that user's active sessions so the
// new trust boundary takes effect immediately.
func TestUsers_PatchRoleInvalidatesSessions(t *testing.T) {
	store := newTestStore(t)
	target := seedUser(t, store, "trish", "operator", "longenoughpw1")
	sessions := auth.NewSessionStore(store)
	if _, _, err := sessions.Create(context.Background(), target); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if got := sessionCount(t, store, target); got != 1 {
		t.Fatalf("seed sanity: session count = %d, want 1", got)
	}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 999, Role: "admin"})))
		})
	})
	MountUsers(r, store, sessions, testClusterLister{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	status, _ := doReq(t, "PATCH", srv.URL+"/users/"+strconv.FormatInt(target, 10), map[string]any{
		"role": "viewer",
	})
	if status != 200 {
		t.Fatalf("want 200 got %d", status)
	}
	if got := sessionCount(t, store, target); got != 0 {
		t.Fatalf("expected sessions cleared, got %d", got)
	}
}

func TestUsers_DeleteSelfRejected(t *testing.T) {
	store := newTestStore(t)
	id := seedUser(t, store, "frank", "admin", "longenoughpw1")
	sessions := auth.NewSessionStore(store)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: id, Role: "admin"})))
		})
	})
	MountUsers(r, store, sessions, testClusterLister{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	status, _ := doReq(t, "DELETE", srv.URL+"/users/"+strconv.FormatInt(id, 10), nil)
	if status != 400 {
		t.Fatalf("want 400 got %d", status)
	}
}

func TestUsers_ResetPasswordRejectsShort(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	id := seedUser(t, store, "gina", "viewer", "")
	status, _ := doReq(t, "POST", srv.URL+"/users/"+strconv.FormatInt(id, 10)+"/reset-password",
		map[string]any{"password": "short"})
	if status != 400 {
		t.Fatalf("want 400 got %d", status)
	}
}

// Reset writes a hash that verifies the new password and not the old,
// and clears every session for the target user.
func TestUsers_ResetPasswordHashesAndInvalidatesSessions(t *testing.T) {
	store := newTestStore(t)
	id := seedUser(t, store, "hank", "viewer", "originalpassword")
	sessions := auth.NewSessionStore(store)

	if _, _, err := sessions.Create(context.Background(), id); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if got := sessionCount(t, store, id); got != 1 {
		t.Fatalf("seed sanity: session count = %d, want 1", got)
	}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 999, Role: "admin"})))
		})
	})
	MountUsers(r, store, sessions, testClusterLister{})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	status, _ := doReq(t, "POST", srv.URL+"/users/"+strconv.FormatInt(id, 10)+"/reset-password",
		map[string]any{"password": "brand-new-password-1"})
	if status != 204 {
		t.Fatalf("want 204 got %d", status)
	}

	var newHash string
	if err := store.DB.QueryRowContext(t.Context(), `SELECT pw_hash FROM users WHERE id=?`, id).Scan(&newHash); err != nil {
		t.Fatalf("read hash: %v", err)
	}
	if ok, _ := auth.VerifyPassword(t.Context(), "brand-new-password-1", newHash); !ok {
		t.Fatalf("new password does not verify against stored hash")
	}
	if ok, _ := auth.VerifyPassword(t.Context(), "originalpassword", newHash); ok {
		t.Fatalf("old password unexpectedly still verifies")
	}
	if got := sessionCount(t, store, id); got != 0 {
		t.Fatalf("expected sessions cleared, got %d", got)
	}
}

// Cluster dimension tests

func TestAddBinding_RejectWildcardCluster(t *testing.T) {
	allowTeamA(t)
	srv, _, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	alice := seedUser(t, newTestStore(t), "alice", "viewer", "pw")
	status, _ := doReq(t, "POST", srv.URL+"/users/"+strconv.FormatInt(alice, 10)+"/bindings",
		map[string]any{
			"roleName":  "operator",
			"namespace": "team-a",
			"cluster":   "*",
		})
	if status != 400 {
		t.Fatalf("want 400 for wildcard cluster, got %d", status)
	}
}

func TestAddBinding_RejectUnknownCluster(t *testing.T) {
	allowTeamA(t)
	srv, _, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	alice := seedUser(t, newTestStore(t), "alice", "viewer", "pw")
	status, _ := doReq(t, "POST", srv.URL+"/users/"+strconv.FormatInt(alice, 10)+"/bindings",
		map[string]any{
			"roleName":  "operator",
			"namespace": "team-a",
			"cluster":   "unknown-cluster",
		})
	if status != 400 {
		t.Fatalf("want 400 for unknown cluster, got %d", status)
	}
}

func TestAddBinding_DefaultsAndReturnsCluster(t *testing.T) {
	allowTeamA(t)
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	alice := seedUser(t, store, "alice", "viewer", "pw")
	status, body := doReq(t, "POST", srv.URL+"/users/"+strconv.FormatInt(alice, 10)+"/bindings",
		map[string]any{
			"roleName":  "operator",
			"namespace": "team-a",
			// Omit cluster; should default to "local"
		})
	if status != 201 {
		t.Fatalf("want 201, got %d body=%s", status, body)
	}
	var binding bindingDTO
	if err := json.Unmarshal(body, &binding); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if binding.Cluster != scope.DefaultCluster {
		t.Fatalf("cluster = %q, want %q", binding.Cluster, scope.DefaultCluster)
	}
	// Verify it was persisted.
	var role string
	err := store.DB.QueryRowContext(t.Context(),
		`SELECT role_name FROM user_role_bindings WHERE user_id = ? AND cluster = ? AND namespace = ?`,
		alice, scope.DefaultCluster, "team-a").Scan(&role)
	if err != nil || role != "operator" {
		t.Fatalf("persisted binding: role=%q err=%v", role, err)
	}
}

func TestAddBinding_ExplicitCluster(t *testing.T) {
	allowTeamA(t)
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	alice := seedUser(t, store, "alice", "viewer", "pw")
	status, body := doReq(t, "POST", srv.URL+"/users/"+strconv.FormatInt(alice, 10)+"/bindings",
		map[string]any{
			"roleName":  "operator",
			"namespace": "team-a",
			"cluster":   "local",
		})
	if status != 201 {
		t.Fatalf("want 201, got %d", status)
	}
	var binding bindingDTO
	if err := json.Unmarshal(body, &binding); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if binding.Cluster != "local" {
		t.Fatalf("cluster = %q, want local", binding.Cluster)
	}
}

func TestListBindings_IncludesCluster(t *testing.T) {
	allowTeamA(t)
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	alice := seedUser(t, store, "alice", "operator", "pw")
	// Insert an explicit binding with cluster.
	if _, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO user_role_bindings(user_id, role_name, cluster, namespace) VALUES (?, ?, ?, ?)`,
		alice, "viewer", "local", "team-a"); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	status, body := doReq(t, "GET", srv.URL+"/users/"+strconv.FormatInt(alice, 10)+"/bindings", nil)
	if status != 200 {
		t.Fatalf("want 200, got %d", status)
	}
	var bindings []bindingDTO
	if err := json.Unmarshal(body, &bindings); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, b := range bindings {
		if b.Cluster == "local" && b.Namespace == "team-a" && b.RoleName == "viewer" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("binding with cluster not found in %+v", bindings)
	}
}

func TestDeleteBinding_RejectWildcardCluster(t *testing.T) {
	allowTeamA(t)
	srv, _, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	alice := seedUser(t, newTestStore(t), "alice", "viewer", "pw")
	status, _ := doReq(t, "DELETE",
		srv.URL+"/users/"+strconv.FormatInt(alice, 10)+"/bindings/operator/team-a?cluster=*", nil)
	if status != 400 {
		t.Fatalf("want 400 for wildcard cluster, got %d", status)
	}
}

func TestDeleteBinding_DefaultsAndDeletesCluster(t *testing.T) {
	allowTeamA(t)
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	alice := seedUser(t, store, "alice", "operator", "pw")
	// Insert a binding with cluster.
	if _, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO user_role_bindings(user_id, role_name, cluster, namespace) VALUES (?, ?, ?, ?)`,
		alice, "viewer", "local", "team-a"); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	// Delete without specifying cluster (should default to "local").
	status, _ := doReq(t, "DELETE",
		srv.URL+"/users/"+strconv.FormatInt(alice, 10)+"/bindings/viewer/team-a", nil)
	if status != 204 {
		t.Fatalf("want 204, got %d", status)
	}
	// Verify deleted.
	var n int
	err := store.DB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM user_role_bindings WHERE user_id = ? AND cluster = ? AND namespace = ?`,
		alice, "local", "team-a").Scan(&n)
	if err != nil || n != 0 {
		t.Fatalf("binding should be deleted, got count=%d err=%v", n, err)
	}
}

// TestUsers_DeleteRemovesAccountRows verifies that DELETE /users/{id}
// removes the account's SSO link, preferences, sessions and bindings, and
// revokes the share links it created.
func TestUsers_DeleteRemovesAccountRows(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 999, Role: "admin"})
	id := seedUser(t, store, "leaving-user", "viewer", "longenoughpw1")
	ctx := t.Context()
	for _, stmt := range []string{
		`INSERT INTO oidc_links(user_id, issuer, subject, email) VALUES (?, 'https://idp.example', 'sub-leaving', 'leaving@example.com')`,
		`INSERT INTO user_preferences(user_id) VALUES (?)`,
		`INSERT INTO sessions(token, user_id, csrf_token, expires_at) VALUES ('sess-leaving', ?, 'csrf-leaving', '2999-01-01T00:00:00Z')`,
		`INSERT INTO user_role_bindings(user_id, role_name, cluster, namespace) VALUES (?, 'viewer', 'local', '*')`,
	} {
		if _, err := store.DB.ExecContext(ctx, stmt, id); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	token, _, err := store.CreateShareLink(ctx, "local", "gameplane-games", "srv-leaving", id, false, nil)
	if err != nil {
		t.Fatalf("create share link: %v", err)
	}

	status, body := doReq(t, "DELETE", srv.URL+"/users/"+strconv.FormatInt(id, 10), nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete user: status=%d body=%s", status, body)
	}

	for _, q := range []string{
		`SELECT COUNT(*) FROM users WHERE id = ?`,
		`SELECT COUNT(*) FROM oidc_links WHERE user_id = ?`,
		`SELECT COUNT(*) FROM user_preferences WHERE user_id = ?`,
		`SELECT COUNT(*) FROM sessions WHERE user_id = ?`,
		`SELECT COUNT(*) FROM user_role_bindings WHERE user_id = ?`,
	} {
		var n int
		if err := store.DB.QueryRowContext(ctx, q, id).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if n != 0 {
			t.Errorf("%s = %d after delete, want 0", q, n)
		}
	}
	if _, err := store.LookupShareLink(ctx, token); !errors.Is(err, db.ErrShareLinkInvalid) {
		t.Errorf("share link after creator delete: got %v, want ErrShareLinkInvalid", err)
	}
}

func TestUsers_ListReturnsRFC3339Timestamps(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	// Seed a user to ensure created_at is present
	id := seedUser(t, store, "alice", "viewer", "pw")

	// List users
	status, body := doReq(t, "GET", srv.URL+"/users", nil)
	if status != 200 {
		t.Fatalf("list want 200 got %d body=%s", status, body)
	}

	var users []userDTO
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Find the seeded user
	var alice *userDTO
	for i := range users {
		if users[i].ID == id {
			alice = &users[i]
			break
		}
	}
	if alice == nil {
		t.Fatalf("seeded user not found in list")
	}

	// Verify created_at is RFC3339
	if _, err := time.Parse(time.RFC3339, alice.CreatedAt); err != nil {
		t.Fatalf("createdAt %q is not RFC3339: %v", alice.CreatedAt, err)
	}
}

func TestUsers_FetchByIDReturnsRFC3339(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 1, Role: "admin"})
	id := seedUser(t, store, "bob", "viewer", "pw")

	status, body := doReq(t, "PATCH", srv.URL+"/users/"+strconv.FormatInt(id, 10), map[string]any{
		"displayName": "Bobby",
	})
	if status != 200 {
		t.Fatalf("patch want 200 got %d body=%s", status, body)
	}

	var user userDTO
	if err := json.Unmarshal(body, &user); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Verify created_at is RFC3339
	if _, err := time.Parse(time.RFC3339, user.CreatedAt); err != nil {
		t.Fatalf("createdAt %q is not RFC3339: %v", user.CreatedAt, err)
	}
}
