package source

import (
	"context"
	"fmt"
	"path"
	"sort"

	"golang.org/x/mod/semver"
)

// OCIClient is the transport slice of *oci.Client the OCI sources need.
// An interface so tests can swap in an in-process fake.
type OCIClient interface {
	// ListTags returns the semver tags under ref, sorted descending.
	ListTags(ctx context.Context, ref string) ([]string, error)
	// Pull fetches the bundle at ref:reference and returns the manifest
	// digest plus the layer blobs keyed by their title annotation.
	Pull(ctx context.Context, ref, reference string) (string, map[string][]byte, error)
}

// OCIRef joins a registry prefix and a module name into a repository reference.
// OCI sources never auto-discover: registries cannot be enumerated portably, so
// the caller lists the names.
func OCIRef(prefix, name string) string { return path.Join(prefix, name) }

// ListVersions lists the semver tags of the repository at ref, newest first.
func ListVersions(ctx context.Context, cli OCIClient, ref string) ([]string, error) {
	tags, err := cli.ListTags(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	return tags, nil
}

// SortVersionsDesc orders semver version strings (without the "v" prefix)
// newest first, in place, so unchanged inputs produce a stable output.
func SortVersionsDesc(versions []string) {
	sort.Strings(versions)
	sort.Slice(versions, func(i, j int) bool {
		return semver.Compare("v"+versions[i], "v"+versions[j]) > 0
	})
}

// PullFiles pulls the bundle named name under the registry prefix at version
// and returns its manifest digest plus the layer blobs keyed by title.
func PullFiles(ctx context.Context, cli OCIClient, prefix, name, version string) (string, map[string][]byte, error) {
	return cli.Pull(ctx, OCIRef(prefix, name), version)
}
