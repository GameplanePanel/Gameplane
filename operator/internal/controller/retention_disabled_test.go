package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/ValgulNecron/gameplane/operator/api/v1alpha1"
)

// Disabled retention must neither query the API nor start snapshot cleanup by
// deleting a succeeded Backup that carries the snapshot finalizer.
func TestTrimBackups_DisabledRetention(t *testing.T) {
	cases := []struct {
		name      string
		retention *gameplanev1alpha1.BackupRetention
	}{
		{name: "nil"},
		{name: "empty", retention: &gameplanev1alpha1.BackupRetention{}},
		{name: "all zero", retention: &gameplanev1alpha1.BackupRetention{
			KeepLast: 0, KeepHourly: 0, KeepDaily: 0, KeepWeekly: 0, KeepMonthly: 0, KeepYearly: 0,
		}},
		{name: "all negative", retention: &gameplanev1alpha1.BackupRetention{
			KeepLast: -1, KeepHourly: -1, KeepDaily: -1, KeepWeekly: -1, KeepMonthly: -1, KeepYearly: -1,
		}},
		{name: "zero and negative", retention: &gameplanev1alpha1.BackupRetention{
			KeepLast: -1, KeepDaily: -1, KeepMonthly: -1,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := scrapeScheme(t)
			newest := retentionBackupWithSnapshotFinalizer("newest", 2026)
			oldest := retentionBackupWithSnapshotFinalizer("oldest", 2024)
			cl := &retentionListCountingClient{Client: fake.NewClientBuilder().
				WithScheme(s).WithObjects(newest, oldest).Build()}
			r := &BackupScheduleReconciler{Client: cl, Scheme: s}
			sched := &gameplanev1alpha1.BackupSchedule{
				ObjectMeta: metav1.ObjectMeta{Name: "sched", Namespace: "ns"},
				Spec:       gameplanev1alpha1.BackupScheduleSpec{Retention: tc.retention},
			}

			if err := r.trimBackups(context.Background(), sched); err != nil {
				t.Fatalf("trimBackups: %v", err)
			}
			if cl.listCalls != 0 {
				t.Errorf("disabled retention listed API objects %d times, want zero", cl.listCalls)
			}
			for _, name := range []string{"newest", "oldest"} {
				var got gameplanev1alpha1.Backup
				if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: name}, &got); err != nil {
					t.Fatalf("get %s: %v", name, err)
				}
				if !got.DeletionTimestamp.IsZero() {
					t.Errorf("%s is being deleted while retention is disabled", name)
				}
			}
		})
	}
}

// Each keep rule independently enables trimming, including when every other
// rule is negative. The older Backup is in a different bucket for every rule.
func TestTrimBackups_AnyPositiveRuleEnablesRetention(t *testing.T) {
	cases := []struct {
		name      string
		retention gameplanev1alpha1.BackupRetention
	}{
		{name: "last", retention: gameplanev1alpha1.BackupRetention{
			KeepLast: 1, KeepHourly: -1, KeepDaily: -1, KeepWeekly: -1, KeepMonthly: -1, KeepYearly: -1,
		}},
		{name: "hourly", retention: gameplanev1alpha1.BackupRetention{
			KeepLast: -1, KeepHourly: 1, KeepDaily: -1, KeepWeekly: -1, KeepMonthly: -1, KeepYearly: -1,
		}},
		{name: "daily", retention: gameplanev1alpha1.BackupRetention{
			KeepLast: -1, KeepHourly: -1, KeepDaily: 1, KeepWeekly: -1, KeepMonthly: -1, KeepYearly: -1,
		}},
		{name: "weekly", retention: gameplanev1alpha1.BackupRetention{
			KeepLast: -1, KeepHourly: -1, KeepDaily: -1, KeepWeekly: 1, KeepMonthly: -1, KeepYearly: -1,
		}},
		{name: "monthly", retention: gameplanev1alpha1.BackupRetention{
			KeepLast: -1, KeepHourly: -1, KeepDaily: -1, KeepWeekly: -1, KeepMonthly: 1, KeepYearly: -1,
		}},
		{name: "yearly", retention: gameplanev1alpha1.BackupRetention{
			KeepLast: -1, KeepHourly: -1, KeepDaily: -1, KeepWeekly: -1, KeepMonthly: -1, KeepYearly: 1,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := scrapeScheme(t)
			cl := fake.NewClientBuilder().WithScheme(s).
				WithObjects(retentionBackupWithSnapshotFinalizer("newest", 2026), retentionBackupWithSnapshotFinalizer("oldest", 2024)).Build()
			r := &BackupScheduleReconciler{Client: cl, Scheme: s}
			sched := &gameplanev1alpha1.BackupSchedule{
				ObjectMeta: metav1.ObjectMeta{Name: "sched", Namespace: "ns"},
				Spec:       gameplanev1alpha1.BackupScheduleSpec{Retention: &tc.retention},
			}

			if err := r.trimBackups(context.Background(), sched); err != nil {
				t.Fatalf("trimBackups: %v", err)
			}
			var newest, oldest gameplanev1alpha1.Backup
			if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "newest"}, &newest); err != nil {
				t.Fatalf("get newest: %v", err)
			}
			if !newest.DeletionTimestamp.IsZero() {
				t.Error("newest Backup is being deleted, want it kept")
			}
			if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: "oldest"}, &oldest); err != nil {
				t.Fatalf("get oldest: %v", err)
			}
			if oldest.DeletionTimestamp.IsZero() {
				t.Error("oldest Backup has no deletion timestamp, want it trimmed")
			}
		})
	}
}

type retentionListCountingClient struct {
	client.Client
	listCalls int
}

func (c *retentionListCountingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	c.listCalls++
	return c.Client.List(ctx, list, opts...)
}

func retentionBackupWithSnapshotFinalizer(name string, year int) *gameplanev1alpha1.Backup {
	ct := metav1.NewTime(time.Date(year, time.June, 15, 12, 0, 0, 0, time.UTC))
	return &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "ns",
			Labels:     map[string]string{"gameplane.local/backup-schedule": "sched"},
			Finalizers: []string{gameplanev1alpha1.BackupSnapshotFinalizer},
		},
		Status: gameplanev1alpha1.BackupStatus{
			Phase:          gameplanev1alpha1.BackupPhaseSucceeded,
			CompletionTime: &ct,
		},
	}
}
