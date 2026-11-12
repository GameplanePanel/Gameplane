package files

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFileSelectionArchive(t *testing.T) {
	address, root := newServer(t)
	for _, folder := range []string{"first", "second"} {
		if err := os.Mkdir(filepath.Join(root, folder), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, folder, "config"), []byte(folder), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	paths := url.Values{"path": {"/first/config", "/second/config"}}
	response := get(t, address, "/files/archive", paths)
	payload, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("download: status %d, err %v", response.StatusCode, err)
	}
	archive, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 2 || archive.File[0].Name != "first/config" || archive.File[1].Name != "second/config" {
		t.Fatalf("missing folder structure: %v", archive.File)
	}
	paths.Set("dest", "/selection.zip")
	for _, want := range []int{http.StatusCreated, http.StatusConflict} {
		response, err = testPost(t, address+"/files/archive?"+paths.Encode(), "application/octet-stream", nil)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("save archive: got %d want %d", response.StatusCode, want)
		}
	}
	for _, item := range []struct {
		paths  []string
		status int
	}{
		{[]string{"/no-such-file"}, 404}, {[]string{"/first", "/first/config"}, 400}, {[]string{"/first", "/first"}, 400},
		{[]string{"/.state"}, 400}, {[]string{"/"}, 400},
	} {
		res := get(t, address, "/files/archive", url.Values{"path": item.paths})
		res.Body.Close()
		if res.StatusCode != item.status {
			t.Errorf("%v: status %d want %d", item.paths, res.StatusCode, item.status)
		}
	}
}

func TestArchiveDoesNotPublishRefusedSelection(t *testing.T) {
	address, root := newServer(t)
	folder := filepath.Join(root, "folder")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name     string
		want     int
		populate func(string) error
	}{
		{"hidden", 400, func(p string) error { return os.WriteFile(filepath.Join(p, ".private"), []byte("secret"), 0o600) }},
		{"symlink", 400, func(p string) error { return os.Symlink(t.TempDir(), filepath.Join(p, "link")) }},
		{"large", 413, func(p string) error {
			name := filepath.Join(p, "large")
			if err := os.WriteFile(name, nil, 0o600); err != nil {
				return err
			}
			return os.Truncate(name, maxArchiveBytes+1)
		}},
	} {
		t.Run(item.name, func(t *testing.T) {
			child := filepath.Join(folder, item.name)
			if err := os.Mkdir(child, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := item.populate(child); err != nil {
				t.Fatal(err)
			}
			q := url.Values{"path": {"/folder/" + item.name}, "dest": {"/result.zip"}}
			getResult := get(t, address, "/files/archive", q)
			getResult.Body.Close()
			post, err := testPost(t, address+"/files/archive?"+q.Encode(), "application/octet-stream", nil)
			if err != nil {
				t.Fatal(err)
			}
			post.Body.Close()
			if post.StatusCode != item.want || getResult.StatusCode != item.want {
				t.Errorf("GET %d POST %d want %d", getResult.StatusCode, post.StatusCode, item.want)
			}
			if _, err := os.Stat(filepath.Join(root, "result.zip")); !os.IsNotExist(err) {
				t.Fatalf("published refused archive: %v", err)
			}
		})
	}
}

func TestArchiveRequestBoundaries(t *testing.T) {
	h := &handler{root: t.TempDir()}
	request := func(method string, paths url.Values, want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.archive(rec, httptest.NewRequestWithContext(t.Context(), method, "/files/archive?"+paths.Encode(), nil))
		if rec.Code != want {
			t.Fatalf("status=%d want=%d body=%s", rec.Code, want, rec.Body.String())
		}
	}
	request(http.MethodGet, nil, http.StatusBadRequest)
	request(http.MethodGet, url.Values{"path": make([]string, maxArchiveSelection+1)}, http.StatusBadRequest)
	for _, dest := range []string{"", "/.private/a.zip", "/source.txt", "/source/a.zip"} {
		request(http.MethodPost, url.Values{"path": {"/source"}, "dest": {dest}}, http.StatusBadRequest)
	}
	h.archiveLock.Lock()
	request(http.MethodGet, url.Values{"path": {"/source"}}, http.StatusConflict)
	h.archiveLock.Unlock()
	h.root = filepath.Join(h.root, "missing")
	request(http.MethodGet, url.Values{"path": {"/source"}}, http.StatusNotFound)
}

func TestArchiveFoldersAndSpecialEntries(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "world")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "one"), []byte("contents"), 0600); err != nil {
		t.Fatal(err)
	}
	h := &handler{root: root}
	archive := func(w io.Writer) *selectionArchive {
		return &selectionArchive{h: h, zip: zip.NewWriter(w), ctx: t.Context(), names: make(map[string]bool)}
	}
	var buf bytes.Buffer
	a := archive(&buf)
	if err := a.add([]string{"world"}, "world"); err != nil {
		t.Fatal(err)
	}
	if err := a.add([]string{"world"}, "world"); !errors.Is(err, errBadPath) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := a.zip.Close(); err != nil {
		t.Fatal(err)
	}
	result, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil || len(result.File) != 2 || result.File[0].Name != "world/" || result.File[1].Name != "world/one" {
		t.Fatalf("archive: %+v, %v", result, err)
	}
	if err = unix.Mkfifo(filepath.Join(root, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = archive(io.Discard).add([]string{"pipe"}, "pipe"); !errors.Is(err, errBadPath) {
		t.Fatalf("FIFO: %v", err)
	}
	if err = os.WriteFile(filepath.Join(folder, "bad:name"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = archive(io.Discard).add([]string{"world"}, "world"); !errors.Is(err, errBadPath) {
		t.Fatalf("unportable ZIP member: %v", err)
	}
	if err = archive(io.Discard).add(make([]string, 129), "deep"); !errors.Is(err, errArchiveLimit) {
		t.Fatalf("depth: %v", err)
	}
}

type rejectedArchiveOutput struct{}

func (rejectedArchiveOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestArchiveStopsOnCanceledReadsAndWriteErrors(t *testing.T) {
	root := t.TempDir()
	h := &handler{root: root}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a := selectionArchive{h: h, zip: zip.NewWriter(io.Discard), ctx: ctx, names: map[string]bool{}}
	if err := a.add([]string{"file"}, "file"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled archive: %v", err)
	}
	if _, err := (archiveReader{ctx, strings.NewReader("text")}).Read(make([]byte, 4)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read: %v", err)
	}
	a.ctx = t.Context()
	data := make([]byte, 1<<18)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "large"), data, 0600); err != nil {
		t.Fatal(err)
	}
	a.zip = zip.NewWriter(rejectedArchiveOutput{})
	if err := a.add([]string{"large"}, "large"); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write error: %v", err)
	}
	for _, path := range []string{"large", ""} {
		a.zip = zip.NewWriter(io.Discard)
		a.names = map[string]bool{}
		var parts []string
		if path != "" {
			parts = []string{path}
		}
		if err := a.add(parts, strings.Repeat("a", 1<<16)); err == nil {
			t.Fatal("accepted oversized ZIP member name")
		}
	}
	dir, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	info, err := dir.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err = dir.Close(); err != nil {
		t.Fatal(err)
	}
	if err = a.addFolder(nil, "closed", dir, info); err == nil {
		t.Fatal("closed directory unexpectedly readable")
	}
}
