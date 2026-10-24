package mods

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// manifestName is the per-volume install ledger. The leading dot keeps it
// out of mod listings and out of reach of client-supplied names (safeName
// rejects dotfiles). Filesystem confinement still applies to ledger reads and
// replacements. Each per-(version+loader)
// mod volume carries its own manifest, so metadata follows the files across
// game-version switches.
const manifestName = ".gameplane-mods.json"

// ModMeta records where an installed mod came from. The dashboard supplies
// it when installing from a registry (provider "upload" for direct uploads);
// the agent stamps sourceUrl/installedAt itself. Listings echo it back so
// the API can check for updates. A mod with no entry is "unmanaged" — a file
// placed out-of-band or installed before manifests existed.
type ModMeta struct {
	Provider      string `json:"provider"`
	ProjectID     string `json:"projectId,omitempty"`
	ProjectName   string `json:"projectName,omitempty"`
	VersionID     string `json:"versionId,omitempty"`
	VersionNumber string `json:"versionNumber,omitempty"`
	GameVersion   string `json:"gameVersion,omitempty"`
	Loader        string `json:"loader,omitempty"`
	SourceURL     string `json:"sourceUrl,omitempty"`
	InstalledAt   string `json:"installedAt,omitempty"`
}

// manifest is the on-disk schema, keyed by installed name (file name, or
// folder name for extract loaders — the same key space as the listing).
type manifest struct {
	Version int                 `json:"version"`
	Mods    map[string]*ModMeta `json:"mods"`
}

// loadManifest reads the manifest under dir. A missing or corrupt file
// degrades to an empty manifest — mods then list as unmanaged rather than
// breaking the endpoint; the next mutation rewrites a clean file.
func loadManifest(dir string) *manifest {
	// Validate that dir doesn't escape expected bounds.
	dirClean := filepath.Clean(dir)
	manifestPath := filepath.Join(dirClean, manifestName)
	data, err := os.ReadFile(manifestPath)
	return decodeManifest(data, err)
}

func decodeManifest(data []byte, err error) *manifest {
	empty := &manifest{Version: 1, Mods: map[string]*ModMeta{}}
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("mod manifest read", "err", err)
		}
		return empty
	}
	var parsed manifest
	if err := json.Unmarshal(data, &parsed); err != nil {
		slog.Warn("mod manifest corrupt; treating as empty", "err", err)
		return empty
	}
	if parsed.Mods == nil {
		parsed.Mods = map[string]*ModMeta{}
	}
	parsed.Version = 1
	return &parsed
}

// updateManifestInRoot keeps DELETE's metadata on the same directory handle as
// the removal. Reopening h.dir here would let a concurrent directory replacement
// redirect this otherwise confined operation. Metadata remains best-effort.
func (h *handler) updateManifestInRoot(root *os.Root, fn func(map[string]*ModMeta)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := decodeManifest(root.ReadFile(manifestName))
	fn(m.Mods)
	for name := range m.Mods {
		if _, err := root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			delete(m.Mods, name)
		}
	}
	if err := m.saveInRoot(root); err != nil {
		slog.Warn("mod manifest write", "err", err)
	}
}

func (m *manifest) saveInRoot(root *os.Root) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmpName := ".manifest-" + rand.Text()
	tmp, err := root.OpenFile(tmpName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(tmpName) }()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename(tmpName, manifestName)
}

// save writes the manifest atomically (dot-prefixed temp + rename, the same
// pattern as downloads) so a crash never leaves a torn file behind.
func (m *manifest) save(dir string) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".manifest-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(tmpName)
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	}
	return os.Rename(tmpName, filepath.Join(dir, manifestName))
}

// updateManifest applies fn to the entry map under lock and persists the
// result, pruning entries whose backing files are gone (self-healing after
// out-of-band deletes or a crash between a rename and the manifest write).
// Failures are logged, not returned: metadata is best-effort and must never
// fail a file operation that already succeeded.
func (h *handler) updateManifest(fn func(map[string]*ModMeta)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := loadManifest(h.dir)
	fn(m.Mods)
	for name := range m.Mods {
		if _, err := os.Stat(filepath.Join(h.dir, name)); errors.Is(err, fs.ErrNotExist) {
			delete(m.Mods, name)
		}
	}
	if err := m.save(h.dir); err != nil {
		slog.Warn("mod manifest write", "err", err)
	}
}
