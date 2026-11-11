//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Exercise the exact rendered quota and Pod admission policy behind a game
// StatefulSet. This isolated bucket owns two disposable Kind workers.
func TestGameWorkerPoolAndQuota(t *testing.T) {
	e, ctx := envInstance, t.Context()
	ns := fmt.Sprintf("game-pool-%d", time.Now().UnixNano())
	key := "gameplane.local/" + ns
	k := e.K8s.CoreV1()
	nodes, err := k.Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var workers []string
	for _, node := range nodes.Items {
		if !node.Spec.Unschedulable && len(node.Spec.Taints) == 0 {
			workers = append(workers, node.Name)
		}
	}
	if len(workers) < 2 {
		t.Fatal("need two schedulable workers")
	}
	if !t.Run("GameServer policy reconciliation", func(t *testing.T) {
		checkGameServerWorkerPolicy(t, workers[0], ns)
	}) {
		return
	}
	neighbor := minecraftBotSpec()
	neighbor.Template = ns + "-neighbor"
	neighbor.Env["PAUSE_WHEN_EMPTY_SECONDS"] = "0"
	gameName, gameNS := createGameBotServer(t, e, neighbor)
	gamePod, err := k.Pods(gameNS).Get(ctx, gameName+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Lose the other worker. The leak, real Restic workload and neighboring
	// Minecraft world must share the surviving worker throughout the burst.
	if gamePod.Spec.NodeName == workers[0] {
		workers[0], workers[1] = workers[1], workers[0]
	}
	if gamePod.Spec.NodeName != workers[1] {
		t.Fatalf("neighbor scheduled outside the two workers: %s", gamePod.Spec.NodeName)
	}
	sampleNeighborTicks(ctx, t, gameNS, gamePod.Name, "baseline")
	kubectl := func(args ...string) {
		t.Helper()
		if out, err := e.Kubectl(ctx, args...); err != nil {
			t.Fatalf("kubectl %v: %v: %s", args, err, out)
		}
	}
	cleanup := func(f func(context.Context)) {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			f(ctx)
		})
	}
	labelWorker := func(name string) {
		kubectl("label", "node", name, key+"=games")
		cleanup(func(ctx context.Context) { _, _ = e.Kubectl(ctx, "label", "node", name, key+"-") })
		kubectl("taint", "node", name, key+"=games:NoSchedule")
		cleanup(func(ctx context.Context) { _, _ = e.Kubectl(ctx, "taint", "node", name, key+":NoSchedule-") })
	}
	labelWorker(workers[0])
	kubectl("create", "namespace", ns)
	cleanup(func(ctx context.Context) { _ = k.Namespaces().Delete(ctx, ns, metav1.DeleteOptions{}) })
	chart, err := findRepoRel("charts/gameplane")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]any{"gamesNamespace": ns, "operator": map[string]any{
		"gameWorkloadPolicy": map[string]string{"poolLabel": key, "poolValue": "games", "minCPURequest": "10m",
			"maxCPURequest": "1", "minMemoryRequest": "8Mi", "maxMemoryLimit": "32Mi"},
		"tenantQuotas": map[string]any{ns: map[string]any{"hard": map[string]string{
			"pods": "1", "requests.cpu": "1", "requests.memory": "32Mi", "limits.memory": "32Mi",
			"persistentvolumeclaims": "1", "requests.storage": "1Gi",
		}}},
	}}
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "values.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	rendered, err := exec.CommandContext(ctx, "helm", "template", ns, chart, "-n", ns, "-f", path,
		"--show-only", "templates/game-worker-admission.yaml", "--show-only", "templates/tenant-quotas.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("helm: %v: %s", err, rendered)
	}
	cleanup(func(ctx context.Context) {
		_, _ = e.KubectlWithStdin(ctx, string(rendered), "delete", "--ignore-not-found", "-f", "-")
	})
	if out, err := e.KubectlWithStdin(ctx, string(rendered), "apply", "-f", "-"); err != nil {
		t.Fatalf("apply worker policy: %v: %s", err, out)
	}

	pods := k.Pods(ns)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "game", Labels: map[string]string{"app.kubernetes.io/name": "gameplane-game"}},
		Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, NodeSelector: map[string]string{key: "management"},
			Tolerations: []corev1.Toleration{{Key: key, Operator: corev1.TolerationOpEqual, Value: "games", Effect: corev1.TaintEffectNoSchedule}},
			Containers: []corev1.Container{{Name: "game", Image: "busybox:1.38.0", Command: []string{"sleep", "600"},
				Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("16Mi")},
					Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("32Mi")}}}}}}
	e.Eventually(t, time.Minute, func() (bool, string) {
		_, err := pods.Create(ctx, pod, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
		return err != nil && strings.Contains(err.Error(), "must select the configured game worker pool"), fmt.Sprint(err)
	})
	pod.Spec.NodeSelector[key] = "games"
	oversized := pod.DeepCopy()
	oversized.Spec.Containers[0].Resources.Limits[corev1.ResourceMemory] = resource.MustParse("64Mi")
	if _, err := pods.Create(ctx, oversized, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}}); err == nil {
		t.Fatal("admission accepted an oversized game")
	}
	if _, err := pods.Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	e.Eventually(t, 2*time.Minute, func() (bool, string) {
		live, err := pods.Get(ctx, pod.Name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		return live.Spec.NodeName == workers[0] && live.Status.Phase == corev1.PodRunning, fmt.Sprint(live.Status.Phase)
	})
	second := pod.DeepCopy()
	second.Name = "replacement"
	if _, err := pods.Create(ctx, second, metav1.CreateOptions{}); !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), "exceeded quota") {
		t.Fatalf("second game should exceed the tenant quota: %v", err)
	}
	kubectl("cordon", workers[0])
	cleanup(func(ctx context.Context) { _, _ = e.Kubectl(ctx, "uncordon", workers[0]) })
	zero := int64(0)
	if err := pods.EvictV1(ctx, &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: ns},
		DeleteOptions: &metav1.DeleteOptions{GracePeriodSeconds: &zero}}); err != nil {
		t.Fatal(err)
	}
	// Touch anonymous pages in one process. Forking a sleep helper at the
	// tenant's ceiling can fail before the kernel OOM-kills the allocator.
	// OnFailure keeps the Pod cgroup alive for postmortem counters. The new
	// container's writable layer has no /tmp/leak, so it stays idle.
	second.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
	second.Spec.Containers[0].Image = "python:3.14-alpine"
	second.Spec.Containers[0].Command = []string{"python", "-c", `
import mmap, os, time
while not os.path.exists("/tmp/leak"):
    time.sleep(0.25)
held = []
while True:
    block = bytearray(1024 * 1024)
    for page in range(0, len(block), mmap.PAGESIZE):
        block[page] = 1
    held.append(block)
    time.sleep(0.25)
`}
	e.Eventually(t, time.Minute, func() (bool, string) {
		_, err := pods.Create(ctx, second, metav1.CreateOptions{})
		if err != nil && !apierrors.IsForbidden(err) {
			t.Fatalf("create replacement: %v", err)
		}
		return err == nil, fmt.Sprint(err)
	})
	e.Eventually(t, time.Minute, func() (bool, string) {
		live, err := pods.Get(ctx, second.Name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		for _, cond := range live.Status.Conditions {
			if cond.Type == corev1.PodScheduled && cond.Reason == "Unschedulable" && live.Spec.NodeName == "" {
				return true, ""
			}
		}
		return false, fmt.Sprint(live.Status.Conditions)
	})
	labelWorker(workers[1])
	stopBackup := startPlacementBackup(ctx, t, gameNS, key, workers[1])
	var leaking *corev1.Pod
	e.Eventually(t, time.Minute, func() (bool, string) {
		live, err := pods.Get(ctx, second.Name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		leaking = live
		return live.Spec.NodeName == workers[1] && len(live.Status.ContainerStatuses) == 1 &&
			live.Status.ContainerStatuses[0].State.Running != nil && live.Status.ContainerStatuses[0].RestartCount == 0, fmt.Sprint(live.Status.Phase)
	})
	recordWorkerCapacity(ctx, t, workers[1])
	before := readTenantMemoryEvents(ctx, t, leaking)
	originalID := leaking.Status.ContainerStatuses[0].ContainerID
	kubectl("exec", "-n", ns, second.Name, "--", "touch", "/tmp/leak")
	sampleNeighborTicks(ctx, t, gameNS, gamePod.Name, "start, leak and backup after worker loss")
	e.Eventually(t, 3*time.Minute, func() (bool, string) {
		live, err := pods.Get(ctx, second.Name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		if live.UID != leaking.UID || live.Spec.NodeName != leaking.Spec.NodeName {
			t.Fatalf("leaking Pod changed identity or node: %s on %s", live.UID, live.Spec.NodeName)
		}
		if len(live.Status.ContainerStatuses) != 1 {
			return false, "waiting for game container status"
		}
		status := live.Status.ContainerStatuses[0]
		ended := status.LastTerminationState.Terminated
		if status.State.Running == nil || ended == nil {
			return false, fmt.Sprint(status.State)
		}
		if status.RestartCount != 1 || ended.ContainerID != originalID || status.ContainerID == originalID ||
			ended.ExitCode != 137 || (ended.Reason != "OOMKilled" && ended.Reason != "Error") {
			t.Fatalf("unexpected game restart: %+v, previous termination: %+v", status, ended)
		}
		after := readTenantMemoryEvents(ctx, t, live)
		if after.group != before.group || after.max <= before.max || after.oom <= before.oom || after.kills <= before.kills {
			t.Fatalf("missing local limit/OOM/kill increments on %s: before=%+v after=%+v", live.Spec.NodeName, before, after)
		}
		t.Logf("tenant %s container %s exited %d (%s) at 32 MiB; Pod cgroup counters before=%+v after=%+v",
			live.UID, originalID, ended.ExitCode, ended.Reason, before, after)
		return true, ""
	})
	sampleNeighborTicks(ctx, t, gameNS, gamePod.Name, "after tenant OOM")
	stopBackup()
	after, err := k.Pods(gameNS).Get(ctx, gamePod.Name, metav1.GetOptions{})
	if err != nil || after.UID != gamePod.UID || after.Spec.NodeName != workers[1] {
		t.Fatalf("neighbor replaced during isolation drill: %v", err)
	}
	for _, status := range after.Status.ContainerStatuses {
		if status.RestartCount != 0 {
			t.Fatalf("neighbor %s restarted during isolation drill", status.Name)
		}
	}
}
