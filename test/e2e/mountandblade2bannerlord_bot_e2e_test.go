//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_MountAndBlade2BannerlordBot_Query boots a Bannerlord dedicated server
// and performs an A2S query to verify QUERY depth readiness.
//
// Bannerlord is in the HEAVY game set due to SteamCMD runtime download and game size.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=mount-and-blade-2-bannerlord make test-e2e-keep
func TestGameServer_MountAndBlade2BannerlordBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "mount-and-blade-2-bannerlord")
	t.Parallel()

	runGameBotTest(t, gameBotSpec{
		Game:        "mount-and-blade-2-bannerlord",
		Template:    fmt.Sprintf("e2e-bannerlord-bot-%d", time.Now().UnixNano()),
		DisplayName: "E2E Mount & Blade II: Bannerlord",
		Image:       "ghcr.io/gameplanepanel/gameplane/mount-and-blade-2-bannerlord:latest@sha256:ac4796548429fce3bf1990839b75002f08ee8d6c11260bc4a26329f5a7d93717",
		Env: map[string]string{
			"SERVER_TOKEN": "test-bannerlord-token",
			"SERVER_NAME":  "Gameplane E2E Bannerlord",
		},
		Ports: []gamePort{
			{Name: "game", Port: 7210, Protocol: "UDP"},
			{Name: "query", Port: 7211, Protocol: "UDP"},
		},
		StorageSize: "15Gi",
		MountPath:   "/data",
		Resources: gameResources{
			ReqCPU: "2", ReqMem: "4Gi",
			LimCPU: "4", LimMem: "8Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     7211,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol": "none",
		},
	})
}
