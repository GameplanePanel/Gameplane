//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

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

	spec := minecraftBotSpec()
	spec.Env["PAUSE_WHEN_EMPTY_SECONDS"] = "-1"
	// The image creates private server files under /data. Use the same
	// unprivileged identity as the Restic reader for this local-path PVC.
	spec.Env["UID"] = "65532"
	spec.Env["GID"] = "65532"
	spec.Quiesce = map[string]any{"quiesce": []any{"save-off", "save-all flush"}, "unquiesce": []any{"save-on"}}
	gs, ns := runGameBotTest(t, spec)
	verifyMinecraftBackupResumesAutosave(t, ns, gs)
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

// Write a new command-storage marker after recovery. Only an automated save
// can put it on disk: this probe issues neither save-all nor a server stop.
func verifyMinecraftBackupResumesAutosave(t *testing.T, ns, gs string) {
	t.Helper()
	ensureResticRepo(t)
	name := gs + "-autosave"
	exec := func(args ...string) (string, error) {
		return envInstance.Kubectl(t.Context(), append([]string{"exec", "-n", ns, "pod/" + gs + "-0", "-c", "game", "--"}, args...)...)
	}
	run := func(command, expected string) {
		t.Helper()
		out, err := exec("rcon-cli", command)
		if err != nil || !strings.Contains(out, expected) {
			t.Fatalf("game command %q: %s (%v), want %q", command, out, err, expected)
		}
	}
	worldMarker := fmt.Sprintf("world_%d", time.Now().UnixNano())
	run("data modify storage gpaudit:backup checkpoint set value "+worldMarker, "Modified storage")
	run("scoreboard objectives add gp_restore dummy", "gp_restore")
	run("scoreboard players set gameplane-bot gp_restore 17", "17")
	b := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "Backup",
		"metadata": map[string]any{"namespace": ns, "name": name},
		"spec":     map[string]any{"serverRef": map[string]any{"name": gs}, "repoRef": map[string]any{"name": "e2e-restic-creds", "key": "repo"}, "quiesce": true},
	}}
	releaseWorker := holdMinecraftBackupWorker(t, b)
	backups := envInstance.Dyn.Resource(backupGVR).Namespace(ns)
	if _, err := backups.Create(t.Context(), b, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { deleteBackupCR(t, ns, name) })
	defer func() {
		if t.Failed() {
			logs, err := envInstance.Kubectl(t.Context(), "logs", "-n", ns, "-l", "job-name="+name, "--all-containers", "--prefix", "--tail=25")
			t.Logf("backup worker logs before cleanup: %s (%v)", logs, err)
		}
	}()
	// Change the live game while its acknowledged save-off protects the
	// already-flushed files. Both marker values must still come from the
	// earlier generation when the worker's snapshot is restored.
	envInstance.Eventually(t, 5*time.Minute, func() (bool, string) {
		got, err := backups.Get(t.Context(), name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		a := got.GetAnnotations()
		if a["backup.gameplane.local/unquiesced-at"] != "" {
			t.Fatal("copy resumed before the live-writer exercise")
		}
		return a["backup.gameplane.local/quiesced-at"] != "", "waiting for the live freeze"
	})
	run("data modify storage gpaudit:backup checkpoint set value during_copy", "Modified storage")
	run("scoreboard players set gameplane-bot gp_restore 23", "23")
	observed, err := backups.Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a := observed.GetAnnotations()
	if a["backup.gameplane.local/quiesced-at"] == "" || a["backup.gameplane.local/unquiesced-at"] != "" || a["backup.gameplane.local/authorized-worker"] != "" {
		t.Fatalf("live writes did not complete during the freeze: quiesced=%q unquiesced=%q authorized-worker=%q",
			a["backup.gameplane.local/quiesced-at"], a["backup.gameplane.local/unquiesced-at"], a["backup.gameplane.local/authorized-worker"])
	}
	releaseWorker()
	waitBackupSucceeded(t, ns, name, 8*time.Minute)
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		got, err := backups.Get(t.Context(), name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		a := got.GetAnnotations()
		outcome, _, _ := unstructured.NestedString(got.Object, "status", "captureManifest", "quiesceOutcome")
		return outcome == "verified-freeze" && a["backup.gameplane.local/quiesce-attempted"] == "true" &&
			a["backup.gameplane.local/unquiesced-at"] != "" && a["backup.gameplane.local/cleanup-outcome"] != "source-gone", "waiting for pause and resume of the live game"
	})
	// An old command storage file cannot satisfy a new marker.
	marker := fmt.Sprintf("gameplane_autosave_%d", time.Now().UnixNano())
	if out, err := exec("rcon-cli", "data modify storage gpaudit:backup marker set value "+marker); err != nil || !strings.Contains(out, "Modified storage") {
		t.Fatalf("set post-backup marker: %s (%v)", out, err)
	}
	if out, err := exec("rcon-cli", "tick sprint 7000t"); err != nil {
		t.Fatalf("advance vanilla autosave ticks: %s (%v)", out, err)
	}
	envInstance.Eventually(t, 7*time.Minute, func() (bool, string) {
		out, err := exec("gzip", "-dc", "/data/world/data/command_storage_gpaudit.dat")
		return err == nil && strings.Contains(out, marker), "waiting for automatic persistence of the post-backup marker"
	})
	// Overwrite both independent markers. Seeing the original values after a
	// real restore proves this join landed on the captured generation.
	run("data modify storage gpaudit:backup checkpoint set value after_backup", "Modified storage")
	run("scoreboard players set gameplane-bot gp_restore 42", "42")
	run("save-all flush", "Saved the game")
	restore := gs + "-restore-proof"
	createRestore(t, ns, restore, gs, name)
	waitRestoreSucceeded(t, ns, restore, 8*time.Minute)
	s := minecraftBotSpec()
	res := envInstance.RunGameProbe(t, GameProbe{
		GameNS: ns, GSName: gs, JobSuffix: "restored", Game: s.Game, Port: s.ProbePort,
		Deadline: s.ProbeDeadline, ExpectDepth: joindepth.JOINED, Args: s.ProbeArgs,
	})
	if res.ExitCode != 0 || res.Verdict == nil || res.Verdict.ReachedDepth != joindepth.JOINED {
		t.Fatalf("join after restored-world boot: %+v", res)
	}
	run("data get storage gpaudit:backup checkpoint", worldMarker)
	run("scoreboard players get gameplane-bot gp_restore", "17")
	run("scoreboard players set gameplane-bot gp_restore 18", "18")
	run("save-all flush", "Saved the game")
}

// The worker prepares its repository before save-off. The controller records
// the freeze, then persists the authorized worker UID before it issues the
// start token. Hold that one metadata update until the live writes finish.
func holdMinecraftBackupWorker(t *testing.T, backup *unstructured.Unstructured) func() {
	t.Helper()
	name := fmt.Sprintf("e2e-live-copy-%d", time.Now().UnixNano())
	const denial = "waiting for the Minecraft live writes"
	policy := fmt.Sprintf(`apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: %[1]s
spec:
  matchConstraints:
    resourceRules:
    - apiGroups: ["gameplane.local"]
      apiVersions: ["v1alpha1"]
      operations: ["CREATE", "UPDATE"]
      resources: ["backups"]
      resourceNames: [%[2]q]
    namespaceSelector:
      matchLabels:
        kubernetes.io/metadata.name: %[3]q
  validations:
  - expression: "!has(object.metadata.annotations) || !('backup.gameplane.local/authorized-worker' in object.metadata.annotations)"
    message: %[4]q
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: %[1]s
spec:
  policyName: %[1]s
  validationActions: [Deny]
`, name, backup.GetName(), backup.GetNamespace(), denial)
	release := func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
		defer cancel()
		if out, err := envInstance.KubectlWithStdin(ctx, policy, "delete", "--ignore-not-found", "-f", "-"); err != nil {
			t.Fatalf("remove live-write barrier: %s (%v)", out, err)
		}
	}
	t.Cleanup(release)
	if out, err := envInstance.KubectlWithStdin(t.Context(), policy, "create", "-f", "-"); err != nil {
		t.Fatalf("create live-write barrier: %s (%v)", out, err)
	}
	// Admission configuration propagates asynchronously. Prove that the
	// barrier is active before the real Backup can start on a fast cluster.
	probe := backup.DeepCopy()
	probe.SetAnnotations(map[string]string{"backup.gameplane.local/authorized-worker": "dry-run"})
	envInstance.Eventually(t, time.Minute, func() (bool, string) {
		_, err := envInstance.Dyn.Resource(backupGVR).Namespace(backup.GetNamespace()).Create(t.Context(), probe, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		return err != nil && strings.Contains(err.Error(), denial), fmt.Sprint(err)
	})
	return release
}
