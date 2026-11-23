package ws

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
)

// contentLengthRoundTripper records the outgoing request's ContentLength and
// answers 204 without touching the network.
type contentLengthRoundTripper struct {
	seen *int64
}

func (rt contentLengthRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	*rt.seen = req.ContentLength
	_, _ = io.Copy(io.Discard, req.Body)
	return &http.Response{
		StatusCode: http.StatusNoContent,
		Body:       http.NoBody,
		Header:     http.Header{},
		Request:    req,
	}, nil
}

func TestDirectAgentTransportForwardsKnownContentLength(t *testing.T) {
	const payload = "enabled=true"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.ContentLength != int64(len(payload)) {
			t.Errorf("agent saw ContentLength = %d, want %d", req.ContentLength, len(payload))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	resp, err := testDirectTransport(srv).Do(t.Context(), agentRequest{
		target: agentTarget{name: "alpha", namespace: "gameplane-games"},
		method: http.MethodPost, path: "/files/stream", rawQuery: "path=world.zip",
		body:          strings.NewReader(payload),
		contentLength: int64(len(payload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestDirectAgentTransportUnknownContentLengthStaysChunked(t *testing.T) {
	const payload = "chunk"
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.ContentLength != -1 {
			t.Errorf("agent saw ContentLength = %d, want -1 (chunked)", req.ContentLength)
		}
		_, _ = io.Copy(io.Discard, req.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	// An unrecognised reader type gives net/http no length, and zero means unknown.
	resp, err := testDirectTransport(srv).Do(t.Context(), agentRequest{
		target: agentTarget{name: "alpha", namespace: "gameplane-games"},
		method: http.MethodPost, path: "/files/stream", rawQuery: "path=world.zip",
		body: io.MultiReader(strings.NewReader(payload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestGatewayTransportForwardsKnownContentLength(t *testing.T) {
	const payload = "setting=true"
	var seen int64 = -2
	transport := &gatewayAgentTransport{
		base:   &url.URL{Scheme: "https", Host: "gateway.test"},
		target: gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "alpha", UID: "remote-uid"},
		http:   &http.Client{Transport: contentLengthRoundTripper{seen: &seen}},
	}
	resp, err := transport.Do(t.Context(), agentRequest{
		target: agentTarget{name: "alpha", namespace: "games"},
		method: http.MethodPost, path: "/files/write", rawQuery: "path=config.ini",
		body:          strings.NewReader(payload),
		contentLength: int64(len(payload)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if seen != int64(len(payload)) {
		t.Fatalf("gateway upstream ContentLength = %d, want %d", seen, len(payload))
	}
}
