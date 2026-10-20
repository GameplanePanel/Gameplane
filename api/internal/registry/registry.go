// Package registry implements read-only browse/search of external game-mod
// registries (Modrinth, Thunderstore, CurseForge, Hangar, the Factorio mod
// portal, the Steam Workshop, Nexus Mods, SpigotMC via Spiget, GitHub
// Releases, uMod) for the dashboard's mod manager.
//
// The engines here are generic: a GameTemplate's capabilities.mods.registry
// block picks a provider and supplies its parameters, and the handler in
// internal/handlers resolves the active version's loader + game-version
// token before calling Search/Versions. Installing a chosen mod is NOT done
// here — the dashboard hands the returned File.DownloadURL to the existing
// agent install path, which re-checks the host allowlist and SSRF guard.
// Search itself only ever GETs fixed provider hostnames
// (api.modrinth.com, thunderstore.io, api.steampowered.com,
// api.nexusmods.com, api.spiget.org, api.github.com, umod.org). Every
// engine shares one HTTP client that dials through netguard.IsPublic, so a
// fetch, redirects included, only ever connects to a globally routable
// address and never goes through a proxy from the pod environment.
//
// Two providers (steam, nexus) never populate Version.Files: Steam Workshop
// content is fetched by steamcmd running inside the game container (there is
// no HTTP download URL to hand back), and Nexus's real download links are
// premium-account-gated and IP-bound to the caller that mints them, which
// would be this API pod rather than the agent pod that actually downloads.
// Both engines are deliberate title/thumbnail-only browsers, not stubs — see
// their package doc comments for the full reasoning.
package registry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/GameplanePanel/gameplane/netguard"
)

// Project is one search hit, normalized across providers.
type Project struct {
	ID          string `json:"id"`
	Slug        string `json:"slug,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Author      string `json:"author,omitempty"`
	IconURL     string `json:"iconUrl,omitempty"`
	Downloads   int64  `json:"downloads,omitempty"`
	PageURL     string `json:"pageUrl,omitempty"`
	Provider    string `json:"provider"`
}

// File is a downloadable artifact of a Version. DownloadURL is what the
// dashboard hands to the agent's install endpoint. RequiresAuth marks files
// the portal only serves with the user's own credentials appended (e.g. the
// Factorio mod portal's username+token query params) — the dashboard then
// offers a from-URL handoff instead of one-click install, and the server
// never embeds credentials in URLs it returns to browsers.
type File struct {
	Filename     string `json:"filename"`
	DownloadURL  string `json:"downloadUrl"`
	Size         int64  `json:"size,omitempty"`
	Primary      bool   `json:"primary,omitempty"`
	RequiresAuth bool   `json:"requiresAuth,omitempty"`
}

// Version is one release of a Project, newest first.
type Version struct {
	ID            string   `json:"id"`
	Name          string   `json:"name,omitempty"`
	VersionNumber string   `json:"versionNumber,omitempty"`
	GameVersions  []string `json:"gameVersions,omitempty"`
	Loaders       []string `json:"loaders,omitempty"`
	Files         []File   `json:"files"`
}

// SearchQuery is a normalized search request. Loader and GameVersion are
// the active version's loader id (used verbatim as the provider facet) and
// clean game-version token; either may be empty (no facet → all results).
// An empty Term is a valid "browse" request — providers return their
// popular/all listing, paginated by Offset+Limit and ordered by Sort.
type SearchQuery struct {
	Term        string
	Loader      string
	GameVersion string
	// ProjectType is "modpack" for the modpacks browser; empty (or "mod")
	// is the regular mod/plugin browser, which excludes modpacks.
	ProjectType string
	// Category narrows the browse to a provider category (e.g. Modrinth
	// "optimization"). Empty means all categories.
	Category string
	// Sort is the provider ordering key for browse: "downloads" (popular),
	// "updated", "newest", or "relevance" (the default when Term is set).
	Sort   string
	Limit  int
	Offset int
}

// modpack reports whether this query targets modpacks rather than mods.
func (q SearchQuery) modpack() bool { return q.ProjectType == "modpack" }

// Filter narrows a Versions lookup to the active loader + game version.
type Filter struct {
	Loader      string
	GameVersion string
}

// KeyFunc returns the API key for a provider, or "" if unconfigured.
type KeyFunc func(ctx context.Context, provider string) string

// StaticKeys wraps a map of static API keys for use with NewSet. Useful for
// command-line flag initialization where the keys don't change at runtime.
func StaticKeys(keys map[string]string) KeyFunc {
	return func(_ context.Context, provider string) string {
		return keys[provider]
	}
}

// Provider is one registry engine bound to its configuration.
type Provider interface {
	Search(ctx context.Context, q SearchQuery) ([]Project, error)
	Versions(ctx context.Context, projectID string, f Filter) ([]Version, error)
	// ModpackDeps resolves a modpack's dependency mods into installable
	// files — used by games that install a modpack by installing its
	// dependencies (e.g. Thunderstore/BepInEx). Providers whose modpacks
	// install via a game-image env (e.g. Modrinth on itzg) return nil.
	ModpackDeps(ctx context.Context, projectID string) ([]File, error)
}

// Config is the per-game registry selection, mirroring the CRD's
// capabilities.mods.registry block.
type Config struct {
	Provider string
	// Community is the Thunderstore community slug or (reusing the same
	// bounded-slug shape) the Nexus Mods game domain slug.
	Community string
	// SteamAppID facets Steam Workshop browse/search to one app. Required
	// by the steam provider; ignored by others.
	SteamAppID int32
	// CurseForgeGameID facets CurseForge browse/search to one numeric
	// CurseForge game (e.g. 432 for Minecraft, 83374 for ARK: Survival
	// Ascended). Required by the curseforge provider; ignored by others.
	CurseForgeGameID int32
	// GitHubOwner and GitHubRepo bind the "github" provider to one
	// repository's Releases — GitHub has no cross-repo mod search, so
	// (unlike every keyless engine above) a template must pick exactly one
	// repo, the way Thunderstore picks a Community. Both are required by
	// the github provider; ignored by others.
	GitHubOwner string
	GitHubRepo  string
}

// Cache windows: a successful build is reused until the key hash changes or
// the entry ages out; a failed build is remembered briefly so a missing key
// can't hammer the engine constructor on every request.
const (
	rebuildTTL      = 10 * time.Minute
	errorBackoffTTL = 30 * time.Second
)

// builtEntry caches one keyed provider's most recently built engine. Its
// engine field holds the bare *Curseforge/*Steam/*Nexus (none of which
// implement Provider on their own — like *Thunderstore, they need a
// per-config wrapper for that, built by For), so one cache shape works for
// every keyed engine; the per-provider *Lazy methods below type-assert
// back to the concrete type they know they stored.
type builtEntry struct {
	engine  any
	err     error
	builtAt time.Time
	keyHash [sha256.Size]byte
}

// Set holds the constructed engines, sharing one HTTP client. Keyless engines
// (modrinth, thunderstore, hangar, factorio, spigot, github, umod) are built
// once and reused; the Thunderstore engine caches its community package
// lists internally. Keyed engines (curseforge, steam, nexus) are built
// lazily behind a TTL + key-hash cache so a key can be changed at runtime
// without restarting. When a key is empty, the provider reports unavailable
// (existing behavior).
type Set struct {
	// immutable keyless engines
	modrinth     *Modrinth
	thunderstore *Thunderstore
	hangar       *Hangar
	factorio     *Factorio
	spigot       *Spigot
	github       *GitHub
	umod         *Umod

	// keyed engine cache, one entry per provider name
	mu       sync.Mutex
	built    map[string]builtEntry
	keyGroup singleflight.Group
	keyFunc  KeyFunc
	client   *http.Client
	ua       string

	// test seam
	now func() time.Time
}

// NewSet builds the engine set. version tags the outbound User-Agent
// (Modrinth asks callers to identify themselves). keyFunc provides API keys
// for providers that need them (curseforge, steam, nexus all require their
// own key). Keys are resolved lazily per request and cached with a TTL.
//
// Every engine, keyed or keyless, shares one client that dials through
// netguard.IsPublic: a loopback, private, link-local or other non-public
// destination is refused at dial time with netguard.ErrBlockedAddr.
func NewSet(version string, keyFunc KeyFunc) *Set {
	client := netguard.HTTPClient(15*time.Second, netguard.IsPublic)
	ua := "gameplane/" + version + " (+https://github.com/GameplanePanel/gameplane)"
	return &Set{
		modrinth:     newModrinth(client, ua),
		thunderstore: newThunderstore(client, ua),
		hangar:       newHangar(client, ua),
		factorio:     newFactorio(client, ua),
		spigot:       newSpigot(client, ua),
		github:       newGitHub(client, ua),
		umod:         newUmod(client, ua),
		keyFunc:      keyFunc,
		client:       client,
		ua:           ua,
		now:          time.Now,
	}
}

// For returns the provider for a module's registry config. ok is false
// when the provider is unknown/unset, a required parameter is missing
// (Thunderstore/Nexus need a community/domain, Steam needs an app id,
// CurseForge needs a game id), or the engine isn't configured (a keyed
// provider without a key) — the handler maps that to 501 so the dashboard
// hides the provider. For keyed providers, the key is resolved lazily via
// context and cached with a TTL.
func (s *Set) For(ctx context.Context, cfg Config) (Provider, bool) {
	switch cfg.Provider {
	case "modrinth":
		return s.modrinth, true
	case "thunderstore":
		if cfg.Community == "" {
			return nil, false
		}
		return &thunderstoreCommunity{ts: s.thunderstore, community: cfg.Community}, true
	case "curseforge":
		if cfg.CurseForgeGameID <= 0 {
			return nil, false
		}
		cf, err := s.curseforgeLazy(ctx)
		if err != nil || cf == nil {
			return nil, false
		}
		return &curseforgeGame{cf: cf, gameID: cfg.CurseForgeGameID}, true
	case "hangar":
		return s.hangar, true
	case "factorio":
		return s.factorio, true
	case "spigot":
		return s.spigot, true
	case "github":
		if cfg.GitHubOwner == "" || cfg.GitHubRepo == "" {
			return nil, false
		}
		return &githubRepo{gh: s.github, owner: cfg.GitHubOwner, repo: cfg.GitHubRepo}, true
	case "umod":
		return s.umod, true
	case "steam":
		if cfg.SteamAppID <= 0 {
			return nil, false
		}
		st, err := s.steamLazy(ctx)
		if err != nil || st == nil {
			return nil, false
		}
		return &steamApp{steam: st, appID: cfg.SteamAppID}, true
	case "nexus":
		if cfg.Community == "" {
			return nil, false
		}
		nx, err := s.nexusLazy(ctx)
		if err != nil || nx == nil {
			return nil, false
		}
		return &nexusGame{nexus: nx, domain: cfg.Community}, true
	default:
		return nil, false
	}
}

// Available reports whether a provider's engine is usable independent of a
// specific server — the providers listing uses it to mark the dashboard's
// provider switch. Keyed providers' availability depends on whether an API
// key is currently configured; it does not check per-server parameters
// (SteamAppID/CurseForgeGameID/Community), which For validates.
func (s *Set) Available(ctx context.Context, provider string) bool {
	switch provider {
	case "modrinth", "thunderstore", "hangar", "factorio", "spigot", "github", "umod":
		return true
	case "curseforge":
		cf, err := s.curseforgeLazy(ctx)
		return err == nil && cf != nil
	case "steam":
		st, err := s.steamLazy(ctx)
		return err == nil && st != nil
	case "nexus":
		nx, err := s.nexusLazy(ctx)
		return err == nil && nx != nil
	default:
		return false
	}
}

// curseforgeLazy resolves and lazily builds the CurseForge engine.
func (s *Set) curseforgeLazy(ctx context.Context) (*Curseforge, error) {
	e, err := s.keyedLazy(ctx, "curseforge", func(key string) any {
		return newCurseforge(s.client, s.ua, key)
	})
	if e == nil || err != nil {
		return nil, err
	}
	cf, ok := e.(*Curseforge)
	if !ok {
		return nil, errors.New("unexpected engine type for curseforge")
	}
	return cf, nil
}

// steamLazy resolves and lazily builds the Steam engine.
func (s *Set) steamLazy(ctx context.Context) (*Steam, error) {
	e, err := s.keyedLazy(ctx, "steam", func(key string) any {
		return newSteam(s.client, s.ua, key)
	})
	if e == nil || err != nil {
		return nil, err
	}
	st, ok := e.(*Steam)
	if !ok {
		return nil, errors.New("unexpected engine type for steam")
	}
	return st, nil
}

// nexusLazy resolves and lazily builds the Nexus engine.
func (s *Set) nexusLazy(ctx context.Context) (*Nexus, error) {
	e, err := s.keyedLazy(ctx, "nexus", func(key string) any {
		return newNexus(s.client, s.ua, key)
	})
	if e == nil || err != nil {
		return nil, err
	}
	nx, ok := e.(*Nexus)
	if !ok {
		return nil, errors.New("unexpected engine type for nexus")
	}
	return nx, nil
}

// resolveKey returns provider's API key. Concurrent calls for the same
// provider are collapsed into a single s.keyFunc invocation via
// singleflight: DBKeyFunc does a DB read plus a live apiserver Secret GET,
// and a hot caller that resolves the same provider many times in a single
// burst (e.g. the Mods-tab update check, which fans out one goroutine per
// distinct installed-mod project) would otherwise hit the DB/Secret once
// per goroutine. Unlike a TTL cache, this adds no staleness for calls that
// don't actually overlap in time — the very next call after an in-flight
// one completes gets a fully fresh resolution, so an admin's key/Secret
// change (or removal) still takes effect immediately, matching
// TestSet_AvailableTogglesWithSecret's documented invariant.
func (s *Set) resolveKey(ctx context.Context, provider string) string {
	v, _, _ := s.keyGroup.Do(provider, func() (any, error) {
		return s.keyFunc(ctx, provider), nil
	})
	if key, ok := v.(string); ok {
		return key
	}
	return ""
}

// keyedLazy resolves provider's key (via resolveKey, which coalesces
// concurrent callers) and builds its engine lazily via build, caching the
// result per-provider against the key hash with a rebuild TTL. When the key
// is empty, returns (nil, nil) — the provider is unconfigured, not an
// error. On build failure, the error is cached briefly so repeated requests
// don't hammer the constructor (build itself never fails for any engine
// today, but the cache shape supports it for engines that later validate
// the key eagerly).
func (s *Set) keyedLazy(ctx context.Context, provider string, build func(key string) any) (any, error) {
	key := s.resolveKey(ctx, provider)
	if key == "" {
		return nil, nil
	}

	keyHash := sha256.Sum256([]byte(key))

	s.mu.Lock()
	entry, hasEntry := s.built[provider]
	s.mu.Unlock()

	if hasEntry && entry.keyHash == keyHash {
		age := s.now().Sub(entry.builtAt)
		if entry.err == nil && age < rebuildTTL {
			return entry.engine, nil
		}
		if entry.err != nil && age < errorBackoffTTL {
			return nil, entry.err
		}
	}

	// Key is new or cache expired; build the engine.
	built := build(key)

	s.mu.Lock()
	if s.built == nil {
		s.built = make(map[string]builtEntry)
	}
	s.built[provider] = builtEntry{engine: built, builtAt: s.now(), keyHash: keyHash}
	s.mu.Unlock()

	return built, nil
}

// Response-body caps. Modrinth search/version responses are small;
// Thunderstore's per-community package list is the whole catalog (~150 MB
// for Valheim), so it gets a far larger ceiling. The Thunderstore engine
// stream-decodes within this bound, so the cap limits bytes read, not
// resident memory.
const (
	defaultMaxRespBytes = 8 << 20   // 8 MiB
	tsMaxRespBytes      = 320 << 20 // 320 MiB
)

// httpGetJSON GETs rawURL and decodes a JSON body into v, capping the body
// at maxBytes. The destination address is checked at dial time by the
// client's netguard policy (see NewSet).
func httpGetJSON(ctx context.Context, client *http.Client, userAgent, rawURL string, v any, maxBytes int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("registry request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return sanitizeUpstreamErr("registry GET", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry GET: upstream status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBytes)).Decode(v); err != nil {
		return fmt.Errorf("registry decode: %w", err)
	}
	return nil
}

// sanitizeUpstreamErr wraps a client.Do failure for safe logging/response
// use. Some providers (Steam) take their API key as a query parameter, and
// Go's *url.Error.Error() embeds the full request URL — query string
// included — so a raw transport error (DNS blip, TLS failure, timeout,
// connection refused) can otherwise carry an admin's API key straight into
// an HTTP error response body (httperr.WriteCode) or a log line that flows
// to the audit/syslog/S3 sinks. Redact the query/fragment before the error
// is ever formatted into a string, while preserving the *url.Error type
// (via a copy) so callers that type-assert it (e.g. for Timeout()) still
// can.
func sanitizeUpstreamErr(prefix string, err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		redacted := *uerr
		if u, parseErr := url.Parse(uerr.URL); parseErr == nil {
			u.RawQuery = ""
			u.Fragment = ""
			redacted.URL = u.String()
		} else {
			redacted.URL = "[redacted]"
		}
		return fmt.Errorf("%s: %w", prefix, &redacted)
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

// isDigits reports whether s is non-empty and every rune is an ASCII digit.
// Steam and Nexus both use it to route a numeric search term to their
// resolve-by-id lookup instead of the provider's (keyed, fuzzy) text search.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
