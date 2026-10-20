package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestIngestWritesBasicAggregatesInOneTransaction(t *testing.T) {
	s := newServer(config{})
	now := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	for _, body := range []string{
		`{"version":"1.0.0","servers":2,"templates":5}`,
		`{"version":"1.0.0","servers":0,"templates":300}`,
		`{"version":"2.0.0","servers":5000,"templates":5}`,
		`{"version":"<bad>","servers":1,"templates":1}`,
	} {
		if code := ingestFrom(t, s, "192.0.2.1:1000", body); code != http.StatusNoContent {
			t.Fatalf("%s: status = %d, want 204", body, code)
		}
	}
	now = now.Add(24 * time.Hour)
	if code := ingestFrom(t, s, "192.0.2.1:1000", `{"version":"1.0.0","servers":3,"templates":3}`); code != http.StatusNoContent {
		t.Fatalf("next day: status = %d", code)
	}

	st := s.store
	day := "2026-10-07"
	if got := countRows(t.Context(), t, st, `SELECT reports FROM daily_basic WHERE day = ?`, day); got != 4 {
		t.Errorf("daily_basic.reports = %d, want 4", got)
	}
	if got := countRows(t.Context(), t, st, `SELECT servers_sum FROM daily_basic WHERE day = ?`, day); got != 2+0+5000+1 {
		t.Errorf("daily_basic.servers_sum = %d, want 5003", got)
	}
	if got := countRows(t.Context(), t, st, `SELECT templates_sum FROM daily_basic WHERE day = ?`, day); got != 5+300+5+1 {
		t.Errorf("daily_basic.templates_sum = %d, want 311", got)
	}
	if got := countRows(t.Context(), t, st, `SELECT duplicates FROM daily_basic WHERE day = ?`, day); got != 0 {
		t.Errorf("daily_basic.duplicates = %d, want 0", got)
	}
	for version, want := range map[string]int{"1.0.0": 2, "2.0.0": 1, "invalid": 1} {
		if got := countRows(t.Context(), t, st, `SELECT reports FROM daily_version WHERE day = ? AND version = ?`, day, version); got != want {
			t.Errorf("daily_version[%s] = %d, want %d", version, got, want)
		}
	}
	if got := countRows(t.Context(), t, st, `SELECT count(*) FROM daily_version WHERE day = ?`, day); got != 3 {
		t.Errorf("daily_version rows = %d, want 3", got)
	}
	// Fleet values are exact up to 1000 and 5000 shares the 1001 row.
	for _, f := range []struct {
		metric string
		value  int
		want   int
	}{
		{"servers", 0, 1}, {"servers", 1, 1}, {"servers", 2, 1}, {"servers", 1001, 1},
		{"templates", 5, 2}, {"templates", 300, 1}, {"templates", 1, 1},
	} {
		got := countRows(t.Context(), t, st, `SELECT reports FROM daily_fleet WHERE day = ? AND metric = ? AND value = ?`, day, f.metric, f.value)
		if got != f.want {
			t.Errorf("daily_fleet[%s=%d] = %d, want %d", f.metric, f.value, got, f.want)
		}
	}
	if got := countRows(t.Context(), t, st, `SELECT count(*) FROM daily_fleet WHERE value > 1001`); got != 0 {
		t.Errorf("%d fleet rows above the 1001 cap", got)
	}
	// The second day has its own rows and the total counts every report.
	if got := countRows(t.Context(), t, st, `SELECT reports FROM daily_basic WHERE day = ?`, "2026-10-08"); got != 1 {
		t.Errorf("next day daily_basic.reports = %d, want 1", got)
	}
	if got := mustMeta(t, st, "reports_total"); got != "5" {
		t.Errorf("reports_total = %s, want 5", got)
	}
	// Nothing extended or identifying was written.
	for _, table := range []string{"daily_ext", "daily_dim", "daily_game", "activity"} {
		if got := countRows(t.Context(), t, st, "SELECT count(*) FROM "+table); got != 0 {
			t.Errorf("%s has %d rows after basic reports, want 0", table, got)
		}
	}
}

func TestIngestClampsSummedCounts(t *testing.T) {
	s := newServer(config{})
	s.now = func() time.Time { return time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC) }
	for range 2 {
		if code := ingestFrom(t, s, "192.0.2.1:1000", `{"version":"1.0.0","servers":5000000,"templates":7000000}`); code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", code)
		}
	}
	if got := countRows(t.Context(), t, s.store, `SELECT servers_sum FROM daily_basic`); got != 2*maxSummedCount {
		t.Errorf("servers_sum = %d, want %d", got, 2*maxSummedCount)
	}
	if got := countRows(t.Context(), t, s.store, `SELECT templates_sum FROM daily_basic`); got != 2*maxSummedCount {
		t.Errorf("templates_sum = %d, want %d", got, 2*maxSummedCount)
	}
	if got := countRows(t.Context(), t, s.store, `SELECT reports FROM daily_fleet WHERE metric = 'servers' AND value = 1001`); got != 2 {
		t.Errorf("fleet 1001 row = %d, want 2", got)
	}
}

func TestIngestStoreFailureIs500AndCountsNothing(t *testing.T) {
	s := newServer(config{})
	if err := s.store.close(); err != nil {
		t.Fatal(err)
	}
	if code := ingestFrom(t, s, "192.0.2.1:1000", okReport); code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", code)
	}
	if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "1.0.0"); got != 0 {
		t.Fatalf("a report that was not stored was counted: %v", got)
	}
}

func TestRecordBasicRollsBackWhenAnyStatementFails(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	ctx := context.Background()
	if _, err := st.db.ExecContext(ctx, "DROP TABLE daily_fleet"); err != nil {
		t.Fatal(err)
	}
	if err := st.recordBasic(ctx, "2026-10-07", "1.0.0", 1, 1); err == nil {
		t.Fatal("recordBasic with a missing table: want an error")
	}
	for _, q := range []string{`SELECT count(*) FROM daily_basic`, `SELECT count(*) FROM daily_version`} {
		if got := countRows(t.Context(), t, st, q); got != 0 {
			t.Errorf("%s = %d after a failed write, want 0 (rolled back)", q, got)
		}
	}
	if got := mustMeta(t, st, "reports_total"); got != "0" {
		t.Errorf("reports_total = %s after a failed write, want 0", got)
	}
}

func TestWriteTxCommitsOnSuccessAndRollsBackOnError(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	ctx := context.Background()
	insert := func(tx *sql.Tx, key string) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, 'v')`, key)
		return err
	}
	if err := st.writeTx(ctx, func(tx *sql.Tx) error { return insert(tx, "kept") }); err != nil {
		t.Fatalf("writeTx: %v", err)
	}
	boom := errors.New("boom")
	err := st.writeTx(ctx, func(tx *sql.Tx) error {
		if err := insert(tx, "dropped"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("writeTx error = %v, want it to wrap boom", err)
	}
	if got := countRows(t.Context(), t, st, `SELECT count(*) FROM meta WHERE key = 'kept'`); got != 1 {
		t.Errorf("committed row count = %d, want 1", got)
	}
	if got := countRows(t.Context(), t, st, `SELECT count(*) FROM meta WHERE key = 'dropped'`); got != 0 {
		t.Errorf("rolled-back row count = %d, want 0", got)
	}
	if err := st.close(); err != nil {
		t.Fatal(err)
	}
	if err := st.writeTx(ctx, func(*sql.Tx) error { return nil }); err == nil {
		t.Fatal("writeTx on a closed store: want an error")
	}
}
