//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_ProjectZomboidBot_Query boots a REAL Project Zomboid server
// (sknnr/zomboid-dedicated-server:v1.1.1) through the operator, waits for it to reach Running,
// then runs a minimal UDP probe inside the cluster to measure the join depth.
//
// Project Zomboid is part of the HEAVY SET because its image is large (requires multi-GB download
// of Proton layers + game assets) and boots slowly (10+ minutes on first run for world generation).
// It never runs in CI; only on maintainer hand-run with GAMEPLANE_E2E_GAMES=all.
//
// The test is deliberately NOT t.Parallel(): concurrent real-game boots OOM-starve a
// single kind node.
//
// DEPTH EXPECTATION: This test expects QUERY depth, which is proven by the server's response
// to a UDP handshake on port 16261. The probe sends a minimal packet and waits for the server
// to respond, confirming it is alive and accepting UDP connections. A full join (PARTIAL or JOINED)
// requires understanding Project Zomboid's undocumented wire protocol, which this probe does not
// yet implement.
//
// Future iterations can escalate to PARTIAL or JOINED once the protocol is reverse-engineered
// from captured server responses (which are logged as hex dumps in the CI job logs).
func TestGameServer_ProjectZomboidBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "project-zomboid")

	runGameBotTest(t, gameBotSpec{
		Game:        "project-zomboid",
		Template:    "e2e-project-zomboid",
		DisplayName: "E2E Project Zomboid",
		// Image is pinned to v1.1.1 (explicit version tag, not :latest).
		// The shipped template has a digest pin; we use an explicit version tag for CI hermetic-ness.
		Image: "sknnr/zomboid-dedicated-server:v1.1.1",
		Env: map[string]string{
			// REQUIRED by the image — fatal if empty.
			"MAX_MEMORY": "4096m",
			// REQUIRED by the image — fatal if empty.
			// In-game admin account, created on first boot.
			"ADMIN_PASSWORD": "gameplane-e2e-admin",
			// Game server name.
			"SERVER_NAME": "Gameplane E2E",
			// Public server listing enabled.
			"PUBLIC": "true",
			// PvP enabled.
			"PVP": "true",
			// Default map.
			"MAP_NAMES": "Muldraugh, KY",
			// No Workshop mods for this test.
			"MOD_IDS":   "",
			"MOD_NAMES": "",
			// Max players (conservative for e2e).
			"MAX_PLAYERS": "4",
		},
		Ports: []gamePort{
			{Name: "game", Port: 16261, Protocol: "UDP"},
			{Name: "direct", Port: 16262, Protocol: "UDP"},
			{Name: "rcon", Port: 27015, Protocol: "TCP"},
		},
		StorageSize: "15Gi",
		MountPath:   "/home/steam/Zomboid",
		Resources: gameResources{
			ReqCPU: "1",
			ReqMem: "4Gi",
			LimCPU: "4",
			LimMem: "8Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     16261,
		ProbeDeadline: 4 * time.Minute,
		// ExpectDepth is QUERY because that is what a UDP handshake response proves.
		// The server responds to our probe on port 16261; no further protocol understanding is claimed.
		ExpectDepth: joindepth.QUERY,
		RCON:        map[string]any{"protocol": "source", "port": int64(27015)},
		Probes: map[string]any{
			"readiness": map[string]any{
				// TCP probe on RCON port (27015/TCP) rather than the game port (UDP).
				// RCON is the control channel and is ready after the server initializes.
				// The game port is UDP-only; TCP readiness probes do not apply there.
				"tcpSocket":           map[string]any{"port": "rcon"},
				"initialDelaySeconds": int64(30),
				"periodSeconds":       int64(10),
				"failureThreshold":    int64(10),
			},
		},
	})
}
