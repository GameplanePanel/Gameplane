package controller

import (
	"context"
	"testing"
	"time"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func scheduleSpec(policy string, deadline *int64) gameplanev1alpha1.BackupScheduleSpec {
	return gameplanev1alpha1.BackupScheduleSpec{
		ConcurrencyPolicy:       policy,
		StartingDeadlineSeconds: deadline,
	}
}

// TestShouldFire exercises the concurrency/deadline decision in isolation.
// None of these cases hit the Replace branch (which deletes via the client),
// so a zero-value reconciler is sufficient and the test stays deterministic.
func TestShouldFire(t *testing.T) {
	r := &BackupScheduleReconciler{}
	ctx := context.Background()
	now := time.Now()
	dl := int64(30)

	cases := []struct {
		name      string
		spec      gameplanev1alpha1.BackupScheduleSpec
		scheduled time.Time
		active    int
		want      bool
	}{
		{"forbid skips while a backup is in flight", scheduleSpec("Forbid", nil), now, 1, false},
		{"forbid fires when idle", scheduleSpec("Forbid", nil), now, 0, true},
		{"empty policy behaves as forbid", scheduleSpec("", nil), now, 1, false},
		{"allow fires despite an in-flight backup", scheduleSpec("Allow", nil), now, 2, true},
		{"deadline skips a late run", scheduleSpec("Allow", &dl), now.Add(-time.Minute), 0, false},
		{"within deadline fires", scheduleSpec("Allow", &dl), now.Add(-10 * time.Second), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sched := &gameplanev1alpha1.BackupSchedule{Spec: tc.spec}
			active := make([]gameplanev1alpha1.Backup, tc.active)
			fire, err := r.shouldFire(ctx, sched, now, tc.scheduled, active)
			if err != nil {
				t.Fatalf("shouldFire err: %v", err)
			}
			if fire != tc.want {
				t.Fatalf("fire=%v, want %v", fire, tc.want)
			}
		})
	}
}
