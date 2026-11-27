package themes

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/bundlefetch/oci"
	"github.com/GameplanePanel/gameplane/bundlefetch/source"
	"github.com/GameplanePanel/gameplane/bundlefetch/verify"
	"github.com/GameplanePanel/gameplane/themepkg"
	"github.com/google/go-containerregistry/pkg/authn"
)

// ThemeArtifactType is the OCI artifactType of a signed theme package
// (spec 043, contracts/theme-package.md). Module bundles use another type.
const ThemeArtifactType = "application/vnd.gameplane.theme.v1+json"

// MinRefreshInterval is the floor of a source's refresh interval (spec 043,
// data-model.md: min 300 s). Stored values below it are read as the floor.
const MinRefreshInterval = 300 * time.Second

// schedulerTick is how often Run checks which sources are due.
const schedulerTick = 30 * time.Second

// refreshTimeout bounds one scheduled refresh of one source.
const refreshTimeout = 2 * time.Minute

// ruleSignature is the problem rule reported when a package fails its
// signature policy. It is not a themepkg rule because themepkg validates the
// package content only.
const ruleSignature = "signature"

// themeFileNames is the closed set of files read from a directory source,
// the same paths themepkg accepts (contracts/theme-package.md).
var themeFileNames = []string{
	"theme.yaml",
	"theme.css",
	"previews/light.png",
	"previews/light.webp",
	"previews/dark.png",
	"previews/dark.webp",
}

// sourceSpec is the type-specific JSON of a theme source plus its allow globs
// (data-model.md theme_sources.spec).
type sourceSpec struct {
	OCI   *ociSpec   `json:"oci,omitempty"`
	Git   *gitSpec   `json:"git,omitempty"`
	HTTP  *httpSpec  `json:"http,omitempty"`
	Local *localSpec `json:"local,omitempty"`
	Allow []string   `json:"allow,omitempty"`
}

// ociSpec is the OCI settings: a registry prefix and the explicit theme names,
// since registries cannot be enumerated.
type ociSpec struct {
	URL      string   `json:"url"`
	Themes   []string `json:"themes"`
	Insecure bool     `json:"insecure"`
}

// gitSpec is the git settings: a repository, an optional ref and sub path.
type gitSpec struct {
	URL  string `json:"url"`
	Ref  string `json:"ref,omitempty"`
	Path string `json:"path,omitempty"`
}

// httpSpec is the HTTP archive settings.
type httpSpec struct {
	URL string `json:"url"`
}

// localSpec is a directory under the configured local root.
type localSpec struct {
	Path string `json:"path"`
}

// verifySpec is the signature policy JSON (data-model.md theme_sources.verify).
type verifySpec struct {
	Key                    *string  `json:"key,omitempty"`
	Keyless                *keyless `json:"keyless,omitempty"`
	RequireTransparencyLog bool     `json:"requireTransparencyLog,omitempty"`
}

// keyless is the keyless policy: the Fulcio issuer and subject of the signer.
type keyless struct {
	Issuer   string `json:"issuer"`
	Identity string `json:"identity"`
}

// indexResult is what one pass over a source produced.
type indexResult struct {
	// entries are the catalog rows to upsert: new or changed themes only.
	entries []db.ThemeCatalogEntry
	// keep lists every theme name the source still offers. Catalog rows of
	// other names are deleted; rows of kept names that failed to index stay.
	keep []string
	// failures are per-theme messages, without credentials.
	failures []string
}

// Refresh re-indexes the source with the given name now, regardless of its
// schedule. It returns ErrThemeNotFound for an unknown name and the source
// error when the whole source failed.
func (s *Service) Refresh(ctx context.Context, name string) error {
	src, err := s.store.GetThemeSource(ctx, name)
	if err != nil {
		return fmt.Errorf("refresh theme source %q: %w", name, err)
	}
	return s.refresh(ctx, src, false)
}

// Run refreshes every due source until ctx is done. It is started on the
// leader replica only. Each pass runs RunDue, then waits for the next tick.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()
	for {
		s.RunDue(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunDue refreshes, one at a time, every enabled source whose refresh interval
// has elapsed since its last sync. Failures are logged and do not stop the pass.
func (s *Service) RunDue(ctx context.Context) {
	srcs, err := s.store.ListThemeSources(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "list theme sources for refresh", "error", err)
		return
	}
	now := s.clock()
	for _, src := range srcs {
		if !isDue(src, now) {
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, refreshTimeout)
		err := s.refresh(sctx, src, false)
		cancel()
		if err != nil {
			s.logger.WarnContext(ctx, "theme source refresh failed", "source", src.Name, "error", redact(err.Error()))
		}
	}
}

// newSourceSettle is how long after its creation the scheduler leaves a source
// alone, so the credential Secret written by CreateSource is in place first.
const newSourceSettle = 10 * time.Second

// isDue reports whether an enabled source should be refreshed at now. A source
// that never synced is due. The interval is never below MinRefreshInterval.
func isDue(src db.ThemeSource, now time.Time) bool {
	if !src.Enabled {
		return false
	}
	// CreateSource inserts the row before it writes the credential Secret, so a
	// scheduled pass in that gap would read a Secret that is not there yet. A
	// source is left alone for newSourceSettle after its creation.
	if created, err := time.Parse(time.RFC3339, src.CreatedAt); err == nil {
		if age := now.Sub(created); age >= 0 && age < newSourceSettle {
			return false
		}
	}
	if src.LastSyncAt == nil {
		return true
	}
	last, err := time.Parse(time.RFC3339, *src.LastSyncAt)
	if err != nil {
		return true
	}
	return !now.Before(last.Add(effectiveInterval(src.RefreshIntervalS)))
}

// effectiveInterval converts a stored refresh interval in seconds, applying the
// floor.
func effectiveInterval(seconds int) time.Duration {
	d := time.Duration(seconds) * time.Second
	if d < MinRefreshInterval {
		return MinRefreshInterval
	}
	return d
}

// refresh indexes one source and writes its catalog and sync status. With
// recheck set, every theme is re-verified even when its digest is unchanged.
// A source-level failure keeps the existing catalog and returns the error.
func (s *Service) refresh(ctx context.Context, src db.ThemeSource, recheck bool) error {
	unlock := s.lockSource(src.ID)
	defer unlock()

	// The source may have been deleted while this pass waited for the lock. A
	// pass must not write catalog rows for a source id that no longer exists.
	if _, err := s.store.GetThemeSourceByID(ctx, src.ID); errors.Is(err, db.ErrThemeNotFound) {
		return nil
	} else if err != nil {
		return fmt.Errorf("refresh theme source %q: %w", src.Name, err)
	}

	now := s.clock()
	current, err := s.store.ListThemeCatalog(ctx, db.ThemeCatalogFilter{Source: src.Name})
	if err != nil {
		return fmt.Errorf("refresh theme source %q: %w", src.Name, err)
	}
	known := make(map[string]string, len(current))
	for _, e := range current {
		known[e.Name] = e.Digest
	}

	res, err := s.index(ctx, src, known, recheck)
	if err != nil {
		msg := redact(err.Error())
		if serr := s.store.SetThemeSourceSync(ctx, src.ID, now, &msg); serr != nil {
			s.logger.ErrorContext(ctx, "record theme source failure", "source", src.Name, "error", serr)
		}
		return fmt.Errorf("refresh theme source %q: %w", src.Name, err)
	}

	indexedAt := now.UTC().Format(time.RFC3339)
	for _, e := range res.entries {
		e.SourceID = src.ID
		e.IndexedAt = indexedAt
		if err := s.store.UpsertThemeCatalogEntry(ctx, e); err != nil {
			return fmt.Errorf("refresh theme source %q: %w", src.Name, err)
		}
	}
	if err := s.store.DeleteThemeCatalogEntriesNotIn(ctx, src.ID, res.keep); err != nil {
		return fmt.Errorf("refresh theme source %q: %w", src.Name, err)
	}
	var lastErr *string
	if len(res.failures) > 0 {
		msg := redact(strings.Join(res.failures, "; "))
		lastErr = &msg
	}
	if err := s.store.SetThemeSourceSync(ctx, src.ID, now, lastErr); err != nil {
		return fmt.Errorf("refresh theme source %q: %w", src.Name, err)
	}
	return nil
}

// index dispatches on the source type. A returned error is a source-level
// failure; per-theme failures are collected in the result.
func (s *Service) index(ctx context.Context, src db.ThemeSource, known map[string]string, recheck bool) (indexResult, error) {
	var sp sourceSpec
	if err := json.Unmarshal([]byte(src.Spec), &sp); err != nil {
		return indexResult{}, fmt.Errorf("decode source settings: %w", err)
	}
	cred, err := s.readCredential(ctx, src)
	if err != nil {
		return indexResult{}, err
	}
	if src.Type == "oci" {
		return s.indexOCI(ctx, src, sp, cred, known, recheck)
	}
	loader, err := s.treeLoader(src.Type, sp, cred)
	if err != nil {
		return indexResult{}, err
	}
	return s.indexTree(ctx, loader, sp.Allow, known, recheck)
}

// indexOCI lists the configured themes of an OCI source, pulls the latest
// version of each and indexes it. A theme whose pull fails keeps its catalog
// row and adds a failure. When every theme fails, the source fails.
func (s *Service) indexOCI(ctx context.Context, src db.ThemeSource, sp sourceSpec, cred map[string][]byte, known map[string]string, recheck bool) (indexResult, error) {
	if sp.OCI == nil || sp.OCI.URL == "" {
		return indexResult{}, errors.New("oci source has no url")
	}
	var creds oci.CredentialFunc
	if src.CredentialRef != nil {
		var err error
		if creds, err = ociCredentialFunc(cred, ociHost(sp.OCI.URL)); err != nil {
			return indexResult{}, err
		}
	}
	cli := s.ociFor(creds, sp.OCI.Insecure)
	verifier, err := s.verifier(ctx, src, sp.OCI.Insecure)
	if err != nil {
		return indexResult{}, err
	}

	var out indexResult
	for _, name := range sp.OCI.Themes {
		out.keep = append(out.keep, name)
		ref := source.OCIRef(sp.OCI.URL, name)
		tags, err := source.ListVersions(ctx, cli, ref)
		if err != nil {
			out.failures = append(out.failures, fmt.Sprintf("theme %s: %v", name, err))
			continue
		}
		source.SortVersionsDesc(tags)
		version, ok := latestVersion(tags)
		if !ok {
			out.failures = append(out.failures, fmt.Sprintf("theme %s: no semver version", name))
			continue
		}
		digest, files, err := source.PullFiles(ctx, cli, sp.OCI.URL, name, version)
		if err != nil {
			out.failures = append(out.failures, fmt.Sprintf("theme %s: %v", name, err))
			continue
		}
		if !recheck && known[name] == digest {
			continue
		}
		entry, err := s.buildEntry(ctx, name, version, tags, digest, files, verifier, ref)
		if err != nil {
			return indexResult{}, err
		}
		out.entries = append(out.entries, entry)
	}
	if len(sp.OCI.Themes) > 0 && len(out.failures) == len(sp.OCI.Themes) {
		return indexResult{}, fmt.Errorf("every theme failed: %s", strings.Join(out.failures, "; "))
	}
	return out, nil
}

// latestVersion picks the newest release from semver tags: the highest version
// without a pre-release part, else the highest pre-release. ok is false when
// tags is empty.
func latestVersion(tags []string) (string, bool) {
	sorted := append([]string(nil), tags...)
	source.SortVersionsDesc(sorted)
	for _, v := range sorted {
		if !isPrerelease(v) {
			return v, true
		}
	}
	if len(sorted) == 0 {
		return "", false
	}
	return sorted[0], true
}

// isPrerelease reports whether a version has a pre-release part ("1.0.0-rc.1").
// Build metadata after "+" is ignored.
func isPrerelease(v string) bool {
	core := strings.SplitN(v, "+", 2)[0]
	return strings.Contains(core, "-")
}

// treeLoader returns the loader of a git, http or local source.
func (s *Service) treeLoader(typ string, sp sourceSpec, cred map[string][]byte) (source.Loader, error) {
	switch typ {
	case "git":
		if sp.Git == nil || sp.Git.URL == "" {
			return nil, errors.New("git source has no url")
		}
		if err := source.CheckGitURL(sp.Git.URL); err != nil {
			return nil, fmt.Errorf("git url: %w", err)
		}
		loader, _ := source.GitLoader(sp.Git.URL, sp.Git.Ref, sp.Git.Path, gitAuth(cred), source.GitClone)
		return loader, nil
	case "http":
		if sp.HTTP == nil || sp.HTTP.URL == "" {
			return nil, errors.New("http source has no url")
		}
		if err := source.CheckHTTPURL(sp.HTTP.URL, false); err != nil {
			return nil, fmt.Errorf("http url: %w", err)
		}
		return source.ArchiveLoader(sp.HTTP.URL, httpAuthHeader(cred)), nil
	case "local":
		if sp.Local == nil {
			return nil, errors.New("local source has no path")
		}
		loader, _, err := source.LocalLoader(s.localRoot, sp.Local.Path)
		if err != nil {
			return nil, fmt.Errorf("local source: %w", err)
		}
		return loader, nil
	default:
		return nil, fmt.Errorf("unknown theme source type %q", typ)
	}
}

// indexTree indexes a git, http or local source. Every directory holding a
// theme.yaml is one theme, named after the directory. Each theme has one
// version, the one in its manifest.
func (s *Service) indexTree(ctx context.Context, loader source.Loader, allow []string, known map[string]string, recheck bool) (indexResult, error) {
	fsys, _, err := loader(ctx)
	if err != nil {
		return indexResult{}, fmt.Errorf("fetch source: %w", err)
	}
	dirs, err := source.FindDirs(fsys, "theme.yaml")
	if err != nil {
		return indexResult{}, fmt.Errorf("scan source: %w", err)
	}

	var out indexResult
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "." {
			out.failures = append(out.failures, "theme.yaml at the source root: put each theme in its own directory")
			continue
		}
		name := path.Base(dir)
		if !source.Allowed(name, allow) {
			continue
		}
		if seen[name] {
			out.failures = append(out.failures, fmt.Sprintf("theme %s: duplicate directory %q", name, dir))
			continue
		}
		seen[name] = true
		out.keep = append(out.keep, name)

		files, err := readThemeDir(fsys, dir)
		if err != nil {
			out.failures = append(out.failures, fmt.Sprintf("theme %s: %v", name, err))
			continue
		}
		digest := source.DigestFiles(files)
		if !recheck && known[name] == digest {
			continue
		}
		entry, err := s.buildEntry(ctx, name, "", nil, digest, files, nil, "")
		if err != nil {
			return indexResult{}, err
		}
		out.entries = append(out.entries, entry)
	}
	return out, nil
}

// readThemeDir reads the theme files of one directory. Each read is capped at
// the package limit, so an oversized file cannot exhaust memory; Validate
// reports it as oversized.
func readThemeDir(fsys fs.FS, dir string) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, name := range themeFileNames {
		data, err := source.ReadFileCapped(fsys, path.Join(dir, name), themepkg.MaxPackageBytes)
		if err != nil {
			continue
		}
		files[name] = data
	}
	if _, ok := files["theme.yaml"]; !ok {
		return nil, errors.New("theme.yaml is not readable")
	}
	return files, nil
}

// buildEntry validates one theme package and turns it into a catalog row. tag
// is the version the registry offered (empty for directory sources, whose
// version is the manifest's). tags lists every version of the theme, or nil
// to use the manifest version. verifier, when set, checks the signature of the
// package at ref and digest; a failed check yields status unverified.
func (s *Service) buildEntry(ctx context.Context, name, tag string, tags []string, digest string, files map[string][]byte, v verify.Verifier, ref string) (db.ThemeCatalogEntry, error) {
	res, problems := themepkg.Validate(files)
	if res.Manifest.Name != name {
		problems = append(problems, themepkg.Problem{Rule: themepkg.RuleSchema,
			Detail: fmt.Sprintf("theme.yaml name %q does not match the theme %q", res.Manifest.Name, name)})
	}
	if tag != "" && res.Manifest.Version != tag {
		problems = append(problems, themepkg.Problem{Rule: themepkg.RuleSchema,
			Detail: fmt.Sprintf("theme.yaml version %q does not match the published version %q", res.Manifest.Version, tag)})
	}

	status := "valid"
	if len(problems) > 0 {
		status = "invalid"
	} else if v != nil {
		if err := v.Verify(ctx, ref, digest); err != nil {
			s.logger.WarnContext(ctx, "theme signature check failed", "theme", name, "error", redact(err.Error()))
			status = "unverified"
			problems = append(problems, themepkg.Problem{Rule: ruleSignature, Detail: "the package signature could not be verified"})
		}
	}

	version := tag
	if res.Manifest.Version != "" {
		version = res.Manifest.Version
	}
	if tags == nil {
		tags = []string{}
		if version != "" {
			tags = append(tags, version)
		}
	}
	versions, err := json.Marshal(tags)
	if err != nil {
		return db.ThemeCatalogEntry{}, fmt.Errorf("encode versions of %s: %w", name, err)
	}
	metadata, err := marshalMetadata(res.Manifest)
	if err != nil {
		return db.ThemeCatalogEntry{}, fmt.Errorf("encode metadata of %s: %w", name, err)
	}
	problemsJSON, err := json.Marshal(nonNilProblems(problems))
	if err != nil {
		return db.ThemeCatalogEntry{}, fmt.Errorf("encode problems of %s: %w", name, err)
	}

	entry := db.ThemeCatalogEntry{
		Name:        name,
		Version:     version,
		Versions:    string(versions),
		Digest:      digest,
		Metadata:    metadata,
		Tokens:      "{}",
		Status:      status,
		Problems:    string(problemsJSON),
		LowContrast: res.LowContrast,
	}
	if status == "invalid" {
		// Content that failed validation is never served: only the metadata
		// (for the list) and the problems are kept.
		entry.LowContrast = false
		return entry, nil
	}
	tokens, err := json.Marshal(res.Manifest.Tokens)
	if err != nil {
		return db.ThemeCatalogEntry{}, fmt.Errorf("encode tokens of %s: %w", name, err)
	}
	entry.Tokens = string(tokens)
	if res.CSS != "" {
		css := res.CSS
		entry.CSS = &css
	}
	if p, ok := res.Previews["light"]; ok {
		entry.PreviewLight = p.Data
	}
	if p, ok := res.Previews["dark"]; ok {
		entry.PreviewDark = p.Data
	}
	return entry, nil
}

// themeMetadata is the list-facing part of a manifest, stored as JSON.
type themeMetadata struct {
	DisplayName string   `json:"displayName"`
	Author      string   `json:"author"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	License     string   `json:"license"`
	Homepage    string   `json:"homepage,omitempty"`
	Changelog   string   `json:"changelog,omitempty"`
}

// marshalMetadata encodes the list-facing manifest fields. An undecodable
// manifest yields an empty object.
func marshalMetadata(m themepkg.Manifest) (string, error) {
	if m.Name == "" && m.DisplayName == "" {
		return "{}", nil
	}
	b, err := json.Marshal(themeMetadata{
		DisplayName: m.DisplayName,
		Author:      m.Author,
		Description: m.Description,
		Tags:        m.Tags,
		License:     m.License,
		Homepage:    m.Homepage,
		Changelog:   m.Changelog,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// nonNilProblems keeps the stored problems list a JSON array, never null.
func nonNilProblems(p []themepkg.Problem) []themepkg.Problem {
	if p == nil {
		return []themepkg.Problem{}
	}
	return p
}

// verifier returns the cached signature verifier of a source, or nil when the
// source has no verify policy. Building one may fetch the Sigstore trust root,
// so it happens here, during a refresh, never at startup. The cache key covers
// the policy and the insecure flag, so an edited policy gets a new verifier.
func (s *Service) verifier(ctx context.Context, src db.ThemeSource, insecure bool) (verify.Verifier, error) {
	if src.Verify == nil {
		return nil, nil
	}
	key := fmt.Sprintf("%d:%t:%x", src.ID, insecure, sha256.Sum256([]byte(*src.Verify)))
	s.verifiersMu.Lock()
	defer s.verifiersMu.Unlock()
	if v, ok := s.verifiers[key]; ok {
		return v, nil
	}
	v, err := s.verifierFor(ctx, *src.Verify, insecure)
	if err != nil {
		return nil, fmt.Errorf("signature policy: %w", err)
	}
	s.verifiers[key] = v
	return v, nil
}

// defaultVerifier builds the bundlefetch verifier for a verify JSON policy. The
// The signature check pulls anonymously: the source credential authenticates the
// theme pull only, so a signature policy on a private registry needs a public
// signature artifact.
func defaultVerifier(ctx context.Context, raw string, insecure bool) (verify.Verifier, error) {
	var vs verifySpec
	if err := json.Unmarshal([]byte(raw), &vs); err != nil {
		return nil, fmt.Errorf("decode verify policy: %w", err)
	}
	switch {
	case vs.Keyless != nil:
		return verify.NewKeyless(ctx, vs.Keyless.Issuer, vs.Keyless.Identity, authn.Anonymous, insecure)
	case vs.Key != nil && *vs.Key != "":
		return verify.NewKeyed(ctx, []byte(*vs.Key), authn.Anonymous, insecure, vs.RequireTransparencyLog)
	default:
		return nil, errors.New("verify policy has neither a key nor a keyless identity")
	}
}

// defaultOCI is the production registry client: creds (nil for anonymous) are
// used for the source's registry only, plain HTTP only when the source says so,
// dialing through netguard, and bounded to theme packages.
func defaultOCI(creds oci.CredentialFunc, insecure bool) source.OCIClient {
	return oci.New(creds, insecure).WithArtifactType(ThemeArtifactType).WithMaxLayerBytes(themepkg.MaxPackageBytes)
}

// userinfoPattern matches the "user:password@" part of a URL in an error text.
var userinfoPattern = regexp.MustCompile(`://[^/@\s]+@`)

// redact removes URL userinfo from an error text before it is stored as
// last_error or logged.
func redact(msg string) string {
	return userinfoPattern.ReplaceAllString(msg, "://")
}
