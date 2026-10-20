package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// telStore returns a migrated file-backed store whose admin made an explicit
// choice: basic is sendMetrics and extended is off. Because the source is
// "admin", the report gate is open without the first-login notice. It
// replaces the fresh-install default Migrate seeds.
func telStore(t *testing.T, sendMetrics bool) *db.Store {
	t.Helper()
	return telStoreExt(t, sendMetrics, false)
}

// telStoreExt is telStore with an explicit extended choice.
func telStoreExt(t *testing.T, sendMetrics, extended bool) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), "sqlite", "file:"+filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	val, _ := json.Marshal(map[string]bool{"sendMetrics": sendMetrics, "extended": extended})
	if _, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO config(key, value, updated_at) VALUES ('telemetry', ?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		string(val), "2026-01-01T00:00:00Z",
	); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if _, err := store.DB.ExecContext(context.Background(),
		`UPDATE telemetry_state SET consent_source = 'admin'`); err != nil {
		t.Fatalf("seed consent source: %v", err)
	}
	if extended {
		if err := store.SetInstallID(context.Background(), testInstallID); err != nil {
			t.Fatalf("seed install id: %v", err)
		}
	}
	return store
}

// freshStore is a migrated file-backed store exactly as a fresh install
// leaves it: consent_source "default", basic and extended on, no notice yet.
func freshStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), "sqlite", "file:"+filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func telKube(objs ...runtime.Object) *kube.Client {
	gvkr := map[schema.GroupVersionResource]string{
		kube.GVRs["servers"]:   "GameServerList",
		kube.GVRs["templates"]: "GameTemplateList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), gvkr, objs...)
	return &kube.Client{Dynamic: dyn}
}

func unstr(kind, name, ns string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetAPIVersion("gameplane.local/v1alpha1")
	o.SetKind(kind)
	o.SetName(name)
	if ns != "" {
		o.SetNamespace(ns)
	}
	return o
}

// fakeClock is a settable clock shared by a Reporter and its test.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// sentReport is one POST the test server received.
type sentReport struct {
	body        []byte
	auth        string
	signature   string
	contentType string
}

// hasExt reports whether the body carries the extended part.
func (s sentReport) hasExt() bool {
	rep, _, err := telemetryschema.Decode(s.body)
	return err == nil && rep.Ext != nil
}

// sink is a test telemetry endpoint that records every POST. respond, when
// set, picks the status for a request (default 200).
type sink struct {
	mu      sync.Mutex
	reqs    []sentReport
	respond func(sentReport) int
}

func newSink(t *testing.T) (*httptest.Server, *sink) {
	t.Helper()
	s := &sink{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The reporter asks for a proof-of-work challenge before each POST
			// (spec 022 T105); this provider offers none.
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		rec := sentReport{
			body:        body,
			auth:        r.Header.Get("Authorization"),
			signature:   r.Header.Get(telemetryschema.SignatureHeader),
			contentType: r.Header.Get("Content-Type"),
		}
		s.mu.Lock()
		s.reqs = append(s.reqs, rec)
		respond := s.respond
		s.mu.Unlock()
		status := http.StatusOK
		if respond != nil {
			status = respond(rec)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, s
}

func (s *sink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs)
}

func (s *sink) at(i int) sentReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reqs[i]
}

func (s *sink) setRespond(f func(sentReport) int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.respond = f
}

// testReporter builds a Reporter on a custom destination at url with a 24h
// interval, the fake clock and zero jitter (every first slot is due at once).
func testReporter(store *db.Store, k *kube.Client, url string, clk *fakeClock) *Reporter {
	return New(Config{
		Dest:     Destination{Kind: KindCustom, URL: url, Host: "test"},
		Interval: 24 * time.Hour,
		Deps:     Deps{Kube: k, Store: store, Version: "v1.2.3"},
		Now:      clk.Now,
		Rand:     func(int64) int64 { return 0 },
	})
}

// sendNow opens the schedule (the first Tick) and sends the report that is
// then due (the second Tick), returning the second Tick's error.
func sendNow(t *testing.T, r *Reporter) error {
	t.Helper()
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("opening tick: %v", err)
	}
	return r.Tick(t.Context())
}

func mustState(t *testing.T, store *db.Store) db.TelemetryState {
	t.Helper()
	st, err := store.GetTelemetryState(t.Context())
	if err != nil {
		t.Fatalf("get state: %v", err)
	}
	return st
}

func mustParse(t *testing.T, ts string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("parse %q: %v", ts, err)
	}
	return v
}

func TestReportOnce_EnabledPostsAnonymousCounts(t *testing.T) {
	srv, got := newSink(t)
	k := telKube(
		unstr("GameServer", "a", "gameplane-games"),
		unstr("GameServer", "b", "gameplane-games"),
		unstr("GameTemplate", "minecraft", ""),
	)
	r := testReporter(telStore(t, true), k, srv.URL, newFakeClock())
	if err := sendNow(t, r); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got.count() != 1 {
		t.Fatalf("posts = %d, want 1", got.count())
	}
	rec := got.at(0)
	rep, _, err := telemetryschema.Decode(rec.body)
	if err != nil {
		t.Fatalf("decode body %s: %v", rec.body, err)
	}
	if rep.Version != "v1.2.3" || rep.Servers != 2 || rep.Templates != 1 || rep.Ext != nil {
		t.Fatalf("report = %+v, want a basic {v1.2.3 2 1}", rep)
	}
	if rec.contentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", rec.contentType)
	}
	if rec.signature != "" {
		t.Fatalf("a basic report must not carry the signature header, got %q", rec.signature)
	}
	st := mustState(t, r.store)
	if st.LastOutcome != "ok" || st.LastSuccessAt == "" || st.ConsecutiveFailures != 0 {
		t.Fatalf("state after success = %+v", st)
	}
}

func TestReportOnce_SendsAuthHeader(t *testing.T) {
	srv, got := newSink(t)
	r := testReporter(telStore(t, true), telKube(), srv.URL, newFakeClock())
	r.auth = "Bearer ingest-tok"
	if err := sendNow(t, r); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got.count() != 1 || got.at(0).auth != "Bearer ingest-tok" {
		t.Fatalf("posts = %d, Authorization = %q; want 1 post with the configured token", got.count(), got.at(0).auth)
	}
}

func TestReportOnce_DisabledSkipsPost(t *testing.T) {
	srv, got := newSink(t)
	clk := newFakeClock()
	r := testReporter(telStore(t, false), telKube(), srv.URL, clk)
	for range 3 {
		if err := r.Tick(t.Context()); err != nil {
			t.Fatalf("tick: %v", err)
		}
		clk.Advance(48 * time.Hour)
	}
	if got.count() != 0 {
		t.Fatal("telemetry must not POST when sendMetrics is off")
	}
	if st := mustState(t, r.store); st.NextDueAt != "" {
		t.Fatalf("next_due_at = %q, want it to stay NULL while the gate is closed", st.NextDueAt)
	}
}

func TestReporter_NoPostWhileDefaultAndNoticeUnseen(t *testing.T) {
	srv, got := newSink(t)
	clk := newFakeClock()
	store := freshStore(t) // consent_source default, sendMetrics and extended on
	r := testReporter(store, telKube(), srv.URL, clk)
	for range 5 {
		if err := r.Tick(t.Context()); err != nil {
			t.Fatalf("tick: %v", err)
		}
		clk.Advance(30 * time.Hour)
	}
	if got.count() != 0 {
		t.Fatalf("posts = %d, want 0 before any admin was shown the notice (FR-004)", got.count())
	}
	st := mustState(t, store)
	if st.NextDueAt != "" || st.InstallID != "" {
		t.Fatalf("state = %+v, want no slot and no install id while the gate is closed", st)
	}
}

func TestReporter_FirstPostWithin15MinutesOfNoticeShown(t *testing.T) {
	srv, got := newSink(t)
	clk := newFakeClock()
	store := freshStore(t)
	k := collectKubeClients(t, collectOpts{})
	r := testReporter(store, k, srv.URL, clk)
	r.rnd = func(n int64) int64 { return n - 1 } // the latest slot the window allows

	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("tick before the notice: %v", err)
	}
	shown := clk.Now()
	if err := store.MarkNoticeShown(t.Context(), shown.Format(time.RFC3339)); err != nil {
		t.Fatalf("mark notice shown: %v", err)
	}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("tick opening the gate: %v", err)
	}
	due := mustParse(t, mustState(t, store).NextDueAt)
	if wait := due.Sub(shown); wait < 0 || wait > 15*time.Minute {
		t.Fatalf("first slot is %v after the notice, want within 15m", wait)
	}
	clk.Advance(14 * time.Minute)
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("early tick: %v", err)
	}
	if got.count() != 0 {
		t.Fatalf("posts = %d before the slot, want 0", got.count())
	}
	clk.Advance(2 * time.Minute)
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("due tick: %v", err)
	}
	if got.count() != 1 {
		t.Fatalf("posts = %d at the slot, want 1", got.count())
	}

	// A fresh install reports basic plus extended, signed with the key the
	// report names, under the install ID the reporter created.
	rec := got.at(0)
	rep, _, err := telemetryschema.Decode(rec.body)
	if err != nil || rep.Ext == nil {
		t.Fatalf("decode %s: ext=%v err=%v, want an extended report", rec.body, rep.Ext, err)
	}
	if err := telemetryschema.Verify(rec.signature, rep.Ext.Key, rec.body); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}
	if st := mustState(t, store); st.InstallID == "" || st.InstallID != rep.Ext.InstallID {
		t.Fatalf("stored install id %q, report carries %q", st.InstallID, rep.Ext.InstallID)
	}
}

func TestReporter_ExtendedOffSendsBasicOnly(t *testing.T) {
	srv, got := newSink(t)
	r := testReporter(telStoreExt(t, true, false), collectKubeClients(t, collectOpts{}), srv.URL, newFakeClock())
	if err := sendNow(t, r); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got.count() != 1 || got.at(0).hasExt() || got.at(0).signature != "" {
		t.Fatalf("want one basic report without a signature, got %d posts", got.count())
	}
}

func TestReporter_SpacingAtLeastInterval(t *testing.T) {
	srv, got := newSink(t)
	clk := newFakeClock()
	r := testReporter(telStore(t, true), telKube(), srv.URL, clk)
	if err := sendNow(t, r); err != nil {
		t.Fatalf("first report: %v", err)
	}
	sentAt := clk.Now()
	if due := mustParse(t, mustState(t, r.store).NextDueAt); due.Sub(sentAt) < 24*time.Hour {
		t.Fatalf("next slot is %v after the send, want at least the 24h interval", due.Sub(sentAt))
	}
	clk.Advance(24*time.Hour - time.Second)
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got.count() != 1 {
		t.Fatalf("posts = %d just under one interval later, want still 1", got.count())
	}
	clk.Advance(time.Second)
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if got.count() != 2 {
		t.Fatalf("posts = %d one interval later, want 2", got.count())
	}
}

func TestReporter_RestartDoesNotSendEarly(t *testing.T) {
	srv, got := newSink(t)
	clk := newFakeClock()
	store := telStore(t, true)
	if err := sendNow(t, testReporter(store, telKube(), srv.URL, clk)); err != nil {
		t.Fatalf("first report: %v", err)
	}
	for _, after := range []time.Duration{time.Minute, 6 * time.Hour} {
		clk.Advance(after)
		restarted := testReporter(store, telKube(), srv.URL, clk) // a new process, same database
		if err := restarted.Tick(t.Context()); err != nil {
			t.Fatalf("tick after restart: %v", err)
		}
	}
	if got.count() != 1 {
		t.Fatalf("posts = %d after restarts inside the interval, want 1", got.count())
	}
	clk.Advance(24 * time.Hour)
	if err := testReporter(store, telKube(), srv.URL, clk).Tick(t.Context()); err != nil {
		t.Fatalf("tick after the interval: %v", err)
	}
	if got.count() != 2 {
		t.Fatalf("posts = %d once the interval passed, want 2", got.count())
	}
}

func TestReporter_FallsBackToBasicOn400ThenWaitsSevenDays(t *testing.T) {
	srv, got := newSink(t)
	got.setRespond(func(rec sentReport) int {
		if rec.hasExt() {
			return http.StatusBadRequest // an old receiver rejects the unknown key
		}
		return http.StatusOK
	})
	clk := newFakeClock()
	store := telStoreExt(t, true, true)
	r := testReporter(store, collectKubeClients(t, collectOpts{}), srv.URL, clk)
	start := clk.Now()

	// Day 0: the extended report is refused, the basic one accepted.
	if err := sendNow(t, r); err != nil {
		t.Fatalf("day 0: %v", err)
	}
	if got.count() != 2 || !got.at(0).hasExt() || got.at(0).signature == "" {
		t.Fatalf("day 0: want an extended signed POST first, got %d posts", got.count())
	}
	if got.at(1).hasExt() || got.at(1).signature != "" {
		t.Fatal("day 0: the retry must be basic only and unsigned")
	}
	st := mustState(t, store)
	if st.LastOutcome != "ok" {
		t.Fatalf("outcome = %q, want ok (the basic report was accepted)", st.LastOutcome)
	}
	if want := start.Add(7 * 24 * time.Hour).Format(time.RFC3339); st.ExtUnsupportedUntil != want || st.ExtUnsupportedEndpoint != srv.URL {
		t.Fatalf("fallback marker = %q for %q, want %q for %q", st.ExtUnsupportedUntil, st.ExtUnsupportedEndpoint, want, srv.URL)
	}

	// Days 1 to 6: one basic POST per day, no extended attempt.
	for day := 1; day <= 6; day++ {
		clk.Advance(24 * time.Hour)
		before := got.count()
		if err := r.Tick(t.Context()); err != nil {
			t.Fatalf("day %d: %v", day, err)
		}
		if got.count() != before+1 || got.at(before).hasExt() {
			t.Fatalf("day %d: want exactly one basic POST", day)
		}
	}

	// Day 7: the marker has expired, so the extended part is tried again.
	clk.Advance(24 * time.Hour)
	before := got.count()
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("day 7: %v", err)
	}
	if got.count() != before+2 || !got.at(before).hasExt() || got.at(before+1).hasExt() {
		t.Fatalf("day 7: want an extended attempt then a basic retry, got %d new posts", got.count()-before)
	}
}

func TestReporter_FallbackMarkerIsPerEndpoint(t *testing.T) {
	oldSrv, oldGot := newSink(t)
	oldGot.setRespond(func(rec sentReport) int {
		if rec.hasExt() {
			return http.StatusBadRequest
		}
		return http.StatusOK
	})
	newSrv, newGot := newSink(t)
	clk := newFakeClock()
	store := telStoreExt(t, true, true)
	k := collectKubeClients(t, collectOpts{})
	if err := sendNow(t, testReporter(store, k, oldSrv.URL, clk)); err != nil {
		t.Fatalf("old endpoint: %v", err)
	}

	// The operator points the install at another endpoint: the marker for the
	// old one doesn't apply, so the extended part goes out again.
	clk.Advance(24 * time.Hour)
	if err := testReporter(store, k, newSrv.URL, clk).Tick(t.Context()); err != nil {
		t.Fatalf("new endpoint: %v", err)
	}
	if newGot.count() != 1 || !newGot.at(0).hasExt() {
		t.Fatalf("new endpoint got %d posts, want one extended report", newGot.count())
	}
}

func TestReporter_BackoffGrowsAndCaps(t *testing.T) {
	srv, got := newSink(t)
	got.setRespond(func(sentReport) int { return http.StatusInternalServerError })
	clk := newFakeClock()
	store := telStore(t, true)
	r := testReporter(store, telKube(), srv.URL, clk)
	if err := r.Tick(t.Context()); err != nil { // opens the first slot
		t.Fatalf("opening tick: %v", err)
	}

	want := []time.Duration{time.Hour, 2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	for i, wait := range want {
		if err := r.Tick(t.Context()); err == nil {
			t.Fatalf("failure %d: want the send error from Tick", i+1)
		}
		st := mustState(t, store)
		if st.LastOutcome != "failed" || st.ConsecutiveFailures != i+1 {
			t.Fatalf("failure %d: state = %+v", i+1, st)
		}
		if backoff := mustParse(t, st.NextDueAt).Sub(clk.Now()); backoff != wait {
			t.Fatalf("failure %d: backoff = %v, want %v", i+1, backoff, wait)
		}
		// Nothing is sent before the backed-off slot, and nothing is queued.
		sent := got.count()
		clk.Advance(wait - time.Second)
		if err := r.Tick(t.Context()); err != nil {
			t.Fatalf("failure %d: early tick: %v", i+1, err)
		}
		if got.count() != sent {
			t.Fatalf("failure %d: sent before the backed-off slot", i+1)
		}
		clk.Advance(time.Second)
	}

	// Recovery resets the counter and schedules a full interval ahead.
	got.setRespond(nil)
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("recovery tick: %v", err)
	}
	st := mustState(t, store)
	if st.LastOutcome != "ok" || st.ConsecutiveFailures != 0 {
		t.Fatalf("after recovery: %+v", st)
	}
	if next := mustParse(t, st.NextDueAt).Sub(clk.Now()); next < 24*time.Hour {
		t.Fatalf("next slot after recovery is %v away, want at least 24h", next)
	}
}

func TestReporter_TwoReportersShareOneSlot(t *testing.T) {
	srv, got := newSink(t)
	clk := newFakeClock()
	store := telStore(t, true)
	a := testReporter(store, telKube(), srv.URL, clk)
	b := testReporter(store, telKube(), srv.URL, clk)
	if err := a.Tick(t.Context()); err != nil { // opens the slot, due now
		t.Fatalf("opening tick: %v", err)
	}

	var wg sync.WaitGroup
	for _, r := range []*Reporter{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Tick(t.Context()); err != nil {
				t.Errorf("tick: %v", err)
			}
		}()
	}
	wg.Wait()
	if got.count() != 1 {
		t.Fatalf("posts = %d from two reporters on one database, want exactly 1", got.count())
	}
}

func TestReporter_ClaimLostMeansNoSend(t *testing.T) {
	srv, got := newSink(t)
	clk := newFakeClock()
	store := telStore(t, true)
	r := testReporter(store, telKube(), srv.URL, clk)
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("opening tick: %v", err)
	}
	st := mustState(t, store)
	// Another replica claims the slot between this one's read and its claim.
	if _, err := store.ClaimTelemetryDue(t.Context(), st.NextDueAt, "2026-10-07T09:00:00Z", "2026-10-06T09:00:00Z"); err != nil {
		t.Fatalf("rival claim: %v", err)
	}
	if err := r.attempt(t.Context(), st, Consent{Basic: true}, clk.Now()); err != nil {
		t.Fatalf("attempt: %v", err)
	}
	if got.count() != 0 {
		t.Fatalf("posts = %d after losing the claim, want 0", got.count())
	}
}

func TestReporter_GateClosingClearsTheSlot(t *testing.T) {
	srv, got := newSink(t)
	clk := newFakeClock()
	store := telStore(t, true)
	r := testReporter(store, telKube(), srv.URL, clk)
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("opening tick: %v", err)
	}
	if mustState(t, store).NextDueAt == "" {
		t.Fatal("the first tick should have opened the schedule")
	}
	if _, err := store.DB.ExecContext(t.Context(),
		`UPDATE config SET value = '{"sendMetrics":false,"extended":false}' WHERE key = 'telemetry'`); err != nil {
		t.Fatalf("turn off: %v", err)
	}
	if err := r.Tick(t.Context()); err != nil {
		t.Fatalf("closing tick: %v", err)
	}
	if got.count() != 0 || mustState(t, store).NextDueAt != "" {
		t.Fatalf("posts = %d, slot = %q; want none and a cleared slot", got.count(), mustState(t, store).NextDueAt)
	}
}

func TestReporter_NoDestinationNeverSends(t *testing.T) {
	srv, got := newSink(t)
	for _, kind := range []string{KindNone, KindDisabled} {
		r := testReporter(telStore(t, true), telKube(), srv.URL, newFakeClock())
		r.dest = Destination{Kind: kind}
		if err := sendNow(t, r); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		r.Run(t.Context()) // returns at once, without a ticker
	}
	if got.count() != 0 {
		t.Fatalf("posts = %d with no destination, want 0", got.count())
	}
}

func TestReporter_RunTicksThenStopsOnCancel(t *testing.T) {
	srv, got := newSink(t)
	store := telStore(t, true)
	r := testReporter(store, telKube(), srv.URL, newFakeClock())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	// Run's first tick opens the schedule without waiting for the poll timer.
	deadline := time.Now().Add(5 * time.Second)
	for mustState(t, store).NextDueAt == "" {
		if time.Now().After(deadline) {
			t.Fatal("Run did not evaluate the schedule on start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	if got.count() != 0 {
		t.Fatalf("posts = %d, want 0 (the opening tick only schedules)", got.count())
	}
}

func TestReporter_SendFailuresAreRecorded(t *testing.T) {
	cases := map[string]string{
		"unreachable": "http://127.0.0.1:1/ingest",
		"bad url":     "http://%zz",
	}
	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			store := telStore(t, true)
			r := testReporter(store, telKube(), url, newFakeClock())
			if err := sendNow(t, r); err == nil {
				t.Fatal("want an error")
			}
			if st := mustState(t, store); st.LastOutcome != "failed" || st.ConsecutiveFailures != 1 {
				t.Fatalf("state = %+v, want a recorded failure", st)
			}
		})
	}
}

func TestReporter_CollectFailureIsRecorded(t *testing.T) {
	srv, got := newSink(t)
	store := telStoreExt(t, true, true)
	if _, err := store.DB.ExecContext(t.Context(), `UPDATE telemetry_state SET signing_secret = '!!not base64!!'`); err != nil {
		t.Fatalf("corrupt secret: %v", err)
	}
	r := testReporter(store, collectKubeClients(t, collectOpts{}), srv.URL, newFakeClock())
	if err := sendNow(t, r); err == nil {
		t.Fatal("want the collection error")
	}
	if got.count() != 0 {
		t.Fatalf("posts = %d, want none when the report can't be built", got.count())
	}
	if st := mustState(t, store); st.LastOutcome != "failed" {
		t.Fatalf("outcome = %q, want failed", st.LastOutcome)
	}
}

func TestReporter_TickErrorsOnClosedStore(t *testing.T) {
	store := telStore(t, true)
	r := testReporter(store, telKube(), "http://127.0.0.1:1", newFakeClock())
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := r.Tick(t.Context()); err == nil {
		t.Fatal("want an error from a closed database")
	}
}
