package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

func TestNiceScale(t *testing.T) {
	for _, tc := range []struct{ max, step, top int64 }{
		{-5, 1, 3}, {0, 1, 3}, {1, 1, 3}, {3, 1, 3}, {4, 2, 6}, {7, 5, 15}, {15, 5, 15}, {16, 10, 30},
		{1284, 500, 1500}, {1500, 500, 1500}, {1501, 1000, 3000},
	} {
		if step, top := niceScale(tc.max); step != tc.step || top != tc.top {
			t.Errorf("niceScale(%d) = %d, %d, want %d, %d", tc.max, step, top, tc.step, tc.top)
		}
	}
}

func TestGroupIntAndPercent(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 7: "7", 999: "999", 1000: "1,000", 1284: "1,284", 412055: "412,055", 1234567: "1,234,567", -4321: "-4,321"} {
		if got := groupInt(n); got != want {
			t.Errorf("groupInt(%d) = %q, want %q", n, got, want)
		}
	}
	for share, want := range map[float64]string{0: "0%", 0.384: "38%", 0.5: "50%", 1: "100%", 0.004: "0%"} {
		if got := percentString(share); got != want {
			t.Errorf("percentString(%v) = %q, want %q", share, got, want)
		}
	}
	if got := displayLabel("2-3"); got != "2–3" {
		t.Errorf("displayLabel(2-3) = %q", got)
	}
	for _, same := range []string{"", "minecraft-java", "1.31", "11+", "a-b-c", "0.3.0-rc.1-x"} {
		if got := displayLabel(same); got != same {
			t.Errorf("displayLabel(%q) = %q, want it unchanged", same, got)
		}
	}
}

func TestFleetLabels(t *testing.T) {
	d := FleetDistribution{Bands: fleetBands(nil)}
	got := fleetLabels(d.Bands)
	want := []string{"0", "1", "2", "3–5", "6–10", "11–25", "26–50", "51–100", "101–250", "250+"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("fleetLabels = %v, want %v", got, want)
	}
}

func TestNewColumnChartLayout(t *testing.T) {
	c := newColumnChart("Reports per day", seriesPink, "Day", "Reports",
		[]point{{"2026-10-04", 0}, {"2026-10-05", 750}, {"2026-10-06", 1500}}, 2)
	if len(c.Cols) != 3 || len(c.Ticks) != colTickCount+1 {
		t.Fatalf("cols %d ticks %d", len(c.Cols), len(c.Ticks))
	}
	if c.Cols[0].H != 0 || c.Cols[2].H <= c.Cols[1].H || c.Cols[1].H <= 0 {
		t.Errorf("column heights = %v %v %v, want 0 < h1 < h2", c.Cols[0].H, c.Cols[1].H, c.Cols[2].H)
	}
	if c.Cols[2].Y != round1(colPlotTop) {
		t.Errorf("the tallest column starts at y=%v, want the top of the plot %v (scale 0..1500)", c.Cols[2].Y, colPlotTop)
	}
	if c.Ticks[0].Label != "0" || c.Ticks[3].Label != "1,500" {
		t.Errorf("tick labels = %v", c.Ticks)
	}
	if len(c.XLabels) != 2 || c.XLabels[0].Label != "2026-10-04" || c.XLabels[1].Label != "2026-10-06" {
		t.Errorf("x labels = %v, want every 2nd point", c.XLabels)
	}
	if c.LastText != "1,500" || c.Cols[2].Title != "2026-10-06: 1,500" {
		t.Errorf("last label %q, title %q", c.LastText, c.Cols[2].Title)
	}
	if len(c.Rows) != 3 || c.Rows[1].Value != "750" {
		t.Errorf("rows = %v", c.Rows)
	}
	if empty := newColumnChart("x", seriesPink, "a", "b", nil, 0); len(empty.Cols) != 0 || empty.LastText != "" {
		t.Errorf("an empty chart has columns or a label: %+v", empty)
	}
}

func TestNewBarChartLayoutAndClamping(t *testing.T) {
	c := newBarChart("Versions", "Version", "Reports", true,
		[]barItem{{"0.3.0", "812", 1}, {"dev", "4", 0.25}, {"bad", "x", 7}, {"neg", "y", -1}})
	if len(c.Bars) != 4 || c.H != 4*barRowH || !c.Track {
		t.Fatalf("bars %d height %v track %v", len(c.Bars), c.H, c.Track)
	}
	if c.Bars[0].Bar != barMaxW || c.Bars[1].Bar != round1(0.25*barMaxW) || c.Bars[2].Bar != barMaxW || c.Bars[3].Bar != 0 {
		t.Errorf("bar lengths = %v %v %v %v", c.Bars[0].Bar, c.Bars[1].Bar, c.Bars[2].Bar, c.Bars[3].Bar)
	}
	if c.Bars[1].Title != "dev: 4" || c.Rows[1].Label != "dev" {
		t.Errorf("title %q rows %v", c.Bars[1].Title, c.Rows)
	}
	items := countBars([]string{"a", "b", "z"}, []int64{10, 5, 0})
	if items[0].Frac != 1 || items[1].Frac != 0.5 || items[2].Frac != 0 || items[0].Value != "10" {
		t.Errorf("countBars = %+v", items)
	}
	if zero := countBars([]string{"a"}, []int64{0}); zero[0].Frac != 0 {
		t.Errorf("countBars of zeros = %+v", zero)
	}
}

func TestDayLabelEvery(t *testing.T) {
	for n, want := range map[int]int{0: 1, 4: 1, 7: 1, 30: 6, 365: 73} {
		if got := dayLabelEvery(n); got != want {
			t.Errorf("dayLabelEvery(%d) = %d, want %d", n, got, want)
		}
	}
}

// seedExtendedDash stores one basic report and two extended reports on the
// day before today (2026-10-05) of a dashboard test server, inside every range.
func seedExtendedDash(t *testing.T, s *server) {
	t.Helper()
	s.now = func() time.Time { return dashNow.Add(-24 * time.Hour) }
	defer func() { s.now = func() time.Time { return dashNow } }()
	if w := extSend(t, s, okReport, ""); w.Code != http.StatusNoContent {
		t.Fatalf("basic report: %d", w.Code)
	}
	for n := 1; n <= 2; n++ {
		body, sig := newExtInstall(n).report(t, s.now(), func(r *telemetryschema.Report) {
			if n == 2 {
				r.Ext.Env.Distro, r.Ext.Features.DB, r.Ext.Features.Clusters = []string{"eks"}, "postgres", "2-3"
			}
		})
		if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
			t.Fatalf("extended report %d: %d (%s)", n, w.Code, w.Body)
		}
	}
}

func TestOverviewRendersChartsTablesAndTheExtendedSection(t *testing.T) {
	s, h := newDash(t, config{})
	seedExtendedDash(t, s)
	cookie := signIn(t, h)

	page := doDash(t, h, dashReq{path: "/?range=7", cookie: cookie})
	body := page.Body.String()
	if page.Code != http.StatusOK {
		t.Fatalf("status = %d", page.Code)
	}
	for _, want := range []string{
		"<svg", "<title>", "Reports per day", "View as table", "Extended reports", "Kubernetes version",
		"Feature adoption", "Games", "minecraft-java", "Coverage:", "k3s", "eks", `href="/?range=30"`,
		`aria-current="page"`, "Sign out",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview lacks %q", want)
		}
	}
	// The CSP is style-src 'self': no inline style and no script, anywhere.
	for _, bad := range []string{"<style", " style=", "<script", "onclick=", "https://", "http://"} {
		if strings.Contains(body, bad) {
			t.Errorf("overview contains %q", bad)
		}
	}

	table := doDash(t, h, dashReq{path: "/?range=7&view=table", cookie: cookie}).Body.String()
	if !strings.Contains(table, `<table class="data">`) || strings.Contains(table, "<svg class=\"chart") ||
		!strings.Contains(table, "View as chart") {
		t.Errorf("table view: want tables, no chart svg and a way back")
	}
}

func TestOverviewExplainsAMissingExtendedBlockAndTheEmptyState(t *testing.T) {
	s, h := newDash(t, config{})
	cookie := signIn(t, h)
	if body := doDash(t, h, dashReq{path: "/", cookie: cookie}).Body.String(); !strings.Contains(body, "No reports yet") ||
		strings.Contains(body, "<svg class=\"chart") || strings.Contains(body, "Basic reports") {
		t.Errorf("empty overview: want the empty state and no chart")
	}
	seedDash(t, s) // basic reports only
	body := doDash(t, h, dashReq{path: "/", cookie: cookie}).Body.String()
	if !strings.Contains(body, "No reports in this range included extended data") {
		t.Error("basic-only overview lacks the no-extended-data message")
	}
	if strings.Contains(body, "Kubernetes version") {
		t.Error("basic-only overview renders an extended chart")
	}
}

func TestLoginPageAndStaticLogo(t *testing.T) {
	_, h := newDash(t, config{})
	login := doDash(t, h, dashReq{path: "/login"}).Body.String()
	for _, want := range []string{"Dashboard token", `name="token"`, "/static/gameplane-icon.png"} {
		if !strings.Contains(login, want) {
			t.Errorf("login page lacks %q", want)
		}
	}
	if strings.Contains(login, "<style") || strings.Contains(login, " style=") {
		t.Error("login page has inline styles")
	}
	logo := doDash(t, h, dashReq{path: "/static/gameplane-icon.png"})
	if logo.Code != http.StatusOK || logo.Header().Get("Content-Type") != "image/png" || logo.Body.Len() < 1000 {
		t.Errorf("logo: %d %q %d bytes", logo.Code, logo.Header().Get("Content-Type"), logo.Body.Len())
	}
}

func TestBuildExtendedChartsNilWithoutExtendedViews(t *testing.T) {
	if got := buildExtendedCharts(Views{}, false); got != nil {
		t.Fatalf("buildExtendedCharts(no extended) = %+v, want nil", got)
	}
	e := &ExtendedViews{
		Coverage: 1.5, Features: []FeatureShare{{"backups", 0.5}},
		Clusters: []ShareRow{{"1", 0.5}, {"4-10", 0.25}, {"other", 0.25}}, DB: []ShareRow{{"postgres", 0.5}},
		Games: []GameShare{{"custom", 0.25, 1}}, Tunnels: []ShareRow{{"frp", 0.1}},
	}
	c := buildExtendedCharts(Views{Extended: e}, true)
	if c == nil || c.CoverageW != 100 {
		t.Fatalf("coverage width = %+v, want clamped to 100", c)
	}
	rows := map[string]string{}
	for _, r := range c.Features.Rows {
		rows[r.Label] = r.Value
	}
	if rows["PostgreSQL"] != "50%" || rows["Multi-cluster"] != "25%" || rows["Tunnel: frp"] != "10%" || rows["Backups"] != "50%" {
		t.Errorf("feature rows = %v", rows)
	}
	if !c.Features.Table || c.Games.Rows[0].Label != "Custom" {
		t.Errorf("table flag %v, games %v", c.Features.Table, c.Games.Rows)
	}
}
