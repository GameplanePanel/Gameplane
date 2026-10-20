//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_FiveMBot_Query boots a FiveM FXServer instance
// and queries the dynamic info.json HTTP endpoint to verify QUERY depth readiness.
//
// FiveM is in the HEAVY game set due to container size and embedded database.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=fivem make test-e2e-keep
func TestGameServer_FiveMBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "fivem")
	t.Parallel()

	runGameBotTest(t, gameBotSpec{
		Game:        "fivem",
		Template:    fmt.Sprintf("e2e-fivem-bot-%d", time.Now().UnixNano()),
		DisplayName: "E2E FiveM",
		Image:       "ghcr.io/gameplanepanel/gameplane/fivem:latest@sha256:23d65f59793e5f207c4c4ee5c320bca5aa56003fd95be259a887f4e58f3d77b1",
		Env: map[string]string{
			"CFX_LICENSE_KEY": "test-cfx-license-key",
			"SV_HOSTNAME":     "Gameplane E2E FiveM",
		},
		Ports: []gamePort{
			{Name: "game", Port: 30120, Protocol: "UDP"},
			{Name: "game-tcp", Port: 30120, Protocol: "TCP"},
			{Name: "txadmin", Port: 40120, Protocol: "TCP"},
		},
		StorageSize: "10Gi",
		MountPath:   "/server-data",
		Resources: gameResources{
			ReqCPU: "1", ReqMem: "2Gi",
			LimCPU: "4", LimMem: "4Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     30120,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol": "rest",
			"port":     int64(40120),
		},
	})
}
