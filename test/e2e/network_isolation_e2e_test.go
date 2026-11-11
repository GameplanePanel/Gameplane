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
	aIP := isolationGameIP(t, ns, a, false)
	bIP := isolationGameIP(t, ns, b, false)
	cIP := isolationGameIP(t, ns, c, true)
	probeA := isolationProbe(t, ns, a, false)
	probeC := isolationProbe(t, ns, c, true)

	// Compatibility: absent-field templates keep their existing broad grant.
	isolationHTTP(t, ns, probeA, bIP, 25575, true)
	isolationHTTP(t, ns, probeC, cIP, 25566, true)
	isolationHTTP(t, ns, probeC, cIP, 25575, false)
	isolationHTTP(t, ns, probeC, aIP, 25566, false)
	isolationHTTP(t, ns, probeA, cIP, 25566, false)

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
	aIP, bIP = isolationGameIP(t, ns, a, true), isolationGameIP(t, ns, b, true)
	ss, err = envInstance.K8s.AppsV1().StatefulSets(ns).Get(ctx, a, metav1.GetOptions{})
	if err != nil || !reflect.DeepEqual(oldSelector, ss.Spec.Selector) {
		t.Fatalf("opt-in must preserve the immutable StatefulSet selector: %v", err)
	}
	_, err = envInstance.K8s.CoreV1().Pods(ns).Patch(ctx, probeA, types.MergePatchType,
		[]byte(`{"metadata":{"labels":{"gameplane.local/sentinel-isolation":"true"}}}`), metav1.PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	isolationHTTP(t, ns, probeA, aIP, 25566, true)
	isolationHTTP(t, ns, probeA, aIP, 25575, false)
	isolationHTTP(t, ns, probeA, bIP, 25566, false)
	isolationHTTP(t, ns, probeA, bIP, 25575, false)

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

	checkTenantNetworkProfile(t, ns, a, probeA)
}

func checkTenantNetworkProfile(t *testing.T, ns, a, probeA string) {
	t.Helper()
	ctx := t.Context()
	stamp := time.Now().UnixNano()
	// Exercise the opt-in upgrade after checking the historical compatibility
	// path. Identically named servers in separate namespaces must not become
	// each other's relay backend.
	tenant := fmt.Sprintf("tenant-net-%d", stamp)
	_, err := envInstance.K8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: tenant},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().Namespaces().Delete(context.Background(), tenant, metav1.DeleteOptions{})
	})
	helmUpgradeReuse(ctx, t, "operator.gameNamespaces[0]="+tenant, "networkPolicies.tenantIsolation=true",
		"networkPolicies.probePorts=null", "networkPolicies.backupEgress.enabled=false", "networkPolicies.gameEgress.enabled=false")
	secondTemplate := fmt.Sprintf("second-probe-%d", stamp)
	isolationTemplate(t, secondTemplate, true)
	_, err = envInstance.Dyn.Resource(gameTemplateGVR).Patch(ctx, secondTemplate, types.MergePatchType,
		[]byte(`{"spec":{"probes":{"readiness":{"httpGet":{"port":25575}}}}}`), metav1.PatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	applyBusyboxGameServer(t, tenant, a, secondTemplate)
	otherIP := isolationGameIP(t, tenant, a, true)
	aIP := isolationGameIP(t, ns, a, true)
	otherProbe := isolationProbe(t, tenant, a, true)
	isolationHTTP(t, tenant, otherProbe, otherIP, 25566, true)
	isolationHTTP(t, ns, probeA, otherIP, 25575, false)
	isolationHTTP(t, tenant, otherProbe, aIP, 25575, false)
	// Both Services resolve and are reachable from their own sentinels.
	// Cross-tenant denials through the same DNS names exercise Service NAT.
	home := a + "." + ns + ".svc"
	away := a + "." + tenant + ".svc"
	isolationHTTP(t, ns, probeA, home, 25566, true)
	isolationHTTP(t, tenant, otherProbe, away, 25566, true)
	isolationHTTP(t, ns, probeA, away, 25566, false)
	isolationHTTP(t, tenant, otherProbe, home, 25566, false)
	for namespace, want := range map[string]int{ns: 25566, tenant: 25575} {
		policy, err := envInstance.K8s.NetworkingV1().NetworkPolicies(namespace).Get(ctx, a+"-probes", metav1.GetOptions{})
		if err != nil || len(policy.Spec.Ingress) != 1 || len(policy.Spec.Ingress[0].Ports) != 1 ||
			policy.Spec.Ingress[0].Ports[0].Port == nil || policy.Spec.Ingress[0].Ports[0].Port.IntValue() != want {
			t.Fatalf("expected only kubelet probe port %d in %s: %v", want, namespace, err)
		}
	}
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

func isolationGameIP(t *testing.T, ns, name string, isolated bool) string {
	t.Helper()
	var ip string
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(t.Context(), name+"-0", metav1.GetOptions{})
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
func isolationProbe(t *testing.T, ns, server string, isolated bool) string {
	t.Helper()
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

func isolationHTTP(t *testing.T, ns, source, ip string, port int, allowed bool) {
	t.Helper()
	// Every exec must succeed regardless of network outcome. Otherwise a broken
	// kubectl/CNI fixture could masquerade as the expected denied connection.
	target := fmt.Sprintf("http://%s:%d/", ip, port)
	envInstance.Eventually(t, 45*time.Second, func() (bool, string) {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		out, err := envInstance.Kubectl(ctx, "exec", "-n", ns, source, "--",
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
