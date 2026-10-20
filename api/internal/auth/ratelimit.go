package auth

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

// TokenBucket is a minimal per-key rate limiter: each key has a bucket
// that refills at `rate` tokens per second up to `burst`. Every call to
// Allow drains one token; denied calls return false without touching
// the bucket.
//
// Sized for the login path, where legitimate traffic is tiny but
// attackers will fire as fast as the argon2 cost allows. Keeping this
// in-process is fine for single-replica deployments; HA setups with
// multiple API pods should replace this with Redis later.
type TokenBucket struct {
	rate  float64 // tokens per second
	burst float64

	mu      sync.Mutex
	buckets map[string]*bucket
	lastGC  time.Time
}

type bucket struct {
	tokens     float64
	lastUpdate time.Time
}

func newTokenBucket(rate, burst float64) *TokenBucket {
	return &TokenBucket{
		rate:    rate,
		burst:   burst,
		buckets: map[string]*bucket{},
		lastGC:  time.Now(),
	}
}

// Allow returns true if the key has tokens available and reserves one, false otherwise.
func (t *TokenBucket) Allow(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()

	// Opportunistic GC — buckets untouched for >5 min are dropped.
	if now.Sub(t.lastGC) > 5*time.Minute {
		for k, b := range t.buckets {
			if now.Sub(b.lastUpdate) > 5*time.Minute {
				delete(t.buckets, k)
			}
		}
		t.lastGC = now
	}

	b, ok := t.buckets[key]
	if !ok {
		b = &bucket{tokens: t.burst, lastUpdate: now}
		t.buckets[key] = b
	}
	elapsed := now.Sub(b.lastUpdate).Seconds()
	b.tokens += elapsed * t.rate
	if b.tokens > t.burst {
		b.tokens = t.burst
	}
	b.lastUpdate = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Middleware enforces the bucket against the client IP. The client IP must
// be set in the request context via chi's ClientIPFromXFF or similar middleware.
func (t *TokenBucket) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// Read the client IP from context set by the ClientIPFromXFF middleware.
		// If not set, fall back to the host portion of RemoteAddr for graceful
		// degradation (e.g., in unit tests that don't run the middleware chain).
		clientIP := middleware.GetClientIP(req.Context())
		if clientIP == "" {
			// Fallback: extract host from RemoteAddr if the middleware wasn't applied.
			// net.SplitHostPort handles both IPv4:port and [IPv6]:port forms correctly;
			// for bare addresses (no port), it returns an error and we use the original.
			host, _, err := net.SplitHostPort(req.RemoteAddr)
			if err != nil {
				// No port separator found, use the whole RemoteAddr as the client IP.
				clientIP = req.RemoteAddr
			} else {
				clientIP = host
			}
		}
		if !t.Allow(clientIP) {
			w.Header().Set("Retry-After", "30")
			httperr.WriteCode(w, req, http.StatusTooManyRequests, errors.New("too many requests"))
			return
		}
		next.ServeHTTP(w, req)
	})
}

// LoginLimiter caps login attempts per client IP. 5/min with a burst of
// 10 tolerates accidental retries but starves a brute-forcer of argon2
// cycles quickly.
var LoginLimiter = newTokenBucket(5.0/60.0, 10)

// LoginUserLimiter caps login attempts per *username*, layered on top of
// LoginLimiter. Without it, an attacker spread across a botnet would
// still burn through usernames at full speed because each IP has its
// own bucket. 3/min with a burst of 6 blunts that without tripping on
// a user fat-fingering their password.
var LoginUserLimiter = newTokenBucket(3.0/60.0, 6)

// OIDCCallbackLimiter guards /auth/oidc/callback. Each redirect back
// from the IdP triggers a token exchange + DB writes; flooding it can
// overwhelm the IdP and the local DB. Tight burst; generous refill.
var OIDCCallbackLimiter = newTokenBucket(10.0/60.0, 10)

// NotifyTestLimiter guards the notification test-send endpoint. Each hit
// dials an admin-configured external endpoint synchronously, so it gets a
// far tighter budget than ordinary mutations: enough to poke every sink
// while setting things up, not enough to use the API as a POST cannon.
var NotifyTestLimiter = newTokenBucket(12.0/60.0, 3)

// MutationLimiter caps authenticated writes per IP. The login bucket is
// narrow (argon2 is expensive); this one is broad — 60/min with 60
// burst — and only exists to keep a single client from pegging the
// database on a mass create/delete loop.
var MutationLimiter = newTokenBucket(1.0, 60) // 60/min refill, 60 burst

// NewFleetReadLimiter creates a budget shared by all fleet reads on one router:
// 60/min sustained, burst 10. It is independent of login and mutation budgets.
func NewFleetReadLimiter() *TokenBucket { return newTokenBucket(1.0, 10) }

// UserMiddleware enforces a bucket keyed by the authenticated account rather
// than IP or session, so separate users behind one proxy do not share a budget.
func (t *TokenBucket) UserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		u := UserFromContext(req.Context())
		if u == nil {
			httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
			return
		}
		if !t.AllowUser(strconv.FormatInt(u.ID, 10)) {
			w.Header().Set("Retry-After", "1")
			httperr.WriteMessage(w, req, messages.New("server.common.too_many_requests", nil))
			return
		}
		next.ServeHTTP(w, req)
	})
}

// ShareLimiter guards public (unauthenticated) share link endpoints.
// Share links are token-guessable, so we need a tighter budget than general
// mutations but higher than login attempts. 20/min with burst of 20 allows
// legitimate players to retry connection attempts while keeping brute-force
// costs reasonable.
var ShareLimiter = newTokenBucket(20.0/60.0, 20)

// AllowUser is a helper for rate-limit checks keyed on something other
// than the IP (e.g. username). Returns the same bool as Middleware's
// check but lets callers produce custom 429 responses.
func (t *TokenBucket) AllowUser(key string) bool { return t.Allow(key) }
