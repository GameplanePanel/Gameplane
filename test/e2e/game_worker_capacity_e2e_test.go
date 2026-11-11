//go:build e2e

package e2e

import (
	"context"
	"fmt"
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
