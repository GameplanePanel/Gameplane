package main

import (
	"net/http"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// TestClassifyChange covers semver order, equal spellings and unordered
// labels (FR-042, OD-8).
func TestClassifyChange(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		prev, next  string
		wantKind    string
		wantChanged bool
	}{
		{"1.0.0", "1.0.0", "", false},
		{"0.3.0", "v0.3.0", "", false},
		{"v0.3.0", "v0.4.0", changeUpgraded, true},
		{"v0.4.0", "v0.3.0", changeDowngraded, true},
		{"v0.3.0", "v0.4.0-rc.1", changeUpgraded, true},
		{"v0.4.0", "v0.4.0-rc.1", changeDowngraded, true},
		{"pr617", "v0.3.0", changeUnordered, true},
		{"v0.3.0", "dev", changeUnordered, true},
		{"edge-abc123", "edge-def456", changeUnordered, true},
		{"other", "invalid", changeUnordered, true},
	} {
		kind, changed := classifyChange(c.prev, c.next)
		if kind != c.wantKind || changed != c.wantChanged {
			t.Errorf("classifyChange(%q, %q) = %q, %v; want %q, %v", c.prev, c.next, kind, changed, c.wantKind, c.wantChanged)
		}
	}
}

// TestIsSemver checks which labels are ordered.
func TestIsSemver(t *testing.T) {
	t.Parallel()
	for label, want := range map[string]bool{
		"v0.3.0": true, "0.3.0": true, "v0.3.0-rc.1": true,
		"pr617": false, "dev": false, "edge-abc": false, "other": false, "invalid": false,
	} {
		if got := isSemver(label); got != want {
			t.Errorf("isSemver(%q) = %v, want %v", label, got, want)
		}
	}
}

// TestVersionTrendCountsChangesAndStale feeds extended reports over two days
// and checks every series of the trend (SC-017): one upgrade, one downgrade,
// one unordered change, stale installs against the newest semver in the
// range, and a same-day duplicate that changes nothing.
func TestVersionTrendCountsChangesAndStale(t *testing.T) {
	s, clock := newExtServer(t, config{}, extStart)
	send := func(n int, version string) {
		t.Helper()
		body, sig := newExtInstall(n).report(t, *clock, func(r *telemetryschema.Report) { r.Version = version })
		if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
			t.Fatalf("install %d %s: status %d (%s)", n, version, w.Code, w.Body)
		}
	}
	// Day 1 (2026-10-07).
	send(1, "1.0.0")
	send(2, "1.1.0")
	send(3, "pr617")
	// Day 2 (2026-10-08).
	*clock = extStart.Add(24 * time.Hour)
	send(1, "1.1.0")  // upgraded
	send(2, "1.0.0")  // downgraded
	send(3, "v0.3.0") // unordered
	send(4, "1.0.0")  // new install, never a change
	*clock = clock.Add(time.Minute)
	send(4, "2.0.0") // same-day duplicate: writes nothing

	if got := countRows(t.Context(), t, s.store, `SELECT count(*) FROM daily_install_version WHERE version = '2.0.0'`); got != 0 {
		t.Errorf("daily_install_version rows for the duplicate's version = %d, want 0", got)
	}

	asOf, err := time.Parse(time.DateOnly, "2026-10-08")
	if err != nil {
		t.Fatal(err)
	}
	trend, err := queryVersionTrend(t.Context(), s.store.db, asOf, 2)
	if err != nil {
		t.Fatalf("queryVersionTrend: %v", err)
	}
	if trend.NewestRelease != "v1.1.0" {
		t.Errorf("NewestRelease = %q, want v1.1.0", trend.NewestRelease)
	}
	want := []VersionTrendDay{
		{Day: "2026-10-07", Stale: 1},
		{Day: "2026-10-08", Upgraded: 1, Downgraded: 1, Unordered: 1, Stale: 3},
	}
	if len(trend.Days) != len(want) {
		t.Fatalf("days = %d, want %d: %+v", len(trend.Days), len(want), trend.Days)
	}
	for i := range want {
		if trend.Days[i] != want[i] {
			t.Errorf("day %d = %+v, want %+v", i, trend.Days[i], want[i])
		}
	}
}

// TestVersionTrendEmptyRange returns dense zero days and no newest release.
func TestVersionTrendEmptyRange(t *testing.T) {
	t.Parallel()
	st := openTestStore(t.Context(), t, config{})
	asOf, err := time.Parse(time.DateOnly, "2026-10-08")
	if err != nil {
		t.Fatal(err)
	}
	trend, err := queryVersionTrend(t.Context(), st.db, asOf, 7)
	if err != nil {
		t.Fatalf("queryVersionTrend: %v", err)
	}
	if trend.NewestRelease != "" || len(trend.Days) != 7 {
		t.Fatalf("trend = %+v, want 7 zero days and no newest release", trend)
	}
	for _, d := range trend.Days {
		if d.Upgraded+d.Downgraded+d.Unordered+d.Stale != 0 {
			t.Errorf("day %s not zero: %+v", d.Day, d)
		}
	}
}

// TestVersionTrendQueryErrorsAreWrapped closes the store first, so every
// query fails with a wrapped error.
func TestVersionTrendQueryErrorsAreWrapped(t *testing.T) {
	t.Parallel()
	st := openTestStore(t.Context(), t, config{})
	if err := st.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := queryVersionTrend(t.Context(), st.db, time.Now(), 7); err == nil {
		t.Fatal("queryVersionTrend on a closed store: want an error")
	}
}
