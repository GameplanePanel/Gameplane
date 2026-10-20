//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_TModLoaderBot_Query boots a tModLoader dedicated server
// and performs an A2S query to verify QUERY depth readiness.
//
// tModLoader is in the HEAVY game set due to SteamCMD and .NET runtime size.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=tmodloader make test-e2e-keep
func TestGameServer_TModLoaderBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "tmodloader")
	t.Parallel()

	runGameBotTest(t, gameBotSpec{
		Game:        "tmodloader",
		Template:    fmt.Sprintf("e2e-tmodloader-bot-%d", time.Now().UnixNano()),
		DisplayName: "E2E tModLoader",
		Image:       "passivelemon/terraria-docker:tmodloader-latest@sha256:3f2d8703421159f1037084bd2c0901a3a63b85a00801cf36f6f928e8b666b44e",
		Env: map[string]string{
			"HOME":      "/data",
			"WORLDNAME": "e2eworld",
		},
		Ports: []gamePort{
			{Name: "game", Port: 7777, Protocol: "TCP"},
		},
		StorageSize: "5Gi",
		MountPath:   "/data",
		Resources: gameResources{
			ReqCPU: "1", ReqMem: "2Gi",
			LimCPU: "2", LimMem: "4Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     7777,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol": "none",
		},
	})
}
