//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestColdWakeFleet_ConcurrentJoins(t *testing.T) {
	if os.Getenv("GAMEPLANE_E2E_GAME_BOT") == "" {
		t.Skip("real client drill runs in an isolated game-bot kind job")
	}
	ctx := t.Context()
	cluster := os.Getenv("GAMEPLANE_E2E_CLUSTER")
	if cluster == "" {
		t.Fatal("missing isolated kind cluster name")
	}
	if out, err := envInstance.Kubectl(ctx, "config", "current-context"); err != nil || strings.TrimSpace(out) != "kind-"+cluster {
		t.Fatalf("refusing operator restart outside the selected kind cluster: %q %v", out, err)
	}
	k := func(args ...string) string {
		t.Helper()
		out, err := envInstance.Kubectl(ctx, args...)
		if err != nil {
			t.Fatalf("kubectl %v: %s (%v)", args, out, err)
		}
		return out
	}
	// This matrix leg has its own operator, networking and node container.
	// Keep the trial on the submitted image and restore the original args.
	operator := envInstance.K8s.AppsV1().Deployments("gameplane-system")
	original, err := operator.Get(ctx, "gameplane-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	restore, err := json.Marshal([]map[string]any{{"op": "replace", "path": "/spec/template/spec/containers/0/args", "value": original.Spec.Template.Spec.Containers[0].Args}})
	if err != nil {
		t.Fatal(err)
	}
	k("-n", "gameplane-system", "patch", "deployment", "gameplane-operator", "--type=json", "-p",
		`[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--wake-start-limit=1"}]`)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		if _, err := operator.Patch(cleanup, original.Name, types.JSONPatchType, restore, metav1.PatchOptions{}); err != nil {
			t.Errorf("restore isolated operator: %v", err)
		}
	})
	k("-n", "gameplane-system", "rollout", "status", "deployment/gameplane-operator", "--timeout=3m")
	mc := minecraftBotSpec()
	mc.Template, mc.Expose, mc.WakeProtocol = "fleet-minecraft", "NodePort", "minecraft"
	tw := gameBotSpec{Template: "fleet-teeworlds", Game: "teeworlds", DisplayName: "Teeworlds 0.7.5", Image: "gameplane-test/wake-teeworlds:ci",
		Expose: "Hostport", WakeProtocol: "generic", ProbePort: 8303, Ports: []gamePort{{Name: "game", Port: 8303, Protocol: "UDP"}},
		MountPath: "/world", StorageSize: "64Mi", ReadyTimeout: 3 * time.Minute,
		Resources: gameResources{ReqCPU: "50m", ReqMem: "32Mi", LimCPU: "1", LimMem: "128Mi"},
		Probes:    map[string]any{"readiness": map[string]any{"exec": map[string]any{"command": []any{"sh", "-ec", "grep -qs ':206F ' /proc/net/udp /proc/net/udp6"}}, "periodSeconds": int64(2)}}}
	mcName, ns := createGameBotServer(t, envInstance, mc)
	parkFleetGame(ctx, t, ns, mcName)
	secondMC := mc
	secondMC.Template = "fleet-minecraft-second"
	secondName, _ := createGameBotServer(t, envInstance, secondMC)
	parkFleetGame(ctx, t, ns, secondName)
	twName, _ := createGameBotServer(t, envInstance, tw)
	parkFleetGame(ctx, t, ns, twName)
	nodes, err := envInstance.K8s.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil || len(nodes.Items) != 1 {
		t.Fatalf("expected one dedicated kind node: %v", err)
	}
	node := nodes.Items[0]
	nodeIP := ""
	for _, address := range node.Status.Addresses {
		if address.Type == "InternalIP" {
			nodeIP = address.Address
			break
		}
	}
	if nodeIP == "" {
		t.Fatal("kind node lacks an InternalIP")
	}
	client := filepath.Join(t.TempDir(), "join-once")
	run := exec.CommandContext(ctx, "go", "build", "-o", client, "./cmd/join-once")
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("build join adapter: %v %s", err, out)
	}
	adapter, err := filepath.Abs("testdata/wake-burst/teeworlds-join.py")
	if err != nil {
		t.Fatal(err)
	}
	reportDir := os.Getenv("GAMEPLANE_E2E_WAKE_REPORTS")
	if reportDir == "" {
		reportDir = t.TempDir()
	}
	if err := os.MkdirAll(reportDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		targets := []map[string]any{}
		for _, entry := range []struct {
			name, game, exposure, protocol string
			port                           int
		}{
			{mcName, mc.Game, mc.Expose, "tcp", mc.ProbePort},
			{secondName, secondMC.Game, secondMC.Expose, "tcp", secondMC.ProbePort},
			{twName, tw.Game, tw.Expose, "udp", tw.ProbePort},
		} {
			path, port := "Hostport", entry.port
			if entry.exposure == "NodePort" {
				path = "Service"
				svc, err := envInstance.K8s.CoreV1().Services(ns).Get(ctx, entry.name, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				port = 0
				for _, servicePort := range svc.Spec.Ports {
					if servicePort.Name == "game" {
						port = int(servicePort.NodePort)
					}
				}
				if port == 0 {
					t.Fatal("missing allocated game NodePort")
				}
			}
			target := map[string]any{"name": entry.name, "namespace": ns, "game": entry.game, "path": path, "protocol": entry.protocol,
				"addr": net.JoinHostPort(nodeIP, fmt.Sprint(port)), "clients": 2, "objectiveSeconds": 300, "deadlineSeconds": 360, "attemptSeconds": 25, "retrySeconds": 2}
			if entry.game == "teeworlds" {
				target["adapter"] = adapter
			}
			targets = append(targets, target)
		}
		plan := filepath.Join(t.TempDir(), "targets.json")
		data, err := json.Marshal(targets)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(plan, data, 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, "python3", "wake_burst.py", "--plan", plan, "--client", client, "--context", "kind-"+cluster,
			"--restart-operator", "gameplane-system/gameplane-operator", "--kind-node", node.Name)
		finishStarts := observeFleetStarts(ctx, ns, []string{mcName, secondName, twName}, 1)
		out, runErr := cmd.CombinedOutput()
		starts, err := finishStarts()
		if writeErr := os.WriteFile(filepath.Join(reportDir, fmt.Sprintf("round-%d-admission.json", round)), starts, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
		if err != nil {
			t.Errorf("fleet admission: %v", err)
		}
		if err := os.WriteFile(filepath.Join(reportDir, fmt.Sprintf("round-%d.json", round)), out, 0o600); err != nil {
			t.Fatal(err)
		}
		if runErr != nil {
			t.Fatalf("cold join burst: %v\n%s", runErr, out)
		}
		if round == 1 {
			parkFleetGame(ctx, t, ns, secondName)
			mc.Expose, tw.Expose = "Hostport", "NodePort"
			for name, expose := range map[string]string{mcName: mc.Expose, twName: tw.Expose} {
				k("-n", ns, "patch", "gameserver", name, "--type=merge", "-p", fmt.Sprintf(`{"spec":{"networking":{"expose":%q}}}`, expose))
				envInstance.Eventually(t, 4*time.Minute, func() (bool, string) {
					pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
					if err != nil {
						return false, err.Error()
					}
					for _, container := range pod.Spec.Containers {
						if container.Name != "game" {
							continue
						}
						for _, port := range container.Ports {
							if port.Name == "game" && (port.HostPort > 0) == (expose == "Hostport") && pod.DeletionTimestamp.IsZero() {
								ready, err := envInstance.PodIsReady(ctx, ns, pod.Name)
								return ready && err == nil, fmt.Sprint(err)
							}
						}
					}
					return false, "awaiting exposure rollout"
				})
				parkFleetGame(ctx, t, ns, name)
			}
		}
	}
}

// Each list is a single Kubernetes snapshot across all target StatefulSets.
// Record active startups from desired replicas and readiness independently of
// the operator's reservation ledger, including during the operator restart.
func observeFleetStarts(ctx context.Context, ns string, targets []string, limit int) func() ([]byte, error) {
	stop := make(chan struct{})
	done := make(chan struct{})
	type sample struct {
		ElapsedMS int64    `json:"elapsedMs"`
		Starting  []string `json:"starting"`
	}
	var samples []sample
	seen := make(map[string]bool)
	var sampleErr error
	go func() {
		defer close(done)
		began := time.Now()
		tick := time.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			sets, err := envInstance.K8s.AppsV1().StatefulSets(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				sampleErr = fmt.Errorf("sample fleet StatefulSets: %w", err)
				return
			}
			starting := []string{}
			for _, set := range sets.Items {
				if slices.Contains(targets, set.Name) && set.Spec.Replicas != nil && *set.Spec.Replicas > 0 && set.Status.ReadyReplicas == 0 {
					starting = append(starting, set.Name)
					seen[set.Name] = true
				}
			}
			slices.Sort(starting)
			if len(samples) == 0 || !slices.Equal(samples[len(samples)-1].Starting, starting) {
				samples = append(samples, sample{time.Since(began).Milliseconds(), starting})
			}
			if len(starting) > limit {
				sampleErr = fmt.Errorf("observed %d simultaneous starts above limit %d: %v", len(starting), limit, starting)
				return
			}
			select {
			case <-stop:
				return
			case <-ctx.Done():
				sampleErr = ctx.Err()
				return
			case <-tick.C:
			}
		}
	}()
	return func() ([]byte, error) {
		close(stop)
		<-done
		if sampleErr == nil && len(seen) != len(targets) {
			sampleErr = fmt.Errorf("observed %d of %d distinct startups", len(seen), len(targets))
		}
		data, err := json.MarshalIndent(samples, "", "  ")
		if err != nil {
			return nil, err
		}
		return data, sampleErr
	}
}

func parkFleetGame(ctx context.Context, t *testing.T, ns, name string) {
	t.Helper()
	patch := []byte(fmt.Sprintf(`{"metadata":{"annotations":{"gameplane.local/idle-asleep-since":%q}}}`, time.Now().UTC().Format(time.RFC3339)))
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
		t.Fatal(err)
	}
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		_, podErr := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, name+"-0", metav1.GetOptions{})
		waker, err := envInstance.K8s.AppsV1().Deployments(ns).Get(ctx, name+"-waker", metav1.GetOptions{})
		return apierrors.IsNotFound(podErr) && err == nil && waker.Status.ReadyReplicas == 1, fmt.Sprint(podErr, err)
	})
}
