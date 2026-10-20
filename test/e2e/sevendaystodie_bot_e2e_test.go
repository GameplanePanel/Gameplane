//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_SevenDaysToDieBot_Query boots a REAL 7 Days to Die server
// (vinanrra/7dtd-server) through the operator, waits for it to reach Running,
// then runs a minimal bot inside the cluster to measure the join depth.
//
// 7 Days to Die is part of the heavy set because it requires 55Gi total storage
// (10Gi world saves + 45Gi game install via SteamCMD) and ~25 minutes for first
// boot. It is never run in CI; only on maintainer hand-run with GAMEPLANE_E2E_GAMES=all.
//
// The test is deliberately NOT t.Parallel(): concurrent real-game boots OOM-starve
// a single kind node and exhaust disk space on CI runners.
//
// DEPTH EXPECTATION: This test expects QUERY depth, which is proven by either
// successful A2S query or TCP connectivity to the game port. The exact protocol
// (A2S vs. custom/LiteNetLib) is discovered during the probe run. The join
// handshake is not attempted (credentials unknown; protocol undocumented).
func TestGameServer_SevenDaysToDieBot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "7-days-to-die")

	runGameBotTest(t, gameBotSpec{
		Game:        "7-days-to-die",
		Template:    "e2e-7-days-to-die",
		DisplayName: "E2E 7 Days to Die",
		// Pinned to a specific version tag rather than floating :latest.
		Image: "vinanrra/7dtd-server:v0.9.3@sha256:75d1193fb2470687ee8748f27482524c170f6cbca110e15f48db8d8ff48f15ca",
		Env: map[string]string{
			// Required by LinuxGSM: START_MODE=1 means "start server" (vs. 0/2/4 which install/update/backup then exit).
			"START_MODE": "1",
		},
		Ports: []gamePort{
			{Name: "game", Port: 26900, Protocol: "TCP"},
			{Name: "game-udp", Port: 26900, Protocol: "UDP"},
			{Name: "game2", Port: 26901, Protocol: "UDP"},
			{Name: "game3", Port: 26902, Protocol: "UDP"},
			{Name: "telnet", Port: 8081, Protocol: "TCP"},
		},
		// World saves (10Gi) and server install (45Gi) are on separate PVCs.
		// The template's mountPath is the world-saves directory.
		StorageSize: "10Gi",
		MountPath:   "/home/sdtdserver/.local/share/7DaysToDie",
		Resources: gameResources{
			ReqCPU: "2",
			ReqMem: "6Gi",
			LimCPU: "4",
			LimMem: "12Gi",
		},
		// 7 Days to Die boots slowly: image pull, SteamCMD download (~17.5GB for game install),
		// world generation, mod installation. Budget 25 minutes for startup.
		ReadyTimeout: 25 * time.Minute,
		// Probe on the primary game port (26900 TCP/UDP). The probe will attempt
		// A2S query first, then fall back to TCP connectivity if A2S fails.
		ProbePort: 26900,
		// Allow the probe 6 minutes to verify query/connectivity and handle retries.
		ProbeDeadline: 6 * time.Minute,
		// ExpectDepth is QUERY because that is what A2S query or TCP connectivity proves.
		// The join handshake is not attempted (protocol undocumented, credentials unknown).
		ExpectDepth: joindepth.QUERY,
		// No RCON (template sets rcon.protocol: none; telnet password cannot be wired).
		RCON: map[string]any{"protocol": "none"},
		// No probes configured; the template deliberately omits them.
		Probes: nil,
	})
}
