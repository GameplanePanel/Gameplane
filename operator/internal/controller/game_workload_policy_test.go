package controller

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const gamePolicyJSON = `{"poolLabel":"gameplane.local/pool","poolValue":"games","minCPURequest":"100m","maxCPURequest":"4","minMemoryRequest":"128Mi","maxMemoryLimit":"8Gi"}`

func TestGameWorkloadPolicyConfiguration(t *testing.T) {
	if p, err := ParseGameWorkloadPolicy(""); err != nil || p != nil {
		t.Fatalf("absent policy: %v %v", p, err)
	}
	for _, raw := range []string{"{}", "null", gamePolicyJSON + "{}", strings.Replace(gamePolicyJSON, "poolValue", "typo", 1),
		strings.Replace(gamePolicyJSON, "gameplane.local/pool", "bad key", 1),
		strings.Replace(gamePolicyJSON, "100m", "8", 1), strings.Replace(gamePolicyJSON, "128Mi", "0", 1)} {
		if _, err := ParseGameWorkloadPolicy(raw); err == nil {
			t.Errorf("accepted invalid policy %s", raw)
		}
	}
}

func gamePolicyFixture(t *testing.T) (*GameWorkloadPolicy, *gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate) {
	t.Helper()
	p, err := ParseGameWorkloadPolicy(gamePolicyJSON)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("256Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
	}
	return p, &gameplanev1alpha1.GameServer{}, tmpl
}

func TestGamePartialResourceOverrideAndPlacement(t *testing.T) {
	p, gs, tmpl := gamePolicyFixture(t)
	gs.Spec.Resources = &corev1.ResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")}}
	gs.Spec.NodeSelector = map[string]string{"rack": "west"}
	gs.Spec.Tolerations = []corev1.Toleration{{Key: p.PoolLabel, Value: p.PoolValue, Effect: corev1.TaintEffectNoSchedule}}
	if err := p.validate(gs, tmpl); err != nil {
		t.Fatal(err)
	}
	res := effectiveResources(gs, tmpl)
	if res.Requests.Cpu().String() != "500m" || res.Requests.Memory().String() != "256Mi" || res.Limits.Memory().String() != "1Gi" || !res.Limits.Cpu().IsZero() {
		t.Fatalf("partial override discarded template bounds or capped CPU bursts: %+v", res)
	}
	if tmpl.Spec.Resources.Requests.Cpu().String() != "250m" || gs.Spec.Resources.Limits != nil {
		t.Fatal("merge mutated its inputs")
	}
	selector, tolerations := p.placement(gs)
	if selector["rack"] != "west" || selector[p.PoolLabel] != p.PoolValue || len(gs.Spec.NodeSelector) != 1 ||
		len(tolerations) != 1 || tolerations[0].Operator != corev1.TolerationOpEqual {
		t.Fatalf("worker placement: %v %v", selector, tolerations)
	}
	var disabled *GameWorkloadPolicy
	sel, tol := disabled.placement(gs)
	if len(sel) != 1 || sel["rack"] != "west" || len(tol) != 1 || tol[0] != gs.Spec.Tolerations[0] {
		t.Fatalf("legacy placement changed: %v %v", sel, tol)
	}
}

func TestGameWorkloadPolicyRejectsOutOfBoundsInputs(t *testing.T) {
	for _, field := range []string{"pool", "tolerations", "missing memory limit", "memory ceiling", "memory reservation", "CPU floor", "CPU cap", "CPU limit"} {
		t.Run(field, func(t *testing.T) {
			p, gs, tmpl := gamePolicyFixture(t)
			switch field {
			case "pool":
				gs.Spec.NodeSelector = map[string]string{p.PoolLabel: "management"}
			case "tolerations":
				gs.Spec.Tolerations = []corev1.Toleration{{Operator: corev1.TolerationOpExists}}
			case "missing memory limit":
				delete(tmpl.Spec.Resources.Limits, corev1.ResourceMemory)
			case "memory ceiling":
				tmpl.Spec.Resources.Limits[corev1.ResourceMemory] = resource.MustParse("9Gi")
			case "memory reservation":
				tmpl.Spec.Resources.Requests[corev1.ResourceMemory] = resource.MustParse("2Gi")
			case "CPU floor":
				tmpl.Spec.Resources.Requests[corev1.ResourceCPU] = resource.MustParse("50m")
			case "CPU cap":
				tmpl.Spec.Resources.Requests[corev1.ResourceCPU] = resource.MustParse("5")
			case "CPU limit":
				tmpl.Spec.Resources.Limits[corev1.ResourceCPU] = resource.MustParse("50m")
			}
			if err := p.validate(gs, tmpl); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

func TestGameWorkloadPolicyRendersMergedResourcesAndWorkerPlacement(t *testing.T) {
	r, gs, effective := revisionFixture(t)
	p, _, tmpl := gamePolicyFixture(t)
	r.GameWorkloadPolicy = p
	effective.Spec.Resources = tmpl.Spec.Resources
	gs.Spec.Resources = &corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")}}
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, effective, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	var ss appsv1.StatefulSet
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	pod := ss.Spec.Template.Spec
	game := pod.Containers[0]
	if game.Name != gameContainerName || game.Resources.Requests.Cpu().String() != "250m" ||
		game.Resources.Limits.Memory().String() != "2Gi" || !game.Resources.Limits.Cpu().IsZero() ||
		pod.NodeSelector[p.PoolLabel] != p.PoolValue || len(pod.Tolerations) != 1 || pod.Tolerations[0].Key != p.PoolLabel {
		t.Fatalf("wrong game resources or placement: %+v / %+v / %+v", game.Resources, pod.NodeSelector, pod.Tolerations)
	}
}

func TestStoredGameCannotCreateWorkloadOutsideWorkerPolicy(t *testing.T) {
	r, gs, _ := revisionFixture(t)
	p, _, _ := gamePolicyFixture(t)
	r.GameWorkloadPolicy = p
	gs.Spec.NodeSelector = map[string]string{p.PoolLabel: "management"}
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	key := client.ObjectKeyFromObject(gs)
	if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(t.Context(), key, gs); err != nil {
		t.Fatal(err)
	}
	ready := meta.FindStatusCondition(gs.Status.Conditions, "Ready")
	if ready == nil || ready.Status != metav1.ConditionFalse || !strings.Contains(ready.Message, "spec.nodeSelector") {
		t.Fatalf("missing actionable policy failure: %+v", gs.Status)
	}
	if err := r.Get(t.Context(), key, &appsv1.StatefulSet{}); !apierrors.IsNotFound(err) {
		t.Fatalf("unsafe workload: %v", err)
	}
}
