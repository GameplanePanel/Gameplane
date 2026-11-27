package rbac

import (
	"net/http"
	"testing"
)

// TestThemeRules_Match pins the permission each theme route resolves to.
// Theme sources are admin-only; the catalog and own installed themes are open
// to any signed-in user; another user's installed themes stay on the users rules.
func TestThemeRules_Match(t *testing.T) {
	cases := []struct {
		method, path, perm string
	}{
		{"GET", "/themes/sources", "themes:manage"},
		{"POST", "/themes/sources", "themes:manage"},
		{"PUT", "/themes/sources/community", "themes:manage"},
		{"DELETE", "/themes/sources/community", "themes:manage"},
		{"POST", "/themes/sources/community/refresh", "themes:manage"},
		{"GET", "/themes/catalog", ""},
		{"GET", "/themes/catalog/3", ""},
		{"GET", "/themes/catalog/3/preview/light", ""},
		{"GET", "/users/me/themes", ""},
		{"POST", "/users/me/themes", ""},
		{"GET", "/users/me/themes/7", ""},
		{"POST", "/users/me/themes/7/update", ""},
		{"DELETE", "/users/me/themes/7", ""},
		{"GET", "/users/12/themes", "users:read"},
		{"POST", "/users/12/themes", "users:manage"},
	}
	for _, c := range cases {
		r, ok := match(c.method, c.path)
		if !ok || r.perm != c.perm {
			t.Errorf("%s %s: rule perm=%q ok=%v, want %q", c.method, c.path, r.perm, ok, c.perm)
		}
	}
}

// TestThemeRules_SourcesNeedThemesManage proves a plain signed-in user cannot read or
// change theme sources, while a themes:manage holder and the admin wildcard can.
func TestThemeRules_SourcesNeedThemesManage(t *testing.T) {
	viewer := clusterWide(RoleViewer)
	manager := clusterWide(RoleAdmin, "themes:manage")
	admin := clusterWide(RoleAdmin, "*")

	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/themes/sources"},
		{http.MethodPost, "/themes/sources"},
		{http.MethodPut, "/themes/sources/community"},
		{http.MethodDelete, "/themes/sources/community"},
		{http.MethodPost, "/themes/sources/community/refresh"},
	} {
		if code := mcpRulesServe(t, viewer, target.method, target.path); code != http.StatusForbidden {
			t.Errorf("viewer %s %s: code=%d, want 403", target.method, target.path, code)
		}
		if code := mcpRulesServe(t, manager, target.method, target.path); code != http.StatusNoContent {
			t.Errorf("themes:manage %s %s: code=%d, want passthrough", target.method, target.path, code)
		}
		if code := mcpRulesServe(t, admin, target.method, target.path); code != http.StatusNoContent {
			t.Errorf("admin %s %s: code=%d, want passthrough", target.method, target.path, code)
		}
	}
}

// TestThemeRules_CatalogAndOwnThemesOpenToAnyUser keeps the catalog and the caller's own
// installed themes reachable by a plain user.
func TestThemeRules_CatalogAndOwnThemesOpenToAnyUser(t *testing.T) {
	plain := clusterWide(RoleViewer)
	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/themes/catalog"},
		{http.MethodGet, "/themes/catalog/3"},
		{http.MethodGet, "/themes/catalog/3/preview/dark"},
		{http.MethodGet, "/users/me/themes"},
		{http.MethodPost, "/users/me/themes"},
		{http.MethodGet, "/users/me/themes/7"},
		{http.MethodPost, "/users/me/themes/7/update"},
		{http.MethodDelete, "/users/me/themes/7"},
	} {
		if code := mcpRulesServe(t, plain, target.method, target.path); code != http.StatusNoContent {
			t.Errorf("%s %s: code=%d, want passthrough", target.method, target.path, code)
		}
	}
}

// TestThemeRules_OtherUsersThemesNeedUsersPermission proves another user's installed
// themes are not reachable through the own-profile rules.
func TestThemeRules_OtherUsersThemesNeedUsersPermission(t *testing.T) {
	plain := clusterWide(RoleViewer)
	if code := mcpRulesServe(t, plain, http.MethodGet, "/users/12/themes"); code != http.StatusForbidden {
		t.Errorf("viewer GET /users/12/themes: code=%d, want 403", code)
	}
	if code := mcpRulesServe(t, plain, http.MethodPost, "/users/12/themes"); code != http.StatusForbidden {
		t.Errorf("viewer POST /users/12/themes: code=%d, want 403", code)
	}
}
