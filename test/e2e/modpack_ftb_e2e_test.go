//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"
)

// FTB pack for T052, from the keyless FTB API (https://api.feed-the-beast.com):
// "FTB Unstable 1.20: Fabric", pack id 109, version 1.3.0 = version id 6571
// (Minecraft 1.20.1, Fabric, 157 files, about 169 MB). Chosen by listing
// /v1/modpacks/public/modpack/all, summing each newest release's file sizes
// from /modpack/{id}/{version}, and keeping the smallest pack on a Minecraft
// version a current itzg image can still run (the smaller ones target 1.4.7).
const (
	ftbPackID      = "109"
	ftbPackVersion = "6571"
	ftbPackTitle   = "FTB Unstable 1.20: Fabric"
	ftbPackName    = "1.3.0"
	ftbGameVersion = "1.20.1"
	// Java 21 image of the minecraft-java module (runs Minecraft 1.20.1).
	ftbImage = "itzg/minecraft-server:java21@sha256:f71555873bdaa6f4b663563de58678cca334bc51bdfbe548f7958ee9f516cf4a"
)

// TestGameServer_Modpack_FTBInstall (T052, US5) installs a small FTB pack
// through the keyless FTB registry route and asserts Applied, Running and the
// FTBA env (TYPE, FTB_MODPACK_ID, FTB_MODPACK_VERSION_ID).
//
// It is in NO CI bucket (see the comment above unbucketed() in buckets.sh):
// even the smallest modern FTB pack downloads about 170 MB of files and runs
// a heavy first boot, which exceeds the CI runner budget. Hand run:
//
//	GAMEPLANE_E2E_GAME_BOT=1 make test-e2e-keep   # with -run TestGameServer_Modpack_FTBInstall
func TestGameServer_Modpack_FTBInstall(t *testing.T) {
	skipUnlessGameInScope(t, "minecraft-java")
	requireModuleProviders(t)
	t.Parallel()

	const name = "modpack-ftb"
	tmpl := "e2e-" + name + "-tmpl"
	gs := "e2e-" + name + "-gs"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyModpackTemplate(t, tmpl, ftbImage, ftbGameVersion)
	applyModpackServer(t, gs, tmpl)
	waitPVCBound(t, modpackNamespace, gs+"-data", 90*time.Second)

	installModpack(t, cli, gs, "ftb", ftbPackID, ftbPackVersion, ftbPackTitle, ftbPackName)
	waitModpackApplied(t, cli, gs, ftbPackID, ftbPackVersion)
	waitServerRunning(t, gs)

	eventuallyGameEnv(t, gs, func(env map[string]string) string {
		want := map[string]string{
			"TYPE": "FTBA", "FTB_MODPACK_ID": ftbPackID, "FTB_MODPACK_VERSION_ID": ftbPackVersion,
		}
		for k, v := range want {
			if env[k] != v {
				return fmt.Sprintf("env %s=%q want %q", k, env[k], v)
			}
		}
		return ""
	})
}
