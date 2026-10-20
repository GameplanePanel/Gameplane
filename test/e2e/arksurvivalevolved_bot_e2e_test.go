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
		Image:       "ghcr.io/gameplanepanel/gameplane/ark-survival-evolved:latest@sha256:2a4298f8055dbff4107c49c961cace1f2bed42809e6b93fb4924371ae4484e58",
		Env: map[string]string{
			"SESSION_NAME":  "Gameplane E2E ASE",
			"RCON_PASSWORD": "secret-rcon-password",
		},
		Ports: []gamePort{
			{Name: "game", Port: 7777, Protocol: "UDP"},
			{Name: "query", Port: 27015, Protocol: "UDP"},
			{Name: "rcon", Port: 27020, Protocol: "TCP"},
		},
		StorageSize: "30Gi",
		MountPath:   "/data",
		Resources: gameResources{
			ReqCPU: "2", ReqMem: "8Gi",
			LimCPU: "4", LimMem: "16Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     27015,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON: map[string]any{
			"protocol": "source",
			"port":     int64(27020),
		},
	})
}
