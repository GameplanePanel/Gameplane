//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/test/e2e/internal/protocol/joindepth"
)

// TestGameServer_CS2Bot_Query boots a REAL Counter-Strike 2 server
// (joedwards32/cs2 — the same image the shipped cs2 module uses) through
// the operator, waits for it to reach Running, then runs an in-cluster
// A2S protocol client to verify it reaches QUERY depth (server accepts A2S queries).
// The Source protocol handshake (Challenge + Connect) is attempted diagnostically
// but does not determine the depth, as the Source CONNECT format is unverified.
//
// CS2 is in the HEAVY game set — large disk requirements (60Gi as declared by vendor),
// multi-GB SteamCMD download (10–30 min+), and extended boot time. This test
// DELIBERATELY NEVER RUNS IN CI. It is validated only by maintainer hand-run with:
//
//	GAMEPLANE_E2E_REUSE_CLUSTER=1 GAMEPLANE_E2E_CONTEXT=<kubelab|prod> GAMEPLANE_E2E_GAMES=all make test-e2e-keep
//
// This test is runnable only on a cluster with at least 60Gb of free disk, as
// the vendor's joedwards32/cs2 image pulls multi-GB SteamCMD content into the
// declared PVC on first boot.
//
// The test uses a placeholder SRCDS_TOKEN (not a valid Valve credential), so
// the server will start and answer A2S queries. Real player connections would be
// rejected at the GSLT gate, but that is not tested here (PARTIAL depth).
//
// DO NOT mark as t.Parallel() — two real game servers booting concurrently
// OOM-starve a single kind node.
func TestGameServer_CS2Bot_Query(t *testing.T) {
	skipUnlessGameInScope(t, "cs2")

	// Pin the image to an explicit version (4.0.1) to ensure reproducibility.
	// The floating "latest" tag may change upstream; explicit pins are mandatory
	// for blocking tests.
	const (
		imageTag        = "4.0.1"
		imageSha        = "34c093d24a23751fb409ae41dffe88884fb36964b365fcd25a31ec45e88ad8cf"
		imageFull       = "joedwards32/cs2:" + imageTag + "@sha256:" + imageSha
		placeholderGSLT = "test-gslt-placeholder"
	)

	runGameBotTest(t, gameBotSpec{
		Game:        "cs2",
		Template:    "e2e-cs2",
		DisplayName: "E2E CS2",
		Image:       imageFull,
		Env: map[string]string{
			"SRCDS_TOKEN": placeholderGSLT,
		},
		Ports: []gamePort{
			{Name: "game", Port: 27015, Protocol: "UDP"},
			{Name: "rcon", Port: 27015, Protocol: "TCP"},
		},
		// Vendor declares 60Gi for full SteamCMD install and assets on first boot.
		// This is the minimum disk requirement to run this test.
		StorageSize: "60Gi",
		MountPath:   "/home/steam/cs2-dedicated",
		Resources: gameResources{
			ReqCPU: "1",
			ReqMem: "2Gi",
			LimCPU: "4",
			LimMem: "4Gi",
		},
		ReadyTimeout:  10 * time.Minute,
		ProbePort:     27015,
		ProbeDeadline: 4 * time.Minute,
		ExpectDepth:   joindepth.QUERY,
		// No custom probe flags; bot name is hardcoded in app.go.
		ProbeArgs: []string{},
		// No explicit readiness probe defined in the e2e GameServer.
		// The probe Job itself (in-cluster) gates on the Protocol depth reached.
		// If needed, add a UDP-based readiness check here.
		Probes: nil,
	})
}
