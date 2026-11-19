package controller

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestReplaceServiceForClassChange — only a controlled LoadBalancer Service
// whose class differs from the requested one, or which has none, is deleted;
// every other case keeps the Service.
func TestReplaceServiceForClassChange(t *testing.T) {
	first, second := "example.com/first", "example.com/second"
	lbService := func(class *string) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "games"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: class},
		}
	}
	cases := []struct {
		name       string
		expose     string
		want       *string
		svc        *corev1.Service
		controlled bool
		wantErr    error
		wantGone   bool
	}{
		{name: "unset class", expose: "LoadBalancer", svc: lbService(&first), controlled: true},
		{name: "not a LoadBalancer server", expose: "ClusterIP", want: &second, svc: lbService(&first), controlled: true},
		{name: "no Service yet", expose: "LoadBalancer", want: &second},
		{name: "same class", expose: "LoadBalancer", want: &first, svc: lbService(&first), controlled: true},
		{name: "Service without a class", expose: "LoadBalancer", want: &second, svc: lbService(nil), controlled: true, wantErr: errServiceReplaced, wantGone: true},
		{name: "ClusterIP Service", expose: "LoadBalancer", want: &second, svc: &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "games"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP},
		}, controlled: true},
		{name: "Service owned by something else", expose: "LoadBalancer", want: &second, svc: lbService(&first)},
		{name: "changed class", expose: "LoadBalancer", want: &second, svc: lbService(&first), controlled: true, wantErr: errServiceReplaced, wantGone: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := wipeScheme(t)
			gs := &gameplanev1alpha1.GameServer{
				ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "games", UID: "server-uid"},
				Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{
					Expose: tc.expose, LoadBalancerClass: tc.want,
				}},
			}
			builder := fake.NewClientBuilder().WithScheme(s).WithObjects(gs)
			if tc.svc != nil {
				if tc.controlled {
					if err := controllerutil.SetControllerReference(gs, tc.svc, s); err != nil {
						t.Fatal(err)
					}
				}
				builder = builder.WithObjects(tc.svc)
			}
			c := builder.Build()
			r := &GameServerReconciler{Client: c, Scheme: s}
			if err := r.replaceServiceForClassChange(t.Context(), gs); !errors.Is(err, tc.wantErr) {
				t.Fatalf("got error %v, want %v", err, tc.wantErr)
			}
			if tc.svc == nil {
				return
			}
			err := c.Get(t.Context(), client.ObjectKeyFromObject(tc.svc), &corev1.Service{})
			if gone := apierrors.IsNotFound(err); gone != tc.wantGone {
				t.Fatalf("Service deleted = %v, want %v (get error %v)", gone, tc.wantGone, err)
			}
		})
	}
}

// TestReplaceServiceForClassChange_Deleting — a Service already being deleted
// asks for a requeue without another delete, so the new Service waits for it.
func TestReplaceServiceForClassChange_Deleting(t *testing.T) {
	first, second := "example.com/first", "example.com/second"
	s := wipeScheme(t)
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "games", UID: "server-uid"},
		Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{
			Expose: "LoadBalancer", LoadBalancerClass: &second,
		}},
	}
	now := metav1.Now()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: "server", Namespace: "games",
			DeletionTimestamp: &now, Finalizers: []string{"service.kubernetes.io/load-balancer-cleanup"},
		},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &first},
	}
	deletes := 0
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, svc).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			deletes++
			return cl.Delete(ctx, obj, opts...)
		},
	}).Build()
	r := &GameServerReconciler{Client: c, Scheme: s}
	if err := r.replaceServiceForClassChange(t.Context(), gs); !errors.Is(err, errServiceReplaced) {
		t.Fatalf("got error %v, want errServiceReplaced", err)
	}
	if deletes != 0 {
		t.Fatalf("deleted a Service that was already being deleted (%d deletes)", deletes)
	}
}

// TestReplaceServiceForClassChange_APIErrors — a failed read or delete is
// returned, never mistaken for a missing or replaced Service.
func TestReplaceServiceForClassChange_APIErrors(t *testing.T) {
	first, second := "example.com/first", "example.com/second"
	boom := apierrors.NewServiceUnavailable("apiserver down")
	for _, tc := range []struct {
		name  string
		funcs interceptor.Funcs
	}{
		{name: "get", funcs: interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*corev1.Service); ok {
					return boom
				}
				return cl.Get(ctx, key, obj, opts...)
			},
		}},
		{name: "delete", funcs: interceptor.Funcs{
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				return boom
			},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := wipeScheme(t)
			gs := &gameplanev1alpha1.GameServer{
				ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "games", UID: "server-uid"},
				Spec: gameplanev1alpha1.GameServerSpec{Networking: gameplanev1alpha1.GameServerNetworking{
					Expose: "LoadBalancer", LoadBalancerClass: &second,
				}},
			}
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: "server", Namespace: "games"},
				Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, LoadBalancerClass: &first},
			}
			if err := controllerutil.SetControllerReference(gs, svc, s); err != nil {
				t.Fatal(err)
			}
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, svc).WithInterceptorFuncs(tc.funcs).Build()
			r := &GameServerReconciler{Client: c, Scheme: s}
			err := r.replaceServiceForClassChange(t.Context(), gs)
			if err == nil || errors.Is(err, errServiceReplaced) || !apierrors.IsServiceUnavailable(err) {
				t.Fatalf("got error %v, want the API error", err)
			}
		})
	}
}
