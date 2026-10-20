package ws

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func captureGatewayFixture(t *testing.T, server *httptest.Server) (*CaptureGatewayClient, *kube.Client, *kube.Client) {
	t.Helper()
	r, home, remote := gatewayFixture(t, server.URL)
	secret, err := home.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "gateway-client", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.Data["ca.crt"] = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if _, err := home.Typed.CoreV1().Secrets("gameplane-system").Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	return &CaptureGatewayClient{resolver: r}, home, remote
}

func TestCaptureCapabilitiesAreVersionedBoundedAndCancelable(t *testing.T) {
	for _, tc := range []struct {
		name, body         string
		status             int
		supported, wantErr bool
	}{
		{"supported", `{"protocol":"v1","targetUID":true,"captureFiles":true,"captureEnabled":false,"maxRetentionSeconds":120}`, 200, true, false},
		{"old", `{"protocol":"v1","targetUID":true}`, 200, false, false},
		{"unknown", `{"protocol":"v2","targetUID":true,"captureFiles":true}`, 200, false, false},
		{"unbound", `{"protocol":"v1","captureFiles":true}`, 200, false, false},
		{"missing", "", 404, false, false},
		{"oversized", strings.Repeat(" ", 4097), 200, false, true},
		{"malformed", "{", 200, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newGatewayTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/capabilities" || r.Header.Get("Cookie") != "" {
					t.Error("wrong capability request")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			client, _, _ := captureGatewayFixture(t, srv)
			caps, err := client.Capabilities(t.Context(), "remote", "gameplane-games", "alpha")
			if (err != nil) != tc.wantErr || caps.CaptureFiles != tc.supported {
				t.Fatalf("caps=%+v err=%v", caps, err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := client.Capabilities(ctx, "remote", "gameplane-games", "alpha"); err == nil {
				t.Error("canceled probe succeeded")
			}
		})
	}
}

func TestCaptureGatewayUsesFreshIdentityAndCredentials(t *testing.T) {
	calls := 0
	srv := newGatewayTLSTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		target, err := gatewayprotocol.ParseCapturePath(r.URL.Path)
		if err != nil || target.UID != "remote-uid" || target.CaptureUID != "capture-uid" {
			t.Errorf("target=%+v err=%v", target, err)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("browser credentials forwarded")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	client, home, remote := captureGatewayFixture(t, srv)
	target := gatewayprotocol.CaptureTarget{Target: gatewayprotocol.Target{Cluster: "remote", Namespace: "gameplane-games", Name: "alpha", UID: "remote-uid"}, Capture: "cap-one", CaptureUID: "capture-uid"}
	resp, err := client.Download(t.Context(), target, http.Header{"Cookie": {"secret"}, "Authorization": {"secret"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	gs, err := remote.Dynamic.Resource(kube.GVRs["servers"]).Namespace("gameplane-games").Get(t.Context(), "alpha", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	gs.SetUID("replacement")
	if _, err := remote.Dynamic.Resource(kube.GVRs["servers"]).Namespace("gameplane-games").Update(t.Context(), gs, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if response, err := client.Delete(t.Context(), target); err == nil {
		if response != nil {
			_ = response.Body.Close()
		}
		t.Fatal("stale target accepted")
	}
	if err := home.Typed.CoreV1().Secrets("gameplane-system").Delete(t.Context(), "gateway-client", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Capabilities(t.Context(), "remote", "gameplane-games", "alpha"); err == nil {
		t.Fatal("revoked credentials cached")
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
}
