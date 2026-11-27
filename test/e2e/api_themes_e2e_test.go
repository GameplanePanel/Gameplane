//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Theme marketplace e2e (spec 043). The theme packages are pushed by
// fixtures/theme-oras-push-job.yaml to the in-cluster test registry
// (fixtures/oci-registry.yaml) under this prefix; the API dials it through
// netguard.IsAllowed, which admits in-cluster addresses.
const (
	e2eThemeRegistry = "gameplane-test-registry.gameplane-system.svc:5000/e2e-themes"
	e2eThemeSigned   = "e2e-theme-signed"
	e2eThemeUnsigned = "e2e-theme-unsigned"
	// e2eThemeKeypair is the Secret theme-cosign-sign-job.yaml generates.
	e2eThemeKeypair = "e2e-theme-sign-keypair"
)

// e2eThemeCatalogItem is one entry of GET /themes/catalog
// (contracts/themes-api.md, "Catalog").
type e2eThemeCatalogItem struct {
	ID        int64  `json:"id"`
	Source    string `json:"source"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	Status    string `json:"status"`
	Installed *struct {
		Version string `json:"version"`
	} `json:"installed"`
	UpdateAvailable bool `json:"updateAvailable"`
}

// e2eThemeCatalog is the GET /themes/catalog body.
type e2eThemeCatalog struct {
	Themes  []e2eThemeCatalogItem `json:"themes"`
	Sources []struct {
		Name      string `json:"name"`
		LastError string `json:"lastError"`
	} `json:"sources"`
}

// e2eInstalledTheme is one installed theme, as listed by GET /users/me/themes
// and, with tokens and css, returned by GET /users/me/themes/{id}.
type e2eInstalledTheme struct {
	ID              int64           `json:"id"`
	Source          string          `json:"source"`
	Name            string          `json:"name"`
	Version         string          `json:"version"`
	UpdateAvailable bool            `json:"updateAvailable"`
	SourceRemoved   bool            `json:"sourceRemoved"`
	Selected        bool            `json:"selected"`
	Tokens          json.RawMessage `json:"tokens"`
}

// e2eThemeSelection is the theme part of GET /users/me/preferences.
type e2eThemeSelection struct {
	ThemeType        string `json:"themeType"`
	PresetID         string `json:"presetId"`
	InstalledThemeID *int64 `json:"installedThemeId"`
	InstalledTheme   *struct {
		Tokens  json.RawMessage `json:"tokens"`
		Version string          `json:"version"`
	} `json:"installedTheme"`
}

// themeCall sends one request and fails the test unless the status is want.
// It returns the response body.
func themeCall(t *testing.T, cli *APIClient, method, path string, body any, want int) []byte {
	t.Helper()
	resp, data, err := cli.DoContext(t.Context(), method, path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	// DoContext has read and closed the body already; this satisfies bodyclose.
	_ = resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, resp.StatusCode, want, string(data))
	}
	return data
}

// themeDecode unmarshals a response body into out or fails the test.
func themeDecode(t *testing.T, data []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode %T: %v\n%s", out, err, string(data))
	}
}

// themeCatalogOf lists the catalog entries of one source as cli sees them.
func themeCatalogOf(t *testing.T, cli *APIClient, source string) map[string]e2eThemeCatalogItem {
	t.Helper()
	var cat e2eThemeCatalog
	themeDecode(t, themeCall(t, cli, http.MethodGet, "/themes/catalog?source="+source, nil, http.StatusOK), &cat)
	out := map[string]e2eThemeCatalogItem{}
	for _, e := range cat.Themes {
		if e.Source == source {
			out[e.Name] = e
		}
	}
	return out
}

// themeSourceErrors returns the lastError the catalog reports for a source, for
// failure messages while waiting on an index.
func themeSourceErrors(t *testing.T, cli *APIClient, source string) string {
	t.Helper()
	var cat e2eThemeCatalog
	themeDecode(t, themeCall(t, cli, http.MethodGet, "/themes/catalog", nil, http.StatusOK), &cat)
	for _, s := range cat.Sources {
		if s.Name == source {
			return s.LastError
		}
	}
	return ""
}

// installedThemes lists the caller's installed themes by id.
func installedThemes(t *testing.T, cli *APIClient) map[int64]e2eInstalledTheme {
	t.Helper()
	var list []e2eInstalledTheme
	themeDecode(t, themeCall(t, cli, http.MethodGet, "/users/me/themes", nil, http.StatusOK), &list)
	out := map[int64]e2eInstalledTheme{}
	for _, it := range list {
		out[it.ID] = it
	}
	return out
}

// themeSelection reads the caller's theme preferences.
func themeSelection(t *testing.T, cli *APIClient) e2eThemeSelection {
	t.Helper()
	var sel e2eThemeSelection
	themeDecode(t, themeCall(t, cli, http.MethodGet, "/users/me/preferences", nil, http.StatusOK), &sel)
	return sel
}

// ociThemeSourceBody is the POST/PUT /themes/sources body of an OCI source on
// the plain-HTTP test registry. verifyKey, when set, is the cosign public key
// PEM of a keyed signature policy.
func ociThemeSourceBody(name string, themes []string, verifyKey string, enabled bool) map[string]any {
	body := map[string]any{
		"name": name,
		"type": "oci",
		"spec": map[string]any{"oci": map[string]any{
			"url":      e2eThemeRegistry,
			"themes":   themes,
			"insecure": true,
		}},
		"refreshInterval": 300,
		"enabled":         enabled,
	}
	if verifyKey != "" {
		body["verify"] = map[string]any{"key": verifyKey}
	}
	return body
}

// ensureThemeRegistry applies the shared in-cluster test registry and waits
// until it serves.
func ensureThemeRegistry(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	envInstance.ApplyYAML(t, "oci-registry.yaml")
	envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
		dep, err := envInstance.K8s.AppsV1().Deployments("gameplane-system").
			Get(ctx, "gameplane-test-registry", metav1.GetOptions{})
		if err != nil {
			return false, "get registry deploy: " + err.Error()
		}
		if dep.Status.ReadyReplicas >= 1 {
			return true, ""
		}
		return false, "registry not ready yet"
	})
}

// pushE2EThemes pushes e2e-theme-signed:0.1.0 and e2e-theme-unsigned:0.1.0.
// The caller holds ociPushMu.
func pushE2EThemes(t *testing.T) {
	t.Helper()
	ensureThemeRegistry(t)
	envInstance.OCIPushFromFixture(t, "gameplane-system", "e2e-theme-oras-push", "theme-oras-push-job.yaml")
}

// runThemeSignJob applies a cosign sign Job fixture and waits for it to
// succeed. The caller holds ociPushMu.
func runThemeSignJob(t *testing.T, fixture, job string) {
	t.Helper()
	ctx := context.Background()
	bg := metav1.DeletePropagationBackground
	_ = envInstance.K8s.BatchV1().Jobs("gameplane-system").
		Delete(ctx, job, metav1.DeleteOptions{PropagationPolicy: &bg})
	envInstance.ApplyYAML(t, fixture)
	t.Cleanup(func() {
		_ = envInstance.K8s.BatchV1().Jobs("gameplane-system").
			Delete(context.Background(), job, metav1.DeleteOptions{PropagationPolicy: &bg})
	})
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		j, err := envInstance.K8s.BatchV1().Jobs("gameplane-system").Get(ctx, job, metav1.GetOptions{})
		if err != nil {
			return false, "get sign job: " + err.Error()
		}
		if j.Status.Succeeded > 0 {
			return true, ""
		}
		if j.Status.Failed > 0 {
			out, _ := envInstance.Kubectl(ctx, "logs", "-n", "gameplane-system",
				"job/"+job, "--all-containers", "--tail=200")
			return false, "sign job failed:\n" + out
		}
		return false, "sign job not done yet"
	})
}

// signE2ETheme generates a fresh key pair and signs e2e-theme-signed:0.1.0.
// It returns the public key PEM. The caller holds ociPushMu.
func signE2ETheme(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	// generate-key-pair refuses an existing Secret, so start clean.
	_ = envInstance.K8s.CoreV1().Secrets("gameplane-system").Delete(ctx, e2eThemeKeypair, metav1.DeleteOptions{})
	t.Cleanup(func() {
		_ = envInstance.K8s.CoreV1().Secrets("gameplane-system").
			Delete(context.Background(), e2eThemeKeypair, metav1.DeleteOptions{})
	})
	runThemeSignJob(t, "theme-cosign-sign-job.yaml", "e2e-theme-cosign-sign")
	sec, err := envInstance.K8s.CoreV1().Secrets("gameplane-system").Get(ctx, e2eThemeKeypair, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get theme signing key pair: %v", err)
	}
	pub := strings.TrimSpace(string(sec.Data["cosign.pub"]))
	if !strings.Contains(pub, "PUBLIC KEY") {
		t.Fatalf("Secret %s has no PEM cosign.pub", e2eThemeKeypair)
	}
	return pub
}

// TestAPI_ThemeMarketplaceLifecycle drives the theme marketplace API (spec
// 043, contracts/themes-api.md) against a real registry: a signed and an
// unsigned theme are pushed to the in-cluster test registry, an admin adds an
// insecure OCI source, the catalog lists both, and a keyed verify policy turns
// the unsigned one unverified (not installable). User A installs and selects
// the signed theme, user B's preferences and installed list stay untouched, a
// newer signed version is pushed and A updates to it. Disabling the source
// keeps A's installed copy rendering (sourceRemoved), deleting the source keeps
// it too, and removing it falls A back to Modern Pink (FR-011, FR-013).
//
// Budget: three logins (e2e-admin, user A, user B); see the themes bucket in
// buckets.sh. t.Parallel(): the source and users have unique names; the
// registry pushes and the cosign Jobs run under ociPushMu.
func TestAPI_ThemeMarketplaceLifecycle(t *testing.T) {
	t.Parallel()
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	sourceName := "e2e-themes-" + suffix

	var pubKey string
	func() {
		ociPushMu.Lock()
		defer ociPushMu.Unlock()
		pushE2EThemes(t)
		pubKey = signE2ETheme(t)
	}()

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	// Registered first so it runs last, after the cleanups that use admin.
	t.Cleanup(admin.Close)

	userA, passA, idA := envInstance.CreateUser(t, admin, "viewer", "e2e-themes-a")
	userB, passB, idB := envInstance.CreateUser(t, admin, "viewer", "e2e-themes-b")
	for _, id := range []string{idA, idB} {
		t.Cleanup(func() {
			r, _, _ := admin.Delete("/users/" + id)
			if r != nil {
				_ = r.Body.Close()
			}
		})
	}
	t.Cleanup(func() {
		r, _, _ := admin.Delete("/themes/sources/" + sourceName)
		if r != nil {
			_ = r.Body.Close()
		}
	})

	themes := []string{e2eThemeSigned, e2eThemeUnsigned}

	// Add the source without a signature policy: both themes index as valid.
	var created struct {
		Name             string  `json:"name"`
		Builtin          bool    `json:"builtin"`
		CredentialSecret *string `json:"credentialSecret"`
	}
	themeDecode(t, themeCall(t, admin, http.MethodPost, "/themes/sources",
		ociThemeSourceBody(sourceName, themes, "", true), http.StatusCreated), &created)
	if created.Name != sourceName || created.Builtin || created.CredentialSecret != nil {
		t.Fatalf("created source = %+v, want %s, not built in, no credential Secret", created, sourceName)
	}

	a := envInstance.APIClient(t, userA, passA)
	defer a.Close()

	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		cat := themeCatalogOf(t, a, sourceName)
		for _, name := range themes {
			e, ok := cat[name]
			if !ok || e.Status != "valid" || e.Version != "0.1.0" {
				return false, fmt.Sprintf("catalog %v (source error %q)", cat, themeSourceErrors(t, admin, sourceName))
			}
		}
		return true, ""
	})

	// Keyed verify with the signing public key: the unsigned theme is flagged.
	themeCall(t, admin, http.MethodPut, "/themes/sources/"+sourceName,
		ociThemeSourceBody(sourceName, themes, pubKey, true), http.StatusOK)
	var catalog map[string]e2eThemeCatalogItem
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		catalog = themeCatalogOf(t, a, sourceName)
		if catalog[e2eThemeSigned].Status != "valid" || catalog[e2eThemeUnsigned].Status != "unverified" {
			return false, fmt.Sprintf("want signed valid and unsigned unverified, catalog %v (source error %q)",
				catalog, themeSourceErrors(t, admin, sourceName))
		}
		return true, ""
	})
	body := themeCall(t, a, http.MethodPost, "/users/me/themes",
		map[string]any{"catalogId": catalog[e2eThemeUnsigned].ID}, http.StatusUnprocessableEntity)
	if !strings.Contains(string(body), "entry_not_valid") {
		t.Errorf("install of the unverified theme: body %s, want entry_not_valid", body)
	}

	// User A installs and selects the signed theme.
	var installed e2eInstalledTheme
	themeDecode(t, themeCall(t, a, http.MethodPost, "/users/me/themes",
		map[string]any{"catalogId": catalog[e2eThemeSigned].ID, "select": true}, http.StatusCreated), &installed)
	if installed.Name != e2eThemeSigned || installed.Version != "0.1.0" || !installed.Selected || installed.SourceRemoved {
		t.Fatalf("installed = %+v, want %s 0.1.0, selected", installed, e2eThemeSigned)
	}
	themeID := strconv.FormatInt(installed.ID, 10)
	selA := themeSelection(t, a)
	if selA.ThemeType != "installed" || selA.InstalledThemeID == nil || *selA.InstalledThemeID != installed.ID ||
		selA.InstalledTheme == nil || selA.InstalledTheme.Version != "0.1.0" {
		t.Fatalf("user A preferences = %+v, want installed theme %d at 0.1.0", selA, installed.ID)
	}
	themeCall(t, a, http.MethodPost, "/users/me/themes",
		map[string]any{"catalogId": catalog[e2eThemeSigned].ID}, http.StatusConflict)

	// User B is unaffected: no installed copy, preset preferences, and A's id
	// is not reachable.
	b := envInstance.APIClient(t, userB, passB)
	defer b.Close()
	if got := installedThemes(t, b); len(got) != 0 {
		t.Fatalf("user B installed themes = %v, want none", got)
	}
	if selB := themeSelection(t, b); selB.ThemeType == "installed" || selB.InstalledThemeID != nil {
		t.Fatalf("user B preferences = %+v, want no installed theme", selB)
	}
	themeCall(t, b, http.MethodGet, "/users/me/themes/"+themeID, nil, http.StatusNotFound)
	themeCall(t, a, http.MethodPost, "/users/me/themes/"+themeID+"/update", nil, http.StatusConflict)

	// Update: push and sign 0.2.0, refresh, and move A's copy to it.
	func() {
		ociPushMu.Lock()
		defer ociPushMu.Unlock()
		envInstance.OCIPushFromFixture(t, "gameplane-system", "e2e-theme-oras-push-v2", "theme-oras-push-v2-job.yaml")
		runThemeSignJob(t, "theme-cosign-sign-v2-job.yaml", "e2e-theme-cosign-sign-v2")
	}()
	themeCall(t, admin, http.MethodPost, "/themes/sources/"+sourceName+"/refresh", nil, http.StatusAccepted)
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		got := installedThemes(t, a)[installed.ID]
		if !got.UpdateAvailable {
			return false, fmt.Sprintf("installed %+v has no update yet (catalog %v)", got, themeCatalogOf(t, a, sourceName))
		}
		return true, ""
	})
	var updated e2eInstalledTheme
	themeDecode(t, themeCall(t, a, http.MethodPost, "/users/me/themes/"+themeID+"/update", nil, http.StatusOK), &updated)
	if updated.ID != installed.ID || updated.Version != "0.2.0" || updated.UpdateAvailable || !updated.Selected {
		t.Fatalf("updated = %+v, want the same id at 0.2.0, selected, no further update", updated)
	}
	if sel := themeSelection(t, a); sel.InstalledTheme == nil || sel.InstalledTheme.Version != "0.2.0" {
		t.Fatalf("user A preferences after update = %+v, want installed theme at 0.2.0", sel)
	}
	if got := installedThemes(t, b); len(got) != 0 {
		t.Fatalf("user B installed themes after A's update = %v, want none", got)
	}

	// Disable the source: its entries leave the catalog, A's copy still renders.
	themeCall(t, admin, http.MethodPut, "/themes/sources/"+sourceName,
		ociThemeSourceBody(sourceName, themes, pubKey, false), http.StatusOK)
	if cat := themeCatalogOf(t, a, sourceName); len(cat) != 0 {
		t.Fatalf("catalog of the disabled source = %v, want empty", cat)
	}
	requireRendersSourceRemoved(t, a, themeID, "0.2.0")
	body = themeCall(t, a, http.MethodPost, "/users/me/themes/"+themeID+"/update", nil, http.StatusConflict)
	if !strings.Contains(string(body), "source_disabled") {
		t.Errorf("update with the source disabled: body %s, want source_disabled", body)
	}

	// Delete the source: the installed copy stays, marked sourceRemoved.
	themeCall(t, admin, http.MethodDelete, "/themes/sources/"+sourceName, nil, http.StatusNoContent)
	requireRendersSourceRemoved(t, a, themeID, "0.2.0")
	body = themeCall(t, a, http.MethodPost, "/users/me/themes/"+themeID+"/update", nil, http.StatusConflict)
	if !strings.Contains(string(body), "source_removed") {
		t.Errorf("update with the source deleted: body %s, want source_removed", body)
	}
	if sel := themeSelection(t, a); sel.ThemeType != "installed" {
		t.Fatalf("user A preferences after source delete = %+v, want the installed theme still selected", sel)
	}

	// Remove the installed copy: A falls back to Modern Pink.
	themeCall(t, a, http.MethodDelete, "/users/me/themes/"+themeID, nil, http.StatusNoContent)
	if sel := themeSelection(t, a); sel.ThemeType != "preset" || sel.PresetID != "pink" || sel.InstalledThemeID != nil {
		t.Fatalf("user A preferences after removal = %+v, want preset pink", sel)
	}
	if got := installedThemes(t, a); len(got) != 0 {
		t.Fatalf("user A installed themes after removal = %v, want none", got)
	}
}

// requireRendersSourceRemoved asserts that GET /users/me/themes/{id} still
// returns the installed copy at version, with its tokens, marked sourceRemoved.
func requireRendersSourceRemoved(t *testing.T, cli *APIClient, id, version string) {
	t.Helper()
	var got e2eInstalledTheme
	themeDecode(t, themeCall(t, cli, http.MethodGet, "/users/me/themes/"+id, nil, http.StatusOK), &got)
	if got.Version != version || !got.SourceRemoved || !strings.Contains(string(got.Tokens), "accent") {
		t.Fatalf("installed theme %s = %+v (tokens %s), want version %s, sourceRemoved, tokens kept",
			id, got, got.Tokens, version)
	}
}
