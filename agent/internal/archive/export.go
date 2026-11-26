package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"

	"github.com/GameplanePanel/gameplane/agent/internal/auth"
)

const (
	// exportPrefix is the path prefix the export listener routes under.
	exportPrefix = "/export"
	// flushEvery is how many response bytes are written between flushes.
	flushEvery = 1 << 20
	// stderrTailBytes is how much of restic's stderr is kept for error messages.
	stderrTailBytes = 4 << 10
	// maxTerminationErrorBytes keeps the termination message under the kubelet limit.
	maxTerminationErrorBytes = 1024
	// exportFailedMessage is the only text returned to a client on failure.
	exportFailedMessage = "archive export failed"
)

// runDump starts "restic dump" for a snapshot and streams the tar archive of
// its root to stdout. Tests replace it with a fake.
var runDump = func(ctx context.Context, snapshot string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "/tools/restic", "dump", snapshot, "/", "--archive", "tar", "--retry-lock", "10m")
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("restic dump: %w", err)
	}
	return nil
}

// exportConfig is the input of one export, read from the environment the
// operator sets on the backup Job.
type exportConfig struct {
	Snapshot string
	Format   string
	Manifest Manifest
}

// exportResult is the termination message of an export run.
type exportResult struct {
	BytesSent int64  `json:"bytesSent,omitempty"`
	Entries   int    `json:"entries,omitempty"`
	Skipped   int    `json:"skipped,omitempty"`
	Error     string `json:"error,omitempty"`
}

// exportConfigFromEnv reads ARCHIVE_SNAPSHOT_ID, ARCHIVE_FORMAT and
// ARCHIVE_MANIFEST through getenv and validates them.
func exportConfigFromEnv(getenv func(string) string) (exportConfig, error) {
	snapshot := getenv("ARCHIVE_SNAPSHOT_ID")
	if snapshot == "" {
		return exportConfig{}, errors.New("ARCHIVE_SNAPSHOT_ID is required")
	}
	if strings.HasPrefix(snapshot, "-") {
		return exportConfig{}, errors.New("ARCHIVE_SNAPSHOT_ID must not start with '-'")
	}
	format := getenv("ARCHIVE_FORMAT")
	if format != "tar.gz" && format != "zip" {
		return exportConfig{}, fmt.Errorf("ARCHIVE_FORMAT %q is unsupported", format)
	}
	raw := getenv("ARCHIVE_MANIFEST")
	if raw == "" {
		return exportConfig{}, errors.New("ARCHIVE_MANIFEST is required")
	}
	m, err := DecodeManifest(strings.NewReader(raw))
	if err != nil {
		return exportConfig{}, fmt.Errorf("ARCHIVE_MANIFEST: %w", err)
	}
	return exportConfig{Snapshot: snapshot, Format: format, Manifest: m}, nil
}

// exporter serves one archive download. It records the outcome of the
// request so runExport can write the termination message afterwards.
type exporter struct {
	cfg   exportConfig
	abort func()

	mu    sync.Mutex
	stats Stats
	sent  int64
	err   error
}

func newExporter(cfg exportConfig, abort func()) *exporter {
	if abort == nil {
		abort = func() {}
	}
	return &exporter{cfg: cfg, abort: abort}
}

// ServeHTTP streams the archive for GET requests. A failure before the first
// response byte is answered with 502. A failure after it aborts the connection
// so the client never sees a clean end of a truncated archive.
func (x *exporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	contentType := "application/gzip"
	if x.cfg.Format == "zip" {
		contentType = "application/zip"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="gameplane-backup.`+x.cfg.Format+`"`)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	pr, pw := io.Pipe()
	var stderr tailBuffer
	dumpDone := make(chan error, 1)
	go func() {
		err := runDump(ctx, x.cfg.Snapshot, pw, &stderr)
		if err != nil {
			if tail := stderr.String(); tail != "" {
				err = fmt.Errorf("%w: %s", err, tail)
			}
		}
		_ = pw.CloseWithError(err)
		dumpDone <- err
	}()

	out := &flushWriter{w: w, rc: http.NewResponseController(w)}
	stats, err := Transcode(out, pr, x.cfg.Format, x.cfg.Manifest)
	if err == nil {
		// Transcode stops at the tar end marker. Drain the rest so a dump
		// that fails after that point is still reported.
		_, err = io.Copy(io.Discard, pr)
	}
	if err == nil {
		err = out.finish()
	}
	if err != nil {
		// Unblock restic's writes and stop it if it is still running.
		_ = pr.CloseWithError(err)
	}
	cancel()
	dumpErr := <-dumpDone
	if err == nil {
		err = dumpErr
	}

	x.record(stats, out.sent, err)
	if err == nil {
		return
	}
	x.abort()
	if !out.wrote {
		w.Header().Del("Content-Disposition")
		w.Header().Del("Content-Type")
		http.Error(w, exportFailedMessage, http.StatusBadGateway)
		return
	}
	panic(http.ErrAbortHandler)
}

func (x *exporter) record(stats Stats, sent int64, err error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.stats = stats
	x.sent = sent
	x.err = err
}

func (x *exporter) result() exportResult {
	x.mu.Lock()
	defer x.mu.Unlock()
	res := exportResult{
		BytesSent: x.sent,
		Entries:   x.stats.Entries,
		Skipped:   x.stats.Skipped,
	}
	if x.err != nil {
		res.Error = x.err.Error()
	}
	return res
}

// flushWriter holds the first responseHold bytes of output before it starts
// the response. A gzip header is written on the first Write, so without the
// hold a failure at the very start would look like a mid-stream abort instead
// of a 502. After that it forwards writes and flushes every flushEvery bytes.
// It counts what was sent and whether any byte reached the client.
type flushWriter struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	held    []byte
	started bool
	sent    int64
	pending int64
	wrote   bool
}

// responseHold is how much output is buffered before the response starts.
const responseHold = 64 << 10

func (f *flushWriter) Write(p []byte) (int, error) {
	if !f.started {
		f.held = append(f.held, p...)
		if len(f.held) < responseHold {
			return len(p), nil
		}
		if err := f.start(); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	if err := f.send(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// finish starts the response with whatever is still held, once the archive
// is known to be complete.
func (f *flushWriter) finish() error {
	if f.started {
		return nil
	}
	return f.start()
}

// start sends the held bytes, which commits the status and headers.
func (f *flushWriter) start() error {
	f.started = true
	held := f.held
	f.held = nil
	return f.send(held)
}

func (f *flushWriter) send(p []byte) error {
	n, err := f.w.Write(p)
	if n > 0 {
		f.wrote = true
	}
	f.sent += int64(n)
	f.pending += int64(n)
	if f.pending >= flushEvery {
		f.pending = 0
		// A failed flush means the connection is gone; the next write reports it.
		_ = f.rc.Flush()
	}
	return err
}

// tailBuffer keeps the last stderrTailBytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - stderrTailBytes; over > 0 {
		n := copy(t.buf, t.buf[over:])
		t.buf = t.buf[:n]
	}
	return len(p), nil
}

// String returns the kept tail with surrounding whitespace removed.
func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}

// runExport serves one archive download on the export listener and returns
// the process exit code: 0 after a complete download, 1 otherwise. The
// outcome is written to the termination log either way.
func runExport(ctx context.Context, opts Options, getenv func(string) string, stderr io.Writer) int {
	cfg, err := exportConfigFromEnv(getenv)
	if err != nil {
		return reportExport(opts, exportResult{Error: err.Error()}, stderr)
	}
	tlsCfg, err := auth.ServerTLS(opts.Cert, opts.Key, opts.ClientCA)
	if err != nil {
		return reportExport(opts, exportResult{Error: "load TLS: " + err.Error()}, stderr)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	x := newExporter(cfg, cancel)
	serveErr := Serve(ctx, ServerConfig{
		Addr:        opts.Addr,
		TLS:         tlsCfg,
		Prefix:      exportPrefix,
		UID:         opts.UID,
		IdleTimeout: opts.IdleTimeout,
	}, x)

	res := x.result()
	if res.Error == "" && serveErr != nil {
		res.Error = serveErr.Error()
	}
	return reportExport(opts, res, stderr)
}

// reportExport writes the termination message and returns the exit code.
func reportExport(opts Options, res exportResult, stderr io.Writer) int {
	if len(res.Error) > maxTerminationErrorBytes {
		res.Error = res.Error[:maxTerminationErrorBytes]
	}
	if res.Error != "" {
		fmt.Fprintln(stderr, "export: "+res.Error)
	}
	if err := WriteTermination(opts.TerminationLog, res); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if res.Error != "" {
		return 1
	}
	return 0
}
