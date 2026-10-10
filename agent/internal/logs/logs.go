// Package logs streams the game container's log file over a WebSocket.
//
// Log paths are resolved beneath the operator-supplied data root. Each read
// uses a confined regular-file descriptor, including after log rotation.
package logs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

type handler struct {
	path      string
	root      string
	relative  string
	configErr error
}

// Mount registers the log-streaming WebSocket endpoints on the supplied router.
func Mount(r chi.Router, root, path string) {
	h := newHandler(root, path)
	r.Get("/logs/tail", h.tail)
	r.Get("/logs/download", h.download)
}

func (h *handler) download(w http.ResponseWriter, req *http.Request) {
	if h.path == "" {
		http.Error(w, "log download not configured (set --game-log-path)", http.StatusServiceUnavailable)
		return
	}
	source, err := h.openSource()
	if err != nil {
		http.Error(w, "log file unavailable", http.StatusInternalServerError)
		return
	}
	defer source.close()
	f, err := source.open()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "log file not found", http.StatusNotFound)
			return
		}
		http.Error(w, "log file unavailable", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "log file unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename=%q`, filepath.Base(h.path)))
	http.ServeContent(w, req, filepath.Base(h.path), fi.ModTime(), f)
}

func (h *handler) tail(w http.ResponseWriter, req *http.Request) {
	if h.path == "" {
		http.Error(w, "log tailing not configured (set --game-log-path)", http.StatusServiceUnavailable)
		return
	}
	conn, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	// Read any "follow from now" marker: ?from=end (default) vs ?from=start.
	// Also read ?tail=N to replay the last N lines before following.
	from := req.URL.Query().Get("from")
	tailParam := req.URL.Query().Get("tail")
	fromEnd := from != "start"
	var tailLines int64
	if tailParam != "" {
		if n, err := strconv.ParseInt(tailParam, 10, 64); err == nil && n > 0 {
			tailLines = min(n, maxTailLines)
			fromEnd = false // Override fromEnd to false when requesting history
		}
	}

	source, err := h.openSource()
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, "log file unavailable")
		return
	}
	defer source.close()
	if err := streamFile(ctx, conn, source, fromEnd, tailLines); err != nil && !errors.Is(err, context.Canceled) {
		_ = conn.Close(websocket.StatusInternalError, "log file unavailable")
	}
}

// streamFile tails a confined source, delivering each full line as a text WS frame.
// If tailLines > 0, replays the last tailLines lines before following.
// Reopens the file on rotation (ENOENT or inode change) with a short
// backoff so logrotate-style setups keep working.
func streamFile(ctx context.Context, conn *websocket.Conn, source *logSource, fromEnd bool, tailLines int64) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		f, err := source.open()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if sleep(ctx, time.Second) != nil {
					return ctx.Err()
				}
				continue
			}
			return err
		}
		// Replay history: read all lines and send the last tailLines before following.
		if tailLines > 0 {
			if err := replayTail(ctx, conn, f, tailLines); err != nil {
				_ = f.Close()
				return err
			}
		} else if fromEnd {
			_, _ = f.Seek(0, io.SeekEnd)
		}
		if err := tailLoop(ctx, conn, source, f); err != nil {
			_ = f.Close()
			if errors.Is(err, errRotated) {
				fromEnd = false
				tailLines = 0
				continue
			}
			return err
		}
		_ = f.Close()
		return nil
	}
}

var errRotated = errors.New("log file rotated")

// maxTailLines caps ?tail=N so a caller cannot make the agent buffer an unbounded history.
const maxTailLines int64 = 20000

const maxTailBytes int64 = 4 << 20 // 4 MiB

// appendTail adds line to the replay window buf (holding total bytes) and evicts
// the oldest lines until it holds at most maxLines lines and maxBytes bytes. An
// empty line is ignored, and a line longer than maxBytes on its own is skipped.
func appendTail(buf []string, total int64, line string, maxLines, maxBytes int64) ([]string, int64) {
	n := int64(len(line))
	if n == 0 || n > maxBytes {
		return buf, total
	}
	buf = append(buf, line)
	total += n
	for len(buf) > 0 && (int64(len(buf)) > maxLines || total > maxBytes) {
		total -= int64(len(buf[0]))
		buf = buf[1:]
	}
	return buf, total
}

// replayTail reads all lines from file f and sends the last tailLines to conn.
func replayTail(ctx context.Context, conn *websocket.Conn, f *os.File, tailLines int64) error {
	reader := bufio.NewReader(f)
	var buffer []string
	var totalBytes int64
	for {
		line, err := reader.ReadString('\n')
		buffer, totalBytes = appendTail(buffer, totalBytes, line, tailLines, maxTailBytes)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}
	// Send all buffered lines to the client.
	for _, line := range buffer {
		if werr := conn.Write(ctx, websocket.MessageText, []byte(line)); werr != nil {
			return werr
		}
	}
	return nil
}

func tailLoop(ctx context.Context, conn *websocket.Conn, source *logSource, f *os.File) error {
	reader := bufio.NewReader(f)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			if werr := conn.Write(ctx, websocket.MessageText, []byte(line)); werr != nil {
				return werr
			}
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, io.EOF):
			if rotated, rerr := checkRotation(source, f); rerr != nil {
				return rerr
			} else if rotated {
				return errRotated
			}
			if sleep(ctx, 200*time.Millisecond) != nil {
				return ctx.Err()
			}
		default:
			return err
		}
	}
}

func checkRotation(source *logSource, f *os.File) (bool, error) {
	fi1, err := f.Stat()
	if err != nil {
		return false, err
	}
	probe, err := source.open()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	defer func() { _ = probe.Close() }()
	fi2, err := probe.Stat()
	if err != nil {
		return false, err
	}
	return !os.SameFile(fi1, fi2), nil
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
