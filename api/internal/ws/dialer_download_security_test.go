package ws

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

// downloadHeaderTransport answers every HTTP operation with a fixed set of
// agent response headers and a small body.
type downloadHeaderTransport struct{ header http.Header }

func (f *downloadHeaderTransport) Do(context.Context, agentRequest) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     f.header.Clone(),
		Body:       io.NopCloser(strings.NewReader("archive-bytes")),
	}, nil
}

func (*downloadHeaderTransport) Dial(context.Context, agentTarget, string, string) (*websocket.Conn, *http.Response, error) {
	return nil, nil, errors.New("unexpected websocket dial")
}

// A proxied download that the agent did not label is forced to an attachment,
// so the browser never renders agent-supplied content under the API origin.
func TestDownloadProxy_ForcesAttachmentWhenAgentOmitsIt(t *testing.T) {
	p := &proxy{transport: &downloadHeaderTransport{header: http.Header{
		"Content-Type": {"text/html"},
	}}}
	router := chi.NewRouter()
	router.Get("/servers/{name}/download", p.httpProxy("/download"))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/download", nil)
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if got := rr.Header().Get("Content-Disposition"); got != "attachment" {
		t.Fatalf("Content-Disposition = %q, want attachment", got)
	}
}

// An attachment disposition the agent already set is passed through unchanged.
func TestDownloadProxy_KeepsAgentAttachmentDisposition(t *testing.T) {
	const want = `attachment; filename="world.zip"`
	p := &proxy{transport: &downloadHeaderTransport{header: http.Header{
		"Content-Disposition": {want},
	}}}
	router := chi.NewRouter()
	router.Get("/servers/{name}/download", p.httpProxy("/download"))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/download", nil)
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Disposition"); got != want {
		t.Fatalf("Content-Disposition = %q, want %q", got, want)
	}
}

// Non-download routes do not get a forced disposition.
func TestDownloadProxy_NonDownloadRouteUntouched(t *testing.T) {
	p := &proxy{transport: &downloadHeaderTransport{header: http.Header{
		"Content-Type": {"application/json"},
	}}}
	router := chi.NewRouter()
	router.Get("/servers/{name}/files/list", p.httpProxy("/files/list"))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/alpha/files/list", nil)
	router.ServeHTTP(rr, req)

	if got := rr.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q, want none", got)
	}
}

func TestForceAttachment_Table(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"absent", "", "attachment"},
		{"inline replaced", "inline; filename=page.html", "attachment"},
		{"attachment kept", `attachment; filename="a.tar"`, `attachment; filename="a.tar"`},
		{"case and spaces tolerated", `  ATTACHMENT; filename=b.bin`, `  ATTACHMENT; filename=b.bin`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.in != "" {
				h.Set("Content-Disposition", tc.in)
			}
			forceAttachment(h)
			if got := h.Get("Content-Disposition"); got != tc.want {
				t.Fatalf("forceAttachment(%q) -> %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
