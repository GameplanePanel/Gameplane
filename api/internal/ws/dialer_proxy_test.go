package ws

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestWSProxy_NoTLSConfigReturns503 covers the early-return when mTLS
// material is unset (the dev-mode fallback).
func TestWSProxy_NoTLSConfigReturns503(t *testing.T) {
	p := &proxy{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/ws/servers/alpha/console", nil)
	p.wsProxy("/console")(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rr.Code)
	}
}

// TestHTTPProxy_NoTLSConfigReturns503 covers the matching early-return
// for the file-browser HTTP proxy.
func TestHTTPProxy_NoTLSConfigReturns503(t *testing.T) {
	p := &proxy{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha/files/list", nil)
	p.httpProxy("/files/list")(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", rr.Code)
	}
}

// TestHTTPProxy_ScopeError fails Resolve by providing an unknown
// namespace query param. tls != nil so we reach the scope check.
func TestHTTPProxy_ScopeError(t *testing.T) {
	p := &proxy{
		transport: newDirectAgentTransport(&tls.Config{}, 0),
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha/files/list?namespace=forbidden", nil)
	p.httpProxy("/files/list")(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("expected non-200, got %d", rr.Code)
	}
}

// TestWSProxy_ScopeError likewise — wsProxy resolves scope before any
// upgrade so the Resolve error path is reachable without a real WS.
func TestWSProxy_ScopeError(t *testing.T) {
	p := &proxy{
		transport: newDirectAgentTransport(&tls.Config{}, 0),
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/ws/servers/alpha/logs?namespace=forbidden", nil)
	p.wsProxy("/logs/tail")(rr, req)
	if rr.Code == http.StatusSwitchingProtocols {
		t.Fatalf("expected non-101, got %d", rr.Code)
	}
}

// TestMount_LogsDownloadRouted proves Mount registers the log-download
// proxy route: with no mTLS material the handler answers 503 (the
// dev-mode fallback), whereas an unregistered path would 404.
func TestMount_LogsDownloadRouted(t *testing.T) {
	r := chi.NewRouter()
	Mount(r, nil, "", "", "")
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha/logs/download", nil)
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rr.Code)
	}
}

// TestMount_ActionsAndStatusRouted proves Mount registers the action-run
// and status proxy routes. With no mTLS material the handler answers 503
// (dev-mode fallback); an unregistered path would 404, and a registered
// route with the wrong method would 405.
func TestMount_ActionsAndStatusRouted(t *testing.T) {
	r := chi.NewRouter()
	Mount(r, nil, "", "", "")
	cases := []struct {
		method, path string
	}{
		{"POST", "/servers/alpha/actions/run"},
		{"POST", "/servers/alpha/files/create"},
		{"GET", "/servers/alpha/status"},
		{"GET", "/servers/alpha/mods"},
		{"POST", "/servers/alpha/mods/install"},
		{"DELETE", "/servers/alpha/mods"},
	}
	for _, tc := range cases {
		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, nil)
		r.ServeHTTP(rr, req)
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: got %d, want 503 (registered but no mTLS)", tc.method, tc.path, rr.Code)
		}
	}
}

// the agent FQDN constructor must include the standard 8090 port so
// callers can derive the right URL even without a real client.
func TestAgentHost_Format(t *testing.T) {
	got := agentHostFor("alpha", "gameplane-games")
	for _, want := range []string{"alpha-agent.gameplane-games", ":8090"} {
		if !strings.Contains(got, want) {
			t.Fatalf("got %q, want substring %q", got, want)
		}
	}
}

type fakeTimeoutErr struct{}

func (fakeTimeoutErr) Error() string   { return "i/o timeout" }
func (fakeTimeoutErr) Timeout() bool   { return true }
func (fakeTimeoutErr) Temporary() bool { return true }

// Transport failures on the API->agent leg must surface as gateway
// statuses (the dashboard and TestAPI_AgentUnreachable key off the
// 502/503/504 range), never as 500.
func TestWriteUpstreamErr_GatewayStatuses(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/x/players", nil)

	rr := httptest.NewRecorder()
	writeUpstreamErr(rr, req, errors.New("dial tcp 10.0.0.1:8090: connect: connection refused"))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("refused: got %d want 502", rr.Code)
	}

	rr = httptest.NewRecorder()
	writeUpstreamErr(rr, req, fmt.Errorf("proxy: %w", fakeTimeoutErr{}))
	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("timeout: got %d want 504", rr.Code)
	}
}
