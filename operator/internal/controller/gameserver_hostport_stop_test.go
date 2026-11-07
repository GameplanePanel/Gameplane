package controller

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func hostportStopFixture(t *testing.T) (*GameServerReconciler, *gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate, *lifecycleStopper) {
	t.Helper()
	node := hostportIdentityNode("original-node", corev1.ConditionTrue)
	now := metav1.Now()
	node.DeletionTimestamp, node.Finalizers = &now, []string{"test/hold"}
	r, gs, tmpl := hostportIdentityFixture(t, node, corev1.PodRunning, nil)
	grace := int32(60)
	gs.Spec.StopGracePeriodSeconds = &grace
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source"}
	tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{Lifecycle: &gameplanev1alpha1.LifecycleSpec{Stop: []string{"save", "stop"}}}
	var ss appsv1.StatefulSet
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	ss.Generation = 1
	ss.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.RollingUpdateStatefulSetStrategyType}
	if err := r.Update(t.Context(), &ss); err != nil {
		t.Fatal(err)
	}
	ss.Status.ReadyReplicas = 1
	if err := r.Status().Update(t.Context(), &ss); err != nil {
		t.Fatal(err)
	}
	var pod corev1.Pod
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod); err != nil {
		t.Fatal(err)
	}
	pod.UID = "original-pod"
	pod.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(&ss, appsv1.SchemeGroupVersion.WithKind("StatefulSet"))}
	if err := r.Update(t.Context(), &pod); err != nil {
		t.Fatal(err)
	}
	stopper := &lifecycleStopper{}
	r.AgentClient = stopper
	return r, gs, tmpl, stopper
}

func hostportStopSet(t *testing.T, r *GameServerReconciler, gs *gameplanev1alpha1.GameServer) *appsv1.StatefulSet {
	t.Helper()
	var ss appsv1.StatefulSet
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	return &ss
}

func TestHostportDeletingOriginalNodeFencesReplacementsBeforeGracefulStop(t *testing.T) {
	r, gs, tmpl, stopper := hostportStopFixture(t)
	original := hostportStopSet(t, r, gs).Spec.Template.DeepCopy()
	for pass := range 3 {
		if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
			t.Fatal(err)
		}
		// replicas=0 models an already-blocked plan. The same mutation gate
		// must also protect a stale healthy plan passing replicas=1.
		replicas := int32(0)
		if pass == 0 {
			replicas = 1
		}
		if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, replicas); err != nil {
			t.Fatal(err)
		}
		ss := hostportStopSet(t, r, gs)
		if *ss.Spec.Replicas != 1 || ss.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType || !slices.ContainsFunc(ss.Spec.Template.Spec.SchedulingGates, func(g corev1.PodSchedulingGate) bool { return g.Name == hostportStopSchedulingGate }) {
			t.Fatal("graceful stop lost original Pod or replacement scheduling fence")
		}
		withoutGate := ss.Spec.Template.DeepCopy()
		withoutGate.Spec.SchedulingGates = nil
		if !reflect.DeepEqual(original, withoutGate) {
			t.Fatal("endpoint drain changed the running game's template")
		}
		wantCalls := 1
		if pass == 0 {
			wantCalls = 0
		}
		if stopper.calls != wantCalls {
			t.Fatalf("pass %d: stop calls=%d, want=%d", pass, stopper.calls, wantCalls)
		}
		if pass == 0 {
			ss.Status.ObservedGeneration = ss.Generation
			if err := r.Status().Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
		}
		// Recreate the reconciler: durable gate and grace timestamp must be
		// sufficient to continue without another stop command.
		r = &GameServerReconciler{Client: r.Client, APIReader: r.APIReader, Scheme: r.Scheme, AgentClient: stopper}
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
		t.Fatal(err)
	}
	gs.Annotations[stopRequestedAtAnnotation] = time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 0); err != nil {
		t.Fatal(err)
	}
	if *hostportStopSet(t, r, gs).Spec.Replicas != 0 {
		t.Fatal("grace deadline did not stop original workload")
	}
}

func TestHostportStopFenceRejectsReplacementPodAndRestoresAfterMigration(t *testing.T) {
	r, gs, tmpl, _ := hostportStopFixture(t)
	originalSet := hostportStopSet(t, r, gs)
	originalSet.Spec.Template.Spec.SchedulingGates = []corev1.PodSchedulingGate{{Name: "example.invalid/existing-gate"}}
	if err := r.Update(t.Context(), originalSet); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	var pod corev1.Pod
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod); err != nil {
		t.Fatal(err)
	}
	pod.UID = "replacement-pod"
	if err := r.Update(t.Context(), &pod); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	if *hostportStopSet(t, r, gs).Spec.Replicas != 0 {
		t.Fatal("replacement Pod inherited original Pod's save window")
	}
	// Explicit endpoint migration follows the documented stopped/drained
	// procedure. Ordinary rendering removes only our temporary scheduling gate.
	if err := r.Delete(t.Context(), &pod); err != nil {
		t.Fatal(err)
	}
	delete(gs.Annotations, hostportNodeAnnotation)
	delete(gs.Annotations, hostportNodeUIDAnnotation)
	if err := r.Update(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 0); err != nil {
		t.Fatal(err)
	}
	ss := hostportStopSet(t, r, gs)
	if ss.Annotations[hostportStopFenceAnnotation] != "" || ss.Spec.UpdateStrategy.Type != appsv1.RollingUpdateStatefulSetStrategyType || !reflect.DeepEqual(ss.Spec.Template.Spec.SchedulingGates, originalSet.Spec.Template.Spec.SchedulingGates) {
		t.Fatal("migration retained temporary drain fence")
	}
}

func TestHostportStopWaitsForFenceObservation(t *testing.T) {
	r, gs, tmpl, stopper := hostportStopFixture(t)
	ss := hostportStopSet(t, r, gs)
	ss.Generation = 9
	if err := r.Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	ss.Status.ObservedGeneration = 8
	if err := r.Status().Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
			t.Fatal(err)
		}
	}
	if stopper.calls != 0 || *hostportStopSet(t, r, gs).Spec.Replicas != 1 {
		t.Fatal("stop started before replacement fence was observed")
	}
	ss = hostportStopSet(t, r, gs)
	ss.Status.ObservedGeneration = ss.Generation
	if err := r.Status().Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	if stopper.calls != 1 {
		t.Fatal("observed replacement fence did not permit graceful stop")
	}
}

func TestHostportValidEndpointRestoresFencedStrategy(t *testing.T) {
	r, gs, tmpl, _ := hostportStopFixture(t)
	if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	// A fresh healthy endpoint read governs cleanup, independently of the
	// previous cached deleting-node plan.
	r.APIReader = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(gs, hostportIdentityNode("original-node", corev1.ConditionTrue)).Build()
	if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	ss := hostportStopSet(t, r, gs)
	if ss.Annotations[hostportStopFenceAnnotation] != "" || ss.Spec.UpdateStrategy.Type != appsv1.RollingUpdateStatefulSetStrategyType || slices.ContainsFunc(ss.Spec.Template.Spec.SchedulingGates, func(g corev1.PodSchedulingGate) bool { return g.Name == hostportStopSchedulingGate }) {
		t.Fatal("valid endpoint retained temporary stop fence")
	}
}

func TestHostportDeletingNodeDoesNotGrantGraceToUntrustedPod(t *testing.T) {
	for _, mode := range []string{"pending", "unowned", "other-node"} {
		t.Run(mode, func(t *testing.T) {
			r, gs, tmpl, stopper := hostportStopFixture(t)
			var pod corev1.Pod
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "pending":
				pod.Status.Phase = corev1.PodPending
			case "unowned":
				pod.OwnerReferences = nil
			case "other-node":
				pod.Spec.NodeName = "node-b"
			}
			if err := r.Update(t.Context(), &pod); err != nil {
				t.Fatal(err)
			}
			if mode == "pending" {
				pod.Status.Phase = corev1.PodPending
				if err := r.Status().Update(t.Context(), &pod); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
				t.Fatal(err)
			}
			if *hostportStopSet(t, r, gs).Spec.Replicas != 0 || stopper.calls != 0 {
				t.Fatal("untrusted Pod bypassed endpoint identity fence")
			}
		})
	}
}

func TestHostportDeletingNodeWaitsForCopyOwnerBeforeSaveStop(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "backup", true: "recovery"}[recovery], func(t *testing.T) {
			r, gs, tmpl, stopper := hostportStopFixture(t)
			op := lifecycleOperation{lifecycleIdentity: lifecycleIdentity{Kind: "Backup", Name: "copy", UID: "copy-uid", TargetUID: gs.UID}}
			if recovery {
				op.Kind, op.Name, op.UID = "Wipe", gs.Name, gs.UID
				op.Token = "wipe"
				op.Recovery = []lifecycleIdentity{{Kind: "Backup", Name: "old-copy", UID: "old-copy-uid", TargetUID: gs.UID}}
			}
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 0); err != nil {
				t.Fatal(err)
			}
			ss := hostportStopSet(t, r, gs)
			ss.Status.ObservedGeneration = ss.Generation
			if err := r.Status().Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 0); err != nil {
				t.Fatal(err)
			}
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			if stopper.calls != 0 || gs.Annotations[stopRequestedAtAnnotation] != "" || *hostportStopSet(t, r, gs).Spec.Replicas != 1 {
				t.Fatal("endpoint stop RPC overlapped copy/save-off ownership")
			}
			delete(gs.Annotations, lifecycleOperationAnnotation)
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 0); err != nil {
				t.Fatal(err)
			}
			if stopper.calls != 1 {
				t.Fatal("copy cleanup did not permit graceful stop")
			}
		})
	}
}

func TestHostportMalformedStopFenceCannotGrantGrace(t *testing.T) {
	for _, raw := range []string{"malformed fence", "{}", "null"} {
		t.Run(raw, func(t *testing.T) {
			r, gs, tmpl, stopper := hostportStopFixture(t)
			ss := hostportStopSet(t, r, gs)
			ss.Annotations = map[string]string{hostportStopFenceAnnotation: raw}
			ss.Spec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
			ss.Spec.Template.Spec.SchedulingGates = []corev1.PodSchedulingGate{{Name: hostportStopSchedulingGate}}
			if err := r.Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			ss.Status.ObservedGeneration = ss.Generation
			if err := r.Status().Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
				t.Fatal(err)
			}
			if *hostportStopSet(t, r, gs).Spec.Replicas != 0 || stopper.calls != 0 {
				t.Fatal("malformed or incomplete fence authorized grace or a stop RPC")
			}
		})
	}
}

func TestHostportFenceCleanupRecoversInvalidMetadataWithoutGuessingStrategy(t *testing.T) {
	partition, unavailable := int32(3), intstr.FromInt32(1)
	saved := appsv1.StatefulSetUpdateStrategy{Type: appsv1.RollingUpdateStatefulSetStrategyType, RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: &partition, MaxUnavailable: &unavailable}}
	valid, err := json.Marshal(hostportStopFence{PodUID: "original-pod", NodeUID: "original-node", Strategy: saved})
	if err != nil {
		t.Fatal(err)
	}
	recreate := appsv1.StatefulSetUpdateStrategy{Type: appsv1.RecreateStatefulSetStrategyType}
	validRecreate, err := json.Marshal(hostportStopFence{PodUID: "original-pod", NodeUID: "original-node", Strategy: recreate})
	if err != nil {
		t.Fatal(err)
	}
	percentage := saved.DeepCopy()
	percent := intstr.FromString("25%")
	percentage.RollingUpdate.MaxUnavailable = &percent
	validPercentage, err := json.Marshal(hostportStopFence{PodUID: "original-pod", NodeUID: "original-node", Strategy: *percentage})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, raw string
		restore   *appsv1.StatefulSetUpdateStrategy
	}{
		{"empty-annotation", "", nil},
		{"missing-annotation", "", nil},
		{"malformed", "malformed fence", nil},
		{"empty-object", "{}", nil},
		{"null", "null", nil},
		{"missing-identities", `{"strategy":{"type":"RollingUpdate"}}`, nil},
		{"empty-strategy", `{"podUID":"original-pod","nodeUID":"original-node","strategy":{}}`, nil},
		{"unknown-strategy", `{"podUID":"original-pod","nodeUID":"original-node","strategy":{"type":"unknown"}}`, nil},
		{"on-delete-with-rolling-fields", `{"podUID":"original-pod","nodeUID":"original-node","strategy":{"type":"OnDelete","rollingUpdate":{}}}`, nil},
		{"negative-partition", `{"podUID":"original-pod","nodeUID":"original-node","strategy":{"type":"RollingUpdate","rollingUpdate":{"partition":-1}}}`, nil},
		{"zero-max-unavailable", `{"podUID":"original-pod","nodeUID":"original-node","strategy":{"type":"RollingUpdate","rollingUpdate":{"maxUnavailable":0}}}`, nil},
		{"invalid-max-unavailable", `{"podUID":"original-pod","nodeUID":"original-node","strategy":{"type":"RollingUpdate","rollingUpdate":{"maxUnavailable":"invalid"}}}`, nil},
		{"excessive-max-unavailable-percent", `{"podUID":"original-pod","nodeUID":"original-node","strategy":{"type":"RollingUpdate","rollingUpdate":{"maxUnavailable":"101%"}}}`, nil},
		{"valid-custom-strategy", string(valid), &saved},
		{"valid-percentage-strategy", string(validPercentage), percentage},
		{"valid-recreate-strategy", string(validRecreate), &recreate},
	} {
		for _, hostport := range []bool{false, true} {
			for _, replicas := range []int32{0, 1} {
				t.Run(fmt.Sprintf("%s/hostport=%t/replicas=%d", test.name, hostport, replicas), func(t *testing.T) {
					r, gs, tmpl, _ := hostportStopFixture(t)
					if !hostport {
						gs.Spec.Networking.Expose = "NodePort"
						if err := r.Update(t.Context(), gs); err != nil {
							t.Fatal(err)
						}
					}
					r.APIReader = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(gs, hostportIdentityNode("original-node", corev1.ConditionTrue)).Build()
					ss := hostportStopSet(t, r, gs)
					ss.Annotations = map[string]string{"example.invalid/retained": "value"}
					if test.name != "missing-annotation" {
						ss.Annotations[hostportStopFenceAnnotation] = test.raw
					}
					current := appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
					ss.Spec.UpdateStrategy = current
					otherGate := corev1.PodSchedulingGate{Name: "example.invalid/retained"}
					ss.Spec.Template.Spec.SchedulingGates = []corev1.PodSchedulingGate{otherGate, {Name: hostportStopSchedulingGate}}
					if err := r.Update(t.Context(), ss); err != nil {
						t.Fatal(err)
					}
					if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, replicas); err != nil {
						t.Fatal(err)
					}
					ss = hostportStopSet(t, r, gs)
					want := current
					if test.restore != nil {
						want = *test.restore
					}
					if !reflect.DeepEqual(ss.Spec.UpdateStrategy, want) || ss.Spec.Replicas == nil || *ss.Spec.Replicas != replicas {
						t.Fatalf("cleanup blocked power intent or guessed strategy: %+v", ss.Spec)
					}
					_, fenceRemains := ss.Annotations[hostportStopFenceAnnotation]
					if !reflect.DeepEqual(ss.Spec.Template.Spec.SchedulingGates, []corev1.PodSchedulingGate{otherGate}) || fenceRemains || ss.Annotations["example.invalid/retained"] != "value" {
						t.Fatal("cleanup changed unrelated state or retained its fence")
					}
				})
			}
		}
	}
}
