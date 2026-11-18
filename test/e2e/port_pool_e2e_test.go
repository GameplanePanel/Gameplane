//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The kind install gives these two templates the port pool e2e-pool,
// 31000-31005 (deploy/kind/e2e.sh), so their names are fixed.
const (
	portPoolTemplateA = "e2e-portpool-a" // two advertised TCP ports; serves HTTP on the first
	portPoolTemplateB = "e2e-portpool-b" // one advertised TCP port
)

// portPoolFixture answers HTTP on 8080, so a client can reach the server
// through its pool port.
const portPoolFixture = `from http.server import BaseHTTPRequestHandler,HTTPServer
class Handler(BaseHTTPRequestHandler):
    def log_message(self,*args): pass
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b'pooled\n')
HTTPServer(('0.0.0.0',8080),Handler).serve_forever()
`

// TestGameServer_PortPoolAllocation — servers of two templates share one
// pool: each gets the lowest free contiguous block, an explicit servicePort
// is reserved so the pool doesn't hand it out, a full pool reports
// PoolExhausted with no Service rather than falling back to the template's
// ports, and a deleted server's block goes to the server waiting for one.
// The main Service publishes the pool ports and reaches the game; the
// sentinel's game-direct Service keeps the container ports it dials.
func TestGameServer_PortPoolAllocation(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	ns := "gameplane-games"
	suffix := time.Now().UnixNano()

	createTemplate := func(name string, spec map[string]any) {
		t.Helper()
		tmpl := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1", "kind": "GameTemplate",
			"metadata": map[string]any{"name": name}, "spec": spec,
		}}
		if _, err := envInstance.Dyn.Resource(gameTemplateGVR).
			Create(ctx, tmpl, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create template %s: %v", name, err)
		}
		t.Cleanup(func() {
			_ = envInstance.Dyn.Resource(gameTemplateGVR).Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{})
		})
	}
	createTemplate(portPoolTemplateA, map[string]any{
		"displayName": "Port pool fixture A", "game": "noop", "version": "1",
		"image": "python:3.14-alpine", "consoleMode": "none",
		"command": []any{"python", "-u", "-c"}, "args": []any{portPoolFixture},
		"ports": []any{
			map[string]any{"name": "game", "containerPort": int64(8080), "protocol": "TCP", "advertise": true},
			map[string]any{"name": "extra", "containerPort": int64(8081), "protocol": "TCP", "advertise": true},
		},
	})
	createTemplate(portPoolTemplateB, map[string]any{
		"displayName": "Port pool fixture B", "game": "busybox", "version": "1",
		"image": "busybox:1.36", "command": []any{"sh", "-c", "sleep 100000"},
		"ports": []any{
			map[string]any{"name": "game", "containerPort": int64(12345), "protocol": "TCP", "advertise": true},
		},
	})

	name := func(s string) string { return fmt.Sprintf("e2e-pp-%s-%d", s, suffix) }
	createServer := func(s, tmpl string, spec map[string]any) string {
		t.Helper()
		gsName := name(s)
		spec["templateRef"] = map[string]any{"name": tmpl}
		gs := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer",
			"metadata": map[string]any{"name": gsName, "namespace": ns}, "spec": spec,
		}}
		if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
			Create(ctx, gs, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create gameserver %s: %v", gsName, err)
		}
		t.Cleanup(func() {
			_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
				Delete(context.WithoutCancel(ctx), gsName, metav1.DeleteOptions{})
		})
		return gsName
	}
	// waitPorts waits until the server's Service publishes want (name ->
	// port) and its PortAllocation condition is Allocated.
	waitPorts := func(gsName string, want map[string]int32) {
		t.Helper()
		envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
			svc, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, gsName, metav1.GetOptions{})
			if err != nil {
				return false, "get service: " + err.Error()
			}
			got := map[string]int32{}
			for _, p := range svc.Spec.Ports {
				got[p.Name] = p.Port
			}
			for k, v := range want {
				if got[k] != v {
					return false, fmt.Sprintf("%s ports = %v, want %v", gsName, got, want)
				}
			}
			obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, gsName, metav1.GetOptions{})
			if err != nil {
				return false, "get gameserver: " + err.Error()
			}
			cond := findCondition(obj.Object, "PortAllocation")
			if cond == nil || cond["status"] != "True" || cond["reason"] != "Allocated" {
				return false, fmt.Sprintf("%s PortAllocation = %v, want True/Allocated", gsName, cond)
			}
			return true, ""
		})
	}

	// Servers are created one at a time, each once the previous one holds
	// its block, so the expected blocks don't depend on reconcile order.
	a1 := createServer("a1", portPoolTemplateA, map[string]any{
		"networking": map[string]any{"expose": "ClusterIP"},
		"idle":       map[string]any{"enabled": true, "afterMinutes": int64(60), "wakeOnConnect": true},
	})
	waitPorts(a1, map[string]int32{"game": 31000, "extra": 31001})
	b1 := createServer("b1", portPoolTemplateB, map[string]any{})
	waitPorts(b1, map[string]int32{"game": 31002})
	// a2's explicit servicePort for "extra" is reserved, and its pooled
	// "game" port takes the next free one.
	a2 := createServer("a2", portPoolTemplateA, map[string]any{
		"networking": map[string]any{"portOverrides": []any{
			map[string]any{"name": "extra", "servicePort": int64(31004)},
		}},
	})
	waitPorts(a2, map[string]int32{"game": 31003, "extra": 31004})
	b2 := createServer("b2", portPoolTemplateB, map[string]any{})
	waitPorts(b2, map[string]int32{"game": 31005})

	// The pool is full: b3 gets no Service, and says why.
	b3 := createServer("b3", portPoolTemplateB, map[string]any{})
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, b3, metav1.GetOptions{})
		if err != nil {
			return false, "get gameserver: " + err.Error()
		}
		cond := findCondition(obj.Object, "PortAllocation")
		if cond == nil || cond["status"] != "False" || cond["reason"] != "PoolExhausted" {
			return false, fmt.Sprintf("PortAllocation = %v, want False/PoolExhausted", cond)
		}
		if msg, _ := cond["message"].(string); !strings.Contains(msg, "e2e-pool") {
			return false, fmt.Sprintf("message %q doesn't name the pool", msg)
		}
		return true, ""
	})
	if _, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, b3, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("b3 Service: got %v, want none while the pool is full", err)
	}

	// Deleting b2 frees 31005 for b3.
	if err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Delete(ctx, b2, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete b2: %v", err)
	}
	waitPorts(b3, map[string]int32{"game": 31005})

	// a1's Service forwards its pool ports to the container ports, and
	// reaches the game.
	svc, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, a1, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get a1 service: %v", err)
	}
	for _, p := range svc.Spec.Ports {
		want := map[string]int32{"game": 8080, "extra": 8081}[p.Name]
		if p.TargetPort.IntVal != want {
			t.Errorf("a1 %s targetPort = %d, want %d", p.Name, p.TargetPort.IntVal, want)
		}
	}
	// The probe runs outside the games namespace, whose default-deny egress
	// would stop it, the way a player's traffic arrives from outside.
	probeNS := "default"
	probe := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name("probe"), Namespace: probeNS},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{
			Name: "probe", Image: "busybox:1.36", Command: []string{"sh", "-c", "sleep 900"},
		}}},
	}
	if _, err := envInstance.K8s.CoreV1().Pods(probeNS).Create(ctx, probe, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create probe pod: %v", err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().Pods(probeNS).Delete(context.WithoutCancel(ctx), probe.Name, metav1.DeleteOptions{})
	})
	url := fmt.Sprintf("http://%s.%s.svc:31000/", a1, ns)
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		out, err := envInstance.Kubectl(ctx, "exec", "-n", probeNS, probe.Name, "--", "wget", "-qO-", "-T", "5", url)
		if err != nil || !strings.Contains(out, "pooled") {
			return false, fmt.Sprintf("GET %s: %v %s", url, err, out)
		}
		return true, ""
	})

	// The sentinel dials the game at its container ports, so the game-direct
	// Service publishes those, not the pool ports.
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		direct, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, a1+"-game-direct", metav1.GetOptions{})
		if err != nil {
			return false, "get game-direct service: " + err.Error()
		}
		for _, p := range direct.Spec.Ports {
			if p.Port != p.TargetPort.IntVal {
				return false, fmt.Sprintf("game-direct %s port %d, want its container port %d", p.Name, p.Port, p.TargetPort.IntVal)
			}
		}
		return true, ""
	})
}
