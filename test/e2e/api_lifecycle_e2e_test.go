//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestAPI_LifecycleStartStop exercises POST /servers/{name}:stop and
// :start through the API gateway. The contract is two-step:
//
//  1. The handler patches GameServer.spec.suspend (=true on stop, =false
//     on start) and returns 202 Accepted.
//  2. The operator reconciles spec.suspend onto the StatefulSet's
//     replica count.
//
// We verify both steps rather than just the API response, otherwise a
// regression that drops the patch silently would still pass the test.
func TestAPI_LifecycleStartStop(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-api-lifecycle-tmpl"
	gs := "e2e-api-lifecycle-startstop"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyBusyboxTemplate(t, tmpl)
	createGameServerViaAPI(t, cli, ns, gs, tmpl)

	// Operator should bring the StatefulSet up to replicas=1.
	waitStatefulSetReplicas(t, ns, gs, 1, 90*time.Second)

	// Stop → 202 + spec.suspend=true → replicas=0.
	resp, body, err := cli.Post("/servers/"+gs+":stop", nil)
	if err != nil {
		t.Fatalf("POST :stop: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf(":stop expected 202, got %d body=%q", resp.StatusCode, string(body))
	}
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gs, metav1.GetOptions{})
		if err != nil {
			return false, "get gs: " + err.Error()
		}
		suspend, _, _ := unstructured.NestedBool(got.Object, "spec", "suspend")
		if !suspend {
			return false, "spec.suspend not yet true"
		}
		return true, ""
	})
	waitStatefulSetReplicas(t, ns, gs, 0, 60*time.Second)

	// Start → 202 + spec.suspend=false → replicas=1.
	resp, body, err = cli.Post("/servers/"+gs+":start", nil)
	if err != nil {
		t.Fatalf("POST :start: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf(":start expected 202, got %d body=%q", resp.StatusCode, string(body))
	}
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, gs, metav1.GetOptions{})
		if err != nil {
			return false, "get gs: " + err.Error()
		}
		suspend, _, _ := unstructured.NestedBool(got.Object, "spec", "suspend")
		if suspend {
			return false, "spec.suspend still true"
		}
		return true, ""
	})
	waitStatefulSetReplicas(t, ns, gs, 1, 60*time.Second)
}

// TestAPI_LifecycleRestart verifies POST :restart drives the existing
// StatefulSet pod through a delete/recreate cycle. The handler stamps a
// restart-requested annotation; the operator owns the actual pod
// replacement (it drains gracefully and only brings a fresh pod up once the
// old one is confirmed gone).
//
// We capture the original pod's UID, hit :restart, and wait for a pod
// with the same name and a different UID. The UID change is the
// canonical "this pod is a different object" check — a name match alone
// is insufficient since the StatefulSet always recreates with the same
// name.
//
// This runs in parallel: because the restart is an operator-owned token that
// persists until acked, a deep reconcile queue can no longer coalesce it into
// a no-op the way the old suspend/resume patch pair could.
func TestAPI_LifecycleRestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-api-lifecycle-restart-tmpl"
	gs := "e2e-api-lifecycle-restart"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyBusyboxTemplate(t, tmpl)
	createGameServerViaAPI(t, cli, ns, gs, tmpl)
	waitStatefulSetReplicas(t, ns, gs, 1, 90*time.Second)

	// Restart a genuinely RUNNING server (which is what the dashboard's
	// restart button acts on), so the operator's graceful stop can issue the
	// agent stop sequence and drive the full drain→recycle cycle.
	requireAgentReady(t, ns, gs)

	// Capture the original pod UID. The pod must exist before :restart;
	// otherwise we can't tell a "fresh first start" from a "restart".
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		_, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		return true, ""
	})
	original, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get original pod: %v", err)
	}
	oldUID := original.UID

	// The API patches the server conditionally on the version it read, so a
	// status write by the operator in between returns 409 "conflict, retry".
	// Repeat the call then, as a real client would; any other status fails.
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		resp, body, err := cli.Post("/servers/"+gs+":restart", nil)
		if err != nil {
			t.Fatalf("POST :restart: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusConflict {
			return false, ":restart conflicted with a controller update: " + string(body)
		}
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf(":restart expected 202, got %d body=%q", resp.StatusCode, string(body))
		}
		return true, ""
	})

	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gs+"-0", metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		if pod.UID == oldUID {
			return false, "pod UID unchanged — not yet restarted"
		}
		return true, ""
	})
}

// TestAPI_LifecycleClone copies an existing GameServer to a new name via
// POST /servers/{name}:clone. The handler:
//
//  1. Reads the source GameServer.
//  2. Strips status, resourceVersion, UID.
//  3. Renames to the requested newName.
//  4. Creates the clone as a fresh CR.
//
// We assert the clone exists, has the same templateRef as the source,
// and ends up with its own PVC (proving the operator went through its
// usual materialize path on the new CR).
func TestAPI_LifecycleClone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-api-lifecycle-clone-tmpl"
	gs := "e2e-api-lifecycle-clone-src"
	cloneName := "e2e-api-lifecycle-clone-dst"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyBusyboxTemplate(t, tmpl)
	createGameServerViaAPI(t, cli, ns, gs, tmpl)
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), cloneName, metav1.DeleteOptions{})
	})

	// Wait for the source's StatefulSet to materialize so the clone has
	// a complete shape to copy from.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		_, err := envInstance.K8s.AppsV1().StatefulSets(ns).Get(ctx, gs, metav1.GetOptions{})
		if err != nil {
			return false, "get src ss: " + err.Error()
		}
		return true, ""
	})

	resp, body, err := cli.Post("/servers/"+gs+":clone", map[string]string{"newName": cloneName})
	if err != nil {
		t.Fatalf("POST :clone: %v", err)
	}
	defer resp.Body.Close()
	// Clone returns the created object (200 OK) per cloneHandler in
	// lifecycle.go — not 202.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf(":clone expected 200, got %d body=%q", resp.StatusCode, string(body))
	}

	// Cloned GameServer must exist and reference the same template.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Get(ctx, cloneName, metav1.GetOptions{})
		if err != nil {
			return false, "get clone: " + err.Error()
		}
		ref, _, _ := unstructured.NestedString(got.Object, "spec", "templateRef", "name")
		if ref != tmpl {
			return false, "clone templateRef=" + ref + " want " + tmpl
		}
		return true, ""
	})

	// Clone PVC distinct from source PVC — i.e. operator went through
	// its full materialize path on the new CR rather than re-pointing at
	// the source's volume.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		_, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
			Get(ctx, cloneName+"-data", metav1.GetOptions{})
		if err != nil {
			return false, "get clone pvc: " + err.Error()
		}
		return true, ""
	})

	srcPVC, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
		Get(ctx, gs+"-data", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get src pvc: %v", err)
	}
	dstPVC, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).
		Get(ctx, cloneName+"-data", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get dst pvc: %v", err)
	}
	if srcPVC.UID == dstPVC.UID {
		t.Errorf("clone shares source PVC UID — they must be distinct objects")
	}
}

// TestAPI_LifecycleNotFound: a lifecycle verb on a missing GameServer
// must surface as 404, not 500. Catches regressions where a panic in
// the patch path turns into a generic error.
func TestAPI_LifecycleNotFound(t *testing.T) {
	t.Parallel()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	resp, body, err := cli.Post("/servers/no-such-server-for-lifecycle:start", nil)
	if err != nil {
		t.Fatalf("POST :start on missing: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf(":start on missing expected 404, got %d body=%q", resp.StatusCode, string(body))
	}
}

// createGameServerViaAPI POSTs a GameServer through the API. The
// envelope mirrors what web/src/lib/endpoints.ts generates for
// Servers.create — apiVersion/kind/metadata/spec — so the test
// exercises the same code path the dashboard hits.
func createGameServerViaAPI(t *testing.T, cli *APIClient, ns, name, tmpl string) {
	t.Helper()
	body := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmpl},
		},
	}
	resp, rb, err := cli.Post("/servers", body)
	if err != nil {
		t.Fatalf("POST /servers: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /servers expected 200/201, got %d body=%q", resp.StatusCode, string(rb))
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}
