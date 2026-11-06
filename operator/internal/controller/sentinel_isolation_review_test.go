package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

type isolationDrainReader struct {
	client.Reader
	workloadGets, podLists int
}

func (r *isolationDrainReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	switch obj.(type) {
	case *appsv1.StatefulSet, *appsv1.Deployment:
		r.workloadGets++
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}
func (r *isolationDrainReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*corev1.PodList); ok {
		r.podLists++
	}
	return r.Reader.List(ctx, list, opts...)
}

func TestSentinelOptOutWithoutOwnedGrantsSkipsLiveDrainReads(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		s := testScheme(t)
		_ = networkingv1.AddToScheme(s)
		c := fake.NewClientBuilder().WithScheme(s).Build()
		gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: "original"}}
		if foreign {
			if err := c.Create(t.Context(), &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "alpha-sentinel-ingress", Namespace: gs.Namespace}}); err != nil {
				t.Fatal(err)
			}
		}
		reader := &isolationDrainReader{Reader: c}
		r := &GameServerReconciler{Client: c, APIReader: reader, Scheme: s}
		if err := r.reconcileSentinelIsolation(t.Context(), gs, singlePortTemplate(false, "none")); err != nil {
			t.Fatal(err)
		}
		if reader.workloadGets != 0 || reader.podLists != 0 {
			t.Fatal("settled opt-out performed live workload drainage reads")
		}
	}
}

func TestSentinelOptOutCleansGrantsAfterDelayedCacheVisibility(t *testing.T) {
	s := testScheme(t)
	_ = networkingv1.AddToScheme(s)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: "original"}}
	tmpl := singlePortTemplate(true, "minecraft")
	tmpl.Spec.SentinelIsolation = true
	r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
	if err := r.reconcileSentinelIsolation(t.Context(), gs, tmpl); err != nil {
		t.Fatal(err)
	}
	hidden := true
	r.Client = interceptor.NewClient(c, interceptor.Funcs{Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, ok := obj.(*networkingv1.NetworkPolicy); ok && hidden {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "networkpolicies"}, key.Name)
		}
		return cl.Get(ctx, key, obj, opts...)
	}})
	reader := &isolationDrainReader{Reader: c}
	r.APIReader = reader
	tmpl.Spec.SentinelIsolation = false
	if err := r.reconcileSentinelIsolation(t.Context(), gs, tmpl); err != nil {
		t.Fatal(err)
	}
	if reader.workloadGets != 0 || reader.podLists != 0 {
		t.Fatal("cached absence did not take fast path")
	}
	// The owned NetworkPolicy watch enqueues after the informer makes its
	// delayed grant visible. The next pass must perform the live safety proof.
	hidden = false
	if err := r.reconcileSentinelIsolation(t.Context(), gs, tmpl); err != nil {
		t.Fatal(err)
	}
	if reader.workloadGets != 2 || reader.podLists != 1 {
		t.Fatalf("visible grants bypassed live drainage proof: gets=%d lists=%d", reader.workloadGets, reader.podLists)
	}
	var policies networkingv1.NetworkPolicyList
	if err := c.List(t.Context(), &policies); err != nil || len(policies.Items) != 0 {
		t.Fatalf("delayed grants were never cleaned: %v %+v", err, policies.Items)
	}
}

func TestInvalidIsolatedTemplateRevokesOnlyOwnedExposure(t *testing.T) {
	for _, mode := range []string{"owned", "foreign", "retryable-delete"} {
		t.Run(mode, func(t *testing.T) {
			s := testScheme(t)
			_ = networkingv1.AddToScheme(s)
			_ = batchv1.AddToScheme(s)
			gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: "games", UID: "original"}}
			tmpl := singlePortTemplate(false, "none")
			tmpl.Name, tmpl.Spec.SentinelIsolation = "isolated", true
			gs.Spec.TemplateRef.Name = tmpl.Name
			owner := *metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))
			if mode == "foreign" {
				owner.UID = "foreign-server"
			}
			var exposures []client.Object
			for _, name := range []string{gameIngressPolicyName(gs), gs.Name + "-sentinel-ingress", gs.Name + "-sentinel-egress"} {
				exposures = append(exposures, &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace, UID: types.UID(name), OwnerReferences: []metav1.OwnerReference{owner}}})
			}
			for _, name := range []string{gs.Name, gs.Name + "-game-direct"} {
				exposures = append(exposures, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace, UID: types.UID(name), OwnerReferences: []metav1.OwnerReference{owner}}})
			}
			one := int32(1)
			workload := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace}, Spec: appsv1.StatefulSetSpec{Replicas: &one}}
			agent := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-agent", Namespace: gs.Namespace, OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(gs, gameplanev1alpha1.GroupVersion.WithKind("GameServer"))}}}
			objects := append([]client.Object{gs, tmpl, workload, agent}, exposures...)
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).WithStatusSubresource(gs).Build()
			deleteErr := errors.New("retryable deletion error")
			cached := interceptor.NewClient(c, interceptor.Funcs{
				Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					switch obj.(type) {
					case *networkingv1.NetworkPolicy, *corev1.Service:
						return apierrors.NewNotFound(schema.GroupResource{Resource: "hidden-exposure"}, key.Name)
					}
					return cl.Get(ctx, key, obj, opts...)
				},
				Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if mode == "retryable-delete" && obj.GetName() == gameIngressPolicyName(gs) {
						return deleteErr
					}
					options := (&client.DeleteOptions{}).ApplyOptions(opts)
					if options.Preconditions == nil || options.Preconditions.UID == nil || options.Preconditions.ResourceVersion == nil {
						t.Fatal("exposure deletion lacks UID/resourceVersion guard")
					}
					return cl.Delete(ctx, obj, opts...)
				},
			})
			r := &GameServerReconciler{Client: cached, APIReader: c, Scheme: s}
			_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)})
			if mode == "retryable-delete" && !errors.Is(err, deleteErr) || mode != "retryable-delete" && err != nil {
				t.Fatalf("cleanup error=%v", err)
			}
			for _, obj := range exposures {
				err := c.Get(t.Context(), client.ObjectKeyFromObject(obj), obj)
				keep := mode == "foreign" || (mode == "retryable-delete" && obj.GetName() == gameIngressPolicyName(gs))
				if keep && err != nil || !keep && !apierrors.IsNotFound(err) {
					t.Fatalf("exposure %s retained=%t error=%v", obj.GetName(), keep, err)
				}
			}
			var current appsv1.StatefulSet
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(workload), &current); err != nil || !reflect.DeepEqual(current.Spec, workload.Spec) {
				t.Fatalf("invalid template mutated workload: %v", err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(agent), agent); err != nil {
				t.Fatal("invalid template removed agent Service", err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), gs); err != nil {
				t.Fatal(err)
			}
			if gs.Status.Phase != gameplanev1alpha1.GameServerPhaseFailed {
				t.Fatal("cleanup error concealed invalid-template failure")
			}
		})
	}
}
