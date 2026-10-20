//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_TheIsleBot_Query boots a The Isle dedicated server
// and performs an A2S_INFO query to verify QUERY depth readiness.
//
// The Isle is in the HEAVY game set due to UE4 engine overhead and SteamCMD download.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=the-isle make test-e2e-keep
func TestGameServer_TheIsleBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "the-isle")
	t.Parallel()

	runGameBotTest(t, gameBotSpec{
		Game:        "the-isle",
		Template:    fmt.Sprintf("e2e-the-isle-bot-%d", time.Now().UnixNano()),
		DisplayName: "E2E The Isle",
		Image:       "ghcr.io/gameplanepanel/gameplane/the-isle:latest@sha256:229ec85c92f8f722beb0b7c735cfdb5dbe40e6782d537a6c56b32c6baa57d5a8",
		Env: map[string]string{
			"SERVER_NAME":   "Gameplane E2E The Isle",
			"RCON_PASSWORD": "secret-rcon-password",
		},
		Ports: []gamePort{
			{Name: "game", Port: 7777, Protocol: "UDP"},
			{Name: "query", Port: 7778, Protocol: "UDP"},
			{Name: "rcon", Port: 8888, Protocol: "TCP"},
		},
		StorageSize: "20Gi",
		MountPath:   "/data",
		Resources: gameResources{
			ReqCPU: "2", ReqMem: "4Gi",
			LimCPU: "4", LimMem: "8Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     7778,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol": "source",
			"port":     int64(8888),
		},
	})
}
