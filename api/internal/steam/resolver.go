package steam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"sync"
	"time"

	"github.com/GameplanePanel/gameplane/netguard"
)

// unavailableWarnInterval bounds how often the "resolver unavailable" warn log is emitted.
const unavailableWarnInterval = time.Minute

// Resolver resolves Steam IDs to display names via the Steam Web API.
// It batches requests, caches results with positive and negative TTL,
// and gracefully degrades to returning nothing when the key is missing or the API is unavailable.
// The resolver is safe for concurrent use.
type Resolver struct {
	apiKey  string
	baseURL string
	client  *http.Client
	cache   *Cache
	opts    *Options
	sf      *singleflightGroup
	clock   Clock

	// warnMu guards warned and lastWarn, which rate-limit the unavailable warn log.
	warnMu   sync.Mutex
	warned   bool
	lastWarn time.Time
}

// NewResolver returns a Resolver, or nil if apiKey is empty.
// opts and clock are optional; Normalize() and time.Now are used as fallbacks.
func NewResolver(apiKey string, opts *Options, clock Clock) *Resolver {
	if apiKey == "" {
		return nil
	}

	if opts == nil {
		opts = &Options{}
	}
	opts = opts.Normalize()

	if clock == nil {
		clock = realClock{}
	}

	client := netguard.HTTPClient(opts.Timeout, netguard.IsPublic)

	return &Resolver{
		apiKey:  apiKey,
		baseURL: "https://api.steampowered.com/ISteamUser/GetPlayerSummaries/v2/",
		client:  client,
		cache:   NewCache(opts, clock),
		opts:    opts,
		sf:      &singleflightGroup{},
		clock:   clock,
	}
}

// Resolve takes a slice of Steam IDs and returns a map of id -> display name.
// Ids that failed to resolve (missing from the Steam response, private profile, deleted account)
// are absent from the map; the caller should fall back to rendering the raw Steam ID.
//
// On cache hits, returns cached values immediately.
// On cache misses, batches uncached ids in groups of up to 100 and queries GetPlayerSummaries.
// DNS failures, dial failures, netguard blocks, timeouts, non-200 responses, and malformed JSON
// cause the affected ids to be treated as unresolved (absent from the map) but are NOT returned
// as errors to the caller. A failed call writes no cache entries at all, so the next lookup retries.
// Only ids that a successful Steam response omitted are stored as negative entries.
// A rate-limited (at most once per minute) warn log is issued for the outbound failure, but never
// includes the API key or any queried ids.
//
// Concurrent lookups of the same uncached ids are collapsed into a single upstream call by singleflight.
func (r *Resolver) Resolve(ctx context.Context, steamIDs []string) map[string]string {
	result := make(map[string]string)

	if r == nil {
		return result
	}

	// Separate cached ids from those needing upstream resolution.
	var uncached []string
	for _, id := range steamIDs {
		val, cached := r.cache.Get(id)
		if cached && val != "" {
			// Positive hit.
			result[id] = val
		}
		// If not cached at all, add to uncached list.
		// If cached but val=="", this is a negative hit (tried before and failed);
		// leave it absent from the result map; caller falls back to the raw id.
		if !cached {
			uncached = append(uncached, id)
		}
	}

	if len(uncached) == 0 {
		return result
	}

	// Collapse concurrent identical requests via singleflight.
	batchRes, err := r.sf.Do(ctx, uncached, func(sfCtx context.Context) (map[string]string, error) {
		return r.resolveBatch(sfCtx, uncached)
	})

	if err != nil {
		// The upstream call failed. Treat as all ids unresolved. No cache entries are written,
		// so a later lookup retries Steam. Log rate-limited, never with the key or ids.
		r.warnUnavailable(err)
		return result
	}

	// Merge resolved ids into the result.
	for id, val := range batchRes {
		result[id] = val
	}

	return result
}

// warnUnavailable logs that the resolver could not reach Steam, at most once per
// unavailableWarnInterval. The error text is key-free; callers must not pass ids or the key.
func (r *Resolver) warnUnavailable(err error) {
	r.warnMu.Lock()
	defer r.warnMu.Unlock()

	now := r.now()
	if r.warned && now.Sub(r.lastWarn) < unavailableWarnInterval {
		return
	}
	r.warned = true
	r.lastWarn = now
	slog.Warn("steam resolver unavailable", "err", err)
}

// now returns the resolver's clock time, falling back to time.Now for resolvers built without one.
func (r *Resolver) now() time.Time {
	if r.clock == nil {
		return time.Now()
	}
	return r.clock.Now()
}

// resolveBatch fetches resolutions for a set of ids, batching into calls of at most 100 ids each.
// On success it caches positive results and stores negative entries only for ids that the
// successful Steam responses omitted. If any batch call fails (transport error, netguard block,
// timeout, non-200 status, malformed JSON), an error is returned and no cache entries are written
// for any of the ids, so future calls retry them.
func (r *Resolver) resolveBatch(ctx context.Context, ids []string) (map[string]string, error) {
	result := make(map[string]string)

	// Batch ids into groups of at most 100.
	for i := 0; i < len(ids); i += 100 {
		end := i + 100
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[i:end]

		batchResult, err := r.getPlayerSummaries(ctx, batch)
		if err != nil {
			// On any batch failure, return the error and leave all ids uncached.
			// A failed call says nothing about the ids themselves, so no negative entries are written.
			return nil, fmt.Errorf("get player summaries: %w", err)
		}

		for id, val := range batchResult {
			result[id] = val
		}
	}

	// Cache all results and negative entries.
	sort.Strings(ids)
	for _, id := range ids {
		if val, ok := result[id]; ok {
			r.cache.Set(id, val, r.opts.TTL)
		} else {
			// Steam didn't return this id; store as a negative entry.
			r.cache.Set(id, "", r.opts.NegativeTTL)
		}
	}

	return result, nil
}

// getPlayerSummaries calls the Steam Web API GetPlayerSummaries endpoint for a batch of ids.
// Returns a map of id -> display name for ids that Steam returned.
// Ids omitted from the response (private profiles, deleted accounts, malformed ids) are absent from the map.
//
// On network failure, netguard block, timeout, or non-200 status, returns an error.
// On malformed JSON, returns an error. A netguard block is reported as an error (never an empty
// success), so the caller does not negative-cache the ids.
// Never logs or returns the API key.
func (r *Resolver) getPlayerSummaries(ctx context.Context, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}

	// Build the request URL. The key is passed as a query parameter.
	params := url.Values{}
	params.Set("key", r.apiKey)
	for _, id := range ids {
		params.Add("steamids", id)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", r.baseURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.URL.RawQuery = params.Encode()

	resp, err := r.client.Do(req)
	if err != nil {
		// Network error, timeout, or netguard block.
		if errors.Is(err, netguard.ErrBlockedAddr) {
			// netguard rejected the address. Return a fresh wrapped sentinel rather than the
			// *url.Error, whose text contains the key-bearing request URL.
			return nil, fmt.Errorf("steam destination blocked: %w", netguard.ErrBlockedAddr)
		}
		// http.Client.Do may return a *url.Error whose Error() method renders the full request URL,
		// which contains the API key as a query parameter. To avoid leaking the key while preserving
		// the error cause for errors.Is/errors.As, wrap the inner Err instead of the full *url.Error.
		var urlErr *url.Error
		if errors.As(err, &urlErr) && urlErr.Err != nil {
			return nil, fmt.Errorf("http request: %w", urlErr.Err)
		}
		// For non-URL errors, wrap directly; they don't contain the URL.
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("steam api returned %d", resp.StatusCode)
	}

	// Parse the response.
	var steamResp struct {
		Response struct {
			Players []struct {
				Steamid                  string `json:"steamid"`
				PersonaName              string `json:"personaname"`
				CommunityVisibilityState int    `json:"communityvisibilitystate"`
			} `json:"players"`
		} `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&steamResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Extract the id -> name mapping. Skip entries with empty personaname.
	result := make(map[string]string)
	for _, p := range steamResp.Response.Players {
		if p.PersonaName != "" {
			result[p.Steamid] = p.PersonaName
		}
	}

	return result, nil
}
