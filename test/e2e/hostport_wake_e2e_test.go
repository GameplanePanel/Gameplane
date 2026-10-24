//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// This bucket owns its two-node cluster. It deliberately retains terminating
// Pods and changes node scheduling; it must not run with other game tests.
func TestGameServer_HostportWakeStableEndpoint(t *testing.T) {
	ctx := t.Context()
	const ns, name, tmpl, port = "gameplane-games", "hostport-wake", "hostport-wake-template", 12345
	nodes, err := envInstance.K8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes.Items) < 2 {
		t.Fatal("wake-hostport requires the two-node kind profile")
	}
	var endpoint corev1.Node
	for _, node := range nodes.Items {
		if _, control := node.Labels["node-role.kubernetes.io/control-plane"]; !control {
			endpoint = node
			break
		}
	}
	if endpoint.Name == "" {
		t.Fatal("no worker node")
	}
	for _, node := range nodes.Items {
		patch := []byte(`{"metadata":{"labels":{"gameplane.local/wake-pool":"test"}}}`)
		if _, err := envInstance.K8s.CoreV1().Nodes().Patch(ctx, node.Name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	// The game and sentinel both need this dedicated-node toleration.
	taints := append(append([]corev1.Taint{}, endpoint.Spec.Taints...), corev1.Taint{Key: "gameplane.local/dedicated", Value: "games", Effect: corev1.TaintEffectNoSchedule})
	patchHostportNode(t, endpoint.Name, map[string]any{"taints": taints})
	t.Cleanup(func() {
		patchHostportNode(t, endpoint.Name, map[string]any{"unschedulable": false, "taints": endpoint.Spec.Taints})
	})
	address := ""
	for _, addr := range endpoint.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP {
			address = net.JoinHostPort(addr.Address, fmt.Sprint(port))
			break
		}
	}
	if address == "" {
		t.Fatal("worker has no InternalIP")
	}

	applyBusyboxTemplate(t, tmpl)
	// During deletion the shell keeps httpd alive for 12 seconds. This makes
	// the old port genuinely occupied while the sleep handoff is observed.
	templatePatch := map[string]any{"spec": map[string]any{
		"command": []string{"sh", "-c", "mkdir -p /tmp/site; echo endpoint-ok >/tmp/site/index.html; httpd -p 12345 -h /tmp/site; trap 'sleep 12; exit 0' TERM; while :; do sleep 1; done"},
		"ports":   []any{map[string]any{"name": "game", "containerPort": port, "advertise": true, "protocol": "TCP", "wakeProtocol": "generic"}},
	}}
	data, err := json.Marshal(templatePatch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).Patch(ctx, tmpl, types.MergePatchType, data, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer",
		"metadata": map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{"templateRef": map[string]any{"name": tmpl}, "networking": map[string]any{"expose": "Hostport"},
			"nodeSelector": map[string]any{"kubernetes.io/hostname": endpoint.Labels["kubernetes.io/hostname"]},
			"tolerations":  []any{map[string]any{"key": "gameplane.local/dedicated", "operator": "Equal", "value": "games", "effect": "NoSchedule"}, map[string]any{"key": "node-role.kubernetes.io/control-plane", "operator": "Exists", "effect": "NoSchedule"}},
			"idle":         map[string]any{"enabled": true, "afterMinutes": int64(60), "wakeOnConnect": true}},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Create(ctx, gs, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	waitPVCBound(t, ns, name+"-data", 90*time.Second)
	httpClient := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	defer httpClient.CloseIdleConnections()
	gameReachable := func() (bool, string) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address, nil)
		if err != nil {
			return false, err.Error()
		}
		resp, err := httpClient.Do(request)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		return err == nil && strings.Contains(string(body), "endpoint-ok"), string(body)
	}
	for cycle := range 2 {
		envInstance.Eventually(t, 2*time.Minute, gameReachable)
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
		if err != nil || pod.Spec.NodeName != endpoint.Name {
			t.Fatalf("game moved: pod=%v err=%v", pod, err)
		}
		patchHostportServer(t, ns, name, map[string]any{"metadata": map[string]any{"annotations": map[string]any{"gameplane.local/idle-asleep-since": time.Now().UTC().Format(time.RFC3339)}}})
		envInstance.Eventually(t, 20*time.Second, func() (bool, string) {
			pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
			return err == nil && !pod.DeletionTimestamp.IsZero(), fmt.Sprint(err)
		})
		if ok, msg := gameReachable(); !ok {
			t.Fatalf("fixture did not retain occupied old port during termination: %s", msg)
		}
		if _, err := envInstance.K8s.AppsV1().Deployments(ns).Get(ctx, name+"-waker", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("sentinel created before old game released port: %v", err)
		}
		assertHostportCondition(t, ns, name, "GameDraining", false)
		if cycle == 0 {
			// Once the endpoint is recorded, broaden the worker pool to both
			// nodes. Only the persisted required affinity can retain identity.
			patchHostportServer(t, ns, name, map[string]any{"spec": map[string]any{"nodeSelector": map[string]any{"kubernetes.io/hostname": nil, "gameplane.local/wake-pool": "test"}}})
			patchHostportNode(t, endpoint.Name, map[string]any{"unschedulable": true})
			envInstance.Eventually(t, 90*time.Second, func() (bool, string) { return hostportConditionIs(t, ns, name, "EndpointUnschedulable", false) })
			// The alternate node is available; the listener must stay Pending
			// on the pinned endpoint instead of silently moving there.
			assertHostportPodsOnNode(t, ns, name, endpoint.Name)
			patchHostportNode(t, endpoint.Name, map[string]any{"unschedulable": false})
		}
		envInstance.Eventually(t, 90*time.Second, func() (bool, string) { return hostportConditionIs(t, ns, name, "ListenerReady", true) })
		assertHostportPodsOnNode(t, ns, name, endpoint.Name)
		wakers, err := envInstance.K8s.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-waker,app.kubernetes.io/instance=" + name})
		if err != nil || len(wakers.Items) != 1 {
			t.Fatalf("sentinel Pods: %v, %v", wakers, err)
		}
		waker := wakers.Items[0]
		finalizer := []byte(`{"metadata":{"finalizers":["test.gameplane.local/hold-handoff"]}}`)
		if _, err := envInstance.K8s.CoreV1().Pods(ns).Patch(ctx, waker.Name, types.MergePatchType, finalizer, metav1.PatchOptions{}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = envInstance.K8s.CoreV1().Pods(ns).Patch(context.Background(), waker.Name, types.MergePatchType, []byte(`{"metadata":{"finalizers":[]}}`), metav1.PatchOptions{})
		})
		// A connection to the same node address must reach the sentinel and
		// wake the game. Hostport clients reconnect after this intentional bounce.
		conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", address)
		if err != nil {
			t.Fatalf("sleeping endpoint unreachable: %v", err)
		}
		_ = conn.Close()
		envInstance.Eventually(t, 30*time.Second, func() (bool, string) { return hostportConditionIs(t, ns, name, "SentinelDraining", false) })
		envInstance.Consistently(t, 3*time.Second, 200*time.Millisecond, func() (bool, string) {
			_, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
			return apierrors.IsNotFound(err), "game scheduled before terminating sentinel Pod disappeared"
		})
		if _, err := envInstance.K8s.CoreV1().Pods(ns).Patch(ctx, waker.Name, types.MergePatchType, []byte(`{"metadata":{"finalizers":[]}}`), metav1.PatchOptions{}); err != nil {
			t.Fatal(err)
		}
		envInstance.Eventually(t, 2*time.Minute, gameReachable)
		assertHostportPodsOnNode(t, ns, name, endpoint.Name)
		t.Logf("cycle %d kept reachable endpoint %s on %s", cycle+1, address, endpoint.Name)
	}
	// Real node deletion (including replacement under the same name) must
	// report unavailable instead of migrating to the otherwise eligible node.
	patchHostportServer(t, ns, name, map[string]any{"metadata": map[string]any{"annotations": map[string]any{"gameplane.local/idle-asleep-since": time.Now().UTC().Format(time.RFC3339)}}})
	if err := envInstance.K8s.CoreV1().Nodes().Delete(ctx, endpoint.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, 45*time.Second, func() (bool, string) { return hostportConditionIs(t, ns, name, "EndpointNodeUnavailable", false) })
	assertHostportPodsOnNode(t, ns, name, endpoint.Name)
}

func patchHostportServer(t *testing.T, ns, name string, patch map[string]any) {
	t.Helper()
	data, err := json.Marshal(patch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Patch(context.Background(), name, types.MergePatchType, data, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
}

func patchHostportNode(t *testing.T, name string, spec map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"spec": spec})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := envInstance.K8s.CoreV1().Nodes().Patch(context.Background(), name, types.MergePatchType, data, metav1.PatchOptions{}); err != nil && !apierrors.IsNotFound(err) {
		t.Error(err)
	}
}

func hostportConditionIs(t *testing.T, ns, name, reason string, ready bool) (bool, string) {
	t.Helper()
	gs, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		return false, err.Error()
	}
	conditions, _, _ := unstructured.NestedSlice(gs.Object, "status", "conditions")
	for _, item := range conditions {
		condition, ok := item.(map[string]any)
		if ok && condition["type"] == "HostportWakeReady" {
			return condition["reason"] == reason && (condition["status"] == "True") == ready, fmt.Sprint(condition)
		}
	}
	return false, "HostportWakeReady not yet present"
}

func assertHostportCondition(t *testing.T, ns, name, reason string, ready bool) {
	t.Helper()
	envInstance.Eventually(t, 15*time.Second, func() (bool, string) { return hostportConditionIs(t, ns, name, reason, ready) })
}

func assertHostportPodsOnNode(t *testing.T, ns, name, node string) {
	t.Helper()
	pods, err := envInstance.K8s.CoreV1().Pods(ns).List(t.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/instance=" + name})
	if err != nil {
		t.Fatal(err)
	}
	for _, pod := range pods.Items {
		if pod.Spec.NodeName != "" && pod.Spec.NodeName != node {
			t.Fatalf("listener %s moved to %s; endpoint remains %s", pod.Name, pod.Spec.NodeName, node)
		}
	}
}
