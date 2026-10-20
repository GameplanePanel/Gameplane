//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_TerrariaBot_Joined boots a REAL Terraria server
// (passivelemon/terraria-docker — the same image the shipped terraria
// module uses) through the operator, waits for it to generate a world and
// reach Running, then runs a minimal Terraria-protocol bot inside the cluster
// to verify it reaches JOINED depth (server accepts the client as a player
// and answers WorldData).
//
// This test explicitly asserts JOINED depth: the probe must complete the real
// Terraria protocol login handshake and observe a WorldData frame. It is one of
// only two shipped games (Minecraft and Terraria) where this is practical with
// a headless protocol client. The test's suffix _Joined enforces alignment with
// the asserted depth (enforced by gamebot_helpers_e2e_test.go).
//
// The automatic negative control (in runGameBotTest) verifies the probe can fail:
// it runs the same probe against 127.0.0.1:1 (a guaranteed-closed address) with
// -expect-fail, proving the probe correctly reports transport failure when the
// server is unreachable, not a false positive.
//
// Terraria is the one non-Minecraft shipped game where this is practical:
// the server ships in the image (no steamcmd download) and speaks TCP.
// Valheim/Palworld boot via multi-GB steamcmd downloads over proprietary
// UDP protocols, and Factorio is UDP-only — none are bot-testable in CI.
//
// Like the Minecraft bot test, it is opt-in (GAMEPLANE_E2E_GAME_BOT=1) and
// runs in the bot bucket. Deliberately NOT t.Parallel(): two real game
// servers booting concurrently OOM-starves a single kind node.
func TestGameServer_TerrariaBot_Joined(t *testing.T) {
	skipUnlessGameInScope(t, "terraria")

	expectedDepth := joindepth.JOINED

	runGameBotTest(t, gameBotSpec{
		Game:        "terraria",
		Template:    "e2e-terraria",
		DisplayName: "E2E Terraria",
		// Pinned because the bot speaks protocol Terraria279 (1.4.4.x);
		// terraria-latest has moved to 1.4.5.x, whose protocol differs.
		// The server's version-mismatch kick (LegacyMultiplayer.4) names no version,
		// so the bot cannot self-correct. A blocking test must be hermetic.
		Image: "passivelemon/terraria-docker:terraria-1.4.4.9",
		Env: map[string]string{
			"WORLDNAME":  "e2e",
			"AUTOCREATE": "1", // small world
			"DIFFICULTY": "0",
			"MAXPLAYERS": "4",
			"SECURE":     "0",
		},
		Ports: []gamePort{
			{Name: "game", Port: 7777, Protocol: "TCP"},
		},
		StorageSize: "2Gi",
		MountPath:   "/opt/terraria/config",
		Resources: gameResources{
			ReqCPU: "200m",
			ReqMem: "512Mi",
			LimCPU: "1",
			LimMem: "1536Mi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     7777,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   expectedDepth,
		RCON:          map[string]any{"protocol": "none"},
		ConsoleMode:   "pty",
		// The module's exec readiness probe: it reads /proc/net/tcp instead of
		// connecting, because a tcpSocket probe's connect-and-close crashes the
		// Terraria server (ObjectDisposedException in Netplay.ServerLoop).
		Probes: map[string]any{
			"readiness": map[string]any{
				"exec": map[string]any{
					"command": []any{"sh", "-c", "grep -qsE ':1E61 [0-9A-F]+:[0-9A-F]{4} 0A' /proc/net/tcp /proc/net/tcp6"},
				},
				"initialDelaySeconds": int64(30),
				"periodSeconds":       int64(10),
				"failureThreshold":    int64(30),
			},
		},
		Actions: []any{
			map[string]any{
				"id":          "broadcast",
				"displayName": "Broadcast message",
				"transport":   "stdin",
				"command":     "say {{.Params.message}}",
				"params": []any{
					map[string]any{
						"name":        "message",
						"displayName": "Message",
						"type":        "string",
						"required":    true,
					},
				},
			},
		},
		// Path A: drive the server through Gameplane via stdin.
		// Terraria declares rcon.protocol: none and consoleMode: pty, so control
		// happens via pod-attach stdin, fire-and-forget (no RCON reply to check).
		// The broadcast action sends a message to all players, which the server
		// prints to its own stdout — Terraria has no persistent log file
		// (modules/terraria/template.yaml), only a console — so
		// runControlActionStdinPTY asserts the message on the console-pty
		// stream (the same route TestAPI_ConsolePTYRoundTrip exercises)
		// rather than tailing logs. See runGameBotPathA's doc comment in
		// gamebot_helpers_e2e_test.go for the full rationale.
		Control: pathAControl{
			Mode:   "stdin",
			Action: "broadcast",
		},
	})
}
