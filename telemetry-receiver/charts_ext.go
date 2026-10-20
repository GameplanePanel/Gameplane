package main

import "strings"

// extCharts holds the charts of the extended section (T076).
type extCharts struct {
	CoverageW       float64 // fill width of the coverage track, 0..100
	NewTotal        int64
	LapsedTotal     int64
	New             columnChart
	Lapsed          columnChart
	K8s             barChart
	Distro          barChart
	Arch            barChart
	Nodes           barChart
	Deployment      barChart
	Games           barChart
	Features        barChart
	VersionsInstall barChart
	VersionsWide    barChart // VersionsInstall laid out for wide screens
	Trend           trendChart
	WindowDays      int
}

// featureLabels are the display names of the boolean features.
var featureLabels = map[string]string{
	"wakeOnConnect":   "Wake on connect",
	"capture":         "Packet capture",
	"backups":         "Backups",
	"sso":             "SSO",
	"auditForwarding": "Audit forwarding",
}

// buildExtendedCharts lays out the extended section from v.Extended. It
// returns nil when the range has no extended reports.
func buildExtendedCharts(v Views, table bool) *extCharts {
	e := v.Extended
	if e == nil {
		return nil
	}
	c := &extCharts{CoverageW: round1(min(max(e.Coverage, 0), 1) * 100), WindowDays: e.VersionsByInstall.WindowDays}
	newPts := make([]point, len(e.NewPerDay))
	for i, d := range e.NewPerDay {
		newPts[i] = point{Label: d.Day, Value: d.Count}
		c.NewTotal += d.Count
	}
	lapsedPts := make([]point, len(e.LapsedPerDay))
	for i, d := range e.LapsedPerDay {
		lapsedPts[i] = point{Label: d.Day, Value: d.Count}
		c.LapsedTotal += d.Count
	}
	every := dayLabelEvery(len(newPts))
	c.New = newColumnChart("New installs per day", seriesPink, "Day", "Installs", newPts, every)
	c.Lapsed = newColumnChart("Lapsed installs per day", seriesBlue, "Day", "Installs", lapsedPts, every)

	c.K8s = shareChart("Kubernetes version", "Version", e.Env.K8s, false)
	c.Distro = shareChart("Distribution", "Distribution", e.Env.Distro, false)
	c.Arch = shareChart("Architecture", "Architecture", e.Env.Arch, false)
	c.Nodes = shareChart("Cluster nodes", "Node band", e.Env.Nodes, false)
	c.Deployment = shareChart("Deployment", "Deployment", e.Env.Deployment, false)

	games := make([]ShareRow, len(e.Games))
	for i, g := range e.Games {
		label := g.Module
		if label == gameCustom {
			label = "Custom"
		}
		games[i] = ShareRow{Label: label, Share: g.InstallsShare}
	}
	c.Games = shareChart("Games", "Module", games, false)

	var features []ShareRow
	for _, f := range e.Features {
		features = append(features, ShareRow{Label: featureLabels[f.Feature], Share: f.Share})
	}
	for _, r := range e.DB {
		if r.Label == "postgres" {
			features = append(features, ShareRow{Label: "PostgreSQL", Share: r.Share})
		}
	}
	var multi float64
	for _, r := range e.Clusters {
		if r.Label != "1" && r.Label != "other" {
			multi += r.Share
		}
	}
	features = append(features, ShareRow{Label: "Multi-cluster", Share: multi})
	for _, r := range e.Tunnels {
		features = append(features, ShareRow{Label: "Tunnel: " + r.Label, Share: r.Share})
	}
	c.Features = shareChart("Feature adoption", "Feature", sortedShares(features, nil), true)

	labels := make([]string, len(e.VersionsByInstall.Items))
	counts := make([]int64, len(e.VersionsByInstall.Items))
	for i, r := range e.VersionsByInstall.Items {
		labels[i], counts[i] = r.Label, r.Installs
	}
	c.VersionsInstall = newBarChart("Versions by install", "Version", "Installs", false, countBars(labels, counts))
	c.Trend = buildTrendChart(e.VersionTrend, table)
	for _, bc := range []*barChart{&c.K8s, &c.Distro, &c.Arch, &c.Nodes, &c.Deployment, &c.Games, &c.Features, &c.VersionsInstall} {
		bc.Table = table
	}
	c.New.Table, c.Lapsed.Table = table, table
	c.VersionsWide = widenBars(c.VersionsInstall)
	return c
}

// trendChart is the version trend (FR-042) as designed in T114: the three
// change panels share one scale, and stale installs get a chart of their own.
// When Table is true the template renders Rows as one table instead.
type trendChart struct {
	Table  bool
	Newest string
	Panels []trendPanel
	Stale  trendPanel
	Rows   []trendRow
}

// trendPanel is one series of the version trend; Class colours its swatch.
type trendPanel struct {
	Label  string
	Class  string
	Note   string
	Chart  columnChart
	Narrow columnChart // set for the stale panel: its layout for narrow screens
}

// trendRow is one day of the version trend's table view.
type trendRow struct {
	Day        string
	Upgraded   string
	Downgraded string
	Unordered  string
	Stale      string
}

// Version trend layouts (T114): a change panel takes a third of the card and
// the stale chart all of it, so their text renders near 11px at full width.
var (
	trendPanelCols = colLayout{W: 424, H: 168, PlotLeft: 36, PlotRight: 392, PlotTop: 16, PlotBottom: 140}
	trendStaleCols = colLayout{W: 1320, H: 168, PlotLeft: 52, PlotRight: 1316, PlotTop: 16, PlotBottom: 140}
)

// buildTrendChart lays out the version trend. Upgraded, downgraded and
// unordered changes share one scale; stale installs, a count of installs
// rather than of changes, get their own.
func buildTrendChart(t VersionTrend, table bool) trendChart {
	n := len(t.Days)
	up, down, unordered, stale := make([]point, n), make([]point, n), make([]point, n), make([]point, n)
	c := trendChart{Table: table, Newest: t.NewestRelease, Rows: make([]trendRow, n)}
	var maxChange int64
	for i, d := range t.Days {
		up[i] = point{Label: d.Day, Value: d.Upgraded}
		down[i] = point{Label: d.Day, Value: d.Downgraded}
		unordered[i] = point{Label: d.Day, Value: d.Unordered}
		stale[i] = point{Label: d.Day, Value: d.Stale}
		maxChange = max(maxChange, d.Upgraded, d.Downgraded, d.Unordered)
		c.Rows[i] = trendRow{
			Day: d.Day, Upgraded: groupInt(d.Upgraded), Downgraded: groupInt(d.Downgraded),
			Unordered: groupInt(d.Unordered), Stale: groupInt(d.Stale),
		}
	}
	every := max(1, (n-1)/2) // three labels, from the first day to about the last
	c.Panels = []trendPanel{
		{Label: "Upgraded", Class: seriesPink,
			Chart: layoutColumns(trendPanelCols, "Upgraded installs per day", seriesPink, "Day", "Installs", up, every, maxChange)},
		{Label: "Downgraded", Class: seriesBlue,
			Chart: layoutColumns(trendPanelCols, "Downgraded installs per day", seriesBlue, "Day", "Installs", down, every, maxChange)},
		{Label: "Changed (unordered)", Class: seriesGreen,
			Chart: layoutColumns(trendPanelCols, "Unordered version changes per day", seriesGreen, "Day", "Installs", unordered, every, maxChange)},
	}
	note := "On its own scale"
	if t.NewestRelease != "" {
		note = "Behind " + t.NewestRelease + ", on its own scale"
	}
	c.Stale = trendPanel{Label: "Stale", Class: seriesAmber, Note: note,
		Chart:  layoutColumns(trendStaleCols, "Stale installs per day", seriesAmber, "Day", "Installs", stale, dayLabelEvery(n), 0),
		Narrow: layoutColumns(trendPanelCols, "Stale installs per day", seriesAmber, "Day", "Installs", stale, every, 0)}
	return c
}

// shareChart draws shares as horizontal bars. With track set every bar is a
// share of 100% and sits on a full-width track; otherwise bars are scaled to
// the largest share.
func shareChart(name, labelHead string, rows []ShareRow, track bool) barChart {
	var maxShare float64
	for _, r := range rows {
		maxShare = max(maxShare, r.Share)
	}
	items := make([]barItem, len(rows))
	for i, r := range rows {
		frac := r.Share
		if !track && maxShare > 0 {
			frac = r.Share / maxShare
		}
		items[i] = barItem{Label: displayLabel(r.Label), Value: percentString(r.Share), Frac: frac}
	}
	return newBarChart(name, labelHead, "Share of install-days", track, items)
}

// displayLabel shows a node or cluster band with an en dash: "2-3" as "2–3".
func displayLabel(label string) string {
	if len(label) > 0 && strings.Count(label, "-") == 1 && label[0] >= '0' && label[0] <= '9' {
		return strings.Replace(label, "-", "–", 1)
	}
	return label
}
