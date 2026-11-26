package source

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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
)

// httptAnyErr in a want-error field means "any non-nil error".
const httptAnyErr = "<any>"

type httptEntry struct {
	name     string
	typeflag byte
	body     string
}

type httptZeroReader struct{}

func (httptZeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func httptTarBytes(t *testing.T, entries []httptEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Typeflag: e.typeflag, Mode: 0o644, Size: int64(len(e.body))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %q: %v", e.name, err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatalf("tar body %q: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

func httptGzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func httptFixtureTarGz(t *testing.T, entries []httptEntry) []byte {
	t.Helper()
	return httptGzip(t, httptTarBytes(t, entries))
}

func httptFixtureZip(t *testing.T, entries []httptEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name})
		if err != nil {
			t.Fatalf("zip header %q: %v", e.name, err)
		}
		if _, err := io.WriteString(w, e.body); err != nil {
			t.Fatalf("zip body %q: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// httptManyEntries returns n empty regular-file entries with unique names.
func httptManyEntries(n int) []httptEntry {
	out := make([]httptEntry, 0, n)
	for i := range n {
		out = append(out, httptEntry{name: fmt.Sprintf("f%05d.txt", i), typeflag: tar.TypeReg})
	}
	return out
}

func httptWantErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "":
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case want == httptAnyErr:
		if err == nil {
			t.Fatal("want an error, got nil")
		}
	default:
		if err == nil {
			t.Fatalf("want error containing %q, got nil", want)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err.Error(), want)
		}
	}
}

func httptServe(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestNewArchiveHTTPClient(t *testing.T) {
	t.Parallel()
	c := newArchiveHTTPClient()
	if c.CheckRedirect == nil {
		t.Fatal("CheckRedirect is nil")
	}
	if c.Timeout == 0 {
		t.Fatal("client has no timeout")
	}
}

func TestNewArchiveHTTPClientCheckRedirect(t *testing.T) {
	t.Parallel()
	c := newArchiveHTTPClient()
	cases := []struct {
		name    string
		via     int
		wantErr string
	}{
		{name: "first hop keeps working", via: 1},
		{name: "ninth hop allowed", via: 9},
		{name: "tenth hop refused", via: 10, wantErr: "stopped after 10 redirects"},
		{name: "beyond cap refused", via: 12, wantErr: "stopped after 10 redirects"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com/bundle.tar.gz", nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			req.Header.Set("Authorization", "Bearer secret")
			via := make([]*http.Request, tc.via)
			err = c.CheckRedirect(req, via)
			httptWantErr(t, err, tc.wantErr)
			if tc.wantErr == "" && req.Header.Get("Authorization") != "" {
				t.Fatalf("Authorization survived redirect: %q", req.Header.Get("Authorization"))
			}
		})
	}
}

func TestArchiveLoaderRedirects(t *testing.T) {
	t.Parallel()
	archive := httptFixtureTarGz(t, []httptEntry{{name: "bundle.yaml", typeflag: tar.TypeReg, body: "ok"}})
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("first request Authorization = %q, want Bearer tok", got)
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("redirect target received Authorization %q, want none", got)
		}
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	t.Run("follows redirect and strips credentials", func(t *testing.T) {
		t.Parallel()
		fsys, _, err := ArchiveLoader(srv.URL+"/start", "Bearer tok")(context.Background())
		httptWantErr(t, err, "")
		data, err := fs.ReadFile(fsys, "bundle.yaml")
		httptWantErr(t, err, "")
		if string(data) != "ok" {
			t.Fatalf("bundle.yaml = %q", data)
		}
	})
	t.Run("caps redirect chain", func(t *testing.T) {
		t.Parallel()
		_, _, err := ArchiveLoader(srv.URL+"/loop", "")(context.Background())
		httptWantErr(t, err, "stopped after 10 redirects")
	})
}

func TestArchiveLoaderOutcomes(t *testing.T) {
	t.Parallel()
	tgz := httptFixtureTarGz(t, []httptEntry{{name: "bundle.yaml", typeflag: tar.TypeReg, body: "name: demo"}})
	zipped := httptFixtureZip(t, []httptEntry{{name: "bundle.yaml", body: "name: demo"}})

	closedURL := func(t *testing.T) string {
		t.Helper()
		srv := httptest.NewServer(http.NotFoundHandler())
		u := srv.URL
		srv.Close()
		return u
	}

	cases := []struct {
		name     string
		url      func(t *testing.T) string
		wantErr  string
		wantFile bool
	}{
		{
			name: "tar.gz served",
			url: func(t *testing.T) string {
				return httptServe(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tgz) }).URL + "/b.tgz"
			},
			wantFile: true,
		},
		{
			name: "zip served",
			url: func(t *testing.T) string {
				return httptServe(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(zipped) }).URL + "/b.zip"
			},
			wantFile: true,
		},
		{
			name: "non-200 status",
			url: func(t *testing.T) string {
				return httptServe(t, func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "nope", http.StatusNotFound)
				}).URL + "/missing.tgz"
			},
			wantErr: "HTTP 404",
		},
		{
			name: "body is not an archive",
			url: func(t *testing.T) string {
				return httptServe(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("plain text")) }).URL + "/x"
			},
			wantErr: "extract",
		},
		{
			name: "body truncated mid-transfer",
			url: func(t *testing.T) string {
				return httptServe(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Length", "1000")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte("short"))
				}).URL + "/x"
			},
			wantErr: httptAnyErr,
		},
		{
			name:    "server not listening",
			url:     closedURL,
			wantErr: "fetch",
		},
		{
			name: "cloud metadata address refused by dial guard",
			url: func(*testing.T) string {
				return "http://169.254.169.254/bundle.tgz"
			},
			wantErr: "fetch",
		},
		{
			name: "unparseable url",
			url: func(*testing.T) string {
				return "http://[::1"
			},
			wantErr: "parse",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys, source, err := ArchiveLoader(tc.url(t), "")(context.Background())
			httptWantErr(t, err, tc.wantErr)
			if tc.wantErr != "" {
				return
			}
			if source != "" {
				t.Fatalf("digest override = %q, want empty", source)
			}
			if tc.wantFile {
				data, err := fs.ReadFile(fsys, "bundle.yaml")
				httptWantErr(t, err, "")
				if string(data) != "name: demo" {
					t.Fatalf("bundle.yaml = %q", data)
				}
			}
		})
	}
}

func TestArchiveLoaderAuthorizationHeader(t *testing.T) {
	t.Parallel()
	archive := httptFixtureTarGz(t, []httptEntry{{name: "a.yaml", typeflag: tar.TypeReg, body: "a"}})
	cases := []struct {
		name string
		auth string
		want string
	}{
		{name: "credential sent", auth: "Bearer tok", want: "Bearer tok"},
		{name: "no credential", auth: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var seen []string
			srv := httptServe(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen = append(seen, r.Header.Get("Authorization"))
				mu.Unlock()
				_, _ = w.Write(archive)
			})
			_, _, err := ArchiveLoader(srv.URL+"/a.tgz", tc.auth)(context.Background())
			httptWantErr(t, err, "")
			mu.Lock()
			defer mu.Unlock()
			if len(seen) != 1 || seen[0] != tc.want {
				t.Fatalf("Authorization headers seen = %q, want [%q]", seen, tc.want)
			}
		})
	}
}

func TestArchiveLoaderOverCompressedLimit(t *testing.T) {
	t.Parallel()
	srv := httptServe(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.CopyN(w, httptZeroReader{}, maxArchiveCompressed+1)
	})
	_, _, err := ArchiveLoader(srv.URL+"/huge.tgz", "")(context.Background())
	httptWantErr(t, err, "exceeds the 64 MiB limit")
}

func TestCheckHTTPURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		raw      string
		insecure bool
		wantErr  string
	}{
		{name: "https public host", raw: "https://registry.example.com/bundle.tgz"},
		{name: "https loopback allowed", raw: "https://127.0.0.1:8443/bundle.tgz"},
		{name: "https bracketed ipv6 loopback", raw: "https://[::1]/bundle.tgz"},
		{name: "plain http refused by default", raw: "http://registry.example.com/x", wantErr: "requires insecure: true"},
		{name: "plain http allowed when insecure", raw: "http://registry.example.com/x", insecure: true},
		{name: "ftp scheme refused", raw: "ftp://registry.example.com/x", wantErr: "only http(s)"},
		{name: "file scheme refused", raw: "file:///etc/passwd", wantErr: "only http(s)"},
		{name: "unparseable url", raw: "https://[::1/x", wantErr: "parse url"},
		{name: "metadata hostname", raw: "https://metadata.google.internal/computeMetadata/", wantErr: "metadata endpoints"},
		{name: "metadata hostname trailing dot and case", raw: "https://METADATA./x", wantErr: "metadata endpoints"},
		{name: "link-local ipv4 metadata", raw: "https://169.254.169.254/latest/", wantErr: "blocked address"},
		{name: "link-local ipv4 other", raw: "https://169.254.10.1/x", wantErr: "blocked address"},
		{name: "unspecified address", raw: "https://0.0.0.0/x", wantErr: "blocked address"},
		{name: "alibaba metadata address", raw: "https://100.100.100.200/x", wantErr: "blocked address"},
		{name: "aws ipv6 metadata address", raw: "https://[fd00:ec2::254]/x", wantErr: "blocked address"},
		{name: "nat64 wrapping link-local", raw: "https://[64:ff9b::a9fe:a9fe]/x", wantErr: "blocked address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			httptWantErr(t, CheckHTTPURL(tc.raw, tc.insecure), tc.wantErr)
		})
	}
}

func TestExtractArchiveDispatch(t *testing.T) {
	t.Parallel()
	tgz := httptFixtureTarGz(t, []httptEntry{{name: "x.yaml", typeflag: tar.TypeReg, body: "1"}})
	zipped := httptFixtureZip(t, []httptEntry{{name: "x.yaml", body: "1"}})
	cases := []struct {
		name    string
		data    []byte
		wantErr string
		wantOK  bool
	}{
		{name: "gzip detected as tar.gz", data: tgz, wantOK: true},
		{name: "zip detected by magic", data: zipped, wantOK: true},
		{name: "empty input", data: nil, wantErr: "unsupported archive format"},
		{name: "single gzip magic byte", data: []byte{0x1f}, wantErr: "unsupported archive format"},
		{name: "short zip magic", data: []byte("PK\x03"), wantErr: "unsupported archive format"},
		{name: "plain text", data: []byte("hello world"), wantErr: "unsupported archive format"},
		{name: "gzip magic with bad header", data: []byte{0x1f, 0x8b, 0x00, 0x00}, wantErr: httptAnyErr},
		{name: "zip magic with no directory", data: []byte("PK\x03\x04garbage"), wantErr: httptAnyErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys, err := ExtractArchive(tc.data)
			httptWantErr(t, err, tc.wantErr)
			if tc.wantOK {
				data, err := fs.ReadFile(fsys, "x.yaml")
				httptWantErr(t, err, "")
				if string(data) != "1" {
					t.Fatalf("x.yaml = %q", data)
				}
			}
		})
	}
}

func TestSafePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "plain file", in: "a/b.yaml", want: "a/b.yaml"},
		{name: "dot prefix cleaned", in: "./a/b.yaml", want: "a/b.yaml"},
		{name: "backslashes normalised", in: `a\b.yaml`, want: "a/b.yaml"},
		{name: "inner dotdot resolved inside root", in: "a/../b.yaml", want: "b.yaml"},
		{name: "trailing slash cleaned", in: "dir/", want: "dir"},
		{name: "dot is directory entry", in: ".", want: ""},
		{name: "empty name is directory entry", in: "", want: ""},
		{name: "absolute path refused", in: "/etc/passwd", wantErr: true},
		{name: "bare dotdot refused", in: "..", wantErr: true},
		{name: "leading dotdot refused", in: "../evil", wantErr: true},
		{name: "backslash traversal refused", in: `..\evil`, wantErr: true},
		{name: "escaping after clean refused", in: "a/../../x", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := safePath(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("safePath(%q) = %q, want error", tc.in, got)
				}
				return
			}
			httptWantErr(t, err, "")
			if got != tc.want {
				t.Fatalf("safePath(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractTarGz(t *testing.T) {
	t.Parallel()
	t.Run("skips dirs symlinks and dot entries, normalises names", func(t *testing.T) {
		t.Parallel()
		data := httptFixtureTarGz(t, []httptEntry{
			{name: "dir/", typeflag: tar.TypeDir},
			{name: "link", typeflag: tar.TypeSymlink},
			{name: ".", typeflag: tar.TypeReg},
			{name: "bundle/a.yaml", typeflag: tar.TypeReg, body: "a: 1"},
			{name: `bundle\b.yaml`, typeflag: tar.TypeReg, body: "b: 2"},
		})
		fsys, err := ExtractArchive(data)
		httptWantErr(t, err, "")
		for name, want := range map[string]string{"bundle/a.yaml": "a: 1", "bundle/b.yaml": "b: 2"} {
			got, err := fs.ReadFile(fsys, name)
			httptWantErr(t, err, "")
			if string(got) != want {
				t.Fatalf("%s = %q, want %q", name, got, want)
			}
		}
		if _, err := fs.Stat(fsys, "link"); err == nil {
			t.Fatal("symlink member was extracted")
		}
	})
	t.Run("path traversal member refused", func(t *testing.T) {
		t.Parallel()
		_, err := ExtractArchive(httptFixtureTarGz(t, []httptEntry{{name: "../evil", typeflag: tar.TypeReg, body: "x"}}))
		httptWantErr(t, err, "escapes the extraction root")
	})
	t.Run("member truncated mid-body", func(t *testing.T) {
		t.Parallel()
		raw := httptTarBytes(t, []httptEntry{{name: "big.yaml", typeflag: tar.TypeReg, body: strings.Repeat("y", 1000)}})
		_, err := ExtractArchive(httptGzip(t, raw[:512+100]))
		httptWantErr(t, err, httptAnyErr)
	})
	t.Run("garbage inside gzip is not a tar header", func(t *testing.T) {
		t.Parallel()
		_, err := ExtractArchive(httptGzip(t, bytes.Repeat([]byte("x"), 1024)))
		httptWantErr(t, err, httptAnyErr)
	})
	t.Run("too many members", func(t *testing.T) {
		t.Parallel()
		_, err := ExtractArchive(httptFixtureTarGz(t, httptManyEntries(maxArchiveFiles+1)))
		httptWantErr(t, err, fmt.Sprintf("more than %d files", maxArchiveFiles))
	})
}

func TestExtractZip(t *testing.T) {
	t.Parallel()
	t.Run("skips dirs and dot entries, normalises names", func(t *testing.T) {
		t.Parallel()
		data := httptFixtureZip(t, []httptEntry{
			{name: "dir/"},
			{name: "."},
			{name: "bundle/a.yaml", body: "a: 1"},
			{name: `bundle\b.yaml`, body: "b: 2"},
		})
		fsys, err := extractZip(data)
		httptWantErr(t, err, "")
		for name, want := range map[string]string{"bundle/a.yaml": "a: 1", "bundle/b.yaml": "b: 2"} {
			got, err := fs.ReadFile(fsys, name)
			httptWantErr(t, err, "")
			if string(got) != want {
				t.Fatalf("%s = %q, want %q", name, got, want)
			}
		}
	})
	t.Run("path traversal member refused", func(t *testing.T) {
		t.Parallel()
		_, err := extractZip(httptFixtureZip(t, []httptEntry{{name: "../evil", body: "x"}}))
		httptWantErr(t, err, "escapes the extraction root")
	})
	t.Run("local header corrupted so body cannot be opened", func(t *testing.T) {
		t.Parallel()
		data := httptFixtureZip(t, []httptEntry{{name: "a.yaml", body: "hello"}})
		data[0] = 0 // local file header signature no longer matches
		_, err := extractZip(data)
		httptWantErr(t, err, httptAnyErr)
	})
	t.Run("stored payload fails CRC on read", func(t *testing.T) {
		t.Parallel()
		data := httptFixtureZip(t, []httptEntry{{name: "a.yaml", body: "CORRUPT-ME-PAYLOAD"}})
		idx := bytes.Index(data, []byte("CORRUPT-ME-PAYLOAD"))
		if idx < 0 {
			t.Fatal("fixture payload not found")
		}
		data[idx] ^= 0xff
		_, err := extractZip(data)
		httptWantErr(t, err, httptAnyErr)
	})
	t.Run("not a zip directory", func(t *testing.T) {
		t.Parallel()
		_, err := extractZip([]byte("PK\x03\x04garbage"))
		httptWantErr(t, err, httptAnyErr)
	})
	t.Run("too many members", func(t *testing.T) {
		t.Parallel()
		_, err := extractZip(httptFixtureZip(t, httptManyEntries(maxArchiveFiles+1)))
		httptWantErr(t, err, fmt.Sprintf("more than %d files", maxArchiveFiles))
	})
}

func TestReadCapped(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		r         io.Reader
		total     int64
		fileCount int
		wantLen   int
		wantTotal int64
		wantErr   string
	}{
		{name: "small member", r: strings.NewReader("hello"), wantLen: 5, wantTotal: 5},
		{name: "fills budget exactly", r: strings.NewReader("12345"), total: maxArchiveExtracted - 5, wantLen: 5, wantTotal: maxArchiveExtracted},
		{name: "one byte over budget", r: strings.NewReader("123456"), total: maxArchiveExtracted - 5, wantErr: "extraction limit"},
		{name: "file count at cap", r: strings.NewReader("x"), fileCount: maxArchiveFiles, wantErr: "more than 10000 files"},
		{name: "underlying read error", r: iotest.ErrReader(errors.New("boom")), wantErr: "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			total := tc.total
			got, err := readCapped(tc.r, &total, tc.fileCount)
			httptWantErr(t, err, tc.wantErr)
			if tc.wantErr != "" {
				return
			}
			if len(got) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tc.wantLen)
			}
			if total != tc.wantTotal {
				t.Fatalf("total = %d, want %d", total, tc.wantTotal)
			}
		})
	}
}
