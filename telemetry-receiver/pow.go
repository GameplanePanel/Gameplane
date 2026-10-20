package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/GameplanePanel/gameplane/telemetryschema"
	"github.com/prometheus/client_golang/prometheus"
)

// Proof-of-work on report ingestion (spec 022 FR-039, research R21, OD-5).
// Challenges are stateless: a MAC'd token the receiver can recognise without
// remembering it. Only solved challenges are remembered (the used set), so
// issuing cannot exhaust memory.
const (
	// Defaults and bounds of INGEST_POW_* (OD-5).
	defaultPoWTargetPerMin = 60
	defaultPoWMinBits      = 0
	defaultPoWMaxBits      = 22

	// powTokenVersion is the first payload byte.
	powTokenVersion = 1
	// powTTLSeconds is how long a challenge stays valid after it is issued.
	powTTLSeconds = 15 * 60
	// powKeySize is the length of the in-memory MAC key K.
	powKeySize = 32
	// powMACSize is the length of the truncated HMAC-SHA256 in a token.
	powMACSize = 16
	// powIDSize is the number of random bytes in a payload; they identify the
	// challenge in the used set.
	powIDSize = 16
	// powPayloadSize is version (1) + issue time (8) + bits (1) + random.
	powPayloadSize = 1 + 8 + 1 + powIDSize
	// powWindow is the trailing window, in seconds, that sets the difficulty.
	powWindow = 60
	// powDecayStep is how long the difficulty peak lasts per bit.
	powDecayStep = 5 * time.Minute
	// powPruneEvery is how often the lazy sweep of the used set may run.
	powPruneEvery = time.Minute
	// powChallengePerMinute and powChallengeBurst are the per-source bucket of
	// GET /v1/challenge.
	powChallengePerMinute = 10
	powChallengeBurst     = 5
	// routeChallenge is the route label of rate_limited_total for the
	// challenge endpoint.
	routeChallenge = "challenge"
)

// powUsedCap bounds the used-challenge set. It is a variable so tests can
// lower it; production code never assigns it.
var powUsedCap = 1_000_000

// Refusal reasons of the proof-of-work check: the JSON error codes and the
// label values of gameplane_telemetry_refused_total.
const (
	refusePoWRequired = "pow_required"
	refusePoWInvalid  = "pow_invalid"
	refusePoWBusy     = "pow_busy"
)

// powVerdict is the outcome of powState.check.
type powVerdict int

const (
	powAccepted powVerdict = iota
	powMissing
	powInvalid
	powBusy
)

// powSlot is one second of the issue window.
type powSlot struct {
	sec int64
	n   int
}

// powState issues and verifies challenges. All of it is in memory: a restart
// invalidates outstanding challenges, and senders recover with the resend of
// FR-040.
type powState struct {
	key                            [powKeySize]byte
	targetPerMin, minBits, maxBits int

	mu    sync.Mutex
	slots [powWindow]powSlot
	// peak and peakAt are the last difficulty that exceeded the decayed one,
	// and when.
	peak   int
	peakAt time.Time
	// used maps a solved challenge's random bytes to the Unix second at which
	// the challenge expires, after which the entry is dropped.
	used      map[[powIDSize]byte]uint64
	lastPrune time.Time
}

// newPoW returns the state for cfg, with a new random MAC key. The settings
// are clamped to a usable range so a zero config cannot divide by zero.
func newPoW(cfg config) *powState {
	p := &powState{
		targetPerMin: max(cfg.powTargetPerMin, 1),
		minBits:      max(cfg.powMinBits, 0),
		maxBits:      min(cfg.powMaxBits, telemetryschema.MaxPoWBits),
		used:         make(map[[powIDSize]byte]uint64),
	}
	p.maxBits = max(p.maxBits, p.minBits)
	if _, err := rand.Read(p.key[:]); err != nil {
		// crypto/rand failing is not recoverable: tokens would be forgeable.
		panic(fmt.Sprintf("telemetry-receiver: pow key: %v", err))
	}
	return p
}

// bitsForRate is the difficulty a challenge carries when r challenges were
// issued in the trailing window, before decay and the minimum:
// 0 when r <= T, otherwise min(MAX, ceil(2*log2(r/T))).
func (p *powState) bitsForRate(r int) int {
	if r <= p.targetPerMin {
		return 0
	}
	bits := int(math.Ceil(2 * math.Log2(float64(r)/float64(p.targetPerMin))))
	return min(p.maxBits, bits)
}

// recordLocked counts one issue at now and returns how many challenges were
// issued in the trailing window, including this one. The caller holds mu.
func (p *powState) recordLocked(now time.Time) int {
	sec := now.Unix()
	slot := &p.slots[((sec%powWindow)+powWindow)%powWindow]
	if slot.sec != sec {
		*slot = powSlot{sec: sec}
	}
	slot.n++
	return p.countLocked(now)
}

// countLocked is the number of challenges issued in the trailing window.
func (p *powState) countLocked(now time.Time) int {
	sec := now.Unix()
	total := 0
	for _, s := range p.slots {
		if s.sec > sec-powWindow && s.sec <= sec {
			total += s.n
		}
	}
	return total
}

// difficultyLocked is max(MIN, target, peak - floor((now-peakAt)/5m)) for r
// challenges in the window. With commit it also records a new peak when the
// target exceeds the decayed one. The caller holds mu.
func (p *powState) difficultyLocked(now time.Time, r int, commit bool) int {
	target := p.bitsForRate(r)
	decayed := 0
	if p.peak > 0 {
		steps := max(int(now.Sub(p.peakAt)/powDecayStep), 0)
		decayed = max(p.peak-steps, 0)
	}
	if target > decayed {
		if commit {
			p.peak, p.peakAt = target, now
		}
		decayed = target
	}
	return max(p.minBits, decayed)
}

// currentBits is the difficulty a challenge issued at now would carry,
// without issuing one: the gameplane_telemetry_pow_bits gauge.
func (p *powState) currentBits(now time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.difficultyLocked(now, p.countLocked(now)+1, false)
}

// mac returns the first powMACSize bytes of HMAC-SHA256(K, payload).
func (p *powState) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, p.key[:])
	m.Write(payload)
	return m.Sum(nil)[:powMACSize]
}

// issue creates a challenge at now: its token, its difficulty and its expiry.
func (p *powState) issue(now time.Time) (token string, bits int, expires time.Time) {
	p.mu.Lock()
	r := p.recordLocked(now)
	bits = p.difficultyLocked(now, r, true)
	p.mu.Unlock()

	issued := max(now.Unix(), 0)
	payload := make([]byte, powPayloadSize)
	payload[0] = powTokenVersion
	binary.BigEndian.PutUint64(payload[1:9], uint64(issued))
	payload[9] = byte(bits & 0xff)
	if _, err := rand.Read(payload[10:]); err != nil {
		panic(fmt.Sprintf("telemetry-receiver: pow challenge: %v", err))
	}
	token = base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(p.mac(payload))
	return token, bits, time.Unix(issued, 0).Add(powTTLSeconds * time.Second).UTC()
}

// parseToken checks the token's shape and MAC and returns its payload.
func (p *powState) parseToken(token string) ([]byte, bool) {
	rawPayload, rawMAC, ok := strings.Cut(token, ".")
	if !ok {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(rawPayload)
	if err != nil || len(payload) != powPayloadSize || payload[0] != powTokenVersion {
		return nil, false
	}
	got, err := base64.RawURLEncoding.DecodeString(rawMAC)
	if err != nil || !hmac.Equal(got, p.mac(payload)) {
		return nil, false
	}
	return payload, true
}

// check runs the verification order of research R21 on a PoWHeader value:
// header present, well formed, MAC valid, not expired, enough leading zero
// bits (as stated in the token, so a sender cannot lower it), not already
// used. A solution that passes is marked used before check returns, whatever
// happens to the report afterwards. When the used set is full it returns
// powBusy and marks nothing.
func (p *powState) check(header string, now time.Time) powVerdict {
	if header == "" {
		return powMissing
	}
	token, nonce, err := telemetryschema.ParsePoW(header)
	if err != nil {
		return powInvalid
	}
	payload, ok := p.parseToken(token)
	if !ok {
		return powInvalid
	}
	nowSec := now.Unix()
	if nowSec < 0 {
		return powInvalid
	}
	issued := binary.BigEndian.Uint64(payload[1:9])
	expires := issued + powTTLSeconds
	if uint64(nowSec) > expires {
		return powInvalid
	}
	if !telemetryschema.PoWOK(token, nonce, int(payload[9])) {
		return powInvalid
	}
	var id [powIDSize]byte
	copy(id[:], payload[10:])

	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	if _, dup := p.used[id]; dup {
		return powInvalid
	}
	if len(p.used) >= powUsedCap {
		return powBusy
	}
	p.used[id] = expires
	return powAccepted
}

// pruneLocked drops expired entries of the used set, at most once a minute.
// An expired challenge is refused by its age, so forgetting it is safe. The
// caller holds mu.
func (p *powState) pruneLocked(now time.Time) {
	if now.Sub(p.lastPrune) < powPruneEvery {
		return
	}
	p.lastPrune = now
	nowSec := now.Unix()
	if nowSec < 0 {
		return
	}
	for id, expires := range p.used {
		if uint64(nowSec) > expires {
			delete(p.used, id)
		}
	}
}

// initPoW sets up proof-of-work: the two metrics always, and the state and
// the challenge limiter only when INGEST_POW is on. With it off, s.pow stays
// nil and /ingest and /v1/challenge behave as before.
func (s *server) initPoW() {
	s.powChallenges = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "gameplane_telemetry_pow_challenges_total",
		Help: "Proof-of-work challenges issued.",
	})
	s.reg.MustRegister(s.powChallenges, prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "gameplane_telemetry_pow_bits",
		Help: "Difficulty, in leading zero bits, of a challenge issued now (0 when proof-of-work is off).",
	}, func() float64 {
		if s.pow == nil {
			return 0
		}
		return float64(s.pow.currentBits(s.now()))
	}))
	if !s.cfg.ingestPoW {
		return
	}
	s.pow = newPoW(s.cfg)
	s.powLimiter = newBucketLimiter(powChallengePerMinute, powChallengeBurst)
	for _, reason := range []string{refusePoWRequired, refusePoWInvalid, refusePoWBusy} {
		s.refused.WithLabelValues(reason)
	}
	s.rateLimited.WithLabelValues(routeChallenge)
}

// requirePoW enforces proof-of-work on /ingest. It returns true when the
// request may go on (proof-of-work is off, or the solution verified) and has
// answered the request itself when it returns false.
func (s *server) requirePoW(w http.ResponseWriter, req *http.Request) bool {
	if s.pow == nil {
		return true
	}
	switch s.pow.check(req.Header.Get(telemetryschema.PoWHeader), s.now()) {
	case powAccepted:
		return true
	case powMissing:
		s.refusePoW(w, refusePoWRequired, http.StatusPreconditionRequired)
	case powBusy:
		w.Header().Set("Retry-After", "60")
		s.refusePoW(w, refusePoWBusy, http.StatusServiceUnavailable)
	default:
		s.refusePoW(w, refusePoWInvalid, http.StatusPreconditionRequired)
	}
	return false
}

// refusePoW answers a refused report with a fixed JSON body and counts it. A
// refused report changes no stored data.
func (s *server) refusePoW(w http.ResponseWriter, reason string, status int) {
	s.refused.WithLabelValues(reason).Inc()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + reason + `"}`))
}

// challengeReply is the body of GET /v1/challenge.
type challengeReply struct {
	Challenge string `json:"challenge"`
	Bits      int    `json:"bits"`
	ExpiresAt string `json:"expiresAt"`
}

// challenge serves GET /v1/challenge: 404 when proof-of-work is off, 429
// past the per-source bucket, otherwise a new challenge.
func (s *server) challenge(w http.ResponseWriter, r *http.Request) {
	if s.pow == nil {
		http.NotFound(w, r)
		return
	}
	now := s.now()
	if !s.powLimiter.allow(sourceIP(r, s.cfg.trustedProxyCIDRs), now) {
		s.rateLimited.WithLabelValues(routeChallenge).Inc()
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	token, bits, expires := s.pow.issue(now)
	s.powChallenges.Inc()
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(challengeReply{Challenge: token, Bits: bits, ExpiresAt: expires.Format(time.RFC3339)})
}
