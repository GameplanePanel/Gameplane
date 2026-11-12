package logs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

const historyBytes = 64 << 10

var errHistoryChanged = errors.New("log history changed")
var errInvalidCursor = errors.New("invalid history cursor")

type historyPage struct {
	Text   string `json:"text"`
	Before string `json:"before,omitempty"`
}

// Authenticate the entire byte window the next request may return. A repeated
// line near the boundary alone cannot identify a file generation.
func fileCursor(identity *syscall.Stat_t, offset int64, window []byte) string {
	hash := sha256.Sum256(window)
	return fmt.Sprintf("%x.%x.%x.%s", identity.Dev, identity.Ino, offset, hex.EncodeToString(hash[:16]))
}

func readHistoryWindow(f io.ReaderAt, end int64) ([]byte, error) {
	start := max(end-historyBytes, 0)
	buf := make([]byte, end-start)
	if _, err := f.ReadAt(buf, start); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errHistoryChanged
		}
		return nil, fmt.Errorf("read log page: %w", err)
	}
	return buf, nil
}

type historyFile interface {
	io.ReaderAt
	Stat() (os.FileInfo, error)
}

func filePage(f historyFile, before string, lines int) (historyPage, error) {
	info, err := f.Stat()
	if err != nil {
		return historyPage{}, fmt.Errorf("stat log: %w", err)
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || identity == nil {
		return historyPage{}, errors.New("log file has no device or inode information")
	}
	end := info.Size()
	if before != "" {
		if len(before) > 128 {
			return historyPage{}, errInvalidCursor
		}
		parts := strings.SplitN(before, ".", 4)
		if len(parts) != 4 {
			return historyPage{}, errInvalidCursor
		}
		end, err = strconv.ParseInt(parts[2], 16, 64)
		if err != nil || end <= 0 {
			return historyPage{}, errInvalidCursor
		}
		if end > info.Size() {
			return historyPage{}, errHistoryChanged
		}
	}
	start := max(end-historyBytes, 0)
	buf, err := readHistoryWindow(f, end)
	if err != nil {
		if before != "" {
			return historyPage{}, errHistoryChanged
		}
		return historyPage{}, err
	}
	if before != "" && before != fileCursor(identity, end, buf) {
		return historyPage{}, errHistoryChanged
	}
	search := bytes.TrimSuffix(buf, []byte{'\n'})
	at := len(search)
	for i := 0; i < lines; i++ {
		at = bytes.LastIndexByte(search[:at], '\n')
		if at < 0 {
			break
		}
	}
	cut := at + 1
	if start > 0 {
		// A byte-bounded page can divide a long line. Keep valid UTF-8 intact;
		// concatenating adjacent page text reconstructs it without synthetic LFs.
		for cut < len(buf) && cut < 4 && !utf8.RuneStart(buf[cut]) {
			cut++
		}
	}
	page := historyPage{Text: strings.ToValidUTF8(string(buf[cut:]), "?")}
	if next := start + int64(cut); next > 0 {
		window, err := readHistoryWindow(f, next)
		if err != nil {
			return historyPage{}, err
		}
		page.Before = fileCursor(identity, next, window)
		// Bind the outgoing cursor and returned text to the same observed
		// bytes even when a writer truncates and regrows between our reads.
		current, err := readHistoryWindow(f, end)
		if err != nil {
			return historyPage{}, err
		}
		if !bytes.Equal(buf, current) {
			return historyPage{}, errHistoryChanged
		}
	}
	return page, nil
}

func (h *handler) history(w http.ResponseWriter, req *http.Request) {
	if h.path == "" {
		http.Error(w, "log history unavailable", http.StatusServiceUnavailable)
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
			if req.URL.Query().Get("before") != "" {
				http.Error(w, errHistoryChanged.Error(), http.StatusConflict)
				return
			}
			http.Error(w, "log file unavailable", http.StatusNotFound)
			return
		}
		http.Error(w, "log file unavailable", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()
	page, err := filePage(f, req.URL.Query().Get("before"), 100)
	switch {
	case errors.Is(err, errInvalidCursor):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, errHistoryChanged):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		http.Error(w, "log file unavailable", http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(page)
	}
}
