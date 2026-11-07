//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Dedicated serial bucket: real Prometheus Operator discovery and Cilium policy
// enforcement, with deterministic game readouts instead of an external module.
func TestMonitoring_DiscoveryPerformanceAndPolicy(t *testing.T) {
	ctx := t.Context()
	if _, err := envInstance.K8s.AppsV1().DaemonSets("kube-system").Get(ctx, "cilium", metav1.GetOptions{}); err != nil {
		t.Fatalf("monitoring requires enforcing Cilium: %v", err)
	}
	port, stop := envInstance.PortForward(t, "monitoring", "svc/prometheus-operated", 9090)
	defer stop()
	client := &http.Client{Timeout: 10 * time.Second}
	query := func(expression string) (string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/v1/query?query=%s", port, url.QueryEscape(expression)), nil)
		if err != nil {
			return "", err
		}
		response, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer response.Body.Close()
		var result struct {
			Status string `json:"status"`
			Data   struct {
				Result []struct {
					Value []json.RawMessage `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
			return "", err
		}
		if response.StatusCode != 200 || result.Status != "success" || len(result.Data.Result) != 1 || len(result.Data.Result[0].Value) != 2 {
			return "", fmt.Errorf("query %q returned status %s and %d results", expression, result.Status, len(result.Data.Result))
		}
		var value string
		err = json.Unmarshal(result.Data.Result[0].Value[1], &value)
		return value, err
	}
	waitQuery := func(expression, want string) {
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			got, err := query(expression)
			return err == nil && got == want, fmt.Sprintf("%s: got %s want %s (%v)", expression, got, want, err)
		})
	}
	envInstance.ApplyYAML(t, "frps.yaml")
	stamp := time.Now().UnixNano()
	name := fmt.Sprintf("monitoring-%d", stamp)
	const gameCode = `from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer
import json,pathlib,threading
value=pathlib.Path('/tmp/performance')
count=pathlib.Path('/tmp/performance-queries')
value.write_text('20')
count.write_text('0')
class Handler(BaseHTTPRequestHandler):
    def log_message(self,*args): pass
    def do_GET(self):
        self.send_response(200); self.end_headers(); self.wfile.write(b'ok')
    def do_POST(self):
        command=json.loads(self.rfile.read(int(self.headers.get('Content-Length','0')))).get('command','')
        if command=='stats':
            count.write_text(str(int(count.read_text())+1))
            output='TPS: '+value.read_text()+' Tick: 50'
        else: output='0 players online'
        self.send_response(200); self.end_headers(); self.wfile.write(json.dumps({'output':output}).encode())
threading.Thread(target=ThreadingHTTPServer(('127.0.0.1',25575),Handler).serve_forever,daemon=True).start()
ThreadingHTTPServer(('0.0.0.0',25566),Handler).serve_forever()
`
	tmpl := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "GameTemplate", "metadata": map[string]any{"name": name}, "spec": map[string]any{
		"displayName": "Monitoring fixture", "game": "noop", "version": "1", "image": "python:3.14-alpine", "consoleMode": "rcon", "command": []any{"python", "-u", "-c"}, "args": []any{gameCode}, "sentinelIsolation": true,
		"ports": []any{map[string]any{"name": "game", "containerPort": int64(25566), "protocol": "TCP", "advertise": true}}, "rcon": map[string]any{"protocol": "rest", "port": int64(25575)},
		"capabilities": map[string]any{"status": map[string]any{"metrics": []any{
			map[string]any{"id": "tps", "displayName": "TPS", "command": "stats", "regex": `TPS: (?P<value>\S+)`, "performance": map[string]any{"signal": "tick_rate", "unit": "per_second", "degradedThreshold": "18"}},
			map[string]any{"id": "tick", "displayName": "Tick", "command": "stats", "regex": `Tick: (?P<value>\S+)`, "performance": map[string]any{"signal": "tick_duration", "unit": "milliseconds"}},
			map[string]any{"id": "display", "displayName": "Display only", "command": "stats", "regex": `TPS: (?P<value>\S+)`},
		}}},
	}}}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).Create(ctx, tmpl, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	gs := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer", "metadata": map[string]any{"name": name, "namespace": ocGamesNS}, "spec": map[string]any{"templateRef": map[string]any{"name": name}, "networking": map[string]any{"expose": "ClusterIP", "tunnel": map[string]any{"enabled": false, "provider": "frp", "frp": map[string]any{"serverAddr": ocFrpsAddr, "remotePorts": []any{map[string]any{"name": "game", "remotePort": int64(30020)}}}}}}}}
	gs, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ocGamesNS).Create(ctx, gs, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ocGamesNS).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name + "-auth", Namespace: ocGamesNS, OwnerReferences: []metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: name, UID: gs.GetUID()}}}, StringData: map[string]string{"token": ocFrpsToken}}
	if _, err := envInstance.K8s.CoreV1().Secrets(ocGamesNS).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"networking": map[string]any{"tunnel": map[string]any{"enabled": true, "credentialsSecretRef": map[string]any{"name": secret.Name}}}}})
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ocGamesNS).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	selector := fmt.Sprintf(`namespace="gameplane-games",server="%s"`, name)
	waitQuery(`max(up{gameplane_component="agent",`+selector+`})`, "1")
	waitQuery(`max(up{gameplane_component="tunnel",`+selector+`})`, "1")
	waitQuery(`max(gameplane_tunnel_ready{`+selector+`})`, "1")
	waitQuery(`max(gameplane_agent_performance_value{metric="tps",`+selector+`})`, "20")
	waitQuery(`max(gameplane_agent_performance_value{metric="tick",`+selector+`})`, "0.05")
	waitQuery(`count(gameplane_agent_performance_value{metric="display",`+selector+`}) or vector(0)`, "0")
	readCount := func() int {
		out, err := envInstance.Kubectl(ctx, "exec", "-n", ocGamesNS, name+"-0", "-c", "game", "--", "python", "-c", `from pathlib import Path; print(Path('/tmp/performance-queries').read_text())`)
		if err != nil {
			t.Fatal(err)
		}
		n, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	pod, err := envInstance.K8s.CoreV1().Pods(ocGamesNS).Get(ctx, name+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	approvedProbe := name + "-approved"
	monitoringProbe(t, "monitoring", approvedProbe)
	before := readCount()
	scrapeStart := time.Now()
	const scrapeMany = `import urllib.request,sys
for _ in range(20):
    with urllib.request.urlopen(sys.argv[1],timeout=2) as response:
        assert b'gameplane_agent_performance_value' in response.read()
`
	if out, err := envInstance.Kubectl(ctx, "exec", "-n", "monitoring", approvedProbe, "--", "python", "-c", scrapeMany, "http://"+pod.Status.PodIP+":9090/metrics"); err != nil {
		t.Fatalf("repeated direct scrape failed: %v %s", err, out)
	}
	if after := readCount(); after-before > int(time.Since(scrapeStart)/(30*time.Second))+1 {
		t.Fatalf("scrapes triggered RCON: %d -> %d", before, after)
	}
	for _, value := range []string{"0", "NaN", "20"} {
		if out, err := envInstance.Kubectl(ctx, "exec", "-n", ocGamesNS, name+"-0", "-c", "game", "--", "python", "-c", `from pathlib import Path; import sys; Path('/tmp/performance').write_text(sys.argv[1])`, value); err != nil {
			t.Fatalf("set measurement: %v %s", err, out)
		}
		if value == "NaN" {
			waitQuery(`max(gameplane_agent_performance_available{metric="tps",`+selector+`})`, "0")
			waitQuery(`count(gameplane_agent_performance_value{metric="tps",`+selector+`}) or vector(0)`, "0")
		} else {
			waitQuery(`max(gameplane_agent_performance_value{metric="tps",`+selector+`})`, value)
		}
	}
	// A successful Prometheus query is not proof of policy enforcement. Revoke
	// exactly the chart's scrape exception, observe scrape loss, then restore.
	policy, err := envInstance.K8s.NetworkingV1().NetworkPolicies(ocGamesNS).Get(ctx, "allow-prometheus-to-agent", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := envInstance.K8s.NetworkingV1().NetworkPolicies(ocGamesNS).Delete(ctx, policy.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	policy.ResourceVersion = ""
	policy.UID = ""
	t.Cleanup(func() {
		_, _ = envInstance.K8s.NetworkingV1().NetworkPolicies(ocGamesNS).Create(context.Background(), policy, metav1.CreateOptions{})
	})
	waitQuery(`max(up{gameplane_component="agent",`+selector+`})`, "0")
	if _, err := envInstance.K8s.NetworkingV1().NetworkPolicies(ocGamesNS).Create(ctx, policy, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitQuery(`max(up{gameplane_component="agent",`+selector+`})`, "1")
	// The system namespace is deliberately outside the monitoring selector.
	probeName := name + "-probe"
	monitoringProbe(t, ocNS, probeName)
	const denied = `import urllib.request,sys
try:
    urllib.request.urlopen(sys.argv[1],timeout=2).read()
    print('ALLOWED')
except (OSError,TimeoutError):
    print('DENIED')
`
	out, err := envInstance.Kubectl(ctx, "exec", "-n", ocNS, probeName, "--", "python", "-c", denied, "http://"+pod.Status.PodIP+":9090/metrics")
	if err != nil || strings.TrimSpace(out) != "DENIED" {
		t.Fatalf("unapproved namespace reached metrics, or probe failed: %v %s", err, out)
	}
}

func monitoringProbe(t *testing.T, namespace, name string) {
	t.Helper()
	probe := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "probe", Image: "python:3.14-alpine", Command: []string{"python", "-c", "import time; time.sleep(900)"}}}}}
	nonRoot, no, uid := true, false, int64(65532)
	probe.Spec.AutomountServiceAccountToken = &no
	probe.Spec.SecurityContext = &corev1.PodSecurityContext{RunAsNonRoot: &nonRoot, RunAsUser: &uid, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}
	probe.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{AllowPrivilegeEscalation: &no, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}
	if _, err := envInstance.K8s.CoreV1().Pods(namespace).Create(t.Context(), probe, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().Pods(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		ready, err := envInstance.PodIsReady(t.Context(), namespace, name)
		return ready, fmt.Sprint(err)
	})
}
