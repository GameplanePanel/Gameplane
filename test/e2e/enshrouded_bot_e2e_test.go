//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_EnshroudedBot_Query boots a REAL Enshrouded server
// (mornedhels/enshrouded-server:1.7.2) through the operator, waits for it to reach Running,
// then runs a minimal probe inside the cluster to verify the query port responds.
//
// Enshrouded is part of the HEAVY set because it requires 25Gi of storage and significant
// boot time (world generation, etc.). The heavy set never runs in CI; it is opt-in only
// via GAMEPLANE_E2E_GAMES=all.
//
// The test is deliberately NOT t.Parallel(): concurrent real-game boots exhaust resources
// on a single kind node. The heavy set is hand-run on a real cluster (kubelab, prod, etc.)
// against an already-up cluster.
//
// DEPTH EXPECTATION: This test expects QUERY depth, proven by the query port (UDP 15637)
// responding to a probe packet. Enshrouded has no console, no RCON, and no remote command
// execution of any kind, so this is the only measurement surface available. The probe sends
// a minimal probe packet, logs the hex response (bounded to prevent log flooding), and returns
// QUERY if any response is received. If the port does not respond, the probe fails with a
// fatal error because there is no other path to measure.
func TestGameServer_EnshroudedBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "enshrouded")

	runGameBotTest(t, gameBotSpec{
		Game:        "enshrouded",
		Template:    "e2e-enshrouded",
		DisplayName: "E2E Enshrouded",
		// Pinned to a specific version tag for hermetic-ness.
		// This tag is verified to exist in the mornedhels/enshrouded-docker repo as of 2026-07-25.
		Image: "mornedhels/enshrouded-server:1.7.2",
		Env: map[string]string{
			// Server name (per module template schema).
			"SERVER_NAME": "Gameplane E2E Enshrouded",
			// Max player slots (per module template schema).
			"SERVER_SLOT_COUNT": "16",
		},
		Ports: []gamePort{
			{Name: "game", Port: 15636, Protocol: "UDP"},
			{Name: "query", Port: 15637, Protocol: "UDP"},
		},
		StorageSize: "25Gi",
		MountPath:   "/opt/enshrouded",
		Resources: gameResources{
			ReqCPU: "2",
			ReqMem: "8Gi",
			LimCPU: "4",
			LimMem: "16Gi",
		},
		// Enshrouded requires significant boot time for world generation and initialization.
		// A 20-minute timeout provides ample margin for first-time image pull and world setup.
		ReadyTimeout:  20 * time.Minute,
		ProbePort:     15636, // The probe will internally derive port 15637 (query) from this
		ProbeDeadline: 4 * time.Minute,
		// ExpectDepth is QUERY because that is what the query port proves.
		// The probe attempts to dial port 15637 (query port), sends a minimal probe packet,
		// and returns QUERY if any response is received. Enshrouded has no console or RCON,
		// so there is no deeper measurement possible.
		ExpectDepth: joindepth.QUERY,
		RCON:        map[string]any{"protocol": "none"},
		// No readiness probe configured; operator default applies (pod Ready status).
		// The bot probe's query port connectivity will measure actual readiness.
		Probes: nil,
		// No console at all.
		ConsoleMode: "none",
	})
}
