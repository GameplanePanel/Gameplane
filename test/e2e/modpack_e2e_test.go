//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Spec 035 (CurseForge and FTB modpacks) end-to-end tests: T017, T031, T040.
// T046 (blocked mod) and T052 (FTB pack) live in modpack_blocked_e2e_test.go
// and modpack_ftb_e2e_test.go and are in no CI bucket; see buckets.sh.
//
// The three tests here run in the bot-fast bucket. Every bot-fast test gets
// its own runner and cluster (hack/ci_scope.py bots), so the one admin login
// each test makes is its own login budget. They are gated like the other
// Minecraft bot tests: GAMEPLANE_E2E_GAME_BOT=1 and minecraft-java in scope.
//
// Pack choice (the suite never pushes the real module, so each test builds a
// template that mirrors the minecraft-java 2.13.0 providers block):
//
//   - CurseForge: "Fabulously Optimized" (slug fabulously-optimized), file
//     9127669 = "13.6.0 for 26.1.2", Fabric. Chosen on its public files page
//     (https://www.curseforge.com/minecraft/modpacks/fabulously-optimized/files)
//     because it is the smallest popular Fabric pack: the pack file is about
//     158 KB and the mods it lists are small client-side optimisers, so the
//     server starts fast. No key is needed in CI: the itzg image (Java 17+
//     tags) ships a built-in CurseForge key, and the API install route never
//     calls CurseForge (it only records spec.modpack).
//   - Modrinth: "FPS Modpack" (slug fps, project nJ2Rn3SV), version kedIKix5 =
//     3.2 for 26.1.2, Fabric, a 5 KB .mrpack. Found with the keyless search
//     https://api.modrinth.com/v2/search?facets=[["project_type:modpack"],
//     ["categories:fabric"]]&index=downloads and its /project/fps/version list,
//     picking the smallest file that matches the same Minecraft version as the
//     CurseForge pack.
const (
	modpackNamespace = "gameplane-games"

	// modpackImage is the image the minecraft-java 2.13.0 module pins for
	// Minecraft 26.x (Java 25).
	modpackImage = "itzg/minecraft-server:java25@sha256:9faa6aefeedd5a883c3ee241653fd1421529bdbafc428d0513e43cae0f2b7d68"

	modpackVersionID   = "e2e-fabric"
	modpackGameVersion = "26.1.2"

	cfPackSlug     = "fabulously-optimized"
	cfPackFileID   = "9127669"
	cfPackFileName = "13.6.0 for 26.1.2"
	cfPackTitle    = "Fabulously Optimized"

	mrPackSlug    = "fps"
	mrPackVersion = "kedIKix5"
	mrPackName    = "3.2"
	mrPackTitle   = "FPS Modpack"

	modpackBootTimeout  = 12 * time.Minute
	modpackApplyTimeout = 4 * time.Minute
)

// requireModuleProviders skips the test when the checked-out minecraft-java
// module predates the CurseForge and FTB providers (module 2.13.0, on its own
// branch until merged). When the submodule is absent the inline template below
// is the only source, so the test runs.
func requireModuleProviders(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "modules", "minecraft-java", "template.yaml"))
	if err != nil {
		return
	}
	if !strings.Contains(string(data), "refEnv: CF_SLUG") {
		t.Skip("modules/minecraft-java/template.yaml has no curseforge modpacks block (module 2.13.0 not merged); " +
			"the providers block is required, skipping")
	}
}

// modpackProviders mirrors capabilities.mods.registry.providers of the
// minecraft-java 2.13.0 template (modules/minecraft-java/template.yaml).
func modpackProviders() []any {
	return []any{
		map[string]any{
			"provider": "modrinth",
			"modpacks": map[string]any{
				"refEnv":     "MODRINTH_MODPACK",
				"versionEnv": "MODRINTH_VERSION",
				"adoptVersionFile": map[string]any{
					"path": ".modrinth-modpack-manifest.json", "field": "versionId",
				},
				"env": []any{map[string]any{"name": "TYPE", "value": "MODRINTH"}},
			},
		},
		map[string]any{
			"provider":         "curseforge",
			"curseforgeGameID": int64(432),
			"modpacks": map[string]any{
				"refEnv":            "CF_SLUG",
				"versionEnv":        "CF_FILE_ID",
				"apiKeyEnv":         "CF_API_KEY",
				"needsKeyImageTags": []any{"java8-multiarch", "java8"},
				"missingFilesPath":  "MODS_NEED_DOWNLOAD.txt",
				"env": []any{
					map[string]any{"name": "TYPE", "value": "AUTO_CURSEFORGE"},
					map[string]any{"name": "CF_DOWNLOADS_REPO", "value": "/data/downloads"},
				},
			},
		},
		map[string]any{
			"provider":     "ftb",
			"modpacksOnly": true,
			"modpacks": map[string]any{
				"refEnv":     "FTB_MODPACK_ID",
				"versionEnv": "FTB_MODPACK_VERSION_ID",
				"env":        []any{map[string]any{"name": "TYPE", "value": "FTBA"}},
			},
		},
	}
}

// applyModpackTemplate creates a trimmed minecraft-java template: one Fabric
// version entry (so the operator provisions the per-loader mods volume the
// removal wipe needs) plus the 2.13.0 providers block.
func applyModpackTemplate(t *testing.T, name, image, gameVersion string) {
	t.Helper()
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"displayName": "E2E modpacks (" + name + ")",
			"game":        "minecraft-java",
			"version":     "1",
			"image":       image,
			"security": map[string]any{
				"runAsUser": int64(1000), "runAsGroup": int64(1000), "fsGroup": int64(1000),
			},
			"env": []any{
				map[string]any{"name": "EULA", "value": "TRUE"},
				map[string]any{"name": "ONLINE_MODE", "value": "FALSE"},
				map[string]any{"name": "INIT_MEMORY", "value": "512M"},
				map[string]any{"name": "MAX_MEMORY", "value": "1536M"},
				map[string]any{"name": "USE_AIKAR_FLAGS", "value": "false"},
				map[string]any{"name": "VIEW_DISTANCE", "value": "4"},
				map[string]any{"name": "ENABLE_RCON", "value": "false"},
			},
			"ports": []any{
				map[string]any{"name": "game", "containerPort": int64(25565), "advertise": true, "protocol": "TCP"},
			},
			"storage": map[string]any{"size": "4Gi", "mountPath": "/data"},
			"resources": map[string]any{
				"requests": map[string]any{"cpu": "250m", "memory": "1Gi"},
				"limits":   map[string]any{"cpu": "2", "memory": "2560Mi"},
			},
			"probes": map[string]any{
				"readiness": map[string]any{
					"exec":                map[string]any{"command": []any{"mc-health"}},
					"initialDelaySeconds": int64(30),
					"periodSeconds":       int64(10),
					"failureThreshold":    int64(60),
				},
			},
			"versions": []any{
				map[string]any{
					"id":          modpackVersionID,
					"displayName": "E2E Fabric " + gameVersion,
					"image":       image,
					"loader":      "fabric",
					"gameVersion": gameVersion,
					"env": []any{
						map[string]any{"name": "TYPE", "value": "FABRIC"},
						map[string]any{"name": "VERSION", "value": gameVersion},
					},
				},
			},
			"capabilities": map[string]any{
				"mods": map[string]any{
					"loaders": map[string]any{
						"fabric": map[string]any{
							"displayName": "Mods (Fabric)", "path": "mods",
							"extensions": []any{".jar"},
						},
					},
					"install": map[string]any{
						"allowedHosts": []any{".modrinth.com", ".curseforge.com", ".forgecdn.net"},
						"maxSizeMB":    int64(512),
					},
					"registry": map[string]any{"providers": modpackProviders()},
				},
			},
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameTemplateGVR).
		Create(context.Background(), tmpl, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create template %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameTemplateGVR).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

// applyModpackServer creates the GameServer on the Fabric version entry.
func applyModpackServer(t *testing.T, gs, tmpl string) {
	t.Helper()
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": gs, "namespace": modpackNamespace},
		"spec": map[string]any{
			"templateRef": map[string]any{"name": tmpl},
			"version":     modpackVersionID,
		},
	}}
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(modpackNamespace).
		Create(context.Background(), obj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create gameserver %s: %v", gs, err)
	}
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(gameServerGVR).Namespace(modpackNamespace).
			Delete(context.Background(), gs, metav1.DeleteOptions{})
	})
}

// modpackView is the GET /servers/{name}/modpack body (contracts/api-modpack.md).
type modpackView struct {
	Active *struct {
		Provider       string `json:"provider"`
		Project        string `json:"project"`
		Version        string `json:"version"`
		Pinned         bool   `json:"pinned"`
		PendingRemoval bool   `json:"pendingRemoval"`
	} `json:"active"`
	Phase        string `json:"phase"`
	Message      string `json:"message"`
	MissingFiles []struct {
		Mod          string `json:"mod"`
		Filename     string `json:"filename"`
		DownloadPage string `json:"downloadPage"`
	} `json:"missingFiles"`
	DownloadsPath string `json:"downloadsPath"`
}

func getModpack(t *testing.T, cli *APIClient, gs string) (modpackView, error) {
	t.Helper()
	var v modpackView
	resp, body, err := cli.Get("/servers/" + gs + "/modpack")
	if err != nil {
		return v, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return v, fmt.Errorf("GET /modpack status %d body=%q", resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return v, fmt.Errorf("decode /modpack: %w body=%q", err, string(body))
	}
	return v, nil
}

// installModpack POSTs the install body and requires 202 (the API only writes
// spec.modpack; the operator applies it).
func installModpack(t *testing.T, cli *APIClient, gs, provider, project, version, title, versionName string) {
	t.Helper()
	resp, body, err := cli.Post("/servers/"+gs+"/modpack", map[string]any{
		"provider": provider, "project": project, "version": version,
		"title": title, "versionName": versionName, "backupFirst": false,
	})
	if err != nil {
		t.Fatalf("POST /modpack (%s/%s): %v", provider, project, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /modpack (%s/%s) expected 202, got %d body=%q", provider, project, resp.StatusCode, string(body))
	}
}

// waitModpackApplied waits for the operator to finish applying the change:
// phase Applied and the active pack matching wantProject ("" means no pack).
func waitModpackApplied(t *testing.T, cli *APIClient, gs, wantProject, wantVersion string) {
	t.Helper()
	envInstance.Eventually(t, modpackApplyTimeout, func() (bool, string) {
		v, err := getModpack(t, cli, gs)
		if err != nil {
			return false, err.Error()
		}
		if v.Phase == "BackupFailed" {
			t.Fatalf("modpack phase BackupFailed: %s", v.Message)
		}
		if v.Phase != "Applied" {
			return false, "phase=" + v.Phase
		}
		if wantProject == "" {
			if v.Active != nil {
				return false, "active pack still set"
			}
			return true, ""
		}
		if v.Active == nil || v.Active.Project != wantProject || v.Active.Version != wantVersion || !v.Active.Pinned {
			return false, fmt.Sprintf("active=%+v", v.Active)
		}
		return true, ""
	})
}

// waitServerRunning waits for status.phase == Running and a Ready game pod.
// Using the pod too matters after a restart, where the GameServer phase can
// still read Running from before the pod went away.
func waitServerRunning(t *testing.T, gs string) {
	t.Helper()
	ctx := context.Background()
	envInstance.Eventually(t, modpackBootTimeout, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(modpackNamespace).
			Get(ctx, gs, metav1.GetOptions{})
		if err != nil {
			return false, "get gs: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase != "Running" {
			return false, "phase=" + phase
		}
		pod, err := envInstance.K8s.CoreV1().Pods(modpackNamespace).Get(ctx, gs+"-0", metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		if pod.DeletionTimestamp != nil {
			return false, "pod terminating"
		}
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				return true, ""
			}
		}
		return false, "pod not Ready"
	})
}

// gameEnv returns the env of every non-agent container of the game pod.
func gameEnv(t *testing.T, gs string) map[string]string {
	t.Helper()
	pod, err := envInstance.K8s.CoreV1().Pods(modpackNamespace).Get(context.Background(), gs+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get pod %s-0: %v", gs, err)
	}
	out := map[string]string{}
	for _, c := range pod.Spec.Containers {
		if c.Name == "agent" {
			continue
		}
		for _, e := range c.Env {
			out[e.Name] = e.Value
		}
	}
	return out
}

// eventuallyGameEnv polls until check returns "" for the pod env; the pod is
// replaced while the operator applies a change, so the first reads can still
// show the previous env.
func eventuallyGameEnv(t *testing.T, gs string, check func(env map[string]string) string) {
	t.Helper()
	envInstance.Eventually(t, modpackApplyTimeout, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods(modpackNamespace).Get(context.Background(), gs+"-0", metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		env := map[string]string{}
		for _, c := range pod.Spec.Containers {
			if c.Name == "agent" {
				continue
			}
			for _, e := range c.Env {
				env[e.Name] = e.Value
			}
		}
		if msg := check(env); msg != "" {
			return false, msg
		}
		return true, ""
	})
}

type fileEntry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
}

// listFiles lists a directory of the data volume through the agent.
func listFiles(t *testing.T, cli *APIClient, gs, dir string) []fileEntry {
	t.Helper()
	resp, body, err := cli.Get("/servers/" + gs + "/files/list?path=" + url.QueryEscape(dir))
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list %s expected 200, got %d body=%q", dir, resp.StatusCode, string(body))
	}
	var entries []fileEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		t.Fatalf("decode list %s: %v body=%q", dir, err, string(body))
	}
	return entries
}

// visibleEntries drops dotfiles: the removal wipe leaves its marker
// (.gameplane-mods-wiped-<generation>) inside the mods folder.
func visibleEntries(entries []fileEntry) []string {
	var names []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name, ".") {
			names = append(names, e.Name)
		}
	}
	return names
}

func writeDataFile(t *testing.T, cli *APIClient, gs, path, payload string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		cli.BaseURL+"/servers/"+gs+"/files/write?path="+url.QueryEscape(path), bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatalf("build write req: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Gameplane-CSRF", cli.CSRF)
	resp, err := cli.HTTP.Do(req)
	if err != nil {
		t.Fatalf("POST /files/write %s: %v", path, err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("/files/write %s expected 2xx, got %d body=%q", path, resp.StatusCode, string(body))
	}
}

func readDataFile(cli *APIClient, gs, path string) (string, error) {
	resp, body, err := cli.Get("/servers/" + gs + "/files/read?path=" + url.QueryEscape(path))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("read %s status %d body=%q", path, resp.StatusCode, string(body))
	}
	return string(body), nil
}

// clearModsFolder empties /mods by hand: switching to another pack does not
// empty it (OD-7 B, only Remove does), so a leg that moves between packs does
// it itself, as the spec tells operators to.
func clearModsFolder(t *testing.T, cli *APIClient, gs string) {
	t.Helper()
	for _, name := range visibleEntries(listFiles(t, cli, gs, "/mods")) {
		resp, body, err := cli.Delete("/servers/" + gs + "/files/delete?recursive=true&path=" + url.QueryEscape("/mods/"+name))
		if err != nil {
			t.Fatalf("delete /mods/%s: %v", name, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			t.Fatalf("delete /mods/%s expected 2xx, got %d body=%q", name, resp.StatusCode, string(body))
		}
	}
}

// findFileID returns the first "fileId" value found anywhere in the JSON of
// the image's .curseforge-manifest.json, as a string.
func findFileID(v any) (string, bool) {
	switch x := v.(type) {
	case map[string]any:
		if id, ok := x["fileId"]; ok {
			return jsonNumberString(id), true
		}
		for _, child := range x {
			if s, ok := findFileID(child); ok {
				return s, true
			}
		}
	case []any:
		for _, child := range x {
			if s, ok := findFileID(child); ok {
				return s, true
			}
		}
	}
	return "", false
}

func jsonNumberString(v any) string {
	if f, ok := v.(float64); ok {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprint(v)
}

// manifestFileID reads the installed CurseForge file id through the agent.
func manifestFileID(cli *APIClient, gs string) (string, error) {
	raw, err := readDataFile(cli, gs, "/.curseforge-manifest.json")
	if err != nil {
		return "", err
	}
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return "", fmt.Errorf("decode .curseforge-manifest.json: %w", err)
	}
	id, ok := findFileID(doc)
	if !ok {
		return "", fmt.Errorf(".curseforge-manifest.json has no fileId")
	}
	return id, nil
}

// bootCurseForgePack creates the template and server, installs the pinned
// CurseForge pack through the API before the first boot (so no throwaway
// vanilla boot is needed) and waits until the pack is Applied and the server
// Running. It returns the logged-in client (one admin login).
func bootCurseForgePack(t *testing.T, name string) (*APIClient, string) {
	t.Helper()
	tmpl := "e2e-" + name + "-tmpl"
	gs := "e2e-" + name + "-gs"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	t.Cleanup(cli.Close)

	applyModpackTemplate(t, tmpl, modpackImage, modpackGameVersion)
	applyModpackServer(t, gs, tmpl)
	t.Cleanup(func() { logGameContainerOnFailure(t, gs) })
	waitPVCBound(t, modpackNamespace, gs+"-data", 90*time.Second)

	installModpack(t, cli, gs, "curseforge", cfPackSlug, cfPackFileID, cfPackTitle, cfPackFileName)
	waitModpackApplied(t, cli, gs, cfPackSlug, cfPackFileID)
	waitServerRunning(t, gs)
	return cli, gs
}

// TestGameServer_Modpack_CurseForgeThenModrinth (T017, SC-004) installs a
// small CurseForge Fabric pack by slug and file id through the API, asserts
// Applied, Running and the pack env, then switches to a Modrinth pack and
// asserts no CF_* env remains.
func TestGameServer_Modpack_CurseForgeThenModrinth(t *testing.T) {
	skipUnlessGameInScope(t, "minecraft-java")
	requireModuleProviders(t)
	t.Parallel()

	cli, gs := bootCurseForgePack(t, "modpack-install")

	eventuallyGameEnv(t, gs, func(env map[string]string) string {
		want := map[string]string{"TYPE": "AUTO_CURSEFORGE", "CF_SLUG": cfPackSlug, "CF_FILE_ID": cfPackFileID}
		for k, v := range want {
			if env[k] != v {
				return fmt.Sprintf("env %s=%q want %q", k, env[k], v)
			}
		}
		for k := range env {
			if strings.HasPrefix(k, "MODRINTH_") {
				return "unexpected " + k
			}
		}
		return ""
	})
	if id, err := manifestFileID(cli, gs); err != nil || id != cfPackFileID {
		t.Fatalf("manifest fileId = %q, err=%v, want %s", id, err, cfPackFileID)
	}

	// Switch to Modrinth. The mods folder is emptied by hand first (OD-7 B).
	clearModsFolder(t, cli, gs)
	installModpack(t, cli, gs, "modrinth", mrPackSlug, mrPackVersion, mrPackTitle, mrPackName)
	waitModpackApplied(t, cli, gs, mrPackSlug, mrPackVersion)
	waitServerRunning(t, gs)

	eventuallyGameEnv(t, gs, func(env map[string]string) string {
		want := map[string]string{"TYPE": "MODRINTH", "MODRINTH_MODPACK": mrPackSlug, "MODRINTH_VERSION": mrPackVersion}
		for k, v := range want {
			if env[k] != v {
				return fmt.Sprintf("env %s=%q want %q", k, env[k], v)
			}
		}
		for k := range env {
			if strings.HasPrefix(k, "CF_") {
				return "stale " + k + " after switching to Modrinth"
			}
		}
		return ""
	})
}

// TestGameServer_Modpack_PinSurvivesRestarts (T031) proves a pinned CurseForge
// file is not upgraded by restarts: after three restarts the API, the pod env
// and the image's own .curseforge-manifest.json still say the pinned file id.
func TestGameServer_Modpack_PinSurvivesRestarts(t *testing.T) {
	skipUnlessGameInScope(t, "minecraft-java")
	requireModuleProviders(t)
	t.Parallel()

	cli, gs := bootCurseForgePack(t, "modpack-pin")
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		pod, err := envInstance.K8s.CoreV1().Pods(modpackNamespace).Get(ctx, gs+"-0", metav1.GetOptions{})
		if err != nil {
			t.Fatalf("restart %d: get pod: %v", i, err)
		}
		oldUID := pod.UID

		resp, body, err := cli.Post("/servers/"+gs+":restart", nil)
		if err != nil {
			t.Fatalf("restart %d: POST :restart: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("restart %d: expected 202, got %d body=%q", i, resp.StatusCode, string(body))
		}
		envInstance.Eventually(t, modpackBootTimeout, func() (bool, string) {
			p, err := envInstance.K8s.CoreV1().Pods(modpackNamespace).Get(ctx, gs+"-0", metav1.GetOptions{})
			if err != nil {
				return false, "get pod: " + err.Error()
			}
			if p.UID == oldUID {
				return false, "pod not replaced yet"
			}
			return true, ""
		})
		waitServerRunning(t, gs)

		if got := gameEnv(t, gs)["CF_FILE_ID"]; got != cfPackFileID {
			t.Fatalf("restart %d: pod CF_FILE_ID = %q, want %s", i, got, cfPackFileID)
		}
		v, err := getModpack(t, cli, gs)
		if err != nil {
			t.Fatalf("restart %d: %v", i, err)
		}
		if v.Active == nil || v.Active.Version != cfPackFileID || !v.Active.Pinned {
			t.Fatalf("restart %d: active = %+v, want pinned %s", i, v.Active, cfPackFileID)
		}
		envInstance.Eventually(t, time.Minute, func() (bool, string) {
			id, err := manifestFileID(cli, gs)
			if err != nil {
				return false, err.Error()
			}
			if id != cfPackFileID {
				t.Fatalf("restart %d: .curseforge-manifest.json fileId = %s, want %s", i, id, cfPackFileID)
			}
			return true, ""
		})
	}
}

// TestGameServer_Modpack_RemoveKeepsWorld (T040) removes the pack: the mods
// folder ends empty, a file planted in the world folder is unchanged and the
// server runs again as the plain Fabric loader.
func TestGameServer_Modpack_RemoveKeepsWorld(t *testing.T) {
	skipUnlessGameInScope(t, "minecraft-java")
	requireModuleProviders(t)
	t.Parallel()

	cli, gs := bootCurseForgePack(t, "modpack-remove")

	if len(visibleEntries(listFiles(t, cli, gs, "/mods"))) == 0 {
		t.Fatalf("mods folder is empty before removal; the pack installed no mods")
	}
	const worldFile = "/world/e2e-world-marker.txt"
	const worldPayload = "world data must survive removing the pack"
	writeDataFile(t, cli, gs, worldFile, worldPayload)

	resp, body, err := cli.Delete("/servers/" + gs + "/modpack?backupFirst=false")
	if err != nil {
		t.Fatalf("DELETE /modpack: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("DELETE /modpack expected 202, got %d body=%q", resp.StatusCode, string(body))
	}
	waitModpackApplied(t, cli, gs, "", "")
	waitServerRunning(t, gs)

	eventuallyGameEnv(t, gs, func(env map[string]string) string {
		if env["TYPE"] != "FABRIC" {
			return fmt.Sprintf("TYPE=%q, want FABRIC (plain loader)", env["TYPE"])
		}
		for _, k := range []string{"CF_SLUG", "CF_FILE_ID", "CF_DOWNLOADS_REPO"} {
			if _, ok := env[k]; ok {
				return "stale " + k
			}
		}
		return ""
	})

	if names := visibleEntries(listFiles(t, cli, gs, "/mods")); len(names) != 0 {
		t.Fatalf("mods folder after removal = %v, want empty", names)
	}
	got, err := readDataFile(cli, gs, worldFile)
	if err != nil || got != worldPayload {
		t.Fatalf("world marker after removal = %q, err=%v, want %q", got, err, worldPayload)
	}
}

// logGameContainerOnFailure prints the tail of the game container's current
// and previous logs when the test failed, so a crash-looping image (for
// example a CurseForge install error) shows its own error in the test output.
func logGameContainerOnFailure(t *testing.T, gs string) {
	t.Helper()
	if !t.Failed() {
		return
	}
	pods := envInstance.K8s.CoreV1().Pods(modpackNamespace)
	tail := int64(200)
	for _, previous := range []bool{true, false} {
		opts := &corev1.PodLogOptions{Container: "game", Previous: previous, TailLines: &tail}
		out, err := pods.GetLogs(gs+"-0", opts).DoRaw(context.Background())
		if err != nil {
			t.Logf("game container logs (previous=%t): %v", previous, err)
			continue
		}
		t.Logf("game container logs (previous=%t):\n%s", previous, out)
	}
}
