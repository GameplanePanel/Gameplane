package themes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/bundlefetch/source"
)

// DefaultSourceRefreshS is the refresh interval a source gets when the request
// names none, in seconds (one hour).
const DefaultSourceRefreshS = 3600

// MinSourceRefreshS is the shortest refresh interval a source may ask for, in
// seconds. It equals MinRefreshInterval, the floor the scheduler enforces.
const MinSourceRefreshS = int(MinRefreshInterval / time.Second)

// maxSourceNameLen caps source names so that a name derived from one still fits
// the 63-character limit of a Kubernetes object name (OD-5, labelled credential
// Secret). Enforced now so the limit does not appear later as a breaking change.
const maxSourceNameLen = 40

// sourceNameRE is the source name rule: a lowercase DNS label of at most
// maxSourceNameLen characters.
var sourceNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,38}[a-z0-9])?$`)

// themeNameRE is the theme name rule. It matches the DNS-1123 name rule of the
// theme manifest (themepkg), so a theme listed in a source is always a valid name.
var themeNameRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// ErrSourceBuiltin reports that the built-in official source cannot be deleted.
// It can be disabled, and its settings edited, but it is never removed
// (design brief, 043-theme-marketplace, admin theme sources).
var ErrSourceBuiltin = errors.New("the built-in theme source cannot be deleted")

// SourceError is a refusal of submitted theme source settings. Its text names
// the rule that failed and never contains credentials. Handlers show it as a
// 422 response.
type SourceError struct {
	// Msg is the English text of the refusal.
	Msg string
}

// Error returns the refusal text.
func (e *SourceError) Error() string { return e.Msg }

// sourceErrf builds a SourceError from a format string.
func sourceErrf(format string, args ...any) error {
	return &SourceError{Msg: fmt.Sprintf(format, args...)}
}

// SourceSettings is the admin-editable part of a theme source as the API
// receives it (contracts/themes-api.md, theme sources).
type SourceSettings struct {
	// Type is "oci", "git", "http" or "local".
	Type string
	// Credential is the optional credential JSON, in the shape checkCredential
	// reads for Type. Absent (nil) means none on create and keep on update. The
	// JSON null clears it on update. The value is stored as a labelled Secret and
	// is never echoed back.
	Credential json.RawMessage
	// Spec is the type-specific JSON in the shape the indexer reads: one of
	// {"oci": {...}}, {"git": {...}}, {"http": {...}} or {"local": {...}}, plus an
	// optional "allow" list of glob patterns.
	Spec json.RawMessage
	// Verify is the signature policy JSON, or nil or null for none. Only oci
	// sources may carry one.
	Verify json.RawMessage
	// RefreshIntervalS is the seconds between automatic refreshes. Nil uses
	// DefaultSourceRefreshS.
	RefreshIntervalS *int
	// Enabled is the enabled flag. Nil means true on create and unchanged on update.
	Enabled *bool
}

// checkedSettings is a validated SourceSettings, ready to store.
type checkedSettings struct {
	typ      string
	spec     string  // canonical JSON of the spec
	verify   *string // canonical JSON of the policy, nil for none
	refreshS int
}

// sourceSpecOf decodes a stored spec. A spec that does not decode yields the
// zero value; stored specs were validated on write, so this is only a guard.
func sourceSpecOf(raw string) sourceSpec {
	var sp sourceSpec
	_ = json.Unmarshal([]byte(raw), &sp)
	return sp
}

// specURL returns the primary location of a spec: the registry prefix, git or
// HTTP URL, or local path. A change of it drops the source's credential
// reference (a credential must not follow a changed location).
func specURL(raw string) string {
	sp := sourceSpecOf(raw)
	switch {
	case sp.OCI != nil:
		return sp.OCI.URL
	case sp.Git != nil:
		return sp.Git.URL
	case sp.HTTP != nil:
		return sp.HTTP.URL
	case sp.Local != nil:
		return sp.Local.Path
	}
	return ""
}

// decodeSpec parses the spec JSON strictly: unknown keys are an error, so a
// misspelled setting never silently has no effect.
func decodeSpec(raw json.RawMessage) (sourceSpec, error) {
	var sp sourceSpec
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return sp, sourceErrf("spec is required")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sp); err != nil {
		return sp, sourceErrf("spec: %v", err)
	}
	return sp, nil
}

// decodeVerify parses the verify JSON strictly.
func decodeVerify(raw json.RawMessage) (verifySpec, error) {
	var vs verifySpec
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&vs); err != nil {
		return vs, sourceErrf("verify: %v", err)
	}
	return vs, nil
}

// rejectUserinfo refuses a URL that carries a user name or password. Credentials
// are stored as a reference, never in the URL, so an admin cannot leak one into
// the spec, the list response or the audit target. Scp-like git addresses
// (git@host:path) do not parse as URLs and pass.
func rejectUserinfo(raw string) error {
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		return sourceErrf("url must not contain credentials")
	}
	return nil
}

// checkSettings validates settings for the source name and returns them in
// canonical form. It mirrors the ModuleSource rules (type-specific settings
// required, others refused, verify only for oci, exactly one of key or keyless,
// refresh interval floor) and the fetch rules of bundlefetch (URL schemes, no
// plain HTTP, no metadata hosts). localRoot is the configured local root;
// empty disables local sources.
func checkSettings(name string, in SourceSettings, localRoot string) (checkedSettings, error) {
	var c checkedSettings
	switch in.Type {
	case "oci", "git", "http", "local":
		c.typ = in.Type
	default:
		return c, sourceErrf("unknown source type %q", in.Type)
	}
	sp, err := decodeSpec(in.Spec)
	if err != nil {
		return c, err
	}
	configs := []struct {
		typ string
		set bool
	}{{"oci", sp.OCI != nil}, {"git", sp.Git != nil}, {"http", sp.HTTP != nil}, {"local", sp.Local != nil}}
	for _, cfg := range configs {
		if cfg.set && cfg.typ != in.Type {
			return c, sourceErrf("%s settings are only valid for type %s", cfg.typ, cfg.typ)
		}
	}
	for _, a := range sp.Allow {
		if _, err := path.Match(a, ""); err != nil {
			return c, sourceErrf("allow pattern %q is not a valid glob", a)
		}
	}
	isBuiltin := name == OfficialSourceName
	if isBuiltin && in.Type != "oci" {
		return c, sourceErrf("the built-in source must stay type oci")
	}
	switch in.Type {
	case "oci":
		if err := checkOCI(sp.OCI, isBuiltin); err != nil {
			return c, err
		}
	case "git":
		if sp.Git == nil || sp.Git.URL == "" {
			return c, sourceErrf("git.url is required")
		}
		if err := rejectUserinfo(sp.Git.URL); err != nil {
			return c, err
		}
		if err := source.CheckGitURL(sp.Git.URL); err != nil {
			return c, &SourceError{Msg: err.Error()}
		}
	case "http":
		if sp.HTTP == nil || sp.HTTP.URL == "" {
			return c, sourceErrf("http.url is required")
		}
		if err := rejectUserinfo(sp.HTTP.URL); err != nil {
			return c, err
		}
		if err := source.CheckHTTPURL(sp.HTTP.URL, false); err != nil {
			return c, &SourceError{Msg: err.Error()}
		}
	case "local":
		if localRoot == "" {
			return c, sourceErrf("local theme sources are disabled on this panel")
		}
		if sp.Local == nil || sp.Local.Path == "" {
			return c, sourceErrf("local.path is required")
		}
		// The same confinement check the indexer runs, so a path that would
		// fail at index time is refused here with a 422.
		if _, _, err := source.LocalLoader(localRoot, sp.Local.Path); err != nil {
			return c, &SourceError{Msg: err.Error()}
		}
	}
	canonical, err := json.Marshal(sp)
	if err != nil {
		return c, fmt.Errorf("encode source spec: %w", err)
	}
	c.spec = string(canonical)

	if c.verify, err = checkVerify(in.Verify, in.Type); err != nil {
		return c, err
	}

	c.refreshS = DefaultSourceRefreshS
	if in.RefreshIntervalS != nil {
		if *in.RefreshIntervalS < MinSourceRefreshS {
			return c, sourceErrf("refreshInterval must be at least %d seconds", MinSourceRefreshS)
		}
		c.refreshS = *in.RefreshIntervalS
	}
	return c, nil
}

// checkOCI validates the oci settings. The official source is seeded with an
// empty theme list until its index is published (OD-6), so it may keep that
// list; every other oci source must name at least one theme, since registries
// cannot be enumerated.
func checkOCI(o *ociSpec, isBuiltin bool) error {
	if o == nil || o.URL == "" {
		return sourceErrf("oci.url is required")
	}
	if strings.Contains(o.URL, "://") || strings.Contains(o.URL, "@") {
		return sourceErrf("oci.url must be a registry and repository, without a scheme or credentials")
	}
	if len(o.Themes) == 0 && !isBuiltin {
		return sourceErrf("oci.themes must list at least one theme")
	}
	for _, t := range o.Themes {
		if !themeNameRE.MatchString(t) {
			return sourceErrf("oci.themes entry %q is not a valid theme name", t)
		}
	}
	return nil
}

// checkVerify validates the signature policy. A nil or null policy means none.
// Only oci sources may carry one. The built-in source's policy is chart-owned
// (UpdateSource keeps the stored one), so no rule applies to it here.
func checkVerify(raw json.RawMessage, typ string) (*string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if typ != "oci" {
		return nil, sourceErrf("verify is only valid for type oci, not %q", typ)
	}
	vs, err := decodeVerify(trimmed)
	if err != nil {
		return nil, err
	}
	if (vs.Key != nil) == (vs.Keyless != nil) {
		return nil, sourceErrf("verify needs exactly one of key or keyless")
	}
	if vs.Key != nil && strings.TrimSpace(*vs.Key) == "" {
		return nil, sourceErrf("verify.key is empty")
	}
	if vs.Keyless != nil && (vs.Keyless.Issuer == "" || vs.Keyless.Identity == "") {
		return nil, sourceErrf("verify.keyless needs issuer and identity")
	}
	canonical, err := json.Marshal(vs)
	if err != nil {
		return nil, fmt.Errorf("encode verify policy: %w", err)
	}
	s := string(canonical)
	return &s, nil
}

// CreateSource validates settings and stores a new theme source named name. A
// new source is enabled unless the request disables it. It returns
// db.ErrThemeSourceExists for a taken name, and a SourceError for invalid
// settings.
func (s *Service) CreateSource(ctx context.Context, name string, in SourceSettings) (db.ThemeSource, error) {
	if !sourceNameRE.MatchString(name) {
		return db.ThemeSource{}, sourceErrf("name must be lowercase letters, digits and dashes, at most %d characters", maxSourceNameLen)
	}
	if name == OfficialSourceName {
		return db.ThemeSource{}, sourceErrf("the name %q is reserved for the built-in source", name)
	}
	c, err := checkSettings(name, in, s.localRoot)
	if err != nil {
		return db.ThemeSource{}, err
	}
	var data map[string][]byte
	var ref *string
	if hasCredentialInput(in.Credential) {
		if data, err = checkCredential(c.typ, in.Credential); err != nil {
			return db.ThemeSource{}, err
		}
		if _, err := s.secretStore(); err != nil {
			return db.ThemeSource{}, err
		}
		secret := credentialSecretName(name)
		ref = &secret
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	// The row is inserted before the Secret is written, so a duplicate name is
	// refused without touching the existing source's credential.
	created, err := s.store.CreateThemeSource(ctx, db.ThemeSource{
		Name:             name,
		Type:             c.typ,
		Spec:             c.spec,
		CredentialRef:    ref,
		Verify:           c.verify,
		RefreshIntervalS: c.refreshS,
		Enabled:          enabled,
	})
	if err != nil {
		return db.ThemeSource{}, fmt.Errorf("create theme source %q: %w", name, err)
	}
	if data != nil {
		if err := s.writeCredential(ctx, name, data); err != nil {
			if derr := s.store.DeleteThemeSource(ctx, created.ID); derr != nil {
				s.logger.ErrorContext(ctx, "remove theme source after credential failure", "source", name, "error", derr)
			}
			return db.ThemeSource{}, err
		}
	}
	// The credential Secret is in place now, so the first index can start at once.
	// The scheduler's settle delay (newSourceSettle) remains as a guard only.
	if enabled {
		s.startJob(ctx, name, false)
	}
	return created, nil
}

// builtinSettings reshapes a PUT on the built-in source to what an admin may
// change. The URL, the theme list, the insecure flag and the verify policy are
// chart-owned (themes.officialSource) and keep their stored values whatever the
// body says, so a PUT never errors on them. The allow globs come from the body
// when it names a spec; otherwise they are kept. Enabled and the refresh
// interval are applied by UpdateSource.
func builtinSettings(old db.ThemeSource, in SourceSettings) (SourceSettings, error) {
	var stored sourceSpec
	if err := json.Unmarshal([]byte(old.Spec), &stored); err != nil || stored.OCI == nil {
		return in, fmt.Errorf("update theme source %q: stored built-in spec is not an oci spec", old.Name)
	}
	if trimmed := bytes.TrimSpace(in.Spec); len(trimmed) > 0 && string(trimmed) != "null" {
		sp, err := decodeSpec(in.Spec)
		if err != nil {
			return in, err
		}
		stored.Allow = sp.Allow
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return in, fmt.Errorf("encode built-in source spec: %w", err)
	}
	in.Spec = raw
	return in, nil
}

// locationChanged reports whether an edit moves a source to another location
// (type, primary URL or path, or the insecure flag of an oci source). A moved
// location drops the credential reference, since a credential must not follow
// the source to a new host or be sent over plain HTTP.
func locationChanged(oldSpec, oldType, newSpec, newType string) bool {
	return oldType != newType ||
		specURL(oldSpec) != specURL(newSpec) ||
		insecureOf(oldSpec) != insecureOf(newSpec)
}

// insecureOf reports whether a stored spec allows plain HTTP to its registry.
func insecureOf(raw string) bool {
	sp := sourceSpecOf(raw)
	return sp.OCI != nil && sp.OCI.Insecure
}

// UpdateSource replaces the settings of the source named name. An edit that
// changes the verify policy re-checks every entry of the source in the
// background (failures become unverified, with no new digests needed). An edit
// that changes the spec re-indexes it. A changed location drops the credential
// reference. It returns db.ErrThemeNotFound for an unknown name.
func (s *Service) UpdateSource(ctx context.Context, name string, in SourceSettings) (db.ThemeSource, error) {
	old, err := s.store.GetThemeSource(ctx, name)
	if err != nil {
		return db.ThemeSource{}, fmt.Errorf("update theme source %q: %w", name, err)
	}
	if name == OfficialSourceName {
		if in, err = builtinSettings(old, in); err != nil {
			return db.ThemeSource{}, err
		}
	}
	c, err := checkSettings(name, in, s.localRoot)
	if err != nil {
		return db.ThemeSource{}, err
	}
	if name == OfficialSourceName {
		// The built-in verify policy is chart-owned: keep the stored one.
		c.verify = old.Verify
	}
	var data map[string][]byte
	if hasCredentialInput(in.Credential) {
		if data, err = checkCredential(c.typ, in.Credential); err != nil {
			return db.ThemeSource{}, err
		}
	}
	urlChanged := locationChanged(old.Spec, old.Type, c.spec, c.typ)
	next := old
	next.Type = c.typ
	next.Spec = c.spec
	next.Verify = c.verify
	next.RefreshIntervalS = c.refreshS
	if in.Enabled != nil {
		next.Enabled = *in.Enabled
	}
	// The row is written before the Secret. If the row write fails, the stored
	// credential is left untouched and the source keeps working; a failed Secret
	// write after the row write is logged and returned, and a failed delete only
	// leaves an orphaned Secret.
	var writeData map[string][]byte
	deleteSecret := false
	switch {
	case data != nil:
		// A new credential replaces the stored one, even when the location changed.
		writeData = data
		ref := credentialSecretName(name)
		next.CredentialRef = &ref
	case (isCredentialNull(in.Credential) || urlChanged) && old.CredentialRef != nil:
		// Clearing, or a moved location, drops the credential: it must not follow
		// the source to a new host.
		deleteSecret = true
		next.CredentialRef = nil
	}
	if err := s.store.UpdateThemeSource(ctx, next); err != nil {
		return db.ThemeSource{}, fmt.Errorf("update theme source %q: %w", name, err)
	}
	credChanged := writeData != nil || deleteSecret
	if writeData != nil {
		if err := s.writeCredential(ctx, name, writeData); err != nil {
			s.logger.ErrorContext(ctx, "write theme source credential after row update", "source", name, "error", err)
			return db.ThemeSource{}, err
		}
	}
	if deleteSecret {
		if err := s.deleteCredential(ctx, name); err != nil {
			s.logger.ErrorContext(ctx, "delete theme source credential after row update", "source", name, "error", err)
			return db.ThemeSource{}, err
		}
	}
	verifyChanged := !equalStringPtr(old.Verify, next.Verify)
	// A source that stays disabled is not fetched. Re-enabling one indexes it.
	reenabled := !old.Enabled && next.Enabled
	if next.Enabled && (verifyChanged || credChanged || old.Spec != next.Spec || old.Type != next.Type || reenabled) {
		s.startJob(ctx, name, verifyChanged)
	}
	updated, err := s.store.GetThemeSource(ctx, name)
	if err != nil {
		return db.ThemeSource{}, fmt.Errorf("update theme source %q: %w", name, err)
	}
	return updated, nil
}

// equalStringPtr reports whether two optional strings hold the same value.
func equalStringPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// DeleteSource removes the source named name with its catalog entries. Users'
// installed copies stay (FR-013): their source link is cleared, so they show as
// source removed. It returns the deleted source for the audit target.
// ErrSourceBuiltin refuses the built-in source, which can only be disabled.
func (s *Service) DeleteSource(ctx context.Context, name string) (db.ThemeSource, error) {
	src, err := s.store.GetThemeSource(ctx, name)
	if err != nil {
		return db.ThemeSource{}, fmt.Errorf("delete theme source %q: %w", name, err)
	}
	if name == OfficialSourceName {
		return db.ThemeSource{}, ErrSourceBuiltin
	}
	// The source lock keeps a refresh pass from writing entries after the
	// delete (refresh re-checks the row under the same lock).
	unlock := s.lockSource(src.ID)
	defer unlock()
	if err := s.store.DeleteThemeSource(ctx, src.ID); err != nil {
		return db.ThemeSource{}, fmt.Errorf("delete theme source %q: %w", name, err)
	}
	if src.CredentialRef != nil {
		// The row is gone, so a Secret that outlives it is only clutter. Log the
		// failure rather than report a delete that did happen as failed.
		if err := s.deleteCredential(ctx, name); err != nil {
			s.logger.WarnContext(ctx, "remove theme source credential", "source", name, "error", err)
		}
	}
	return src, nil
}

// StartRefresh starts an index of the source named name in the background and
// returns at once (the API answers 202). It returns db.ErrThemeNotFound for an
// unknown name and ErrSourceDisabled for a disabled source.
func (s *Service) StartRefresh(ctx context.Context, name string) (db.ThemeSource, error) {
	src, err := s.store.GetThemeSource(ctx, name)
	if err != nil {
		return db.ThemeSource{}, fmt.Errorf("refresh theme source %q: %w", name, err)
	}
	if !src.Enabled {
		return db.ThemeSource{}, ErrSourceDisabled
	}
	s.startJob(ctx, name, false)
	return src, nil
}

// startJob runs a refresh of the named source in its own goroutine. The request
// context ends with the response, so the job takes a context that is not
// cancelled with it and has its own timeout. Concurrent jobs for one source are
// serialised by the source lock inside refresh.
func (s *Service) startJob(ctx context.Context, name string, recheck bool) {
	jobCtx := context.WithoutCancel(ctx)
	s.jobs.Add(1)
	go func() {
		defer s.jobs.Done()
		rctx, cancel := context.WithTimeout(jobCtx, refreshTimeout)
		defer cancel()
		src, err := s.store.GetThemeSource(rctx, name)
		if err != nil {
			// Deleted before the job started: nothing to index.
			return
		}
		if err := s.refresh(rctx, src, recheck); err != nil {
			s.logger.WarnContext(rctx, "theme source refresh failed", "source", name, "error", redact(err.Error()))
		}
	}()
}

// WaitIdle blocks until every background refresh started by CreateSource,
// UpdateSource or StartRefresh has finished. Tests use it; the scheduler does
// not need it.
func (s *Service) WaitIdle() {
	s.jobs.Wait()
}
