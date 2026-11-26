package archive

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// ServerConfig configures the one-shot HTTPS listener of the archive worker.
type ServerConfig struct {
	Addr        string
	TLS         *tls.Config
	Prefix      string
	UID         string
	IdleTimeout time.Duration
}

// ErrIdleTimeout reports that no request completed successfully within the
// configured idle timeout.
var ErrIdleTimeout = errors.New("idle timeout")

// listen is a seam so tests can bind to 127.0.0.1:0.
var listen = net.Listen

const (
	readHeaderTimeout = 10 * time.Second
	shutdownGrace     = 10 * time.Second
)

// Serve listens on cfg.Addr over TLS and routes only the exact path
// cfg.Prefix + "/" + cfg.UID to h. Serve returns nil once h has answered a
// request with a status below 400, ErrIdleTimeout when no successful request
// arrives within cfg.IdleTimeout of the last request, or ctx.Err() when ctx
// is cancelled.
func Serve(ctx context.Context, cfg ServerConfig, h http.Handler) error {
	if cfg.TLS == nil {
		return errors.New("archive server: TLS config is required")
	}
	ln, err := listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("archive server: listen %s: %w", cfg.Addr, err)
	}
	ln = tls.NewListener(ln, cfg.TLS)
	target := cfg.Prefix + "/" + cfg.UID

	var busy atomic.Bool
	success := make(chan struct{}, 1)
	activity := make(chan struct{}, 1)
	gate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != target {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !busy.CompareAndSwap(false, true) {
			w.WriteHeader(http.StatusConflict)
			return
		}
		defer func() {
			busy.Store(false)
			notify(activity)
		}()
		notify(activity)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r)
		if rec.status < http.StatusBadRequest {
			notify(success)
		}
	})

	srv := &http.Server{Handler: gate, ReadHeaderTimeout: readHeaderTimeout}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	finish := func(err error) error {
		sctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if serr := srv.Shutdown(sctx); serr != nil && err == nil {
			return fmt.Errorf("archive server: shutdown: %w", serr)
		}
		return err
	}

	idle := time.NewTimer(cfg.IdleTimeout)
	defer idle.Stop()
	for {
		select {
		case <-ctx.Done():
			return finish(ctx.Err())
		case err := <-serveErr:
			return fmt.Errorf("archive server: serve: %w", err)
		case <-success:
			return finish(nil)
		case <-activity:
			idle.Reset(cfg.IdleTimeout)
		case <-idle.C:
			// A request still in flight, or one that just succeeded, is not idle.
			if busy.Load() || len(success) > 0 {
				idle.Reset(cfg.IdleTimeout)
				continue
			}
			return finish(ErrIdleTimeout)
		}
	}
}

// notify performs a non-blocking send so a pending signal is never lost and
// a second one never blocks the sender.
func notify(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// statusRecorder remembers the first status a handler writes. A handler that
// never writes a header is answered with 200 by net/http, so the default is 200.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
