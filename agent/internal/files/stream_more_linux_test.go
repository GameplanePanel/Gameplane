package files

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestMountWithMaxUploadBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		opt  Option
		want int
	}{
		{"limit applies", WithMaxUploadBytes(4), http.StatusRequestEntityTooLarge},
		{"non-positive keeps default", WithMaxUploadBytes(0), http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := resolvedTempDir(t)
			r := chi.NewRouter()
			Mount(r, root, tc.opt)
			srv := httptest.NewServer(r)
			t.Cleanup(srv.Close)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, srv.URL+"/files/stream?path=/f.txt", strings.NewReader("12345"))
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("status=%d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestStream_ResumeSourceSwappedForDirectoryRejected(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	target := filepath.Join(root, "f.txt")
	put(t, target, "abc")
	// The destination check sees a regular file; it becomes a directory
	// before the resume prefix is opened.
	raceAt(t, h, "open", "", "f.txt", 1, func() {
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(target, 0o755); err != nil {
			t.Fatal(err)
		}
	})
	resp := streamTo(t, h, "/files/stream?path=/f.txt&offset=1", strings.NewReader("x"))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.Code)
	}
	assertNoStreamTemp(t, root)
}

func TestStream_FreeSpaceErrorFails(t *testing.T) {
	h, root, _ := raceHandler(t)
	h.maxUploadBytes = DefaultMaxUploadBytes
	h.freeBytes = func(int) (uint64, error) { return 0, errors.New("statfs failed") }
	resp := streamTo(t, h, "/files/stream?path=/f.txt", strings.NewReader("abc"))
	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", resp.Code)
	}
	if _, err := os.Stat(filepath.Join(root, "f.txt")); !os.IsNotExist(err) {
		t.Fatalf("file written despite failed free-space check: %v", err)
	}
}

func TestFreeUsesStatfs(t *testing.T) {
	h := &handler{root: resolvedTempDir(t)}
	fd, err := openRoot(h.root)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFD(fd)
	if free, err := h.free(fd); err != nil || free == 0 {
		t.Fatalf("free=%d err=%v, want space on the test volume", free, err)
	}
	if _, err := h.free(-1); err == nil {
		t.Fatal("free on a bad descriptor succeeded")
	}
}

func TestRoutesFailWhenRootIsMissing(t *testing.T) {
	h := &handler{root: filepath.Join(resolvedTempDir(t), "gone"), maxUploadBytes: DefaultMaxUploadBytes}
	for name, rr := range map[string]*httptest.ResponseRecorder{
		"stat":   serve(t, h.stat, http.MethodGet, "/files/stat?path=/a", nil),
		"stream": serve(t, h.stream, http.MethodPut, "/files/stream?path=/a", strings.NewReader("x")),
		"rename": serve(t, h.rename, http.MethodPost, "/files/rename", renameRequest(t, "/a", "/b", false)),
	} {
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s: status=%d, want 404", name, rr.Code)
		}
	}
}

func TestRename_MissingSourceParentNotFound(t *testing.T) {
	h, _, _ := raceHandler(t)
	if resp := doRename(t, h, "/nope/a.txt", "/b.txt", false); resp.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.Code)
	}
}

func TestRename_DirectoryIntoItselfRejected(t *testing.T) {
	h, root, _ := raceHandler(t)
	if err := os.MkdirAll(filepath.Join(root, "d", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	resp := doRename(t, h, "/d", "/d/sub/d", false)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", resp.Code)
	}
	if info, err := os.Stat(filepath.Join(root, "d", "sub")); err != nil || !info.IsDir() {
		t.Fatalf("source tree changed: %v", err)
	}
}

func TestRename_DirectoryOverFileFails(t *testing.T) {
	h, root, _ := raceHandler(t)
	if err := os.Mkdir(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "f.txt"), "keep")
	resp := doRename(t, h, "/d", "/f.txt", true)
	if resp.Code < 400 {
		t.Fatalf("status=%d, want an error", resp.Code)
	}
	if got, err := os.ReadFile(filepath.Join(root, "f.txt")); err != nil || string(got) != "keep" {
		t.Fatalf("destination changed: %q %v", got, err)
	}
}
