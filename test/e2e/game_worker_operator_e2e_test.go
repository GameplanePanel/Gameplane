//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Exercise the controller across a policy rollout on the isolated placement
// cluster. Direct CR writes also cover installations without the panel API.
func checkGameServerWorkerPolicy(t *testing.T, worker, prefix string) {
	e, ctx := envInstance, t.Context()
	const ns, system = "gameplane-games", "gameplane-system"
	template, legacy, valid, refused := prefix+"-template", prefix+"-legacy", prefix+"-valid", prefix+"-refused"
	key := "gameplane.local/" + prefix
	kubectl := func(ctx context.Context, args ...string) string {
		t.Helper()
		out, err := e.Kubectl(ctx, args...)
		if err != nil {
			t.Fatalf("kubectl %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	pods, sets := e.K8s.CoreV1().Pods(ns), e.K8s.AppsV1().StatefulSets(ns)
	applyBusyboxTemplate(t, template)
	kubectl(ctx, "patch", "gametemplate", template, "--type=merge", "-p",
		`{"spec":{"resources":{"requests":{"cpu":"25m","memory":"16Mi"},"limits":{"memory":"32Mi"}}}}`)
	applyBusyboxGameServer(t, ns, legacy, template)
	kubectl(ctx, "-n", ns, "patch", "gameserver", legacy, "--type=merge", "-p",
		`{"spec":{"resources":{"limits":{"memory":"64Mi"}}}}`)
	e.Eventually(t, 3*time.Minute, func() (bool, string) {
		pod, err := pods.Get(ctx, legacy+"-0", metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		game := pod.Spec.Containers[0]
		return pod.Status.Phase == corev1.PodRunning && game.Name == "game" &&
			game.Resources.Limits.Memory().String() == "64Mi", fmt.Sprint(pod.Status.Phase, game.Resources)
	})
	before, err := sets.Get(ctx, legacy, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kubectl(ctx, "label", "node", worker, key+"=games")
	t.Cleanup(func() { kubectl(context.Background(), "label", "node", worker, key+"-") })
	dep, err := e.K8s.AppsV1().Deployments(system).Get(ctx, "gameplane-operator", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	manager := dep.Spec.Template.Spec.Containers[0]
	if manager.Name != "manager" {
		t.Fatalf("unexpected operator container %q", manager.Name)
	}
	for _, arg := range manager.Args {
		if strings.HasPrefix(arg, "--game-workload-policy") {
			t.Fatalf("operator already has a worker policy; run in the isolated placement cluster")
		}
	}
	setArgs := func(ctx context.Context, current, next []string) {
		t.Helper()
		const path = "/spec/template/spec/containers/0/args"
		data, err := json.Marshal([]map[string]any{
			{"op": "test", "path": path, "value": current},
			{"op": "replace", "path": path, "value": next},
		})
		if err != nil {
			t.Fatal(err)
		}
		kubectl(ctx, "-n", system, "patch", "deployment/gameplane-operator", "--type=json", "-p", string(data))
	}
	policy := `--game-workload-policy={"poolLabel":"` + key + `","poolValue":"games","minCPURequest":"10m","maxCPURequest":"1","minMemoryRequest":"8Mi","maxMemoryLimit":"32Mi"}`
	installed := append(slices.Clone(manager.Args), policy)
	setArgs(ctx, manager.Args, installed)
	awaitRollout := func(ctx context.Context) {
		kubectl(ctx, "-n", system, "rollout", "status", "deployment/gameplane-operator", "--timeout=3m")
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		setArgs(cleanup, installed, manager.Args)
		awaitRollout(cleanup)
	})
	awaitRollout(ctx)
	waitRejection := func(name, reason string) {
		t.Helper()
		e.Eventually(t, time.Minute, func() (bool, string) {
			m := kubectl(ctx, "-n", ns, "get", "gameserver", name, "-o",
				`jsonpath={.status.conditions[?(@.type=="Ready")].message}`)
			return strings.Contains(m, reason), m
		})
	}
	waitRejection(legacy, "resources.limits.memory")
	kubectl(ctx, "-n", ns, "patch", "gameserver", legacy, "--type=merge", "-p", `{"spec":{"suspend":true}}`)
	e.Eventually(t, 2*time.Minute, func() (bool, string) {
		ss, err := sets.Get(ctx, legacy, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		_, podErr := pods.Get(ctx, legacy+"-0", metav1.GetOptions{})
		return ss.UID == before.UID && ss.Spec.Replicas != nil && *ss.Spec.Replicas == 0 && apierrors.IsNotFound(podErr) &&
			ss.Spec.Template.Spec.Containers[0].Resources.Limits.Memory().String() == "64Mi", fmt.Sprint(ss.Status, podErr)
	})
	applyBusyboxTemplate(t, refused)
	applyBusyboxGameServer(t, ns, refused, refused)
	waitRejection(refused, "resources.requests.cpu")
	if _, err := sets.Get(ctx, refused, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("rejected GameServer produced a StatefulSet or lookup failed: %v", err)
	}
	applyBusyboxGameServer(t, ns, valid, template)
	kubectl(ctx, "-n", ns, "patch", "gameserver", valid, "--type=merge", "-p",
		`{"spec":{"resources":{"requests":{"cpu":"50m"}}}}`)
	e.Eventually(t, 3*time.Minute, func() (bool, string) {
		pod, err := pods.Get(ctx, valid+"-0", metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		game := pod.Spec.Containers[0]
		match := corev1.Toleration{Key: key, Value: "games", Operator: corev1.TolerationOpEqual, Effect: corev1.TaintEffectNoSchedule}
		return pod.Status.Phase == corev1.PodRunning && pod.Spec.NodeName == worker && pod.Spec.NodeSelector[key] == "games" &&
				slices.Contains(pod.Spec.Tolerations, match) && game.Name == "game" && game.Resources.Requests.Cpu().String() == "50m" &&
				game.Resources.Requests.Memory().String() == "16Mi" && game.Resources.Limits.Memory().String() == "32Mi",
			fmt.Sprint(pod.Spec.NodeName, pod.Status.Phase, game.Resources)
	})
}
