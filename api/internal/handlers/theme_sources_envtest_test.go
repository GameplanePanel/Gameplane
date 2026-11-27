package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/themes"
)

// This file exercises the admin theme source routes (spec 043, T048): the
// validation refusals, the create and list shapes with no credential in any
// response, the verify edit re-check, disabling, deletion keeping installed
// copies, the built-in official source, and refresh. The RBAC rule for the
// routes is covered in the rbac package. It reuses the catalog fixture and the
// helpers of themes_catalog_envtest_test.go and themes_installed_envtest_test.go.

// themeSrcSpec returns the oci settings JSON of a source whose registry prefix
// is name and whose one theme is deep-ocean.
func themeSrcSpec(name string) string {
	return fmt.Sprintf(`{"oci":{"url":"registry.test/%s","themes":["deep-ocean"]}}`, name)
}

// themeSrcBody builds a POST or PUT body from its parts. extra is appended as
// further top-level members, each starting with a comma.
func themeSrcBody(name, typ, spec, extra string) string {
	return fmt.Sprintf(`{"name":%q,"type":%q,"spec":%s%s}`, name, typ, spec, extra)
}

// themeSrcPublish publishes the valid good theme under the registry prefix name
// without indexing it.
func themeSrcPublish(t *testing.T, env *themeCatEnv, name string) {
	t.Helper()
	files := map[string][]byte{
		"theme.yaml":        themeCatFixture(t, "good", "theme.yaml"),
		"previews/dark.png": themeCatFixture(t, "good", "previews", "dark.png"),
	}
	env.oci.publish("registry.test/"+name+"/deep-ocean", "1.2.0", "sha256:"+name, files)
}

// themeSrcItems decodes GET /themes/sources.
func themeSrcItems(t *testing.T, h http.Handler) []themeSourceItem {
	t.Helper()
	rr := themeInstDo(t, h, http.MethodGet, "/themes/sources", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("list sources: status %d body %s", rr.Code, rr.Body.String())
	}
	themeSrcAssertNoCredential(t, rr.Body.String())
	var items []themeSourceItem
	if err := json.Unmarshal(rr.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode sources: %v", err)
	}
	return items
}

// themeSrcFind returns the source row with the given name.
func themeSrcFind(t *testing.T, items []themeSourceItem, name string) themeSourceItem {
	t.Helper()
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("source %s missing from %+v", name, items)
	return themeSourceItem{}
}

// themeSrcEntries returns the cached catalog rows of one source.
func themeSrcEntries(t *testing.T, env *themeCatEnv, source string) []db.ThemeCatalogEntry {
	t.Helper()
	entries, err := env.store.ListThemeCatalog(context.Background(), db.ThemeCatalogFilter{Source: source})
	if err != nil {
		t.Fatalf("list catalog of %s: %v", source, err)
	}
	return entries
}

// themeSrcAssertNoCredential fails when a response body carries a credential
// value field. The credentialSecret name is allowed.
func themeSrcAssertNoCredential(t *testing.T, body string) {
	t.Helper()
	// Error texts may name the credential ("credential for an oci source must
	// be ..."); only a JSON field carrying it, or its Secret reference, is a leak.
	lower := strings.ToLower(body)
	for _, key := range []string{`"credential"`, `"credentialref"`, `"credential_ref"`, `"secret"`, `"secretref"`, `"secretname"`, `"secret_ref"`} {
		if strings.Contains(lower, key) {
			t.Fatalf("response carries a credential field (%s): %s", key, body)
		}
	}
}

func TestThemeSourcesRefuseInvalidSettings(t *testing.T) {
	env := newThemeCatEnv(t)
	h := env.router("admin")
	before := len(themeSrcItems(t, h))
	cases := []struct {
		name string
		body string
		want int
	}{
		{"bad name", themeSrcBody("Bad_Name", "oci", themeSrcSpec("bad-name"), ""), http.StatusUnprocessableEntity},
		{"reserved name", themeSrcBody("official", "oci", themeSrcSpec("official-x"), ""), http.StatusUnprocessableEntity},
		{"name too long", themeSrcBody(strings.Repeat("a", 41), "oci", themeSrcSpec("long"), ""), http.StatusUnprocessableEntity},
		{"unknown type", themeSrcBody("svn-one", "svn", `{}`, ""), http.StatusUnprocessableEntity},
		{"no settings for type", themeSrcBody("no-settings", "oci", `{}`, ""), http.StatusUnprocessableEntity},
		{"two config blocks", themeSrcBody("two-configs", "oci",
			`{"oci":{"url":"registry.test/two","themes":["deep-ocean"]},"git":{"url":"https://example.com/t.git"}}`, ""),
			http.StatusUnprocessableEntity},
		{"oci without themes", themeSrcBody("no-themes", "oci",
			`{"oci":{"url":"registry.test/no-themes","themes":[]}}`, ""), http.StatusUnprocessableEntity},
		{"oci url with scheme", themeSrcBody("scheme", "oci",
			`{"oci":{"url":"https://registry.test/scheme","themes":["deep-ocean"]}}`, ""), http.StatusUnprocessableEntity},
		{"verify on git", themeSrcBody("git-verify", "git",
			`{"git":{"url":"https://example.com/t.git"}}`, `,"verify":{"key":"abc"}`), http.StatusUnprocessableEntity},
		{"verify without key or keyless", themeSrcBody("empty-verify", "oci",
			themeSrcSpec("empty-verify"), `,"verify":{}`), http.StatusUnprocessableEntity},
		{"refresh interval under five minutes", themeSrcBody("short-interval", "oci",
			themeSrcSpec("short-interval"), `,"refreshInterval":60`), http.StatusUnprocessableEntity},
		{"local source without a root", themeSrcBody("local-one", "local",
			`{"local":{"path":"themes"}}`, ""), http.StatusUnprocessableEntity},
		{"url with credentials", themeSrcBody("git-creds", "git",
			`{"git":{"url":"https://user:secret@example.com/t.git"}}`, ""), http.StatusUnprocessableEntity},
		{"plain http url", themeSrcBody("plain-http", "http",
			`{"http":{"url":"http://example.com/t.tar.gz"}}`, ""), http.StatusUnprocessableEntity},
		{"unknown spec key", themeSrcBody("typo-key", "oci",
			`{"oci":{"url":"registry.test/typo","themez":["deep-ocean"]}}`, ""), http.StatusUnprocessableEntity},
		{"credential secret not a Kubernetes name", themeSrcBody("cred-shape", "oci", themeSrcSpec("cred-shape"),
			`,"credentialSecret":"Not_A_Name"`), http.StatusUnprocessableEntity},
		{"credential secret on local source", themeSrcBody("cred-local", "local",
			`{"local":{"path":"themes"}}`, `,"credentialSecret":"theme-creds"`), http.StatusUnprocessableEntity},
	}
	for _, c := range cases {
		rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", c.body)
		if rr.Code != c.want {
			t.Errorf("%s: status %d, want %d; body %s", c.name, rr.Code, c.want, rr.Body.String())
		}
	}
	if after := len(themeSrcItems(t, h)); after != before {
		t.Errorf("refused sources were stored: %d sources before, %d after", before, after)
	}
}

func TestThemeSourcesCreateAndListWithoutCredentials(t *testing.T) {
	env := newThemeCatEnv(t)
	h := env.router("admin")
	body := themeSrcBody("extra-src", "oci", themeSrcSpec("extra-src"), `,"refreshInterval":7200`)

	rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rr.Code, rr.Body.String())
	}
	themeSrcAssertNoCredential(t, rr.Body.String())
	var created themeSourceItem
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Name != "extra-src" || created.RefreshInterval != 7200 || !created.Enabled || created.Builtin {
		t.Fatalf("created source: %+v", created)
	}

	if rr := themeInstDo(t, h, http.MethodPost, "/themes/sources", body); rr.Code != http.StatusConflict {
		t.Fatalf("duplicate create: status %d body %s", rr.Code, rr.Body.String())
	}

	listed := themeSrcFind(t, themeSrcItems(t, h), "extra-src")
	if listed.RefreshInterval != 7200 || listed.Type != "oci" {
		t.Fatalf("listed source: %+v", listed)
	}
}

func TestThemeSourceVerifyEditRechecksEntries(t *testing.T) {
	env := newThemeCatEnv(t)
	ctx := context.Background()
	h := env.router("admin")
	const name = "recheck-src"
	themeSrcPublish(t, env, name)

	if rr := themeInstDo(t, h, http.MethodPost, "/themes/sources",
		themeSrcBody(name, "oci", themeSrcSpec(name), "")); rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d body %s", rr.Code, rr.Body.String())
	}
	if err := env.svc.Refresh(ctx, name); err != nil {
		t.Fatalf("index %s: %v", name, err)
	}
	before := themeSrcEntries(t, env, name)
	if len(before) != 1 || before[0].Status != "valid" {
		t.Fatalf("before the policy edit: %+v", before)
	}

	// The test verifier rejects every signature, so adding a key policy must
	// move the entries to unverified without a new digest.
	signed := fmt.Sprintf(`{"type":"oci","spec":%s,"verify":{"key":"-----BEGIN PUBLIC KEY-----\nMFkw\n-----END PUBLIC KEY-----"}}`,
		themeSrcSpec(name))
	if rr := themeInstDo(t, h, http.MethodPut, "/themes/sources/"+name, signed); rr.Code != http.StatusOK {
		t.Fatalf("edit verify: status %d body %s", rr.Code, rr.Body.String())
	}
	env.svc.WaitIdle()

	after := themeSrcEntries(t, env, name)
	if len(after) != 1 || after[0].Status != "unverified" {
		t.Fatalf("after the policy edit: %+v, want one unverified entry", after)
	}
	if after[0].Digest != before[0].Digest {
		t.Fatalf("digest changed on a verify re-check: %s -> %s", before[0].Digest, after[0].Digest)
	}
}

func TestThemeSourceDisableHidesEntriesAndRefusesInstalls(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, _ := newThemeInstUsers(t, env.store)
	user := themeInstRouter(env, alice, nil)
	admin := env.router("admin")
	catalogID := themeInstCatalogID(t, env, env.good)

	disable := fmt.Sprintf(`{"type":"oci","spec":%s,"enabled":false}`, themeSrcSpec(env.good))
	rr := themeInstDo(t, admin, http.MethodPut, "/themes/sources/"+env.good, disable)
	if rr.Code != http.StatusOK {
		t.Fatalf("disable: status %d body %s", rr.Code, rr.Body.String())
	}
	env.svc.WaitIdle()
	if row := themeSrcFind(t, themeSrcItems(t, admin), env.good); row.Enabled {
		t.Fatalf("source still enabled after disable: %+v", row)
	}

	if _, ok := themeCatFind(themeCatList(t, user, ""), env.good, "deep-ocean"); ok {
		t.Fatalf("disabled source still listed in the catalog")
	}
	rr = themeInstDo(t, user, http.MethodPost, "/users/me/themes", fmt.Sprintf(`{"catalogId":%d}`, catalogID))
	if rr.Code != http.StatusConflict {
		t.Fatalf("install from disabled source: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := themeInstDo(t, admin, http.MethodPost, "/themes/sources/"+env.good+"/refresh", ""); rr.Code != http.StatusConflict {
		t.Fatalf("refresh of disabled source: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestThemeSourceDeleteKeepsInstalledCopies(t *testing.T) {
	env := newThemeCatEnv(t)
	alice, _ := newThemeInstUsers(t, env.store)
	user := themeInstRouter(env, alice, nil)
	admin := env.router("admin")
	installed := themeInstInstall(t, user, themeInstCatalogID(t, env, env.good), false)

	if rr := themeInstDo(t, admin, http.MethodDelete, "/themes/sources/"+env.good, ""); rr.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s", rr.Code, rr.Body.String())
	}
	row := themeUpdFind(t, themeInstList(t, user), installed.ID)
	if !row.SourceRemoved {
		t.Fatalf("installed copy after source delete: %+v, want sourceRemoved", row)
	}
	if _, ok := themeCatFind(themeCatList(t, user, ""), env.good, "deep-ocean"); ok {
		t.Fatalf("deleted source still listed in the catalog")
	}
	if rr := themeInstDo(t, admin, http.MethodDelete, "/themes/sources/"+env.good, ""); rr.Code != http.StatusNotFound {
		t.Fatalf("second delete: status %d body %s", rr.Code, rr.Body.String())
	}
}

// officialTestCosignKey is the official signing public key (signing/cosign.pub,
// the same block as the chart's defaultModuleSource.oci.verify.cosignPublicKey).
const officialTestCosignKey = "-----BEGIN PUBLIC KEY-----\nMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEptXw/hWES+fiWwez02SAqfPvvXzb\nDleBuL5fwB4MM20CXQWykjj12Kx3ZHLmf0KTb6TN4QCjJ7B4tj2pc066ow==\n-----END PUBLIC KEY-----\n"

func TestThemeSourceOfficialIsBuiltIn(t *testing.T) {
	env := newThemeCatEnv(t)
	ctx := context.Background()
	if err := env.svc.SeedOfficial(ctx, themes.OfficialConfig{
		Enabled:         true,
		VerifyEnabled:   true,
		CosignPublicKey: officialTestCosignKey,
	}); err != nil {
		t.Fatalf("seed official: %v", err)
	}
	admin := env.router("admin")

	if rr := themeInstDo(t, admin, http.MethodDelete, "/themes/sources/"+themes.OfficialSourceName, ""); rr.Code != http.StatusConflict {
		t.Fatalf("delete official: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := themeInstDo(t, admin, http.MethodPost, "/themes/sources",
		themeSrcBody(themes.OfficialSourceName, "oci", themeSrcSpec("x"), "")); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create named official: status %d body %s", rr.Code, rr.Body.String())
	}

	// The chart owns the built-in source's signature policy: a PUT without
	// verify is accepted and the stored policy is kept (OD-6).
	unsigned := fmt.Sprintf(`{"type":"oci","spec":{"oci":{"url":%q,"themes":[]}}}`, themes.OfficialSourceURL)
	if rr := themeInstDo(t, admin, http.MethodPut, "/themes/sources/"+themes.OfficialSourceName, unsigned); rr.Code != http.StatusOK {
		t.Fatalf("put official without verify: status %d body %s", rr.Code, rr.Body.String())
	} else if !strings.Contains(rr.Body.String(), "BEGIN PUBLIC KEY") {
		t.Fatalf("put official without verify dropped the chart key: %s", rr.Body.String())
	}
	// The stored row keeps the key too, not only the response.
	for _, it := range themeSrcItems(t, admin) {
		if it.Name == themes.OfficialSourceName && !strings.Contains(string(it.Verify), "BEGIN PUBLIC KEY") {
			t.Fatalf("stored official verify lost the chart key: %s", it.Verify)
		}
	}

	// Disabling it is allowed; the response marks it built in.
	disable := fmt.Sprintf(`{"type":"oci","spec":{"oci":{"url":%q,"themes":[]}},"verify":{"key":%q},"enabled":false}`,
		themes.OfficialSourceURL, officialTestCosignKey)
	rr := themeInstDo(t, admin, http.MethodPut, "/themes/sources/"+themes.OfficialSourceName, disable)
	if rr.Code != http.StatusOK {
		t.Fatalf("disable official: status %d body %s", rr.Code, rr.Body.String())
	}
	var item themeSourceItem
	if err := json.Unmarshal(rr.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode official: %v", err)
	}
	if !item.Builtin || item.Enabled {
		t.Fatalf("official after disable: %+v", item)
	}
	env.svc.WaitIdle()

	if rr := themeInstDo(t, admin, http.MethodPut, "/themes/sources/nope-src", disable); rr.Code != http.StatusNotFound {
		t.Fatalf("edit unknown source: status %d body %s", rr.Code, rr.Body.String())
	}
}

func TestThemeSourceRefreshRunsInBackground(t *testing.T) {
	env := newThemeCatEnv(t)
	admin := env.router("admin")

	if rr := themeInstDo(t, admin, http.MethodPost, "/themes/sources/nope-src/refresh", ""); rr.Code != http.StatusNotFound {
		t.Fatalf("refresh unknown source: status %d body %s", rr.Code, rr.Body.String())
	}
	if rr := themeInstDo(t, admin, http.MethodPost, "/themes/sources/"+env.good+"/refresh", ""); rr.Code != http.StatusAccepted {
		t.Fatalf("refresh: status %d body %s", rr.Code, rr.Body.String())
	}
	env.svc.WaitIdle()
	if entries := themeSrcEntries(t, env, env.good); len(entries) != 1 || entries[0].Status != "valid" {
		t.Fatalf("after refresh: %+v", entries)
	}
}
