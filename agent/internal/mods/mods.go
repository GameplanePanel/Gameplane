// Package mods serves the mod/plugin management API. Mods are files
// under a directory on the game's data volume (declared by the module's
// spec.capabilities.mods). The dashboard is generic: it lists, installs,
// and removes mods by calling these endpoints; the per-game specifics
// (directory, allowed download hosts) live in the template.
//
// Install downloads a user-supplied URL into the mods directory. Because
// the agent runs in-cluster, that download is a classic SSRF risk, so it
// is guarded three ways: the host must be on the module's allowlist, the
// dialed IP must be public (blocks DNS-rebinding to cluster/metadata
// addresses), and the body is size-capped.
package mods

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/GameplanePanel/gameplane/agent/internal/httpjson"
	"github.com/GameplanePanel/gameplane/netguard"
)

const (
	defaultMaxBytes = 256 << 20 // 256 MiB
	// moduleFileMode is the file permission for extracted mod files. Must be
	// 0o644 (owner rw, group+world r) because the mods volume is shared with
	// the game container, which runs as a different uid and must read the files.
	// The gosec G302 finding for this file is scoped in .golangci.yml.
	moduleFileMode = 0o644
	// moduleDirMode is the permission for directories the agent creates on the
	// shared mods/data volume (mods dir, archive extraction dirs). The game
	// container runs as a different uid and must be able to traverse them.
	// The gosec G301 finding for this file is scoped in .golangci.yml.
	moduleDirMode = 0o755
)

type handler struct {
	dir      string   // absolute mods directory, "" when unconfigured
	exts     []string // lowercased extension allowlist ("" = any)
	allowed  []string // download host allowlist; nil disables installs
	extract  bool     // unpack downloaded archives into per-mod folders
	maxBytes int64
	client   *http.Client
	mu       sync.Mutex // serializes manifest read-modify-write cycles
}

// Mod is one installed mod file. Meta is nil for unmanaged files (placed
// out-of-band or installed before the manifest existed).
type Mod struct {
	Name    string   `json:"name"`
	Size    int64    `json:"size"`
	ModTime string   `json:"modTime"`
	Meta    *ModMeta `json:"meta,omitempty"`
}

// Mount registers the mods endpoints. spec is the module's declared mods
// config (nil when the template declares none — every endpoint then
// degrades rather than 404ing, so the dashboard renders uniformly).
func Mount(r chi.Router, dataRoot string, spec *caps.Mods) {
	h := newHandler(dataRoot, spec)
	r.Get("/mods", h.list)
	r.Post("/mods/install", h.install)
	r.Post("/mods/upload", h.upload)
	r.Delete("/mods", h.remove)
}

func newHandler(dataRoot string, spec *caps.Mods) *handler {
	h := &handler{}
	if spec == nil || spec.Path == "" {
		return h
	}
	// Resolve the mods dir under the data root, rejecting traversal. The
	// CRD already forbids ".." at apply time; this is defense in depth in
	// case a bundle reaches the agent another way.
	root := filepath.Clean(dataRoot)
	dir := filepath.Join(root, filepath.Clean(spec.Path))
	if dir != root && !strings.HasPrefix(dir, root+string(os.PathSeparator)) {
		slog.Warn("mods path escapes data root; mods disabled", "path", spec.Path)
		return h
	}
	h.dir = dir
	h.extract = spec.Extract
	for _, e := range spec.Extensions {
		h.exts = append(h.exts, strings.ToLower(e))
	}
	if spec.Install != nil && len(spec.Install.AllowedHosts) > 0 {
		h.allowed = spec.Install.AllowedHosts
		h.maxBytes = int64(spec.Install.MaxSizeMB) << 20
		if h.maxBytes <= 0 {
			h.maxBytes = defaultMaxBytes
		}
		h.client = newSafeClient(h.allowed)
	}
	return h
}

func (h *handler) list(w http.ResponseWriter, _ *http.Request) {
	if h.dir == "" {
		httpjson.Write(w, http.StatusOK, []Mod{})
		return
	}
	entries, err := os.ReadDir(h.dir)
	if errors.Is(err, os.ErrNotExist) {
		httpjson.Write(w, http.StatusOK, []Mod{})
		return
	}
	if err != nil {
		slog.Warn("mods list", "err", err)
		httpjson.Error(w, http.StatusInternalServerError, "could not read mods directory")
		return
	}
	meta := loadManifest(h.dir).Mods
	out := []Mod{}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue // in-flight temp dirs/files and the manifest itself
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if h.extract {
			// Extract loaders store each mod as a folder.
			if !e.IsDir() {
				continue
			}
			out = append(out, Mod{
				Name:    name,
				Size:    dirSize(filepath.Join(h.dir, name)),
				ModTime: info.ModTime().UTC().Format(time.RFC3339),
				Meta:    meta[name],
			})
			continue
		}
		if e.IsDir() || !h.extAllowed(name) {
			continue
		}
		out = append(out, Mod{
			Name:    name,
			Size:    info.Size(),
			ModTime: info.ModTime().UTC().Format(time.RFC3339),
			Meta:    meta[name],
		})
	}
	httpjson.Write(w, http.StatusOK, out)
}

type installReq struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
	// Replaces names an existing mod to swap out after the new one lands
	// (in-place upgrade): install new → remove old → swap manifest entry.
	Replaces string `json:"replaces,omitempty"`
	// Meta is the registry identity recorded in the manifest. Absent for a
	// plain URL install, which makes the result unmanaged.
	Meta *ModMeta `json:"meta,omitempty"`
}

func (h *handler) install(w http.ResponseWriter, req *http.Request) {
	if h.dir == "" || h.client == nil {
		httpjson.Error(w, http.StatusNotImplemented, "mod installs are not enabled for this game")
		return
	}
	var body installReq
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4<<10)).Decode(&body); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid json body")
		return
	}

	u, err := url.Parse(strings.TrimSpace(body.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		httpjson.Error(w, http.StatusBadRequest, "url must be an absolute http(s) URL")
		return
	}
	if !hostAllowed(u.Hostname(), h.allowed) {
		httpjson.Error(w, http.StatusForbidden, "download host is not allowed by this module")
		return
	}

	name := body.Name
	if name == "" {
		name = path.Base(u.Path)
	}
	name, err = safeName(name)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.extAllowed(name) {
		httpjson.Error(w, http.StatusBadRequest, "file type is not an accepted mod for this game")
		return
	}
	replaces := ""
	if body.Replaces != "" {
		replaces, err = safeName(body.Replaces)
		if err != nil {
			httpjson.Error(w, http.StatusBadRequest, "replaces: "+err.Error())
			return
		}
	}

	// Inject registry credentials if available (e.g., Factorio mod-portal).
	// The provider is determined from the install request's metadata.
	provider := ""
	if body.Meta != nil {
		provider = body.Meta.Provider
	}
	downloadURL := injectModCreds(u.String(), provider)

	// Extract loaders (e.g. BepInEx): the download is an archive unpacked
	// into its own folder so the loader's recursive scan finds the files.
	installName := name
	var size int64
	if h.extract {
		installName = archiveFolderName(name)
		size, err = h.installArchive(req.Context(), downloadURL, installName)
	} else {
		size, err = h.download(req.Context(), downloadURL, name)
	}
	switch {
	case errors.Is(err, errTooLarge):
		httpjson.Error(w, http.StatusRequestEntityTooLarge, "mod exceeds the size limit")
		return
	case errors.Is(err, netguard.ErrBlockedAddr) || errors.Is(err, errHostNotAllowed):
		httpjson.Error(w, http.StatusForbidden, "download was redirected to a disallowed address")
		return
	case errors.Is(err, errBadArchive):
		httpjson.Error(w, http.StatusBadGateway, "downloaded file is not a valid archive")
		return
	case err != nil:
		// Never reflect the raw error — it may carry internal addresses.
		slog.Warn("mod install", "host", u.Hostname(), "err", err)
		httpjson.Error(w, http.StatusBadGateway, "could not download the mod")
		return
	}

	// Upgrade path: the new mod is in place, so removing the old one now
	// can only leave a visible duplicate on crash — never a missing mod.
	if replaces != "" && replaces != installName {
		if rmErr := h.removeEntry(replaces); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			slog.Warn("mod replace cleanup", "name", replaces, "err", rmErr)
		}
	}
	meta := body.Meta
	if meta != nil {
		stamped := *meta
		stamped.SourceURL = u.String()
		stamped.InstalledAt = time.Now().UTC().Format(time.RFC3339)
		meta = &stamped
	}
	h.updateManifest(func(mods map[string]*ModMeta) {
		delete(mods, replaces)
		if meta != nil {
			mods[installName] = meta
		} else {
			// A plain URL (re)install carries no identity — drop any stale
			// entry so the listing doesn't claim a provenance it lost.
			delete(mods, installName)
		}
	})
	httpjson.Write(w, http.StatusOK, Mod{Name: installName, Size: size, Meta: meta})
}

// upload receives a mod file as multipart form data (field "file") and
// installs it like a URL install: same name/extension/size checks, dot-temp
// + rename, archive extraction for extract loaders. Enabled whenever a mods
// directory is configured — an upload carries no SSRF risk, so it doesn't
// require the install (host allowlist) block. The manifest records provider
// "upload", which the update check skips.
func (h *handler) upload(w http.ResponseWriter, req *http.Request) {
	if h.dir == "" {
		httpjson.Error(w, http.StatusNotImplemented, "mods are not configured for this game")
		return
	}
	maxBytes := h.maxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	// 1 MiB of multipart-framing headroom; the exact per-file cap is
	// enforced on the copy below.
	req.Body = http.MaxBytesReader(w, req.Body, maxBytes+(1<<20))
	reader, err := req.MultipartReader()
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, `multipart form with a "file" field is required`)
		return
	}
	// Stream into the mounted mods directory. FormFile spills large uploads
	// to the system temp directory, which is read-only in the sidecar.
	// Preserve ReadForm's default part-count bound as well as the byte cap.
	parts := 0
	errTooManyParts := errors.New("too many multipart parts")
	nextPart := func() (*multipart.Part, error) {
		part, partErr := reader.NextPart()
		if partErr != nil {
			return nil, partErr
		}
		parts++
		if parts > 1000 {
			return nil, errTooManyParts
		}
		return part, nil
	}
	file, err := nextPart()
	for err == nil && (file.FormName() != "file" || file.FileName() == "") {
		if _, err = io.Copy(io.Discard, file); err != nil {
			break
		}
		file, err = nextPart()
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpjson.Error(w, http.StatusRequestEntityTooLarge, "mod exceeds the size limit")
			return
		}
		httpjson.Error(w, http.StatusBadRequest, `multipart form with a "file" field is required`)
		return
	}
	defer func() { _ = file.Close() }()

	name, err := safeName(file.FileName())
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if !h.extAllowed(name) {
		httpjson.Error(w, http.StatusBadRequest, "file type is not an accepted mod for this game")
		return
	}

	if err := os.MkdirAll(h.dir, moduleDirMode); err != nil {
		slog.Warn("mod upload mkdir", "err", err)
		httpjson.Error(w, http.StatusInternalServerError, "could not store the upload")
		return
	}
	tmp, err := os.CreateTemp(h.dir, ".up-*")
	if err != nil {
		slog.Warn("mod upload temp", "err", err)
		httpjson.Error(w, http.StatusInternalServerError, "could not store the upload")
		return
	}
	tmpName := tmp.Name()
	n, err := io.Copy(tmp, io.LimitReader(file, maxBytes+1))
	closeErr := tmp.Close()
	// Consume remaining parts before publishing, preserving the request cap
	// and rejecting truncated multipart bodies without using a temp directory.
	for err == nil && n <= maxBytes {
		part, nextErr := nextPart()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			err = nextErr
			break
		}
		_, err = io.Copy(io.Discard, part)
	}
	if err != nil || closeErr != nil || n > maxBytes {
		_ = os.Remove(tmpName)
		var tooLarge *http.MaxBytesError
		if n > maxBytes || errors.As(err, &tooLarge) {
			httpjson.Error(w, http.StatusRequestEntityTooLarge, "mod exceeds the size limit")
			return
		}
		if errors.Is(err, errTooManyParts) {
			httpjson.Error(w, http.StatusBadRequest, "too many multipart parts")
			return
		}
		slog.Warn("mod upload copy", "err", err, "closeErr", closeErr)
		httpjson.Error(w, http.StatusInternalServerError, "could not store the upload")
		return
	}
	// Chmod before rename: the temp file is 0o600, but mods must be 0o644 so
	// the game container (different uid, shared fsGroup) can read them.
	if err := os.Chmod(tmpName, moduleFileMode); err != nil {
		_ = os.Remove(tmpName)
		slog.Warn("mod upload chmod", "err", err)
		httpjson.Error(w, http.StatusInternalServerError, "could not store the upload")
		return
	}

	installName := name
	if h.extract {
		installName = archiveFolderName(name)
		swapErr := h.swapInArchive(tmpName, installName, maxBytes)
		_ = os.Remove(tmpName)
		switch {
		case errors.Is(swapErr, errBadArchive):
			httpjson.Error(w, http.StatusBadRequest, "uploaded file is not a valid archive")
			return
		case errors.Is(swapErr, errTooLarge):
			httpjson.Error(w, http.StatusRequestEntityTooLarge, "mod exceeds the size limit")
			return
		case swapErr != nil:
			slog.Warn("mod upload extract", "err", swapErr)
			httpjson.Error(w, http.StatusInternalServerError, "could not unpack the upload")
			return
		}
	} else {
		// Use ConfinePath to validate and resolve the destination within h.dir.
		// ConfinePath consolidates validation and symlink resolution in one function,
		// making the guard legible to CodeQL taint analysis.
		finalPath, err := ConfinePath(h.dir, name)
		if err != nil {
			_ = os.Remove(tmpName)
			slog.Warn("mod upload confinement", "err", err)
			httpjson.Error(w, http.StatusBadRequest, "file name is not valid for this mod path")
			return
		}
		// Re-check inline so the guard is visible at the point of use.
		rootClean := filepath.Clean(h.dir)
		finalPath = filepath.Clean(finalPath)
		if finalPath != rootClean && !strings.HasPrefix(finalPath, rootClean+string(os.PathSeparator)) {
			_ = os.Remove(tmpName)
			slog.Warn("mod upload escape attempt", "path", finalPath)
			httpjson.Error(w, http.StatusBadRequest, "path escape attempt")
			return
		}
		if err := os.Rename(tmpName, finalPath); err != nil {
			_ = os.Remove(tmpName)
			slog.Warn("mod upload rename", "err", err)
			httpjson.Error(w, http.StatusInternalServerError, "could not store the upload")
			return
		}
	}

	meta := &ModMeta{
		Provider:    "upload",
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	h.updateManifest(func(mods map[string]*ModMeta) {
		mods[installName] = meta
	})
	httpjson.Write(w, http.StatusOK, Mod{Name: installName, Size: n, Meta: meta})
}

// removeEntry deletes an installed mod by name — the whole folder for
// extract loaders, a single file otherwise.
func (h *handler) removeEntry(name string) error {
	// Use ConfinePath to validate and resolve the target within h.dir.
	// ConfinePath consolidates validation and symlink resolution in one function,
	// making the guard legible to CodeQL taint analysis.
	target, err := ConfinePath(h.dir, name)
	if err != nil {
		return fmt.Errorf("path escape attempt: %w", err)
	}
	// Re-check inline so the guard is visible at the point of use.
	rootClean := filepath.Clean(h.dir)
	target = filepath.Clean(target)
	if target != rootClean && !strings.HasPrefix(target, rootClean+string(os.PathSeparator)) {
		return errors.New("path escape attempt")
	}
	if h.extract {
		return os.RemoveAll(target)
	}
	return os.Remove(target)
}

// downloadTemp streams rawURL into a temp file under the mods dir, enforcing
// the size cap. The caller owns the returned path (rename it into place or
// unpack it), and must remove it.
func (h *handler) downloadTemp(ctx context.Context, rawURL string) (string, int64, error) {
	if err := os.MkdirAll(h.dir, moduleDirMode); err != nil {
		return "", 0, fmt.Errorf("mkdir mods: %w", err)
	}
	// Validate the URL scheme and host in this function, on the parsed URL
	// that is actually used, even though the caller has already checked.
	// This makes the SSRF guard explicit to CodeQL's taint analysis.
	// The caller-side checks (install() validates scheme and hostAllowed)
	// remain in place for defense-in-depth.
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		if err != nil {
			return "", 0, fmt.Errorf("parse url: %w", err)
		}
		return "", 0, errors.New("invalid url scheme or host")
	}
	if !hostAllowed(u.Hostname(), h.allowed) {
		return "", 0, errHostNotAllowed
	}
	// Build the request from the validated *url.URL rather than from rawURL,
	// so the value that reaches the client is the one the checks above ran
	// against. http.NewRequestWithContext would re-parse rawURL to the same
	// URL, so this is behaviourally identical; it exists so the sanitized
	// value is what flows into the request.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", 0, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return "", 0, redactURLErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	if resp.ContentLength > h.maxBytes {
		return "", 0, errTooLarge
	}

	tmp, err := os.CreateTemp(h.dir, ".dl-*")
	if err != nil {
		return "", 0, err
	}
	tmpName := tmp.Name()
	// LimitReader caps the copy at maxBytes+1 so an oversize body (with no
	// or a lying Content-Length) is caught mid-stream.
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, h.maxBytes+1))
	closeErr := tmp.Close()
	if err != nil || closeErr != nil || n > h.maxBytes {
		_ = os.Remove(tmpName)
		switch {
		case err != nil:
			return "", 0, err
		case closeErr != nil:
			return "", 0, closeErr
		default:
			return "", 0, errTooLarge
		}
	}
	return tmpName, n, nil
}

// download streams url into <dir>/<name>, then atomically renames it.
func (h *handler) download(ctx context.Context, url, name string) (int64, error) {
	tmpName, n, err := h.downloadTemp(ctx, url)
	if err != nil {
		return 0, err
	}
	// Use ConfinePath to validate and resolve the destination within h.dir.
	// ConfinePath consolidates validation and symlink resolution in one function,
	// making the guard legible to CodeQL taint analysis.
	finalPath, err := ConfinePath(h.dir, name)
	if err != nil {
		_ = os.Remove(tmpName)
		return 0, err
	}
	// Chmod before rename: the temp file is 0o600, but mods must be 0o644 so
	// the game container (different uid, shared fsGroup) can read them.
	if err := os.Chmod(tmpName, moduleFileMode); err != nil {
		_ = os.Remove(tmpName)
		return 0, fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, finalPath); err != nil {
		_ = os.Remove(tmpName)
		return 0, err
	}
	return n, nil
}

// installArchive downloads a zip and unpacks it into <dir>/<folder>/,
// replacing any existing folder of that name. Returns the downloaded size.
func (h *handler) installArchive(ctx context.Context, url, folder string) (int64, error) {
	tmpZip, size, err := h.downloadTemp(ctx, url)
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(tmpZip) }()
	if err := h.swapInArchive(tmpZip, folder, h.maxBytes); err != nil {
		return 0, err
	}
	return size, nil
}

// swapInArchive unpacks tmpZip into <dir>/<folder>/, replacing any existing
// folder of that name — the shared tail of URL installs and uploads.
func (h *handler) swapInArchive(tmpZip, folder string, maxBytes int64) error {
	staging, err := os.MkdirTemp(h.dir, ".ex-*")
	if err != nil {
		return err
	}
	if err := unzipInto(tmpZip, staging, maxBytes); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	// MkdirTemp creates 0700 directories. The game container uses another
	// uid and needs to traverse the installed archive root.
	if err := os.Chmod(staging, moduleDirMode); err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("chmod archive root: %w", err)
	}
	// Use ConfinePath to validate and resolve the final path within h.dir.
	// ConfinePath consolidates validation and symlink resolution in one function,
	// making the guard legible to CodeQL taint analysis.
	final, err := ConfinePath(h.dir, folder)
	if err != nil {
		_ = os.RemoveAll(staging)
		return fmt.Errorf("path escape attempt: %w", err)
	}
	// Re-check inline so the guard is visible at the point of use.
	rootClean := filepath.Clean(h.dir)
	final = filepath.Clean(final)
	if final != rootClean && !strings.HasPrefix(final, rootClean+string(os.PathSeparator)) {
		_ = os.RemoveAll(staging)
		return errors.New("path escape attempt")
	}
	if err := os.RemoveAll(final); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	if err := os.Rename(staging, final); err != nil {
		_ = os.RemoveAll(staging)
		return err
	}
	return nil
}

// Unix mode type bits as stored in a zip entry's external attributes
// (S_IFMT and S_IFLNK). archive/zip keeps its own unexported copies.
const (
	unixTypeMask    = 0o170000
	unixTypeSymlink = 0o120000
)

// unzipInto extracts zipPath into dst, guarding against zip-slip and
// capping the total uncompressed size.
func unzipInto(zipPath, dst string, maxBytes int64) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("%w: %w", errBadArchive, err)
	}
	defer func() { _ = zr.Close() }()

	dstClean := filepath.Clean(dst)
	var total int64
	for _, f := range zr.File {
		// Validate the archive entry in a single gate: use ConfineRelPath to validate and
		// resolve the entry name within the destination directory. Any entry that escapes
		// the destination returns an error immediately rather than being silently skipped.
		// ConfineRelPath handles multi-segment paths and dotfiles that legitimate archives contain.
		target, confineErr := ConfineRelPath(dstClean, f.Name)
		if confineErr != nil {
			return fmt.Errorf("zip-slip: %w", confineErr)
		}
		// Re-check inline so the guard is visible at the point of use.
		target = filepath.Clean(target)
		if target != dstClean && !strings.HasPrefix(target, dstClean+string(os.PathSeparator)) {
			return fmt.Errorf("zip-slip: %w", ErrEscapesRoot)
		}
		// Reject symlink entries outright: a symlink's target is resolved at use
		// time and therefore cannot be confined by a path check at extraction time.
		//
		// Test the raw Unix type bits as well as Mode(). archive/zip decodes them
		// only when the entry declares a Unix creator (FileHeader.Mode falls back
		// to the MS-DOS attribute bits otherwise, which carry no symlink type), and
		// the archive is untrusted input — a crafted entry can set the symlink type
		// in ExternalAttrs while leaving CreatorVersion zero.
		if f.Mode()&os.ModeSymlink != 0 || (f.ExternalAttrs>>16)&unixTypeMask == unixTypeSymlink {
			return fmt.Errorf("zip-slip: %w", errSymlinkEntry)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, moduleDirMode); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), moduleDirMode); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, moduleFileMode)
		if err != nil {
			_ = rc.Close()
			return err
		}
		n, err := io.Copy(out, io.LimitReader(rc, maxBytes-total+1))
		_ = rc.Close()
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		total += n
		if total > maxBytes {
			return errTooLarge
		}
	}
	return nil
}

// archiveFolderName strips a known archive extension to name the per-mod
// folder (e.g. "Owner-Mod-1.0.0.zip" → "Owner-Mod-1.0.0").
func archiveFolderName(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(lower, ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name
}

// dirSize sums the size of every regular file under dir.
func dirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

func (h *handler) remove(w http.ResponseWriter, req *http.Request) {
	if h.dir == "" {
		httpjson.Error(w, http.StatusNotImplemented, "mods are not configured for this game")
		return
	}
	name, err := safeName(req.URL.Query().Get("name"))
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	// Use ConfinePath to validate and resolve the target within h.dir.
	// ConfinePath consolidates validation and symlink resolution in one function,
	// making the guard legible to CodeQL taint analysis.
	target, err := ConfinePath(h.dir, name)
	if err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	// Re-check inline so the guard is visible at the point of use.
	rootClean := filepath.Clean(h.dir)
	target = filepath.Clean(target)
	if target != rootClean && !strings.HasPrefix(target, rootClean+string(os.PathSeparator)) {
		httpjson.Error(w, http.StatusBadRequest, "path escape attempt")
		return
	}
	if _, statErr := os.Stat(target); errors.Is(statErr, os.ErrNotExist) {
		httpjson.Error(w, http.StatusNotFound, "no such mod")
		return
	}
	if err := h.removeEntry(name); err != nil {
		slog.Warn("mod remove", "err", err)
		httpjson.Error(w, http.StatusInternalServerError, "could not remove mod")
		return
	}
	h.updateManifest(func(mods map[string]*ModMeta) {
		delete(mods, name)
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) extAllowed(name string) bool {
	if len(h.exts) == 0 {
		return true
	}
	lower := strings.ToLower(name)
	for _, e := range h.exts {
		if strings.HasSuffix(lower, e) {
			return true
		}
	}
	return false
}

// safeName accepts a bare filename and rejects anything that could escape
// the mods directory (path separators, "..", leading dot/dash, control
// chars). Returns the cleaned base name.
func safeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("name is required")
	}
	if name != filepath.Base(name) || name != path.Base(name) {
		return "", errors.New("name must not contain a path")
	}
	if name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return "", errors.New("invalid mod name")
	}
	if len(name) > 200 {
		return "", errors.New("name is too long")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return "", errors.New("name contains an illegal character")
		}
	}
	return name, nil
}

// hostAllowed matches host against the allowlist: an exact hostname, or a
// leading-dot suffix (".example.com") matching that domain and any
// subdomain. Comparison is case-insensitive.
// modPortalPath is where mod-portal credentials are mounted, organized
// by provider. The agent tries to read username and token files for the
// requested provider; missing files are treated gracefully. Must stay in
// lockstep with the operator's copy in gameserver_modcreds.go.
// Package var so tests can override it.
var modPortalPath = "/etc/gameplane/mod-creds"

// redactURLErr strips the query from the URL embedded in a *url.Error so
// credentials injected for mod-portal downloads can never reach logs.
// Mutates in place to preserve the errors.Is/As chain.
func redactURLErr(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		if u, perr := url.Parse(ue.URL); perr == nil {
			u.RawQuery = ""
			ue.URL = u.String()
		}
	}
	return err
}

// injectModCreds appends username and token query parameters to the URL
// for providers that have credentials mounted. Missing or unreadable
// credential files are ignored, and the URL is returned unchanged.
// Credentials are only injected when provider=="factorio" and the URL
// hostname is mods.factorio.com or a .factorio.com subdomain.
func injectModCreds(urlStr, provider string) string {
	if provider != "factorio" {
		// Only factorio currently needs credentials.
		return urlStr
	}

	// Parse the URL first to validate the hostname and ensure injection is safe.
	u, err := url.Parse(urlStr)
	if err != nil {
		// Should not happen (URL was already validated), but be safe.
		return urlStr
	}
	hostname := strings.ToLower(u.Hostname())
	// Only inject for factorio.com or *.factorio.com.
	if hostname != "mods.factorio.com" && !strings.HasSuffix(hostname, ".factorio.com") {
		return urlStr
	}

	credsPath := fmt.Sprintf("%s/%s", modPortalPath, provider)
	username, err := os.ReadFile(fmt.Sprintf("%s/username", credsPath))
	if err != nil {
		// Credentials not available; download without auth.
		return urlStr
	}
	token, err := os.ReadFile(fmt.Sprintf("%s/token", credsPath))
	if err != nil {
		// Token missing; skip auth even if username exists.
		return urlStr
	}

	usernameStr := strings.TrimSpace(string(username))
	tokenStr := strings.TrimSpace(string(token))
	if usernameStr == "" || tokenStr == "" {
		// Empty credentials; skip auth.
		return urlStr
	}

	// Append credentials as query parameters. Re-parse the URL so we can
	// safely modify the query string without corrupting existing params.
	q := u.Query()
	q.Set("username", usernameStr)
	q.Set("token", tokenStr)
	u.RawQuery = q.Encode()
	return u.String()
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, a := range allowed {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if strings.HasPrefix(a, ".") {
			if host == a[1:] || strings.HasSuffix(host, a) {
				return true
			}
			continue
		}
		if host == a {
			return true
		}
	}
	return false
}

var (
	errTooLarge       = errors.New("download exceeds size limit")
	errHostNotAllowed = errors.New("redirect host not allowed")
	errBadArchive     = errors.New("not a valid archive")
	errSymlinkEntry   = errors.New("symlink archive entry")
)

// ssrfPolicy decides whether the agent may dial an address. It defaults to
// the shared public-only policy (netguard.IsPublic) and is a package var only
// so tests can permit loopback to reach an httptest server.
var ssrfPolicy = netguard.IsPublic

// newSafeClient builds an HTTP client that refuses to connect to non-public
// addresses (the shared SSRF guard, applied to the actual dialed IP so DNS
// rebinding can't slip past the host allowlist) and re-checks the host
// allowlist on every redirect (which the shared client leaves to the caller).
func newSafeClient(allowed []string) *http.Client {
	client := netguard.HTTPClient(2*time.Minute, ssrfPolicy)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if !hostAllowed(req.URL.Hostname(), allowed) {
			return errHostNotAllowed
		}
		return nil
	}
	return client
}
