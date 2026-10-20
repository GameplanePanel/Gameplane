package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// F-057: an unparseable spec.idle.wakeWindows entry must be surfaced as its
// own status condition, not only folded into status.idle.reason (which is
// only populated while the server happens to be asleep).

func TestReconcileIdle_InvalidWakeWindowReturnsScheduleError(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "smp", Namespace: "games"},
		Spec: gameplanev1alpha1.GameServerSpec{
			Idle: enabledIdle(30, "every-night"),
		},
	}
	gs.Annotations = map[string]string{
		IdleAsleepSinceAnnotation: baseTime.Add(-time.Hour).Format(time.RFC3339),
	}
	r := newTestGameServerReconciler(t, gs)

	_, _, _, idleScheduleErr, err := r.reconcileIdle(context.Background(), gs)
	if err != nil {
		t.Fatalf("reconcileIdle: unexpected fatal error: %v", err)
	}
	if idleScheduleErr == nil {
		t.Fatal("idleScheduleErr = nil, want the unparseable wake-window error surfaced")
	}
}

// F-057 (awake case): the schedule must be validated even when the server is
// not asleep, so a bad window on a running server is caught immediately
// instead of only once the server happens to fall asleep.
func TestIdleDecide_InvalidWakeWindowSurfacedWhileAwake(t *testing.T) {
	t.Parallel()

	in := idleInputs{
		spec:          enabledIdle(30, "every-night"),
		phase:         gameplanev1alpha1.GameServerPhaseRunning,
		hbFresh:       true,
		playersOnline: idlePtr(int32(3)), // players online: clock not accruing, server stays awake
		now:           baseTime,
	}

	out := idleDecide(in)
	if out.state != idleAwake {
		t.Fatalf("state = %v, want idleAwake", out.state)
	}
	if out.err == nil {
		t.Fatal("err = nil, want the unparseable wake-window error surfaced while awake")
	}
}

func TestReconcileStatus_IdleScheduleInvalidConditionSetAndCleared(t *testing.T) {
	gs := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: "smp", Namespace: "games"},
		Spec: gameplanev1alpha1.GameServerSpec{
			Idle: enabledIdle(30, "every-night"),
		},
	}
	r := newTestGameServerReconciler(t, gs)
	ctx := context.Background()

	// Pass 1: schedule is bad, condition must be set True.
	scheduleErr := errors.New("cron expr: expected 5 fields, found 1")
	if _, err := r.reconcileStatus(ctx, gs, idleAsleep, &gameplanev1alpha1.IdleStatus{Asleep: true}, scheduleErr, tunnelPlan{}, nil, nil, ""); err != nil {
		t.Fatalf("reconcileStatus: %v", err)
	}
	cond, ok := condByType(gs.Status.Conditions, "IdleScheduleInvalid")
	if !ok || cond.Status != metav1.ConditionTrue {
		t.Fatalf("IdleScheduleInvalid = %+v, want True after an unparseable window", cond)
	}

	// Pass 2: schedule fixed (no error this pass); condition must clear.
	if _, err := r.reconcileStatus(ctx, gs, idleAwake, nil, nil, tunnelPlan{}, nil, nil, ""); err != nil {
		t.Fatalf("reconcileStatus: %v", err)
	}
	if _, ok := condByType(gs.Status.Conditions, "IdleScheduleInvalid"); ok {
		t.Error("IdleScheduleInvalid should be cleared once the schedule parses cleanly")
	}
}
