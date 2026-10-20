//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_Left4Dead2Bot_Query boots a Left 4 Dead 2 dedicated server
// and connects via A2S query to verify QUERY depth readiness.
//
// L4D2 is in the HEAVY game set due to SteamCMD runtime download requirements.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=left-4-dead-2 make test-e2e-keep
func TestGameServer_Left4Dead2Bot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "left-4-dead-2")
	t.Parallel()

	runGameBotTest(t, gameBotSpec{
		Game:        "left-4-dead-2",
		Template:    fmt.Sprintf("e2e-l4d2-bot-%d", time.Now().UnixNano()),
		DisplayName: "E2E Left 4 Dead 2",
		Image:       "cm2network/l4d2:latest@sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Env: map[string]string{
			"SERVER_HOSTNAME": "Gameplane E2E L4D2",
			"RCON_PASSWORD":   "secret-rcon-password",
		},
		Ports: []gamePort{
			{Name: "game", Port: 27015, Protocol: "UDP"},
			{Name: "rcon", Port: 27015, Protocol: "TCP"},
		},
		StorageSize: "15Gi",
		MountPath:   "/home/steam/l4d2-dedicated",
		Resources: gameResources{
			ReqCPU: "1", ReqMem: "1Gi",
			LimCPU: "2", LimMem: "2Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     27015,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol": "source",
			"port":     int64(27015),
		},
	})
}
