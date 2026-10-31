//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// This bucket uses Cilium, not kindnet. Exercise the effective union of all
// installed policies, including an existing legacy server's opt-in rollout.
func TestNetworkIsolation_EnforcedAndUpgrade(t *testing.T) {
	ctx := t.Context()
	const ns = "gameplane-games"
	if _, err := envInstance.K8s.AppsV1().DaemonSets("kube-system").Get(ctx, "cilium", metav1.GetOptions{}); err != nil {
		t.Fatalf("network-isolation bucket requires the enforcing Cilium fixture: %v", err)
	}
	stamp := time.Now().UnixNano()
	legacyTemplate := fmt.Sprintf("net-legacy-%d", stamp)
	isolatedTemplate := fmt.Sprintf("net-isolated-%d", stamp)
	isolationTemplate(t, legacyTemplate, false)
	isolationTemplate(t, isolatedTemplate, true)
	a, b, c := fmt.Sprintf("net-a-%d", stamp), fmt.Sprintf("net-b-%d", stamp), fmt.Sprintf("net-c-%d", stamp)
	for _, pair := range [][2]string{{a, legacyTemplate}, {b, legacyTemplate}, {c, isolatedTemplate}} {
		applyBusyboxGameServer(t, ns, pair[0], pair[1])
	}
	aIP := isolationGameIP(t, a, false)
	bIP := isolationGameIP(t, b, false)
	cIP := isolationGameIP(t, c, true)
	probeA := isolationProbe(t, a, false)
	probeC := isolationProbe(t, c, true)

	// Compatibility: absent-field templates keep their existing broad grant.
	isolationHTTP(t, probeA, bIP, 25575, true)
	isolationHTTP(t, probeC, cIP, 25566, true)
	isolationHTTP(t, probeC, cIP, 25575, false)
	isolationHTTP(t, probeC, aIP, 25566, false)
	isolationHTTP(t, probeA, cIP, 25566, false)

	ss, err := envInstance.K8s.AppsV1().StatefulSets(ns).Get(ctx, a, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	oldSelector := ss.Spec.Selector.DeepCopy()
	_, err = envInstance.Dyn.Resource(gameTemplateGVR).Patch(ctx, legacyTemplate, types.MergePatchType,
		[]byte(`{"spec":{"sentinelIsolation":true}}`), metav1.PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	aIP, bIP = isolationGameIP(t, a, true), isolationGameIP(t, b, true)
	ss, err = envInstance.K8s.AppsV1().StatefulSets(ns).Get(ctx, a, metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(oldSelector, ss.Spec.Selector) {
		t.Fatalf("opt-in must preserve the immutable StatefulSet selector: %v", err)
	}
	_, err = envInstance.K8s.CoreV1().Pods(ns).Patch(ctx, probeA, types.MergePatchType,
		[]byte(`{"metadata":{"labels":{"gameplane.local/sentinel-isolation":"true"}}}`), metav1.PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	isolationHTTP(t, probeA, aIP, 25566, true)
	isolationHTTP(t, probeA, aIP, 25575, false)
	isolationHTTP(t, probeA, bIP, 25566, false)
	isolationHTTP(t, probeA, bIP, 25575, false)

	// Kubelet HTTP readiness and agent heartbeats still work with strict CIDRs.
	// Also exercise an actual API -> agent mTLS call through its Service.
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()
	requireAgentReady(t, ns, a)
	waitAgentReachable(t, cli, a)
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		gs, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, a, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		hb, _, _ := unstructured.NestedString(gs.Object, "status", "agent", "lastHeartbeat")
		stamp, err := time.Parse(time.RFC3339, hb)
		return err == nil && time.Since(stamp) < 45*time.Second, "waiting for fresh agent heartbeat"
	})
}

func isolationTemplate(t *testing.T, name string, isolated bool) {
	t.Helper()
	spec := map[string]any{
		"displayName": name, "game": "busybox", "version": "1", "image": "busybox:1.36",
		"command": []any{"sh", "-c", "mkdir -p /tmp/http; echo reachable >/tmp/http/index.html; httpd -p 25575 -h /tmp/http; exec httpd -f -p 25566 -h /tmp/http"},
		"ports": []any{
			map[string]any{"name": "game", "containerPort": int64(25566), "protocol": "TCP", "advertise": true},
			map[string]any{"name": "management", "containerPort": int64(25575), "protocol": "TCP", "advertise": false},
		},
		"probes": map[string]any{"readiness": map[string]any{
			"httpGet": map[string]any{"path": "/", "port": int64(25566)}, "periodSeconds": int64(2),
		}},
	}
	if isolated {
		spec["sentinelIsolation"] = true
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "GameTemplate",
		"metadata": map[string]any{"name": name}, "spec": spec,
	}}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).Create(t.Context(), obj, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

func isolationGameIP(t *testing.T, name string, isolated bool) string {
	t.Helper()
	var ip string
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods("gameplane-games").Get(t.Context(), name+"-0", metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		if pod.DeletionTimestamp != nil || (pod.Labels["gameplane.local/sentinel-isolation"] == "true") != isolated {
			return false, "waiting for rollout with expected isolation label"
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue && pod.Status.PodIP != "" {
				ip = pod.Status.PodIP
				return true, ""
			}
		}
		return false, "waiting for HTTP readiness probe"
	})
	return ip
}

// A synthetic client carries the exact real sentinel selectors. It exposes no
// listener and owns no Service/Deployment, so the running game is unaffected.
func isolationProbe(t *testing.T, server string, isolated bool) string {
	t.Helper()
	const ns = "gameplane-games"
	name := server + "-probe"
	labels := map[string]string{"app.kubernetes.io/name": "gameplane-waker", "app.kubernetes.io/instance": server}
	if isolated {
		labels["gameplane.local/sentinel-isolation"] = "true"
	}
	noToken := false
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec: corev1.PodSpec{AutomountServiceAccountToken: &noToken,
			Containers: []corev1.Container{{Name: "probe", Image: "busybox:1.36", Command: []string{"sleep", "3600"}}}},
	}
	if _, err := envInstance.K8s.CoreV1().Pods(ns).Create(t.Context(), pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().Pods(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		got, err := envInstance.K8s.CoreV1().Pods(ns).Get(t.Context(), name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		return got.Status.Phase == corev1.PodRunning, "waiting for probe pod"
	})
	return name
}

func isolationHTTP(t *testing.T, source, ip string, port int, allowed bool) {
	t.Helper()
	// Every exec must succeed regardless of network outcome. Otherwise a broken
	// kubectl/CNI fixture could masquerade as the expected denied connection.
	target := fmt.Sprintf("http://%s:%d/", ip, port)
	envInstance.Eventually(t, 45*time.Second, func() (bool, string) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		out, err := envInstance.Kubectl(ctx, "exec", "-n", "gameplane-games", source, "--",
			"sh", "-c", `if wget -q -T 2 -O /dev/null "$1"; then echo ALLOWED; else echo DENIED; fi`, "probe", target)
		if err != nil {
			return false, "probe execution failed: " + err.Error() + out
		}
		got := isolationProbeVerdict(out)
		want := "DENIED"
		if allowed {
			want = "ALLOWED"
		}
		return got == want, fmt.Sprintf("%s -> %s: got %q, want %s; output: %q", source, target, got, want, out)
	})
}

// Kubectl combines stdout and stderr, so wget's expected timeout diagnostic
// can precede the result. Require one exact marker; never accept missing or
// contradictory results, and retain the full output in the failure message.
func isolationProbeVerdict(out string) string {
	var verdict string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "ALLOWED" && line != "DENIED" {
			continue
		}
		if verdict != "" {
			return ""
		}
		verdict = line
	}
	return verdict
}

func TestNetworkIsolation_ProbeOutput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, output, want string }{
		{"allowed", "ALLOWED\n", "ALLOWED"},
		{"denied with wget timeout", "wget: download timed out\nDENIED\n", "DENIED"},
		{"denied with carriage return", "wget: connection refused\r\nDENIED\r\n", "DENIED"},
		{"diagnostic only", "wget: download timed out\n", ""},
		{"marker substring", "unexpected ALLOWED response\n", ""},
		{"contradictory markers", "ALLOWED\nDENIED\n", ""},
		{"duplicate markers", "DENIED\nDENIED\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isolationProbeVerdict(tc.output); got != tc.want {
				t.Fatalf("probe verdict = %q, want %q for %q", got, tc.want, tc.output)
			}
		})
	}
}
