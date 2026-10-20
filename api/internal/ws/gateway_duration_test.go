package ws

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"

	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func durationGatewayTransport(t *testing.T, handler http.Handler) *gatewayAgentTransport {
	t.Helper()
	srv := newGatewayTLSTestServer(t, handler)
	t.Cleanup(srv.Close)
	resolver, home, _ := gatewayFixture(t, srv.URL)
	secret, err := home.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "gateway-client", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Data["ca.crt"] = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if _, err := home.Typed.CoreV1().Secrets("gameplane-system").Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	_, transport, err := resolver.resolve(t.Context(), "remote", agentTarget{name: "alpha", namespace: "gameplane-games"})
	if err != nil {
		t.Fatal(err)
	}
	return transport
}

func TestGatewayTransportDelayedHeaders(t *testing.T) {
	for _, path := range []string{"/mods/install", "/mods/upload"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			transport := durationGatewayTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				select {
				case <-time.After(31 * time.Second):
					w.WriteHeader(http.StatusNoContent)
				case <-r.Context().Done():
				}
			}))
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			resp, err := transport.Do(ctx, agentRequest{target: agentTarget{name: "alpha", namespace: "gameplane-games"}, method: http.MethodPost, path: path, body: strings.NewReader("archive")})
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Fatalf("delayed operation returned %d", resp.StatusCode)
			}
		})
	}
}

func TestGatewayTransportKeepsOperationPoliciesIndependent(t *testing.T) {
	transport := durationGatewayTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(150 * time.Millisecond):
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
		}
	}))
	transport.http.Transport.(*http.Transport).ResponseHeaderTimeout = 30 * time.Millisecond
	for _, tc := range []struct {
		path, method    string
		cancel, timeout bool
	}{
		{"/mods/install", http.MethodPost, false, false},
		{"/status", http.MethodGet, false, true},
		{"/mods/upload", http.MethodPost, true, true},
	} {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			resp, err := transport.Do(ctx, agentRequest{target: agentTarget{name: "alpha", namespace: "gameplane-games"}, method: tc.method, path: tc.path})
			if resp != nil {
				defer resp.Body.Close()
			}
			if tc.cancel {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("parent deadline lost: %v", err)
				}
			} else if tc.timeout {
				var timeout net.Error
				if !errors.As(err, &timeout) || !timeout.Timeout() {
					t.Fatalf("ordinary operation must time out: %v", err)
				}
			} else if err != nil || resp.StatusCode != http.StatusNoContent {
				t.Fatalf("long operation failed: response=%v err=%v", resp, err)
			}
		})
	}
}
