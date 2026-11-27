package theme

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/themepkg"
)

// sampleHTML is the static sample page the preview renders with the package's
// tokens and theme.css applied (contracts/gp-module-theme-cli.md, theme preview).
//
//go:embed sample.html
var sampleHTML string

var sampleTmpl = template.Must(template.New("sample").Parse(sampleHTML))

// DefaultPreviewPort is the port `theme preview` serves on unless --port is set.
const DefaultPreviewPort = 8787

// sampleData is the template input of the sample page.
type sampleData struct {
	DisplayName string
	Version     string
	Mode        string
	ModesCSV    string
}

// ChooseMode returns the mode the preview opens in. An empty request picks light
// when the package has it, otherwise the first mode. A request for a mode the
// package does not carry is an error.
func ChooseMode(modes []string, requested string) (string, error) {
	if len(modes) == 0 {
		return "", fmt.Errorf("%w: the package has no token modes", ErrInvalid)
	}
	if requested == "" {
		for _, m := range modes {
			if m == "light" {
				return m, nil
			}
		}
		return modes[0], nil
	}
	for _, m := range modes {
		if m == requested {
			return m, nil
		}
	}
	return "", fmt.Errorf("the package has no %s tokens (modes: %s)", requested, strings.Join(modes, ", "))
}

// tokensCSS renders the token custom properties of every mode the package
// carries, each under html[data-mode=...]. Every value passed tokens.format, so
// it is a lowercase #rrggbb and safe to place in a stylesheet.
func tokensCSS(m themepkg.Manifest) string {
	var b strings.Builder
	for _, mode := range m.Modes() {
		tokens := m.Tokens.Light
		if mode == "dark" {
			tokens = m.Tokens.Dark
		}
		fmt.Fprintf(&b, "html[data-mode=%q] {\n", mode)
		for _, name := range themepkg.TokenNames {
			fmt.Fprintf(&b, "  --%s: %s;\n", name, tokens[name])
		}
		b.WriteString("}\n")
	}
	return b.String()
}

// NewPreviewHandler builds the HTTP handler of the preview for a package that
// passed validation. The page, tokens.css and theme.css are rendered in memory,
// so nothing is read from disk while the server runs.
func NewPreviewHandler(p *Package, requestedMode string) (http.Handler, error) {
	if !p.Valid() {
		return nil, fmt.Errorf("%w: %d problem(s), run `gp-module theme validate %s`", ErrInvalid, len(p.Problems), p.Dir)
	}
	m := p.Result.Manifest
	modes := m.Modes()
	mode, err := ChooseMode(modes, requestedMode)
	if err != nil {
		return nil, err
	}
	var page bytes.Buffer
	data := sampleData{
		DisplayName: displayName(m),
		Version:     m.Version,
		Mode:        mode,
		ModesCSV:    strings.Join(modes, ","),
	}
	if err := sampleTmpl.Execute(&page, data); err != nil {
		return nil, fmt.Errorf("render sample page: %w", err)
	}
	tokens := []byte(tokensCSS(m))
	themeCSS := p.Files["theme.css"]

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(page.Bytes())
	})
	mux.HandleFunc("/tokens.css", cssHandler(tokens))
	mux.HandleFunc("/theme.css", cssHandler(themeCSS))
	return mux, nil
}

// cssHandler serves fixed stylesheet bytes.
func cssHandler(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(body)
	}
}

// displayName returns the package's display name, falling back to its name.
func displayName(m themepkg.Manifest) string {
	if m.DisplayName != "" {
		return m.DisplayName
	}
	return m.Name
}

// Serve runs the preview on 127.0.0.1:port until ctx is cancelled, then shuts
// the server down and returns nil. It binds to loopback only: nothing is
// published. Progress is written to out.
func Serve(ctx context.Context, p *Package, port int, requestedMode string, out io.Writer) error {
	handler, err := NewPreviewHandler(p, requestedMode)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	mode, _ := ChooseMode(p.Result.Manifest.Modes(), requestedMode)
	fmt.Fprintf(out, "theme preview: http://%s/ (mode %s; press Ctrl+C to stop)\n", addr, mode)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve preview: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down preview: %w", err)
		}
		return nil
	}
}
