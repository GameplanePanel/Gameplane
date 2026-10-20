//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// i18nProblemAccept is the Accept header a dashboard caller sends to receive
// the RFC 9457 problem document (with its localizable messageId) instead of
// the plain-text body.
const i18nProblemAccept = "application/json, application/problem+json"

// rawDo sends one request through the client's cookie jar, so the session
// cookie rides along, and returns the status, Content-Type and raw body. It
// exists because APIClient.Do cannot set an Accept header.
func rawDo(t *testing.T, cli *APIClient, method, path string, body any, accept string) (int, string, []byte) {
	t.Helper()
	var br io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		br = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, cli.BaseURL+path, br)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if isMutation(method) {
		req.Header.Set("X-Gameplane-CSRF", cli.CSRF)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := cli.HTTP.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), raw
}

// decodeProblemBody decodes an RFC 9457 problem body and fails the test when
// the Content-Type is not application/problem+json.
func decodeProblemBody(t *testing.T, label, contentType string, raw []byte) map[string]any {
	t.Helper()
	if contentType != "application/problem+json" {
		t.Fatalf("%s: Content-Type=%q, want application/problem+json (body %q)", label, contentType, string(raw))
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: decode problem: %v (body %q)", label, err, string(raw))
	}
	return doc
}

// requireProblemFields asserts the status, messageId and detail of a problem
// document.
func requireProblemFields(t *testing.T, label string, doc map[string]any, status int, messageID, detail string) {
	t.Helper()
	if doc["status"] != float64(status) {
		t.Errorf("%s: problem status=%v, want %d", label, doc["status"], status)
	}
	if doc["messageId"] != messageID {
		t.Errorf("%s: messageId=%v, want %q", label, doc["messageId"], messageID)
	}
	if doc["detail"] != detail {
		t.Errorf("%s: detail=%v, want %q", label, doc["detail"], detail)
	}
}

// TestAPI_I18n_ProblemJSON checks the content negotiation of the API error
// responses end to end: a caller that asks for application/problem+json gets
// a problem document with a registry messageId and args, every other caller
// keeps the byte-identical plain-text body, and the login error stays
// identical for unknown users and wrong passwords (FR-030).
//
// Budget: one e2e-admin login (to create the user) plus one viewer login and
// one failed-login pair on the raw client. It runs on the primary cluster.
func TestAPI_I18n_ProblemJSON(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	username, password, id := envInstance.CreateUser(t, admin, "viewer", "e2e-i18n-pj")
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/users/" + id)
		if r != nil {
			_ = r.Body.Close()
		}
	})
	viewer := envInstance.APIClient(t, username, password)
	defer viewer.Close()

	// Forbidden: a plain viewer cannot create a server. The RBAC middleware
	// rejects the request before the body is read.
	gs := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer",
		"metadata": map[string]any{"name": "e2e-i18n-denied", "namespace": "gameplane-games"},
		"spec":     map[string]any{"templateRef": map[string]any{"name": "e2e-i18n-template"}},
	}
	status, ct, raw := rawDo(t, viewer, http.MethodPost, "/servers", gs, "")
	if status != http.StatusForbidden {
		t.Fatalf("viewer POST /servers (plain): status=%d body=%q", status, string(raw))
	}
	if !strings.HasPrefix(ct, "text/plain") || string(raw) != "forbidden\n" {
		t.Errorf("viewer POST /servers (plain): Content-Type=%q body=%q, want text/plain and \"forbidden\\n\"", ct, string(raw))
	}
	for _, accept := range []string{"*/*", "application/problem+json;q=0"} {
		status, ct, raw = rawDo(t, viewer, http.MethodPost, "/servers", gs, accept)
		if status != http.StatusForbidden {
			t.Fatalf("viewer POST /servers (Accept %q): status=%d", accept, status)
		}
		if !strings.HasPrefix(ct, "text/plain") || string(raw) != "forbidden\n" {
			t.Errorf("viewer POST /servers (Accept %q): Content-Type=%q body=%q, want the plain body", accept, ct, string(raw))
		}
	}
	status, ct, raw = rawDo(t, viewer, http.MethodPost, "/servers", gs, i18nProblemAccept)
	if status != http.StatusForbidden {
		t.Fatalf("viewer POST /servers (problem): status=%d body=%q", status, string(raw))
	}
	doc := decodeProblemBody(t, "viewer POST /servers (problem)", ct, raw)
	requireProblemFields(t, "viewer POST /servers (problem)", doc, http.StatusForbidden, "server.forbidden", "forbidden")
	if args, ok := doc["args"].(map[string]any); !ok || len(args) != 0 {
		t.Errorf("viewer POST /servers (problem): args=%v, want empty object", doc["args"])
	}

	// Validation: an unsupported notification sink kind is a 422 whose plain
	// body and problem detail carry the same sentence.
	notif := map[string]any{"sinks": []any{map[string]any{"name": "x", "kind": "telegram", "enabled": false}}}
	const notifText = "sinks[0].kind must be one of discord|slack|smtp|webhook|ntfy"
	status, _, raw = rawDo(t, admin, http.MethodPut, "/admin/config/notifications", notif, "")
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("PUT /admin/config/notifications (plain): status=%d body=%q", status, string(raw))
	}
	if string(raw) != notifText+"\n" {
		t.Errorf("PUT /admin/config/notifications (plain): body=%q, want %q", string(raw), notifText+"\n")
	}
	status, ct, raw = rawDo(t, admin, http.MethodPut, "/admin/config/notifications", notif, i18nProblemAccept)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("PUT /admin/config/notifications (problem): status=%d body=%q", status, string(raw))
	}
	doc = decodeProblemBody(t, "PUT /admin/config/notifications (problem)", ct, raw)
	requireProblemFields(t, "PUT /admin/config/notifications (problem)", doc, http.StatusUnprocessableEntity, "server.generic.422", notifText)

	// Validation with an argument: an unlisted instance language is a 400
	// whose problem document carries the language as a message argument.
	status, _, raw = rawDo(t, admin, http.MethodPut, "/admin/config/i18n", map[string]any{"defaultLanguage": "zz"}, "")
	if status != http.StatusBadRequest {
		t.Fatalf("PUT /admin/config/i18n (plain): status=%d body=%q", status, string(raw))
	}
	if string(raw) != "language is not available: zz\n" {
		t.Errorf("PUT /admin/config/i18n (plain): body=%q", string(raw))
	}
	status, ct, raw = rawDo(t, admin, http.MethodPut, "/admin/config/i18n", map[string]any{"defaultLanguage": "zz"}, i18nProblemAccept)
	if status != http.StatusBadRequest {
		t.Fatalf("PUT /admin/config/i18n (problem): status=%d body=%q", status, string(raw))
	}
	doc = decodeProblemBody(t, "PUT /admin/config/i18n (problem)", ct, raw)
	requireProblemFields(t, "PUT /admin/config/i18n (problem)", doc, http.StatusBadRequest, "server.i18n.unlisted_language", "language is not available: zz")
	if args, ok := doc["args"].(map[string]any); !ok || args["language"] != "zz" {
		t.Errorf("PUT /admin/config/i18n (problem): args=%v, want language=zz", doc["args"])
	}

	// Login privacy (FR-030): an unknown user and a wrong password get the
	// same problem document, byte for byte. A fresh client without a cookie
	// jar keeps these attempts out of the viewer's session.
	rawLogin := func(body string) (int, string, []byte) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, viewer.BaseURL+"/auth/login", strings.NewReader(body))
		if err != nil {
			t.Fatalf("build login request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", i18nProblemAccept)
		resp, err := (&http.Client{Timeout: viewer.HTTP.Timeout}).Do(req)
		if err != nil {
			t.Fatalf("login do: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("login read: %v", err)
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("login rate limited (429) body=%q", string(b))
		}
		return resp.StatusCode, resp.Header.Get("Content-Type"), b
	}
	uStatus, uCT, uBody := rawLogin(`{"username":"e2e-i18n-does-not-exist","password":"definitely-not-the-password"}`)
	wStatus, wCT, wBody := rawLogin(`{"username":"` + username + `","password":"definitely-not-the-password"}`)
	if uStatus != http.StatusUnauthorized || wStatus != http.StatusUnauthorized {
		t.Fatalf("login statuses: unknown=%d wrong=%d, want 401 for both", uStatus, wStatus)
	}
	uDoc := decodeProblemBody(t, "login unknown user", uCT, uBody)
	requireProblemFields(t, "login unknown user", uDoc, http.StatusUnauthorized, "server.auth.invalid_credentials", "invalid credentials")
	wDoc := decodeProblemBody(t, "login wrong password", wCT, wBody)
	requireProblemFields(t, "login wrong password", wDoc, http.StatusUnauthorized, "server.auth.invalid_credentials", "invalid credentials")
	if string(uBody) != string(wBody) {
		t.Fatalf("login error responses differ — enumeration oracle:\n  unknown: %q\n  wrong:   %q", string(uBody), string(wBody))
	}

	// Unauthenticated: a request with no session cookie gets the generic
	// unauthenticated text, plain or as a problem document.
	anon := &APIClient{BaseURL: viewer.BaseURL, HTTP: &http.Client{Timeout: viewer.HTTP.Timeout}}
	status, _, raw = rawDo(t, anon, http.MethodGet, "/servers", nil, "")
	if status != http.StatusUnauthorized || string(raw) != "unauthenticated\n" {
		t.Errorf("anonymous GET /servers (plain): status=%d body=%q, want 401 \"unauthenticated\\n\"", status, string(raw))
	}
	status, ct, raw = rawDo(t, anon, http.MethodGet, "/servers", nil, i18nProblemAccept)
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous GET /servers (problem): status=%d body=%q", status, string(raw))
	}
	anonDoc := decodeProblemBody(t, "anonymous GET /servers (problem)", ct, raw)
	requireProblemFields(t, "anonymous GET /servers (problem)", anonDoc, http.StatusUnauthorized, "server.common.unauthenticated", "unauthenticated")
}
