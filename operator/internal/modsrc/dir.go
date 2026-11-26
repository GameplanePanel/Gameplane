package modsrc

import (
	"context"
	"fmt"
	"io/fs"
	"sort"

	"github.com/GameplanePanel/gameplane/bundlefetch/source"
	"github.com/GameplanePanel/gameplane/i18n/pack"
	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
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
	dirs, err := source.FindDirs(fsys, FileMetadata)
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
	files, digest, err := source.ReadDir(fsys, dir, digest,
		[]string{FileMetadata, FileTemplate, FileReadme, FileIcon}, pack.ReviewFile)
	if err != nil {
		return nil, err
	}
	return FromFiles(digest, files)
}

// readLocaleFiles adds locales/<tag>.json to files (see source.ReadLocaleFiles).
// The review file (pack.ReviewFile, translator source hashes) is not a
// language and is skipped, so it neither becomes a locale nor counts toward the
// size budget.
func readLocaleFiles(fsys fs.FS, dir string, files map[string][]byte) {
	source.ReadLocaleFiles(fsys, dir, files, pack.ReviewFile)
}

// readFileCapped reads at most limit+1 bytes of name (see source.ReadFileCapped).
func readFileCapped(fsys fs.FS, name string, limit int64) ([]byte, error) {
	return source.ReadFileCapped(fsys, name, limit)
}

// contentDigest hashes every regular file under dir (see source.ContentDigest).
func contentDigest(fsys fs.FS, dir string) (string, error) {
	return source.ContentDigest(fsys, dir)
}
