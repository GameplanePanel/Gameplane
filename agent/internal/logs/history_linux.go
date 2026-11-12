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

// The guard detects copytruncate followed by regrowth past the cursor. Pinning
// a descriptor and checking device/inode also covers rename-and-replace.
func fileCursor(f *os.File, identity *syscall.Stat_t, offset int64) (string, error) {
	var sample [64]byte
	n := min(offset, int64(len(sample)))
	if _, err := f.ReadAt(sample[:n], offset-n); err != nil {
		return "", fmt.Errorf("read cursor guard: %w", err)
	}
	hash := sha256.Sum256(sample[:n])
	return fmt.Sprintf("%x.%x.%x.%s", identity.Dev, identity.Ino, offset, hex.EncodeToString(hash[:16])), nil
}

func filePage(f *os.File, before string, lines int) (historyPage, error) {
	info, err := f.Stat()
	if err != nil {
		return historyPage{}, fmt.Errorf("stat log: %w", err)
	}
	identity := info.Sys().(*syscall.Stat_t)
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
		current, err := fileCursor(f, identity, end)
		if err != nil || current != before {
			return historyPage{}, errHistoryChanged
		}
	}
	start := max(end-historyBytes, 0)
	buf := make([]byte, end-start)
	if _, err := f.ReadAt(buf, start); err != nil {
		if errors.Is(err, io.EOF) {
			return historyPage{}, errHistoryChanged
		}
		return historyPage{}, fmt.Errorf("read log page: %w", err)
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
	if start+int64(cut) > 0 {
		page.Before, err = fileCursor(f, identity, start+int64(cut))
		if err != nil {
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
