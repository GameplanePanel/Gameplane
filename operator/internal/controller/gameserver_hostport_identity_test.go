package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func hostportIdentityFixture(t *testing.T, node *corev1.Node, phase corev1.PodPhase, operation *lifecycleOperation) (*GameServerReconciler, *gameplanev1alpha1.GameServer, *gameplanev1alpha1.GameTemplate) {
	t.Helper()
	r, gs, tmpl := hostportFixture(t)
	gs.Annotations = map[string]string{hostportNodeAnnotation: "node-a", hostportNodeUIDAnnotation: "original-node"}
	if operation != nil {
		operation.TargetUID = gs.UID
		gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(*operation)
	}
	one := int32(1)
	ss := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace, UID: "workload", OwnerReferences: []metav1.OwnerReference{{APIVersion: "gameplane.local/v1alpha1", Kind: "GameServer", Name: gs.Name, UID: gs.UID, Controller: ownerBoolPtr(true)}}}, Spec: appsv1.StatefulSetSpec{Replicas: &one, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"preserve": "template"}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "game", Image: "existing-image"}}}}}}
	objects := []client.Object{gs, ss}
	if node != nil {
		objects = append(objects, node)
	}
	if phase != "" {
		objects = append(objects, &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, Labels: map[string]string{"app.kubernetes.io/name": "gameplane-game", "app.kubernetes.io/instance": gs.Name}}, Spec: corev1.PodSpec{NodeName: "node-a"}, Status: corev1.PodStatus{Phase: phase}})
	}
	c := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(objects...).Build()
	r.Client, r.APIReader = c, c
	return r, gs, tmpl
}

func hostportIdentityNode(uid types.UID, ready corev1.ConditionStatus) *corev1.Node {
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", UID: uid}, Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}}}}
}

func TestHostportNodeIdentityFencesExistingReplacementPods(t *testing.T) {
	t.Parallel()
	deleting := hostportIdentityNode("original-node", corev1.ConditionTrue)
	now := metav1.Now()
	deleting.DeletionTimestamp, deleting.Finalizers = &now, []string{"test/hold"}
	for _, tc := range []struct {
		name string
		node *corev1.Node
	}{
		{"replaced", hostportIdentityNode("replacement-node", corev1.ConditionTrue)},
		{"missing", nil},
		{"deleting", deleting},
	} {
		for _, phase := range []corev1.PodPhase{corev1.PodPending, corev1.PodRunning} {
			t.Run(tc.name+"/"+string(phase), func(t *testing.T) {
				r, gs, tmpl := hostportIdentityFixture(t, tc.node, phase, nil)
				plan, err := r.planSentinel(t.Context(), gs, tmpl, idleAwake)
				if err != nil {
					t.Fatal(err)
				}
				if !plan.blockGame || plan.wantSentinel || plan.hostportCondition == nil || plan.hostportCondition.Reason != "EndpointNodeUnavailable" {
					t.Fatalf("replacement Pod bypassed endpoint identity: %+v", plan)
				}
			})
		}
	}
}

func TestHostportNotReadyPreservesOnlyAnExistingRunningGame(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		phase corev1.PodPhase
		block bool
	}{{corev1.PodRunning, false}, {corev1.PodPending, true}, {corev1.PodSucceeded, true}, {"", true}} {
		t.Run(string(tc.phase), func(t *testing.T) {
			r, gs, tmpl := hostportIdentityFixture(t, hostportIdentityNode("original-node", corev1.ConditionFalse), tc.phase, nil)
			plan, err := r.planSentinel(t.Context(), gs, tmpl, idleAwake)
			if err != nil || plan.blockGame != tc.block || plan.hostportCondition == nil || plan.hostportCondition.Reason != "EndpointNodeUnavailable" {
				t.Fatalf("NotReady plan = %+v, %v; want block=%v", plan, err, tc.block)
			}
		})
	}
}

func TestHostportIdentityFenceSurvivesLifecycleReplicaPreservation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		op   *lifecycleOperation
	}{
		{"ordinary", nil},
		{"backup", &lifecycleOperation{lifecycleIdentity: lifecycleIdentity{Kind: "Backup", Name: "backup", UID: "backup-uid"}}},
		{"restore-startup", &lifecycleOperation{lifecycleIdentity: lifecycleIdentity{Kind: "Restore", Name: "restore", UID: "restore-uid", InitialStartup: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, gs, tmpl := hostportIdentityFixture(t, hostportIdentityNode("original-node", corev1.ConditionFalse), corev1.PodRunning, tc.op)
			// A transient readiness loss must not stop an existing listener,
			// including while a backup owns its replica count.
			plan, err := r.planSentinel(t.Context(), gs, tmpl, idleAwake)
			if err != nil || plan.blockGame {
				t.Fatalf("same-node running listener was blocked: %+v %v", plan, err)
			}
			replicas, _, err := r.desiredReplicas(t.Context(), gs, tmpl, idleAwake)
			if err != nil || replicas != 1 {
				t.Fatalf("running listener replicas=%d, err=%v", replicas, err)
			}
			var original appsv1.StatefulSet
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &original); err != nil {
				t.Fatal(err)
			}
			// Node replacement races the prior plan and desiredReplicas read.
			// The mutation must recheck identity instead of trusting replicas=1.
			// Keep the cache's old Node available; only the uncached reader
			// sees the replacement that must govern the mutation.
			r.APIReader = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(gs, hostportIdentityNode("replacement-node", corev1.ConditionTrue)).Build()
			if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, replicas); err != nil {
				t.Fatal(err)
			}
			var current appsv1.StatefulSet
			if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &current); err != nil {
				t.Fatal(err)
			}
			if current.Spec.Replicas == nil || *current.Spec.Replicas != 0 || !reflect.DeepEqual(original.Spec.Template, current.Spec.Template) {
				t.Fatalf("endpoint loss did not stop the existing workload without changing its template: %+v", current.Spec)
			}
		})
	}
}

func TestHostportMutationKeepsRunningGameDuringNotReady(t *testing.T) {
	t.Parallel()
	r, gs, tmpl := hostportIdentityFixture(t, hostportIdentityNode("original-node", corev1.ConditionUnknown), corev1.PodRunning, nil)
	if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); err != nil {
		t.Fatal(err)
	}
	var ss appsv1.StatefulSet
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 1 {
		t.Fatalf("transient NotReady stopped an existing game: %+v", ss.Spec.Replicas)
	}
}

func TestHostportNodeReadErrorDoesNotMutateWorkload(t *testing.T) {
	t.Parallel()
	r, gs, tmpl := hostportIdentityFixture(t, hostportIdentityNode("original-node", corev1.ConditionTrue), corev1.PodRunning, nil)
	readErr := errors.New("API unavailable")
	r.APIReader = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
		if _, ok := object.(*corev1.Node); ok {
			return readErr
		}
		return cl.Get(ctx, key, object, options...)
	}})
	if err := r.reconcileStatefulSet(t.Context(), gs, tmpl, nil, &materializedConfig{}, 1); !errors.Is(err, readErr) {
		t.Fatalf("node read failure lost: %v", err)
	}
	var ss appsv1.StatefulSet
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); err != nil {
		t.Fatal(err)
	}
	if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 1 || ss.Spec.Template.Spec.Containers[0].Image != "existing-image" {
		t.Fatalf("failed node read changed the workload: %+v", ss.Spec)
	}
}
