package rbac

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
)

// themeSourceServe sends one request as u through Middleware(nil) and returns the
// status code. The inner handler answers 204, so 204 means the rule let it through.
func themeSourceServe(t *testing.T, u *auth.User, method, target string) int {
	t.Helper()
	h := Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	req = req.WithContext(auth.WithUser(req.Context(), u))
	h.ServeHTTP(rr, req)
	return rr.Code
}

// TestThemeSourceRules_NeedThemesManage pins the permission of every admin
// theme source route. The source list must not fall through to the catch-all
// rule, which would let any signed-in user read the sources.
func TestThemeSourceRules_NeedThemesManage(t *testing.T) {
	cases := []struct {
		method, path, perm string
	}{
		{"GET", "/themes/sources", "themes:manage"},
		{"POST", "/themes/sources", "themes:manage"},
		{"PUT", "/themes/sources/example", "themes:manage"},
		{"DELETE", "/themes/sources/example", "themes:manage"},
		{"POST", "/themes/sources/example/refresh", "themes:manage"},
		{"GET", "/themes/catalog", ""},
	}
	for _, c := range cases {
		r, ok := match(c.method, c.path)
		if !ok || r.perm != c.perm {
			t.Errorf("%s %s: rule perm=%q ok=%v, want %q", c.method, c.path, r.perm, ok, c.perm)
		}
	}
}

// TestThemeSourceRules_ViewerRefused checks the same routes at request time: a
// signed-in user without themes:manage gets 403, while the catalog stays open.
func TestThemeSourceRules_ViewerRefused(t *testing.T) {
	viewer := &auth.User{ID: 9, Username: "viewer", Role: "viewer"}
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/themes/sources"},
		{http.MethodPost, "/themes/sources"},
		{http.MethodPut, "/themes/sources/example"},
		{http.MethodDelete, "/themes/sources/example"},
		{http.MethodPost, "/themes/sources/example/refresh"},
	} {
		if code := themeSourceServe(t, viewer, c.method, c.target); code != http.StatusForbidden {
			t.Errorf("%s %s: code=%d, want 403", c.method, c.target, code)
		}
	}
	if code := themeSourceServe(t, viewer, http.MethodGet, "/themes/catalog"); code != http.StatusNoContent {
		t.Errorf("GET /themes/catalog: code=%d, want passthrough", code)
	}
}
