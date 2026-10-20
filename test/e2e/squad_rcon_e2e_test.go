//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// TestGameServer_Squad_RCON boots a Squad dedicated server,
// verifies that administrative commands can be executed over Source-family RCON via the API,
// and verifies that the stop sequence cleanly suspends the server.
//
// Squad is in the HEAVY game set (>35Gi storage, SteamCMD download).
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=squad make test-e2e-keep
func TestGameServer_Squad_RCON(t *testing.T) {
	skipUnlessGameInScope(t, "squad")
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	uid := time.Now().UnixNano()
	tmplName := fmt.Sprintf("e2e-squad-rcon-%d", uid)
	gsName := fmt.Sprintf("squad-rcon-%d", uid)
	const mountPath = "/data"

	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": tmplName},
		"spec": map[string]any{
			"displayName": "E2E Squad RCON",
			"game":        "squad",
			"version":     "1.0.0",
			"image":       "ghcr.io/gameplanepanel/gameplane/squad:latest@sha256:c96561c975b1cd049668da930582181de7ac8d74e5d8d2251080423ec2ee2748",
			"env": []any{
				map[string]any{"name": "RCON_PASSWORD", "value": "secret-rcon-password"},
			},
			"security": map[string]any{
				"runAsUser":  int64(10000),
				"runAsGroup": int64(10000),
				"fsGroup":    int64(10000),
			},
			"ports": []any{
				map[string]any{"name": "game", "containerPort": int64(7787), "protocol": "UDP", "advertise": true},
				map[string]any{"name": "query", "containerPort": int64(27165), "protocol": "UDP", "advertise": true},
				map[string]any{"name": "rcon", "containerPort": int64(21114), "protocol": "TCP", "advertise": false},
			},
			"storage": map[string]any{
				"size":      "40Gi",
				"mountPath": mountPath,
			},
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "2", "memory": "8Gi"},
				"limits":   map[string]any{"cpu": "6", "memory": "16Gi"},
			},
			"rcon": map[string]any{
				"protocol":    "source",
				"port":        int64(21114),
				"passwordEnv": "RCON_PASSWORD",
			},
			"consoleMode": "rcon",
			"capabilities": map[string]any{
				"actions": []any{
					map[string]any{
						"id":          "broadcast",
						"displayName": "Broadcast",
						"command":     "AdminBroadcast {{.Params.message}}",
						"params": []any{
							map[string]any{"name": "message", "type": "string", "required": true},
						},
					},
				},
			},
		},
	}}

	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).Create(ctx, tmpl, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create template %s: %v", tmplName, err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).Delete(context.Background(), tmplName, metav1.DeleteOptions{})
	})

	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata": map[string]any{
			"name":      gsName,
			"namespace": ns,
		},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmplName},
			"storage": map[string]any{
				"size": "40Gi",
			},
		},
	}}

	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Create(ctx, gs, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create gameserver %s: %v", gsName, err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Delete(context.Background(), gsName, metav1.DeleteOptions{})
	})

	waitPVCBound(t, ns, gsName+"-data", 2*time.Minute)
	waitGameContainerReady(t, ns, gsName+"-0", 10*time.Minute)

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	requireAgentReady(t, ns, gsName)
	waitAgentReachable(t, cli, gsName)

	resp, body, err := cli.Post(
		fmt.Sprintf("/servers/%s/actions/run", gsName),
		map[string]any{
			"id": "broadcast",
			"params": map[string]any{
				"message": "Hello Squad RCON",
			},
		},
	)
	if err != nil {
		t.Fatalf("POST /actions/run error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /actions/run returned status %d: %s", resp.StatusCode, string(body))
	}

	suspendPatch := []byte(`{"spec":{"suspend":true}}`)
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Patch(ctx, gsName, types.MergePatchType, suspendPatch, metav1.PatchOptions{}); err != nil {
		t.Fatalf("patch suspend=true: %v", err)
	}

	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		ss, err := envInstance.K8s.AppsV1().StatefulSets(ns).Get(ctx, gsName, metav1.GetOptions{})
		if err != nil {
			return false, "get ss: " + err.Error()
		}
		if ss.Spec.Replicas != nil && *ss.Spec.Replicas == 0 {
			return true, ""
		}
		return false, fmt.Sprintf("expected replicas=0, got %v", ss.Spec.Replicas)
	})
}
