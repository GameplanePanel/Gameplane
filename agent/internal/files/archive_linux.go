package files

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"time"
)

// ZIP downloads are staged before headers are sent, so source errors never
// become successful downloads of incomplete archives. The game volume is the
// agent's writable mount. Only one archive may use it at a time.
const (
	maxArchiveSelection       = 256
	maxArchiveEntries         = 10000
	maxArchiveBytes     int64 = 1 << 30
)

var errArchiveLimit = errors.New("selection exceeds archive limits")

func (h *handler) archive(w http.ResponseWriter, req *http.Request) {
	if !h.archiveLock.TryLock() {
		http.Error(w, "another archive is in progress", http.StatusConflict)
		return
	}
	defer h.archiveLock.Unlock()
	selected, prefix, err := h.archivePaths(req.URL.Query()["path"])
	if err != nil {
		h.fail(w, err)
		return
	}
	write := func(dst io.Writer) error {
		files := selectionArchive{h: h, zip: zip.NewWriter(dst), ctx: req.Context(), names: make(map[string]bool)}
		for _, components := range selected {
			if err := files.add(components, strings.Join(components[prefix:], "/")); err != nil {
				return err
			}
		}
		return files.zip.Close()
	}
	if req.Method == http.MethodPost {
		dest, parseErr := h.components(req.URL.Query().Get("dest"))
		if parseErr != nil {
			h.fail(w, parseErr)
			return
		}
		if len(dest) == 0 || !strings.EqualFold(path.Ext(dest[len(dest)-1]), ".zip") {
			http.Error(w, "choose a .zip filename", http.StatusBadRequest)
			return
		}
		for _, source := range selected {
			if len(dest) >= len(source) && slices.Equal(source, dest[:len(source)]) {
				http.Error(w, "archive destination overlaps selection", http.StatusBadRequest)
				return
			}
		}
		err = h.createFile(dest, write)
		if err != nil {
			h.archiveError(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
		return
	}
	tmp, err := os.CreateTemp(h.root, ".archive-")
	if err != nil {
		h.fail(w, err)
		return
	}
	defer func() { _ = tmp.Close() }()
	// Unlink now; even an interrupted request or a restarted agent leaves no file.
	if err = os.Remove(tmp.Name()); err != nil {
		h.fail(w, err)
		return
	}
	if err = write(tmp); err != nil {
		h.archiveError(w, err)
		return
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err != nil {
		h.fail(w, err)
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\"selection.zip\"")
	w.Header().Set("Content-Type", "application/zip")
	http.ServeContent(w, req, "selection.zip", time.Time{}, io.NewSectionReader(tmp, 0, size))
}

func (h *handler) archiveError(w http.ResponseWriter, err error) {
	if errors.Is(err, errArchiveLimit) {
		http.Error(w, errArchiveLimit.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	h.fail(w, err)
}

func (h *handler) archivePaths(paths []string) ([][]string, int, error) {
	if len(paths) == 0 || len(paths) > maxArchiveSelection {
		return nil, 0, errBadPath
	}
	selected := make([][]string, 0, len(paths))
	names := make(map[string]bool, len(paths))
	for _, raw := range paths {
		comps, err := h.components(raw)
		if err != nil {
			return nil, 0, err
		}
		if len(comps) == 0 || strings.ContainsAny(raw, "\\:") {
			return nil, 0, errBadPath
		}
		key := strings.Join(comps, "/")
		if names[key] {
			return nil, 0, errBadPath
		}
		names[key] = true
		selected = append(selected, comps)
	}
	prefix := parentsOf(selected[0])
	for _, comps := range selected {
		for i := 1; i < len(comps); i++ {
			if names[strings.Join(comps[:i], "/")] {
				return nil, 0, errBadPath
			}
		}
		for len(prefix) > len(comps)-1 || !slices.Equal(prefix, comps[:len(prefix)]) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	return selected, len(prefix), nil
}

type selectionArchive struct {
	h     *handler
	zip   *zip.Writer
	ctx   context.Context
	names map[string]bool
	size  int64
}

func (a *selectionArchive) add(components []string, name string) error {
	if err := a.ctx.Err(); err != nil {
		return err
	}
	// Directory entries can move during traversal. Never publish two members
	// with the same archive name, even if the filesystem reports one twice.
	if a.names[name] {
		return errBadPath
	}
	if len(a.names) >= maxArchiveEntries || len(components) > 128 {
		return errArchiveLimit
	}
	a.names[name] = true
	entry, err := a.h.openFile(components)
	if err != nil {
		return err
	}
	defer func() { _ = entry.Close() }()
	info, err := entry.Stat()
	if err != nil {
		return fmt.Errorf("stat archive entry: %w", err)
	}
	if info.IsDir() {
		return a.addFolder(components, name, entry, info)
	}
	if !info.Mode().IsRegular() {
		return errBadPath
	}
	if info.Size() > maxArchiveBytes-a.size {
		return errArchiveLimit
	}
	a.size += info.Size()
	header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: info.ModTime()}
	dst, err := a.zip.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create archive entry: %w", err)
	}
	// Limit reads to the observed size, including for files that grow while being archived.
	n, err := io.Copy(dst, io.LimitReader(archiveReader{a.ctx, entry}, info.Size()))
	if err != nil {
		return fmt.Errorf("copy archive entry: %w", err)
	}
	if n != info.Size() {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (a *selectionArchive) addFolder(components []string, name string, folder *os.File, info fs.FileInfo) error {
	header := &zip.FileHeader{Name: name + "/", Modified: info.ModTime()}
	header.SetMode(fs.ModeDir | 0o755)
	if _, err := a.zip.CreateHeader(header); err != nil {
		return fmt.Errorf("create archive directory: %w", err)
	}
	for {
		names, err := folder.Readdirnames(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("list archive directory: %w", err)
		}
		for _, child := range names {
			if strings.HasPrefix(child, ".") {
				return errDotfile
			}
			if strings.ContainsAny(child, "\\:") {
				return errBadPath
			}
			if err := a.add(append(slices.Clone(components), child), name+"/"+child); err != nil {
				return err
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}

type archiveReader struct {
	ctx context.Context
	src io.Reader
}

func (r archiveReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.src.Read(p)
}
