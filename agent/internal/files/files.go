// Package files serves the file-browser HTTP API. All paths are resolved
// relative to a fixed root (e.g. /data). The requested path is validated
// lexically, then every operation runs relative to a directory descriptor
// opened on that root, one component at a time with symlinks refused (see
// rooted_linux.go), so a filesystem change made after validation cannot
// redirect an operation outside the root.
package files

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/GameplanePanel/gameplane/agent/internal/httpjson"
)

// errPathOutOfRoot is the only "bad path" error safe to echo back to the
// client. Anything else (filesystem errors, etc.) might leak absolute paths or
// volume internals — those get logged and 400'd with a generic message.
// Handlers route through fail()/badRequest() so the classification is
// enforced in one place.
var errPathOutOfRoot = errors.New("path escapes root")

// errDotfile rejects any path with a dot-prefixed component. Dotfiles in the
// data root hold agent-managed state (e.g. the mods manifest) and are not
// part of the file-browser surface. Like errPathOutOfRoot it is safe to echo.
var errDotfile = errors.New("dotfile access denied")

// errFileExists is a client-safe conflict for create-only operations.
var errFileExists = errors.New("file already exists")

// hasDotComponent reports whether the slash-separated relative path rel
// (already stripped of its leading slash) has a dot-prefixed component.
func hasDotComponent(rel string) bool {
	return strings.HasPrefix(rel, ".") || strings.Contains(rel, "/.")
}

type handler struct {
	root string

	// beforeOpen is a test seam. When non-nil it is called synchronously
	// immediately before a path component is opened ("open"), before the
	// destination of a write is inspected ("stat"), before the final rename of
	// an atomic write ("commit") and before a non-recursive delete ("remove").
	// dir is the slash-joined parent path below the root ("" for the root).
	// Production code never sets it.
	beforeOpen func(stage, dir, name string)
	// Test seam for a filesystem that refuses access-metadata restoration.
	preserveAccess func(*os.File, fileAccess) error
}

// Mount registers the file-browser HTTP handlers on the supplied router.
func Mount(r chi.Router, root string) {
	h := &handler{root: filepath.Clean(root)}
	r.Route("/files", func(r chi.Router) {
		// Transfers can legitimately outlive the ordinary operation timeout.
		r.Get("/download", h.download)
		r.Post("/upload", h.upload)
		r.Group(func(bounded chi.Router) {
			bounded.Use(middleware.Timeout(30 * time.Second))
			bounded.Get("/list", h.list)
			bounded.Get("/read", h.read)
			bounded.Post("/write", h.write)
			bounded.Post("/create", h.create)
			bounded.Post("/mkdir", h.mkdir)
			bounded.Delete("/delete", h.del)
		})
	})
}

// Entry is one directory item returned by /files/list.
type Entry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	Dir     bool   `json:"dir"`
	ModTime string `json:"modTime"`
}

// resolve validates the client-supplied path lexically (dot components,
// escape from the root) and returns it joined below the root. It does not
// touch the filesystem: symlink and dotfile-through-symlink checks happen
// while the descriptor walk performs the operation, so there is no
// validated-then-reopened window to race.
func (h *handler) resolve(rel string) (string, error) {
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return h.root, nil
	}
	if hasDotComponent(rel) {
		return "", errDotfile
	}
	abs := filepath.Join(h.root, filepath.Clean("/"+rel))
	if !strings.HasPrefix(abs, h.root+string(os.PathSeparator)) && abs != h.root {
		return "", errPathOutOfRoot
	}
	return abs, nil
}

// components validates rel and returns its path components below the root
// (nil for the root itself).
func (h *handler) components(rel string) ([]string, error) {
	p, err := h.resolve(rel)
	if err != nil {
		return nil, err
	}
	return relComponents(h.root, p)
}

// badRequest writes a 400 with a client-safe message. errPathOutOfRoot and
// errDotfile are the classes we echo verbatim — everything else (filesystem
// errors, multipart parse details, etc.) is logged and replaced with a
// generic "bad request" so filesystem/implementation details stay inside the
// pod.
func (h *handler) badRequest(w http.ResponseWriter, err error) {
	if errors.Is(err, errPathOutOfRoot) || errors.Is(err, errDotfile) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	slog.Warn("files bad request", "err", err)
	http.Error(w, "bad request", http.StatusBadRequest)
}

// fail maps an error from path validation or a descriptor walk to a response:
// bad-path classes are 400s via badRequest, everything else goes through
// httpErr (404/403/500).
func (h *handler) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, errPathOutOfRoot) || errors.Is(err, errDotfile) || errors.Is(err, errBadPath) {
		h.badRequest(w, err)
		return
	}
	httpErr(w, err)
}

func (h *handler) list(w http.ResponseWriter, req *http.Request) {
	comps, err := h.components(req.URL.Query().Get("path"))
	if err != nil {
		h.fail(w, err)
		return
	}
	out, err := h.listDir(comps)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, out)
}

func (h *handler) read(w http.ResponseWriter, req *http.Request) {
	comps, err := h.components(req.URL.Query().Get("path"))
	if err != nil {
		h.fail(w, err)
		return
	}
	f, err := h.openFile(comps)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		httpErr(w, err)
		return
	}
	if fi.IsDir() {
		http.Error(w, "is a directory", http.StatusBadRequest)
		return
	}
	const maxRead = 2 << 20 // 2 MiB inline read
	if fi.Size() > maxRead {
		http.Error(w, "file too large; use /files/download", http.StatusRequestEntityTooLarge)
		return
	}
	http.ServeContent(w, req, fi.Name(), fi.ModTime(), f)
}

func (h *handler) download(w http.ResponseWriter, req *http.Request) {
	comps, err := h.components(req.URL.Query().Get("path"))
	if err != nil {
		h.fail(w, err)
		return
	}
	f, err := h.openFile(comps)
	if err != nil {
		h.fail(w, err)
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		httpErr(w, err)
		return
	}
	if fi.IsDir() {
		http.Error(w, "download of directories not supported", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename=%q`, fi.Name()))
	http.ServeContent(w, req, fi.Name(), fi.ModTime(), f)
}

// maxWriteBytes caps a single /files/write or /files/create body. The API-side ws proxy
// already enforces 64 MiB, so this is defense-in-depth against direct
// agent access with a valid mTLS cert.
const maxWriteBytes = 64 << 20

// maxUploadFileBytes caps each individual file in a multipart upload.
// The MaxBytesReader on the request body is a *total* budget — without a
// per-file cap, one oversized file is still allowed as long as it fits
// inside that total.
const maxUploadFileBytes = 64 << 20

// maxUploadFiles caps the number of attachments per request. Reasonable
// uploads (config patches, mod files) come in small counts; thousands
// of tiny files are an attempted inode-exhaustion DoS.
const maxUploadFiles = 64

func (h *handler) write(w http.ResponseWriter, req *http.Request) {
	h.storeRequest(w, req, false)
}

func (h *handler) create(w http.ResponseWriter, req *http.Request) {
	h.storeRequest(w, req, true)
}

func (h *handler) storeRequest(w http.ResponseWriter, req *http.Request, createOnly bool) {
	comps, err := h.components(req.URL.Query().Get("path"))
	if err != nil {
		h.fail(w, err)
		return
	}
	defer func() { _ = req.Body.Close() }()
	body := http.MaxBytesReader(w, req.Body, maxWriteBytes)
	// The data goes to a temp file next to the target and is renamed over it
	// only on success, so any error after the copy starts (ENOSPC, a body that
	// ends early, an agent restart mid-copy) leaves the previous file intact
	// (F-102). The whole sequence runs through one parent directory descriptor.
	store := h.writeFile
	if createOnly {
		store = h.createFile
	}
	err = store(comps, func(dst io.Writer) error {
		_, copyErr := io.Copy(dst, body)
		return copyErr
	})
	if err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) upload(w http.ResponseWriter, req *http.Request) {
	const maxFormSize = int64(64 << 20)
	// Bound the whole request body first, then stream the parts one at a
	// time. ParseMultipartForm would buffer the entire form (memory plus
	// a temp-file spill) before any of it could be validated.
	req.Body = http.MaxBytesReader(w, req.Body, maxFormSize)
	mr, err := req.MultipartReader()
	if err != nil {
		h.badRequest(w, err)
		return
	}
	comps, err := h.components(req.URL.Query().Get("path"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if err := h.makeDirs(comps); err != nil {
		h.fail(w, err)
		return
	}
	count := 0
	foundAnyPart := false
	for {
		part, err := mr.NextPart()
		// NextPart returns io.EOF at a genuine closing boundary, but
		// io.ErrUnexpectedEOF when the body is truncated mid-stream without a
		// closing boundary. We check for each explicitly so errorlint can
		// verify error handling without conflating the two cases.
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			h.badRequest(w, err)
			return
		}
		if err != nil {
			h.badRequest(w, err)
			return
		}
		foundAnyPart = true
		if part.FileName() == "" {
			// A plain form field, not an attachment: nothing to store.
			_ = part.Close()
			continue
		}
		count++
		if count > maxUploadFiles {
			_ = part.Close()
			h.badRequest(w, fmt.Errorf("too many files (max %d)", maxUploadFiles))
			return
		}
		saveErr := h.storePart(comps, part.FileName(), part, maxUploadFileBytes)
		_ = part.Close()
		if saveErr != nil {
			if errors.Is(saveErr, io.ErrUnexpectedEOF) {
				h.badRequest(w, saveErr)
				return
			}
			h.fail(w, saveErr)
			return
		}
	}
	// If ContentLength > 0 but we didn't find any parts, the multipart body is malformed
	if !foundAnyPart && req.ContentLength > 0 {
		h.badRequest(w, errors.New("malformed multipart body"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// savePart streams one multipart part into the directory dir (an absolute
// path below root) under a sanitized name, refusing anything larger than
// limit bytes and any destination that is a symlink. It is a thin wrapper
// over handler.storePart, which does the work through directory descriptors.
func savePart(root, dir, filename string, src io.Reader, limit int64) error {
	h := &handler{root: root}
	comps, err := relComponents(h.root, dir)
	if err != nil {
		return err
	}
	return h.storePart(comps, filename, src, limit)
}

// storePart stores one upload part in the directory comps (below the root)
// under a sanitized name. It writes to a temp file in that directory first
// and renames it over the final name only once the copy succeeds, so a
// failure partway through (a truncated/erroring source, or an over-limit
// part) removes only the temp file — a pre-existing file at that name is left
// untouched instead of being deleted (F-102).
func (h *handler) storePart(comps []string, filename string, src io.Reader, limit int64) error {
	// Sanitize filename — reject anything that would climb out of the directory.
	name := filepath.Base(filename)
	if name == "." || name == ".." || name == string(os.PathSeparator) {
		return errors.New("invalid filename")
	}
	if strings.HasPrefix(name, ".") {
		return errDotfile
	}
	return h.storeIn(comps, name, ".upload-", false, storeReplace, func(dst io.Writer) error {
		// Read one byte past the cap: if that byte materializes the part is
		// over the limit, whatever its multipart headers claimed.
		n, err := io.Copy(dst, io.LimitReader(src, limit+1))
		if err != nil {
			return fmt.Errorf("save %q: %w", name, err)
		}
		if n > limit {
			return fmt.Errorf("file %q exceeds %d-byte limit", name, limit)
		}
		return nil
	})
}

func (h *handler) mkdir(w http.ResponseWriter, req *http.Request) {
	comps, err := h.components(req.URL.Query().Get("path"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if err := h.makeDirs(comps); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) del(w http.ResponseWriter, req *http.Request) {
	comps, err := h.components(req.URL.Query().Get("path"))
	if err != nil {
		h.fail(w, err)
		return
	}
	if len(comps) == 0 {
		http.Error(w, "refusing to delete root", http.StatusBadRequest)
		return
	}
	// A recursive delete refuses trees containing dot-prefixed entries, since a
	// direct request for such a path is denied too. A symlink is deleted as the
	// link it is, never dereferenced.
	if err := h.removePath(comps, req.URL.Query().Get("recursive") == "true"); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func httpErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errFileExists):
		http.Error(w, errFileExists.Error(), http.StatusConflict)
	case errors.Is(err, errPreserveAccess):
		http.Error(w, "cannot preserve existing file access; the file changed or the filesystem does not support its group/POSIX ACL permissions", http.StatusConflict)
	case errors.Is(err, os.ErrNotExist):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, os.ErrPermission):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		// Never echo the raw error — it frequently contains absolute
		// paths, syscall details, or PVC mount info.
		slog.Warn("files internal error", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// dirMode is the permission for directories the agent creates (mkdir, upload
// and write ancestors). The game container runs as a different uid and, on
// templates without an fsGroup, reaches the data volume only through the
// "other" bits, so directories must be traversable (0o755) just like files are
// readable (0o644). The gosec G301 finding for this file is scoped in
// .golangci.yml.
const dirMode = 0o755
