package modsrc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

// newFSFetcher builds a Fetcher over any filesystem-shaped source —
// the shared engine behind the git, http, local, and upload types.
//
// load produces the filesystem to scan plus an optional digest
// override: git sources stamp every module with the resolved commit
// ("git:<sha>"); when empty, each module gets a sha256 content hash
// over its directory. refPrefix locates the source in ModuleEntry
// references ("<type>:<location>"). Each Index/Pull calls load fresh
// so the catalog tracks the source's current content.
func newFSFetcher(load func(ctx context.Context) (fs.FS, string, error), refPrefix string, allow []string) Fetcher {
	return &fsFetcher{load: load, refPrefix: refPrefix, allow: allow}
}

type fsFetcher struct {
	load      func(ctx context.Context) (fs.FS, string, error)
	refPrefix string
	allow     []string
}

// dirModule is one discovered module directory.
type dirModule struct {
	dir    string
	bundle *Bundle
}

func (f *fsFetcher) Index(ctx context.Context) ([]gameplanev1alpha1.ModuleEntry, []string, error) {
	mods, warnings, err := f.scan(ctx)
	if err != nil {
		return nil, nil, err
	}
	entries := make([]gameplanev1alpha1.ModuleEntry, 0, len(mods))
	for _, m := range mods {
		meta := m.bundle.Metadata
		entries = append(entries, gameplanev1alpha1.ModuleEntry{
			Name:          meta.Name,
			DisplayName:   meta.DisplayName,
			Summary:       meta.Summary,
			Game:          meta.Game,
			Categories:    meta.Categories,
			Icon:          meta.Icon,
			Reference:     f.reference(m.dir),
			Versions:      []string{meta.Version},
			LatestVersion: meta.Version,
			Digest:        m.bundle.Digest,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, warnings, nil
}

func (f *fsFetcher) Pull(ctx context.Context, name, version string) (*Bundle, error) {
	mods, _, err := f.scan(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range mods {
		if m.bundle.Metadata.Name != name {
			continue
		}
		if version != "" && m.bundle.Metadata.Version != version {
			return nil, fmt.Errorf("module %q is at version %q, requested %q (source moved on?)",
				name, m.bundle.Metadata.Version, version)
		}
		return m.bundle, nil
	}
	return nil, fmt.Errorf("module %q not found in %s", name, f.refPrefix)
}

func (f *fsFetcher) reference(dir string) string {
	if dir == "." {
		return f.refPrefix
	}
	return f.refPrefix + "/" + dir
}

// scan walks the loaded filesystem for directories containing a
// module.yaml. Invalid modules become warnings, never errors; a
// duplicate module name keeps the first (lexically shallowest) hit.
func (f *fsFetcher) scan(ctx context.Context) ([]dirModule, []string, error) {
	fsys, digestOverride, err := f.load(ctx)
	if err != nil {
		return nil, nil, err
	}
	var dirs []string
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if path.Base(p) == FileMetadata {
			dirs = append(dirs, path.Dir(p))
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan %s: %w", f.refPrefix, err)
	}

	var mods []dirModule
	var warnings []string
	seen := map[string]string{} // module name → dir
	for _, dir := range dirs {
		bundle, err := bundleFromDir(fsys, dir, digestOverride)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", f.reference(dir), err))
			continue
		}
		name := bundle.Metadata.Name
		if !allowed(name, f.allow) {
			continue
		}
		if prev, dup := seen[name]; dup {
			warnings = append(warnings, fmt.Sprintf("%s: duplicate module name %q (already provided by %s)",
				f.reference(dir), name, f.reference(prev)))
			continue
		}
		seen[name] = dir
		mods = append(mods, dirModule{dir: dir, bundle: bundle})
	}
	return mods, warnings, nil
}

// bundleFromDir loads one module directory into a Bundle. digest, when
// empty, is computed as a content hash over the directory.
func bundleFromDir(fsys fs.FS, dir, digest string) (*Bundle, error) {
	files := map[string][]byte{}
	for _, name := range []string{FileMetadata, FileTemplate, FileReadme, FileIcon} {
		data, err := fs.ReadFile(fsys, path.Join(dir, name))
		if err != nil {
			continue // FromFiles reports the required ones
		}
		files[name] = data
	}
	readLocaleFiles(fsys, dir, files)
	if digest == "" {
		var err error
		if digest, err = contentDigest(fsys, dir); err != nil {
			return nil, fmt.Errorf("hash module dir: %w", err)
		}
	}
	return FromFiles(digest, files)
}

// readLocaleFiles adds locales/<tag>.json (regular files only) to files under
// the key "locales/<tag>.json", the same names an OCI bundle uses. A file over
// oci.MaxLocaleLayerSize is not read: oci.OversizedLocale is stored in its
// place so the reconciler reports the language instead of dropping it. The
// files together are also bounded by oci.MaxLocaleTotalSize: languages are
// counted in name order, and one that would exceed the total is recorded as
// oversized the same way.
func readLocaleFiles(fsys fs.FS, dir string, files map[string][]byte) {
	entries, err := fs.ReadDir(fsys, path.Join(dir, "locales"))
	if err != nil {
		return
	}
	var budget oci.LocaleBudget
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || !strings.HasSuffix(name, ".json") {
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
		data, err := readFileCapped(fsys, path.Join(dir, key), oci.MaxLocaleLayerSize)
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

// readFileCapped reads at most limit+1 bytes of name, so a caller can tell a file
// over limit apart without holding more than that in memory.
func readFileCapped(fsys fs.FS, name string, limit int64) ([]byte, error) {
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

// contentDigest hashes every regular file under dir — relative path
// and bytes — in the deterministic lexical order fs.WalkDir guarantees,
// so identical content always yields the same digest regardless of
// which source type served it.
func contentDigest(fsys fs.FS, dir string) (string, error) {
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
