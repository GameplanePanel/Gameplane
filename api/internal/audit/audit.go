// Package audit records every mutating request to the API for later
// review by administrators. Records land in the audit_events table
// and are exposed via /admin/audit.
package audit

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
)

const (
	// MaxAuditPageSize is the maximum number of audit events returned in a single page.
	MaxAuditPageSize = 500
	// DefaultAuditPageSize is the default number of audit events per page when
	// the client doesn't specify a limit or specifies an invalid one.
	DefaultAuditPageSize = 100
)

// webhookEvents counts audit-event webhook deliveries by outcome. A "dropped"
// or "failed" delta is operationally important — it means the external audit
// mirror has a gap — so it's surfaced at /metrics (default registry, served by
// promhttp).
var webhookEvents = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "gameplane_audit_webhook_events_total",
	Help: "Audit-event webhook deliveries by result (sent, failed, dropped).",
}, []string{"result"})

// Auditor records every API call into a tamper-evident log chain.
type Auditor struct {
	db      *db.Store
	sink    *slog.Logger // structured stdout sink; nil disables it
	webhook *WebhookSink // outbound HTTP push sink; nil disables it
	s3      *S3Sink      // S3 batch sink; nil disables it

	// chainMu serializes the read-prev/compute-hash/insert sequence (and the
	// checkpoint-then-delete sequence in Prune) so two requests never read
	// the same "previous row" and fork the chain. See insertChained's doc
	// comment for the single-writer/multi-replica caveat this doesn't cover.
	chainMu sync.Mutex
}

// Option configures an Auditor.
type Option func(*Auditor)

// WithStdoutSink mirrors each audited event to logger as a structured log
// line, so cluster log aggregation (Loki/ELK/CloudWatch scraping the pod's
// stdout) captures the audit trail — the Kubernetes-native "external sink".
// A nil logger leaves the sink disabled (the default; events still land in
// the database).
func WithStdoutSink(logger *slog.Logger) Option {
	return func(a *Auditor) { a.sink = logger }
}

// WithWebhookSink pushes each audited event to an external HTTP endpoint. A nil
// sink leaves it disabled (the default). The sink's worker must be started
// (sink.Start) by the caller; see NewWebhookSink.
func WithWebhookSink(s *WebhookSink) Option {
	return func(a *Auditor) { a.webhook = s }
}

// WithS3Sink batches each audited event into NDJSON objects written to an
// S3-compatible endpoint. A nil sink leaves it disabled (the default). The
// sink's worker must be started (sink.Start) by the caller; see NewS3Sink.
func WithS3Sink(s *S3Sink) Option {
	return func(a *Auditor) { a.s3 = s }
}

// webhookBuffer bounds how many unsent events the webhook sink holds before it
// starts dropping. Audit events are low-rate (one per mutating request), so a
// healthy endpoint never approaches this; the bound exists so a stalled
// endpoint can't grow memory without limit.
const webhookBuffer = 1024

// WebhookSink mirrors each audit event to an external HTTP endpoint by POSTing
// it as JSON. Delivery is best-effort and fully decoupled from the request
// path: events are handed to a bounded buffer and shipped by a single
// background worker, so a slow or unreachable endpoint never blocks — or fails
// — an audited request. The database stays the source of truth; this is the
// same "mirror, don't gate" contract as the stdout sink, just pushed rather
// than scraped.
type WebhookSink struct {
	url    string
	auth   string // optional Authorization header value; "" omits the header
	client *http.Client
	ch     chan Event
}

// NewWebhookSink returns a sink that POSTs audit events as JSON to url.
// authHeader, when non-empty, is sent verbatim as the Authorization header
// (e.g. "Bearer <token>"). Call Start to launch the delivery worker.
func NewWebhookSink(url, authHeader string) *WebhookSink {
	return &WebhookSink{
		url:    url,
		auth:   authHeader,
		client: &http.Client{Timeout: 5 * time.Second},
		ch:     make(chan Event, webhookBuffer),
	}
}

// Enqueue hands an event to the worker without ever blocking. When the buffer
// is full (a stalled endpoint backing up), the event is dropped and counted —
// a dropped mirror leaves a hole in the external trail, so it must be visible.
func (s *WebhookSink) Enqueue(e Event) {
	select {
	case s.ch <- e:
	default:
		webhookEvents.WithLabelValues("dropped").Inc()
	}
}

// Start runs the delivery worker until ctx is cancelled, then best-effort
// drains whatever is already buffered within a short deadline. It blocks; run
// it in a goroutine.
func (s *WebhookSink) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			s.drain(ctx)
			return
		case e := <-s.ch:
			s.post(ctx, e)
		}
	}
}

// drain ships already-buffered events on shutdown, bounded by a short deadline
// so a wedged endpoint can't stall process exit.
func (s *WebhookSink) drain(ctx context.Context) {
	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	for {
		select {
		case e := <-s.ch:
			s.post(drainCtx, e)
		case <-drainCtx.Done():
			return
		default:
			return
		}
	}
}

// post delivers one event. It uses context.WithoutCancel to detach from the
// worker's lifecycle context: at shutdown the select in Start can still pick a
// buffered event after ctx is cancelled, and a cancelled context would fail
// that delivery even though the event could have been shipped. The client
// timeout still bounds each attempt.
//
// context.WithoutCancel strips any deadline as well as cancellation, so a
// bare WithoutCancel(ctx) here would silently undo drain's 2s budget on the
// shutdown path. Re-apply ctx's own deadline (if any) to the detached
// context so drain's bound still holds; the normal Start path has no
// deadline on ctx, so this is a no-op there.
func (s *WebhookSink) post(ctx context.Context, e Event) {
	reqCtx := context.WithoutCancel(ctx)
	if dl, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(reqCtx, time.Until(dl))
		defer cancel()
	}
	body, err := json.Marshal(webhookPayload{
		TS: e.TS, Actor: e.Actor, Method: e.Method, Path: e.Path,
		Target: e.Target, Status: e.Status, IP: e.IP, Reason: e.Reason,
	})
	if err != nil {
		webhookEvents.WithLabelValues("failed").Inc()
		slog.Warn("audit webhook marshal failed", "err", err)
		return
	}
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		webhookEvents.WithLabelValues("failed").Inc()
		slog.Warn("audit webhook build request failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.auth != "" {
		req.Header.Set("Authorization", s.auth)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		webhookEvents.WithLabelValues("failed").Inc()
		slog.Warn("audit webhook post failed", "err", err, "url", s.url)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		webhookEvents.WithLabelValues("failed").Inc()
		slog.Warn("audit webhook non-2xx", "status", resp.StatusCode, "url", s.url)
		return
	}
	webhookEvents.WithLabelValues("sent").Inc()
}

// webhookPayload is the JSON shipped to the webhook: the audit event minus the
// database row id, which isn't known at emit time and is meaningless to an
// external sink (which keys on ts/actor/path).
type webhookPayload struct {
	TS     string `json:"ts"`
	Actor  string `json:"actor"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Target string `json:"target,omitempty"`
	Status int    `json:"status"`
	IP     string `json:"ip,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// New returns an Auditor configured with options.
func New(store *db.Store, opts ...Option) *Auditor {
	a := &Auditor{db: store}
	for _, o := range opts {
		o(a)
	}
	return a
}

// ---- tamper-evidence (hash chain) ----
//
// Every row inserted after migration 005 carries prev_hash (the previous
// row's hash) and hash = SHA-256(prev_hash || canonical(row)). A DB-level
// UPDATE changes a row's content without recomputing hash, and a DELETE
// removes a link entirely — both break the chain, which Verify detects by
// re-walking it and recomputing each hash. Rows written before migration 005
// have NULL prev_hash/hash; the chain simply starts fresh at the first row
// inserted afterward (see previousHash).
//
// Walking the surviving chain alone has a blind spot: deleting the newest
// row(s) (`DELETE FROM audit_events WHERE id > N`) leaves every remaining
// row's prev_hash/hash link intact, since nothing downstream of the deleted
// rows exists to notice they're gone. audit.head (see below) closes that
// case by anchoring the chain's far end the same way audit.checkpoint
// anchors its near end.
//
// Neither anchor defeats an attacker with DB write access who also
// recomputes and rewrites them — see the threat-model note in
// docs/security.md. This mechanism is aimed at naive in-DB tampering
// (UPDATE/DELETE that doesn't also patch the config table) and accidental
// corruption, not a knowledgeable adversary.

// chainConfigKey is the config-table key Prune writes a checkpoint under
// before deleting rows, so Verify can resume the chain across a retention
// sweep instead of needing every historical row kept forever.
const chainConfigKey = "audit.checkpoint"

// chainCheckpoint is the JSON value stored under chainConfigKey: the id and
// hash of the newest row a retention sweep is about to delete. Verify starts
// just after ID, trusting Hash as the prev_hash of the first surviving row —
// it doesn't reappear in the table, but its hash needs to be remembered for
// the link into what does survive to still check out.
type chainCheckpoint struct {
	ID   int64  `json:"id"`
	Hash string `json:"hash"`
}

// headConfigKey is the config-table key insertChained upserts, in the same
// transaction as every row insert, recording the newest row's id + hash.
//
// It exists to catch tail truncation: a DELETE that removes only the newest
// row(s) leaves every surviving row's prev_hash link intact, so walking the
// chain from audit.checkpoint alone reports no break. The head anchors the
// chain's *new* boundary the same way audit.checkpoint anchors the *old* one
// (see Prune) — together they bound the surviving rows on both ends. Prune
// only ever deletes rows older than its cutoff and writes chainConfigKey,
// never headConfigKey, so the two coexist without either clobbering the
// other.
const headConfigKey = "audit.head"

// chainHead is the JSON value stored under headConfigKey: the id and hash of
// the newest row as of the most recent insert.
type chainHead struct {
	LastID int64  `json:"lastID"`
	Hash   string `json:"hash"`
}

// writeConfigTx upserts value under key inside tx, using the same
// insert-or-update-on-conflict shape as every other config-table write.
func writeConfigTx(ctx context.Context, tx *sql.Tx, key, value string) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO config(key, value, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		     value      = excluded.value,
		     updated_at = excluded.updated_at`,
		key, value, db.NowTimestamp(),
	)
	return err
}

// canonicalize serializes the chained fields of an event into an
// unambiguous byte string. Each field is prefixed with its own length so
// that no delimiter embedded in attacker-influenced content (path, actor,
// ip, ...) can make two different rows canonicalize to the same bytes.
//
// The Reason field is included in the hash only when non-empty. This maintains
// backward compatibility with pre-migration rows (which have NULL/empty Reason):
// their hash was computed without Reason, and Verify must reproduce that
// bit-for-bit. A non-empty Reason changes the hash, so two rows differing
// only in Reason will hash differently (after the migration 007_audit_reason.sql
// lands). This preserves both the integrity of pre-migration rows and the
// security of post-migration ones.
func canonicalize(e Event) []byte {
	var buf bytes.Buffer
	field := func(s string) {
		fmt.Fprintf(&buf, "%d:", len(s))
		buf.WriteString(s)
	}
	field(e.TS)
	field(e.Actor)
	field(e.Method)
	field(e.Path)
	field(e.Target)
	field(strconv.Itoa(e.Status))
	field(e.IP)
	// Only include Reason in the canonical form if it is non-empty.
	// Empty Reason must serialize identically to pre-migration rows (no Reason field at all).
	if e.Reason != "" {
		field(e.Reason)
	}
	return buf.Bytes()
}

// computeHash chains prevHash into the SHA-256 of the canonicalized row, so
// altering any field of the row, or splicing in a different predecessor,
// changes the result.
func computeHash(prevHash string, e Event) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write(canonicalize(e))
	return hex.EncodeToString(h.Sum(nil))
}

// insertChained appends one row to audit_events, chaining it to the
// previous row's hash so a later DB-level UPDATE or DELETE against
// audit_events is detectable via Verify. It also upserts audit.head (see
// headConfigKey) in the same transaction, so the newest-row anchor Verify
// checks against is never out of sync with what was actually committed.
//
// Single-writer assumption: chainMu serializes the read-prev/compute/insert
// sequence against other requests in this process. That's correct for a
// single API replica — the only deployment topology this covers today (see
// docs/architecture.md). A multi-replica deployment would let two processes
// each read the same "previous row" concurrently and insert two rows
// chained to the same prev_hash, silently forking the chain; making that
// safe needs a DB-level serialization point (e.g. a Postgres advisory lock
// keyed on the audit_events table) in addition to this in-process mutex.
func (a *Auditor) insertChained(ctx context.Context, ts, actor, method, path, target, reason string, status int, ip string) error {
	a.chainMu.Lock()
	defer a.chainMu.Unlock()

	tx, err := a.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin audit tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	prevHash, err := a.previousHash(ctx, tx)
	if err != nil {
		return fmt.Errorf("read previous audit hash: %w", err)
	}

	e := Event{TS: ts, Actor: actor, Method: method, Path: path, Target: target, Status: status, IP: ip, Reason: reason}
	hash := computeHash(prevHash, e)

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO audit_events(ts, actor, method, path, target, status, ip, reason, prev_hash, hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ts, actor, method, path, target, status, ip, reason, prevHash, hash,
	); err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}

	// Read back the id of the row just inserted rather than relying on
	// driver-specific LastInsertId support (pgx's stdlib driver doesn't
	// implement it): this SELECT runs inside the same tx, guarded by
	// chainMu, so it can only see the row this call just committed.
	var newID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id FROM audit_events ORDER BY id DESC LIMIT 1`,
	).Scan(&newID); err != nil {
		return fmt.Errorf("read new audit row id: %w", err)
	}

	head, err := json.Marshal(chainHead{LastID: newID, Hash: hash})
	if err != nil {
		return fmt.Errorf("encode audit head: %w", err)
	}
	if err := writeConfigTx(ctx, tx, headConfigKey, string(head)); err != nil {
		return fmt.Errorf("write audit head: %w", err)
	}

	return tx.Commit()
}

// previousHash returns the hash to chain the next insert from: the current
// latest row's hash, the latest retention checkpoint's hash if the table has
// been pruned to empty (or the latest row predates this feature and carries
// no hash), or "" (genesis) if neither exists yet.
func (a *Auditor) previousHash(ctx context.Context, tx *sql.Tx) (string, error) {
	var hash sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT hash FROM audit_events ORDER BY id DESC LIMIT 1`,
	).Scan(&hash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Empty table: fresh install, or every row was just pruned. Either
		// way, fall through to the checkpoint below.
	case err != nil:
		return "", err
	default:
		if hash.Valid && hash.String != "" {
			return hash.String, nil
		}
		// The latest row predates this feature (migrated in with a NULL
		// hash): the chain restarts here at genesis.
		return "", nil
	}

	raw, ok, err := db.ConfigValueTx(ctx, tx, chainConfigKey)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	var cp chainCheckpoint
	if err := json.Unmarshal([]byte(raw), &cp); err != nil {
		return "", fmt.Errorf("decode audit checkpoint: %w", err)
	}
	return cp.Hash, nil
}

// VerifyResult is the outcome of walking the audit_events hash chain.
type VerifyResult struct {
	OK         bool   `json:"ok"`
	FirstBadID int64  `json:"firstBadId,omitempty"`
	Checked    int64  `json:"checked"`
	Message    string `json:"message"`
}

// Verify walks the audit_events hash chain from the latest retention
// checkpoint (or genesis, if the table has never been pruned), recomputing
// each row's hash. It checks both that a row's stored hash matches its
// current content (an UPDATE would change the content without updating the
// hash) and that its prev_hash matches the previous row's hash (a DELETE, or
// an inserted/reordered row, breaks that link). It stops and reports at the
// first break; rows written before migration 005 (NULL hash) are treated as
// pre-chain history, not breaks.
//
// Walking the chain can't see a truncated tail on its own (nothing survives
// to complain about a missing successor), so after the walk it separately
// checks audit.head — see verifyHead.
func (a *Auditor) Verify(ctx context.Context) (VerifyResult, error) {
	expectedPrev := ""
	var afterID int64

	if raw, ok, err := a.db.ConfigValue(ctx, chainConfigKey); err != nil {
		return VerifyResult{}, fmt.Errorf("load audit checkpoint: %w", err)
	} else if ok {
		var cp chainCheckpoint
		if err := json.Unmarshal([]byte(raw), &cp); err != nil {
			return VerifyResult{}, fmt.Errorf("decode audit checkpoint: %w", err)
		}
		expectedPrev = cp.Hash
		afterID = cp.ID
	}

	rows, err := a.db.DB.QueryContext(ctx,
		`SELECT id, ts, actor, method, path, COALESCE(target,''), status, COALESCE(ip,''), COALESCE(reason,''), COALESCE(prev_hash,''), COALESCE(hash,'')
		 FROM audit_events
		 WHERE id > ? AND hash IS NOT NULL AND hash <> ''
		 ORDER BY id ASC`, afterID,
	)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("query audit chain: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var checked int64
	for rows.Next() {
		var e Event
		var prevHash, hash string
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Method, &e.Path, &e.Target, &e.Status, &e.IP, &e.Reason, &prevHash, &hash); err != nil {
			return VerifyResult{}, fmt.Errorf("scan audit row: %w", err)
		}
		checked++
		if prevHash != expectedPrev {
			return VerifyResult{
				OK: false, FirstBadID: e.ID, Checked: checked,
				Message: fmt.Sprintf("row %d: prev_hash does not match the previous row's hash (a row was inserted, deleted, or reordered)", e.ID),
			}, nil
		}
		if want := computeHash(prevHash, e); want != hash {
			return VerifyResult{
				OK: false, FirstBadID: e.ID, Checked: checked,
				Message: fmt.Sprintf("row %d: stored hash does not match its recomputed content (the row was modified)", e.ID),
			}, nil
		}
		expectedPrev = hash
	}
	if err := rows.Err(); err != nil {
		return VerifyResult{}, fmt.Errorf("iterate audit chain: %w", err)
	}
	// Release the connection before verifyHead issues its own query: sqlite
	// is opened with SetMaxOpenConns(1) (see db.Open), so a second query
	// while rows is still open would deadlock waiting for a connection the
	// still-open rows is holding. Close is idempotent, so the deferred call
	// above stays as a safety net for the early returns above.
	if err := rows.Close(); err != nil {
		return VerifyResult{}, fmt.Errorf("close audit chain rows: %w", err)
	}

	if brk, err := a.verifyHead(ctx, afterID, checked); err != nil {
		return VerifyResult{}, err
	} else if brk != nil {
		return *brk, nil
	}

	return VerifyResult{OK: true, Checked: checked, Message: "audit chain intact"}, nil
}

// verifyHead checks the audit.head anchor written by insertChained, closing
// the gap the chain walk above can't see: DELETE FROM audit_events WHERE
// id > N removes only the newest row(s), so every surviving row's prev_hash
// link still checks out and the walk reports no break. The head remembers
// what the newest row was as of the last insert; if that row is now gone,
// or its content no longer matches, the tail was truncated (or otherwise
// altered) after the fact.
//
// checkpointID is the current audit.checkpoint boundary (0 if none). A head
// at or before that boundary refers to a row a legitimate Prune has since
// checkpointed and removed — the checkpoint itself already vouches for that
// row, so it is not treated as a break (this is what makes a full-table
// prune, which deletes even the row the head points at, a non-event rather
// than a false positive).
func (a *Auditor) verifyHead(ctx context.Context, checkpointID, checked int64) (*VerifyResult, error) {
	raw, ok, err := a.db.ConfigValue(ctx, headConfigKey)
	if err != nil {
		return nil, fmt.Errorf("load audit head: %w", err)
	}
	if !ok {
		// Fresh install, or a legacy DB with no post-migration insert yet:
		// nothing to anchor against.
		return nil, nil
	}
	var head chainHead
	if err := json.Unmarshal([]byte(raw), &head); err != nil {
		return nil, fmt.Errorf("decode audit head: %w", err)
	}
	if head.LastID <= checkpointID {
		return nil, nil
	}

	var hash string
	err = a.db.DB.QueryRowContext(ctx,
		`SELECT COALESCE(hash,'') FROM audit_events WHERE id = ?`, head.LastID,
	).Scan(&hash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return &VerifyResult{
			OK: false, FirstBadID: head.LastID, Checked: checked,
			Message: fmt.Sprintf("row %d: recorded as the chain's newest row but is now missing (the tail was truncated)", head.LastID),
		}, nil
	case err != nil:
		return nil, fmt.Errorf("read audit head row: %w", err)
	}
	if hash != head.Hash {
		return &VerifyResult{
			OK: false, FirstBadID: head.LastID, Checked: checked,
			Message: fmt.Sprintf("row %d: recorded as the chain's newest row but its hash no longer matches (the row was modified)", head.LastID),
		}, nil
	}
	return nil, nil
}

// recordedKey is the context key for the per-request "already audited" flag.
type recordedKey struct{}

// MarkRecorded tells Middleware that the handler has already written this
// request's audit row itself (via Auditor.WriteSync), so the generic row must
// not be added on top of it. Call it only after WriteSync returned nil: if the
// explicit write failed, leaving the flag unset lets Middleware record the
// request as a fallback instead of leaving a hole in the trail. It is a no-op
// on a context that did not pass through Middleware.
func MarkRecorded(ctx context.Context) {
	if f, ok := ctx.Value(recordedKey{}).(*atomic.Bool); ok {
		f.Store(true)
	}
}

// Middleware logs every mutating request after the handler returns.
// Reads and health probes are skipped to keep the audit log signal-dense.
// A request whose handler called MarkRecorded is skipped too: its own
// synchronous row (which also carries the failure reason and the composite
// target) is the single record of that request.
func Middleware(a *Auditor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Install an actor holder before the chain runs. Authenticate
			// sets it on this same context; the user it puts on a child
			// context never propagates back up here, which is why audit
			// rows used to record "anonymous" for authenticated actions.
			ctx, holder := auth.WithActorHolder(req.Context())
			recorded := new(atomic.Bool)
			ctx = context.WithValue(ctx, recordedKey{}, recorded)
			req = req.WithContext(ctx)
			rw := &responseRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rw, req)
			if !shouldLog(req) || recorded.Load() {
				return
			}
			actor := "anonymous"
			if name := holder.Name(); name != "" {
				actor = name
			} else if u := auth.UserFromContext(ctx); u != nil && u.Username != "" {
				// Fallback for callers that put the user directly on this
				// context instead of via the actor holder. In the normal
				// chain Authenticate fills the holder, so this never overrides
				// it; in production the authenticated user lives on a child
				// context the audit middleware can't see, so this stays nil.
				actor = u.Username
			}
			target := req.URL.Query().Get("name")
			// Stamp once so the DB row, stdout line, and webhook payload all
			// agree on the event time.
			ts := time.Now().UTC().Format(time.RFC3339)

			// Extract the client IP from context (set by ClientIPFromXFF middleware).
			// Fall back to the host portion of RemoteAddr if not set, so audit
			// records stay consistent when the middleware is absent (e.g., in tests).
			clientIP := middleware.GetClientIP(ctx)
			if clientIP == "" {
				// Fallback: extract host from RemoteAddr. net.SplitHostPort handles
				// both IPv4:port and [IPv6]:port forms correctly; for bare addresses
				// (no port), it returns an error and we use the original.
				host, _, err := net.SplitHostPort(req.RemoteAddr)
				if err != nil {
					// No port separator found, use the whole RemoteAddr as the client IP.
					clientIP = req.RemoteAddr
				} else {
					clientIP = host
				}
			}

			// Redact share link tokens from the path for audit logging.
			// Share tokens are credentials; record the request structure without the token.
			path := redactShareToken(req.URL.Path)

			// WithoutCancel: the audit write must survive the request context being
			// cancelled, or a client disconnect would silently punch a hole in the trail.
			if err := a.insertChained(context.WithoutCancel(ctx), ts, actor, req.Method, path, target, "", rw.status, clientIP); err != nil {
				// A dropped security-audit write must not be silent — surface it
				// so an operator notices the trail has a hole.
				slog.Warn("audit insert failed",
					"err", err, "actor", actor, "method", req.Method, "path", path)
			}
			if a.sink != nil {
				a.sink.Info("audit",
					"actor", actor, "method", req.Method, "path", path,
					"target", target, "status", rw.status, "ip", clientIP)
			}
			if a.webhook != nil {
				a.webhook.Enqueue(Event{
					TS: ts, Actor: actor, Method: req.Method, Path: path,
					Target: target, Status: rw.status, IP: clientIP,
				})
			}
			if a.s3 != nil {
				a.s3.Enqueue(Event{
					TS: ts, Actor: actor, Method: req.Method, Path: path,
					Target: target, Status: rw.status, IP: clientIP,
				})
			}
		})
	}
}

// WriteSync performs a synchronous audit write directly from handler code,
// before the HTTP response is returned to the client. It is used by routes
// that cannot rely on the generic Middleware (e.g., routes with path
// parameters instead of query strings, or routes that need the audit event
// written before the response is sent per FR-006).
//
// WriteSync accepts the audit parameters: HTTP method, request path,
// composite target (server name or "server:captureId" for capture operations),
// a machine-readable reason for failures (empty on success), and the HTTP
// status code. Actor and client IP are extracted from the request context
// (set by Authenticate and ClientIPFromXFF middleware, respectively).
//
// On database write failure, WriteSync logs a warning with slog.Warn and
// returns the error. Handlers must treat audit-write failures as non-fatal:
// log the error (or rely on WriteSync's internal warning), but do not fail
// the capture operation itself. The operation's actual HTTP response (status,
// body) is returned to the client regardless of audit-write success.
func (a *Auditor) WriteSync(ctx context.Context, method, path, target, reason string, status int) error {
	// Extract actor from context (set by auth.WithActorHolder).
	actor := "anonymous"
	if u := auth.UserFromContext(ctx); u != nil && u.Username != "" {
		actor = u.Username
	}

	// Extract client IP from context (set by ClientIPFromXFF middleware).
	// WriteSync relies on the client IP already being in the context; records
	// an empty IP if it is not.
	clientIP := middleware.GetClientIP(ctx)

	// Generate timestamp once so the audit row has a consistent time.
	ts := time.Now().UTC().Format(time.RFC3339)

	// WithoutCancel: the audit write must survive the request context being
	// cancelled, or a client disconnect would silently punch a hole in the trail.
	if err := a.insertChained(context.WithoutCancel(ctx), ts, actor, method, path, target, reason, status, clientIP); err != nil {
		// A dropped security-audit write must not be silent — surface it
		// so an operator notices the trail has a hole. The caller does not
		// fail the operation; this log line is the only signal.
		slog.Warn("audit write failed",
			"err", err, "actor", actor, "method", method, "path", path)
		return fmt.Errorf("audit write: %w", err)
	}

	// Best-effort fan-out to external sinks (same as Middleware).
	if a.sink != nil {
		a.sink.Info("audit",
			"actor", actor, "method", method, "path", path,
			"target", target, "status", status, "ip", clientIP, "reason", reason)
	}
	if a.webhook != nil {
		a.webhook.Enqueue(Event{
			TS: ts, Actor: actor, Method: method, Path: path,
			Target: target, Status: status, IP: clientIP, Reason: reason,
		})
	}
	if a.s3 != nil {
		a.s3.Enqueue(Event{
			TS: ts, Actor: actor, Method: method, Path: path,
			Target: target, Status: status, IP: clientIP, Reason: reason,
		})
	}

	return nil
}

func shouldLog(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	switch {
	case req.URL.Path == "/healthz", req.URL.Path == "/metrics":
		return false
	case strings.HasPrefix(req.URL.Path, "/auth/oidc/"):
		// Login events are audited only on success via the session
		// creation path; OIDC callbacks themselves are too noisy.
		return false
	}
	return true
}

// redactShareToken replaces the share link token in paths like /shares/{token}
// and /shares/{token}/start with a placeholder, so audit logs record the
// request structure without exposing the credential.
func redactShareToken(path string) string {
	if !strings.HasPrefix(path, "/shares/") {
		return path
	}
	// Extract everything after /shares/ (the token and any trailing path).
	rest := strings.TrimPrefix(path, "/shares/")
	// Find the first / to separate token from trailing path.
	slashIdx := strings.IndexByte(rest, '/')
	if slashIdx < 0 {
		// No trailing path, just /shares/{token}
		return "/shares/<token>"
	}
	// /shares/{token}/something — preserve the trailing path
	return "/shares/<token>" + rest[slashIdx:]
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Hijack forwards to the underlying writer. Without this, the embedded
// interface hides the concrete writer's Hijacker and every WebSocket
// upgrade behind this middleware fails with 501 (websocket.Accept
// type-asserts http.Hijacker).
func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("audit: underlying ResponseWriter does not implement http.Hijacker")
	}
	return hj.Hijack()
}

// Flush forwards so streaming responses keep working through the
// recorder.
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ---- query API ----

// Event is an audit log entry.
type Event struct {
	ID     int64  `json:"id"`
	TS     string `json:"ts"`
	Actor  string `json:"actor"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Target string `json:"target,omitempty"`
	Status int    `json:"status"`
	IP     string `json:"ip,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// Page returns the most recent events, oldest-first within the page.
// `before` is an ID cursor (exclusive); 0 means "from latest".
func (a *Auditor) Page(req *http.Request, limit int, before int64) ([]Event, error) {
	// Clamp the limit into a new variable so taint analysis recognizes it as bounded.
	pageSize := limit
	if pageSize <= 0 || pageSize > MaxAuditPageSize {
		pageSize = DefaultAuditPageSize
	}
	rows, err := a.db.DB.QueryContext(req.Context(),
		`SELECT id, ts, actor, method, path, COALESCE(target,''), status, COALESCE(ip,''), COALESCE(reason,'')
		 FROM audit_events
		 WHERE (? = 0 OR id < ?)
		 ORDER BY id DESC
		 LIMIT ?`, before, before, pageSize,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]Event, 0, pageSize)
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Method, &e.Path, &e.Target, &e.Status, &e.IP, &e.Reason); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// StreamFilter narrows an export. The zero value streams everything.
//
// It deliberately mirrors the dashboard's audit-page filters so an export
// contains exactly the rows the operator is looking at — the page filters
// client-side over the pages it has scrolled, which is a different (and
// smaller) set than the table holds.
type StreamFilter struct {
	Since  string // RFC3339 lower bound, inclusive; "" = unbounded
	Until  string // RFC3339 upper bound, inclusive; "" = unbounded
	Actor  string // case-insensitive substring; "" = any
	Method string // exact HTTP method; "" = any
	// StatusMin/StatusMax bound the HTTP status inclusively. StatusMax == 0 means no status filter.
	StatusMin int
	StatusMax int
}

// likeEscape neutralizes the LIKE wildcards so an actor containing "%" or
// "_" matches literally instead of broadening the export.
func likeEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// Stream invokes fn for every audit event matching f, oldest-first. It
// iterates rows rather than buffering, so the whole table can be exported
// without holding it in memory. ts is stored as fixed-width RFC3339 (see
// Middleware), so the lexicographic comparison is chronological — no per-row
// parsing, and it stays portable across the sqlite and pgx drivers.
func (a *Auditor) Stream(ctx context.Context, f StreamFilter, fn func(Event) error) error {
	actorPattern := "%" + likeEscape(strings.ToLower(f.Actor)) + "%"
	rows, err := a.db.DB.QueryContext(ctx,
		`SELECT id, ts, actor, method, path, COALESCE(target,''), status, COALESCE(ip,''), COALESCE(reason,'')
		 FROM audit_events
		 WHERE (? = '' OR ts >= ?) AND (? = '' OR ts <= ?)
		   AND (? = '' OR LOWER(actor) LIKE ? ESCAPE '\')
		   AND (? = '' OR method = ?)
		   AND (? = 0 OR (status >= ? AND status <= ?))
		 ORDER BY id ASC`,
		f.Since, f.Since, f.Until, f.Until,
		f.Actor, actorPattern,
		f.Method, f.Method,
		f.StatusMax, f.StatusMin, f.StatusMax,
	)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Method, &e.Path, &e.Target, &e.Status, &e.IP, &e.Reason); err != nil {
			return err
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ---- retention ----

// Prune deletes audit events whose ts predates the cutoff and returns the
// number of rows removed. ts is stored as RFC3339 (see Middleware), a
// fixed-width zero-padded UTC format, so a lexicographic "<" comparison is
// also a chronological one — no per-row time parsing needed, and the query
// stays portable across the sqlite and postgres drivers.
//
// Before deleting, it checkpoints the newest row about to be removed (see
// chainCheckpoint) so Verify can still validate the hash-chain link into the
// first surviving row after the boundary row itself is gone.
func (a *Auditor) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	cutoffStr := cutoff.UTC().Format(time.RFC3339)

	// Same in-process serialization as insertChained: checkpointing and
	// deleting must be atomic with respect to a concurrent insert reading
	// "the previous row's hash", or that insert could read a row that's
	// about to be pruned out from under it.
	a.chainMu.Lock()
	defer a.chainMu.Unlock()

	tx, err := a.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin prune tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var boundaryID int64
	var boundaryHash sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT id, hash FROM audit_events WHERE ts < ? ORDER BY id DESC LIMIT 1`,
		cutoffStr,
	).Scan(&boundaryID, &boundaryHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Nothing to prune.
		return 0, tx.Commit()
	case err != nil:
		return 0, fmt.Errorf("find prune boundary: %w", err)
	}

	// A NULL boundary hash means every row up to the cutoff predates the
	// chain (migration 005 hasn't shipped a hash for anything this old yet);
	// there's no live link to preserve, so skip the checkpoint write.
	//
	// This only ever writes chainConfigKey ("audit.checkpoint"), never
	// headConfigKey ("audit.head") — the two anchors bound the surviving
	// chain from opposite ends and must not clobber each other. Pruning
	// never touches the head even when it deletes the row the head
	// currently points at (a full-table prune): Verify treats a head at or
	// before the checkpoint boundary as already covered by the checkpoint,
	// not as tail truncation (see verifyHead).
	if boundaryHash.Valid && boundaryHash.String != "" {
		cp, mErr := json.Marshal(chainCheckpoint{ID: boundaryID, Hash: boundaryHash.String})
		if mErr != nil {
			return 0, fmt.Errorf("encode audit checkpoint: %w", mErr)
		}
		if err := writeConfigTx(ctx, tx, chainConfigKey, string(cp)); err != nil {
			return 0, fmt.Errorf("write audit checkpoint: %w", err)
		}
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE ts < ?`, cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("prune audit events: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune audit events: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit prune: %w", err)
	}
	return n, nil
}

// RunRetention periodically prunes audit events older than the retention
// window, blocking until ctx is cancelled. A sweep runs immediately on start
// (a long-lived process shouldn't wait a full interval to first prune) and
// then every interval. A retention of zero or less disables retention and
// returns immediately, preserving the keep-forever default.
func (a *Auditor) RunRetention(ctx context.Context, retention, interval time.Duration, logger *slog.Logger) {
	if retention <= 0 {
		return
	}
	sweep := func() {
		cutoff := time.Now().Add(-retention)
		n, err := a.Prune(ctx, cutoff)
		if err != nil {
			logger.Warn("audit retention sweep failed", "err", err)
			return
		}
		if n > 0 {
			logger.Info("audit retention sweep",
				"deleted", n, "olderThan", cutoff.UTC().Format(time.RFC3339))
		}
	}
	sweep()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}

// WriteJSON is a convenience for handlers.
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
