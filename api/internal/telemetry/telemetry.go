// Package telemetry sends the install's usage report to its resolved
// destination (specs/done_022-default-telemetry-dashboard).
//
// Privacy: the basic report carries only the control-plane version and
// total counts. The extended report adds an install ID and environment
// categories, only while the admin's extended switch is on, and is signed.
// Nothing is sent while the operator disabled telemetry, no destination is in
// effect, the admin turned basic off, or (on a fresh install) before the
// notice was shown to an admin (FR-004).
//
// The schedule lives in the database (telemetry_state.next_due_at), so a
// restart never sends early and replicas sharing one database claim each
// slot with a conditional UPDATE (research R12).
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/telemetryschema"
)

const (
	// maxPoll caps how often the reporter looks at the schedule.
	maxPoll = 5 * time.Minute
	// baseBackoff is the wait after the first failure; it doubles per
	// further failure up to the report interval.
	baseBackoff = time.Hour
	// extFallbackFor is how long the extended part is withheld from an
	// endpoint that answered 400 to it (research R2).
	extFallbackFor = 7 * 24 * time.Hour
	// outcomeOK and outcomeFailed are the telemetry_state.last_outcome values.
	outcomeOK     = "ok"
	outcomeFailed = "failed"
	// challengeTimeout bounds the request for a proof-of-work challenge
	// (research R21).
	challengeTimeout = 10 * time.Second
	// solveMargin is how long before a challenge expires the solver must
	// give up.
	solveMargin = 30 * time.Second
	// challengeTTL is how long a challenge lives when the provider's
	// expiresAt is missing or unreadable (the receiver's fixed lifetime).
	challengeTTL = 15 * time.Minute
	// maxChallengeBody bounds how much of a challenge response is read.
	maxChallengeBody = 4096
)

// errPoWDifficulty is wrapped by the failure for a challenge whose difficulty
// is outside 0 to telemetryschema.MaxPoWBits (FR-040).
var errPoWDifficulty = errors.New("challenge difficulty out of range")

// Config configures a Reporter.
type Config struct {
	// Dest is the resolved destination. Kinds disabled and none never send.
	Dest Destination
	// Interval is the spacing between reports; non-positive means 24h.
	Interval time.Duration
	// Auth, when non-empty, is sent verbatim as the Authorization header.
	Auth string
	// Deps is what Collect reads from. Deps.Store is required. Extended and
	// Now are set per report by the reporter.
	Deps Deps
	// Client is the HTTP client; nil means one with a 15s timeout.
	Client *http.Client
	// Now returns the current time; nil means time.Now. Tests set it.
	Now func() time.Time
	// Rand returns a uniform value in [0, n); nil means math/rand/v2.
	Rand func(n int64) int64
}

// Reporter sends telemetry reports on the persisted schedule.
type Reporter struct {
	dest     Destination
	interval time.Duration
	auth     string
	deps     Deps
	store    *db.Store
	client   *http.Client
	now      func() time.Time
	rnd      func(int64) int64
}

// New builds a Reporter from cfg.
func New(cfg Config) *Reporter {
	if cfg.Interval <= 0 {
		cfg.Interval = 24 * time.Hour
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Rand == nil {
		cfg.Rand = randN
	}
	return &Reporter{
		dest:     cfg.Dest,
		interval: cfg.Interval,
		auth:     cfg.Auth,
		deps:     cfg.Deps,
		store:    cfg.Deps.Store,
		client:   cfg.Client,
		now:      cfg.Now,
		rnd:      cfg.Rand,
	}
}

// pollEvery is how often Run evaluates the schedule: min(5m, interval/4).
func (r *Reporter) pollEvery() time.Duration {
	return max(time.Second, min(maxPoll, r.interval/4))
}

// Run evaluates the schedule every pollEvery until ctx is cancelled. It
// returns at once, making no connection, when the destination is disabled or
// none.
func (r *Reporter) Run(ctx context.Context) {
	if !destActive(r.dest) {
		slog.Info("telemetry off: no destination in effect", "kind", r.dest.Kind)
		return
	}
	t := time.NewTicker(r.pollEvery())
	defer t.Stop()
	for {
		if err := r.Tick(ctx); err != nil {
			slog.Warn("telemetry report", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick evaluates the schedule once. It opens or closes the gate's slot,
// and, when a slot is due and this process wins the claim, sends one report.
// It returns an error when the send failed (the failure is also recorded).
func (r *Reporter) Tick(ctx context.Context) error {
	now := r.now()
	st, err := r.store.GetTelemetryState(ctx)
	if err != nil {
		return fmt.Errorf("telemetry tick: %w", err)
	}
	c, err := ReadConsent(ctx, r.store)
	if err != nil {
		return fmt.Errorf("telemetry tick: %w", err)
	}
	if !gateOpen(destActive(r.dest) && c.Basic, st) {
		if st.NextDueAt == "" {
			return nil
		}
		if err := r.store.CloseTelemetrySchedule(ctx); err != nil {
			return fmt.Errorf("telemetry tick: %w", err)
		}
		return nil
	}
	if st.NextDueAt == "" {
		if _, err := r.store.OpenTelemetrySchedule(ctx, stamp(firstDue(now, r.interval, r.rnd))); err != nil {
			return fmt.Errorf("telemetry tick: %w", err)
		}
		return nil
	}
	due, perr := time.Parse(time.RFC3339, st.NextDueAt)
	if perr != nil {
		due = now // a corrupt slot counts as due, and the claim replaces it
	}
	if now.Before(due) {
		return nil
	}
	return r.attempt(ctx, st, c, now)
}

// jitter returns U(0, d).
func (r *Reporter) jitter(d time.Duration) time.Duration {
	return time.Duration(r.rnd(int64(d)))
}

// extWithheld reports whether the extended part is withheld because this
// endpoint answered 400 to it within the last 7 days.
func (r *Reporter) extWithheld(st db.TelemetryState, now time.Time) bool {
	return ExtWithheld(st, r.dest.URL, now)
}

// ExtWithheld reports whether the extended part is withheld from endpoint
// because it answered 400 to it within the last 7 days. The reporter and the
// admin preview both use it, so the preview shows what would be sent.
func ExtWithheld(st db.TelemetryState, endpoint string, now time.Time) bool {
	if st.ExtUnsupportedEndpoint != endpoint {
		return false
	}
	until, err := time.Parse(time.RFC3339, st.ExtUnsupportedUntil)
	return err == nil && now.Before(until)
}

// attempt claims the due slot and, if it won, builds, signs and sends one
// report, then records the outcome.
func (r *Reporter) attempt(ctx context.Context, st db.TelemetryState, c Consent, now time.Time) error {
	claimed := stamp(now.Add(r.interval + r.jitter(r.interval/48)))
	won, err := r.store.ClaimTelemetryDue(ctx, st.NextDueAt, claimed, stamp(now))
	if err != nil {
		return fmt.Errorf("telemetry attempt: %w", err)
	}
	if !won {
		return nil // another replica sent this slot
	}
	rep, err := r.build(ctx, st, c.Extended && !r.extWithheld(st, now), now)
	if err != nil {
		return r.finish(ctx, st, claimed, err)
	}
	status, err := r.send(ctx, rep)
	if err == nil && status == http.StatusBadRequest && rep.Ext != nil {
		// The endpoint doesn't understand the extended part: re-send the
		// basic report once and stop sending ext to it for a week.
		basic := rep
		basic.Ext = nil
		status, err = r.send(ctx, basic)
		if mErr := r.store.SetExtUnsupported(ctx, stamp(now.Add(extFallbackFor)), r.dest.URL); mErr != nil {
			slog.Warn("telemetry fallback marker", "err", mErr)
		}
	} else if err == nil && status == http.StatusConflict && rep.Ext != nil {
		// The receiver says another key claimed this install ID (FR-037):
		// replace the ID, keep the signing secret, and re-send once. A second
		// 409 falls through to the failure below. A 403 never gets here.
		status, err = r.rotateAndResend(ctx, now)
	}
	if err == nil && (status < 200 || status > 299) {
		err = fmt.Errorf("telemetry endpoint returned %d", status)
	}
	return r.finish(ctx, st, claimed, err)
}

// finish records the outcome of a claimed attempt: cause is nil on success.
// On success the next slot is interval plus jitter after completion, so
// reports are at least one interval apart. On failure it backs off
// min(1h*2^n, interval). cause is returned.
func (r *Reporter) finish(ctx context.Context, st db.TelemetryState, claimed string, cause error) error {
	done := r.now()
	res := db.TelemetryResult{ExpectedNext: claimed}
	if cause == nil {
		res.Outcome = outcomeOK
		res.SuccessAt = stamp(done)
		res.NextDue = stamp(done.Add(r.interval + r.jitter(r.interval/48)))
	} else {
		res.Outcome = outcomeFailed
		res.Failures = st.ConsecutiveFailures + 1
		res.NextDue = stamp(done.Add(r.backoff(st.ConsecutiveFailures)))
	}
	if err := r.store.RecordTelemetryResult(ctx, res); err != nil {
		return errors.Join(cause, fmt.Errorf("telemetry attempt: %w", err))
	}
	return cause
}

// backoff is min(1h*2^failures, interval).
func (r *Reporter) backoff(failures int) time.Duration {
	d := baseBackoff
	for i := 0; i < failures && d < r.interval; i++ {
		d *= 2
	}
	return min(d, r.interval)
}

// build collects the report. extended is the gate's extendedOn minus the
// fallback marker; a fresh install is seeded with extended on and no install
// ID yet, so the ID is created here when missing.
func (r *Reporter) build(ctx context.Context, st db.TelemetryState, extended bool, now time.Time) (telemetryschema.Report, error) {
	if extended && st.InstallID == "" {
		id, err := NewInstallID()
		if err != nil {
			return telemetryschema.Report{}, fmt.Errorf("telemetry build: %w", err)
		}
		if err := r.store.SetInstallID(ctx, id); err != nil {
			return telemetryschema.Report{}, fmt.Errorf("telemetry build: %w", err)
		}
	}
	rep, err := BuildReport(ctx, r.deps, extended, now)
	if err != nil {
		return telemetryschema.Report{}, fmt.Errorf("telemetry build: %w", err)
	}
	return rep, nil
}

// BuildReport collects the report for deps at now, with the extended part
// when extended is true. The reporter and the admin preview both call it, so
// the preview is the report that would be sent (SC-006).
func BuildReport(ctx context.Context, deps Deps, extended bool, now time.Time) (telemetryschema.Report, error) {
	deps.Extended = extended
	deps.Now = func() time.Time { return now }
	return Collect(ctx, deps)
}

// rotateAndResend handles a 409 id_claimed: it replaces the install ID (the
// signing secret is kept, so the new ID gets a new, unlinkable key), records
// last_id_rotation_at, rebuilds and re-signs the extended report, and POSTs it
// once. It returns that POST's status.
func (r *Reporter) rotateAndResend(ctx context.Context, now time.Time) (int, error) {
	id, err := NewInstallID()
	if err != nil {
		return 0, fmt.Errorf("telemetry rotate: %w", err)
	}
	if err := r.store.RotateInstallID(ctx, id, stamp(now)); err != nil {
		return 0, fmt.Errorf("telemetry rotate: %w", err)
	}
	rep, err := BuildReport(ctx, r.deps, true, now)
	if err != nil {
		return 0, fmt.Errorf("telemetry rotate: %w", err)
	}
	return r.send(ctx, rep)
}

// send encodes rep and POSTs it, signing the exact body bytes when rep
// carries the extended part (the signature header is absent otherwise,
// FR-038). It returns the HTTP status.
func (r *Reporter) send(ctx context.Context, rep telemetryschema.Report) (int, error) {
	body, err := telemetryschema.Encode(rep)
	if err != nil {
		return 0, fmt.Errorf("telemetry send: %w", err)
	}
	sig := ""
	if rep.Ext != nil {
		secret, err := r.store.EnsureSigningSecret(ctx)
		if err != nil {
			return 0, fmt.Errorf("telemetry send: %w", err)
		}
		priv, err := telemetryschema.DeriveKey(secret, rep.Ext.InstallID)
		if err != nil {
			return 0, fmt.Errorf("telemetry send: %w", err)
		}
		sig = telemetryschema.Sign(priv, body)
	}
	return r.post(ctx, body, sig)
}

// post sends one report and returns the status code. When the destination
// offers proof-of-work challenges (research R21) each request carries a solved
// one: the challenge is fetched fresh for every request, because challenges
// are single use. A 428 gets one new challenge and one resend; a second 428 is
// returned to the caller, which records it as a failed attempt. An error
// means no request got an answer, or the challenge could not be solved.
func (r *Reporter) post(ctx context.Context, body []byte, sig string) (int, error) {
	status := 0
	for range 2 {
		header, err := r.solveChallenge(ctx)
		if err != nil {
			return 0, err
		}
		status, err = r.postOnce(ctx, body, sig, header)
		if err != nil || status != http.StatusPreconditionRequired {
			return status, err
		}
	}
	return status, nil
}

// postOnce sends one request, with the proof-of-work header when pow is not
// empty, and returns the status code.
func (r *Reporter) postOnce(ctx context.Context, body []byte, sig, pow string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.dest.URL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("telemetry post: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.auth != "" {
		req.Header.Set("Authorization", r.auth)
	}
	if sig != "" {
		req.Header.Set(telemetryschema.SignatureHeader, sig)
	}
	if pow != "" {
		req.Header.Set(telemetryschema.PoWHeader, pow)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("telemetry post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	return resp.StatusCode, nil
}

// challengeReply is the body of the provider's GET /v1/challenge.
type challengeReply struct {
	Challenge string `json:"challenge"`
	Bits      int    `json:"bits"`
	ExpiresAt string `json:"expiresAt"`
}

// challengeURL is the endpoint with its last path segment replaced by
// v1/challenge: https://host/prefix/ingest becomes
// https://host/prefix/v1/challenge. A query or fragment is dropped.
func challengeURL(endpoint string) (string, error) {
	base, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("telemetry challenge url: %w", err)
	}
	return base.ResolveReference(&url.URL{Path: "v1/challenge"}).String(), nil
}

// solveChallenge returns the proof-of-work header value for the next
// request, or "" when the destination does not offer challenges (FR-040).
// It fails, without any POST, when the challenge is harder than
// telemetryschema.MaxPoWBits or cannot be solved before it expires. Solving
// runs on the calling goroutine, which is the reporter's own.
func (r *Reporter) solveChallenge(ctx context.Context) (string, error) {
	ch, ok := r.fetchChallenge(ctx)
	if !ok {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("telemetry challenge: %w", err)
		}
		return "", nil
	}
	if ch.Bits < 0 || ch.Bits > telemetryschema.MaxPoWBits {
		return "", fmt.Errorf("telemetry pow: %w: %d bits, the cap is %d", errPoWDifficulty, ch.Bits, telemetryschema.MaxPoWBits)
	}
	left := challengeTTL
	if exp, perr := time.Parse(time.RFC3339, ch.ExpiresAt); perr == nil {
		left = exp.Sub(r.now())
	}
	budget := left - solveMargin
	if budget <= 0 {
		return "", errors.New("telemetry pow: challenge expires too soon to solve")
	}
	solveCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	nonce, err := telemetryschema.SolvePoW(solveCtx, ch.Challenge, ch.Bits)
	if err != nil {
		return "", fmt.Errorf("telemetry pow: %w", err)
	}
	return telemetryschema.FormatPoW(ch.Challenge, nonce), nil
}

// fetchChallenge asks the destination for a challenge. ok is false when it
// offers none: any answer other than a readable 200 means the provider does
// not require proof-of-work, and so does a request that fails (the POST that
// follows meets the same failure and reports it). The request is a plain GET
// without the Authorization header, with a 10-second timeout.
func (r *Reporter) fetchChallenge(ctx context.Context) (ch challengeReply, ok bool) {
	target, err := challengeURL(r.dest.URL)
	if err != nil {
		return ch, false
	}
	cctx, cancel := context.WithTimeout(ctx, challengeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, target, nil)
	if err != nil {
		return ch, false
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return ch, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ch, false
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxChallengeBody)).Decode(&ch); err != nil || ch.Challenge == "" {
		return challengeReply{}, false
	}
	return ch, true
}
