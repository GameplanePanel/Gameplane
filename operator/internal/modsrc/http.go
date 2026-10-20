package modsrc

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"testing/fstest"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/GameplanePanel/gameplane/netguard"
	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Hard caps on archives an http source will accept. A module bundle is
// a few YAML files and an icon; anything near these limits is hostile
// or a mistake, and the operator must not OOM unpacking it.
const (
	maxArchiveCompressed = 64 << 20  // 64 MiB on the wire
	maxArchiveExtracted  = 256 << 20 // 256 MiB unpacked
	maxArchiveFiles      = 10_000
)

// httpFetchClient guards every archive download against being pointed at the
// cloud metadata endpoint (see internal/netguard). Loopback and private
// registries are still reachable — only link-local/metadata/multicast are
// refused, at dial time so a DNS name rebinding to one is caught too.
var httpFetchClient = netguard.HTTPClient(2*time.Minute, netguard.IsAllowed)

// newHTTP builds a Fetcher over an archive (.tar.gz/.tgz/.zip) served
// at an http(s) URL. The archive is downloaded fresh on each index and
// scanned for module directories like every filesystem source.
func newHTTP(ctx context.Context, c client.Client, namespace string, spec *gameplanev1alpha1.HTTPSourceSpec, allow []string) (Fetcher, error) {
	if err := checkHTTPURL(spec.URL, spec.Insecure); err != nil {
		return nil, err
	}
	authHeader, err := httpAuth(ctx, c, namespace, spec.SecretRef)
	if err != nil {
		return nil, err
	}
	srcURL := spec.URL
	load := func(ctx context.Context) (fs.FS, string, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srcURL, nil)
		if err != nil {
			return nil, "", err
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		resp, err := httpFetchClient.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("fetch %s: %w", srcURL, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("fetch %s: HTTP %s", srcURL, resp.Status)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxArchiveCompressed+1))
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", srcURL, err)
		}
		if len(data) > maxArchiveCompressed {
			return nil, "", fmt.Errorf("archive at %s exceeds the %d MiB limit", srcURL, maxArchiveCompressed>>20)
		}
		fsys, err := extractArchive(data)
		if err != nil {
			return nil, "", fmt.Errorf("extract %s: %w", srcURL, err)
		}
		return fsys, "", nil
	}
	return newFSFetcher(load, spec.URL, allow), nil
}

// checkHTTPURL enforces https (plain http only with insecure: true)
// and refuses the link-local range that cloud metadata services live
// on — the operator's credentials must not be exfiltratable by
// pointing a source at 169.254.169.254.
func checkHTTPURL(raw string, insecure bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse url %q: %w", raw, err)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !insecure {
			return fmt.Errorf("plain http url %q requires insecure: true", raw)
		}
	default:
		return fmt.Errorf("url %q: only http(s) is supported", raw)
	}
	host := u.Hostname()
	if netguard.HostIsMetadata(host) {
		return fmt.Errorf("url %q: metadata endpoints are not allowed", raw)
	}
	if ip := net.ParseIP(host); ip != nil && !netguard.IsAllowed(ip) {
		return fmt.Errorf("url %q: %s is a blocked address (link-local/metadata/multicast)", raw, ip)
	}
	return nil
}

// httpAuth resolves the source's credential Secret into an
// Authorization header value: "token" → Bearer, else basic auth from
// "username"+"password".
func httpAuth(ctx context.Context, c client.Client, namespace string, secretRef *corev1.LocalObjectReference) (string, error) {
	if secretRef == nil || secretRef.Name == "" {
		return "", nil
	}
	var sec corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: secretRef.Name}, &sec); err != nil {
		return "", fmt.Errorf("get http credentials %s/%s: %w", namespace, secretRef.Name, err)
	}
	if token, ok := sec.Data["token"]; ok && len(token) > 0 {
		return "Bearer " + string(token), nil
	}
	user, uok := sec.Data["username"]
	pass, pok := sec.Data["password"]
	if uok && pok {
		req := &http.Request{Header: http.Header{}}
		req.SetBasicAuth(string(user), string(pass))
		return req.Header.Get("Authorization"), nil
	}
	return "", fmt.Errorf("secret %s needs \"token\" or \"username\"+\"password\"", secretRef.Name)
}

// extractArchive unpacks a tar.gz or zip (detected by magic bytes)
// into an in-memory filesystem, rejecting path traversal and anything
// over the extraction caps.
func extractArchive(data []byte) (fs.FS, error) {
	switch {
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		return extractTarGz(data)
	case len(data) >= 4 && bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return extractZip(data)
	default:
		return nil, errors.New("unsupported archive format (need .tar.gz or .zip)")
	}
}

// safePath normalizes an archive member path and rejects traversal.
func safePath(name string) (string, error) {
	p := path.Clean(strings.ReplaceAll(name, `\`, "/"))
	if p == "." || p == "" {
		return "", nil // directory entries
	}
	if path.IsAbs(p) || p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("archive member %q escapes the extraction root", name)
	}
	return p, nil
}

func extractTarGz(data []byte) (fs.FS, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	out := fstest.MapFS{}
	var total int64
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		p, err := safePath(hdr.Name)
		if err != nil {
			return nil, err
		}
		if p == "" {
			continue
		}
		content, err := readCapped(tr, &total, len(out))
		if err != nil {
			return nil, err
		}
		out[p] = &fstest.MapFile{Data: content}
	}
}

func extractZip(data []byte) (fs.FS, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	out := fstest.MapFS{}
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		p, err := safePath(f.Name)
		if err != nil {
			return nil, err
		}
		if p == "" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		content, err := readCapped(rc, &total, len(out))
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		out[p] = &fstest.MapFile{Data: content}
	}
	return out, nil
}

// readCapped reads one archive member while enforcing the global
// extraction caps (decompression-bomb guard).
func readCapped(r io.Reader, total *int64, fileCount int) ([]byte, error) {
	if fileCount >= maxArchiveFiles {
		return nil, fmt.Errorf("archive has more than %d files", maxArchiveFiles)
	}
	remaining := maxArchiveExtracted - *total
	content, err := io.ReadAll(io.LimitReader(r, remaining+1))
	if err != nil {
		return nil, err
	}
	*total += int64(len(content))
	if *total > maxArchiveExtracted {
		return nil, fmt.Errorf("archive expands past the %d MiB extraction limit", maxArchiveExtracted>>20)
	}
	return content, nil
}
