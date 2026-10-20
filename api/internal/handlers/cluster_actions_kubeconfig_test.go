package handlers

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	certv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func TestRenderKubeconfig(t *testing.T) {
	out := renderKubeconfig("https://api.test:6443", []byte("CA"), []byte("CERT"), []byte("KEY"), "alice")
	b64 := base64.StdEncoding.EncodeToString
	for _, want := range []string{
		"kind: Config",
		"server: https://api.test:6443",
		"certificate-authority-data: " + b64([]byte("CA")),
		"client-certificate-data: " + b64([]byte("CERT")),
		"client-key-data: " + b64([]byte("KEY")),
		"user: alice",
		"name: alice",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("kubeconfig missing %q:\n%s", want, out)
		}
	}
}

// signedCSRReactor injects status.certificate on Get so signClientCert
// returns immediately — the fake signer never populates it on its own.
func signedCSRReactor(a k8stesting.Action) (bool, runtime.Object, error) {
	name := a.(k8stesting.GetAction).GetName()
	return true, &certv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: certv1.CertificateSigningRequestStatus{
			Certificate: []byte("-----BEGIN CERTIFICATE-----\nsigned\n-----END CERTIFICATE-----\n"),
		},
	}, nil
}

func TestClusterActions_KubeconfigHappyPath(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("get", "certificatesigningrequests", signedCSRReactor)
	k := &kube.Client{
		Typed: cs,
		Config: &rest.Config{
			Host:            "https://api.test:6443",
			TLSClientConfig: rest.TLSClientConfig{CAData: testCAPEM(t)},
		},
	}
	r := chi.NewRouter()
	MountClusterActions(r, k, true, "")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/cluster/kubeconfig", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Fatalf("content-type = %q, want application/yaml", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); !strings.Contains(cd, "gameplane-kubeconfig.yaml") {
		t.Fatalf("content-disposition = %q", cd)
	}
	body := rr.Body.String()
	for _, want := range []string{"kind: Config", "gameplane-admin", "server: https://api.test:6443"} {
		if !strings.Contains(body, want) {
			t.Fatalf("kubeconfig missing %q:\n%s", want, body)
		}
	}
}

func TestClusterActions_KubeconfigCSRCreateError(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "certificatesigningrequests", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("apiserver rejected the CSR")
	})
	k := &kube.Client{
		Typed: cs,
		Config: &rest.Config{
			Host:            "https://api.test:6443",
			TLSClientConfig: rest.TLSClientConfig{CAData: testCAPEM(t)},
		},
	}
	r := chi.NewRouter()
	MountClusterActions(r, k, true, "")

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/cluster/kubeconfig", nil))
	if rr.Code == http.StatusOK {
		t.Fatalf("expected an error status when CSR creation fails, got 200: %s", rr.Body.String())
	}
}
