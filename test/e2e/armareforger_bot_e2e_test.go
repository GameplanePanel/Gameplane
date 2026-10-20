//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_ArmaReforgerBot_Query boots an Arma Reforger dedicated server
// and performs an A2S_INFO query to verify QUERY depth readiness.
//
// Arma Reforger is in the HEAVY game set due to Enfusion engine size and SteamCMD download.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=arma-reforger make test-e2e-keep
func TestGameServer_ArmaReforgerBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "arma-reforger")
	t.Parallel()

	runGameBotTest(t, gameBotSpec{
		Game:        "arma-reforger",
		Template:    fmt.Sprintf("e2e-arma-reforger-bot-%d", time.Now().UnixNano()),
		DisplayName: "E2E Arma Reforger",
		Image:       "ghcr.io/acemod/arma-reforger:latest@sha256:beb50bcaebbc3b3124363ed1c0a5729b514493d72b3cadd52a204ade6105fecd",
		Env: map[string]string{
			"GAME_NAME":         "Gameplane E2E Arma Reforger",
			"ARMA_PROFILE":      "/reforger/profile",
			"ARMA_WORKSHOP_DIR": "/reforger/workshop",
		},
		Ports: []gamePort{
			{Name: "game", Port: 2001, Protocol: "UDP"},
			{Name: "query", Port: 17777, Protocol: "UDP"},
		},
		StorageSize: "25Gi",
		MountPath:   "/reforger",
		Resources: gameResources{
			ReqCPU: "2", ReqMem: "8Gi",
			LimCPU: "6", LimMem: "16Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     17777,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol": "none",
		},
	})
}
