//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_SatisfactoryBot_Query boots a REAL Satisfactory server
// (wolveix/satisfactory-server — the same image the shipped satisfactory
// module uses) through the operator, waits for it to reach Running phase
// (which includes a multi-GB steamcmd download on first boot), then runs
// a minimal Satisfactory HTTPS API client inside the cluster to verify it
// reaches QUERY depth (unauthenticated API call succeeds).
//
// Satisfactory is a heavy game and deliberately never runs in CI. It
// requires several gigabytes of disk and network bandwidth for the steamcmd
// download, which is not practical on GitHub-hosted runners. This test is
// hand-run only via:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<kubelab|prod> \
//	  GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=all make test-e2e-keep
//
// Admin password and authenticated functions (RunCommand, PasswordLogin) are
// unreachable in CI because they require an in-game server claim, which is a
// manual step. The probe measures only the unauthenticated QueryServerState
// API, which proves the endpoint is alive. See
// test/e2e/internal/satisfactory/spec.md for details.
//
// Like the Terraria test, it is deliberately NOT t.Parallel(): a real
// Satisfactory server (with steamcmd download and game initialization) is
// heavy on resources and should not boot concurrently with other games.
func TestGameServer_SatisfactoryBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "satisfactory")

	runGameBotTest(t, gameBotSpec{
		Game:        "satisfactory",
		Template:    "e2e-satisfactory",
		DisplayName: "E2E Satisfactory",
		// Pinned to an explicit tag because the :latest tag may drift,
		// and we want hermetic, reproducible test runs. Verify the tag exists
		// before updating.
		Image: "wolveix/satisfactory-server:latest@sha256:e103700ae6ae4c50f19dac80eadb2a805c5b885e179ae2a40850e967bf189efd",
		Env: map[string]string{
			// Do not auto-update; use the image's built-in game version for
			// hermetic testing. The first boot still downloads/installs; we're
			// just pinning the branch and version.
			"SKIPUPDATE": "false",
			// Run as non-root user inside the image.
			"PUID": "1000",
			"PGID": "1000",
			// Install the public release (not experimental).
			"STEAMBETA": "false",
		},
		Ports: []gamePort{
			{Name: "game", Port: 7777, Protocol: "UDP"},
			{Name: "game-tcp", Port: 7777, Protocol: "TCP"},
			{Name: "messaging", Port: 8888, Protocol: "TCP"},
		},
		// Game files, saves, and logs all live on the data volume.
		StorageSize: "25Gi",
		MountPath:   "/config",
		Resources: gameResources{
			// Satisfactory needs real CPU and memory; these are the values from
			// the module template.
			ReqCPU: "2",
			ReqMem: "6Gi",
			LimCPU: "6",
			LimMem: "16Gi",
		},
		// Satisfactory's first boot includes a multi-GB steamcmd download and
		// game initialization. Increase the timeout to allow this.
		ReadyTimeout: 25 * time.Minute,
		// The probe dials the TCP API port.
		ProbePort: 7777,
		// Retry for up to 4 minutes after the pod reaches Running.
		ProbeDeadline: 4 * time.Minute,
		// Measure QUERY depth (unauthenticated API call succeeds).
		ExpectDepth: joindepth.QUERY,
		// No RCON configured for this test; the probe speaks the HTTPS API
		// directly (not via the agent's RCON bridge).
		// Note: the template itself declares rcon.protocol: satisfactory for
		// Console tab use, but the probe tests the raw API.
		RCON: map[string]any{"protocol": "none"},
		// No stdin console; no special console mode needed for the probe.
		ConsoleMode: "pty",
		// No readiness probe configured; the probe runs once the pod reaches
		// Running phase.
		Probes: nil,
	})
}
