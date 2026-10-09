package kube

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/streaming/pkg/httpstream"
	httpstreamspdy "k8s.io/streaming/pkg/httpstream/spdy"

	"github.com/GameplanePanel/gameplane/netguard"
)

func standaloneSPDYFixture(t *testing.T, handler http.Handler) (*Client, *RemoteAccessPolicy, *url.URL) {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ca := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	cfg, err := ValidateStandaloneKubeconfig(standaloneConfigInput("https://cluster.example:"+port, fmt.Sprintf(`,"certificate-authority-data":%q,"tls-server-name":"127.0.0.1"`, ca)))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewRemoteAccessPolicy([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	policy.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("10.0.0.1")}}, nil
	}
	policy.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != net.JoinHostPort("10.0.0.1", port) {
			return nil, errors.New("unexpected validated destination")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	ApplyStandaloneRemotePolicy(cfg, policy)
	client, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(cfg.Host + "/api/v1/namespaces/games/pods/game/attach?stdout=true")
	if err != nil {
		t.Fatal(err)
	}
	return client, policy, target
}

func TestStandaloneSPDYExecutorRejectsDisallowedDNSBeforeDial(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "169.254.169.254", "192.168.1.1"} {
		t.Run(address, func(t *testing.T) {
			client, policy, target := standaloneSPDYFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "unexpected connection", http.StatusInternalServerError)
			}))
			policy.resolve = func(context.Context, string) ([]net.IPAddr, error) {
				return []net.IPAddr{{IP: net.ParseIP(address)}}, nil
			}
			var dials atomic.Int32
			policy.dial = func(context.Context, string, string) (net.Conn, error) {
				dials.Add(1)
				return nil, errors.New("unexpected dial")
			}
			executor, err := client.NewSPDYExecutor(http.MethodPost, target)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: io.Discard})
			// client-go SPDY negotiation stringifies transport errors with %v.
			// Verify its blocked-destination cause and that no dial occurred.
			if err == nil || !strings.HasSuffix(err.Error(), netguard.ErrBlockedAddr.Error()) || dials.Load() != 0 {
				t.Fatalf("SPDY bypassed destination guard: error=%v dials=%d", err, dials.Load())
			}
		})
	}
}

func TestStandaloneSPDYExecutorTrustedUpgradeUsesPolicyAndTLS(t *testing.T) {
	var observed atomic.Bool
	payload := strings.Repeat("trusted output", 100*1024)
	client, _, target := standaloneSPDYFixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer private-token" || req.Header.Get("Upgrade") != "SPDY/3.1" {
			http.Error(w, "missing authenticated upgrade", http.StatusBadRequest)
			return
		}
		observed.Store(true)
		w.Header().Set(httpstream.HeaderProtocolVersion, "v4.channel.k8s.io")
		connection := httpstreamspdy.NewResponseUpgrader().UpgradeResponse(w, req, func(stream httpstream.Stream, replySent <-chan struct{}) error {
			go func() {
				<-replySent
				if stream.Headers().Get(corev1.StreamType) == corev1.StreamTypeStdout {
					_, _ = io.WriteString(stream, payload)
				}
				_ = stream.Close()
			}()
			return nil
		})
		if connection != nil {
			defer connection.Close()
			<-connection.CloseChan()
		}
	}))
	executor, err := client.NewSPDYExecutor(http.MethodPost, target)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	var output bytes.Buffer
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &output})
	if err != nil || !observed.Load() || output.String() != payload {
		t.Fatalf("trusted SPDY upgrade failed: error=%v observed=%v output bytes=%d", err, observed.Load(), output.Len())
	}
}

func TestStandaloneSPDYExecutorSystemRootsRejectUntrustedTLSBeforeAuthentication(t *testing.T) {
	var observed atomic.Bool
	client, policy, target := standaloneSPDYFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		observed.Store(true)
		http.Error(w, "unexpected authenticated request", http.StatusBadRequest)
	}))
	// A kubeconfig without custom TLS fields uses system trust roots. In this
	// case rest.TLSConfigFor returns nil, which SPDY must not treat as insecure.
	client.Config.CAData = nil
	client.Config.ServerName = ""
	var dials atomic.Int32
	dial := policy.dial
	policy.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return dial(ctx, network, address)
	}
	executor, err := client.NewSPDYExecutor(http.MethodPost, target)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "certificate") || dials.Load() != 1 || observed.Load() {
		t.Fatalf("system-root SPDY request bypassed TLS verification: error=%v dials=%d handler reached=%v", err, dials.Load(), observed.Load())
	}
}

func TestStandaloneSPDYExecutorRejectsDNSRebindingAfterRESTRequest(t *testing.T) {
	client, policy, target := standaloneSPDYFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{}`)
	}))
	var resolutions atomic.Int32
	policy.resolve = func(context.Context, string) ([]net.IPAddr, error) {
		address := "10.0.0.1"
		if resolutions.Add(1) > 1 {
			address = "127.0.0.1"
		}
		return []net.IPAddr{{IP: net.ParseIP(address)}}, nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := client.Typed.Discovery().RESTClient().Get().AbsPath("/version").Do(ctx).Raw(); err != nil {
		t.Fatal(err)
	}
	executor, err := client.NewSPDYExecutor(http.MethodPost, target)
	if err != nil {
		t.Fatal(err)
	}
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: io.Discard})
	if err == nil || !strings.HasSuffix(err.Error(), netguard.ErrBlockedAddr.Error()) || resolutions.Load() != 2 {
		t.Fatalf("SPDY reused unvalidated rebound DNS: error=%v resolutions=%d", err, resolutions.Load())
	}
}

func TestStandaloneSPDYExecutorBoundsPreUpgradeErrors(t *testing.T) {
	client, _, target := standaloneSPDYFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, strings.Repeat("x", remoteResponseLimit+1024))
	}))
	executor, err := client.NewSPDYExecutor(http.MethodPost, target)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "unable to read error") || len(err.Error()) > 1024 {
		t.Fatalf("SPDY buffered or reflected oversized error: length=%d", len(fmt.Sprint(err)))
	}
}

func TestStandaloneSPDYExecutorBoundsUpgradeHeaders(t *testing.T) {
	client, _, target := standaloneSPDYFixture(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		connection, buffered, err := hijacker.Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		_, _ = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nX-Padding: " + strings.Repeat("x", remoteResponseLimit) + "\r\n\r\n")
		_ = buffered.Flush()
	}))
	executor, err := client.NewSPDYExecutor(http.MethodPost, target)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: io.Discard})
	if err == nil || !strings.Contains(err.Error(), "observation limit") || len(err.Error()) > 1024 {
		t.Fatalf("SPDY accepted or reflected oversized headers: error length=%d", len(fmt.Sprint(err)))
	}
}

func TestStandaloneSPDYExecutorBoundsUpgradedErrorStream(t *testing.T) {
	client, _, target := standaloneSPDYFixture(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set(httpstream.HeaderProtocolVersion, "v4.channel.k8s.io")
		connection := httpstreamspdy.NewResponseUpgrader().UpgradeResponse(w, req, func(stream httpstream.Stream, replySent <-chan struct{}) error {
			go func() {
				<-replySent
				if stream.Headers().Get(corev1.StreamType) == corev1.StreamTypeError {
					_, _ = io.WriteString(stream, strings.Repeat("x", remoteVersionLimit+1024))
				}
				_ = stream.Close()
			}()
			return nil
		})
		if connection != nil {
			defer connection.Close()
			<-connection.CloseChan()
		}
	}))
	executor, err := client.NewSPDYExecutor(http.MethodPost, target)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: io.Discard})
	if !errors.Is(err, errRemoteResponseTooLarge) || len(err.Error()) > 1024 {
		t.Fatalf("SPDY accepted or reflected oversized control stream: error length=%d", len(fmt.Sprint(err)))
	}
}
