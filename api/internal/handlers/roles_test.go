package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

func newRolesServer(t *testing.T) (*httptest.Server, *db.Store) {
	t.Helper()
	store := newTestStore(t)
	r := chi.NewRouter()
	MountRoles(r, store)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, store
}

func TestRoles_ListIncludesBuiltins(t *testing.T) {
	srv, _ := newRolesServer(t)
	status, body := doReq(t, "GET", srv.URL+"/roles", nil)
	if status != 200 {
		t.Fatalf("list want 200 got %d body=%s", status, body)
	}
	var roles []roleDTO
	if err := json.Unmarshal(body, &roles); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := map[string]roleDTO{}
	for _, r := range roles {
		byName[r.Name] = r
	}
	for _, name := range []string{"admin", "operator", "viewer"} {
		if !byName[name].Builtin {
			t.Errorf("role %q not present/builtin", name)
		}
	}
	// admin carries the wildcard; operator can write servers.
	if got := byName["admin"].Permissions; len(got) != 1 || got[0] != "*" {
		t.Errorf("admin perms = %v, want [*]", got)
	}
	if !containsStr(byName["operator"].Permissions, "servers:write") {
		t.Errorf("operator missing servers:write: %v", byName["operator"].Permissions)
	}
}

// TestRoles_CreateSetsJSONContentType is the F-083 regression test: POST
// /roles's 201 response must carry Content-Type: application/json. net/http
// snapshots headers at WriteHeader, so calling WriteHeader(201) before the
// JSON encoder set the header (as this handler used to) leaves the body
// sniffed as text/plain.
func TestRoles_CreateSetsJSONContentType(t *testing.T) {
	srv, _ := newRolesServer(t)
	body, err := json.Marshal(map[string]any{
		"name":        "ct-check",
		"description": "Content-Type regression",
		"permissions": []string{"servers:read"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/roles", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		got, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 201; body=%s", resp.StatusCode, got)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestRoles_Catalog(t *testing.T) {
	srv, _ := newRolesServer(t)
	status, body := doReq(t, "GET", srv.URL+"/roles/permissions", nil)
	if status != 200 {
		t.Fatalf("catalog want 200 got %d body=%s", status, body)
	}
	var out struct {
		Groups []struct {
			Resource    string `json:"resource"`
			Permissions []struct {
				Key        string `json:"key"`
				Namespaced bool   `json:"namespaced"`
			} `json:"permissions"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Groups) == 0 {
		t.Fatal("empty catalog")
	}
}

func TestRoles_CreateAndDelete(t *testing.T) {
	srv, store := newRolesServer(t)
	status, body := doReq(t, "POST", srv.URL+"/roles", map[string]any{
		"name":        "support",
		"description": "Help desk",
		"permissions": []string{"servers:read", "servers:console", "backups:read"},
	})
	if status != 201 {
		t.Fatalf("create want 201 got %d body=%s", status, body)
	}
	var got roleDTO
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Builtin || len(got.Permissions) != 3 {
		t.Fatalf("unexpected role: %+v", got)
	}

	// Delete the unused custom role.
	status, body = doReq(t, "DELETE", srv.URL+"/roles/support", nil)
	if status != 204 {
		t.Fatalf("delete want 204 got %d body=%s", status, body)
	}
	var n int
	_ = store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM role_permissions WHERE role_name='support'`).Scan(&n)
	if n != 0 {
		t.Fatalf("permission rows not cleaned up: %d", n)
	}
}

func TestRoles_CreateRejectsWildcardAndUnknown(t *testing.T) {
	srv, _ := newRolesServer(t)
	for _, perm := range []string{"*", "servers:teleport"} {
		status, _ := doReq(t, "POST", srv.URL+"/roles", map[string]any{
			"name":        "bad",
			"permissions": []string{perm},
		})
		if status != 400 {
			t.Errorf("perm %q: want 400 got %d", perm, status)
		}
	}
}

func TestRoles_CreateRejectsDuplicate(t *testing.T) {
	srv, _ := newRolesServer(t)
	status, _ := doReq(t, "POST", srv.URL+"/roles", map[string]any{
		"name": "viewer", "permissions": []string{"servers:read"},
	})
	if status != 409 {
		t.Fatalf("duplicate name want 409 got %d", status)
	}
}

func TestRoles_AdminImmutable(t *testing.T) {
	srv, _ := newRolesServer(t)
	status, _ := doReq(t, "PATCH", srv.URL+"/roles/admin", map[string]any{
		"permissions": []string{"servers:read"},
	})
	if status != 400 {
		t.Fatalf("editing admin want 400 got %d", status)
	}
	status, _ = doReq(t, "DELETE", srv.URL+"/roles/admin", nil)
	if status != 400 {
		t.Fatalf("deleting admin want 400 got %d", status)
	}
}

func TestRoles_OperatorEditable(t *testing.T) {
	srv, _ := newRolesServer(t)
	status, body := doReq(t, "PATCH", srv.URL+"/roles/operator", map[string]any{
		"permissions": []string{"servers:read", "servers:write"},
	})
	if status != 200 {
		t.Fatalf("editing operator want 200 got %d body=%s", status, body)
	}
	var got roleDTO
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Permissions) != 2 {
		t.Fatalf("operator perms not replaced: %v", got.Permissions)
	}
}

func TestRoles_UpdateDescriptionOnly(t *testing.T) {
	srv, store := newRolesServer(t)
	if _, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO roles(name, builtin, description) VALUES ('support', 0, 'old')`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO role_permissions(role_name, permission) VALUES ('support', 'servers:read')`); err != nil {
		t.Fatalf("seed perm: %v", err)
	}
	status, body := doReq(t, "PATCH", srv.URL+"/roles/support", map[string]any{
		"description": "new desc",
	})
	if status != 200 {
		t.Fatalf("update want 200 got %d body=%s", status, body)
	}
	var got roleDTO
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Description != "new desc" {
		t.Errorf("description = %q, want 'new desc'", got.Description)
	}
	// Permissions are untouched when only the description changes.
	if len(got.Permissions) != 1 || got.Permissions[0] != "servers:read" {
		t.Errorf("permissions changed unexpectedly: %v", got.Permissions)
	}
}

func TestRoles_UpdateNotFound(t *testing.T) {
	srv, _ := newRolesServer(t)
	status, _ := doReq(t, "PATCH", srv.URL+"/roles/ghost", map[string]any{
		"description": "x",
	})
	if status != 404 {
		t.Fatalf("update missing role want 404 got %d", status)
	}
}

func TestRoles_DeleteNotFound(t *testing.T) {
	srv, _ := newRolesServer(t)
	status, _ := doReq(t, "DELETE", srv.URL+"/roles/ghost", nil)
	if status != 404 {
		t.Fatalf("delete missing role want 404 got %d", status)
	}
}

func TestRoles_DeleteBuiltinRejected(t *testing.T) {
	srv, _ := newRolesServer(t)
	status, _ := doReq(t, "DELETE", srv.URL+"/roles/viewer", nil)
	if status != 400 {
		t.Fatalf("deleting builtin want 400 got %d", status)
	}
}

func TestRoles_DeleteInUseRejected(t *testing.T) {
	srv, store := newRolesServer(t)
	if _, err := store.DB.ExecContext(t.Context(), `INSERT INTO roles(name, builtin) VALUES ('support', 0)`); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	uid := seedUser(t, store, "kim", "viewer", "longenoughpw1")
	if _, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO user_role_bindings(user_id, role_name, namespace) VALUES (?, 'support', 'team-a')`, uid); err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	status, _ := doReq(t, "DELETE", srv.URL+"/roles/support", nil)
	if status != 409 {
		t.Fatalf("delete in-use want 409 got %d", status)
	}
}
