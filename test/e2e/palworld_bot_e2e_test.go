//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_PalworldBot_Query boots a REAL Palworld server
// (thijsvanloef/palworld-server-docker) through the operator, waits for it to reach Running,
// then runs a minimal protocol bot inside the cluster to measure the join depth.
//
// Palworld is part of the heavy set because:
// - First boot downloads multi-GB game files via steamcmd (10+ minutes).
// - Requires 20Gi storage.
// - Never runs in CI; maintainer-only via GAMEPLANE_E2E_GAMES=all.
//
// The test is deliberately NOT t.Parallel(): concurrent real-game boots
// exhaust node disk and memory on a single kind node.
//
// DEPTH EXPECTATION: This test expects QUERY depth, which is proven by A2S_INFO
// succeeding on the query port (27015). The game protocol (Unreal Engine) is
// undocumented and not publicly joinable without Steam/Epic auth; the REST admin
// API requires operator-generated secrets. A2S is the only honest, citable depth.
func TestGameServer_PalworldBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "palworld")

	runGameBotTest(t, gameBotSpec{
		Game:        "palworld",
		Template:    "e2e-palworld",
		DisplayName: "E2E Palworld",
		// Pinned to a specific version tag for CI hermetic-ness.
		// The shipped template pins to digest (latest@sha256:...), but we use an
		// explicit version tag. This tag is verified to exist in thijsvanloef/palworld-server-docker
		// as of 2026-07-25.
		Image: "thijsvanloef/palworld-server-docker:v2.4.1",
		Env: map[string]string{
			// REST API enabled for readiness probes and future admin access.
			"REST_API_ENABLED": "true",
			"REST_API_PORT":    "8212",
			// Game port (Unreal UDP).
			"PORT": "8211",
			// Query port (Steam A2S).
			"QUERY_PORT": "27015",
			// Install/update server on boot. First boot downloads several GB (~15-30 min).
			"UPDATE_ON_BOOT": "true",
			// Server identity.
			"SERVER_NAME":        "Gameplane E2E Palworld",
			"SERVER_DESCRIPTION": "E2E test server",
			// Max players for a small test cluster.
			"PLAYERS": "4",
			// Disable multithreading in the test to save CPU.
			"MULTITHREADING": "false",
		},
		Ports: []gamePort{
			{Name: "game", Port: 8211, Protocol: "UDP"},
			{Name: "query", Port: 27015, Protocol: "UDP"},
			{Name: "rest-api", Port: 8212, Protocol: "TCP"},
		},
		StorageSize: "20Gi",
		MountPath:   "/palworld",
		Resources: gameResources{
			ReqCPU: "1",
			ReqMem: "4Gi",
			LimCPU: "4",
			LimMem: "12Gi",
		},
		// First boot requires steamcmd download: budget 30 minutes.
		// Subsequent boots are much faster (resume from cache).
		ReadyTimeout:  30 * time.Minute,
		ProbePort:     27015, // Query port for A2S
		ProbeDeadline: 4 * time.Minute,
		// ExpectDepth is QUERY because that is what A2S_INFO proves.
		ExpectDepth: joindepth.QUERY,
		Probes: map[string]any{
			"startup": map[string]any{
				// REST API TCP port is the only reliable liveness signal during boot
				// (game traffic is UDP, query is connectionless).
				"tcpSocket":           map[string]any{"port": "rest-api"},
				"initialDelaySeconds": int64(30),
				"periodSeconds":       int64(15),
				"failureThreshold":    int64(120), // ~30 minutes at 15-second intervals
			},
			"readiness": map[string]any{
				"tcpSocket":        map[string]any{"port": "rest-api"},
				"periodSeconds":    int64(10),
				"failureThreshold": int64(6),
			},
		},
	})
}
