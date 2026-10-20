//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestGameServer_FiveM_Persistence verifies that data written to FiveM's
// data directory (/server-data) survives container restarts and StatefulSet restarts.
//
// FiveM is in the HEAVY game set (>5Gi storage, embedded database + txAdmin supervision).
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=fivem make test-e2e-keep
func TestGameServer_FiveM_Persistence(t *testing.T) {
	skipUnlessGameInScope(t, "fivem")
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	uid := time.Now().UnixNano()
	tmplName := fmt.Sprintf("e2e-fivem-persist-%d", uid)
	gsName := fmt.Sprintf("fivem-persist-%d", uid)
	const mountPath = "/server-data"
	marker := fmt.Sprintf("marker-fivem-%d", uid)

	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": tmplName},
		"spec": map[string]any{
			"displayName": "E2E FiveM Persistence",
			"game":        "fivem",
			"version":     "1.0.0",
			"image":       "ghcr.io/gameplanepanel/gameplane/fivem:latest@sha256:23d65f59793e5f207c4c4ee5c320bca5aa56003fd95be259a887f4e58f3d77b1",
			"ports": []any{
				map[string]any{"name": "game", "containerPort": int64(30120), "protocol": "UDP", "advertise": true},
				map[string]any{"name": "http", "containerPort": int64(30120), "protocol": "TCP", "advertise": true},
				map[string]any{"name": "txadmin", "containerPort": int64(40120), "protocol": "TCP", "advertise": true},
			},
			"storage": map[string]any{
				"size":      "10Gi",
				"mountPath": mountPath,
			},
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "1", "memory": "2Gi"},
				"limits":   map[string]any{"cpu": "4", "memory": "4Gi"},
			},
			"rcon": map[string]any{
				"protocol": "rest",
				"port":     int64(40120),
			},
			"consoleMode": "rcon",
			"capabilities": map[string]any{
				"lifecycle": map[string]any{
					"stop": []any{"quit"},
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
				"size": "10Gi",
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
	waitGameContainerReady(t, ns, gsName+"-0", 5*time.Minute)

	markerPath := mountPath + "/marker.txt"
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gsName+"-0", "sh", "-c", fmt.Sprintf("echo -n %s > %s && sync", marker, markerPath)); err != nil {
		t.Fatalf("write marker: %v\n%s", err, out)
	}

	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gsName+"-0", "cat", markerPath); err != nil || !strings.Contains(out, marker) {
		t.Fatalf("marker not verified prior to restart: err=%v, out=%s", err, out)
	}

	if err := envInstance.K8s.CoreV1().Pods(ns).Delete(ctx, gsName+"-0", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete pod for restart: %v", err)
	}

	waitGameContainerReady(t, ns, gsName+"-0", 5*time.Minute)

	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		out, err := envInstance.KubectlExec(t, ns, "pod/"+gsName+"-0", "cat", markerPath)
		if err != nil {
			return false, "exec error reading marker: " + err.Error() + " out=" + out
		}
		if !strings.Contains(out, marker) {
			return false, fmt.Sprintf("marker %q missing from %s, got %q", marker, markerPath, out)
		}
		return true, ""
	})
}
