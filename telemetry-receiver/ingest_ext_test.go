package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// extStart is 12:00 UTC on 2026-10-07, the clock of the extended tests.
var extStart = time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

// extInstall is a synthetic install: an install ID and the signing secret its
// key derives from.
type extInstall struct {
	id     string
	secret []byte
}

// newExtInstall returns install number n. Its ID is a lowercase UUIDv4 and
// its secret is derived from n, so runs are reproducible.
func newExtInstall(n int) extInstall {
	secret := make([]byte, telemetryschema.SecretSize)
	secret[0], secret[1] = byte(n), byte(n>>8)
	return extInstall{id: fmt.Sprintf("00000000-0000-4000-8000-%012d", n), secret: secret}
}

// withSecret returns the same install ID with another signing secret: a
// different key claiming the same ID.
func (in extInstall) withSecret(b byte) extInstall {
	secret := make([]byte, telemetryschema.SecretSize)
	secret[0], secret[1], secret[2] = 0xEE, 0xEE, b
	return extInstall{id: in.id, secret: secret}
}

// report builds a report with an ext part sent at sentAt and returns the body
// and the signature header value. mod, when non-nil, changes the report before
// it is encoded and signed. Servers is 3 and templates is 2.
func (in extInstall) report(t *testing.T, sentAt time.Time, mod func(*telemetryschema.Report)) (string, string) {
	t.Helper()
	priv, err := telemetryschema.DeriveKey(in.secret, in.id)
	if err != nil {
		t.Fatalf("DeriveKey: %v", err)
	}
	rep := telemetryschema.Report{Version: "1.0.0", Servers: 3, Templates: 2, Ext: &telemetryschema.Extended{
		Schema: telemetryschema.SchemaVersion, InstallID: in.id,
		Env:   telemetryschema.Env{K8s: []string{"1.31"}, Distro: []string{"k3s"}, Arch: []string{"amd64"}, Nodes: "1", Deployment: "cluster"},
		Games: telemetryschema.Games{Official: map[string]int{"minecraft-java": 2}, Custom: 1},
		Features: telemetryschema.Features{
			Backups: true, Tunnels: []string{"playit"}, Clusters: "1", DB: "sqlite", Language: "en",
		},
		Key: telemetryschema.PublicKeyString(priv), SentAt: sentAt,
	}}
	if mod != nil {
		mod(&rep)
	}
	body, err := telemetryschema.Encode(rep)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return string(body), telemetryschema.Sign(priv, body)
}

// newExtServer returns a receiver over a private in-memory store with a
// controllable clock. The returned pointer is the clock: assign to it to move
// time.
func newExtServer(t *testing.T, cfg config, start time.Time) (*server, *time.Time) {
	t.Helper()
	s := newServer(cfg)
	t.Cleanup(func() { _ = s.store.close() })
	clock := start
	s.now = func() time.Time { return clock }
	return s, &clock
}

// extSend POSTs body to /ingest from a fixed source, with the signature
// header when sig is not empty.
func extSend(t *testing.T, s *server, body, sig string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ingest", strings.NewReader(body))
	r.RemoteAddr = "192.0.2.1:1000"
	if sig != "" {
		r.Header.Set(telemetryschema.SignatureHeader, sig)
	}
	w := httptest.NewRecorder()
	s.ingest(w, r)
	return w
}

// extDump returns the full content of every receiver table, so a test can
// assert that a refused report changed nothing at all (SC-014).
func extDump(t *testing.T, st *store) string {
	t.Helper()
	var b strings.Builder
	for _, table := range wantTables {
		rows, err := st.db.QueryContext(context.Background(), "SELECT * FROM "+table+" ORDER BY rowid")
		if err != nil {
			t.Fatalf("dump %s: %v", table, err)
		}
		cols, err := rows.Columns()
		if err != nil {
			t.Fatalf("columns %s: %v", table, err)
		}
		fmt.Fprintf(&b, "[%s]\n", table)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan %s: %v", table, err)
			}
			fmt.Fprintln(&b, vals...)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close %s: %v", table, err)
		}
	}
	return b.String()
}

func TestIngestExtendedFirstReportCountsEverything(t *testing.T) {
	s, _ := newExtServer(t, config{}, extStart)
	in := newExtInstall(1)
	body, sig := in.report(t, extStart, nil)
	if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", w.Code, w.Body)
	}
	st := s.store
	day := "2026-10-07"
	for _, c := range []struct {
		name, query string
		args        []any
		want        int
	}{
		{"daily_basic.reports", `SELECT reports FROM daily_basic WHERE day = ?`, []any{day}, 1},
		{"daily_basic.duplicates", `SELECT duplicates FROM daily_basic WHERE day = ?`, []any{day}, 0},
		{"daily_version", `SELECT reports FROM daily_version WHERE day = ? AND version = '1.0.0'`, []any{day}, 1},
		{"daily_ext.ext_reports", `SELECT ext_reports FROM daily_ext WHERE day = ?`, []any{day}, 1},
		{"daily_ext.active_installs", `SELECT active_installs FROM daily_ext WHERE day = ?`, []any{day}, 1},
		{"daily_ext.new_installs", `SELECT new_installs FROM daily_ext WHERE day = ?`, []any{day}, 1},
		{"daily_ext.lapsed_installs", `SELECT lapsed_installs FROM daily_ext WHERE day = ?`, []any{day}, 0},
		{"dim k8s", `SELECT installs FROM daily_dim WHERE day = ? AND dim = 'k8s' AND value = '1.31'`, []any{day}, 1},
		{"dim distro", `SELECT installs FROM daily_dim WHERE day = ? AND dim = 'distro' AND value = 'k3s'`, []any{day}, 1},
		{"dim arch", `SELECT installs FROM daily_dim WHERE day = ? AND dim = 'arch' AND value = 'amd64'`, []any{day}, 1},
		{"dim nodes", `SELECT installs FROM daily_dim WHERE day = ? AND dim = 'nodes' AND value = '1'`, []any{day}, 1},
		{"dim tunnel", `SELECT installs FROM daily_dim WHERE day = ? AND dim = 'tunnel' AND value = 'playit'`, []any{day}, 1},
		{"dim clusters", `SELECT installs FROM daily_dim WHERE day = ? AND dim = 'clusters' AND value = '1'`, []any{day}, 1},
		{"dim db", `SELECT installs FROM daily_dim WHERE day = ? AND dim = 'db' AND value = 'sqlite'`, []any{day}, 1},
		{"dim language", `SELECT installs FROM daily_dim WHERE day = ? AND dim = 'language' AND value = 'en'`, []any{day}, 1},
		{"dim feature backups", `SELECT installs FROM daily_dim WHERE day = ? AND dim = 'feature' AND value = 'backups'`, []any{day}, 1},
		{"no row for a feature that is off", `SELECT count(*) FROM daily_dim WHERE day = ? AND dim = 'feature' AND value = 'wakeOnConnect'`, []any{day}, 0},
		{"feature rows", `SELECT count(*) FROM daily_dim WHERE day = ? AND dim = 'feature'`, []any{day}, 1},
		{"game official installs", `SELECT installs FROM daily_game WHERE day = ? AND module = 'minecraft-java'`, []any{day}, 1},
		{"game official servers", `SELECT servers FROM daily_game WHERE day = ? AND module = 'minecraft-java'`, []any{day}, 2},
		{"game custom installs", `SELECT installs FROM daily_game WHERE day = ? AND module = 'custom'`, []any{day}, 1},
		{"game custom servers", `SELECT servers FROM daily_game WHERE day = ? AND module = 'custom'`, []any{day}, 1},
		{"activity rows", `SELECT count(*) FROM activity`, nil, 1},
	} {
		if got := countRows(t.Context(), t, st, c.query, c.args...); got != c.want {
			t.Errorf("%s = %d, want %d", c.name, got, c.want)
		}
	}
	if got := mustMeta(t, st, "reports_total"); got != "1" {
		t.Errorf("reports_total = %s, want 1", got)
	}
	// Only the transformed ID is stored, and the claim is the key fingerprint.
	if got := countRows(t.Context(), t, st, `SELECT count(*) FROM activity WHERE id_hmac = ?`, in.id); got != 0 {
		t.Errorf("the raw install ID is stored: %d rows", got)
	}
	var keyFP, firstSeen, lastSeen, lastSent, lastVersion string
	if err := st.db.QueryRowContext(context.Background(),
		`SELECT key_fp, first_seen, last_seen, last_sent_at, last_version FROM activity WHERE id_hmac = ?`,
		st.installHMAC(in.id)).Scan(&keyFP, &firstSeen, &lastSeen, &lastSent, &lastVersion); err != nil {
		t.Fatalf("read activity: %v", err)
	}
	priv, err := telemetryschema.DeriveKey(in.secret, in.id)
	if err != nil {
		t.Fatal(err)
	}
	if keyFP != telemetryschema.KeyFingerprint(telemetryschema.PublicKeyString(priv)) ||
		firstSeen != day || lastSeen != day || lastSent != "2026-10-07T12:00:00Z" || lastVersion != "1.0.0" {
		t.Errorf("activity = %s %s %s %s %s", keyFP, firstSeen, lastSeen, lastSent, lastVersion)
	}
	if got := extCounter(t, s, "gameplane_telemetry_extended_reports_total"); got != 1 {
		t.Errorf("extended_reports_total = %v, want 1", got)
	}
	if got := extCounter(t, s, "gameplane_telemetry_duplicates_total"); got != 0 {
		t.Errorf("duplicates_total = %v, want 0", got)
	}
	if got := counterValue(t, s, "gameplane_telemetry_reports_total", "version", "1.0.0"); got != 1 {
		t.Errorf("reports_total{version} = %v, want 1", got)
	}
}

// extCounter reads an unlabelled counter from the server's registry.
func extCounter(t *testing.T, s *server, name string) float64 {
	t.Helper()
	families, err := s.reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() == name && len(f.Metric) == 1 {
			return f.Metric[0].GetCounter().GetValue()
		}
	}
	t.Fatalf("metric %s not found", name)
	return 0
}

func TestIngestExtendedSameDayRepeatIsADuplicate(t *testing.T) {
	s, clock := newExtServer(t, config{}, extStart)
	in := newExtInstall(1)
	first, firstSig := in.report(t, extStart, nil)
	if w := extSend(t, s, first, firstSig); w.Code != http.StatusNoContent {
		t.Fatalf("first: %d", w.Code)
	}
	*clock = extStart.Add(2 * time.Hour)
	second, secondSig := in.report(t, *clock, func(r *telemetryschema.Report) { r.Servers, r.Version = 9, "2.0.0" })
	if w := extSend(t, s, second, secondSig); w.Code != http.StatusNoContent {
		t.Fatalf("repeat: %d, want 204", w.Code)
	}
	st := s.store
	day := "2026-10-07"
	if got := countRows(t.Context(), t, st, `SELECT duplicates FROM daily_basic WHERE day = ?`, day); got != 1 {
		t.Errorf("duplicates = %d, want 1", got)
	}
	// Nothing else changed: the repeat's version and servers were not counted.
	for _, c := range []struct {
		name, query string
		want        int
	}{
		{"daily_basic.reports", `SELECT reports FROM daily_basic`, 1},
		{"daily_basic.servers_sum", `SELECT servers_sum FROM daily_basic`, 3},
		{"daily_version rows", `SELECT count(*) FROM daily_version`, 1},
		{"daily_ext.ext_reports", `SELECT ext_reports FROM daily_ext`, 1},
		{"daily_ext.active_installs", `SELECT active_installs FROM daily_ext`, 1},
		{"daily_game installs", `SELECT installs FROM daily_game WHERE module = 'minecraft-java'`, 1},
		{"daily_dim k8s", `SELECT installs FROM daily_dim WHERE dim = 'k8s'`, 1},
		{"activity rows", `SELECT count(*) FROM activity`, 1},
	} {
		if got := countRows(t.Context(), t, st, c.query); got != c.want {
			t.Errorf("%s = %d, want %d", c.name, got, c.want)
		}
	}
	if got := mustMeta(t, st, "reports_total"); got != "1" {
		t.Errorf("reports_total = %s, want 1", got)
	}
	if got := extCounter(t, s, "gameplane_telemetry_duplicates_total"); got != 1 {
		t.Errorf("duplicates_total = %v, want 1", got)
	}
	if got := extCounter(t, s, "gameplane_telemetry_extended_reports_total"); got != 1 {
		t.Errorf("extended_reports_total = %v, want 1", got)
	}
}

func TestIngestExtendedReturningInstallOnALaterDay(t *testing.T) {
	s, clock := newExtServer(t, config{}, extStart)
	in := newExtInstall(1)
	body, sig := in.report(t, extStart, nil)
	if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
		t.Fatalf("day 1: %d", w.Code)
	}
	*clock = extStart.Add(24 * time.Hour)
	body, sig = in.report(t, *clock, func(r *telemetryschema.Report) { r.Version = "1.1.0" })
	if w := extSend(t, s, body, sig); w.Code != http.StatusNoContent {
		t.Fatalf("day 2: %d", w.Code)
	}
	st := s.store
	if got := countRows(t.Context(), t, st, `SELECT new_installs FROM daily_ext WHERE day = '2026-10-08'`); got != 0 {
		t.Errorf("new_installs on day 2 = %d, want 0", got)
	}
	if got := countRows(t.Context(), t, st, `SELECT active_installs FROM daily_ext WHERE day = '2026-10-08'`); got != 1 {
		t.Errorf("active_installs on day 2 = %d, want 1", got)
	}
	if got := countRows(t.Context(), t, st, `SELECT count(*) FROM activity`); got != 1 {
		t.Errorf("activity rows = %d, want 1", got)
	}
	var first, last, version string
	if err := st.db.QueryRowContext(context.Background(),
		`SELECT first_seen, last_seen, last_version FROM activity`).Scan(&first, &last, &version); err != nil {
		t.Fatal(err)
	}
	if first != "2026-10-07" || last != "2026-10-08" || version != "1.1.0" {
		t.Errorf("activity = %s %s %s, want 2026-10-07 2026-10-08 1.1.0", first, last, version)
	}
	if got := mustMeta(t, st, "reports_total"); got != "2" {
		t.Errorf("reports_total = %s, want 2", got)
	}
}

func TestIngestExtendedWithUnusableInstallIDIsCountedAsBasic(t *testing.T) {
	s, _ := newExtServer(t, config{}, extStart)
	in := newExtInstall(1)
	// An ID that is not a lowercase UUIDv4 makes the receiver drop ext, so no
	// signature is needed and the report is a basic one.
	body, _ := in.report(t, extStart, func(r *telemetryschema.Report) { r.Ext.InstallID = "NOT-A-UUID" })
	if w := extSend(t, s, body, ""); w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (%s)", w.Code, w.Body)
	}
	st := s.store
	if got := countRows(t.Context(), t, st, `SELECT reports FROM daily_basic`); got != 1 {
		t.Errorf("daily_basic.reports = %d, want 1", got)
	}
	for _, table := range []string{"daily_ext", "daily_dim", "daily_game", "activity"} {
		if got := countRows(t.Context(), t, st, "SELECT count(*) FROM "+table); got != 0 {
			t.Errorf("%s has %d rows, want 0", table, got)
		}
	}
}

func TestIngestExtendedStructuralErrorIs400(t *testing.T) {
	s, _ := newExtServer(t, config{}, extStart)
	in := newExtInstall(1)
	before := extDump(t, s.store)
	for name, mod := range map[string]func(*telemetryschema.Report){
		"schema 3":        func(r *telemetryschema.Report) { r.Ext.Schema = 3 },
		"no architecture": func(r *telemetryschema.Report) { r.Ext.Env.Arch = nil },
		"bad key":         func(r *telemetryschema.Report) { r.Ext.Key = "short" },
	} {
		body, sig := in.report(t, extStart, mod)
		if w := extSend(t, s, body, sig); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, w.Code)
		}
	}
	if after := extDump(t, s.store); after != before {
		t.Errorf("a rejected report changed the store:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestDecodePayloadStillRejectsExtendedReports(t *testing.T) {
	in := newExtInstall(1)
	body, _ := in.report(t, extStart, nil)
	if _, err := decodePayload([]byte(body)); err == nil {
		t.Fatal("decodePayload accepted a report with ext, want errInvalidPayload")
	}
}

func TestIngestExtendedCountsAgainstTheSourceLimit(t *testing.T) {
	s, _ := newExtServer(t, config{ingestSourceDailyLimit: 1}, extStart)
	a, aSig := newExtInstall(1).report(t, extStart, nil)
	b, bSig := newExtInstall(2).report(t, extStart, nil)
	if w := extSend(t, s, a, aSig); w.Code != http.StatusNoContent {
		t.Fatalf("first: %d", w.Code)
	}
	if w := extSend(t, s, b, bSig); w.Code != http.StatusTooManyRequests {
		t.Fatalf("second: %d, want 429", w.Code)
	}
	if got := counterValue(t, s, "gameplane_telemetry_rate_limited_total", "route", "ingest"); got != 1 {
		t.Errorf("rate_limited_total{ingest} = %v, want 1", got)
	}
	// The limited report left no activity record.
	if got := countRows(t.Context(), t, s.store, `SELECT count(*) FROM activity`); got != 1 {
		t.Errorf("activity rows = %d, want 1", got)
	}
}

func TestIngestExtendedStoreFailureIs500AndRollsBack(t *testing.T) {
	s, _ := newExtServer(t, config{}, extStart)
	if _, err := s.store.db.ExecContext(context.Background(), "DROP TABLE daily_game"); err != nil {
		t.Fatal(err)
	}
	body, sig := newExtInstall(1).report(t, extStart, nil)
	if w := extSend(t, s, body, sig); w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	for _, q := range []string{`SELECT count(*) FROM activity`, `SELECT count(*) FROM daily_basic`, `SELECT count(*) FROM daily_ext`} {
		if got := countRows(t.Context(), t, s.store, q); got != 0 {
			t.Errorf("%s = %d after a failed report, want 0 (rolled back)", q, got)
		}
	}
	if got := extCounter(t, s, "gameplane_telemetry_extended_reports_total"); got != 0 {
		t.Errorf("extended_reports_total = %v, want 0", got)
	}
}

func TestActivityTableHasExactlyTheSixColumns(t *testing.T) {
	st := openTestStore(t.Context(), t, config{})
	rows, err := st.db.QueryContext(context.Background(), `SELECT name FROM pragma_table_info('activity') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"id_hmac", "key_fp", "first_seen", "last_seen", "last_sent_at", "last_version"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("activity columns = %v, want exactly %v (FR-014, SC-012)", got, want)
	}
}
