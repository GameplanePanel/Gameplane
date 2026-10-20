package auth

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// ProviderSecretLabel must be set to "true" on a Secret before the
// registry will read a clientSecret from it — the same contract
// notification sinks use, under a separate label so a config:manage user
// can't point an auth provider at a sink Secret or vice versa.
const ProviderSecretLabel = "gameplane.local/auth-provider"

// HelmProviderName is the reserved name of the synthetic provider built
// from the --oidc-* Helm flags at startup. It is listed and routable like
// a DB provider but owned by values.yaml: not editable, not deletable,
// and rejected as a DB provider name by validateAuth.
const HelmProviderName = "helm"

// authProviderPrefix derives the default Secret name for a provider's
// clientSecret when the config row carries no explicit configRef.
const authProviderPrefix = "gameplane-auth-"

// ErrUnknownProvider covers "no such provider", "disabled", and "not an
// OIDC kind" alike — route handlers map it to one neutral 404 so probing
// /auth/oidc/{name}/start can't distinguish the cases.
var ErrUnknownProvider = errors.New("no such enabled OIDC provider")

// Cache windows: a successful build is reused until the config row's
// hash changes or the entry ages out (bounding staleness after an
// out-of-band Secret rotation); a failed build is remembered briefly so
// a down IdP can't be used to hammer discovery on every login click.
const (
	rebuildTTL      = 10 * time.Minute
	errorBackoffTTL = 30 * time.Second
)

// RoleMappings lists, per dashboard role, the IdP group values that grant
// it. A user matching several gets the most privileged (admin > operator >
// viewer).
type RoleMappings struct {
	Admin    []string `json:"admin,omitempty"`
	Operator []string `json:"operator,omitempty"`
	Viewer   []string `json:"viewer,omitempty"`
}

// Provider is one entry of the admin-managed auth config, as consumers
// (login page, routes) see it.
type Provider struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"` // local|oidc|google|github
	DisplayName string `json:"displayName,omitempty"`
	Enabled     bool   `json:"enabled"`
	Issuer      string `json:"issuer,omitempty"`
	ClientID    string `json:"clientID,omitempty"`
	ConfigRef   string `json:"configRef,omitempty"`
	// Scopes are extra OAuth scopes requested beyond the base
	// openid/profile/email (e.g. "groups").
	Scopes []string `json:"scopes,omitempty"`
	// GroupsClaim names the ID-token claim holding group memberships;
	// empty means "groups".
	GroupsClaim string `json:"groupsClaim,omitempty"`
	// RoleMappings maps IdP groups to dashboard roles. nil disables
	// mapping entirely: new users get viewer and roles are never touched
	// again.
	RoleMappings *RoleMappings `json:"roleMappings,omitempty"`
	// DefaultRole applies when RoleMappings is set but no group matches:
	// ""|viewer|operator|admin|deny ("" = viewer; deny refuses the login).
	DefaultRole string `json:"defaultRole,omitempty"`
}

// Label returns the login-button text: the admin's display name, or a
// per-kind fallback that never derives from the issuer (login-privacy —
// issuer URLs leak internal hostnames).
func (p Provider) Label() string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	if p.Kind == "local" {
		return "Local account"
	}
	return p.Name
}

// SecretReader fetches a named credential Secret's data. The production
// implementation reads labelled Secrets from the control-plane namespace;
// tests substitute a map.
type SecretReader func(ctx context.Context, name string) (map[string][]byte, error)

// NewK8sSecretReader reads Secrets from ns, refusing any not labelled
// ProviderSecretLabel=true.
func NewK8sSecretReader(k *kube.Client, ns string) SecretReader {
	return func(ctx context.Context, name string) (map[string][]byte, error) {
		sec, err := k.Secrets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("get provider secret %s/%s: %w", ns, name, err)
		}
		if sec.Labels[ProviderSecretLabel] != "true" {
			return nil, fmt.Errorf("secret %s/%s is not labelled %s=true", ns, name, ProviderSecretLabel)
		}
		return sec.Data, nil
	}
}

type builtEntry struct {
	oidc    *OIDC
	err     error
	builtAt time.Time
	cfgHash [sha256.Size]byte
}

// Registry resolves the current identity-provider set from the admin
// auth config on demand — no restart needed after a config save. The
// config row is re-read per resolve (one indexed SELECT, only on /auth/*
// traffic — the same re-read-on-use pattern notify and telemetry use),
// which stays correct under multiple API replicas and kubectl-level DB
// edits. OIDC construction (issuer discovery — network I/O) happens
// lazily per provider and is cached against the row hash.
type Registry struct {
	store       *db.Store
	secrets     SecretReader
	legacy      *OIDC
	legacyLabel string
	helmPolicy  *ProviderPolicy

	mu    sync.Mutex
	built map[string]builtEntry

	// auditWriteSync is attached to every provider build creates, so their
	// OIDC role assignments are audited (FR-014). Guarded by mu.
	auditWriteSync func(ctx context.Context, method, path, target, reason string, status int) error

	now func() time.Time // test seam
}

// NewRegistry builds a Registry. legacy is the Helm-flag OIDC provider
// (nil when the flags are unset), legacyLabel its login-button text, and
// helmPolicy the Helm-seeded role mapping policy (nil when OIDC is not
// configured or role mappings are unset).
func NewRegistry(store *db.Store, secrets SecretReader, legacy *OIDC, legacyLabel string, helmPolicy *ProviderPolicy) *Registry {
	if legacyLabel == "" {
		legacyLabel = "Single sign-on"
	}
	reg := &Registry{
		store:       store,
		secrets:     secrets,
		legacy:      legacy,
		legacyLabel: legacyLabel,
		helmPolicy:  helmPolicy,
		built:       map[string]builtEntry{},
		now:         time.Now,
	}
	// The legacy provider needs the store for account linking; wiring it
	// here also fixes the Helm-flag path, whose callback previously ran
	// with no store attached and 500'd on every login. Also attach the
	// helm role overrides function so it can be read on every login (SC-007).
	if legacy != nil {
		legacy.AttachStore(store)
		legacy.AttachHelmRoleOverridesFunc(func(ctx context.Context) *RoleMappings {
			return reg.HelmRoleOverrides(ctx)
		})
	}
	return reg
}

// AttachAuditWriteSyncFunc sets the audit write func attached to every
// dashboard-managed provider the registry builds, so their OIDC role
// assignments are audited like the Helm provider's (FR-014). Providers
// already built are dropped from the cache so the next resolve rebuilds
// them with the func attached. A nil func disables the audit emission.
func (r *Registry) AttachAuditWriteSyncFunc(fn func(ctx context.Context, method, path, target, reason string, status int) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auditWriteSync = fn
	r.built = map[string]builtEntry{}
}

// defaultProviders is the provider set of an install whose auth config
// row was never written (or cannot be parsed): local login enabled. A
// corrupted row must degrade to a working login page, not a lockout.
func defaultProviders() []Provider {
	return []Provider{{Name: "local", Kind: "local", Enabled: true}}
}

// snapshot returns the parsed provider list plus the hash of the raw row
// it came from. Missing or malformed rows yield the fail-open default.
func (r *Registry) snapshot(ctx context.Context) ([]Provider, [sha256.Size]byte) {
	raw, ok, err := r.store.ConfigValue(ctx, "auth")
	if err != nil || !ok {
		return defaultProviders(), sha256.Sum256(nil)
	}
	var cfg struct {
		Providers []Provider `json:"providers"`
	}
	if json.Unmarshal([]byte(raw), &cfg) != nil || len(cfg.Providers) == 0 {
		return defaultProviders(), sha256.Sum256(nil)
	}
	return cfg.Providers, sha256.Sum256([]byte(raw))
}

// HelmRoleOverrides returns the current helmOverride.roleMappings from the
// "auth" config row, if present. It parses the row to extract the optional
// per-role override lists that the login path uses to compute the effective
// Helm policy. Returns nil if the auth row is missing, malformed, or carries
// no helmOverride.
func (r *Registry) HelmRoleOverrides(ctx context.Context) *RoleMappings {
	raw, ok, err := r.store.ConfigValue(ctx, "auth")
	if err != nil || !ok {
		return nil
	}
	var cfg struct {
		HelmOverride *struct {
			RoleMappings *RoleMappings `json:"roleMappings"`
		} `json:"helmOverride"`
	}
	if json.Unmarshal([]byte(raw), &cfg) != nil {
		return nil
	}
	if cfg.HelmOverride == nil || cfg.HelmOverride.RoleMappings == nil {
		return nil
	}
	return cfg.HelmOverride.RoleMappings
}

// Enabled lists the providers the login page may offer: enabled DB
// providers plus the synthetic Helm provider when the flags are set.
// Row read only — no Secret fetch, no discovery.
func (r *Registry) Enabled(ctx context.Context) []Provider {
	providers, _ := r.snapshot(ctx)
	out := make([]Provider, 0, len(providers)+1)
	for _, p := range providers {
		if p.Enabled {
			out = append(out, p)
		}
	}
	if r.legacy != nil {
		helmProvider := Provider{
			Name:        HelmProviderName,
			Kind:        "oidc",
			DisplayName: r.legacyLabel,
			Enabled:     true,
		}
		// Populate the synthetic provider with Helm-seeded policy fields.
		if r.helmPolicy != nil {
			helmProvider.GroupsClaim = r.helmPolicy.GroupsClaim
			helmProvider.RoleMappings = r.helmPolicy.RoleMappings
			helmProvider.DefaultRole = r.helmPolicy.DefaultRole
		}
		out = append(out, helmProvider)
	}
	return out
}

// LocalEnabled reports whether local (username/password) login is
// allowed. A missing or unreadable config row means yes — fresh installs
// must be able to log in.
func (r *Registry) LocalEnabled(ctx context.Context) bool {
	providers, _ := r.snapshot(ctx)
	for _, p := range providers {
		if p.Kind == "local" {
			return p.Enabled
		}
	}
	// Row exists but carries no local entry (pre-v2 hand-edited config):
	// fail open for the same reason a missing row does.
	return true
}

// OIDCFor resolves the named provider to a ready OIDC client, building
// (Secret fetch + issuer discovery) lazily and caching per row-hash.
func (r *Registry) OIDCFor(ctx context.Context, name string) (*OIDC, error) {
	if name == HelmProviderName {
		if r.legacy == nil {
			// No Helm flags set: indistinguishable from any other unknown
			// provider so the legacy routes 404 neutrally.
			return nil, fmt.Errorf("%q: %w", name, ErrUnknownProvider)
		}
		return r.legacy, nil
	}
	providers, hash := r.snapshot(ctx)
	var found *Provider
	for i := range providers {
		if providers[i].Name == name {
			found = &providers[i]
			break
		}
	}
	if found == nil || !found.Enabled || found.Kind == "local" {
		return nil, fmt.Errorf("%q: %w", name, ErrUnknownProvider)
	}

	r.mu.Lock()
	entry, cached := r.built[name]
	r.mu.Unlock()
	if cached && entry.cfgHash == hash {
		age := r.now().Sub(entry.builtAt)
		if entry.err == nil && age < rebuildTTL {
			return entry.oidc, nil
		}
		if entry.err != nil && age < errorBackoffTTL {
			return nil, entry.err
		}
	}

	o, err := r.build(ctx, *found)
	r.mu.Lock()
	r.built[name] = builtEntry{oidc: o, err: err, builtAt: r.now(), cfgHash: hash}
	r.mu.Unlock()
	return o, err
}

func (r *Registry) build(ctx context.Context, p Provider) (*OIDC, error) {
	secretName := p.ConfigRef
	if secretName == "" {
		secretName = authProviderPrefix + p.Name
	}
	data, err := r.secrets(ctx, secretName)
	if err != nil {
		return nil, fmt.Errorf("provider %q: %w", p.Name, err)
	}
	clientSecret := string(data["clientSecret"])
	if clientSecret == "" {
		return nil, fmt.Errorf(`provider %q: secret %s has no "clientSecret" key`, p.Name, secretName)
	}
	redirect, err := r.redirectURL(ctx, p.Name)
	if err != nil {
		return nil, fmt.Errorf("provider %q: %w", p.Name, err)
	}
	o, err := NewOIDCWithPolicy(ctx, p.Issuer, p.ClientID, clientSecret, redirect, &ProviderPolicy{
		Scopes:       p.Scopes,
		GroupsClaim:  p.GroupsClaim,
		RoleMappings: p.RoleMappings,
		DefaultRole:  p.DefaultRole,
	})
	if err != nil {
		return nil, fmt.Errorf("provider %q: discover issuer: %w", p.Name, err)
	}
	o.AttachStore(r.store)
	o.AttachHelmRoleOverridesFunc(func(ctx context.Context) *RoleMappings {
		return r.HelmRoleOverrides(ctx)
	})
	r.mu.Lock()
	auditFn := r.auditWriteSync
	r.mu.Unlock()
	o.AttachAuditWriteSyncFunc(auditFn)
	// The provider's own name labels its audit events; validateAuth keeps
	// dashboard providers from using the reserved Helm name.
	o.SetProviderName(p.Name)
	return o, nil
}

// redirectURL derives the provider's callback URL from the general
// config's externalURL — the same canonical base the install advertises
// for OIDC callbacks everywhere else.
func (r *Registry) redirectURL(ctx context.Context, name string) (string, error) {
	raw, ok, err := r.store.ConfigValue(ctx, "general")
	if err != nil || !ok {
		return "", errors.New("set Admin Settings → General → External URL first (needed for the OIDC redirect URL)")
	}
	var g struct {
		ExternalURL string `json:"externalURL"`
	}
	if json.Unmarshal([]byte(raw), &g) != nil || g.ExternalURL == "" {
		return "", errors.New("set Admin Settings → General → External URL first (needed for the OIDC redirect URL)")
	}
	base, err := url.Parse(g.ExternalURL)
	if err != nil {
		return "", fmt.Errorf("externalURL: %w", err)
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + "/auth/oidc/" + name + "/callback"
	return base.String(), nil
}
