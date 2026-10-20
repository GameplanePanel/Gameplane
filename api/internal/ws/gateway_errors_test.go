package ws

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

type gatewayErrorTransport struct{ err error }

func (tr gatewayErrorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, tr.err
}

func TestGatewayClientCertificatePreservesCause(t *testing.T) {
	resolver, home, _ := gatewayFixture(t, "https://gateway.test")
	secret, err := home.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "gateway-client", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Data["tls.key"] = []byte("invalid-key-material")
	if _, err := home.Typed.CoreV1().Secrets("gameplane-system").Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	_, _, err = resolver.resolve(t.Context(), "remote", agentTarget{name: "alpha", namespace: "gameplane-games"})
	if err == nil || errors.Unwrap(err) == nil || !strings.HasPrefix(err.Error(), "invalid gateway client certificate or key:") {
		t.Fatalf("certificate parse cause lost: %v", err)
	}
	if strings.Contains(err.Error(), "invalid-key-material") {
		t.Fatal("certificate error exposed private key material")
	}
}

func TestGatewayRequestErrorContextPreservesClassification(t *testing.T) {
	for _, cause := range []error{context.Canceled, fakeTimeoutErr{}} {
		transport := &gatewayAgentTransport{
			base:   &url.URL{Scheme: "https", Host: "gateway.test"},
			target: gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "alpha", UID: "remote-uid"},
			http:   &http.Client{Transport: gatewayErrorTransport{err: cause}},
		}
		resp, err := transport.Do(t.Context(), agentRequest{target: agentTarget{name: "alpha", namespace: "games"}, method: http.MethodGet, path: "/status"})
		if resp != nil {
			defer resp.Body.Close()
		}
		if err == nil || !strings.HasPrefix(err.Error(), "send gateway agent request:") || !errors.Is(err, cause) {
			t.Fatalf("request context or cause lost: %v", err)
		}
		want := http.StatusBadGateway
		var timeout net.Error
		if errors.As(cause, &timeout) {
			want = http.StatusGatewayTimeout
			if !errors.As(err, &timeout) || !timeout.Timeout() {
				t.Fatalf("timeout classification lost: %v", err)
			}
		}
		rr := httptest.NewRecorder()
		writeUpstreamErr(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/players", nil), err)
		if rr.Code != want || rr.Body.String() != "agent unreachable\n" {
			t.Fatalf("public error changed: status=%d body=%s", rr.Code, rr.Body)
		}
	}
}

func TestCaptureGatewayErrorsPreserveStageAndCause(t *testing.T) {
	resolver, _, _ := gatewayFixture(t, "https://gateway.test")
	client := &CaptureGatewayClient{resolver: resolver}
	target := gatewayprotocol.CaptureTarget{Target: gatewayprotocol.Target{Cluster: "remote", Namespace: "gameplane-games", Name: "alpha", UID: "remote-uid"}, Capture: "cap-one", CaptureUID: "capture-uid"}
	bad := target
	bad.CaptureUID = "../bad"
	resp, err := client.Delete(t.Context(), bad)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil || errors.Unwrap(err) == nil || !strings.HasPrefix(err.Error(), "build gateway capture path:") {
		t.Fatalf("missing capture path context: %v", err)
	}
	bad = target
	bad.Cluster = "missing"
	resp, err = client.Delete(t.Context(), bad)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil || !errors.Is(err, scope.ErrForbiddenCluster) || !strings.HasPrefix(err.Error(), "resolve gateway capture target:") {
		t.Fatalf("missing capture resolution context: %v", err)
	}
	if _, err := client.Capabilities(t.Context(), "missing", "gameplane-games", "alpha"); err == nil || !errors.Is(err, scope.ErrForbiddenCluster) || !strings.HasPrefix(err.Error(), "resolve gateway capability target:") {
		t.Fatalf("missing capability resolution context: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	resp, err = client.Delete(ctx, target)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err == nil || !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "send gateway capture request:") {
		t.Fatalf("missing capture request context: %v", err)
	}
}
