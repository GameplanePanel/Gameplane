//go:build envtest

package handlers

import (
	"context"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// TestLifecycle_StopSetsSuspend — POST /servers/{name}:stop patches
// spec.suspend=true on the cluster object.
func TestLifecycle_StopSetsSuspend(t *testing.T) {
	name := uniqueResourceName("life-stop")
	createServerForLifecycleTest(t, name)

	resp := doJSON(t, http.MethodPost, "/servers/"+name+":stop", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf(":stop status = %d, want 202; body=%s", resp.StatusCode, readBody(t, resp))
	}

	if got := lifecycleSuspendOf(t, name); got != true {
		t.Errorf("after :stop, spec.suspend = %v, want true", got)
	}
}

// TestLifecycle_StartClearsSuspend — POST /servers/{name}:start clears
// spec.suspend (sets to false).
func TestLifecycle_StartClearsSuspend(t *testing.T) {
	name := uniqueResourceName("life-start")
	createServerForLifecycleTest(t, name)

	// First stop to get into a known suspended state.
	resp := doJSON(t, http.MethodPost, "/servers/"+name+":stop", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf(":stop precondition: %d", resp.StatusCode)
	}
	resp.Body.Close()
	if got := lifecycleSuspendOf(t, name); !got {
		t.Fatalf(":stop precondition didn't take effect")
	}

	resp = doJSON(t, http.MethodPost, "/servers/"+name+":start", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf(":start status = %d, want 202; body=%s", resp.StatusCode, readBody(t, resp))
	}

	if got := lifecycleSuspendOf(t, name); got != false {
		t.Errorf("after :start, spec.suspend = %v, want false", got)
	}
}

// TestLifecycle_RestartStampsAnnotation — :restart stamps the
// restart-requested annotation (the operator owns the recycle) and must NOT
// touch spec.suspend.
func TestLifecycle_RestartStampsAnnotation(t *testing.T) {
	name := uniqueResourceName("life-restart")
	createServerForLifecycleTest(t, name)

	resp := doJSON(t, http.MethodPost, "/servers/"+name+":restart", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf(":restart status = %d, want 202; body=%s", resp.StatusCode, readBody(t, resp))
	}

	gs, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	if got := gs.GetAnnotations()[restartRequestedAnnotation]; got == "" {
		t.Errorf("after :restart, missing %s annotation; got %v", restartRequestedAnnotation, gs.GetAnnotations())
	}
	if got := lifecycleSuspendOf(t, name); got != false {
		t.Errorf("after :restart, spec.suspend = %v, want it left false", got)
	}
}

// TestLifecycle_CloneCreatesNewServer — :clone with newName creates a
// duplicate of the source server under the new name.
func TestLifecycle_CloneCreatesNewServer(t *testing.T) {
	src := uniqueResourceName("clone-src")
	dst := uniqueResourceName("clone-dst")
	createServerForLifecycleTest(t, src)
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrServers()).
			Namespace(scope.DefaultNamespace).
			Delete(context.Background(), dst, metav1.DeleteOptions{})
	})

	resp := doJSON(t, http.MethodPost, "/servers/"+src+":clone", map[string]any{"newName": dst})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf(":clone status = %d, want 200; body=%s", resp.StatusCode, readBody(t, resp))
	}

	dup, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Get(context.Background(), dst, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("clone target not present on cluster: %v", err)
	}
	if dup.GetName() != dst {
		t.Errorf("clone name = %q, want %q", dup.GetName(), dst)
	}
	tmpl, _, _ := unstructured.NestedString(dup.Object, "spec", "templateRef", "name")
	if tmpl != "minecraft" {
		t.Errorf("clone lost templateRef: %q", tmpl)
	}
}

// TestLifecycle_CloneRequiresNewName — POST /servers/{name}:clone with
// no newName returns 4xx.
func TestLifecycle_CloneRequiresNewName(t *testing.T) {
	src := uniqueResourceName("clone-bad")
	createServerForLifecycleTest(t, src)

	resp := doJSON(t, http.MethodPost, "/servers/"+src+":clone", map[string]any{})
	if resp.StatusCode < 400 || resp.StatusCode >= 500 {
		t.Errorf(":clone without newName status = %d, want 4xx", resp.StatusCode)
	}
	resp.Body.Close()
}

// ---------- helpers ----------

// createServerForLifecycleTest writes a GameServer directly to the
// apiserver (bypassing the API handler — that's covered by
// resources_envtest_test.go) and registers cleanup.
func createServerForLifecycleTest(t *testing.T, name string) {
	t.Helper()
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": scope.DefaultNamespace},
		"spec":       map[string]any{"templateRef": map[string]any{"name": "minecraft"}},
	}}
	if _, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Create(context.Background(), gs, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create server fixture: %v", err)
	}
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrServers()).
			Namespace(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

func lifecycleSuspendOf(t *testing.T, name string) bool {
	t.Helper()
	gs, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	suspend, _, _ := unstructured.NestedBool(gs.Object, "spec", "suspend")
	return suspend
}
