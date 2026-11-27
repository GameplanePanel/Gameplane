package themes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/bundlefetch/oci"
	"github.com/GameplanePanel/gameplane/bundlefetch/source"
	"github.com/GameplanePanel/gameplane/bundlefetch/verify"
	"github.com/GameplanePanel/gameplane/themepkg"
)

// fakeOCI is an in-process registry for the indexer: tags per repository and
// files per repository@tag. Pull returns the digest set for that tag.
type fakeOCI struct {
	mu        sync.Mutex
	tags      map[string][]string
	tagsErr   map[string]error
	files     map[string]map[string][]byte
	digests   map[string]string
	listCalls map[string]int
}

func newFakeOCI() *fakeOCI {
	return &fakeOCI{
		tags:      map[string][]string{},
		tagsErr:   map[string]error{},
		files:     map[string]map[string][]byte{},
		digests:   map[string]string{},
		listCalls: map[string]int{},
	}
}

// ListTags returns the configured tags of ref, or the configured error.
func (f *fakeOCI) ListTags(_ context.Context, ref string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls[ref]++
	if err := f.tagsErr[ref]; err != nil {
		return nil, err
	}
	return append([]string(nil), f.tags[ref]...), nil
}

// Pull returns the digest and a copy of the files set for ref@reference.
func (f *fakeOCI) Pull(_ context.Context, ref, reference string) (string, map[string][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := ref + "@" + reference
	files, ok := f.files[key]
	if !ok {
		return "", nil, fmt.Errorf("manifest unknown: %s", key)
	}
	out := make(map[string][]byte, len(files))
	for k, v := range files {
		out[k] = v
	}
	return f.digests[key], out, nil
}

// publish makes version of the theme repository ref available with files and digest.
func (f *fakeOCI) publish(ref, version, digest string, files map[string][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags[ref] = append(f.tags[ref], version)
	f.files[ref+"@"+version] = files
	f.digests[ref+"@"+version] = digest
}

// fakeVerifier stands in for the Sigstore verifier; err is returned by Verify.
type fakeVerifier struct {
	mu  sync.Mutex
	err error
}

// Verify reports the configured result.
func (f *fakeVerifier) Verify(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *fakeVerifier) set(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// fakeClock is a manually advanced clock.
type fakeClock struct{ now time.Time }

// Now returns the clock's time.
func (c *fakeClock) Now() time.Time { return c.now }

// harness is a Service over an in-memory store with fake registry, verifier
// and clock. Local sources read from localRoot.
type harness struct {
	t         *testing.T
	store     *db.Store
	svc       *Service
	oci       *fakeOCI
	verifier  *fakeVerifier
	clock     *fakeClock
	localRoot string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t:         t,
		store:     newTestStore(t),
		oci:       newFakeOCI(),
		verifier:  &fakeVerifier{},
		clock:     &fakeClock{now: time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)},
		localRoot: t.TempDir(),
	}
	h.svc = New(Deps{
		Store:     h.store,
		Clock:     h.clock.Now,
		LocalRoot: h.localRoot,
		OCI:       func(oci.CredentialFunc, bool) source.OCIClient { return h.oci },
		Verifier: func(context.Context, string, bool) (verify.Verifier, error) {
			return h.verifier, nil
		},
	})
	return h
}

// newTestStore opens an in-memory SQLite store and runs migrations.
func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), "sqlite", "file::memory:?_pragma=journal_mode(WAL)&cache=shared")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

// addSource stores an enabled source with the default interval.
func (h *harness) addSource(name, typ, spec string, verifyJSON *string) {
	h.t.Helper()
	_, err := h.store.CreateThemeSource(context.Background(), db.ThemeSource{
		Name: name, Type: typ, Spec: spec, Verify: verifyJSON,
		RefreshIntervalS: 3600, Enabled: true,
	})
	if err != nil {
		h.t.Fatalf("create source %s: %v", name, err)
	}
}

// source reloads a source by name.
func (h *harness) source(name string) db.ThemeSource {
	h.t.Helper()
	src, err := h.store.GetThemeSource(context.Background(), name)
	if err != nil {
		h.t.Fatalf("get source %s: %v", name, err)
	}
	return src
}

// refresh runs Refresh and fails the test on error.
func (h *harness) refresh(name string) {
	h.t.Helper()
	if err := h.svc.Refresh(context.Background(), name); err != nil {
		h.t.Fatalf("refresh %s: %v", name, err)
	}
}

// detail returns the full catalog row of a theme in a source.
func (h *harness) detail(sourceName, theme string) (db.ThemeCatalogEntry, bool) {
	h.t.Helper()
	list, err := h.store.ListThemeCatalog(context.Background(), db.ThemeCatalogFilter{Source: sourceName})
	if err != nil {
		h.t.Fatalf("list catalog: %v", err)
	}
	for _, e := range list {
		if e.Name == theme {
			full, err := h.store.GetThemeCatalogEntry(context.Background(), e.ID)
			if err != nil {
				h.t.Fatalf("get catalog entry: %v", err)
			}
			// Previews are served on their own route, so the entry read does not
			// carry them; load them the way that route does.
			for mode, dst := range map[string]*[]byte{"light": &full.PreviewLight, "dark": &full.PreviewDark} {
				data, err := h.store.GetThemeCatalogPreview(context.Background(), e.ID, mode)
				switch {
				case err == nil:
					*dst = data
				case errors.Is(err, db.ErrThemeNotFound):
				default:
					h.t.Fatalf("get %s preview: %v", mode, err)
				}
			}
			return full, true
		}
	}
	return db.ThemeCatalogEntry{}, false
}

// problems decodes the stored problems JSON.
func problems(t *testing.T, raw string) []themepkg.Problem {
	t.Helper()
	var out []themepkg.Problem
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode problems %q: %v", raw, err)
	}
	return out
}

// fixture reads a file of the shared theme package fixture.
func fixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	p := filepath.Join(append([]string{"..", "..", "..", "themepkg", "testdata", "good"}, parts...)...)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture %s: %v", p, err)
	}
	return data
}

// goodFiles is a valid deep-ocean 1.2.0 package.
func goodFiles(t *testing.T) map[string][]byte {
	t.Helper()
	return map[string][]byte{
		"theme.yaml":        fixture(t, "theme.yaml"),
		"previews/dark.png": fixture(t, "previews", "dark.png"),
	}
}

// writeLocalTheme writes a theme package into root/<dir>.
func writeLocalTheme(t *testing.T, root, dir string, files map[string][]byte) {
	t.Helper()
	for name, data := range files {
		p := filepath.Join(root, dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
}

const (
	deepOceanRepo = "registry.test/themes/deep-ocean"
	ociSpecOne    = `{"oci":{"url":"registry.test/themes","themes":["deep-ocean"]}}`
)

func TestRefreshOCIIndexesLatestValidTheme(t *testing.T) {
	h := newHarness(t)
	h.oci.publish(deepOceanRepo, "1.1.0", "sha256:old", goodFiles(t))
	h.oci.publish(deepOceanRepo, "1.2.0", "sha256:new", goodFiles(t))
	h.addSource("oci-src", "oci", ociSpecOne, nil)

	h.refresh("oci-src")

	e, ok := h.detail("oci-src", "deep-ocean")
	if !ok {
		t.Fatal("deep-ocean not indexed")
	}
	if e.Status != "valid" {
		t.Fatalf("status = %q, want valid (problems %s)", e.Status, e.Problems)
	}
	if e.Version != "1.2.0" || e.Digest != "sha256:new" {
		t.Fatalf("version/digest = %s/%s, want 1.2.0/sha256:new", e.Version, e.Digest)
	}
	if e.Versions != `["1.2.0","1.1.0"]` {
		t.Fatalf("versions = %s", e.Versions)
	}
	if e.CSS != nil {
		t.Fatalf("css set for a package without theme.css")
	}
	if len(e.PreviewDark) == 0 || len(e.PreviewLight) != 0 {
		t.Fatalf("previews dark=%d light=%d", len(e.PreviewDark), len(e.PreviewLight))
	}
	if !strings.Contains(e.Metadata, `"displayName":"Deep Ocean"`) {
		t.Fatalf("metadata = %s", e.Metadata)
	}
	if !strings.Contains(e.Tokens, `"dark"`) {
		t.Fatalf("tokens = %s", e.Tokens)
	}
	src := h.source("oci-src")
	if src.LastSyncAt == nil || src.LastError != nil {
		t.Fatalf("sync state: last_sync=%v last_error=%v", src.LastSyncAt, src.LastError)
	}
}

func TestRefreshSkipsUnchangedDigest(t *testing.T) {
	h := newHarness(t)
	h.oci.publish(deepOceanRepo, "1.2.0", "sha256:aaa", goodFiles(t))
	h.addSource("oci-src", "oci", ociSpecOne, nil)
	h.refresh("oci-src")
	first, _ := h.detail("oci-src", "deep-ocean")

	h.clock.now = h.clock.now.Add(time.Hour)
	h.refresh("oci-src")
	again, _ := h.detail("oci-src", "deep-ocean")
	if again.IndexedAt != first.IndexedAt {
		t.Fatalf("unchanged digest re-indexed: indexed_at %s -> %s", first.IndexedAt, again.IndexedAt)
	}

	h.oci.publish(deepOceanRepo, "1.3.0", "sha256:bbb", goodFiles(t))
	h.clock.now = h.clock.now.Add(time.Hour)
	h.refresh("oci-src")
	changed, _ := h.detail("oci-src", "deep-ocean")
	if changed.Digest != "sha256:bbb" || changed.IndexedAt == first.IndexedAt {
		t.Fatalf("changed digest not re-indexed: digest %s indexed_at %s", changed.Digest, changed.IndexedAt)
	}
}

func TestRefreshInvalidPackageIsNotServed(t *testing.T) {
	h := newHarness(t)
	files := goodFiles(t)
	files["theme.yaml"] = []byte(strings.Replace(string(files["theme.yaml"]), "name: deep-ocean", "name: other", 1))
	h.oci.publish(deepOceanRepo, "1.2.0", "sha256:bad", files)
	h.addSource("oci-src", "oci", ociSpecOne, nil)

	h.refresh("oci-src")

	e, ok := h.detail("oci-src", "deep-ocean")
	if !ok {
		t.Fatal("invalid theme has no catalog row")
	}
	if e.Status != "invalid" {
		t.Fatalf("status = %q, want invalid", e.Status)
	}
	if e.CSS != nil || e.Tokens != "{}" || len(e.PreviewDark) != 0 {
		t.Fatalf("invalid content served: css=%v tokens=%s preview=%d", e.CSS, e.Tokens, len(e.PreviewDark))
	}
	found := false
	for _, p := range problems(t, e.Problems) {
		found = found || p.Rule == themepkg.RuleSchema
	}
	if !found {
		t.Fatalf("no schema problem in %s", e.Problems)
	}
}

func TestRefreshVerifyFailureIsUnverifiedUntilRecheck(t *testing.T) {
	h := newHarness(t)
	h.oci.publish(deepOceanRepo, "1.2.0", "sha256:sig", goodFiles(t))
	policy := `{"keyless":{"issuer":"https://issuer.test","identity":"id"}}`
	h.addSource("oci-src", "oci", ociSpecOne, &policy)
	h.verifier.set(errors.New("no matching signatures"))

	h.refresh("oci-src")
	e, _ := h.detail("oci-src", "deep-ocean")
	if e.Status != "unverified" {
		t.Fatalf("status = %q, want unverified", e.Status)
	}
	if got := problems(t, e.Problems); len(got) != 1 || got[0].Rule != ruleSignature {
		t.Fatalf("problems = %s", e.Problems)
	}

	// The policy now passes, but the digest is unchanged, so a plain refresh
	// keeps the row. A recheck re-verifies every entry (spec 043 R4).
	h.verifier.set(nil)
	h.refresh("oci-src")
	if e, _ := h.detail("oci-src", "deep-ocean"); e.Status != "unverified" {
		t.Fatalf("unchanged digest re-verified without recheck: %s", e.Status)
	}
	if err := h.svc.refresh(context.Background(), h.source("oci-src"), true); err != nil {
		t.Fatalf("recheck: %v", err)
	}
	if e, _ := h.detail("oci-src", "deep-ocean"); e.Status != "valid" {
		t.Fatalf("recheck status = %q, want valid", e.Status)
	}
}

func TestSourceFailureIsolatedAndRedacted(t *testing.T) {
	h := newHarness(t)
	h.oci.publish(deepOceanRepo, "1.2.0", "sha256:ok", goodFiles(t))
	h.addSource("oci-src", "oci", ociSpecOne, nil)
	h.refresh("oci-src")

	writeLocalTheme(t, h.localRoot, "other", goodFiles(t))
	h.addSource("local-src", "local", `{"local":{"path":"."}}`, nil)
	h.refresh("local-src")

	// The registry now fails for the only theme: the source fails, keeps its
	// rows, and records a redacted error.
	h.oci.mu.Lock()
	h.oci.tagsErr[deepOceanRepo] = errors.New("dial https://user:hunter2@registry.test failed")
	h.oci.mu.Unlock()
	if err := h.svc.Refresh(context.Background(), "oci-src"); err == nil {
		t.Fatal("Refresh returned nil for a failed source")
	}

	if _, ok := h.detail("oci-src", "deep-ocean"); !ok {
		t.Fatal("failed source lost its catalog rows")
	}
	src := h.source("oci-src")
	if src.LastError == nil || strings.Contains(*src.LastError, "hunter2") {
		t.Fatalf("last_error = %v, want redacted message", src.LastError)
	}
	if _, ok := h.detail("local-src", "other"); !ok {
		t.Fatal("other source's rows were affected")
	}
	if other := h.source("local-src"); other.LastError != nil {
		t.Fatalf("other source got last_error %q", *other.LastError)
	}
}

func TestPartialThemeFailureKeepsRowAndRecordsError(t *testing.T) {
	h := newHarness(t)
	h.oci.publish(deepOceanRepo, "1.2.0", "sha256:ok", goodFiles(t))
	h.addSource("oci-src", "oci", `{"oci":{"url":"registry.test/themes","themes":["deep-ocean","missing"]}}`, nil)
	h.oci.mu.Lock()
	h.oci.tagsErr["registry.test/themes/missing"] = errors.New("not found")
	h.oci.mu.Unlock()

	h.refresh("oci-src")

	if _, ok := h.detail("oci-src", "deep-ocean"); !ok {
		t.Fatal("deep-ocean not indexed beside a failing theme")
	}
	if _, ok := h.detail("oci-src", "missing"); ok {
		t.Fatal("failing theme got a catalog row")
	}
	src := h.source("oci-src")
	if src.LastError == nil || !strings.Contains(*src.LastError, "missing") {
		t.Fatalf("last_error = %v, want the failing theme named", src.LastError)
	}
}

func TestRemovedThemeIsDeleted(t *testing.T) {
	h := newHarness(t)
	h.oci.publish(deepOceanRepo, "1.2.0", "sha256:ok", goodFiles(t))
	h.addSource("oci-src", "oci", ociSpecOne, nil)
	h.refresh("oci-src")

	src := h.source("oci-src")
	src.Spec = `{"oci":{"url":"registry.test/themes","themes":[]}}`
	if err := h.store.UpdateThemeSource(context.Background(), src); err != nil {
		t.Fatalf("update source: %v", err)
	}
	h.refresh("oci-src")
	if _, ok := h.detail("oci-src", "deep-ocean"); ok {
		t.Fatal("theme no longer listed by the source is still cached")
	}
}

func TestLocalDirectorySourceIndexesEachTheme(t *testing.T) {
	h := newHarness(t)
	writeLocalTheme(t, h.localRoot, "deep-ocean", goodFiles(t))
	broken := goodFiles(t)
	broken["theme.yaml"] = []byte("apiVersion: nope\n")
	writeLocalTheme(t, h.localRoot, "broken", broken)
	h.addSource("local-src", "local", `{"local":{"path":"."}}`, nil)

	h.refresh("local-src")

	ok, found := h.detail("local-src", "deep-ocean")
	if !found || ok.Status != "valid" || ok.Versions != `["1.2.0"]` {
		t.Fatalf("deep-ocean = %+v (found %v)", ok, found)
	}
	bad, found := h.detail("local-src", "broken")
	if !found || bad.Status != "invalid" {
		t.Fatalf("broken theme status = %+v (found %v)", bad, found)
	}
}

func TestLocalSourceWithoutRootFails(t *testing.T) {
	h := newHarness(t)
	h.svc.localRoot = ""
	h.addSource("local-src", "local", `{"local":{"path":"."}}`, nil)
	if err := h.svc.Refresh(context.Background(), "local-src"); err == nil {
		t.Fatal("local source with no root indexed")
	}
}

func TestRunDueRefreshesOnlyDueSources(t *testing.T) {
	h := newHarness(t)
	h.oci.publish("registry.test/themes/one", "1.0.0", "sha256:1", goodFiles(t))
	h.oci.publish("registry.test/themes/two", "1.0.0", "sha256:2", goodFiles(t))
	h.addSource("first", "oci", `{"oci":{"url":"registry.test/themes","themes":["one"]}}`, nil)
	h.refresh("first")
	h.addSource("second", "oci", `{"oci":{"url":"registry.test/themes","themes":["two"]}}`, nil)

	h.clock.now = h.clock.now.Add(10 * time.Minute)
	h.svc.RunDue(context.Background())

	h.oci.mu.Lock()
	defer h.oci.mu.Unlock()
	if n := h.oci.listCalls["registry.test/themes/one"]; n != 1 {
		t.Fatalf("source inside its interval was refreshed (list calls %d)", n)
	}
	if n := h.oci.listCalls["registry.test/themes/two"]; n != 1 {
		t.Fatalf("never-synced source not refreshed (list calls %d)", n)
	}
}

func TestIsDueAppliesIntervalFloor(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *string {
		s := now.Add(-d).Format(time.RFC3339)
		return &s
	}
	tests := []struct {
		name     string
		src      db.ThemeSource
		wantDue  bool
		scenario string
	}{
		{"never synced", db.ThemeSource{Enabled: true, RefreshIntervalS: 3600}, true, "due"},
		{"disabled", db.ThemeSource{Enabled: false, RefreshIntervalS: 60}, false, "disabled sources are never due"},
		{"stored interval below floor", db.ThemeSource{Enabled: true, RefreshIntervalS: 60, LastSyncAt: at(200 * time.Second)}, false, "floor is 300 s"},
		{"stored interval below floor, elapsed", db.ThemeSource{Enabled: true, RefreshIntervalS: 60, LastSyncAt: at(301 * time.Second)}, true, "floor is 300 s"},
		{"hour interval not elapsed", db.ThemeSource{Enabled: true, RefreshIntervalS: 3600, LastSyncAt: at(59 * time.Minute)}, false, "interval 3600 s"},
		{"hour interval elapsed", db.ThemeSource{Enabled: true, RefreshIntervalS: 3600, LastSyncAt: at(time.Hour)}, true, "interval 3600 s"},
		{"unparseable sync time", db.ThemeSource{Enabled: true, RefreshIntervalS: 3600, LastSyncAt: func() *string { s := "yesterday"; return &s }()}, true, "due"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDue(tt.src, now); got != tt.wantDue {
				t.Fatalf("isDue = %v, want %v (%s)", got, tt.wantDue, tt.scenario)
			}
		})
	}
}

func TestLatestVersionPrefersReleases(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want string
		ok   bool
	}{
		{"release beats newer pre-release", []string{"1.3.0-rc.1", "1.2.0", "1.1.0"}, "1.2.0", true},
		{"only pre-releases", []string{"2.0.0-rc.1", "1.0.0-rc.2"}, "2.0.0-rc.1", true},
		{"build metadata is not a pre-release", []string{"1.0.0+build.1"}, "1.0.0+build.1", true},
		{"no tags", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := latestVersion(tt.tags)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("latestVersion = %q,%v want %q,%v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRedactStripsURLUserinfo(t *testing.T) {
	got := redact("get https://user:secret@registry.test/v2/: denied")
	if strings.Contains(got, "secret") || !strings.Contains(got, "registry.test/v2/") {
		t.Fatalf("redact = %q", got)
	}
}
