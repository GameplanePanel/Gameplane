package kube

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"
)

const (
	remoteResponseLimit   = 8 << 20
	remoteVersionLimit    = 64 << 10
	remoteWatchFrameLimit = 1 << 20
)

var errRemoteResponseTooLarge = errors.New("remote Kubernetes response exceeds the observation limit")

type remoteResponseTransport struct{ next http.RoundTripper }

func boundedRemoteTransport(next http.RoundTripper) http.RoundTripper {
	return &remoteResponseTransport{next: next}
}

func (t *remoteResponseTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Query().Get("watch") == "true" {
		// Select JSON framing consistently, including typed clients that otherwise
		// prefer protobuf. Kubernetes supports JSON for every watched resource.
		req = req.Clone(req.Context())
		req.Header.Set("Accept", "application/json")
	}
	resp, err := t.next.RoundTrip(req)
	if err != nil || resp.Body == nil {
		return resp, err
	}
	subresource := kubernetesPodStream(req.URL.Path)
	// SPDY adds the Upgrade header inside its own cloned request. Its explicit
	// stream negotiation header is visible to the surrounding REST wrappers.
	requestedUpgrade := req.Header.Get("Upgrade") != "" ||
		(subresource != "" && subresource != "log" && req.Header.Get("X-Stream-Protocol-Version") != "")
	if resp.StatusCode == http.StatusSwitchingProtocols && !requestedUpgrade {
		// Client-go buffers responses before checking their status. Never let a
		// remote turn an ordinary observation into an unbounded raw connection.
		_ = resp.Body.Close()
		return nil, errors.New("unsolicited remote protocol upgrade")
	}
	// User log/exec/attach streams are intentionally not finite observations.
	// A watch is different: each event needs a bound, while its lifetime does not.
	if resp.StatusCode == http.StatusSwitchingProtocols ||
		(subresource == "log" && resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return resp, nil
	}
	body := resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		decoded, err := gzip.NewReader(body)
		if err != nil {
			_ = body.Close()
			return nil, errors.New("invalid compressed remote response")
		}
		body = &decodedRemoteBody{Reader: decoded, compressed: resp.Body}
		resp.Header.Del("Content-Encoding")
		resp.Header.Del("Content-Length")
		resp.ContentLength = -1
		resp.Uncompressed = true
	}
	if req.URL.Query().Get("watch") == "true" && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		resp.Body = &remoteWatchBody{ReadCloser: body}
	} else {
		limit := int64(remoteResponseLimit)
		if strings.TrimSuffix(req.URL.Path, "/") == "/version" {
			limit = remoteVersionLimit
		}
		resp.Body = &finiteRemoteBody{ReadCloser: body, remaining: limit}
	}
	return resp, nil
}

func kubernetesPodStream(path string) string {
	// Match core Pod subresources, preserving any kubeconfig API-server prefix.
	// A CR or other resource named "log" must remain a bounded observation.
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 7 {
		return ""
	}
	parts = parts[len(parts)-7:]
	if parts[0] != "api" || parts[1] != "v1" || parts[2] != "namespaces" || parts[3] == "" || parts[4] != "pods" || parts[5] == "" {
		return ""
	}
	switch parts[6] {
	case "log", "exec", "attach", "portforward":
		return parts[6]
	default:
		return ""
	}
}

type decodedRemoteBody struct {
	*gzip.Reader
	compressed io.ReadCloser
}

func (r *decodedRemoteBody) Close() error { return errors.Join(r.Reader.Close(), r.compressed.Close()) }

type finiteRemoteBody struct {
	io.ReadCloser
	remaining int64
	failed    bool
}

func (r *finiteRemoteBody) Read(p []byte) (int, error) {
	if r.failed {
		return 0, errRemoteResponseTooLarge
	}
	if len(p) == 0 {
		return 0, nil
	}
	if int64(len(p)) > r.remaining+1 {
		p = p[:r.remaining+1]
	}
	n, err := r.ReadCloser.Read(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
		r.remaining = 0
		r.failed = true
		return n, errRemoteResponseTooLarge
	}
	r.remaining -= int64(n)
	return n, err
}

// remoteWatchBody recognizes JSON object boundaries without decoding or retaining
// a frame. This runs before client-go's JSON framer, which otherwise allocates the
// entire RawMessage before applying its own size limit.
type remoteWatchBody struct {
	io.ReadCloser
	size, depth               int
	inString, escaped, failed bool
}

func (r *remoteWatchBody) Read(p []byte) (int, error) {
	if r.failed {
		return 0, errRemoteResponseTooLarge
	}
	if len(p) == 0 {
		return 0, nil
	}
	if remaining := remoteWatchFrameLimit - r.size + 1; len(p) > remaining {
		p = p[:remaining]
	}
	n, err := r.ReadCloser.Read(p)
	for i, b := range p[:n] {
		r.size++
		if r.size > remoteWatchFrameLimit {
			r.failed = true
			return i, errRemoteResponseTooLarge
		}
		if r.inString {
			if r.escaped {
				r.escaped = false
			} else if b == '\\' {
				r.escaped = true
			} else if b == '"' {
				r.inString = false
			}
			continue
		}
		switch b {
		case '"':
			r.inString = true
		case '{', '[':
			r.depth++
		case '}', ']':
			r.depth--
			if r.depth == 0 {
				r.size = 0
			}
		}
	}
	return n, err
}
