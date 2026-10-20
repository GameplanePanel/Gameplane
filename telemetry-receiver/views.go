package main

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
	"golang.org/x/mod/semver"
)

// validRanges are the dashboard ranges in days (FR-022); anything else falls
// back to defaultRange.
var validRanges = []int{7, 30, 90, 365}

const (
	defaultRange = 30
	// topVersions is how many versions are listed before "Other" (FR-024).
	topVersions = 10
	// Labels of the two aggregate version rows. Stored rows use the lower-case
	// forms written by versionLabel.
	versionOtherLabel   = "Other"
	versionInvalidLabel = "Invalid"
	versionOtherStored  = "other"
	versionInvalidStore = "invalid"
)

// fleetBandBounds are the inclusive upper bounds of the fleet-size bands
// (data-model.md); a final "250+" band takes everything above the last bound.
var fleetBandBounds = []int{0, 1, 2, 5, 10, 25, 50, 100, 250}

// fleetBandOver is the label of the last, open-ended fleet band.
const fleetBandOver = "250+"

// Views is the dashboard view model, the body of GET /api/v1/views
// (contracts/receiver-http.md). Extended is nil when the range has no
// extended reports.
type Views struct {
	Range    int            `json:"range"`
	AsOf     string         `json:"asOf"`
	Empty    bool           `json:"empty"`
	Basic    *BasicViews    `json:"basic"`
	Extended *ExtendedViews `json:"extended"`
}

// ExtendedViews is the extended block (FR-025, FR-026). Every list is
// non-nil, so it encodes as [] and never as null.
type ExtendedViews struct {
	Coverage          float64           `json:"coverage"`
	Installs          InstallCounts     `json:"installs"`
	NewPerDay         []DayCount        `json:"newPerDay"`
	LapsedPerDay      []DayCount        `json:"lapsedPerDay"`
	VersionsByInstall VersionsByInstall `json:"versionsByInstall"`
	VersionTrend      VersionTrend      `json:"versionTrend"`
	Env               EnvViews          `json:"env"`
	Games             []GameShare       `json:"games"`
	Features          []FeatureShare    `json:"features"`
	Tunnels           []ShareRow        `json:"tunnels"`
	Clusters          []ShareRow        `json:"clusters"`
	DB                []ShareRow        `json:"db"`
	Language          []ShareRow        `json:"language"`
}

// InstallCounts are unique installs by recency: activity records last seen
// on or after asOf, asOf-6 and asOf-29.
type InstallCounts struct {
	Active1d  int64 `json:"active1d"`
	Active7d  int64 `json:"active7d"`
	Active30d int64 `json:"active30d"`
}

// DayCount is one day of a new-installs or lapsed-installs series.
type DayCount struct {
	Day   string `json:"day"`
	Count int64  `json:"count"`
}

// VersionsByInstall is version adoption by install (activity records). It
// covers WindowDays = min(range, ACTIVITY_EXPIRY_DAYS) days.
type VersionsByInstall struct {
	WindowDays int              `json:"windowDays"`
	Items      []InstallVersion `json:"items"`
}

// InstallVersion is one version with the number of installs on it.
type InstallVersion struct {
	Label    string `json:"label"`
	Installs int64  `json:"installs"`
}

// VersionTrend is the daily version trend over the range (FR-042).
// NewestRelease is the highest semver version reported by any install in the
// range, pre-releases included (OD-7, OD-10), or "" when none is semver.
type VersionTrend struct {
	NewestRelease string            `json:"newestRelease"`
	Days          []VersionTrendDay `json:"days"`
}

// VersionTrendDay is one day of the version trend.
type VersionTrendDay struct {
	Day        string `json:"day"`
	Upgraded   int64  `json:"upgraded"`
	Downgraded int64  `json:"downgraded"`
	Unordered  int64  `json:"unordered"`
	Stale      int64  `json:"stale"`
}

// EnvViews are the environment shares.
type EnvViews struct {
	K8s        []ShareRow `json:"k8s"`
	Distro     []ShareRow `json:"distro"`
	Arch       []ShareRow `json:"arch"`
	Nodes      []ShareRow `json:"nodes"`
	Deployment []ShareRow `json:"deployment"`
}

// ShareRow is a category's share of the reporting install-days in the range
// (research R4): its summed installs over the summed extended reports.
type ShareRow struct {
	Label string  `json:"label"`
	Share float64 `json:"share"`
}

// GameShare is one module's install share and its average servers per day.
type GameShare struct {
	Module        string  `json:"module"`
	InstallsShare float64 `json:"installsShare"`
	ServersPerDay float64 `json:"serversPerDay"`
}

// FeatureShare is one boolean feature's share of the install-days.
type FeatureShare struct {
	Feature string  `json:"feature"`
	Share   float64 `json:"share"`
}

// BasicViews is the basic block (FR-024).
type BasicViews struct {
	ReportsPerDay []DayReports `json:"reportsPerDay"`
	Versions      []VersionRow `json:"versions"`
	Fleet         FleetViews   `json:"fleet"`
	LatestDay     LatestDayRow `json:"latestDay"`
}

// DayReports is one day of the reports-per-day trend.
type DayReports struct {
	Day     string `json:"day"`
	Reports int64  `json:"reports"`
}

// VersionRow is one version-adoption bar.
type VersionRow struct {
	Label   string `json:"label"`
	Reports int64  `json:"reports"`
}

// FleetViews holds the fleet-size distributions.
type FleetViews struct {
	Servers   FleetDistribution `json:"servers"`
	Templates FleetDistribution `json:"templates"`
}

// FleetDistribution is a banded histogram plus the exact median. A median of
// 1001 means "more than 1000".
type FleetDistribution struct {
	Bands  []FleetBand `json:"bands"`
	Median float64     `json:"median"`
}

// FleetBand is one histogram band; Le is the inclusive upper bound ("250+"
// for the open band).
type FleetBand struct {
	Le      string `json:"le"`
	Reports int64  `json:"reports"`
}

// LatestDayRow holds the totals for the as-of day.
type LatestDayRow struct {
	ServersTotal   int64 `json:"serversTotal"`
	TemplatesTotal int64 `json:"templatesTotal"`
}

// normalizeRange returns days when it is a valid range, else defaultRange.
func normalizeRange(days int) int {
	for _, r := range validRanges {
		if days == r {
			return r
		}
	}
	return defaultRange
}

// BuildViews computes the basic dashboard views over the rangeDays ending
// on today, the current UTC day, which is still in progress (OD-6). An
// invalid rangeDays falls back to 30. When no daily_basic row exists in the
// range the result is the empty state: Empty is true and Basic is nil.
func BuildViews(ctx context.Context, st *store, rangeDays int, now time.Time) (Views, error) {
	rangeDays = normalizeRange(rangeDays)
	asOf := now.UTC().Truncate(24 * time.Hour)
	from := asOf.AddDate(0, 0, 1-rangeDays)
	v := Views{Range: rangeDays, AsOf: dayString(asOf)}
	fromDay, toDay := dayString(from), dayString(asOf)

	perDay, latest, err := queryDailyBasic(ctx, st.db, fromDay, toDay, v.AsOf)
	if err != nil {
		return Views{}, err
	}
	if len(perDay) == 0 {
		v.Empty = true
		return v, nil
	}
	b := &BasicViews{LatestDay: latest}
	b.ReportsPerDay = denseDays(from, rangeDays, perDay)
	if b.Versions, err = queryVersions(ctx, st.db, fromDay, toDay); err != nil {
		return Views{}, err
	}
	if b.Fleet.Servers, err = queryFleet(ctx, st.db, fleetMetricServers, fromDay, toDay); err != nil {
		return Views{}, err
	}
	if b.Fleet.Templates, err = queryFleet(ctx, st.db, fleetMetricTemplates, fromDay, toDay); err != nil {
		return Views{}, err
	}
	v.Basic = b
	var basicReports int64
	for _, n := range perDay {
		basicReports += n
	}
	if v.Extended, err = buildExtended(ctx, st, rangeDays, fromDay, toDay, basicReports); err != nil {
		return Views{}, err
	}
	return v, nil
}

// queryDailyBasic returns reports per day that has a row in [from, to], and
// the totals of the asOf day (zero when it has no row).
func queryDailyBasic(ctx context.Context, db *sql.DB, from, to, asOf string) (map[string]int64, LatestDayRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT day, reports, servers_sum, templates_sum FROM daily_basic WHERE day >= ? AND day <= ?`, from, to)
	if err != nil {
		return nil, LatestDayRow{}, fmt.Errorf("query daily_basic: %w", err)
	}
	defer func() { _ = rows.Close() }()
	perDay := make(map[string]int64)
	var latest LatestDayRow
	for rows.Next() {
		var day string
		var reports, servers, templates int64
		if err := rows.Scan(&day, &reports, &servers, &templates); err != nil {
			return nil, LatestDayRow{}, fmt.Errorf("scan daily_basic: %w", err)
		}
		perDay[day] = reports
		if day == asOf {
			latest = LatestDayRow{ServersTotal: servers, TemplatesTotal: templates}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, LatestDayRow{}, fmt.Errorf("read daily_basic: %w", err)
	}
	return perDay, latest, nil
}

// denseDays lists every day of the range, oldest first, with 0 for days that
// have no row.
func denseDays(from time.Time, n int, perDay map[string]int64) []DayReports {
	out := make([]DayReports, 0, n)
	for i := range n {
		day := dayString(from.AddDate(0, 0, i))
		out = append(out, DayReports{Day: day, Reports: perDay[day]})
	}
	return out
}

// queryVersions sums daily_version over [from, to] and returns the top
// topVersions versions by reports (ties by label), then Other, then Invalid.
// Other and Invalid are left out when they have no reports.
func queryVersions(ctx context.Context, db *sql.DB, from, to string) ([]VersionRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT version, SUM(reports) FROM daily_version WHERE day >= ? AND day <= ? GROUP BY version`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query daily_version: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var named []VersionRow
	var other, invalid int64
	for rows.Next() {
		var version string
		var n int64
		if err := rows.Scan(&version, &n); err != nil {
			return nil, fmt.Errorf("scan daily_version: %w", err)
		}
		switch version {
		case versionInvalidStore:
			invalid += n
		case versionOtherStored:
			other += n
		default:
			named = append(named, VersionRow{Label: version, Reports: n})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read daily_version: %w", err)
	}
	sort.Slice(named, func(i, j int) bool {
		if named[i].Reports != named[j].Reports {
			return named[i].Reports > named[j].Reports
		}
		return named[i].Label < named[j].Label
	})
	if len(named) > topVersions {
		for _, r := range named[topVersions:] {
			other += r.Reports
		}
		named = named[:topVersions]
	}
	out := append([]VersionRow{}, named...)
	if other > 0 {
		out = append(out, VersionRow{Label: versionOtherLabel, Reports: other})
	}
	if invalid > 0 {
		out = append(out, VersionRow{Label: versionInvalidLabel, Reports: invalid})
	}
	return out, nil
}

// queryFleet sums the exact-value histogram of one metric over [from, to],
// then derives the bands and the exact median.
func queryFleet(ctx context.Context, db *sql.DB, metric, from, to string) (FleetDistribution, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT value, SUM(reports) FROM daily_fleet WHERE metric = ? AND day >= ? AND day <= ?
		 GROUP BY value ORDER BY value`, metric, from, to)
	if err != nil {
		return FleetDistribution{}, fmt.Errorf("query daily_fleet %s: %w", metric, err)
	}
	defer func() { _ = rows.Close() }()
	var hist []fleetPoint
	for rows.Next() {
		var p fleetPoint
		if err := rows.Scan(&p.value, &p.reports); err != nil {
			return FleetDistribution{}, fmt.Errorf("scan daily_fleet: %w", err)
		}
		hist = append(hist, p)
	}
	if err := rows.Err(); err != nil {
		return FleetDistribution{}, fmt.Errorf("read daily_fleet: %w", err)
	}
	return FleetDistribution{Bands: fleetBands(hist), Median: fleetMedian(hist)}, nil
}

// fleetPoint is one exact histogram entry; points are sorted by value.
type fleetPoint struct {
	value   int
	reports int64
}

// fleetBands folds an exact histogram into the fixed bands. Every band is
// returned, including empty ones, so a histogram has a stable shape.
func fleetBands(hist []fleetPoint) []FleetBand {
	bands := make([]FleetBand, 0, len(fleetBandBounds)+1)
	for _, bound := range fleetBandBounds {
		bands = append(bands, FleetBand{Le: fmt.Sprint(bound)})
	}
	bands = append(bands, FleetBand{Le: fleetBandOver})
	for _, p := range hist {
		i := len(fleetBandBounds)
		for k, bound := range fleetBandBounds {
			if p.value <= bound {
				i = k
				break
			}
		}
		bands[i].Reports += p.reports
	}
	return bands
}

// fleetMedian returns the exact median of the histogram: the middle value,
// or the mean of the two middle values for an even count. It is 0 for an
// empty histogram. hist must be sorted by value.
func fleetMedian(hist []fleetPoint) float64 {
	var total int64
	for _, p := range hist {
		total += p.reports
	}
	if total == 0 {
		return 0
	}
	at := func(rank int64) int {
		var seen int64
		for _, p := range hist {
			seen += p.reports
			if rank < seen {
				return p.value
			}
		}
		return hist[len(hist)-1].value
	}
	lo, hi := at((total-1)/2), at(total/2)
	return float64(lo+hi) / 2
}

// buildExtended computes the extended block over [from, to], the rangeDays
// ending on today (in progress). basicReports is the sum of daily_basic
// reports over the same range. It returns nil when the range has no extended
// reports.
func buildExtended(ctx context.Context, st *store, rangeDays int, from, to string, basicReports int64) (*ExtendedViews, error) {
	var extReports, extDays int64
	if err := st.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(ext_reports), 0), COUNT(CASE WHEN ext_reports > 0 THEN 1 END)
		 FROM daily_ext WHERE day >= ? AND day <= ?`, from, to).Scan(&extReports, &extDays); err != nil {
		return nil, fmt.Errorf("query daily_ext totals: %w", err)
	}
	if extReports == 0 {
		return nil, nil
	}
	asOf, err := time.Parse(time.DateOnly, to)
	if err != nil {
		return nil, fmt.Errorf("parse asOf %q: %w", to, err)
	}
	e := &ExtendedViews{}
	if basicReports > 0 {
		e.Coverage = float64(extReports) / float64(basicReports)
	}
	if e.Installs, err = queryInstallCounts(ctx, st.db, asOf); err != nil {
		return nil, err
	}
	if e.NewPerDay, e.LapsedPerDay, err = queryNewLapsed(ctx, st.db, asOf, rangeDays); err != nil {
		return nil, err
	}
	window := rangeDays
	if x := st.activityExpiryDays; x > 0 && x < window {
		window = x
	}
	e.VersionsByInstall.WindowDays = window
	if e.VersionsByInstall.Items, err = queryInstallVersions(ctx, st.db, dayString(asOf.AddDate(0, 0, 1-window))); err != nil {
		return nil, err
	}
	if e.VersionTrend, err = queryVersionTrend(ctx, st.db, asOf, rangeDays); err != nil {
		return nil, err
	}
	dims, err := queryDims(ctx, st.db, from, to, extReports)
	if err != nil {
		return nil, err
	}
	e.Env = EnvViews{
		K8s:        sortedShares(dims[dimK8s], nil),
		Distro:     sortedShares(dims[dimDistro], nil),
		Arch:       sortedShares(dims[dimArch], nil),
		Nodes:      sortedShares(dims[dimNodes], telemetryschema.NodeBands),
		Deployment: sortedShares(dims[dimDeployment], nil),
	}
	e.Tunnels = sortedShares(dims[dimTunnel], nil)
	e.Clusters = sortedShares(dims[dimClusters], telemetryschema.ClusterBands)
	e.DB = sortedShares(dims[dimDB], nil)
	e.Language = sortedShares(dims[dimLanguage], nil)
	featureShares := make(map[string]float64, len(featureNames))
	for _, r := range dims[dimFeature] {
		featureShares[r.Label] = r.Share
	}
	e.Features = make([]FeatureShare, 0, len(featureNames))
	for _, f := range featureNames {
		e.Features = append(e.Features, FeatureShare{Feature: f, Share: featureShares[f]})
	}
	if e.Games, err = queryGames(ctx, st.db, from, to, extReports, extDays); err != nil {
		return nil, err
	}
	return e, nil
}

// queryInstallCounts counts unique installs by recency from the activity
// records (data-model.md, Unique active installs).
func queryInstallCounts(ctx context.Context, db *sql.DB, asOf time.Time) (InstallCounts, error) {
	d1, d7, d30 := dayString(asOf), dayString(asOf.AddDate(0, 0, -6)), dayString(asOf.AddDate(0, 0, -29))
	var c InstallCounts
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(CASE WHEN last_seen >= ? THEN 1 END), COUNT(CASE WHEN last_seen >= ? THEN 1 END), COUNT(*)
		 FROM activity WHERE last_seen >= ?`, d1, d7, d30).Scan(&c.Active1d, &c.Active7d, &c.Active30d); err != nil {
		return InstallCounts{}, fmt.Errorf("query activity counts: %w", err)
	}
	return c, nil
}

// queryNewLapsed returns the dense new and lapsed install series over the
// rangeDays ending on asOf.
func queryNewLapsed(ctx context.Context, db *sql.DB, asOf time.Time, rangeDays int) ([]DayCount, []DayCount, error) {
	from := asOf.AddDate(0, 0, 1-rangeDays)
	rows, err := db.QueryContext(ctx,
		`SELECT day, new_installs, lapsed_installs FROM daily_ext WHERE day >= ? AND day <= ?`,
		dayString(from), dayString(asOf))
	if err != nil {
		return nil, nil, fmt.Errorf("query daily_ext series: %w", err)
	}
	defer func() { _ = rows.Close() }()
	newBy, lapsedBy := make(map[string]int64), make(map[string]int64)
	for rows.Next() {
		var day string
		var n, l int64
		if err := rows.Scan(&day, &n, &l); err != nil {
			return nil, nil, fmt.Errorf("scan daily_ext series: %w", err)
		}
		newBy[day], lapsedBy[day] = n, l
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read daily_ext series: %w", err)
	}
	newPer, lapsedPer := make([]DayCount, 0, rangeDays), make([]DayCount, 0, rangeDays)
	for i := range rangeDays {
		day := dayString(from.AddDate(0, 0, i))
		newPer = append(newPer, DayCount{Day: day, Count: newBy[day]})
		lapsedPer = append(lapsedPer, DayCount{Day: day, Count: lapsedBy[day]})
	}
	return newPer, lapsedPer, nil
}

// queryVersionTrend returns the dense daily version trend over the rangeDays
// ending on asOf (FR-042, data-model § Derived views). Changes come from
// daily_version_change. Stale counts, per day, the installs on a semver
// version lower than the newest semver version seen in the range; versions
// that aren't semver are never stale (OD-8).
func queryVersionTrend(ctx context.Context, db *sql.DB, asOf time.Time, rangeDays int) (VersionTrend, error) {
	from := asOf.AddDate(0, 0, 1-rangeDays)
	fromDay, toDay := dayString(from), dayString(asOf)
	t := VersionTrend{Days: make([]VersionTrendDay, 0, rangeDays)}

	type dayVersion struct {
		day, version string
		installs     int64
	}
	var installs []dayVersion
	rows, err := db.QueryContext(ctx,
		`SELECT day, version, installs FROM daily_install_version WHERE day >= ? AND day <= ?`, fromDay, toDay)
	if err != nil {
		return VersionTrend{}, fmt.Errorf("query daily_install_version: %w", err)
	}
	defer func() { _ = rows.Close() }()
	newest := ""
	for rows.Next() {
		var dv dayVersion
		if err := rows.Scan(&dv.day, &dv.version, &dv.installs); err != nil {
			return VersionTrend{}, fmt.Errorf("scan daily_install_version: %w", err)
		}
		installs = append(installs, dv)
		if v, ok := canonicalSemver(dv.version); ok && (newest == "" || semver.Compare(v, newest) > 0) {
			newest = v
		}
	}
	if err := rows.Err(); err != nil {
		return VersionTrend{}, fmt.Errorf("read daily_install_version: %w", err)
	}
	stale := make(map[string]int64)
	for _, dv := range installs {
		if v, ok := canonicalSemver(dv.version); ok && semver.Compare(v, newest) < 0 {
			stale[dv.day] += dv.installs
		}
	}
	t.NewestRelease = newest

	changes := make(map[string]map[string]int64)
	crows, err := db.QueryContext(ctx,
		`SELECT day, kind, installs FROM daily_version_change WHERE day >= ? AND day <= ?`, fromDay, toDay)
	if err != nil {
		return VersionTrend{}, fmt.Errorf("query daily_version_change: %w", err)
	}
	defer func() { _ = crows.Close() }()
	for crows.Next() {
		var day, kind string
		var n int64
		if err := crows.Scan(&day, &kind, &n); err != nil {
			return VersionTrend{}, fmt.Errorf("scan daily_version_change: %w", err)
		}
		if changes[day] == nil {
			changes[day] = make(map[string]int64)
		}
		changes[day][kind] = n
	}
	if err := crows.Err(); err != nil {
		return VersionTrend{}, fmt.Errorf("read daily_version_change: %w", err)
	}

	for i := range rangeDays {
		day := dayString(from.AddDate(0, 0, i))
		c := changes[day]
		t.Days = append(t.Days, VersionTrendDay{
			Day:        day,
			Upgraded:   c[changeUpgraded],
			Downgraded: c[changeDowngraded],
			Unordered:  c[changeUnordered],
			Stale:      stale[day],
		})
	}
	return t, nil
}

// queryInstallVersions counts installs per last reported version among the
// activity records last seen on or after sinceDay: the top topVersions, then
// Other and Invalid (left out when empty).
func queryInstallVersions(ctx context.Context, db *sql.DB, sinceDay string) ([]InstallVersion, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT last_version, COUNT(*) FROM activity WHERE last_seen >= ? GROUP BY last_version`, sinceDay)
	if err != nil {
		return nil, fmt.Errorf("query activity versions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var named []InstallVersion
	var other, invalid int64
	for rows.Next() {
		var version string
		var n int64
		if err := rows.Scan(&version, &n); err != nil {
			return nil, fmt.Errorf("scan activity versions: %w", err)
		}
		switch version {
		case versionInvalidStore:
			invalid += n
		case versionOtherStored:
			other += n
		default:
			named = append(named, InstallVersion{Label: version, Installs: n})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read activity versions: %w", err)
	}
	sort.Slice(named, func(i, j int) bool {
		if named[i].Installs != named[j].Installs {
			return named[i].Installs > named[j].Installs
		}
		return named[i].Label < named[j].Label
	})
	if len(named) > topVersions {
		for _, r := range named[topVersions:] {
			other += r.Installs
		}
		named = named[:topVersions]
	}
	out := append([]InstallVersion{}, named...)
	if other > 0 {
		out = append(out, InstallVersion{Label: versionOtherLabel, Installs: other})
	}
	if invalid > 0 {
		out = append(out, InstallVersion{Label: versionInvalidLabel, Installs: invalid})
	}
	return out, nil
}

// queryDims sums daily_dim over [from, to] and returns each dimension's rows
// as shares of extReports (install-day weighted, research R4).
func queryDims(ctx context.Context, db *sql.DB, from, to string, extReports int64) (map[string][]ShareRow, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT dim, value, SUM(installs) FROM daily_dim WHERE day >= ? AND day <= ? GROUP BY dim, value`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query daily_dim: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string][]ShareRow)
	for rows.Next() {
		var dim, value string
		var n int64
		if err := rows.Scan(&dim, &value, &n); err != nil {
			return nil, fmt.Errorf("scan daily_dim: %w", err)
		}
		out[dim] = append(out[dim], ShareRow{Label: value, Share: float64(n) / float64(extReports)})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read daily_dim: %w", err)
	}
	return out, nil
}

// sortedShares returns rows ordered by order (members not in order, such as
// "other", come last) when order is non-nil, else by share descending then
// label. It never returns nil.
func sortedShares(rows []ShareRow, order []string) []ShareRow {
	out := append([]ShareRow{}, rows...)
	rank := func(label string) int {
		if i := slices.Index(order, label); i >= 0 {
			return i
		}
		return len(order)
	}
	sort.Slice(out, func(i, j int) bool {
		if order != nil {
			if ri, rj := rank(out[i].Label), rank(out[j].Label); ri != rj {
				return ri < rj
			}
		} else if out[i].Share != out[j].Share {
			return out[i].Share > out[j].Share
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// queryGames sums daily_game over [from, to]: each module's installs as a
// share of extReports, and its servers averaged over the days that have
// extended reports (extDays).
func queryGames(ctx context.Context, db *sql.DB, from, to string, extReports, extDays int64) ([]GameShare, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT module, SUM(installs), SUM(servers) FROM daily_game WHERE day >= ? AND day <= ? GROUP BY module`, from, to)
	if err != nil {
		return nil, fmt.Errorf("query daily_game: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []GameShare{}
	for rows.Next() {
		var module string
		var installs, servers int64
		if err := rows.Scan(&module, &installs, &servers); err != nil {
			return nil, fmt.Errorf("scan daily_game: %w", err)
		}
		out = append(out, GameShare{
			Module:        module,
			InstallsShare: float64(installs) / float64(extReports),
			ServersPerDay: round1(float64(servers) / float64(max(extDays, 1))),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read daily_game: %w", err)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].InstallsShare != out[j].InstallsShare {
			return out[i].InstallsShare > out[j].InstallsShare
		}
		return out[i].Module < out[j].Module
	})
	return out, nil
}

// round1 rounds f to one decimal place.
func round1(f float64) float64 { return math.Round(f*10) / 10 }
