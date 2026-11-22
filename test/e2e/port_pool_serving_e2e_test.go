//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// The kind install gives this template the port pool e2e-pool-serving,
// 31010-31012 (deploy/kind/e2e.sh), so its name is fixed.
const portPoolServingTemplate = "e2e-portpool-c"

// TestGameServer_PortPoolServingAllocation — servers that already publish
// pool ports keep them when a change can't be made: a servicePort another
// server holds is refused, and a template update that needs more ports than
// the full pool has waits on the current revision. While it waits, the
// server keeps its Service, its sleep and wake routing, and its reservation;
// a new server isn't given its ports. A server whose update fits is rolled
// out onto its new block.
func TestGameServer_PortPoolServingAllocation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	ns := "gameplane-games"
	suffix := time.Now().UnixNano()
	templates := envInstance.Dyn.Resource(gameTemplateGVR)
	servers := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns)

	gamePort := map[string]any{"name": "game", "containerPort": int64(25565), "protocol": "TCP", "advertise": true, "wakeProtocol": "minecraft"}
	queryPort := map[string]any{"name": "query", "containerPort": int64(25566), "protocol": "TCP", "advertise": true}
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "GameTemplate",
		"metadata": map[string]any{"name": portPoolServingTemplate},
		"spec": map[string]any{
			"displayName": "Port pool fixture C", "game": "minecraft-java", "version": "1",
			"image": "busybox:1.36", "command": []any{"sh", "-c", "sleep 100000"},
			"ports": []any{gamePort},
		},
	}}
	if _, err := templates.Create(ctx, tmpl, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create template: %v", err)
	}
	t.Cleanup(func() {
		_ = templates.Delete(context.WithoutCancel(ctx), portPoolServingTemplate, metav1.DeleteOptions{})
	})

	create := func(s string, spec map[string]any) string {
		t.Helper()
		name := fmt.Sprintf("e2e-pps-%s-%d", s, suffix)
		spec["templateRef"] = map[string]any{"name": portPoolServingTemplate}
		gs := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer",
			"metadata": map[string]any{"name": name, "namespace": ns}, "spec": spec,
		}}
		if _, err := servers.Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create gameserver %s: %v", name, err)
		}
		t.Cleanup(func() { _ = servers.Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{}) })
		return name
	}
	patch := func(name, body string) {
		t.Helper()
		if _, err := servers.Patch(ctx, name, types.MergePatchType, []byte(body), metav1.PatchOptions{}); err != nil {
			t.Fatalf("patch %s: %v", name, err)
		}
	}
	// waitFor waits until name's Service publishes want (name -> port; nil
	// for no Service) and its PortAllocation condition has reason.
	waitFor := func(name string, want map[string]int32, reason string) {
		t.Helper()
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			obj, err := servers.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, "get gameserver: " + err.Error()
			}
			if cond := findCondition(obj.Object, "PortAllocation"); cond == nil || cond["reason"] != reason {
				return false, fmt.Sprintf("%s PortAllocation = %v, want %s", name, cond, reason)
			}
			svc, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
			if want == nil {
				if !apierrors.IsNotFound(err) {
					return false, fmt.Sprintf("%s Service: %v, want none", name, err)
				}
				return true, ""
			}
			if err != nil {
				return false, "get service: " + err.Error()
			}
			got := map[string]int32{}
			for _, p := range svc.Spec.Ports {
				got[p.Name] = p.Port
			}
			if len(got) != len(want) {
				return false, fmt.Sprintf("%s ports = %v, want %v", name, got, want)
			}
			for k, v := range want {
				if got[k] != v {
					return false, fmt.Sprintf("%s ports = %v, want %v", name, got, want)
				}
			}
			return true, ""
		})
	}

	// Fill the pool, one server at a time so the blocks are predictable.
	s1 := create("s1", map[string]any{"idle": map[string]any{"enabled": true, "afterMinutes": int64(60), "wakeOnConnect": true}})
	waitFor(s1, map[string]int32{"game": 31010}, "Allocated")
	s2 := create("s2", map[string]any{})
	waitFor(s2, map[string]int32{"game": 31011}, "Allocated")
	s3 := create("s3", map[string]any{})
	waitFor(s3, map[string]int32{"game": 31012}, "Allocated")

	// s2 asks for s1's port: refused, and both keep theirs.
	patch(s2, `{"spec":{"networking":{"portOverrides":[{"name":"game","servicePort":31010}]}}}`)
	waitFor(s2, map[string]int32{"game": 31011}, "ServicePortConflict")
	waitFor(s1, map[string]int32{"game": 31010}, "Allocated")
	patch(s2, `{"spec":{"networking":{"portOverrides":null}}}`)
	waitFor(s2, map[string]int32{"game": 31011}, "Allocated")

	pin := func(name string) string {
		t.Helper()
		obj, err := servers.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get %s: %v", name, err)
		}
		return obj.GetAnnotations()["gameplane.local/effective-template-revision"]
	}
	s1Pin := pin(s1)
	if s1Pin == "" {
		t.Fatal("s1 has no pinned template revision")
	}

	// A second port needs a two-port block, and the pool has none: every
	// server keeps its revision and its port.
	body, err := json.Marshal(map[string]any{"spec": map[string]any{"ports": []any{gamePort, queryPort}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := templates.Patch(ctx, portPoolServingTemplate, types.MergePatchType, body, metav1.PatchOptions{}); err != nil {
		t.Fatalf("add a port to the template: %v", err)
	}
	waitFor(s1, map[string]int32{"game": 31010}, "RolloutPending")
	waitFor(s2, map[string]int32{"game": 31011}, "RolloutPending")
	if got := pin(s1); got != s1Pin {
		t.Fatalf("s1 revision changed to %s while its ports couldn't be reserved", got)
	}
	ss, err := envInstance.K8s.AppsV1().StatefulSets(ns).Get(ctx, s1, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get s1 StatefulSet: %v", err)
	}
	if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 1 {
		t.Fatalf("s1 replicas = %v, want it left running", ss.Spec.Replicas)
	}

	// While it waits, s1 still sleeps and wakes through its Service.
	past := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	patch(s1, `{"metadata":{"annotations":{"gameplane.local/idle-asleep-since":"`+past+`"}}}`)
	waitSelector(t, ns, s1, "gameplane-waker")
	waitFor(s1, map[string]int32{"game": 31010}, "RolloutPending")
	patch(s1, `{"metadata":{"annotations":{"gameplane.local/idle-asleep-since":null}}}`)
	waitSelector(t, ns, s1, "gameplane-game")

	// Deleting s3 frees 31012, so s2's update fits at its own 31011-31012 and
	// rolls out. s1 still waits on 31010, and a new server isn't given it.
	if err := servers.Delete(ctx, s3, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete s3: %v", err)
	}
	waitFor(s2, map[string]int32{"game": 31011, "query": 31012}, "Allocated")
	waitFor(s1, map[string]int32{"game": 31010}, "RolloutPending")
	s4 := create("s4", map[string]any{})
	waitFor(s4, nil, "PoolExhausted")
	obj, err := servers.Get(ctx, s4, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get s4: %v", err)
	}
	if msg, _ := findCondition(obj.Object, "PortAllocation")["message"].(string); !strings.Contains(msg, "e2e-pool-serving") {
		t.Errorf("s4 message %q doesn't name the pool", msg)
	}
}

// waitSelector waits until name's Service selects app.kubernetes.io/name=app:
// the game, or the wake sentinel while the server sleeps.
func waitSelector(t *testing.T, ns, name, app string) {
	t.Helper()
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		svc, err := envInstance.K8s.CoreV1().Services(ns).Get(t.Context(), name, metav1.GetOptions{})
		if err != nil {
			return false, "get service: " + err.Error()
		}
		if svc.Spec.Selector["app.kubernetes.io/name"] != app {
			return false, fmt.Sprintf("selector = %v, want %s", svc.Spec.Selector, app)
		}
		if app == "gameplane-waker" {
			waker, err := envInstance.K8s.AppsV1().Deployments(ns).Get(t.Context(), name+"-waker", metav1.GetOptions{})
			if err != nil || waker.Status.ReadyReplicas == 0 {
				return false, "waker not ready"
			}
		}
		return true, ""
	})
}
