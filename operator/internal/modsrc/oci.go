package modsrc

import (
	"context"
	"fmt"
	"path"
	"sort"

	"golang.org/x/mod/semver"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// OCIClient is the transport slice of *oci.Client the OCI fetcher
// needs. An interface so tests can swap in an in-process fake.
type OCIClient interface {
	// ListTags returns the semver tags under ref, sorted descending.
	ListTags(ctx context.Context, ref string) ([]string, error)
	// Pull fetches the bundle at ref:reference and returns the manifest
	// digest plus the layer blobs keyed by their title annotation.
	Pull(ctx context.Context, ref, reference string) (string, map[string][]byte, error)
}

// NewOCI builds a Fetcher over one OCI registry prefix. modules is the
// explicit list from the ModuleSource spec — registries cannot be
// enumerated portably, so OCI sources never auto-discover.
func NewOCI(cli OCIClient, url string, modules []string) Fetcher {
	return &ociFetcher{cli: cli, url: url, modules: modules}
}

type ociFetcher struct {
	cli     OCIClient
	url     string
	modules []string
}

func (f *ociFetcher) Index(ctx context.Context) ([]gameplanev1alpha1.ModuleEntry, []string, error) {
	entries := make([]gameplanev1alpha1.ModuleEntry, 0, len(f.modules))
	var warnings []string
	var firstErr error
	for _, name := range f.modules {
		ref := path.Join(f.url, name)
		entry, err := f.indexModule(ctx, name, ref)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("index %s: %v", name, err))
			if firstErr == nil {
				firstErr = err
			}
			// Keep a stub entry so the UI shows the failure inline.
			entries = append(entries, gameplanev1alpha1.ModuleEntry{Name: name, Reference: ref})
			continue
		}
		entries = append(entries, entry)
	}
	// Every module errored: the registry itself is almost certainly
	// unreachable. Treat as total failure so the source isn't published
	// as a healthy catalog of empty stubs.
	if len(f.modules) > 0 && len(warnings) == len(f.modules) {
		return nil, nil, fmt.Errorf("all %d module(s) failed to index: %w", len(f.modules), firstErr)
	}
	return entries, warnings, nil
}

// indexModule lists tags for a module and probes its latest version to
// pick up display metadata.
func (f *ociFetcher) indexModule(ctx context.Context, name, ref string) (gameplanev1alpha1.ModuleEntry, error) {
	tags, err := f.cli.ListTags(ctx, ref)
	if err != nil {
		return gameplanev1alpha1.ModuleEntry{}, fmt.Errorf("list tags: %w", err)
	}
	entry := gameplanev1alpha1.ModuleEntry{
		Name:      name,
		Reference: ref,
		Versions:  tags,
	}
	if len(tags) == 0 {
		return entry, fmt.Errorf("no semver tags found at %s", ref)
	}
	entry.LatestVersion = tags[0]

	bundle, err := f.Pull(ctx, name, entry.LatestVersion)
	if err != nil {
		return entry, fmt.Errorf("pull metadata for %s:%s: %w", ref, entry.LatestVersion, err)
	}
	if bundle.Metadata.Name != name {
		return entry, fmt.Errorf("bundle metadata name %q != source ref name %q", bundle.Metadata.Name, name)
	}
	entry.DisplayName = bundle.Metadata.DisplayName
	entry.Summary = bundle.Metadata.Summary
	entry.Game = bundle.Metadata.Game
	entry.Categories = bundle.Metadata.Categories
	entry.Icon = bundle.Metadata.Icon
	// Record the latest bundle's digest so the module controller can detect
	// content drift behind a reused tag/version (dir and upload sources do
	// the same); without it entry.Digest stays empty and the controller's
	// digest comparison is skipped.
	entry.Digest = bundle.Digest
	// Stable order on output so unchanged inputs produce no status churn.
	sort.Strings(entry.Versions)
	sort.Slice(entry.Versions, func(i, j int) bool {
		return semver.Compare("v"+entry.Versions[i], "v"+entry.Versions[j]) > 0
	})
	return entry, nil
}

func (f *ociFetcher) Pull(ctx context.Context, name, version string) (*Bundle, error) {
	ref := path.Join(f.url, name)
	digest, files, err := f.cli.Pull(ctx, ref, version)
	if err != nil {
		return nil, err
	}
	return FromFiles(digest, files)
}
