package controller

import (
	"context"
	"fmt"
	"sort"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// trimBackups enforces the schedule's retention policy by deleting
// Backups that fall outside all keep-* buckets. Backup objects still
// in-flight (Phase != Succeeded) are never deleted. Without a positive
// keep rule, retention is disabled.
func (r *BackupScheduleReconciler) trimBackups(
	ctx context.Context, sched *gameplanev1alpha1.BackupSchedule,
) error {
	ret := sched.Spec.Retention
	if ret == nil || (ret.KeepLast <= 0 && ret.KeepHourly <= 0 &&
		ret.KeepDaily <= 0 && ret.KeepWeekly <= 0 &&
		ret.KeepMonthly <= 0 && ret.KeepYearly <= 0) {
		return nil
	}

	var list gameplanev1alpha1.BackupList
	if err := r.List(ctx, &list,
		client.InNamespace(sched.Namespace),
		client.MatchingLabels{"gameplane.local/backup-schedule": sched.Name},
	); err != nil {
		return err
	}

	// Don't yank a backup out from under an in-flight Restore.
	var restores gameplanev1alpha1.RestoreList
	if err := r.List(ctx, &restores, client.InNamespace(sched.Namespace)); err != nil {
		return err
	}
	pinned := map[string]bool{}
	for _, rs := range restores.Items {
		switch rs.Status.Phase {
		case gameplanev1alpha1.RestorePhaseSucceeded, gameplanev1alpha1.RestorePhaseFailed:
			continue
		}
		pinned[rs.Spec.BackupRef.Name] = true
	}

	// Only succeeded backups are candidates for trimming.
	succeeded := make([]gameplanev1alpha1.Backup, 0, len(list.Items))
	for _, b := range list.Items {
		if b.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded {
			continue
		}
		if b.Status.CompletionTime == nil {
			continue
		}
		// A Backup already being deleted is held by its snapshot finalizer and
		// still reads Succeeded. Counting it would shift the keep window past
		// Backups that should stay and re-issue a Delete already in flight.
		if !b.DeletionTimestamp.IsZero() {
			continue
		}
		succeeded = append(succeeded, b)
	}
	if len(succeeded) == 0 {
		return nil
	}

	// Newest first.
	sort.Slice(succeeded, func(i, j int) bool {
		return succeeded[i].Status.CompletionTime.After(succeeded[j].Status.CompletionTime.Time)
	})

	keep := selectKept(succeeded, ret)

	for i := range succeeded {
		if keep[succeeded[i].Name] || pinned[succeeded[i].Name] {
			continue
		}
		target := succeeded[i]
		if err := r.Delete(ctx, &target); err != nil {
			return err
		}
	}
	return nil
}

// selectKept returns the set of Backup names that must be kept under
// the given retention policy. The policy mirrors restic's keep-*
// semantics: for each time bucket (hourly, daily, …) and for each of
// the most recent N distinct bucket keys, keep the newest Backup in
// that bucket. KeepLast is a simple "keep the N newest" clause.
func selectKept(
	newestFirst []gameplanev1alpha1.Backup, ret *gameplanev1alpha1.BackupRetention,
) map[string]bool {
	keep := map[string]bool{}

	if ret.KeepLast > 0 {
		n := int(ret.KeepLast)
		if n > len(newestFirst) {
			n = len(newestFirst)
		}
		for i := 0; i < n; i++ {
			keep[newestFirst[i].Name] = true
		}
	}

	bucket := func(t time.Time, which string) string {
		switch which {
		case "hour":
			return t.UTC().Format("2006-01-02T15")
		case "day":
			return t.UTC().Format("2006-01-02")
		case "week":
			y, w := t.UTC().ISOWeek()
			return isoWeekKey(y, w)
		case "month":
			return t.UTC().Format("2006-01")
		case "year":
			return t.UTC().Format("2006")
		}
		return ""
	}

	keepByBucket := func(n int32, which string) {
		if n <= 0 {
			return
		}
		seen := map[string]bool{}
		for i := range newestFirst {
			b := newestFirst[i]
			k := bucket(b.Status.CompletionTime.Time, which)
			if seen[k] {
				continue
			}
			seen[k] = true
			keep[b.Name] = true
			if len(seen) >= int(n) {
				return
			}
		}
	}

	keepByBucket(ret.KeepHourly, "hour")
	keepByBucket(ret.KeepDaily, "day")
	keepByBucket(ret.KeepWeekly, "week")
	keepByBucket(ret.KeepMonthly, "month")
	keepByBucket(ret.KeepYearly, "year")

	return keep
}

func isoWeekKey(year, week int) string {
	return fmt.Sprintf("%04d-W%02d", year, week)
}
