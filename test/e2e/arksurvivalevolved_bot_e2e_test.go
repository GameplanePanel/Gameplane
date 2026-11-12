//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_ArkSurvivalEvolvedBot_Query boots an ARK: Survival Evolved dedicated server
// and connects via A2S query to verify QUERY depth readiness.
//
// ASE is in the HEAVY game set due to massive SteamCMD download and >30Gi storage requirements.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<context> GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=ark-survival-evolved make test-e2e-keep
func TestGameServer_ArkSurvivalEvolvedBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "ark-survival-evolved")
	t.Parallel()

	runGameBotTest(t, gameBotSpec{
		Game:        "ark-survival-evolved",
		Template:    fmt.Sprintf("e2e-ase-bot-%d", time.Now().UnixNano()),
		DisplayName: "E2E ARK: Survival Evolved",
		Image:       "hermsi/ark-server:latest@sha256:ce291990613e76f7019be31b96fe247188ce4957724c913c0e30097042c44bf5",
		Env: map[string]string{
			"SESSION_NAME":    "Gameplane E2E ASE",
			"SERVER_PASSWORD": "",
		},
		Ports: []gamePort{
			{Name: "game", Port: 7777, Protocol: "UDP"},
			{Name: "raw", Port: 7778, Protocol: "UDP"},
			{Name: "query", Port: 27015, Protocol: "UDP"},
			{Name: "rcon", Port: 27020, Protocol: "TCP"},
		},
		StorageSize: "30Gi",
		MountPath:   "/app",
		Resources: gameResources{
			ReqCPU: "2", ReqMem: "8Gi",
			LimCPU: "4", LimMem: "16Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     27015,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		// The operator injects its managed RCON password as ADMIN_PASSWORD,
		// which hermsi/ark-server writes as ServerAdminPassword. Path A below
		// proves the agent authenticates with it.
		RCON: map[string]any{
			"protocol":    "source",
			"port":        int64(27020),
			"passwordEnv": "ADMIN_PASSWORD",
		},
		ConsoleMode: "rcon",
		Actions: []any{
			map[string]any{
				"id":          "list-players",
				"displayName": "List players",
				"command":     "ListPlayers",
			},
		},
		// ARK answers ListPlayers on an empty server with "No Players
		// Connected"; a broadcast (ServerChat) reply would be empty.
		Control: pathAControl{
			Mode:      "rcon",
			Action:    "list-players",
			ExpectRaw: "No Players Connected",
		},
	})
}
