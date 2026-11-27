package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/themes"
	"github.com/GameplanePanel/gameplane/bundlefetch/oci"
	"github.com/GameplanePanel/gameplane/bundlefetch/source"
	"github.com/GameplanePanel/gameplane/bundlefetch/verify"
)

// This file exercises the theme catalog routes (spec 043, T023) against an
// in-process registry fake and the SQLite store. It needs no cluster and no
// network, so it carries no build tag.

// themeCatFakeOCI is an in-process OCI registry: tags and files per repository.
// A repository listed in tagsErr fails its tag listing.
type themeCatFakeOCI struct {
	mu      sync.Mutex
	tags    map[string][]string
	tagsErr map[string]error
	files   map[string]map[string][]byte
	digests map[string]string
}

func newThemeCatFakeOCI() *themeCatFakeOCI {
	return &themeCatFakeOCI{
		tags:    map[string][]string{},
		tagsErr: map[string]error{},
		files:   map[string]map[string][]byte{},
		digests: map[string]string{},
	}
}

// ListTags implements source.OCIClient.
func (f *themeCatFakeOCI) ListTags(_ context.Context, ref string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.tagsErr[ref]; err != nil {
		return nil, err
	}
	return append([]string(nil), f.tags[ref]...), nil
}

// Pull implements source.OCIClient.
func (f *themeCatFakeOCI) Pull(_ context.Context, ref, reference string) (string, map[string][]byte, error) {
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

// publish makes one version of the theme repository ref available.
func (f *themeCatFakeOCI) publish(ref, version, digest string, files map[string][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags[ref] = append(f.tags[ref], version)
	f.files[ref+"@"+version] = files
	f.digests[ref+"@"+version] = digest
}

// themeCatVerifier rejects every signature, so a source with a verify policy
// lands its entries as unverified.
type themeCatVerifier struct{}

// Verify implements verify.Verifier.
func (themeCatVerifier) Verify(context.Context, string, string) error {
	return errors.New("signature does not match the policy")
}

// themeCatFixture reads a file from the shared themepkg testdata.
func themeCatFixture(t *testing.T, parts ...string) []byte {
	t.Helper()
	p := filepath.Join(append([]string{"..", "..", "..", "themepkg", "testdata"}, parts...)...)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read fixture %s: %v", p, err)
	}
	return data
}

// themeCatSeq gives every test its own source names. The test store is an
// in-memory database shared by the package's tests, so names must not repeat.
var themeCatSeq atomic.Int64

// themeCatEnv is a store with four sources and the registry fake behind them:
// a valid one, one whose theme is invalid, one whose policy fails, and one
// whose registry is unreachable.
type themeCatEnv struct {
	t        *testing.T
	store    *db.Store
	svc      *themes.Service
	oci      *themeCatFakeOCI
	good     string
	bad      string
	unsigned string
	down     string
}

func newThemeCatEnv(t *testing.T) *themeCatEnv {
	t.Helper()
	ctx := context.Background()
	store := newTestStore(t)
	prefix := fmt.Sprintf("tc%d", themeCatSeq.Add(1))
	env := &themeCatEnv{
		t:        t,
		store:    store,
		oci:      newThemeCatFakeOCI(),
		good:     prefix + "-good",
		bad:      prefix + "-bad",
		unsigned: prefix + "-unsigned",
		down:     prefix + "-down",
	}
	env.svc = themes.New(themes.Deps{
		Store: store,
		OCI:   func(oci.CredentialFunc, bool) source.OCIClient { return env.oci },
		Verifier: func(context.Context, string, bool) (verify.Verifier, error) {
			return themeCatVerifier{}, nil
		},
	})

	goodFiles := map[string][]byte{
		"theme.yaml":        themeCatFixture(t, "good", "theme.yaml"),
		"previews/dark.png": themeCatFixture(t, "good", "previews", "dark.png"),
	}
	badFiles := map[string][]byte{
		"theme.yaml":        themeCatFixture(t, "bad", "css-syntax", "theme.yaml"),
		"previews/dark.png": themeCatFixture(t, "bad", "css-syntax", "previews", "dark.png"),
		"theme.css":         themeCatFixture(t, "bad", "css-syntax", "theme.css"),
	}
	signed := `{"key":"-----BEGIN PUBLIC KEY-----\nMFkw\n-----END PUBLIC KEY-----"}`

	env.addSource(env.good, `{"oci":{"url":"registry.test/%s","themes":["deep-ocean"]}}`, nil)
	env.addSource(env.bad, `{"oci":{"url":"registry.test/%s","themes":["deep-ocean"]}}`, nil)
	env.addSource(env.unsigned, `{"oci":{"url":"registry.test/%s","themes":["deep-ocean"]}}`, &signed)
	env.addSource(env.down, `{"oci":{"url":"registry.test/%s","themes":["deep-ocean"]}}`, nil)

	env.oci.publish("registry.test/"+env.good+"/deep-ocean", "1.2.0", "sha256:good", goodFiles)
	env.oci.publish("registry.test/"+env.bad+"/deep-ocean", "1.2.0", "sha256:bad", badFiles)
	env.oci.publish("registry.test/"+env.unsigned+"/deep-ocean", "1.2.0", "sha256:unsigned", goodFiles)
	env.oci.tagsErr["registry.test/"+env.down+"/deep-ocean"] = errors.New("registry unreachable")

	for _, name := range []string{env.good, env.bad, env.unsigned, env.down} {
		// Refresh returns an error for the unreachable source; the others succeed.
		if err := env.svc.Refresh(ctx, name); err != nil && name != env.down {
			t.Fatalf("refresh %s: %v", name, err)
		}
	}
	return env
}

// addSource stores an enabled OCI source. spec is a format string with the
// source name, so each source points at its own repository.
func (env *themeCatEnv) addSource(name, spec string, verifyJSON *string) {
	env.t.Helper()
	_, err := env.store.CreateThemeSource(context.Background(), db.ThemeSource{
		Name:             name,
		Type:             "oci",
		Spec:             fmt.Sprintf(spec, name),
		Verify:           verifyJSON,
		RefreshIntervalS: 3600,
		Enabled:          true,
	})
	if err != nil {
		env.t.Fatalf("create source %s: %v", name, err)
	}
}

// setEnabled toggles a source on or off.
func (env *themeCatEnv) setEnabled(name string, enabled bool) {
	env.t.Helper()
	ctx := context.Background()
	src, err := env.store.GetThemeSource(ctx, name)
	if err != nil {
		env.t.Fatalf("get source %s: %v", name, err)
	}
	src.Enabled = enabled
	if err := env.store.UpdateThemeSource(ctx, src); err != nil {
		env.t.Fatalf("update source %s: %v", name, err)
	}
}

// router serves the catalog routes as the given caller.
func (env *themeCatEnv) router(role string) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithUser(req.Context(), &auth.User{ID: 424242, Username: "caller", Role: role})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	MountThemes(r, env.store, env.svc)
	return r
}

// themeCatGet sends a GET request to h.
func themeCatGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// themeCatList decodes GET /themes/catalog with the given query.
func themeCatList(t *testing.T, h http.Handler, query string) themeCatalogResp {
	t.Helper()
	rr := themeCatGet(t, h, "/themes/catalog"+query)
	if rr.Code != http.StatusOK {
		t.Fatalf("list %q: status %d body %s", query, rr.Code, rr.Body.String())
	}
	var resp themeCatalogResp
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return resp
}

// themeCatFind returns the list row of one theme in one source.
func themeCatFind(resp themeCatalogResp, source, name string) (themeCatalogItem, bool) {
	for _, item := range resp.Themes {
		if item.Source == source && item.Name == name {
			return item, true
		}
	}
	return themeCatalogItem{}, false
}

func TestThemeCatalogListReportsStatusAndFailingSources(t *testing.T) {
	env := newThemeCatEnv(t)
	h := env.router("viewer")
	resp := themeCatList(t, h, "")

	good, ok := themeCatFind(resp, env.good, "deep-ocean")
	if !ok {
		t.Fatalf("valid theme missing from catalog: %+v", resp.Themes)
	}
	if good.Status != "valid" || good.Version != "1.2.0" || good.DisplayName != "Deep Ocean" {
		t.Errorf("valid row = %+v", good)
	}
	if len(good.Modes) != 1 || good.Modes[0] != "dark" {
		t.Errorf("valid row modes = %v, want [dark]", good.Modes)
	}
	if good.Installed != nil || good.UpdateAvailable {
		t.Errorf("valid row shows install state without an install: %+v", good)
	}

	if bad, ok := themeCatFind(resp, env.bad, "deep-ocean"); !ok || bad.Status != "invalid" || len(bad.Modes) != 0 {
		t.Errorf("invalid row = %+v (found %v), want status invalid and no modes", bad, ok)
	}
	if signed, ok := themeCatFind(resp, env.unsigned, "deep-ocean"); !ok || signed.Status != "unverified" {
		t.Errorf("unsigned row = %+v (found %v), want status unverified", signed, ok)
	}

	// The unreachable source is reported; the healthy sources are still listed.
	var downErr string
	for _, s := range resp.Sources {
		if s.Name == env.down {
			downErr = s.LastError
		}
		if s.Name == env.good {
			t.Errorf("healthy source reported as failing: %+v", s)
		}
	}
	if downErr == "" {
		t.Errorf("unreachable source missing from sources: %+v", resp.Sources)
	}
}

func TestThemeCatalogListFilters(t *testing.T) {
	env := newThemeCatEnv(t)
	h := env.router("viewer")

	if resp := themeCatList(t, h, "?source="+env.good); len(resp.Themes) != 1 || resp.Themes[0].Source != env.good {
		t.Errorf("source filter returned %+v", resp.Themes)
	}
	if resp := themeCatList(t, h, "?source="+env.good+"&q=DEEP"); len(resp.Themes) != 1 {
		t.Errorf("case-insensitive q returned %+v", resp.Themes)
	}
	if resp := themeCatList(t, h, "?source="+env.good+"&q=nothing-matches"); len(resp.Themes) != 0 {
		t.Errorf("non-matching q returned %+v", resp.Themes)
	}
	if resp := themeCatList(t, h, "?source="+env.good+"&tag=Calm"); len(resp.Themes) != 1 {
		t.Errorf("tag filter (case-insensitive) returned %+v", resp.Themes)
	}
	if resp := themeCatList(t, h, "?source="+env.good+"&tag=neon"); len(resp.Themes) != 0 {
		t.Errorf("absent tag returned %+v", resp.Themes)
	}
}

func TestThemeCatalogDetailAndVisibility(t *testing.T) {
	env := newThemeCatEnv(t)
	h := env.router("viewer")

	good, ok := themeCatFind(themeCatList(t, h, "?source="+env.good), env.good, "deep-ocean")
	if !ok {
		t.Fatal("valid theme missing")
	}
	rr := themeCatGet(t, h, fmt.Sprintf("/themes/catalog/%d", good.ID))
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status %d body %s", rr.Code, rr.Body.String())
	}
	var detail map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if _, ok := detail["tokens"]; !ok {
		t.Errorf("valid detail lacks tokens")
	}
	if detail["changelog"] == "" || detail["homepage"] == "" {
		t.Errorf("valid detail lacks changelog or homepage: %v", detail)
	}

	unsigned, ok := themeCatFind(themeCatList(t, h, "?source="+env.unsigned), env.unsigned, "deep-ocean")
	if !ok {
		t.Fatal("unsigned theme missing")
	}
	rr = themeCatGet(t, h, fmt.Sprintf("/themes/catalog/%d", unsigned.ID))
	if rr.Code != http.StatusOK {
		t.Fatalf("unsigned detail status %d", rr.Code)
	}
	if bytes.Contains(rr.Body.Bytes(), []byte(`"tokens"`)) || bytes.Contains(rr.Body.Bytes(), []byte(`"css"`)) {
		t.Errorf("unverified detail exposes tokens or css: %s", rr.Body.String())
	}

	for _, path := range []string{"/themes/catalog/abc", "/themes/catalog/0", "/themes/catalog/987654321"} {
		if rr := themeCatGet(t, h, path); rr.Code != http.StatusNotFound {
			t.Errorf("GET %s status %d, want 404", path, rr.Code)
		}
	}

	// Entries of a disabled source stay cached but are hidden everywhere.
	env.setEnabled(env.good, false)
	if rr := themeCatGet(t, h, fmt.Sprintf("/themes/catalog/%d", good.ID)); rr.Code != http.StatusNotFound {
		t.Errorf("detail of disabled source status %d, want 404", rr.Code)
	}
	if _, ok := themeCatFind(themeCatList(t, h, ""), env.good, "deep-ocean"); ok {
		t.Errorf("disabled source still listed")
	}
	env.setEnabled(env.good, true)
	if _, ok := themeCatFind(themeCatList(t, h, ""), env.good, "deep-ocean"); !ok {
		t.Errorf("re-enabled source not listed again")
	}
}

func TestThemeCatalogPreviewHeaders(t *testing.T) {
	env := newThemeCatEnv(t)
	h := env.router("viewer")

	good, _ := themeCatFind(themeCatList(t, h, "?source="+env.good), env.good, "deep-ocean")
	rr := themeCatGet(t, h, fmt.Sprintf("/themes/catalog/%d/preview/dark", good.ID))
	if rr.Code != http.StatusOK {
		t.Fatalf("dark preview status %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if v := rr.Header().Get("X-Content-Type-Options"); v != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", v)
	}
	if v := rr.Header().Get("Cache-Control"); v != "private, max-age=3600" {
		t.Errorf("Cache-Control = %q", v)
	}
	if !bytes.Equal(rr.Body.Bytes(), themeCatFixture(t, "good", "previews", "dark.png")) {
		t.Errorf("preview bytes differ from the stored image")
	}

	if rr := themeCatGet(t, h, fmt.Sprintf("/themes/catalog/%d/preview/light", good.ID)); rr.Code != http.StatusNotFound {
		t.Errorf("missing light preview status %d, want 404", rr.Code)
	}
	if rr := themeCatGet(t, h, fmt.Sprintf("/themes/catalog/%d/preview/neon", good.ID)); rr.Code != http.StatusNotFound {
		t.Errorf("unknown mode status %d, want 404", rr.Code)
	}

	unsigned, _ := themeCatFind(themeCatList(t, h, "?source="+env.unsigned), env.unsigned, "deep-ocean")
	if rr := themeCatGet(t, h, fmt.Sprintf("/themes/catalog/%d/preview/dark", unsigned.ID)); rr.Code != http.StatusNotFound {
		t.Errorf("unverified theme preview status %d, want 404", rr.Code)
	}
}

func TestThemeCatalogReadableByAnySignedInUser(t *testing.T) {
	env := newThemeCatEnv(t)
	for _, role := range []string{"viewer", "admin"} {
		if rr := themeCatGet(t, env.router(role), "/themes/catalog"); rr.Code != http.StatusOK {
			t.Errorf("role %s list status %d, want 200", role, rr.Code)
		}
	}
}
