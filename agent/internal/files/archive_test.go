package files

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
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
