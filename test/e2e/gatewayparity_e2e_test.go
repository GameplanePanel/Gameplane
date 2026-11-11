//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const gatewayTestPeer = "spiffe://gameplane.e2e/central-api"

type gatewayTestPKI struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

type gatewayTestLeaf struct {
	cert, key []byte
	tls       tls.Certificate
}

type gatewayParity struct {
	remote                    *Env
	admin, client             *APIClient
	cluster, name, template   string
	serverSecret, trustSecret string
	clientSecret              string
	endpoint, nodeIP          string
	serverCA, clientCA        gatewayTestPKI
	clientLeaf                gatewayTestLeaf
	localUID, remoteUID       string
	service                   *corev1.Service
}

// This test is deliberately not parallel: it changes the remote Helm release
// and rotates mounted gateway trust. Other multicluster tests run afterwards.
// CI requires both clusters; a missing gateway must never become a green skip.
func TestMultiCluster_GatewayParity(t *testing.T) {
	if os.Getenv("GAMEPLANE_E2E_REQUIRE_GATEWAY") != "1" {
		t.Skip("requires the multicluster job's gateway acceptance opt-in")
	}
	remote, err := newEnvForContext("kind-" + clusterBKindName())
	if err != nil {
		t.Fatalf("required remote cluster configuration: %v", err)
	}
	if err := remote.ensureCluster(); err != nil {
		t.Fatalf("required remote cluster is unavailable: %v", err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	p := &gatewayParity{remote: remote, cluster: "e2e-gw-" + suffix, name: "e2e-gw-" + suffix,
		template: "e2e-gw-tmpl-" + suffix, serverSecret: "e2e-gw-server-" + suffix,
		trustSecret: "e2e-gw-trust-" + suffix, clientSecret: "e2e-gw-client-" + suffix}
	p.install(t)
	p.register(t)
	p.fixtures(t)
	t.Run("files-mods-and-selected-CRs", p.operations)
	t.Run("capture-files-and-identity", p.capture)
	t.Run("real-Minecraft-RCON-players-and-status", p.minecraft)
	t.Run("authenticated-denials", p.denials)
	t.Run("rotation-revocation-and-endpoint-replacement", p.rotation)
}

func newGatewayTestCA(t *testing.T) gatewayTestPKI {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "ephemeral gateway e2e CA"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(2 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return gatewayTestPKI{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca gatewayTestPKI) issue(t *testing.T, identity, ip string) gatewayTestLeaf {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "ephemeral gateway e2e leaf"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	if identity != "" {
		u, err := url.Parse(identity)
		if err != nil {
			t.Fatal(err)
		}
		cert.URIs, cert.ExtKeyUsage = []*url.URL{u}, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		cert.IPAddresses, cert.ExtKeyUsage = []net.IP{net.ParseIP(ip)}, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	leaf := gatewayTestLeaf{cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private})}
	leaf.tls, err = tls.X509KeyPair(leaf.cert, leaf.key)
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

func gatewayNodeIP(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "docker", "inspect", "-f",
		`{{(index .NetworkSettings.Networks "kind").IPAddress}}`, name+"-control-plane").Output()
	if err != nil {
		t.Fatalf("inspect required kind node: %v", err)
	}
	ip := strings.TrimSpace(string(out))
	if net.ParseIP(ip) == nil {
		t.Fatal("kind node has no valid private-network IP")
	}
	return ip
}

func gatewayBridgeIP(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "docker", "inspect", "-f",
		`{{(index .NetworkSettings.Networks "kind").Gateway}}`, name+"-control-plane").Output()
	if err != nil {
		t.Fatalf("inspect required kind bridge: %v", err)
	}
	address := strings.TrimSpace(string(out))
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() == nil || !ip.IsPrivate() {
		t.Fatal("kind bridge has no valid private IPv4 address")
	}
	return address
}

func gatewaySecret(t *testing.T, env *Env, namespace, name string, data map[string][]byte, create bool) {
	t.Helper()
	secrets := env.K8s.CoreV1().Secrets(namespace)
	if create {
		_, err := secrets.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name,
			Namespace: namespace, Labels: map[string]string{"gameplane.local/agent-gateway-credentials": "true"}}, Data: data}, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("create ephemeral credential Secret: %v", err)
		}
		t.Cleanup(func() { _ = secrets.Delete(context.Background(), name, metav1.DeleteOptions{}) })
		return
	}
	secret, err := secrets.Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Data = data
	if _, err := secrets.Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("rotate ephemeral credential Secret: %v", err)
	}
}

func (p *gatewayParity) install(t *testing.T) {
	t.Helper()
	p.nodeIP = gatewayNodeIP(t, clusterBKindName())
	p.serverCA, p.clientCA = newGatewayTestCA(t), newGatewayTestCA(t)
	server := p.serverCA.issue(t, "", p.nodeIP)
	p.clientLeaf = p.clientCA.issue(t, gatewayTestPeer, "")
	gatewaySecret(t, p.remote, "gameplane-system", p.serverSecret, map[string][]byte{"tls.crt": server.cert, "tls.key": server.key}, true)
	gatewaySecret(t, p.remote, "gameplane-system", p.trustSecret, map[string][]byte{"ca.crt": p.clientCA.pem}, true)
	apiService, err := p.remote.K8s.CoreV1().Services("default").Get(t.Context(), "kubernetes", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{"gateway": map[string]any{"enabled": true, "clusterID": p.cluster,
		"peerURI": gatewayTestPeer, "namespaces": []string{"gameplane-games"}, "serverTLSSecret": p.serverSecret,
		"centralClientCASecret": p.trustSecret, "maxRequestDuration": "10s",
		"networkPolicy": map[string]any{"peerCIDRs": []string{
			gatewayNodeIP(t, envInstance.ClusterName) + "/32", // Central API pods through their node.
			gatewayBridgeIP(t, clusterBKindName()) + "/32",    // Direct TLS controls from the Linux host.
		},
			"apiServerCIDRs": []string{p.nodeIP + "/32", apiService.Spec.ClusterIP + "/32"}}}}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "gateway-values.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "helm", "upgrade", "gameplane", "../../charts/gameplane", "--kube-context", p.remote.Context,
		"--namespace", "gameplane-system", "--reuse-values", "--values", path, "--wait", "--timeout", "5m")
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("install checked-out gateway chart: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cleanupCancel()
		command := exec.CommandContext(cleanupCtx, "helm", "upgrade", "gameplane", "../../charts/gameplane", "--kube-context", p.remote.Context,
			"--namespace", "gameplane-system", "--reuse-values", "--set", "gateway.enabled=false", "--wait", "--timeout", "2m")
		if out, err := command.CombinedOutput(); err != nil {
			t.Errorf("remove temporary gateway chart deployment: %v\n%s", err, out)
		}
	})
	// The chart remains ClusterIP-only. This separate, temporary service is
	// reachable only through Kind's Docker bridge, like the registered API.
	p.service = p.newService(t, p.name)
	p.endpoint = fmt.Sprintf("https://%s:%d", p.nodeIP, p.service.Spec.Ports[0].NodePort)
	// Direct controls use the same NodePort as the registered gateway. A TLS
	// rejection must not tear down a shared kubectl port-forward and turn later
	// authentication checks into unrelated connection-refused failures.
	client := p.directClient(t, p.clientLeaf, p.nodeIP)
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.endpoint+"/v1/capabilities", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("actual gateway chart TLS listener: %v", err)
	}
	defer resp.Body.Close()
	var caps struct {
		Protocol       string `json:"protocol"`
		TargetUID      bool   `json:"targetUID"`
		CaptureFiles   bool   `json:"captureFiles"`
		CaptureEnabled bool   `json:"captureEnabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil || resp.StatusCode != http.StatusOK ||
		caps.Protocol != "v1" || !caps.TargetUID || !caps.CaptureFiles || !caps.CaptureEnabled {
		t.Fatalf("deployed gateway capability contract: status=%d caps=%+v error=%v", resp.StatusCode, caps, err)
	}
}

func (p *gatewayParity) newService(t *testing.T, name string) *corev1.Service {
	t.Helper()
	service, err := p.remote.K8s.CoreV1().Services("gameplane-system").Create(t.Context(), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "gameplane-system"},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeNodePort, Selector: map[string]string{
			"app.kubernetes.io/name": "gameplane-gateway", "app.kubernetes.io/instance": "gameplane"},
			Ports: []corev1.ServicePort{{Name: "mtls", Port: 8443, TargetPort: intstr.FromInt32(8443)}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = p.remote.K8s.CoreV1().Services("gameplane-system").Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	// Helm waited for the gateway Deployment before this Service existed.
	// Its new EndpointSlice and kube-proxy route still need to converge. Wait
	// only for TCP acceptance; TLS identity and capabilities are asserted once
	// by the caller, without retrying authentication or protocol failures.
	address := fmt.Sprintf("%s:%d", p.nodeIP, service.Spec.Ports[0].NodePort)
	dialer := net.Dialer{Timeout: time.Second}
	p.remote.Eventually(t, 30*time.Second, func() (bool, string) {
		conn, err := dialer.DialContext(t.Context(), "tcp", address)
		if err != nil {
			if !errors.Is(err, syscall.ECONNREFUSED) {
				t.Fatalf("new gateway Service TCP readiness: %v", err)
			}
			return false, "new gateway NodePort is not accepting connections yet"
		}
		_ = conn.Close()
		return true, ""
	})
	return service
}

func (p *gatewayParity) directClient(t *testing.T, leaf gatewayTestLeaf, serverName string) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(p.serverCA.pem) {
		t.Fatal("test server CA is invalid")
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: serverName}
	if len(leaf.cert) != 0 {
		config.Certificates = []tls.Certificate{leaf.tls}
	}
	transport := &http.Transport{TLSClientConfig: config, DisableKeepAlives: true}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}
}

func (p *gatewayParity) register(t *testing.T) {
	t.Helper()
	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	p.admin = envInstance.APIClient(t, adminUsername, adminPassword)
	t.Cleanup(p.admin.Close)
	p.expect(t, p.admin, http.MethodPost, "/clusters", map[string]string{"name": p.cluster,
		"displayName": "Gateway parity test", "kubeconfig": string(podReachableKubeconfig(t, clusterBKindName()))}, http.StatusCreated)
	t.Cleanup(func() {
		response, _, _ := p.admin.Delete("/clusters/" + p.cluster)
		if response != nil {
			response.Body.Close()
		}
	})
	gatewaySecret(t, envInstance, credentialsNS, p.clientSecret, p.credentials(p.clientLeaf), true)
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"agentGateway": map[string]any{
		"url": p.endpoint, "tlsSecretRef": map[string]string{"name": p.clientSecret}}}})
	if _, err := envInstance.Dyn.Resource(clusterGVR).Patch(t.Context(), p.cluster, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(clusterGVR).Get(t.Context(), p.cluster, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		return phase == "Healthy", "remote Kubernetes phase=" + phase
	})
	user, password, id := envInstance.CreateUser(t, p.admin, "viewer", "e2e-gateway-writer")
	t.Cleanup(func() {
		response, _, _ := p.admin.Delete("/users/" + id)
		if response != nil {
			response.Body.Close()
		}
	})
	p.expect(t, p.admin, http.MethodPost, "/users/"+id+"/bindings", map[string]string{
		"roleName": "admin", "cluster": p.cluster, "namespace": "gameplane-games"}, http.StatusCreated)
	p.client = envInstance.APIClient(t, user, password)
	t.Cleanup(p.client.Close)
}

func (p *gatewayParity) credentials(leaf gatewayTestLeaf) map[string][]byte {
	return map[string][]byte{"tls.crt": leaf.cert, "tls.key": leaf.key, "ca.crt": p.serverCA.pem}
}

func (p *gatewayParity) route(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return "/servers/" + p.name + path + sep + "cluster=" + p.cluster + "&namespace=gameplane-games"
}

func (p *gatewayParity) expect(t *testing.T, client *APIClient, method, path string, body any, want int) []byte {
	t.Helper()
	response, raw, err := client.Do(method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, response.StatusCode, want, raw)
	}
	return raw
}

func (p *gatewayParity) createFixture(t *testing.T, env *Env, name, template string, spec map[string]any) *unstructured.Unstructured {
	t.Helper()
	tmpl := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "GameTemplate",
		"metadata": map[string]any{"name": template}, "spec": spec}}
	if _, err := env.Dyn.Resource(gameTemplateGVR).Create(t.Context(), tmpl, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = env.Dyn.Resource(gameTemplateGVR).Delete(context.Background(), template, metav1.DeleteOptions{})
	})
	gs, err := env.Dyn.Resource(gameServerGVR).Namespace("gameplane-games").Create(t.Context(), &unstructured.Unstructured{
		Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer",
			"metadata": map[string]any{"name": name, "namespace": "gameplane-games"},
			"spec":     map[string]any{"templateRef": map[string]any{"name": template}}}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = env.Dyn.Resource(gameServerGVR).Namespace("gameplane-games").Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	return gs
}

func (p *gatewayParity) fixtures(t *testing.T) {
	t.Helper()
	for _, site := range []struct {
		env    *Env
		marker string
	}{{envInstance, "local"}, {p.remote, "remote"}} {
		spec := map[string]any{"displayName": "Gateway parity " + site.marker, "game": "busybox", "version": "1", "image": "busybox:1.36",
			"command": []any{"sh", "-c", "echo SITE:" + site.marker + " >/data/site.txt; while true; do echo LIVE:" + site.marker + " | tee -a /data/game.log; sleep 1; done"},
			"logPath": "/data/game.log", "storage": map[string]any{"size": "64Mi", "mountPath": "/data"},
			"ports": []any{map[string]any{"name": "noop", "containerPort": int64(12345), "protocol": "TCP", "advertise": true}},
			"capabilities": map[string]any{"mods": map[string]any{"path": "mods",
				"install":  map[string]any{"allowedHosts": []any{"raw.githubusercontent.com"}, "maxSizeMB": int64(1)},
				"idList":   map[string]any{"env": "E2E_MOD_IDS"},
				"registry": map[string]any{"providers": []any{map[string]any{"provider": "modrinth", "modpacks": map[string]any{"refEnv": "E2E_MODPACK"}}}}}}}
		gs := p.createFixture(t, site.env, p.name, p.template, spec)
		if site.marker == "local" {
			p.localUID = string(gs.GetUID())
		} else {
			p.remoteUID = string(gs.GetUID())
		}
	}
	if p.localUID == "" || p.remoteUID == "" || p.localUID == p.remoteUID {
		t.Fatal("same-name fixtures must have different UIDs")
	}
	p.ready(t, p.name, 3*time.Minute)
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		ready, err := envInstance.PodIsReady(t.Context(), "gameplane-games", p.name+"-0")
		return err == nil && ready, fmt.Sprint(err)
	})
}

func (p *gatewayParity) ready(t *testing.T, name string, timeout time.Duration) {
	t.Helper()
	p.remote.Eventually(t, timeout, func() (bool, string) {
		ready, err := p.remote.PodIsReady(t.Context(), "gameplane-games", name+"-0")
		if err != nil || !ready {
			return false, fmt.Sprint(err)
		}
		response, body, err := p.client.Get("/servers/" + name + "/players?cluster=" + p.cluster + "&namespace=gameplane-games")
		if err != nil {
			return false, err.Error()
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK, fmt.Sprintf("agent readiness status=%d body=%s", response.StatusCode, body)
	})
}

func (p *gatewayParity) raw(t *testing.T, method, path, contentType string, body []byte, want int) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, p.client.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	if isMutation(method) {
		req.Header.Set("X-Gameplane-CSRF", p.client.CSRF)
	}
	response, err := p.client.HTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, want, raw)
	}
	return raw
}

func (p *gatewayParity) operations(t *testing.T) {
	remoteFile := p.expect(t, p.client, http.MethodGet, p.route("/files/read?path=/site.txt"), nil, http.StatusOK)
	if string(remoteFile) != "SITE:remote\n" {
		t.Fatalf("remote read reached wrong site: %q", remoteFile)
	}
	marker := []byte("REMOTE-MUTATION:" + p.remoteUID + "\n")
	p.raw(t, http.MethodPost, p.route("/files/write?path=/gateway-parity.txt"), "application/octet-stream", marker, http.StatusNoContent)
	if got := p.expect(t, p.client, http.MethodGet, p.route("/files/read?path=/gateway-parity.txt"), nil, http.StatusOK); !bytes.Equal(got, marker) {
		t.Fatalf("remote file round trip: %q", got)
	}
	direct, err := p.remote.Kubectl(t.Context(), "exec", "-n", "gameplane-games", p.name+"-0", "-c", "game", "--", "cat", "/data/gateway-parity.txt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(direct) != strings.TrimSpace(string(marker)) {
		t.Fatal("remote Pod does not contain the API write")
	}
	local, err := envInstance.Kubectl(t.Context(), "exec", "-n", "gameplane-games", p.name+"-0", "-c", "game", "--", "cat", "/data/site.txt")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(local) != "SITE:local" {
		t.Fatalf("local namesake changed: %q", local)
	}
	if _, err := envInstance.Kubectl(t.Context(), "exec", "-n", "gameplane-games", p.name+"-0", "-c", "game", "--", "test", "!", "-e", "/data/gateway-parity.txt"); err != nil {
		t.Fatal("remote file write created the file in the local namesake")
	}
	p.expect(t, p.client, http.MethodDelete, p.route("/files/delete?path=/gateway-parity.txt"), nil, http.StatusNoContent)
	p.expect(t, p.client, http.MethodGet, p.route("/files/read?path=/gateway-parity.txt"), nil, http.StatusNotFound)

	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	file, err := form.CreateFormFile("file", "gateway-test.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(marker); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	p.raw(t, http.MethodPost, p.route("/mods/upload"), form.FormDataContentType(), upload.Bytes(), http.StatusOK)
	var mods []modEntry
	if err := json.Unmarshal(p.expect(t, p.client, http.MethodGet, p.route("/mods"), nil, http.StatusOK), &mods); err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].Name != "gateway-test.bin" || mods[0].Meta == nil || mods[0].Meta.Provider != "upload" {
		t.Fatalf("remote installed mod manifest: %+v", mods)
	}
	p.expect(t, p.client, http.MethodDelete, p.route("/mods?name=gateway-test.bin"), nil, http.StatusNoContent)
	if got := strings.TrimSpace(string(p.expect(t, p.client, http.MethodGet, p.route("/mods"), nil, http.StatusOK))); got != "[]" {
		t.Fatalf("mod delete did not remove manifest: %s", got)
	}
	var providers []struct {
		Provider            string `json:"provider"`
		Available, Modpacks bool
	}
	if err := json.Unmarshal(p.expect(t, p.client, http.MethodGet, p.route("/mods/registry/providers"), nil, http.StatusOK), &providers); err != nil {
		t.Fatal(err)
	}
	if len(providers) != 1 || providers[0].Provider != "modrinth" || !providers[0].Available || !providers[0].Modpacks {
		t.Fatalf("remote template provider: %+v", providers)
	}
	p.expect(t, p.client, http.MethodPost, p.route("/modpack?provider=modrinth"), map[string]string{"ref": "gateway-parity-pack"}, http.StatusOK)
	p.expect(t, p.client, http.MethodPut, p.route("/mods/ids"), []map[string]string{{"id": "remote-parity-id", "name": "Remote parity"}}, http.StatusOK)
	ids := p.expect(t, p.client, http.MethodGet, p.route("/mods/ids"), nil, http.StatusOK)
	if !bytes.Contains(ids, []byte("remote-parity-id")) {
		t.Fatalf("remote ID list: %s", ids)
	}
	for _, site := range []struct {
		env    *Env
		remote bool
	}{{envInstance, false}, {p.remote, true}} {
		gs, err := site.env.Dyn.Resource(gameServerGVR).Namespace("gameplane-games").Get(t.Context(), p.name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		envs, _, _ := unstructured.NestedSlice(gs.Object, "spec", "env")
		gotPack := false
		for _, entry := range envs {
			item := entry.(map[string]any)
			if item["name"] == "E2E_MODPACK" && item["value"] == "gateway-parity-pack" {
				gotPack = true
			}
		}
		modIDs, _, _ := unstructured.NestedSlice(gs.Object, "spec", "mods", "ids")
		if gotPack != site.remote || (len(modIDs) == 1) != site.remote {
			t.Fatal("modpack/ID-list update reached the wrong Kubernetes cluster")
		}
	}
	// Wait for the operator's env updates to finish rolling the game Pod.
	p.remote.Eventually(t, 3*time.Minute, func() (bool, string) {
		pod, err := p.remote.K8s.CoreV1().Pods("gameplane-games").Get(t.Context(), p.name+"-0", metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		for _, c := range pod.Spec.Containers {
			if c.Name == "game" {
				for _, e := range c.Env {
					if e.Name == "E2E_MOD_IDS" && e.Value == "remote-parity-id" {
						return true, ""
					}
				}
			}
		}
		return false, "operator has not rendered the remote ID list"
	})
	p.ready(t, p.name, 3*time.Minute)
	p.liveLogs(t)
}

func (p *gatewayParity) liveLogs(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	endpoint := "ws" + strings.TrimPrefix(p.client.BaseURL, "http") + "/ws/servers/" + p.name + "/logs?cluster=" + p.cluster + "&namespace=gameplane-games"
	conn, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{HTTPClient: p.client.HTTP})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatalf("remote live logs: %v", err)
	}
	defer conn.CloseNow()
	found := false
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			if !found {
				t.Fatalf("remote live marker was never received: %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("gateway stream did not close at its configured 10-second deadline")
			}
			return
		}
		if bytes.Contains(raw, []byte("LIVE:local")) {
			t.Fatal("remote logs reached the local namesake")
		}
		found = found || bytes.Contains(raw, []byte("LIVE:remote"))
	}
}

func (p *gatewayParity) capture(t *testing.T) {
	p.expect(t, p.client, http.MethodPost, p.route(":capture-enable"), nil, http.StatusOK)
	p.remote.Eventually(t, 3*time.Minute, func() (bool, string) {
		gs, err := p.remote.Dyn.Resource(gameServerGVR).Namespace("gameplane-games").Get(t.Context(), p.name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		ready, _, _ := unstructured.NestedBool(gs.Object, "status", "capture", "ready")
		return ready, "remote capture sidecar is not ready"
	})
	var start struct {
		CaptureID string `json:"captureId"`
	}
	if err := json.Unmarshal(p.expect(t, p.client, http.MethodPost, p.route(":capture-start"), map[string]any{
		"filter": "tcp port 8090", "maxDurationSeconds": 300, "maxSizeBytes": 1048576, "ttlSecondsAfterFinished": 3600}, http.StatusAccepted), &start); err != nil {
		t.Fatal(err)
	}
	if start.CaptureID == "" {
		t.Fatal("remote capture returned no ID")
	}
	t.Cleanup(func() {
		_ = p.remote.Dyn.Resource(networkCaptureGVR).Namespace("gameplane-games").Delete(context.Background(), start.CaptureID, metav1.DeleteOptions{})
	})
	p.capturePhase(t, start.CaptureID, "Running")
	// Generate actual target traffic before stopping, not just an empty file.
	p.expect(t, p.client, http.MethodGet, p.route("/files/read?path=/site.txt"), nil, http.StatusOK)
	// Running means the socket is active, not that its asynchronous ring has
	// delivered this request to the writer. Stop deliberately cancels reads.
	packetCtx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	p.remote.Eventually(t, 30*time.Second, func() (bool, string) {
		capture, err := p.remote.Dyn.Resource(networkCaptureGVR).Namespace("gameplane-games").Get(packetCtx, start.CaptureID, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("read capture progress before stop: %v", err)
		}
		phase, found, err := unstructured.NestedString(capture.Object, "status", "phase")
		if err != nil || !found || phase != "Running" {
			t.Fatalf("capture must remain Running before stop: phase=%q found=%v err=%v", phase, found, err)
		}
		packets, _, err := unstructured.NestedInt64(capture.Object, "status", "packetsWritten")
		if err != nil || packets < 0 {
			t.Fatalf("invalid capture packet counter before stop: packets=%d err=%v", packets, err)
		}
		return packets > 0, "capture writer has not recorded target traffic"
	})
	p.expect(t, p.client, http.MethodPost, p.route(":capture-stop"), map[string]string{"captureId": start.CaptureID}, http.StatusOK)
	p.capturePhase(t, start.CaptureID, "Completed")
	remoteCapture, err := p.remote.Dyn.Resource(networkCaptureGVR).Namespace("gameplane-games").Get(t.Context(), start.CaptureID, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	packets, found, err := unstructured.NestedInt64(remoteCapture.Object, "status", "packetsWritten")
	if err != nil || !found || packets <= 0 {
		t.Fatalf("completed remote capture must contain target traffic: packets=%d found=%v err=%v", packets, found, err)
	}
	privatePath := fmt.Sprintf("/v1/clusters/%s/namespaces/gameplane-games/servers/%s/uids/%s/captures/%s/uids/%s/file",
		p.cluster, p.name, p.remoteUID, start.CaptureID, remoteCapture.GetUID())
	publicFile := p.expect(t, p.client, http.MethodGet, p.route(":capture-file?id="+start.CaptureID), nil, http.StatusOK)
	if len(publicFile) < 28 || !bytes.Equal(publicFile[:4], []byte{0x0a, 0x0d, 0x0d, 0x0a}) {
		t.Fatal("capture download is not a PCAPNG section")
	}
	client := p.directClient(t, p.clientLeaf, p.nodeIP)
	defer client.CloseIdleConnections()
	if direct := gatewayDirect(t, client, http.MethodGet, p.endpoint+privatePath, http.StatusOK); !bytes.Equal(direct, publicFile) {
		t.Fatal("public remote capture differs from its bound gateway file")
	}
	for _, bad := range []string{strings.Replace(privatePath, p.remoteUID, p.localUID, 1),
		strings.Replace(privatePath, string(remoteCapture.GetUID()), "previous-capture-uid", 1)} {
		gatewayDirect(t, client, http.MethodGet, p.endpoint+bad, http.StatusNotFound)
		gatewayDirect(t, client, http.MethodDelete, p.endpoint+bad, http.StatusNotFound)
	}
	// Read the remote sidecar directly with the cluster's own agent identity.
	// This distinguishes file cleanup from merely deleting the NetworkCapture CR.
	sidecar := p.sidecarClient(t)
	defer sidecar.CloseIdleConnections()
	boundFile := fmt.Sprintf("https://%s-agent.gameplane-games.svc.cluster.local/v1/targets/%s/captures/%s/uids/%s/file",
		p.name, p.remoteUID, start.CaptureID, remoteCapture.GetUID())
	if direct := gatewayDirect(t, sidecar, http.MethodGet, boundFile, http.StatusOK); !bytes.Equal(direct, publicFile) {
		t.Fatal("remote sidecar ground truth differs from public capture")
	}
	for _, bad := range []string{strings.Replace(boundFile, p.remoteUID, p.localUID, 1),
		strings.Replace(boundFile, string(remoteCapture.GetUID()), "replacement-capture-uid", 1)} {
		gatewayDirect(t, sidecar, http.MethodGet, bad, http.StatusNotFound)
		gatewayDirect(t, sidecar, http.MethodDelete, bad, http.StatusNotFound)
	}
	gatewayDirect(t, sidecar, http.MethodGet, boundFile, http.StatusOK)
	// Simulate a cleanup acknowledgement lost before the central CR deletion.
	gatewayDirect(t, sidecar, http.MethodDelete, boundFile, http.StatusNoContent)
	gatewayDirect(t, sidecar, http.MethodDelete, boundFile, http.StatusNoContent)
	p.expect(t, p.client, http.MethodDelete, p.route(":capture?id="+start.CaptureID), nil, http.StatusOK)
	gatewayDirect(t, sidecar, http.MethodGet, boundFile, http.StatusGone)
	if _, err := p.remote.Dyn.Resource(networkCaptureGVR).Namespace("gameplane-games").Get(t.Context(), start.CaptureID, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("capture deletion did not remove the remote CR: %v", err)
	}
	local, err := envInstance.Dyn.Resource(gameServerGVR).Namespace("gameplane-games").Get(t.Context(), p.name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	enabled, _, _ := unstructured.NestedBool(local.Object, "spec", "capture", "enabled")
	if enabled {
		t.Fatal("remote capture enable changed the local namesake")
	}
	p.captureAbsentCleanup(t, remoteCapture, client, sidecar)
}

func (p *gatewayParity) captureAbsentCleanup(t *testing.T, previous *unstructured.Unstructured, gateway, sidecar *http.Client) {
	t.Helper()
	pod, err := p.remote.K8s.CoreV1().Pods("gameplane-games").Get(t.Context(), p.name+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// A stop requested at creation prevents StartCapture. Keep a recorded pod UID
	// so the API must obtain the sidecar's 410 instead of using the never-started
	// shortcut. This exercises the same empty-volume state as a replaced pod.
	nc := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "NetworkCapture",
		"spec": previous.DeepCopy().Object["spec"],
	}}
	nc.SetName(previous.GetName() + "-absent")
	nc.SetNamespace("gameplane-games")
	nc.SetOwnerReferences(previous.GetOwnerReferences())
	nc.SetAnnotations(map[string]string{"gameplane.local/stop-requested": time.Now().UTC().Format(time.RFC3339), "gameplane.local/capture-pod-uid": string(pod.UID)})
	nc, err = p.remote.Dyn.Resource(networkCaptureGVR).Namespace(nc.GetNamespace()).Create(t.Context(), nc, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = p.remote.Dyn.Resource(networkCaptureGVR).Namespace(nc.GetNamespace()).Delete(context.Background(), nc.GetName(), metav1.DeleteOptions{})
	})
	p.capturePhase(t, nc.GetName(), "Completed")
	boundFile := fmt.Sprintf("https://%s-agent.gameplane-games.svc.cluster.local/v1/targets/%s/captures/%s/uids/%s/file", p.name, p.remoteUID, nc.GetName(), nc.GetUID())
	gatewayDirect(t, sidecar, http.MethodDelete, boundFile, http.StatusGone)
	privatePath := fmt.Sprintf("/v1/clusters/%s/namespaces/gameplane-games/servers/%s/uids/%s/captures/%s/uids/%s/file", p.cluster, p.name, p.remoteUID, nc.GetName(), nc.GetUID())
	gatewayDirect(t, gateway, http.MethodDelete, p.endpoint+privatePath, http.StatusGone)
	p.expect(t, p.client, http.MethodDelete, p.route(":capture?id="+nc.GetName()), nil, http.StatusOK)
	if _, err := p.remote.Dyn.Resource(networkCaptureGVR).Namespace(nc.GetNamespace()).Get(t.Context(), nc.GetName(), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("absent capture CR retained: %v", err)
	}
}

func (p *gatewayParity) capturePhase(t *testing.T, id, phase string) {
	t.Helper()
	p.remote.Eventually(t, 2*time.Minute, func() (bool, string) {
		capture, err := p.remote.Dyn.Resource(networkCaptureGVR).Namespace("gameplane-games").Get(t.Context(), id, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		actual, _, _ := unstructured.NestedString(capture.Object, "status", "phase")
		return actual == phase, "capture phase=" + actual
	})
}

func (p *gatewayParity) sidecarClient(t *testing.T) *http.Client {
	t.Helper()
	clientSecret, err := p.remote.K8s.CoreV1().Secrets("gameplane-system").Get(t.Context(), "gameplane-agent-client", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ca, err := p.remote.K8s.CoreV1().Secrets("gameplane-system").Get(t.Context(), "gameplane-agent-ca", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(clientSecret.Data["tls.crt"], clientSecret.Data["tls.key"])
	if err != nil {
		t.Fatal("remote agent keypair cannot be loaded")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca.Data["ca.crt"]) {
		t.Fatal("remote agent CA cannot be loaded")
	}
	port, stop := p.remote.PortForward(t, "gameplane-games", "pod/"+p.name+"-0", 9091)
	t.Cleanup(stop)
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool,
		Certificates: []tls.Certificate{cert}}, DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, fmt.Sprintf("127.0.0.1:%d", port))
		}}
	return &http.Client{Transport: transport, Timeout: 15 * time.Second}
}

func gatewayDirect(t *testing.T, client *http.Client, method, endpoint string, want int) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatalf("gateway %s: %v", method, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("gateway %s %s status=%d want=%d body=%s", method, endpoint, response.StatusCode, want, raw)
	}
	return raw
}

func (p *gatewayParity) minecraft(t *testing.T) {
	// Match the existing Minecraft gamebot's small vanilla 1.21.4 fixture.
	// Seed the offline profile cache so whitelist commands need no external
	// player-profile lookup. The whitelist itself must start empty.
	const player = "gatewaye2ebot"
	// Java UUID.nameUUIDFromBytes("OfflinePlayer:gatewaye2ebot".getBytes(UTF_8)).
	const offlineUUID = "47180571-e953-377d-aed7-699234c8bd40"
	const cacheTimeFormat = "2006-01-02 15:04:05 -0700"
	userCache, err := json.Marshal([]map[string]string{{
		"name": player, "uuid": offlineUUID,
		"expiresOn": time.Now().UTC().Add(24 * time.Hour).Format(cacheTimeFormat),
	}})
	if err != nil {
		t.Fatal(err)
	}
	name, template := p.name+"-mc", p.template+"-mc"
	vars := map[string]string{"EULA": "TRUE", "TYPE": "VANILLA", "VERSION": "1.21.4", "ONLINE_MODE": "FALSE",
		"INIT_MEMORY": "512M", "MAX_MEMORY": "1G", "USE_AIKAR_FLAGS": "false", "LEVEL_TYPE": "FLAT",
		"VIEW_DISTANCE": "4", "SPAWN_PROTECTION": "0", "ENABLE_RCON": "true", "RCON_PORT": "25575"}
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	envs := make([]any, 0, len(vars))
	for _, key := range keys {
		envs = append(envs, map[string]any{"name": key, "value": vars[key]})
	}
	spec := map[string]any{"displayName": "Remote gateway Minecraft", "game": "minecraft-java", "version": "1", "image": "itzg/minecraft-server:java21",
		"env": envs, "storage": map[string]any{"size": "2Gi", "mountPath": "/data"},
		"configFiles": []any{map[string]any{"path": "usercache.json", "template": string(userCache)}},
		"resources":   map[string]any{"requests": map[string]any{"cpu": "250m", "memory": "1Gi"}, "limits": map[string]any{"cpu": "2", "memory": "1536Mi"}},
		"ports":       []any{map[string]any{"name": "game", "containerPort": int64(25565), "protocol": "TCP", "advertise": true}, map[string]any{"name": "rcon", "containerPort": int64(25575), "protocol": "TCP", "advertise": false}},
		"probes":      map[string]any{"readiness": map[string]any{"exec": map[string]any{"command": []any{"mc-health"}}, "initialDelaySeconds": int64(30), "periodSeconds": int64(10), "failureThreshold": int64(60)}},
		"rcon":        map[string]any{"protocol": "source", "port": int64(25575), "passwordEnv": "RCON_PASSWORD"},
		"capabilities": map[string]any{"actions": []any{map[string]any{"id": "save-world", "displayName": "Save world", "command": "save-all flush"}},
			"status":  map[string]any{"metrics": []any{map[string]any{"id": "online", "displayName": "Online players", "command": "list", "regex": `There are (?P<value>\d+) of a max`}}},
			"players": map[string]any{"whitelist": map[string]any{"list": "whitelist list", "add": "whitelist add {{.Player}}", "remove": "whitelist remove {{.Player}}", "listRegex": `:\s*(?P<names>.*)`}}}}
	p.createFixture(t, p.remote, name, template, spec)
	// Cleanup is LIFO: classify failures while the fixture's agent still exists.
	t.Cleanup(func() {
		if t.Failed() {
			p.minecraftFailureCategories(t, name)
		}
	})
	p.ready(t, name, 10*time.Minute)
	route := func(path string) string {
		return "/servers/" + name + path + "?cluster=" + p.cluster + "&namespace=gameplane-games"
	}
	var cached []struct{ Name, UUID, ExpiresOn string }
	if err := json.Unmarshal(p.expect(t, p.client, http.MethodGet, route("/files/read")+"&path=/usercache.json", nil, http.StatusOK), &cached); err != nil {
		t.Fatal(err)
	}
	if len(cached) != 1 || cached[0].Name != player || cached[0].UUID != offlineUUID {
		t.Fatal("seeded offline profile is missing or has an unexpected identity")
	}
	expires, err := time.Parse(cacheTimeFormat, cached[0].ExpiresOn)
	if err != nil || !expires.After(time.Now()) {
		t.Fatal("seeded offline profile does not have a valid future expiry")
	}
	var players struct {
		Online  int      `json:"online"`
		Max     int      `json:"max"`
		Players []string `json:"players"`
	}
	if err := json.Unmarshal(p.expect(t, p.client, http.MethodGet, route("/players"), nil, http.StatusOK), &players); err != nil {
		t.Fatal(err)
	}
	if players.Online != 0 || players.Max <= 0 || len(players.Players) != 0 {
		t.Fatalf("actual Minecraft player snapshot: %+v", players)
	}
	var metrics []struct{ ID, Value string }
	if err := json.Unmarshal(p.expect(t, p.client, http.MethodGet, route("/status"), nil, http.StatusOK), &metrics); err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0].ID != "online" || metrics[0].Value != "0" {
		t.Fatalf("actual Minecraft RCON status: %+v", metrics)
	}
	var action struct {
		OK  bool   `json:"ok"`
		Raw string `json:"raw"`
	}
	if err := json.Unmarshal(p.expect(t, p.client, http.MethodPost, route("/actions/run"), map[string]string{"id": "save-world"}, http.StatusOK), &action); err != nil {
		t.Fatal(err)
	}
	if !action.OK || !strings.Contains(action.Raw, "Saved the game") {
		t.Fatalf("actual Minecraft action response: %+v", action)
	}
	// A player-list mutation traverses the same remote gateway and RCON path.
	var listed []string
	if err := json.Unmarshal(p.expect(t, p.client, http.MethodGet, route("/players/whitelist"), nil, http.StatusOK), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("remote whitelist must start empty: %v", listed)
	}
	p.expect(t, p.client, http.MethodPost, route("/players/whitelist/add"), map[string]string{"name": player}, http.StatusOK)
	if err := json.Unmarshal(p.expect(t, p.client, http.MethodGet, route("/players/whitelist"), nil, http.StatusOK), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0] != player {
		t.Fatalf("remote whitelist mutation missing: %v", listed)
	}
	p.expect(t, p.client, http.MethodPost, route("/players/whitelist/remove"), map[string]string{"name": player}, http.StatusOK)
	if err := json.Unmarshal(p.expect(t, p.client, http.MethodGet, route("/players/whitelist"), nil, http.StatusOK), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 0 {
		t.Fatalf("remote whitelist removal failed: %v", listed)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(p.client.BaseURL, "http")+"/ws/servers/"+name+"/console?cluster="+p.cluster+"&namespace=gameplane-games", &websocket.DialOptions{HTTPClient: p.client.HTTP})
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatalf("remote RCON console upgrade: %v", err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"kind":"cmd","body":"list"}`)); err != nil {
		t.Fatal(err)
	}
	for {
		_, message, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("remote RCON console ended before command reply: %v", err)
		}
		var reply struct {
			Kind string `json:"kind"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal(message, &reply); err != nil {
			t.Fatalf("remote RCON console returned malformed envelope: %v", err)
		}
		if reply.Kind == "err" {
			t.Fatalf("remote RCON console command failed: %s", reply.Body)
		}
		if reply.Kind == "out" && strings.Contains(reply.Body, "There are 0 of a max") {
			break
		}
	}
}

func (p *gatewayParity) minecraftFailureCategories(t *testing.T, name string) {
	t.Helper()
	// t.Context is canceled before cleanup. Bound both retrieval time and bytes.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tail, limit := int64(100), int64(32<<10)
	stream, err := p.remote.K8s.CoreV1().Pods("gameplane-games").GetLogs(name+"-0", &corev1.PodLogOptions{
		Container: "agent", TailLines: &tail, LimitBytes: &limit,
	}).Stream(ctx)
	if err != nil {
		t.Log("Minecraft moderation diagnostics: agent log unavailable")
		return
	}
	defer stream.Close()
	raw, err := io.ReadAll(io.LimitReader(stream, limit))
	if err != nil {
		t.Log("Minecraft moderation diagnostics: agent log unreadable")
		return
	}
	// Only fixed categories and counts may reach CI output. Never emit the raw
	// log, command, error, or any other log-supplied string, even after redaction.
	var timeout, eof, reset, auth, other int
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		var entry struct{ Msg, Err string }
		if json.Unmarshal(line, &entry) != nil || entry.Msg != "players moderation rcon" {
			continue
		}
		detail := strings.ToLower(entry.Err)
		switch {
		case strings.Contains(detail, "timeout"), strings.Contains(detail, "deadline exceeded"):
			timeout++
		case strings.Contains(detail, "eof"):
			eof++
		case strings.Contains(detail, "connection reset"), strings.Contains(detail, "broken pipe"):
			reset++
		case strings.Contains(detail, "authentication failed"):
			auth++
		default:
			other++
		}
	}
	t.Logf("Minecraft moderation diagnostics: timeout=%d eof=%d reset=%d auth=%d other=%d", timeout, eof, reset, auth, other)
}

func (p *gatewayParity) privatePath(uid, operation string) string {
	return fmt.Sprintf("/v1/clusters/%s/namespaces/gameplane-games/servers/%s/uids/%s%s", p.cluster, p.name, uid, operation)
}

func (p *gatewayParity) denials(t *testing.T) {
	// The central admin's primary local role confers no remote namespaced grant.
	p.expect(t, p.admin, http.MethodGet, p.route("/files/read?path=/site.txt"), nil, http.StatusForbidden)
	p.expect(t, p.client, http.MethodPost, "/servers/"+p.name+"/modpack", map[string]string{"ref": "must-not-apply"}, http.StatusForbidden)
	good := p.directClient(t, p.clientLeaf, p.nodeIP)
	defer good.CloseIdleConnections()
	gatewayDirect(t, good, http.MethodGet, p.endpoint+"/v1/capabilities", http.StatusOK)
	for _, path := range []string{p.privatePath(p.localUID, "/files/read?path=/site.txt"),
		strings.Replace(p.privatePath(p.remoteUID, "/status"), "/clusters/"+p.cluster+"/", "/clusters/wrong-cluster/", 1),
		strings.Replace(p.privatePath(p.remoteUID, "/status"), "/namespaces/gameplane-games/", "/namespaces/kube-system/", 1),
		p.privatePath(p.remoteUID, "/quiesce")} {
		gatewayDirect(t, good, http.MethodGet, p.endpoint+path, http.StatusNotFound)
	}
	wrongCA := newGatewayTestCA(t)
	for _, tc := range []struct {
		name       string
		leaf       gatewayTestLeaf
		serverName string
	}{
		{"no-client-certificate", gatewayTestLeaf{}, p.nodeIP},
		{"wrong-peer-URI", p.clientCA.issue(t, "spiffe://gameplane.e2e/untrusted-api", ""), p.nodeIP},
		{"unknown-client-CA", wrongCA.issue(t, gatewayTestPeer, ""), p.nodeIP},
		{"wrong-server-SAN", p.clientLeaf, "wrong-gateway.e2e.invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := p.directClient(t, tc.leaf, tc.serverName)
			defer client.CloseIdleConnections()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.endpoint+"/v1/capabilities", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(req)
			if response != nil {
				response.Body.Close()
			}
			if err == nil {
				t.Fatal("invalid gateway identity was accepted")
			}
			if tc.name == "wrong-server-SAN" {
				var nameError x509.HostnameError
				if !errors.As(err, &nameError) {
					t.Fatalf("expected SAN verification failure, got %v", err)
				}
			} else if !strings.Contains(strings.ToLower(err.Error()), "certificate") {
				t.Fatalf("expected TLS client-certificate rejection, got %v", err)
			}
			// A contemporaneous positive control rules out a general outage.
			gatewayDirect(t, good, http.MethodGet, p.endpoint+"/v1/capabilities", http.StatusOK)
		})
	}
}

func (p *gatewayParity) rotation(t *testing.T) {
	checkOperation := func() {
		body := p.expect(t, p.client, http.MethodGet, p.route("/files/read?path=/site.txt"), nil, http.StatusOK)
		if string(body) != "SITE:remote\n" {
			t.Fatalf("rotation changed target: %q", body)
		}
	}
	checkOperation()
	// Successful leaf rotation is followed by a CA transition that makes using
	// the new Secret necessary; a cached old transport cannot pass that check.
	leaf := p.clientCA.issue(t, gatewayTestPeer, "")
	gatewaySecret(t, envInstance, credentialsNS, p.clientSecret, p.credentials(leaf), false)
	checkOperation()
	oldClient := p.directClient(t, p.clientLeaf, p.nodeIP)
	defer oldClient.CloseIdleConnections()
	server := p.serverCA.issue(t, "", p.nodeIP)
	newServer, err := x509.ParseCertificate(server.tls.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	gatewaySecret(t, p.remote, "gameplane-system", p.serverSecret, map[string][]byte{"tls.crt": server.cert, "tls.key": server.key}, false)
	p.remote.Eventually(t, 3*time.Minute, func() (bool, string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.endpoint+"/v1/capabilities", nil)
		if err != nil {
			return false, err.Error()
		}
		response, err := oldClient.Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK && response.TLS.PeerCertificates[0].SerialNumber.Cmp(newServer.SerialNumber) == 0, "mounted server certificate has not changed"
	})
	checkOperation()
	newCA := newGatewayTestCA(t)
	newLeaf := newCA.issue(t, gatewayTestPeer, "")
	newClient := p.directClient(t, newLeaf, p.nodeIP)
	defer newClient.CloseIdleConnections()
	overlap := append(append([]byte{}, p.clientCA.pem...), newCA.pem...)
	gatewaySecret(t, p.remote, "gameplane-system", p.trustSecret, map[string][]byte{"ca.crt": overlap}, false)
	p.remote.Eventually(t, 3*time.Minute, func() (bool, string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.endpoint+"/v1/capabilities", nil)
		if err != nil {
			return false, err.Error()
		}
		response, err := newClient.Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK, "overlapping client roots have not projected"
	})
	gatewayDirect(t, oldClient, http.MethodGet, p.endpoint+"/v1/capabilities", http.StatusOK)
	gatewaySecret(t, envInstance, credentialsNS, p.clientSecret, p.credentials(newLeaf), false)
	checkOperation()
	gatewaySecret(t, p.remote, "gameplane-system", p.trustSecret, map[string][]byte{"ca.crt": newCA.pem}, false)
	p.remote.Eventually(t, 3*time.Minute, func() (bool, string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.endpoint+"/v1/capabilities", nil)
		if err != nil {
			return false, err.Error()
		}
		fresh, err := newClient.Do(req)
		if err != nil {
			return false, "new identity failed: " + err.Error()
		}
		fresh.Body.Close()
		if fresh.StatusCode != http.StatusOK {
			return false, "new identity no longer authorized"
		}
		oldRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, p.endpoint+"/v1/capabilities", nil)
		if err != nil {
			return false, err.Error()
		}
		old, err := oldClient.Do(oldRequest)
		if old != nil {
			old.Body.Close()
		}
		return err != nil && strings.Contains(strings.ToLower(err.Error()), "certificate"), "old trust root is still accepted"
	})
	p.clientCA, p.clientLeaf = newCA, newLeaf
	checkOperation()
	// Switch to a separate NodePort and remove the original Service. Success
	// now proves that the central API refreshed the Cluster URL, not just TLS.
	replacement := p.newService(t, p.name+"-replacement")
	replacementURL := fmt.Sprintf("https://%s:%d", p.nodeIP, replacement.Spec.Ports[0].NodePort)
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"agentGateway": map[string]any{"url": replacementURL}}})
	if _, err := envInstance.Dyn.Resource(clusterGVR).Patch(t.Context(), p.cluster, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := p.remote.K8s.CoreV1().Services("gameplane-system").Delete(t.Context(), p.service.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	p.endpoint = replacementURL
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		response, body, err := p.client.Get(p.route("/files/read?path=/site.txt"))
		if err != nil {
			return false, err.Error()
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK && string(body) == "SITE:remote\n", fmt.Sprintf("replacement endpoint status=%d", response.StatusCode)
	})
	// Credential enrollment revocation must be observed on the very next
	// operation. Every denial is followed by restoration and a positive read.
	secrets := envInstance.K8s.CoreV1().Secrets(credentialsNS)
	secret, err := secrets.Get(t.Context(), p.clientSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delete(secret.Labels, "gameplane.local/agent-gateway-credentials")
	if _, err := secrets.Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	p.expect(t, p.client, http.MethodGet, p.route("/files/read?path=/site.txt"), nil, http.StatusInternalServerError)
	secret, err = secrets.Get(t.Context(), p.clientSecret, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if secret.Labels == nil {
		secret.Labels = make(map[string]string)
	}
	secret.Labels["gameplane.local/agent-gateway-credentials"] = "true"
	if _, err := secrets.Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	checkOperation()
	gatewaySecret(t, envInstance, credentialsNS, p.clientSecret, map[string][]byte{"tls.crt": []byte("not-a-certificate")}, false)
	p.expect(t, p.client, http.MethodGet, p.route("/files/read?path=/site.txt"), nil, http.StatusInternalServerError)
	gatewaySecret(t, envInstance, credentialsNS, p.clientSecret, p.credentials(p.clientLeaf), false)
	checkOperation()
	if err := secrets.Delete(t.Context(), p.clientSecret, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	p.expect(t, p.client, http.MethodGet, p.route("/files/read?path=/site.txt"), nil, http.StatusNotFound)
	gatewaySecret(t, envInstance, credentialsNS, p.clientSecret, p.credentials(p.clientLeaf), true)
	checkOperation()
	if _, err := envInstance.Dyn.Resource(clusterGVR).Patch(t.Context(), p.cluster, types.MergePatchType,
		[]byte(`{"spec":{"agentGateway":null}}`), metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	p.expect(t, p.client, http.MethodGet, p.route("/files/read?path=/site.txt"), nil, http.StatusServiceUnavailable)
	restored, _ := json.Marshal(map[string]any{"spec": map[string]any{"agentGateway": map[string]any{
		"url": p.endpoint, "tlsSecretRef": map[string]string{"name": p.clientSecret}}}})
	if _, err := envInstance.Dyn.Resource(clusterGVR).Patch(t.Context(), p.cluster, types.MergePatchType, restored, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	checkOperation()
}
