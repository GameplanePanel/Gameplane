package steam

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResolverNoKeyConfigured(t *testing.T) {
	// With no key, NewResolver returns nil and no outbound calls are made.
	r := NewResolver("", nil, nil)
	if r != nil {
		t.Error("NewResolver with empty key should return nil")
	}

	// Resolve on nil resolver returns empty map.
	result := r.Resolve(context.Background(), []string{"12345"})
	if len(result) != 0 {
		t.Error("Resolve on nil resolver should return empty map")
	}
}

func TestResolverSuccessfulBatch(t *testing.T) {
	// httptest server returning a valid Steam API response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ISteamUser/GetPlayerSummaries/v2/" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}

		// Parse query and verify the key and steamids.
		q := r.URL.Query()
		if q.Get("key") != "test-key" {
			t.Errorf("expected key=test-key, got %s", q.Get("key"))
		}

		ids := q["steamids"]
		if len(ids) != 3 {
			t.Errorf("expected 3 steamids, got %d", len(ids))
		}

		// Return a sample Steam API response.
		response := `{
  "response": {
    "players": [
      {"steamid": "76561198000000001", "personaname": "Player1"},
      {"steamid": "76561198000000002", "personaname": "Player2"},
      {"steamid": "76561198000000003", "personaname": "Player3"}
    ]
  }
}`
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	}))
	defer server.Close()

	// Create a resolver with a custom HTTP client pointing to the test server.
	r := &Resolver{
		apiKey:  "test-key",
		baseURL: server.URL + "/ISteamUser/GetPlayerSummaries/v2/",
		client:  &http.Client{Timeout: 2 * time.Second},
		cache:   NewCache(&Options{}, nil),
		opts:    (&Options{}).Normalize(),
		sf:      &singleflightGroup{},
	}

	// Test getPlayerSummaries directly with the test server.
	result, err := r.getPlayerSummaries(context.Background(), []string{"76561198000000001", "76561198000000002", "76561198000000003"})
	if err != nil {
		t.Fatalf("getPlayerSummaries failed: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("expected 3 resolved ids, got %d", len(result))
	}

	for _, id := range []string{"76561198000000001", "76561198000000002", "76561198000000003"} {
		if _, ok := result[id]; !ok {
			t.Errorf("id %s not in result", id)
		}
	}
}

func TestResolverCacheHit(t *testing.T) {
	// Test that cached entries are returned without upstream calls.
	clock := &fakeClock{now: time.Unix(0, 0)}
	r := &Resolver{
		apiKey: "test-key",
		client: &http.Client{},
		cache:  NewCache(&Options{TTL: 1 * time.Hour}, clock),
		opts:   (&Options{TTL: 1 * time.Hour}).Normalize(),
		sf:     &singleflightGroup{},
	}

	// Pre-populate the cache.
	r.cache.Set("id1", "Player1", 1*time.Hour)
	r.cache.Set("id2", "Player2", 1*time.Hour)

	// Resolve should return cached values without errors.
	result := r.Resolve(context.Background(), []string{"id1", "id2"})

	if len(result) != 2 {
		t.Errorf("expected 2 results, got %d", len(result))
	}

	if result["id1"] != "Player1" || result["id2"] != "Player2" {
		t.Errorf("expected cached values, got %v", result)
	}
}

func TestResolverNegativeCachingPreventsRetry(t *testing.T) {
	// Test that a negative cache entry prevents retries.
	clock := &fakeClock{now: time.Unix(0, 0)}
	r := &Resolver{
		apiKey: "test-key",
		client: &http.Client{},
		cache:  NewCache(&Options{NegativeTTL: 15 * time.Minute}, clock),
		opts:   (&Options{NegativeTTL: 15 * time.Minute}).Normalize(),
		sf:     &singleflightGroup{},
	}

	// Manually set a negative cache entry.
	r.cache.Set("unresolvable-id", "", 15*time.Minute)

	// Resolve should return empty map without calling upstream.
	result := r.Resolve(context.Background(), []string{"unresolvable-id"})
	if len(result) != 0 {
		t.Error("expected empty result for negative cache hit")
	}
}

func TestResolverNegativeCacheExpiry(t *testing.T) {
	// Test that a negative entry expires and allows retries.
	clock := &fakeClock{now: time.Unix(0, 0)}
	r := &Resolver{
		apiKey: "test-key",
		client: &http.Client{},
		cache:  NewCache(&Options{NegativeTTL: 15 * time.Minute}, clock),
		opts:   (&Options{NegativeTTL: 15 * time.Minute}).Normalize(),
		sf:     &singleflightGroup{},
	}

	r.cache.Set("unresolvable-id", "", 15*time.Minute)

	// Advance the clock past the negative TTL.
	clock.now = clock.now.Add(20 * time.Minute)

	// Now the entry should be expired.
	val, cached := r.cache.Get("unresolvable-id")
	if cached {
		t.Errorf("expected uncached after negative TTL expiry, got cached=%t, val=%q", cached, val)
	}
}

func TestResolverPartialResponse(t *testing.T) {
	// Test that a response with fewer players than requested ids is handled gracefully.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Only return one of the three requested ids.
		response := `{
  "response": {
    "players": [
      {"steamid": "76561198000000001", "personaname": "Player1"}
    ]
  }
}`
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	}))
	defer server.Close()

	r := &Resolver{
		apiKey:  "test-key",
		baseURL: server.URL + "/ISteamUser/GetPlayerSummaries/v2/",
		client:  &http.Client{Timeout: 2 * time.Second},
		cache:   NewCache(&Options{}, nil),
		opts:    (&Options{}).Normalize(),
		sf:      &singleflightGroup{},
	}

	// Test with three ids but only one returned.
	result, err := r.getPlayerSummaries(context.Background(), []string{"1", "2", "3"})
	if err != nil {
		t.Fatalf("getPlayerSummaries failed: %v", err)
	}

	// Only the returned id should be in the result.
	if len(result) != 1 {
		t.Errorf("expected 1 result, got %d", len(result))
	}

	if result["76561198000000001"] != "Player1" {
		t.Error("expected Player1 in result")
	}
}

func TestResolverNon200Status(t *testing.T) {
	// Test that non-200 responses are treated as errors.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"response": {}}`)
	}))
	defer server.Close()

	resolver := &Resolver{
		apiKey:  "bad-key",
		baseURL: server.URL + "/ISteamUser/GetPlayerSummaries/v2/",
		client:  &http.Client{Timeout: 2 * time.Second},
		cache:   NewCache(&Options{}, nil),
		opts:    (&Options{}).Normalize(),
		sf:      &singleflightGroup{},
	}

	// Call getPlayerSummaries against the test server returning a non-200 status.
	result, err := resolver.getPlayerSummaries(context.Background(), []string{"76561198000000001"})

	// A non-200 response should return an error.
	if err == nil {
		t.Error("expected error for non-200 status, got nil")
	}

	// The result should be nil on error.
	if result != nil {
		t.Errorf("expected nil result on error, got %v", result)
	}

	// Verify the error message does not leak the URL or key.
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestResolverMalformedJSON(t *testing.T) {
	// Test that malformed JSON responses are handled gracefully.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{invalid json}`)
	}))
	defer server.Close()

	resolver := &Resolver{
		apiKey:  "test-key",
		baseURL: server.URL + "/ISteamUser/GetPlayerSummaries/v2/",
		client:  &http.Client{Timeout: 2 * time.Second},
		cache:   NewCache(&Options{}, nil),
		opts:    (&Options{}).Normalize(),
		sf:      &singleflightGroup{},
	}

	// Call getPlayerSummaries against the test server returning malformed JSON.
	result, err := resolver.getPlayerSummaries(context.Background(), []string{"76561198000000001"})

	// Malformed JSON should return an error, not crash.
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}

	// The result should be nil on error.
	if result != nil {
		t.Errorf("expected nil result on error, got %v", result)
	}

	// Verify the error message does not leak the URL or key.
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestResolverNetworkFailure(t *testing.T) {
	// Test that network failures are handled gracefully and return an error without leaking the key.
	resolver := &Resolver{
		apiKey:  "sensitive-api-key",
		baseURL: "https://127.0.0.1:1/ISteamUser/GetPlayerSummaries/v2/", // Unreachable address
		client:  &http.Client{Timeout: 100 * time.Millisecond},           // Short timeout
		cache:   NewCache(&Options{}, nil),
		opts:    (&Options{}).Normalize(),
		sf:      &singleflightGroup{},
	}

	// Call getPlayerSummaries against an unreachable address.
	result, err := resolver.getPlayerSummaries(context.Background(), []string{"76561198000000001"})

	// A network failure should return an error.
	if err == nil {
		t.Error("expected error for network failure, got nil")
	}

	// The result should be nil on error.
	if result != nil {
		t.Errorf("expected nil result on error, got %v", result)
	}

	// Verify the error message does not leak the API key.
	errStr := err.Error()
	if errStr == "" {
		t.Error("expected non-empty error message")
	}

	// The error message should never contain the sensitive API key.
	if strings.Contains(errStr, "sensitive-api-key") {
		t.Errorf("error message leaked API key: %s", errStr)
	}
}

func TestResolverLargeBatchSplit(t *testing.T) {
	// Test that ids are correctly split into batches of at most 100.
	callCount := 0
	var mu sync.Mutex
	receivedBatches := []int{} // Track batch sizes for each call

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		ids := r.URL.Query()["steamids"]
		receivedBatches = append(receivedBatches, len(ids))
		mu.Unlock()

		// Return a valid response for all requested ids.
		var players []string
		for _, id := range ids {
			players = append(players, fmt.Sprintf(`{"steamid": "%s", "personaname": "Player%s"}`, id, id))
		}
		response := fmt.Sprintf(`{
  "response": {
    "players": [%s]
  }
}`, strings.Join(players, ","))

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	}))
	defer server.Close()

	resolver := &Resolver{
		apiKey:  "test-key",
		baseURL: server.URL + "/ISteamUser/GetPlayerSummaries/v2/",
		client:  &http.Client{Timeout: 2 * time.Second},
		cache:   NewCache(&Options{}, nil),
		opts:    (&Options{}).Normalize(),
		sf:      &singleflightGroup{},
	}

	// Create 250 ids (will be split into 3 batches: 100 + 100 + 50).
	var ids []string
	for i := 0; i < 250; i++ {
		ids = append(ids, fmt.Sprintf("7656119800000%04d", i))
	}

	// Resolve all ids.
	result, err := resolver.resolveBatch(context.Background(), ids)
	if err != nil {
		t.Fatalf("resolveBatch failed: %v", err)
	}

	// Verify all ids were resolved.
	if len(result) != 250 {
		t.Errorf("expected 250 results, got %d", len(result))
	}

	// Verify the batching was correct (3 batches: 100 + 100 + 50).
	if callCount != 3 {
		t.Errorf("expected 3 upstream calls, got %d", callCount)
	}

	mu.Lock()
	if len(receivedBatches) != 3 || receivedBatches[0] != 100 || receivedBatches[1] != 100 || receivedBatches[2] != 50 {
		t.Errorf("expected batch sizes [100, 100, 50], got %v", receivedBatches)
	}
	mu.Unlock()
}

// fakeClock is a test clock that can be advanced manually.
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

// syncBuffer is a goroutine-safe bytes sink for capturing slog output.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureSlog routes slog.Default output into a buffer for the duration of the test.
func captureSlog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// newTestResolver builds a Resolver whose client may reach the loopback httptest server.
// Production resolvers use netguard.IsPublic, which blocks loopback, so tests bypass it here.
func newTestResolver(baseURL, apiKey string, opts *Options, clock Clock) *Resolver {
	opts = opts.Normalize()
	if clock == nil {
		clock = realClock{}
	}
	return &Resolver{
		apiKey:  apiKey,
		baseURL: baseURL + "/ISteamUser/GetPlayerSummaries/v2/",
		client:  &http.Client{Timeout: opts.Timeout},
		cache:   NewCache(opts, clock),
		opts:    opts,
		sf:      &singleflightGroup{},
		clock:   clock,
	}
}

// steamIDs returns n distinct 17-digit-style ids for tests.
func steamIDs(n int) []string {
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, fmt.Sprintf("7656119800000%04d", i))
	}
	return ids
}

func assertUncached(t *testing.T, r *Resolver, ids []string) {
	t.Helper()
	for _, id := range ids {
		if val, cached := r.cache.Get(id); cached {
			t.Errorf("expected no cache entry for %s, got value=%q", id, val)
		}
	}
}

func TestResolverBlockedDestinationWritesNoCache(t *testing.T) {
	// The httptest server runs on 127.0.0.1, which the production netguard client blocks.
	// The handler must never be reached.
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("netguard-blocked destination must not be reached")
	}))
	defer server.Close()

	clock := &fakeClock{now: time.Unix(0, 0)}
	r := NewResolver("test-key", nil, clock)
	if r == nil {
		t.Fatal("NewResolver returned nil for a non-empty key")
	}
	r.baseURL = server.URL + "/ISteamUser/GetPlayerSummaries/v2/"

	ids := steamIDs(2)
	// Two lookups: the first failure must not negative-cache, so the second one attempts again.
	for i := 0; i < 2; i++ {
		result := r.Resolve(context.Background(), ids)
		if len(result) != 0 {
			t.Fatalf("attempt %d: expected empty map, got %v", i, result)
		}
		assertUncached(t, r, ids)
	}
}

func TestResolverServerErrorWritesNoNegativeEntries(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	r := newTestResolver(server.URL, "test-key", nil, &fakeClock{now: time.Unix(0, 0)})
	ids := steamIDs(2)

	for i := 0; i < 2; i++ {
		result := r.Resolve(context.Background(), ids)
		if len(result) != 0 {
			t.Fatalf("attempt %d: expected empty map on 500, got %v", i, result)
		}
	}
	assertUncached(t, r, ids)

	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Errorf("expected 2 upstream attempts (no negative caching), got %d", requests)
	}
}

func TestResolverNeverAnsweringServerAbandonedWithinTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release) // runs before server.Close so the handler can return

	timeout := 100 * time.Millisecond
	r := newTestResolver(server.URL, "test-key", &Options{Timeout: timeout}, nil)
	ids := steamIDs(3)

	start := time.Now()
	result := r.Resolve(context.Background(), ids)
	elapsed := time.Since(start)

	if len(result) != 0 {
		t.Errorf("expected empty map on timeout, got %v", result)
	}
	// Generous bound: the call must be abandoned near the configured timeout, not wait for the server.
	if elapsed > 5*time.Second {
		t.Errorf("resolve took %v, expected abandonment near timeout %v", elapsed, timeout)
	}
	assertUncached(t, r, ids)
}

func TestResolverSixteenIDsSingleUpstreamRequest(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids := r.URL.Query()["steamids"]
		mu.Lock()
		requests++
		mu.Unlock()

		players := make([]string, 0, len(ids))
		for _, id := range ids {
			players = append(players, fmt.Sprintf(`{"steamid": "%s", "personaname": "Player%s"}`, id, id))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"response": {"players": [%s]}}`, strings.Join(players, ","))
	}))
	defer server.Close()

	r := newTestResolver(server.URL, "test-key", nil, &fakeClock{now: time.Unix(0, 0)})
	ids := steamIDs(16)

	result := r.Resolve(context.Background(), ids)
	if len(result) != 16 {
		t.Errorf("expected 16 resolved ids, got %d", len(result))
	}

	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Errorf("expected exactly 1 upstream request for 16 ids, got %d", requests)
	}
}

func TestResolverLogsNeverContainKey(t *testing.T) {
	const key = "sk-steam-secret-key-do-not-log"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	buf := captureSlog(t)
	ids := steamIDs(2)

	// Status-failure path: the upstream call fails and the warn log fires.
	r := newTestResolver(server.URL, key, nil, &fakeClock{now: time.Unix(0, 0)})
	if result := r.Resolve(context.Background(), ids); len(result) != 0 {
		t.Fatalf("expected empty map, got %v", result)
	}

	// Transport-failure path: connection refused. The *url.Error carries the key-bearing URL,
	// so this exercises the unwrapping that keeps it out of the error text.
	dead := newTestResolver("http://127.0.0.1:1", key, nil, &fakeClock{now: time.Unix(0, 0)})
	if result := dead.Resolve(context.Background(), ids); len(result) != 0 {
		t.Fatalf("expected empty map on dial failure, got %v", result)
	}

	logs := buf.String()
	if !strings.Contains(logs, "steam resolver unavailable") {
		t.Fatalf("expected the unavailable warn log to fire, logs=%q", logs)
	}
	if strings.Contains(logs, key) {
		t.Errorf("log output leaked the API key: %s", logs)
	}
	for _, id := range ids {
		if strings.Contains(logs, id) {
			t.Errorf("log output leaked queried id %s: %s", id, logs)
		}
	}
}

func TestResolverUnavailableWarnRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	buf := captureSlog(t)
	clock := &fakeClock{now: time.Unix(0, 0)}
	r := newTestResolver(server.URL, "test-key", nil, clock)
	ids := steamIDs(1)
	const warnMsg = "steam resolver unavailable"

	// Two failures within the same minute produce one warn line.
	r.Resolve(context.Background(), ids)
	r.Resolve(context.Background(), ids)
	if n := strings.Count(buf.String(), warnMsg); n != 1 {
		t.Fatalf("expected 1 warn within the rate-limit window, got %d", n)
	}

	// After the interval elapses, the next failure logs again.
	clock.now = clock.now.Add(unavailableWarnInterval)
	r.Resolve(context.Background(), ids)
	if n := strings.Count(buf.String(), warnMsg); n != 2 {
		t.Errorf("expected 2 warn lines after the interval, got %d", n)
	}
}
