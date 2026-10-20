//go:build envtest

package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// setSSStatus fakes StatefulSet status in envtest (which has no kubelet),
// setting the "pods created" (Replicas) and "pods ready" (ReadyReplicas)
// counters independently. Restart's drain gate reads Replicas while the
// soft-stop scale-down reads ReadyReplicas, so a restart test must move them
// separately: not-ready (1,0) → gone (0,0).
func setSSStatus(t *testing.T, ns, name string, replicas, ready int32) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var ss appsv1.StatefulSet
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &ss); err != nil {
			return err
		}
		ss.Status.Replicas = replicas
		ss.Status.ReadyReplicas = ready
		return k8sClient.Status().Update(context.Background(), &ss)
	}); err != nil {
		t.Fatalf("set ss status replicas=%d ready=%d: %v", replicas, ready, err)
	}
}

func stampRestart(t *testing.T, ns, name, token string) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, name)
		if gs.Annotations == nil {
			gs.Annotations = map[string]string{}
		}
		gs.Annotations[RestartRequestedAnnotation] = token
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("stamp restart: %v", err)
	}
}

// TestGameServer_RestartRecyclesPod exercises the full graceful-restart path:
// a running server whose template declares a stop sequence is drained (stop
// issued, pod held up while it saves), scaled to zero once the game goes
// not-ready, and only brought back up — with the token acked — once the pod is
// confirmed gone (Status.Replicas == 0). That last gate is what guarantees the
// pod identity actually changes rather than the 1→0→1 coalescing into a no-op.
func TestGameServer_RestartRecyclesPod(t *testing.T) {
	ns := newNamespace(t)
	stopper := &fakeStopper{}
	startMgr(t, ns, withGameServerReconcilerStopper(t, ns, stopper))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	// selectStopTransport requires the template to actually declare RCON
	// before it will pick stopTransportRCON — buildGameTemplate leaves RCON
	// unset, so without this the stop sequence is never issued and the
	// eventually() below times out waiting on stopper.count().
	tmpl.Spec.RCON = &gameplanev1alpha1.RCONSpec{Protocol: "source", Port: 25575}
	if tmpl.Spec.Capabilities == nil {
		tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{}
	}
	tmpl.Spec.Capabilities.Lifecycle = &gameplanev1alpha1.LifecycleSpec{Stop: []string{"stop"}}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "smp", tmpl.Name)
	// Long grace so the scale-down is driven by the game going not-ready, not by
	// the backstop deadline firing mid-assertion.
	grace := int32(120)
	gs.Spec.StopGracePeriodSeconds = &grace
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "smp") == 1, "replicas not yet 1"
	})
	setSSStatus(t, ns, "smp", 1, 1) // fake a running game

	// Request the restart. The operator should issue the stop and hold the pod
	// up while the game saves.
	stampRestart(t, ns, "smp", "tok1")
	eventually(t, func() (bool, string) {
		if stopper.count() == 0 {
			return false, "stop sequence not issued yet"
		}
		g := getGameServer(t, ns, "smp")
		if _, ok := g.Annotations[stopRequestedAtAnnotation]; !ok {
			return false, "stop-requested annotation not set"
		}
		if r := ssReplicas(t, ns, "smp"); r != 1 {
			return false, "pod scaled down before the game went not-ready"
		}
		return true, ""
	})

	// Game goes not-ready but the pod still exists (Status.Replicas == 1): the
	// operator scales the spec to zero, but must NOT ack yet — the pod isn't
	// gone, so a resume now would risk coalescing into a no-op.
	setSSStatus(t, ns, "smp", 1, 0)
	eventuallyWith(t, 15*time.Second, func() (bool, string) {
		return ssReplicas(t, ns, "smp") == 0, "spec not yet scaled to zero"
	})
	if g := getGameServer(t, ns, "smp"); g.Annotations[RestartCompletedAnnotation] != "" {
		t.Fatalf("restart acked while pod still present (Status.Replicas>0)")
	}

	// Pod is deleted (Status.Replicas == 0): now the operator acks and brings a
	// fresh pod up, clearing the grace clock.
	setSSStatus(t, ns, "smp", 0, 0)
	eventually(t, func() (bool, string) {
		g := getGameServer(t, ns, "smp")
		if g.Annotations[RestartCompletedAnnotation] != "tok1" {
			return false, "restart not acked"
		}
		if _, ok := g.Annotations[stopRequestedAtAnnotation]; ok {
			return false, "grace clock not cleared"
		}
		return ssReplicas(t, ns, "smp") == 1, "not scaled back to 1"
	})
}

// TestGameServer_RestartIdempotent proves an already-completed token never
// re-runs: the operator leaves the running server untouched.
func TestGameServer_RestartIdempotent(t *testing.T) {
	ns := newNamespace(t)
	stopper := &fakeStopper{}
	startMgr(t, ns, withGameServerReconcilerStopper(t, ns, stopper))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if tmpl.Spec.Capabilities == nil {
		tmpl.Spec.Capabilities = &gameplanev1alpha1.CapabilitiesSpec{}
	}
	tmpl.Spec.Capabilities.Lifecycle = &gameplanev1alpha1.LifecycleSpec{Stop: []string{"stop"}}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "idem", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "idem") == 1, "replicas not yet 1"
	})
	setSSStatus(t, ns, "idem", 1, 1)

	// Seed a request that already has a matching completion echo.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		g := getGameServer(t, ns, "idem")
		if g.Annotations == nil {
			g.Annotations = map[string]string{}
		}
		g.Annotations[RestartRequestedAnnotation] = "tok1"
		g.Annotations[RestartCompletedAnnotation] = "tok1"
		return k8sClient.Update(context.Background(), g)
	}); err != nil {
		t.Fatalf("seed acked token: %v", err)
	}

	consistently(t, 3*time.Second, func() (bool, string) {
		if r := ssReplicas(t, ns, "idem"); r != 1 {
			return false, "server scaled away on an already-completed token"
		}
		if stopper.count() != 0 {
			return false, "stop issued on an already-completed token"
		}
		g := getGameServer(t, ns, "idem")
		if _, ok := g.Annotations[stopRequestedAtAnnotation]; ok {
			return false, "grace clock set on an already-completed token"
		}
		return true, ""
	})
}

// TestGameServer_RestartHardRecycle covers a template with no stop sequence:
// the restart recycles the pod with a straight scale-down (no agent stop) and
// still acks once the pod is gone.
func TestGameServer_RestartHardRecycle(t *testing.T) {
	ns := newNamespace(t)
	stopper := &fakeStopper{}
	startMgr(t, ns, withGameServerReconcilerStopper(t, ns, stopper))

	tmpl := buildGameTemplate(uniqueName("minecraft")) // no Lifecycle.Stop declared
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "hard", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "hard") == 1, "replicas not yet 1"
	})
	setSSStatus(t, ns, "hard", 1, 1)

	stampRestart(t, ns, "hard", "tok1")
	// No stop sequence → straight scale-down; the pod still exists so no ack yet.
	eventuallyWith(t, 15*time.Second, func() (bool, string) {
		return ssReplicas(t, ns, "hard") == 0, "spec not yet scaled to zero"
	})
	if stopper.count() != 0 {
		t.Fatalf("stop issued for a template with no lifecycle.stop")
	}

	setSSStatus(t, ns, "hard", 0, 0)
	eventually(t, func() (bool, string) {
		g := getGameServer(t, ns, "hard")
		if g.Annotations[RestartCompletedAnnotation] != "tok1" {
			return false, "restart not acked"
		}
		return ssReplicas(t, ns, "hard") == 1, "not scaled back to 1"
	})
}
