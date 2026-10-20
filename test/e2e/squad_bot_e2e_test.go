//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_SquadBot_Query boots a Squad dedicated server
// and performs an A2S_INFO query against the query port to verify QUERY depth readiness.
//
// Squad is in the HEAVY game set due to SteamCMD download and >35Gi storage requirements.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=squad make test-e2e-keep
func TestGameServer_SquadBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "squad")
	t.Parallel()

	runGameBotTest(t, gameBotSpec{
		Game:        "squad",
		Template:    fmt.Sprintf("e2e-squad-bot-%d", time.Now().UnixNano()),
		DisplayName: "E2E Squad",
		Image:       "ghcr.io/gameplanepanel/gameplane/squad:latest@sha256:c96561c975b1cd049668da930582181de7ac8d74e5d8d2251080423ec2ee2748",
		Env: map[string]string{
			"SERVER_NAME":   "Gameplane E2E Squad",
			"RCON_PASSWORD": "secret-rcon-password",
		},
		Ports: []gamePort{
			{Name: "game", Port: 7787, Protocol: "UDP"},
			{Name: "query", Port: 27165, Protocol: "UDP"},
			{Name: "rcon", Port: 21114, Protocol: "TCP"},
		},
		StorageSize: "40Gi",
		MountPath:   "/data",
		Resources: gameResources{
			ReqCPU: "2", ReqMem: "8Gi",
			LimCPU: "6", LimMem: "16Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     27165,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol": "source",
			"port":     int64(21114),
		},
	})
}
