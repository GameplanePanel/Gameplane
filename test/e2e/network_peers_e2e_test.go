//go:build e2e

package e2e

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// The wrapper discovers both endpoints on kind's private IPv4 bridge. Render
// the parsed address before passing it to Helm, kubectl or Docker.
func kindFixtureIP(t *testing.T, key string) string {
	t.Helper()
	ip, err := netip.ParseAddr(os.Getenv(key))
	if err != nil || !ip.Is4() || !ip.IsPrivate() {
		t.Fatalf("%s must identify a private IPv4 address on the kind bridge", key)
	}
	return ip.String()
}

func checkTenantEndpoints(t *testing.T, ns, tenant, server, localPeer, homeIP, awayIP, peerIP string, profileApplied time.Time) {
	t.Helper()
	monitor := isolationClient(t, "monitoring", server+"-monitor", nil)
	const fetchPeer = `wget --no-check-certificate -q -T 3 -O - "$1" | grep -Fxq isolated-kind-peer`
	const scrape = `wget -q -T 3 -O - "$1" | grep -q '^gameplane_agent_'`
	const connect = `nc -w 3 "$1" "$2" </dev/null`
	// Require responses from live endpoints before interpreting tenant timeouts.
	// The link-local destination is a stand-in on the disposable kind bridge.
	urls := []string{"https://" + peerIP + "/", "http://169.254.169.253/latest/meta-data/"}
	for _, url := range urls {
		isolationAttempt(t, "monitoring", monitor, true, fetchPeer, url)
	}
	for _, dest := range []struct{ ns, ip string }{{ns, homeIP}, {tenant, awayIP}} {
		namespace, err := envInstance.K8s.CoreV1().Namespaces().Get(t.Context(), dest.ns, metav1.GetOptions{})
		if err != nil || namespace.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
			t.Fatalf("tenant namespace %s requires restricted PSA: %v", dest.ns, err)
		}
		envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
			game, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(dest.ns).Get(t.Context(), server, metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			raw, _, _ := unstructured.NestedString(game.Object, "status", "agent", "lastHeartbeat")
			beat, err := time.Parse(time.RFC3339, raw)
			return err == nil && beat.After(profileApplied), "waiting for heartbeat after tenant policies took effect"
		})
		pod := server + "-0"
		isolationAttempt(t, dest.ns, pod, true, connect, "127.0.0.1", "8090")
		isolationAttempt(t, "monitoring", monitor, true, scrape, "http://"+dest.ip+":9090/metrics")
		for _, url := range urls {
			isolationAttempt(t, dest.ns, pod, false, fetchPeer, url)
		}
		isolationAttempt(t, dest.ns, pod, false, connect, peerIP, "443")
		isolationAttempt(t, dest.ns, pod, false, connect, "169.254.169.253", "80")
	}
	for _, dest := range []string{homeIP, server + "-agent." + ns + ".svc"} {
		isolationAttempt(t, ns, localPeer+"-0", false, connect, dest, "8090")
		isolationAttempt(t, tenant, server+"-0", false, connect, dest, "8090")
	}
	isolationAttempt(t, tenant, server+"-0", false, connect, homeIP, "9090")
	isolationAttempt(t, ns, localPeer+"-0", false, connect, homeIP, "25575")
	isolationAttempt(t, tenant, server+"-0", false, connect, homeIP, "25575")
	// Use the additional tenant explicitly: both the operator's Job labels and
	// the chart policy for this namespace must match for restic to write data.
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: server + "-repo"}, StringData: map[string]string{
		"repo": "rest:http://" + peerIP + ":8000/world/", "password": "kind-disposable-repository",
	}}
	if _, err := envInstance.K8s.CoreV1().Secrets(tenant).Create(t.Context(), secret, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	createBackup(t, tenant, server+"-copy", server, secret.Name, "repo")
	waitBackupSucceeded(t, tenant, server+"-copy", 5*time.Minute)
	isolationAttempt(t, tenant, server+"-0", false, connect, peerIP, "8000")
	backupPeer := isolationClient(t, tenant, server+"-backup-peer", map[string]string{"app.kubernetes.io/name": "gameplane-backup-restore"})
	isolationAttempt(t, tenant, backupPeer, true, connect, peerIP, "8000")
	isolationAttempt(t, tenant, backupPeer, false, connect, peerIP, "443")
	checkExternalPlayer(t, ns, server)
}

func checkExternalPlayer(t *testing.T, ns, server string) {
	t.Helper()
	// Keep the external source IP across kube-proxy's NodePort translation.
	// The same operator-created game pod receives both the direct and relayed request.
	game, err := envInstance.K8s.CoreV1().Services(ns).Get(t.Context(), server, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	port := intstr.FromInt32(25566)
	svc, err := envInstance.K8s.CoreV1().Services(ns).Create(t.Context(), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: server + "-player"},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyLocal,
			Selector: game.Spec.Selector, Ports: []corev1.ServicePort{{Name: "game", Port: 25566, TargetPort: port}}},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().Services(ns).Delete(context.Background(), svc.Name, metav1.DeleteOptions{})
	})
	node := kindFixtureIP(t, "GAMEPLANE_NET_NODE_IP")
	id, err := hex.DecodeString(os.Getenv("GAMEPLANE_NET_PEER_CONTAINER"))
	if err != nil || len(id) != 32 {
		t.Fatal("expected the full container ID from the kind network fixture")
	}
	client := hex.EncodeToString(id)
	url := fmt.Sprintf("http://%s:%d/", node, svc.Spec.Ports[0].NodePort)
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		out, err := exec.CommandContext(t.Context(), "docker", "exec", client, "wget", "-T", "3", "-qO-", url).CombinedOutput()
		return err == nil && strings.TrimSpace(string(out)) == "reachable", fmt.Sprintf("external player -> %s: %v %s", url, err, out)
	})
}

func enableIsolatedFRP(t *testing.T, ns, server string) {
	t.Helper()
	ctx := t.Context()
	envInstance.ApplyYAML(t, "frps.yaml")
	game, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, server, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	auth := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: server + "-frp", OwnerReferences: []metav1.OwnerReference{
		{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: server, UID: game.GetUID()},
	}}, StringData: map[string]string{"token": ocFrpsToken}}
	if _, err := envInstance.K8s.CoreV1().Secrets(ns).Create(ctx, auth, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	// This is the administrator-supplied provider exception documented for
	// tenant isolation. Cilium evaluates the selected frps backend after DNAT.
	port := intstr.FromInt32(7000)
	policy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: server + "-frp-provider"},
		Spec: networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
			"app.kubernetes.io/name": "gameplane-tunnel", "app.kubernetes.io/instance": server}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ocNS}},
				PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "e2e-frps"}},
			}}, Ports: []networkingv1.NetworkPolicyPort{{Port: &port, Protocol: ptr.To(corev1.ProtocolTCP)}}}},
		}}
	if _, err := envInstance.K8s.NetworkingV1().NetworkPolicies(ns).Create(ctx, policy, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = envInstance.K8s.NetworkingV1().NetworkPolicies(ns).Delete(context.Background(), policy.Name, metav1.DeleteOptions{})
	})
	patch := []byte(fmt.Sprintf(`{"spec":{"networking":{"tunnel":{"enabled":true,"provider":"frp","credentialsSecretRef":{"name":%q},"frp":{"serverAddr":%q,"remotePorts":[{"name":"game","remotePort":30000}]}}}}}`, auth.Name, ocFrpsAddr))
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Patch(ctx, server, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	out, err := envInstance.Kubectl(ctx, "rollout", "status", "-n", ocNS, "deployment/e2e-frps", "--timeout=2m")
	if err != nil {
		t.Fatalf("frps: %v %s", err, out)
	}
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		got, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(ctx, server, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		return ocTunnelReady(got)
	})
	peers, err := envInstance.K8s.CoreV1().Pods(ocNS).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=e2e-frps"})
	if err != nil || len(peers.Items) != 1 {
		t.Fatalf("frps pods: %v %v", peers, err)
	}
	control := isolationClient(t, "monitoring", server+"-frp-player", nil)
	isolationAttempt(t, "monitoring", control, true, `wget -q -T 3 -O - "$1" | grep -Fxq reachable`, "http://"+peers.Items[0].Status.PodIP+":30000/")
}
