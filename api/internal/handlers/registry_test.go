package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
)

// fakeProvider records the query it was called with and returns canned data.
type fakeProvider struct {
	gotSearch  registry.SearchQuery
	gotFilter  registry.Filter
	gotProject string
	searchErr  error
}

func (f *fakeProvider) Search(_ context.Context, q registry.SearchQuery) ([]registry.Project, error) {
	f.gotSearch = q
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return []registry.Project{{ID: "p1", Title: "Mod One", Provider: "modrinth"}}, nil
}

func (f *fakeProvider) Versions(_ context.Context, project string, fl registry.Filter) ([]registry.Version, error) {
	f.gotProject = project
	f.gotFilter = fl
	return []registry.Version{{ID: "v1", Files: []registry.File{{Filename: "a.jar", DownloadURL: "https://cdn/a.jar", Primary: true}}}}, nil
}

func (f *fakeProvider) ModpackDeps(_ context.Context, project string) ([]registry.File, error) {
	f.gotProject = project
	return []registry.File{{Filename: "dep.zip", DownloadURL: "https://cdn/dep.zip", Primary: true}}, nil
}

// fakeSet returns p for any config, unless absent is true. When cfgOut is
// set, For records the Config it was called with, so a test can assert on
// how the handler translated a template's declared provider block.
type fakeSet struct {
	p      registry.Provider
	absent bool
	cfgOut *registry.Config
}

func (s fakeSet) For(_ context.Context, cfg registry.Config) (registry.Provider, bool) {
	if s.cfgOut != nil {
		*s.cfgOut = cfg
	}
	if s.absent {
		return nil, false
	}
	return s.p, true
}

func (s fakeSet) Available(_ context.Context, _ string) bool { return !s.absent }

func newTemplateObj(name string, registryBlock map[string]any, versions []any) *unstructured.Unstructured {
	mods := map[string]any{
		"loaders": map[string]any{"paper": map[string]any{"path": "plugins"}},
	}
	// registryBlock is a single provider entry; wrap it in the providers[]
	// list the CRD now uses.
	if registryBlock != nil {
		mods["registry"] = map[string]any{"providers": []any{registryBlock}}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"game":         "minecraft-java",
			"versions":     versions,
			"capabilities": map[string]any{"mods": mods},
		},
	}}
}

func serverWithVersion(ns, name, tmpl, version string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": ns},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmpl},
			"version":     version,
		},
	}}
}

func mountRegistryRouter(k *kube.Client, set registrySet) http.Handler {
	r := chi.NewRouter()
	MountRegistry(r, k, set)
	return r
}

// TestRegistrySearch_NegativeOffsetClamped covers the offset-clamp nit: a
// negative ?offset= must never reach a Provider.Search unchanged — several
// engines (nexus.go, thunderstore.go, factorio.go) slice their client-side
// pagination as matched[q.Offset:] after only a q.Offset >= len(matched)
// bounds check, which a negative offset slips past and panics.
func TestRegistrySearch_NegativeOffsetClamped(t *testing.T) {
	versions := []any{map[string]any{"id": "v", "loader": "paper", "gameVersion": "1.21.4", "default": true}}
	k := fakeKubeClient(
		newTemplateObj("minecraft", map[string]any{"provider": "modrinth"}, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", ""),
	)
	fp := &fakeProvider{}
	r := mountRegistryRouter(k, fakeSet{p: fp})

	rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?q=x&offset=-5", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if fp.gotSearch.Offset != 0 {
		t.Errorf("Offset = %d, want clamped to 0", fp.gotSearch.Offset)
	}
}

func TestRegistrySearch_ResolvesLoaderAndGameVersion(t *testing.T) {
	versions := []any{
		map[string]any{"id": "1.21.4-paper", "loader": "paper", "gameVersion": "1.21.4", "default": true},
		map[string]any{"id": "1.20.1-fabric", "loader": "fabric", "gameVersion": "1.20.1"},
	}
	k := fakeKubeClient(
		newTemplateObj("minecraft", map[string]any{"provider": "modrinth"}, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", "1.20.1-fabric"),
	)
	fp := &fakeProvider{}
	r := mountRegistryRouter(k, fakeSet{p: fp})

	rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?q=sodium&limit=5", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	// The selected version (1.20.1-fabric) drives the facets, not the default.
	if fp.gotSearch.Loader != "fabric" || fp.gotSearch.GameVersion != "1.20.1" {
		t.Errorf("facets = loader %q gv %q, want fabric/1.20.1", fp.gotSearch.Loader, fp.gotSearch.GameVersion)
	}
	if fp.gotSearch.Term != "sodium" || fp.gotSearch.Limit != 5 {
		t.Errorf("query = %+v", fp.gotSearch)
	}
	var got []registry.Project
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Mod One" {
		t.Errorf("body = %+v", got)
	}
}

func TestRegistrySearch_ThreadsSteamAppID(t *testing.T) {
	versions := []any{map[string]any{"id": "v", "loader": "", "gameVersion": "", "default": true}}
	for _, tc := range []struct {
		name    string
		appID   any // matches whatever type an unstructured numeric field may decode as
		wantVal int32
	}{
		// Not int: apimachinery's DeepCopyJSONValue (used by the fake
		// dynamic client's object tracker) only supports the JSON-decode
		// shapes a real unstructured object can actually contain — bool,
		// int64, float64, string, json.Number, nil — and panics on a bare
		// int, which a real unstructured decode never produces anyway.
		// nestedInt32's `case int:` branch is covered directly by
		// TestNestedInt32 below instead.
		{"int64", int64(4000), 4000},
		{"float64", float64(4000), 4000},
		{"json.Number", json.Number("4000"), 4000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := fakeKubeClient(
				newTemplateObj("gmod", map[string]any{"provider": "steam", "steamAppID": tc.appID}, versions),
				serverWithVersion("gameplane-games", "alpha", "gmod", ""),
			)
			var gotCfg registry.Config
			r := mountRegistryRouter(k, fakeSet{p: &fakeProvider{}, cfgOut: &gotCfg})

			rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?q=wire", nil)
			if rr.Code != 200 {
				t.Fatalf("got %d %s", rr.Code, rr.Body)
			}
			if gotCfg.Provider != "steam" || gotCfg.SteamAppID != tc.wantVal {
				t.Errorf("cfg = %+v, want provider=steam steamAppID=%d", gotCfg, tc.wantVal)
			}
		})
	}
}

// TestRegistrySearch_ThreadsCurseForgeGameID is the handler-level regression
// guard for the "ARK's mod browser shows Minecraft mods" bug:
// registryProviders() must read a template's declared curseforgeGameID and
// thread it through to registry.Config, never a hardcoded Minecraft id —
// mirrors TestRegistrySearch_ThreadsSteamAppID above.
func TestRegistrySearch_ThreadsCurseForgeGameID(t *testing.T) {
	versions := []any{map[string]any{"id": "v", "loader": "", "gameVersion": "", "default": true}}
	for _, tc := range []struct {
		name    string
		gameID  any // matches whatever type an unstructured numeric field may decode as
		wantVal int32
	}{
		{"int64", int64(83374), 83374},
		{"float64", float64(83374), 83374},
		{"json.Number", json.Number("83374"), 83374},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := fakeKubeClient(
				newTemplateObj("ark", map[string]any{"provider": "curseforge", "curseforgeGameID": tc.gameID}, versions),
				serverWithVersion("gameplane-games", "alpha", "ark", ""),
			)
			var gotCfg registry.Config
			r := mountRegistryRouter(k, fakeSet{p: &fakeProvider{}, cfgOut: &gotCfg})

			rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?q=ark-mod", nil)
			if rr.Code != 200 {
				t.Fatalf("got %d %s", rr.Code, rr.Body)
			}
			if gotCfg.Provider != "curseforge" || gotCfg.CurseForgeGameID != tc.wantVal {
				t.Errorf("cfg = %+v, want provider=curseforge curseforgeGameID=%d (not the hardcoded Minecraft 432)", gotCfg, tc.wantVal)
			}
		})
	}
}

// TestNestedInt32 covers every numeric shape nestedInt32 accepts, including
// the bare int case (a defensive branch for a hand-built test fixture; a
// real unstructured decode never produces it) that TestRegistrySearch_
// ThreadsSteamAppID above can't exercise since it goes through the fake
// dynamic client's object tracker, which panics on a bare int.
func TestNestedInt32(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want int32
	}{
		{"int", int(4000), 4000},
		{"int32", int32(4000), 4000},
		{"int64", int64(4000), 4000},
		{"float64", float64(4000), 4000},
		{"json.Number", json.Number("4000"), 4000},
		{"json.Number invalid", json.Number("not-a-number"), 0},
		{"unsupported type", "4000", 0},
		{"nil", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nestedInt32(tc.in); got != tc.want {
				t.Errorf("nestedInt32(%v) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestRegistrySearch_DefaultVersionWhenUnset(t *testing.T) {
	versions := []any{
		map[string]any{"id": "1.21.4-paper", "loader": "paper", "gameVersion": "1.21.4", "default": true},
	}
	k := fakeKubeClient(
		newTemplateObj("minecraft", map[string]any{"provider": "modrinth"}, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", ""),
	)
	fp := &fakeProvider{}
	r := mountRegistryRouter(k, fakeSet{p: fp})
	rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?q=x", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if fp.gotSearch.Loader != "paper" || fp.gotSearch.GameVersion != "1.21.4" {
		t.Errorf("default facets = %q/%q", fp.gotSearch.Loader, fp.gotSearch.GameVersion)
	}
}

func TestRegistryVersions(t *testing.T) {
	versions := []any{map[string]any{"id": "1.21.4-paper", "loader": "paper", "gameVersion": "1.21.4", "default": true}}
	k := fakeKubeClient(
		newTemplateObj("minecraft", map[string]any{"provider": "modrinth"}, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", ""),
	)
	fp := &fakeProvider{}
	r := mountRegistryRouter(k, fakeSet{p: fp})
	rr := do(t, r, "GET", "/servers/alpha/mods/registry/projects/sodium/versions", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if fp.gotProject != "sodium" {
		t.Errorf("project = %q", fp.gotProject)
	}
	if fp.gotFilter.Loader != "paper" || fp.gotFilter.GameVersion != "1.21.4" {
		t.Errorf("filter = %+v", fp.gotFilter)
	}
}

func TestRegistry_NoRegistryBlock_501(t *testing.T) {
	versions := []any{map[string]any{"id": "v", "loader": "tmodloader", "default": true}}
	k := fakeKubeClient(
		newTemplateObj("terraria", nil, versions), // no registry block
		serverWithVersion("gameplane-games", "alpha", "terraria", ""),
	)
	r := mountRegistryRouter(k, fakeSet{p: &fakeProvider{}})
	rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?q=x", nil)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("got %d, want 501", rr.Code)
	}
}

func TestRegistry_UnselectableProvider_501(t *testing.T) {
	versions := []any{map[string]any{"id": "v", "loader": "bepinex", "default": true}}
	k := fakeKubeClient(
		newTemplateObj("valheim", map[string]any{"provider": "thunderstore"}, versions),
		serverWithVersion("gameplane-games", "alpha", "valheim", ""),
	)
	// Set.For reports the provider unselectable (e.g. missing community).
	r := mountRegistryRouter(k, fakeSet{absent: true})
	rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?q=x", nil)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("got %d, want 501", rr.Code)
	}
}

func TestRegistry_UnknownServer_404(t *testing.T) {
	k := fakeKubeClient()
	r := mountRegistryRouter(k, fakeSet{p: &fakeProvider{}})
	rr := do(t, r, "GET", "/servers/ghost/mods/registry/search?q=x", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", rr.Code)
	}
}

func TestInstallModpack_SetsEnv(t *testing.T) {
	versions := []any{map[string]any{"id": "1.21.4-fabric", "loader": "fabric", "gameVersion": "1.21.4", "default": true}}
	reg := map[string]any{
		"provider": "modrinth",
		"modpacks": map[string]any{
			"refEnv": "MODRINTH_MODPACK",
			"env":    []any{map[string]any{"name": "TYPE", "value": "MODRINTH"}},
		},
	}
	k := fakeKubeClient(
		newTemplateObj("minecraft", reg, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", "1.21.4-fabric"),
	)
	r := mountRegistryRouter(k, fakeSet{p: &fakeProvider{}})

	rr := do(t, r, "POST", "/servers/alpha/modpack", map[string]any{"ref": "cobblemon"})
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	gs, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace("gameplane-games").
		Get(t.Context(), "alpha", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	env, _, _ := unstructured.NestedSlice(gs.Object, "spec", "env")
	got := map[string]string{}
	for _, e := range env {
		m := e.(map[string]any)
		got[m["name"].(string)], _ = m["value"].(string)
	}
	if got["MODRINTH_MODPACK"] != "cobblemon" || got["TYPE"] != "MODRINTH" {
		t.Errorf("env = %v, want MODRINTH_MODPACK=cobblemon TYPE=MODRINTH", got)
	}
}

func TestInstallModpack_DepsModeConflict(t *testing.T) {
	versions := []any{map[string]any{"id": "stable", "loader": "bepinex", "default": true}}
	reg := map[string]any{"provider": "thunderstore", "community": "valheim", "modpacks": map[string]any{}}
	k := fakeKubeClient(
		newTemplateObj("valheim", reg, versions),
		serverWithVersion("gameplane-games", "alpha", "valheim", "stable"),
	)
	r := mountRegistryRouter(k, fakeSet{p: &fakeProvider{}})
	rr := do(t, r, "POST", "/servers/alpha/modpack", map[string]any{"ref": "x"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409", rr.Code)
	}
}

func TestModpackDeps(t *testing.T) {
	versions := []any{map[string]any{"id": "stable", "loader": "bepinex", "default": true}}
	reg := map[string]any{"provider": "thunderstore", "community": "valheim", "modpacks": map[string]any{}}
	k := fakeKubeClient(
		newTemplateObj("valheim", reg, versions),
		serverWithVersion("gameplane-games", "alpha", "valheim", "stable"),
	)
	r := mountRegistryRouter(k, fakeSet{p: &fakeProvider{}})
	rr := do(t, r, "GET", "/servers/alpha/mods/registry/projects/some-pack/modpack", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	var files []registry.File
	if err := json.Unmarshal(rr.Body.Bytes(), &files); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(files) != 1 || files[0].Filename != "dep.zip" {
		t.Errorf("files = %+v", files)
	}
}

func TestRegistryProviders(t *testing.T) {
	versions := []any{map[string]any{"id": "1.21.4-fabric", "loader": "fabric", "gameVersion": "1.21.4", "default": true}}
	tmpl := newTemplateObj("minecraft", nil, versions)
	_ = unstructured.SetNestedField(tmpl.Object, map[string]any{
		"providers": []any{
			map[string]any{"provider": "modrinth", "modpacks": map[string]any{"refEnv": "MODRINTH_MODPACK"}},
			map[string]any{"provider": "curseforge"},
		},
	}, "spec", "capabilities", "mods", "registry")
	k := fakeKubeClient(tmpl, serverWithVersion("gameplane-games", "alpha", "minecraft", ""))
	r := mountRegistryRouter(k, fakeSet{p: &fakeProvider{}})

	rr := do(t, r, "GET", "/servers/alpha/mods/registry/providers", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	var got []providerInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("providers = %+v", got)
	}
	if got[0].Provider != "modrinth" || !got[0].Modpacks || !got[0].Available {
		t.Errorf("p0 = %+v", got[0])
	}
	if got[1].Provider != "curseforge" || got[1].Modpacks {
		t.Errorf("p1 = %+v", got[1])
	}
}

func TestRegistrySearch_UnknownProvider_501(t *testing.T) {
	versions := []any{map[string]any{"id": "1.21.4-fabric", "loader": "fabric", "gameVersion": "1.21.4", "default": true}}
	k := fakeKubeClient(
		newTemplateObj("minecraft", map[string]any{"provider": "modrinth"}, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", ""),
	)
	r := mountRegistryRouter(k, fakeSet{p: &fakeProvider{}})
	// curseforge isn't declared on this template → 501.
	rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?q=x&provider=curseforge", nil)
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("got %d, want 501", rr.Code)
	}
}

func TestRegistry_UpstreamError_502(t *testing.T) {
	versions := []any{map[string]any{"id": "v", "loader": "paper", "gameVersion": "1.21.4", "default": true}}
	k := fakeKubeClient(
		newTemplateObj("minecraft", map[string]any{"provider": "modrinth"}, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", ""),
	)
	fp := &fakeProvider{searchErr: errors.New("boom")}
	r := mountRegistryRouter(k, fakeSet{p: fp})
	rr := do(t, r, "GET", "/servers/alpha/mods/registry/search?q=x", nil)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", rr.Code)
	}
}
