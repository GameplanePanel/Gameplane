//go:build envtest

package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

// Spec 034 users API: every user response carries roleSource, a role set
// through PATCH is hand-set (FR-010), and PATCH roleSource "sso" returns an
// SSO user to SSO control (OD-6, contracts/users-api.md).

// linkSSO gives the user an oidc_links row and the given role source.
func linkSSO(t *testing.T, store *db.Store, userID int64, username, source string) {
	t.Helper()
	if _, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO oidc_links(user_id, issuer, subject, email) VALUES (?, 'https://idp.example', ?, ?)`,
		userID, "sub-"+username, username+"@example.com"); err != nil {
		t.Fatalf("link %s: %v", username, err)
	}
	if err := store.SetRoleSource(t.Context(), nil, userID, source); err != nil {
		t.Fatalf("set role source of %s: %v", username, err)
	}
}

// storedSource returns the user's stored role source.
func storedSource(t *testing.T, store *db.Store, userID int64) string {
	t.Helper()
	src, err := store.RoleSource(t.Context(), userID)
	if err != nil {
		t.Fatalf("read role source: %v", err)
	}
	return src
}

// storedRole returns the user's stored role.
func storedRole(t *testing.T, store *db.Store, userID int64) string {
	t.Helper()
	var role string
	if err := store.DB.QueryRowContext(t.Context(), `SELECT role FROM users WHERE id = ?`, userID).Scan(&role); err != nil {
		t.Fatalf("read role: %v", err)
	}
	return role
}

func usersPath(id int64) string { return "/users/" + strconv.FormatInt(id, 10) }

// wantMessage requires status and an English body naming the message.
func wantMessage(t *testing.T, gotStatus int, body []byte, status int, id string) {
	t.Helper()
	if gotStatus != status {
		t.Fatalf("status = %d, want %d (%s); body=%s", gotStatus, status, id, body)
	}
	entry := messages.New(messages.ID(id), nil).English
	if string(body) != entry+"\n" {
		t.Fatalf("body = %q, want %q (%s)", body, entry, id)
	}
}

// --- T028: role changes are hand-set, every response carries roleSource ---

func TestUsersRoleSource_PatchRoleSetsManual(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 999, Role: "admin"})
	id := seedUser(t, store, "rs-patch-role", "viewer", "")
	linkSSO(t, store, id, "rs-patch-role", db.RoleSourceSSO)

	status, body := doReq(t, http.MethodPatch, srv.URL+usersPath(id), map[string]any{"role": "operator"})
	if status != http.StatusOK {
		t.Fatalf("patch: %d %s", status, body)
	}
	var got userDTO
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Role != "operator" || got.RoleSource != db.RoleSourceManual {
		t.Fatalf("response role/roleSource = %q/%q, want operator/manual", got.Role, got.RoleSource)
	}
	if src := storedSource(t, store, id); src != db.RoleSourceManual {
		t.Fatalf("stored role_source = %q, want manual", src)
	}
}

// FR-010: setting the same role again still marks it hand-set.
func TestUsersRoleSource_PatchSameRoleSetsManual(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 999, Role: "admin"})
	id := seedUser(t, store, "rs-same-role", "viewer", "")
	linkSSO(t, store, id, "rs-same-role", db.RoleSourceSSO)

	status, body := doReq(t, http.MethodPatch, srv.URL+usersPath(id), map[string]any{"role": "viewer"})
	if status != http.StatusOK {
		t.Fatalf("patch: %d %s", status, body)
	}
	if src := storedSource(t, store, id); src != db.RoleSourceManual {
		t.Fatalf("stored role_source = %q, want manual", src)
	}
}

// A PATCH without role leaves the source alone.
func TestUsersRoleSource_PatchWithoutRoleKeepsSource(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 999, Role: "admin"})
	id := seedUser(t, store, "rs-display", "viewer", "")
	linkSSO(t, store, id, "rs-display", db.RoleSourceSSO)

	status, body := doReq(t, http.MethodPatch, srv.URL+usersPath(id), map[string]any{"displayName": "RS"})
	if status != http.StatusOK {
		t.Fatalf("patch: %d %s", status, body)
	}
	if src := storedSource(t, store, id); src != db.RoleSourceSSO {
		t.Fatalf("stored role_source = %q, want sso", src)
	}
}

func TestUsersRoleSource_EveryResponseCarriesRoleSource(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 999, Username: "rs-caller", Role: "admin"})
	ssoID := seedUser(t, store, "rs-every-sso", "viewer", "")
	linkSSO(t, store, ssoID, "rs-every-sso", db.RoleSourceSSOPending)
	localID := seedUser(t, store, "rs-every-local", "viewer", "longenoughpw1")

	// List: every entry has the key, with each user's stored value.
	status, body := doReq(t, http.MethodGet, srv.URL+"/users", nil)
	if status != http.StatusOK {
		t.Fatalf("list: %d %s", status, body)
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	want := map[float64]string{float64(ssoID): db.RoleSourceSSOPending, float64(localID): db.RoleSourceManual}
	for _, u := range list {
		src, ok := u["roleSource"]
		if !ok {
			t.Fatalf("list entry %v has no roleSource", u)
		}
		if w, tracked := want[u["id"].(float64)]; tracked && src != w {
			t.Fatalf("list entry %v: roleSource = %v, want %s", u["username"], src, w)
		}
	}

	// Create, PATCH and /users/me carry it too.
	checks := []struct {
		name   string
		method string
		path   string
		body   any
		want   string
	}{
		{"create", http.MethodPost, "/users", map[string]any{"username": "rs-every-new", "password": "longenoughpw1"}, db.RoleSourceManual},
		{"patch", http.MethodPatch, usersPath(ssoID), map[string]any{"displayName": "Every"}, db.RoleSourceSSOPending},
		{"me", http.MethodGet, "/users/me", nil, db.RoleSourceManual},
	}
	for _, c := range checks {
		status, body := doReq(t, c.method, srv.URL+c.path, c.body)
		if status != http.StatusOK {
			t.Fatalf("%s: %d %s", c.name, status, body)
		}
		var u map[string]any
		if err := json.Unmarshal(body, &u); err != nil {
			t.Fatalf("%s: decode: %v", c.name, err)
		}
		if u["roleSource"] != c.want {
			t.Fatalf("%s: roleSource = %v, want %s; body=%s", c.name, u["roleSource"], c.want, body)
		}
	}
}

// --- T040: return to SSO control ---

func TestUsersRoleSource_ReturnToSSOSetsPendingAndKeepsRole(t *testing.T) {
	srv, store, sessions := newUsersServer(t, &auth.User{ID: 999, Role: "admin"})
	id := seedUser(t, store, "rs-return", "operator", "")
	linkSSO(t, store, id, "rs-return", db.RoleSourceManual)
	if _, _, err := sessions.Create(t.Context(), id); err != nil {
		t.Fatalf("create session: %v", err)
	}

	status, body := doReq(t, http.MethodPatch, srv.URL+usersPath(id), map[string]any{"roleSource": "sso"})
	if status != http.StatusOK {
		t.Fatalf("patch: %d %s", status, body)
	}
	var got userDTO
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.RoleSource != db.RoleSourceSSOPending || got.Role != "operator" {
		t.Fatalf("response roleSource/role = %q/%q, want sso_pending/operator", got.RoleSource, got.Role)
	}
	if src := storedSource(t, store, id); src != db.RoleSourceSSOPending {
		t.Fatalf("stored role_source = %q, want sso_pending", src)
	}
	if role := storedRole(t, store, id); role != "operator" {
		t.Fatalf("stored role = %q, want operator unchanged", role)
	}
	// The role does not change yet, so the user's sessions stay.
	if n := sessionCount(t, store, id); n != 1 {
		t.Fatalf("sessions = %d, want 1 (not ended)", n)
	}
}

func TestUsersRoleSource_ReturnToSSORefusals(t *testing.T) {
	srv, store, _ := newUsersServer(t, &auth.User{ID: 999, Role: "admin"})
	ssoID := seedUser(t, store, "rs-refuse-sso", "operator", "")
	linkSSO(t, store, ssoID, "rs-refuse-sso", db.RoleSourceManual)
	localID := seedUser(t, store, "rs-refuse-local", "viewer", "longenoughpw1")

	cases := []struct {
		name   string
		id     int64
		body   map[string]any
		status int
		msg    string
	}{
		{"other value", ssoID, map[string]any{"roleSource": "manual"}, http.StatusBadRequest, "server.users.role_source_invalid"},
		{"pending value", ssoID, map[string]any{"roleSource": "sso_pending"}, http.StatusBadRequest, "server.users.role_source_invalid"},
		{"with role", ssoID, map[string]any{"roleSource": "sso", "role": "viewer"}, http.StatusBadRequest, "server.users.role_source_with_role"},
		{"no SSO link", localID, map[string]any{"roleSource": "sso"}, http.StatusBadRequest, "server.users.role_source_not_sso"},
		{"unknown user", 987654, map[string]any{"roleSource": "sso"}, http.StatusNotFound, "server.common.user_not_found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, body := doReq(t, http.MethodPatch, srv.URL+usersPath(c.id), c.body)
			wantMessage(t, status, body, c.status, c.msg)
		})
	}
	if src := storedSource(t, store, ssoID); src != db.RoleSourceManual {
		t.Fatalf("refused requests changed role_source to %q", src)
	}
	if role := storedRole(t, store, ssoID); role != "operator" {
		t.Fatalf("refused requests changed role to %q", role)
	}
	if src := storedSource(t, store, localID); src != db.RoleSourceManual {
		t.Fatalf("local user's role_source = %q, want manual", src)
	}
}

// The target check of a role change applies: a delegated manager cannot
// return a privileged account to SSO control; a full admin can.
func TestUsersRoleSource_ReturnToSSOTargetCheck(t *testing.T) {
	caller := pgUser(0, "users:manage")
	srv, store, _ := newUsersServer(t, caller)
	caller.ID = seedUser(t, store, "rs-delegate", "viewer", "")
	seedRole(t, store, "rs-priv", "users:manage")
	target := seedUser(t, store, "rs-priv-target", "viewer", "")
	pgBind(t, store, target, "rs-priv")
	linkSSO(t, store, target, "rs-priv-target", db.RoleSourceManual)

	status, body := doReq(t, http.MethodPatch, srv.URL+usersPath(target), map[string]any{"roleSource": "sso"})
	if status != http.StatusForbidden {
		t.Fatalf("delegated manager on privileged target: %d %s, want 403", status, body)
	}
	if src := storedSource(t, store, target); src != db.RoleSourceManual {
		t.Fatalf("refused request changed role_source to %q", src)
	}

	caller.Role = rbac.RoleAdmin
	caller.Perms = nil
	status, body = doReq(t, http.MethodPatch, srv.URL+usersPath(target), map[string]any{"roleSource": "sso"})
	if status != http.StatusOK {
		t.Fatalf("full admin: %d %s, want 200", status, body)
	}
	if src := storedSource(t, store, target); src != db.RoleSourceSSOPending {
		t.Fatalf("stored role_source = %q, want sso_pending", src)
	}
}
