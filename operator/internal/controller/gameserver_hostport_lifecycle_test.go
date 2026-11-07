package controller

import (
	"reflect"
	"slices"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestHostportEndpointLossAllowsAdmittedRolloutDrain(t *testing.T) {
	for _, state := range []string{"missing", "replaced", "deleting"} {
		t.Run(state, func(t *testing.T) {
			var r *GameServerReconciler
			var gs *gameplanev1alpha1.GameServer
			var tmpl *gameplanev1alpha1.GameTemplate
			stopper := &lifecycleStopper{}
			if state == "deleting" {
				r, gs, tmpl, stopper = hostportStopFixture(t)
			} else {
				var node *corev1.Node
				if state == "replaced" {
					node = hostportIdentityNode("replacement-node", corev1.ConditionTrue)
				}
				r, gs, tmpl = hostportIdentityFixture(t, node, corev1.PodRunning, nil)
				r.AgentClient = stopper
			}
			tmpl.Name, tmpl.Spec.Image = "hostport-rollout", "example.invalid/game:original"
			old, err := r.saveTemplateSnapshot(t.Context(), gs, tmpl)
			if err != nil {
				t.Fatal(err)
			}
			target := tmpl.DeepCopy()
			target.Spec.Image = "example.invalid/game:admitted"
			if err := r.Create(t.Context(), target); err != nil {
				t.Fatal(err)
			}
			revision, err := r.saveTemplateSnapshot(t.Context(), gs, target)
			if err != nil {
				t.Fatal(err)
			}
			op := lifecycleOperation{lifecycleIdentity: lifecycleIdentity{Kind: "Rollout", Name: gs.Name, UID: gs.UID, TargetUID: gs.UID, Token: revision}}
			gs.Spec.TemplateRef.Name = target.Name
			gs.Annotations[effectiveTemplateAnnotation] = old
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			ss := hostportStopSet(t, r, gs)
			ss.Annotations = map[string]string{lifecycleOperationAnnotation: "stale-owner", "example.invalid/preserve": "metadata"}
			if err := r.Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			// Even a stale healthy plan cannot start a replacement listener.
			if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
				t.Fatal(err)
			}
			ss = hostportStopSet(t, r, gs)
			if ss.Annotations[lifecycleOperationAnnotation] != gs.Annotations[lifecycleOperationAnnotation] || ss.Annotations["example.invalid/preserve"] != "metadata" {
				t.Fatal("endpoint stop did not project the admitted owner while preserving metadata")
			}
			if state == "deleting" {
				if *ss.Spec.Replicas != 1 || ss.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType || !slices.ContainsFunc(ss.Spec.Template.Spec.SchedulingGates, func(g corev1.PodSchedulingGate) bool { return g.Name == hostportStopSchedulingGate }) || stopper.calls != 0 {
					t.Fatal("owner projection bypassed the durable graceful-stop fence")
				}
				ss.Status.ObservedGeneration = ss.Generation
				if err := r.Status().Update(t.Context(), ss); err != nil {
					t.Fatal(err)
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
				ss = hostportStopSet(t, r, gs)
			}
			if *ss.Spec.Replicas != 0 {
				t.Fatal("invalid endpoint retained replacement permission after stop")
			}
			ss.Status.Replicas = 0
			ss.Status.ObservedGeneration = ss.Generation
			if err := r.Status().Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			if drained, err := r.templateWorkloadDrained(t.Context(), gs); err != nil || drained {
				t.Fatalf("a surviving Pod was not a rollout drain barrier: drained=%t err=%v", drained, err)
			}
			if err := r.Delete(t.Context(), &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace}}); err != nil {
				t.Fatal(err)
			}
			if drained, err := r.templateWorkloadDrained(t.Context(), gs); err != nil || !drained {
				t.Fatalf("observed zero and actual Pod absence did not finish drain: drained=%t err=%v", drained, err)
			}
			if _, changed, err := r.reconcileTemplateRevision(t.Context(), gs); err != nil || !changed || gs.Annotations[effectiveTemplateAnnotation] != revision {
				t.Fatalf("admitted rollout remained stuck after endpoint-loss drain: changed=%t err=%v", changed, err)
			}
		})
	}
}

func TestHostportIdentityStopProjectsOnlyExplicitOwnerMetadata(t *testing.T) {
	for _, owner := range []string{"", "malformed-owner"} {
		t.Run(owner, func(t *testing.T) {
			r, gs, tmpl := hostportIdentityFixture(t, nil, corev1.PodRunning, nil)
			stopper := &lifecycleStopper{}
			r.AgentClient = stopper
			gs.Annotations[backupGuardAnnotation] = backupIdentityJSON("legacy", "legacy-uid")
			if owner != "" {
				gs.Annotations[lifecycleOperationAnnotation] = owner
			}
			if err := r.Update(t.Context(), gs); err != nil {
				t.Fatal(err)
			}
			ss := hostportStopSet(t, r, gs)
			ss.Annotations = map[string]string{lifecycleOperationAnnotation: "stale-owner", "example.invalid/preserve": "metadata"}
			original := ss.Spec.Template.DeepCopy()
			if err := r.Update(t.Context(), ss); err != nil {
				t.Fatal(err)
			}
			if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
				t.Fatal(err)
			}
			ss = hostportStopSet(t, r, gs)
			_, present := ss.Annotations[lifecycleOperationAnnotation]
			if ss.Annotations[lifecycleOperationAnnotation] != owner || present != (owner != "") || ss.Annotations["example.invalid/preserve"] != "metadata" || !reflect.DeepEqual(ss.Spec.Template, *original) {
				t.Fatal("identity stop changed unrelated state or invented a legacy lifecycle annotation")
			}
			if *ss.Spec.Replicas != 0 || stopper.calls != 0 {
				t.Fatal("owner metadata parsing prevented identity-loss zero or issued a stop RPC")
			}
		})
	}
}
