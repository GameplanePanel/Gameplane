//go:build envtest

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// newRolesServerHelm mounts the role routes with the given Helm policy.
func newRolesServerHelm(t *testing.T, helm *auth.ProviderPolicy) (*httptest.Server, *db.Store) {
	t.Helper()
	store := newTestStore(t)
	r := chi.NewRouter()
	MountRolesWithHelm(r, store, helm)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, store
}

// putAuthRow stores raw as the "auth" config row and removes it at test end.
func putAuthRow(t *testing.T, store *db.Store, raw string) {
	t.Helper()
	if _, err := store.DB.ExecContext(t.Context(),
		`INSERT INTO config(key, value, updated_at) VALUES ('auth', ?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		raw, db.NowTimestamp()); err != nil {
		t.Fatalf("seed auth row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.DB.ExecContext(t.Context(), `DELETE FROM config WHERE key = 'auth'`)
	})
}

func roleExists(t *testing.T, store *db.Store, name string) bool {
	t.Helper()
	var n int
	if err := store.DB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM roles WHERE name = ?`, name).Scan(&n); err != nil {
		t.Fatalf("query role %s: %v", name, err)
	}
	return n > 0
}

// expectBlocked deletes role and requires 409 naming provider, with the role kept.
func expectBlocked(t *testing.T, srv *httptest.Server, store *db.Store, role, provider string) {
	t.Helper()
	status, body := doReq(t, http.MethodDelete, srv.URL+"/roles/"+role, nil)
	if status != http.StatusConflict {
		t.Fatalf("delete %s: want 409 got %d body=%s", role, status, body)
	}
	if !strings.Contains(string(body), provider) {
		t.Fatalf("delete %s: body %q does not name provider %q", role, body, provider)
	}
	if !roleExists(t, store, role) {
		t.Fatalf("role %s was deleted despite the 409", role)
	}
}

func TestRolesSSOGuard_ProviderRuleBlocksDelete(t *testing.T) {
	srv, store := newRolesServerHelm(t, nil)
	seedRole(t, store, "sso-guard-rule", "servers:read")
	putAuthRow(t, store, `{"providers":[{"name":"authentik","kind":"oidc","enabled":true,`+
		`"roleRules":[{"group":"ops","role":"sso-guard-rule"}]}]}`)
	expectBlocked(t, srv, store, "sso-guard-rule", "authentik")
}

func TestRolesSSOGuard_ProviderDefaultRoleBlocksDelete(t *testing.T) {
	srv, store := newRolesServerHelm(t, nil)
	seedRole(t, store, "sso-guard-default", "servers:read")
	putAuthRow(t, store, `{"providers":[{"name":"keycloak","kind":"oidc","enabled":true,`+
		`"roleRules":[],"defaultRole":"sso-guard-default"}]}`)
	expectBlocked(t, srv, store, "sso-guard-default", "keycloak")
}

func TestRolesSSOGuard_HelmOverrideRuleBlocksDelete(t *testing.T) {
	srv, store := newRolesServerHelm(t, nil)
	seedRole(t, store, "sso-guard-ovr-rule", "servers:read")
	putAuthRow(t, store, `{"providers":[],"helmOverride":{"roleRules":[{"group":"g","role":"sso-guard-ovr-rule"}]}}`)
	expectBlocked(t, srv, store, "sso-guard-ovr-rule", auth.HelmProviderName)
}

func TestRolesSSOGuard_HelmOverrideDefaultRoleBlocksDelete(t *testing.T) {
	srv, store := newRolesServerHelm(t, nil)
	seedRole(t, store, "sso-guard-ovr-def", "servers:read")
	putAuthRow(t, store, `{"providers":[],"helmOverride":{"defaultRole":"sso-guard-ovr-def"}}`)
	expectBlocked(t, srv, store, "sso-guard-ovr-def", auth.HelmProviderName)
}

func TestRolesSSOGuard_HelmPolicyRuleBlocksDelete(t *testing.T) {
	srv, store := newRolesServerHelm(t, &auth.ProviderPolicy{
		Rules: []auth.RoleRule{{Group: "g", Role: "sso-guard-helm-rule"}},
	})
	seedRole(t, store, "sso-guard-helm-rule", "servers:read")
	expectBlocked(t, srv, store, "sso-guard-helm-rule", auth.HelmProviderName)
}

func TestRolesSSOGuard_HelmPolicyDefaultRoleBlocksDelete(t *testing.T) {
	srv, store := newRolesServerHelm(t, &auth.ProviderPolicy{DefaultRole: "sso-guard-helm-def"})
	seedRole(t, store, "sso-guard-helm-def", "servers:read")
	expectBlocked(t, srv, store, "sso-guard-helm-def", auth.HelmProviderName)
}

func TestRolesSSOGuard_UnreferencedRoleStillDeletes(t *testing.T) {
	srv, store := newRolesServerHelm(t, &auth.ProviderPolicy{
		Rules: []auth.RoleRule{{Group: "g", Role: "admin"}}, DefaultRole: "deny",
	})
	seedRole(t, store, "sso-guard-free", "servers:read")
	putAuthRow(t, store, `{"providers":[{"name":"authentik","kind":"oidc","enabled":true,`+
		`"roleRules":[{"group":"ops","role":"operator"}],"defaultRole":"viewer"}],`+
		`"helmOverride":{"roleRules":[{"group":"g","role":"viewer"}]}}`)
	status, body := doReq(t, http.MethodDelete, srv.URL+"/roles/sso-guard-free", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete unreferenced role: want 204 got %d body=%s", status, body)
	}
	if roleExists(t, store, "sso-guard-free") {
		t.Fatal("role still present after 204")
	}
}

func TestRolesSSOGuard_NoAuthRowDeletes(t *testing.T) {
	srv, store := newRolesServerHelm(t, nil)
	seedRole(t, store, "sso-guard-norow", "servers:read")
	status, body := doReq(t, http.MethodDelete, srv.URL+"/roles/sso-guard-norow", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete with no auth row: want 204 got %d body=%s", status, body)
	}
}
