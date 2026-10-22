package steam

import (
	"time"
)

// Options configures the resolver's cache size, TTL, and upstream timeout.
type Options struct {
	// MaxEntries limits the number of entries in the LRU cache.
	// Zero or negative falls back to DefaultMaxEntries.
	MaxEntries int

	// TTL is the time-to-live for positive cache entries (successful resolutions).
	// Zero or negative falls back to DefaultTTL.
	TTL time.Duration

	// NegativeTTL is the time-to-live for negative cache entries (ids that failed to resolve).
	// Zero or negative falls back to DefaultNegativeTTL.
	NegativeTTL time.Duration

	// Timeout bounds each individual HTTP call to Steam's GetPlayerSummaries endpoint.
	// Zero or negative falls back to DefaultTimeout.
	Timeout time.Duration
}

// DefaultMaxEntries is the default LRU cache entry count limit.
// A Steam ID plus display name is ~50–100 bytes, so 10k entries stays well under 1MB.
const DefaultMaxEntries = 10000

// DefaultTTL is the default positive cache entry lifetime.
// Display names change rarely and a stale name is harmless when moderation keys on steamId.
const DefaultTTL = 12 * time.Hour

// DefaultNegativeTTL is the default negative cache entry lifetime.
// Unresolvable ids are cached shorter to allow eventual retry if a private profile becomes public.
const DefaultNegativeTTL = 15 * time.Minute

// DefaultTimeout is the default per-call HTTP timeout, well inside the SC-004 five-second budget.
const DefaultTimeout = 2 * time.Second

// Normalize normalizes o in place, replacing all zero or negative fields with their defaults.
// This ensures misconfiguration can never produce an unbounded cache or call.
// If o is nil, Normalize allocates a new Options, normalizes it, and returns the pointer.
func (o *Options) Normalize() *Options {
	if o == nil {
		o = &Options{}
	}
	if o.MaxEntries <= 0 {
		o.MaxEntries = DefaultMaxEntries
	}
	if o.TTL <= 0 {
		o.TTL = DefaultTTL
	}
	if o.NegativeTTL <= 0 {
		o.NegativeTTL = DefaultNegativeTTL
	}
	if o.Timeout <= 0 {
		o.Timeout = DefaultTimeout
	}
	return o
}

// Scenario 5 check.
