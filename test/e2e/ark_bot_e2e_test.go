//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_ArkBot_Query boots a REAL ARK: Survival Ascended server
// (mschnitzer/asa-linux-server) through the operator, waits for it to reach Running,
// then runs a minimal bot inside the cluster to measure the join depth.
//
// ARK is part of the heavy set because its image is ~20GB and requires SteamCMD
// downloads on first boot. It is never run in CI; only on maintainer hand-run
// with GAMEPLANE_E2E_GAMES=all.
//
// The test is deliberately NOT t.Parallel(): concurrent real-game boots OOM-starve
// a single kind node.
//
// DEPTH EXPECTATION: This test expects QUERY depth, which is proven by successful
// UDP connectivity to the game port (7777). ARK's join protocol requires EOS/Steam
// identity that CI cannot mint, so the probe cannot progress beyond connectivity
// verification. No join attempt is made.
func TestGameServer_ArkBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "ark-survival-ascended")

	runGameBotTest(t, gameBotSpec{
		Game:        "ark-survival-ascended",
		Template:    "e2e-ark-survival-ascended",
		DisplayName: "E2E ARK: Survival Ascended",
		// Pinned to a specific version tag rather than floating :latest.
		Image: "mschnitzer/asa-linux-server:1.5.1@sha256:6a62f7c6eb77ba4b4b79ef6c2981a0bd9fab287e744d9797e66efc762ebb811e",
		Env: map[string]string{
			// Enable debug logging to monitor server startup.
			"ENABLE_DEBUG": "0",
		},
		Ports: []gamePort{
			{Name: "game", Port: 7777, Protocol: "UDP"},
			{Name: "peer", Port: 7778, Protocol: "UDP"},
			{Name: "rcon", Port: 27020, Protocol: "TCP"},
		},
		StorageSize: "30Gi",
		MountPath:   "/home/gameserver",
		Resources: gameResources{
			ReqCPU: "2",
			ReqMem: "10Gi",
			LimCPU: "6",
			LimMem: "20Gi",
		},
		// ARK boots slowly: image pull (~20GB), SteamCMD extraction, world generation.
		// Budget 20 minutes for startup.
		ReadyTimeout: 20 * time.Minute,
		// Probe on the game port (7777 UDP). The template deliberately omits
		// readiness probes because ARK exposes no probeable TCP port.
		ProbePort: 7777,
		// Allow the probe 6 minutes to verify UDP connectivity and handle retries.
		ProbeDeadline: 6 * time.Minute,
		// ExpectDepth is QUERY because that is what UDP connectivity proves.
		// ARK's join protocol requires EOS/Steam credentials that CI cannot mint.
		ExpectDepth: joindepth.QUERY,
		// No RCON in this test (would require the admin to manually set it up).
		RCON: map[string]any{"protocol": "source"},
		// No probes configured; the template deliberately omits them.
		Probes: nil,
	})
}
