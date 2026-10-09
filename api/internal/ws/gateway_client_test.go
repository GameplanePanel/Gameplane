package ws

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func gatewayFixture(t *testing.T, endpoint string) (*agentGatewayResolver, *kube.Client, *kube.Client) {
	t.Helper()
	certPath, keyPath := writeKeypair(t)
	cert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gateway-client", Namespace: "gameplane-system", Labels: map[string]string{gatewayCredentialsLabel: "true"}},
		Data:       map[string][]byte{"tls.crt": cert, "tls.key": key, "ca.crt": cert},
	}
	registration := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "Cluster",
		"metadata": map[string]any{"name": "remote"},
		"spec":     map[string]any{"agentGateway": map[string]any{"url": endpoint, "tlsSecretRef": map[string]any{"name": "gateway-client"}}},
	}}
	newDynamic := func(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
		return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
			kube.GVRCluster: "ClusterList", kube.GVRs["servers"]: "GameServerList",
		}, objects...)
	}
	gs := func(uid string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer",
			"metadata": map[string]any{"name": "alpha", "namespace": "gameplane-games", "uid": uid},
		}}
	}
	home := &kube.Client{Dynamic: newDynamic(registration, gs("local-uid")), Typed: kubefake.NewSimpleClientset(secret)}
	remote := &kube.Client{Dynamic: newDynamic(gs("remote-uid"))}
	registry := kube.NewRegistry(scope.DefaultCluster)
	registry.Set(scope.DefaultCluster, home)
	registry.Set("remote", remote)
	return &agentGatewayResolver{registry: registry, namespace: "gameplane-system"}, home, remote
}

func TestGatewayURLRejectsNonOrigins(t *testing.T) {
	for _, raw := range []string{"http://gateway.test", "https://user:pass@gateway.test", "https://gateway.test/forward", "https://gateway.test?target=other", "https://gateway.test#other", "https:///empty", "//gateway.test"} {
		if _, err := gatewayURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"https://gateway.test:8443", "https://10.0.0.2/"} {
		if _, err := gatewayURL(raw); err != nil {
			t.Errorf("rejected %q: %v", raw, err)
		}
	}
}

func TestStandaloneGatewayRejectsUnsafeDestinations(t *testing.T) {
	for _, endpoint := range []string{"https://127.0.0.1:8443", "https://169.254.169.254", "https://metadata.google.internal", "https://10.1.2.3:8443"} {
		t.Run(endpoint, func(t *testing.T) {
			resolver, home, _ := gatewayFixture(t, endpoint)
			// Select the existing fake Kubernetes registration/secret clients as
			// standalone management storage, without introducing a network bypass.
			home.ClusterStore = home.Clusters()
			home.RemoteAccess, _ = kube.NewRemoteAccessPolicy([]string{"192.168.0.0/16"})
			if _, _, err := resolver.resolve(t.Context(), "remote", agentTarget{name: "alpha", namespace: "gameplane-games"}); err == nil {
				t.Fatal("unsafe gateway endpoint was accepted")
			}
		})
	}
}

func TestGatewayResolverBindsRemoteUIDAndReadsCredentialsFresh(t *testing.T) {
	resolver, home, remote := gatewayFixture(t, "https://gateway.test:8443")
	target := agentTarget{name: "alpha", namespace: "gameplane-games"}
	selected, transport, err := resolver.resolve(t.Context(), "remote", target)
	if err != nil {
		t.Fatal(err)
	}
	if selected != remote || transport.target.UID != "remote-uid" {
		t.Fatal("resolved the local namesake")
	}
	secret, err := home.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "gateway-client", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	delete(secret.Labels, gatewayCredentialsLabel)
	if _, err := home.Typed.CoreV1().Secrets("gameplane-system").Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolver.resolve(t.Context(), "remote", target); err == nil {
		t.Fatal("unlabeled Secret was used through cached credentials")
	}
	if err := home.Typed.CoreV1().Secrets("gameplane-system").Delete(t.Context(), "gateway-client", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolver.resolve(t.Context(), "remote", target); err == nil {
		t.Fatal("deleted credentials remained usable")
	}
	if _, _, err := resolver.resolve(t.Context(), "missing", target); err == nil {
		t.Fatal("unknown cluster was accepted")
	}
}

func TestGatewayTransportPreservesBoundTargetAndFiltersCredentials(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		target, path, err := gatewayprotocol.ParsePath(req.URL.Path)
		if err != nil || target.Cluster != "remote" || target.UID != "remote-uid" || path != "/files/write" {
			t.Errorf("target = %+v, path = %s, error = %v", target, path, err)
		}
		if req.URL.Query().Get("cluster") != "" || req.URL.Query().Get("namespace") != "" || req.URL.Query().Get("path") != "config.ini" {
			t.Errorf("query = %s", req.URL.RawQuery)
		}
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" || req.Header.Get("X-Gameplane-Server-UID") != "" {
			t.Error("untrusted identity headers reached gateway")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil || string(body) != "setting=true" {
			t.Errorf("body = %q, error = %v", body, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	resolver, home, _ := gatewayFixture(t, srv.URL)
	secret, _ := home.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "gateway-client", metav1.GetOptions{})
	secret.Data["ca.crt"] = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if _, err := home.Typed.CoreV1().Secrets("gameplane-system").Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	target := agentTarget{name: "alpha", namespace: "gameplane-games"}
	_, transport, err := resolver.resolve(t.Context(), "remote", target)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := transport.Do(t.Context(), agentRequest{
		target: target, method: http.MethodPost, path: "/files/write", rawQuery: "cluster=other&namespace=other&path=config.ini",
		header: http.Header{"Authorization": {"Bearer private"}, "Cookie": {"session=private"}, "X-Gameplane-Server-Uid": {"spoofed"}},
		body:   strings.NewReader("setting=true"),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	mismatched, err := transport.Do(t.Context(), agentRequest{target: agentTarget{name: "beta", namespace: "gameplane-games"}, method: http.MethodGet, path: "/mods"})
	if mismatched != nil && mismatched.Body != nil {
		_ = mismatched.Body.Close()
	}
	if err == nil {
		t.Fatal("bound target was replaced")
	}
	unsupported, err := transport.Do(t.Context(), agentRequest{target: target, method: http.MethodPost, path: "/quiesce"})
	if unsupported != nil && unsupported.Body != nil {
		_ = unsupported.Body.Close()
	}
	if err == nil {
		t.Fatal("unsupported operation was accepted")
	}
}

func TestGatewayRouteUsesPerRequestProxy(t *testing.T) {
	resolver, home, remote := gatewayFixture(t, "https://gateway.test")
	p := &proxy{k: home, stdin: home, gateway: resolver}
	router := chi.NewRouter()
	router.Get("/servers/{name}/status", p.agentRoute(func(selected *proxy) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			want := home
			if req.URL.Query().Get("cluster") == "remote" {
				want = remote
			}
			if selected.k != want || selected.stdin != want {
				t.Error("wrong request-scoped Kubernetes client")
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	for _, query := range []string{"?cluster=remote", "?cluster=local", ""} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/status"+query, nil))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("query %s: %d %s", query, rec.Code, rec.Body)
		}
	}
	if p.k != home || p.stdin != home || p.transport != nil {
		t.Fatal("shared proxy was mutated")
	}
}

func TestGatewayEndpointCannotChangeClusterWithQuery(t *testing.T) {
	base, _ := url.Parse("https://gateway.test")
	tr := &gatewayAgentTransport{base: base, target: gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "alpha", UID: "remote-uid"}}
	endpoint, err := tr.endpoint(agentTarget{name: "alpha", namespace: "games"}, http.MethodGet, "/mods", "cluster=local", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(endpoint, "/clusters/remote/") || strings.Contains(endpoint, "cluster=local") {
		t.Fatalf("endpoint = %s", endpoint)
	}
}

func TestGatewayStdinRejectsRecreatedServerWithValidOwnership(t *testing.T) {
	k := &kube.Client{}
	_ = streamTestRegistry(k)
	if _, err := serverPod(t.Context(), k, "gameplane-games", "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := serverPodForUID(t.Context(), k, "gameplane-games", "alpha", "previous-server-uid"); err == nil {
		t.Fatal("a valid replacement workload was accepted for the previously bound server")
	}
}

func TestGatewayRejectsReplacementAfterOwnershipAuthorization(t *testing.T) {
	resolver, home, _ := gatewayFixture(t, "https://gateway.test")
	p := &proxy{k: home, gateway: resolver}
	router := chi.NewRouter()
	router.Use(rbac.Middleware(authorizationSnapshot{uid: "previous-server-uid"}))
	router.Get("/servers/{name}/status", p.agentRoute(func(*proxy) http.HandlerFunc {
		return func(http.ResponseWriter, *http.Request) { t.Error("replacement reached the agent operation") }
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/status?cluster=remote", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d: %s", response.Code, response.Body)
	}
}
