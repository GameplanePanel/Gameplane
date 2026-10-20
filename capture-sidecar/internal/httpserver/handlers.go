// Package httpserver provides the mTLS-authenticated HTTP control plane
// for the capture sidecar: starting/stopping captures, polling status,
// and downloading completed PCAPNG files.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/GameplanePanel/gameplane/capture-sidecar/internal/capture"
)

const (
	// captureInterface is the network interface the sidecar captures from.
	// Game pods have exactly one pod-network interface, so this is fixed
	// rather than request-configurable; the PCAPNG file's interface block
	// documents it under this name (see capture.NewWriter).
	captureInterface = "eth0"

	// maxDurationSecondsLimit is the sidecar's own outer bound on a capture's
	// runtime. The API tier caps requests far lower, but the sidecar is a
	// separate trust boundary and must not accept a value that overflows
	// int64 nanoseconds when multiplied by time.Second: that yields a negative
	// duration, and time.AfterFunc fires a negative duration immediately, so
	// an absurdly large request would silently produce a zero-length capture.
	maxDurationSecondsLimit = 7 * 24 * 60 * 60

	// stopDrainTimeout bounds how long HandleStop waits for the packet-reading
	// goroutine to exit. WriteTimeout is deliberately unset on this server so
	// multi-GiB downloads can stream, which means an unbounded wait here would
	// hang the request forever with no diagnostic if the read loop ever failed
	// to observe cancellation.
	stopDrainTimeout = 5 * time.Second

	// maxCompletedRetained bounds the number of finished captures whose
	// terminal state is kept in memory for status/stop replay. Only one
	// capture runs at a time and the retention reconciler deletes the files,
	// so a small ring is ample; it exists purely so a long-lived pod cannot
	// accumulate state without bound.
	maxCompletedRetained = 16
)

// Capture status values reported in the `status` field of every response.
// The operator's NetworkCapture reconciler switches on exactly these strings,
// so they must not drift.
const (
	statusRunning   = "running"
	statusCompleted = "completed"
	statusFailed    = "failed"
)

// Stopping reasons reported in `stoppingReason`. The size- and duration-limit
// reasons are produced by capture.Writer itself and read back through
// LimitReason, so they are deliberately not duplicated here.
const (
	reasonUserRequested = "user_requested"
	reasonMaxDuration   = "max_duration_reached"
	reasonDiskFull      = "disk_full"
	reasonError         = "error"
)

// captureIDPattern bounds a capture id to a conservative charset before it is
// ever used to build a filesystem path. Ids are operator-generated
// (`cap-<hex>`), so nothing exotic has any business reaching the filesystem.
var captureIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// captureState holds the state of a single capture session.
// BytesWritten/PacketsWritten are deliberately not duplicated here: the
// underlying capture.Writer already tracks them under its own mutex, and is
// the single source of truth read by HandleStatus/HandleStop.
type captureState struct {
	// Immutable after construction; safe to read without the mutex.
	id              string
	serverUID       string
	captureUID      string
	startedAt       time.Time
	maxDurationSecs int64
	maxSizeBytes    int64
	filePath        string

	writer capture.PacketWriter
	cancel context.CancelFunc
	done   chan struct{}
	timer  *time.Timer

	// mu guards the mutable fields below. finish writes them from whichever
	// path ends the capture (the duration timer, the packet-reading goroutine,
	// or a stop request) while status and stop responses read them, so both
	// sides must hold it: CI runs `go test -race`.
	mu             sync.Mutex
	status         string
	completedAt    time.Time
	stoppingReason string
	message        string
}

// captureSnapshot is a value copy of the mutable half of captureState, taken
// under its mutex so a single response is always internally consistent.
type captureSnapshot struct {
	status         string
	completedAt    time.Time
	stoppingReason string
	message        string
}

// snapshot copies the mutable fields under the capture's mutex. Every read of
// those fields goes through it, so no handler ever touches them unguarded.
func (c *captureState) snapshot() captureSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return captureSnapshot{
		status:         c.status,
		completedAt:    c.completedAt,
		stoppingReason: c.stoppingReason,
		message:        c.message,
	}
}

// Server manages the capture control plane.
type Server struct {
	mu               sync.Mutex
	currentCapture   *captureState
	finishingCapture *captureState
	completed        map[string]*captureState
	completedOrder   []string

	captureDataDir string
	serverUID      string

	// budgetBytes is the maximum total bytes (already-retained capture files
	// on captureDataDir, plus one new capture's own maxSizeBytes) HandleStart
	// will allow before refusing to start (F-187: the volume this directory
	// lives on is a size-limited emptyDir, and retained files that are never
	// counted here can silently push total usage past that limit and get the
	// whole pod evicted). Zero disables the check - used by tests that don't
	// exercise it and don't want retained on-disk fixtures from a previous
	// case to affect an unrelated one. Production always sets it: see
	// cmd/main.go's CAPTURE_VOLUME_BUDGET_BYTES handling, which the operator
	// derives from the "captures" emptyDir's own SizeLimit (see
	// operator/internal/controller/gameserver_controller.go's
	// captureVolumeBudgetBytes) so the two limits can never drift apart.
	budgetBytes int64

	// newSource opens the live packet source for a capture. It is a field
	// rather than a direct call to capture.NewAFPacketSource so tests can
	// inject a synthetic source - and a failing one - without root, a NIC, or
	// CAP_NET_RAW. Production always uses the AF_PACKET implementation wired
	// up in NewServer.
	newSource func(iface, filterExpr string) (capture.PacketSource, error)

	// baseCtx is the parent for every capture goroutine's context. It is
	// deliberately NOT derived from the HTTP request that starts a capture:
	// a capture is meant to keep running, independent of that request, until
	// it hits its own duration/size limit, is explicitly stopped, or the
	// process itself is shutting down. Deriving from r.Context() instead
	// would cancel the capture the instant the "started" response finished
	// sending, which would silently truncate every capture to ~nothing. Tying
	// it to baseCtx instead means process shutdown (see cmd/main.go, which
	// passes its signal.NotifyContext here) cancels any still-running capture
	// cleanly, rather than leaking its goroutine past server exit.
	baseCtx context.Context
}

// NewServer creates a new capture server. ctx is the parent context for every
// capture goroutine the server starts; cancelling it (e.g. on process
// shutdown) stops all in-flight captures. Pass a context that outlives the
// server's HTTP handling, not a per-request one. budgetBytes is the volume
// budget HandleStart enforces (see the Server.budgetBytes field doc); pass 0
// to disable the check.
func NewServer(ctx context.Context, captureDataDir string, budgetBytes int64, serverUID ...string) *Server {
	s := &Server{
		captureDataDir: filepath.Clean(captureDataDir),
		budgetBytes:    budgetBytes,
		completed:      make(map[string]*captureState),
		baseCtx:        ctx,
	}
	if len(serverUID) > 0 {
		s.serverUID = serverUID[0]
	}
	s.newSource = func(iface, filterExpr string) (capture.PacketSource, error) {
		src, err := capture.NewAFPacketSource(iface, 0, filterExpr)
		if err != nil {
			return nil, err
		}
		return src, nil
	}
	return s
}

// Routes builds the sidecar's HTTP router, wrapping every capture endpoint in
// mw (the mTLS middleware in production; nil means "no wrapper", for tests).
//
// This lives here rather than in cmd/main.go so a test can exercise the real
// patterns. net/http.ServeMux validates patterns at registration time and
// panics on a bad one - a wildcard segment must be exactly `{name}`, so the
// chi-style `{id}:start` suffix used elsewhere in this project is invalid
// here - and no test that calls the handler functions directly can catch it.
func (s *Server) Routes(mw func(http.Handler) http.Handler) *http.ServeMux {
	if mw == nil {
		mw = func(next http.Handler) http.Handler { return next }
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", HandleHealthz)
	mux.Handle("POST /captures/{id}/start", mw(http.HandlerFunc(s.HandleStart)))
	mux.Handle("POST /captures/{id}/stop", mw(http.HandlerFunc(s.HandleStop)))
	mux.Handle("GET /captures/{id}/status", mw(http.HandlerFunc(s.HandleStatus)))
	mux.Handle("GET /captures/{id}/file", mw(http.HandlerFunc(s.HandleDownload)))
	mux.Handle("DELETE /captures/{id}", mw(http.HandlerFunc(s.HandleDelete)))
	mux.Handle("GET /v1/targets/{serverUID}/captures/{id}/uids/{captureUID}/file", mw(http.HandlerFunc(s.HandleBoundDownload)))
	mux.Handle("DELETE /v1/targets/{serverUID}/captures/{id}/uids/{captureUID}/file", mw(http.HandlerFunc(s.HandleBoundDelete)))
	return mux
}

// HandleHealthz answers a liveness check that reports nothing about the pod,
// the cluster, or any capture.
//
// It is not wrapped in the mTLS auth middleware here (unlike every other
// route), but that does not make it reachable without a client certificate:
// cmd/main.go serves every route, including this one, over the same
// ListenAndServeTLS listener whose TLSConfig requires and verifies a client
// certificate at the TLS handshake, before any request reaches this mux at
// all. Nothing calls this route today - the Kubernetes API does not support
// probes on ephemeral containers, which is what the sidecar always is - so
// it is unauthenticated only in the sense that this file's own middleware
// wrapping does not gate it a second time (F-193).
func HandleHealthz(w http.ResponseWriter, _ *http.Request) {
	if _, err := fmt.Fprint(w, "ok"); err != nil {
		slog.Error("failed to write healthz response", "err", err)
	}
}

// startRequest represents a POST /captures/{id}/start request body.
type startRequest struct {
	ServerUID          string `json:"serverUID,omitempty"`
	CaptureUID         string `json:"captureUID,omitempty"`
	Filter             string `json:"filter"`
	MaxDurationSeconds int64  `json:"maxDurationSeconds"`
	MaxSizeBytes       int64  `json:"maxSizeBytes"`
}

// startResponse represents a successful start response.
type startResponse struct {
	Status         string `json:"status"`
	CaptureID      string `json:"captureId"`
	StartedAt      string `json:"startedAt"`
	BytesWritten   int64  `json:"bytesWritten"`
	PacketsWritten int64  `json:"packetsWritten"`
}

// stopRequest represents a POST /captures/{id}/stop request body.
type stopRequest struct {
	Reason string `json:"reason,omitempty"`
}

// stopResponse represents a successful stop response.
type stopResponse struct {
	Status         string `json:"status"`
	CaptureID      string `json:"captureId"`
	StartedAt      string `json:"startedAt"`
	CompletedAt    string `json:"completedAt"`
	StoppingReason string `json:"stoppingReason"`
	Message        string `json:"message,omitempty"`
	BytesWritten   int64  `json:"bytesWritten"`
	PacketsWritten int64  `json:"packetsWritten"`
	File           string `json:"file"`
}

// statusResponseRunning represents a GET /captures/{id}/status response
// (running state). completedAt and stoppingReason are explicit JSON nulls.
type statusResponseRunning struct {
	CaptureID                     string  `json:"captureId"`
	Status                        string  `json:"status"`
	StartedAt                     string  `json:"startedAt"`
	CompletedAt                   *string `json:"completedAt"`
	StoppingReason                *string `json:"stoppingReason"`
	BytesWritten                  int64   `json:"bytesWritten"`
	PacketsWritten                int64   `json:"packetsWritten"`
	EstimatedTimeRemainingSeconds int64   `json:"estimatedTimeRemainingSeconds"`
	EstimatedBytesRemaining       int64   `json:"estimatedBytesRemaining"`
}

// statusResponseCompleted represents a GET /captures/{id}/status response for
// a capture that has reached a terminal state (completed or failed).
type statusResponseCompleted struct {
	CaptureID      string `json:"captureId"`
	Status         string `json:"status"`
	StartedAt      string `json:"startedAt"`
	CompletedAt    string `json:"completedAt"`
	StoppingReason string `json:"stoppingReason"`
	Message        string `json:"message,omitempty"`
	BytesWritten   int64  `json:"bytesWritten"`
	PacketsWritten int64  `json:"packetsWritten"`
	File           string `json:"file"`
}

// validCaptureID reports whether id is safe to use as a filesystem path
// component. Every handler validates the {id} path value with this before
// using it to build a path under captureDataDir.
func validCaptureID(id string) bool {
	return captureIDPattern.MatchString(id)
}

// captureFilePath returns the on-disk path for a capture's PCAPNG file.
//
// Every caller of this method has already rejected id with validCaptureID
// (letters, digits, '_', '-' only - no '.', so no ".." segment is even
// expressible), so the join below cannot escape captureDataDir today. It is
// still defensively re-verified here, mirroring the containment check in
// agent/internal/files: filepath.Join + Clean the candidate path, then
// require it to fall strictly under captureDataDir (or equal it) by prefix
// comparison on the *cleaned* forms, so that check can never be fooled by a
// literal ".." that made it past validCaptureID some other way (e.g. a
// future caller that forgets to validate). captureDataDir itself is cleaned
// once in NewServer, so both sides of the comparison are in the same form.
func (s *Server) captureFilePath(id string) string {
	candidate := filepath.Clean(filepath.Join(s.captureDataDir, fmt.Sprintf("capture-%s.pcapng", id)))
	if candidate != s.captureDataDir && !strings.HasPrefix(candidate, s.captureDataDir+string(os.PathSeparator)) {
		// Every id reaching here was already validated by validCaptureID, so
		// this is unreachable in practice; treat it as a programmer error
		// rather than silently falling back to some other path.
		panic(fmt.Sprintf("capture id %q resolved outside capture directory", id))
	}
	return candidate
}

// retainedCaptureBytes sums the size of every capture PCAPNG file currently
// on dir. A file only exists here while it is "retained": expiry is
// deletion in this system (the operator's NetworkCapture reconciler deletes
// a capture's file via HandleDelete once its retention window elapses, see
// expireCapture in operator/internal/controller/networkcapture_controller.go),
// so a file this glob finds is by definition not yet expired, and a file
// that has expired and been deleted is by definition absent from this sum -
// no separate age check is needed here.
//
// A file left over from an earlier capture that reused the same id (for
// example after a pod restart and retry of a start that was interrupted
// mid-write) is counted the same as any other retained file: it is real,
// unexpired bytes on the volume, so counting it is harmless and correct,
// even though it will be overwritten rather than accumulate once the retry
// succeeds.
func retainedCaptureBytes(dir string) (int64, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "capture-*.pcapng"))
	if err != nil {
		return 0, fmt.Errorf("glob capture files in %s: %w", dir, err)
	}
	var total int64
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Deleted between the glob and the stat (e.g. a concurrent
				// expiry) - it no longer occupies space, so it correctly
				// drops out of the sum rather than failing the whole start.
				continue
			}
			return 0, fmt.Errorf("stat %s: %w", path, err)
		}
		total += info.Size()
	}
	return total, nil
}

// rememberCompletedLocked records a finished capture so its terminal state can
// still be served after it stops being the current one. Callers must hold s.mu.
func (s *Server) rememberCompletedLocked(state *captureState) {
	if _, dup := s.completed[state.id]; !dup {
		s.completedOrder = append(s.completedOrder, state.id)
	}
	s.completed[state.id] = state
	for len(s.completedOrder) > maxCompletedRetained {
		delete(s.completed, s.completedOrder[0])
		s.completedOrder = s.completedOrder[1:]
	}
}

// lookup returns the capture with the given id, current or finished.
func (s *Server) lookup(id string) *captureState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentCapture != nil && s.currentCapture.id == id {
		return s.currentCapture
	}
	return s.completed[id]
}

// newCaptureContext derives the context that will govern one capture
// goroutine's lifetime. It deliberately returns a child of s.baseCtx (the
// server's own lifetime - see the baseCtx field doc), not of the caller's
// request context: a capture must keep running after the "started" response
// has been sent, and must only stop on its own duration/size limit, an
// explicit stop, or process shutdown.
//
// The unused context.Context parameter (the caller passes r.Context()) exists
// purely so contextcheck's static analysis has a validated context.Context
// value to anchor this function's return to. Without it, the linter's
// whole-program walk cannot see that s.baseCtx already traces back to a
// legitimately inherited context (the one NewServer was constructed with,
// itself signal.NotifyContext wrapping the process's root context in
// cmd/main.go) and independently
// re-flags both the WithCancel call here and every downstream call site
// (including the `go` statement that spawns runCapture) as a "non-inherited
// new context" - even though the break from the request context is
// deliberate, not accidental. This is the pattern the analyzer's own docs
// recommend for exactly this case: https://github.com/kkHAIKE/contextcheck#example.
func (s *Server) newCaptureContext(_ context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(s.baseCtx)
}

// HandleStart handles POST /captures/{id}/start requests to begin capturing.
func (s *Server) HandleStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validCaptureID(id) {
		http.Error(w, "capture id required", http.StatusBadRequest)
		return
	}

	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.MaxDurationSeconds <= 0 || req.MaxDurationSeconds > maxDurationSecondsLimit {
		http.Error(w, "maxDurationSeconds out of range", http.StatusBadRequest)
		return
	}
	if req.MaxSizeBytes <= 0 {
		http.Error(w, "maxSizeBytes out of range", http.StatusBadRequest)
		return
	}
	if !s.validStartIdentity(req) {
		http.Error(w, "capture identity does not match this server", http.StatusBadRequest)
		return
	}

	// FR-003 makes the filter optional at the *API* boundary, where an omitted
	// filter means "restrict the capture to the game server's own advertised
	// ports". Only the control plane knows those ports, so it must materialise
	// that default before calling here. The sidecar cannot reconstruct it, and
	// must never fall back to "no filter": that records every packet on the pod
	// network - the agent's and the API's mTLS traffic, RCON, any plaintext
	// protocol - into a file the requester can download, and silently voids the
	// contract's guarantee that 100% of packets in the file match the filter.
	if req.Filter == "" {
		http.Error(w, "filter is required: the control plane must supply the default port filter", http.StatusBadRequest)
		return
	}
	// Validate up front (defense-in-depth; the API tier already validated it)
	// so a malformed expression is rejected before any capture resource is
	// opened (FR-003).
	if _, err := capture.CompileFilter(req.Filter); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	if s.currentCapture != nil || s.finishingCapture != nil {
		// Name the capture that is actually running (s.currentCapture.id),
		// not the rejected request's id: they differ whenever a second,
		// different capture id is requested while one is already in
		// progress, and naming the rejected id here would describe a
		// capture that never started as "in progress" (F-192).
		active := s.currentCapture
		if active == nil {
			active = s.finishingCapture
		}
		runningID := active.id
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("capture '%s' already in progress", runningID), http.StatusConflict)
		return
	}

	// F-187: refuse a start that would push the volume past its budget,
	// before opening any socket or file. Bytes already on disk from
	// retained (not-yet-expired) captures count against the same budget as
	// this new capture's own ceiling - counting only the new capture's
	// maxSizeBytes, as the check used to, lets retained files accumulate
	// past the "captures" emptyDir's real SizeLimit and get the pod evicted
	// (see captureVolumeBudgetBytes in
	// operator/internal/controller/gameserver_controller.go).
	if s.budgetBytes > 0 {
		retained, err := retainedCaptureBytes(s.captureDataDir)
		if err != nil {
			s.mu.Unlock()
			slog.Error("failed to compute retained capture bytes", "id", id, "err", err)
			http.Error(w, fmt.Sprintf("failed to check capture volume budget: %v", err), http.StatusInternalServerError)
			return
		}
		// Compare without adding: retained is always >= 0 (it is a sum of
		// non-negative file sizes) and s.budgetBytes is always > 0 (checked
		// above), so budgetBytes-retained cannot overflow. req.MaxSizeBytes
		// is attacker/operator controlled and can be as large as
		// math.MaxInt64 (a NetworkCapture CR applied directly, bypassing the
		// API tier's DefaultMaxSizeBytes clamp, reaches here unclamped via
		// nc.Spec.MaxSize.Value()); retained+req.MaxSizeBytes would wrap
		// negative in that case and silently defeat this check.
		if req.MaxSizeBytes > s.budgetBytes-retained {
			s.mu.Unlock()
			http.Error(w, fmt.Sprintf(
				"capture volume budget exceeded: %d bytes retained + %d bytes requested would exceed the %d byte budget; wait for retained captures to expire or request a smaller maxSizeBytes",
				retained, req.MaxSizeBytes, s.budgetBytes,
			), http.StatusInsufficientStorage)
			return
		}
	}

	// Open the live packet source synchronously, before the capture file and
	// before the 200. An AF_PACKET open can fail (no CAP_NET_RAW, no such
	// interface), and a capture that answered "running" but never opened a
	// socket finalises to a valid, empty PCAPNG that the user cannot tell apart
	// from "your filter matched nothing" - which the contract's Guarantee 6
	// forbids: a capture that cannot start must return a clear error.
	source, err := s.newSource(captureInterface, req.Filter)
	if err != nil {
		s.mu.Unlock()
		slog.Error("failed to open packet source", "id", id, "iface", captureInterface, "err", err)
		http.Error(w, fmt.Sprintf("failed to start capture: %v", err), http.StatusInternalServerError)
		return
	}

	bound, err := s.prepareCaptureBinding(id, req)
	if err != nil {
		_ = source.Close()
		s.mu.Unlock()
		if errors.Is(err, fs.ErrExist) {
			http.Error(w, "capture name is retained; delete it before reuse", http.StatusConflict)
		} else if errors.Is(err, errCaptureIdentityBudget) {
			http.Error(w, "capture identity retention budget reached", http.StatusInsufficientStorage)
		} else {
			slog.Error("failed to bind capture identity", "id", id, "err", err)
			http.Error(w, "failed to bind capture identity", http.StatusInternalServerError)
		}
		return
	}
	filePath := s.captureFilePath(id)
	writer, err := capture.NewWriter(filePath, req.MaxDurationSeconds, req.MaxSizeBytes, 0, req.Filter)
	if err != nil {
		if bound {
			_ = os.Remove(s.captureIdentityPath(id))
		}
		_ = source.Close()
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("failed to open capture file: %v", err), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	// Derived from s.baseCtx (the server's own lifetime), not r.Context(): see
	// the baseCtx field doc for why this must outlive the HTTP request.
	ctx, cancel := s.newCaptureContext(r.Context())
	state := &captureState{
		id:              id,
		serverUID:       req.ServerUID,
		captureUID:      req.CaptureUID,
		startedAt:       now,
		maxDurationSecs: req.MaxDurationSeconds,
		maxSizeBytes:    req.MaxSizeBytes,
		filePath:        filePath,
		writer:          writer,
		cancel:          cancel,
		done:            make(chan struct{}),
		status:          statusRunning,
	}
	s.currentCapture = state

	// Hard duration cap, independent of the writer's own per-packet duration
	// check: a quiet capture that never receives a matching packet must still
	// stop on time. It is armed only once the capture is published as current,
	// and matches on pointer identity, so a stale timer can never terminate a
	// successor capture that happens to reuse the same id.
	state.timer = time.AfterFunc(time.Duration(req.MaxDurationSeconds)*time.Second, func() {
		s.finish("", state, reasonMaxDuration, nil)
	})
	s.mu.Unlock()

	go s.runCapture(ctx, state, source)

	slog.Info("capture started", "id", id, "filter", req.Filter)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(startResponse{
		Status:         statusRunning,
		CaptureID:      id,
		StartedAt:      now.Format(time.RFC3339),
		BytesWritten:   0,
		PacketsWritten: 0,
	}); err != nil {
		slog.Error("failed to encode start response", "id", id, "err", err)
	}
}

// runCapture drives one capture session's packet loop. The source is already
// open (HandleStart opens it synchronously so a failure is reported to the
// caller), so this only reads until ctx is canceled - by finish, via an
// explicit stop or the duration timer - or a limit or a real I/O failure ends
// it. It always runs in its own goroutine and always closes state.done.
func (s *Server) runCapture(ctx context.Context, state *captureState, source capture.PacketSource) {
	defer close(state.done)

	capturer := capture.NewCapturer(source)
	defer func() { _ = capturer.Stop() }()

	if err := capturer.Start(ctx); err != nil {
		s.finish("", state, reasonError, err)
		return
	}

	readErr := capturer.ReadPackets(ctx, func(pkt *capture.RawPacket) error {
		return state.writer.WritePacket(pkt)
	})

	switch {
	case ctx.Err() != nil:
		// finish canceled us; it owns the terminal state.
	case errors.Is(readErr, capture.ErrLimitReached):
		s.finish("", state, state.writer.LimitReason(), nil)
	default:
		// A genuine mid-capture failure: link down, socket closed, write
		// error. The file already holds real packets and stays downloadable,
		// but the capture must not be reported as a clean completion.
		// When ENOSPC surfaces from WritePacket (rare, since Close normally
		// catches it during flush), check both the error and the writer's
		// LimitReason to distinguish disk-full from other errors.
		reason := reasonError
		if errors.Is(readErr, syscall.ENOSPC) || state.writer.LimitReason() == capture.LimitReasonDiskFull {
			reason = reasonDiskFull
		}
		s.finish("", state, reason, readErr)
	}
}

// finish finalizes a capture and moves it into the completed set so its
// terminal state remains observable: it cancels the capture's context, stops
// its duration timer, and closes the PCAPNG writer so the file is always left
// valid and readable.
//
// Exactly one of target and id selects the capture. target matches on pointer
// identity (used by the timer and the packet goroutine, which must never touch
// a successor capture that reuses the same id); id matches the current
// capture's id (used by HandleStop). finish is idempotent: a capture that has
// already finished, or never existed, returns ok == false without touching
// anything, so a duplicate stop or timeout never double-closes the writer.
//
// failure, when non-nil, marks the capture failed rather than completed.
func (s *Server) finish(id string, target *captureState, reason string, failure error) (*captureState, bool) {
	s.mu.Lock()
	state := s.currentCapture
	matched := state != nil
	if matched {
		if target != nil {
			matched = state == target
		} else {
			matched = state.id == id
		}
	}
	if !matched {
		s.mu.Unlock()
		return nil, false
	}
	s.currentCapture = nil
	s.finishingCapture = state
	s.rememberCompletedLocked(state)
	if state.timer != nil {
		state.timer.Stop()
	}
	s.mu.Unlock()

	state.cancel()

	if reason == "" {
		reason = reasonUserRequested
	}
	status := statusCompleted
	message := ""
	if failure != nil {
		status = statusFailed
		message = failure.Error()
	}

	// Close before the terminal state is published, so that any observer who
	// sees a status other than "running" is guaranteed a fully flushed, closed
	// file on disk. Close is also where a full disk actually surfaces:
	// pcapgo.NgWriter buffers through a bufio.Writer, so ENOSPC normally
	// appears at this final flush rather than at WritePacket, and a capture
	// whose file could not be finalized has a truncated tail and must not be
	// reported as "completed".
	if err := state.writer.Close(); err != nil {
		slog.Error("failed to finalize capture file", "id", state.id, "err", err)
		status = statusFailed
		if errors.Is(err, syscall.ENOSPC) {
			reason = reasonDiskFull
		}
		message = fmt.Sprintf("finalize capture file: %v", err)
	}

	state.mu.Lock()
	state.status = status
	state.completedAt = time.Now()
	state.stoppingReason = reason
	state.message = message
	state.mu.Unlock()
	s.mu.Lock()
	if s.finishingCapture == state {
		s.finishingCapture = nil
	}
	s.mu.Unlock()

	return state, true
}

// HandleStop handles POST /captures/{id}/stop requests to end a capture.
func (s *Server) HandleStop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validCaptureID(id) {
		http.Error(w, "capture id required", http.StatusBadRequest)
		return
	}

	var req stopRequest
	// The body is optional. A client that builds the request without an
	// explicit ContentLength sends it chunked (ContentLength == -1), so test
	// for "not empty" rather than "> 0", and tolerate an empty stream.
	if r.ContentLength != 0 && r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
	}

	var target *captureState
	if hasControlIdentity(r) {
		target = s.lookup(id)
		if !s.controlIdentityMatches(r, target) {
			http.NotFound(w, r)
			return
		}
	}
	state, ok := s.finish(id, target, req.Reason, nil)
	if !ok {
		// Already finished - by the duration timer, the size limit, or an
		// earlier stop. Replay its stored terminal result instead of a 404, so
		// the final counters and stopping reason are never lost.
		state = s.lookup(id)
		if state == nil || !s.controlIdentityMatches(r, state) {
			http.Error(w, fmt.Sprintf("capture '%s' not found", id), http.StatusNotFound)
			return
		}
		writeStopResponse(w, state)
		return
	}

	// Bounded wait for the capture goroutine to exit, so a client that stops
	// and immediately polls never observes a terminal response while a packet
	// write is still in flight. Bounded, because WriteTimeout is unset on this
	// server: an unbounded wait on a stuck read loop would hang forever.
	select {
	case <-state.done:
	case <-time.After(stopDrainTimeout):
		slog.Warn("capture goroutine did not exit before the stop deadline",
			"id", id, "timeout", stopDrainTimeout)
	}

	snap := state.snapshot()
	slog.Info("capture stopped", "id", id, "reason", snap.stoppingReason, "status", snap.status)
	writeStopResponse(w, state)
}

// writeStopResponse encodes a capture's terminal state as a stop response.
func writeStopResponse(w http.ResponseWriter, state *captureState) {
	snap := state.snapshot()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stopResponse{
		Status:         snap.status,
		CaptureID:      state.id,
		StartedAt:      state.startedAt.Format(time.RFC3339),
		CompletedAt:    snap.completedAt.Format(time.RFC3339),
		StoppingReason: snap.stoppingReason,
		Message:        snap.message,
		BytesWritten:   state.writer.BytesWritten(),
		PacketsWritten: state.writer.PacketsWritten(),
		File:           state.filePath,
	}); err != nil {
		slog.Error("failed to encode stop response", "id", state.id, "err", err)
	}
}

// HandleStatus handles GET /captures/{id}/status requests for capture state.
func (s *Server) HandleStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validCaptureID(id) {
		http.Error(w, "capture id required", http.StatusBadRequest)
		return
	}

	state := s.lookup(id)
	if state == nil || !s.controlIdentityMatches(r, state) {
		http.Error(w, fmt.Sprintf("capture '%s' not found", id), http.StatusNotFound)
		return
	}

	snap := state.snapshot()
	w.Header().Set("Content-Type", "application/json")

	if snap.status == statusRunning {
		elapsed := time.Since(state.startedAt).Seconds()
		remaining := float64(state.maxDurationSecs) - elapsed
		if remaining < 0 {
			remaining = 0
		}
		bytesWritten := state.writer.BytesWritten()
		if err := json.NewEncoder(w).Encode(statusResponseRunning{
			CaptureID:                     id,
			Status:                        statusRunning,
			StartedAt:                     state.startedAt.Format(time.RFC3339),
			CompletedAt:                   nil,
			StoppingReason:                nil,
			BytesWritten:                  bytesWritten,
			PacketsWritten:                state.writer.PacketsWritten(),
			EstimatedTimeRemainingSeconds: int64(remaining),
			EstimatedBytesRemaining:       state.maxSizeBytes - bytesWritten,
		}); err != nil {
			slog.Error("failed to encode status response", "id", id, "err", err)
		}
		return
	}

	if err := json.NewEncoder(w).Encode(statusResponseCompleted{
		CaptureID:      id,
		Status:         snap.status,
		StartedAt:      state.startedAt.Format(time.RFC3339),
		CompletedAt:    snap.completedAt.Format(time.RFC3339),
		StoppingReason: snap.stoppingReason,
		Message:        snap.message,
		BytesWritten:   state.writer.BytesWritten(),
		PacketsWritten: state.writer.PacketsWritten(),
		File:           state.filePath,
	}); err != nil {
		slog.Error("failed to encode status response", "id", id, "err", err)
	}
}

// HandleDownload handles GET /captures/{id}/file requests to download PCAPNG.
func (s *Server) HandleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validCaptureID(id) {
		http.Error(w, "capture id required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	running := s.captureBusyLocked(id)
	s.mu.Unlock()

	if running {
		http.Error(w, "capture is still running", http.StatusConflict)
		return
	}

	filePath := s.captureFilePath(id)
	file, err := os.Open(filepath.Clean(filePath))
	if err != nil {
		// 410 is reserved for a capture this sidecar knows about whose file is
		// genuinely gone (retention deleted it). Any other open failure -
		// EACCES, EIO, EINVAL - is a real server-side fault and must not be
		// dressed up as "deleted", which sends an operator down the wrong
		// diagnostic path.
		if os.IsNotExist(err) {
			if s.lookup(id) != nil {
				http.Error(w, "capture file has been deleted", http.StatusGone)
				return
			}
			http.Error(w, fmt.Sprintf("capture '%s' not found", id), http.StatusNotFound)
			return
		}
		slog.Error("failed to open capture file", "id", id, "err", err)
		http.Error(w, fmt.Sprintf("failed to open capture file: %v", err), http.StatusInternalServerError)
		return
	}
	defer func() { _ = file.Close() }()

	stat, err := file.Stat()
	if err != nil {
		http.Error(w, "failed to read capture file", http.StatusInternalServerError)
		return
	}

	name := fmt.Sprintf("capture-%s.pcapng", id)
	w.Header().Set("Content-Type", "application/vnd.tcpdump.pcap")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))

	// ServeContent streams the file without buffering (a capture can be
	// multi-GiB), sets Content-Length and Accept-Ranges, and - unlike a bare
	// io.Copy - actually honours a Range header with 206/416 instead of
	// silently resending from byte 0 and corrupting a resumed download.
	http.ServeContent(w, r, name, stat.ModTime(), file)
}

// FinalizeCurrentCapture finalizes an in-flight capture on server shutdown.
// If there is a currently-running capture, it stops the capture, closes the
// writer to finalize the PCAPNG file, and moves it into the completed set.
// Called after server.Shutdown to ensure the capture is properly finalized
// instead of leaving the PCAPNG file truncated and unreadable.
func (s *Server) FinalizeCurrentCapture() {
	s.mu.Lock()
	state := s.currentCapture
	s.mu.Unlock()

	if state != nil {
		s.finish("", state, reasonUserRequested, nil)
		// Wait for the capture goroutine to fully exit, honoring the stop drain
		// timeout to avoid hanging if the goroutine is stuck. Log if it times out
		// so the operator knows the capture may be truncated.
		select {
		case <-state.done:
		case <-time.After(stopDrainTimeout):
			slog.Warn("capture goroutine did not exit before finalization deadline",
				"id", state.id, "timeout", stopDrainTimeout)
		}
	}
}

// HandleDelete handles DELETE /captures/{id} requests to delete a capture file.
func (s *Server) HandleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validCaptureID(id) {
		http.Error(w, "capture id required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if hasControlIdentity(r) {
		r.SetPathValue("serverUID", r.Header.Get("X-Gameplane-Server-UID"))
		r.SetPathValue("captureUID", r.Header.Get("X-Gameplane-Capture-UID"))
		if !s.matchCaptureIdentity(r) {
			http.NotFound(w, r)
			return
		}
	}
	running := s.captureBusyLocked(id)

	if running {
		http.Error(w, "capture is still running", http.StatusConflict)
		return
	}

	filePath := s.captureFilePath(id)
	err := os.Remove(filepath.Clean(filePath))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Error("failed to delete capture file", "id", id, "err", err)
		http.Error(w, fmt.Sprintf("failed to delete capture file: %v", err), http.StatusInternalServerError)
		return
	}

	slog.Info("capture file deleted", "id", id)
	if err := os.Remove(s.captureIdentityPath(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		slog.Error("failed to delete capture identity", "id", id, "err", err)
		http.Error(w, "failed to delete capture identity", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
