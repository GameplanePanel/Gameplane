//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestAPI_RBAC_MCPTokenReads proves that a personal MCP token carries exactly
// the caller's own RBAC: reads follow the user's namespace grant, writes are
// refused by the token guard, an admin can list (without secrets) and revoke
// the user's tokens, and revocation, removing the grant, and an admin password
// reset each cut the token off. Budget: two logins (the
// e2e-admin fixture and the throwaway user).
func TestAPI_RBAC_MCPTokenReads(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	suffix := time.Now().UnixNano()
	const grantedNS = "gameplane-games"
	otherNS := fmt.Sprintf("e2e-mcp-other-%d", suffix)
	readerRole := fmt.Sprintf("e2e-mcp-rd-%d", suffix)
	// The user's primary role is mirrored as a cluster-wide binding, so it
	// must carry no permissions; the namespace grant comes from readerRole.
	emptyRole := fmt.Sprintf("e2e-mcp-none-%d", suffix)

	resp, body, err := admin.Post("/roles", map[string]any{
		"name":        readerRole,
		"description": "MCP token read helper",
		"permissions": []string{"servers:read"},
	})
	if err != nil {
		t.Fatalf("create reader role: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create reader role: status=%d body=%s", resp.StatusCode, body)
	}
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/roles/" + readerRole)
		if r != nil {
			_ = r.Body.Close()
		}
	})

	resp, body, err = admin.Post("/roles", map[string]any{
		"name":        emptyRole,
		"description": "MCP token no-permission primary role",
		"permissions": []string{},
	})
	if err != nil {
		t.Fatalf("create empty role: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create empty role: status=%d body=%s", resp.StatusCode, body)
	}
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/roles/" + emptyRole)
		if r != nil {
			_ = r.Body.Close()
		}
	})

	userName, userPW, userID := envInstance.CreateUser(t, admin, emptyRole, "e2e-mcp-reads")
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/users/" + userID)
		if r != nil {
			_ = r.Body.Close()
		}
	})

	resp, body, err = admin.Post("/users/"+userID+"/bindings", map[string]any{
		"roleName":  readerRole,
		"namespace": grantedNS,
	})
	if err != nil {
		t.Fatalf("add namespace binding: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add namespace binding: status=%d body=%s", resp.StatusCode, body)
	}

	// Log in after the binding: binding changes invalidate existing sessions.
	user := envInstance.APIClient(t, userName, userPW)
	defer user.Close()

	// Create all tokens while the session is valid; the admin's later
	// binding removal and password reset end that session.
	readID, tokenRead := mcpTokenReadsCreate(t, user, fmt.Sprintf("e2e-mcp-read-%d", suffix))
	revokeID, tokenRevoke := mcpTokenReadsCreate(t, user, fmt.Sprintf("e2e-mcp-revoke-%d", suffix))
	_, tokenReset := mcpTokenReadsCreate(t, user, fmt.Sprintf("e2e-mcp-reset-%d", suffix))

	// --- Reads and refusals with the granted token ---------------------------
	status, b := mcpTokenReadsCall(t, user.BaseURL, tokenRead, http.MethodGet, "/servers")
	if status != http.StatusOK {
		t.Errorf("GET /servers with token: status=%d want 200 body=%s", status, b)
	} else {
		var list struct {
			Items []struct {
				Metadata struct {
					Namespace string `json:"namespace"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err := json.Unmarshal(b, &list); err != nil {
			t.Errorf("GET /servers with token: decode: %v body=%s", err, b)
		}
		for _, item := range list.Items {
			if item.Metadata.Namespace != grantedNS {
				t.Errorf("GET /servers with token listed namespace %q outside grant %q", item.Metadata.Namespace, grantedNS)
			}
		}
	}

	status, b = mcpTokenReadsCall(t, user.BaseURL, tokenRead, http.MethodGet, "/pods?namespace="+grantedNS)
	if status != http.StatusOK {
		t.Errorf("GET /pods granted ns with token: status=%d want 200 body=%s", status, b)
	}

	status, b = mcpTokenReadsCall(t, user.BaseURL, tokenRead, http.MethodGet, "/pods?namespace="+otherNS)
	// An ungranted namespace is refused before the handler (400 namespace not permitted).
	if status != http.StatusBadRequest && status != http.StatusForbidden {
		t.Errorf("GET /pods ungranted ns with token: status=%d want a 400/403 refusal body=%s", status, b)
	}

	status, b = mcpTokenReadsCall(t, user.BaseURL, tokenRead, http.MethodGet, "/kube-events?namespace="+grantedNS)
	if status != http.StatusOK {
		t.Errorf("GET /kube-events granted ns with token: status=%d want 200 body=%s", status, b)
	}

	status, b = mcpTokenReadsCall(t, user.BaseURL, tokenRead, http.MethodPost, "/servers")
	if status != http.StatusForbidden {
		t.Errorf("POST /servers with token: status=%d want 403 body=%s", status, b)
	}

	// --- Revocation by the owner ---------------------------------------------
	resp, body, err = user.Delete(fmt.Sprintf("/users/me/mcp-tokens/%d", revokeID))
	if err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke token: status=%d want 204 body=%s", resp.StatusCode, body)
	}
	status, b = mcpTokenReadsCall(t, user.BaseURL, tokenRevoke, http.MethodGet, "/servers")
	if status != http.StatusUnauthorized {
		t.Errorf("GET /servers with revoked token: status=%d want 401 body=%s", status, b)
	}

	// --- Admin removes the namespace grant -----------------------------------
	resp, body, err = admin.Delete("/users/" + userID + "/bindings/" + readerRole + "/" + grantedNS)
	if err != nil {
		t.Fatalf("remove binding: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("remove binding: status=%d want 204 body=%s", resp.StatusCode, body)
	}
	status, b = mcpTokenReadsCall(t, user.BaseURL, tokenRead, http.MethodGet, "/pods?namespace="+grantedNS)
	// With no grant left on the cluster, the token gets the unknown-cluster
	// answer (FR-021: 400 cluster not permitted).
	if status != http.StatusBadRequest && status != http.StatusForbidden {
		t.Errorf("GET /pods after grant removed: status=%d want a 400/403 refusal body=%s", status, b)
	}

	// --- Admin lists the user's tokens and revokes one -----------------------
	resp, body, err = admin.Get("/users/" + userID + "/mcp-tokens")
	if err != nil {
		t.Fatalf("admin list tokens: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin list tokens: status=%d want 200 body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), tokenRead) {
		t.Errorf("admin list tokens: response contains a token secret")
	}
	var listed struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		t.Fatalf("admin list tokens: decode: %v body=%s", err, body)
	}
	found := false
	for _, it := range listed.Items {
		if it.ID == readID {
			found = true
		}
	}
	if !found {
		t.Errorf("admin list tokens: token %d missing from body=%s", readID, body)
	}
	resp, body, err = admin.Delete(fmt.Sprintf("/users/%s/mcp-tokens/%d", userID, readID))
	if err != nil {
		t.Fatalf("admin revoke token: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("admin revoke token: status=%d want 204 body=%s", resp.StatusCode, body)
	}
	status, b = mcpTokenReadsCall(t, user.BaseURL, tokenRead, http.MethodGet, "/users/me")
	if status != http.StatusUnauthorized {
		t.Errorf("GET /users/me with admin-revoked token: status=%d want 401 body=%s", status, b)
	}

	// --- Admin password reset ends remaining tokens --------------------------
	const newPW = "e2e-mcp-reset-password-1234"
	resp, body, err = admin.Post("/users/"+userID+"/reset-password", map[string]string{
		"password": newPW,
	})
	if err != nil {
		t.Fatalf("reset-password: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("reset-password: status=%d want 204 body=%s", resp.StatusCode, body)
	}
	status, b = mcpTokenReadsCall(t, user.BaseURL, tokenReset, http.MethodGet, "/users/me")
	if status != http.StatusUnauthorized {
		t.Errorf("GET /users/me with token after password reset: status=%d want 401 body=%s", status, b)
	}
}

// mcpTokenReadsCreate creates a 30-day personal MCP token through the user's
// session and returns its id and the one-time token string.
func mcpTokenReadsCreate(t *testing.T, cli *APIClient, name string) (int64, string) {
	t.Helper()
	resp, body, err := cli.Post("/users/me/mcp-tokens", map[string]any{
		"name":          name,
		"expiresInDays": 30,
	})
	if err != nil {
		t.Fatalf("create MCP token %s: %v", name, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create MCP token %s: status=%d body=%s", name, resp.StatusCode, body)
	}
	var out struct {
		ID    int64  `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode MCP token %s: %v body=%s", name, err, body)
	}
	if !strings.HasPrefix(out.Token, "gpmcp_") {
		t.Fatalf("MCP token %s: unexpected token format", name)
	}
	return out.ID, out.Token
}

// mcpTokenReadsCall sends a request authenticated only by the bearer token:
// no session cookie and no CSRF header.
func mcpTokenReadsCall(t *testing.T, baseURL, token, method, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, baseURL+path, nil)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	return resp.StatusCode, b
}
