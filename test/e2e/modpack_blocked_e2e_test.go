//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestGameServer_Modpack_BlockedModManualDownload (T046, US4) installs a
// CurseForge pack that contains a mod its author does not allow to be
// downloaded automatically, and checks that the Modpacks tab data lists it
// (GET /servers/{name}/modpack missingFiles, read from MODS_NEED_DOWNLOAD.txt
// through the agent) and that uploading the file to downloads/ is accepted.
//
// It is in NO CI bucket (see the comment above unbucketed() in buckets.sh):
// the last step is a manual browser download, because CurseForge only serves
// a blocked file to a browser session. The test therefore pauses for the
// operator: set GAMEPLANE_E2E_CF_BLOCKED_SLUG and GAMEPLANE_E2E_CF_BLOCKED_FILE_ID
// to a pack whose files page lists a "needs manual download" mod, and
// GAMEPLANE_E2E_CF_BLOCKED_JAR to the path of the jar downloaded in a browser
// from the downloadPage this test logs. A hand run:
//
//	GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_CF_BLOCKED_SLUG=<slug> \
//	GAMEPLANE_E2E_CF_BLOCKED_FILE_ID=<id> GAMEPLANE_E2E_CF_BLOCKED_JAR=/path/mod.jar \
//	  make test-e2e-keep   # with -run TestGameServer_Modpack_BlockedModManualDownload
//
// Without the jar the test stops after asserting the list and logs the link.
func TestGameServer_Modpack_BlockedModManualDownload(t *testing.T) {
	skipUnlessGameInScope(t, "minecraft-java")
	requireModuleProviders(t)
	slug := os.Getenv("GAMEPLANE_E2E_CF_BLOCKED_SLUG")
	fileID := os.Getenv("GAMEPLANE_E2E_CF_BLOCKED_FILE_ID")
	if slug == "" || fileID == "" {
		t.Skip("set GAMEPLANE_E2E_CF_BLOCKED_SLUG and GAMEPLANE_E2E_CF_BLOCKED_FILE_ID to a CurseForge pack with a mod that needs a manual download")
	}
	t.Parallel()

	const name = "modpack-blocked"
	tmpl := "e2e-" + name + "-tmpl"
	gs := "e2e-" + name + "-gs"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyModpackTemplate(t, tmpl, modpackImage, modpackGameVersion)
	applyModpackServer(t, gs, tmpl)
	waitPVCBound(t, modpackNamespace, gs+"-data", 90*time.Second)
	requireAgentReady(t, modpackNamespace, gs)

	installModpack(t, cli, gs, "curseforge", slug, fileID, slug, fileID)
	waitModpackApplied(t, cli, gs, slug, fileID)

	// The image stops on the blocked file and writes MODS_NEED_DOWNLOAD.txt;
	// the agent sidecar keeps it readable while the game container is down.
	var missing []string
	envInstance.Eventually(t, modpackBootTimeout, func() (bool, string) {
		v, err := getModpack(t, cli, gs)
		if err != nil {
			return false, err.Error()
		}
		if len(v.MissingFiles) == 0 {
			return false, "missingFiles empty"
		}
		if v.DownloadsPath != "downloads/" {
			return false, "downloadsPath=" + v.DownloadsPath
		}
		for _, m := range v.MissingFiles {
			if !strings.HasPrefix(m.DownloadPage, "https://www.curseforge.com/") {
				return false, "downloadPage=" + m.DownloadPage
			}
			t.Logf("blocked mod %q file %q: download it in a browser from %s", m.Mod, m.Filename, m.DownloadPage)
			missing = append(missing, m.Filename)
		}
		return true, ""
	})

	jar := os.Getenv("GAMEPLANE_E2E_CF_BLOCKED_JAR")
	if jar == "" {
		t.Skip("blocked mods listed; set GAMEPLANE_E2E_CF_BLOCKED_JAR to the manually downloaded jar to continue")
	}
	data, err := os.ReadFile(jar)
	if err != nil {
		t.Fatalf("read %s: %v", jar, err)
	}
	// Upload into downloads/ (where CF_DOWNLOADS_REPO points), restart, and
	// expect the list to clear and the server to run.
	writeDataFile(t, cli, gs, "/downloads/"+missing[0], string(data))
	resp, body, err := cli.Post("/servers/"+gs+":restart", nil)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("restart expected 202, got %d body=%q", resp.StatusCode, string(body))
	}
	waitServerRunning(t, gs)
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		v, err := getModpack(t, cli, gs)
		if err != nil {
			return false, err.Error()
		}
		if len(v.MissingFiles) != 0 {
			return false, "missingFiles still listed"
		}
		return true, ""
	})
}
