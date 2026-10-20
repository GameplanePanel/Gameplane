package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// dailyTables are the day-keyed tables the retention sweep covers.
var dailyTables = []string{"daily_basic", "daily_version", "daily_fleet", "daily_ext", "daily_dim", "daily_game", "daily_install_version", "daily_version_change"}

var lifecycleNow = time.Date(2026, 10, 7, 3, 30, 0, 0, time.UTC) // yesterday is 2026-10-06

func TestPendingDays(t *testing.T) {
	cases := []struct {
		name    string
		through string
		want    []string
	}{
		{"never run gives yesterday only", "", []string{"2026-10-06"}},
		{"unparsable marker gives yesterday only", "garbage", []string{"2026-10-06"}},
		{"already through yesterday", "2026-10-06", nil},
		{"marker in the future", "2026-12-01", nil},
		{"one day behind", "2026-10-05", []string{"2026-10-06"}},
		{"three days behind", "2026-10-03", []string{"2026-10-04", "2026-10-05", "2026-10-06"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := pendingDays(tc.through, lifecycleNow); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("pendingDays(%q) = %v, want %v", tc.through, got, tc.want)
			}
		})
	}
	long := pendingDays("2000-01-01", lifecycleNow)
	if len(long) != maxRolloverCatchup || long[len(long)-1] != "2026-10-06" {
		t.Fatalf("a long outage returned %d days ending %s, want %d ending 2026-10-06",
			len(long), long[len(long)-1], maxRolloverCatchup)
	}
}

// seedAllDaily puts one row in every daily_* table for day, plus an
// activity record that the retention sweep must not touch.
func seedAllDaily(t *testing.T, st *store, day string) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO daily_basic (day, reports) VALUES (?, 1)`,
		`INSERT INTO daily_version (day, version, reports) VALUES (?, '1.0.0', 1)`,
		`INSERT INTO daily_fleet (day, metric, value, reports) VALUES (?, 'servers', 1, 1)`,
		`INSERT INTO daily_ext (day, ext_reports) VALUES (?, 1)`,
		`INSERT INTO daily_dim (day, dim, value, installs) VALUES (?, 'k8s', '1.31', 1)`,
		`INSERT INTO daily_game (day, module, installs, servers) VALUES (?, 'custom', 1, 1)`,
		`INSERT INTO daily_install_version (day, version, installs) VALUES (?, '1.0.0', 1)`,
		`INSERT INTO daily_version_change (day, kind, installs) VALUES (?, 'upgraded', 1)`,
	} {
		if _, err := st.db.ExecContext(ctx, q, day); err != nil {
			t.Fatalf("seed %s: %v", q, err)
		}
	}
}

func dailyRows(t *testing.T, st *store, day string) int {
	t.Helper()
	total := 0
	for _, table := range dailyTables {
		total += countRows(t.Context(), t, st, "SELECT count(*) FROM "+table+" WHERE day = ?", day)
	}
	return total
}

func TestLifecycleDeletesDailyRowsOlderThanRetention(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	ctx := context.Background()
	// With retention 365 and today 2026-10-07 the cutoff is 2025-10-07:
	// that day is kept, the day before it is deleted.
	seedAllDaily(t, st, "2025-10-06")
	seedAllDaily(t, st, "2025-10-07")
	seedAllDaily(t, st, "2026-10-06")
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO activity (id_hmac, key_fp, first_seen, last_seen, last_sent_at, last_version)
		 VALUES ('h', 'k', '2020-01-01', '2020-01-02', '2020-01-02T00:00:00Z', '1.0.0')`); err != nil {
		t.Fatal(err)
	}
	if err := st.lifecycleOnce(ctx, lifecycleNow, 365); err != nil {
		t.Fatalf("lifecycleOnce: %v", err)
	}
	if got := dailyRows(t, st, "2025-10-06"); got != 0 {
		t.Errorf("rows older than the retention remain: %d", got)
	}
	if got := dailyRows(t, st, "2025-10-07"); got != len(dailyTables) {
		t.Errorf("rows on the cutoff day = %d, want %d", got, len(dailyTables))
	}
	if got := dailyRows(t, st, "2026-10-06"); got != len(dailyTables) {
		t.Errorf("recent rows = %d, want %d", got, len(dailyTables))
	}
	if got := countRows(t.Context(), t, st, `SELECT count(*) FROM activity`); got != 1 {
		t.Errorf("the retention sweep touched activity: %d rows", got)
	}
	if got := mustMeta(t, st, "reports_total"); got != "0" {
		t.Errorf("the sweep changed reports_total: %s", got)
	}
}

func TestLifecycleIsIdempotentAndResumesFromRolloverMarker(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	ctx := context.Background()
	if _, ok, err := st.metaGet(ctx, "rollover_through"); err != nil || ok {
		t.Fatalf("rollover_through before the first run: ok=%v err=%v, want unset", ok, err)
	}
	seedAllDaily(t, st, "2026-10-06")
	for range 3 {
		if err := st.lifecycleOnce(ctx, lifecycleNow, 365); err != nil {
			t.Fatal(err)
		}
		if got := mustMeta(t, st, "rollover_through"); got != "2026-10-06" {
			t.Fatalf("rollover_through = %s, want 2026-10-06", got)
		}
		if got := dailyRows(t, st, "2026-10-06"); got != len(dailyTables) {
			t.Fatalf("a repeated run changed the data: %d rows", got)
		}
	}
	// A later run resumes from the marker and advances it.
	if err := st.lifecycleOnce(ctx, lifecycleNow.Add(48*time.Hour), 365); err != nil {
		t.Fatal(err)
	}
	if got := mustMeta(t, st, "rollover_through"); got != "2026-10-08" {
		t.Fatalf("rollover_through = %s, want 2026-10-08", got)
	}
	// The marker never moves backwards, e.g. after a clock correction.
	if err := st.lifecycleOnce(ctx, lifecycleNow, 365); err != nil {
		t.Fatal(err)
	}
	if got := mustMeta(t, st, "rollover_through"); got != "2026-10-08" {
		t.Fatalf("rollover_through moved back to %s", got)
	}
}

func TestLifecycleWithoutRetentionDeletesNothing(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	ctx := context.Background()
	seedAllDaily(t, st, "2001-01-01")
	for _, retention := range []int{0, -1} {
		if err := st.lifecycleOnce(ctx, lifecycleNow, retention); err != nil {
			t.Fatal(err)
		}
		if got := dailyRows(t, st, "2001-01-01"); got != len(dailyTables) {
			t.Fatalf("retention %d deleted rows: %d left", retention, got)
		}
	}
	if got := mustMeta(t, st, "rollover_through"); got != "2026-10-06" {
		t.Fatalf("rollover_through = %s, want 2026-10-06", got)
	}
}

func TestLifecycleFailsOnClosedStore(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	if err := st.close(); err != nil {
		t.Fatal(err)
	}
	if err := st.lifecycleOnce(context.Background(), lifecycleNow, 365); err == nil {
		t.Fatal("lifecycleOnce on a closed store: want an error")
	}
}

func TestRunLifecycleRunsImmediatelyAndStopsOnCancel(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	seedAllDaily(t, st, "2020-01-01")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		st.runLifecycle(ctx, 365, func() time.Time { return lifecycleNow })
	}()
	deadline := time.Now().Add(5 * time.Second)
	for dailyRows(t, st, "2020-01-01") != 0 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("the first lifecycle pass did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runLifecycle did not stop after the context was cancelled")
	}
}

func TestRunLifecycleLogsFailuresAndKeepsGoing(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	if err := st.close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		st.runLifecycle(ctx, 365, func() time.Time { return lifecycleNow })
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runLifecycle did not stop after a failed pass")
	}
}
