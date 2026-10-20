//go:build envtest

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

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

// Config handler RBAC and helmOverride integration tests.
//
// These tests exercise config.go's PUT /admin/config/auth (with helmOverride
// persistence), GET /admin/config (reflecting overrides via key presence/absence),
// DELETE /admin/config/auth/role-mappings/{role} (resetting role to Helm seed),
// and audit event emission for both paths.
//
// Like capture_rbac_envtest_test.go, these tests build their own chi.Mux
// with rbac.Middleware wired in front of MountConfig, reusing the shared
// envtest apiserver (testEnv/cfg/kubeC) and database (captureAuditStore).

// newConfigRBACRouter builds a chi.Mux with rbac.Middleware wired in
// front of MountConfig, backed by the shared envtest database store
// (captureAuditStore) that suite_envtest_test.go's TestMain already opened.
func newConfigRBACRouter(t *testing.T) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	r.Use(rbac.Middleware(kube.NewRegistry("default")))
	// No Helm OIDC, no storage class, no Helm policy for tests.
	MountConfig(r, captureAuditStore, audit.New(captureAuditStore), false, "", nil)
	return r
}

// doConfigAsUser sends method+path through h with u (nil for unauthenticated)
// injected into the request context via auth.WithUser.
func doConfigAsUser(t *testing.T, h http.Handler, u *auth.User, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		buf = bytes.NewReader(b)
	}
	ctx := t.Context()
	if u != nil {
		ctx = auth.WithUser(ctx, u)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// configAdminUser returns a user with admin role (wildcard "*" permissions).
func configAdminUser() *auth.User {
	return &auth.User{
		ID:       1,
		Username: "config-admin",
		Role:     rbac.RoleAdmin,
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {"*": {}}},
		},
	}
}

// configOperatorUser returns a user with operator role (has servers:* but NOT config:manage).
func configOperatorUser() *auth.User {
	return &auth.User{
		ID:       101,
		Username: "config-operator",
		Role:     rbac.RoleOperator,
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {
				"servers:read":      {},
				"servers:write":     {},
				"servers:console":   {},
				"backups:read":      {},
				"backups:write":     {},
				"schedules:write":   {},
				"templates:read":    {},
				"templates:write":   {},
				"destinations:read": {},
				"modules:read":      {},
				"audit:read":        {},
			}},
		},
	}
}

// configViewerUser returns a user with viewer role (read-only, no config:manage).
func configViewerUser() *auth.User {
	return &auth.User{
		ID:       102,
		Username: "config-viewer",
		Role:     rbac.RoleViewer,
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {
				"servers:read":      {},
				"backups:read":      {},
				"templates:read":    {},
				"destinations:read": {},
				"modules:read":      {},
				"audit:read":        {},
			}},
		},
	}
}

// TestConfigRBAC_PutAuthForbiddenWithoutManagePermission tests that PUT
// /admin/config/auth requires config:manage permission, rejecting operator
// and viewer roles with 403.
func TestConfigRBAC_PutAuthForbiddenWithoutManagePermission(t *testing.T) {
	r := newConfigRBACRouter(t)
	payload := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
		"helmOverride": map[string]any{
			"roleMappings": map[string]any{
				"admin": []string{"helm-admins"},
			},
		},
	}

	for _, testCase := range []struct {
		label string
		user  *auth.User
	}{
		{"operator", configOperatorUser()},
		{"viewer", configViewerUser()},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			rr := doConfigAsUser(t, r, testCase.user, http.MethodPut, "/admin/config/auth", payload)
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s PUT /admin/config/auth = %d, want 403; body=%s",
					testCase.label, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestConfigRBAC_DeleteRoleMappingForbiddenWithoutManagePermission tests that
// DELETE /admin/config/auth/role-mappings/{role} requires config:manage permission,
// rejecting operator and viewer roles with 403.
func TestConfigRBAC_DeleteRoleMappingForbiddenWithoutManagePermission(t *testing.T) {
	r := newConfigRBACRouter(t)

	for _, testCase := range []struct {
		label string
		user  *auth.User
	}{
		{"operator", configOperatorUser()},
		{"viewer", configViewerUser()},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			rr := doConfigAsUser(t, r, testCase.user, http.MethodDelete, "/admin/config/auth/role-mappings/admin", nil)
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s DELETE .../role-mappings/admin = %d, want 403; body=%s",
					testCase.label, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestConfigRBAC_AdminCanManage tests that admin role can successfully PUT
// and DELETE config routes (downstream behavior, not rejection).
func TestConfigRBAC_AdminCanManage(t *testing.T) {
	r := newConfigRBACRouter(t)
	admin := configAdminUser()
	payload := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
		"helmOverride": map[string]any{
			"roleMappings": map[string]any{
				"admin": []string{"helm-admins"},
			},
		},
	}

	// Admin should reach the PUT handler (not 403).
	rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/auth", payload)
	if rr.Code == http.StatusForbidden {
		t.Errorf("admin PUT /admin/config/auth = 403, want non-403; body=%s", rr.Body.String())
	}

	// Admin should reach the DELETE handler (not 403).
	rr = doConfigAsUser(t, r, admin, http.MethodDelete, "/admin/config/auth/role-mappings/admin", nil)
	if rr.Code == http.StatusForbidden {
		t.Errorf("admin DELETE .../role-mappings/admin = 403, want non-403; body=%s", rr.Body.String())
	}
}

// TestConfig_HelmOverridePersistenceAndReflection tests that PUT /admin/config/auth
// persists helmOverride changes and GET /admin/config reflects them via key
// presence/absence (no separate provenance field).
func TestConfig_HelmOverridePersistenceAndReflection(t *testing.T) {
	r := newConfigRBACRouter(t)
	admin := configAdminUser()

	// Step 1: PUT an auth config with helmOverride for admin and viewer roles.
	payload := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
		"helmOverride": map[string]any{
			"roleMappings": map[string]any{
				"admin":  []string{"helm-admins", "superusers"},
				"viewer": []string{}, // Meaningful empty override
			},
		},
	}

	rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/auth", payload)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT /admin/config/auth = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Step 2: GET /admin/config and verify helmOverride is persisted.
	rr = doConfigAsUser(t, r, admin, http.MethodGet, "/admin/config", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/config = %d; body=%s", rr.Code, rr.Body.String())
	}

	var cfg map[string]json.RawMessage
	if err := json.NewDecoder(rr.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	// Extract the auth section and verify key presence.
	authRaw, ok := cfg["auth"]
	if !ok {
		t.Fatalf("auth section missing from config")
	}

	var auth struct {
		Providers    []map[string]any `json:"providers"`
		HelmOverride *struct {
			RoleMappings *struct {
				Admin    *[]string `json:"admin,omitempty"`
				Operator *[]string `json:"operator,omitempty"`
				Viewer   *[]string `json:"viewer,omitempty"`
			} `json:"roleMappings,omitempty"`
		} `json:"helmOverride,omitempty"`
	}
	if err := json.Unmarshal(authRaw, &auth); err != nil {
		t.Fatalf("unmarshal auth: %v", err)
	}

	// Verify key presence signals provenance (no separate "source" field).
	if auth.HelmOverride == nil || auth.HelmOverride.RoleMappings == nil {
		t.Fatalf("helmOverride.roleMappings missing from persisted config")
	}

	// admin key should be present with the overridden values.
	if auth.HelmOverride.RoleMappings.Admin == nil {
		t.Errorf("admin override missing (should be present)")
	} else if len(*auth.HelmOverride.RoleMappings.Admin) != 2 ||
		(*auth.HelmOverride.RoleMappings.Admin)[0] != "helm-admins" ||
		(*auth.HelmOverride.RoleMappings.Admin)[1] != "superusers" {
		t.Errorf("admin override = %v, want [helm-admins superusers]",
			*auth.HelmOverride.RoleMappings.Admin)
	}

	// viewer key should be present with empty slice (meaningful override).
	if auth.HelmOverride.RoleMappings.Viewer == nil {
		t.Errorf("viewer override missing (should be present as empty slice)")
	} else if len(*auth.HelmOverride.RoleMappings.Viewer) != 0 {
		t.Errorf("viewer override = %v, want []",
			*auth.HelmOverride.RoleMappings.Viewer)
	}

	// operator key should be absent (no override for operator role).
	if auth.HelmOverride.RoleMappings.Operator != nil {
		t.Errorf("operator override = %v, want absent (nil)", *auth.HelmOverride.RoleMappings.Operator)
	}
}

// TestConfig_OverrideTakesEffectOnNextLogin tests that overrides take effect
// immediately on the next login without requiring an API restart (SC-007).
// This is a schema/persistence test; the actual login merging behavior (proving
// the override is read per-login, not cached at startup) is covered by unit tests
// in api/internal/auth/oidc_rolemap_test.go and e2e subtests in test/e2e/api_auth_e2e_test.go.
func TestConfig_OverrideTakesEffectOnNextLogin(t *testing.T) {
	r := newConfigRBACRouter(t)
	admin := configAdminUser()

	// PUT an initial override for admin role.
	payload1 := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
		"helmOverride": map[string]any{
			"roleMappings": map[string]any{
				"admin": []string{"initial-admins"},
			},
		},
	}

	rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/auth", payload1)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT (initial) = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Verify the initial override is persisted.
	rr = doConfigAsUser(t, r, admin, http.MethodGet, "/admin/config", nil)
	var cfg1 map[string]json.RawMessage
	json.NewDecoder(rr.Body).Decode(&cfg1)
	var auth1 struct {
		HelmOverride *struct {
			RoleMappings *struct {
				Admin *[]string `json:"admin,omitempty"`
			} `json:"roleMappings,omitempty"`
		} `json:"helmOverride,omitempty"`
	}
	json.Unmarshal(cfg1["auth"], &auth1)
	if auth1.HelmOverride == nil || auth1.HelmOverride.RoleMappings == nil ||
		auth1.HelmOverride.RoleMappings.Admin == nil ||
		len(*auth1.HelmOverride.RoleMappings.Admin) != 1 ||
		(*auth1.HelmOverride.RoleMappings.Admin)[0] != "initial-admins" {
		t.Errorf("initial override not persisted correctly")
	}

	// PUT a second override (different value) for admin role.
	payload2 := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
		"helmOverride": map[string]any{
			"roleMappings": map[string]any{
				"admin": []string{"updated-admins", "extra-admins"},
			},
		},
	}

	rr = doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/auth", payload2)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT (updated) = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Verify the updated override is persisted immediately.
	rr = doConfigAsUser(t, r, admin, http.MethodGet, "/admin/config", nil)
	var cfg2 map[string]json.RawMessage
	json.NewDecoder(rr.Body).Decode(&cfg2)
	var auth2 struct {
		HelmOverride *struct {
			RoleMappings *struct {
				Admin *[]string `json:"admin,omitempty"`
			} `json:"roleMappings,omitempty"`
		} `json:"helmOverride,omitempty"`
	}
	json.Unmarshal(cfg2["auth"], &auth2)
	if auth2.HelmOverride == nil || auth2.HelmOverride.RoleMappings == nil ||
		auth2.HelmOverride.RoleMappings.Admin == nil ||
		len(*auth2.HelmOverride.RoleMappings.Admin) != 2 ||
		(*auth2.HelmOverride.RoleMappings.Admin)[0] != "updated-admins" ||
		(*auth2.HelmOverride.RoleMappings.Admin)[1] != "extra-admins" {
		t.Errorf("updated override not persisted correctly")
	}
}

// TestConfig_DeleteRoleMappingResetsToHelmSeed tests that DELETE
// /admin/config/auth/role-mappings/{role} removes THAT role's override
// and leaves the other roles' overrides untouched.
func TestConfig_DeleteRoleMappingResetsToHelmSeed(t *testing.T) {
	r := newConfigRBACRouter(t)
	admin := configAdminUser()

	// PUT an auth config with overrides for all three roles.
	payload := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
		"helmOverride": map[string]any{
			"roleMappings": map[string]any{
				"admin":    []string{"admin-group"},
				"operator": []string{"op-group"},
				"viewer":   []string{"view-group"},
			},
		},
	}

	rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/auth", payload)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Step 1: DELETE the operator role's override.
	rr = doConfigAsUser(t, r, admin, http.MethodDelete, "/admin/config/auth/role-mappings/operator", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE operator = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Step 2: GET and verify that operator is gone but admin and viewer remain.
	rr = doConfigAsUser(t, r, admin, http.MethodGet, "/admin/config", nil)
	var cfg map[string]json.RawMessage
	json.NewDecoder(rr.Body).Decode(&cfg)

	var auth struct {
		HelmOverride *struct {
			RoleMappings *struct {
				Admin    *[]string `json:"admin,omitempty"`
				Operator *[]string `json:"operator,omitempty"`
				Viewer   *[]string `json:"viewer,omitempty"`
			} `json:"roleMappings,omitempty"`
		} `json:"helmOverride,omitempty"`
	}
	json.Unmarshal(cfg["auth"], &auth)

	if auth.HelmOverride == nil || auth.HelmOverride.RoleMappings == nil {
		t.Fatalf("helmOverride.roleMappings missing after DELETE")
	}

	// Verify admin is still present.
	if auth.HelmOverride.RoleMappings.Admin == nil || len(*auth.HelmOverride.RoleMappings.Admin) != 1 ||
		(*auth.HelmOverride.RoleMappings.Admin)[0] != "admin-group" {
		t.Errorf("admin override was unexpectedly modified: %v",
			auth.HelmOverride.RoleMappings.Admin)
	}

	// Verify operator is removed (nil pointer = no override).
	if auth.HelmOverride.RoleMappings.Operator != nil {
		t.Errorf("operator override should be nil after DELETE, got %v",
			*auth.HelmOverride.RoleMappings.Operator)
	}

	// Verify viewer is still present.
	if auth.HelmOverride.RoleMappings.Viewer == nil || len(*auth.HelmOverride.RoleMappings.Viewer) != 1 ||
		(*auth.HelmOverride.RoleMappings.Viewer)[0] != "view-group" {
		t.Errorf("viewer override was unexpectedly modified: %v",
			auth.HelmOverride.RoleMappings.Viewer)
	}
}

// TestConfig_DeleteRoleMappingIdempotent tests that DELETE is idempotent:
// calling it on a role with no override returns 200 and doesn't emit an audit event.
func TestConfig_DeleteRoleMappingIdempotent(t *testing.T) {
	r := newConfigRBACRouter(t)
	admin := configAdminUser()

	// Start with an empty auth config (no overrides).
	initialPayload := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
	}

	rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/auth", initialPayload)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT initial = %d; body=%s", rr.Code, rr.Body.String())
	}

	// DELETE admin role (which has no override).
	rr = doConfigAsUser(t, r, admin, http.MethodDelete, "/admin/config/auth/role-mappings/admin", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("DELETE admin (no override) = %d, want 200; body=%s",
			rr.Code, rr.Body.String())
	}

	// DELETE again on the same role (should still be 200, idempotent).
	rr = doConfigAsUser(t, r, admin, http.MethodDelete, "/admin/config/auth/role-mappings/admin", nil)
	if rr.Code != http.StatusOK {
		t.Errorf("DELETE admin (second call) = %d, want 200; body=%s",
			rr.Code, rr.Body.String())
	}
}

// TestConfig_DeleteRoleMappingInvalidRole tests that DELETE rejects an
// invalid role name with 400 Bad Request.
func TestConfig_DeleteRoleMappingInvalidRole(t *testing.T) {
	r := newConfigRBACRouter(t)
	admin := configAdminUser()

	rr := doConfigAsUser(t, r, admin, http.MethodDelete, "/admin/config/auth/role-mappings/invalid-role", nil)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("DELETE invalid-role = %d, want 400; body=%s",
			rr.Code, rr.Body.String())
	}

	// Verify the error response is JSON structured.
	var errResp map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&errResp); err != nil {
		t.Errorf("error response is not JSON: %v", err)
	}
	if _, ok := errResp["error"]; !ok {
		t.Errorf("error response missing 'error' field: %v", errResp)
	}
}

// TestConfig_AuditEventsOnPutChange tests that audit events are recorded when
// a PUT /admin/config/auth changes a helmOverride. Assert set membership
// (no exact count checks, per the flake note in the task brief).
func TestConfig_AuditEventsOnPutChange(t *testing.T) {
	r := newConfigRBACRouter(t)
	admin := configAdminUser()

	// Clear any existing audit events.
	_, _ = captureAuditStore.DB.ExecContext(t.Context(), `DELETE FROM audit_events`)

	// PUT an auth config with helmOverride for admin role.
	payload := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
		"helmOverride": map[string]any{
			"roleMappings": map[string]any{
				"admin": []string{"helm-admins"},
			},
		},
	}

	rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/auth", payload)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Query audit events and verify at least one event exists with the expected reason.
	rows, err := captureAuditStore.DB.QueryContext(t.Context(),
		`SELECT reason FROM audit_events WHERE reason LIKE 'oidc role mapping override%'`)
	if err != nil {
		t.Fatalf("query audit events: %v", err)
	}
	defer rows.Close()

	var reasons []string
	for rows.Next() {
		var reason string
		if err := rows.Scan(&reason); err != nil {
			t.Fatalf("scan audit event: %v", err)
		}
		reasons = append(reasons, reason)
	}

	// Assert that at least one event exists with the expected reason pattern.
	found := false
	for _, reason := range reasons {
		if strings.Contains(reason, "oidc role mapping override set") &&
			strings.Contains(reason, "role=admin") &&
			strings.Contains(reason, "groups=helm-admins") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected audit event not found; got reasons: %v", reasons)
	}
}

// TestConfig_AuditEventsOnDeleteChange tests that audit events are recorded when
// a DELETE /admin/config/auth/role-mappings/{role} removes an override.
func TestConfig_AuditEventsOnDeleteChange(t *testing.T) {
	r := newConfigRBACRouter(t)
	admin := configAdminUser()

	// Clear any existing audit events.
	_, _ = captureAuditStore.DB.ExecContext(t.Context(), `DELETE FROM audit_events`)

	// PUT an auth config with an override for admin role.
	payload := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
		"helmOverride": map[string]any{
			"roleMappings": map[string]any{
				"admin": []string{"helm-admins"},
			},
		},
	}

	rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/auth", payload)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Clear audit events again so we only see the DELETE event.
	_, _ = captureAuditStore.DB.ExecContext(t.Context(), `DELETE FROM audit_events`)

	// DELETE the admin role's override.
	rr = doConfigAsUser(t, r, admin, http.MethodDelete, "/admin/config/auth/role-mappings/admin", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Query audit events and verify the DELETE event.
	rows, err := captureAuditStore.DB.QueryContext(t.Context(),
		`SELECT reason FROM audit_events WHERE reason LIKE 'oidc role mapping override reset%'`)
	if err != nil {
		t.Fatalf("query audit events: %v", err)
	}
	defer rows.Close()

	var reasons []string
	for rows.Next() {
		var reason string
		if err := rows.Scan(&reason); err != nil {
			t.Fatalf("scan audit event: %v", err)
		}
		reasons = append(reasons, reason)
	}

	// Assert that at least one event exists with the expected reason pattern.
	found := false
	for _, reason := range reasons {
		if strings.Contains(reason, "oidc role mapping override reset") &&
			strings.Contains(reason, "role=admin") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected audit event not found; got reasons: %v", reasons)
	}
}

// TestConfig_NoAuditEventOnIdempotentDelete tests that no audit event is
// recorded when DELETE is called on a role with no existing override.
func TestConfig_NoAuditEventOnIdempotentDelete(t *testing.T) {
	r := newConfigRBACRouter(t)
	admin := configAdminUser()

	// Clear any existing audit events.
	_, _ = captureAuditStore.DB.ExecContext(t.Context(), `DELETE FROM audit_events`)

	// PUT an auth config with NO overrides.
	payload := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
	}

	rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/auth", payload)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Clear audit events again.
	_, _ = captureAuditStore.DB.ExecContext(t.Context(), `DELETE FROM audit_events`)

	// DELETE admin role (which has no override).
	rr = doConfigAsUser(t, r, admin, http.MethodDelete, "/admin/config/auth/role-mappings/admin", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE = %d; body=%s", rr.Code, rr.Body.String())
	}

	// Query audit events and verify none exist (idempotent DELETE should not emit).
	rows, err := captureAuditStore.DB.QueryContext(t.Context(),
		`SELECT COUNT(*) FROM audit_events WHERE reason LIKE 'oidc role mapping override%'`)
	if err != nil {
		t.Fatalf("query audit events: %v", err)
	}
	defer rows.Close()

	var count int
	if rows.Next() {
		if err := rows.Scan(&count); err != nil {
			t.Fatalf("scan count: %v", err)
		}
	}

	if count > 0 {
		t.Errorf("idempotent DELETE on role with no override emitted %d audit event(s), want 0", count)
	}
}

// newConfigRBACRouterWithSettings builds a chi.Mux with rbac.Middleware and
// configHandler initialized with gameDataStorageClass and helmPolicy.
// Passes the shared captureAuditStore for audit tracking.
func newConfigRBACRouterWithSettings(t *testing.T, gameDataStorageClass string, helmPolicy *auth.ProviderPolicy) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	r.Use(rbac.Middleware(kube.NewRegistry("default")))
	MountConfig(r, captureAuditStore, audit.New(captureAuditStore), helmPolicy != nil, gameDataStorageClass, helmPolicy)
	return r
}

// TestConfig_InstallTimeSettingsWithStorageClass tests that installTimeSettings
// is present and includes the Helm-configured gameDataStorageClass.
func TestConfig_InstallTimeSettingsWithStorageClass(t *testing.T) {
	r := newConfigRBACRouterWithSettings(t, "fast-ssd", nil)
	admin := configAdminUser()

	rr := doConfigAsUser(t, r, admin, http.MethodGet, "/admin/config", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/config = %d; body=%s", rr.Code, rr.Body.String())
	}

	var cfg map[string]json.RawMessage
	if err := json.NewDecoder(rr.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	// Verify installTimeSettings is present.
	installTimeSettingsRaw, ok := cfg["installTimeSettings"]
	if !ok {
		t.Fatalf("installTimeSettings missing from config")
	}

	var installTimeSettings struct {
		GameDataStorageClass string `json:"gameDataStorageClass"`
		OIDCHelmProvider     any    `json:"oidcHelmProvider,omitempty"`
	}
	if err := json.Unmarshal(installTimeSettingsRaw, &installTimeSettings); err != nil {
		t.Fatalf("unmarshal installTimeSettings: %v", err)
	}

	// Verify gameDataStorageClass is correct.
	if installTimeSettings.GameDataStorageClass != "fast-ssd" {
		t.Errorf("gameDataStorageClass = %q, want %q",
			installTimeSettings.GameDataStorageClass, "fast-ssd")
	}

	// Verify oidcHelmProvider is absent (no Helm OIDC configured).
	if installTimeSettings.OIDCHelmProvider != nil {
		t.Errorf("oidcHelmProvider should be absent when Helm OIDC not configured, got %v",
			installTimeSettings.OIDCHelmProvider)
	}
}

// TestConfig_InstallTimeSettingsWithHelmOIDC tests that installTimeSettings
// includes the Helm-configured OIDC provider (groupsClaim, defaultRole, roleMappings).
func TestConfig_InstallTimeSettingsWithHelmOIDC(t *testing.T) {
	helmPolicy := &auth.ProviderPolicy{
		GroupsClaim: "groups",
		DefaultRole: "viewer",
		RoleMappings: &auth.RoleMappings{
			Admin:    []string{"helm-admins"},
			Operator: []string{"helm-ops"},
			Viewer:   []string{"helm-viewers"},
		},
	}
	r := newConfigRBACRouterWithSettings(t, "standard", helmPolicy)
	admin := configAdminUser()

	rr := doConfigAsUser(t, r, admin, http.MethodGet, "/admin/config", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/config = %d; body=%s", rr.Code, rr.Body.String())
	}

	var cfg map[string]json.RawMessage
	if err := json.NewDecoder(rr.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	// Verify installTimeSettings is present.
	installTimeSettingsRaw, ok := cfg["installTimeSettings"]
	if !ok {
		t.Fatalf("installTimeSettings missing from config")
	}

	var installTimeSettings struct {
		GameDataStorageClass string `json:"gameDataStorageClass"`
		OIDCHelmProvider     *struct {
			GroupsClaim  string `json:"groupsClaim"`
			DefaultRole  string `json:"defaultRole"`
			RoleMappings *struct {
				Admin    []string `json:"admin"`
				Operator []string `json:"operator"`
				Viewer   []string `json:"viewer"`
			} `json:"roleMappings"`
		} `json:"oidcHelmProvider,omitempty"`
	}
	if err := json.Unmarshal(installTimeSettingsRaw, &installTimeSettings); err != nil {
		t.Fatalf("unmarshal installTimeSettings: %v", err)
	}

	// Verify gameDataStorageClass.
	if installTimeSettings.GameDataStorageClass != "standard" {
		t.Errorf("gameDataStorageClass = %q, want %q",
			installTimeSettings.GameDataStorageClass, "standard")
	}

	// Verify oidcHelmProvider is present.
	if installTimeSettings.OIDCHelmProvider == nil {
		t.Fatalf("oidcHelmProvider should be present when Helm OIDC configured")
	}

	// Verify groupsClaim and defaultRole.
	if installTimeSettings.OIDCHelmProvider.GroupsClaim != "groups" {
		t.Errorf("groupsClaim = %q, want %q",
			installTimeSettings.OIDCHelmProvider.GroupsClaim, "groups")
	}
	if installTimeSettings.OIDCHelmProvider.DefaultRole != "viewer" {
		t.Errorf("defaultRole = %q, want %q",
			installTimeSettings.OIDCHelmProvider.DefaultRole, "viewer")
	}

	// Verify roleMappings contains the Helm-seeded values.
	if installTimeSettings.OIDCHelmProvider.RoleMappings == nil {
		t.Fatalf("roleMappings should be present")
	}
	if len(installTimeSettings.OIDCHelmProvider.RoleMappings.Admin) != 1 ||
		installTimeSettings.OIDCHelmProvider.RoleMappings.Admin[0] != "helm-admins" {
		t.Errorf("admin roleMappings = %v, want [helm-admins]",
			installTimeSettings.OIDCHelmProvider.RoleMappings.Admin)
	}
	if len(installTimeSettings.OIDCHelmProvider.RoleMappings.Operator) != 1 ||
		installTimeSettings.OIDCHelmProvider.RoleMappings.Operator[0] != "helm-ops" {
		t.Errorf("operator roleMappings = %v, want [helm-ops]",
			installTimeSettings.OIDCHelmProvider.RoleMappings.Operator)
	}
	if len(installTimeSettings.OIDCHelmProvider.RoleMappings.Viewer) != 1 ||
		installTimeSettings.OIDCHelmProvider.RoleMappings.Viewer[0] != "helm-viewers" {
		t.Errorf("viewer roleMappings = %v, want [helm-viewers]",
			installTimeSettings.OIDCHelmProvider.RoleMappings.Viewer)
	}
}

// TestConfig_InstallTimeSettingsUnaffectedByOverride tests that installTimeSettings
// always shows the Helm-seeded values, independent of helmOverride overrides.
// Sets an override via PUT /admin/config/auth, then verifies that installTimeSettings
// still shows the Helm seed while the "auth" section shows the override.
func TestConfig_InstallTimeSettingsUnaffectedByOverride(t *testing.T) {
	helmPolicy := &auth.ProviderPolicy{
		GroupsClaim: "groups",
		DefaultRole: "operator",
		RoleMappings: &auth.RoleMappings{
			Admin:    []string{"helm-admins"},
			Operator: []string{"helm-ops"},
			Viewer:   []string{"helm-viewers"},
		},
	}
	r := newConfigRBACRouterWithSettings(t, "standard", helmPolicy)
	admin := configAdminUser()

	// PUT an auth config with helmOverride to override the admin role.
	overridePayload := map[string]any{
		"providers": []map[string]any{
			{
				"name":        "local",
				"kind":        "local",
				"enabled":     true,
				"displayName": "Local",
			},
		},
		"helmOverride": map[string]any{
			"roleMappings": map[string]any{
				"admin": []string{"override-admins", "extra-admins"},
			},
		},
	}

	rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/auth", overridePayload)
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT /admin/config/auth = %d; body=%s", rr.Code, rr.Body.String())
	}

	// GET /admin/config and verify both sections.
	rr = doConfigAsUser(t, r, admin, http.MethodGet, "/admin/config", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/config = %d; body=%s", rr.Code, rr.Body.String())
	}

	var cfg map[string]json.RawMessage
	if err := json.NewDecoder(rr.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	// Verify the auth section shows the override.
	authRaw, ok := cfg["auth"]
	if !ok {
		t.Fatalf("auth section missing from config")
	}

	var authCfg struct {
		HelmOverride *struct {
			RoleMappings *struct {
				Admin *[]string `json:"admin,omitempty"`
			} `json:"roleMappings,omitempty"`
		} `json:"helmOverride,omitempty"`
	}
	if err := json.Unmarshal(authRaw, &authCfg); err != nil {
		t.Fatalf("unmarshal auth: %v", err)
	}

	// Verify the override is present in auth section.
	if authCfg.HelmOverride == nil || authCfg.HelmOverride.RoleMappings == nil ||
		authCfg.HelmOverride.RoleMappings.Admin == nil {
		t.Fatalf("helmOverride.roleMappings.admin missing from auth section")
	}
	if len(*authCfg.HelmOverride.RoleMappings.Admin) != 2 ||
		(*authCfg.HelmOverride.RoleMappings.Admin)[0] != "override-admins" {
		t.Errorf("auth.helmOverride.roleMappings.admin = %v, want [override-admins extra-admins]",
			*authCfg.HelmOverride.RoleMappings.Admin)
	}

	// Verify installTimeSettings still shows the Helm seed (not the override).
	installTimeSettingsRaw, ok := cfg["installTimeSettings"]
	if !ok {
		t.Fatalf("installTimeSettings missing from config")
	}

	var installTimeSettings struct {
		OIDCHelmProvider *struct {
			RoleMappings *struct {
				Admin []string `json:"admin"`
			} `json:"roleMappings,omitempty"`
		} `json:"oidcHelmProvider,omitempty"`
	}
	if err := json.Unmarshal(installTimeSettingsRaw, &installTimeSettings); err != nil {
		t.Fatalf("unmarshal installTimeSettings: %v", err)
	}

	if installTimeSettings.OIDCHelmProvider == nil ||
		installTimeSettings.OIDCHelmProvider.RoleMappings == nil ||
		installTimeSettings.OIDCHelmProvider.RoleMappings.Admin == nil {
		t.Fatalf("installTimeSettings.oidcHelmProvider.roleMappings.admin missing")
	}

	// The Helm seed should be [helm-admins], NOT the override.
	if len(installTimeSettings.OIDCHelmProvider.RoleMappings.Admin) != 1 ||
		installTimeSettings.OIDCHelmProvider.RoleMappings.Admin[0] != "helm-admins" {
		t.Errorf("installTimeSettings.oidcHelmProvider.roleMappings.admin = %v, want [helm-admins] (Helm seed, not override)",
			installTimeSettings.OIDCHelmProvider.RoleMappings.Admin)
	}
}

// TestConfig_InstallTimeSettingsReadOnly tests that installTimeSettings
// is not writable: PUT /admin/config/installTimeSettings is rejected.
func TestConfig_InstallTimeSettingsReadOnly(t *testing.T) {
	r := newConfigRBACRouterWithSettings(t, "standard", nil)
	admin := configAdminUser()

	payload := map[string]any{
		"gameDataStorageClass": "custom-storage",
	}

	rr := doConfigAsUser(t, r, admin, http.MethodPut, "/admin/config/installTimeSettings", payload)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("PUT /admin/config/installTimeSettings = %d, want 400; body=%s",
			rr.Code, rr.Body.String())
	}

	// Verify the error indicates an unknown section.
	if !strings.Contains(rr.Body.String(), "unknown section") {
		t.Errorf("error response should mention 'unknown section', got: %s", rr.Body.String())
	}
}

// TestConfig_InstallTimeSettingsAbsentWhenNoSettings tests that installTimeSettings
// is not present in the response when neither gameDataStorageClass nor helmPolicy
// are configured.
func TestConfig_InstallTimeSettingsAbsentWhenNoSettings(t *testing.T) {
	r := newConfigRBACRouterWithSettings(t, "", nil)
	admin := configAdminUser()

	rr := doConfigAsUser(t, r, admin, http.MethodGet, "/admin/config", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /admin/config = %d; body=%s", rr.Code, rr.Body.String())
	}

	var cfg map[string]json.RawMessage
	if err := json.NewDecoder(rr.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode config: %v", err)
	}

	// Verify installTimeSettings is absent when no install-time settings are configured.
	if _, ok := cfg["installTimeSettings"]; ok {
		t.Errorf("installTimeSettings should be absent when no install-time settings are configured")
	}
}
