package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"

	"sort"

	"github.com/GameplanePanel/gameplane/bundlefetch/oci"
)

// Loader produces the filesystem to scan plus an optional digest override: a
// git source stamps every bundle with the resolved commit ("git:<sha>"); when
// the digest is empty each bundle gets a content hash over its directory. It is
// called afresh on every index and pull, so a catalog tracks the source's
// current content.
type Loader func(ctx context.Context) (fsys fs.FS, digest string, err error)

// FindDirs walks fsys and returns, in walk order, every directory that holds a
// regular file named marker (for example "module.yaml").
func FindDirs(fsys fs.FS, marker string) ([]string, error) {
	var dirs []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if path.Base(p) == marker {
			dirs = append(dirs, path.Dir(p))
		}
		return nil
	})
	return dirs, err
}

// ReadDir reads the named files of one bundle directory into a map keyed by
// file name; a missing file is left out, so the caller's parser reports the
// required ones. Locale files (locales/<tag>.json) are added the way
// ReadLocaleFiles does, skipping the localeSkip names. When digest is empty it
// is computed with ContentDigest; the digest used is returned.
func ReadDir(fsys fs.FS, dir, digest string, names []string, localeSkip ...string) (map[string][]byte, string, error) {
	files := map[string][]byte{}
	for _, name := range names {
		data, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			continue
		}
		files[name] = data
	}
	ReadLocaleFiles(fsys, dir, files, localeSkip...)
	if digest == "" {
		var err error
		if digest, err = ContentDigest(fsys, dir); err != nil {
			return nil, "", fmt.Errorf("hash module dir: %w", err)
		}
	}
	return files, digest, nil
}

// ReadLocaleFiles adds locales/<tag>.json (regular files only) to files under
// the key "locales/<tag>.json", the same names an OCI bundle uses. A file over
// oci.MaxLocaleLayerSize is not read: oci.OversizedLocale is stored in its
// place so the caller reports the language instead of dropping it. The files
// together are also bounded by oci.MaxLocaleTotalSize: languages are counted in
// name order, and one that would exceed the total is recorded as oversized the
// same way. Names in skip (for example the translator review file) are not
// languages; they neither become a locale nor count toward the size budget.
func ReadLocaleFiles(fsys fs.FS, dir string, files map[string][]byte, skip ...string) {
	entries, err := fs.ReadDir(fsys, path.Join(dir, "locales"))
	if err != nil {
		return
	}
	var budget oci.LocaleBudget
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasSuffix(name, ".json") || contains(skip, name) {
			continue
		}
		key := oci.LocaleLayerPrefix + name
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() > oci.MaxLocaleLayerSize || !budget.Take(info.Size()) {
			files[key] = oci.OversizedLocale
			continue
		}
		data, err := ReadFileCapped(fsys, path.Join(dir, key), oci.MaxLocaleLayerSize)
		if err != nil {
			continue
		}
		if len(data) > oci.MaxLocaleLayerSize {
			// The file grew after the size check; the cap still holds.
			files[key] = oci.OversizedLocale
			continue
		}
		files[key] = data
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// ReadFileCapped reads at most limit+1 bytes of name, so a caller can tell a file
// over limit apart without holding more than that in memory.
func ReadFileCapped(fsys fs.FS, name string, limit int64) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return data, nil
}

// ContentDigest hashes every regular file under dir — relative path
// and bytes — in the deterministic lexical order fs.WalkDir guarantees,
// so identical content always yields the same digest regardless of
// which source type served it.
func ContentDigest(fsys fs.FS, dir string) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := p[len(dir):]
		_, _ = io.WriteString(h, rel)
		_, _ = h.Write([]byte{0})
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		_, _ = h.Write(data)
		_, _ = h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// DigestFiles is the map-shaped sibling of ContentDigest: a deterministic hash
// over sorted filename+content pairs. The same content hashes identically
// through either.
func DigestFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		_, _ = io.WriteString(h, "/"+name)
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(files[name])
		_, _ = h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// Allowed reports whether name passes an allow-list. Entries are exact names or
// path.Match globs; an empty list allows everything.
func Allowed(name string, allow []string) bool {
	if len(allow) == 0 {
		return true
	}
	for _, pat := range allow {
		if pat == name {
			return true
		}
		if ok, err := path.Match(pat, name); err == nil && ok {
			return true
		}
	}
	return false
}
