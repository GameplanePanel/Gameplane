package telemetry

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// bareStore is a migrated store with no telemetry config row. Migrate seeds
// the fresh-install default; this helper removes it so some branches behave as
// before.
func bareStore(ctx context.Context, t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(ctx, "sqlite", "file:"+filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := store.DB.ExecContext(ctx, `DELETE FROM config WHERE key = 'telemetry'`); err != nil {
		t.Fatalf("delete telemetry: %v", err)
	}
	return store
}

func TestNew_DefaultsInterval(t *testing.T) {
	r := New(Config{Deps: Deps{Store: bareStore(context.Background(), t), Kube: telKube()}, Interval: 0})
	if r.interval != 24*time.Hour {
		t.Fatalf("interval = %v, want the 24h default for a non-positive interval", r.interval)
	}
	if r.client == nil || r.now == nil || r.rnd == nil {
		t.Fatal("New must fill the client, clock and random source defaults")
	}
}

func TestPollEvery(t *testing.T) {
	for _, tc := range []struct {
		interval time.Duration
		want     time.Duration
	}{
		{24 * time.Hour, 5 * time.Minute},
		{time.Hour, 5 * time.Minute},
		{20 * time.Minute, 5 * time.Minute},
		{time.Minute, 15 * time.Second},
		{time.Second, time.Second},
	} {
		r := New(Config{Interval: tc.interval, Deps: Deps{Store: bareStore(context.Background(), t)}})
		if got := r.pollEvery(); got != tc.want {
			t.Errorf("pollEvery(interval %v) = %v, want %v", tc.interval, got, tc.want)
		}
	}
}

func TestEnabled_Branches(t *testing.T) {
	ctx := context.Background()

	t.Run("absent config is disabled", func(t *testing.T) {
		c, err := ReadConsent(ctx, bareStore(ctx, t))
		if err != nil || c != (Consent{}) {
			t.Fatalf("consent = %+v, %v; want both tiers off for a missing telemetry config row", c, err)
		}
	})

	t.Run("malformed config is disabled", func(t *testing.T) {
		store := bareStore(ctx, t)
		if _, err := store.DB.ExecContext(ctx,
			`INSERT INTO config(key, value, updated_at) VALUES ('telemetry', '{bad', ?)`,
			"2026-01-01T00:00:00Z"); err != nil {
			t.Fatalf("seed: %v", err)
		}
		c, err := ReadConsent(ctx, store)
		if err != nil || c != (Consent{}) {
			t.Fatalf("consent = %+v, %v; want both tiers off for a malformed telemetry config", c, err)
		}
	})

	t.Run("extended is off while basic is off", func(t *testing.T) {
		if c := parseConsent(`{"sendMetrics":false,"extended":true}`, true); c != (Consent{}) {
			t.Fatalf("consent = %+v, want both off", c)
		}
	})

	t.Run("both tiers on", func(t *testing.T) {
		if c := parseConsent(`{"sendMetrics":true,"extended":true}`, true); c != (Consent{Basic: true, Extended: true}) {
			t.Fatalf("consent = %+v, want both on", c)
		}
	})

	t.Run("a legacy value without extended is basic only", func(t *testing.T) {
		if c := parseConsent(`{"sendMetrics":true}`, true); c != (Consent{Basic: true}) {
			t.Fatalf("consent = %+v, want basic only", c)
		}
	})
}

func TestCount_UnknownKind(t *testing.T) {
	deps := Deps{Store: bareStore(context.Background(), t), Kube: telKube()}
	if items := listItems(context.Background(), deps, "bogus"); len(items) != 0 {
		t.Fatalf("listItems(bogus) = %d items, want 0", len(items))
	}
}

func TestReportOnce_EndpointErrorPropagates(t *testing.T) {
	srv, got := newSink(t)
	got.setRespond(func(sentReport) int { return http.StatusInternalServerError })
	r := testReporter(telStore(t, true), telKube(), srv.URL, newFakeClock())
	if err := sendNow(t, r); err == nil {
		t.Fatal("want an error when the telemetry endpoint returns 500")
	}
	if st := mustState(t, r.store); st.LastOutcome != "failed" || st.ConsecutiveFailures != 1 {
		t.Fatalf("state = %+v, want one recorded failure", st)
	}
}

func TestBackoff_DoublesAndCapsAtInterval(t *testing.T) {
	r := New(Config{Interval: 24 * time.Hour, Deps: Deps{Store: bareStore(context.Background(), t)}})
	for failures, want := range map[int]time.Duration{
		0: time.Hour, 1: 2 * time.Hour, 2: 4 * time.Hour, 3: 8 * time.Hour,
		4: 16 * time.Hour, 5: 24 * time.Hour, 40: 24 * time.Hour,
	} {
		if got := r.backoff(failures); got != want {
			t.Errorf("backoff(%d) = %v, want %v", failures, got, want)
		}
	}
	short := New(Config{Interval: time.Minute, Deps: Deps{Store: bareStore(context.Background(), t)}})
	if got := short.backoff(0); got != time.Minute {
		t.Errorf("backoff(0) with a 1m interval = %v, want the interval cap", got)
	}
}

func TestExtWithheld_Branches(t *testing.T) {
	now := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	r := New(Config{
		Dest: Destination{Kind: KindCustom, URL: "https://t.example/ingest"},
		Deps: Deps{Store: bareStore(context.Background(), t)},
	})
	active := db.TelemetryState{
		ExtUnsupportedEndpoint: "https://t.example/ingest",
		ExtUnsupportedUntil:    now.Add(time.Hour).Format(time.RFC3339),
	}
	if !r.extWithheld(active, now) {
		t.Error("an unexpired marker for this endpoint must withhold ext")
	}
	expired := active
	expired.ExtUnsupportedUntil = now.Add(-time.Second).Format(time.RFC3339)
	if r.extWithheld(expired, now) {
		t.Error("an expired marker must not withhold ext")
	}
	other := active
	other.ExtUnsupportedEndpoint = "https://other.example/ingest"
	if r.extWithheld(other, now) {
		t.Error("a marker for another endpoint must not withhold ext")
	}
	garbled := active
	garbled.ExtUnsupportedUntil = "not a time"
	if r.extWithheld(garbled, now) {
		t.Error("an unparsable marker must not withhold ext")
	}
}
