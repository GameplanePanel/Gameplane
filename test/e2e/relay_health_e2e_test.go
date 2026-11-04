//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Called by the serial optional-components lifecycle test while its real frps
// fixture is installed. Both GameServers share one template and one relay.
func testFrpIdentityHealth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	suffix := time.Now().UnixNano() % 1_000_000
	templateName := fmt.Sprintf("relay-echo-%d", suffix)
	const echo = `import socketserver, socket, threading
prefix = socket.gethostname().encode() + b':'
class TCP(socketserver.BaseRequestHandler):
    def handle(self):
        data = b''
        while len(data) < 12:
            chunk = self.request.recv(12-len(data))
            if not chunk: return
            data += chunk
        self.request.sendall(prefix + data)
class UDP(socketserver.BaseRequestHandler):
    def handle(self):
        data, sock = self.request
        sock.sendto(prefix + data, self.client_address)
tcp = socketserver.ThreadingTCPServer(('0.0.0.0', 25565), TCP)
threading.Thread(target=tcp.serve_forever, daemon=True).start()
socketserver.ThreadingUDPServer(('0.0.0.0', 19132), UDP).serve_forever()
`
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "GameTemplate", "metadata": map[string]any{"name": templateName},
		"spec": map[string]any{"displayName": "Relay echo", "game": "noop", "version": "1", "image": "python:3.14-alpine", "consoleMode": "none", "command": []any{"python", "-u", "-c"}, "args": []any{echo},
			"ports": []any{map[string]any{"name": "game", "containerPort": int64(25565), "protocol": "TCP", "advertise": true}, map[string]any{"name": "query", "containerPort": int64(19132), "protocol": "UDP", "advertise": true}}},
	}}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).Create(ctx, tmpl, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).Delete(context.Background(), templateName, metav1.DeleteOptions{})
	})
	gsClient := envInstance.Dyn.Resource(gameServerGVR).Namespace(ocGamesNS)
	names := []string{fmt.Sprintf("relay-a-%d", suffix), fmt.Sprintf("relay-b-%d", suffix)}
	uids := make([]types.UID, 2)
	for i, name := range names {
		gs := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer", "metadata": map[string]any{"name": name, "namespace": ocGamesNS},
			"spec": map[string]any{"templateRef": map[string]any{"name": templateName}, "networking": map[string]any{"expose": "ClusterIP", "tunnel": map[string]any{"enabled": false, "provider": "frp", "frp": map[string]any{"serverAddr": ocFrpsAddr, "remotePorts": []any{map[string]any{"name": "game", "remotePort": int64(30000 + i*10)}, map[string]any{"name": "query", "remotePort": int64(30001 + i*10)}}}}}}}}
		created, err := gsClient.Create(ctx, gs, metav1.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		uids[i] = created.GetUID()
		t.Cleanup(func() {
			_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ocGamesNS).Delete(context.Background(), name, metav1.DeleteOptions{})
		})
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name + "-auth", Namespace: ocGamesNS, OwnerReferences: []metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: name, UID: created.GetUID()}}}, StringData: map[string]string{"token": ocFrpsToken}}
		if _, err := envInstance.K8s.CoreV1().Secrets(ocGamesNS).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		patch, err := json.Marshal(map[string]any{"spec": map[string]any{"networking": map[string]any{"tunnel": map[string]any{"enabled": true, "credentialsSecretRef": map[string]any{"name": secret.Name}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := gsClient.Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if uids[0] == uids[1] {
		t.Fatal("fixture GameServers must have distinct identities")
	}
	waitReady := func(name string, want bool) {
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			gs, err := gsClient.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			ready, detail := ocTunnelReady(gs)
			conditions, _, _ := unstructured.NestedSlice(gs.Object, "status", "conditions")
			current := false
			for _, entry := range conditions {
				cond, ok := entry.(map[string]any)
				if !ok || cond["type"] != "TunnelReady" {
					continue
				}
				observed, _, _ := unstructured.NestedInt64(cond, "observedGeneration")
				current = observed == gs.GetGeneration()
			}
			if !current {
				return false, "waiting for current-generation tunnel condition"
			}
			return ready == want, detail
		})
	}
	for _, name := range names {
		waitReady(name, true)
	}
	// Use an independent client Pod: neither kubectl TCP port-forward nor a
	// backend-local probe can stand in for real UDP through the relay.
	probeName := fmt.Sprintf("relay-probe-%d", suffix)
	probe := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: probeName, Namespace: ocNS}, Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{{Name: "probe", Image: "python:3.14-alpine", Command: []string{"python", "-c", "import time; time.sleep(900)"}}}}}
	nonRoot, no, uid := true, false, int64(65532)
	probe.Spec.AutomountServiceAccountToken = &no
	probe.Spec.SecurityContext = &corev1.PodSecurityContext{RunAsNonRoot: &nonRoot, RunAsUser: &uid, SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}
	probe.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{AllowPrivilegeEscalation: &no, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}
	if _, err := envInstance.K8s.CoreV1().Pods(ocNS).Create(ctx, probe, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().Pods(ocNS).Delete(context.Background(), probeName, metav1.DeleteOptions{})
	})
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		ready, err := envInstance.PodIsReady(ctx, ocNS, probeName)
		return ready, fmt.Sprint(err)
	})
	const probeCode = `import socket,sys
host,port,kind,want = sys.argv[1:]
with socket.socket(socket.AF_INET, socket.SOCK_STREAM if kind == 'tcp' else socket.SOCK_DGRAM) as s:
    s.settimeout(4)
    s.connect((host,int(port)))
    s.sendall(b'game-payload')
    expected = (want+':game-payload').encode()
    reply = s.recv(4096)
    if kind == 'tcp':
        while len(reply) < len(expected):
            chunk = s.recv(4096)
            if not chunk: break
            reply += chunk
    assert reply == expected, repr(reply)
print('forwarded')
`
	traffic := func(i int) {
		envInstance.Eventually(t, time.Minute, func() (bool, string) {
			pods, err := envInstance.K8s.CoreV1().Pods(ocNS).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=e2e-frps"})
			if err != nil || len(pods.Items) != 1 {
				return false, fmt.Sprintf("frps pods: %v", err)
			}
			for j, protocol := range []string{"tcp", "udp"} {
				out, err := envInstance.Kubectl(ctx, "exec", "-n", ocNS, probeName, "--", "python", "-c", probeCode, pods.Items[0].Status.PodIP, fmt.Sprint(30000+i*10+j), protocol, names[i]+"-0")
				if err != nil {
					return false, fmt.Sprintf("%s: %v %s", protocol, err, out)
				}
			}
			return true, ""
		})
	}
	traffic(0)
	traffic(1)
	// A conflicting registration is rejected even while the child and its
	// local HTTP server stay alive. Restoring the mapping recovers readiness.
	for _, remotePort := range []int{30000, 30010} {
		patch := []byte(fmt.Sprintf(`{"spec":{"networking":{"tunnel":{"frp":{"remotePorts":[{"name":"game","remotePort":%d},{"name":"query","remotePort":30011}]}}}}}`, remotePort))
		if _, err := gsClient.Patch(ctx, names[1], types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			t.Fatal(err)
		}
		waitReady(names[1], remotePort == 30010)
		if remotePort == 30000 {
			envInstance.Consistently(t, 10*time.Second, time.Second, func() (bool, string) {
				gs, err := gsClient.Get(ctx, names[1], metav1.GetOptions{})
				if err != nil {
					return false, err.Error()
				}
				ready, detail := ocTunnelReady(gs)
				return !ready, detail
			})
		}
		waitReady(names[0], true)
		traffic(0)
	}
	traffic(1)
	// Recreate replacement reuses the UID. The other server stays connected.
	oldPods, err := envInstance.K8s.CoreV1().Pods(ocGamesNS).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-tunnel,app.kubernetes.io/instance=" + names[0]})
	if err != nil || len(oldPods.Items) != 1 {
		t.Fatalf("expected one tunnel Pod before replacement: %v", err)
	}
	if _, err := envInstance.K8s.AppsV1().Deployments(ocGamesNS).Patch(ctx, names[0]+"-tunnel", types.MergePatchType, []byte(`{"spec":{"template":{"metadata":{"annotations":{"relay-test":"replace"}}}}}`), metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	envInstance.Consistently(t, 10*time.Second, time.Second, func() (bool, string) {
		gs, err := gsClient.Get(ctx, names[1], metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		return ocTunnelReady(gs)
	})
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		pods, err := envInstance.K8s.CoreV1().Pods(ocGamesNS).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-tunnel,app.kubernetes.io/instance=" + names[0]})
		if err != nil || len(pods.Items) != 1 {
			return false, fmt.Sprint(err)
		}
		pod := pods.Items[0]
		return pod.UID != oldPods.Items[0].UID && pod.Status.Phase == corev1.PodRunning && pod.DeletionTimestamp == nil, "waiting for the old identity owner to drain and its replacement to start"
	})
	waitReady(names[0], true)
	traffic(0)
	traffic(1)
	// Invalid credentials must not be masked by a living supervisor. Force a
	// fresh child so the projected Secret is read again, then repair it.
	for _, token := range []string{"invalid-fixture-token", ocFrpsToken} {
		secret, err := envInstance.K8s.CoreV1().Secrets(ocGamesNS).Get(ctx, names[0]+"-auth", metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		secret.Data["token"] = []byte(token)
		if _, err := envInstance.K8s.CoreV1().Secrets(ocGamesNS).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
		pods, err := envInstance.K8s.CoreV1().Pods(ocGamesNS).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-tunnel,app.kubernetes.io/instance=" + names[0]})
		if err != nil {
			t.Fatal(err)
		}
		for _, pod := range pods.Items {
			if err := envInstance.K8s.CoreV1().Pods(ocGamesNS).Delete(ctx, pod.Name, metav1.DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		envInstance.Eventually(t, time.Minute, func() (bool, string) {
			replacements, err := envInstance.K8s.CoreV1().Pods(ocGamesNS).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-tunnel,app.kubernetes.io/instance=" + names[0]})
			if err != nil || len(replacements.Items) != 1 {
				return false, fmt.Sprint(err)
			}
			replacement := replacements.Items[0]
			for _, old := range pods.Items {
				if old.UID == replacement.UID {
					return false, "old Pod has not drained"
				}
			}
			return replacement.Status.Phase == corev1.PodRunning, "waiting for replacement supervisor"
		})
		waitReady(names[0], token == ocFrpsToken)
		if token != ocFrpsToken {
			envInstance.Consistently(t, 10*time.Second, time.Second, func() (bool, string) {
				gs, err := gsClient.Get(ctx, names[0], metav1.GetOptions{})
				if err != nil {
					return false, err.Error()
				}
				ready, detail := ocTunnelReady(gs)
				return !ready, detail
			})
		}
		waitReady(names[1], true)
	}
	traffic(0)
	// Stop the real control endpoint, then restore it. Both independent
	// supervisors must lose readiness and recover their own mappings.
	t.Cleanup(func() {
		_, _ = envInstance.K8s.AppsV1().Deployments(ocNS).Patch(context.Background(), "e2e-frps", types.MergePatchType, []byte(`{"spec":{"replicas":1}}`), metav1.PatchOptions{})
	})
	for _, replicas := range []int{0, 1} {
		if _, err := envInstance.K8s.AppsV1().Deployments(ocNS).Patch(ctx, "e2e-frps", types.MergePatchType, []byte(fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)), metav1.PatchOptions{}); err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			waitReady(name, replicas == 1)
		}
	}
	traffic(0)
	traffic(1)
	for _, name := range names {
		out, err := envInstance.Kubectl(ctx, "logs", "-n", ocGamesNS, "deploy/"+name+"-tunnel", "-c", "tunnel")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, ocFrpsToken) || strings.Contains(out, "invalid-fixture-token") {
			t.Fatal("relay log exposed credential material")
		}
	}
}
