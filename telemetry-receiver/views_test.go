package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// viewsNow is the clock of the views tests: today (asOf) is 2026-10-06.
var viewsNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// viewsDay returns the day offset days before asOf (2026-10-06, today).
func viewsDay(offset int) string {
	return dayString(viewsNow.AddDate(0, 0, -offset))
}

// seedViews writes a synthetic multi-day dataset. Reports sit at 0, 3, 10,
// 40, 100 and 400 days before asOf, so each range holds a different subset.
func seedViews(t *testing.T, st *store) {
	t.Helper()
	for _, r := range []struct {
		offset             int
		version            string
		servers, templates int
	}{
		{0, "1.0.0", 2, 5},
		{0, "1.0.0", 0, 0},
		{0, "2.0.0", 12, 300},
		{3, "1.0.0", 1, 1},
		{3, "invalid", 3, 4},
		{10, "1.1.0", 7, 11},
		{40, "0.9.0", 100, 2000},
		{40, "other", 1001, 0},
		{100, "0.8.0", 0, 0},
		{400, "0.1.0", 5, 5},
	} {
		if err := st.recordBasic(context.Background(), viewsDay(r.offset), r.version, r.servers, r.templates); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func vr(label string, n int64) VersionRow { return VersionRow{Label: label, Reports: n} }

func bandReports(d FleetDistribution) []int64 {
	out := make([]int64, 0, len(d.Bands))
	for _, b := range d.Bands {
		out = append(out, b.Reports)
	}
	return out
}

func TestBuildViewsExactFiguresPerRange(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	seedViews(t, st)
	cases := []struct {
		rangeDays     int
		firstDay      string
		totalReports  int64
		versions      []VersionRow
		serverBands   []int64
		serverMedian  float64
		templateBands []int64
		templateMed   float64
	}{
		{7, "2026-09-30", 5,
			[]VersionRow{vr("1.0.0", 3), vr("2.0.0", 1), vr("Invalid", 1)},
			[]int64{1, 1, 1, 1, 0, 1, 0, 0, 0, 0}, 2,
			[]int64{1, 1, 0, 2, 0, 0, 0, 0, 0, 1}, 4},
		{30, "2026-09-07", 6,
			[]VersionRow{vr("1.0.0", 3), vr("1.1.0", 1), vr("2.0.0", 1), vr("Invalid", 1)},
			[]int64{1, 1, 1, 1, 1, 1, 0, 0, 0, 0}, 2.5,
			[]int64{1, 1, 0, 2, 0, 1, 0, 0, 0, 1}, 4.5},
		{90, "2026-07-09", 8,
			[]VersionRow{vr("1.0.0", 3), vr("0.9.0", 1), vr("1.1.0", 1), vr("2.0.0", 1), vr("Other", 1), vr("Invalid", 1)},
			[]int64{1, 1, 1, 1, 1, 1, 0, 1, 0, 1}, 5,
			[]int64{2, 1, 0, 2, 0, 1, 0, 0, 0, 2}, 4.5},
		{365, "2025-10-07", 9,
			[]VersionRow{vr("1.0.0", 3), vr("0.8.0", 1), vr("0.9.0", 1), vr("1.1.0", 1), vr("2.0.0", 1), vr("Other", 1), vr("Invalid", 1)},
			[]int64{2, 1, 1, 1, 1, 1, 0, 1, 0, 1}, 3,
			[]int64{3, 1, 0, 2, 0, 1, 0, 0, 0, 2}, 4},
	}
	wantLe := []string{"0", "1", "2", "5", "10", "25", "50", "100", "250", "250+"}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("range_%d", tc.rangeDays), func(t *testing.T) {
			v, err := BuildViews(context.Background(), st, tc.rangeDays, viewsNow)
			if err != nil {
				t.Fatalf("BuildViews: %v", err)
			}
			if v.Range != tc.rangeDays || v.AsOf != "2026-10-06" || v.Empty || v.Basic == nil || v.Extended != nil {
				t.Fatalf("header = range %d asOf %s empty %v basic nil %v extended %v",
					v.Range, v.AsOf, v.Empty, v.Basic == nil, v.Extended)
			}
			days := v.Basic.ReportsPerDay
			if len(days) != tc.rangeDays || days[0].Day != tc.firstDay || days[len(days)-1].Day != "2026-10-06" {
				t.Fatalf("reportsPerDay spans %d days from %s to %s", len(days), days[0].Day, days[len(days)-1].Day)
			}
			var total int64
			for _, d := range days {
				total += d.Reports
			}
			if total != tc.totalReports {
				t.Errorf("reports in range = %d, want %d", total, tc.totalReports)
			}
			if got := days[len(days)-1].Reports; got != 3 {
				t.Errorf("reports on asOf = %d, want 3", got)
			}
			if !reflect.DeepEqual(v.Basic.Versions, tc.versions) {
				t.Errorf("versions = %+v, want %+v", v.Basic.Versions, tc.versions)
			}
			for name, c := range map[string]struct {
				got    FleetDistribution
				bands  []int64
				median float64
			}{
				"servers":   {v.Basic.Fleet.Servers, tc.serverBands, tc.serverMedian},
				"templates": {v.Basic.Fleet.Templates, tc.templateBands, tc.templateMed},
			} {
				if got := bandReports(c.got); !reflect.DeepEqual(got, c.bands) {
					t.Errorf("%s bands = %v, want %v", name, got, c.bands)
				}
				if c.got.Median != c.median {
					t.Errorf("%s median = %v, want %v", name, c.got.Median, c.median)
				}
				for i, b := range c.got.Bands {
					if b.Le != wantLe[i] {
						t.Errorf("%s band %d le = %q, want %q", name, i, b.Le, wantLe[i])
					}
				}
			}
			if want := (LatestDayRow{ServersTotal: 14, TemplatesTotal: 305}); v.Basic.LatestDay != want {
				t.Errorf("latestDay = %+v, want %+v", v.Basic.LatestDay, want)
			}
		})
	}
}

func TestBuildViewsReportsPerDayIsDense(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	seedViews(t, st)
	v, err := BuildViews(context.Background(), st, 7, viewsNow)
	if err != nil {
		t.Fatalf("BuildViews: %v", err)
	}
	want := []DayReports{
		{"2026-09-30", 0}, {"2026-10-01", 0}, {"2026-10-02", 0}, {"2026-10-03", 2},
		{"2026-10-04", 0}, {"2026-10-05", 0}, {"2026-10-06", 3},
	}
	if !reflect.DeepEqual(v.Basic.ReportsPerDay, want) {
		t.Fatalf("reportsPerDay = %+v, want %+v", v.Basic.ReportsPerDay, want)
	}
}

// TestBuildViewsIncludesTodayInProgress proves that the range ends on today
// (UTC, in progress): a report accepted today is the last reportsPerDay
// entry, leaves the empty state, and its totals are the latest-day row.
func TestBuildViewsIncludesTodayInProgress(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	ctx := context.Background()
	today := dayString(viewsNow)
	for range 2 {
		if err := st.recordBasic(ctx, today, "1.0.0", 4, 9); err != nil {
			t.Fatal(err)
		}
	}
	v, err := BuildViews(ctx, st, 7, viewsNow)
	if err != nil {
		t.Fatalf("BuildViews: %v", err)
	}
	if v.Empty || v.Basic == nil {
		t.Fatalf("empty = %v with reports accepted today, want the populated view", v.Empty)
	}
	if v.AsOf != today {
		t.Fatalf("asOf = %s, want today %s", v.AsOf, today)
	}
	days := v.Basic.ReportsPerDay
	if last := days[len(days)-1]; last.Day != today || last.Reports != 2 {
		t.Fatalf("last reportsPerDay entry = %+v, want %s with 2 reports", last, today)
	}
	if want := (LatestDayRow{ServersTotal: 8, TemplatesTotal: 18}); v.Basic.LatestDay != want {
		t.Fatalf("latestDay = %+v, want %+v (today's totals)", v.Basic.LatestDay, want)
	}
}

func TestBuildViewsInvalidRangeFallsBackTo30(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	seedViews(t, st)
	for _, n := range []int{0, -7, 5, 31, 366, 1000} {
		v, err := BuildViews(context.Background(), st, n, viewsNow)
		if err != nil {
			t.Fatalf("range %d: %v", n, err)
		}
		if v.Range != 30 || len(v.Basic.ReportsPerDay) != 30 {
			t.Errorf("range %d: got range %d with %d days, want 30", n, v.Range, len(v.Basic.ReportsPerDay))
		}
	}
}

func TestBuildViewsEmptyState(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	check := func(label string) {
		t.Helper()
		for _, r := range validRanges {
			v, err := BuildViews(context.Background(), st, r, viewsNow)
			if err != nil {
				t.Fatalf("%s range %d: %v", label, r, err)
			}
			if !v.Empty || v.Basic != nil || v.Extended != nil || v.AsOf != "2026-10-06" || v.Range != r {
				t.Fatalf("%s range %d: %+v, want the empty state", label, r, v)
			}
		}
	}
	check("empty store")
	// Reports from tomorrow (after asOf) and from beyond the widest range do
	// not leave the empty state; today's do (TestBuildViewsIncludesTodayInProgress).
	if err := st.recordBasic(context.Background(), "2026-10-07", "1.0.0", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.recordBasic(context.Background(), "2024-01-01", "1.0.0", 1, 1); err != nil {
		t.Fatal(err)
	}
	check("only out-of-range data")
	body, err := json.Marshal(Views{Range: 30, AsOf: "2026-10-06", Empty: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"basic":null`) || !strings.Contains(string(body), `"extended":null`) {
		t.Fatalf("empty view JSON = %s, want basic and extended null", body)
	}
}

func TestBuildViewsTopTenVersionsThenOtherAndInvalid(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	ctx := context.Background()
	day := viewsDay(1)
	// Twelve named versions with 12, 11, ... 1 reports, plus other and
	// invalid rows. The two smallest named versions fold into Other.
	for i := range 12 {
		for range 12 - i {
			if err := st.recordBasic(ctx, day, fmt.Sprintf("3.%d.0", i), 1, 1); err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 4 {
		if err := st.recordBasic(ctx, day, "other", 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.recordBasic(ctx, day, "invalid", 1, 1); err != nil {
		t.Fatal(err)
	}
	v, err := BuildViews(ctx, st, 7, viewsNow)
	if err != nil {
		t.Fatal(err)
	}
	got := v.Basic.Versions
	if len(got) != 12 {
		t.Fatalf("versions = %+v, want 10 named + Other + Invalid", got)
	}
	for i := range 10 {
		if want := vr(fmt.Sprintf("3.%d.0", i), int64(12-i)); got[i] != want {
			t.Errorf("versions[%d] = %+v, want %+v", i, got[i], want)
		}
	}
	// Other = 4 (stored "other") + 2 + 1 (3.10.0 and 3.11.0).
	if got[10] != vr("Other", 7) || got[11] != vr("Invalid", 1) {
		t.Errorf("tail = %+v %+v, want Other 7 and Invalid 1", got[10], got[11])
	}
}

func TestBuildViewsOmitsOtherAndInvalidWithoutReports(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	if err := st.recordBasic(context.Background(), viewsDay(0), "1.0.0", 1, 1); err != nil {
		t.Fatal(err)
	}
	v, err := BuildViews(context.Background(), st, 7, viewsNow)
	if err != nil {
		t.Fatal(err)
	}
	if want := []VersionRow{vr("1.0.0", 1)}; !reflect.DeepEqual(v.Basic.Versions, want) {
		t.Fatalf("versions = %+v, want %+v", v.Basic.Versions, want)
	}
}

func TestBuildViewsLatestDayZeroWhenAsOfHasNoRow(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	if err := st.recordBasic(context.Background(), viewsDay(3), "1.0.0", 4, 9); err != nil {
		t.Fatal(err)
	}
	v, err := BuildViews(context.Background(), st, 7, viewsNow)
	if err != nil {
		t.Fatal(err)
	}
	if v.Empty || v.Basic.LatestDay != (LatestDayRow{}) {
		t.Fatalf("latestDay = %+v (empty %v), want zeros with data in range", v.Basic.LatestDay, v.Empty)
	}
}

func TestBuildViewsReadErrors(t *testing.T) {
	for _, drop := range []string{"daily_basic", "daily_version", "daily_fleet"} {
		t.Run(drop, func(t *testing.T) {
			st := openTestStore(t.Context(), t, config{})
			if err := st.recordBasic(context.Background(), viewsDay(0), "1.0.0", 1, 1); err != nil {
				t.Fatal(err)
			}
			if _, err := st.db.ExecContext(context.Background(), "DROP TABLE "+drop); err != nil {
				t.Fatal(err)
			}
			if _, err := BuildViews(context.Background(), st, 7, viewsNow); err == nil {
				t.Fatalf("BuildViews with %s dropped: want an error", drop)
			}
		})
	}
}

func TestFleetMedian(t *testing.T) {
	cases := []struct {
		name string
		hist []fleetPoint
		want float64
	}{
		{"empty", nil, 0},
		{"single", []fleetPoint{{4, 1}}, 4},
		{"odd", []fleetPoint{{1, 1}, {2, 1}, {9, 1}}, 2},
		{"even mean", []fleetPoint{{1, 1}, {2, 1}, {3, 1}, {10, 1}}, 2.5},
		{"weighted", []fleetPoint{{0, 5}, {10, 1}}, 0},
		{"cap value", []fleetPoint{{1001, 3}}, 1001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fleetMedian(tc.hist); got != tc.want {
				t.Fatalf("median = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFleetBandsBoundaries(t *testing.T) {
	hist := []fleetPoint{{0, 1}, {1, 2}, {2, 3}, {3, 4}, {5, 5}, {6, 6}, {250, 7}, {251, 8}, {1001, 9}}
	want := []int64{1, 2, 3, 9, 6, 0, 0, 0, 7, 17}
	if got := bandReports(FleetDistribution{Bands: fleetBands(hist)}); !reflect.DeepEqual(got, want) {
		t.Fatalf("bands = %v, want %v", got, want)
	}
}

func TestNormalizeRange(t *testing.T) {
	for in, want := range map[int]int{7: 7, 30: 30, 90: 90, 365: 365, 0: 30, 8: 30, -1: 30} {
		if got := normalizeRange(in); got != want {
			t.Errorf("normalizeRange(%d) = %d, want %d", in, got, want)
		}
	}
}
