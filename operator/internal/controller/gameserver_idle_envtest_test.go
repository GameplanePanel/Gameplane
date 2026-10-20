//go:build envtest

package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// setAgentPlayers fakes the sidecar heartbeat envtest has no agent to produce.
// A nil count means "the game reports no player source" — deliberately distinct
// from zero, which is the whole basis of the idle trigger.
func setAgentPlayers(t *testing.T, ns, name string, players *int32) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, name)
		now := metav1.Now()
		gs.Status.Agent = &gameplanev1alpha1.AgentStatus{
			LastHeartbeat: &now,
			PlayersOnline: players,
		}
		return k8sClient.Status().Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("set agent players: %v", err)
	}
}

// enableIdle turns on idle auto-sleep with the given threshold. The CRD floors
// afterMinutes at 5, so a test can't reach the sleep decision by picking a tiny
// value — it backdates the clock with backdateEmptySince instead.
func enableIdle(t *testing.T, ns, name string, afterMinutes int32, windows ...string) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, name)
		gs.Spec.Idle = &gameplanev1alpha1.IdleSpec{
			Enabled:      true,
			AfterMinutes: &afterMinutes,
			WakeWindows:  windows,
		}
		return k8sClient.Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("enable idle: %v", err)
	}
}

func backdateEmptySince(t *testing.T, ns, name string, d time.Duration) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		gs := getGameServer(t, ns, name)
		if gs.Status.Idle == nil {
			gs.Status.Idle = &gameplanev1alpha1.IdleStatus{}
		}
		past := metav1.NewTime(time.Now().UTC().Add(-d))
		gs.Status.Idle.EmptySince = &past
		return k8sClient.Status().Update(context.Background(), gs)
	}); err != nil {
		t.Fatalf("backdate emptySince: %v", err)
	}
}

// TestGameServer_IdleSleepsAndWakes is the end-to-end operator path: an empty
// server past its threshold is scaled to zero through the *graceful* stop (the
// module stop sequence is issued first, so the world saves), reports Suspended
// with the IdleAsleep reason, and comes back up on an explicit wake request
// with the token acked.
func TestGameServer_IdleSleepsAndWakes(t *testing.T) {
	ns := newNamespace(t)
	stopper := &fakeStopper{}
	startMgr(t, ns, withGameServerReconcilerStopper(t, ns, stopper))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	// selectStopTransport only picks RCON when the template actually declares
	// it; without this the stop sequence is never issued and the graceful-path
	// assertion below can't be made.
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
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "smp") == 1, "replicas not yet 1"
	})
	setSSStatus(t, ns, "smp", 1, 1)                // a running game
	setAgentPlayers(t, ns, "smp", ptrTo(int32(0))) // ...with nobody on it

	// Reaching Running is what arms the idle clock: idle time only accrues
	// from Running, never from Starting.
	eventually(t, func() (bool, string) {
		g := getGameServer(t, ns, "smp")
		return g.Status.Phase == gameplanev1alpha1.GameServerPhaseRunning,
			"phase is " + string(g.Status.Phase)
	})

	enableIdle(t, ns, "smp", 5)
	eventually(t, func() (bool, string) {
		g := getGameServer(t, ns, "smp")
		if g.Status.Idle == nil || g.Status.Idle.EmptySince == nil {
			return false, "idle clock not started"
		}
		return true, ""
	})

	// Jump the clock past the threshold rather than sleeping through it.
	backdateEmptySince(t, ns, "smp", 10*time.Minute)

	eventually(t, func() (bool, string) {
		g := getGameServer(t, ns, "smp")
		if _, ok := g.Annotations[IdleAsleepSinceAnnotation]; !ok {
			return false, "sleep marker not stamped"
		}
		if stopper.count() == 0 {
			return false, "stop sequence not issued — sleep must be graceful, not a kill"
		}
		return true, ""
	})

	// The game goes not-ready as it shuts down; the operator finishes the
	// scale-down and reports the sleep.
	setSSStatus(t, ns, "smp", 1, 0)
	eventuallyWith(t, 20*time.Second, func() (bool, string) {
		if r := ssReplicas(t, ns, "smp"); r != 0 {
			return false, "not scaled to zero"
		}
		g := getGameServer(t, ns, "smp")
		if g.Status.Phase != gameplanev1alpha1.GameServerPhaseSuspended {
			return false, "phase is " + string(g.Status.Phase) + ", want Suspended"
		}
		if g.Status.Idle == nil || !g.Status.Idle.Asleep {
			return false, "status.idle.asleep not set"
		}
		// The phase is shared with a user stop, so the reason is the only way
		// to tell the two apart.
		for _, c := range g.Status.Conditions {
			if c.Type == "Ready" && c.Reason != "IdleAsleep" {
				return false, "Ready reason is " + c.Reason + ", want IdleAsleep"
			}
		}
		return true, ""
	})
	setSSStatus(t, ns, "smp", 0, 0)

	// Wake it: the operator clears its marker, acks the token, and scales back.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		g := getGameServer(t, ns, "smp")
		if g.Annotations == nil {
			g.Annotations = map[string]string{}
		}
		g.Annotations[IdleWakeRequestedAnnotation] = "wake-1"
		return k8sClient.Update(context.Background(), g)
	}); err != nil {
		t.Fatalf("stamp wake: %v", err)
	}

	eventually(t, func() (bool, string) {
		g := getGameServer(t, ns, "smp")
		if g.Annotations[IdleWakeCompletedAnnotation] != "wake-1" {
			return false, "wake not acked"
		}
		if _, ok := g.Annotations[IdleAsleepSinceAnnotation]; ok {
			return false, "sleep marker not cleared"
		}
		return ssReplicas(t, ns, "smp") == 1, "not scaled back to 1"
	})
}

// TestGameServer_IdleNeverSleepsWithoutAPlayerCount is the safety property that
// matters most: a game whose agent reports no player count must stay up
// forever, no matter how long it has been "empty". Unknown is not zero.
func TestGameServer_IdleNeverSleepsWithoutAPlayerCount(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("valheim"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "world", tmpl.Name)
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		return ssReplicas(t, ns, "world") == 1, "replicas not yet 1"
	})
	setSSStatus(t, ns, "world", 1, 1)
	setAgentPlayers(t, ns, "world", nil) // the game exposes no player source

	enableIdle(t, ns, "world", 5)

	// Give the reconciler room to do the wrong thing, then prove it didn't:
	// no clock, no marker, still running.
	eventually(t, func() (bool, string) {
		g := getGameServer(t, ns, "world")
		return g.Status.Idle != nil, "idle status not populated yet"
	})
	time.Sleep(2 * time.Second)

	g := getGameServer(t, ns, "world")
	if _, ok := g.Annotations[IdleAsleepSinceAnnotation]; ok {
		t.Fatal("server slept despite an unknown player count")
	}
	if g.Status.Idle.EmptySince != nil {
		t.Errorf("idle clock started on an unknown player count: %v", g.Status.Idle.EmptySince)
	}
	if g.Status.Idle.Reason == "" {
		t.Error("no reason recorded; a server that can never sleep must say so")
	}
	if r := ssReplicas(t, ns, "world"); r != 1 {
		t.Errorf("replicas = %d, want 1", r)
	}
}
