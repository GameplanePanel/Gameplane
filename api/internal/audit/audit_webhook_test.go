package audit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/auth"

	dto "github.com/prometheus/client_model/go"
)

// counterValue reads the current value of the shared webhook counter for a
// result label. The counter is process-global, so tests assert on a measured
// before/after delta rather than an absolute. (We deliberately avoid
// prometheus/testutil here: pulling it in drags a transitive dep the CI build —
// which never runs `go mod tidy` — can't resolve. client_model is already an
// indirect dep in go.sum, so reading the metric directly is safe.)
func counterValue(t *testing.T, result string) float64 {
	t.Helper()
	var m dto.Metric
	if err := webhookEvents.WithLabelValues(result).Write(&m); err != nil {
		t.Fatalf("read counter %q: %v", result, err)
	}
	return m.GetCounter().GetValue()
}

type captured struct {
	method      string
	contentType string
	auth        string
	body        []byte
}

func TestWebhookSink_PostsEvent(t *testing.T) {
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- captured{
			method:      r.Method,
			contentType: r.Header.Get("Content-Type"),
			auth:        r.Header.Get("Authorization"),
			body:        b,
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	before := counterValue(t, "sent")
	s := NewWebhookSink(srv.URL, "Bearer t0ken")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	s.Enqueue(Event{
		TS: "2026-06-30T00:00:00Z", Actor: "admin", Method: "POST",
		Path: "/api/v1/servers", Target: "alpha", Status: 201, IP: "10.0.0.5",
	})

	select {
	case c := <-got:
		if c.method != http.MethodPost {
			t.Errorf("method = %s, want POST", c.method)
		}
		if !strings.Contains(c.contentType, "application/json") {
			t.Errorf("content-type = %q", c.contentType)
		}
		if c.auth != "Bearer t0ken" {
			t.Errorf("authorization = %q, want %q", c.auth, "Bearer t0ken")
		}
		var p map[string]any
		if err := json.Unmarshal(c.body, &p); err != nil {
			t.Fatalf("decode body: %v (%s)", err, c.body)
		}
		if p["actor"] != "admin" || p["path"] != "/api/v1/servers" || p["target"] != "alpha" {
			t.Errorf("payload = %v", p)
		}
		if _, hasID := p["id"]; hasID {
			t.Errorf("payload must not carry the db id: %v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not received within deadline")
	}

	// The worker increments "sent" only after the POST returns, so poll briefly.
	deadline := time.Now().Add(time.Second)
	for counterValue(t, "sent") <= before {
		if time.Now().After(deadline) {
			t.Fatal("sent counter did not advance")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The middleware must fan an audited request out to the webhook in addition to
// (not instead of) the database row.
func TestWebhookSink_MiddlewareFanout(t *testing.T) {
	got := make(chan map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		_ = json.NewDecoder(r.Body).Decode(&p)
		got <- p
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	store := newStore(t)
	sink := NewWebhookSink(srv.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sink.Start(ctx)
	a := New(store, WithWebhookSink(sink))

	h := Middleware(a)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	req := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/servers?name=beta", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{Username: "root"}))
	h.ServeHTTP(httptest.NewRecorder(), req)

	select {
	case p := <-got:
		if p["actor"] != "root" || p["path"] != "/api/v1/servers" || p["target"] != "beta" {
			t.Errorf("payload = %v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("middleware did not fan out to the webhook")
	}
	if n := countEvents(t, store); n != 1 {
		t.Errorf("db events = %d, want 1 (webhook mirrors, does not replace)", n)
	}
}

// A full buffer must drop without ever blocking the caller (the request path).
func TestWebhookSink_DropsWhenBufferFull(t *testing.T) {
	s := NewWebhookSink("http://127.0.0.1:9", "") // worker NOT started → ch never drains
	for i := 0; i < webhookBuffer; i++ {
		s.Enqueue(Event{Actor: "x"})
	}
	before := counterValue(t, "dropped")
	const overflow = 5
	done := make(chan struct{})
	go func() {
		for i := 0; i < overflow; i++ {
			s.Enqueue(Event{Actor: "x"}) // must not block on a full buffer
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Enqueue blocked on a full buffer")
	}
	if delta := counterValue(t, "dropped") - before; delta != overflow {
		t.Errorf("dropped delta = %v, want %d", delta, overflow)
	}
}

func TestWebhookSink_CountsConnectionFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // connections to url are now refused

	s := NewWebhookSink(url, "")
	before := counterValue(t, "failed")
	s.post(t.Context(), Event{Actor: "x", TS: "2026-06-30T00:00:00Z"})
	if delta := counterValue(t, "failed") - before; delta != 1 {
		t.Errorf("failed delta = %v, want 1", delta)
	}
}

func TestWebhookSink_CountsNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := NewWebhookSink(srv.URL, "")
	before := counterValue(t, "failed")
	s.post(t.Context(), Event{Actor: "x"})
	if delta := counterValue(t, "failed") - before; delta != 1 {
		t.Errorf("failed delta = %v, want 1", delta)
	}
}

// TestWebhookSink_IncludesReason — the reason field must survive the webhook
// serialization path (Event.Reason -> webhookPayload.Reason -> JSON body), not
// just the DB write.
func TestWebhookSink_IncludesReason(t *testing.T) {
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- captured{body: b}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := NewWebhookSink(srv.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	s.Enqueue(Event{
		TS: "2026-06-30T00:00:00Z", Actor: "admin", Method: "POST",
		Path: "/api/v1/servers", Target: "alpha", Status: 403, Reason: "rbac: missing role",
	})

	select {
	case c := <-got:
		var p map[string]any
		if err := json.Unmarshal(c.body, &p); err != nil {
			t.Fatalf("decode body: %v (%s)", err, c.body)
		}
		if p["reason"] != "rbac: missing role" {
			t.Errorf("payload reason = %v, want %q", p["reason"], "rbac: missing role")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not received within deadline")
	}
}

// TestWebhookSink_OmitsEmptyReason — when Reason is unset, the JSON body must
// not carry an empty "reason" key (the field is `omitempty`).
func TestWebhookSink_OmitsEmptyReason(t *testing.T) {
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- captured{body: b}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := NewWebhookSink(srv.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Start(ctx)

	s.Enqueue(Event{
		TS: "2026-06-30T00:00:00Z", Actor: "admin", Method: "POST",
		Path: "/api/v1/servers", Status: 201,
	})

	select {
	case c := <-got:
		var p map[string]any
		if err := json.Unmarshal(c.body, &p); err != nil {
			t.Fatalf("decode body: %v (%s)", err, c.body)
		}
		if _, has := p["reason"]; has {
			t.Errorf("payload must not carry an empty reason key: %v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook not received within deadline")
	}
}

// drain ships whatever is already buffered on shutdown.
func TestWebhookSink_DrainDeliversBuffered(t *testing.T) {
	got := make(chan struct{}, 3)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		got <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewWebhookSink(srv.URL, "")
	for i := 0; i < 3; i++ {
		s.Enqueue(Event{Actor: "x"})
	}
	s.drain(t.Context())
	if len(got) != 3 {
		t.Errorf("delivered %d events on drain, want 3", len(got))
	}
}
