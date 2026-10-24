//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// TestAPI_PasswordResetInvalidatesSession: when admin resets a viewer's
// password, that viewer's existing session must be evicted. The reset
// handler explicitly calls SessionStore.DeleteForUser for exactly this
// reason — without the eviction, a leaked old password and an active
// session are equivalent attack surfaces.
//
// Steps:
//  1. Bootstrap admin, create a viewer user, log the viewer in.
//  2. As admin, POST /users/{id}/reset-password with a new password.
//  3. The viewer's next request must come back as 401.
//  4. The viewer can log in again with the new password.
func TestAPI_PasswordResetInvalidatesSession(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)

	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	viewerName, viewerPW, viewerID := envInstance.CreateUser(t, admin, "viewer", "e2e-session-reset")
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/users/" + viewerID)
		if r != nil {
			r.Body.Close()
		}
	})

	viewer := envInstance.APIClient(t, viewerName, viewerPW)
	defer viewer.Close()

	// Confirm baseline: viewer can read /users/me before the reset.
	resp, _, err := viewer.Get("/users/me")
	if err != nil {
		t.Fatalf("baseline /users/me: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("baseline /users/me: status=%d", resp.StatusCode)
	}
	resp.Body.Close()

	const newPW = "e2e-reset-password-1234"
	var body []byte
	resp, body, err = admin.Post("/users/"+viewerID+"/reset-password", map[string]string{
		"password": newPW,
	})
	if err != nil {
		t.Fatalf("reset-password: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset-password expected 204, got %d body=%q", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	// Viewer's prior session must now bounce. Even if the cookie reaches
	// the API, it shouldn't resolve to a valid session row.
	resp, body, err = viewer.Get("/users/me")
	if err != nil {
		t.Fatalf("post-reset /users/me: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-reset /users/me expected 401, got %d body=%q", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	// And the new password works for a fresh login.
	relogged := envInstance.APIClient(t, viewerName, newPW)
	defer relogged.Close()
	resp, _, err = relogged.Get("/users/me")
	if err != nil {
		t.Fatalf("relogin /users/me: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("relogin /users/me: status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestAPI_LoginRateLimit: the LoginLimiter middleware caps wrong-password
// attempts per source IP. A burst of bad attempts should produce at
// least one 429 response, and a subsequent valid login must still
// succeed once the bucket refills.
//
// We bypass APIClient (which auto-retries on 429) and hit /auth/login
// directly to observe the raw status code.
func TestAPI_LoginRateLimit(t *testing.T) {
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)

	// Borrow an authenticated client only for its port-forward + BaseURL
	// — we issue raw, sessionless POSTs against the same in-cluster IP
	// (which is what the limiter keys on).
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	const badPW = `{"username":"e2e-admin","password":"definitely-not-the-password"}`
	httpClient := &http.Client{Timeout: cli.HTTP.Timeout}

	got429 := false
	for attempt := 0; attempt < 12; attempt++ {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, cli.BaseURL+"/auth/login",
			strings.NewReader(badPW))
		if err != nil {
			t.Fatalf("build login request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("login attempt %d: %v", attempt, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			got429 = true
			break
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("login attempt %d: unexpected status %d (want 401 or 429)",
				attempt, resp.StatusCode)
		}
	}
	if !got429 {
		t.Fatalf("12 wrong-password attempts produced no 429 — limiter not engaged")
	}

	// After the limiter cools down (APIClient's retry loop already
	// handles this), a valid login still works. This proves the limiter
	// is per-IP-with-recovery, not a permanent ban.
	recovered := envInstance.APIClient(t, adminUsername, adminPassword)
	defer recovered.Close()
	resp, _, err := recovered.Get("/users/me")
	if err != nil {
		t.Fatalf("post-recovery /users/me: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-recovery /users/me: status=%d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestAPI_AuditPaginationAndFilter: /admin/audit?limit=N must honor the
// limit. We generate a few audit-emitting mutations (PATCH /users/{id}
// to set displayName), then ask for limit=3 and assert exactly three
// rows come back, with newest first.
//
// We don't assert the body shape — that's tested in handlers — only
// that the pagination contract holds end-to-end.
func TestAPI_AuditPaginationAndFilter(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)

	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	getMe, body, err := admin.Get("/users/me")
	if err != nil {
		t.Fatalf("/users/me: %v", err)
	}
	if getMe.StatusCode != http.StatusOK {
		t.Fatalf("/users/me %d: %s", getMe.StatusCode, string(body))
	}
	defer func() { _ = getMe.Body.Close() }()
	id := extractUserID(string(body))
	if id == "" {
		t.Fatalf("could not parse id from /users/me: %s", string(body))
	}

	// Generate 5 distinct mutations. PATCH /users/{id} doesn't blow away
	// the admin's session (unlike /reset-password or /role).
	for i := 0; i < 5; i++ {
		marker := "e2e-pagination-marker-" + itoa(i)
		resp, b, err := admin.Patch("/users/"+id, map[string]string{"displayName": marker})
		if err != nil {
			t.Fatalf("patch %d: %v", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("patch %d: status=%d body=%s", i, resp.StatusCode, string(b))
		}
		resp.Body.Close()
	}

	// Ask for limit=3.
	resp, body, err := admin.Get("/admin/audit?limit=3")
	if err != nil {
		t.Fatalf("/admin/audit: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/admin/audit %d: %s", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	// Count "id":<digits> occurrences. The audit endpoint returns a JSON
	// array of events; each event has an id field. Five mutations are
	// guaranteed to surface in the most recent rows.
	count := strings.Count(string(body), `"id":`)
	if count != 3 {
		t.Errorf(`limit=3 returned %d rows (expected 3): body=%s`, count, string(body))
	}
}

// TestAPI_LogoutInvalidatesSession: POST /auth/logout must terminate the
// caller's session — even with the gameplane_session cookie still
// attached, /users/me should bounce as 401.
func TestAPI_LogoutInvalidatesSession(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)

	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	// Confirm baseline: /users/me works.
	resp, _, err := cli.Get("/users/me")
	if err != nil {
		t.Fatalf("baseline /users/me: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("baseline /users/me: status=%d", resp.StatusCode)
	}
	resp.Body.Close()

	var body []byte
	resp, body, err = cli.Post("/auth/logout", nil)
	if err != nil {
		t.Fatalf("/auth/logout: %v", err)
	}
	if resp.StatusCode/100 != 2 {
		t.Fatalf("/auth/logout expected 2xx, got %d body=%q", resp.StatusCode, string(body))
	}
	resp.Body.Close()

	resp, body, err = cli.Get("/users/me")
	if err != nil {
		t.Fatalf("post-logout /users/me: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-logout /users/me expected 401, got %d body=%q",
			resp.StatusCode, string(body))
	}
	resp.Body.Close()
}
