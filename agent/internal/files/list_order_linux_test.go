//go:build linux

package files

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestListDirectoriesFirst(t *testing.T) {
	t.Parallel()
	srvURL, root := newServer(t)
	for _, dir := range []string{"", "beta"} {
		base := filepath.Join(root, dir)
		for _, name := range []string{"zeta", "beta", "aardvark"} {
			if err := os.Mkdir(filepath.Join(base, name), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, name := range []string{"gamma.txt", "alpha.txt", ".hidden"} {
			if err := os.WriteFile(filepath.Join(base, name), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		// A link to a directory stays in the non-directory group.
		if err := os.Symlink("zeta", filepath.Join(base, "aardvark-link")); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"/", "/beta", "/", "/beta"} {
		resp := get(t, srvURL, "/files/list", url.Values{"path": {path}})
		var entries []Entry
		err := json.NewDecoder(resp.Body).Decode(&entries)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK || err != nil {
			t.Fatalf("list %s: status=%d, error=%v", path, resp.StatusCode, err)
		}
		names := make([]string, len(entries))
		for i, entry := range entries {
			names[i] = entry.Name
			if entry.Dir != (i < 3) {
				t.Errorf("list %s: entry %s has dir=%v", path, entry.Name, entry.Dir)
			}
		}
		want := []string{"aardvark", "beta", "zeta", "aardvark-link", "alpha.txt", "gamma.txt"}
		if !slices.Equal(names, want) {
			t.Errorf("list %s: got %v, want %v", path, names, want)
		}
	}
}
