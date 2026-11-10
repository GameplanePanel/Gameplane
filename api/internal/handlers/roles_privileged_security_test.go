package handlers

import (
	"net/http"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

// The wildcard is never grantable to a custom role, whoever asks.
func TestRolesPriv_CreateWildcardRejected(t *testing.T) {
	srv, _ := newRolesServerAs(t, pgUser(700, "roles:manage"))
	status, body := doReq(t, "POST", srv.URL+"/roles/", map[string]any{
		"name": "rp-wildcard", "permissions": []string{"*"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("create role with *: want 400 got %d body=%s", status, body)
	}
}

// A delegated role manager can create roles with ordinary permissions but
// not with users:manage, roles:manage, config:manage or cluster:manage.
func TestRolesPriv_CreatePrivilegedPermissionGate(t *testing.T) {
	caller := pgUser(701, "roles:manage")
	srv, store := newRolesServerAs(t, caller)

	status, body := doReq(t, "POST", srv.URL+"/roles/", map[string]any{
		"name": "rp-create-priv", "permissions": []string{"servers:read", "users:manage"},
	})
	if status != http.StatusForbidden {
		t.Fatalf("delegated create with users:manage: want 403 got %d body=%s", status, body)
	}
	if roleHasPermission(t, store, "rp-create-priv", "users:manage") {
		t.Fatal("refused role was still created with users:manage")
	}

	status, body = doReq(t, "POST", srv.URL+"/roles/", map[string]any{
		"name": "rp-create-plain", "permissions": []string{"servers:read"},
	})
	if status != http.StatusCreated {
		t.Fatalf("delegated create with ordinary permission: want 201 got %d body=%s", status, body)
	}

	caller.Role = rbac.RoleAdmin
	caller.Perms = nil
	status, body = doReq(t, "POST", srv.URL+"/roles/", map[string]any{
		"name": "rp-create-admin", "permissions": []string{"users:manage"},
	})
	if status != http.StatusCreated {
		t.Fatalf("full admin create with users:manage: want 201 got %d body=%s", status, body)
	}
}

// An edit that adds a privileged permission is refused for a delegated role
// manager; ordinary edits pass; a full admin may add it.
func TestRolesPriv_UpdateAddingPrivilegedPermissionGate(t *testing.T) {
	caller := pgUser(702, "roles:manage")
	srv, store := newRolesServerAs(t, caller)
	seedRole(t, store, "rp-edit-target", "servers:read")

	status, body := doReq(t, "PATCH", srv.URL+"/roles/rp-edit-target", map[string]any{
		"permissions": []string{"servers:read", "users:manage"},
	})
	if status != http.StatusForbidden {
		t.Fatalf("delegated edit adding users:manage: want 403 got %d body=%s", status, body)
	}
	if roleHasPermission(t, store, "rp-edit-target", "users:manage") {
		t.Fatal("refused edit still granted users:manage")
	}

	status, body = doReq(t, "PATCH", srv.URL+"/roles/rp-edit-target", map[string]any{
		"permissions": []string{"servers:read", "servers:write"},
	})
	if status != http.StatusOK {
		t.Fatalf("delegated ordinary edit: want 200 got %d body=%s", status, body)
	}

	caller.Role = rbac.RoleAdmin
	caller.Perms = nil
	status, body = doReq(t, "PATCH", srv.URL+"/roles/rp-edit-target", map[string]any{
		"permissions": []string{"servers:read", "users:manage"},
	})
	if status != http.StatusOK {
		t.Fatalf("full admin edit adding users:manage: want 200 got %d body=%s", status, body)
	}
}
