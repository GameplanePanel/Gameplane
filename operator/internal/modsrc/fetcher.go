package modsrc

import (
	"context"
	"fmt"
	"path"

	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

// Fetcher is one ModuleSource's view of its backing store. Index builds
// the catalog; Pull fetches one module bundle for installation.
//
// Index error contract: a non-nil error means the source as a whole is
// unreachable and no catalog should be published. Per-module failures
// are NOT errors — the failed module appears in the returned entries as
// a stub (Name + Reference, no versions) and a human-readable warning
// is appended, so one bad module never blanks the whole catalog.
type Fetcher interface {
	Index(ctx context.Context) (entries []gameplanev1alpha1.ModuleEntry, warnings []string, err error)
	Pull(ctx context.Context, name, version string) (*Bundle, error)
}

// Options carries operator-level configuration that individual fetchers
// need (set from CLI flags, not from the ModuleSource spec).
type Options struct {
	// LocalRoot is the base directory that local-type sources resolve
	// their relative paths under. Empty disables local sources.
	LocalRoot string
}

// ForSource builds the Fetcher for a ModuleSource based on its type.
// c and namespace are used to resolve credential Secrets living in the
// operator namespace.
func ForSource(ctx context.Context, c client.Client, namespace string, src *gameplanev1alpha1.ModuleSource, opts Options) (Fetcher, error) {
	switch src.Spec.Type {
	// Empty matches pre-defaulting objects constructed in Go (the API
	// server always defaults type to "oci").
	case gameplanev1alpha1.ModuleSourceTypeOCI, "":
		spec := src.Spec.OCI
		if spec == nil {
			return nil, fmt.Errorf("spec.oci is required when spec.type is oci")
		}
		creds, err := oci.CredentialFromSecret(ctx, c, namespace, spec.PullSecretRef)
		if err != nil {
			return nil, fmt.Errorf("resolve credentials: %w", err)
		}
		names := make([]string, 0, len(spec.Modules))
		for _, m := range spec.Modules {
			if allowed(m.Name, src.Spec.Allow) {
				names = append(names, m.Name)
			}
		}
		return NewOCI(oci.New(creds, spec.Insecure), spec.URL, names), nil
	case gameplanev1alpha1.ModuleSourceTypeGit:
		spec := src.Spec.Git
		if spec == nil {
			return nil, fmt.Errorf("spec.git is required when spec.type is git")
		}
		return newGit(ctx, c, namespace, spec, src.Spec.Allow)
	case gameplanev1alpha1.ModuleSourceTypeLocal:
		spec := src.Spec.Local
		if spec == nil {
			return nil, fmt.Errorf("spec.local is required when spec.type is local")
		}
		return newLocal(opts.LocalRoot, spec.Path, src.Spec.Allow)
	case gameplanev1alpha1.ModuleSourceTypeHTTP:
		spec := src.Spec.HTTP
		if spec == nil {
			return nil, fmt.Errorf("spec.http is required when spec.type is http")
		}
		return newHTTP(ctx, c, namespace, spec, src.Spec.Allow)
	case gameplanev1alpha1.ModuleSourceTypeUpload:
		return newUpload(c, namespace, src.Spec.Allow), nil
	default:
		return nil, fmt.Errorf("unknown source type %q", src.Spec.Type)
	}
}

// allowed reports whether name passes the source's allow-list. Entries
// are exact names or path.Match globs; an empty list allows everything.
func allowed(name string, allow []string) bool {
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
