//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_GarrysModBot_Query boots a REAL Garry's Mod server
// (ceifa/garrysmod with sv_lan 1) through the operator, waits for it to reach Running,
// then runs a minimal Source-protocol bot inside the cluster to measure the join depth.
//
// Garry's Mod is part of the fast set because it uses the Source engine (A2S queries),
// which is shared with CS2. The server boots from a pre-baked image (no steamcmd download)
// and is lightweight.
//
// The test is deliberately NOT t.Parallel(): concurrent real-game boots OOM-starve a
// single kind node.
//
// DEPTH EXPECTATION: This test expects QUERY depth, which is proven by A2S_INFO succeeding.
// The app.go probe attempts a Source engine connection as diagnostic instrumentation only
// to gather evidence about the CONNECT protocol handshake. These diagnostics are logged
// (including raw response bytes) but do not affect the depth measurement.
// A future run can escalate to PARTIAL or JOINED once the connect format is verified against
// a real server response; for now, QUERY is the honest, defensible measurement.
func TestGameServer_GarrysModBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "garrys-mod")

	runGameBotTest(t, gameBotSpec{
		Game:        "garrys-mod",
		Template:    "e2e-garrys-mod",
		DisplayName: "E2E Garry's Mod",
		// Pinned to a specific version tag rather than floating :latest.
		// The shipped template has a digest pin (ceifa/garrysmod:latest@sha256:...)
		// but we use an explicit version tag for CI hermetic-ness.
		// This tag is verified to exist in the ceifa/garrysmod repo as of 2026-07-24.
		Image: "ceifa/garrysmod:debian",
		Env: map[string]string{
			// Debian production image variant (per the shipped template).
			"PRODUCTION": "1",
			// Source dedicated server port.
			"PORT": "27015",
			// Server name.
			"NAME": "Gameplane E2E",
			// Gamemode.
			"GAMEMODE": "sandbox",
			// Map.
			"MAP": "gm_construct",
			// Max players.
			"MAXPLAYERS": "4",
			// Disable SteamCMD auto-update to avoid live network operations on every boot.
			// The ceifa/garrysmod image defaults AUTOUPDATE=on, which runs app_update 4020 validate
			// on every startup; this adds wall-clock flakiness. Set to 0 to boot from the baked image.
			"AUTOUPDATE": "0",
			// Extra launch arguments: sv_lan 1 disables Steam GSLT requirement,
			// allowing LAN/direct-connect joins without a Game Server Login Token.
			// This is the load-bearing setting for non-authenticated joins in CI.
			"ARGS": "+sv_lan 1",
		},
		Ports: []gamePort{
			{Name: "game", Port: 27015, Protocol: "UDP"},
			{Name: "game-tcp", Port: 27015, Protocol: "TCP"},
			{Name: "client", Port: 27005, Protocol: "UDP"},
		},
		StorageSize: "2Gi",
		MountPath:   "/home/gmod/server/garrysmod/data",
		Resources: gameResources{
			ReqCPU: "500m",
			ReqMem: "1Gi",
			LimCPU: "2",
			LimMem: "2Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     27015,
		ProbeDeadline: 4 * time.Minute,
		// ExpectDepth is QUERY because that is what A2S_INFO proves.
		// The source protocol connection attempt is diagnostic only; logs will show
		// the connect outcome, raw bytes, and any rejection message for evidence.
		ExpectDepth: joindepth.QUERY,
		RCON:        map[string]any{"protocol": "none"},
		Probes: map[string]any{
			"readiness": map[string]any{
				// tcpSocket on the TCP port (27015/TCP) rather than UDP.
				// Source engine binds TCP on the same port for pings/RCON queries,
				// even when the game port itself is UDP. The TCP listener indicates
				// the server is ready.
				"tcpSocket":           map[string]any{"port": "game-tcp"},
				"initialDelaySeconds": int64(30),
				"periodSeconds":       int64(10),
				"failureThreshold":    int64(30),
			},
		},
	})
}
