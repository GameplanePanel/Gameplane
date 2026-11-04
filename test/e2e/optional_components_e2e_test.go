//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

const (
	ocAdminUser = "ocadmin"
	ocAdminPass = "optional-components-admin-pw-1"
	ocNS        = "gameplane-system"
	ocGamesNS   = "gameplane-games"
	// ocAuditAuth is the shared audit webhook credential: the API sends it as
	// its Authorization header and the bridge requires it verbatim.
	ocAuditSecret = "e2e-audit-webhook-auth"
	ocAuditAuth   = "Bearer e2e-audit-webhook-token-0123456789"
	// ocFrpsToken matches the e2e-frps-token Secret in fixtures/frps.yaml.
	ocFrpsToken     = "e2e-frps-token-0123456789"
	ocFrpsDashUser  = "e2e"
	ocFrpsDashPass  = "e2e-frps-dashboard"
	ocFrpsAddr      = "e2e-frps.gameplane-system.svc.cluster.local"
	ocFrpRemotePort = 25565
)

// ocMCPSession drives the MCP server's stdio transport through kubectl exec.
type ocMCPSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	sc     *bufio.Scanner
	stderr *bytes.Buffer
	cancel context.CancelFunc
}

// ocStartMCP execs /mcp-server serve in the MCP server pod and returns a stdio JSON-RPC session to it.
func ocStartMCP(ctx context.Context, t *testing.T) *ocMCPSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	cmd := exec.CommandContext(ctx, "kubectl", "--context", envInstance.Context, "exec", "-i", "-n", ocNS, "deploy/gameplane-mcp-server", "--", "/mcp-server", "serve")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatalf("mcp stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("mcp stdout pipe: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start mcp session: %v", err)
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	return &ocMCPSession{cmd: cmd, stdin: stdin, sc: sc, stderr: stderr, cancel: cancel}
}

// call sends one JSON-RPC request and returns the result object of the reply
// with the same id. Notifications and other lines are skipped.
func (m *ocMCPSession) call(t *testing.T, id int, method string, params any) map[string]any {
	t.Helper()
	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		t.Fatalf("marshal %s: %v", method, err)
	}
	if _, err := m.stdin.Write(append(line, '\n')); err != nil {
		t.Fatalf("write %s: %v\nstderr: %s", method, err, m.stderr.String())
	}
	for m.sc.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(m.sc.Bytes(), &msg); err != nil {
			continue
		}
		gotID, ok := msg["id"].(float64)
		if !ok || int(gotID) != id {
			continue
		}
		if rpcErr, has := msg["error"]; has {
			t.Fatalf("%s returned error: %v", method, rpcErr)
		}
		result, _ := msg["result"].(map[string]any)
		return result
	}
	t.Fatalf("no reply to %s: scan err=%v\nstderr: %s", method, m.sc.Err(), m.stderr.String())
	return nil
}

// notify sends a JSON-RPC notification (no id, no reply expected).
func (m *ocMCPSession) notify(t *testing.T, method string) {
	t.Helper()
	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		t.Fatalf("marshal notification %s: %v", method, err)
	}
	if _, err := m.stdin.Write(append(line, '\n')); err != nil {
		t.Fatalf("write notification %s: %v\nstderr: %s", method, err, m.stderr.String())
	}
}

// close ends the session: it closes stdin, waits for the exec to exit and cancels its context.
func (m *ocMCPSession) close() {
	_ = m.stdin.Close()
	_ = m.cmd.Wait()
	m.cancel()
}

// ocTextOf concatenates the text parts of a tools/call result.
func ocTextOf(result map[string]any) string {
	var sb strings.Builder
	content, _ := result["content"].([]any)
	for _, c := range content {
		if cm, ok := c.(map[string]any); ok {
			if s, ok := cm["text"].(string); ok {
				sb.WriteString(s)
			}
		}
	}
	return sb.String()
}

// ocTail returns the last n bytes of s (or all of s when shorter).
func ocTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ocSyslogDiagnostics gathers each hop of the audit path for a failure
// message: the API's latest audit rows, the API's webhook log lines, the
// bridge's log and the sink's file.
func ocSyslogDiagnostics(admin *APIClient, sinkOut string) string {
	ctx := context.Background()
	var b strings.Builder
	if resp, body, err := admin.Get("/admin/audit?limit=5"); err != nil {
		fmt.Fprintf(&b, "GET /admin/audit: %v\n", err)
	} else {
		_ = resp.Body.Close()
		fmt.Fprintf(&b, "GET /admin/audit = %d: %s\n", resp.StatusCode, ocTail(string(body), 2000))
	}
	apiLogs, err := envInstance.Kubectl(ctx, "logs", "-n", ocNS, "deploy/gameplane-api", "--tail=300")
	if err != nil {
		fmt.Fprintf(&b, "api logs: %v\n", err)
	}
	for _, line := range strings.Split(apiLogs, "\n") {
		if strings.Contains(line, "audit") {
			fmt.Fprintf(&b, "api: %s\n", line)
		}
	}
	bridgeLogs, err := envInstance.Kubectl(ctx, "logs", "-n", ocNS, "deploy/gameplane-audit-syslog-bridge", "--tail=50")
	if err != nil {
		fmt.Fprintf(&b, "bridge logs: %v\n", err)
	}
	fmt.Fprintf(&b, "bridge log tail:\n%s\n", ocTail(bridgeLogs, 2000))
	fmt.Fprintf(&b, "sink file tail:\n%s", ocTail(sinkOut, 2000))
	return b.String()
}

// ocContainer returns the named container status of pod.
func ocContainer(pod *corev1.Pod, name string) (corev1.ContainerStatus, bool) {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == name {
			return cs, true
		}
	}
	return corev1.ContainerStatus{}, false
}

// ocTunnelReady reports the TunnelReady condition of a GameServer.
func ocTunnelReady(obj *unstructured.Unstructured) (bool, string) {
	conds, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, c := range conds {
		cm, ok := c.(map[string]any)
		if !ok || cm["type"] != "TunnelReady" {
			continue
		}
		if cm["status"] == "True" {
			return true, ""
		}
		return false, fmt.Sprintf("TunnelReady=%v reason=%v message=%v", cm["status"], cm["reason"], cm["message"])
	}
	return false, "no TunnelReady condition yet"
}

// ocFrpsProxyStatus asks the frps dashboard whether the noop proxy is online
// and returns its lastStartTime, which changes each time frpc opens a new
// session for it.
func ocFrpsProxyStatus(ctx context.Context, t *testing.T, port int, proxyName string) (online bool, lastStart, detail string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/proxy/tcp", port), nil)
	if err != nil {
		return false, "", err.Error()
	}
	req.SetBasicAuth(ocFrpsDashUser, ocFrpsDashPass)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, "", err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, "", err.Error()
	}
	var out struct {
		Proxies []struct {
			Name          string `json:"name"`
			Status        string `json:"status"`
			LastStartTime string `json:"lastStartTime"`
		} `json:"proxies"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return false, "", fmt.Sprintf("decode frps proxies: %v body=%s", err, body)
	}
	for _, p := range out.Proxies {
		if p.Name == proxyName && p.Status == "online" {
			return true, p.LastStartTime, string(body)
		}
	}
	return false, "", string(body)
}

// TestOptionalComponents_Lifecycle walks spec 013 US3 (research R7) on the
// bucket's own cluster: the three optional components are absent by default,
// a `helm upgrade --reuse-values` enables the MCP server and the audit-syslog
// bridge, then the MCP server answers over stdio, audit events reach a syslog
// collector, an frp tunnel stays up when frpc dies, and core features still
// work. It is the bucket's only test because it runs `helm upgrade`. Admin
// logins: one, plus one restricted-user login.
func TestOptionalComponents_Lifecycle(t *testing.T) {
	t.Parallel()
	parent := t

	suffix := time.Now().UnixNano()
	tmplName := fmt.Sprintf("e2e-oc-tmpl-%d", suffix)
	gsName := fmt.Sprintf("e2e-oc-gs-%d", suffix)
	tunnelName := fmt.Sprintf("e2e-oc-tunnel-%d", suffix)
	secName := fmt.Sprintf("e2e-oc-frp-%d", suffix)
	roleName := fmt.Sprintf("e2e-oc-role-%d", suffix)

	var admin *APIClient

	if !t.Run("disabled", func(t *testing.T) {
		// A fresh context per subtest: contextcheck flags a captured one.
		ctx := context.Background()
		_, err := envInstance.K8s.AppsV1().Deployments(ocNS).Get(ctx, "gameplane-mcp-server", metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Fatalf("mcp server deployment: want NotFound, got err=%v", err)
		}
		_, err = envInstance.K8s.AppsV1().Deployments(ocNS).Get(ctx, "gameplane-audit-syslog-bridge", metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Fatalf("audit-syslog-bridge deployment: want NotFound, got err=%v", err)
		}
		_, err = envInstance.K8s.CoreV1().Services(ocNS).Get(ctx, "gameplane-audit-syslog-bridge", metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Fatalf("audit-syslog-bridge service: want NotFound, got err=%v", err)
		}
		tunnels, err := envInstance.K8s.AppsV1().Deployments(ocGamesNS).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-tunnel"})
		if err != nil {
			t.Fatalf("list tunnel deployments: %v", err)
		}
		if len(tunnels.Items) != 0 {
			t.Fatalf("want no tunnel deployments by default, got %d", len(tunnels.Items))
		}
	}) {
		return
	}

	if !t.Run("enable", func(t *testing.T) {
		// A fresh context per subtest: contextcheck flags a captured one.
		ctx := context.Background()
		envInstance.ApplyYAML(t, "syslog-sink.yaml")
		envInstance.ApplyYAML(t, "frps.yaml")
		parent.Cleanup(func() {
			for _, name := range []string{"syslog-sink.yaml", "frps.yaml"} {
				abs, err := filepath.Abs(filepath.Join("fixtures", name))
				if err != nil {
					continue
				}
				cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Minute)
				_, _ = envInstance.Kubectl(cctx, "delete", "-f", abs, "--ignore-not-found")
				ccancel()
			}
		})

		_, err := envInstance.K8s.CoreV1().Secrets(ocNS).Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: ocAuditSecret, Namespace: ocNS},
			StringData: map[string]string{"token": ocAuditAuth},
		}, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("create audit webhook secret: %v", err)
		}

		helmUpgradeReuse(ctx, t,
			"mcpServer.enabled=true",
			"api.audit.webhook.syslogBridge.enabled=true",
			"api.audit.webhook.syslogBridge.syslog.addr=e2e-syslog-sink:514",
			"api.audit.webhook.authSecretRef.name="+ocAuditSecret,
		)

		ready := func(ns, name string) func() (bool, string) {
			return func() (bool, string) {
				d, err := envInstance.K8s.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					return false, err.Error()
				}
				return d.Status.ReadyReplicas >= 1, fmt.Sprintf("%s readyReplicas=%d", name, d.Status.ReadyReplicas)
			}
		}
		for _, name := range []string{"e2e-syslog-sink", "e2e-frps", "gameplane-mcp-server", "gameplane-audit-syslog-bridge"} {
			envInstance.Eventually(t, 3*time.Minute, ready(ocNS, name))
		}

		applyBusyboxTemplate(parent, tmplName)
		applyBusyboxGameServer(parent, ocGamesNS, gsName, tmplName)
	}) {
		return
	}

	if !t.Run("mcp", func(t *testing.T) {
		// A fresh context per subtest: contextcheck flags a captured one.
		ctx := context.Background()
		m := ocStartMCP(ctx, t)
		defer m.close()

		initRes := m.call(t, 1, "initialize", map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "gameplane-e2e", "version": "1"},
		})
		serverInfo, _ := initRes["serverInfo"].(map[string]any)
		if name, _ := serverInfo["name"].(string); name == "" {
			t.Fatalf("initialize: empty serverInfo.name in %v", initRes)
		}
		m.notify(t, "notifications/initialized")

		list := m.call(t, 2, "tools/list", map[string]any{})
		tools, _ := list["tools"].([]any)
		var names []string
		for _, tool := range tools {
			if tm, ok := tool.(map[string]any); ok {
				if name, ok := tm["name"].(string); ok {
					names = append(names, name)
				}
			}
		}
		sort.Strings(names)
		// Seven tools: the six read tools from research R7 plus propose_fix,
		// which mcp-server/tools.go also registers and which only returns
		// suggestion text.
		want := []string{
			"get_gameplane_resource", "get_pod", "get_pod_logs", "list_events",
			"list_gameplane_resources", "list_pods", "propose_fix",
		}
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Fatalf("tools/list: got %v, want %v", names, want)
		}

		listRes := m.call(t, 3, "tools/call", map[string]any{
			"name":      "list_gameplane_resources",
			"arguments": map[string]any{"kind": "GameServer", "namespace": ocGamesNS},
		})
		if isErr, _ := listRes["isError"].(bool); isErr {
			t.Fatalf("list_gameplane_resources returned isError: %s", ocTextOf(listRes))
		}
		if text := ocTextOf(listRes); !strings.Contains(text, gsName) {
			t.Fatalf("list_gameplane_resources: %s not in result %q", gsName, text)
		}

		// Cluster is outside the server's ClusterRole and kind list, so this
		// must be a tool error result, not data.
		badRes := m.call(t, 4, "tools/call", map[string]any{
			"name":      "get_gameplane_resource",
			"arguments": map[string]any{"kind": "clusters", "namespace": ocNS, "name": "any"},
		})
		if isErr, _ := badRes["isError"].(bool); !isErr {
			t.Fatalf("get_gameplane_resource clusters: want isError=true, got %v", badRes)
		}
	}) {
		return
	}

	if !t.Run("syslog", func(t *testing.T) {
		// A fresh context per subtest: contextcheck flags a captured one.
		ctx := context.Background()
		envInstance.BootstrapAdmin(t, ocAdminUser, ocAdminPass)
		admin = envInstance.APIClient(parent, ocAdminUser, ocAdminPass)
		parent.Cleanup(func() { admin.Close() })

		resp, _, err := admin.Post("/roles", map[string]any{
			"name":        roleName,
			"description": "e2e optional components",
			"permissions": []string{"servers:read"},
		})
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			t.Fatalf("create role: %v", err)
		}
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create role: want 201, got %d", resp.StatusCode)
		}

		resp, _, err = admin.Delete("/roles/" + roleName)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			t.Fatalf("delete role: %v", err)
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			t.Fatalf("delete role: want 2xx, got %d", resp.StatusCode)
		}

		// Poll the sink by hand rather than with Eventually so a timeout can
		// report every hop: the API's audit trail and webhook log lines, the
		// bridge's log and the sink's file.
		var sinkOut string
		deadline := time.Now().Add(2 * time.Minute)
		for {
			out, err := envInstance.Kubectl(ctx, "exec", "-n", ocNS, "deploy/e2e-syslog-sink", "--", "cat", "/tmp/syslog.log")
			sinkOut = out
			if err == nil && strings.Contains(out, "gameplane-audit") && strings.Contains(out, "/roles/"+roleName) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no RFC 5424 record for /roles/%s reached the syslog sink within 2m\n%s",
					roleName, ocSyslogDiagnostics(admin, sinkOut))
			}
			time.Sleep(3 * time.Second)
		}

		port, stop := detachedPortForward(ctx, t, ocNS, "svc/gameplane-audit-syslog-bridge", 8514)
		defer stop()
		client := &http.Client{Timeout: 15 * time.Second}
		bridgeReq, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/", port), strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("build unauthenticated bridge request: %v", err)
		}
		bridgeReq.Header.Set("Content-Type", "application/json")
		bridgeResp, err := client.Do(bridgeReq)
		if err != nil {
			t.Fatalf("unauthenticated POST to bridge: %v", err)
		}
		_ = bridgeResp.Body.Close()
		if bridgeResp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated POST to bridge: want 401, got %d", bridgeResp.StatusCode)
		}
	}) {
		return
	}

	if !t.Run("tunnel-frp", func(t *testing.T) {
		// A fresh context per subtest: contextcheck flags a captured one.
		ctx := context.Background()
		tunnel := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1",
			"kind":       "GameServer",
			"metadata": map[string]any{
				"name":      tunnelName,
				"namespace": ocGamesNS,
			},
			// Disabled first: the operator only mounts a credentials Secret
			// that the GameServer owns. Creating the owned Secret before
			// enabling keeps the tunnel pod from ever starting without a token.
			"spec": map[string]any{
				"templateRef": map[string]any{"name": tmplName},
				"networking": map[string]any{
					"tunnel": map[string]any{
						"enabled":  false,
						"provider": "frp",
						"frp": map[string]any{
							"serverAddr": ocFrpsAddr,
							"remotePorts": []any{
								map[string]any{"name": "noop", "remotePort": int64(ocFrpRemotePort)},
							},
						},
					},
				},
			},
		}}
		gsClient := envInstance.Dyn.Resource(gameServerGVR).Namespace(ocGamesNS)
		created, err := gsClient.Create(ctx, tunnel, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("create tunnel GameServer: %v", err)
		}
		parent.Cleanup(func() {
			_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(ocGamesNS).Delete(context.Background(), tunnelName, metav1.DeleteOptions{})
		})
		uid := string(created.GetUID())

		owned := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secName,
				Namespace: ocGamesNS,
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "gameplane.local/v1alpha1",
					Kind:       "GameServer",
					Name:       tunnelName,
					UID:        types.UID(uid),
				}},
			},
			StringData: map[string]string{"token": ocFrpsToken},
		}
		if _, err := envInstance.K8s.CoreV1().Secrets(ocGamesNS).Create(ctx, owned, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create owned frp credentials secret: %v", err)
		}

		patch, err := json.Marshal(map[string]any{
			"spec": map[string]any{
				"networking": map[string]any{
					"tunnel": map[string]any{
						"enabled":              true,
						"credentialsSecretRef": map[string]any{"name": secName},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("marshal tunnel patch: %v", err)
		}
		if _, err := gsClient.Patch(ctx, tunnelName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			t.Fatalf("enable tunnel: %v", err)
		}

		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			cur, err := gsClient.Get(ctx, tunnelName, metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			return ocTunnelReady(cur)
		})

		dashPort, stopDash := detachedPortForward(ctx, t, ocNS, "svc/e2e-frps", 7500)
		defer stopDash()
		// lastStartTime of the frpc session before the kill; recovery must
		// show a different one, proving frpc opened a new session.
		var firstStart string
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			online, start, detail := ocFrpsProxyStatus(ctx, t, dashPort, uid+".noop")
			if !online || start == "" {
				return false, detail
			}
			firstStart = start
			return true, ""
		})

		var podName, podUID string
		tunnelSel := "app.kubernetes.io/name=gameplane-tunnel,app.kubernetes.io/instance=" + tunnelName
		envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
			list, err := envInstance.K8s.CoreV1().Pods(ocGamesNS).List(ctx, metav1.ListOptions{LabelSelector: tunnelSel})
			if err != nil {
				return false, err.Error()
			}
			if len(list.Items) != 1 {
				return false, fmt.Sprintf("%d tunnel pods, want 1", len(list.Items))
			}
			pod := &list.Items[0]
			cs, ok := ocContainer(pod, "tunnel")
			if pod.Status.Phase == corev1.PodRunning && ok && cs.Ready {
				podName, podUID = pod.Name, string(pod.UID)
				return true, ""
			}
			return false, fmt.Sprintf("pod %s phase=%s", pod.Name, pod.Status.Phase)
		})

		// stillSame reports whether the same pod exists with the tunnel
		// container never restarted.
		stillSame := func() (bool, string) {
			pod, err := envInstance.K8s.CoreV1().Pods(ocGamesNS).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			cs, ok := ocContainer(pod, "tunnel")
			if !ok {
				return false, "no tunnel container status"
			}
			if string(pod.UID) != podUID {
				return false, fmt.Sprintf("pod UID changed to %s", pod.UID)
			}
			return cs.RestartCount == 0, fmt.Sprintf("tunnel restartCount=%d", cs.RestartCount)
		}
		envInstance.Consistently(t, 60*time.Second, 5*time.Second, stillSame)

		// The tunnel image is distroless, so kill frpc from an ephemeral debug
		// container sharing the tunnel's process namespace. 65532 is the uid
		// frpc runs as; the pod enforces runAsNonRoot.
		debugFile := filepath.Join(t.TempDir(), "debug.json")
		if err := os.WriteFile(debugFile, []byte(`{"securityContext":{"runAsUser":65532,"runAsNonRoot":true}}`), 0o600); err != nil {
			t.Fatalf("write debug profile: %v", err)
		}
		// The script prints who it runs as and the process table, so a
		// failed kill shows whether frpc was visible at all.
		const killScript = `id; ps -o pid,user,comm,args; pids=$(pidof frpc); echo "frpc pids: $pids"; [ -n "$pids" ] && kill -9 $pids`
		debugName := fmt.Sprintf("e2e-kill-frpc-%d", time.Now().UnixNano()%100000)
		if out, err := envInstance.Kubectl(ctx, "debug", "-n", ocGamesNS, "pod/"+podName,
			"--image=busybox:1.36", "--target=tunnel", "--profile=general",
			"--container="+debugName, "--custom="+debugFile,
			"--", "sh", "-c", killScript); err != nil {
			t.Fatalf("kill frpc via debug container: %v\n%s", err, out)
		}
		var exitCode int32
		envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
			pod, err := envInstance.K8s.CoreV1().Pods(ocGamesNS).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			for _, cs := range pod.Status.EphemeralContainerStatuses {
				if cs.Name != debugName {
					continue
				}
				if cs.State.Terminated != nil {
					exitCode = cs.State.Terminated.ExitCode
					return true, ""
				}
				return false, fmt.Sprintf("debug container %s state: %+v", debugName, cs.State)
			}
			return false, fmt.Sprintf("debug container %s has no status yet", debugName)
		})
		if exitCode != 0 {
			out, _ := envInstance.Kubectl(ctx, "logs", "-n", ocGamesNS, "pod/"+podName, "-c", debugName)
			t.Fatalf("kill frpc via debug container exited %d:\n%s", exitCode, out)
		}

		envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
			out, err := envInstance.Kubectl(ctx, "logs", "-n", ocGamesNS, "pod/"+podName, "-c", "tunnel")
			if err != nil {
				return false, err.Error()
			}
			return strings.Contains(out, "relay process exited"), "tunnel log tail: " + ocTail(out, 2000)
		})

		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			online, start, detail := ocFrpsProxyStatus(ctx, t, dashPort, uid+".noop")
			if !online || start == "" || start == firstStart {
				return false, fmt.Sprintf("want a new session (lastStartTime != %q): %s", firstStart, detail)
			}
			return true, ""
		})

		// frpc was restarted inside the container, not by the kubelet.
		envInstance.Eventually(t, 1*time.Minute, stillSame)
	}) {
		return
	}

	if !t.Run("frp-identity-health", testFrpIdentityHealth) {
		return
	}

	t.Run("core-unaffected", func(t *testing.T) {
		// A fresh context per subtest: contextcheck flags a captured one.
		ctx := context.Background()
		if admin == nil {
			t.Fatal("admin client not initialised by the syslog subtest")
		}
		requireAgentReady(t, ocGamesNS, gsName)
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			cur, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ocGamesNS).Get(ctx, gsName, metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			phase, _, _ := unstructured.NestedString(cur.Object, "status", "phase")
			heartbeat, _, _ := unstructured.NestedString(cur.Object, "status", "agent", "lastHeartbeat")
			return phase == "Running" && heartbeat != "", fmt.Sprintf("phase=%q lastHeartbeat=%q", phase, heartbeat)
		})

		viewer, viewerPW, _ := envInstance.CreateUser(t, admin, "viewer", "e2e-oc-viewer")
		vc := envInstance.APIClient(t, viewer, viewerPW)
		defer vc.Close()

		resp, _, err := vc.Get("/admin/audit?limit=1")
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			t.Fatalf("restricted user GET /admin/audit: %v", err)
		}
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("restricted user GET /admin/audit: want 403, got %d", resp.StatusCode)
		}
	})
}
