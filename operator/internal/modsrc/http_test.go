package modsrc

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func tarGzArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func archiveFiles(prefix, name, version string) map[string]string {
	files := map[string]string{}
	for f, content := range validModuleFiles(name, version) {
		files[prefix+name+"/"+f] = content
	}
	return files
}

func serveArchive(t *testing.T, body []byte, check func(r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newHTTPFetcher(t *testing.T, spec *gameplanev1alpha1.HTTPSourceSpec, objs ...*corev1.Secret) Fetcher {
	t.Helper()
	b := fake.NewClientBuilder()
	for _, o := range objs {
		b = b.WithObjects(o)
	}
	f, err := newHTTP(context.Background(), b.Build(), "gameplane-system", spec, nil)
	if err != nil {
		t.Fatalf("newHTTP: %v", err)
	}
	return f
}

func TestHTTPFetcher_TarGz(t *testing.T) {
	// Mimic a GitHub release tarball: one top-level dir wrapping the tree.
	srv := serveArchive(t, tarGzArchive(t, archiveFiles("mods-1.0/", "mc", "1.0.0")), nil)
	f := newHTTPFetcher(t, &gameplanev1alpha1.HTTPSourceSpec{URL: srv.URL + "/mods.tar.gz", Insecure: true})

	entries, warnings, err := f.Index(context.Background())
	if err != nil || len(warnings) != 0 {
		t.Fatalf("Index: %v warnings=%v", err, warnings)
	}
	if len(entries) != 1 || entries[0].Name != "mc" || entries[0].LatestVersion != "1.0.0" {
		t.Fatalf("entries = %+v", entries)
	}
	if !strings.HasPrefix(entries[0].Digest, "sha256:") {
		t.Errorf("digest = %q", entries[0].Digest)
	}

	b, err := f.Pull(context.Background(), "mc", "1.0.0")
	if err != nil || b.Metadata.Name != "mc" {
		t.Fatalf("Pull: %+v %v", b, err)
	}
}

func TestHTTPFetcher_Zip(t *testing.T) {
	srv := serveArchive(t, zipArchive(t, archiveFiles("", "valheim", "0.9.0")), nil)
	f := newHTTPFetcher(t, &gameplanev1alpha1.HTTPSourceSpec{URL: srv.URL + "/mods.zip", Insecure: true})
	entries, _, err := f.Index(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Name != "valheim" {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
}

func TestHTTPFetcher_AuthHeaders(t *testing.T) {
	var got string
	srv := serveArchive(t, zipArchive(t, archiveFiles("", "mc", "1.0.0")), func(r *http.Request) {
		got = r.Header.Get("Authorization")
	})

	t.Run("bearer token", func(t *testing.T) {
		sec := &corev1.Secret{}
		sec.Name, sec.Namespace = "creds", "gameplane-system"
		sec.Data = map[string][]byte{"token": []byte("tok123")}
		f := newHTTPFetcher(t, &gameplanev1alpha1.HTTPSourceSpec{
			URL: srv.URL + "/m.zip", Insecure: true,
			SecretRef: &corev1.LocalObjectReference{Name: "creds"},
		}, sec)
		if _, _, err := f.Index(context.Background()); err != nil {
			t.Fatalf("Index: %v", err)
		}
		if got != "Bearer tok123" {
			t.Errorf("Authorization = %q", got)
		}
	})

	t.Run("basic auth", func(t *testing.T) {
		sec := &corev1.Secret{}
		sec.Name, sec.Namespace = "creds", "gameplane-system"
		sec.Data = map[string][]byte{"username": []byte("u"), "password": []byte("p")}
		f := newHTTPFetcher(t, &gameplanev1alpha1.HTTPSourceSpec{
			URL: srv.URL + "/m.zip", Insecure: true,
			SecretRef: &corev1.LocalObjectReference{Name: "creds"},
		}, sec)
		if _, _, err := f.Index(context.Background()); err != nil {
			t.Fatalf("Index: %v", err)
		}
		if !strings.HasPrefix(got, "Basic ") {
			t.Errorf("Authorization = %q", got)
		}
	})

	t.Run("unusable secret errors", func(t *testing.T) {
		sec := &corev1.Secret{}
		sec.Name, sec.Namespace = "creds", "gameplane-system"
		sec.Data = map[string][]byte{"junk": []byte("x")}
		b := fake.NewClientBuilder().WithObjects(sec).Build()
		_, err := newHTTP(context.Background(), b, "gameplane-system",
			&gameplanev1alpha1.HTTPSourceSpec{URL: "https://x/m.zip",
				SecretRef: &corev1.LocalObjectReference{Name: "creds"}}, nil)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestCheckHTTPURL(t *testing.T) {
	cases := []struct {
		url      string
		insecure bool
		wantErr  string
	}{
		{url: "https://example.com/m.tar.gz"},
		{url: "http://example.com/m.tar.gz", wantErr: "insecure"},
		{url: "http://example.com/m.tar.gz", insecure: true},
		{url: "ftp://example.com/m.tar.gz", wantErr: "only http(s)"},
		{url: "http://169.254.169.254/latest/meta-data", insecure: true, wantErr: "link-local"},
		{url: "https://metadata.google.internal/computeMetadata", wantErr: "metadata"},
		// Self-hosted registries on private/loopback literals are allowed —
		// only the metadata/link-local range is an SSRF target.
		{url: "http://10.0.0.5/m.tar.gz", insecure: true},
		{url: "http://127.0.0.1:5001/m.tar.gz", insecure: true},
	}
	for _, tc := range cases {
		err := checkHTTPURL(tc.url, tc.insecure)
		if tc.wantErr == "" && err != nil {
			t.Errorf("%s: unexpected error %v", tc.url, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: err = %v, want %q", tc.url, err, tc.wantErr)
		}
	}
}

func TestExtract_RejectsTraversalAndBadFormat(t *testing.T) {
	if _, err := extractArchive([]byte("plain text, not an archive")); err == nil {
		t.Error("non-archive accepted")
	}

	evil := tarGzArchive(t, map[string]string{"../../etc/passwd": "boom"})
	if _, err := extractArchive(evil); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Errorf("traversal err = %v", err)
	}

	evilZip := zipArchive(t, map[string]string{"..\\..\\evil.txt": "boom"})
	if _, err := extractArchive(evilZip); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Errorf("zip traversal err = %v", err)
	}
}

func TestHTTPFetcher_ServerErrorIsTotalFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	f := newHTTPFetcher(t, &gameplanev1alpha1.HTTPSourceSpec{URL: srv.URL + "/m.tar.gz", Insecure: true})
	if _, _, err := f.Index(context.Background()); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("err = %v", err)
	}
}
