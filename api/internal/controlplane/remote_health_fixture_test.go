package controlplane

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Use a real private-network address and verified TLS so the watcher exercises
// the production destination guard without permitting loopback or external I/O.
func privateTLSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	listener, ip := privateTestListener(t)
	t.Cleanup(func() { _ = listener.Close() })
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IPAddresses:           []net.IP{ip},
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	_ = server.Listener.Close()
	server.Listener = listener
	server.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		Certificates: []tls.Certificate{{
			Certificate: [][]byte{der},
			PrivateKey:  key,
		}},
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func privateTestListener(t *testing.T) (net.Listener, net.IP) {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			t.Fatalf("inspect private test interface: %v", err)
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err != nil || ip.To4() == nil || !ip.IsPrivate() || ip.IsLoopback() {
				continue
			}
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp4", net.JoinHostPort(ip.String(), "0"))
			if err != nil {
				t.Fatalf("bind private remote-health test listener: %v", err)
			}
			return listener, ip
		}
	}
	t.Fatal("remote health test requires an up interface with a private IPv4 address")
	return nil, nil
}
