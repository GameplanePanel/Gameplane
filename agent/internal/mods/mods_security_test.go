package mods

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
)

// secEntry is one member of a test archive. A name with a trailing slash is
// a directory entry and carries no body.
type secEntry struct {
	name string
	body string
}

// secZip builds a stored (uncompressed) zip from entries, in the given order.
func secZip(t *testing.T, entries []secEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip header %s: %v", e.name, err)
		}
		if strings.HasSuffix(e.name, "/") {
			continue
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatalf("zip write %s: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// secReader parses data as a zip archive.
func secReader(t *testing.T, data []byte) *zip.Reader {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip reader: %v", err)
	}
	return zr
}

// secRoot opens a fresh os.Root on a temp directory.
func secRoot(t *testing.T) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

// secWriteFile writes data to path and returns path.
func secWriteFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// secWriteZip writes an archive of entries to a new file in dir and returns it.
func secWriteZip(t *testing.T, dir, name string, entries []secEntry) string {
	t.Helper()
	return secWriteFile(t, filepath.Join(dir, name), secZip(t, entries))
}

// secHandler returns a handler whose mods dir ("mods") exists under a fresh
// data root, and that root.
func secHandler(t *testing.T) (*handler, string) {
	t.Helper()
	root := t.TempDir()
	h := newHandler(root, modsSpec("mods", nil))
	if err := os.MkdirAll(h.dir, 0o755); err != nil {
		t.Fatalf("mkdir mods: %v", err)
	}
	return h, root
}

// secSkipIfRoot skips tests that rely on permission bits, which root bypasses.
func secSkipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root — permission bits are bypassed")
	}
}

// secNoStaging fails the test if a swap left an ".ex-" staging dir in dir.
func secNoStaging(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ex-") {
			t.Fatalf("staging dir left behind: %s", e.Name())
		}
	}
}

// secNoTemp fails the test if a download left a ".dl-" temp file in dir.
func secNoTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".dl-") {
			t.Fatalf("download temp file left behind: %s", e.Name())
		}
	}
}

// secPatchMethod rewrites the compression method of every local and central
// directory header in a stored zip, so readers meet an unsupported algorithm.
func secPatchMethod(data []byte, method uint16) []byte {
	out := bytes.Clone(data)
	for i := 0; i+12 <= len(out); i++ {
		switch {
		case bytes.HasPrefix(out[i:], []byte("PK\x03\x04")):
			out[i+8] = byte(method)
			out[i+9] = byte(method >> 8)
		case bytes.HasPrefix(out[i:], []byte("PK\x01\x02")):
			out[i+10] = byte(method)
			out[i+11] = byte(method >> 8)
		}
	}
	return out
}

func TestSwapInArchive_InstallsFolder(t *testing.T) {
	h, root := secHandler(t)
	zp := secWriteZip(t, t.TempDir(), "mod.zip", []secEntry{
		{name: "plugins/"},
		{name: "plugins/Cool.dll", body: "DLLBYTES"},
		{name: "manifest.json", body: `{"name":"Cool"}`},
	})
	if err := h.swapInArchive(zp, "Cool-1.0", defaultMaxBytes); err != nil {
		t.Fatalf("swap: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "mods", "Cool-1.0", "plugins", "Cool.dll"))
	if err != nil || string(got) != "DLLBYTES" {
		t.Fatalf("installed dll = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "Cool-1.0", "manifest.json")); err != nil {
		t.Fatalf("manifest not installed: %v", err)
	}
	secNoStaging(t, h.dir)
}

func TestSwapInArchive_ReplacesExistingFolder(t *testing.T) {
	h, _ := secHandler(t)
	old := filepath.Join(h.dir, "Cool-1.0")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatalf("mkdir old: %v", err)
	}
	secWriteFile(t, filepath.Join(old, "stale.txt"), []byte("old"))
	zp := secWriteZip(t, t.TempDir(), "mod.zip", []secEntry{{name: "new.dll", body: "NEW"}})
	if err := h.swapInArchive(zp, "Cool-1.0", defaultMaxBytes); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if _, err := os.Stat(filepath.Join(old, "stale.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale file survived the swap: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(old, "new.dll"))
	if err != nil || string(got) != "NEW" {
		t.Fatalf("new file = %q, %v", got, err)
	}
	secNoStaging(t, h.dir)
}

func TestSwapInArchive_NonRegularArchiveIsBadArchive(t *testing.T) {
	h, _ := secHandler(t)
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe.zip")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	if err := h.swapInArchive(fifo, "Cool", defaultMaxBytes); !errors.Is(err, errBadArchive) {
		t.Fatalf("FIFO archive: got %v, want errBadArchive", err)
	}
	// A directory is also not a regular file.
	if err := h.swapInArchive(dir, "Cool", defaultMaxBytes); !errors.Is(err, errBadArchive) {
		t.Fatalf("directory archive: got %v, want errBadArchive", err)
	}
}

func TestSwapInArchive_GarbageArchiveIsBadArchive(t *testing.T) {
	h, _ := secHandler(t)
	zp := secWriteFile(t, filepath.Join(t.TempDir(), "bad.zip"), []byte("this is not a zip archive"))
	if err := h.swapInArchive(zp, "Cool", defaultMaxBytes); !errors.Is(err, errBadArchive) {
		t.Fatalf("garbage archive: got %v, want errBadArchive", err)
	}
}

func TestSwapInArchive_MissingArchiveErrors(t *testing.T) {
	h, _ := secHandler(t)
	if err := h.swapInArchive(filepath.Join(t.TempDir(), "absent.zip"), "Cool", defaultMaxBytes); err == nil {
		t.Fatal("missing archive: want error")
	}
}

func TestSwapInArchive_OversizedIsTooLarge(t *testing.T) {
	h, _ := secHandler(t)
	zp := secWriteZip(t, t.TempDir(), "big.zip", []secEntry{{name: "big.bin", body: strings.Repeat("x", 1000)}})
	if err := h.swapInArchive(zp, "Big", 10); !errors.Is(err, errTooLarge) {
		t.Fatalf("oversized archive: got %v, want errTooLarge", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "Big")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized archive installed a folder: %v", err)
	}
	secNoStaging(t, h.dir)
}

func TestSwapInArchive_TraversalEntryRejected(t *testing.T) {
	h, root := secHandler(t)
	zp := secWriteZip(t, t.TempDir(), "evil.zip", []secEntry{
		{name: "ok.txt", body: "ok"},
		{name: "../escape.txt", body: "pwned"},
	})
	if err := h.swapInArchive(zp, "Evil", defaultMaxBytes); !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("traversal entry: got %v, want ErrEscapesRoot", err)
	}
	if _, err := os.Stat(filepath.Join(root, "escape.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("traversal entry wrote outside the mods dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "Evil")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed archive installed a folder: %v", err)
	}
	secNoStaging(t, h.dir)
}

func TestSwapInArchive_StagingDirNotWritable(t *testing.T) {
	secSkipIfRoot(t)
	h, _ := secHandler(t)
	zp := secWriteZip(t, t.TempDir(), "mod.zip", []secEntry{{name: "a.txt", body: "a"}})
	if err := os.Chmod(h.dir, 0o555); err != nil {
		t.Fatalf("chmod mods: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(h.dir, 0o755) })
	if err := h.swapInArchive(zp, "Cool", defaultMaxBytes); err == nil {
		t.Fatal("swap into read-only mods dir: want error")
	}
}

func TestSwapInArchive_RemoveOldFolderFails(t *testing.T) {
	secSkipIfRoot(t)
	h, _ := secHandler(t)
	locked := filepath.Join(h.dir, "Cool", "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatalf("mkdir locked: %v", err)
	}
	secWriteFile(t, filepath.Join(locked, "x.txt"), []byte("x"))
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatalf("chmod locked: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
	zp := secWriteZip(t, t.TempDir(), "mod.zip", []secEntry{{name: "a.txt", body: "a"}})
	if err := h.swapInArchive(zp, "Cool", defaultMaxBytes); err == nil {
		t.Fatal("swap over an undeletable folder: want error")
	}
	secNoStaging(t, h.dir)
}

func TestUnzipInto_NestedDirectories(t *testing.T) {
	zp := secWriteZip(t, t.TempDir(), "nested.zip", []secEntry{
		{name: "a/"},
		{name: "a/b/c/deep.txt", body: "deep"},
		{name: "empty/"},
		{name: "top.txt", body: "top"},
	})
	dst := filepath.Join(t.TempDir(), "out")
	if err := unzipInto(zp, dst, defaultMaxBytes); err != nil {
		t.Fatalf("unzip: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "a", "b", "c", "deep.txt"))
	if err != nil || string(got) != "deep" {
		t.Fatalf("deep.txt = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "top.txt")); err != nil || string(got) != "top" {
		t.Fatalf("top.txt = %q, %v", got, err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "empty")); err != nil || !fi.IsDir() {
		t.Fatalf("empty dir entry: %v", err)
	}
}

func TestUnzipInto_DestinationIsFile(t *testing.T) {
	dst := secWriteFile(t, filepath.Join(t.TempDir(), "blocker"), []byte("file"))
	zp := secWriteZip(t, t.TempDir(), "m.zip", []secEntry{{name: "x.txt", body: "x"}})
	if err := unzipInto(zp, dst, defaultMaxBytes); err == nil {
		t.Fatal("destination is a file: want error")
	}
}

func TestUnzipInto_UnreadableDestination(t *testing.T) {
	secSkipIfRoot(t)
	dst := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(dst, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dst, 0o755) })
	zp := secWriteZip(t, t.TempDir(), "m.zip", []secEntry{{name: "x.txt", body: "x"}})
	if err := unzipInto(zp, dst, defaultMaxBytes); err == nil {
		t.Fatal("unreadable destination: want error")
	}
}

func TestExtractZip_EntryCountCap(t *testing.T) {
	entries := make([]secEntry, 0, maxArchiveEntries+1)
	for i := range maxArchiveEntries + 1 {
		entries = append(entries, secEntry{name: fmt.Sprintf("f%05d", i)})
	}
	zr := secReader(t, secZip(t, entries))
	if err := extractZip(zr, secRoot(t), defaultMaxBytes); !errors.Is(err, errTooLarge) {
		t.Fatalf("%d entries: got %v, want errTooLarge", len(entries), err)
	}
}

func TestExtractZip_EscapingEntriesRejected(t *testing.T) {
	for _, name := range []string{"../escape.txt", "/abs/escape.txt", "a/../../escape.txt"} {
		t.Run(name, func(t *testing.T) {
			zr := secReader(t, secZip(t, []secEntry{{name: name, body: "x"}}))
			if err := extractZip(zr, secRoot(t), defaultMaxBytes); !errors.Is(err, ErrEscapesRoot) {
				t.Fatalf("entry %q: got %v, want ErrEscapesRoot", name, err)
			}
		})
	}
}

func TestExtractZip_DirectoryEntryOverFileFails(t *testing.T) {
	zr := secReader(t, secZip(t, []secEntry{
		{name: "a", body: "file"},
		{name: "a/"},
	}))
	if err := extractZip(zr, secRoot(t), defaultMaxBytes); err == nil {
		t.Fatal("directory entry over a file: want error")
	}
}

func TestExtractZip_ParentDirectoryOverFileFails(t *testing.T) {
	zr := secReader(t, secZip(t, []secEntry{
		{name: "blk", body: "file"},
		{name: "blk/inner.txt", body: "x"},
	}))
	if err := extractZip(zr, secRoot(t), defaultMaxBytes); err == nil {
		t.Fatal("parent directory over a file: want error")
	}
}

func TestExtractEntry_FileOverDirectoryFails(t *testing.T) {
	zr := secReader(t, secZip(t, []secEntry{
		{name: "a/b.txt", body: "x"},
		{name: "a", body: "file"},
	}))
	if err := extractZip(zr, secRoot(t), defaultMaxBytes); err == nil {
		t.Fatal("file entry over a directory: want error")
	}
}

func TestExtractEntry_UnsupportedMethodFails(t *testing.T) {
	data := secPatchMethod(secZip(t, []secEntry{{name: "x.txt", body: "x"}}), 99)
	zr := secReader(t, data)
	if err := extractZip(zr, secRoot(t), defaultMaxBytes); err == nil {
		t.Fatal("unsupported compression method: want error")
	}
}

func TestExtractEntry_CorruptDataFails(t *testing.T) {
	data := secZip(t, []secEntry{{name: "data.txt", body: "CORRUPTME"}})
	if !bytes.Contains(data, []byte("CORRUPTME")) {
		t.Fatal("payload not found in archive")
	}
	// Same length, so the headers stay valid and only the CRC check fails.
	data = bytes.Replace(data, []byte("CORRUPTME"), []byte("CORRUPTMF"), 1)
	zr := secReader(t, data)
	if err := extractZip(zr, secRoot(t), defaultMaxBytes); err == nil {
		t.Fatal("corrupt entry data: want error")
	}
}

func TestFillStaging_MissingStagingDirFails(t *testing.T) {
	zr := secReader(t, secZip(t, nil))
	if err := fillStaging(secRoot(t), "does-not-exist", zr, defaultMaxBytes); err == nil {
		t.Fatal("missing staging dir: want error")
	}
}

func TestPublishTemp_MissingTempFileFails(t *testing.T) {
	h, _ := secHandler(t)
	if err := h.publishTemp(filepath.Join(h.dir, ".missing-temp"), "x.jar"); err == nil {
		t.Fatal("missing temp file: want error")
	}
}

func TestPublishTemp_RenameOverDirectoryFails(t *testing.T) {
	h, _ := secHandler(t)
	tmp := secWriteFile(t, filepath.Join(h.dir, ".tmp-ok"), []byte("data"))
	blocked := filepath.Join(h.dir, "blocked.jar")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	secWriteFile(t, filepath.Join(blocked, "keep.txt"), []byte("keep"))
	if err := h.publishTemp(tmp, "blocked.jar"); err == nil {
		t.Fatal("rename over a directory: want error")
	}
}

func TestPublishTemp_DirOutsideDataRootFails(t *testing.T) {
	dataRoot := t.TempDir()
	other := t.TempDir()
	h := &handler{dataRoot: filepath.Clean(dataRoot), dir: filepath.Join(other, "mods")}
	tmp := secWriteFile(t, filepath.Join(other, ".tmp"), []byte("data"))
	if err := h.publishTemp(tmp, "x.jar"); !errors.Is(err, ErrEscapesRoot) {
		t.Fatalf("mods dir outside data root: got %v, want ErrEscapesRoot", err)
	}
}

func TestUpload_PublishFailureIs500(t *testing.T) {
	root := t.TempDir()
	srv := newSrv(t, root, modsSpec("mods", nil))
	blocked := filepath.Join(root, "mods", "blocked.jar")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	secWriteFile(t, filepath.Join(blocked, "keep.txt"), []byte("keep"))
	status, _ := postUpload(t, srv, "file", "blocked.jar", []byte("payload"))
	if status != http.StatusInternalServerError {
		t.Fatalf("publish over a directory: status=%d, want 500", status)
	}
	secNoTemp(t, filepath.Join(root, "mods"))
}

func TestDownload_PublishFailureRemovesTemp(t *testing.T) {
	root := t.TempDir()
	h := newHandler(root, &caps.Mods{
		Path: "mods",
		Install: &caps.ModInstall{
			AllowedHosts: []string{"example.com"},
			MaxSizeMB:    1,
		},
	})
	h.client = mockHTTPClient("payload")
	blocked := filepath.Join(h.dir, "blocked.jar")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	secWriteFile(t, filepath.Join(blocked, "keep.txt"), []byte("keep"))
	if _, err := h.download(t.Context(), "http://example.com/blocked.jar", "blocked.jar"); err == nil {
		t.Fatal("publish over a directory: want error")
	}
	secNoTemp(t, h.dir)
}

func TestNewSafeClient_RedirectDropsReferer(t *testing.T) {
	allowLoopback(t)
	referers := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referers <- r.Header.Get("Referer")
		_, _ = w.Write([]byte("jar"))
	}))
	t.Cleanup(target.Close)
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/mod.jar?token=secret", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)

	client := newSafeClient([]string{"127.0.0.1"})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, redirector.URL+"/start", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Referer", "http://127.0.0.1/start?token=secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200 after redirect", resp.StatusCode)
	}
	if got := <-referers; got != "" {
		t.Fatalf("Referer reached the redirect target: %q", got)
	}
}
