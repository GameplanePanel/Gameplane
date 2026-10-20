//go:build envtest

package controller

import (
	"fmt"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestGameServer_ConditionArgsSetAndCleared: a missing template puts the
// Ready condition on Failed with its English detail in conditionArgs; once the
// template exists the reason no longer carries arguments, so the entry goes.
func TestGameServer_ConditionArgsSetAndCleared(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))
	tmpl := buildGameTemplate(uniqueName("late-template"))
	gs := buildGameServer(ns, "args-waiting", tmpl.Name)
	if err := k8sClient.Create(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Namespace: ns, Name: gs.Name}
	wantDetail := fmt.Sprintf("GameTemplate %q not found", tmpl.Name)
	eventually(t, func() (bool, string) {
		if err := k8sClient.Get(t.Context(), key, gs); err != nil {
			return false, err.Error()
		}
		ready := meta.FindStatusCondition(gs.Status.Conditions, "Ready")
		if ready == nil || ready.Reason != "Failed" {
			return false, "waiting for Ready reason Failed"
		}
		got := gs.Status.ConditionArgs["Ready"]["detail"]
		return got == wantDetail, fmt.Sprintf("conditionArgs[Ready][detail] = %q, want %q", got, wantDetail)
	})

	if err := k8sClient.Create(t.Context(), tmpl); err != nil {
		t.Fatal(err)
	}
	deleteCleanup(t, tmpl)
	eventually(t, func() (bool, string) {
		if err := k8sClient.Get(t.Context(), key, gs); err != nil {
			return false, err.Error()
		}
		ready := meta.FindStatusCondition(gs.Status.Conditions, "Ready")
		if ready == nil || ready.Reason == "Failed" {
			return false, "waiting for Ready reason to leave Failed"
		}
		_, present := gs.Status.ConditionArgs["Ready"]
		return !present, "conditionArgs[Ready] still present after recovery"
	})
}

// TestGameServer_ConditionArgsAddressInUse: the second server requesting an
// address held by the first reports AddressInUse with the address and the
// holder's name as arguments.
func TestGameServer_ConditionArgsAddressInUse(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerAddressManager(t, ns, "metallb"))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(t.Context(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	const addr = "203.0.113.20"
	gs1 := buildGameServer(ns, "args-holder", tmpl.Name)
	gs1.Spec.Networking.Expose = "LoadBalancer"
	gs1.Spec.Networking.Address = addr
	if err := k8sClient.Create(t.Context(), gs1); err != nil {
		t.Fatalf("create server1: %v", err)
	}
	deleteCleanup(t, gs1)
	eventually(t, func() (bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(t.Context(),
			types.NamespacedName{Namespace: ns, Name: "args-holder"}, &svc); err != nil {
			return false, "service: " + err.Error()
		}
		return true, ""
	})

	// As in TestGameServer_AddressConflictDetection, no address manager runs in
	// envtest, so the LoadBalancer ingress is written as a real address manager
	// would write it.
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var svc corev1.Service
		if err := k8sClient.Get(t.Context(),
			types.NamespacedName{Namespace: ns, Name: "args-holder"}, &svc); err != nil {
			return err
		}
		svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: addr}}
		return k8sClient.Status().Update(t.Context(), &svc)
	}); err != nil {
		t.Fatalf("update server1 service status: %v", err)
	}
	eventually(t, func() (bool, string) {
		var gs gameplanev1alpha1.GameServer
		if err := k8sClient.Get(t.Context(),
			types.NamespacedName{Namespace: ns, Name: "args-holder"}, &gs); err != nil {
			return false, "get server1: " + err.Error()
		}
		for _, ep := range gs.Status.Endpoints {
			if ep.Host == addr {
				return true, ""
			}
		}
		return false, "server1 endpoints do not yet reflect " + addr
	})

	gs2 := buildGameServer(ns, "args-contender", tmpl.Name)
	gs2.Spec.Networking.Expose = "LoadBalancer"
	gs2.Spec.Networking.Address = addr
	if err := k8sClient.Create(t.Context(), gs2); err != nil {
		t.Fatalf("create server2: %v", err)
	}
	deleteCleanup(t, gs2)
	eventually(t, func() (bool, string) {
		var gs gameplanev1alpha1.GameServer
		if err := k8sClient.Get(t.Context(),
			types.NamespacedName{Namespace: ns, Name: "args-contender"}, &gs); err != nil {
			return false, "get server2: " + err.Error()
		}
		cond := meta.FindStatusCondition(gs.Status.Conditions, gameplanev1alpha1.GameServerConditionAddressAssignment)
		if cond == nil {
			return false, "AddressAssignment condition not found"
		}
		if cond.Reason != "AddressInUse" {
			return false, "AddressAssignment reason = " + cond.Reason + ", want AddressInUse"
		}
		want := map[string]string{"address": addr, "server": "args-holder"}
		got := gs.Status.ConditionArgs[gameplanev1alpha1.GameServerConditionAddressAssignment]
		return reflect.DeepEqual(got, want), fmt.Sprintf("conditionArgs[AddressAssignment] = %v, want %v", got, want)
	})
}

// TestGameServer_ConditionArgsInvalidAddress: a requested address that does
// not parse reports InvalidAddress with the raw address as its argument.
func TestGameServer_ConditionArgsInvalidAddress(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconcilerAddressManager(t, ns, "metallb"))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(t.Context(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "args-bad-address", tmpl.Name)
	gs.Spec.Networking.Expose = "LoadBalancer"
	gs.Spec.Networking.Address = "not-an-ip"
	if err := k8sClient.Create(t.Context(), gs); err != nil {
		t.Fatalf("create server: %v", err)
	}
	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.GameServer
		if err := k8sClient.Get(t.Context(),
			types.NamespacedName{Namespace: ns, Name: gs.Name}, &got); err != nil {
			return false, "get server: " + err.Error()
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, gameplanev1alpha1.GameServerConditionAddressAssignment)
		if cond == nil {
			return false, "AddressAssignment condition not found"
		}
		if cond.Reason != "InvalidAddress" {
			return false, "AddressAssignment reason = " + cond.Reason + ", want InvalidAddress"
		}
		addr := got.Status.ConditionArgs[gameplanev1alpha1.GameServerConditionAddressAssignment]["address"]
		return addr == "not-an-ip", fmt.Sprintf("conditionArgs address = %q, want not-an-ip", addr)
	})
}
