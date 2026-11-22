//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ocMCPSA is the MCP server's service account as kubectl auth can-i expects it.
const ocMCPSA = "system:serviceaccount:gameplane-system:gameplane-mcp-server"

// ocCanI asks the API server whether the MCP server's service account may
// perform the action. kubectl auth can-i exits 1 for "no", so the verdict is
// read from the last output line; anything other than yes or no fails the test.
func ocCanI(ctx context.Context, t *testing.T, args ...string) bool {
	t.Helper()
	full := append([]string{"auth", "can-i"}, args...)
	full = append(full, "--as="+ocMCPSA)
	out, err := envInstance.Kubectl(ctx, full...)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	verdict := strings.TrimSpace(lines[len(lines)-1])
	switch verdict {
	case "yes":
		return true
	case "no":
		return false
	default:
		t.Fatalf("kubectl auth can-i %v: unexpected output %q (err=%v)", args, out, err)
		return false
	}
}

// ocDeployReady reports whether the named Deployment has at least want ready replicas.
func ocDeployReady(ctx context.Context, ns, name string, want int32) func() (bool, string) {
	return func() (bool, string) {
		d, err := envInstance.K8s.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		return d.Status.ReadyReplicas >= want, fmt.Sprintf("%s readyReplicas=%d want>=%d", name, d.Status.ReadyReplicas, want)
	}
}

// TestOptionalComponents_MCPScope checks the break-glass MCP server's
// namespaced RBAC and its scope guard, then proves it still answers in the
// release namespace when the API is down. It runs sequentially on the
// optional-components bucket cluster after the lifecycle test: it enables the
// MCP server itself, scales the API to 0 and back, and makes no logins.
func TestOptionalComponents_MCPScope(t *testing.T) {
	ctx := context.Background()

	// Go runs this non-parallel test before the parallel lifecycle test resumes,
	// and the lifecycle test expects the MCP server disabled on entry, so turn it
	// back off once this test is done.
	t.Cleanup(func() {
		cctx := context.Background()
		helmUpgradeReuse(cctx, t, "mcpServer.enabled=false")
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			_, err := envInstance.K8s.AppsV1().Deployments(ocNS).Get(cctx, "gameplane-mcp-server", metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, "gameplane-mcp-server removed"
			}
			return false, fmt.Sprintf("gameplane-mcp-server still present (err=%v)", err)
		})
	})

	helmUpgradeReuse(ctx, t, "mcpServer.enabled=true")
	envInstance.Eventually(t, 3*time.Minute, ocDeployReady(ctx, ocNS, "gameplane-mcp-server", 1))

	// Namespaced reads only: pods and logs outside the managed namespaces are denied.
	if ocCanI(ctx, t, "list", "pods", "-n", "kube-system") {
		t.Fatalf("mcp service account: list pods in kube-system must be denied")
	}
	if ocCanI(ctx, t, "get", "pods/log", "-n", "kube-system") {
		t.Fatalf("mcp service account: get pods/log in kube-system must be denied")
	}
	if !ocCanI(ctx, t, "list", "pods", "-n", ocGamesNS) {
		t.Fatalf("mcp service account: list pods in %s must be allowed", ocGamesNS)
	}
	if !ocCanI(ctx, t, "list", "pods", "-n", ocNS) {
		t.Fatalf("mcp service account: list pods in %s must be allowed", ocNS)
	}
	if !ocCanI(ctx, t, "list", "gametemplates.gameplane.local") {
		t.Fatalf("mcp service account: list gametemplates must be allowed (catalog ClusterRole)")
	}
	if ocCanI(ctx, t, "create", "gameservers.gameplane.local", "-n", ocGamesNS) {
		t.Fatalf("mcp service account: create gameservers must be denied (read-only)")
	}

	// The old cluster-wide ClusterRole must be gone.
	_, err := envInstance.K8s.RbacV1().ClusterRoles().Get(ctx, "gameplane-mcp-server-read", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("clusterrole gameplane-mcp-server-read: want NotFound, got err=%v", err)
	}

	// Scope guard inside the MCP server: default namespace listing skips
	// kube-system, and an explicit out-of-scope namespace is refused.
	m := ocStartMCP(ctx, t)
	defer m.close()
	m.call(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gameplane-e2e", "version": "1"},
	})
	m.notify(t, "notifications/initialized")

	defaultRes := m.call(t, 2, "tools/call", map[string]any{
		"name":      "list_pods",
		"arguments": map[string]any{},
	})
	if isErr, _ := defaultRes["isError"].(bool); isErr {
		t.Fatalf("list_pods without arguments returned isError: %s", ocTextOf(defaultRes))
	}
	if text := ocTextOf(defaultRes); strings.Contains(text, `"namespace": "kube-system"`) || strings.Contains(text, `"namespace":"kube-system"`) {
		t.Fatalf("list_pods without arguments leaked kube-system pods")
	}

	outRes := m.call(t, 3, "tools/call", map[string]any{
		"name":      "list_pods",
		"arguments": map[string]any{"namespace": "kube-system"},
	})
	if isErr, _ := outRes["isError"].(bool); !isErr {
		t.Fatalf("list_pods kube-system: want isError=true, got %v", outRes)
	}
	if text := ocTextOf(outRes); !strings.Contains(text, "not in scope") {
		t.Fatalf("list_pods kube-system: want %q in error text, got %q", "not in scope", text)
	}

	// Break-glass without the API: the MCP server reads the release namespace
	// directly, so it must answer while gameplane-api has no replicas.
	apiScale, err := envInstance.K8s.AppsV1().Deployments(ocNS).GetScale(ctx, "gameplane-api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get scale gameplane-api: %v", err)
	}
	apiReplicas := apiScale.Spec.Replicas
	t.Cleanup(func() {
		cctx := context.Background()
		s, err := envInstance.K8s.AppsV1().Deployments(ocNS).GetScale(cctx, "gameplane-api", metav1.GetOptions{})
		if err != nil {
			t.Errorf("get scale gameplane-api for restore: %v", err)
			return
		}
		s.Spec.Replicas = apiReplicas
		if _, err := envInstance.K8s.AppsV1().Deployments(ocNS).UpdateScale(cctx, "gameplane-api", s, metav1.UpdateOptions{}); err != nil {
			t.Errorf("restore gameplane-api replicas to %d: %v", apiReplicas, err)
			return
		}
		envInstance.Eventually(t, 3*time.Minute, ocDeployReady(cctx, ocNS, "gameplane-api", apiReplicas))
	})

	apiScale.Spec.Replicas = 0
	if _, err := envInstance.K8s.AppsV1().Deployments(ocNS).UpdateScale(ctx, "gameplane-api", apiScale, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("scale gameplane-api to 0: %v", err)
	}
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		d, err := envInstance.K8s.AppsV1().Deployments(ocNS).Get(ctx, "gameplane-api", metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		return d.Status.Replicas == 0, fmt.Sprintf("gameplane-api replicas=%d", d.Status.Replicas)
	})

	bg := ocStartMCP(ctx, t)
	defer bg.close()
	bg.call(t, 1, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "gameplane-e2e", "version": "1"},
	})
	bg.notify(t, "notifications/initialized")

	sysRes := bg.call(t, 2, "tools/call", map[string]any{
		"name":      "list_pods",
		"arguments": map[string]any{"namespace": ocNS},
	})
	if isErr, _ := sysRes["isError"].(bool); isErr {
		t.Fatalf("break-glass list_pods %s with API scaled to 0: isError: %s", ocNS, ocTextOf(sysRes))
	}
}
