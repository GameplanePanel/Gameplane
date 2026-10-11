package files

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func assertNoCreateTemp(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".create-") {
			t.Fatalf("temporary file remains: %s", entry.Name())
		}
	}
}

func TestCreateHTTPRefusesExistingEntries(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			url, root := newServer(t)
			target := filepath.Join(root, "existing")
			switch kind {
			case "file":
				put(t, target, "keep this content")
			case "directory":
				put(t, filepath.Join(target, "child"), "keep child")
			case "symlink":
				// A dotfile target must still report a collision, without inspecting it.
				put(t, filepath.Join(root, ".secret"), "keep secret")
				if err := os.Symlink(".secret", target); err != nil {
					t.Fatal(err)
				}
			}
			var before unix.Stat_t
			if err := unix.Lstat(target, &before); err != nil {
				t.Fatal(err)
			}
			resp, err := testPost(t, url+"/files/create?path=/existing", "application/octet-stream", strings.NewReader(""))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("status = %d, want 409", resp.StatusCode)
			}
			var after unix.Stat_t
			if err := unix.Lstat(target, &after); err != nil || before != after {
				t.Fatalf("existing entry metadata changed: before=%+v after=%+v err=%v", before, after, err)
			}
			contentPath, want := target, "keep this content"
			switch kind {
			case "directory":
				contentPath, want = filepath.Join(target, "child"), "keep child"
			case "symlink":
				contentPath, want = filepath.Join(root, ".secret"), "keep secret"
				if link, err := os.Readlink(target); err != nil || link != ".secret" {
					t.Fatalf("symlink changed: %q %v", link, err)
				}
			}
			got, err := os.ReadFile(contentPath)
			if err != nil || string(got) != want {
				t.Fatalf("content = %q, error = %v", got, err)
			}
			assertNoCreateTemp(t, root)
		})
	}
}

func TestCreateHTTPAndWriteOverwrite(t *testing.T) {
	url, root := newServer(t)
	for _, operation := range []struct{ path, body string }{{"create", ""}, {"write", "edited"}} {
		resp, err := testPost(t, url+"/files/"+operation.path+"?path=/sub/new.txt", "application/octet-stream", strings.NewReader(operation.body))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("%s status = %d", operation.path, resp.StatusCode)
		}
		got, err := os.ReadFile(filepath.Join(root, "sub", "new.txt"))
		if err != nil || string(got) != operation.body {
			t.Fatalf("content = %q, error = %v", got, err)
		}
		info, err := os.Stat(filepath.Join(root, "sub", "new.txt"))
		if err != nil || info.Mode().Perm() != 0o644 {
			t.Fatalf("new-file permissions = %v, error = %v", info, err)
		}
		assertNoCreateTemp(t, filepath.Join(root, "sub"))
	}
}

func TestCreateCommitCollision(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			h, root, outside := raceHandler(t)
			target := filepath.Join(root, "new.txt")
			raceAt(t, h, "commit", "", "new.txt", 1, func() {
				switch kind {
				case "file":
					put(t, target, "concurrent creator")
				case "directory":
					if err := os.Mkdir(target, 0o700); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Symlink(filepath.Join(outside, "secret.txt"), target); err != nil {
						t.Fatal(err)
					}
				}
			})
			rr := serve(t, h.create, http.MethodPost, "/files/create?path=/new.txt", strings.NewReader("new body"))
			if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "already exists") {
				t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
			}
			info, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "file" {
				got, err := os.ReadFile(target)
				if err != nil || string(got) != "concurrent creator" {
					t.Fatalf("concurrent content=%q err=%v", got, err)
				}
			} else if (kind == "directory" && !info.IsDir()) || (kind == "symlink" && info.Mode()&os.ModeSymlink == 0) {
				t.Fatalf("concurrent entry replaced: %v", info)
			}
			assertNoCreateTemp(t, root)
			assertOutsideUntouched(t, outside)
		})
	}
}

func TestCreateFailureDoesNotPublish(t *testing.T) {
	for _, failure := range []string{"body", "metadata", "body limit"} {
		t.Run(failure, func(t *testing.T) {
			h, root, _ := raceHandler(t)
			body := &errAfterReader{n: 4}
			switch failure {
			case "metadata":
				h.preserveAccess = func(*os.File, fileAccess) error { return errors.New("metadata failure") }
				rr := serve(t, h.create, http.MethodPost, "/files/create?path=/new.txt", strings.NewReader("payload"))
				if rr.Code != http.StatusConflict {
					t.Fatalf("status=%d", rr.Code)
				}
			case "body limit":
				rr := serve(t, h.create, http.MethodPost, "/files/create?path=/new.txt", bytes.NewReader(bytes.Repeat([]byte("a"), maxWriteBytes+1)))
				if rr.Code != http.StatusInternalServerError {
					t.Fatalf("oversized body status=%d", rr.Code)
				}
			default:
				rr := serve(t, h.create, http.MethodPost, "/files/create?path=/new.txt", body)
				if rr.Code != http.StatusInternalServerError {
					t.Fatalf("failed body status=%d", rr.Code)
				}
			}
			if _, err := os.Lstat(filepath.Join(root, "new.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("target published: %v", err)
			}
			assertNoCreateTemp(t, root)
		})
	}
}

func TestCreateRemovesInheritedAccessACL(t *testing.T) {
	h, root, _ := raceHandler(t)
	acl := encodeACL([]aclEntry{{aclUserObj, 7, aclUndefined}, {aclUser, 4, uint32(os.Getuid() + 1)}, {aclGroupObj, 0, aclUndefined}, {aclMask, 4, aclUndefined}, {aclOther, 0, aclUndefined}})
	if err := unix.Setxattr(root, "system.posix_acl_default", acl, 0); err != nil {
		if errors.Is(err, unix.EOPNOTSUPP) {
			t.Skip("test filesystem lacks POSIX ACL support")
		}
		t.Fatal(err)
	}
	rr := serve(t, h.create, http.MethodPost, "/files/create?path=/new.txt", strings.NewReader(""))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%q", rr.Code, rr.Body.String())
	}
	if _, err := unix.Getxattr(filepath.Join(root, "new.txt"), accessACLName, nil); !errors.Is(err, unix.ENODATA) {
		t.Fatalf("inherited access ACL remains: %v", err)
	}
}

func TestCreateConfinedPaths(t *testing.T) {
	h, root, outside := raceHandler(t)
	for _, tc := range []struct {
		path   string
		status int
	}{{"/", http.StatusConflict}, {"/.hidden", http.StatusBadRequest}, {"/sub/.hidden/new", http.StatusBadRequest}} {
		rr := serve(t, h.create, http.MethodPost, "/files/create?path="+tc.path, strings.NewReader(""))
		if rr.Code != tc.status {
			t.Fatalf("path=%s status=%d body=%q", tc.path, rr.Code, rr.Body.String())
		}
	}
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	raceAt(t, h, "open", "", "sub", 1, func() { swapForSymlink(t, filepath.Join(root, "sub"), outside) })
	rr := serve(t, h.create, http.MethodPost, "/files/create?path=/sub/new.txt", strings.NewReader(""))
	assertRejected(t, rr)
	assertOutsideUntouched(t, outside)
	assertNoCreateTemp(t, root)
}
