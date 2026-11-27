package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"golang.org/x/sys/unix"
)

// renameBody is the /files/rename request body.
type renameBody struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Overwrite bool   `json:"overwrite"`
}

// rename moves one entry within the data root (spec 040 T006).
func (h *handler) rename(w http.ResponseWriter, req *http.Request) {
	defer func() { _ = req.Body.Close() }()
	var body renameBody
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 64<<10)).Decode(&body); err != nil {
		h.badRequest(w, err)
		return
	}
	fromComps, err := h.components(body.From)
	if err != nil {
		h.fail(w, err)
		return
	}
	toComps, err := h.components(body.To)
	if err != nil {
		h.fail(w, err)
		return
	}
	if len(fromComps) == 0 || len(toComps) == 0 {
		http.Error(w, "refusing to rename root", http.StatusBadRequest)
		return
	}
	if err := h.renamePath(fromComps, toComps, body.Overwrite); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// renamePath renames the entry at from to the path to, both below the root,
// using renameat on descriptors from the rooted walk. A symlink is renamed as
// the link it is. With overwrite false the commit uses RENAME_NOREPLACE.
func (h *handler) renamePath(from, to []string, overwrite bool) error {
	rootFD, err := openRoot(h.root)
	if err != nil {
		return err
	}
	defer closeFD(rootFD)
	srcDir, srcName, err := h.locate(rootFD, from)
	if err != nil {
		return err
	}
	defer closeFD(srcDir)
	dstDir, dstName, err := h.locate(rootFD, to)
	if err != nil {
		return err
	}
	defer closeFD(dstDir)
	// The source must exist; a symlink is renamed as the link it is.
	h.fire("stat", parentsOf(from), srcName)
	var st unix.Stat_t
	if err := unix.Fstatat(srcDir, srcName, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("stat %q: %w", srcName, err)
	}
	h.fire("commit", parentsOf(to), dstName)
	if overwrite {
		err = unix.Renameat(srcDir, srcName, dstDir, dstName)
	} else {
		err = unix.Renameat2(srcDir, srcName, dstDir, dstName, unix.RENAME_NOREPLACE)
	}
	return renameError(srcName, err)
}

// renameError maps a renameat result to the file API's client-safe errors:
// an existing (or non-empty directory) destination is a conflict, EINVAL
// (a directory moved into itself) is a bad request.
func renameError(name string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST), errors.Is(err, unix.ENOTEMPTY):
		return fmt.Errorf("rename %q: %w: %w", name, errFileExists, err)
	case errors.Is(err, unix.EINVAL):
		return fmt.Errorf("rename %q: %w: %w", name, errBadRename, err)
	default:
		return fmt.Errorf("rename %q: %w", name, err)
	}
}
