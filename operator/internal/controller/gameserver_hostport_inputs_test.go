package controller

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestHostportStatefulSetMutationRejectsChangedEndpointInputs(t *testing.T) {
	for _, change := range []string{"endpoint-name", "endpoint-uid", "clear-pin", "new-pin", "leave-hostport", "enter-hostport"} {
		for _, existing := range []bool{false, true} {
			name := change + "/new"
			if existing {
				name = change + "/existing"
			}
			t.Run(name, func(t *testing.T) {
				r, planned, tmpl := hostportIdentityFixture(t, hostportIdentityNode("original-node", corev1.ConditionTrue), "", nil)
				planned.Annotations[effectiveTemplateAnnotation] = strings.Repeat("a", 64)
				if change == "new-pin" || change == "enter-hostport" {
					delete(planned.Annotations, hostportNodeAnnotation)
					delete(planned.Annotations, hostportNodeUIDAnnotation)
				}
				if change == "enter-hostport" {
					planned.Spec.Networking.Expose = "NodePort"
				}
				if err := r.Update(t.Context(), planned); err != nil {
					t.Fatal(err)
				}
				original := hostportStopSet(t, r, planned)
				zero := int32(0)
				original.Spec.Replicas = &zero
				original.Spec.Template.Spec.Affinity = hostportAffinity(planned)
				if node := planned.Annotations[hostportNodeAnnotation]; node != "" {
					original.Spec.Template.Annotations[hostportNodeAnnotation] = node
				}
				if err := r.Update(t.Context(), original); err != nil {
					t.Fatal(err)
				}
				if !existing {
					if err := r.Delete(t.Context(), original); err != nil {
						t.Fatal(err)
					}
				}
				// The concurrent edit changes only endpoint inputs, keeping the
				// server UID, effective revision and child resourceVersion intact.
				live := planned.DeepCopy()
				live.ResourceVersion = "999"
				switch change {
				case "endpoint-name", "new-pin", "enter-hostport":
					live.Spec.Networking.Expose = "Hostport"
					live.Annotations[hostportNodeAnnotation] = "node-b"
					live.Annotations[hostportNodeUIDAnnotation] = "node-b-uid"
				case "endpoint-uid":
					live.Annotations[hostportNodeUIDAnnotation] = "replacement-node"
				case "clear-pin":
					delete(live.Annotations, hostportNodeAnnotation)
					delete(live.Annotations, hostportNodeUIDAnnotation)
				case "leave-hostport":
					live.Spec.Networking.Expose = "NodePort"
				}
				objects := []client.Object{live}
				wantNodeRead := live.Spec.Networking.Expose == "Hostport" && live.Annotations[hostportNodeAnnotation] != ""
				if wantNodeRead {
					node := hostportIdentityNode("", corev1.ConditionTrue)
					node.Name = live.Annotations[hostportNodeAnnotation]
					node.UID = types.UID(live.Annotations[hostportNodeUIDAnnotation])
					objects = append(objects, node)
				}
				reader := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(objects...).Build()
				nodeReads := 0
				r.APIReader = interceptor.NewClient(reader, interceptor.Funcs{Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*corev1.Node); ok {
						nodeReads++
					}
					return cl.Get(ctx, key, obj, opts...)
				}})
				if err := r.reconcileStatefulSet(t.Context(), planned, tmpl, nil, &materializedConfig{}, 1); err == nil || !strings.Contains(err.Error(), "hostport endpoint changed") {
					t.Fatalf("stale planned inputs were not rejected: %v", err)
				}
				if (nodeReads != 0) != wantNodeRead {
					t.Fatalf("live endpoint identity check reached=%t, want=%t", nodeReads != 0, wantNodeRead)
				}
				var current appsv1.StatefulSet
				err := r.Get(t.Context(), client.ObjectKeyFromObject(planned), &current)
				if !existing {
					if !apierrors.IsNotFound(err) {
						t.Fatalf("stale inputs created a workload: %v", err)
					}
				} else if err != nil || !reflect.DeepEqual(original.Spec, current.Spec) || original.ResourceVersion != current.ResourceVersion {
					t.Fatalf("stale inputs changed the existing workload: %v", err)
				}
			})
		}
	}
}

func TestHostportChangedInputsStillForceInvalidEndpointToZero(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "replaced"}[replaced], func(t *testing.T) {
			r, planned, tmpl := hostportIdentityFixture(t, hostportIdentityNode("original-node", corev1.ConditionTrue), "", nil)
			original := hostportStopSet(t, r, planned)
			live := planned.DeepCopy()
			live.ResourceVersion = "999"
			live.Annotations[hostportNodeAnnotation], live.Annotations[hostportNodeUIDAnnotation] = "node-b", "node-b-uid"
			objects := []client.Object{live}
			if replaced {
				node := hostportIdentityNode("untrusted-replacement", corev1.ConditionTrue)
				node.Name = "node-b"
				objects = append(objects, node)
			}
			r.APIReader = fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(objects...).Build()
			if err := r.reconcileStatefulSet(t.Context(), planned, tmpl, nil, &materializedConfig{}, 1); err != nil {
				t.Fatal(err)
			}
			current := hostportStopSet(t, r, planned)
			if *current.Spec.Replicas != 0 || !reflect.DeepEqual(original.Spec.Template, current.Spec.Template) {
				t.Fatal("endpoint input mismatch prevented identity-loss zero or changed the template")
			}
		})
	}
}

func TestHostportChangedInputsPreserveDeletingNodeGrace(t *testing.T) {
	r, live, tmpl, stopper := hostportStopFixture(t)
	planned := live.DeepCopy()
	planned.Annotations[hostportNodeUIDAnnotation] = "stale-planned-uid"
	for pass := range 2 {
		if err := r.reconcileStatefulSet(t.Context(), planned, tmpl, nil, &materializedConfig{}, 1); err != nil {
			t.Fatal(err)
		}
		ss := hostportStopSet(t, r, planned)
		if *ss.Spec.Replicas != 1 || ss.Spec.UpdateStrategy.Type != appsv1.OnDeleteStatefulSetStrategyType || !slices.ContainsFunc(ss.Spec.Template.Spec.SchedulingGates, func(g corev1.PodSchedulingGate) bool { return g.Name == hostportStopSchedulingGate }) || stopper.calls != pass {
			t.Fatalf("pass %d: changed inputs bypassed the durable fence or bounded stop", pass)
		}
		ss.Status.ObservedGeneration = ss.Generation
		if err := r.Status().Update(t.Context(), ss); err != nil {
			t.Fatal(err)
		}
	}
}
