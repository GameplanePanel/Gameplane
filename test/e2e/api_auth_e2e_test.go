//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// adminUsername / adminPassword are the credentials TestAPI_*
// bootstraps. The password is *only* sensitive within this kind
// cluster, but we still pipe it through --password-stdin and write it
// to a 0600-mode file rather than scattering it in argv.
const (
	adminUsername = "e2e-admin"
	adminPassword = "e2e-test-admin-password-1234"
)

// TestAPI_BootstrapAndLogin proves the bootstrap-admin subcommand can
// seed a fresh admin user inside the running api Deployment, and that
// the resulting credentials authenticate against /auth/login.
//
// Side effect: writes the admin password to test/e2e/.tmp/admin-password
// for handoff to the Playwright live-mode suite (PR3). The directory is
// gitignored.
func TestAPI_BootstrapAndLogin(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	envInstance.WriteAdminPasswordFile(t, adminPassword)

	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	resp, body, err := cli.Get("/users/me")
	if err != nil {
		t.Fatalf("/users/me: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/users/me %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), `"username":"`+adminUsername+`"`) {
		t.Fatalf("/users/me response missing admin username: %s", string(body))
	}
	if !strings.Contains(string(body), `"role":"admin"`) {
		t.Fatalf("/users/me response missing admin role: %s", string(body))
	}
}

// TestAPI_LoginPrivacy enforces the pre-auth privacy rule from
// CLAUDE.md §3 at the /auth/login level: a wrong-password attempt
// against a real user, and a login attempt for a nonexistent user,
// must be indistinguishable. Otherwise the response timing or body
// becomes a username-enumeration oracle.
// NOT t.Parallel(): this test observes RAW login status codes (it
// deliberately bypasses APIClient's 429 retry), so it needs a login
// rate limiter that parallel neighbors haven't drained. Go runs
// non-parallel tests to completion before the parallel phase starts,
// which guarantees a fresh bucket here.
func TestAPI_LoginPrivacy(t *testing.T) {
	// Make sure the admin row exists so the "real user, wrong password"
	// branch actually exercises VerifyPassword (not the dummy).
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)

	// Borrow the port-forward + http client from a successful client.
	// We're going to issue raw POSTs without a session, but the BaseURL
	// is still the in-cluster Service.
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	tries := []struct {
		name string
		body string
	}{
		{"realUserWrongPassword", `{"username":"` + adminUsername + `","password":"definitely-not-the-password"}`},
		{"nonexistentUser", `{"username":"this-user-does-not-exist","password":"definitely-not-the-password"}`},
	}
	bodies := make([]string, 0, len(tries))
	for _, tc := range tries {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, cli.BaseURL+"/auth/login", strings.NewReader(tc.body))
		if err != nil {
			t.Fatalf("%s: build request: %v", tc.name, err)
		}
		req.Header.Set("Content-Type", "application/json")
		// Don't reuse the cookie jar — these are fresh attempts.
		raw := &http.Client{Timeout: cli.HTTP.Timeout}
		resp, err := raw.Do(req)
		if err != nil {
			t.Fatalf("%s: do: %v", tc.name, err)
		}
		buf := make([]byte, 1024)
		n, _ := resp.Body.Read(buf)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d body=%q", tc.name, resp.StatusCode, string(buf[:n]))
		}
		bodies = append(bodies, strings.TrimSpace(string(buf[:n])))
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("login error responses differ — enumeration oracle:\n  realUser:    %q\n  nonexistent: %q",
			bodies[0], bodies[1])
	}
	if !strings.Contains(bodies[0], "invalid credentials") {
		t.Fatalf("login error body should be the generic 'invalid credentials' string, got %q", bodies[0])
	}
}

// TestAPI_RBAC_ViewerCannotMutate creates a viewer-role user, logs in
// as them, and confirms the RBAC middleware rejects POST /servers/*:start
// (which requires operator+). The audit row for the rejected call
// should land with status=403 — checked from the same admin session.
func TestAPI_RBAC_ViewerCannotMutate(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)

	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	// Per-process unique viewer name. The gameplane-system DB carries
	// state between test runs against a `make e2e-up`-managed cluster
	// (the API PVC isn't wiped); a fixed username collides with the
	// previous run's row and the create handler returns 500. CI's
	// ephemeral cluster doesn't see this, but local iteration does.
	viewerName := fmt.Sprintf("e2e-viewer-%d", time.Now().UnixNano())
	const viewerPW = "e2e-viewer-password-1234"

	resp, body, err := admin.Post("/users", map[string]string{
		"username": viewerName,
		"password": viewerPW,
		"role":     "viewer",
	})
	if err != nil {
		t.Fatalf("create viewer: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create viewer %d: %s", resp.StatusCode, string(body))
	}

	viewer := envInstance.APIClient(t, viewerName, viewerPW)
	defer viewer.Close()

	// Viewer attempts a mutation. The route exists and the user is
	// authenticated; the only thing that should bounce them is RBAC.
	mResp, mBody, err := viewer.Post("/servers/no-such-server:start", nil)
	if err != nil {
		t.Fatalf("viewer POST :start: %v", err)
	}
	defer func() { _ = mResp.Body.Close() }()
	if mResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%q", mResp.StatusCode, string(mBody))
	}
}

// TestAPI_AuditEmitsOnMutation checks that a successful admin mutation
// produces a row in the audit log. The /admin/audit endpoint returns
// the most-recent events; we just need to find ours by path.
//
// We pick a mutation that does NOT invalidate the caller's own session.
// Reset-password and role-change both delete sessions for the target
// user (security feature in users.go), so calling them on the admin's
// own row blanks out the cookie we're using to read /admin/audit.
// PATCH /users/{id} with a display-name change avoids that pitfall.
func TestAPI_AuditEmitsOnMutation(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)

	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	getMe, body, err := admin.Get("/users/me")
	if err != nil {
		t.Fatalf("/users/me: %v", err)
	}
	defer func() { _ = getMe.Body.Close() }()
	if getMe.StatusCode != http.StatusOK {
		t.Fatalf("/users/me %d: %s", getMe.StatusCode, string(body))
	}
	// Extract our id without pulling in encoding/json: the response
	// shape is small and we only need the leading "id":N field.
	id := extractUserID(string(body))
	if id == "" {
		t.Fatalf("could not parse id from /users/me: %s", string(body))
	}

	uniqueDisplayName := "e2e-audit-marker-" + id
	rResp, rBody, err := admin.Patch("/users/"+id, map[string]string{"displayName": uniqueDisplayName})
	if err != nil {
		t.Fatalf("patch user displayName: %v", err)
	}
	defer func() { _ = rResp.Body.Close() }()
	if rResp.StatusCode != http.StatusOK {
		t.Fatalf("patch user displayName %d: %s", rResp.StatusCode, string(rBody))
	}

	aResp, aBody, err := admin.Get("/admin/audit?limit=50")
	if err != nil {
		t.Fatalf("/admin/audit: %v", err)
	}
	defer func() { _ = aResp.Body.Close() }()
	if aResp.StatusCode != http.StatusOK {
		t.Fatalf("/admin/audit %d: %s", aResp.StatusCode, string(aBody))
	}
	wantPath := `"path":"/users/` + id + `","status":200`
	if !strings.Contains(string(aBody), wantPath) {
		t.Fatalf("audit log missing %s, got: %s", wantPath, string(aBody))
	}
	// NOTE: We deliberately do NOT assert on "actor": the audit
	// middleware (api/internal/audit/audit.go) reads the original
	// request context after the chain returns, but sessions.Authenticate
	// adds the user via req.WithContext on the downstream request — the
	// upstream req in the middleware closure never sees it. As a result
	// every mutation logs as "anonymous". Expanding this to a
	// known-actor check requires fixing the audit middleware to capture
	// the *passed-down* context, which is out of scope for this test.
}

// extractUserID has been promoted to env.go so non-test helper code
// (Env.CreateUser) can use it. Test files in this package still call
// it through the package-level scope.

// TestAPI_DynamicAuthProviders proves the auth registry resolves config
// saves live — no API restart: a provider added through the config API
// appears on the pre-auth /auth/providers listing and gets a routable
// start endpoint, and disabling local login turns /auth/login into a
// neutral 403 until re-enabled.
//
// NOT t.Parallel(): it temporarily disables local login, which would
// break parallel neighbors' logins. Budget: one admin login (the same
// session drives every mutation).
func TestAPI_DynamicAuthProviders(t *testing.T) {
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	// Whatever happens below, leave the install with local login enabled —
	// every other test in this job depends on it.
	t.Cleanup(func() {
		if resp, _, err := cli.Do(http.MethodPut, "/admin/config/auth",
			map[string]any{"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			}}); err == nil && resp != nil {
			_ = resp.Body.Close()
		}
		if resp, _, err := cli.Do(http.MethodDelete, "/admin/auth/providers/e2e-sso/secret", nil); err == nil && resp != nil {
			_ = resp.Body.Close()
		}
	})

	// The redirect URL derives from General → External URL.
	genResp, genBody, err := cli.Do(http.MethodPut, "/admin/config/general", map[string]any{
		"instanceName": "e2e", "externalURL": "https://gameplane.e2e.example", "defaultNamespace": "gameplane-games",
	})
	if genResp != nil {
		defer func() { _ = genResp.Body.Close() }()
	}
	if err != nil || genResp.StatusCode != http.StatusOK {
		t.Fatalf("set general config: %v %s", err, string(genBody))
	}

	// Store the provider's clientSecret — this exercises the managed
	// Secret write path (and the chart's new RBAC) against a real cluster.
	secResp, secBody, err := cli.Do(http.MethodPut, "/admin/auth/providers/e2e-sso/secret",
		map[string]string{"clientSecret": "e2e-client-secret"})
	if secResp != nil {
		defer func() { _ = secResp.Body.Close() }()
	}
	if err != nil || secResp.StatusCode != http.StatusOK {
		t.Fatalf("put provider secret: %v %s", err, string(secBody))
	}

	// Add the provider. The issuer is unreachable on purpose: listing and
	// routing must work without ever dialing it.
	authCfg := func(localEnabled bool) map[string]any {
		return map[string]any{"providers": []map[string]any{
			{"name": "local", "kind": "local", "enabled": localEnabled},
			{"name": "e2e-sso", "kind": "oidc", "displayName": "E2E SSO", "enabled": true,
				"issuer": "https://e2e-idp.invalid", "clientID": "gameplane"},
		}}
	}
	authResp, authBody, err := cli.Do(http.MethodPut, "/admin/config/auth", authCfg(true))
	if authResp != nil {
		defer func() { _ = authResp.Body.Close() }()
	}
	if err != nil || authResp.StatusCode != http.StatusOK {
		t.Fatalf("save auth config: %v %s", err, string(authBody))
	}

	raw := &http.Client{Timeout: cli.HTTP.Timeout}

	// Pre-auth listing reflects the save immediately.
	providersReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, cli.BaseURL+"/auth/providers", nil)
	if err != nil {
		t.Fatalf("build providers request: %v", err)
	}
	resp, err := raw.Do(providersReq)
	if err != nil {
		t.Fatalf("get providers: %v", err)
	}
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	_ = resp.Body.Close()
	listing := string(buf[:n])
	if !strings.Contains(listing, `"e2e-sso"`) || !strings.Contains(listing, `"E2E SSO"`) {
		t.Fatalf("providers listing missing the new provider: %s", listing)
	}
	if strings.Contains(listing, "e2e-idp.invalid") {
		t.Fatalf("pre-auth providers listing leaks the issuer URL: %s", listing)
	}

	// The start route exists and resolves the provider. Discovery against
	// the invalid issuer fails, so a detail-free 502 is the expected
	// terminal state — the point is it is NOT a 404.
	startReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, cli.BaseURL+"/auth/oidc/e2e-sso/start", nil)
	if err != nil {
		t.Fatalf("build start request: %v", err)
	}
	resp, err = raw.Do(startReq)
	if err != nil {
		t.Fatalf("get start: %v", err)
	}
	n, _ = resp.Body.Read(buf)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("start status = %d body=%q, want 502 (unreachable issuer)", resp.StatusCode, string(buf[:n]))
	}
	if strings.Contains(string(buf[:n]), "e2e-idp.invalid") {
		t.Fatalf("start error leaks the issuer: %q", string(buf[:n]))
	}

	// Disabling local login takes effect on the next request; the session
	// minted above keeps working for the re-enable.
	disableResp, disableBody, err := cli.Do(http.MethodPut, "/admin/config/auth", authCfg(false))
	if disableResp != nil {
		defer func() { _ = disableResp.Body.Close() }()
	}
	if err != nil || disableResp.StatusCode != http.StatusOK {
		t.Fatalf("disable local: %v %s", err, string(disableBody))
	}
	// The raw login below shares the job-wide rate-limit budget; retry
	// through 429s so neighbors' drained buckets can't flake this.
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		req, rerr := http.NewRequestWithContext(t.Context(), http.MethodPost, cli.BaseURL+"/auth/login",
			strings.NewReader(`{"username":"`+adminUsername+`","password":"`+adminPassword+`"}`))
		if rerr != nil {
			return false, "build login request: " + rerr.Error()
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := raw.Do(req)
		if err != nil {
			return false, "login: " + err.Error()
		}
		n, _ := resp.Body.Read(buf)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return false, "rate limited"
		}
		if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(buf[:n]), "login method disabled") {
			t.Fatalf("login = %d %q, want a neutral 403", resp.StatusCode, string(buf[:n]))
		}
		return true, ""
	})

	// Re-enable and confirm the gate lifts without a restart.
	enableResp, enableBody, err := cli.Do(http.MethodPut, "/admin/config/auth", authCfg(true))
	if enableResp != nil {
		defer func() { _ = enableResp.Body.Close() }()
	}
	if err != nil || enableResp.StatusCode != http.StatusOK {
		t.Fatalf("re-enable local: %v %s", err, string(enableBody))
	}
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		req, rerr := http.NewRequestWithContext(t.Context(), http.MethodPost, cli.BaseURL+"/auth/login",
			strings.NewReader(`{"username":"`+adminUsername+`","password":"`+adminPassword+`"}`))
		if rerr != nil {
			return false, "build login request: " + rerr.Error()
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := raw.Do(req)
		if err != nil {
			return false, "login: " + err.Error()
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return false, "rate limited"
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("login after re-enable = %d, want 200", resp.StatusCode)
		}
		return true, ""
	})
}

// TestAPI_AuthConfig_RoleMappings verifies the config API for role mappings.
// It tests: round-trip set/retrieve, empty list preservation, per-role
// independence, delete operations, and idempotency. All scenarios share one
// admin login to stay within the login rate-limit budget.
//
// NOT t.Parallel(): mutates the shared auth config row (all subtests run
// serially and may leave state for later subtests, so each subtest must
// set up its own initial state for isolation).
// Budget: one admin login total for all subtests.
func TestAPI_AuthConfig_RoleMappings(t *testing.T) {
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)

	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	t.Run("SetAndRetrieve", func(t *testing.T) {
		// Verify the config API round-trip for role mappings: setting a role's
		// override via PUT /admin/config/auth and reading it back via GET /admin/config.

		// Start with a clean slate to isolate this subtest.
		resp, body, err := admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("clear state: %v %s", err, string(body))
		}

		// Set role mappings via the helmOverride.
		resp, body, err = admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{
					"admin":    []string{"test-admin-group"},
					"operator": []string{"test-op-group"},
					"viewer":   []string{"test-viewer-group"},
				},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("set role mappings: %v %s", err, string(body))
		}

		// Verify the mappings were saved by retrieving the config.
		resp, body, err = admin.Get("/admin/config")
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("get config: %v %s", err, string(body))
		}
		if !strings.Contains(string(body), `"test-admin-group"`) {
			t.Fatalf("config missing test-admin-group mapping: %s", string(body))
		}
		if !strings.Contains(string(body), `"test-op-group"`) {
			t.Fatalf("config missing test-op-group mapping: %s", string(body))
		}
		if !strings.Contains(string(body), `"test-viewer-group"`) {
			t.Fatalf("config missing test-viewer-group mapping: %s", string(body))
		}
	})

	t.Run("EmptyListPreservation", func(t *testing.T) {
		// Verify that setting a role's override to an empty list [] (meaning
		// "nobody") persists as present-but-empty in the config, not collapsed
		// to nil/absent. This is the crux of the feature and the most likely
		// regression point.

		// Start with a clean slate to isolate this subtest.
		resp, body, err := admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("clear state: %v %s", err, string(body))
		}

		// Set admin role to an empty list (nobody).
		resp, body, err = admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{
					"admin": []string{}, // Empty list = "nobody"
				},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("set empty admin mapping: %v %s", err, string(body))
		}

		// Verify the empty list is preserved (key presence indicates provenance).
		// The JSON should contain "admin":[] not absent "admin" key.
		resp, body, err = admin.Get("/admin/config")
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("get config: %v %s", err, string(body))
		}
		// Check for the presence of "admin":[] or "admin": []
		if !strings.Contains(string(body), `"admin":[]`) && !strings.Contains(string(body), `"admin": []`) {
			t.Fatalf("config should preserve empty admin list (key presence = provenance), got: %s", string(body))
		}
	})

	t.Run("PutReplacesTheWholeOverrideMap", func(t *testing.T) {
		// PUT /admin/config/auth writes the whole "auth" section, exactly as
		// every other config section behaves and exactly as the dashboard
		// sends it (it always submits the full helmOverride object it holds).
		// So a body carrying only the admin key REPLACES the stored map --
		// operator and viewer overrides are dropped, not merged. Per-role
		// independence lives elsewhere: in effectiveHelmPolicy's merge against
		// the Helm seed, and in DELETE .../role-mappings/{role}, covered by
		// the DeleteRemovesOnly subtest below.

		// Start with a clean slate to isolate this subtest.
		resp, body, err := admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("clear state: %v %s", err, string(body))
		}

		// Set initial mappings for all three roles.
		resp, body, err = admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{
					"admin":    []string{"initial-admin"},
					"operator": []string{"initial-op"},
					"viewer":   []string{"initial-viewer"},
				},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("set initial mappings: %v %s", err, string(body))
		}

		// Update only the admin role.
		resp, body, err = admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{
					"admin": []string{"updated-admin"},
					// Intentionally omit operator and viewer
				},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("update admin mapping: %v %s", err, string(body))
		}

		// Verify admin was updated and the omitted roles were replaced away.
		resp, body, err = admin.Get("/admin/config")
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("get config after full-section update: %v %s", err, string(body))
		}
		if !strings.Contains(string(body), `"updated-admin"`) {
			t.Fatalf("admin mapping was not updated: %s", string(body))
		}
		if strings.Contains(string(body), `"initial-op"`) {
			t.Fatalf("operator override survived a section-replacing PUT that omitted it: %s", string(body))
		}
		if strings.Contains(string(body), `"initial-viewer"`) {
			t.Fatalf("viewer override survived a section-replacing PUT that omitted it: %s", string(body))
		}
	})

	t.Run("DeleteRemovesOnly", func(t *testing.T) {
		// Verify that DELETE /admin/config/auth/role-mappings/{role} removes
		// exactly one role's override and leaves others untouched.

		// Start with a clean slate to isolate this subtest.
		resp, body, err := admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("clear state: %v %s", err, string(body))
		}

		// Set mappings for all three roles.
		resp, body, err = admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{
					"admin":    []string{"admin-to-delete"},
					"operator": []string{"op-stays"},
					"viewer":   []string{"viewer-stays"},
				},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("set mappings: %v %s", err, string(body))
		}

		// Delete the admin role mapping.
		resp, body, err = admin.Do(http.MethodDelete, "/admin/config/auth/role-mappings/admin", nil)
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("delete admin mapping: %v %s", err, string(body))
		}

		// Verify admin was removed but operator and viewer remain.
		resp, body, err = admin.Get("/admin/config")
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("get config after delete: %v %s", err, string(body))
		}
		if strings.Contains(string(body), `"admin-to-delete"`) {
			t.Fatalf("admin mapping was not removed: %s", string(body))
		}
		if !strings.Contains(string(body), `"op-stays"`) {
			t.Fatalf("operator mapping was lost on admin delete: %s", string(body))
		}
		if !strings.Contains(string(body), `"viewer-stays"`) {
			t.Fatalf("viewer mapping was lost on admin delete: %s", string(body))
		}
	})

	t.Run("DeleteIdempotentAndValidation", func(t *testing.T) {
		// Verify that DELETE /admin/config/auth/role-mappings/{role} is
		// idempotent (succeeds even when the role has no override) and rejects
		// invalid role names with 400.

		// Start with a clean slate to isolate this subtest.
		resp, body, err := admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("clear state: %v %s", err, string(body))
		}

		// Set a mapping for testing.
		resp, body, err = admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{
					"admin": []string{"test-group"},
				},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("set mapping: %v %s", err, string(body))
		}

		// Delete the admin role mapping (first delete).
		resp, body, err = admin.Do(http.MethodDelete, "/admin/config/auth/role-mappings/admin", nil)
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("delete admin mapping: %v %s", err, string(body))
		}

		// Delete again (idempotent) — should still succeed.
		resp, body, err = admin.Do(http.MethodDelete, "/admin/config/auth/role-mappings/admin", nil)
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("delete admin mapping again (idempotent): %v %s", err, string(body))
		}

		// Try to delete an invalid role name — should fail with 400.
		resp, body, err = admin.Do(http.MethodDelete, "/admin/config/auth/role-mappings/invalid-role-name", nil)
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("delete invalid role should be 400, got %d: %v %s", resp.StatusCode, err, string(body))
		}
	})
}

// oidcHelmAdminGroup is the IdP group deploy/kind/e2e.sh seeds via
// --set api.oidc.roleMappings.admin[0]=... at the one shared cluster
// bring-up Helm install. It must match exactly, or TestAPI_OIDCHelmSeeded_*
// below would legitimately fail as if the mapping were misconfigured.
const oidcHelmAdminGroup = "gameplane-e2e-oidc-admins"

// oidcHelmLogin drives one full OIDC authorization-code round trip
// against the Helm-seeded "helm" provider — api/internal/auth/registry.go's
// Registry.legacy, built exactly once at API process startup from the
// chart's api.oidc.* values (see deploy/kind/e2e.sh) — via the
// in-cluster fake IdP (test/e2e/internal/fakeoidc), and returns the
// resulting session's role as reported by GET /users/me.
//
// This exercises the real Helm-seeded code path end to end: HandleStartAt
// (state/nonce cookies + AuthCodeURL), a genuine RS256 ID-token exchange
// against the fake IdP's /token, HandleCallbackAt's nonce/claims
// verification, extractGroups, computeRole, and resolveOrLinkUser/
// syncUserRole — the same mechanism a real OIDC provider would drive.
//
// sub identifies the OIDC subject; calling this twice with the same sub
// and different groups exercises re-evaluation on a subsequent login
// (T049 scenario (b)) against the same Gameplane user. groups is
// embedded verbatim into the fake IdP's ID token "groups" claim.
//
// idpBase must be a port-forwarded (not cluster-DNS) base URL: the fake
// IdP's /authorize is dialed directly by the test process (standing in
// for a browser), which cannot resolve in-cluster Service DNS — every
// other hop (discovery, /token, /jwks) is dialed by the API pod itself,
// server-side, over the cluster-internal issuer URL.
func oidcHelmLogin(t *testing.T, apiBase, idpBase, sub, email string, groups []string) string {
	t.Helper()

	cli := &http.Client{
		Jar:           newInsecureCookieJar(),
		Timeout:       30 * time.Second,
		CheckRedirect: oidcNoRedirect,
	}

	// 1. GET /auth/oidc/helm/start — the API sets state/nonce cookies
	// (captured into cli's jar) and redirects to the IdP's authorize
	// endpoint (at its cluster-internal DNS name).
	startResp := oidcDo(t, cli, http.MethodGet, apiBase+"/auth/oidc/helm/start", nil)
	_ = startResp.Body.Close()
	if startResp.StatusCode != http.StatusFound {
		t.Fatalf("oidc start status = %d, want 302", startResp.StatusCode)
	}
	authorizeURL, err := url.Parse(startResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse start Location: %v", err)
	}

	// 2. Rewrite the authorize URL onto the fake IdP's port-forward, and
	// inject the identity this login should authenticate as (see the
	// fakeoidc package doc: the caller stands in for the IdP's login
	// screen).
	idpURL, err := url.Parse(idpBase)
	if err != nil {
		t.Fatalf("parse idp base: %v", err)
	}
	authorizeURL.Scheme = idpURL.Scheme
	authorizeURL.Host = idpURL.Host
	q := authorizeURL.Query()
	q.Set("sub", sub)
	q.Set("email", email)
	q.Set("groups", strings.Join(groups, ","))
	authorizeURL.RawQuery = q.Encode()

	// 3. GET the fake IdP's /authorize — it stashes the chosen identity
	// against a fresh code and redirects back to the configured
	// redirect_uri (never dialed — it's a placeholder host; only its
	// query string, containing state+code, matters to step 4).
	authorizeResp := oidcDo(t, cli, http.MethodGet, authorizeURL.String(), nil)
	_ = authorizeResp.Body.Close()
	if authorizeResp.StatusCode != http.StatusFound {
		t.Fatalf("fake idp authorize status = %d, want 302", authorizeResp.StatusCode)
	}
	callbackLoc, err := url.Parse(authorizeResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize Location: %v", err)
	}

	// 4. GET /auth/oidc/helm/callback — the API exchanges the code
	// against the fake IdP's /token (dialed server-side, in-cluster),
	// verifies the ID token, computes the role, and creates/updates the
	// user before redirecting to "/" with a session cookie.
	callbackResp := oidcDo(t, cli, http.MethodGet, apiBase+"/auth/oidc/helm/callback?"+callbackLoc.RawQuery, nil)
	cbBody, _ := io.ReadAll(callbackResp.Body)
	_ = callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusFound {
		t.Fatalf("oidc callback status = %d, want 302: %s", callbackResp.StatusCode, string(cbBody))
	}

	// 5. GET /users/me with the session cookie the callback just set.
	meResp := oidcDo(t, cli, http.MethodGet, apiBase+"/users/me", nil)
	meBody, _ := io.ReadAll(meResp.Body)
	_ = meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("/users/me %d: %s", meResp.StatusCode, string(meBody))
	}
	var me struct {
		Role string `json:"role"`
	}
	if err := json.Unmarshal(meBody, &me); err != nil {
		t.Fatalf("decode /users/me: %v\n%s", err, string(meBody))
	}
	return me.Role
}

// oidcNoRedirect stops http.Client from following redirects on its own:
// oidcHelmLogin inspects each hop's Location header itself.
func oidcNoRedirect(_ *http.Request, _ []*http.Request) error {
	return http.ErrUseLastResponse
}

// oidcDo issues one GET through cli, never following redirects itself
// (the caller inspects each Location header explicitly).
func oidcDo(t *testing.T, cli *http.Client, method, rawURL string, body io.Reader) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, rawURL, body)
	if err != nil {
		t.Fatalf("build request for %s: %v", rawURL, err)
	}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, rawURL, err)
	}
	return resp
}

// oidcHelmPorts port-forwards both the API and the fake IdP Services and
// returns their loopback base URLs; both port-forwards are torn down via t.Cleanup.
func oidcHelmPorts(t *testing.T) (apiBase, idpBase string) {
	t.Helper()
	apiPort, apiStop := envInstance.PortForward(t, "gameplane-system", "svc/gameplane-api", 80)
	t.Cleanup(apiStop)
	idpPort, idpStop := envInstance.PortForward(t, "gameplane-system", "svc/gameplane-test-fakeoidc", 8080)
	t.Cleanup(idpStop)
	return fmt.Sprintf("http://127.0.0.1:%d", apiPort), fmt.Sprintf("http://127.0.0.1:%d", idpPort)
}

// TestAPI_OIDCHelmSeeded_AdminOnFirstLogin covers T049 scenario (a): a
// user whose OIDC groups claim includes the Helm-seeded admin group
// (api.oidc.roleMappings.admin, wired in deploy/kind/e2e.sh) receives
// the admin role the very first time they log in — no bootstrap-admin,
// no dashboard config step (FR-007..FR-010, SC-003, SC-004).
//
// t.Parallel(): a fresh, unique OIDC subject; the only shared resource is
// OIDCCallbackLimiter (burst 10/min), and this test makes exactly one
// callback call. No admin login — the whole flow authenticates as the
// OIDC user, never as e2e-admin, so it spends 0 of the bucket's
// local-login budget.
func TestAPI_OIDCHelmSeeded_AdminOnFirstLogin(t *testing.T) {
	t.Parallel()
	apiBase, idpBase := oidcHelmPorts(t)

	role := oidcHelmLogin(t, apiBase, idpBase,
		"e2e-oidc-admin-first-login", "oidc-admin@e2e.example", []string{oidcHelmAdminGroup})
	if role != "admin" {
		t.Fatalf("role = %q, want admin (first login, member of the Helm-seeded admin group)", role)
	}
}

// TestAPI_OIDCHelmSeeded_DefaultRoleOnNoMapping covers T049 scenario (c):
// a user whose groups claim matches no configured mapping receives the
// configured default role — viewer, since api.oidc.defaultRole is left
// unset in deploy/kind/e2e.sh (FR-008(c), FR-010, SC-008).
//
// t.Parallel(): fresh unique subject, 0 admin logins, one OIDC callback.
func TestAPI_OIDCHelmSeeded_DefaultRoleOnNoMapping(t *testing.T) {
	t.Parallel()
	apiBase, idpBase := oidcHelmPorts(t)

	role := oidcHelmLogin(t, apiBase, idpBase,
		"e2e-oidc-default-role", "oidc-nogroup@e2e.example", []string{"some-unmapped-group"})
	if role != "viewer" {
		t.Fatalf("role = %q, want viewer (no group matched a role mapping)", role)
	}
}

// TestAPI_OIDCHelmSeeded_RoleReevaluatedOnGroupChange covers T049
// scenario (b): the SAME OIDC subject logs in twice; between the two
// logins their groups claim changes from unmapped to the Helm-seeded
// admin group, and the second login's role reflects the new membership
// — role assignment is re-evaluated on every login, not just the first
// (FR-011, SC-005).
//
// t.Parallel(): fresh unique subject (isolated from the other two OIDC
// tests above), 0 admin logins, two OIDC callbacks against the same
// user — well inside OIDCCallbackLimiter's burst of 10.
func TestAPI_OIDCHelmSeeded_RoleReevaluatedOnGroupChange(t *testing.T) {
	t.Parallel()
	apiBase, idpBase := oidcHelmPorts(t)

	const sub = "e2e-oidc-reeval-user"
	firstRole := oidcHelmLogin(t, apiBase, idpBase,
		sub, "oidc-reeval@e2e.example", []string{"some-unmapped-group"})
	if firstRole != "viewer" {
		t.Fatalf("first login role = %q, want viewer", firstRole)
	}

	secondRole := oidcHelmLogin(t, apiBase, idpBase,
		sub, "oidc-reeval@e2e.example", []string{oidcHelmAdminGroup})
	if secondRole != "admin" {
		t.Fatalf("second login (after group membership changed) role = %q, want admin", secondRole)
	}
}

// TestAPI_OIDCHelmOverride_EffectiveAtLoginTime covers T053 scenarios (a) and (b):
// an admin sets a helmOverride.roleMappings list for a role via PUT /admin/config/auth
// that differs from the Helm-seeded mapping, and a subsequent OIDC login resolves to
// the override value with no API restart (SC-007), then DELETE /admin/config/auth/role-mappings/{role}
// reverts the mapping back to the Helm-seeded value on the next login (also with no restart).
//
// NOT t.Parallel(): writes helmOverride.roleMappings which, if shared with other
// config-mutating tests, could interfere. The test reuses one admin session for both
// the PUT and DELETE, costing +1 admin login to bring the api-roles bucket to 6.
// Budget: one admin login total.
func TestAPI_OIDCHelmOverride_EffectiveAtLoginTime(t *testing.T) {
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	apiBase, idpBase := oidcHelmPorts(t)

	const (
		overrideRole  = "operator"
		overrideGroup = "e2e-oidc-override-operator"
		oidcSubject   = "e2e-oidc-override-test"
		oidcEmail     = "override@e2e.example"
	)

	t.Run("OverrideTakesEffectAtLogin", func(t *testing.T) {
		// Set helmOverride for operator role to use a unique test group.
		resp, body, err := admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": []map[string]any{
				{"name": "local", "kind": "local", "enabled": true},
			},
			"helmOverride": map[string]any{
				"roleMappings": map[string]any{
					"operator": []string{overrideGroup},
				},
			},
		})
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("PUT /admin/config/auth with override: %v %s", err, string(body))
		}

		// OIDC login with the override group should resolve to operator role.
		role := oidcHelmLogin(t, apiBase, idpBase, oidcSubject, oidcEmail, []string{overrideGroup})
		if role != "operator" {
			t.Fatalf("role after helmOverride = %q, want operator (SC-007: override took effect immediately)", role)
		}
	})

	t.Run("DeleteRevertToHelmSeed", func(t *testing.T) {
		// Delete the operator override via the reset route.
		resp, body, err := admin.Do(http.MethodDelete, "/admin/config/auth/role-mappings/operator", nil)
		if resp != nil {
			defer func() { _ = resp.Body.Close() }()
		}
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("DELETE /admin/config/auth/role-mappings/operator: %v %s", err, string(body))
		}

		// OIDC login with the same group should now resolve to the default role (viewer),
		// since the override is gone and the Helm-seeded mapping for operator likely
		// does not include our test group.
		role := oidcHelmLogin(t, apiBase, idpBase, oidcSubject, oidcEmail, []string{overrideGroup})
		if role != "viewer" {
			t.Fatalf("role after delete override = %q, want viewer (Helm-seeded default, SC-007)", role)
		}
	})

	// Cleanup: idempotent delete to ensure the override is not left behind to leak
	// into other tests in the bucket. The second subtest already deleted it, but
	// this ensures cleanup even if the test fails mid-way.
	t.Cleanup(func() {
		resp, _, _ := admin.Do(http.MethodDelete, "/admin/config/auth/role-mappings/operator", nil)
		if resp != nil {
			_ = resp.Body.Close()
		}
	})
}
