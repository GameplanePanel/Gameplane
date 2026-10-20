//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// modDownloadURL is a small, stable, public file the agent can fetch through
// its SSRF guard (the guard requires a PUBLIC dialed IP, so an in-cluster
// fixture server can't stand in). raw.githubusercontent.com is already a
// hard dependency of CI (actions, images), and the file is this repo's own
// README (~25 KB, well under the 16 MB mods limit).
const modDownloadURL = "https://raw.githubusercontent.com/GameplanePanel/Gameplane/main/README.md"

// applyModsTemplate is applyBusyboxTemplate plus a mods capability: a plain
// "mods" directory with URL installs allowed from GitHub raw. No extension
// filter — the e2e "mod" is an arbitrary small file.
func applyModsTemplate(t *testing.T, tmplName string) {
	t.Helper()
	ctx := context.Background()
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": tmplName},
		"spec": map[string]any{
			"displayName": "E2E mods busybox (" + tmplName + ")",
			"game":        "busybox",
			"version":     "1",
			"image":       "busybox:1.36",
			"command":     []any{"sh", "-c", "sleep 100000"},
			"ports": []any{
				map[string]any{"name": "noop", "containerPort": int64(12345), "advertise": true, "protocol": "TCP"},
			},
			"capabilities": map[string]any{
				"mods": map[string]any{
					"path": "mods",
					"install": map[string]any{
						"allowedHosts": []any{"raw.githubusercontent.com"},
						"maxSizeMB":    int64(16),
					},
				},
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).
		Create(ctx, tmpl, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create template %s: %v", tmplName, err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).
			Delete(context.Background(), tmplName, metav1.DeleteOptions{})
	})
}

// modEntry mirrors the agent's mod listing (agent/internal/mods.Mod).
type modEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Meta *struct {
		Provider      string `json:"provider"`
		ProjectID     string `json:"projectId"`
		VersionID     string `json:"versionId"`
		VersionNumber string `json:"versionNumber"`
		SourceURL     string `json:"sourceUrl"`
		InstalledAt   string `json:"installedAt"`
	} `json:"meta"`
}

func listServerMods(t *testing.T, cli *APIClient, gs string) []modEntry {
	t.Helper()
	resp, body, err := cli.Get("/servers/" + gs + "/mods")
	if err != nil {
		t.Fatalf("GET /mods: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/mods expected 200, got %d body=%q", resp.StatusCode, string(body))
	}
	var mods []modEntry
	if err := json.Unmarshal(body, &mods); err != nil {
		t.Fatalf("decode /mods: %v body=%q", err, string(body))
	}
	return mods
}

// TestAPI_ModManifestInstallUpgrade proves the mod install manifest
// round-trips through the whole stack: dashboard-shaped install requests
// with registry metadata land in the agent's per-volume manifest, listings
// echo the metadata back, `replaces` performs an in-place upgrade (new file
// in, old file + manifest entry out), and remove prunes the entry.
func TestAPI_ModManifestInstallUpgrade(t *testing.T) {
	t.Parallel()

	ns := "gameplane-games"
	tmpl := "e2e-mods-manifest-tmpl"
	gs := "e2e-mods-manifest-gs"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyModsTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
	requireAgentReady(t, ns, gs)

	// The mods list is the cheapest mods-capable readiness signal.
	envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
		resp, body, err := cli.Get("/servers/" + gs + "/mods")
		if err != nil {
			return false, "GET /mods: " + err.Error()
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false, "status=" + http.StatusText(resp.StatusCode) + " body=" + string(body)
		}
		return true, ""
	})

	// Install v1 with registry identity. The download host must be public
	// (SSRF guard), so this leg needs egress — same dependency as image
	// pulls in this job.
	resp, body, err := cli.Post("/servers/"+gs+"/mods/install", map[string]any{
		"url":  modDownloadURL,
		"name": "e2e-mod-1.0.0.bin",
		"meta": map[string]any{
			"provider":      "modrinth",
			"projectId":     "e2e-proj",
			"versionId":     "v1",
			"versionNumber": "1.0.0",
		},
	})
	if err != nil {
		t.Fatalf("POST /mods/install: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("install expected 200, got %d body=%q", resp.StatusCode, string(body))
	}

	mods := listServerMods(t, cli, gs)
	if len(mods) != 1 || mods[0].Name != "e2e-mod-1.0.0.bin" {
		t.Fatalf("mods after install = %+v", mods)
	}
	if m := mods[0].Meta; m == nil || m.Provider != "modrinth" || m.ProjectID != "e2e-proj" ||
		m.VersionID != "v1" || m.InstalledAt == "" || m.SourceURL == "" {
		t.Fatalf("manifest meta after install = %+v", mods[0].Meta)
	}

	// Upgrade in place: install v2 replacing v1.
	resp, body, err = cli.Post("/servers/"+gs+"/mods/install", map[string]any{
		"url":      modDownloadURL,
		"name":     "e2e-mod-2.0.0.bin",
		"replaces": "e2e-mod-1.0.0.bin",
		"meta": map[string]any{
			"provider":      "modrinth",
			"projectId":     "e2e-proj",
			"versionId":     "v2",
			"versionNumber": "2.0.0",
		},
	})
	if err != nil {
		t.Fatalf("POST /mods/install (upgrade): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upgrade expected 200, got %d body=%q", resp.StatusCode, string(body))
	}

	mods = listServerMods(t, cli, gs)
	if len(mods) != 1 || mods[0].Name != "e2e-mod-2.0.0.bin" {
		t.Fatalf("mods after upgrade = %+v, want only e2e-mod-2.0.0.bin", mods)
	}
	if m := mods[0].Meta; m == nil || m.VersionID != "v2" {
		t.Fatalf("manifest meta after upgrade = %+v, want versionId v2", mods[0].Meta)
	}

	// Remove prunes both the file and its manifest entry.
	delResp, delBody, err := cli.Delete("/servers/" + gs + "/mods?name=" + url.QueryEscape("e2e-mod-2.0.0.bin"))
	if err != nil {
		t.Fatalf("DELETE /mods: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode/100 != 2 {
		t.Fatalf("remove expected 2xx, got %d body=%q", delResp.StatusCode, string(delBody))
	}
	if mods = listServerMods(t, cli, gs); len(mods) != 0 {
		t.Fatalf("mods after remove = %+v, want empty", mods)
	}
}
