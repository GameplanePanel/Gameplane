//go:build envtest

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestGameServer_LoadBalancerClass — the class reaches the Service, and a
// changed class replaces the Service, since Kubernetes doesn't allow
// changing it in place.
func TestGameServer_LoadBalancerClass(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	first, second := "example.com/first", "example.com/second"
	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Networking = gameplanev1alpha1.GameServerNetworking{
		Expose:            "LoadBalancer",
		LoadBalancerClass: &first,
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	key := types.NamespacedName{Namespace: ns, Name: "smp"}
	var firstUID types.UID
	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(), key, &svc); err != nil {
			return false, err.Error()
		}
		if svc.Spec.LoadBalancerClass == nil || *svc.Spec.LoadBalancerClass != first {
			return false, "class not set"
		}
		firstUID = svc.UID
		return true, ""
	})

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cur gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), key, &cur); err != nil {
			return err
		}
		cur.Spec.Networking.LoadBalancerClass = &second
		return k8sClient.Update(context.Background(), &cur)
	}); err != nil {
		t.Fatalf("update class: %v", err)
	}

	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(), key, &svc); err != nil {
			return false, err.Error()
		}
		if svc.UID == firstUID {
			return false, "Service not replaced yet"
		}
		if svc.Spec.LoadBalancerClass == nil || *svc.Spec.LoadBalancerClass != second {
			return false, "new Service lacks the new class"
		}
		return true, ""
	})
}

// TestGameServer_LoadBalancerClassUnsetKeepsTheService — a Service whose class
// was set by something else (here, directly) is kept when the GameServer sets
// none.
func TestGameServer_LoadBalancerClassUnsetKeepsTheService(t *testing.T) {
	ns := newNamespace(t)

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	class := "example.com/external"
	pre := &corev1.Service{}
	pre.Name, pre.Namespace = "smp", ns
	pre.Spec.Type = corev1.ServiceTypeLoadBalancer
	pre.Spec.LoadBalancerClass = &class
	pre.Spec.Ports = []corev1.ServicePort{{Name: "game", Port: 25565}}
	if err := k8sClient.Create(context.Background(), pre); err != nil {
		t.Fatalf("create Service: %v", err)
	}

	startMgr(t, ns, withGameServerReconciler(t, ns))
	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Networking = gameplanev1alpha1.GameServerNetworking{Expose: "LoadBalancer"}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	key := types.NamespacedName{Namespace: ns, Name: "smp"}
	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(), key, &svc); err != nil {
			return false, err.Error()
		}
		if svc.UID != pre.UID {
			return false, "Service was replaced"
		}
		if len(svc.OwnerReferences) == 0 {
			return false, "not adopted yet"
		}
		if svc.Spec.LoadBalancerClass == nil || *svc.Spec.LoadBalancerClass != class {
			return false, "class changed"
		}
		return true, ""
	})
}
