//go:build envtest

package controller

import (
	"context"
	"slices"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

// TestGameServer_LoadBalancerClassAddedToAnExistingService — a server that
// was already a LoadBalancer without a class gets a new Service with the
// class once one is requested, since Kubernetes rejects adding a class to an
// existing LoadBalancer Service.
func TestGameServer_LoadBalancerClassAddedToAnExistingService(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	gs.Spec.Networking = gameplanev1alpha1.GameServerNetworking{Expose: "LoadBalancer"}
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
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			return false, "not a LoadBalancer yet"
		}
		if svc.Spec.LoadBalancerClass != nil {
			return false, "unexpected class"
		}
		firstUID = svc.UID
		return true, ""
	})

	class := "example.com/added"
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cur gameplanev1alpha1.GameServer
		if err := k8sClient.Get(context.Background(), key, &cur); err != nil {
			return err
		}
		cur.Spec.Networking.LoadBalancerClass = &class
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
		if svc.Spec.LoadBalancerClass == nil || *svc.Spec.LoadBalancerClass != class {
			return false, "new Service lacks the class"
		}
		return true, ""
	})
}

// TestGameServer_StopWhileTheServiceIsReplaced — Stop and status keep
// working while a class change waits for the old Service's load balancer to
// clean up. The test holds the provider's cleanup finalizer, so the old
// Service stays terminating until it is released.
func TestGameServer_StopWhileTheServiceIsReplaced(t *testing.T) {
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
	const cleanup = "service.kubernetes.io/load-balancer-cleanup"
	var oldUID types.UID
	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(), key, &ss); err != nil {
			return false, err.Error()
		}
		if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 1 {
			return false, "StatefulSet not running yet"
		}
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(), key, &svc); err != nil {
			return false, err.Error()
		}
		oldUID = svc.UID
		return true, ""
	})
	// Hold the old Service the way a load-balancer provider does.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(), key, &svc); err != nil {
			return err
		}
		svc.Finalizers = append(svc.Finalizers, cleanup)
		return k8sClient.Update(context.Background(), &svc)
	}); err != nil {
		t.Fatalf("add cleanup finalizer: %v", err)
	}
	release := func() {
		_ = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var svc corev1.Service
			if err := k8sClient.Get(context.Background(), key, &svc); err != nil {
				return client.IgnoreNotFound(err)
			}
			if svc.UID != oldUID || !slices.Contains(svc.Finalizers, cleanup) {
				return nil
			}
			svc.Finalizers = slices.DeleteFunc(svc.Finalizers, func(f string) bool { return f == cleanup })
			return k8sClient.Update(context.Background(), &svc)
		})
	}
	t.Cleanup(release)

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur := getGameServer(t, ns, "smp")
		cur.Spec.Networking.LoadBalancerClass = &second
		return k8sClient.Update(context.Background(), cur)
	}); err != nil {
		t.Fatalf("update class: %v", err)
	}
	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(), key, &svc); err != nil {
			return false, err.Error()
		}
		if svc.UID != oldUID || svc.DeletionTimestamp.IsZero() {
			return false, "old Service not terminating yet"
		}
		return true, ""
	})

	// Stop while the old Service is still held.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur := getGameServer(t, ns, "smp")
		cur.Spec.Suspend = true
		return k8sClient.Update(context.Background(), cur)
	}); err != nil {
		t.Fatalf("stop: %v", err)
	}
	eventually(t, func() (bool, string) {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(), key, &ss); err != nil {
			return false, err.Error()
		}
		if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 {
			return false, "game not stopped"
		}
		cur := getGameServer(t, ns, "smp")
		if cur.Status.ObservedGeneration != cur.Generation {
			return false, "status not updated for the Stop"
		}
		if cur.Status.Phase != gameplanev1alpha1.GameServerPhaseSuspended {
			return false, "phase = " + string(cur.Status.Phase)
		}
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(), key, &svc); err != nil || svc.UID != oldUID {
			return false, "old Service gone before the check"
		}
		return true, ""
	})

	// Once the provider lets go, the new Service gets the new class.
	release()
	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(), key, &svc); err != nil {
			return false, err.Error()
		}
		if svc.UID == oldUID {
			return false, "Service not replaced yet"
		}
		if svc.Spec.LoadBalancerClass == nil || *svc.Spec.LoadBalancerClass != second {
			return false, "new Service lacks the new class"
		}
		return true, ""
	})
}
