//go:build e2e

package e2e

import (
	"context"
	"encoding/hex"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var tickPercentiles = regexp.MustCompile(`P95:\s*([0-9.]+)\s*ms\s+P99:\s*([0-9.]+)\s*ms`)

// The Pod parent survives a container restart. Its hierarchical memory.events
// retains the original leaf's limit hits and OOM kills after containerd
// removes that leaf. This test Pod has just one application container.
type tenantMemoryEvents struct {
	group           string
	max, oom, kills uint64
}

// readTenantMemoryEvents identifies the exact container leaf and reads its Pod's hierarchical memory counters on a disposable Kind worker.
func readTenantMemoryEvents(ctx context.Context, t *testing.T, pod *corev1.Pod) tenantMemoryEvents {
	t.Helper()
	if envInstance.Context != "kind-"+envInstance.ClusterName || !strings.HasPrefix(pod.Spec.NodeName, envInstance.ClusterName+"-") {
		t.Fatal("memory qualification requires the disposable Kind worker")
	}
	if len(pod.Status.ContainerStatuses) != 1 || pod.Status.ContainerStatuses[0].Name != "game" {
		t.Fatalf("unexpected tenant containers: %+v", pod.Status.ContainerStatuses)
	}
	id, ok := strings.CutPrefix(pod.Status.ContainerStatuses[0].ContainerID, "containerd://")
	rawID, err := hex.DecodeString(id)
	if !ok || err != nil || len(rawID) != 32 {
		t.Fatalf("invalid containerd game container ID: %q", id)
	}
	const read = `
set -eu
leaf=$(find /sys/fs/cgroup -xdev -ignore_readdir_race -type d \( -name "$1" -o -name "cri-containerd-$1.scope" \) -print)
test -d "$leaf" || { echo "missing unique cgroup for $1" >&2; exit 1; }
group=${leaf%/*}
systemd_uid=$(printf '%s' "$2" | tr '-' '_')
case ${group##*/} in
  "pod$2"|*"-pod$systemd_uid.slice") ;;
  *) echo "container $1 has unexpected Pod parent $group for $2" >&2; exit 1 ;;
esac
awk -v group="$group" -v limit="$(cat "$leaf/memory.max")" '
  { value[$1]=$2 }
  END {
    if (!("max" in value && "oom" in value && "oom_kill" in value)) exit 1
    print group, limit, value["max"], value["oom"], value["oom_kill"]
  }' "$group/memory.events"
`
	out, err := exec.CommandContext(ctx, "docker", "exec", pod.Spec.NodeName, "sh", "-ec", read, "sh", id, string(pod.UID)).CombinedOutput()
	if err != nil {
		t.Fatalf("read tenant cgroup on %s: %v: %s", pod.Spec.NodeName, err, out)
	}
	var snapshot tenantMemoryEvents
	var limit int64
	if _, err := fmt.Sscan(string(out), &snapshot.group, &limit, &snapshot.max, &snapshot.oom, &snapshot.kills); err != nil || limit != 32<<20 {
		t.Fatalf("expected counters and a 32 MiB game leaf on %s: %q (%v)", pod.Spec.NodeName, out, err)
	}
	return snapshot
}

// sampleNeighborTicks records live Minecraft tick durations against the disposable worker's latency budget.
func sampleNeighborTicks(ctx context.Context, t *testing.T, ns, pod, phase string) {
	t.Helper()
	out, err := envInstance.Kubectl(ctx, "exec", "-n", ns, pod, "-c", "game", "--", "rcon-cli", "tick", "query")
	if err != nil {
		t.Fatalf("query Minecraft tick durations at %s: %v %s", phase, err, out)
	}
	match := tickPercentiles.FindStringSubmatch(string(out))
	if len(match) != 3 {
		t.Fatalf("Minecraft did not report tick percentiles at %s: %s", phase, out)
	}
	p95, err95 := strconv.ParseFloat(match[1], 64)
	p99, err99 := strconv.ParseFloat(match[2], 64)
	t.Logf("%s: Minecraft p95=%s ms p99=%s ms", phase, match[1], match[2])
	if err95 != nil || err99 != nil || p95 > 100 || p99 > 250 {
		t.Fatalf("neighbor exceeded the disposable CI fixture's 100 ms p95 / 250 ms p99 budget at %s", phase)
	}
}

// recordWorkerCapacity checks worker pressure and records the requests Kubernetes has scheduled.
func recordWorkerCapacity(ctx context.Context, t *testing.T, worker string) {
	t.Helper()
	node, err := envInstance.K8s.CoreV1().Nodes().Get(ctx, worker, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, cond := range node.Status.Conditions {
		if (cond.Type == corev1.NodeMemoryPressure || cond.Type == corev1.NodeDiskPressure) && cond.Status != corev1.ConditionFalse {
			t.Fatalf("worker %s reported %s=%s", worker, cond.Type, cond.Status)
		}
	}
	out, err := envInstance.Kubectl(ctx, "describe", "node", worker)
	_, allocated, found := strings.Cut(string(out), "Allocated resources:")
	if err != nil || !found {
		t.Fatalf("read scheduler requests on %s: %v", worker, err)
	}
	allocated, _, _ = strings.Cut(allocated, "Events:")
	t.Logf("worker=%s allocatable CPU=%s memory=%s; allocated resources:%s", worker,
		node.Status.Allocatable.Cpu(), node.Status.Allocatable.Memory(), allocated)
}

// startPlacementBackup keeps Restic copying on the game worker until the caller releases it for repository verification.
func startPlacementBackup(ctx context.Context, t *testing.T, ns, poolKey, worker string) func() {
	t.Helper()
	pods := envInstance.K8s.CoreV1().Pods(ns)
	work := `export RESTIC_REPOSITORY=/tmp/repo RESTIC_PASSWORD=disposable
mkdir /tmp/world
restic init
while test ! -e /tmp/finish; do
  dd if=/dev/urandom of=/tmp/world/chunk bs=1M count=2 2>/dev/null
  restic backup -q /tmp/world
  touch /tmp/snapshot-complete
  sleep 1
done
restic check --read-data`
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{GenerateName: "placement-restic-"}, Spec: corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever, NodeSelector: map[string]string{poolKey: "games"},
		Tolerations: []corev1.Toleration{{Key: poolKey, Operator: corev1.TolerationOpEqual, Value: "games", Effect: corev1.TaintEffectNoSchedule}},
		Containers: []corev1.Container{{Name: "backup", Image: "restic/restic:0.19.1", Command: []string{"sh", "-ec", work},
			Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("100m"), corev1.ResourceMemory: resource.MustParse("128Mi")},
				Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")}}}},
	}}
	created, err := pods.Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	name := created.Name
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_ = pods.Delete(cleanup, name, metav1.DeleteOptions{})
	})
	started := time.Now()
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		live, err := pods.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		if live.Status.Phase != corev1.PodRunning || live.Spec.NodeName != worker {
			return false, fmt.Sprint(live.Status.Phase, live.Spec.NodeName)
		}
		out, err := envInstance.Kubectl(ctx, "exec", "-n", ns, name, "--", "sh", "-ec", "test -f /tmp/snapshot-complete")
		return err == nil, string(out)
	})
	return func() {
		t.Helper()
		if out, err := envInstance.Kubectl(ctx, "exec", "-n", ns, name, "--", "touch", "/tmp/finish"); err != nil {
			t.Fatalf("finish restic load: %v %s", err, out)
		}
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			live, err := pods.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			return live.Status.Phase == corev1.PodSucceeded, fmt.Sprint(live.Status.Phase)
		})
		t.Logf("restic backup and repository read verification completed on %s in %s", worker, time.Since(started))
	}
}
