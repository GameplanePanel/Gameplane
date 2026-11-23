package files

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func statFor(t *testing.T, h *handler, target string) (int, Entry, string) {
	t.Helper()
	rr := serve(t, h.stat, http.MethodGet, target, nil)
	var e Entry
	if rr.Code == http.StatusOK {
		if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
			t.Fatalf("decode entry: %v (body %q)", err, rr.Body.String())
		}
	}
	return rr.Code, e, rr.Body.String()
}

func TestStat_File(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, "a", "b.txt"), "hello")
	code, e, body := statFor(t, h, "/files/stat?path=/a/b.txt")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", code, body)
	}
	if e.Name != "b.txt" || e.Path != "/a/b.txt" || e.Size != 5 || e.Dir {
		t.Fatalf("entry=%+v, want file b.txt at /a/b.txt size 5 dir=false", e)
	}
}

func TestStat_Directory(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, "a", "b.txt"), "hello")
	code, e, body := statFor(t, h, "/files/stat?path=/a")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", code, body)
	}
	if e.Name != "a" || e.Path != "/a" || !e.Dir {
		t.Fatalf("entry=%+v, want directory a at /a", e)
	}
}

func TestStat_Root(t *testing.T) {
	h, _, _ := raceHandler(t)
	code, e, body := statFor(t, h, "/files/stat?path=/")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", code, body)
	}
	if e.Path != "/" || !e.Dir {
		t.Fatalf("entry=%+v, want root with path / and dir=true", e)
	}
}

func TestStat_Missing(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, "a", "b.txt"), "hello")
	for _, target := range []string{"/files/stat?path=/nope", "/files/stat?path=/nope/child"} {
		if code, _, body := statFor(t, h, target); code != http.StatusNotFound {
			t.Fatalf("%s: status=%d body=%q, want 404", target, code, body)
		}
	}
}

func TestStat_DotfileRejected(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, ".env"), "secret")
	if code, _, body := statFor(t, h, "/files/stat?path=/.env"); code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q, want 400", code, body)
	}
}

func TestStat_DotDotEscapeRejected(t *testing.T) {
	h, _, _ := raceHandler(t)
	if code, _, body := statFor(t, h, "/files/stat?path=/../etc/passwd"); code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%q, want 400", code, body)
	}
}

func TestStat_SymlinkReportedNotFollowed(t *testing.T) {
	h, root, _ := raceHandler(t)
	put(t, filepath.Join(root, "a", "b.txt"), "hello")
	if err := os.Symlink(filepath.Join(root, "a", "b.txt"), filepath.Join(root, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	code, e, body := statFor(t, h, "/files/stat?path=/link")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%q, want 200", code, body)
	}
	if e.Name != "link" || e.Dir || !strings.HasPrefix(e.Mode, "L") {
		t.Fatalf("entry=%+v, want symlink link with mode starting L and dir=false", e)
	}
}

func TestStat_AncestorSwappedForSymlinkRejected(t *testing.T) {
	h, root, outside := raceHandler(t)
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	put(t, filepath.Join(root, "sub", "f.txt"), "inside")
	raceAt(t, h, "open", "", "sub", 1, func() {
		swapForSymlink(t, filepath.Join(root, "sub"), outside)
	})
	rr := serve(t, h.stat, http.MethodGet, "/files/stat?path=/sub/f.txt", nil)
	assertRejected(t, rr)
	assertOutsideUntouched(t, outside)
}
