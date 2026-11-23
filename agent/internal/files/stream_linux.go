package files

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// stream stores a raw request body at path, optionally resuming after the
// first offset bytes of the existing file (spec 040 T007). It is PUT
// /files/stream?path=&offset=N. Like every write it goes through a temp file
// that is renamed over the target only once the body has been copied in full.
func (h *handler) stream(w http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	comps, err := h.components(q.Get("path"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if len(comps) == 0 {
		http.Error(w, "is a directory", http.StatusBadRequest)
		return
	}
	var offset int64
	if s := q.Get("offset"); s != "" {
		offset, err = strconv.ParseInt(s, 10, 64)
		if err != nil || offset < 0 {
			http.Error(w, "invalid offset", http.StatusBadRequest)
			return
		}
	}
	if offset > h.maxUploadBytes || (req.ContentLength > 0 && offset+req.ContentLength > h.maxUploadBytes) {
		http.Error(w, "upload exceeds size limit", http.StatusRequestEntityTooLarge)
		return
	}
	defer func() { _ = req.Body.Close() }()
	body := http.MaxBytesReader(w, req.Body, h.maxUploadBytes-offset)
	defer func() { _ = body.Close() }()

	err = h.storeStream(comps, offset, req.ContentLength, body)
	if err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// storeStream writes the file at comps: the first offset bytes of the existing
// file (when offset > 0), then the body. The offset prefix stays in the file
// until the commit, so the free-space check counts it as well as the body.
func (h *handler) storeStream(comps []string, offset, contentLength int64, body io.Reader) error {
	rootFD, err := openRoot(h.root)
	if err != nil {
		return err
	}
	defer closeFD(rootFD)
	parents := parentsOf(comps)
	name := comps[len(comps)-1]
	dirFD, err := h.walkDirs(rootFD, parents, true, false)
	if err != nil {
		return err
	}
	defer closeFD(dirFD)

	need := offset
	if contentLength > 0 {
		need += contentLength
	}
	free, err := h.free(dirFD)
	if err != nil {
		return err
	}
	// need is non-negative and at most maxUploadBytes (checked by stream).
	if need > 0 && uint64(need) > free {
		return errNoSpace
	}

	return h.storeFile(dirFD, parents, name, ".stream-", storeReplace, func(dst io.Writer) error {
		if offset > 0 {
			if err := h.copyPrefix(dst, dirFD, parents, name, offset); err != nil {
				return err
			}
		}
		if _, err := io.Copy(dst, body); err != nil {
			return fmt.Errorf("copy body %q: %w", name, err)
		}
		return nil
	})
}

// copyPrefix copies the first n bytes of the existing regular file name in
// dirFD into dst. The file must be at least n bytes long.
func (h *handler) copyPrefix(dst io.Writer, dirFD int, parents []string, name string, n int64) error {
	fd, err := h.openChild(dirFD, parents, name, fileFlags)
	if err != nil {
		return err
	}
	// openChild succeeded, so fd is a valid descriptor.
	f := os.NewFile(uintptr(fd), name)
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", name, err)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: not a regular file", errBadPath)
	}
	if fi.Size() < n {
		return errBadOffset
	}
	if _, err := io.CopyN(dst, f, n); err != nil {
		return fmt.Errorf("copy prefix %q: %w", name, err)
	}
	return nil
}

// free reports the bytes available to unprivileged writers on the volume that
// holds dirFD. nil h.freeBytes means statfs.
func (h *handler) free(dirFD int) (uint64, error) {
	if h.freeBytes != nil {
		return h.freeBytes(dirFD)
	}
	var s unix.Statfs_t
	if err := unix.Fstatfs(dirFD, &s); err != nil {
		return 0, fmt.Errorf("statfs: %w", err)
	}
	if s.Bsize <= 0 {
		return 0, nil
	}
	return s.Bavail * uint64(s.Bsize), nil
}
