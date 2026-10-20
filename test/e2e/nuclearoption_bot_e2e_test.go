//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_NuclearOptionBot_Query boots a Nuclear Option dedicated server
// and sends an A2S_INFO query to its query port (UDP 7778) to verify QUERY depth readiness.
// The join handshake is not probed: UDP 7777 is not bound and joins route through
// Steam game-server networking, so a JOINED test waits on a packet capture (spec 002 FR-004).
//
// Nuclear Option is in the HEAVY game set due to its 8-16 GiB memory footprint and SteamCMD download.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=nuclear-option make test-e2e-keep
func TestGameServer_NuclearOptionBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "nuclear-option")
	t.Parallel()

	runGameBotTest(t, gameBotSpec{
		Game:        "nuclear-option",
		Template:    fmt.Sprintf("e2e-nuclear-option-bot-%d", time.Now().UnixNano()),
		DisplayName: "E2E Nuclear Option",
		Image:       "ghcr.io/gameplanepanel/gameplane/nuclear-option@sha256:acaeb3b7f0ab55bf6efe678b74745ed1a437ff97bf6d52cd26844efd28c13ce6",
		Ports: []gamePort{
			{Name: "query", Port: 7778, Protocol: "UDP"},
		},
		StorageSize: "2Gi",
		MountPath:   "/data",
		Resources: gameResources{
			ReqCPU: "2", ReqMem: "8Gi",
			LimCPU: "4", LimMem: "16Gi",
		},
		ReadyTimeout:  20 * time.Minute,
		ProbePort:     7778,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol":       "nuclearoption",
			"port":           int64(7779),
			"authentication": false,
		},
	})
}
