package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
)

// fakeModLister serves a canned agent /mods payload (or an error).
type fakeModLister struct {
	mods []installedMod
	err  error
}

func (f *fakeModLister) GetJSON(_ context.Context, _, _, path string, out any) error {
	if f.err != nil {
		return f.err
	}
	if path != "/mods" {
		return errors.New("unexpected path " + path)
	}
	b, _ := json.Marshal(f.mods)
	return json.Unmarshal(b, out)
}

// fakeVersionsProvider returns canned versions per project and counts calls.
type fakeVersionsProvider struct {
	mu        sync.Mutex
	byProject map[string][]registry.Version
	errFor    map[string]bool
	calls     atomic.Int64
	gotFilter registry.Filter
}

func (f *fakeVersionsProvider) Search(context.Context, registry.SearchQuery) ([]registry.Project, error) {
	return nil, nil
}

func (f *fakeVersionsProvider) Versions(_ context.Context, project string, fl registry.Filter) ([]registry.Version, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.gotFilter = fl
	f.mu.Unlock()
	if f.errFor[project] {
		return nil, errors.New("upstream 503")
	}
	return f.byProject[project], nil
}

func (f *fakeVersionsProvider) getGotFilter() registry.Filter {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gotFilter
}

func (f *fakeVersionsProvider) ModpackDeps(context.Context, string) ([]registry.File, error) {
	return nil, nil
}

func mountUpdatesRouter(k *kube.Client, set registrySet, lister AgentModLister) http.Handler {
	r := chi.NewRouter()
	MountModUpdates(r, k, set, lister)
	return r
}

func managedMod(name, provider, project, versionID string) installedMod {
	return installedMod{Name: name, Meta: &installedRef{
		Provider:  provider,
		ProjectID: project,
		VersionID: versionID,
		Loader:    "paper",
	}}
}

func updatesFixtureKube() *kube.Client {
	versions := []any{
		map[string]any{"id": "1.21.4-paper", "loader": "paper", "gameVersion": "1.21.4", "default": true},
	}
	return fakeKubeClient(
		newTemplateObj("minecraft", map[string]any{"provider": "modrinth"}, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", ""),
	)
}

func decodeUpdates(t *testing.T, body []byte) modUpdatesResponse {
	t.Helper()
	var resp modUpdatesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp
}

func TestModUpdates_ReportsOutdatedOnly(t *testing.T) {
	fp := &fakeVersionsProvider{byProject: map[string][]registry.Version{
		"sodium": {{ID: "v-new", VersionNumber: "0.6.13", Files: []registry.File{
			{Filename: "sodium-0.6.13.jar", DownloadURL: "https://cdn/sodium-0.6.13.jar", Primary: true},
		}}},
		"lithium": {{ID: "v-same", VersionNumber: "1.0.0", Files: []registry.File{
			{Filename: "lithium.jar", DownloadURL: "https://cdn/lithium.jar"},
		}}},
	}}
	lister := &fakeModLister{mods: []installedMod{
		managedMod("sodium-0.6.9.jar", "modrinth", "sodium", "v-old"),
		managedMod("lithium.jar", "modrinth", "lithium", "v-same"),
	}}
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: fp}, lister)

	rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	resp := decodeUpdates(t, rr.Body.Bytes())
	if len(resp.Updates) != 1 || len(resp.Errors) != 0 {
		t.Fatalf("resp = %+v", resp)
	}
	up := resp.Updates[0]
	if up.Name != "sodium-0.6.9.jar" || up.LatestVersionID != "v-new" ||
		up.File.DownloadURL != "https://cdn/sodium-0.6.13.jar" {
		t.Errorf("update = %+v", up)
	}
	if resp.CheckedAt == "" {
		t.Error("checkedAt missing")
	}
	// The manifest's loader (paper) drives the filter.
	if fp.getGotFilter().Loader != "paper" {
		t.Errorf("filter = %+v", fp.getGotFilter())
	}
}

func TestModUpdates_SkipsUnmanagedAndUploads(t *testing.T) {
	fp := &fakeVersionsProvider{}
	lister := &fakeModLister{mods: []installedMod{
		{Name: "handmade.jar", Meta: nil},
		{Name: "custom.jar", Meta: &installedRef{Provider: "upload"}},
	}}
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: fp}, lister)

	rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	resp := decodeUpdates(t, rr.Body.Bytes())
	if len(resp.Updates) != 0 || len(resp.Errors) != 0 {
		t.Fatalf("resp = %+v, want empty", resp)
	}
	if fp.calls.Load() != 0 {
		t.Errorf("upstream calls = %d, want 0", fp.calls.Load())
	}
}

func TestModUpdates_UndeclaredProviderErrors(t *testing.T) {
	fp := &fakeVersionsProvider{}
	lister := &fakeModLister{mods: []installedMod{
		managedMod("x.jar", "thunderstore", "Owner-Pkg", "1.0.0"),
	}}
	// Template declares only modrinth.
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: fp}, lister)

	rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil)
	resp := decodeUpdates(t, rr.Body.Bytes())
	if len(resp.Errors) != 1 || resp.Errors[0].Name != "x.jar" {
		t.Fatalf("resp = %+v, want one error for x.jar", resp)
	}
}

func TestModUpdates_RegistryFailureIsPerMod(t *testing.T) {
	fp := &fakeVersionsProvider{
		byProject: map[string][]registry.Version{
			"good": {{ID: "v2", Files: []registry.File{{Filename: "good.jar", DownloadURL: "https://cdn/good.jar"}}}},
		},
		errFor: map[string]bool{"broken": true},
	}
	lister := &fakeModLister{mods: []installedMod{
		managedMod("good.jar", "modrinth", "good", "v1"),
		managedMod("broken.jar", "modrinth", "broken", "v1"),
	}}
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: fp}, lister)

	rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	resp := decodeUpdates(t, rr.Body.Bytes())
	if len(resp.Updates) != 1 || resp.Updates[0].Name != "good.jar" {
		t.Errorf("updates = %+v", resp.Updates)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Name != "broken.jar" || resp.Errors[0].Error != "registry unavailable" {
		t.Errorf("errors = %+v", resp.Errors)
	}
}

func TestModUpdates_NoCompatibleReleaseIsSilent(t *testing.T) {
	// Provider has no versions for the filter → neither update nor error.
	fp := &fakeVersionsProvider{}
	lister := &fakeModLister{mods: []installedMod{
		managedMod("niche.jar", "modrinth", "niche", "v1"),
	}}
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: fp}, lister)

	resp := decodeUpdates(t, do(t, r, "GET", "/servers/alpha/mods/updates", nil).Body.Bytes())
	if len(resp.Updates) != 0 || len(resp.Errors) != 0 {
		t.Fatalf("resp = %+v, want empty", resp)
	}
}

func TestModUpdates_CachesAndDedupes(t *testing.T) {
	fp := &fakeVersionsProvider{byProject: map[string][]registry.Version{
		"sodium": {{ID: "v2", Files: []registry.File{{Filename: "s.jar", DownloadURL: "https://cdn/s.jar"}}}},
	}}
	// Two installed files referencing the same project + a second request:
	// upstream must be queried exactly once.
	lister := &fakeModLister{mods: []installedMod{
		managedMod("sodium-a.jar", "modrinth", "sodium", "v1"),
		managedMod("sodium-b.jar", "modrinth", "sodium", "v1"),
	}}
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: fp}, lister)

	for range 2 {
		if rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil); rr.Code != 200 {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	}
	if fp.calls.Load() != 1 {
		t.Errorf("upstream calls = %d, want 1 (dedupe + cache)", fp.calls.Load())
	}
}

func TestModUpdates_FallsBackToActiveVersionFilter(t *testing.T) {
	fp := &fakeVersionsProvider{}
	lister := &fakeModLister{mods: []installedMod{
		{Name: "old.jar", Meta: &installedRef{Provider: "modrinth", ProjectID: "old", VersionID: "v1"}},
	}}
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: fp}, lister)

	do(t, r, "GET", "/servers/alpha/mods/updates", nil)
	// Meta lacks loader/gameVersion → the server's active version fills in.
	if fp.getGotFilter().Loader != "paper" || fp.getGotFilter().GameVersion != "1.21.4" {
		t.Errorf("filter = %+v, want paper/1.21.4", fp.getGotFilter())
	}
}

func TestModUpdates_AgentUnreachable(t *testing.T) {
	lister := &fakeModLister{err: errors.New("dial refused")}
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: &fakeVersionsProvider{}}, lister)
	if rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil); rr.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", rr.Code)
	}
}

func TestModUpdates_NilListerIs503(t *testing.T) {
	r := mountUpdatesRouter(updatesFixtureKube(), fakeSet{p: &fakeVersionsProvider{}}, nil)
	if rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", rr.Code)
	}
}

// countingAvailSet counts Available calls per provider so a test can prove
// B3's hoist: at most one Available call per distinct declared provider,
// not one per installed mod file.
type countingAvailSet struct {
	p              registry.Provider
	availableCalls map[string]int
}

func (s *countingAvailSet) For(_ context.Context, _ registry.Config) (registry.Provider, bool) {
	return s.p, true
}

func (s *countingAvailSet) Available(_ context.Context, provider string) bool {
	s.availableCalls[provider]++
	return true
}

// TestModUpdates_AvailableResolvedOncePerProvider is B3's first half: three
// installed mods sharing one declared provider must resolve Available once,
// not three times — a keyed provider's Available call resolves its API key
// (a DB read plus a live apiserver Secret GET for DBKeyFunc), so this
// bounds that cost by distinct provider, not by installed mod count.
func TestModUpdates_AvailableResolvedOncePerProvider(t *testing.T) {
	fp := &fakeVersionsProvider{byProject: map[string][]registry.Version{
		"a": {{ID: "va"}},
		"b": {{ID: "vb"}},
		"c": {{ID: "vc"}},
	}}
	set := &countingAvailSet{p: fp, availableCalls: map[string]int{}}
	lister := &fakeModLister{mods: []installedMod{
		managedMod("a.jar", "modrinth", "a", "va"),
		managedMod("b.jar", "modrinth", "b", "vb"),
		managedMod("c.jar", "modrinth", "c", "vc"),
	}}
	r := mountUpdatesRouter(updatesFixtureKube(), set, lister)

	if rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil); rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if got := set.availableCalls["modrinth"]; got != 1 {
		t.Errorf("Available(modrinth) calls = %d, want 1 (hoisted out of the per-mod loop)", got)
	}
}

// TestModUpdates_SkipsGitHubProvider is M4: github's Project.ID and
// Version.ID are both the release id (see github.go), so an installed
// mod's "latest" and "installed" release always compare equal — there is
// never an update to detect, so github mods must be skipped rather than
// checked (which would also burn a shared 60/hr api.github.com quota for
// nothing).
func TestModUpdates_SkipsGitHubProvider(t *testing.T) {
	fp := &fakeVersionsProvider{}
	versions := []any{map[string]any{"id": "1.21.4-paper", "loader": "paper", "gameVersion": "1.21.4", "default": true}}
	k := fakeKubeClient(
		newTemplateObj("minecraft", map[string]any{
			"provider": "github",
			"github":   map[string]any{"owner": "someorg", "repo": "somemod"},
		}, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", ""),
	)
	// ProjectID == VersionID, exactly what github.go's Search/Versions
	// produce for an installed release today.
	lister := &fakeModLister{mods: []installedMod{
		managedMod("x.jar", "github", "42", "42"),
	}}
	r := mountUpdatesRouter(k, fakeSet{p: fp}, lister)

	rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil)
	if rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	resp := decodeUpdates(t, rr.Body.Bytes())
	if len(resp.Updates) != 0 || len(resp.Errors) != 0 {
		t.Fatalf("resp = %+v, want empty (github is silently skipped)", resp)
	}
	if fp.calls.Load() != 0 {
		t.Errorf("upstream calls = %d, want 0 (github is never checked)", fp.calls.Load())
	}
}

// TestModUpdates_ThreadsSteamAppID is M5: modUpdateKey/latestFor must carry
// SteamAppID through to registry.Config, or Set.For would reject the steam
// provider (SteamAppID <= 0) for every installed Steam mod.
func TestModUpdates_ThreadsSteamAppID(t *testing.T) {
	fp := &fakeVersionsProvider{byProject: map[string][]registry.Version{
		"42": {{ID: "42"}},
	}}
	var gotCfg registry.Config
	set := fakeSet{p: fp, cfgOut: &gotCfg}
	versions := []any{map[string]any{"id": "1.21.4-paper", "loader": "paper", "gameVersion": "1.21.4", "default": true}}
	k := fakeKubeClient(
		// json.Number, not a bare int: the fake dynamic client's object
		// tracker deep-copies via apimachinery's DeepCopyJSONValue, which
		// panics on a bare int (a real unstructured decode never produces
		// one anyway) but supports json.Number, one of the real shapes it
		// does produce.
		newTemplateObj("minecraft", map[string]any{"provider": "steam", "steamAppID": json.Number("4000")}, versions),
		serverWithVersion("gameplane-games", "alpha", "minecraft", ""),
	)
	lister := &fakeModLister{mods: []installedMod{
		managedMod("x.jar", "steam", "42", "41"),
	}}
	r := mountUpdatesRouter(k, set, lister)

	if rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil); rr.Code != 200 {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	if gotCfg.Provider != "steam" || gotCfg.SteamAppID != 4000 {
		t.Errorf("cfg = %+v, want provider=steam steamAppID=4000 (threaded from the template)", gotCfg)
	}
}

func TestModUpdates_CacheExpiryRefetches(t *testing.T) {
	fp := &fakeVersionsProvider{byProject: map[string][]registry.Version{
		"sodium": {{ID: "v2", Files: []registry.File{{Filename: "s.jar", DownloadURL: "https://cdn/s.jar"}}}},
	}}
	lister := &fakeModLister{mods: []installedMod{
		managedMod("sodium.jar", "modrinth", "sodium", "v1"),
	}}
	k := updatesFixtureKube()
	h := &modUpdatesHandler{
		k: k, reg: fakeSet{p: fp}, agent: lister,
		ttl:   0, // everything is instantly stale
		cache: map[modUpdateKey]cachedLatest{},
		sem:   make(chan struct{}, 4),
	}
	r := chi.NewRouter()
	r.Get("/servers/{name}/mods/updates", h.updates)

	for range 2 {
		if rr := do(t, r, "GET", "/servers/alpha/mods/updates", nil); rr.Code != 200 {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	}
	if fp.calls.Load() != 2 {
		t.Errorf("upstream calls = %d, want 2 (ttl expired)", fp.calls.Load())
	}
}
