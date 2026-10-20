//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_DontStarveTogetherBot_Query boots a REAL Don't Starve Together server
// (jamesits/dst-server:vanilla — the same image the shipped dont-starve-together
// module uses) through the operator, waits for it to bootstrap and reach Running,
// then runs a minimal DST diagnostic probe inside the cluster to verify the game
// port (UDP 10999) is listening and responds to a probe.
//
// Don't Starve Together's wire protocol is not publicly documented, so the probe
// can only establish QUERY depth: the server is listening and responds to UDP
// probes on the game port. Escalating to PARTIAL (protocol handshake) or JOINED
// (player acceptance) would require reverse-engineered protocol specification or
// public documentation from Klei, which do not exist. This is an honest QUERY
// measurement, not a regression.
//
// Like the Minecraft and Terraria bot tests, it is opt-in (GAMEPLANE_E2E_GAME_BOT=1)
// and run in the bot bucket. Deliberately NOT t.Parallel(): two real game servers
// booting concurrently OOM-starves a single kind node.
//
// This test is part of the heavy set and does NOT run in GitHub Actions CI. Run it
// locally against a persistent cluster (e.g., kubelab) with:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 \
//	GAMEPLANE_E2E_CONTEXT=kubelab \
//	GAMEPLANE_E2E_GAME_BOT=1 \
//	GAMEPLANE_E2E_GAMES=dont-starve-together \
//	make test-e2e-keep
func TestGameServer_DontStarveTogetherBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "dont-starve-together")

	runGameBotTest(t, gameBotSpec{
		Game:        "dont-starve-together",
		Template:    "e2e-dont-starve-together",
		DisplayName: "E2E Don't Starve Together",
		// Pinned vanilla variant to prevent image drift to a new major version.
		// DST updates frequently; :latest could drift and require protocol changes.
		Image: "jamesits/dst-server:vanilla",
		Env:   map[string]string{
			// DST server boots offline without a Klei cluster token.
			// The template declares DST_CLUSTER_TOKEN as optional config;
			// CI does not set it, so the server runs in offline/LAN mode.
			// No additional env vars needed for CI testing.
		},
		Ports: []gamePort{
			{Name: "game", Port: 10999, Protocol: "UDP"},
			{Name: "caves", Port: 11000, Protocol: "UDP"},
			{Name: "steam1", Port: 12346, Protocol: "UDP"},
			{Name: "steam2", Port: 12347, Protocol: "UDP"},
			// Steam GameServer query port (A2S). Not documented by the
			// jamesits/dst-server-docker image itself; derived from
			// node-gamedig's "dst" game definition (port_query: 27016) and
			// DST's own cluster.ini [STEAM] master_server_port default. Must
			// be advertised so the probe (which dials the Service, not the
			// pod IP) can reach it. See internal/dont-starve-together/spec.md.
			{Name: "steamquery", Port: 27016, Protocol: "UDP"},
		},
		StorageSize: "5Gi",
		MountPath:   "/data",
		Resources: gameResources{
			ReqCPU: "1",
			ReqMem: "1Gi",
			LimCPU: "2",
			LimMem: "2Gi",
		},
		ReadyTimeout:  15 * time.Minute,
		ProbePort:     10999,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON:          map[string]any{"protocol": "none"},
		ConsoleMode:   "pty",
		Probes: map[string]any{
			"readiness": map[string]any{
				"tcpSocket":           map[string]any{"port": "game"},
				"initialDelaySeconds": int64(30),
				"periodSeconds":       int64(10),
				"failureThreshold":    int64(30),
			},
		},
	})
}
