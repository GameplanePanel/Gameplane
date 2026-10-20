//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_FarmingSimulator25Bot_Query boots a Farming Simulator 25 dedicated server
// and performs an HTTP query against the dedicated server web API to verify QUERY depth readiness.
//
// FS25 is in the HEAVY game set due to Wine/Xvfb overhead and storage requirements.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=farming-simulator-25 make test-e2e-keep
func TestGameServer_FarmingSimulator25Bot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "farming-simulator-25")
	t.Parallel()

	runGameBotTest(t, gameBotSpec{
		Game:        "farming-simulator-25",
		Template:    fmt.Sprintf("e2e-fs25-bot-%d", time.Now().UnixNano()),
		DisplayName: "E2E Farming Simulator 25",
		Image:       "ghcr.io/gameplanepanel/gameplane/farming-simulator-25:latest@sha256:a19a825612a2a77b5a27224bcd4c3362e604ec4b962d4f77e1cbb22c71f2773f",
		Env: map[string]string{
			"ADMIN_PASSWORD": "adminpassword",
			"SERVER_NAME":    "Gameplane E2E FS25",
		},
		Ports: []gamePort{
			{Name: "game", Port: 10823, Protocol: "UDP"},
			{Name: "web", Port: 8080, Protocol: "TCP"},
		},
		StorageSize: "20Gi",
		MountPath:   "/data/My Games/FarmingSimulator2025",
		Resources: gameResources{
			ReqCPU: "2", ReqMem: "4Gi",
			LimCPU: "4", LimMem: "8Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     8080,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol": "rest",
			"port":     int64(8080),
		},
	})
}
