package rbac

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// mcpRulesServe sends one request as u through Middleware(nil) and returns the status code.
func mcpRulesServe(t *testing.T, u *auth.User, method, target string) int {
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

func TestMCPRules_MatchReadAndTokenRoutes(t *testing.T) {
	cases := []struct {
		method, path, perm string
	}{
		{"GET", "/pods", "servers:read"},
		{"GET", "/pods/alpha-0", "servers:read"},
		{"GET", "/kube-events", "servers:read"},
		{"GET", "/servers/alpha/pods", "servers:read"},
		{"GET", "/servers/alpha/logs/pod/tail", "servers:read"},
		{"GET", "/users/me/mcp-tokens", ""},
		{"POST", "/users/me/mcp-tokens", ""},
		{"DELETE", "/users/me/mcp-tokens/12", ""},
		{"GET", "/users/12/mcp-tokens", "users:read"},
		{"DELETE", "/users/12/mcp-tokens/3", "users:manage"},
	}
	for _, c := range cases {
		r, ok := match(c.method, c.path)
		if !ok || r.perm != c.perm {
			t.Errorf("%s %s: rule perm=%q ok=%v, want %q", c.method, c.path, r.perm, ok, c.perm)
		}
	}
}

func TestMCPRules_OwnTokenRoutesOpenToAnyUser(t *testing.T) {
	plain := &auth.User{ID: 9, Username: "plain", Role: "viewer"}
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/users/me/mcp-tokens"},
		{http.MethodPost, "/users/me/mcp-tokens"},
		{http.MethodDelete, "/users/me/mcp-tokens/4"},
	} {
		if code := mcpRulesServe(t, plain, c.method, c.target); code != http.StatusNoContent {
			t.Errorf("%s %s: code=%d, want passthrough", c.method, c.target, code)
		}
	}
}

func TestMCPRules_AdminTokenRoutesNeedUsersPermissions(t *testing.T) {
	viewer := clusterWide(RoleViewer, "servers:read")
	reader := clusterWide(RoleOperator, "users:read")
	manager := clusterWide(RoleAdmin, "users:read", "users:manage")

	if code := mcpRulesServe(t, viewer, http.MethodGet, "/users/12/mcp-tokens"); code != http.StatusForbidden {
		t.Errorf("viewer lists another user's tokens: code=%d, want 403", code)
	}
	if code := mcpRulesServe(t, reader, http.MethodGet, "/users/12/mcp-tokens"); code != http.StatusNoContent {
		t.Errorf("users:read lists another user's tokens: code=%d, want passthrough", code)
	}
	if code := mcpRulesServe(t, reader, http.MethodDelete, "/users/12/mcp-tokens/3"); code != http.StatusForbidden {
		t.Errorf("users:read revokes another user's token: code=%d, want 403", code)
	}
	if code := mcpRulesServe(t, manager, http.MethodDelete, "/users/12/mcp-tokens/3"); code != http.StatusNoContent {
		t.Errorf("users:manage revokes another user's token: code=%d, want passthrough", code)
	}
}

func TestMCPRules_PodAndEventReadsNeedServersRead(t *testing.T) {
	viewer := clusterWide(RoleViewer, "servers:read")
	for _, target := range []string{
		"/pods?namespace=" + scope.DefaultNamespace,
		"/pods/alpha-0?namespace=" + scope.DefaultNamespace,
		"/kube-events?namespace=" + scope.DefaultNamespace,
	} {
		if code := mcpRulesServe(t, viewer, http.MethodGet, target); code != http.StatusNoContent {
			t.Errorf("servers:read GET %s: code=%d, want passthrough", target, code)
		}
	}
	nobody := &auth.User{ID: 3, Username: "nobody", Role: "viewer"}
	if code := mcpRulesServe(t, nobody, http.MethodGet, "/pods?namespace="+scope.DefaultNamespace); code != http.StatusForbidden {
		t.Errorf("user without servers:read GET /pods: code=%d, want 403", code)
	}
}

func TestMCPRules_ControlPlaneNamespaceAdminOnlyLocal(t *testing.T) {
	prev := scope.ControlPlaneNamespace
	scope.SetControlPlaneNamespace("gameplane-system")
	t.Cleanup(func() { scope.SetControlPlaneNamespace(prev) })

	admin := clusterWide(RoleAdmin, "*")
	viewer := clusterWide(RoleViewer, "servers:read")
	operator := clusterWide(RoleOperator, "servers:read", "servers:write")

	for _, path := range []string{"/pods", "/kube-events"} {
		target := path + "?namespace=gameplane-system"
		if code := mcpRulesServe(t, admin, http.MethodGet, target); code != http.StatusNoContent {
			t.Errorf("admin GET %s: code=%d, want passthrough", target, code)
		}
		if code := mcpRulesServe(t, viewer, http.MethodGet, target); code != http.StatusForbidden {
			t.Errorf("servers:read GET %s: code=%d, want 403", target, code)
		}
		if code := mcpRulesServe(t, operator, http.MethodGet, target); code != http.StatusForbidden {
			t.Errorf("operator GET %s: code=%d, want 403", target, code)
		}
		remote := target + "&cluster=remote"
		if code := mcpRulesServe(t, admin, http.MethodGet, remote); code != http.StatusForbidden {
			t.Errorf("admin GET %s: code=%d, want 403 on a non-local cluster", remote, code)
		}
	}
}
