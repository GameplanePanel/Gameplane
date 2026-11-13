//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ssoRulesFakeIdPRedirect is the one redirect_uri the in-cluster fake IdP
// accepts (deploy/kind/e2e.sh, REDIRECT_URI on gameplane-test-fakeoidc). The
// dashboard provider of this test derives a different callback URL from the
// External URL, so ssoRulesLogin swaps the authorize request's redirect_uri
// for this value; the fake IdP's /token never checks it.
const ssoRulesFakeIdPRedirect = "https://gameplane.e2e.invalid/auth/oidc/helm/callback"

// ssoRulesFakeIdPClientID is the audience the fake IdP puts in every ID token
// (CLIENT_ID on the same Deployment), so the dashboard provider must use it.
const ssoRulesFakeIdPClientID = "gameplane-e2e-helm-oidc"

// ssoRulesFakeIdPIssuer is the in-cluster issuer URL the API pod dials.
const ssoRulesFakeIdPIssuer = "http://gameplane-test-fakeoidc.gameplane-system.svc.cluster.local:8080"

// ssoRulesLoginResult is one finished SSO round trip.
type ssoRulesLoginResult struct {
	// Status is the callback's status: 302 for an admitted sign-in, 403 for a
	// refused one.
	Status int
	// UserID and Role come from GET /users/me; set only when Status is 302.
	UserID int64
	Role   string
}

// ssoRulesLogin drives one authorization-code round trip against the
// dashboard-managed provider and the in-cluster fake IdP, like oidcHelmLogin
// but for a named provider and tolerant of a refused sign-in. A 429 from the
// start or callback limiter is retried from the top, so a neighbouring test's
// drained bucket cannot flake this one.
func ssoRulesLogin(t *testing.T, apiBase, idpBase, provider, sub, email string, groups []string) ssoRulesLoginResult {
	t.Helper()
	var last string
	for attempt := 0; attempt < 12; attempt++ {
		res, retry, detail := ssoRulesLoginOnce(t, apiBase, idpBase, provider, sub, email, groups)
		if !retry {
			return res
		}
		last = detail
		select {
		case <-t.Context().Done():
			t.Fatalf("sso login for %s cancelled: %v", sub, t.Context().Err())
		case <-time.After(10 * time.Second):
		}
	}
	t.Fatalf("sso login for %s kept hitting the rate limiter: %s", sub, last)
	return ssoRulesLoginResult{}
}

// ssoRulesLoginOnce is one attempt; retry is true on a 429.
func ssoRulesLoginOnce(t *testing.T, apiBase, idpBase, provider, sub, email string, groups []string) (res ssoRulesLoginResult, retry bool, detail string) {
	t.Helper()
	cli := &http.Client{
		Jar:           newInsecureCookieJar(),
		Timeout:       30 * time.Second,
		CheckRedirect: oidcNoRedirect,
	}

	startResp := oidcDo(t, cli, http.MethodGet, apiBase+"/auth/oidc/"+provider+"/start", nil)
	_ = startResp.Body.Close()
	if startResp.StatusCode == http.StatusTooManyRequests {
		return res, true, "start returned 429"
	}
	if startResp.StatusCode != http.StatusFound {
		t.Fatalf("oidc start status = %d, want 302", startResp.StatusCode)
	}
	authorizeURL, err := url.Parse(startResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse start Location: %v", err)
	}
	idpURL, err := url.Parse(idpBase)
	if err != nil {
		t.Fatalf("parse idp base: %v", err)
	}
	authorizeURL.Scheme = idpURL.Scheme
	authorizeURL.Host = idpURL.Host
	q := authorizeURL.Query()
	q.Set("redirect_uri", ssoRulesFakeIdPRedirect)
	q.Set("sub", sub)
	q.Set("email", email)
	q.Set("groups", strings.Join(groups, ","))
	authorizeURL.RawQuery = q.Encode()

	authorizeResp := oidcDo(t, cli, http.MethodGet, authorizeURL.String(), nil)
	_ = authorizeResp.Body.Close()
	if authorizeResp.StatusCode != http.StatusFound {
		t.Fatalf("fake idp authorize status = %d, want 302", authorizeResp.StatusCode)
	}
	callbackLoc, err := url.Parse(authorizeResp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize Location: %v", err)
	}

	cbResp := oidcDo(t, cli, http.MethodGet, apiBase+"/auth/oidc/"+provider+"/callback?"+callbackLoc.RawQuery, nil)
	cbBody, _ := io.ReadAll(cbResp.Body)
	_ = cbResp.Body.Close()
	if cbResp.StatusCode == http.StatusTooManyRequests {
		return res, true, "callback returned 429"
	}
	res.Status = cbResp.StatusCode
	if cbResp.StatusCode != http.StatusFound {
		if cbResp.StatusCode != http.StatusForbidden {
			t.Fatalf("oidc callback status = %d, want 302 or 403: %s", cbResp.StatusCode, string(cbBody))
		}
		return res, false, ""
	}

	meResp := oidcDo(t, cli, http.MethodGet, apiBase+"/users/me", nil)
	meBody, _ := io.ReadAll(meResp.Body)
	_ = meResp.Body.Close()
	if meResp.StatusCode != http.StatusOK {
		t.Fatalf("/users/me %d: %s", meResp.StatusCode, string(meBody))
	}
	var me struct {
		ID   int64  `json:"id"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(meBody, &me); err != nil || me.ID == 0 {
		t.Fatalf("decode /users/me: %v\n%s", err, string(meBody))
	}
	res.UserID = me.ID
	res.Role = me.Role
	return res, false, ""
}

// ssoRulesUser is one row of GET /users, the fields this test reads.
type ssoRulesUser struct {
	ID         int64  `json:"id"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	RoleSource string `json:"roleSource"`
}

func ssoRulesListUsers(t *testing.T, admin *APIClient) []ssoRulesUser {
	t.Helper()
	resp, body, err := admin.Get("/users")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("list users: err=%v body=%s", err, string(body))
	}
	var users []ssoRulesUser
	if err := json.Unmarshal(body, &users); err != nil {
		t.Fatalf("decode users: %v\n%s", err, string(body))
	}
	return users
}

// ssoRulesUserByID returns the user with the given id, failing when absent.
func ssoRulesUserByID(t *testing.T, admin *APIClient, id int64) ssoRulesUser {
	t.Helper()
	for _, u := range ssoRulesListUsers(t, admin) {
		if u.ID == id {
			return u
		}
	}
	t.Fatalf("user %d not in GET /users", id)
	return ssoRulesUser{}
}

// ssoRulesProblem sends one request asking for an RFC 9457 problem document
// and returns the status, the messageId and the args, so a test can assert on
// the stable message key rather than the English text.
func ssoRulesProblem(t *testing.T, admin *APIClient, method, path string, body any) (status int, messageID string, args map[string]any) {
	t.Helper()
	var br io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		br = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, admin.BaseURL+path, br)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	req.Header.Set("Accept", "application/problem+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if isMutation(method) {
		req.Header.Set("X-Gameplane-CSRF", admin.CSRF)
	}
	resp, err := admin.HTTP.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var p struct {
		MessageID string         `json:"messageId"`
		Args      map[string]any `json:"args"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("%s %s: response is not a problem document (status %d): %s", method, path, resp.StatusCode, string(raw))
	}
	return resp.StatusCode, p.MessageID, p.Args
}

// TestAPI_SSORoleRules covers spec 034 over the real API and the in-cluster
// fake IdP: an ordered rule list that maps IdP groups to custom roles, the
// refusals around it, and the role-source behaviour at sign-in (a role set by
// hand is kept, an SSO-managed user follows the rules, and "return to SSO
// control" hands a user back).
//
// Budget: ONE e2e-admin login (the same session drives every mutation). The
// test adds its own dashboard-managed OIDC provider pointing at the fake IdP,
// so it never writes helmOverride and never touches the Helm provider that
// TestAPI_OIDCHelmSeeded_* rely on. It makes 8 SSO callbacks
// (auth.OIDCCallbackLimiter, burst 10/min), counted per subtest below; a 429
// is retried. It is the only test in its bucket that writes
// /admin/config/auth or /admin/config/general.
//
// The subtests are sequential and share state on purpose: each builds on the
// users and rules the one before left behind.
func TestAPI_SSORoleRules(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()
	apiBase, idpBase := oidcHelmPorts(t)

	stamp := time.Now().UnixNano()
	const (
		provider = "e2e-ssorr"
		roleA    = "e2e-ssorr-role-a"
		roleB    = "e2e-ssorr-role-b"
		grpA     = "e2e-ssorr grp a"
		grpB     = "e2e-ssorr grp b"
		grpOther = "e2e-ssorr-unmapped"
	)
	closeBody := func(r *http.Response) {
		if r != nil {
			_ = r.Body.Close()
		}
	}

	// Cleanups run last-in first: users, then the auth config that names the
	// roles, then the Secret, then the roles themselves.
	for _, role := range []string{roleA, roleB} {
		t.Cleanup(func() {
			r, _, _ := admin.Delete("/roles/" + role)
			closeBody(r)
		})
	}
	t.Cleanup(func() {
		r, _, _ := admin.Do(http.MethodDelete, "/admin/auth/providers/"+provider+"/secret", nil)
		closeBody(r)
	})
	localOnly := []map[string]any{{"name": "local", "kind": "local", "enabled": true}}
	t.Cleanup(func() {
		r, _, _ := admin.Do(http.MethodPut, "/admin/config/auth", map[string]any{"providers": localOnly})
		closeBody(r)
	})
	var userIDs []int64
	t.Cleanup(func() {
		for _, id := range userIDs {
			r, _, _ := admin.Delete(fmt.Sprintf("/users/%d", id))
			closeBody(r)
		}
	})

	// Two custom roles with different permissions, so a rule result is
	// distinguishable by name.
	for _, c := range []struct {
		name  string
		perms []string
	}{{roleA, []string{"servers:read"}}, {roleB, []string{"servers:read", "backups:read"}}} {
		resp, body, err := admin.Post("/roles", map[string]any{
			"name": c.name, "description": "spec 034 e2e", "permissions": c.perms,
		})
		if err != nil || resp.StatusCode != http.StatusCreated {
			t.Fatalf("create role %s: err=%v body=%s", c.name, err, string(body))
		}
	}

	// The provider's redirect URL derives from General -> External URL. Only
	// fill it in when empty, keeping the section's other fields, so another
	// test's values are never overwritten. GET /admin/config returns every
	// section keyed by name; there is no per-section GET.
	var sections map[string]json.RawMessage
	if resp, body, err := admin.Get("/admin/config"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("get config: err=%v body=%s", err, string(body))
	} else if err := json.Unmarshal(body, &sections); err != nil {
		t.Fatalf("decode config: %v\n%s", err, string(body))
	}
	general := map[string]any{}
	if raw, ok := sections["general"]; ok {
		if err := json.Unmarshal(raw, &general); err != nil {
			t.Fatalf("decode general config: %v\n%s", err, string(raw))
		}
	}
	if ext, _ := general["externalURL"].(string); ext == "" {
		general["externalURL"] = "https://gameplane.e2e.example"
		if ns, _ := general["defaultNamespace"].(string); ns == "" {
			general["defaultNamespace"] = "gameplane-games"
		}
		resp, body, err := admin.Do(http.MethodPut, "/admin/config/general", general)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("set general config: err=%v body=%s", err, string(body))
		}
	}

	resp, body, err := admin.Do(http.MethodPut, "/admin/auth/providers/"+provider+"/secret",
		map[string]string{"clientSecret": "e2e-client-secret"})
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("put provider secret: err=%v body=%s", err, string(body))
	}

	// save PUTs the auth section: local login plus this provider with the
	// given ordered rules and fallback. It returns the raw status and problem
	// message id so refusals can be asserted too.
	save := func(rules []map[string]string, fallback string) (int, string) {
		t.Helper()
		status, msgID, _ := ssoRulesProblem(t, admin, http.MethodPut, "/admin/config/auth", map[string]any{
			"providers": append(append([]map[string]any{}, localOnly...), map[string]any{
				"name": provider, "kind": "oidc", "displayName": "E2E rules", "enabled": true,
				"issuer": ssoRulesFakeIdPIssuer, "clientID": ssoRulesFakeIdPClientID,
				"groupsClaim": "groups", "roleRules": rules, "defaultRole": fallback,
			}),
		})
		return status, msgID
	}
	mustSave := func(rules []map[string]string, fallback string) {
		t.Helper()
		if status, msgID := save(rules, fallback); status != http.StatusOK {
			t.Fatalf("save auth config rules=%v fallback=%q: status=%d messageId=%s", rules, fallback, status, msgID)
		}
	}
	login := func(sub, email string, groups ...string) ssoRulesLoginResult {
		t.Helper()
		res := ssoRulesLogin(t, apiBase, idpBase, provider, sub, email, groups)
		if res.UserID != 0 {
			seen := false
			for _, id := range userIDs {
				seen = seen || id == res.UserID
			}
			if !seen {
				userIDs = append(userIDs, res.UserID)
			}
		}
		return res
	}

	ruleA := map[string]string{"group": grpA, "role": roleA}
	ruleB := map[string]string{"group": grpB, "role": roleB}

	sub1 := fmt.Sprintf("e2e-ssorr-one-%d", stamp)
	email1 := sub1 + "@e2e.example"
	sub2 := fmt.Sprintf("e2e-ssorr-two-%d", stamp)
	email2 := sub2 + "@e2e.example"
	sub3 := fmt.Sprintf("e2e-ssorr-three-%d", stamp)
	email3 := sub3 + "@e2e.example"

	mustSave([]map[string]string{ruleA, ruleB}, "deny")
	var user1, user2 int64

	// SSO callbacks so far: 0.
	t.Run("custom role rule", func(t *testing.T) {
		// 1 callback: a new user in group A only gets custom role A, SSO-managed.
		res := login(sub1, email1, grpA)
		if res.Status != http.StatusFound || res.Role != roleA {
			t.Fatalf("login = %+v, want admitted with role %s", res, roleA)
		}
		user1 = res.UserID
		if u := ssoRulesUserByID(t, admin, user1); u.RoleSource != "sso" || u.Role != roleA {
			t.Fatalf("user row = %+v, want role %s from sso", u, roleA)
		}
	})

	t.Run("rule order", func(t *testing.T) {
		// 2 callbacks (running total 3): in both groups, the first listed
		// rule wins; reordering the list changes the result on the next
		// sign-in, because the user is SSO-managed.
		res := login(sub2, email2, grpA, grpB)
		if res.Status != http.StatusFound || res.Role != roleA {
			t.Fatalf("login with rules [A,B] = %+v, want role %s", res, roleA)
		}
		user2 = res.UserID
		mustSave([]map[string]string{ruleB, ruleA}, "deny")
		res = login(sub2, email2, grpA, grpB)
		if res.Status != http.StatusFound || res.Role != roleB {
			t.Fatalf("login with rules [B,A] = %+v, want role %s", res, roleB)
		}
	})

	t.Run("deny fallback refused", func(t *testing.T) {
		// 1 callback (total 4): a new user in no mapped group, fallback
		// deny: refused with 403 and no account created.
		res := login(sub3, email3, grpOther)
		if res.Status != http.StatusForbidden {
			t.Fatalf("login = %+v, want 403", res)
		}
		for _, u := range ssoRulesListUsers(t, admin) {
			if u.Email == email3 {
				t.Fatalf("refused sign-in created user %+v", u)
			}
		}
	})

	t.Run("unknown role save refused", func(t *testing.T) {
		// No callbacks.
		status, msgID := save([]map[string]string{{"group": grpA, "role": "e2e-ssorr-no-such-role"}}, "deny")
		if status != http.StatusBadRequest || msgID != "server.config.auth_role_unknown" {
			t.Fatalf("unknown rule role: status=%d messageId=%s, want 400 server.config.auth_role_unknown", status, msgID)
		}
		status, msgID = save([]map[string]string{ruleA}, "e2e-ssorr-no-such-role")
		if status != http.StatusBadRequest || msgID != "server.config.auth_role_unknown" {
			t.Fatalf("unknown fallback role: status=%d messageId=%s, want 400 server.config.auth_role_unknown", status, msgID)
		}
	})

	t.Run("delete of a used role refused", func(t *testing.T) {
		// No callbacks. The saved config (rules [B,A]) still names both roles.
		for _, role := range []string{roleA, roleB} {
			status, msgID, args := ssoRulesProblem(t, admin, http.MethodDelete, "/roles/"+role, nil)
			if status != http.StatusConflict || msgID != "server.roles.in_use_by_sso" {
				t.Fatalf("delete %s: status=%d messageId=%s, want 409 server.roles.in_use_by_sso", role, status, msgID)
			}
			if got, _ := args["provider"].(string); got != provider {
				t.Fatalf("delete %s: args.provider = %v, want %s", role, args["provider"], provider)
			}
		}
	})

	t.Run("hand-set role kept at sign-in", func(t *testing.T) {
		// 2 callbacks (total 6). user1 gets role B by hand while the rules
		// still give A; sign-in leaves it alone, and so does a sign-in in no
		// mapped group under a deny fallback (the hand-set user is admitted).
		mustSave([]map[string]string{ruleA, ruleB}, "deny")
		resp, body, err := admin.Patch(fmt.Sprintf("/users/%d", user1), map[string]string{"role": roleB})
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("set role by hand: err=%v body=%s", err, string(body))
		}
		if u := ssoRulesUserByID(t, admin, user1); u.Role != roleB || u.RoleSource != "manual" {
			t.Fatalf("after PATCH user = %+v, want role %s from manual", u, roleB)
		}

		res := login(sub1, email1, grpA)
		if res.Status != http.StatusFound || res.Role != roleB {
			t.Fatalf("login in group A = %+v, want hand-set role %s kept", res, roleB)
		}

		res = login(sub1, email1, grpOther)
		if res.Status != http.StatusFound || res.Role != roleB {
			t.Fatalf("login in no mapped group under deny = %+v, want admitted with hand-set role %s", res, roleB)
		}
		if u := ssoRulesUserByID(t, admin, user1); u.Role != roleB || u.RoleSource != "manual" {
			t.Fatalf("after sign-ins user = %+v, want role %s still from manual", u, roleB)
		}
	})

	t.Run("sso user removed from every group refused", func(t *testing.T) {
		// 1 callback (total 7). user2 is SSO-managed; with no mapped group
		// left and a deny fallback the sign-in is refused and the stored role
		// and source stay as they were.
		res := login(sub2, email2, grpOther)
		if res.Status != http.StatusForbidden {
			t.Fatalf("login = %+v, want 403", res)
		}
		if u := ssoRulesUserByID(t, admin, user2); u.Role != roleB || u.RoleSource != "sso" {
			t.Fatalf("after refusal user = %+v, want role %s from sso unchanged", u, roleB)
		}
	})

	t.Run("return to sso control", func(t *testing.T) {
		// 1 callback (total 8). Returning user1 to SSO control keeps the
		// role for now (sso_pending); the next sign-in applies the rule
		// result (A) and the source becomes sso.
		resp, body, err := admin.Patch(fmt.Sprintf("/users/%d", user1), map[string]string{"roleSource": "sso"})
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("return to sso control: err=%v body=%s", err, string(body))
		}
		if u := ssoRulesUserByID(t, admin, user1); u.Role != roleB || u.RoleSource != "sso_pending" {
			t.Fatalf("after return user = %+v, want role %s kept, source sso_pending", u, roleB)
		}

		res := login(sub1, email1, grpA)
		if res.Status != http.StatusFound || res.Role != roleA {
			t.Fatalf("login after return = %+v, want rule result %s", res, roleA)
		}
		if u := ssoRulesUserByID(t, admin, user1); u.Role != roleA || u.RoleSource != "sso" {
			t.Fatalf("after sign-in user = %+v, want role %s from sso", u, roleA)
		}
	})
}
