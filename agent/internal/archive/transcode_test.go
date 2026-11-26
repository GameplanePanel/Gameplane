package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"math/rand"
	"strings"
	"testing"
	"time"
)

var testCaptured = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

func testManifest() Manifest {
	return Manifest{
		FormatVersion: 1,
		Writer:        "test",
		CapturedAt:    testCaptured,
		Volumes: []ManifestVolume{
			{LogicalName: "data", Folder: "data"},
			{LogicalName: "world", Folder: "volumes/world"},
		},
	}
}

type srcEntry struct {
	hdr  tar.Header
	body string
}

// buildSource writes a tar stream the way restic dump --archive tar would.
func buildSource(t *testing.T, entries []srcEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := e.hdr
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatalf("WriteHeader(%q): %v", h.Name, err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("Write(%q): %v", h.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close source tar: %v", err)
	}
	return buf.Bytes()
}

// fullSource mixes kept entries with entries that must be skipped.
func fullSource() []srcEntry {
	return []srcEntry{
		{hdr: tar.Header{Name: "data/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: testCaptured}},
		{hdr: tar.Header{Name: "data/a.txt", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: testCaptured}, body: "hello"},
		{hdr: tar.Header{Name: "./volumes/", Typeflag: tar.TypeDir, Mode: 0o755}},
		{hdr: tar.Header{Name: "/volumes/world/level.dat", Typeflag: tar.TypeReg, Mode: 0o600}, body: "world"},
		{hdr: tar.Header{Name: "volumes/world/sub/", Typeflag: tar.TypeDir, Mode: 0o755}},
		{hdr: tar.Header{Name: "data/link", Typeflag: tar.TypeSymlink, Linkname: "a.txt", Mode: 0o777}},
		{hdr: tar.Header{Name: "volumes/world/hard", Typeflag: tar.TypeLink, Linkname: "volumes/world/level.dat"}},
		{hdr: tar.Header{Name: "etc/secret", Typeflag: tar.TypeReg, Mode: 0o600}, body: "x"},
		{hdr: tar.Header{Name: "dev/null", Typeflag: tar.TypeChar, Mode: 0o666}},
		{hdr: tar.Header{Name: "data/fifo", Typeflag: tar.TypeFifo, Mode: 0o600}},
		{hdr: tar.Header{Name: "gameplane-backup.json", Typeflag: tar.TypeReg, Mode: 0o644}, body: "{}"},
		{hdr: tar.Header{Name: "/", Typeflag: tar.TypeDir, Mode: 0o755}},
	}
}

func readTarGz(t *testing.T, data []byte) ([]*tar.Header, map[string]string) {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	tr := tar.NewReader(gz)
	var hdrs []*tar.Header
	bodies := map[string]string{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %q: %v", h.Name, err)
		}
		hdrs = append(hdrs, h)
		bodies[h.Name] = string(b)
	}
	return hdrs, bodies
}

func readZip(t *testing.T, data []byte) []*zip.File {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip reader: %v", err)
	}
	return zr.File
}

func zipBody(t *testing.T, f *zip.File) string {
	t.Helper()
	rc, err := f.Open()
	if err != nil {
		t.Fatalf("open %q: %v", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", f.Name, err)
	}
	return string(b)
}

func TestTranscodeTarGzKeepsVolumesOnly(t *testing.T) {
	src := buildSource(t, fullSource())
	var out bytes.Buffer
	stats, err := Transcode(&out, bytes.NewReader(src), "tar.gz", testManifest())
	if err != nil {
		t.Fatalf("Transcode: %v", err)
	}
	if stats.Entries != 8 || stats.Skipped != 5 || stats.Bytes != 10 {
		t.Errorf("stats = %+v, want Entries 8, Skipped 5, Bytes 10", stats)
	}

	hdrs, bodies := readTarGz(t, out.Bytes())
	if len(hdrs) == 0 || hdrs[0].Name != ManifestName {
		t.Fatalf("first entry is not the manifest: %+v", hdrs)
	}
	if hdrs[0].Mode != 0o644 || !hdrs[0].ModTime.Equal(testCaptured) {
		t.Errorf("manifest header mode %o time %v, want 0644 %v", hdrs[0].Mode, hdrs[0].ModTime, testCaptured)
	}
	m, err := DecodeManifest(strings.NewReader(bodies[ManifestName]))
	if err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m.Writer != "test" || len(m.Volumes) != 2 {
		t.Errorf("manifest round trip = %+v", m)
	}

	got := map[string]*tar.Header{}
	for _, h := range hdrs {
		got[h.Name] = h
	}
	for _, name := range []string{"data", "data/a.txt", "volumes", "volumes/world/level.dat", "volumes/world/sub", "data/link", "volumes/world/hard"} {
		if _, ok := got[name]; !ok {
			t.Errorf("entry %q missing from archive; have %v", name, keys(got))
		}
	}
	for _, name := range []string{"etc/secret", "dev/null", "data/fifo", "gameplane-backup.json", "abs"} {
		if _, ok := got[name]; ok {
			t.Errorf("entry %q should have been skipped", name)
		}
	}
	if bodies["data/a.txt"] != "hello" || bodies["volumes/world/level.dat"] != "world" {
		t.Errorf("bodies = %v", bodies)
	}
	if link := got["data/link"]; link == nil || link.Typeflag != tar.TypeSymlink || link.Linkname != "a.txt" {
		t.Errorf("symlink header = %+v", link)
	}
	if hard := got["volumes/world/hard"]; hard == nil || hard.Typeflag != tar.TypeLink {
		t.Errorf("hard link header = %+v", hard)
	}
	if d := got["data"]; d == nil || d.Typeflag != tar.TypeDir {
		t.Errorf("directory header = %+v", d)
	}
}

func TestTranscodeZipKeepsVolumesOnly(t *testing.T) {
	src := buildSource(t, fullSource())
	var out bytes.Buffer
	stats, err := Transcode(&out, bytes.NewReader(src), "zip", testManifest())
	if err != nil {
		t.Fatalf("Transcode: %v", err)
	}
	// The hard link is skipped in zip output on top of the five skipped sources.
	if stats.Entries != 7 || stats.Skipped != 6 || stats.Bytes != 10 {
		t.Errorf("stats = %+v, want Entries 7, Skipped 6, Bytes 10", stats)
	}

	files := readZip(t, out.Bytes())
	if len(files) == 0 || files[0].Name != ManifestName {
		t.Fatalf("first entry is not the manifest: %v", zipNames(files))
	}
	if files[0].Mode().Perm() != 0o644 || !files[0].Modified.Equal(testCaptured) {
		t.Errorf("manifest mode %v modified %v", files[0].Mode(), files[0].Modified)
	}
	if !strings.Contains(zipBody(t, files[0]), `"formatVersion": 1`) {
		t.Errorf("manifest body = %q", zipBody(t, files[0]))
	}

	got := map[string]*zip.File{}
	for _, f := range files {
		got[f.Name] = f
	}
	for _, name := range []string{"data/", "volumes/", "volumes/world/sub/", "data/a.txt", "volumes/world/level.dat", "data/link"} {
		if _, ok := got[name]; !ok {
			t.Errorf("entry %q missing from zip; have %v", name, zipNames(files))
		}
	}
	if _, ok := got["volumes/world/hard"]; ok {
		t.Error("hard link must be skipped in zip output")
	}
	if d := got["data/"]; d == nil || !d.Mode().IsDir() {
		t.Errorf("directory entry mode = %v", d)
	}
	if l := got["data/link"]; l == nil || l.Mode()&fs.ModeSymlink == 0 || zipBody(t, l) != "a.txt" {
		t.Errorf("symlink entry = %+v", l)
	}
	if f := got["data/a.txt"]; f == nil || zipBody(t, f) != "hello" || f.Method != zip.Deflate {
		t.Errorf("file entry = %+v", f)
	}
}

func keys(m map[string]*tar.Header) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func zipNames(files []*zip.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}

func TestTranscodeEmptySourceWritesManifestOnly(t *testing.T) {
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			stats, err := Transcode(&out, bytes.NewReader(nil), format, Manifest{FormatVersion: 1})
			if err != nil {
				t.Fatalf("Transcode: %v", err)
			}
			if stats.Entries != 1 || stats.Skipped != 0 || stats.Bytes != 0 {
				t.Errorf("stats = %+v, want only the manifest", stats)
			}
		})
	}
}

func TestTranscodeZeroCapturedAtUsesEpoch(t *testing.T) {
	m := testManifest()
	m.CapturedAt = time.Time{}
	var out bytes.Buffer
	if _, err := Transcode(&out, bytes.NewReader(nil), "tar.gz", m); err != nil {
		t.Fatalf("Transcode: %v", err)
	}
	hdrs, _ := readTarGz(t, out.Bytes())
	if len(hdrs) != 1 || hdrs[0].ModTime.Unix() != 0 {
		t.Errorf("manifest ModTime = %v, want Unix epoch", hdrs)
	}
}

func TestTranscodeUnsupportedFormat(t *testing.T) {
	var out bytes.Buffer
	stats, err := Transcode(&out, bytes.NewReader(nil), "rar", testManifest())
	if err == nil || !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("err = %v, want unsupported format", err)
	}
	if stats != (Stats{}) || out.Len() != 0 {
		t.Errorf("stats %+v and %d output bytes, want none", stats, out.Len())
	}
}

func TestTranscodeRejectsDotDot(t *testing.T) {
	src := buildSource(t, []srcEntry{
		{hdr: tar.Header{Name: "data/../../etc/passwd", Typeflag: tar.TypeReg, Mode: 0o600}, body: "x"},
	})
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			_, err := Transcode(io.Discard, bytes.NewReader(src), format, testManifest())
			if err == nil || !strings.Contains(err.Error(), "unsafe archive entry") {
				t.Fatalf("err = %v, want unsafe archive entry", err)
			}
		})
	}
}

func TestTranscodeGarbageSourceFails(t *testing.T) {
	src := strings.NewReader("this is not a tar stream and is shorter than one block")
	_, err := Transcode(io.Discard, src, "zip", testManifest())
	if err == nil || !strings.Contains(err.Error(), "read archive") {
		t.Fatalf("err = %v, want read archive error", err)
	}
}

func TestTranscodeTruncatedBodyFails(t *testing.T) {
	big := strings.Repeat("z", 1000)
	full := buildSource(t, []srcEntry{
		{hdr: tar.Header{Name: "data/big.bin", Typeflag: tar.TypeReg, Mode: 0o644}, body: big},
	})
	// Keep the header block and only part of the body.
	src := full[:512+100]
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			_, err := Transcode(io.Discard, bytes.NewReader(src), format, testManifest())
			if err == nil || !strings.Contains(err.Error(), "big.bin") {
				t.Fatalf("err = %v, want copy error for data/big.bin", err)
			}
		})
	}
}

func TestTranscodeWriterErrorPropagates(t *testing.T) {
	src := buildSource(t, fullSource())
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			if _, err := Transcode(failWriter{}, bytes.NewReader(src), format, testManifest()); err == nil {
				t.Fatal("Transcode to a failing writer returned nil error")
			}
		})
	}
}

func TestTranscodeLargeWriterErrorPropagates(t *testing.T) {
	// Incompressible content forces the writers to flush during the copy.
	rng := rand.New(rand.NewSource(1))
	body := make([]byte, 1<<20)
	_, _ = rng.Read(body)
	src := buildSource(t, []srcEntry{
		{hdr: tar.Header{Name: "data/random.bin", Typeflag: tar.TypeReg, Mode: 0o644}, body: string(body)},
	})
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			if _, err := Transcode(failWriter{}, bytes.NewReader(src), format, testManifest()); err == nil {
				t.Fatal("Transcode to a failing writer returned nil error")
			}
		})
	}
}

func TestTranscodeSucceedsWithLargeContent(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	body := make([]byte, 256<<10)
	_, _ = rng.Read(body)
	src := buildSource(t, []srcEntry{
		{hdr: tar.Header{Name: "data/random.bin", Typeflag: tar.TypeReg, Mode: 0o644}, body: string(body)},
	})
	for _, format := range []string{"tar.gz", "zip"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			stats, err := Transcode(&out, bytes.NewReader(src), format, testManifest())
			if err != nil {
				t.Fatalf("Transcode: %v", err)
			}
			if stats.Bytes != int64(len(body)) {
				t.Errorf("Bytes = %d, want %d", stats.Bytes, len(body))
			}
		})
	}
}

func TestKeptNameRules(t *testing.T) {
	folders := []string{"data", "volumes/world"}
	tests := []struct {
		name string
		hdr  tar.Header
		want string
		keep bool
	}{
		{"folder itself", tar.Header{Name: "data", Typeflag: tar.TypeDir}, "data", true},
		{"nested", tar.Header{Name: "volumes/world/a/b", Typeflag: tar.TypeReg}, "volumes/world/a/b", true},
		{"volumes parent", tar.Header{Name: "volumes", Typeflag: tar.TypeDir}, "volumes", true},
		{"prefix sibling", tar.Header{Name: "database/x", Typeflag: tar.TypeReg}, "", false},
		{"other volume", tar.Header{Name: "volumes/other/x", Typeflag: tar.TypeReg}, "", false},
		{"device", tar.Header{Name: "data/dev", Typeflag: tar.TypeBlock}, "", false},
		{"root", tar.Header{Name: "./", Typeflag: tar.TypeDir}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, keep, err := keptName(&tt.hdr, folders)
			if err != nil {
				t.Fatalf("keptName: %v", err)
			}
			if keep != tt.keep || got != tt.want {
				t.Errorf("keptName(%q) = %q, %v; want %q, %v", tt.hdr.Name, got, keep, tt.want, tt.keep)
			}
		})
	}
}
