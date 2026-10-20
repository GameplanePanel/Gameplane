//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_DayZBot_Query boots a REAL DayZ server
// (GodBleak/ServerZ backed by the shipped dayz module template)
// through the operator, waits for it to reach Running, then runs a
// minimal A2S query bot inside the cluster to measure the join depth.
//
// DayZ is part of the heavy set: it requires 40Gi storage and multi-GB
// SteamCMD downloads on first boot, making it unsuitable for CI.
// This test runs only on maintainer hand-run via:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=all make test-e2e-keep
//
// The test is deliberately NOT t.Parallel(): real game boots OOM-starve
// a single kind node, and heavy-set tests are already serialized.
//
// DEPTH EXPECTATION: This test expects QUERY depth.
//   - The A2S query protocol (Steam A2S on UDP 27015) is documented and verified.
//   - The game protocol on UDP 2302 (Enfusion/BattlEye) is not publicly documented.
//   - BattlEye RCON is bound to 127.0.0.1 via BE_IP, unreachable from a probe pod
//     in the default namespace — this is a real product gap, not a measurement
//     limitation. A diagnostic UDP probe is sent to 2302 to log what the server
//     responds with, but no depth is claimed from it.
func TestGameServer_DayZBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "dayz")

	runGameBotTest(t, gameBotSpec{
		Game:        "dayz",
		Template:    "dayz",
		DisplayName: "DayZ",
		// Pinned to the explicit 2.0.0 release tag to ensure hermetic CI builds.
		// ServerZ is maintained at https://github.com/GodBleak/ServerZ;
		// the digest is the committed value from the dayz module template.
		Image: "registry.godbleak.dev/godbleak/serverz:2.0.0@sha256:5e8757beae763c862d08a9c08587c35211cda74ce399f7419492d7520adab4fe",
		Env: map[string]string{
			// Game port (Enfusion protocol, UDP).
			"PORT": "2302",
			// Steam query port (A2S query, UDP).
			// https://developer.valvesoftware.com/wiki/Server_queries
			"STEAM_QUERY_PORT": "27015",
			// BattlEye RCon configuration.
			// BE_PASSWORD is injected by the operator (see rcon.passwordEnv).
			"BE_PORT": "2305",
			// Bind RCon to loopback: unreachable from the probe pod,
			// so this is not a usable control channel for testing. Documented
			// in spec.md as a product gap.
			"BE_IP": "127.0.0.1",
			// Default mission template.
			"TEMPLATE": "dayzOffline.chernarusplus",
			// No Steam Workshop mods (avoids Steam auth requirement).
			"MOD_LIST": "",
		},
		Ports: []gamePort{
			{Name: "game", Port: 2302, Protocol: "UDP"},
			{Name: "query", Port: 27015, Protocol: "UDP"},
		},
		// Heavy set: 40Gi storage as documented by ServerZ.
		// A minimal boot might succeed with less, but the vendor's documented
		// budget is the safe target; trim only after observing real measurements
		// on kubelab (never in CI).
		StorageSize: "40Gi",
		MountPath:   "/data",
		Resources: gameResources{
			ReqCPU: "2",
			ReqMem: "6Gi",
			LimCPU: "4",
			LimMem: "12Gi",
		},
		// World generation takes several minutes on first boot.
		ReadyTimeout:  30 * time.Minute,
		ProbePort:     27015,
		ProbeDeadline: 8 * time.Minute,
		// ExpectDepth is QUERY because that is what A2S_INFO proves.
		// The game protocol and control channel are not reachable/documented
		// from a probe pod, so QUERY is the honest measurement boundary.
		ExpectDepth: joindepth.QUERY,
		RCON:        map[string]any{"protocol": "battleye"},
		// No readiness probe: ServerZ provides no TCP health check endpoint.
		// UDP game/query ports are not reliable probes (servers may silently
		// rate-limit or drop ICMP errors). The operator falls back to "ready
		// when the container is running" — accepted tradeoff documented in
		// the dayz module template.
		Probes: map[string]any{},
	})
}
