//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_ValheimBot_Query boots a REAL Valheim server
// (lloesche/valheim-server — the same image the shipped valheim module uses)
// through the operator, waits for it to download the game binary (SteamCMD),
// start the dedicated server, and reach Running, then runs a minimal HTTP
// status probe inside the cluster to verify it reaches QUERY depth (server
// responds to the documented status.json health endpoint).
//
// Valheim is in the heavy set: the lloesche image executes SteamCMD on first
// boot, which downloads a multi-GB game binary. This test is deliberately
// excluded from CI (see test/e2e/buckets.sh). It is only run when a
// maintainer hand-executes:
//
//	GAMEPLANE_E2E_GAMES=all GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_REUSE_CLUSTER=1 make test-e2e-keep
//
// Deliberately NOT t.Parallel(): a real Valheim server boot consumes 2–6GB
// memory; two servers concurrently OOM-starve a single kind node.
//
// Depth is QUERY because Valheim's UDP game protocol (port 2456+) is not
// publicly documented, so a full join is impossible in CI. The HTTP status
// endpoint is the documented health protocol; successfully fetching
// /status.json proves the server is alive and ready.
func TestGameServer_ValheimBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "valheim")

	runGameBotTest(t, gameBotSpec{
		Game:        "valheim",
		Template:    "e2e-valheim",
		DisplayName: "E2E Valheim",
		// Pinned to a known-good version tag. The default 'latest' floats
		// and has broken this suite before (SteamCMD download failures,
		// Proton version mismatches). Pin to a stable digest for hermetic CI.
		Image: "lloesche/valheim-server:latest@sha256:20fde516ce311e6084f82f295c9eb6934af57b357c657937a04f62bdf5946149",
		Env: map[string]string{
			"SERVER_NAME": "Gameplane Valheim",
			"SERVER_PASS": "gameplane-e2e-pass",
			"WORLD_NAME":  "e2e",
			"BEPINEX":     "false",
			"BACKUPS":     "false",
		},
		Ports: []gamePort{
			{Name: "game", Port: 2456, Protocol: "UDP"},
			{Name: "game2", Port: 2457, Protocol: "UDP"},
			{Name: "game3", Port: 2458, Protocol: "UDP"},
			{Name: "status", Port: 80, Protocol: "TCP"},
		},
		StorageSize: "5Gi",
		MountPath:   "/config",
		Resources: gameResources{
			ReqCPU: "500m",
			ReqMem: "2Gi",
			LimCPU: "2",
			LimMem: "6Gi",
		},
		// Valheim takes 2–5 minutes to boot (SteamCMD download + world init).
		// Use a generous timeout to avoid flakes on slow runners.
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     80,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON:          map[string]any{"protocol": "none"},
		ConsoleMode:   "pty",
		Probes: map[string]any{
			"readiness": map[string]any{
				"httpGet": map[string]any{
					"path": "/status.json",
					"port": "status",
				},
				"initialDelaySeconds": int64(60),
				"periodSeconds":       int64(15),
				"failureThreshold":    int64(10),
			},
		},
	})
}
