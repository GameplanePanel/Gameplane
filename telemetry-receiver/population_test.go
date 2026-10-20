package main

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// popStart is day 0 of the synthetic population.
var popStart = time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

const (
	popDays   = 60 // days 0..59 report; day 60 only finalises day 59
	popExpiry = 90
)

// popDay returns the UTC midnight of day d.
func popDay(d int) time.Time { return popStart.AddDate(0, 0, d) }

// popEntity is one install ID in the synthetic population: the days it
// reports are first..last, minus an optional gap.
type popEntity struct {
	n          int
	first      int
	last       int
	gapFrom    int // inclusive; gapTo < gapFrom means no gap
	gapTo      int
	upgradeDay int // from this day the entity reports version 1.1.0; -1 means never
	twice      bool
}

func (e popEntity) active(d int) bool {
	return d >= e.first && d <= e.last && (d < e.gapFrom || d > e.gapTo)
}

func (e popEntity) version(d int) string {
	if e.upgradeDay >= 0 && d >= e.upgradeDay {
		return "1.1.0"
	}
	return "1.0.0"
}

// lastActive returns the latest day <= through on which e reported, or -1.
func (e popEntity) lastActive(through int) int {
	for d := min(through, e.last); d >= e.first; d-- {
		if e.active(d) {
			return d
		}
	}
	return -1
}

// popPopulation builds the synthetic installs:
//   - 0..23: join on day n/2; every 4th stops 20 days after joining (a lapse);
//     every 5th skips 5 days (a gap far shorter than the lapse window);
//   - 24..27: an old ID used on days 2..24 (an install that then reset);
//   - 28..31: the replacement ID, new from day 25;
//   - every 3rd entity upgrades from 1.0.0 to 1.1.0 on day 30;
//   - every 6th entity sends a second report on each day (a duplicate).
func popPopulation() []popEntity {
	var out []popEntity
	for n := range 32 {
		e := popEntity{n: n, gapFrom: 1, gapTo: 0, upgradeDay: -1, twice: n%6 == 0}
		switch {
		case n < 24:
			e.first, e.last = n/2, popDays-1
			if n%4 == 0 {
				e.last = e.first + 20
			}
			if n%5 == 0 {
				e.gapFrom, e.gapTo = e.first+10, e.first+14
			}
		case n < 28:
			e.first, e.last = 2, 24
		default:
			e.first, e.last = 25, popDays-1
		}
		if n%3 == 0 {
			e.upgradeDay = 30
		}
		out = append(out, e)
	}
	return out
}

func TestPopulationProducesExactNewLapsedAndActiveCounts(t *testing.T) {
	ctx := context.Background()
	s, clock := newExtServer(t, config{activityExpiryDays: popExpiry}, popDay(0))
	pop := popPopulation()
	var wantFresh, wantDuplicates int
	for d := 0; d <= popDays; d++ {
		// The hourly job runs shortly after midnight and finalises day d-1.
		*clock = popDay(d).Add(30 * time.Minute)
		if err := s.store.lifecycleOnce(ctx, *clock, 730); err != nil {
			t.Fatalf("lifecycle day %d: %v", d, err)
		}
		if d == popDays {
			break
		}
		*clock = popDay(d).Add(12 * time.Hour)
		for _, e := range pop {
			if !e.active(d) {
				continue
			}
			in := newExtInstall(e.n)
			body, sig := in.report(t, *clock, func(r *telemetryschema.Report) { r.Version = e.version(d) })
			if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
				t.Fatalf("day %d entity %d: status %d (%s)", d, e.n, w.Code, w.Body)
			}
			wantFresh++
			if e.twice {
				// A second report 30 minutes later is a same-day duplicate.
				later := clock.Add(30 * time.Minute)
				body, sig = in.report(t, later, func(r *telemetryschema.Report) { r.Version = e.version(d) })
				if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
					t.Fatalf("day %d entity %d repeat: status %d", d, e.n, w.Code)
				}
				wantDuplicates++
			}
		}
	}
	st := s.store

	// Per-day new, active and lapsed counts against an independent model.
	var wantLapsedTotal int
	for d := 0; d < popDays; d++ {
		var wantNew, wantActive, wantLapsed int
		for _, e := range pop {
			if e.active(d) {
				wantActive++
				if d == e.first {
					wantNew++
				}
			}
			// Lapsed on day d: its last report so far was exactly 30 days ago.
			if d >= lapsedWindowDays && e.lastActive(d) == d-lapsedWindowDays {
				wantLapsed++
			}
		}
		wantLapsedTotal += wantLapsed
		var gotNew, gotActive, gotLapsed int
		err := st.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(new_installs), 0), COALESCE(SUM(active_installs), 0), COALESCE(SUM(lapsed_installs), 0)
			 FROM daily_ext WHERE day = ?`, dayString(popDay(d))).Scan(&gotNew, &gotActive, &gotLapsed)
		if err != nil {
			t.Fatalf("day %d: %v", d, err)
		}
		if gotNew != wantNew || gotActive != wantActive || gotLapsed != wantLapsed {
			t.Errorf("day %d: new/active/lapsed = %d/%d/%d, want %d/%d/%d",
				d, gotNew, gotActive, gotLapsed, wantNew, wantActive, wantLapsed)
		}
	}
	if wantLapsedTotal == 0 {
		t.Fatal("the population has no lapsed installs, so the test proves nothing")
	}

	// Dedupe: one counted report per install per day, repeats only counted
	// in duplicates.
	if got := countRows(t.Context(), t, st, `SELECT COALESCE(SUM(duplicates), 0) FROM daily_basic`); got != wantDuplicates {
		t.Errorf("duplicates = %d, want %d", got, wantDuplicates)
	}
	if got := countRows(t.Context(), t, st, `SELECT COALESCE(SUM(reports), 0) FROM daily_basic`); got != wantFresh {
		t.Errorf("reports = %d, want %d", got, wantFresh)
	}
	if got := countRows(t.Context(), t, st, `SELECT COALESCE(SUM(ext_reports), 0) FROM daily_ext`); got != wantFresh {
		t.Errorf("ext_reports = %d, want %d", got, wantFresh)
	}
	if got := mustMeta(t, st, "reports_total"); got != fmt.Sprint(wantFresh) {
		t.Errorf("reports_total = %s, want %d", got, wantFresh)
	}
	if got := countRows(t.Context(), t, st, `SELECT count(*) FROM activity`); got != len(pop) {
		t.Errorf("activity rows = %d, want %d (nothing expires inside %d days)", got, len(pop), popExpiry)
	}

	// The views at the end of day 59: active counts and the 30-day unique
	// count (SC-008 allows 1%; the distinct count is exact).
	*clock = popDay(popDays - 1).Add(12 * time.Hour) // today (asOf) is day 59
	v, err := BuildViews(ctx, st, 30, *clock)
	if err != nil {
		t.Fatalf("BuildViews: %v", err)
	}
	if v.Extended == nil {
		t.Fatal("Extended is nil, want the extended block")
	}
	asOf := popDays - 1
	var want1, want7, want30 int
	for _, e := range pop {
		switch last := e.lastActive(asOf); {
		case last < 0:
		default:
			if last >= asOf {
				want1++
			}
			if last >= asOf-6 {
				want7++
			}
			if last >= asOf-29 {
				want30++
			}
		}
	}
	got := v.Extended.Installs
	if got.Active1d != int64(want1) || got.Active7d != int64(want7) || got.Active30d != int64(want30) {
		t.Errorf("active 1d/7d/30d = %d/%d/%d, want %d/%d/%d",
			got.Active1d, got.Active7d, got.Active30d, want1, want7, want30)
	}
	if diff := got.Active30d - int64(want30); diff*100 > int64(want30) || -diff*100 > int64(want30) {
		t.Errorf("30-day unique count %d is not within 1%% of %d", got.Active30d, want30)
	}

	// New and lapsed series over days 30..59 against the table.
	if len(v.Extended.NewPerDay) != 30 || len(v.Extended.LapsedPerDay) != 30 {
		t.Fatalf("series lengths = %d/%d, want 30", len(v.Extended.NewPerDay), len(v.Extended.LapsedPerDay))
	}
	for i := range 30 {
		d := 30 + i
		var wantNew, wantLapsed int64
		for _, e := range pop {
			if e.active(d) && d == e.first {
				wantNew++
			}
			if d >= lapsedWindowDays && e.lastActive(d) == d-lapsedWindowDays {
				wantLapsed++
			}
		}
		if n := v.Extended.NewPerDay[i]; n.Day != dayString(popDay(d)) || n.Count != wantNew {
			t.Errorf("newPerDay[%d] = %+v, want %s %d", i, n, dayString(popDay(d)), wantNew)
		}
		if l := v.Extended.LapsedPerDay[i]; l.Day != dayString(popDay(d)) || l.Count != wantLapsed {
			t.Errorf("lapsedPerDay[%d] = %+v, want %s %d", i, l, dayString(popDay(d)), wantLapsed)
		}
	}

	// Version adoption by install covers the 30-day window: each install
	// that reported in it, by its last version.
	if v.Extended.VersionsByInstall.WindowDays != 30 {
		t.Errorf("windowDays = %d, want 30", v.Extended.VersionsByInstall.WindowDays)
	}
	wantVersions := map[string]int64{}
	for _, e := range pop {
		if last := e.lastActive(asOf); last >= asOf-29 {
			wantVersions[e.version(last)]++
		}
	}
	gotVersions := map[string]int64{}
	for _, it := range v.Extended.VersionsByInstall.Items {
		gotVersions[it.Label] = it.Installs
	}
	if fmt.Sprint(gotVersions) != fmt.Sprint(wantVersions) {
		t.Errorf("versionsByInstall = %v, want %v", gotVersions, wantVersions)
	}
}

func TestLifecycleLapsedIsIdempotentAndWritesNothingForQuietDays(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	ctx := context.Background()
	// One install last seen on 2026-09-06: lapsed on 2026-10-06 (30 days).
	if _, err := st.db.ExecContext(ctx,
		`INSERT INTO activity (id_hmac, key_fp, first_seen, last_seen, last_sent_at, last_version)
		 VALUES ('h', 'k', '2026-08-01', '2026-09-06', '2026-09-06T00:00:00Z', '1.0.0')`); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := st.lifecycleOnce(ctx, lifecycleNow, 365); err != nil {
			t.Fatalf("lifecycleOnce: %v", err)
		}
	}
	if got := countRows(t.Context(), t, st, `SELECT lapsed_installs FROM daily_ext WHERE day = '2026-10-06'`); got != 1 {
		t.Errorf("lapsed_installs[2026-10-06] = %d, want 1", got)
	}
	if got := countRows(t.Context(), t, st, `SELECT count(*) FROM daily_ext`); got != 1 {
		t.Errorf("daily_ext rows = %d, want 1 (quiet days write nothing)", got)
	}
}

func TestLifecycleExpiresActivityRecordsAndTheirClaims(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expiryDays int
		wantRows   int
	}{
		{"expiry 31 removes records older than 31 days", 31, 2},
		{"expiry 0 keeps every record", 0, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expiresActivityCase(t, tc)
		})
	}
}

func expiresActivityCase(t *testing.T, tc struct {
	name       string
	expiryDays int
	wantRows   int
}) {
	ctx := t.Context()
	st := openTestStore(t.Context(), t, config{activityExpiryDays: tc.expiryDays})
	// lifecycleNow is 2026-10-07: with expiry 31 the cutoff is
	// 2026-09-06, which is kept; the day before it is deleted.
	for id, lastSeen := range map[string]string{"a": "2026-09-05", "b": "2026-09-06", "c": "2026-10-06"} {
		if _, err := st.db.ExecContext(ctx,
			`INSERT INTO activity (id_hmac, key_fp, first_seen, last_seen, last_sent_at, last_version)
			 VALUES (?, 'k', '2026-01-01', ?, '2026-01-01T00:00:00Z', '1.0.0')`, id, lastSeen); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.lifecycleOnce(ctx, lifecycleNow, 365); err != nil {
		t.Fatalf("lifecycleOnce: %v", err)
	}
	if got := countRows(t.Context(), t, st, `SELECT count(*) FROM activity`); got != tc.wantRows {
		t.Errorf("activity rows = %d, want %d", got, tc.wantRows)
	}
	if tc.expiryDays > 0 {
		if got := countRows(t.Context(), t, st, `SELECT count(*) FROM activity WHERE id_hmac = 'a'`); got != 0 {
			t.Error("the record last seen before the cutoff was kept")
		}
	}
}

func TestBuildViewsExtendedSharesGamesAndFeatures(t *testing.T) {
	ctx := context.Background()
	s, clock := newExtServer(t, config{activityExpiryDays: 31}, extStart)
	// Two installs on the same day: k3s with minecraft and sso, eks with
	// terraria plus a custom module on a 2-3 node cluster.
	a, aSig := newExtInstall(1).report(t, extStart, func(r *telemetryschema.Report) {
		r.Ext.Features.SSO = true
		r.Ext.Games = telemetryschema.Games{Official: map[string]int{"minecraft-java": 4}, Custom: 0}
	})
	b, bSig := newExtInstall(2).report(t, extStart, func(r *telemetryschema.Report) {
		r.Ext.Env.Distro, r.Ext.Env.Nodes, r.Ext.Env.K8s = []string{"eks"}, "2-3", []string{"1.30"}
		r.Ext.Features.Backups, r.Ext.Features.Tunnels = false, []string{}
		r.Ext.Features.DB, r.Ext.Features.Clusters = "postgres", "2-3"
		r.Ext.Games = telemetryschema.Games{Official: map[string]int{"terraria": 1}, Custom: 2}
	})
	for _, c := range []struct{ body, sig string }{{a, aSig}, {b, bSig}} {
		if w := extSend(t, s, c.body, c.sig); w.Code != http.StatusNoContent {
			t.Fatalf("status = %d (%s)", w.Code, w.Body)
		}
	}
	*clock = extStart.Add(24 * time.Hour) // asOf is 2026-10-08 (today); the reports are on 2026-10-07
	v, err := BuildViews(ctx, s.store, 7, *clock)
	if err != nil {
		t.Fatal(err)
	}
	e := v.Extended
	if e == nil {
		t.Fatal("Extended is nil")
	}
	if e.Coverage != 1 {
		t.Errorf("coverage = %v, want 1 (every report is extended)", e.Coverage)
	}
	if e.VersionsByInstall.WindowDays != 7 {
		t.Errorf("windowDays = %d, want min(7, 31) = 7", e.VersionsByInstall.WindowDays)
	}
	wantDistro := []ShareRow{{"eks", 0.5}, {"k3s", 0.5}}
	if fmt.Sprint(e.Env.Distro) != fmt.Sprint(wantDistro) {
		t.Errorf("distro = %v, want %v (share desc, then label)", e.Env.Distro, wantDistro)
	}
	if fmt.Sprint(e.Env.Nodes) != fmt.Sprint([]ShareRow{{"1", 0.5}, {"2-3", 0.5}}) {
		t.Errorf("nodes = %v, want band order 1, 2-3", e.Env.Nodes)
	}
	if fmt.Sprint(e.Clusters) != fmt.Sprint([]ShareRow{{"1", 0.5}, {"2-3", 0.5}}) {
		t.Errorf("clusters = %v", e.Clusters)
	}
	if len(e.Features) != 5 || e.Features[0].Feature != "wakeOnConnect" || e.Features[3].Feature != "sso" ||
		e.Features[3].Share != 0.5 || e.Features[2].Feature != "backups" || e.Features[2].Share != 0.5 ||
		e.Features[0].Share != 0 {
		t.Errorf("features = %+v, want all five in order with sso and backups at 0.5", e.Features)
	}
	games := map[string]GameShare{}
	for _, g := range e.Games {
		games[g.Module] = g
	}
	if games["minecraft-java"].InstallsShare != 0.5 || games["minecraft-java"].ServersPerDay != 4 ||
		games["custom"].InstallsShare != 0.5 || games["custom"].ServersPerDay != 2 || len(games) != 3 {
		t.Errorf("games = %+v", e.Games)
	}
	if len(e.Tunnels) != 1 || e.Tunnels[0].Label != "playit" || e.Tunnels[0].Share != 0.5 {
		t.Errorf("tunnels = %+v", e.Tunnels)
	}
	// A range without extended reports has no extended block at all.
	empty, err := BuildViews(ctx, openTestStoreWithBasic(ctx, t), 30, *clock)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Extended != nil {
		t.Errorf("Extended = %+v, want nil when no report in range is extended", empty.Extended)
	}
}

// openTestStoreWithBasic returns a store holding one basic report on
// 2026-10-07 and nothing extended.
func openTestStoreWithBasic(ctx context.Context, t *testing.T) *store {
	t.Helper()
	st := openTestStore(ctx, t, config{})
	if err := st.recordBasic(ctx, "2026-10-07", "1.0.0", 1, 1); err != nil {
		t.Fatal(err)
	}
	return st
}
