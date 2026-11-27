package theme

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/themepkg"
)

func TestChooseMode(t *testing.T) {
	cases := []struct {
		name      string
		modes     []string
		requested string
		want      string
		wantErr   bool
	}{
		{name: "dark only opens dark", modes: []string{"dark"}, want: "dark"},
		{name: "light preferred when both", modes: []string{"dark", "light"}, want: "light"},
		{name: "explicit dark", modes: []string{"light", "dark"}, requested: "dark", want: "dark"},
		{name: "missing requested mode", modes: []string{"dark"}, requested: "light", wantErr: true},
		{name: "no modes", modes: nil, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ChooseMode(c.modes, c.requested)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ChooseMode(%v, %q) = %q, want error", c.modes, c.requested, got)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("ChooseMode(%v, %q) = %q, %v; want %q", c.modes, c.requested, got, err, c.want)
			}
		})
	}
}

// serve sends one GET request to the handler and returns the recorded response.
func serve(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil))
	return rec
}

func TestPreviewHandlerServesStyledSamplePage(t *testing.T) {
	pkg, err := Inspect(buildValidPackage(t, t.TempDir()))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	h, err := NewPreviewHandler(pkg, "")
	if err != nil {
		t.Fatalf("NewPreviewHandler: %v", err)
	}

	page := serve(t, h, "/")
	if page.Code != http.StatusOK {
		t.Fatalf("GET / = %d", page.Code)
	}
	body := page.Body.String()
	for _, want := range []string{`data-mode="dark"`, "Deep Ocean", `href="/tokens.css"`, `href="/theme.css"`, "Primary", "<table>", "<form"} {
		if !strings.Contains(body, want) {
			t.Fatalf("sample page lacks %q", want)
		}
	}
	if csp := page.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("Content-Security-Policy = %q", csp)
	}

	tokens := serve(t, h, "/tokens.css")
	if tokens.Code != http.StatusOK || !strings.Contains(tokens.Body.String(), `html[data-mode="dark"] {`) {
		t.Fatalf("GET /tokens.css = %d %q", tokens.Code, tokens.Body.String())
	}
	if !strings.Contains(tokens.Body.String(), "--accent: #38bdf8;") {
		t.Fatalf("tokens.css lacks the accent token:\n%s", tokens.Body.String())
	}
	if strings.Contains(tokens.Body.String(), `html[data-mode="light"]`) {
		t.Fatalf("tokens.css carries a light mode the package does not have")
	}

	theme := serve(t, h, "/theme.css")
	if theme.Code != http.StatusOK || theme.Body.String() != ".card { border-radius: 12px; }" {
		t.Fatalf("GET /theme.css = %d %q", theme.Code, theme.Body.String())
	}
	if got := serve(t, h, "/missing"); got.Code != http.StatusNotFound {
		t.Fatalf("GET /missing = %d, want 404", got.Code)
	}
}

func TestPreviewHandlerWithoutThemeCSS(t *testing.T) {
	root := t.TempDir()
	dir := buildValidPackage(t, root)
	pkg, err := Inspect(dir)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	// Drop the overlay from the in-memory package: the preview must still serve
	// an empty theme.css rather than fail.
	delete(pkg.Files, "theme.css")
	h, err := NewPreviewHandler(pkg, "dark")
	if err != nil {
		t.Fatalf("NewPreviewHandler: %v", err)
	}
	if got := serve(t, h, "/theme.css"); got.Code != http.StatusOK || got.Body.Len() != 0 {
		t.Fatalf("GET /theme.css without an overlay = %d, %d bytes", got.Code, got.Body.Len())
	}
}

func TestPreviewRefusesInvalidOrMissingMode(t *testing.T) {
	invalid := &Package{Dir: "broken", Problems: []themepkg.Problem{{Rule: themepkg.RuleSchema, Detail: "x"}}}
	if _, err := NewPreviewHandler(invalid, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid package: err = %v, want ErrInvalid", err)
	}

	pkg, err := Inspect(buildValidPackage(t, t.TempDir()))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if _, err := NewPreviewHandler(pkg, "light"); err == nil {
		t.Fatalf("a light preview of a dark-only package succeeded")
	}
}

func TestServeStopsWhenContextEnds(t *testing.T) {
	pkg, err := Inspect(buildValidPackage(t, t.TempDir()))
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	// Port 0 asks the kernel for a free loopback port, so the test cannot clash.
	if err := Serve(ctx, pkg, 0, "", &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if !strings.Contains(out.String(), "http://127.0.0.1:") {
		t.Fatalf("Serve did not announce its address: %q", out.String())
	}
}

func TestServeRefusesInvalidPackage(t *testing.T) {
	invalid := &Package{Dir: "broken", Problems: []themepkg.Problem{{Rule: themepkg.RuleSchema, Detail: "x"}}}
	var out bytes.Buffer
	if err := Serve(context.Background(), invalid, 0, "", &out); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Serve of an invalid package: err = %v, want ErrInvalid", err)
	}
	if out.Len() != 0 {
		t.Fatalf("an invalid package must not start a server, output %q", out.String())
	}
}
