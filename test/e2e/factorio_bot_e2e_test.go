//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_FactorioBot_Query boots a Factorio server through the operator,
// waits for it to reach Running (startup includes world generation), then runs a
// probe inside the cluster to verify the RCON port (TCP 27015) is open and
// accepting connections.
//
// Factorio's multiplayer protocol is not publicly documented (only high-level
// conceptual descriptions in FFF #149/#147). The test does NOT attempt a full
// join (which would require credentials and the in-game UI). Instead, it asserts
// that the RCON port (TCP 27015) is accepting connections, which proves the
// server is listening and responsive. This establishes QUERY depth.
//
// The Factorio headless server cannot be joined in CI without the in-game
// multiplayer UI, so the probe stops at QUERY—verifying the server is running
// without claiming we can join as a player.
//
// Factorio boots quickly (no steamcmd, small initial world generation) but
// early images may vary. It is in fastGameSet (gamebot_helpers_e2e_test.go),
// so it runs by default with GAMEPLANE_E2E_GAME_BOT=1 alone (GAMEPLANE_E2E_GAMES
// need not be "all"). It is still excluded from CI's bot-fast bucket and
// bucketed under bot-heavy for CI disk budget reasons — see buckets.sh.
// Deliberately NOT t.Parallel(): two real game servers booting concurrently
// OOM-starves a single kind node.
func TestGameServer_FactorioBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "factorio")

	runGameBotTest(t, gameBotSpec{
		Game:        "factorio",
		Template:    "e2e-factorio",
		DisplayName: "E2E Factorio",
		// Pin to stable channel from the module's template.yaml (2.0.x).
		// The floating :stable tag tracks releases but not pre-releases;
		// pinning the digest ensures hermetic tests and allows version-specific
		// protocol handling if needed later.
		// Digest corresponds to factoriotools/factorio:stable as of 2026-07-25.
		Image: "factoriotools/factorio:stable@sha256:7052b3cca8ca7790f99f4058617d5c8089df544de736b1baa23f2c5f58fb7f48",
		Env: map[string]string{
			"LOAD_LATEST_SAVE": "true",
		},
		Ports: []gamePort{
			{Name: "game", Port: 34197, Protocol: "UDP"},
			{Name: "rcon", Port: 27015, Protocol: "TCP"},
		},
		StorageSize: "1Gi",
		MountPath:   "/factorio",
		Resources: gameResources{
			ReqCPU: "250m",
			ReqMem: "512Mi",
			LimCPU: "1",
			LimMem: "2Gi",
		},
		ReadyTimeout:  8 * time.Minute,
		ProbePort:     34197,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol":     "source",
			"port":         int64(27015),
			"passwordFile": "config/rconpw",
		},
		ConsoleMode: "pty",
		Probes: map[string]any{
			"readiness": map[string]any{
				"tcpSocket":           map[string]any{"port": "rcon"},
				"initialDelaySeconds": int64(10),
				"periodSeconds":       int64(10),
				"failureThreshold":    int64(30),
			},
		},
	})
}
