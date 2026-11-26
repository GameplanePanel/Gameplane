package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/GameplanePanel/gameplane/agent/internal/auth"
)

const (
	// uploadPrefix is the path prefix the receive listener routes under.
	uploadPrefix = "/upload"
	// defaultStagingDir is where the upload and its unpacked tree are written.
	defaultStagingDir = "/staging"
)

// receiveConfig is the input of one receive run, read from the environment the
// operator sets on the restore Job.
type receiveConfig struct {
	Format        string
	DeclaredBytes int64
	MaxUnpacked   int64
	StagingDir    string
	Plain         bool
}

// receiveSummary is the termination message of a receive run.
type receiveSummary struct {
	Kind                string    `json:"kind"`
	Manifest            *Manifest `json:"manifest,omitempty"`
	UnpackedBytes       int64     `json:"unpackedBytes"`
	Volumes             []string  `json:"volumes"`
	ZipPermissionsReset bool      `json:"zipPermissionsReset"`
	Error               string    `json:"error"`
}

// receiveConfigFromEnv reads ARCHIVE_FORMAT, ARCHIVE_DECLARED_BYTES,
// ARCHIVE_MAX_UNPACKED_BYTES, ARCHIVE_STAGING_DIR and ARCHIVE_PLAIN_TEMPLATE
// through getenv and validates them.
func receiveConfigFromEnv(getenv func(string) string) (receiveConfig, error) {
	format := getenv("ARCHIVE_FORMAT")
	if format != "tar.gz" && format != "zip" {
		return receiveConfig{}, fmt.Errorf("ARCHIVE_FORMAT %q is unsupported", format)
	}
	declared, err := positiveEnv(getenv, "ARCHIVE_DECLARED_BYTES")
	if err != nil {
		return receiveConfig{}, err
	}
	maxUnpacked, err := positiveEnv(getenv, "ARCHIVE_MAX_UNPACKED_BYTES")
	if err != nil {
		return receiveConfig{}, err
	}
	staging := getenv("ARCHIVE_STAGING_DIR")
	if staging == "" {
		staging = defaultStagingDir
	}
	return receiveConfig{
		Format:        format,
		DeclaredBytes: declared,
		MaxUnpacked:   maxUnpacked,
		StagingDir:    staging,
		Plain:         getenv("ARCHIVE_PLAIN_TEMPLATE") != "",
	}, nil
}

// positiveEnv parses a positive integer environment variable.
func positiveEnv(getenv func(string) string, name string) (int64, error) {
	n, err := strconv.ParseInt(getenv(name), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return n, nil
}

// errUploadOverflow reports a body longer than the declared size.
var errUploadOverflow = errors.New("upload exceeds the declared size")

// receiver accepts the one upload of a restore. It refuses anything that does
// not match the declared size, writes the file, answers 200 once the file is
// synced, then unpacks it and records the summary.
type receiver struct {
	cfg        receiveConfig
	cancel     context.CancelFunc
	uploadPath string
	unpackDir  string

	mu      sync.Mutex
	summary receiveSummary
}

func newReceiver(cfg receiveConfig, cancel context.CancelFunc) *receiver {
	return &receiver{
		cfg:        cfg,
		cancel:     cancel,
		uploadPath: filepath.Join(cfg.StagingDir, "upload."+cfg.Format),
		unpackDir:  filepath.Join(cfg.StagingDir, "unpacked"),
	}
}

// ServeHTTP handles the single PUT. Refusals before the 200 end the run with an
// error summary; a body longer than declared aborts the connection.
func (rv *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", http.MethodPut)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if rv.missingLength(r) {
		rv.refuse(w, http.StatusLengthRequired, errors.New("content-length required"))
		return
	}
	if r.ContentLength != rv.cfg.DeclaredBytes {
		rv.refuse(w, http.StatusBadRequest, fmt.Errorf("content-length %d does not match the declared %d bytes", r.ContentLength, rv.cfg.DeclaredBytes))
		return
	}
	if err := rv.store(r.Body); err != nil {
		if errors.Is(err, errUploadOverflow) {
			rv.fail(err)
			// Abort rather than answer: the client sent more than it declared.
			panic(http.ErrAbortHandler)
		}
		rv.refuse(w, http.StatusBadRequest, err)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = http.NewResponseController(w).Flush()
	rv.unpack()
}

// missingLength reports a request without a usable Content-Length.
func (rv *receiver) missingLength(r *http.Request) bool {
	if r.ContentLength < 0 || len(r.TransferEncoding) > 0 {
		return true
	}
	return r.ContentLength == 0 && r.Header.Get("Content-Length") == ""
}

// store writes exactly the declared number of bytes to the upload file and
// fsyncs it. A body longer than declared returns errUploadOverflow.
func (rv *receiver) store(body io.Reader) error {
	if err := os.MkdirAll(rv.cfg.StagingDir, 0o755); err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	f, err := os.OpenFile(rv.uploadPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create upload file: %w", err)
	}
	n, err := io.Copy(f, io.LimitReader(body, rv.cfg.DeclaredBytes+1))
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("receive upload: %w", err)
	}
	if n > rv.cfg.DeclaredBytes {
		_ = f.Close()
		return errUploadOverflow
	}
	if n != rv.cfg.DeclaredBytes {
		_ = f.Close()
		return fmt.Errorf("upload is %d bytes, declared %d", n, rv.cfg.DeclaredBytes)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync upload: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close upload: %w", err)
	}
	return nil
}

// unpack runs the safe unpacker on the stored upload and records the outcome.
func (rv *receiver) unpack() {
	res, err := func() (UnpackResult, error) {
		if err := os.RemoveAll(rv.unpackDir); err != nil {
			return UnpackResult{}, fmt.Errorf("clear unpack directory: %w", err)
		}
		if err := os.MkdirAll(rv.unpackDir, 0o755); err != nil {
			return UnpackResult{}, fmt.Errorf("create unpack directory: %w", err)
		}
		return Unpack(rv.uploadPath, rv.unpackDir, UnpackOptions{
			Format:   rv.cfg.Format,
			Plain:    rv.cfg.Plain,
			MaxBytes: rv.cfg.MaxUnpacked,
		})
	}()
	rv.mu.Lock()
	defer rv.mu.Unlock()
	if err != nil {
		rv.summary.Error = truncateError(err.Error())
		return
	}
	rv.summary = receiveSummary{
		Kind:                res.Kind,
		Manifest:            res.Manifest,
		UnpackedBytes:       res.UnpackedBytes,
		Volumes:             res.Volumes,
		ZipPermissionsReset: res.ZipPermissionsReset,
	}
}

// refuse answers a refused request, records the error and ends the run.
func (rv *receiver) refuse(w http.ResponseWriter, status int, err error) {
	rv.fail(err)
	http.Error(w, "upload refused", status)
}

// fail records the reason the run ends and cancels the listener.
func (rv *receiver) fail(err error) {
	rv.mu.Lock()
	rv.summary.Error = truncateError(err.Error())
	rv.mu.Unlock()
	rv.cancel()
}

// result returns the summary of the run.
func (rv *receiver) result() receiveSummary {
	rv.mu.Lock()
	defer rv.mu.Unlock()
	s := rv.summary
	if s.Volumes == nil {
		s.Volumes = []string{}
	}
	return s
}

// truncateError keeps an error message under the termination error limit.
func truncateError(msg string) string {
	if len(msg) > maxTerminationErrorBytes {
		return msg[:maxTerminationErrorBytes]
	}
	return msg
}

// runReceive serves one upload on the receive listener and returns the process
// exit code: 0 after an upload that unpacked cleanly, 1 otherwise. The summary
// is written to the termination log either way.
func runReceive(ctx context.Context, opts Options, getenv func(string) string, stderr io.Writer) int {
	cfg, err := receiveConfigFromEnv(getenv)
	if err != nil {
		return reportReceive(opts, receiveSummary{Volumes: []string{}, Error: err.Error()}, stderr)
	}
	tlsCfg, err := auth.ServerTLS(opts.Cert, opts.Key, opts.ClientCA)
	if err != nil {
		return reportReceive(opts, receiveSummary{Volumes: []string{}, Error: "load TLS: " + err.Error()}, stderr)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rv := newReceiver(cfg, cancel)
	serveErr := Serve(ctx, ServerConfig{
		Addr:        opts.Addr,
		TLS:         tlsCfg,
		Prefix:      uploadPrefix,
		UID:         opts.UID,
		IdleTimeout: opts.IdleTimeout,
	}, rv)

	res := rv.result()
	if res.Error == "" && serveErr != nil {
		res.Error = truncateError(serveErr.Error())
	}
	if res.Error == "" && res.Kind == "" {
		res.Error = "no upload received"
	}
	return reportReceive(opts, res, stderr)
}

// reportReceive writes the termination message and returns the exit code.
func reportReceive(opts Options, sum receiveSummary, stderr io.Writer) int {
	if sum.Error != "" {
		_, _ = fmt.Fprintln(stderr, "receive: "+sum.Error)
	}
	if err := WriteTermination(opts.TerminationLog, sum); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	if sum.Error != "" {
		return 1
	}
	return 0
}
