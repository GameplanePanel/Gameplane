package controller

import (
	"testing"
	"time"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestIdleWakeRespectsManualStop(t *testing.T) {
	t.Parallel()
	for _, suspended := range []bool{false, true} {
		in := runningEmpty()
		in.asleepSince = idlePtr(baseTime.Add(-time.Hour))
		in.wakePending = true
		in.suspended = suspended
		out := idleDecide(in)
		server := &gameplanev1alpha1.GameServer{}
		server.Spec.Suspend = suspended
		phase := derivePhase(server, false, false, false, out.state)
		if suspended {
			if phase != gameplanev1alpha1.GameServerPhaseSuspended || out.status.Reason != "stopped by user" || out.status.LastWakeTime != nil {
				t.Fatalf("wake bypassed manual stop: phase=%s status=%+v", phase, out.status)
			}
		} else if !out.wake || out.status.Asleep || out.status.LastWakeTime == nil || phase != gameplanev1alpha1.GameServerPhasePending {
			t.Fatalf("idle server did not begin waking: phase=%s outcome=%+v", phase, out)
		}
	}
}
