package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func versionIDs(vs []Version) []string {
	ids := make([]string, 0, len(vs))
	for _, v := range vs {
		ids = append(ids, v.ID)
	}
	return ids
}

// TestCurseforgeModpackVersionsFiltered serves the hand-made class 4471
// fixture (files mixing Minecraft versions and loaders, one with a null
// downloadUrl) and checks the engine asks CurseForge for the right facets
// and keeps only files matching the server's game version and loader.
func TestCurseforgeModpackVersionsFiltered(t *testing.T) {
	body, err := os.ReadFile("testdata/curseforge/modpack-files.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var gotPath, gotGV, gotLoader, gotPageSize, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotGV = r.URL.Query().Get("gameVersion")
		gotLoader = r.URL.Query().Get("modLoaderType")
		gotPageSize = r.URL.Query().Get("pageSize")
		gotKey = r.Header.Get("x-api-key")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	cases := []struct {
		name       string
		filter     Filter
		wantIDs    []string
		wantGV     string
		wantLoader string
	}{
		{"neoforge 1.21.1", Filter{Loader: "neoforge", GameVersion: "1.21.1"}, []string{"6512345", "6498001"}, "1.21.1", "6"},
		{"forge 1.20.1", Filter{Loader: "forge", GameVersion: "1.20.1"}, []string{"5900123"}, "1.20.1", "1"},
		{"fabric 1.20.1", Filter{Loader: "fabric", GameVersion: "1.20.1"}, []string{"5712004"}, "1.20.1", "4"},
		{"forge 1.16.5", Filter{Loader: "forge", GameVersion: "1.16.5"}, []string{"4321987"}, "1.16.5", "1"},
		{"forge 1.21.1 has none", Filter{Loader: "forge", GameVersion: "1.21.1"}, []string{}, "1.21.1", "1"},
		{"no filter keeps every downloadable file", Filter{}, []string{"6512345", "6498001", "5900123", "5712004", "4321987"}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.filter.ProjectType = "modpack"
			got, err := testCurseforgeGame(srv.URL, cfGameMinecraft).Versions(context.Background(), "925200", tc.filter)
			if err != nil {
				t.Fatalf("Versions: %v", err)
			}
			if gotPath != "/mods/925200/files" {
				t.Errorf("path = %q, want /mods/925200/files", gotPath)
			}
			if gotKey != "secret-key" {
				t.Errorf("x-api-key = %q", gotKey)
			}
			if gotGV != tc.wantGV || gotLoader != tc.wantLoader || gotPageSize != "50" {
				t.Errorf("query gameVersion=%q modLoaderType=%q pageSize=%q, want %q %q 50", gotGV, gotLoader, gotPageSize, tc.wantGV, tc.wantLoader)
			}
			if ids := versionIDs(got); !reflect.DeepEqual(ids, tc.wantIDs) {
				t.Errorf("ids = %v, want %v", ids, tc.wantIDs)
			}
		})
	}
}

// TestCurseforgeModpackVersionFields checks the mapped fields of the newest
// matching file, and that the null-downloadUrl file (6480777) is skipped.
func TestCurseforgeModpackVersionFields(t *testing.T) {
	body, err := os.ReadFile("testdata/curseforge/modpack-files.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, err := testCurseforgeGame(srv.URL, cfGameMinecraft).Versions(context.Background(), "925200",
		Filter{Loader: "neoforge", GameVersion: "1.21.1", ProjectType: "modpack"})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (beta file without downloadUrl is skipped)", len(got))
	}
	v := got[0]
	if v.ID != "6512345" || v.Name != "All the Mods 10-2.41" || v.VersionNumber != "All the Mods 10-2.41" {
		t.Errorf("version = %+v", v)
	}
	if len(v.Files) != 1 || v.Files[0].Filename != "All the Mods 10-2.41.zip" ||
		v.Files[0].Size != 184549376 || !v.Files[0].Primary {
		t.Errorf("files = %+v", v.Files)
	}
}

// TestCurseforgeModVersionsNotFilteredClientSide proves the client-side
// filter is modpack-only: mod lookups keep relying on CurseForge's own
// facets, so a mock that ignores them returns every downloadable file.
func TestCurseforgeModVersionsNotFilteredClientSide(t *testing.T) {
	body, err := os.ReadFile("testdata/curseforge/modpack-files.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, err := testCurseforgeGame(srv.URL, cfGameMinecraft).Versions(context.Background(), "925200",
		Filter{Loader: "neoforge", GameVersion: "1.21.1"})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("len = %d, want 5 (no client-side filtering for mods)", len(got))
	}
}
