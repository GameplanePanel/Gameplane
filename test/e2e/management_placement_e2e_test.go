//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// This bucket has no API logins and runs alone on the three-node management
// profile. It uses chart-rendered placement/PDBs with sleeping fixture containers
// to exercise the real scheduler and eviction controller without game downloads.
func TestManagementPlacementAndDisruption(t *testing.T) {
	e := envInstance
	nodes, err := e.K8s.CoreV1().Nodes().List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var candidates []string
	for _, node := range nodes.Items {
		eligible := !node.Spec.Unschedulable
		for _, taint := range node.Spec.Taints {
			if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
				eligible = false
			}
		}
		ready := false
		for _, condition := range node.Status.Conditions {
			ready = ready || (condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue)
		}
		if eligible && ready {
			candidates = append(candidates, node.Name)
		}
	}
	if len(candidates) < 2 {
		t.Fatal("management placement requires two schedulable workers; use the management e2e profile")
	}
	candidates = candidates[:2]
	ns := fmt.Sprintf("placement-%d", time.Now().UnixNano())
	if _, err := e.K8s.CoreV1().Namespaces().Create(t.Context(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.K8s.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })
	poolLabel := "gameplane.local/" + ns
	for _, name := range candidates {
		patchNode := func(value any) error {
			patch, err := json.Marshal(map[string]any{"metadata": map[string]any{"labels": map[string]any{poolLabel: value}}})
			if err != nil {
				return err
			}
			_, err = e.K8s.CoreV1().Nodes().Patch(context.Background(), name, types.MergePatchType, patch, metav1.PatchOptions{})
			return err
		}
		if err := patchNode("management"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = patchNode(nil) })
	}

	chart, err := findRepoRel("charts/gameplane")
	if err != nil {
		t.Fatal(err)
	}
	selector := map[string]any{"app.kubernetes.io/name": "gameplane-web", "app.kubernetes.io/instance": ns}
	values := map[string]any{
		"operator": map[string]any{"enabled": false},
		"api":      map[string]any{"pdb": map[string]any{"enabled": true, "maxUnavailable": 1}},
		"web": map[string]any{
			"enabled": true, "replicas": 2, "nodeSelector": map[string]any{poolLabel: "management"},
			"pdb":                       map[string]any{"enabled": true, "maxUnavailable": 1},
			"affinity":                  map[string]any{"podAntiAffinity": map[string]any{"requiredDuringSchedulingIgnoredDuringExecution": []any{map[string]any{"topologyKey": "kubernetes.io/hostname", "labelSelector": map[string]any{"matchLabels": selector}}}}},
			"topologySpreadConstraints": []any{map[string]any{"maxSkew": 1, "topologyKey": "kubernetes.io/hostname", "whenUnsatisfiable": "DoNotSchedule", "labelSelector": map[string]any{"matchLabels": selector}}},
		},
	}
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	valuesPath := filepath.Join(t.TempDir(), "values.json")
	if err := os.WriteFile(valuesPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	rendered, err := exec.CommandContext(t.Context(), "helm", "template", ns, chart, "--namespace", ns, "--values", valuesPath, "--show-only", "templates/web.yaml", "--show-only", "templates/poddisruptionbudgets.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("render placement chart: %v: %s", err, rendered)
	}
	fixture := corev1.Container{Name: "fixture", Image: "busybox:1.38.0", Command: []string{"sleep", "3600"}}
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(rendered), 4096)
	for {
		var object unstructured.Unstructured
		if err := decoder.Decode(&object); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		switch object.GetKind() {
		case "Deployment":
			var deployment appsv1.Deployment
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &deployment); err != nil {
				t.Fatal(err)
			}
			deployment.Spec.Template.Spec.Containers = []corev1.Container{fixture}
			if _, err := e.K8s.AppsV1().Deployments(ns).Create(t.Context(), &deployment, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
		case "PodDisruptionBudget":
			var pdb policyv1.PodDisruptionBudget
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object.Object, &pdb); err != nil {
				t.Fatal(err)
			}
			if _, err := e.K8s.PolicyV1().PodDisruptionBudgets(ns).Create(t.Context(), &pdb, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	webPods := func() ([]corev1.Pod, error) {
		list, err := e.K8s.CoreV1().Pods(ns).List(t.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-web"})
		if err != nil {
			return nil, err
		}
		return list.Items, nil
	}
	waitReady := func() {
		e.Eventually(t, 3*time.Minute, func() (bool, string) {
			pods, err := webPods()
			if err != nil || len(pods) != 2 {
				return false, fmt.Sprintf("pods=%d error=%v", len(pods), err)
			}
			seen := map[string]bool{}
			for _, pod := range pods {
				ready, err := e.PodIsReady(t.Context(), ns, pod.Name)
				if err != nil || !ready || !pod.DeletionTimestamp.IsZero() {
					return false, "web fixture not ready"
				}
				if pod.Spec.NodeName != candidates[0] && pod.Spec.NodeName != candidates[1] {
					t.Fatalf("pod escaped management selector to %s", pod.Spec.NodeName)
				}
				seen[pod.Spec.NodeName] = true
			}
			return len(seen) == 2, "web replicas were not spread across two workers"
		})
	}
	waitReady()
	setSelector := func(value string) {
		patch, err := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"nodeSelector": map[string]any{poolLabel: value}}}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.K8s.AppsV1().Deployments(ns).Patch(t.Context(), "gameplane-web", types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	setSelector("absent-pool")
	e.Eventually(t, time.Minute, func() (bool, string) {
		pods, err := webPods()
		if err != nil {
			return false, err.Error()
		}
		for _, pod := range pods {
			for _, condition := range pod.Status.Conditions {
				if pod.Spec.NodeSelector[poolLabel] == "absent-pool" && condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse && condition.Reason == "Unschedulable" {
					return true, ""
				}
			}
		}
		return false, "missing unschedulable replacement"
	})
	setSelector("management")
	waitReady()
	cordon := func(unschedulable bool) error {
		patch, err := json.Marshal(map[string]any{"spec": map[string]any{"unschedulable": unschedulable}})
		if err != nil {
			return err
		}
		_, err = e.K8s.CoreV1().Nodes().Patch(context.Background(), candidates[0], types.MergePatchType, patch, metav1.PatchOptions{})
		return err
	}
	if err := cordon(true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cordon(false) })
	waitBudget := func(name string, allowed int32) {
		e.Eventually(t, time.Minute, func() (bool, string) {
			pdb, err := e.K8s.PolicyV1().PodDisruptionBudgets(ns).Get(t.Context(), name, metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			return pdb.Status.ObservedGeneration >= pdb.Generation && pdb.Status.DisruptionsAllowed == allowed, fmt.Sprintf("budget status: %+v", pdb.Status)
		})
	}
	waitBudget("gameplane-web", 1)
	pods, err := webPods()
	if err != nil {
		t.Fatal(err)
	}
	var remaining string
	for _, pod := range pods {
		if pod.Spec.NodeName == candidates[0] {
			if err := e.K8s.CoreV1().Pods(ns).EvictV1(t.Context(), &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: pod.Name, Namespace: ns}}); err != nil {
				t.Fatal(err)
			}
		} else {
			remaining = pod.Name
		}
	}
	waitBudget("gameplane-web", 0)
	if err := e.K8s.CoreV1().Pods(ns).EvictV1(t.Context(), &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: remaining, Namespace: ns}}); !apierrors.IsTooManyRequests(err) {
		t.Fatalf("second eviction bypassed web availability budget: %v", err)
	}
	if ready, err := e.PodIsReady(t.Context(), ns, remaining); err != nil || !ready {
		t.Fatalf("remaining web replica lost readiness: %v", err)
	}
	if err := cordon(false); err != nil {
		t.Fatal(err)
	}
	waitReady()
	// The chart's API budget must permit the only API pod to be evicted. This
	// isolated labeled fixture tests the PDB without stopping the suite's API.
	apiLabels := map[string]string{"app.kubernetes.io/name": "gameplane-api", "app.kubernetes.io/instance": ns}
	one := int32(1)
	apiDeployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "gameplane-api", Namespace: ns}, Spec: appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: apiLabels}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: apiLabels}, Spec: corev1.PodSpec{Containers: []corev1.Container{fixture}}}}}
	if _, err := e.K8s.AppsV1().Deployments(ns).Create(t.Context(), apiDeployment, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	waitBudget("gameplane-api", 1)
	apiPods, err := e.K8s.CoreV1().Pods(ns).List(t.Context(), metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=gameplane-api"})
	if err != nil || len(apiPods.Items) != 1 {
		t.Fatalf("API fixture pods: %v", err)
	}
	if err := e.K8s.CoreV1().Pods(ns).EvictV1(t.Context(), &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Name: apiPods.Items[0].Name, Namespace: ns}}); err != nil {
		t.Fatalf("API singleton PDB blocked drain: %v", err)
	}
}
