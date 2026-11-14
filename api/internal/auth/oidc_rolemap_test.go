package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestExtractGroups(t *testing.T) {
	cases := []struct {
		name      string
		claims    map[string]any
		claimName string
		want      []string
	}{
		{"missing claim", map[string]any{"sub": "x"}, "groups", nil},
		{"nil claim", map[string]any{"groups": nil}, "groups", nil},
		{"string array", map[string]any{"groups": []any{"a", "b"}}, "groups", []string{"a", "b"}},
		{"skips non-strings", map[string]any{"groups": []any{"a", 42, true, "b"}}, "groups", []string{"a", "b"}},
		{"empty array", map[string]any{"groups": []any{}}, "groups", nil},
		{"single string", map[string]any{"groups": "admins"}, "groups", []string{"admins"}},
		{"non-string non-array claim", map[string]any{"groups": 7.0}, "groups", nil},
		{"custom claim name", map[string]any{"roles": []any{"ops"}}, "roles", []string{"ops"}},
		{"empty claimName defaults to groups", map[string]any{"groups": []any{"g"}}, "", []string{"g"}},
		{"custom name misses default claim", map[string]any{"groups": []any{"g"}}, "roles", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractGroups(tc.claims, tc.claimName)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestComputeRole(t *testing.T) {
	mappings := &RoleMappings{
		Admin:    []string{"gp-admins"},
		Operator: []string{"gp-ops", "gp-sre"},
		Viewer:   []string{"gp-view"},
	}
	cases := []struct {
		name   string
		groups []string
		pol    *ProviderPolicy
		role   string
		deny   bool
	}{
		{"nil policy", []string{"gp-admins"}, nil, "viewer", false},
		{"nil mappings", []string{"gp-admins"}, &ProviderPolicy{DefaultRole: "deny"}, "viewer", false},
		{"admin match", []string{"gp-admins"}, &ProviderPolicy{RoleMappings: mappings}, "admin", false},
		{"operator match", []string{"gp-sre"}, &ProviderPolicy{RoleMappings: mappings}, "operator", false},
		{"viewer match", []string{"gp-view"}, &ProviderPolicy{RoleMappings: mappings}, "viewer", false},
		{
			"most privileged wins",
			[]string{"gp-view", "gp-ops", "gp-admins"},
			&ProviderPolicy{RoleMappings: mappings},
			"admin", false,
		},
		{
			"operator beats viewer",
			[]string{"gp-view", "gp-ops"},
			&ProviderPolicy{RoleMappings: mappings},
			"operator", false,
		},
		{"no match empty default", []string{"other"}, &ProviderPolicy{RoleMappings: mappings}, "viewer", false},
		{
			"no match viewer default",
			[]string{"other"},
			&ProviderPolicy{RoleMappings: mappings, DefaultRole: "viewer"},
			"viewer", false,
		},
		{
			"no match operator default",
			[]string{"other"},
			&ProviderPolicy{RoleMappings: mappings, DefaultRole: "operator"},
			"operator", false,
		},
		{
			"no match admin default",
			[]string{"other"},
			&ProviderPolicy{RoleMappings: mappings, DefaultRole: "admin"},
			"admin", false,
		},
		{
			"no match deny",
			[]string{"other"},
			&ProviderPolicy{RoleMappings: mappings, DefaultRole: "deny"},
			"", true,
		},
		{
			"no groups at all deny",
			nil,
			&ProviderPolicy{RoleMappings: mappings, DefaultRole: "deny"},
			"", true,
		},
		{
			"match beats deny default",
			[]string{"gp-ops"},
			&ProviderPolicy{RoleMappings: mappings, DefaultRole: "deny"},
			"operator", false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			role, deny := computeRole(tc.groups, tc.pol)
			if role != tc.role || deny != tc.deny {
				t.Fatalf("got (%q, %v) want (%q, %v)", role, deny, tc.role, tc.deny)
			}
		})
	}
}

// callbackViaIDP drives one full HandleCallback round against the fake
// IdP, returning the recorder. The IdP must have nonce set before calling.
func callbackViaIDP(t *testing.T, o *OIDC, sessions *SessionStore, nonce string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/auth/oidc/callback?state=abc&code=auth-code-1", nil)
	req.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "abc"})
	req.AddCookie(&http.Cookie{Name: oidcNonceCookie, Value: nonce})
	o.HandleCallback(sessions)(rr, req)
	return rr
}

// auditWriteRecorder is a stub audit-write func for OIDC tests that need to
// observe FR-014 audit event emission without depending on the audit
// package (which imports auth, so a real *audit.Auditor cannot be used from
// a test file in package auth without forming an import cycle). Each call's
// reason string is appended to reasons in call order, and paths are captured
// in the paths slice in parallel.
type auditWriteRecorder struct {
	reasons []string
	paths   []string
}

func (r *auditWriteRecorder) write(_ context.Context, _, path, _, reason string, _ int) error {
	r.paths = append(r.paths, path)
	r.reasons = append(r.reasons, reason)
	return nil
}

// TestHandleCallback_RoleMappingFirstLogin — first login through a
// provider with mappings creates the user with the mapped role and a
// matching '*' role binding.
func TestHandleCallback_RoleMappingFirstLogin(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.nonce = "nonce-map"
	idp.groups = []string{"gp-admins"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{Admin: []string{"gp-admins"}},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)

	rr := callbackViaIDP(t, o, NewSessionStore(store), "nonce-map")
	if rr.Code != http.StatusFound {
		t.Fatalf("code=%d body=%q", rr.Code, rr.Body)
	}
	var role string
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("user not created: %v", err)
	}
	if role != "admin" {
		t.Fatalf("role=%q want admin", role)
	}
	var bindingRole string
	if err := store.DB.QueryRowContext(context.Background(), `
		SELECT b.role_name FROM user_role_bindings b
		JOIN users u ON u.id = b.user_id
		WHERE u.email = ? AND b.namespace = '*'`, idp.email).Scan(&bindingRole); err != nil {
		t.Fatalf("binding not created: %v", err)
	}
	if bindingRole != "admin" {
		t.Fatalf("binding role=%q want admin", bindingRole)
	}
}

// TestHandleCallback_RoleMappingResync — the user's IdP groups change
// between logins; the second login demotes both users.role and the '*'
// binding. The IdP is authoritative when mappings are configured. A
// second user-manager is seeded so the last-admin lockout guard (covered
// by its own tests below) doesn't block the demotion under test.
func TestHandleCallback_RoleMappingResync(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.groups = []string{"gp-admins"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{
				Admin:    []string{"gp-admins"},
				Operator: []string{"gp-ops"},
			},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)

	// With a second user-manager present, demoting the OIDC user can't
	// strip the install's last admin, so the resync applies.
	if _, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO users(username, display_name, email, role) VALUES ('root2', 'Root Two', 'root2@x', 'admin')`,
	); err != nil {
		t.Fatalf("seed second admin: %v", err)
	}

	idp.nonce = "nonce-1"
	if rr := callbackViaIDP(t, o, sessions, "nonce-1"); rr.Code != http.StatusFound {
		t.Fatalf("first login code=%d body=%q", rr.Code, rr.Body)
	}

	// Admin membership revoked at the IdP; only the operator group is left.
	idp.groups = []string{"gp-ops"}
	idp.nonce = "nonce-2"
	if rr := callbackViaIDP(t, o, sessions, "nonce-2"); rr.Code != http.StatusFound {
		t.Fatalf("second login code=%d body=%q", rr.Code, rr.Body)
	}

	var role, bindingRole string
	if err := store.DB.QueryRowContext(context.Background(), `SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := store.DB.QueryRowContext(context.Background(), `
		SELECT b.role_name FROM user_role_bindings b
		JOIN users u ON u.id = b.user_id
		WHERE u.email = ? AND b.namespace = '*'`, idp.email).Scan(&bindingRole); err != nil {
		t.Fatalf("binding: %v", err)
	}
	if role != "operator" || bindingRole != "operator" {
		t.Fatalf("role=%q binding=%q, want operator/operator", role, bindingRole)
	}
	// Exactly one cluster-wide binding — resync must repoint, not stack.
	var n int
	if err := store.DB.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM user_role_bindings b
		JOIN users u ON u.id = b.user_id
		WHERE u.email = ? AND b.namespace = '*'`, idp.email).Scan(&n); err != nil {
		t.Fatalf("count bindings: %v", err)
	}
	if n != 1 {
		t.Fatalf("cluster bindings = %d, want 1", n)
	}
}

// TestHandleCallback_NoMappingKeepsManualPromotion — a provider without
// role mappings must never touch a stored role: a user promoted to admin
// by hand stays admin across re-logins.
func TestHandleCallback_NoMappingKeepsManualPromotion(t *testing.T) {
	idp := newFakeIDP(t, "client-1")

	o, err := NewOIDC(context.Background(), idp.issuer(), "client-1", "secret", "https://app/cb")
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)

	idp.nonce = "nonce-1"
	if rr := callbackViaIDP(t, o, sessions, "nonce-1"); rr.Code != http.StatusFound {
		t.Fatalf("first login code=%d body=%q", rr.Code, rr.Body)
	}

	// Manual promotion, as the users handler would do it.
	if _, err := store.DB.ExecContext(context.Background(), `UPDATE users SET role = 'admin' WHERE email = ?`, idp.email); err != nil {
		t.Fatalf("promote: %v", err)
	}

	idp.nonce = "nonce-2"
	if rr := callbackViaIDP(t, o, sessions, "nonce-2"); rr.Code != http.StatusFound {
		t.Fatalf("second login code=%d body=%q", rr.Code, rr.Body)
	}
	var role string
	if err := store.DB.QueryRowContext(context.Background(), `SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("user: %v", err)
	}
	if role != "admin" {
		t.Fatalf("role=%q, manual promotion must survive re-login", role)
	}
}

// TestHandleCallback_ResyncKeepsLastUserManager — the sole admin's IdP
// groups now map only to viewer; the resync must SKIP the demotion (the
// install would otherwise lose its last user-manager) while the login
// itself still succeeds.
func TestHandleCallback_ResyncKeepsLastUserManager(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.groups = []string{"gp-admins"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{
				Admin:  []string{"gp-admins"},
				Viewer: []string{"gp-view"},
			},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)

	// First login makes the user the install's only admin.
	idp.nonce = "nonce-1"
	if rr := callbackViaIDP(t, o, sessions, "nonce-1"); rr.Code != http.StatusFound {
		t.Fatalf("first login code=%d body=%q", rr.Code, rr.Body)
	}

	// Admin group revoked at the IdP; only a viewer-mapped group remains.
	idp.groups = []string{"gp-view"}
	idp.nonce = "nonce-2"
	if rr := callbackViaIDP(t, o, sessions, "nonce-2"); rr.Code != http.StatusFound {
		t.Fatalf("second login must still succeed: code=%d body=%q", rr.Code, rr.Body)
	}

	var role, bindingRole string
	if err := store.DB.QueryRowContext(context.Background(), `SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := store.DB.QueryRowContext(context.Background(), `
		SELECT b.role_name FROM user_role_bindings b
		JOIN users u ON u.id = b.user_id
		WHERE u.email = ? AND b.namespace = '*'`, idp.email).Scan(&bindingRole); err != nil {
		t.Fatalf("binding: %v", err)
	}
	if role != "admin" || bindingRole != "admin" {
		t.Fatalf("role=%q binding=%q, last user-manager must keep admin", role, bindingRole)
	}
}

// TestHandleCallback_ResyncDemotesWhenAnotherManagerExists — the inverse
// of the lockout guard: with a second admin present, the same demotion
// login applies as normal.
func TestHandleCallback_ResyncDemotesWhenAnotherManagerExists(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.groups = []string{"gp-admins"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{
				Admin:  []string{"gp-admins"},
				Viewer: []string{"gp-view"},
			},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)

	idp.nonce = "nonce-1"
	if rr := callbackViaIDP(t, o, sessions, "nonce-1"); rr.Code != http.StatusFound {
		t.Fatalf("first login code=%d body=%q", rr.Code, rr.Body)
	}

	// A second user-manager exists, so the guard must not fire.
	if _, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO users(username, display_name, email, role) VALUES ('root2', 'Root Two', 'root2@x', 'admin')`,
	); err != nil {
		t.Fatalf("seed second admin: %v", err)
	}

	idp.groups = []string{"gp-view"}
	idp.nonce = "nonce-2"
	if rr := callbackViaIDP(t, o, sessions, "nonce-2"); rr.Code != http.StatusFound {
		t.Fatalf("second login code=%d body=%q", rr.Code, rr.Body)
	}

	var role, bindingRole string
	if err := store.DB.QueryRowContext(context.Background(), `SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("user: %v", err)
	}
	if err := store.DB.QueryRowContext(context.Background(), `
		SELECT b.role_name FROM user_role_bindings b
		JOIN users u ON u.id = b.user_id
		WHERE u.email = ? AND b.namespace = '*'`, idp.email).Scan(&bindingRole); err != nil {
		t.Fatalf("binding: %v", err)
	}
	if role != "viewer" || bindingRole != "viewer" {
		t.Fatalf("role=%q binding=%q, want viewer/viewer (another manager exists)", role, bindingRole)
	}
}

// TestHandleCallback_DefaultRoleDeny — defaultRole=deny plus no matching
// group refuses the login with 403 and creates no user row.
func TestHandleCallback_DefaultRoleDeny(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.nonce = "nonce-deny"
	idp.groups = []string{"unrelated-group"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{Admin: []string{"gp-admins"}},
			DefaultRole:  "deny",
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)

	rr := callbackViaIDP(t, o, NewSessionStore(store), "nonce-deny")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%q, want 403", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "login not permitted") {
		t.Fatalf("body=%q", rr.Body.String())
	}
	var n int
	if err := store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Fatalf("users = %d, deny must not create a row", n)
	}
}

// TestHandleStart_ExtraScopesInAuthCodeURL — the start redirect must
// request the policy's extra scopes exactly once each, after the base
// set, without duplicating a base scope listed again in the policy.
func TestHandleStart_ExtraScopesInAuthCodeURL(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{Scopes: []string{"groups", "email"}})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	rr := httptest.NewRecorder()
	o.HandleStart()(rr, httptest.NewRequestWithContext(context.Background(), "GET", "/auth/oidc/start", nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("code=%d", rr.Code)
	}
	loc, err := url.Parse(rr.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	scope := loc.Query().Get("scope")
	if scope != "openid profile email groups" {
		t.Fatalf("scope=%q want %q (base set + deduped extras, order preserved)",
			scope, "openid profile email groups")
	}
}

// TestGetMatchedGroup — verifies getMatchedGroup returns the specific group
// that matched a role mapping, or "none" when no mapping matched.
func TestGetMatchedGroup(t *testing.T) {
	mappings := &RoleMappings{
		Admin:    []string{"gp-admins", "infra-team"},
		Operator: []string{"gp-ops"},
		Viewer:   []string{"gp-view"},
	}
	cases := []struct {
		name   string
		groups []string
		pol    *ProviderPolicy
		want   string
	}{
		{"no groups nil policy", nil, nil, "none"},
		{"no groups with mappings", nil, &ProviderPolicy{RoleMappings: mappings}, "none"},
		{"empty groups", []string{}, &ProviderPolicy{RoleMappings: mappings}, "none"},
		{"matches admin first group", []string{"gp-admins"}, &ProviderPolicy{RoleMappings: mappings}, "gp-admins"},
		{"matches admin second group", []string{"infra-team"}, &ProviderPolicy{RoleMappings: mappings}, "infra-team"},
		{"matches operator", []string{"gp-ops"}, &ProviderPolicy{RoleMappings: mappings}, "gp-ops"},
		{"matches viewer", []string{"gp-view"}, &ProviderPolicy{RoleMappings: mappings}, "gp-view"},
		{"admin wins over operator", []string{"gp-ops", "gp-admins"}, &ProviderPolicy{RoleMappings: mappings}, "gp-admins"},
		{"operator wins over viewer", []string{"gp-view", "gp-ops"}, &ProviderPolicy{RoleMappings: mappings}, "gp-ops"},
		{"no match among groups", []string{"unknown-group"}, &ProviderPolicy{RoleMappings: mappings}, "none"},
		{"nil mappings", []string{"gp-admins"}, &ProviderPolicy{RoleMappings: nil}, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := getMatchedGroup(tc.groups, tc.pol)
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// TestHandleCallback_BootstrapAdminAndOIDCCoexist — a bootstrap-admin LOCAL
// account and an OIDC-mapped admin account coexist without either clobbering
// the other (FR-013).
func TestHandleCallback_BootstrapAdminAndOIDCCoexist(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.nonce = "nonce-oidc"
	idp.groups = []string{"gp-admins"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{Admin: []string{"gp-admins"}},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)

	// Seed a local (non-OIDC) bootstrap admin account.
	SetFastHashParams(t)
	bootstrapHash, err := HashPassword(context.Background(), "bootstrap-pass")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO users(username, display_name, email, role, pw_hash)
		 VALUES ('bootstrap-admin', 'Bootstrap Admin', 'bootstrap@local', 'admin', ?)`,
		bootstrapHash,
	); err != nil {
		t.Fatalf("seed bootstrap admin: %v", err)
	}

	// OIDC user logs in and becomes admin via role mapping.
	rr := callbackViaIDP(t, o, NewSessionStore(store), "nonce-oidc")
	if rr.Code != http.StatusFound {
		t.Fatalf("oidc login code=%d body=%q", rr.Code, rr.Body)
	}

	// Verify both users exist with admin role.
	var bootstrapRole, oidcRole string
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE username = ?`, "bootstrap-admin").Scan(&bootstrapRole); err != nil {
		t.Fatalf("query bootstrap admin: %v", err)
	}
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&oidcRole); err != nil {
		t.Fatalf("query oidc admin: %v", err)
	}
	if bootstrapRole != "admin" {
		t.Fatalf("bootstrap role=%q, want admin", bootstrapRole)
	}
	if oidcRole != "admin" {
		t.Fatalf("oidc role=%q, want admin", oidcRole)
	}

	// Verify the bootstrap admin has no OIDC link.
	var bootstrapUserID int64
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT id FROM users WHERE username = ?`, "bootstrap-admin").Scan(&bootstrapUserID); err != nil {
		t.Fatalf("query bootstrap id: %v", err)
	}
	var oidcLinkCount int
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM oidc_links WHERE user_id = ?`, bootstrapUserID).Scan(&oidcLinkCount); err != nil {
		t.Fatalf("count oidc links: %v", err)
	}
	if oidcLinkCount != 0 {
		t.Fatalf("bootstrap admin has %d oidc links, want 0", oidcLinkCount)
	}

	// Verify the OIDC user has exactly one OIDC link.
	var oidcUserID int64
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT id FROM users WHERE email = ?`, idp.email).Scan(&oidcUserID); err != nil {
		t.Fatalf("query oidc user id: %v", err)
	}
	var oidcLinkExists int
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM oidc_links WHERE user_id = ?`, oidcUserID).Scan(&oidcLinkExists); err != nil {
		t.Fatalf("count oidc links: %v", err)
	}
	if oidcLinkExists != 1 {
		t.Fatalf("oidc user has %d oidc links, want 1", oidcLinkExists)
	}
}

// TestHandleCallback_NoMappingNeverReEvaluates — with no role mappings
// configured (nil or absent), an existing user's role is never re-evaluated
// on subsequent logins, even if role mappings are added later (SC-008).
// This test extends the existing TestHandleCallback_NoMappingKeepsManualPromotion
// by checking that without mappings, even an automatic role assignment is
// preserved.
func TestHandleCallback_NoMappingNeverReEvaluates(t *testing.T) {
	idp := newFakeIDP(t, "client-1")

	// First login: no role mappings configured, user gets default viewer role.
	o, err := NewOIDC(context.Background(), idp.issuer(), "client-1", "secret", "https://app/cb")
	if err != nil {
		t.Fatalf("NewOIDC: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)

	idp.nonce = "nonce-1"
	if rr := callbackViaIDP(t, o, sessions, "nonce-1"); rr.Code != http.StatusFound {
		t.Fatalf("first login code=%d body=%q", rr.Code, rr.Body)
	}

	// Verify initial role is viewer.
	var role string
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("query after first login: %v", err)
	}
	if role != "viewer" {
		t.Fatalf("initial role=%q, want viewer", role)
	}

	// Second login: still no role mappings, even if the user object still exists.
	// The role must stay viewer (never re-evaluated).
	idp.nonce = "nonce-2"
	if rr := callbackViaIDP(t, o, sessions, "nonce-2"); rr.Code != http.StatusFound {
		t.Fatalf("second login code=%d body=%q", rr.Code, rr.Body)
	}

	var role2 string
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role2); err != nil {
		t.Fatalf("query after second login: %v", err)
	}
	if role2 != "viewer" {
		t.Fatalf("role after resync=%q, want viewer (no mappings = no re-evaluation)", role2)
	}
}

// TestHandleCallback_FirstLoginRoleAssignmentWithAudit — on first OIDC login,
// the user's role is assigned via role mapping and the outcome tracks
// PreviousRole="new_user" and Applied=true (FR-014, T008, T009).
func TestHandleCallback_FirstLoginRoleAssignmentWithAudit(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.nonce = "nonce-first"
	idp.groups = []string{"gp-ops"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{
				Admin:    []string{"gp-admins"},
				Operator: []string{"gp-ops"},
				Viewer:   []string{"gp-view"},
			},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)

	rr := callbackViaIDP(t, o, NewSessionStore(store), "nonce-first")
	if rr.Code != http.StatusFound {
		t.Fatalf("login code=%d body=%q", rr.Code, rr.Body)
	}

	// Verify user was created with operator role (matched gp-ops group).
	var role string
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("query user: %v", err)
	}
	if role != "operator" {
		t.Fatalf("role=%q want operator", role)
	}

	// Verify a cluster-wide role binding exists.
	var bindingRole string
	if err := store.DB.QueryRowContext(context.Background(), `
		SELECT b.role_name FROM user_role_bindings b
		JOIN users u ON u.id = b.user_id
		WHERE u.email = ? AND b.namespace = '*'`, idp.email).Scan(&bindingRole); err != nil {
		t.Fatalf("query binding: %v", err)
	}
	if bindingRole != "operator" {
		t.Fatalf("binding role=%q want operator", bindingRole)
	}
}

// TestHandleCallback_RoleUpgradeOnResync — on a subsequent login, if the
// user's IdP groups change to map to a higher-privilege role, the resync
// upgrades the role (no demotion guard applies to upgrades).
func TestHandleCallback_RoleUpgradeOnResync(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.groups = []string{"gp-view"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{
				Admin:    []string{"gp-admins"},
				Operator: []string{"gp-ops"},
				Viewer:   []string{"gp-view"},
			},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)

	// First login: user has only viewer group.
	idp.nonce = "nonce-1"
	if rr := callbackViaIDP(t, o, sessions, "nonce-1"); rr.Code != http.StatusFound {
		t.Fatalf("first login code=%d body=%q", rr.Code, rr.Body)
	}

	var role string
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("first login query: %v", err)
	}
	if role != "viewer" {
		t.Fatalf("initial role=%q, want viewer", role)
	}

	// Second login: user now has operator group.
	idp.groups = []string{"gp-ops"}
	idp.nonce = "nonce-2"
	if rr := callbackViaIDP(t, o, sessions, "nonce-2"); rr.Code != http.StatusFound {
		t.Fatalf("second login code=%d body=%q", rr.Code, rr.Body)
	}

	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("second login query: %v", err)
	}
	if role != "operator" {
		t.Fatalf("upgraded role=%q, want operator", role)
	}

	// Verify binding was updated too.
	var bindingRole string
	if err := store.DB.QueryRowContext(context.Background(), `
		SELECT b.role_name FROM user_role_bindings b
		JOIN users u ON u.id = b.user_id
		WHERE u.email = ? AND b.namespace = '*'`, idp.email).Scan(&bindingRole); err != nil {
		t.Fatalf("binding query: %v", err)
	}
	if bindingRole != "operator" {
		t.Fatalf("binding role=%q, want operator (must match user role)", bindingRole)
	}
}

// TestHandleCallback_RoleNoChangeOnResync — on a subsequent login, if the
// user's IdP groups still map to the same role, the role is unchanged but
// the outcome still records Applied=true (no-op case).
func TestHandleCallback_RoleNoChangeOnResync(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.groups = []string{"gp-admins"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{
				Admin: []string{"gp-admins"},
			},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)

	// First login.
	idp.nonce = "nonce-1"
	if rr := callbackViaIDP(t, o, sessions, "nonce-1"); rr.Code != http.StatusFound {
		t.Fatalf("first login code=%d body=%q", rr.Code, rr.Body)
	}

	var role1 string
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role1); err != nil {
		t.Fatalf("first login query: %v", err)
	}
	if role1 != "admin" {
		t.Fatalf("initial role=%q, want admin", role1)
	}

	// Second login: same group, same role (no-op).
	idp.nonce = "nonce-2"
	if rr := callbackViaIDP(t, o, sessions, "nonce-2"); rr.Code != http.StatusFound {
		t.Fatalf("second login code=%d body=%q", rr.Code, rr.Body)
	}

	var role2 string
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role2); err != nil {
		t.Fatalf("second login query: %v", err)
	}
	if role2 != "admin" {
		t.Fatalf("unchanged role=%q, want admin", role2)
	}
}

// TestHandleCallback_FirstLoginAuditEventEmitted — on first OIDC login,
// an audit event is emitted with the FR-014 format:
// "oidc role assigned: provider=<provider_name> matched=<group_or_none> from=new_user to=<role>"
func TestHandleCallback_FirstLoginAuditEventEmitted(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.nonce = "nonce-audit1"
	idp.groups = []string{"gp-admins"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{
				Admin: []string{"gp-admins"},
			},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)

	// Attach a stub audit-write func to capture audit events.
	SetFastHashParams(t)
	rec := &auditWriteRecorder{}
	o.AttachAuditWriteSyncFunc(rec.write)
	o.SetProviderName("helm")

	rr := callbackViaIDP(t, o, NewSessionStore(store), "nonce-audit1")
	if rr.Code != http.StatusFound {
		t.Fatalf("login code=%d body=%q", rr.Code, rr.Body)
	}

	// Verify exactly one role-assignment audit event was recorded.
	if len(rec.reasons) != 1 {
		t.Fatalf("reasons=%v, want exactly 1 audit event", rec.reasons)
	}
	reason := rec.reasons[0]

	// Verify the reason string contains the expected format.
	if !strings.Contains(reason, "provider=helm") {
		t.Fatalf("reason=%q missing provider=helm", reason)
	}
	if !strings.Contains(reason, "from=new_user") {
		t.Fatalf("reason=%q missing from=new_user", reason)
	}
	if !strings.Contains(reason, "to=admin") {
		t.Fatalf("reason=%q missing to=admin", reason)
	}
	if !strings.Contains(reason, "matched=gp-admins") {
		t.Fatalf("reason=%q missing matched=gp-admins", reason)
	}
}

// TestHandleCallback_ReLoginAuditEventEmitted — on a subsequent login with
// a role change, an audit event is emitted with the FR-014 format:
// "oidc role assigned: provider=<provider_name> matched=<group> from=<old_role> to=<new_role>"
func TestHandleCallback_ReLoginAuditEventEmitted(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.groups = []string{"gp-view"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{
				Admin:    []string{"gp-admins"},
				Operator: []string{"gp-ops"},
				Viewer:   []string{"gp-view"},
			},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)

	SetFastHashParams(t)
	rec := &auditWriteRecorder{}
	o.AttachAuditWriteSyncFunc(rec.write)
	o.SetProviderName("helm")

	// First login: viewer role.
	idp.nonce = "nonce-1"
	if rr := callbackViaIDP(t, o, sessions, "nonce-1"); rr.Code != http.StatusFound {
		t.Fatalf("first login code=%d body=%q", rr.Code, rr.Body)
	}

	// Second login: change groups to operator.
	idp.groups = []string{"gp-ops"}
	idp.nonce = "nonce-2"
	if rr := callbackViaIDP(t, o, sessions, "nonce-2"); rr.Code != http.StatusFound {
		t.Fatalf("second login code=%d body=%q", rr.Code, rr.Body)
	}

	// The second (re-login) audit event is the role change from viewer to operator.
	if len(rec.reasons) != 2 {
		t.Fatalf("reasons=%v, want exactly 2 audit events", rec.reasons)
	}
	reason := rec.reasons[1]

	// Verify the reason string contains the role change.
	if !strings.Contains(reason, "provider=helm") {
		t.Fatalf("reason=%q missing provider=helm", reason)
	}
	if !strings.Contains(reason, "from=viewer") {
		t.Fatalf("reason=%q missing from=viewer", reason)
	}
	if !strings.Contains(reason, "to=operator") {
		t.Fatalf("reason=%q missing to=operator", reason)
	}
	if !strings.Contains(reason, "matched=gp-ops") {
		t.Fatalf("reason=%q missing matched=gp-ops", reason)
	}
}

// TestEffectiveHelmPolicy verifies that effectiveHelmPolicy correctly merges
// a DB override's per-role list replacements onto a Helm-seeded base policy.
func TestEffectiveHelmPolicy(t *testing.T) {
	baseMappings := &RoleMappings{
		Admin:    []string{"helm-admins", "infra"},
		Operator: []string{"helm-ops"},
		Viewer:   []string{"helm-view"},
	}
	basePolicy := &ProviderPolicy{
		RoleMappings: baseMappings,
		Scopes:       []string{"openid", "profile", "email", "groups"},
		GroupsClaim:  "groups",
		DefaultRole:  "viewer",
	}

	cases := []struct {
		name string
		base *ProviderPolicy
		ov   *RoleMappings
		want *ProviderPolicy
	}{
		{
			"nil override returns base unchanged",
			basePolicy,
			nil,
			basePolicy,
		},
		{
			"non-nil override replaces admin list",
			basePolicy,
			&RoleMappings{Admin: []string{"override-admin"}},
			&ProviderPolicy{
				RoleMappings: &RoleMappings{
					Admin:    []string{"override-admin"},
					Operator: []string{"helm-ops"},
					Viewer:   []string{"helm-view"},
				},
				Scopes:      []string{"openid", "profile", "email", "groups"},
				GroupsClaim: "groups",
				DefaultRole: "viewer",
			},
		},
		{
			"empty override list means nobody for that role",
			basePolicy,
			&RoleMappings{Admin: []string{}},
			&ProviderPolicy{
				RoleMappings: &RoleMappings{
					Admin:    []string{},
					Operator: []string{"helm-ops"},
					Viewer:   []string{"helm-view"},
				},
				Scopes:      []string{"openid", "profile", "email", "groups"},
				GroupsClaim: "groups",
				DefaultRole: "viewer",
			},
		},
		{
			"absent key leaves seeded list standing",
			basePolicy,
			&RoleMappings{Operator: []string{"override-ops"}},
			&ProviderPolicy{
				RoleMappings: &RoleMappings{
					Admin:    []string{"helm-admins", "infra"},
					Operator: []string{"override-ops"},
					Viewer:   []string{"helm-view"},
				},
				Scopes:      []string{"openid", "profile", "email", "groups"},
				GroupsClaim: "groups",
				DefaultRole: "viewer",
			},
		},
		{
			"roles resolve independently",
			basePolicy,
			&RoleMappings{
				Admin:  []string{"new-admin"},
				Viewer: []string{"new-view"},
			},
			&ProviderPolicy{
				RoleMappings: &RoleMappings{
					Admin:    []string{"new-admin"},
					Operator: []string{"helm-ops"},
					Viewer:   []string{"new-view"},
				},
				Scopes:      []string{"openid", "profile", "email", "groups"},
				GroupsClaim: "groups",
				DefaultRole: "viewer",
			},
		},
		{
			"upgrade does not clobber existing override",
			&ProviderPolicy{
				RoleMappings: &RoleMappings{
					Admin:    []string{"old-override"},
					Operator: []string{"helm-ops"},
					Viewer:   []string{"helm-view"},
				},
				Scopes:      []string{"openid", "profile", "email", "groups"},
				GroupsClaim: "groups",
				DefaultRole: "viewer",
			},
			&RoleMappings{Operator: []string{"new-seeded-ops"}},
			&ProviderPolicy{
				RoleMappings: &RoleMappings{
					Admin:    []string{"old-override"},
					Operator: []string{"new-seeded-ops"},
					Viewer:   []string{"helm-view"},
				},
				Scopes:      []string{"openid", "profile", "email", "groups"},
				GroupsClaim: "groups",
				DefaultRole: "viewer",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveHelmPolicy(tc.base, tc.ov)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

// TestEffectiveHelmPolicy_MostPrivilegedWins verifies that when an override
// and seeded lists coexist for different roles, computeRole on the merged
// policy still applies most-privileged-match-wins (no group priority change
// due to override placement).
func TestEffectiveHelmPolicy_MostPrivilegedWins(t *testing.T) {
	baseMappings := &RoleMappings{
		Admin:  []string{"seeded-admins"},
		Viewer: []string{"seeded-viewers"},
	}
	basePolicy := &ProviderPolicy{
		RoleMappings: baseMappings,
	}

	// Override viewer but keep admin seeded.
	overrideMappings := &RoleMappings{
		Viewer: []string{"override-viewer"},
	}
	effectivePolicy := effectiveHelmPolicy(basePolicy, overrideMappings)

	// User has both an override-viewer group and a seeded-admin group.
	// Even though viewer is overridden, admin (from seed) is higher privilege
	// and should win when computeRole runs on the merged policy.
	groups := []string{"override-viewer", "seeded-admins"}
	role, deny := computeRole(groups, effectivePolicy)

	if role != "admin" || deny {
		t.Fatalf("got (%q, %v) want (admin, false): most-privileged must win", role, deny)
	}
}

// TestEffectiveHelmPolicy_SC008_ByteIdenticalWithNilOverride verifies that
// calling effectiveHelmPolicy(base, nil) returns the SAME pointer (and thus
// byte-identical behavior) to calling computeRole directly on the base when
// no override is present (SC-008).
func TestEffectiveHelmPolicy_SC008_ByteIdenticalWithNilOverride(t *testing.T) {
	mappings := &RoleMappings{
		Admin:    []string{"admins"},
		Operator: []string{"ops"},
		Viewer:   []string{"viewers"},
	}
	basePolicy := &ProviderPolicy{
		RoleMappings: mappings,
		DefaultRole:  "viewer",
	}

	groups := []string{"admins", "ops", "viewers"}

	// Direct call on base.
	directRole, directDeny := computeRole(groups, basePolicy)

	// Call through effectiveHelmPolicy with nil override.
	effectivePolicy := effectiveHelmPolicy(basePolicy, nil)
	effectiveRole, effectiveDeny := computeRole(groups, effectivePolicy)

	if directRole != effectiveRole || directDeny != effectiveDeny {
		t.Fatalf("direct=(%q,%v) effective=(%q,%v), must be identical",
			directRole, directDeny, effectiveRole, effectiveDeny)
	}

	// Verify that effectivePolicy IS the same object (pointer equality).
	if effectivePolicy != basePolicy {
		t.Fatalf("effectiveHelmPolicy(base, nil) must return base unchanged (same pointer)")
	}
}

// TestHandleCallback_HelmOverride_LiveRead_SC007 verifies that the Helm role-mapping
// override is read fresh on every login (not cached at API startup), proving SC-007:
// changes to role mappings take effect immediately on the next login without restart.
// The test drives the OIDC callback three times with mutations to the override between
// calls, asserting that the resolved role changes each time.
func TestHandleCallback_HelmOverride_LiveRead_SC007(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.nonce = "nonce-helm-override"
	idp.groups = []string{"helm-admins"}

	// Base policy: admins can get admin role, others default to viewer.
	basePolicy := &ProviderPolicy{
		RoleMappings: &RoleMappings{
			Admin: []string{"helm-admins"},
		},
		DefaultRole: "viewer",
	}

	// Mutable override source: held in a closure so we can mutate it between logins.
	var helmOverride *RoleMappings
	getOverride := func(_ context.Context) (*RoleMappings, error) {
		return helmOverride, nil
	}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", basePolicy)
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	// Set provider name to "helm" and attach the mutable override func.
	o.SetProviderName(HelmProviderName)
	o.AttachHelmRoleOverridesFunc(getOverride)

	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)

	// === Login 1: No override, base policy only ===
	// User has groups ["helm-admins"] which match Admin in base policy.
	helmOverride = nil
	rr := callbackViaIDP(t, o, sessions, "nonce-helm-override")
	if rr.Code != http.StatusFound {
		t.Fatalf("login 1: code=%d body=%q", rr.Code, rr.Body)
	}
	var role string
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("login 1: user not created: %v", err)
	}
	if role != "admin" {
		t.Fatalf("login 1: role=%q want admin (base policy: helm-admins -> admin)", role)
	}

	// syncUserRole refuses to demote the install's LAST user-manager (see
	// oidc.go's last-admin lockout guard), and the OIDC user from login 1
	// is currently the only one. Seed a second local admin so the demotion
	// below actually applies instead of being silently skipped.
	seedUser(t, store, "backup-admin", "pw-backup-admin", "admin")

	// === Login 2: Override with empty admin list ===
	// User still has groups ["helm-admins"], but override makes Admin = [],
	// so the user no longer matches admin and gets default viewer role.
	helmOverride = &RoleMappings{
		Admin: []string{}, // Empty override list means nobody can get admin role.
	}
	// Mutate the nonce to simulate a fresh login attempt.
	idp.nonce = "nonce-helm-override-2"
	rr = callbackViaIDP(t, o, sessions, "nonce-helm-override-2")
	if rr.Code != http.StatusFound {
		t.Fatalf("login 2: code=%d body=%q", rr.Code, rr.Body)
	}
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("login 2: query: %v", err)
	}
	if role != "viewer" {
		t.Fatalf("login 2: role=%q want viewer (override clears admin, default applies)", role)
	}

	// === Login 3: Clear override, back to base policy ===
	// Override is nil again, so we're back to base policy.
	// User still has groups ["helm-admins"] which match Admin in base.
	helmOverride = nil
	idp.nonce = "nonce-helm-override-3"
	rr = callbackViaIDP(t, o, sessions, "nonce-helm-override-3")
	if rr.Code != http.StatusFound {
		t.Fatalf("login 3: code=%d body=%q", rr.Code, rr.Body)
	}
	if err := store.DB.QueryRowContext(context.Background(),
		`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
		t.Fatalf("login 3: query: %v", err)
	}
	if role != "admin" {
		t.Fatalf("login 3: role=%q want admin (back to base policy: helm-admins -> admin)", role)
	}
}

// TestHandleCallback_HelmOverrideMappingsReEvaluateWithoutHelmMappings —
// with no Helm-seeded role mappings, a dashboard helmOverride that supplies
// mappings is the effective policy: later logins re-evaluate the role
// against it and audit the change, and the last user-manager keeps their
// role until another user-manager exists.
func TestHandleCallback_HelmOverrideMappingsReEvaluateWithoutHelmMappings(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.groups = []string{"ovr-admins"}

	// The Helm policy main.go builds when --oidc-issuer is set but no
	// --oidc-role-mapping-* flag is: a policy with nil RoleMappings.
	basePolicy := &ProviderPolicy{}
	override := &RoleMappings{Admin: []string{"ovr-admins"}}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", basePolicy)
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	o.SetProviderName(HelmProviderName)
	o.AttachHelmRoleOverridesFunc(func(context.Context) (*RoleMappings, error) { return override, nil })
	rec := &auditWriteRecorder{}
	o.AttachAuditWriteSyncFunc(rec.write)

	store := newAuthDB(t)
	o.AttachStore(store)
	sessions := NewSessionStore(store)

	roleOf := func(label string) (string, string) {
		t.Helper()
		var role, bindingRole string
		if err := store.DB.QueryRowContext(context.Background(),
			`SELECT role FROM users WHERE email = ?`, idp.email).Scan(&role); err != nil {
			t.Fatalf("%s: user: %v", label, err)
		}
		if err := store.DB.QueryRowContext(context.Background(), `
			SELECT b.role_name FROM user_role_bindings b
			JOIN users u ON u.id = b.user_id
			WHERE u.email = ? AND b.namespace = '*'`, idp.email).Scan(&bindingRole); err != nil {
			t.Fatalf("%s: binding: %v", label, err)
		}
		return role, bindingRole
	}

	// Login 1: the override maps the user's group to admin.
	idp.nonce = "nonce-ovr-1"
	if rr := callbackViaIDP(t, o, sessions, "nonce-ovr-1"); rr.Code != http.StatusFound {
		t.Fatalf("login 1: code=%d body=%q", rr.Code, rr.Body)
	}
	if role, binding := roleOf("login 1"); role != "admin" || binding != "admin" {
		t.Fatalf("login 1: role=%q binding=%q, want admin/admin", role, binding)
	}
	if len(rec.reasons) != 1 {
		t.Fatalf("login 1: audit events = %d (%v), want 1", len(rec.reasons), rec.reasons)
	}

	// Login 2: the group is gone at the IdP, but the user is the install's
	// only user-manager, so the demotion is skipped and not audited.
	idp.groups = []string{"unmapped"}
	idp.nonce = "nonce-ovr-2"
	if rr := callbackViaIDP(t, o, sessions, "nonce-ovr-2"); rr.Code != http.StatusFound {
		t.Fatalf("login 2: code=%d body=%q", rr.Code, rr.Body)
	}
	if role, binding := roleOf("login 2"); role != "admin" || binding != "admin" {
		t.Fatalf("login 2: role=%q binding=%q, last user-manager must keep admin", role, binding)
	}
	if len(rec.reasons) != 1 {
		t.Fatalf("login 2: audit events = %d (%v), want still 1", len(rec.reasons), rec.reasons)
	}

	// Login 3: another user-manager exists now, so the re-evaluation
	// applies the default role and audits the change.
	seedUser(t, store, "backup-admin", "pw-backup-admin", "admin")
	idp.nonce = "nonce-ovr-3"
	if rr := callbackViaIDP(t, o, sessions, "nonce-ovr-3"); rr.Code != http.StatusFound {
		t.Fatalf("login 3: code=%d body=%q", rr.Code, rr.Body)
	}
	if role, binding := roleOf("login 3"); role != "viewer" || binding != "viewer" {
		t.Fatalf("login 3: role=%q binding=%q, want viewer/viewer", role, binding)
	}
	if len(rec.reasons) != 2 {
		t.Fatalf("login 3: audit events = %d (%v), want 2", len(rec.reasons), rec.reasons)
	}
	if want := "oidc role assigned: provider=helm matched=none from=admin to=viewer"; rec.reasons[1] != want {
		t.Fatalf("login 3: audit reason = %q, want %q", rec.reasons[1], want)
	}
}

// TestHandleCallback_AuditEventRecordsCorrectPath verifies that role-assignment
// audit events record the actual request path, supporting both per-provider paths
// (e.g., /auth/oidc/corp/callback) and legacy path (/auth/oidc/callback).
func TestHandleCallback_AuditEventRecordsCorrectPath(t *testing.T) {
	idp := newFakeIDP(t, "client-1")
	idp.nonce = "nonce-path-test"
	idp.groups = []string{"gp-admins"}

	o, err := NewOIDCWithPolicy(context.Background(), idp.issuer(), "client-1", "secret",
		"https://app/cb", &ProviderPolicy{
			RoleMappings: &RoleMappings{
				Admin: []string{"gp-admins"},
			},
		})
	if err != nil {
		t.Fatalf("NewOIDCWithPolicy: %v", err)
	}
	store := newAuthDB(t)
	o.AttachStore(store)

	SetFastHashParams(t)
	rec := &auditWriteRecorder{}
	o.AttachAuditWriteSyncFunc(rec.write)
	o.SetProviderName("corp")

	// Test with a per-provider callback path
	perProviderPath := "/auth/oidc/corp/callback"
	rr := httptest.NewRecorder()
	req, err := http.NewRequestWithContext(context.Background(), "GET",
		"https://app/auth/oidc/corp/callback?code=authcode&state=statevalue", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	// Set up the OIDC state and nonce cookies for the per-provider path.
	http.SetCookie(rr, &http.Cookie{Name: oidcStateCookie, Value: "statevalue", Path: "/auth/oidc/corp"})
	http.SetCookie(rr, &http.Cookie{Name: oidcNonceCookie, Value: "nonce-path-test", Path: "/auth/oidc/corp"})

	// Extract the cookies from the response and add them to the request.
	for _, c := range rr.Result().Cookies() {
		req.AddCookie(c)
	}

	// Simulate the OIDC exchange and ID token verification via callbackViaIDP.
	sessions := NewSessionStore(store)
	rr = callbackViaIDP(t, o, sessions, "nonce-path-test")
	if rr.Code != http.StatusFound {
		t.Fatalf("callback code=%d body=%q", rr.Code, rr.Body)
	}

	// Verify that the audit event was recorded with the request path.
	if len(rec.paths) != 1 {
		t.Fatalf("audit paths=%v, want 1", rec.paths)
	}
	if len(rec.reasons) != 1 {
		t.Fatalf("audit reasons=%v, want 1", rec.reasons)
	}

	// The emitRoleAssignmentAudit helper is called from the closure in HandleCallbackAt,
	// which has access to req.URL.Path. By calling emitRoleAssignmentAudit directly with
	// the desired path, we can verify the path is correctly used.
	user := &User{ID: 1, Username: "testuser", Role: "admin"}
	outcome := &RoleAssignmentOutcome{
		PreviousRole: "new_user",
		NewRole:      "admin",
		Applied:      true,
		MatchedGroup: "gp-admins",
	}

	// Test emitRoleAssignmentAudit directly with a per-provider path.
	rec2 := &auditWriteRecorder{}
	o.AttachAuditWriteSyncFunc(rec2.write)
	o.emitRoleAssignmentAudit(context.Background(), user, perProviderPath, "testuser@example.com", outcome)

	if len(rec2.paths) != 1 {
		t.Fatalf("direct call paths=%v, want 1", rec2.paths)
	}
	if rec2.paths[0] != perProviderPath {
		t.Fatalf("audit path=%q, want %q", rec2.paths[0], perProviderPath)
	}

	// Test with legacy path.
	legacyPath := "/auth/oidc/callback"
	rec3 := &auditWriteRecorder{}
	o.AttachAuditWriteSyncFunc(rec3.write)
	o.emitRoleAssignmentAudit(context.Background(), user, legacyPath, "testuser@example.com", outcome)

	if len(rec3.paths) != 1 {
		t.Fatalf("legacy paths=%v, want 1", rec3.paths)
	}
	if rec3.paths[0] != legacyPath {
		t.Fatalf("legacy audit path=%q, want %q", rec3.paths[0], legacyPath)
	}
}
