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

// TestGameServer_ArkSurvival_ClusterPersistence verifies that cluster-travel
// shared storage between ARK shards (e.g. Saved/clusters/<cluster-id> or cluster-shared)
// preserves character profiles, uploaded dinos, and transfer markers across
// individual shard container restarts.
//
// Both ARK: Survival Ascended and ARK: Survival Evolved are in the HEAVY game set.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=ark-survival-evolved,ark-survival-ascended make test-e2e-keep
func TestGameServer_ArkSurvival_ClusterPersistence(t *testing.T) {
	skipUnlessGameInScope(t, "ark-survival-evolved")
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	uid := time.Now().UnixNano() % 100000
	tmplName := fmt.Sprintf("e2e-ark-cluster-%d", uid)
	gsName := fmt.Sprintf("ark-shard-01-%d", uid)
	const mountPath = "/data"
	clusterID := fmt.Sprintf("cluster-%d", uid)
	marker := fmt.Sprintf("ark-survivor-payload-%d", uid)

	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": tmplName},
		"spec": map[string]any{
			"displayName": "E2E ARK Cluster Shard",
			"game":        "ark-survival-evolved",
			"version":     "1.0.0",
			"image":       "ghcr.io/gameplanepanel/gameplane/ark-survival-evolved:latest@sha256:2a4298f8055dbff4107c49c961cace1f2bed42809e6b93fb4924371ae4484e58",
			"ports": []any{
				map[string]any{"name": "game", "containerPort": int64(7777), "protocol": "UDP", "advertise": true},
				map[string]any{"name": "query", "containerPort": int64(27015), "protocol": "UDP", "advertise": true},
				map[string]any{"name": "rcon", "containerPort": int64(27020), "protocol": "TCP", "advertise": false},
			},
			"storage": map[string]any{
				"size":      "35Gi",
				"mountPath": mountPath,
			},
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "2", "memory": "8Gi"},
				"limits":   map[string]any{"cpu": "6", "memory": "16Gi"},
			},
			"rcon": map[string]any{
				"protocol": "source",
				"port":     int64(27020),
			},
			"consoleMode": "rcon",
			"capabilities": map[string]any{
				"lifecycle": map[string]any{
					"stop": []any{"SaveWorld", "DoExit"},
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
			"config": map[string]any{
				"CLUSTER_ID": clusterID,
			},
			"storage": map[string]any{
				"size": "35Gi",
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

	clusterDir := fmt.Sprintf("%s/ShooterGame/Saved/clusters/%s", mountPath, clusterID)
	markerFile := fmt.Sprintf("%s/survivor.dat", clusterDir)

	writeCmd := fmt.Sprintf("mkdir -p %s && echo -n %s > %s && sync", clusterDir, marker, markerFile)
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gsName+"-0", "sh", "-c", writeCmd); err != nil {
		t.Fatalf("write cluster marker: %v\n%s", err, out)
	}

	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gsName+"-0", "cat", markerFile); err != nil || !strings.Contains(out, marker) {
		t.Fatalf("marker not verified prior to restart: err=%v, out=%s", err, out)
	}

	if err := envInstance.K8s.CoreV1().Pods(ns).Delete(ctx, gsName+"-0", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete shard pod for restart: %v", err)
	}

	waitGameContainerReady(t, ns, gsName+"-0", 5*time.Minute)

	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		out, err := envInstance.KubectlExec(t, ns, "pod/"+gsName+"-0", "cat", markerFile)
		if err != nil {
			return false, "exec error reading cluster marker: " + err.Error() + " out=" + out
		}
		if !strings.Contains(out, marker) {
			return false, fmt.Sprintf("cluster marker %q missing from %s, got %q", marker, markerFile, out)
		}
		return true, ""
	})
}
