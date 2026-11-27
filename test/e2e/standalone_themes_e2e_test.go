//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A second panel-only release, so this test never shares names with
// TestStandalone_HelmPanelLifecycle (which owns e2e-standalone /
// e2e-standalone-panel) and both can run in parallel in the standalone bucket.
const (
	standaloneThemesNamespace = "e2e-standalone-themes"
	standaloneThemesRelease   = "e2e-standalone-themes"
)

// TestStandalone_ThemeSourceInstall covers spec 043 OD-4 A on a panel with no
// operator: the chart's panel-only profile (api.standalone=true,
// operator.enabled=false) adds an OCI theme source on the in-cluster test
// registry, indexes it, and the admin installs and selects a theme. Themes live
// in the panel database and the API fetches them itself, so nothing here needs
// a cluster registration. The chart has no setting for a local theme root
// (--themes-local-root), so an OCI source is used.
//
// Budget: one login against the standalone panel's own limiter; none on the
// shared panel. The registry push runs under ociPushMu.
func TestStandalone_ThemeSourceInstall(t *testing.T) {
	t.Parallel()
	func() {
		ociPushMu.Lock()
		defer ociPushMu.Unlock()
		pushE2EThemes(t)
	}()

	p := &standalonePanel{t: t, password: recoveryRandom(t)}
	installStandaloneThemesPanel(t)
	admin := standaloneThemesLogin(t, p)
	defer admin.Close()

	var installation struct {
		Standalone   bool `json:"standalone"`
		LocalCluster bool `json:"localCluster"`
	}
	p.decode(themeCall(t, admin, http.MethodGet, "/admin/installation", nil, http.StatusOK), &installation)
	if !installation.Standalone || installation.LocalCluster {
		t.Fatalf("installation = %+v, want standalone without a local cluster", installation)
	}

	// No signature policy, so the unsigned package indexes as valid.
	sourceName := "e2e-sa-themes-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	themeCall(t, admin, http.MethodPost, "/themes/sources",
		ociThemeSourceBody(sourceName, []string{e2eThemeUnsigned}, "", true), http.StatusCreated)
	var entry e2eThemeCatalogItem
	envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
		entry = themeCatalogOf(t, admin, sourceName)[e2eThemeUnsigned]
		if entry.Status != "valid" || entry.Version != "0.1.0" {
			return false, fmt.Sprintf("catalog entry %+v (source error %q)", entry, themeSourceErrors(t, admin, sourceName))
		}
		return true, ""
	})

	var installed e2eInstalledTheme
	themeDecode(t, themeCall(t, admin, http.MethodPost, "/users/me/themes",
		map[string]any{"catalogId": entry.ID, "select": true}, http.StatusCreated), &installed)
	if installed.Name != e2eThemeUnsigned || installed.Version != "0.1.0" || !installed.Selected {
		t.Fatalf("installed = %+v, want %s 0.1.0, selected", installed, e2eThemeUnsigned)
	}
	sel := themeSelection(t, admin)
	if sel.ThemeType != "installed" || sel.InstalledTheme == nil || !strings.Contains(string(sel.InstalledTheme.Tokens), "accent") {
		t.Fatalf("preferences = %+v, want the installed theme selected with its tokens", sel)
	}

	themeCall(t, admin, http.MethodDelete, "/users/me/themes/"+strconv.FormatInt(installed.ID, 10), nil, http.StatusNoContent)
	if sel := themeSelection(t, admin); sel.ThemeType != "preset" || sel.PresetID != "pink" {
		t.Fatalf("preferences after removal = %+v, want preset pink", sel)
	}
	themeCall(t, admin, http.MethodDelete, "/themes/sources/"+sourceName, nil, http.StatusNoContent)
}

// installStandaloneThemesPanel installs the panel-only profile as its own Helm
// release, with the same settings as standalonePanel.install except the
// remote CIDR list, which only applies to cluster registrations.
func installStandaloneThemesPanel(t *testing.T) {
	t.Helper()
	tag := envInstance.Tag
	if tag == "" {
		tag = "e2e"
	}
	chart := filepath.Join("..", "..", "charts", "gameplane")
	t.Cleanup(func() {
		if t.Failed() && os.Getenv("GITHUB_ACTIONS") == "true" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		_ = exec.CommandContext(ctx, "helm", "uninstall", standaloneThemesRelease, "--namespace", standaloneThemesNamespace,
			"--kube-context", envInstance.Context, "--wait", "--timeout", "2m").Run()
		_ = envInstance.K8s.CoreV1().Namespaces().Delete(ctx, standaloneThemesNamespace, metav1.DeleteOptions{})
	})
	cmd := exec.CommandContext(t.Context(), "helm", "install", standaloneThemesRelease, chart,
		"--namespace", standaloneThemesNamespace, "--kube-context", envInstance.Context, "--create-namespace",
		"--set", "image.registry=gameplane-test,image.tag="+tag+",operator.enabled=false,api.standalone=true,web.enabled=false,ingress.enabled=false,crds.autoApply.enabled=false",
		"--wait", "--timeout", "5m")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install standalone themes panel: %v: %s", err, out)
	}
}

// standaloneThemesLogin bootstraps the panel's admin with p.password and signs
// in through a fresh port-forward.
func standaloneThemesLogin(t *testing.T, p *standalonePanel) *APIClient {
	t.Helper()
	out, err := envInstance.KubectlWithStdin(t.Context(), p.password+"\n",
		"exec", "-i", "-n", standaloneThemesNamespace, "deploy/gameplane-api", "-c", "api", "--",
		"/api", "bootstrap-admin", "--username=admin", "--password-stdin", "--force")
	if err != nil {
		t.Fatalf("bootstrap standalone themes admin: %v: %s", err, out)
	}
	port, stop := envInstance.PortForward(t, standaloneThemesNamespace, "svc/gameplane-api", 80)
	c := &APIClient{BaseURL: fmt.Sprintf("http://127.0.0.1:%d", port), HTTP: &http.Client{Jar: newInsecureCookieJar(), Timeout: 20 * time.Second}, stop: stop}
	var login struct {
		CSRF string `json:"csrf"`
	}
	p.decode(p.expect(c, http.MethodPost, "/auth/login", map[string]string{"username": "admin", "password": p.password}, http.StatusOK), &login)
	if login.CSRF == "" {
		t.Fatal("standalone themes login response lacked a CSRF token")
	}
	c.CSRF = login.CSRF
	return c
}
