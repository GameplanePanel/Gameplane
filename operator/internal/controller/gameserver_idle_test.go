package controller

import (
	"testing"
	"time"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func idlePtr[T any](v T) *T { return &v }

// baseTime is a fixed reference so the wake-window cases are deterministic.
// 2026-07-22 09:00:00 UTC is a Wednesday.
var baseTime = time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)

func enabledIdle(afterMinutes int32, windows ...string) *gameplanev1alpha1.IdleSpec {
	return &gameplanev1alpha1.IdleSpec{
		Enabled:      true,
		AfterMinutes: idlePtr(afterMinutes),
		WakeWindows:  windows,
	}
}

// runningEmpty is the baseline: an eligible server reporting zero players.
func runningEmpty() idleInputs {
	return idleInputs{
		spec:          enabledIdle(30),
		phase:         gameplanev1alpha1.GameServerPhaseRunning,
		hbFresh:       true,
		playersOnline: idlePtr(int32(0)),
		now:           baseTime,
	}
}

func TestIdleDecide_NeverSleepsWithoutAKnownEmptyServer(t *testing.T) {
	t.Parallel()

	// These are the cases where sleeping would be actively harmful: each one
	// must leave the server awake AND must not start the idle clock, because
	// a running clock would sleep the server as soon as the condition cleared.
	tests := []struct {
		name string
		mut  func(*idleInputs)
		why  string
	}{
		{
			name: "stale heartbeat",
			mut:  func(in *idleInputs) { in.hbFresh = false },
			why:  "a dead agent's last-known count is not evidence the server is empty",
		},
		{
			name: "player count unknown",
			mut:  func(in *idleInputs) { in.playersOnline = nil },
			why:  "games with no player source report null; null is not zero",
		},
		{
			name: "players online",
			mut:  func(in *idleInputs) { in.playersOnline = idlePtr(int32(3)) },
			why:  "people are playing",
		},
		{
			name: "not running yet",
			mut:  func(in *idleInputs) { in.phase = gameplanev1alpha1.GameServerPhaseStarting },
			why:  "a booting server reports zero players before anyone can join",
		},
		{
			name: "restart or wipe in flight",
			mut:  func(in *idleInputs) { in.busy = true },
			why:  "sleeping would fight another operator-owned lifecycle op",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := runningEmpty()
			tc.mut(&in)

			got := idleDecide(in)

			if got.state != idleAwake {
				t.Fatalf("state = %v, want idleAwake (%s)", got.state, tc.why)
			}
			if got.sleep {
				t.Errorf("sleep = true, want false (%s)", tc.why)
			}
			if got.status == nil {
				t.Fatal("status = nil, want a block explaining why it is not sleeping")
			}
			if got.status.EmptySince != nil {
				t.Errorf("EmptySince = %v, want nil — the idle clock must not run (%s)",
					got.status.EmptySince, tc.why)
			}
			if got.status.Reason == "" {
				t.Error("Reason is empty; a server that never sleeps must explain itself")
			}
		})
	}
}

func TestIdleDecide_ClockAndSleepThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		emptyFor    time.Duration // 0 => no prior emptySince
		hasClock    bool
		after       int32
		wantSleep   bool
		wantClock   bool
		wantRequeue time.Duration
	}{
		{
			name: "first empty reading starts the clock", hasClock: false,
			after: 30, wantSleep: false, wantClock: true, wantRequeue: 30 * time.Minute,
		},
		{
			name: "still within the threshold", hasClock: true, emptyFor: 10 * time.Minute,
			after: 30, wantSleep: false, wantClock: true, wantRequeue: 20 * time.Minute,
		},
		{
			name: "one second short still holds", hasClock: true, emptyFor: 30*time.Minute - time.Second,
			after: 30, wantSleep: false, wantClock: true, wantRequeue: time.Second,
		},
		{
			name: "exactly at the threshold sleeps", hasClock: true, emptyFor: 30 * time.Minute,
			after: 30, wantSleep: true, wantClock: false,
		},
		{
			name: "well past the threshold sleeps", hasClock: true, emptyFor: 3 * time.Hour,
			after: 30, wantSleep: true, wantClock: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := runningEmpty()
			in.spec = enabledIdle(tc.after)
			if tc.hasClock {
				in.emptySince = idlePtr(baseTime.Add(-tc.emptyFor))
			}

			got := idleDecide(in)

			if got.sleep != tc.wantSleep {
				t.Fatalf("sleep = %v, want %v", got.sleep, tc.wantSleep)
			}
			wantState := idleAwake
			if tc.wantSleep {
				wantState = idleAsleep
			}
			if got.state != wantState {
				t.Errorf("state = %v, want %v", got.state, wantState)
			}
			if (got.status.EmptySince != nil) != tc.wantClock {
				t.Errorf("EmptySince set = %v, want %v", got.status.EmptySince != nil, tc.wantClock)
			}
			if tc.wantSleep {
				if !got.status.Asleep || got.status.AsleepSince == nil {
					t.Errorf("status not marked asleep: %+v", got.status)
				}
			} else if tc.wantRequeue != 0 && got.requeue != tc.wantRequeue {
				t.Errorf("requeue = %v, want %v (must land exactly on the sleep deadline)",
					got.requeue, tc.wantRequeue)
			}
		})
	}
}

// The clock must survive across reconciles rather than restarting each pass,
// otherwise a server polled more often than afterMinutes would never sleep.
func TestIdleDecide_ClockIsPreservedNotRestarted(t *testing.T) {
	t.Parallel()

	started := baseTime.Add(-10 * time.Minute)
	in := runningEmpty()
	in.emptySince = idlePtr(started)

	got := idleDecide(in)

	if got.status.EmptySince == nil || !got.status.EmptySince.Time.Equal(started) {
		t.Fatalf("EmptySince = %v, want the original %v carried forward", got.status.EmptySince, started)
	}
}

func TestIdleDecide_ExplicitIntentBeatsThePolicy(t *testing.T) {
	t.Parallel()

	asleep := idlePtr(baseTime.Add(-2 * time.Hour))

	t.Run("disabling idle wakes a sleeping server", func(t *testing.T) {
		t.Parallel()
		in := runningEmpty()
		in.spec = &gameplanev1alpha1.IdleSpec{Enabled: false}
		in.asleepSince = asleep

		got := idleDecide(in)

		if got.state != idleAwake || !got.wake {
			t.Fatalf("state=%v wake=%v, want awake+wake — disabling must not strand it asleep",
				got.state, got.wake)
		}
		if got.status != nil {
			t.Errorf("status = %+v, want nil so status.idle is cleared", got.status)
		}
	})

	t.Run("no spec at all is a no-op", func(t *testing.T) {
		t.Parallel()
		in := runningEmpty()
		in.spec = nil

		got := idleDecide(in)

		if got.state != idleAwake || got.wake || got.sleep || got.status != nil {
			t.Fatalf("got %+v, want an inert awake outcome", got)
		}
	})

	t.Run("a user stop wins and clears the sleep marker", func(t *testing.T) {
		t.Parallel()
		in := runningEmpty()
		in.suspended = true
		in.asleepSince = asleep

		got := idleDecide(in)

		if got.state != idleAwake {
			t.Fatalf("state = %v, want idleAwake — spec.suspend drives the scale-down itself", got.state)
		}
		if !got.wake {
			t.Error("wake = false, want true so a manual resume does not come back still flagged asleep")
		}
	})

	t.Run("a wake request outranks the idle clock", func(t *testing.T) {
		t.Parallel()
		in := runningEmpty()
		in.asleepSince = asleep
		in.wakePending = true

		got := idleDecide(in)

		if got.state != idleAwake || !got.wake {
			t.Fatalf("state=%v wake=%v, want awake+wake", got.state, got.wake)
		}
		if got.status.Asleep {
			t.Error("status.Asleep = true, want false")
		}
		if got.status.LastWakeTime == nil || !got.status.LastWakeTime.Time.Equal(baseTime) {
			t.Errorf("LastWakeTime = %v, want now (%v)", got.status.LastWakeTime, baseTime)
		}
		if got.status.EmptySince != nil {
			t.Error("EmptySince set; a just-woken server must get a full fresh idle period")
		}
	})

	// A wake request must be honored even for a server that is not currently
	// asleep, so the ack is written and the token cannot re-fire forever.
	t.Run("a wake request on an awake server still acks", func(t *testing.T) {
		t.Parallel()
		in := runningEmpty()
		in.wakePending = true

		got := idleDecide(in)

		if !got.wake {
			t.Fatal("wake = false; the token would never be acked and would re-fire every reconcile")
		}
	})
}

func TestIdleDecide_WakeWindows(t *testing.T) {
	t.Parallel()

	// Asleep since 03:00; "0 8 * * *" fires at 08:00, before now (09:00).
	t.Run("a window that has passed wakes the server", func(t *testing.T) {
		t.Parallel()
		in := runningEmpty()
		in.spec = enabledIdle(30, "0 8 * * *")
		in.asleepSince = idlePtr(baseTime.Add(-6 * time.Hour))

		got := idleDecide(in)

		if got.state != idleAwake || !got.wake {
			t.Fatalf("state=%v wake=%v, want awake+wake", got.state, got.wake)
		}
		if got.status.LastWakeTime == nil {
			t.Error("LastWakeTime not stamped")
		}
	})

	t.Run("a future window keeps it asleep and requeues on the fire time", func(t *testing.T) {
		t.Parallel()
		in := runningEmpty()
		in.spec = enabledIdle(30, "0 17 * * *") // 17:00, eight hours out
		in.asleepSince = idlePtr(baseTime.Add(-1 * time.Hour))

		got := idleDecide(in)

		if got.state != idleAsleep {
			t.Fatalf("state = %v, want idleAsleep", got.state)
		}
		if want := 8 * time.Hour; got.requeue != want {
			t.Errorf("requeue = %v, want %v (exactly the next fire time)", got.requeue, want)
		}
	})

	t.Run("no windows means it stays asleep until asked", func(t *testing.T) {
		t.Parallel()
		in := runningEmpty()
		in.asleepSince = idlePtr(baseTime.Add(-time.Hour))

		got := idleDecide(in)

		if got.state != idleAsleep {
			t.Fatalf("state = %v, want idleAsleep", got.state)
		}
		if got.requeue != idleReevaluate {
			t.Errorf("requeue = %v, want the %v backstop", got.requeue, idleReevaluate)
		}
	})

	t.Run("the soonest of several windows wins", func(t *testing.T) {
		t.Parallel()
		in := runningEmpty()
		in.spec = enabledIdle(30, "0 20 * * *", "0 12 * * *", "0 17 * * *")
		in.asleepSince = idlePtr(baseTime.Add(-time.Hour))

		got := idleDecide(in)

		if want := 3 * time.Hour; got.requeue != want {
			t.Errorf("requeue = %v, want %v (12:00 is nearest)", got.requeue, want)
		}
	})

	// An unparseable window must not wedge the server or fail the reconcile —
	// it reports the problem and keeps the current state.
	t.Run("an invalid window is reported, not fatal", func(t *testing.T) {
		t.Parallel()
		in := runningEmpty()
		in.spec = enabledIdle(30, "every-night")
		in.asleepSince = idlePtr(baseTime.Add(-time.Hour))

		got := idleDecide(in)

		if got.err == nil {
			t.Fatal("err = nil, want a parse error surfaced for the condition")
		}
		if got.state != idleAsleep {
			t.Errorf("state = %v, want the server left as it was", got.state)
		}
		if got.status.Reason == "" {
			t.Error("Reason is empty; the bad schedule must be visible on status")
		}
	})
}

func TestWakeWindowDue_FiresAtMostOncePerSleep(t *testing.T) {
	t.Parallel()

	// Anchoring on asleepSince is what makes this stateless. Once the server
	// wakes, the anchor is gone; while it sleeps, the same window must not
	// re-arm within the same day.
	asleep := time.Date(2026, 7, 22, 9, 30, 0, 0, time.UTC) // just after 09:00
	windows := []string{"0 9 * * *"}

	due, next, err := wakeWindowDue(windows, asleep, asleep.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if due {
		t.Fatal("due = true; the 09:00 window already passed before the sleep began")
	}
	if want := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Errorf("next = %v, want tomorrow's %v", next, want)
	}
}

func TestIdleDecide_DefaultsToThirtyMinutesWhenUnset(t *testing.T) {
	t.Parallel()

	in := runningEmpty()
	in.spec = &gameplanev1alpha1.IdleSpec{Enabled: true} // AfterMinutes nil

	got := idleDecide(in)

	if got.requeue != defaultIdleAfter {
		t.Errorf("requeue = %v, want the %v CRD default", got.requeue, defaultIdleAfter)
	}
}
