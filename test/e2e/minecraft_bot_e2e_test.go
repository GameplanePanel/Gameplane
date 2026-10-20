//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_MinecraftJavaBot_Joined is the most end-to-end test in the
// suite: it stands up a REAL Minecraft server (itzg/minecraft-server) through
// the operator, waits for it to actually boot, and runs a headless protocol bot
// inside the cluster that pings the server and completes a login — proving the
// server is genuinely playable, not merely "Running" in Kubernetes.
//
// The bot runs as an in-cluster Job (see `runGameBotTest`) and dials the game
// Service directly rather than through a `kubectl port-forward` tunnel.
//
// This test explicitly asserts JOINED depth: the probe must complete the real
// Minecraft protocol login handshake and observe a Login Success packet. It is
// one of only two shipped games (Minecraft and Terraria) where this is practical
// with a headless protocol client. The test's suffix _Joined enforces alignment
// with the asserted depth (enforced by gamebot_helpers_e2e_test.go).
//
// The automatic negative control (in runGameBotTest) verifies the probe can fail:
// it runs the same probe against 127.0.0.1:1 (a guaranteed-closed address) with
// -expect-fail, proving the probe correctly reports transport failure when the
// server is unreachable, not a false positive.
//
// Unlike the other GameServer tests (which use a busybox "fake game" and never
// wait for a Ready pod), this pulls a large external image and boots a JVM, so
// it is opt-in (set GAMEPLANE_E2E_GAME_BOT=1) and runs on its own CI job with a
// generous timeout. The protocol client is also exercised against the shipped
// minecraft-java template on a real cluster; here we use a trimmed vanilla
// template so it boots fast and fits a single kind node.
//
// Terraria has its own bespoke protocol bot (terraria_bot_e2e_test.go).
// Headless clients for the remaining shipped games are not viable:
//   - Valheim uses a proprietary, password-gated UDP protocol — no open
//     client — and boots via a multi-GB steamcmd download; same for Palworld.
//   - Factorio's game traffic is UDP-only and our module runs RCON off, so
//     there is no assertable control channel.
func TestGameServer_MinecraftJavaBot_Joined(t *testing.T) {
	skipUnlessGameInScope(t, "minecraft-java")

	runGameBotTest(t, minecraftBotSpec())
}

// minecraftBotSpec is the trimmed vanilla Minecraft Java 1.21.4 GameServer used by
// TestGameServer_MinecraftJavaBot_Joined. It is shared so other tests can boot the
// same server.
func minecraftBotSpec() gameBotSpec {
	return gameBotSpec{
		Game:        "minecraft-java",
		Template:    "e2e-minecraft",
		DisplayName: "E2E Minecraft",
		Image:       "itzg/minecraft-server:java21",
		Env: map[string]string{
			"EULA":             "TRUE",
			"TYPE":             "VANILLA",
			"VERSION":          "1.21.4",
			"ONLINE_MODE":      "FALSE",
			"INIT_MEMORY":      "512M",
			"MAX_MEMORY":       "1G",
			"USE_AIKAR_FLAGS":  "false",
			"LEVEL_TYPE":       "FLAT",
			"VIEW_DISTANCE":    "4",
			"SPAWN_PROTECTION": "0",
			"ENABLE_RCON":      "true",
			"RCON_PORT":        "25575",
		},
		Ports: []gamePort{
			{Name: "game", Port: 25565, Protocol: "TCP"},
			{Name: "rcon", Port: 25575, Protocol: "TCP"},
		},
		StorageSize: "2Gi",
		MountPath:   "/data",
		Resources: gameResources{
			ReqCPU: "250m",
			ReqMem: "1Gi",
			LimCPU: "2",
			LimMem: "1536Mi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     25565,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.JOINED,
		ProbeArgs:     []string{"-user", "gameplane-bot"},
		Probes: map[string]any{
			"readiness": map[string]any{
				"exec":                map[string]any{"command": []any{"mc-health"}},
				"initialDelaySeconds": int64(30),
				"periodSeconds":       int64(10),
				"failureThreshold":    int64(60),
			},
		},
		RCON: map[string]any{
			"protocol":    "source",
			"port":        int64(25575),
			"passwordEnv": "RCON_PASSWORD",
		},
		Actions: []any{
			map[string]any{
				"id":          "broadcast",
				"displayName": "Broadcast message",
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
			map[string]any{
				"id":          "save-world",
				"displayName": "Save world",
				"command":     "save-all flush",
			},
		},
		// Path A: drive the server through Gameplane via RCON, using
		// save-world rather than broadcast. Vanilla Minecraft's "say"
		// command returns an EMPTY RCON reply — the message only shows up
		// in chat/logs, never in the reply itself — so asserting a
		// broadcast's reply would be vacuous. "save-all flush" replies
		// with the exact text of Mojang's "commands.save.success" lang
		// string, "Saved the game" (verified against the vanilla 1.21.4
		// en_us.json, not assumed), which runControlActionRCON asserts
		// against directly. See runGameBotPathA's doc comment in
		// gamebot_helpers_e2e_test.go for why this reads the RCON reply
		// instead of tailing logs.
		Control: pathAControl{
			Mode:      "rcon",
			Action:    "save-world",
			ExpectRaw: "Saved the game",
		},
	}
}
