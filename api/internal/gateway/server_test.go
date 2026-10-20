package gateway

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	typedfake "k8s.io/client-go/kubernetes/fake"

	"github.com/coder/websocket"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

const testPeerURI = "spiffe://gameplane.test/central"

func credentials(t *testing.T, expires time.Time) (TLSFiles, *x509.Certificate, tls.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := url.Parse(testPeerURI)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "test-peer"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: expires,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true, URIs: []*url.URL{identity}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	files := TLSFiles{Certificate: filepath.Join(t.TempDir(), "tls.crt"), Key: filepath.Join(t.TempDir(), "tls.key"), CA: filepath.Join(t.TempDir(), "ca.crt")}
	for path, content := range map[string][]byte{files.Certificate: certPEM, files.Key: keyPEM, files.CA: certPEM} {
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return files, cert, pair
}

func targetClient(serverUID, ownerUID string) *kube.Client {
	server := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gameplane.local/v1alpha1", "kind": "GameServer", "metadata": map[string]any{"name": "same-name", "namespace": "games", "uid": serverUID}}}
	owner := true
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "same-name-agent", Namespace: "games", OwnerReferences: []metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: "same-name", Controller: &owner}}}}
	service.OwnerReferences[0].UID = server.GetUID()
	if ownerUID != serverUID {
		service.OwnerReferences[0].UID = "different-owner"
	}
	return &kube.Client{Dynamic: fake.NewSimpleDynamicClient(runtime.NewScheme(), server), Typed: typedfake.NewSimpleClientset(service)}
}

func newFixture(t *testing.T) (*handler, *x509.Certificate) {
	t.Helper()
	files, cert, _ := credentials(t, time.Now().Add(time.Hour))
	h, err := NewHandler(Config{Cluster: "remote", Namespaces: []string{"games"}, PeerURI: testPeerURI, TLS: files, AgentTLS: files, MaxRequestDuration: time.Minute}, targetClient("original-uid", "original-uid"))
	if err != nil {
		t.Fatal(err)
	}
	return h.(*handler), cert
}

func request(t *testing.T, cert *x509.Certificate, method, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	return req
}

func TestGatewayRejectsUntrustedAndMisdirectedRequests(t *testing.T) {
	for _, tc := range []struct {
		name, cluster, ns, uid, serviceUID string
		authenticated                      bool
		method, operation                  string
		status                             int
	}{
		{"no mTLS", "remote", "games", "original-uid", "original-uid", false, http.MethodGet, "/status", 401},
		{"wrong cluster", "local", "games", "original-uid", "original-uid", true, http.MethodGet, "/status", 404},
		{"forbidden namespace", "remote", "other", "original-uid", "original-uid", true, http.MethodGet, "/status", 404},
		{"stale UID", "remote", "games", "stale-uid", "original-uid", true, http.MethodGet, "/status", 404},
		{"foreign service", "remote", "games", "original-uid", "foreign-uid", true, http.MethodGet, "/status", 404},
		{"unsupported method", "remote", "games", "original-uid", "original-uid", true, http.MethodPost, "/status", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cert := newFixture(t)
			h.client = targetClient("original-uid", tc.serviceUID)
			path, err := gatewayprotocol.Path(gatewayprotocol.Target{Cluster: tc.cluster, Namespace: tc.ns, Name: "same-name", UID: tc.uid}, tc.operation)
			if err != nil {
				t.Fatal(err)
			}
			req := request(t, cert, tc.method, path)
			if !tc.authenticated {
				req.TLS = nil
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestGatewayForwardsOnlyBoundTargetAndSafeHeaders(t *testing.T) {
	h, cert := newFixture(t)
	var called atomic.Bool
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called.Store(true)
		if req.URL.Path != "/v1/targets/original-uid/files/write" {
			t.Errorf("upstream path=%s", req.URL.Path)
		}
		if req.URL.RawQuery != "path=%2Fconfig.txt" {
			t.Errorf("query=%s", req.URL.RawQuery)
		}
		for _, header := range []string{"Cookie", "Authorization", "X-Gameplane-Csrf", "X-Forwarded-For", "Idempotency-Key"} {
			if req.Header.Get(header) != "" {
				t.Errorf("leaked %s", header)
			}
		}
		body, err := io.ReadAll(req.Body)
		if err != nil || string(body) != "setting=true" {
			t.Errorf("body=%s err=%v", body, err)
		}
		w.Header().Set("Set-Cookie", "bad=secret")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	h.transport = func(TLSFiles) (*http.Transport, error) {
		transport := upstream.Client().Transport.(*http.Transport).Clone()
		transport.TLSClientConfig.ServerName = "127.0.0.1"
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}
		return transport, nil
	}
	path, err := gatewayprotocol.Path(gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "original-uid"}, "/files/write")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path+"?path=%2Fconfig.txt", strings.NewReader("setting=true"))
	req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	for _, header := range []string{"Cookie", "Authorization", "X-Gameplane-Csrf", "X-Forwarded-For", "Idempotency-Key"} {
		req.Header.Set(header, "secret")
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if !called.Load() || response.Code != http.StatusNoContent {
		t.Fatalf("called=%v status=%d body=%s", called.Load(), response.Code, response.Body)
	}
	if response.Header().Get("Set-Cookie") != "" {
		t.Fatal("agent cookie leaked")
	}
}

func TestPeerTrustReloadRejectsExistingConnection(t *testing.T) {
	h, cert := newFixture(t)
	req := request(t, cert, http.MethodGet, "/v1/capabilities")
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code)
	}
	replacement, _, _ := credentials(t, time.Now().Add(time.Hour))
	raw, err := os.ReadFile(replacement.CA)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.cfg.TLS.CA, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked peer status=%d", response.Code)
	}
}

func TestTLSRejectsWrongPeerIdentityAndExpiredCertificate(t *testing.T) {
	files, cert, _ := credentials(t, time.Now().Add(time.Hour))
	state := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	if err := checkPeer(state, files.CA, testPeerURI); err != nil {
		t.Fatal(err)
	}
	if err := checkPeer(state, files.CA, "spiffe://gameplane.test/other"); err == nil {
		t.Fatal("wrong URI accepted")
	}
	expiredFiles, expired, _ := credentials(t, time.Now().Add(-time.Minute))
	if err := checkPeer(tls.ConnectionState{PeerCertificates: []*x509.Certificate{expired}}, expiredFiles.CA, testPeerURI); err == nil {
		t.Fatal("expired certificate accepted")
	}
	config, err := ServerTLS(files, testPeerURI)
	if err != nil {
		t.Fatal(err)
	}
	if config.ClientAuth != tls.RequireAndVerifyClientCert || !config.SessionTicketsDisabled {
		t.Fatal("gateway must require fresh peer verification")
	}
	if _, err := ServerTLS(files, "not-an-identity"); err == nil {
		t.Fatal("invalid peer identity accepted")
	}
}

func TestGatewayWebSocketUsesUIDAndClosesAtDeadline(t *testing.T) {
	h, cert := newFixture(t)
	h.cfg.MaxRequestDuration = time.Second
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/targets/original-uid/console" {
			t.Errorf("stream target=%s", req.URL.Path)
		}
		conn, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		kind, data, err := conn.Read(req.Context())
		if err != nil {
			return
		}
		if err := conn.Write(req.Context(), kind, data); err != nil {
			return
		}
		_, _, _ = conn.Read(req.Context())
	}))
	defer upstream.Close()
	h.transport = func(TLSFiles) (*http.Transport, error) {
		transport := upstream.Client().Transport.(*http.Transport).Clone()
		transport.TLSClientConfig.ServerName = "127.0.0.1"
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}
		return transport, nil
	}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
		h.ServeHTTP(w, req)
	}))
	defer front.Close()
	path, err := gatewayprotocol.Path(gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "original-uid"}, "/console")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(front.URL, "http")+path, nil)
	if response != nil && response.Body != nil {
		defer response.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if err := conn.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != "hello" {
		t.Fatalf("echo=%s err=%v", data, err)
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("stream outlived its bounded lifetime")
	}
	if ctx.Err() != nil {
		t.Fatal("gateway did not close stream before caller timeout")
	}
}

func TestGatewayRejectsOversizedAndEncodedRequestsBeforeForwarding(t *testing.T) {
	h, cert := newFixture(t)
	path, err := gatewayprotocol.Path(gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "original-uid"}, "/actions/run")
	if err != nil {
		t.Fatal(err)
	}
	req := request(t, cert, http.MethodPost, path)
	req.ContentLength = (16 << 10) + 1
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d", response.Code)
	}
	req = request(t, cert, http.MethodPost, strings.Replace(path, "same-name", "same%2dname", 1))
	response = httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound {
		t.Fatalf("encoded target status=%d", response.Code)
	}
}

func TestGatewayTLSListenerRequiresEnrolledClient(t *testing.T) {
	h, _ := newFixture(t)
	config, err := ServerTLS(h.cfg.TLS, testPeerURI)
	if err != nil {
		t.Fatal(err)
	}
	listener := httptest.NewUnstartedServer(h)
	listener.TLS = config
	listener.StartTLS()
	defer listener.Close()
	roots, err := readPool(h.cfg.TLS.CA)
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	anonymousRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, listener.URL+"/v1/capabilities", nil)
	if err != nil {
		t.Fatal(err)
	}
	anonymousResponse, anonymousErr := client.Do(anonymousRequest)
	if anonymousResponse != nil {
		anonymousResponse.Body.Close()
	}
	if anonymousErr == nil {
		t.Fatal("TLS accepted a client without an enrolled certificate")
	}
	peer, err := tls.LoadX509KeyPair(h.cfg.TLS.Certificate, h.cfg.TLS.Key)
	if err != nil {
		t.Fatal(err)
	}
	enrolledTransport := transport.Clone()
	enrolledTransport.TLSClientConfig.Certificates = []tls.Certificate{peer}
	defer enrolledTransport.CloseIdleConnections()
	enrolled := &http.Client{Transport: enrolledTransport, Timeout: time.Second}
	enrolledRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, listener.URL+"/v1/capabilities", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := enrolled.Do(enrolledRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("enrolled peer status=%d", response.StatusCode)
	}
}
