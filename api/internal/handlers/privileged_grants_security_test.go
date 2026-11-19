package handlers

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// pgUser builds a non-admin caller holding exactly perms cluster-wide.
func pgUser(id int64, perms ...string) *auth.User {
	set := map[string]struct{}{}
	for _, p := range perms {
		set[p] = struct{}{}
	}
	return &auth.User{
		ID:       id,
		Username: "pg-user-" + strconv.FormatInt(id, 10),
		Role:     "pg-delegate",
		Perms: map[string]map[string]map[string]struct{}{
			scope.DefaultCluster: {"*": set},
		},
	}
}

// pgBind attaches role to userID as a cluster-wide binding on the default cluster.
func pgBind(t *testing.T, store *db.Store, userID int64, role string) {
	t.Helper()
	if _, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO user_role_bindings(user_id, role_name, cluster, namespace) VALUES (?, ?, ?, '*')`,
		userID, role, scope.DefaultCluster); err != nil {
		t.Fatalf("bind %s to %d: %v", role, userID, err)
	}
}

// pgGuardRequest returns a request carrying caller in its context.
func pgGuardRequest(t *testing.T, caller *auth.User) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/guard", nil)
	if caller != nil {
		req = req.WithContext(auth.WithUser(req.Context(), caller))
	}
	return req
}

func TestPrivGrant_IsPrivilegedPermission(t *testing.T) {
	for _, p := range []string{"*", "users:manage", "roles:manage", "config:manage", "cluster:manage"} {
		if !isPrivilegedPermission(p) {
			t.Errorf("%q should be privileged", p)
		}
	}
	for _, p := range []string{"servers:read", "servers:write", "", "users:read"} {
		if isPrivilegedPermission(p) {
			t.Errorf("%q should not be privileged", p)
		}
	}
}

func TestPrivGrant_IsFullAdmin(t *testing.T) {
	if isFullAdmin(nil) {
		t.Fatal("nil user must not be a full admin")
	}
	if !isFullAdmin(&auth.User{Role: rbac.RoleAdmin}) {
		t.Fatal("built-in admin role must be a full admin")
	}
	wildcard := &auth.User{Role: "pg-custom", Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {"*": {"*": {}}},
	}}
	if !isFullAdmin(wildcard) {
		t.Fatal("holder of the * permission must be a full admin")
	}
	if isFullAdmin(pgUser(1, "users:manage", "roles:manage")) {
		t.Fatal("delegated manager must not be a full admin")
	}
}

func TestPrivGrant_RoleIsPrivileged(t *testing.T) {
	store := newTestStore(t)
	seedRole(t, store, "pg-role-config", "config:manage")
	seedRole(t, store, "pg-role-plain", "servers:read")

	ctx := t.Context()
	if ok, err := roleIsPrivileged(ctx, store.DB, "pg-role-config"); err != nil || !ok {
		t.Fatalf("config:manage role: ok=%v err=%v, want true", ok, err)
	}
	if ok, err := roleIsPrivileged(ctx, store.DB, "pg-role-plain"); err != nil || ok {
		t.Fatalf("plain role: ok=%v err=%v, want false", ok, err)
	}
	if ok, err := roleIsPrivileged(ctx, store.DB, "pg-role-missing"); err != nil || ok {
		t.Fatalf("missing role: ok=%v err=%v, want false", ok, err)
	}

	closed := newTestStore(t)
	_ = closed.Close()
	if _, err := roleIsPrivileged(ctx, closed.DB, "pg-role-config"); err == nil {
		t.Fatal("closed store: want error")
	}
}

func TestPrivGrant_UserIsPrivileged(t *testing.T) {
	store := newTestStore(t)
	seedRole(t, store, "pg-user-cluster", "cluster:manage")
	seedRole(t, store, "pg-user-view", "servers:read")
	admin := seedUser(t, store, "pg-bound-admin", "viewer", "")
	pgBind(t, store, admin, "pg-user-cluster")
	viewer := seedUser(t, store, "pg-bound-viewer", "viewer", "")
	pgBind(t, store, viewer, "pg-user-view")
	unbound := seedUser(t, store, "pg-unbound", "viewer", "")

	ctx := t.Context()
	if ok, err := userIsPrivileged(ctx, store.DB, admin); err != nil || !ok {
		t.Fatalf("bound privileged user: ok=%v err=%v, want true", ok, err)
	}
	if ok, err := userIsPrivileged(ctx, store.DB, viewer); err != nil || ok {
		t.Fatalf("bound plain user: ok=%v err=%v, want false", ok, err)
	}
	if ok, err := userIsPrivileged(ctx, store.DB, unbound); err != nil || ok {
		t.Fatalf("unbound user: ok=%v err=%v, want false", ok, err)
	}

	closed := newTestStore(t)
	_ = closed.Close()
	if _, err := userIsPrivileged(ctx, closed.DB, admin); err == nil {
		t.Fatal("closed store: want error")
	}
}

func TestPrivGrant_AddsPrivileged(t *testing.T) {
	store := newTestStore(t)
	seedRole(t, store, "pg-add-role", "servers:read", "roles:manage")
	ctx := t.Context()

	// config:manage is not on the role yet, so it is an addition.
	if ok, err := addsPrivileged(ctx, store.DB, "pg-add-role", []string{"servers:read", "config:manage"}); err != nil || !ok {
		t.Fatalf("new privileged perm: ok=%v err=%v, want true", ok, err)
	}
	// roles:manage is already granted, so keeping it is not an addition.
	if ok, err := addsPrivileged(ctx, store.DB, "pg-add-role", []string{"roles:manage"}); err != nil || ok {
		t.Fatalf("already granted privileged perm: ok=%v err=%v, want false", ok, err)
	}
	// Non-privileged permissions never count.
	if ok, err := addsPrivileged(ctx, store.DB, "pg-add-role", []string{"servers:write"}); err != nil || ok {
		t.Fatalf("non-privileged perm: ok=%v err=%v, want false", ok, err)
	}

	closed := newTestStore(t)
	_ = closed.Close()
	if _, err := addsPrivileged(ctx, closed.DB, "pg-add-role", []string{"users:manage"}); err == nil {
		t.Fatal("closed store with privileged perm: want error")
	}
}

func TestPrivGrant_AllowRoleGrant(t *testing.T) {
	store := newTestStore(t)
	seedRole(t, store, "pg-grant-priv", "users:manage")
	seedRole(t, store, "pg-grant-plain", "servers:read")

	// Full administrator may grant a privileged role.
	rr := httptest.NewRecorder()
	if !allowRoleGrant(rr, pgGuardRequest(t, &auth.User{ID: 1, Role: rbac.RoleAdmin}), store.DB, "pg-grant-priv") {
		t.Fatal("full admin refused a privileged grant")
	}

	// Delegated manager may grant an unprivileged role.
	rr = httptest.NewRecorder()
	if !allowRoleGrant(rr, pgGuardRequest(t, pgUser(2, "users:manage")), store.DB, "pg-grant-plain") {
		t.Fatal("delegated manager refused an unprivileged grant")
	}

	// Delegated manager may not grant a privileged role.
	rr = httptest.NewRecorder()
	if allowRoleGrant(rr, pgGuardRequest(t, pgUser(2, "users:manage")), store.DB, "pg-grant-priv") {
		t.Fatal("delegated manager allowed to grant a privileged role")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("privileged grant refusal: status %d, want 403", rr.Code)
	}

	// An anonymous caller is treated as a non-admin.
	rr = httptest.NewRecorder()
	if allowRoleGrant(rr, pgGuardRequest(t, nil), store.DB, "pg-grant-priv") {
		t.Fatal("anonymous caller allowed to grant a privileged role")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("anonymous refusal: status %d, want 403", rr.Code)
	}

	// A lookup failure refuses the grant instead of allowing it.
	closed := newTestStore(t)
	_ = closed.Close()
	rr = httptest.NewRecorder()
	if allowRoleGrant(rr, pgGuardRequest(t, pgUser(3, "users:manage")), closed.DB, "pg-grant-priv") {
		t.Fatal("grant allowed although the role check failed")
	}
}

func TestPrivGrant_AllowTargetChange(t *testing.T) {
	store := newTestStore(t)
	seedRole(t, store, "pg-target-priv", "config:manage")
	privileged := seedUser(t, store, "pg-target-admin", "viewer", "")
	pgBind(t, store, privileged, "pg-target-priv")
	plain := seedUser(t, store, "pg-target-plain", "viewer", "")

	// A delegated manager cannot change a privileged account.
	rr := httptest.NewRecorder()
	if allowTargetChange(rr, pgGuardRequest(t, pgUser(10, "users:manage")), store.DB, privileged) {
		t.Fatal("delegated manager allowed to change a privileged account")
	}
	if rr.Code != http.StatusForbidden {
		t.Fatalf("privileged target refusal: status %d, want 403", rr.Code)
	}

	// A delegated manager may change an ordinary account.
	rr = httptest.NewRecorder()
	if !allowTargetChange(rr, pgGuardRequest(t, pgUser(10, "users:manage")), store.DB, plain) {
		t.Fatal("delegated manager refused an ordinary account")
	}

	// A caller may always change their own account, even a privileged one.
	rr = httptest.NewRecorder()
	if !allowTargetChange(rr, pgGuardRequest(t, pgUser(privileged, "users:manage")), store.DB, privileged) {
		t.Fatal("self change refused")
	}

	// A full administrator may change any account.
	rr = httptest.NewRecorder()
	if !allowTargetChange(rr, pgGuardRequest(t, &auth.User{ID: 1, Role: rbac.RoleAdmin}), store.DB, privileged) {
		t.Fatal("full admin refused a privileged account change")
	}

	// A lookup failure refuses the change.
	closed := newTestStore(t)
	_ = closed.Close()
	rr = httptest.NewRecorder()
	if allowTargetChange(rr, pgGuardRequest(t, pgUser(10, "users:manage")), closed.DB, privileged) {
		t.Fatal("change allowed although the privilege check failed")
	}
}
