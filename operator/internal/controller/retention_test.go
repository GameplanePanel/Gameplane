package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// selectKept is pure, so we unit test it directly without envtest.
func TestSelectKept_KeepLast(t *testing.T) {
	backups := fakeBackups(10, time.Hour)
	keep := selectKept(backups, &gameplanev1alpha1.BackupRetention{KeepLast: 3})
	if len(keep) != 3 {
		t.Fatalf("KeepLast=3 expected 3 kept, got %d: %v", len(keep), keep)
	}
	for i := 0; i < 3; i++ {
		if !keep[backups[i].Name] {
			t.Errorf("expected newest[%d]=%s to be kept", i, backups[i].Name)
		}
	}
}

func TestSelectKept_DailyBucketing(t *testing.T) {
	// 6 backups across 3 days (2 per day).
	base := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	items := []gameplanev1alpha1.Backup{
		fakeBackup("newest-d2", base),
		fakeBackup("older-d2", base.Add(-time.Hour)),
		fakeBackup("newest-d1", base.Add(-24*time.Hour)),
		fakeBackup("older-d1", base.Add(-25*time.Hour)),
		fakeBackup("newest-d0", base.Add(-48*time.Hour)),
		fakeBackup("older-d0", base.Add(-49*time.Hour)),
	}
	keep := selectKept(items, &gameplanev1alpha1.BackupRetention{KeepDaily: 2})

	// Expect the newest-of-day for days 2 and 1 kept, nothing from day 0.
	for _, name := range []string{"newest-d2", "newest-d1"} {
		if !keep[name] {
			t.Errorf("expected %s kept", name)
		}
	}
	for _, name := range []string{"older-d2", "older-d1", "newest-d0", "older-d0"} {
		if keep[name] {
			t.Errorf("did not expect %s kept", name)
		}
	}
}

func TestSelectKept_CombinedPolicies(t *testing.T) {
	// Fixed midday-UTC base so a/b/c share one calendar day and d lands on the
	// prior day. selectKept buckets by UTC calendar day, so time.Now() here
	// flakes when the test runs within ~2h after UTC midnight (c spills into
	// the previous day and becomes the newest "daily", not d).
	base := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	items := []gameplanev1alpha1.Backup{
		fakeBackup("a", base),
		fakeBackup("b", base.Add(-time.Hour)),
		fakeBackup("c", base.Add(-2*time.Hour)),
		fakeBackup("d", base.Add(-24*time.Hour)),
	}
	keep := selectKept(items, &gameplanev1alpha1.BackupRetention{KeepLast: 1, KeepDaily: 2})
	if !keep["a"] {
		t.Error("KeepLast=1 should keep a")
	}
	if !keep["d"] {
		t.Error("KeepDaily=2 should cover yesterday (d)")
	}
}

func fakeBackup(name string, completion time.Time) gameplanev1alpha1.Backup {
	ct := metav1.NewTime(completion)
	return gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: gameplanev1alpha1.BackupStatus{
			Phase:          gameplanev1alpha1.BackupPhaseSucceeded,
			CompletionTime: &ct,
		},
	}
}

func fakeBackups(n int, spacing time.Duration) []gameplanev1alpha1.Backup {
	out := make([]gameplanev1alpha1.Backup, n)
	base := time.Now()
	for i := 0; i < n; i++ {
		name := "b" + string(rune('a'+i))
		out[i] = fakeBackup(name, base.Add(-time.Duration(i)*spacing))
	}
	return out
}
