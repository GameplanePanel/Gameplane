//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_VRisingBot_Query boots a REAL V Rising server
// (trueosiris/vrising — the same image the shipped v-rising module uses)
// through the operator, waits for it to bootstrap and reach Running, then runs
// a minimal probe inside the cluster to verify it reaches QUERY depth (server
// responds on the query port).
//
// V Rising is in the heavy set because it allocates 20Gi of persistent storage
// (for game install + world data), and multiple concurrent boots exhaust
// GitHub-hosted runner disk. This test runs only when explicitly enabled:
//
//	GAMEPLANE_E2E_GAME_BOT=1 GAMEPLANE_E2E_GAMES=all make test-e2e-keep
//
// The probe attempts A2S query (a diagnostic) and falls back to a raw UDP probe
// if A2S fails. The query protocol is not officially documented by Stunlock;
// depth remains QUERY (basic responsiveness) until the protocol is understood.
//
// Deliberately NOT t.Parallel(): multiple game servers booting concurrently
// compete for disk and memory on a single kind node.
func TestGameServer_VRisingBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "v-rising")

	runGameBotTest(t, gameBotSpec{
		Game:        "v-rising",
		Template:    "e2e-vrising",
		DisplayName: "E2E V Rising",
		// Pinned image as specified in modules/v-rising/template.yaml.
		// V Rising's server installation and world data are stored in the PVC.
		Image: "trueosiris/vrising:2026-04-23-1036",
		Env: map[string]string{
			"SERVERNAME": "E2E V Rising Server",
			"WORLDNAME":  "e2e-world",
		},
		Ports: []gamePort{
			{Name: "game", Port: 9876, Protocol: "UDP"},
			{Name: "query", Port: 9877, Protocol: "UDP"},
			{Name: "rcon", Port: 25575, Protocol: "TCP"},
		},
		StorageSize: "20Gi",
		MountPath:   "/mnt/vrising",
		Resources: gameResources{
			ReqCPU: "1",
			ReqMem: "4Gi",
			LimCPU: "4",
			LimMem: "8Gi",
		},
		ReadyTimeout:  15 * time.Minute,
		ProbePort:     9877,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		RCON:          map[string]any{"protocol": "source", "port": int64(25575)},
		ConsoleMode:   "rcon",
		Probes: map[string]any{
			"readiness": map[string]any{
				"tcpSocket":           map[string]any{"port": "rcon"},
				"initialDelaySeconds": int64(30),
				"periodSeconds":       int64(10),
				"failureThreshold":    int64(30),
			},
		},
	})
}
