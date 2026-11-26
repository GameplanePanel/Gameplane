package archive

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// testPKI holds a throwaway CA with a server and a client certificate.
type testPKI struct {
	serverTLS *tls.Config
	clientTLS *tls.Config
}

func newTestPKI(t *testing.T) testPKI {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)

	serverCert := issueTestCert(t, caCert, caKey, 2, x509.ExtKeyUsageServerAuth, []net.IP{net.IPv4(127, 0, 0, 1)})
	clientCert := issueTestCert(t, caCert, caKey, 3, x509.ExtKeyUsageClientAuth, nil)
	return testPKI{
		serverTLS: &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientCAs:    pool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			MinVersion:   tls.VersionTLS12,
		},
		clientTLS: &tls.Config{
			Certificates: []tls.Certificate{clientCert},
			RootCAs:      pool,
			MinVersion:   tls.VersionTLS12,
		},
	}
}

func issueTestCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, serial int64, usage x509.ExtKeyUsage, ips []net.IP) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func (p testPKI) client() *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:   p.clientTLS,
			DisableKeepAlives: true,
		},
	}
}

// captureListener makes the package listen on 127.0.0.1:0 and reports the
// listener it opened. Tests using it must not run in parallel.
func captureListener(t *testing.T) <-chan net.Listener {
	t.Helper()
	ch := make(chan net.Listener, 1)
	orig := listen
	listen = func(network, _ string) (net.Listener, error) {
		ln, err := orig(network, "127.0.0.1:0")
		if err == nil {
			ch <- ln
		}
		return ln, err
	}
	t.Cleanup(func() { listen = orig })
	return ch
}

// startServe runs Serve in the background and returns its result channel and
// the address it listens on.
func startServe(t *testing.T, ctx context.Context, cfg ServerConfig, h http.Handler) (<-chan error, string) {
	t.Helper()
	lnCh := captureListener(t)
	result := make(chan error, 1)
	go func() { result <- Serve(ctx, cfg, h) }()
	select {
	case ln := <-lnCh:
		return result, ln.Addr().String()
	case err := <-result:
		t.Fatalf("Serve returned before listening: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not open a listener")
	}
	return nil, ""
}

func await(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
		return nil
	}
}

func fetch(c *http.Client, url string) (int, error) {
	resp, err := c.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func noop(http.ResponseWriter, *http.Request) {}

func TestServeServesOnlyExactPath(t *testing.T) {
	pki := newTestPKI(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var hits atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	cfg := ServerConfig{
		Addr:        "127.0.0.1:0",
		TLS:         pki.serverTLS,
		Prefix:      "/api/archive",
		UID:         "uid-1",
		IdleTimeout: 5 * time.Second,
	}
	result, addr := startServe(t, ctx, cfg, h)
	c := pki.client()
	for _, p := range []string{"/", "/api/archive", "/api/archive/uid-2", "/api/archive/uid-1/extra", "/api/archive/uid-1/"} {
		code, err := fetch(c, "https://"+addr+p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		if code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", p, code)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("handler ran %d times for wrong paths", n)
	}
	code, err := fetch(c, "https://"+addr+"/api/archive/uid-1")
	if err != nil {
		t.Fatalf("GET exact path: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("GET exact path: status %d, want 200", code)
	}
	if err := await(t, result); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeImplicitOKCountsAsSuccess(t *testing.T) {
	pki := newTestPKI(t)
	cfg := ServerConfig{Addr: "127.0.0.1:0", TLS: pki.serverTLS, UID: "u", IdleTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, addr := startServe(t, ctx, cfg, http.HandlerFunc(noop))
	code, err := fetch(pki.client(), "https://"+addr+"/u")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if code != http.StatusOK {
		t.Errorf("status %d, want 200", code)
	}
	if err := await(t, result); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeErrorStatusKeepsServing(t *testing.T) {
	pki := newTestPKI(t)
	var calls atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	cfg := ServerConfig{Addr: "127.0.0.1:0", TLS: pki.serverTLS, UID: "u", IdleTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, addr := startServe(t, ctx, cfg, h)
	c := pki.client()
	code, err := fetch(c, "https://"+addr+"/u")
	if err != nil || code != http.StatusInternalServerError {
		t.Fatalf("first GET = %d, %v; want 500", code, err)
	}
	code, err = fetch(c, "https://"+addr+"/u")
	if err != nil || code != http.StatusOK {
		t.Fatalf("second GET = %d, %v; want 200", code, err)
	}
	if err := await(t, result); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeIdleAfterFailedRequest(t *testing.T) {
	pki := newTestPKI(t)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	cfg := ServerConfig{Addr: "127.0.0.1:0", TLS: pki.serverTLS, UID: "u", IdleTimeout: 300 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, addr := startServe(t, ctx, cfg, h)
	code, err := fetch(pki.client(), "https://"+addr+"/u")
	if err != nil || code != http.StatusInternalServerError {
		t.Fatalf("GET = %d, %v; want 500", code, err)
	}
	if err := await(t, result); !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("Serve error = %v, want ErrIdleTimeout", err)
	}
}

func TestServeIdleTimeoutWithoutRequests(t *testing.T) {
	pki := newTestPKI(t)
	cfg := ServerConfig{Addr: "127.0.0.1:0", TLS: pki.serverTLS, UID: "u", IdleTimeout: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, _ := startServe(t, ctx, cfg, http.HandlerFunc(noop))
	if err := await(t, result); !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("Serve error = %v, want ErrIdleTimeout", err)
	}
}

func TestServeLongRequestIsNotIdle(t *testing.T) {
	pki := newTestPKI(t)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(400 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	cfg := ServerConfig{Addr: "127.0.0.1:0", TLS: pki.serverTLS, UID: "u", IdleTimeout: 100 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, addr := startServe(t, ctx, cfg, h)
	code, err := fetch(pki.client(), "https://"+addr+"/u")
	if err != nil || code != http.StatusOK {
		t.Fatalf("GET = %d, %v; want 200", code, err)
	}
	if err := await(t, result); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeConcurrentRequestGets409(t *testing.T) {
	pki := newTestPKI(t)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	})
	cfg := ServerConfig{Addr: "127.0.0.1:0", TLS: pki.serverTLS, UID: "u", IdleTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result, addr := startServe(t, ctx, cfg, h)
	url := "https://" + addr + "/u"
	c := pki.client()

	first := make(chan int, 1)
	go func() {
		code, err := fetch(c, url)
		if err != nil {
			t.Errorf("first GET: %v", err)
		}
		first <- code
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler was not reached")
	}
	code, err := fetch(c, url)
	if err != nil || code != http.StatusConflict {
		t.Fatalf("concurrent GET = %d, %v; want 409", code, err)
	}
	close(release)
	if code := <-first; code != http.StatusOK {
		t.Errorf("first GET status %d, want 200", code)
	}
	if err := await(t, result); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeContextCancel(t *testing.T) {
	pki := newTestPKI(t)
	cfg := ServerConfig{Addr: "127.0.0.1:0", TLS: pki.serverTLS, UID: "u", IdleTimeout: time.Minute}
	ctx, cancel := context.WithCancel(context.Background())
	result, _ := startServe(t, ctx, cfg, http.HandlerFunc(noop))
	cancel()
	if err := await(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve error = %v, want context.Canceled", err)
	}
}

func TestServeRequiresTLS(t *testing.T) {
	cfg := ServerConfig{Addr: "127.0.0.1:0", UID: "u", IdleTimeout: time.Second}
	if err := Serve(context.Background(), cfg, http.HandlerFunc(noop)); err == nil {
		t.Fatal("Serve without TLS config: want error")
	}
}

func TestServeListenError(t *testing.T) {
	errBind := errors.New("address in use")
	orig := listen
	listen = func(string, string) (net.Listener, error) { return nil, errBind }
	t.Cleanup(func() { listen = orig })

	pki := newTestPKI(t)
	cfg := ServerConfig{Addr: "127.0.0.1:1", TLS: pki.serverTLS, UID: "u", IdleTimeout: time.Second}
	if err := Serve(context.Background(), cfg, http.HandlerFunc(noop)); !errors.Is(err, errBind) {
		t.Fatalf("Serve error = %v, want wrapped bind error", err)
	}
}
