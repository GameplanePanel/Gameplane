package main

import (
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// New-install proof-of-work. The first report of an install ID the receiver
// has no activity record for must carry a solved challenge of at least
// INGEST_CLAIM_POW_BITS, so creating installs in bulk costs CPU while known
// installs never solve anything (unless INGEST_POW is on for every report).
//
// The API reporter already fetches a challenge before each POST and, on a
// 428, fetches a new one and resends once (FR-040). The receiver uses that:
// a claim without enough work gets 428 pow_claim and its source is marked
// for a short while, and challenges issued to a marked source carry the
// claim difficulty. Marks hold a source prefix in memory only, like the
// limiters (FR-014), and expire with the challenges they ask for.
const (
	defaultClaimPoWBits = 20
	// claimMarkTTL is how long a source keeps getting claim-grade
	// challenges after a refused claim.
	claimMarkTTL = powTTLSeconds * time.Second
	// claimMarkCap bounds the marked sources. When it is full and nothing
	// has expired, a new source is not marked: its reporter fails this
	// attempt and retries at its next interval.
	claimMarkCap = 100_000
)

// claimMarks is the set of sources with a pending new-install claim.
type claimMarks struct {
	mu    sync.Mutex
	until map[netip.Prefix]time.Time
}

func newClaimMarks() *claimMarks {
	return &claimMarks{until: make(map[netip.Prefix]time.Time)}
}

// mark records a pending claim from src until now + claimMarkTTL.
func (c *claimMarks) mark(src netip.Addr, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := sourceKey(src)
	if _, ok := c.until[key]; !ok && len(c.until) >= claimMarkCap {
		for k, t := range c.until {
			if !now.Before(t) {
				delete(c.until, k)
			}
		}
		if len(c.until) >= claimMarkCap {
			return
		}
	}
	c.until[key] = now.Add(claimMarkTTL)
}

// pending reports whether src has an unexpired mark.
func (c *claimMarks) pending(src netip.Addr, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := sourceKey(src)
	t, ok := c.until[key]
	if ok && !now.Before(t) {
		delete(c.until, key)
		return false
	}
	return ok
}

// clear drops src's mark after its claim was accepted.
func (c *claimMarks) clear(src netip.Addr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.until, sourceKey(src))
}

// requireClaimPoW decides whether a report that would create a new install
// may go on. powBits is the difficulty of the solution requirePoW already
// verified, or -1. With INGEST_POW off the header has not been checked yet,
// so it is verified here. It answers the request itself and returns false
// when the claim must be refused.
func (s *server) requireClaimPoW(w http.ResponseWriter, req *http.Request, powBits int) bool {
	if s.claims == nil {
		return true
	}
	now := s.now()
	src := sourceIP(req, s.cfg.trustedProxyCIDRs)
	if powBits < 0 && !s.cfg.ingestPoW {
		if header := req.Header.Get(telemetryschema.PoWHeader); header != "" {
			if verdict, bits := s.pow.checkBits(header, now); verdict == powAccepted {
				powBits = bits
			}
		}
	}
	if powBits >= s.cfg.claimPoWBits {
		s.claims.clear(src)
		return true
	}
	s.claims.mark(src, now)
	s.refusePoW(w, refusePoWClaim, http.StatusPreconditionRequired)
	return false
}
