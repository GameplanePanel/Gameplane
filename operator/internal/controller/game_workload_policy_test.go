package controller

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const gamePolicyJSON = `{"poolLabel":"gameplane.local/pool","poolValue":"games","minCPURequest":"100m","maxCPURequest":"4","minMemoryRequest":"128Mi","maxMemoryLimit":"8Gi"}`

// TestGameWorkloadPolicyConfiguration rejects malformed, ambiguous and incomplete administrator policies.
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
	var syntax *json.SyntaxError
	if _, err := ParseGameWorkloadPolicy(gamePolicyJSON + "!"); !errors.As(err, &syntax) {
		t.Fatalf("trailing JSON syntax error lost: %v", err)
	}
}

// gamePolicyFixture provides a valid resource tier with no CPU limit for burst capacity.
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

// TestGamePartialResourceOverrideAndPlacement checks that an override leaves independent resource and placement settings intact.
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
	selector, tolerations := p.placement(gs, tmpl)
	if selector["rack"] != "west" || selector[p.PoolLabel] != p.PoolValue || len(gs.Spec.NodeSelector) != 1 ||
		len(tolerations) != 1 || tolerations[0].Operator != corev1.TolerationOpEqual {
		t.Fatalf("worker placement: %v %v", selector, tolerations)
	}
	var disabled *GameWorkloadPolicy
	sel, tol := disabled.placement(gs, tmpl)
	if len(sel) != 1 || sel["rack"] != "west" || len(tol) != 1 || tol[0] != gs.Spec.Tolerations[0] {
		t.Fatalf("legacy placement changed: %v %v", sel, tol)
	}
}

// TestGameWorkloadPolicyRejectsOutOfBoundsInputs exercises each rejected quantity and scheduling constraint.
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

// TestGameWorkloadPolicyRendersMergedResourcesAndWorkerPlacement inspects the actual game container and Pod specification.
func TestGameWorkloadPolicyRendersMergedResourcesAndWorkerPlacement(t *testing.T) {
	r, gs, effective := revisionFixture(t)
	p, _, tmpl := gamePolicyFixture(t)
	r.GameWorkloadPolicy = p
	effective.Spec.Resources = tmpl.Spec.Resources
	effective.Spec.RequiredNodeArchitecture = "amd64"
	gs.Spec.NodeSelector = map[string]string{"rack": "west"}
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
		pod.NodeSelector[p.PoolLabel] != p.PoolValue || pod.NodeSelector[corev1.LabelArchStable] != "amd64" ||
		pod.NodeSelector["rack"] != "west" || len(gs.Spec.NodeSelector) != 1 ||
		len(pod.Tolerations) != 1 || pod.Tolerations[0].Key != p.PoolLabel {
		t.Fatalf("wrong game resources or placement: %+v / %+v / %+v", game.Resources, pod.NodeSelector, pod.Tolerations)
	}
}

func TestTemplateArchitectureRejectsConflictingPlacement(t *testing.T) {
	for _, conflict := range []string{"server", "worker pool"} {
		t.Run(conflict, func(t *testing.T) {
			r, gs, tmpl := revisionFixture(t)
			tmpl.Spec.RequiredNodeArchitecture = "amd64"
			if conflict == "worker pool" {
				p, _, valid := gamePolicyFixture(t)
				p.PoolLabel, p.PoolValue = corev1.LabelArchStable, "arm64"
				r.GameWorkloadPolicy, tmpl.Spec.Resources = p, valid.Spec.Resources
			} else {
				gs.Spec.NodeSelector = map[string]string{corev1.LabelArchStable: "arm64"}
			}
			delete(gs.Annotations, effectiveTemplateAnnotation)
			ctx := t.Context()
			if err := r.Update(ctx, tmpl); err != nil {
				t.Fatal(err)
			}
			if err := r.Update(ctx, gs); err != nil {
				t.Fatal(err)
			}
			key := client.ObjectKeyFromObject(gs)
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(ctx, key, gs); err != nil {
				t.Fatal(err)
			}
			ready := meta.FindStatusCondition(gs.Status.Conditions, "Ready")
			if ready == nil || ready.Status != metav1.ConditionFalse || !strings.Contains(ready.Message, "amd64") ||
				gs.Annotations[lifecycleOperationAnnotation] != "" {
				t.Fatalf("conflicting placement was admitted or not explained: %+v", gs)
			}
			if err := r.Get(ctx, key, &appsv1.StatefulSet{}); !apierrors.IsNotFound(err) {
				t.Fatalf("conflicting placement produced a workload: %v", err)
			}
		})
	}
}

// TestStoredGameCannotCreateWorkloadOutsideWorkerPolicy checks direct-CR rejection without creating a StatefulSet.
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

// TestRejectedWorkloadOnlyStopsItsOwnedStatefulSet verifies that a stop preserves the old template and respects ownership.
func TestRejectedWorkloadOnlyStopsItsOwnedStatefulSet(t *testing.T) {
	for _, tc := range []struct {
		name      string
		legacy    bool
		suspended bool
		foreign   bool
		want      int32
	}{
		{name: "pinned and running", want: 1},
		{name: "pinned and suspended", suspended: true},
		{name: "unadmitted baseline and suspended", legacy: true, suspended: true},
		{name: "foreign StatefulSet", suspended: true, foreign: true, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, gs, effective := revisionFixture(t)
			if err := r.reconcileStatefulSet(t.Context(), gs, effective, nil, &materializedConfig{}, 1); err != nil {
				t.Fatal(err)
			}
			key := client.ObjectKeyFromObject(gs)
			var ss appsv1.StatefulSet
			if err := r.Get(t.Context(), key, &ss); err != nil {
				t.Fatal(err)
			}
			if tc.foreign {
				ss.OwnerReferences[0].UID = "another-server"
				if err := r.Update(t.Context(), &ss); err != nil {
					t.Fatal(err)
				}
			}
			original := ss.Spec.DeepCopy()
			if tc.legacy {
				delete(gs.Annotations, effectiveTemplateAnnotation)
			}
			r.GameWorkloadPolicy, _, _ = gamePolicyFixture(t)
			gs.Spec.NodeSelector = map[string]string{r.GameWorkloadPolicy.PoolLabel: "invalid"}
			gs.Spec.Suspend = tc.suspended
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(t.Context(), key, &ss); err != nil {
				t.Fatal(err)
			}
			original.Replicas = &tc.want
			if !reflect.DeepEqual(ss.Spec, *original) {
				t.Fatalf("expected existing workload preserved with %d replicas, got %+v", tc.want, ss.Spec)
			}
		})
	}
}

// TestRejectedRolloutDrainsWithThePinnedStopContract keeps the game running during its verified stop grace.
func TestRejectedRolloutDrainsWithThePinnedStopContract(t *testing.T) {
	r, gs, old := revisionFixture(t)
	old.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source"}
	old.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{Lifecycle: &gameplanev1alpha1.LifecycleSpec{Stop: []string{"save", "stop"}}}
	pin, err := r.saveTemplateSnapshot(t.Context(), gs, old)
	if err != nil {
		t.Fatal(err)
	}
	gs.Annotations[effectiveTemplateAnnotation] = pin
	gs.Spec.TemplateRollout.Policy = "Automatic"
	gs.Spec.Suspend = true
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	one := int32(1)
	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, OwnerReferences: []metav1.OwnerReference{
			*metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer")),
		}},
		Spec: appsv1.StatefulSetSpec{Replicas: &one}, Status: appsv1.StatefulSetStatus{ReadyReplicas: 1, Replicas: 1},
	}
	if err := r.Create(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	stopper := &revisionStopper{}
	r.AgentClient = stopper
	r.GameWorkloadPolicy, _, _ = gamePolicyFixture(t)
	key := client.ObjectKeyFromObject(gs)
	result, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: key})
	if err != nil || result.RequeueAfter <= 0 || stopper.calls != 1 {
		t.Fatalf("pinned graceful stop: calls=%d requeue=%v err=%v", stopper.calls, result.RequeueAfter, err)
	}
	if err := r.Get(t.Context(), key, ss); err != nil || *ss.Spec.Replicas != 1 {
		t.Fatalf("failed to preserve the running world during grace: %v", err)
	}
}

// Historical baseline migration must validate the pinned probe before claiming a drain.
func TestWorkerPolicyRefusesUnsafeProbeInHistoricalBaseline(t *testing.T) {
	ctx := t.Context()
	r, gs, catalog := revisionFixture(t)
	policy, _, valid := gamePolicyFixture(t)
	r.GameWorkloadPolicy = policy
	old := catalog.DeepCopy()
	old.Spec.Resources = valid.Spec.Resources
	old.Spec.Probes = &gameplanev1alpha1.GameProbesSpec{Liveness: probeSecHTTP("169.254.169.254", intstr.FromInt32(80))}
	pin, err := r.saveTemplateSnapshot(ctx, gs, old)
	if err != nil {
		t.Fatal(err)
	}
	gs.Annotations[effectiveTemplateAnnotation] = pin
	gs.Annotations[baselineTemplateAnnotation] = pin
	if err := r.Update(ctx, gs); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.reconcileTemplateRevision(ctx, gs); err == nil || !strings.Contains(err.Error(), "spec.probes.liveness.httpGet.host") {
		t.Fatalf("expected the pinned probe host to block migration: %v", err)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	if gs.Annotations[lifecycleOperationAnnotation] != "" {
		t.Fatal("unsafe pinned probe claimed a migration drain")
	}
}

// A catalog snapshot saved during migration is not proof of the legacy Pod's stop contract.
func TestWorkerPolicyDrainsLegacyPodBeforeFirstPin(t *testing.T) {
	for _, contract := range []string{"unversioned", "verified", "previously pinned baseline", "template architecture only"} {
		t.Run(contract, func(t *testing.T) {
			ctx := t.Context()
			r, gs, catalog := revisionFixture(t)
			policy, _, valid := gamePolicyFixture(t)
			r.GameWorkloadPolicy = policy
			catalog.Spec.Resources = valid.Spec.Resources
			if contract == "template architecture only" {
				r.GameWorkloadPolicy = nil
				catalog.Spec.RequiredNodeArchitecture = "amd64"
			}
			catalog.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source"}
			catalog.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{Lifecycle: &gameplanev1alpha1.LifecycleSpec{Stop: []string{"save", "stop"}}}
			oldRevision, err := r.saveTemplateSnapshot(ctx, gs, catalog)
			if err != nil {
				t.Fatal(err)
			}
			catalog.Spec.Image = "example.invalid/game:current"
			if contract == "verified" {
				// Only the old, verified contract has a stop sequence in this case.
				catalog.Spec.Capabilities = nil
			}
			if err := r.Update(ctx, catalog); err != nil {
				t.Fatal(err)
			}
			delete(gs.Annotations, effectiveTemplateAnnotation)
			if contract == "previously pinned baseline" {
				pin, err := r.saveTemplateSnapshot(ctx, gs, catalog)
				if err != nil {
					t.Fatal(err)
				}
				gs.Annotations[effectiveTemplateAnnotation] = pin
				gs.Annotations[baselineTemplateAnnotation] = pin
				gs.Annotations[baselineWorkloadAnnotation] = "legacy"
			}
			if err := r.Update(ctx, gs); err != nil {
				t.Fatal(err)
			}
			one := int32(1)
			ss := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "legacy", OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))}},
				Spec:       appsv1.StatefulSetSpec{Replicas: &one, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "game", Image: "example.invalid/game:legacy"}}}}},
				Status:     appsv1.StatefulSetStatus{Replicas: 1, ReadyReplicas: 1},
			}
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(ss, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}},
				Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Name: "game", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}},
			}
			if contract == "verified" {
				pod.Annotations = map[string]string{templateRevisionAnnotation: oldRevision}
			}
			for _, obj := range []client.Object{ss, pod} {
				if err := r.Create(ctx, obj); err != nil {
					t.Fatal(err)
				}
			}
			stopper := &revisionStopper{}
			r.AgentClient = stopper
			original := ss.Spec.Template.DeepCopy()
			key := client.ObjectKeyFromObject(gs)
			step := func() {
				t.Helper()
				if err := r.Get(ctx, key, gs); err != nil {
					t.Fatal(err)
				}
				tmpl, changed, err := r.reconcileTemplateRevision(ctx, gs)
				if err != nil {
					t.Fatal(err)
				}
				if !changed {
					replicas, _, err := r.desiredReplicas(ctx, gs, tmpl, idleAwake)
					if err != nil {
						t.Fatal(err)
					}
					if err := r.reconcileStatefulSet(ctx, gs, tmpl, nil, &materializedConfig{}, replicas); err != nil {
						t.Fatal(err)
					}
				}
				if err := r.Get(ctx, key, ss); err != nil {
					t.Fatal(err)
				}
			}
			for range 4 {
				step()
				if !reflect.DeepEqual(*original, ss.Spec.Template) {
					t.Fatal("worker policy replaced the live legacy Pod template before drain")
				}
			}
			if contract == "verified" {
				if stopper.calls != 1 || *ss.Spec.Replicas != 1 {
					t.Fatalf("expected one verified stop and a running game during save: calls=%d replicas=%d", stopper.calls, *ss.Spec.Replicas)
				}
			} else if stopper.calls != 0 || *ss.Spec.Replicas != 0 {
				t.Fatalf("expected Kubernetes termination without catalog commands: calls=%d replicas=%d", stopper.calls, *ss.Spec.Replicas)
			}
			ss.Status.ReadyReplicas = 0
			if err := r.Status().Update(ctx, ss); err != nil {
				t.Fatal(err)
			}
			step()
			ss.Status.Replicas, ss.Status.ObservedGeneration = 0, ss.Generation
			if err := r.Status().Update(ctx, ss); err != nil {
				t.Fatal(err)
			}
			step()
			if *ss.Spec.Replicas != 0 || !reflect.DeepEqual(*original, ss.Spec.Template) {
				t.Fatal("zero replicas did not fence the still-present legacy Pod")
			}
			if contract == "previously pinned baseline" {
				// The StatefulSet can disappear while its old Pod still owns the
				// world. Race the owner with a stale scale-up decision on recreate.
				if err := r.Delete(ctx, ss); err != nil {
					t.Fatal(err)
				}
				stale := gs.DeepCopy()
				delete(stale.Annotations, lifecycleOperationAnnotation)
				if err := r.reconcileStatefulSet(ctx, stale, catalog, nil, &materializedConfig{}, 1); err != nil {
					t.Fatal(err)
				}
				for range 3 {
					if err := r.Get(ctx, key, ss); err != nil {
						t.Fatal(err)
					}
					if *ss.Spec.Replicas != 0 {
						t.Fatalf("replacement escaped the baseline fence while the old writer survived: replicas=%d", *ss.Spec.Replicas)
					}
					step()
				}
			}
			if err := r.Delete(ctx, pod); err != nil {
				t.Fatal(err)
			}
			for range 5 {
				step()
			}
			selector := ss.Spec.Template.Spec.NodeSelector
			if contract == "template architecture only" && selector[corev1.LabelArchStable] != "amd64" {
				t.Fatalf("replacement lacks the required architecture: %v", selector)
			}
			if *ss.Spec.Replicas != 1 || (r.GameWorkloadPolicy != nil && selector[policy.PoolLabel] != policy.PoolValue) ||
				!validTemplateRevision(ss.Spec.Template.Annotations[templateRevisionAnnotation]) || ss.Spec.Template.Spec.Containers[0].Image != catalog.Spec.Image {
				t.Fatal("drained workload did not resume on its policy-compliant revision")
			}
		})
	}
}
