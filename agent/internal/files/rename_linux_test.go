package files

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renameRequest builds the JSON body of POST /files/rename.
func renameRequest(t *testing.T, from, to string, overwrite bool) *strings.Reader {
	t.Helper()
	b, err := json.Marshal(renameBody{From: from, To: to, Overwrite: overwrite})
	if err != nil {
		t.Fatalf("marshal rename body: %v", err)
	}
	return strings.NewReader(string(b))
}

func doRename(t *testing.T, h *handler, from, to string, overwrite bool) *http.Response {
	t.Helper()
	rr := serve(t, h.rename, http.MethodPost, "/files/rename", renameRequest(t, from, to, overwrite))
	return rr.Result()
}

func TestRename_FileAcrossDirectories(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, "a", "src.txt"), "moved")
	if err := os.Mkdir(filepath.Join(root, "b"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	resp := doRename(t, h, "/a/src.txt", "/b/dst.txt", false)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", resp.StatusCode)
	}
	got, err := os.ReadFile(filepath.Join(root, "b", "dst.txt"))
	if err != nil || string(got) != "moved" {
		t.Fatalf("dst content=%q err=%v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "a", "src.txt")); !os.IsNotExist(err) {
		t.Fatalf("source still present: err=%v", err)
	}
}

func TestRename_NoOverwriteConflictKeepsBoth(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, "a.txt"), "A")
	put(t, filepath.Join(root, "b.txt"), "B")
	resp := doRename(t, h, "/a.txt", "/b.txt", false)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d, want 409", resp.StatusCode)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(got) != "A" {
		t.Fatalf("a.txt changed: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "b.txt")); string(got) != "B" {
		t.Fatalf("b.txt changed: %q", got)
	}
}

func TestRename_OverwriteReplaces(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, "a.txt"), "new")
	put(t, filepath.Join(root, "b.txt"), "old")
	resp := doRename(t, h, "/a.txt", "/b.txt", true)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d, want 204", resp.StatusCode)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "b.txt")); string(got) != "new" {
		t.Fatalf("b.txt content=%q, want new", got)
	}
	if _, err := os.Lstat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatalf("source still present: err=%v", err)
	}
}

func TestRename_RootRefused(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, "a.txt"), "A")
	if resp := doRename(t, h, "/", "/x", false); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("from root: status=%d, want 400", resp.StatusCode)
	}
	if resp := doRename(t, h, "/a.txt", "/", true); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("to root: status=%d, want 400", resp.StatusCode)
	}
}

func TestRename_DotfileRefused(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, ".secret"), "s")
	put(t, filepath.Join(root, "a.txt"), "A")
	if resp := doRename(t, h, "/.secret", "/x.txt", false); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("dotfile source: status=%d, want 400", resp.StatusCode)
	}
	if resp := doRename(t, h, "/a.txt", "/.hidden", false); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("dotfile destination: status=%d, want 400", resp.StatusCode)
	}
}

func TestRename_MissingSourceNotFound(t *testing.T) {
	h, _, _ := raceHandler(t)
	if resp := doRename(t, h, "/nope.txt", "/x.txt", false); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}

func TestRename_MissingDestinationParentNotFound(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, "a.txt"), "A")
	if resp := doRename(t, h, "/a.txt", "/missing/x.txt", false); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
	if _, err := os.Lstat(filepath.Join(root, "a.txt")); err != nil {
		t.Fatalf("source disturbed: %v", err)
	}
}

func TestRename_BadJSONRejected(t *testing.T) {
	h, _, _ := raceHandler(t)
	rr := serve(t, h.rename, http.MethodPost, "/files/rename", strings.NewReader("{not json"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rr.Code)
	}
}

func TestRename_DestinationParentSwappedForSymlinkRejected(t *testing.T) {
	h, root, outside := raceHandler(t)
	put(t, filepath.Join(root, "src.txt"), "payload")
	if err := os.Mkdir(filepath.Join(root, "dst"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raceAt(t, h, "open", "", "dst", 1, func() {
		swapForSymlink(t, filepath.Join(root, "dst"), outside)
	})
	rr := serve(t, h.rename, http.MethodPost, "/files/rename", renameRequest(t, "/src.txt", "/dst/moved.txt", false))
	assertRejected(t, rr)
	assertOutsideUntouched(t, outside)
	if got, err := os.ReadFile(filepath.Join(root, "src.txt")); err != nil || string(got) != "payload" {
		t.Fatalf("source not left in place: got %q err=%v", got, err)
	}
}
