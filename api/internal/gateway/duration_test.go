package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
)

func TestGatewayAcceptsOptionalRequestDuration(t *testing.T) {
	h, _ := newFixture(t)
	for _, duration := range []time.Duration{0, time.Hour, -time.Second} {
		cfg := h.cfg
		cfg.MaxRequestDuration = duration
		_, err := NewHandler(cfg, h.client)
		if (err != nil) != (duration < 0) {
			t.Errorf("duration %s: error=%v", duration, err)
		}
	}
}

func TestGatewayLongOperationDelayedHeaders(t *testing.T) {
	for _, path := range []string{"/mods/install", "/mods/upload"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			h, cert := newFixture(t)
			h.cfg.MaxRequestDuration = 0
			front := durationProxy(t.Context(), t, h, cert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				select {
				case <-time.After(31 * time.Second):
					w.WriteHeader(http.StatusNoContent)
				case <-r.Context().Done():
				}
			}))
			fixtureTransport := h.transport
			h.transport = func(files TLSFiles) (*http.Transport, error) {
				tr, err := fixtureTransport(files)
				if err == nil {
					// Retain the production header policy while redirecting only the dial.
					tr.ResponseHeaderTimeout = newHTTPTransport(nil).ResponseHeaderTimeout
				}
				return tr, err
			}
			gatewayPath, err := gatewayprotocol.Path(gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "original-uid"}, path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, front.URL+gatewayPath, strings.NewReader("archive"))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := front.Client().Do(req)
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

// Use real HTTP and WebSocket connections: context cancellation must propagate
// through ReverseProxy, including after an upgrade or response headers.
func durationProxy(ctx context.Context, t *testing.T, h *handler, cert *x509.Certificate, upstreamHandler http.Handler) *httptest.Server {
	t.Helper()
	upstream := httptest.NewTLSServer(upstreamHandler)
	t.Cleanup(upstream.Close)
	h.transport = func(TLSFiles) (*http.Transport, error) {
		tr := upstream.Client().Transport.(*http.Transport).Clone()
		tr.TLSClientConfig.ServerName = "127.0.0.1"
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}
		return tr, nil
	}
	front := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
		h.ServeHTTP(w, r)
	}))
	front.Config.BaseContext = func(net.Listener) context.Context { return ctx }
	front.Start()
	t.Cleanup(front.Close)
	return front
}

func TestGatewayTransfersWithoutTotalCap(t *testing.T) {
	for _, path := range []string{"/files/download", "/logs/download", "/files/upload", "/mods/upload", "/mods/install", "capture"} {
		t.Run(path, func(t *testing.T) {
			h, cert := newFixture(t)
			h.cfg.MaxRequestDuration = 0
			method := http.MethodGet
			if path == "/files/upload" || path == "/mods/upload" || path == "/mods/install" {
				method = http.MethodPost
			}
			var gatewayPath string
			var err error
			if path == "capture" {
				target := gatewayCaptureFixture(t, h, "original-uid", "Completed", time.Second)
				gatewayPath, err = gatewayprotocol.CapturePath(target)
			} else {
				gatewayPath, err = gatewayprotocol.Path(gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "original-uid"}, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			front := durationProxy(t.Context(), t, h, cert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
					return
				}
				w.Header().Set("Content-Length", "8")
				_, _ = io.WriteString(w, "first")
				w.(http.Flusher).Flush()
				select {
				case <-time.After(50 * time.Millisecond):
					_, _ = io.WriteString(w, "end")
				case <-r.Context().Done():
				}
			}))
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, method, front.URL+gatewayPath, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != http.StatusOK || string(body) != "firstend" {
				t.Fatalf("status=%d body=%q err=%v", resp.StatusCode, body, err)
			}
		})
	}
}

func TestGatewayUncappedWebSocketCancellation(t *testing.T) {
	for _, stop := range []string{"disconnect", "shutdown", "certificate", "configured limit"} {
		t.Run(stop, func(t *testing.T) {
			h, cert := newFixture(t)
			h.cfg.MaxRequestDuration = 0
			if stop == "certificate" {
				files, shortCert, _ := credentials(t, time.Now().Add(3*time.Second))
				h.cfg.TLS, cert = files, shortCert
			}
			if stop == "configured limit" {
				h.cfg.MaxRequestDuration = time.Second
			}
			upstreamDone := make(chan struct{})
			serverCtx, shutdown := context.WithCancel(t.Context())
			defer shutdown()
			front := durationProxy(serverCtx, t, h, cert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(upstreamDone)
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for {
					kind, data, err := conn.Read(r.Context())
					if err != nil {
						return
					}
					if err := conn.Write(r.Context(), kind, data); err != nil {
						return
					}
				}
			}))
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			url := "ws" + strings.TrimPrefix(front.URL, "http") + "/v1/clusters/remote/namespaces/games/servers/same-name/uids/original-uid/logs/tail"
			conn, response, err := websocket.Dial(ctx, url, nil)
			if response != nil && response.Body != nil {
				defer response.Body.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			if err := conn.Write(ctx, websocket.MessageText, []byte("still streaming")); err != nil {
				t.Fatal(err)
			}
			_, data, err := conn.Read(ctx)
			if err != nil || string(data) != "still streaming" {
				t.Fatalf("echo=%q err=%v", data, err)
			}
			switch stop {
			case "disconnect":
				_ = conn.CloseNow()
			case "shutdown":
				shutdown()
			}
			if stop != "disconnect" {
				if _, _, err := conn.Read(ctx); err == nil || ctx.Err() != nil {
					t.Fatalf("stream did not close before caller timeout: %v", err)
				}
			}
			select {
			case <-upstreamDone:
			case <-ctx.Done():
				t.Fatal("upstream connection leaked")
			}
		})
	}
}

func TestGatewayDeadlinePolicy(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		longRunning  bool
	}{
		{http.MethodGet, "/console", true},
		{http.MethodGet, "/logs/tail", true},
		{http.MethodGet, "/logs/download", true},
		{http.MethodGet, "/files/download", true},
		{http.MethodPost, "/files/upload", true},
		{http.MethodPost, "/mods/upload", true},
		{http.MethodPost, "/mods/install", true},
		{http.MethodGet, "/status", false},
		{http.MethodGet, "/files/list", false},
		{http.MethodGet, "/files/read", false},
		{http.MethodPost, "/files/write", false},
		{http.MethodDelete, "/files/delete", false},
		{http.MethodPost, "/actions/run", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h, cert := newFixture(t)
				h.cfg.MaxRequestDuration = 0
				ctx, cancel := h.requestContext(t.Context(), cert.NotAfter, gatewayprotocol.LongRunningOperation(tc.method, tc.path))
				defer cancel()
				time.Sleep(6 * time.Minute)
				if tc.longRunning && ctx.Err() != nil {
					t.Fatalf("healthy stream/transfer canceled: %v", ctx.Err())
				}
				if !tc.longRunning && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
					t.Fatal("ordinary operation was not bounded")
				}
				time.Sleep(time.Hour)
				if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
					t.Fatal("operation survived peer certificate expiry")
				}
			})
		})
	}
}

func TestGatewayDownloadCancellation(t *testing.T) {
	for _, capture := range []bool{false, true} {
		for _, stop := range []string{"disconnect", "shutdown", "certificate", "configured limit"} {
			name := "file/" + stop
			if capture {
				name = "capture/" + stop
			}
			t.Run(name, func(t *testing.T) {
				h, cert := newFixture(t)
				h.cfg.MaxRequestDuration = 0
				if stop == "certificate" {
					files, shortCert, _ := credentials(t, time.Now().Add(3*time.Second))
					h.cfg.TLS, cert = files, shortCert
				}
				if stop == "configured limit" {
					h.cfg.MaxRequestDuration = time.Second
				}
				path := "/v1/clusters/remote/namespaces/games/servers/same-name/uids/original-uid/files/download"
				if capture {
					target := gatewayCaptureFixture(t, h, "original-uid", "Completed", time.Second)
					var err error
					path, err = gatewayprotocol.CapturePath(target)
					if err != nil {
						t.Fatal(err)
					}
				}
				serverCtx, shutdown := context.WithCancel(t.Context())
				defer shutdown()
				upstreamDone := make(chan struct{})
				front := durationProxy(serverCtx, t, h, cert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(upstreamDone)
					w.Header().Set("Content-Length", "16384")
					_, _ = io.WriteString(w, strings.Repeat("x", 8192))
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}))
				ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, front.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					t.Fatal(resp.Status)
				}
				switch stop {
				case "disconnect":
					_ = resp.Body.Close()
				case "shutdown":
					shutdown()
				}
				if stop != "disconnect" {
					if _, err := io.Copy(io.Discard, resp.Body); err == nil || ctx.Err() != nil {
						t.Fatalf("download did not abort before caller timeout: %v", err)
					}
				}
				select {
				case <-upstreamDone:
				case <-ctx.Done():
					t.Fatal("upstream download leaked")
				}
			})
		}
	}
}
