package handlers

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// testCAPEM returns a self-signed CA cert PEM for caCertHash/kubeconfig.
func testCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func clusterActionsClient(t *testing.T) *kube.Client {
	return &kube.Client{
		Typed: fake.NewSimpleClientset(),
		Config: &rest.Config{
			Host:            "https://api.test:6443",
			TLSClientConfig: rest.TLSClientConfig{CAData: testCAPEM(t)},
		},
	}
}

func TestClusterActions_DisabledReturns501(t *testing.T) {
	r := chi.NewRouter()
	MountClusterActions(r, clusterActionsClient(t), false, "")

	for _, path := range []string{"/cluster/nodes:join", "/cluster/kubeconfig"} {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil))
		if rr.Code != http.StatusNotImplemented {
			t.Fatalf("%s: status = %d, want 501 when clusterOps disabled", path, rr.Code)
		}
	}
}

func TestClusterActions_AddNodeCreatesBootstrapToken(t *testing.T) {
	k := clusterActionsClient(t)
	r := chi.NewRouter()
	MountClusterActions(r, k, true, "")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/cluster/nodes:join", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp joinResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.CACertHash, "sha256:") {
		t.Fatalf("caCertHash = %q, want sha256: prefix", resp.CACertHash)
	}
	if resp.Endpoint != "api.test:6443" {
		t.Fatalf("endpoint = %q", resp.Endpoint)
	}
	// token is <6>.<16>
	parts := strings.SplitN(resp.Token, ".", 2)
	if len(parts) != 2 || len(parts[0]) != 6 || len(parts[1]) != 16 {
		t.Fatalf("token = %q, want 6.16 format", resp.Token)
	}
	if !strings.Contains(resp.Command, "kubeadm join api.test:6443 --token") {
		t.Fatalf("command = %q", resp.Command)
	}

	// The bootstrap-token Secret was created in kube-system.
	secs, err := k.Typed.CoreV1().Secrets("kube-system").List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list secrets: %v", err)
	}
	if len(secs.Items) != 1 || !strings.HasPrefix(secs.Items[0].Name, "bootstrap-token-") {
		t.Fatalf("expected one bootstrap-token secret, got %d", len(secs.Items))
	}
	if secs.Items[0].Type != corev1.SecretType("bootstrap.kubernetes.io/token") {
		t.Fatalf("secret type = %q", secs.Items[0].Type)
	}
}

// TestClusterActions_AddNodeUsesExternalAddress covers F-081: the join
// command must use the configured external address, not the in-cluster
// ClusterIP the API's own kube client talks to, when one is set.
func TestClusterActions_AddNodeUsesExternalAddress(t *testing.T) {
	k := clusterActionsClient(t)
	r := chi.NewRouter()
	MountClusterActions(r, k, true, "203.0.113.10:6443")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/cluster/nodes:join", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp joinResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Endpoint != "203.0.113.10:6443" {
		t.Fatalf("endpoint = %q, want the configured external address, not the in-cluster host", resp.Endpoint)
	}
	if !strings.Contains(resp.Command, "kubeadm join 203.0.113.10:6443 --token") {
		t.Fatalf("command = %q", resp.Command)
	}
}

// TestClusterActions_KubeconfigUsesExternalAddress covers F-081 for the
// downloaded kubeconfig's "server:" field.
func TestClusterActions_KubeconfigUsesExternalAddress(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("get", "certificatesigningrequests", signedCSRReactor)
	k := &kube.Client{
		Typed: cs,
		Config: &rest.Config{
			Host:            "https://10.0.0.1:6443",
			TLSClientConfig: rest.TLSClientConfig{CAData: testCAPEM(t)},
		},
	}
	r := chi.NewRouter()
	MountClusterActions(r, k, true, "k8s.example.com:6443")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/cluster/kubeconfig", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "server: https://k8s.example.com:6443") {
		t.Fatalf("kubeconfig should use the external address, got:\n%s", body)
	}
	if strings.Contains(body, "10.0.0.1") {
		t.Fatalf("kubeconfig should not leak the in-cluster host once an external address is set:\n%s", body)
	}
}
