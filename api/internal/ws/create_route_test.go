package ws

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ValgulNecron/gameplane/api/internal/gatewayprotocol"
	"github.com/ValgulNecron/gameplane/api/internal/kube"
)

func TestLocalCreateProxyPreservesResponseWithoutWriteFallback(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusConflict, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.Method != http.MethodPost || req.URL.Path != "/v1/targets/gs-uid/files/create" {
					t.Errorf("unsafe upstream operation: %s %s", req.Method, req.URL.Path)
				}
				if req.URL.Query().Get("path") != "/new #?.txt" {
					t.Errorf("path query = %s", req.URL.RawQuery)
				}
				body, err := io.ReadAll(req.Body)
				if err != nil || string(body) != "payload" {
					t.Errorf("body=%q err=%v", body, err)
				}
				w.WriteHeader(status)
				if status != http.StatusNoContent {
					_, _ = w.Write([]byte("create refused"))
				}
			}))
			defer srv.Close()
			k := &kube.Client{}
			_ = streamTestRegistry(k)
			p := &proxy{k: k, transport: testDirectTransport(srv)}
			r := chi.NewRouter()
			r.Post("/servers/{name}/files/create", p.agentHTTP("/files/create"))
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/servers/alpha/files/create?path=%2Fnew%20%23%3F.txt", strings.NewReader("payload")))
			if rr.Code != status || calls != 1 {
				t.Fatalf("status=%d calls=%d", rr.Code, calls)
			}
			if status != http.StatusNoContent && rr.Body.String() != "create refused" {
				t.Fatalf("body=%q", rr.Body.String())
			}
		})
	}
}

func TestRemoteCreateProxyPreservesScopeAndRefusalWithoutFallback(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusConflict, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				calls++
				target, operation, err := gatewayprotocol.ParsePath(req.URL.Path)
				if err != nil || target != (gatewayprotocol.Target{Cluster: "remote", Namespace: "gameplane-games", Name: "alpha", UID: "remote-uid"}) || operation != "/files/create" || req.Method != http.MethodPost {
					t.Errorf("target=%+v operation=%s method=%s err=%v", target, operation, req.Method, err)
				}
				if req.URL.RawQuery != "path=%2Fnew+%23%3F.txt" {
					t.Errorf("query=%s", req.URL.RawQuery)
				}
				if req.Header.Get("Cookie") != "" || req.Header.Get("X-Gameplane-CSRF") != "" {
					t.Error("session credentials forwarded")
				}
				body, err := io.ReadAll(req.Body)
				if err != nil || string(body) != "payload" {
					t.Errorf("body=%q err=%v", body, err)
				}
				w.WriteHeader(status)
				if status != http.StatusNoContent {
					_, _ = w.Write([]byte("create refused"))
				}
			}))
			defer srv.Close()
			resolver, home, _ := gatewayFixture(t, srv.URL)
			secret, err := home.Typed.CoreV1().Secrets("gameplane-system").Get(t.Context(), "gateway-client", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			secret.Data["ca.crt"] = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
			if _, err := home.Typed.CoreV1().Secrets("gameplane-system").Update(t.Context(), secret, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			p := &proxy{k: home, gateway: resolver}
			r := chi.NewRouter()
			r.Post("/servers/{name}/files/create", p.agentHTTP("/files/create"))
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/servers/alpha/files/create?cluster=remote&namespace=gameplane-games&path=%2Fnew%20%23%3F.txt", strings.NewReader("payload"))
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("Cookie", "session=private")
			req.Header.Set("X-Gameplane-CSRF", "private")
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != status || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", rr.Code, calls, rr.Body)
			}
			if status != http.StatusNoContent && rr.Body.String() != "create refused" {
				t.Fatalf("body=%q", rr.Body.String())
			}
		})
	}
}
