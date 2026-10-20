package main

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// TestBuildViewsOverAYearOfAggregatesIsFast seeds 365 days at 10,000 reports
// per day as aggregates and asserts that the heaviest range builds in under
// three seconds (SC-010).
func TestBuildViewsOverAYearOfAggregatesIsFast(t *testing.T) {
	st := openTestStore(t.Context(), t, config{activityExpiryDays: 90})
	ctx := context.Background()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	const days, perDay = 365, 10000
	versions := []string{"0.1.0", "0.2.0", "0.3.0", "0.3.1", "dev", "other", "invalid"}
	err := st.writeTx(ctx, func(tx *sql.Tx) error {
		exec := func(q string, args ...any) error {
			if _, err := tx.ExecContext(ctx, q, args...); err != nil {
				return fmt.Errorf("seed %q: %w", q, err)
			}
			return nil
		}
		for i := range days {
			day := dayString(now.AddDate(0, 0, -1-i))
			if err := exec(`INSERT INTO daily_basic (day, reports, duplicates, servers_sum, templates_sum) VALUES (?, ?, 50, ?, ?)`,
				day, perDay, perDay*3, perDay*5); err != nil {
				return err
			}
			if err := exec(`INSERT INTO daily_ext (day, ext_reports, active_installs, new_installs, lapsed_installs) VALUES (?, ?, ?, 20, 10)`,
				day, perDay/2, perDay/2); err != nil {
				return err
			}
			for _, v := range versions {
				if err := exec(`INSERT INTO daily_version (day, version, reports) VALUES (?, ?, ?)`, day, v, perDay/len(versions)); err != nil {
					return err
				}
			}
			for _, metric := range []string{"servers", "templates"} {
				for value := 0; value <= 40; value++ {
					if err := exec(`INSERT INTO daily_fleet (day, metric, value, reports) VALUES (?, ?, ?, ?)`, day, metric, value, perDay/41); err != nil {
						return err
					}
				}
			}
			for k := range 40 {
				if err := exec(`INSERT INTO daily_dim (day, dim, value, installs) VALUES (?, 'k8s', ?, ?)`,
					day, fmt.Sprintf("1.%d", k), perDay/80); err != nil {
					return err
				}
			}
			for k := range 12 {
				if err := exec(`INSERT INTO daily_game (day, module, installs, servers) VALUES (?, ?, ?, ?)`,
					day, fmt.Sprintf("game-%d", k), perDay/24, perDay/8); err != nil {
					return err
				}
			}
		}
		for i := range 20000 {
			if err := exec(`INSERT INTO activity (id_hmac, key_fp, first_seen, last_seen, last_sent_at, last_version) VALUES (?, 'k', '2026-01-01', ?, '2026-01-01T00:00:00Z', ?)`,
				fmt.Sprintf("id-%d", i), dayString(now.AddDate(0, 0, -1-i%60)), versions[i%len(versions)]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	start := time.Now()
	v, err := BuildViews(ctx, st, 365, now)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("BuildViews: %v", err)
	}
	if v.Empty || v.Basic == nil || v.Extended == nil {
		t.Fatalf("BuildViews returned %+v, want populated basic and extended blocks", v)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("BuildViews(365) took %v, want under 3s (SC-010)", elapsed)
	}
}
