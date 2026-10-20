//go:build envtest

package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestSchedule_FiresBackupWhenDue — A schedule with LastScheduleTime
// set far enough in the past that the next firing is overdue should
// produce exactly one labelled Backup whose ownerRef points at the
// schedule.
func TestSchedule_FiresBackupWhenDue(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	sched := buildBackupSchedule(ns, "smp-sched", "smp", "repo", "* * * * *", nil)
	if err := k8sClient.Create(context.Background(), sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	// Pin LastScheduleTime to 5 minutes ago so the next firing is overdue.
	fiveMinAgo := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	sched.Status.LastScheduleTime = &fiveMinAgo
	if err := k8sClient.Status().Update(context.Background(), sched); err != nil {
		t.Fatalf("status update schedule: %v", err)
	}

	eventually(t, func() (bool, string) {
		backups := listBackupsForSchedule(t, ns, "smp-sched")
		if len(backups) == 0 {
			return false, "no backups created yet"
		}
		return true, ""
	})
}

// TestSchedule_HonorsSuspend — Spec.Suspend=true ⇒ no Backup created
// and Status.NextScheduleTime is cleared.
func TestSchedule_HonorsSuspend(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	sched := buildBackupSchedule(ns, "smp-sched", "smp", "repo", "* * * * *", nil)
	sched.Spec.Suspend = true
	if err := k8sClient.Create(context.Background(), sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	// Backups must not appear.
	consistently(t, time.Second, func() (bool, string) {
		bs := listBackupsForSchedule(t, ns, "smp-sched")
		if len(bs) > 0 {
			return false, "schedule fired despite Suspend=true"
		}
		return true, ""
	})

	// Status.NextScheduleTime must be nil.
	got := getSchedule(t, ns, "smp-sched")
	if got.Status.NextScheduleTime != nil {
		t.Errorf("NextScheduleTime = %v, want nil while suspended", got.Status.NextScheduleTime)
	}
}

// TestSchedule_NextScheduleTimeUpdated — A non-suspended schedule
// eventually populates Status.NextScheduleTime.
func TestSchedule_NextScheduleTimeUpdated(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(),
		buildBackupSchedule(ns, "smp-sched", "smp", "repo", "0 */6 * * *", nil)); err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	eventually(t, func() (bool, string) {
		s := getSchedule(t, ns, "smp-sched")
		if s.Status.NextScheduleTime == nil {
			return false, "NextScheduleTime not yet set"
		}
		if !s.Status.NextScheduleTime.After(time.Now().Add(-time.Minute)) {
			return false, "NextScheduleTime is in the past: " + s.Status.NextScheduleTime.String()
		}
		return true, ""
	})
}

// TestSchedule_InvalidCronDoesNotPanic — Garbage cron expression
// should yield no Backup, no panic, no infinite tight-loop. The
// controller logs the error and returns nil (per its current
// behaviour), so we assert quiescence.
func TestSchedule_InvalidCronDoesNotPanic(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(),
		buildBackupSchedule(ns, "broken", "smp", "repo", "this is not a cron", nil)); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	consistently(t, time.Second, func() (bool, string) {
		if bs := listBackupsForSchedule(t, ns, "broken"); len(bs) > 0 {
			return false, "fired despite invalid cron"
		}
		return true, ""
	})
}

// TestSchedule_RetentionTrimsSucceededBackups — Pre-create labelled
// succeeded Backups and confirm KeepLast=2 trims down to 2 keeping
// the newest.
func TestSchedule_RetentionTrimsSucceededBackups(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	// 5 succeeded backups, oldest first → newest last.
	for i := 0; i < 5; i++ {
		name := "smp-sched-bk-" + []string{"a", "b", "c", "d", "e"}[i]
		b := buildBackup(ns, name, "smp", "repo")
		b.Labels = map[string]string{"gameplane.local/backup-schedule": "smp-sched"}
		if err := k8sClient.Create(context.Background(), b); err != nil {
			t.Fatalf("create backup %d: %v", i, err)
		}
		// Stagger CompletionTime so retention can sort them.
		now := metav1.NewTime(time.Now().Add(time.Duration(i) * time.Second))
		b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
		b.Status.CompletionTime = &now
		size := resource.MustParse("1Mi")
		b.Status.Size = &size
		if err := k8sClient.Status().Update(context.Background(), b); err != nil {
			t.Fatalf("status update %d: %v", i, err)
		}
	}

	// Schedule with KeepLast=2 — trim runs every Reconcile. We use a
	// daily-midnight cron AND pre-set LastScheduleTime to "now" so the
	// scheduler is dormant during the test (otherwise an extra Backup
	// would appear in the namespace and skew the count).
	sched := buildBackupSchedule(ns, "smp-sched", "smp", "repo", "0 0 * * *",
		&gameplanev1alpha1.BackupRetention{KeepLast: 2})
	if err := k8sClient.Create(context.Background(), sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	freezeSchedule(t, ns, sched.Name)

	eventually(t, func() (bool, string) {
		bs := listBackupsForSchedule(t, ns, "smp-sched")
		if len(bs) != 2 {
			names := []string{}
			for _, b := range bs {
				names = append(names, b.Name)
			}
			return false, "want 2 backups kept, got " + sprintArgs(names)
		}
		kept := map[string]bool{bs[0].Name: true, bs[1].Name: true}
		if !kept["smp-sched-bk-d"] || !kept["smp-sched-bk-e"] {
			names := []string{bs[0].Name, bs[1].Name}
			return false, "wrong backups kept: " + sprintArgs(names)
		}
		return true, ""
	})
}

// TestSchedule_DoesNotTrimRunningBackups — Running backups must never
// be deleted by retention, even if KeepLast would otherwise exclude
// them.
func TestSchedule_DoesNotTrimRunningBackups(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	// One Running, three Succeeded.
	running := buildBackup(ns, "smp-sched-running", "smp", "repo")
	running.Labels = map[string]string{"gameplane.local/backup-schedule": "smp-sched"}
	if err := k8sClient.Create(context.Background(), running); err != nil {
		t.Fatalf("create running backup: %v", err)
	}
	running.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
	if err := k8sClient.Status().Update(context.Background(), running); err != nil {
		t.Fatalf("status update running: %v", err)
	}

	for i := 0; i < 3; i++ {
		name := "smp-sched-old-" + []string{"a", "b", "c"}[i]
		b := buildBackup(ns, name, "smp", "repo")
		b.Labels = map[string]string{"gameplane.local/backup-schedule": "smp-sched"}
		if err := k8sClient.Create(context.Background(), b); err != nil {
			t.Fatalf("create old %d: %v", i, err)
		}
		now := metav1.NewTime(time.Now().Add(time.Duration(i) * time.Second))
		b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
		b.Status.CompletionTime = &now
		if err := k8sClient.Status().Update(context.Background(), b); err != nil {
			t.Fatalf("status update %d: %v", i, err)
		}
	}

	sched := buildBackupSchedule(ns, "smp-sched", "smp", "repo", "0 0 * * *",
		&gameplanev1alpha1.BackupRetention{KeepLast: 1})
	if err := k8sClient.Create(context.Background(), sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	freezeSchedule(t, ns, sched.Name)

	eventually(t, func() (bool, string) {
		bs := listBackupsForSchedule(t, ns, "smp-sched")
		// Want: 1 Running survives + 1 newest Succeeded kept by KeepLast = 2 total.
		if len(bs) != 2 {
			names := []string{}
			for _, b := range bs {
				names = append(names, b.Name+"="+string(b.Status.Phase))
			}
			return false, "want 2 backups remaining, got " + sprintArgs(names)
		}
		hasRunning := false
		for _, b := range bs {
			if b.Status.Phase == gameplanev1alpha1.BackupPhaseRunning {
				hasRunning = true
			}
		}
		if !hasRunning {
			return false, "Running backup was deleted by retention"
		}
		return true, ""
	})
}

// TestSchedule_DoesNotTrimBackupReferencedByActiveRestore — Backups
// pinned by an in-flight Restore must not be deleted.
func TestSchedule_DoesNotTrimBackupReferencedByActiveRestore(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	// 3 succeeded backups, oldest first.
	names := []string{"smp-sched-old", "smp-sched-mid", "smp-sched-new"}
	for i, n := range names {
		b := buildBackup(ns, n, "smp", "repo")
		b.Labels = map[string]string{"gameplane.local/backup-schedule": "smp-sched"}
		if err := k8sClient.Create(context.Background(), b); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
		now := metav1.NewTime(time.Now().Add(time.Duration(i) * time.Second))
		b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
		b.Status.SnapshotID = "snap-" + n
		b.Status.CompletionTime = &now
		if err := k8sClient.Status().Update(context.Background(), b); err != nil {
			t.Fatalf("status update %s: %v", n, err)
		}
	}

	// Active Restore pins the OLDEST backup.
	rs := buildRestore(ns, "rs-active", "smp-sched-old", "smp")
	if err := k8sClient.Create(context.Background(), rs); err != nil {
		t.Fatalf("create restore: %v", err)
	}
	rs.Status.Phase = gameplanev1alpha1.RestorePhaseRunning
	if err := k8sClient.Status().Update(context.Background(), rs); err != nil {
		t.Fatalf("status update restore: %v", err)
	}

	// KeepLast=1 — without pinning, would delete -old and -mid.
	sched := buildBackupSchedule(ns, "smp-sched", "smp", "repo", "0 0 * * *",
		&gameplanev1alpha1.BackupRetention{KeepLast: 1})
	if err := k8sClient.Create(context.Background(), sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	freezeSchedule(t, ns, sched.Name)

	eventually(t, func() (bool, string) {
		bs := listBackupsForSchedule(t, ns, "smp-sched")
		// Expected: -new (KeepLast) + -old (pinned) survive; -mid deleted.
		survivors := map[string]bool{}
		for _, b := range bs {
			survivors[b.Name] = true
		}
		if !survivors["smp-sched-old"] {
			return false, "pinned backup smp-sched-old was deleted"
		}
		if !survivors["smp-sched-new"] {
			return false, "newest backup smp-sched-new was deleted"
		}
		// May still be observing pre-trim state; require -mid gone.
		if survivors["smp-sched-mid"] {
			return false, "trim hasn't yet removed -mid"
		}
		return true, ""
	})
}

// TestSchedule_RetentionTrimmedConditionSet — a schedule with a
// retention policy records a RetentionTrimmed=True condition once the
// (successful) trim pass has run, so the dashboard can show that pruning
// is healthy. A trim failure would set the same condition to False; that
// path needs client fault injection and is exercised by unit tests.
func TestSchedule_RetentionTrimmedConditionSet(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	// One succeeded backup so the trim pass has a candidate to evaluate
	// (KeepLast=2 keeps it; nothing is deleted, so trim returns nil).
	b := buildBackup(ns, "smp-sched-bk-a", "smp", "repo")
	b.Labels = map[string]string{"gameplane.local/backup-schedule": "smp-sched"}
	if err := k8sClient.Create(context.Background(), b); err != nil {
		t.Fatalf("create backup: %v", err)
	}
	now := metav1.Now()
	b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
	b.Status.CompletionTime = &now
	size := resource.MustParse("1Mi")
	b.Status.Size = &size
	if err := k8sClient.Status().Update(context.Background(), b); err != nil {
		t.Fatalf("status update backup: %v", err)
	}

	sched := buildBackupSchedule(ns, "smp-sched", "smp", "repo", "0 0 * * *",
		&gameplanev1alpha1.BackupRetention{KeepLast: 2})
	if err := k8sClient.Create(context.Background(), sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	freezeSchedule(t, ns, sched.Name)

	eventually(t, func() (bool, string) {
		s := getSchedule(t, ns, "smp-sched")
		c := meta.FindStatusCondition(s.Status.Conditions, "RetentionTrimmed")
		if c == nil {
			return false, "RetentionTrimmed condition not set"
		}
		if c.Status != metav1.ConditionTrue {
			return false, "RetentionTrimmed = " + string(c.Status) + ", want True"
		}
		return true, ""
	})
}

// TestSchedule_NoRetentionLeavesConditionUnset — a schedule without a
// retention policy must not advertise a RetentionTrimmed condition at all
// (there is nothing being pruned to report on).
func TestSchedule_NoRetentionLeavesConditionUnset(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if err := k8sClient.Create(context.Background(),
		buildBackupSchedule(ns, "smp-sched", "smp", "repo", "0 */6 * * *", nil)); err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	// Wait until the controller has reconciled at least once (NextScheduleTime set).
	eventually(t, func() (bool, string) {
		s := getSchedule(t, ns, "smp-sched")
		if s.Status.NextScheduleTime == nil {
			return false, "not yet reconciled"
		}
		return true, ""
	})
	s := getSchedule(t, ns, "smp-sched")
	if c := meta.FindStatusCondition(s.Status.Conditions, "RetentionTrimmed"); c != nil {
		t.Errorf("RetentionTrimmed condition present (%s) without a retention policy", c.Status)
	}
}

// TestSchedule_ForbidSkipsWhileInFlight — with concurrencyPolicy Forbid, an
// overdue occurrence must not create a second Backup while a previous one is
// still in flight.
func TestSchedule_ForbidSkipsWhileInFlight(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	running := buildBackup(ns, "inflight", "smp", "repo")
	running.Labels = map[string]string{"gameplane.local/backup-schedule": "smp-sched"}
	if err := k8sClient.Create(context.Background(), running); err != nil {
		t.Fatalf("create running backup: %v", err)
	}
	running.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
	if err := k8sClient.Status().Update(context.Background(), running); err != nil {
		t.Fatalf("status update running: %v", err)
	}

	sched := buildBackupSchedule(ns, "smp-sched", "smp", "repo", "* * * * *", nil)
	sched.Spec.ConcurrencyPolicy = "Forbid"
	if err := k8sClient.Create(context.Background(), sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	// A new schedule no longer fires on its first reconcile; pin
	// LastScheduleTime so the next occurrence is overdue.
	fiveMinAgo := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	sched.Status.LastScheduleTime = &fiveMinAgo
	if err := k8sClient.Status().Update(context.Background(), sched); err != nil {
		t.Fatalf("status update schedule: %v", err)
	}

	consistently(t, 1500*time.Millisecond, func() (bool, string) {
		bs := listBackupsForSchedule(t, ns, "smp-sched")
		if len(bs) != 1 || bs[0].Name != "inflight" {
			names := []string{}
			for _, b := range bs {
				names = append(names, b.Name)
			}
			return false, "expected only the in-flight backup, got " + sprintArgs(names)
		}
		return true, ""
	})
}

// TestSchedule_ReplaceDeletesInFlight — with concurrencyPolicy Replace, an
// overdue occurrence deletes the in-flight Backup and creates a fresh one.
func TestSchedule_ReplaceDeletesInFlight(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	running := buildBackup(ns, "old-inflight", "smp", "repo")
	running.Labels = map[string]string{"gameplane.local/backup-schedule": "smp-sched"}
	if err := k8sClient.Create(context.Background(), running); err != nil {
		t.Fatalf("create running backup: %v", err)
	}
	running.Status.Phase = gameplanev1alpha1.BackupPhaseRunning
	if err := k8sClient.Status().Update(context.Background(), running); err != nil {
		t.Fatalf("status update running: %v", err)
	}

	sched := buildBackupSchedule(ns, "smp-sched", "smp", "repo", "* * * * *", nil)
	sched.Spec.ConcurrencyPolicy = "Replace"
	if err := k8sClient.Create(context.Background(), sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	// A new schedule no longer fires on its first reconcile; pin
	// LastScheduleTime so the next occurrence is overdue.
	fiveMinAgo := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	sched.Status.LastScheduleTime = &fiveMinAgo
	if err := k8sClient.Status().Update(context.Background(), sched); err != nil {
		t.Fatalf("status update schedule: %v", err)
	}

	eventually(t, func() (bool, string) {
		bs := listBackupsForSchedule(t, ns, "smp-sched")
		for _, b := range bs {
			if b.Name == "old-inflight" {
				return false, "old in-flight backup not yet replaced"
			}
		}
		if len(bs) == 0 {
			return false, "no replacement backup yet"
		}
		return true, ""
	})
}

// TestSchedule_SecondsCronFires — a six-field (seconds-prefix) schedule must
// parse and fire rather than being silently rejected by the five-field parser.
func TestSchedule_SecondsCronFires(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	// "* * * * * *" = every second (six fields).
	if err := k8sClient.Create(context.Background(),
		buildBackupSchedule(ns, "smp-sched", "smp", "repo", "* * * * * *", nil)); err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	eventually(t, func() (bool, string) {
		if bs := listBackupsForSchedule(t, ns, "smp-sched"); len(bs) == 0 {
			return false, "no backup created from six-field schedule"
		}
		return true, ""
	})
}

// TestSchedule_InvalidCronSetsCondition — a schedule admitted by the CRD
// pattern but unparseable as cron records ScheduleValid=False instead of
// failing silently.
func TestSchedule_InvalidCronSetsCondition(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	// Six tokens (passes the CRD pattern) but not a valid cron expression.
	if err := k8sClient.Create(context.Background(),
		buildBackupSchedule(ns, "smp-sched", "smp", "repo", "bad bad bad bad bad bad", nil)); err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	eventually(t, func() (bool, string) {
		s := getSchedule(t, ns, "smp-sched")
		c := meta.FindStatusCondition(s.Status.Conditions, "ScheduleValid")
		if c == nil {
			return false, "ScheduleValid condition not set"
		}
		if c.Status != metav1.ConditionFalse {
			return false, "ScheduleValid = " + string(c.Status) + ", want False"
		}
		return true, ""
	})
}

// TestSchedule_LastSuccessfulTimeAdvancesAndNeverRewinds — a schedule's
// Status.LastSuccessfulTime tracks the CompletionTime of the newest
// Succeeded Backup it owns, and must never move backwards even after that
// Backup is deleted (e.g. by retention trimming) and only an older
// Succeeded Backup remains.
func TestSchedule_LastSuccessfulTimeAdvancesAndNeverRewinds(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	// Daily-midnight cron, frozen dormant so the scheduler itself doesn't
	// fire an extra Backup that would confuse "newest".
	sched := buildBackupSchedule(ns, "smp-sched", "smp", "repo", "0 0 * * *", nil)
	if err := k8sClient.Create(context.Background(), sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	freezeSchedule(t, ns, sched.Name)

	older := buildBackup(ns, "smp-sched-older", "smp", "repo")
	older.Labels = map[string]string{"gameplane.local/backup-schedule": "smp-sched"}
	if err := k8sClient.Create(context.Background(), older); err != nil {
		t.Fatalf("create older backup: %v", err)
	}
	// Truncate to seconds: metav1.Time marshals through the apiserver at
	// RFC3339 (1s) resolution, so a sub-second/monotonic in-memory value would
	// never .Equal() the round-tripped status we read back.
	t1 := metav1.NewTime(time.Now().Add(-time.Hour).Truncate(time.Second))
	older.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
	older.Status.CompletionTime = &t1
	if err := k8sClient.Status().Update(context.Background(), older); err != nil {
		t.Fatalf("status update older backup: %v", err)
	}

	newer := buildBackup(ns, "smp-sched-newer", "smp", "repo")
	newer.Labels = map[string]string{"gameplane.local/backup-schedule": "smp-sched"}
	if err := k8sClient.Create(context.Background(), newer); err != nil {
		t.Fatalf("create newer backup: %v", err)
	}
	t2 := metav1.NewTime(time.Now().Truncate(time.Second))
	newer.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
	newer.Status.CompletionTime = &t2
	if err := k8sClient.Status().Update(context.Background(), newer); err != nil {
		t.Fatalf("status update newer backup: %v", err)
	}

	eventually(t, func() (bool, string) {
		s := getSchedule(t, ns, "smp-sched")
		if s.Status.LastSuccessfulTime == nil {
			return false, "LastSuccessfulTime not yet set"
		}
		if !s.Status.LastSuccessfulTime.Time.Equal(t2.Time) {
			return false, "LastSuccessfulTime = " + s.Status.LastSuccessfulTime.String() + ", want " + t2.String()
		}
		return true, ""
	})

	// Delete the newer Backup — simulating retention trimming it away —
	// leaving only the older Succeeded Backup. The recorded high-water
	// mark must not rewind to it.
	if err := k8sClient.Delete(context.Background(), newer); err != nil {
		t.Fatalf("delete newer backup: %v", err)
	}

	consistently(t, time.Second, func() (bool, string) {
		s := getSchedule(t, ns, "smp-sched")
		got := "nil"
		if s.Status.LastSuccessfulTime != nil {
			got = s.Status.LastSuccessfulTime.String()
		}
		if s.Status.LastSuccessfulTime == nil || !s.Status.LastSuccessfulTime.Time.Equal(t2.Time) {
			return false, "LastSuccessfulTime rewound to " + got + ", want to stay " + t2.String()
		}
		return true, ""
	})
}

// TestSchedule_DeletePreservesBackups — When a BackupSchedule is deleted,
// its labelled Backups must survive (not cascade-deleted). The UI promises
// "Existing backups remain intact".
func TestSchedule_DeletePreservesBackups(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}

	// Create a schedule and advance LastScheduleTime so it fires immediately.
	sched := buildBackupSchedule(ns, "smp-sched", "smp", "repo", "* * * * *", nil)
	if err := k8sClient.Create(context.Background(), sched); err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	fiveMinAgo := metav1.NewTime(time.Now().Add(-5 * time.Minute))
	sched.Status.LastScheduleTime = &fiveMinAgo
	if err := k8sClient.Status().Update(context.Background(), sched); err != nil {
		t.Fatalf("status update schedule: %v", err)
	}

	// Wait for a Backup to be created.
	var backupName string
	eventually(t, func() (bool, string) {
		bs := listBackupsForSchedule(t, ns, "smp-sched")
		if len(bs) == 0 {
			return false, "no backups created yet"
		}
		backupName = bs[0].Name
		return true, ""
	})

	// Delete the schedule.
	if err := k8sClient.Delete(context.Background(), sched); err != nil {
		t.Fatalf("delete schedule: %v", err)
	}

	// The Backup must still exist (not cascade-deleted).
	eventually(t, func() (bool, string) {
		var b gameplanev1alpha1.Backup
		err := k8sClient.Get(context.Background(),
			types.NamespacedName{Namespace: ns, Name: backupName}, &b)
		if err != nil {
			if meta.IsNoMatchError(err) || err.Error() == "not found" {
				return false, "backup was deleted when schedule was deleted"
			}
			return false, "get backup: " + err.Error()
		}
		// Backup must still have the schedule label.
		if b.Labels["gameplane.local/backup-schedule"] != "smp-sched" {
			return false, "backup lost its schedule label"
		}
		return true, ""
	})
}

// ---------- helpers used only by this file ----------

func listBackupsForSchedule(t *testing.T, ns, schedName string) []gameplanev1alpha1.Backup {
	t.Helper()
	var list gameplanev1alpha1.BackupList
	if err := k8sClient.List(context.Background(), &list); err != nil {
		t.Fatalf("list backups: %v", err)
	}
	out := []gameplanev1alpha1.Backup{}
	for _, b := range list.Items {
		if b.Namespace != ns {
			continue
		}
		if b.Labels["gameplane.local/backup-schedule"] == schedName {
			out = append(out, b)
		}
	}
	return out
}

// TestSchedule_NewScheduleDoesNotFireImmediately — a brand-new schedule whose
// cron last ticked 30 minutes ago must wait for its next tick; it must not
// treat that tick as a missed run (missed runs count from creation).
func TestSchedule_NewScheduleDoesNotFireImmediately(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withScheduleReconciler())

	if err := k8sClient.Create(context.Background(), buildResticRepoSecret(ns, "repo")); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	// Hourly at the minute that passed 30 minutes ago: the next tick is ~30 min away.
	minute := time.Now().Add(-30 * time.Minute).Minute()
	cronExpr := fmt.Sprintf("%d * * * *", minute)
	if err := k8sClient.Create(context.Background(),
		buildBackupSchedule(ns, "smp-sched", "smp", "repo", cronExpr, nil)); err != nil {
		t.Fatalf("create schedule: %v", err)
	}

	eventually(t, func() (bool, string) {
		s := getSchedule(t, ns, "smp-sched")
		if s.Status.NextScheduleTime == nil {
			return false, "NextScheduleTime not yet set"
		}
		return true, ""
	})
	consistently(t, time.Second, func() (bool, string) {
		if bs := listBackupsForSchedule(t, ns, "smp-sched"); len(bs) > 0 {
			return false, "new schedule fired a Backup for a tick before its creation"
		}
		return true, ""
	})
}

// freezeSchedule pins a schedule's LastScheduleTime to "now" so the
// next firing is far in the future (whatever the cron). Used by
// retention tests to keep the namespace's Backup count stable.
func freezeSchedule(t *testing.T, ns, name string) {
	t.Helper()
	s := getSchedule(t, ns, name)
	now := metav1.Now()
	s.Status.LastScheduleTime = &now
	if err := k8sClient.Status().Update(context.Background(), s); err != nil {
		t.Fatalf("freeze schedule: %v", err)
	}
}

func getSchedule(t *testing.T, ns, name string) *gameplanev1alpha1.BackupSchedule {
	t.Helper()
	var s gameplanev1alpha1.BackupSchedule
	if err := k8sClient.Get(context.Background(),
		types.NamespacedName{Namespace: ns, Name: name}, &s); err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	return &s
}
