package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTLSMaterial(t *testing.T) (caPath, certPath, keyPath string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "x"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true, IsCA: true,
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	keyDER, _ := x509.MarshalECPrivateKey(priv)
	dir := t.TempDir()
	certPath = filepath.Join(dir, "c.pem")
	keyPath = filepath.Join(dir, "k.pem")
	caPath = filepath.Join(dir, "ca.pem")
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	_ = os.WriteFile(certPath, pemCert, 0o600)
	_ = os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	_ = os.WriteFile(caPath, pemCert, 0o600)
	return
}

func TestNew_DisabledWhenAllEmpty(t *testing.T) {
	c, err := New(Config{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !c.Disabled {
		t.Fatal("expected disabled")
	}
}

func TestNew_PartialIsRejected(t *testing.T) {
	_, err := New(Config{CABundle: "/x"})
	if err == nil || !strings.Contains(err.Error(), "partial mTLS") {
		t.Fatalf("got %v", err)
	}
}

func TestNew_BadCert(t *testing.T) {
	_, err := New(Config{CABundle: "/a", ClientCert: "/b", ClientKey: "/c"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNew_BadCABundle(t *testing.T) {
	_, certPath, keyPath := writeTLSMaterial(t)
	bogus := filepath.Join(t.TempDir(), "bogus")
	_ = os.WriteFile(bogus, []byte("not a cert"), 0o600)
	if _, err := New(Config{CABundle: bogus, ClientCert: certPath, ClientKey: keyPath}); err == nil ||
		!strings.Contains(err.Error(), "empty CA") {
		t.Fatalf("got %v", err)
	}
}

func TestNew_MissingCAFile(t *testing.T) {
	_, certPath, keyPath := writeTLSMaterial(t)
	if _, err := New(Config{CABundle: "/no/such", ClientCert: certPath, ClientKey: keyPath}); err == nil {
		t.Fatal("expected error")
	}
}

func TestNew_Success_DefaultTimeout(t *testing.T) {
	caPath, certPath, keyPath := writeTLSMaterial(t)
	c, err := New(Config{CABundle: caPath, ClientCert: certPath, ClientKey: keyPath})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if c.Disabled {
		t.Fatal("expected enabled")
	}
	if c.http.Timeout != 30*time.Second {
		t.Fatalf("timeout=%v", c.http.Timeout)
	}
}

func TestNew_Success_OverrideTimeout(t *testing.T) {
	caPath, certPath, keyPath := writeTLSMaterial(t)
	c, _ := New(Config{
		CABundle: caPath, ClientCert: certPath, ClientKey: keyPath, Timeout: 5 * time.Second,
	})
	if c.http.Timeout != 5*time.Second {
		t.Fatalf("timeout=%v", c.http.Timeout)
	}
}

func TestAgentURL(t *testing.T) {
	got := agentURL("gameplane-games", "alpha", "/quiesce")
	want := "https://alpha-agent.gameplane-games.svc.cluster.local:8090/quiesce"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestPodBoundQuiesceNeverFallsBackToLegacyRoute(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			paths := []string{}
			c, closeServer := newMockClient(t, func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"quiesced":true}`))
			})
			defer closeServer()
			err := c.QuiescePod(t.Context(), "ns", "server", "original-pod")
			if (err == nil) != (status == http.StatusOK) {
				t.Fatalf("status=%d error=%v", status, err)
			}
			if len(paths) != 1 || paths[0] != "/v1/pods/original-pod/quiesce" {
				t.Fatalf("unbound mutation/fallback: %v", paths)
			}
		})
	}
}

func TestPodBoundUnquiesceAndDisabledClient(t *testing.T) {
	c, closeServer := newMockClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/pods/original-pod/unquiesce" {
			t.Errorf("wrong cleanup target: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"quiesced":false}`))
	})
	defer closeServer()
	if err := c.UnquiescePod(t.Context(), "ns", "server", "original-pod"); err != nil {
		t.Fatal(err)
	}
	if err := c.QuiescePod(t.Context(), "ns", "server", ""); err == nil {
		t.Fatal("empty pod UID admitted")
	}
	if err := (&Client{Disabled: true}).QuiescePod(t.Context(), "ns", "server", "pod"); err == nil {
		t.Fatal("disabled client reported identity-bound quiesce")
	}
}

func TestQuiesce_DisabledNoOp(t *testing.T) {
	c := &Client{Disabled: true}
	if err := c.Quiesce(context.Background(), "ns", "srv"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if err := c.Unquiesce(context.Background(), "ns", "srv"); err != nil {
		t.Fatalf("err: %v", err)
	}
}

// callQuiesceTest spins up a mock agent endpoint and points the client
// at it via Transport rewrite (since the real client builds an FQDN we
// can't influence directly).
func newMockClient(t *testing.T, h http.HandlerFunc) (*Client, func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	rewriteTo, _ := url.Parse(srv.URL)
	client := &http.Client{
		Transport: &rewriteTransport{base: http.DefaultTransport, host: rewriteTo.Host, scheme: rewriteTo.Scheme},
		Timeout:   5 * time.Second,
	}
	return &Client{http: client}, srv.Close
}

type rewriteTransport struct {
	base   http.RoundTripper
	host   string
	scheme string
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = rt.scheme
	req2.URL.Host = rt.host
	req2.Host = rt.host
	return rt.base.RoundTrip(req2)
}

func TestStop_DisabledNoOp(t *testing.T) {
	c := &Client{Disabled: true}
	if err := c.Stop(context.Background(), "ns", "srv"); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestStop_HappyPath(t *testing.T) {
	var gotPath string
	c, cleanup := newMockClient(t, func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"stopped": true})
	})
	defer cleanup()
	if err := c.Stop(context.Background(), "ns", "srv"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if gotPath != "/lifecycle/stop" {
		t.Fatalf("unexpected path %q", gotPath)
	}
}

func TestStop_5xx(t *testing.T) {
	c, cleanup := newMockClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	defer cleanup()
	if err := c.Stop(context.Background(), "ns", "srv"); err == nil {
		t.Fatal("expected error on 5xx")
	}
}

func TestQuiesce_HappyPath(t *testing.T) {
	c, cleanup := newMockClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"quiesced": true})
	})
	defer cleanup()
	if err := c.Quiesce(context.Background(), "ns", "srv"); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestQuiesce_UnsupportedGame(t *testing.T) {
	c, cleanup := newMockClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"quiesced": false, "reason": "game does not support quiesce",
		})
	})
	defer cleanup()
	if err := c.Quiesce(context.Background(), "ns", "srv"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v", err)
	}
}

func TestQuiesce_RefusedWithReason(t *testing.T) {
	c, cleanup := newMockClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"quiesced": false, "reason": "rcon error"})
	})
	defer cleanup()
	err := c.Quiesce(context.Background(), "ns", "srv")
	if err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("got %v", err)
	}
}

func TestQuiesce_5xx(t *testing.T) {
	c, cleanup := newMockClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer cleanup()
	if err := c.Quiesce(context.Background(), "ns", "srv"); err == nil ||
		!strings.Contains(err.Error(), "status 500") {
		t.Fatalf("got %v", err)
	}
}

func TestQuiesce_BadJSON(t *testing.T) {
	c, cleanup := newMockClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	defer cleanup()
	if err := c.Quiesce(context.Background(), "ns", "srv"); err == nil ||
		!strings.Contains(err.Error(), "decode") {
		t.Fatalf("got %v", err)
	}
}

func TestUnquiesce_HappyPath(t *testing.T) {
	c, cleanup := newMockClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"quiesced": false})
	})
	defer cleanup()
	if err := c.Unquiesce(context.Background(), "ns", "srv"); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestCheckPodRequiresIdentityEndpoint(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotFound, http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c, cleanup := newMockClient(t, func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.Method != http.MethodGet || req.URL.Path != "/v1/pods/original/identity" {
					t.Errorf("unexpected preflight request: %s %s", req.Method, req.URL.Path)
				}
				w.WriteHeader(status)
			})
			defer cleanup()
			err := c.CheckPod(t.Context(), "ns", "server", "original")
			if (err == nil) != (status == http.StatusNoContent) || calls != 1 {
				t.Fatalf("identity result = %v, calls = %d", err, calls)
			}
		})
	}
	if err := (&Client{Disabled: true}).CheckPod(t.Context(), "ns", "server", "original"); err == nil {
		t.Fatal("disabled client confirmed identity")
	}
	if err := (&Client{}).CheckPod(t.Context(), "ns", "server", ""); err == nil {
		t.Fatal("empty pod UID confirmed identity")
	}
}

func TestQuiesce_TransportError(t *testing.T) {
	// Use a base transport that always errors.
	c := &Client{
		http: &http.Client{Transport: errTransport{}, Timeout: time.Second},
	}
	if err := c.Quiesce(context.Background(), "ns", "srv"); err == nil ||
		!strings.Contains(err.Error(), "/quiesce") {
		t.Fatalf("got %v", err)
	}
}

type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial fail")
}
