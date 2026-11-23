package rbac

import (
	"net/http"
	"testing"
)

// TestSFTPAdminRules_MatchUsersManage checks that the admin SFTP routes resolve to users:manage and the
// self-service SFTP routes stay open to any signed-in user.
func TestSFTPAdminRules_MatchUsersManage(t *testing.T) {
	cases := []struct {
		method, path, perm string
	}{
		{"GET", "/users/12/sftp", "users:manage"},
		{"DELETE", "/users/12/sftp/keys/7", "users:manage"},
		{"DELETE", "/users/12/sftp/password", "users:manage"},
		{"GET", "/users/me/sftp", ""},
		{"POST", "/users/me/sftp/keys", ""},
		{"DELETE", "/users/me/sftp/keys/7", ""},
		{"POST", "/users/me/sftp/password", ""},
		{"DELETE", "/users/me/sftp/password", ""},
	}
	for _, c := range cases {
		r, ok := match(c.method, c.path)
		if !ok || r.perm != c.perm {
			t.Errorf("%s %s: rule perm=%q ok=%v, want %q", c.method, c.path, r.perm, ok, c.perm)
		}
	}
}

// TestSFTPAdminRules_NeedUsersManage proves that reading or revoking another user's SFTP credentials is
// refused to users:read holders and allowed to users:manage holders.
func TestSFTPAdminRules_NeedUsersManage(t *testing.T) {
	viewer := clusterWide(RoleViewer, "servers:read")
	reader := clusterWide(RoleOperator, "users:read")
	manager := clusterWide(RoleAdmin, "users:read", "users:manage")

	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/users/12/sftp"},
		{http.MethodDelete, "/users/12/sftp/keys/7"},
		{http.MethodDelete, "/users/12/sftp/password"},
	} {
		if code := mcpRulesServe(t, viewer, target.method, target.path); code != http.StatusForbidden {
			t.Errorf("viewer %s %s: code=%d, want 403", target.method, target.path, code)
		}
		if code := mcpRulesServe(t, reader, target.method, target.path); code != http.StatusForbidden {
			t.Errorf("users:read %s %s: code=%d, want 403", target.method, target.path, code)
		}
		if code := mcpRulesServe(t, manager, target.method, target.path); code != http.StatusNoContent {
			t.Errorf("users:manage %s %s: code=%d, want passthrough", target.method, target.path, code)
		}
	}
}

// TestSFTPAdminRules_SelfRoutesOpenToAnyUser keeps the personal SFTP routes reachable by a plain user
// even though the admin rules sit next to them.
func TestSFTPAdminRules_SelfRoutesOpenToAnyUser(t *testing.T) {
	plain := clusterWide(RoleViewer)
	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/users/me/sftp"},
		{http.MethodDelete, "/users/me/sftp/keys/4"},
		{http.MethodDelete, "/users/me/sftp/password"},
	} {
		if code := mcpRulesServe(t, plain, target.method, target.path); code != http.StatusNoContent {
			t.Errorf("%s %s: code=%d, want passthrough", target.method, target.path, code)
		}
	}
}
