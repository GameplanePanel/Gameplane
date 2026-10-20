//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_RustBot_Query boots a REAL Rust server
// (didstopia/rust-server with sv_lan 1) through the operator, waits for it to reach Running,
// then runs a minimal A2S protocol bot inside the cluster to measure the join depth.
//
// Rust is part of the HEAVY game set — the didstopia/rust-server image performs
// multi-GB SteamCMD downloads on first boot and has extended initialization time.
// This test DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run with:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<kubelab|prod> GAMEPLANE_E2E_GAMES=all make test-e2e-keep
//
// This test requires at least 10Gb of free disk (declared PVC size) and 10+ minutes
// wall-clock time for the Rust server to initialize and be ready for queries.
//
// The test uses sv_lan 1 to disable Steam GSLT requirement, allowing the server to start
// without valid Valve credentials. However, the Rust game protocol is RakNet-derived and
// undocumented, so real player joins would fail at the Steam auth gate. This probe measures
// QUERY depth only — the server answers A2S queries but does not accept real joins.
//
// DO NOT mark as t.Parallel() — two real game servers booting concurrently on a single
// kind node OOM-starve the allocator.
func TestGameServer_RustBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "rust")

	// Pin the image to an explicit SHA256 digest rather than floating :latest.
	// The didstopia/rust-server image is frequently updated; hermetic testing
	// requires an explicit, verified digest. This digest is taken from the
	// shipped modules/rust/template.yaml and verified to exist on Docker Hub.
	const (
		imageTag  = "latest"
		imageSha  = "a16589d9182245faee4353cd99e59965e6a9bec17d3d757bb427c48a17546a2b"
		imageFull = "didstopia/rust-server:" + imageTag + "@sha256:" + imageSha
	)

	runGameBotTest(t, gameBotSpec{
		Game:        "rust",
		Template:    "e2e-rust",
		DisplayName: "E2E Rust",
		Image:       imageFull,
		Env: map[string]string{
			// Rust server configuration (didstopia/rust-server defaults).
			// Server name and settings.
			"RUST_SERVER_NAME":       "Gameplane E2E Rust",
			"RUST_SERVER_MAXPLAYERS": "10",
			// sv_lan 1 disables Steam GSLT requirement, allowing LAN/local-only joins.
			// This is required in CI where we cannot provide valid Steam credentials.
			// See: https://rust.facepunch.com/wiki/Server_Commands
			"ARGS": "+sv_lan 1",
		},
		Ports: []gamePort{
			// Game protocol: RakNet on UDP.
			{Name: "game", Port: 28015, Protocol: "UDP"},
			// Query protocol: A2S (Valve Source query) on UDP (optional but supported).
			// Not a separate port; A2S queries on the same port as game traffic.
			// No explicit port mapping needed; the probe dials :28015.
		},
		// Storage: 10Gi as declared in the Rust template.
		// This is needed for the SteamCMD download and world files on first boot.
		StorageSize: "10Gi",
		MountPath:   "/steamcmd/rust",
		Resources: gameResources{
			ReqCPU: "1",
			ReqMem: "4Gi",
			LimCPU: "4",
			LimMem: "8Gi",
		},
		// Time budget for the pod to reach Running state (downloading + initialization).
		ReadyTimeout: 10 * time.Minute,
		// Probe port: game port (28015), where A2S queries are sent.
		ProbePort: 28015,
		// Probe deadline: 4 minutes for A2S to succeed after the pod is Running.
		ProbeDeadline: 4 * time.Minute,
		// ExpectDepth is QUERY because Rust answers A2S queries.
		// The real game protocol is undocumented and requires Steam auth, so JOINED
		// depth is not reachable in CI. PARTIAL would require further auth negotiation
		// we cannot complete. QUERY is the honest measurement: the server accepts A2S
		// status queries.
		ExpectDepth: joindepth.QUERY,
		// No custom probe flags; the probe hardcodes port 28015 in its A2S attempt.
		ProbeArgs: []string{},
		// Readiness probe: TCP on port 28016 (WebSocket RCON).
		// The didstopia/rust-server image binds the WebSocket RCON listener on port 28016,
		// which indicates the server is ready to accept RCON commands and likely ready
		// for A2S queries as well. A simple TCP connection confirms the port is open.
		Probes: map[string]any{
			"readiness": map[string]any{
				"tcpSocket": map[string]any{
					"port": int64(28016),
				},
				"initialDelaySeconds": int64(30), // RCON startup delay.
				"periodSeconds":       int64(10), // Check every 10 seconds.
				"failureThreshold":    int64(60), // Allow up to 10 minutes (60 × 10s) for boot.
			},
		},
		// RCON configuration: WebSocket protocol on port 28016.
		// This is provided for Path A (Gameplane heartbeat + console).
		// Path B (this probe) does not use RCON, so it doesn't measure depth via RCON auth.
		RCON: map[string]any{
			"protocol": "websocket",
			"port":     int64(28016),
		},
		// consoleMode: RCON allows console commands to be executed via WebSocket RCON.
		ConsoleMode: "rcon",
	})
}
