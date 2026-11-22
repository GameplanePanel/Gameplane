//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestMulticluster_MCPTokenRemoteGrant proves that a personal MCP token
// follows its owner's per-cluster grants on a registered REMOTE cluster, not
// just the home cluster. The owner has no local grant at all, and only a
// namespace-scoped servers:read grant on the remote cluster. With that token:
// a namespaced read on the remote cluster succeeds, the same read against the
// home cluster or against an unknown cluster id is refused with an identical
// status and body, and a cluster-wide read on the remote cluster (no
// namespace) is refused. Requires the dual-cluster setup described in the
// package doc of multicluster_e2e_test.go (see ensureClusterB).
//
// Budget: one e2e-admin login (the admin fixture) and one throwaway user login.
func TestMulticluster_MCPTokenRemoteGrant(t *testing.T) {
	t.Parallel()

	_, clusterBName := ensureClusterB(t)

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	suffix := time.Now().UnixNano()
	const ns = "gameplane-games"
	clusterID := fmt.Sprintf("e2e-mcp-mc-%d", suffix)
	readerRole := fmt.Sprintf("e2e-mcp-mc-rd-%d", suffix)
	// The user's primary role is mirrored as a cluster-wide binding on the
	// home cluster, so it carries no permissions: the only grant the user
	// holds is the remote namespace binding added below.
	emptyRole := fmt.Sprintf("e2e-mcp-mc-none-%d", suffix)
	unknownCluster := fmt.Sprintf("e2e-mcp-mc-does-not-exist-%d", suffix)

	mcpMCCreateRole(t, admin, readerRole, []string{"servers:read"})
	mcpMCCreateRole(t, admin, emptyRole, []string{})

	mcpMCRegisterRemoteCluster(t, admin, clusterID, clusterBName)

	userName, userPW, userID := envInstance.CreateUser(t, admin, emptyRole, "e2e-mcp-mc")
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/users/" + userID)
		if r != nil {
			_ = r.Body.Close()
		}
	})

	resp, body, err := admin.Post("/users/"+userID+"/bindings", map[string]any{
		"roleName":  readerRole,
		"cluster":   clusterID,
		"namespace": ns,
	})
	if err != nil {
		t.Fatalf("add remote namespace binding: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add remote namespace binding: status=%d body=%s", resp.StatusCode, body)
	}
	_ = resp.Body.Close()

	// Log in after the binding: binding changes invalidate existing sessions.
	user := envInstance.APIClient(t, userName, userPW)
	defer user.Close()

	_, token := mcpTokenReadsCreate(t, user, fmt.Sprintf("e2e-mcp-mc-%d", suffix))

	// --- Granted: a namespaced read on the remote cluster succeeds ----------
	status, b := mcpTokenReadsCall(t, user.BaseURL, token, http.MethodGet,
		"/pods?namespace="+ns+"&cluster="+clusterID)
	if status != http.StatusOK {
		t.Errorf("GET /pods granted ns on remote cluster with token: status=%d want 200 body=%s", status, b)
	}

	// --- Ungranted: the home cluster is refused, exactly like an unknown id --
	// The owner holds no grant on "local", so the request is refused there; an
	// unregistered id must answer with the same status and body, so the caller
	// cannot probe which cluster ids are registered.
	localStatus, localBody := mcpTokenReadsCall(t, user.BaseURL, token, http.MethodGet,
		"/pods?namespace="+ns+"&cluster=local")
	if localStatus < 400 || localStatus >= 500 {
		t.Errorf("GET /pods ungranted local cluster with token: status=%d want a 4xx refusal body=%s", localStatus, localBody)
	}
	unknownStatus, unknownBody := mcpTokenReadsCall(t, user.BaseURL, token, http.MethodGet,
		"/pods?namespace="+ns+"&cluster="+unknownCluster)
	if unknownStatus != localStatus || !bytes.Equal(unknownBody, localBody) {
		t.Errorf("GET /pods unknown cluster with token: status=%d body=%s; want identical to ungranted status=%d body=%s",
			unknownStatus, unknownBody, localStatus, localBody)
	}

	// --- Namespace-only grant: other namespaces on the remote cluster are refused ---
	status, b = mcpTokenReadsCall(t, user.BaseURL, token, http.MethodGet,
		"/pods?namespace=kube-system&cluster="+clusterID)
	if status != http.StatusForbidden {
		t.Errorf("GET /pods kube-system on remote cluster with namespace-only grant: status=%d want 403 body=%s", status, b)
	}
}

// mcpMCCreateRole creates a custom role and removes it at cleanup.
func mcpMCCreateRole(t *testing.T, admin *APIClient, name string, permissions []string) {
	t.Helper()
	resp, body, err := admin.Post("/roles", map[string]any{
		"name":        name,
		"description": "MCP token multicluster helper",
		"permissions": permissions,
	})
	if err != nil {
		t.Fatalf("create role %s: %v", name, err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create role %s: status=%d body=%s", name, resp.StatusCode, body)
	}
	_ = resp.Body.Close()
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/roles/" + name)
		if r != nil {
			_ = r.Body.Close()
		}
	})
}

// mcpMCRegisterRemoteCluster registers the kind cluster kindName as a remote
// target under clusterID, waits until the API reports it Healthy, and
// deregisters it at cleanup.
func mcpMCRegisterRemoteCluster(t *testing.T, admin *APIClient, clusterID, kindName string) {
	t.Helper()
	kubeconfig := podReachableKubeconfig(t, kindName)
	resp, body, err := admin.Post("/clusters", map[string]string{
		"name":        clusterID,
		"displayName": "E2E MCP remote cluster",
		"kubeconfig":  string(kubeconfig),
	})
	if err != nil {
		t.Fatalf("POST /clusters: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /clusters: status=%d body=%s", resp.StatusCode, string(body))
	}
	_ = resp.Body.Close()
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/clusters/" + clusterID)
		if r != nil {
			_ = r.Body.Close()
		}
	})

	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		resp, body, err := admin.Get("/clusters")
		if err != nil {
			return false, err.Error()
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("GET /clusters: status=%d body=%s", resp.StatusCode, string(body))
		}
		var discovery struct {
			Items []struct {
				Name  string `json:"name"`
				Phase string `json:"phase"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &discovery); err != nil {
			return false, err.Error()
		}
		for _, item := range discovery.Items {
			if item.Name == clusterID && item.Phase == "Healthy" {
				return true, ""
			}
		}
		return false, "cluster not yet healthy: " + string(body)
	})
}
