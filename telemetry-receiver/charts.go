package main

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
)

// Chart geometry (SVG user units; every chart scales to its container).
const (
	colChartW      = 960.0
	colChartH      = 240.0
	colPlotLeft    = 56.0
	colPlotRight   = 952.0
	colPlotTop     = 16.0
	colPlotBottom  = 212.0
	colSlotFill    = 0.62 // share of a slot the column itself covers
	colTickCount   = 3    // gridlines above the baseline
	colLabelOffset = 4.0

	barChartW    = 600.0
	barRowH      = 30.0
	barLabelW    = 150.0
	barMaxW      = 380.0
	barValueX    = 592.0
	barHeight    = 12.0
	barTrackFrac = 1.0
)

// Series classes; style.css maps them to the chart tokens.
const (
	seriesPink  = "s1"
	seriesBlue  = "s2"
	seriesGreen = "s3"
	seriesAmber = "s4"
)

// pageFuncs are the template functions of the dashboard pages.
var pageFuncs = template.FuncMap{"num": groupInt, "pct": percentString}

// tableRow is one row of the "View as table" alternative of a chart.
type tableRow struct {
	Label string
	Value string
}

// tick is one y-axis gridline.
type tick struct {
	Y     float64
	Label string
}

// xLabel is one x-axis label.
type xLabel struct {
	X     float64
	Label string
}

// column is one vertical bar; Title becomes the SVG <title> tooltip.
type column struct {
	X, Y, W, H float64
	Title      string
}

// columnChart is a vertical bar chart. It starts at zero, has a single
// y-axis and labels only the latest value. When Table is true the template
// renders Rows as an HTML table instead of the SVG.
type columnChart struct {
	Table     bool
	Name      string
	Class     string
	Head1     string
	Head2     string
	W, H      float64
	PlotLeft  float64
	PlotRight float64
	LabelX    float64 // x of the y-axis labels
	BaseY     float64
	AxisY     float64 // y of the x-axis labels
	Ticks     []tick
	Cols      []column
	XLabels   []xLabel
	LastText  string
	LastX     float64
	LastY     float64
	Rows      []tableRow
}

// point is one input of a column chart.
type point struct {
	Label string
	Value int64
}

// niceScale returns a tick step and the axis top (step * colTickCount) for a
// maximum value: the smallest 1, 2 or 5 times a power of ten whose top covers
// maxV.
func niceScale(maxV int64) (step, top int64) {
	if maxV < 1 {
		return 1, colTickCount
	}
	for mag := int64(1); ; mag *= 10 {
		for _, m := range []int64{1, 2, 5} {
			if s := m * mag; s*colTickCount >= maxV {
				return s, s * colTickCount
			}
		}
	}
}

// groupInt formats n with thousands separators: 1284 becomes "1,284".
func groupInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// percentString formats a 0..1 share as a whole percent: 0.384 becomes "38%".
func percentString(share float64) string {
	return strconv.Itoa(int(math.Round(share*100))) + "%"
}

// colLayout is the geometry of a column chart in SVG user units.
type colLayout struct {
	W, H                float64
	PlotLeft, PlotRight float64
	PlotTop, PlotBottom float64
}

// dailyCols is the layout of the daily and fleet charts.
var dailyCols = colLayout{
	W: colChartW, H: colChartH, PlotLeft: colPlotLeft, PlotRight: colPlotRight,
	PlotTop: colPlotTop, PlotBottom: colPlotBottom,
}

// newColumnChart lays out pts as columns. labelEvery spaces the x labels
// (every n-th point, always including the first). valueHead is the header of
// the table view's value column.
func newColumnChart(name, class, labelHead, valueHead string, pts []point, labelEvery int) columnChart {
	return layoutColumns(dailyCols, name, class, labelHead, valueHead, pts, labelEvery, 0)
}

// layoutColumns lays out pts as columns in g. The axis covers at least
// scaleMax, so charts given the same scaleMax share one scale.
func layoutColumns(g colLayout, name, class, labelHead, valueHead string, pts []point, labelEvery int, scaleMax int64) columnChart {
	c := columnChart{
		Name: name, Class: class, Head1: labelHead, Head2: valueHead,
		W: g.W, H: g.H, PlotLeft: g.PlotLeft, PlotRight: g.PlotRight,
		LabelX: g.PlotLeft - 8, BaseY: g.PlotBottom, AxisY: g.H - 4,
	}
	maxV := scaleMax
	for _, p := range pts {
		maxV = max(maxV, p.Value)
		c.Rows = append(c.Rows, tableRow{Label: p.Label, Value: groupInt(p.Value)})
	}
	step, top := niceScale(maxV)
	plotH := g.PlotBottom - g.PlotTop
	for k := int64(0); k <= colTickCount; k++ {
		c.Ticks = append(c.Ticks, tick{
			Y:     round1(g.PlotBottom - float64(k*step)/float64(top)*plotH),
			Label: groupInt(k * step),
		})
	}
	if len(pts) == 0 {
		return c
	}
	if labelEvery < 1 {
		labelEvery = 1
	}
	slot := (g.PlotRight - g.PlotLeft) / float64(len(pts))
	w := slot * colSlotFill
	for i, p := range pts {
		h := float64(p.Value) / float64(top) * plotH
		x := g.PlotLeft + slot*float64(i) + (slot-w)/2
		c.Cols = append(c.Cols, column{
			X: round1(x), Y: round1(g.PlotBottom - h), W: round1(w), H: round1(h),
			Title: p.Label + ": " + groupInt(p.Value),
		})
		if i%labelEvery == 0 {
			c.XLabels = append(c.XLabels, xLabel{X: round1(x + w/2), Label: p.Label})
		}
	}
	last := c.Cols[len(c.Cols)-1]
	c.LastText = groupInt(pts[len(pts)-1].Value)
	c.LastX = round1(last.X + last.W/2)
	c.LastY = round1(last.Y - colLabelOffset)
	return c
}

// barItem is one input of a bar chart. Frac is the bar length as a share of
// the full bar width (0..1); Value is the text shown at the right.
type barItem struct {
	Label string
	Value string
	Frac  float64
}

// barRow is one laid-out horizontal bar.
type barRow struct {
	Y, TextY float64
	RectY    float64 // top of the bar rectangle
	Bar      float64 // bar length
	Label    string
	Value    string
	Title    string
}

// barChart is a horizontal bar chart. When Table is true the template renders
// Rows as an HTML table instead of the SVG. Track draws a full-width track
// behind each bar.
type barChart struct {
	Table  bool
	Name   string
	Class  string
	Head1  string
	Head2  string
	Track  bool
	W, H   float64
	BarX   float64
	TrackW float64
	ValueX float64
	BarY   float64 // offset of the bar inside its row
	BarH   float64
	Bars   []barRow
	Rows   []tableRow
}

// newBarChart lays out items, top to bottom, with a value column on the right.
func newBarChart(name, labelHead, valueHead string, track bool, items []barItem) barChart {
	c := barChart{
		Name: name, Class: seriesPink, Head1: labelHead, Head2: valueHead, Track: track,
		W: barChartW, H: barRowH * float64(len(items)), BarX: barLabelW, TrackW: barMaxW,
		ValueX: barValueX, BarY: (barRowH - barHeight) / 2, BarH: barHeight,
	}
	for i, it := range items {
		frac := min(max(it.Frac, 0), barTrackFrac)
		y := barRowH * float64(i)
		c.Bars = append(c.Bars, barRow{
			Y: y, TextY: y + barRowH/2 + 4, RectY: y + c.BarY, Bar: round1(frac * barMaxW),
			Label: it.Label, Value: it.Value, Title: it.Label + ": " + it.Value,
		})
		c.Rows = append(c.Rows, tableRow{Label: it.Label, Value: it.Value})
	}
	return c
}

// Wide bar layout for a bar card that spans the page (Versions by install),
// so its text renders near 13px at full width.
const (
	wideBarChartW = 1320.0
	wideBarLabelW = 200.0
	wideBarMaxW   = 1020.0
	wideBarValueX = 1312.0
)

// widenBars returns c laid out for a full-width card. Bar lengths keep their
// share of the track.
func widenBars(c barChart) barChart {
	w := c
	w.W, w.BarX, w.TrackW, w.ValueX = wideBarChartW, wideBarLabelW, wideBarMaxW, wideBarValueX
	w.Bars = make([]barRow, len(c.Bars))
	for i, b := range c.Bars {
		b.Bar = round1(b.Bar / barMaxW * wideBarMaxW)
		w.Bars[i] = b
	}
	return w
}

// countBars builds bar items from counts, scaled to the largest count.
func countBars(labels []string, counts []int64) []barItem {
	var maxV int64
	for _, n := range counts {
		maxV = max(maxV, n)
	}
	items := make([]barItem, len(labels))
	for i := range labels {
		frac := 0.0
		if maxV > 0 {
			frac = float64(counts[i]) / float64(maxV)
		}
		items[i] = barItem{Label: labels[i], Value: groupInt(counts[i]), Frac: frac}
	}
	return items
}

// fleetLabels returns the display label of every fleet band: "0", "1", "2",
// "3–5", "6–10" and so on, ending with the open band.
func fleetLabels(bands []FleetBand) []string {
	out := make([]string, len(bands))
	prev := -1
	for i, b := range bands {
		bound, err := strconv.Atoi(b.Le)
		switch {
		case err != nil:
			out[i] = b.Le // the open "250+" band
		case bound == prev+1:
			out[i] = b.Le
			prev = bound
		default:
			out[i] = fmt.Sprintf("%d–%d", prev+1, bound)
			prev = bound
		}
	}
	return out
}

// fleetChart draws one fleet distribution as a column histogram.
func fleetChart(name, xHead string, d FleetDistribution, table bool) columnChart {
	labels := fleetLabels(d.Bands)
	pts := make([]point, len(d.Bands))
	for i, b := range d.Bands {
		pts[i] = point{Label: labels[i], Value: b.Reports}
	}
	c := newColumnChart(name, seriesPink, xHead, "Reports", pts, 1)
	c.Table = table
	return c
}

// rangeLink is one entry of the range selector.
type rangeLink struct {
	Days   int
	Href   string
	Active bool
}

// overviewPage is the data of the overview template: the view model plus
// the pre-computed charts. Views is embedded so templates read .Range,
// .AsOf, .Empty, .Basic and .Extended directly.
type overviewPage struct {
	Views
	Table          bool
	Ranges         []rangeLink
	TableHref      string
	ChartHref      string
	ReportsTotal   int64
	Since          string
	LatestReports  int64
	RangeReports   int64
	MeanServers    float64
	PerDay         columnChart
	VersionsChart  barChart
	ServersChart   columnChart
	TemplatesChart columnChart
	Ext            *extCharts
}

// newOverviewPage builds the overview data. total and since come from the
// store's meta table (reports_total, collection_started).
func newOverviewPage(v Views, table bool, total int64, since string) overviewPage {
	p := overviewPage{Views: v, Table: table, ReportsTotal: total, Since: since}
	for _, d := range validRanges {
		p.Ranges = append(p.Ranges, rangeLink{Days: d, Href: "/?range=" + strconv.Itoa(d), Active: d == v.Range})
	}
	q := "/?range=" + strconv.Itoa(v.Range)
	p.ChartHref = q
	p.TableHref = q + "&view=table"
	if v.Basic == nil {
		return p
	}
	b := v.Basic
	pts := make([]point, len(b.ReportsPerDay))
	for i, d := range b.ReportsPerDay {
		pts[i] = point{Label: d.Day, Value: d.Reports}
		p.RangeReports += d.Reports
	}
	if n := len(b.ReportsPerDay); n > 0 {
		p.LatestReports = b.ReportsPerDay[n-1].Reports
	}
	if p.LatestReports > 0 {
		p.MeanServers = round1(float64(b.LatestDay.ServersTotal) / float64(p.LatestReports))
	}
	p.PerDay = newColumnChart("Reports per day", seriesPink, "Day", "Reports", pts, dayLabelEvery(len(pts)))
	p.PerDay.Table = table

	labels := make([]string, len(b.Versions))
	counts := make([]int64, len(b.Versions))
	for i, r := range b.Versions {
		labels[i], counts[i] = r.Label, r.Reports
	}
	p.VersionsChart = newBarChart("Reports by version", "Version", "Reports", false, countBars(labels, counts))
	p.VersionsChart.Table = table
	p.ServersChart = fleetChart("Servers per install", "Game servers", b.Fleet.Servers, table)
	p.TemplatesChart = fleetChart("Templates per install", "Templates", b.Fleet.Templates, table)
	p.Ext = buildExtendedCharts(v, table)
	return p
}

// dayLabelEvery spaces the x labels of a daily chart so that about five show.
func dayLabelEvery(n int) int {
	return max(1, n/5)
}
