package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
)

// versionsProvider is a fakeProvider whose Versions returns a fixed list and
// counts its calls, so a test can see whether the handler asked at all.
type versionsProvider struct {
	fakeProvider
	versions []registry.Version
	calls    int
}

func (f *versionsProvider) Versions(_ context.Context, project string, fl registry.Filter) ([]registry.Version, error) {
	f.calls++
	f.gotProject = project
	f.gotFilter = fl
	return f.versions, nil
}

// allLoadersTemplate has a forge entry with no gameVersion (a moving entry)
// ahead of the neoforge default and a forge 1.21.1 entry, so tests can tell
// moving entries, template order and the server's current entry apart.
func allLoadersTemplate(name string) *unstructured.Unstructured {
	tmpl := modpackTestTemplate(name)
	versions, _, _ := unstructured.NestedSlice(tmpl.Object, "spec", "versions")
	versions = append([]any{map[string]any{"id": "forge-latest", "displayName": "Latest Forge", "loader": "forge"}}, versions...)
	versions = append(versions, map[string]any{"id": "1.21.1-forge", "displayName": "1.21.1 Forge", "loader": "forge", "gameVersion": "1.21.1"})
	_ = unstructured.SetNestedSlice(tmpl.Object, versions, "spec", "versions")
	return tmpl
}

func TestMatchServerVersions(t *testing.T) {
	tmpl := allLoadersTemplate("minecraft")
	forgeOnly := registry.Version{ID: "forge-121", Loaders: []string{"forge"}, GameVersions: []string{"1.21.1"}}
	both := registry.Version{ID: "both-121", Loaders: []string{"forge", "neoforge"}, GameVersions: []string{"1.21.1"}}
	cases := []struct {
		name     string
		selected string
		in       []registry.Version
		want     []string // kept IDs
		wantSrv  []string // ServerVersion per kept ID
	}{
		{
			name:    "forge pack on the template's forge entry, skipping the moving entry",
			in:      []registry.Version{forgeOnly},
			want:    []string{"forge-121"},
			wantSrv: []string{"1.21.1-forge"},
		},
		{
			name:    "loader match is case-insensitive",
			in:      []registry.Version{{ID: "nf", Loaders: []string{"NeoForge"}, GameVersions: []string{"1.21.1"}}},
			want:    []string{"nf"},
			wantSrv: []string{"1.21.1-neoforge"},
		},
		{
			name:    "version with no fitting entry is dropped",
			in:      []registry.Version{{ID: "fab", Loaders: []string{"fabric"}, GameVersions: []string{"1.21.1"}}, {ID: "old", Loaders: []string{"forge"}, GameVersions: []string{"1.7.10"}}},
			want:    nil,
			wantSrv: nil,
		},
		{
			name:    "moving entry without a gameVersion never matches",
			in:      []registry.Version{{ID: "latest", Loaders: []string{"forge"}, GameVersions: []string{"latest"}}},
			want:    nil,
			wantSrv: nil,
		},
		{
			name:     "server's current entry wins when it fits",
			selected: "1.21.1-forge",
			in:       []registry.Version{both},
			want:     []string{"both-121"},
			wantSrv:  []string{"1.21.1-forge"},
		},
		{
			name:    "server's default entry wins when it fits",
			in:      []registry.Version{both},
			want:    []string{"both-121"},
			wantSrv: []string{"1.21.1-neoforge"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matchServerVersions(tmpl, tc.selected, tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("kept %d versions, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].ID != tc.want[i] || got[i].ServerVersion != tc.wantSrv[i] {
					t.Errorf("version %d = %s on %q, want %s on %q", i, got[i].ID, got[i].ServerVersion, tc.want[i], tc.wantSrv[i])
				}
			}
		})
	}
}

func TestMatchServerVersions_NamesEntryDisplayName(t *testing.T) {
	got := matchServerVersions(allLoadersTemplate("minecraft"), "", []registry.Version{
		{ID: "nf", Loaders: []string{"neoforge"}, GameVersions: []string{"1.21.1"}},
	})
	if len(got) != 1 || got[0].ServerVersionName != "1.21.1 NeoForge" {
		t.Fatalf("got %+v, want serverVersionName 1.21.1 NeoForge", got)
	}
}

func TestRegistryVersions_AllLoadersMatchesTemplate(t *testing.T) {
	k := fakeKubeClient(
		allLoadersTemplate("minecraft"),
		serverWithVersion("gameplane-games", "alpha", "minecraft", "1.12.2-forge"),
	)
	fp := &versionsProvider{versions: []registry.Version{
		{ID: "nf", Loaders: []string{"neoforge"}, GameVersions: []string{"1.21.1"}},
		{ID: "fab", Loaders: []string{"fabric"}, GameVersions: []string{"1.21.1"}},
	}}
	r := mountRegistryRouter(k, fakeSet{p: fp})

	rr := do(t, r, "GET", "/servers/alpha/mods/registry/projects/p1/versions?provider=modrinth&type=modpack&allLoaders=true", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if fp.gotFilter.Loader != "" || fp.gotFilter.GameVersion != "" || fp.gotFilter.ProjectType != "modpack" {
		t.Errorf("provider filter = %+v, want empty facets with modpack type", fp.gotFilter)
	}
	var got []registry.Version
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "nf" || got[0].ServerVersion != "1.21.1-neoforge" || got[0].ServerVersionName != "1.21.1 NeoForge" {
		t.Errorf("body = %+v, want only nf on 1.21.1-neoforge", got)
	}
}

func TestRegistryVersions_WithoutAllLoadersKeepsServerFilter(t *testing.T) {
	k := fakeKubeClient(
		allLoadersTemplate("minecraft"),
		serverWithVersion("gameplane-games", "alpha", "minecraft", "1.12.2-forge"),
	)
	fp := &versionsProvider{versions: []registry.Version{{ID: "x"}}}
	r := mountRegistryRouter(k, fakeSet{p: fp})

	rr := do(t, r, "GET", "/servers/alpha/mods/registry/projects/p1/versions?provider=modrinth&type=modpack", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if fp.gotFilter.Loader != "forge" || fp.gotFilter.GameVersion != "1.12.2" {
		t.Errorf("provider filter = %+v, want the server's forge 1.12.2", fp.gotFilter)
	}
}

func TestRegistryVersions_AllLoadersIgnoredForMods(t *testing.T) {
	k := fakeKubeClient(
		allLoadersTemplate("minecraft"),
		serverWithVersion("gameplane-games", "alpha", "minecraft", "1.12.2-forge"),
	)
	fp := &versionsProvider{versions: []registry.Version{{ID: "x"}}}
	r := mountRegistryRouter(k, fakeSet{p: fp})

	rr := do(t, r, "GET", "/servers/alpha/mods/registry/projects/p1/versions?provider=modrinth&type=mod&allLoaders=true", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if fp.gotFilter.Loader != "forge" || fp.gotFilter.GameVersion != "1.12.2" {
		t.Errorf("provider filter = %+v, want the server's facets for type=mod", fp.gotFilter)
	}
	if strings.Contains(rr.Body.String(), "serverVersion") {
		t.Errorf("type=mod must not add serverVersion: %s", rr.Body)
	}
}

func TestRegistrySearch_AllLoadersDropsFacetsForModpacks(t *testing.T) {
	k := fakeKubeClient(
		allLoadersTemplate("minecraft"),
		serverWithVersion("gameplane-games", "alpha", "minecraft", "1.12.2-forge"),
	)
	fp := &fakeProvider{}
	r := mountRegistryRouter(k, fakeSet{p: fp})

	if rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?provider=modrinth&type=modpack&allLoaders=true", nil); rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if fp.gotSearch.Loader != "" || fp.gotSearch.GameVersion != "" || fp.gotSearch.ProjectType != "modpack" {
		t.Errorf("search query = %+v, want no facets for all-loaders modpacks", fp.gotSearch)
	}

	if rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?provider=modrinth&type=mod&allLoaders=true", nil); rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if fp.gotSearch.Loader != "forge" || fp.gotSearch.GameVersion != "1.12.2" {
		t.Errorf("type=mod search query = %+v, want the server's facets", fp.gotSearch)
	}
}

// installSwitchFixture is a neoforge 1.21.1 server, and a provider that lists
// the given pack versions for any pack.
func installSwitchFixture(versions []registry.Version) (*kube.Client, *versionsProvider) {
	k := fakeKubeClient(
		allLoadersTemplate("minecraft"),
		serverWithVersion("gameplane-games", "alpha", "minecraft", "1.21.1-neoforge"),
	)
	return k, &versionsProvider{versions: versions}
}

func serverSpecVersion(t *testing.T, k *kube.Client) (version string, hasModpack bool) {
	t.Helper()
	gs, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace("gameplane-games").
		Get(t.Context(), "alpha", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	version, _, _ = unstructured.NestedString(gs.Object, "spec", "version")
	_, hasModpack, _ = unstructured.NestedMap(gs.Object, "spec", "modpack")
	return version, hasModpack
}

func TestInstallModpack_ServerVersionSwitchWritesVersionAndPack(t *testing.T) {
	k, fp := installSwitchFixture([]registry.Version{{ID: "pack-forge", Loaders: []string{"forge"}, GameVersions: []string{"1.12.2"}}})
	mount := mountRegistryRouter(k, fakeSet{p: fp})

	rr := modpackReq(t, mount, http.MethodPost, "/servers/alpha/modpack", map[string]any{
		"provider": "modrinth", "project": "pack", "version": "pack-forge",
		"serverVersion": "1.12.2-forge",
	})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if fp.gotFilter.Loader != "forge" || fp.gotFilter.GameVersion != "1.12.2" || fp.gotFilter.ProjectType != "modpack" {
		t.Errorf("provider checked with %+v, want forge/1.12.2 modpack", fp.gotFilter)
	}
	version, hasModpack := serverSpecVersion(t, k)
	if version != "1.12.2-forge" || !hasModpack {
		t.Errorf("spec.version = %q modpack set %v, want 1.12.2-forge with the pack", version, hasModpack)
	}
}

func TestInstallModpack_ServerVersionSameAsCurrentSkipsCheck(t *testing.T) {
	k, fp := installSwitchFixture(nil)
	mount := mountRegistryRouter(k, fakeSet{p: fp})

	rr := modpackReq(t, mount, http.MethodPost, "/servers/alpha/modpack", map[string]any{
		"provider": "modrinth", "project": "pack", "version": "pack-neo",
		"serverVersion": "1.21.1-neoforge",
	})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if fp.calls != 0 {
		t.Errorf("provider called %d times, want none when serverVersion is the current version", fp.calls)
	}
	if version, _ := serverSpecVersion(t, k); version != "1.21.1-neoforge" {
		t.Errorf("spec.version = %q, want unchanged", version)
	}
}

func TestInstallModpack_ServerVersionRejects(t *testing.T) {
	forgePack := []registry.Version{{ID: "pack-forge", Loaders: []string{"forge"}, GameVersions: []string{"1.12.2"}}}
	cases := []struct {
		name string
		body map[string]any
		// versions overrides the provider's list when set.
		versions []registry.Version
	}{
		{
			name:     "pack version missing for that server version",
			body:     map[string]any{"provider": "modrinth", "project": "pack", "version": "other", "serverVersion": "1.12.2-forge"},
			versions: forgePack,
		},
		{
			name:     "server version is not a template entry",
			body:     map[string]any{"provider": "modrinth", "project": "pack", "version": "pack-forge", "serverVersion": "9.9-nothing"},
			versions: forgePack,
		},
		{
			name:     "moving entry without a gameVersion",
			body:     map[string]any{"provider": "modrinth", "project": "pack", "version": "pack-forge", "serverVersion": "forge-latest"},
			versions: forgePack,
		},
		{
			name: "switch without a pack version",
			// The legacy ref body carries no version, so the switch has no pack version to check.
			body:     map[string]any{"provider": "modrinth", "ref": "pack", "serverVersion": "1.12.2-forge"},
			versions: forgePack,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, fp := installSwitchFixture(tc.versions)
			mount := mountRegistryRouter(k, fakeSet{p: fp})
			rr := modpackReq(t, mount, http.MethodPost, "/servers/alpha/modpack", tc.body)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("got %d %s, want 400", rr.Code, rr.Body)
			}
			if !strings.Contains(rr.Body.String(), "server.modpack.server_version_mismatch") {
				t.Errorf("body %s does not carry server.modpack.server_version_mismatch", rr.Body)
			}
			if version, hasModpack := serverSpecVersion(t, k); version != "1.21.1-neoforge" || hasModpack {
				t.Errorf("rejected install changed the server: version %q, modpack set %v", version, hasModpack)
			}
		})
	}
}

// A switch to an image that needs the CurseForge key is blocked by that
// entry's image, even though the current entry's image would not be.
func TestInstallModpack_ServerVersionSwitchChecksTargetImageKey(t *testing.T) {
	k := fakeKubeClient(
		allLoadersTemplate("minecraft"),
		serverWithVersion("gameplane-games", "alpha", "minecraft", "1.21.1-neoforge"),
	)
	fp := &versionsProvider{versions: []registry.Version{{ID: "pack-forge", Loaders: []string{"forge"}, GameVersions: []string{"1.12.2"}}}}
	mount := mountRegistryRouter(k, fakeSet{p: fp})

	rr := modpackReq(t, mount, http.MethodPost, "/servers/alpha/modpack", map[string]any{
		"provider": "curseforge", "project": "pack", "version": "pack-forge", "serverVersion": "1.12.2-forge",
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d %s, want 409 key_required from the 1.12.2 image", rr.Code, rr.Body)
	}
	if version, hasModpack := serverSpecVersion(t, k); version != "1.21.1-neoforge" || hasModpack {
		t.Errorf("blocked install changed the server: version %q, modpack set %v", version, hasModpack)
	}
}

// The provider can return the requested pack version with loader or game
// version tags that do not fit the target entry; the install must still refuse.
func TestInstallModpack_ServerVersionRejectsUnfittingVersion(t *testing.T) {
	cases := []struct {
		name    string
		version registry.Version
	}{
		{
			name:    "loader tag the entry does not run",
			version: registry.Version{ID: "pack-forge", Loaders: []string{"paper"}, GameVersions: []string{"1.12.2"}},
		},
		{
			name:    "no loader tag",
			version: registry.Version{ID: "pack-forge", GameVersions: []string{"1.12.2"}},
		},
		{
			name:    "game version the entry does not run",
			version: registry.Version{ID: "pack-forge", Loaders: []string{"forge"}, GameVersions: []string{"1.7.10"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, fp := installSwitchFixture([]registry.Version{tc.version})
			mount := mountRegistryRouter(k, fakeSet{p: fp})
			rr := modpackReq(t, mount, http.MethodPost, "/servers/alpha/modpack", map[string]any{
				"provider": "curseforge", "project": "pack", "version": "pack-forge",
				"serverVersion": "1.12.2-forge",
			})
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("got %d %s, want 400", rr.Code, rr.Body)
			}
			if !strings.Contains(rr.Body.String(), "server.modpack.server_version_mismatch") {
				t.Errorf("body %s does not carry server.modpack.server_version_mismatch", rr.Body)
			}
			if version, hasModpack := serverSpecVersion(t, k); version != "1.21.1-neoforge" || hasModpack {
				t.Errorf("rejected install changed the server: version %q, modpack set %v", version, hasModpack)
			}
		})
	}
}

// With key sharing off, a CurseForge template entry whose image needs the key
// is never named by the all-loaders browse, so a pack version only that entry
// fits is dropped, and a version that also fits an unblocked entry names that
// entry even when the server currently runs the blocked one.
func TestRegistryVersions_AllLoadersSkipsKeyBlockedEntries(t *testing.T) {
	forgeOnly := registry.Version{ID: "forge-only", Loaders: []string{"forge"}, GameVersions: []string{"1.12.2"}}
	both := registry.Version{ID: "both", Loaders: []string{"forge", "neoforge"}, GameVersions: []string{"1.12.2", "1.21.1"}}
	cases := []struct {
		name     string
		selected string
		want     []string
		wantSrv  []string
	}{
		{
			name:     "blocked forge entry is not offered for a forge-only version",
			selected: "1.21.1-neoforge",
			want:     []string{"both"},
			wantSrv:  []string{"1.21.1-neoforge"},
		},
		{
			name:     "blocked current entry is not preferred",
			selected: "1.12.2-forge",
			want:     []string{"both"},
			wantSrv:  []string{"1.21.1-neoforge"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := fakeKubeClient(
				allLoadersTemplate("minecraft"),
				serverWithVersion("gameplane-games", "alpha", "minecraft", tc.selected),
			)
			fp := &versionsProvider{versions: []registry.Version{forgeOnly, both}}
			r := mountRegistryRouter(k, fakeSet{p: fp})

			rr := do(t, r, "GET", "/servers/alpha/mods/registry/projects/p1/versions?provider=curseforge&type=modpack&allLoaders=true", nil)
			if rr.Code != http.StatusOK {
				t.Fatalf("got %d %s", rr.Code, rr.Body)
			}
			var got []registry.Version
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("kept %+v, want IDs %v", got, tc.want)
			}
			for i := range got {
				if got[i].ID != tc.want[i] || got[i].ServerVersion != tc.wantSrv[i] {
					t.Errorf("version %d = %s on %q, want %s on %q", i, got[i].ID, got[i].ServerVersion, tc.want[i], tc.wantSrv[i])
				}
			}
			if strings.Contains(rr.Body.String(), "1.12.2-forge") {
				t.Errorf("blocked entry named in body: %s", rr.Body)
			}
		})
	}
}
