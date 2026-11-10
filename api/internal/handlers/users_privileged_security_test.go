package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// A delegated manager may create accounts with an ordinary role but not with
// a role that carries a privileged permission; a full admin may do both.
func TestUsersPriv_CreateRoleGrantGate(t *testing.T) {
	caller := pgUser(501, "users:manage")
	srv, store, _ := newUsersServer(t, caller)
	seedRole(t, store, "up-create-priv", "users:manage")
	seedRole(t, store, "up-create-plain", "servers:read")

	status, body := doReq(t, "POST", srv.URL+"/users", map[string]any{
		"username": "up-new-priv", "password": "longenoughpw1", "role": "up-create-priv",
	})
	if status != http.StatusForbidden {
		t.Fatalf("delegated manager creating privileged-role user: want 403 got %d body=%s", status, body)
	}

	status, body = doReq(t, "POST", srv.URL+"/users", map[string]any{
		"username": "up-new-plain", "password": "longenoughpw1", "role": "up-create-plain",
	})
	if status != http.StatusOK {
		t.Fatalf("delegated manager creating plain-role user: want 200 got %d body=%s", status, body)
	}

	caller.Role = rbac.RoleAdmin
	caller.Perms = nil
	status, body = doReq(t, "POST", srv.URL+"/users", map[string]any{
		"username": "up-new-admin", "password": "longenoughpw1", "role": "up-create-priv",
	})
	if status != http.StatusOK {
		t.Fatalf("full admin creating privileged-role user: want 200 got %d body=%s", status, body)
	}
}

// Changing a privileged account is refused for a delegated manager, and
// granting a privileged role through an edit is refused too. Self-edits pass.
func TestUsersPriv_UpdateGates(t *testing.T) {
	caller := pgUser(0, "users:manage", "roles:manage")
	srv, store, _ := newUsersServer(t, caller)
	caller.ID = seedUser(t, store, "up-upd-manager", "viewer", "")
	seedRole(t, store, "up-upd-priv", "users:manage")
	seedRole(t, store, "up-upd-plain", "servers:read")
	privTarget := seedUser(t, store, "up-upd-target", "viewer", "")
	pgBind(t, store, privTarget, "up-upd-priv")
	plainTarget := seedUser(t, store, "up-upd-plain-user", "viewer", "")

	patch := func(id int64, body map[string]any) (int, []byte) {
		return doReq(t, "PATCH", srv.URL+"/users/"+strconv.FormatInt(id, 10), body)
	}

	if status, body := patch(privTarget, map[string]any{"displayName": "Renamed"}); status != http.StatusForbidden {
		t.Fatalf("delegated manager editing privileged account: want 403 got %d body=%s", status, body)
	}
	if status, body := patch(plainTarget, map[string]any{"role": "up-upd-priv"}); status != http.StatusForbidden {
		t.Fatalf("delegated manager granting privileged role by edit: want 403 got %d body=%s", status, body)
	}
	if status, body := patch(caller.ID, map[string]any{"displayName": "Self"}); status != http.StatusOK {
		t.Fatalf("self edit: want 200 got %d body=%s", status, body)
	}
}

// Delete, password reset and binding changes on a privileged account are
// refused for a delegated manager.
func TestUsersPriv_DeleteResetAndBindingGates(t *testing.T) {
	caller := pgUser(0, "users:manage")
	srv, store, _ := newUsersServer(t, caller)
	caller.ID = seedUser(t, store, "up-bind-manager", "viewer", "")
	seedRole(t, store, "up-bind-priv", "users:manage")
	seedRole(t, store, "up-bind-plain", "servers:read")
	target := seedUser(t, store, "up-bind-target", "viewer", "")
	pgBind(t, store, target, "up-bind-priv")
	base := srv.URL + "/users/" + strconv.FormatInt(target, 10)

	if status, body := doReq(t, "DELETE", base, nil); status != http.StatusForbidden {
		t.Fatalf("delete privileged account: want 403 got %d body=%s", status, body)
	}
	if status, body := doReq(t, "POST", base+"/reset-password",
		map[string]any{"password": "brand-new-password-1"}); status != http.StatusForbidden {
		t.Fatalf("reset privileged password: want 403 got %d body=%s", status, body)
	}
	// Target holds a privileged role, so binding even an ordinary role is refused.
	if status, body := doReq(t, "POST", base+"/bindings", map[string]any{
		"roleName": "up-bind-plain", "namespace": scope.DefaultNamespace, "cluster": scope.DefaultCluster,
	}); status != http.StatusForbidden {
		t.Fatalf("bind plain role to privileged account: want 403 got %d body=%s", status, body)
	}
	// Binding a privileged role to any account is refused for a delegated manager.
	plainTarget := seedUser(t, store, "up-bind-plain-user", "viewer", "")
	if status, body := doReq(t, "POST", srv.URL+"/users/"+strconv.FormatInt(plainTarget, 10)+"/bindings", map[string]any{
		"roleName": "up-bind-priv", "namespace": scope.DefaultNamespace, "cluster": scope.DefaultCluster,
	}); status != http.StatusForbidden {
		t.Fatalf("bind privileged role: want 403 got %d body=%s", status, body)
	}
	if status, body := doReq(t, "DELETE",
		base+"/bindings/up-bind-priv/"+scope.DefaultNamespace+"?cluster="+scope.DefaultCluster, nil); status != http.StatusForbidden {
		t.Fatalf("delete binding on privileged account: want 403 got %d body=%s", status, body)
	}
}

// A full administrator passes the same gates on a privileged account.
func TestUsersPriv_FullAdminChangesPrivilegedAccount(t *testing.T) {
	caller := &auth.User{ID: 1, Role: rbac.RoleAdmin}
	srv, store, _ := newUsersServer(t, caller)
	seedRole(t, store, "up-admin-priv", "users:manage")
	target := seedUser(t, store, "up-admin-target", "viewer", "")
	pgBind(t, store, target, "up-admin-priv")
	base := srv.URL + "/users/" + strconv.FormatInt(target, 10)

	if status, body := doReq(t, "PATCH", base, map[string]any{"displayName": "Admin Edit"}); status != http.StatusOK {
		t.Fatalf("full admin edit: want 200 got %d body=%s", status, body)
	}
	if status, body := doReq(t, "POST", base+"/reset-password",
		map[string]any{"password": "brand-new-password-1"}); status != http.StatusNoContent {
		t.Fatalf("full admin reset: want 204 got %d body=%s", status, body)
	}
}

// A password reset ends every session the target had, so a session token
// that worked before the reset is rejected afterwards.
func TestUsersPriv_ResetPasswordRevokesOldSession(t *testing.T) {
	caller := &auth.User{ID: 1, Role: rbac.RoleAdmin}
	srv, store, _ := newUsersServer(t, caller)
	target := seedUser(t, store, "up-reset-target", "viewer", "originalpassword")

	sessions := auth.NewSessionStore(store)
	token, _, err := sessions.Create(t.Context(), target)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	// Session cookie name as set by the auth package.
	const sessionCookieName = "gameplane_session"
	guarded := sessions.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	probe := func() int {
		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/me", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		guarded.ServeHTTP(rr, req)
		return rr.Code
	}

	if got := probe(); got != http.StatusOK {
		t.Fatalf("session before reset: want 200 got %d", got)
	}
	if status, body := doReq(t, "POST", srv.URL+"/users/"+strconv.FormatInt(target, 10)+"/reset-password",
		map[string]any{"password": "brand-new-password-1"}); status != http.StatusNoContent {
		t.Fatalf("reset: want 204 got %d body=%s", status, body)
	}
	if got := probe(); got == http.StatusOK {
		t.Fatal("old session still accepted after password reset")
	}
}

// When the transaction cannot begin, the reset is reported as a server error.
func TestUsersPriv_ResetPasswordStoreFailure(t *testing.T) {
	caller := &auth.User{ID: 1, Role: rbac.RoleAdmin}
	srv, store, _ := newUsersServer(t, caller)
	target := seedUser(t, store, "up-reset-broken", "viewer", "originalpassword")
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	status, body := doReq(t, "POST", srv.URL+"/users/"+strconv.FormatInt(target, 10)+"/reset-password",
		map[string]any{"password": "brand-new-password-1"})
	if status != http.StatusInternalServerError {
		t.Fatalf("reset on closed store: want 500 got %d body=%s", status, body)
	}
}
