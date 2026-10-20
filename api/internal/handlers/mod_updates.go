package handlers

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// AgentModLister fetches JSON from a server's agent sidecar; satisfied by
// *ws.AgentClient. nil means agent mTLS isn't configured — the endpoint
// then degrades to 503, matching the proxy routes.
type AgentModLister interface {
	GetJSON(ctx context.Context, name, namespace, path string, out any) error
}

// ClusterAgentModLister reads from the explicitly selected cluster.
type ClusterAgentModLister interface {
	GetJSONForCluster(ctx context.Context, cluster, name, namespace, path string, out any) error
}

// MountModUpdates wires the batch mod update check. A GET → viewer+ under
// the standard /servers RBAC rules. One request checks every managed mod on
// the server against its registry provider, so the dashboard never fans out
// per-mod requests to upstream registries.
func MountModUpdates(r chi.Router, k *kube.Client, reg registrySet, agent AgentModLister) {
	mountModUpdates(r, k, nil, reg, agent)
}

// MountModUpdatesWithRegistry enables gateway reads and template lookup in the
// same registered cluster. The original mount remains local-only for callers
// that have not supplied a registry.
func MountModUpdatesWithRegistry(r chi.Router, clients *kube.Registry, reg registrySet, agent AgentModLister) {
	mountModUpdates(r, clients.Default(), clients, reg, agent)
}

func mountModUpdates(r chi.Router, k *kube.Client, clients *kube.Registry, reg registrySet, agent AgentModLister) {
	h := &modUpdatesHandler{
		clients: clients,
		k:       k,
		reg:     reg,
		agent:   agent,
		ttl:     5 * time.Minute,
		cache:   map[modUpdateKey]cachedLatest{},
		sem:     make(chan struct{}, 4),
	}
	r.Get("/servers/{name}/mods/updates", h.updates)
}

type modUpdatesHandler struct {
	clients *kube.Registry
	k       *kube.Client
	reg     registrySet
	agent   AgentModLister

	ttl time.Duration
	mu  sync.Mutex
	// cache holds the freshest known release per (provider, project,
	// loader, gameVersion) so repeated checks (page revisits, several
	// servers sharing mods) don't hammer upstream registries.
	cache map[modUpdateKey]cachedLatest
	// sem bounds concurrent upstream lookups per API replica.
	sem chan struct{}
}

// modUpdateCacheCap bounds the cache; on overflow expired entries are
// pruned, and if everything is still fresh the cache resets (cheap, rare).
const modUpdateCacheCap = 1024

type modUpdateKey struct {
	provider    string
	community   string
	steamAppID  int32
	githubOwner string
	githubRepo  string
	projectID   string
	loader      string
	gameVersion string
}

type cachedLatest struct {
	latest  registry.Version
	fetched time.Time
}

// installedMod mirrors the agent's GET /mods entry (agent/internal/mods).
type installedMod struct {
	Name string        `json:"name"`
	Meta *installedRef `json:"meta"`
}

type installedRef struct {
	Provider      string `json:"provider"`
	ProjectID     string `json:"projectId"`
	ProjectName   string `json:"projectName"`
	VersionID     string `json:"versionId"`
	VersionNumber string `json:"versionNumber"`
	GameVersion   string `json:"gameVersion"`
	Loader        string `json:"loader"`
}

// ModUpdate is one available upgrade for an installed mod.
type ModUpdate struct {
	Name                   string        `json:"name"`
	Provider               string        `json:"provider"`
	ProjectID              string        `json:"projectId"`
	ProjectName            string        `json:"projectName,omitempty"`
	InstalledVersionID     string        `json:"installedVersionId"`
	InstalledVersionNumber string        `json:"installedVersionNumber,omitempty"`
	LatestVersionID        string        `json:"latestVersionId"`
	LatestVersionNumber    string        `json:"latestVersionNumber,omitempty"`
	File                   registry.File `json:"file"`
}

type modUpdateError struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type modUpdatesResponse struct {
	CheckedAt string           `json:"checkedAt"`
	Updates   []ModUpdate      `json:"updates"`
	Errors    []modUpdateError `json:"errors,omitempty"`
}

func (h *modUpdatesHandler) updates(w http.ResponseWriter, req *http.Request) {
	cluster := scope.DefaultCluster
	k := h.k
	if h.clients == nil {
		if rejectRemoteCluster(w, req) {
			return
		}
	} else {
		var err error
		cluster, err = scope.ResolveCluster(req, h.clients)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		var ok bool
		k, ok = h.clients.Get(cluster)
		if !ok || k == nil {
			httperr.Write(w, req, scope.ErrForbiddenCluster)
			return
		}
	}
	if configured, ok := h.agent.(interface{ ConfiguredForCluster(string) bool }); ok && !configured.ConfiguredForCluster(cluster) {
		http.Error(w, "agent mTLS not configured", http.StatusServiceUnavailable)
		return
	}
	if h.agent == nil {
		http.Error(w, "agent mTLS not configured", http.StatusServiceUnavailable)
		return
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")

	var mods []installedMod
	var agentErr error
	if cluster == scope.DefaultCluster {
		agentErr = h.agent.GetJSON(req.Context(), name, ns, "/mods", &mods)
	} else if scoped, ok := h.agent.(ClusterAgentModLister); ok {
		agentErr = scoped.GetJSONForCluster(req.Context(), cluster, name, ns, "/mods", &mods)
	} else {
		http.Error(w, "agent gateway not configured", http.StatusServiceUnavailable)
		return
	}
	if agentErr != nil {
		httperr.WriteCode(w, req, http.StatusBadGateway, errors.New("agent unreachable"))
		return
	}

	gs, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(ns).Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if err := rbac.ValidateServerIdentity(req.Context(), cluster, ns, name, string(gs.GetUID())); err != nil {
		http.NotFound(w, req)
		return
	}
	tmplName, _, _ := unstructured.NestedString(gs.Object, "spec", "templateRef", "name")
	var tmpl *unstructured.Unstructured
	if tmplName != "" {
		if tmpl, err = k.Dynamic.Resource(kube.GVRs["templates"]).Get(req.Context(), tmplName, metav1.GetOptions{}); err != nil {
			httperr.Write(w, req, err)
			return
		}
	}
	selected, _, _ := unstructured.NestedString(gs.Object, "spec", "version")
	activeLoader, activeGameVersion := activeVersion(tmpl, selected)
	declared := map[string]providerCfg{}
	for _, p := range registryProviders(tmpl) {
		declared[p.provider] = p
	}
	// Resolve availability once per distinct declared provider (at most 8
	// — ModRegistrySpec.Providers is MaxItems=8), not once per installed
	// mod file: a keyed provider's Available call resolves its API key
	// (DBKeyFunc does a DB read plus a live apiserver Secret GET), and a
	// server with e.g. 40 installed CurseForge mods would otherwise fan
	// that out to 40 calls per Mods-tab load.
	availByProvider := make(map[string]bool, len(declared))
	for name := range declared {
		availByProvider[name] = h.reg.Available(req.Context(), name)
	}

	resp := modUpdatesResponse{
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
		Updates:   []ModUpdate{},
	}

	// Group managed mods by lookup key so a project is checked once even
	// when several files reference it.
	groups := map[modUpdateKey][]installedMod{}
	for _, m := range mods {
		ref := m.Meta
		// Unmanaged files and direct uploads have no registry identity to
		// check against; skip them silently — the dashboard already renders
		// them as unmanaged. github is skipped the same way: its Project.ID
		// and Version.ID are both the release id (see github.go), so an
		// installed mod's "latest release" and "installed release" always
		// compare equal here — there is no update to ever detect, and
		// checking anyway would burn one anonymous api.github.com call per
		// installed mod against the shared 60/hr quota for zero benefit.
		if ref == nil || ref.ProjectID == "" || ref.Provider == "" || ref.Provider == "upload" || ref.Provider == "github" {
			continue
		}
		cfg, ok := declared[ref.Provider]
		if !ok {
			resp.Errors = append(resp.Errors, modUpdateError{Name: m.Name, Error: "provider not declared by this game"})
			continue
		}
		if !availByProvider[ref.Provider] {
			resp.Errors = append(resp.Errors, modUpdateError{Name: m.Name, Error: "provider not configured"})
			continue
		}
		key := modUpdateKey{
			provider:    ref.Provider,
			community:   cfg.community,
			steamAppID:  cfg.steamAppID,
			githubOwner: cfg.githubOwner,
			githubRepo:  cfg.githubRepo,
			projectID:   ref.ProjectID,
			loader:      ref.Loader,
			gameVersion: ref.GameVersion,
		}
		// Older manifests may lack loader/gameVersion; fall back to the
		// server's active version so the filter stays meaningful.
		if key.loader == "" {
			key.loader = activeLoader
		}
		if key.gameVersion == "" {
			key.gameVersion = activeGameVersion
		}
		groups[key] = append(groups[key], m)
	}

	// Resolve each unique project concurrently, bounded by the semaphore.
	type lookup struct {
		latest registry.Version
		err    error
	}
	results := make(map[modUpdateKey]lookup, len(groups))
	var (
		wg sync.WaitGroup
		rm sync.Mutex
	)
	for key := range groups {
		wg.Add(1)
		go func(key modUpdateKey, ctx context.Context) {
			defer wg.Done()
			latest, err := h.latestFor(ctx, key)
			rm.Lock()
			results[key] = lookup{latest: latest, err: err}
			rm.Unlock()
		}(key, req.Context())
	}
	wg.Wait()

	for key, group := range groups {
		res := results[key]
		for _, m := range group {
			switch {
			case res.err != nil:
				resp.Errors = append(resp.Errors, modUpdateError{Name: m.Name, Error: "registry unavailable"})
			case res.latest.ID == "" || len(res.latest.Files) == 0:
				// No compatible release for this loader+gameVersion —
				// nothing to offer, not an error.
			case res.latest.ID != m.Meta.VersionID:
				resp.Updates = append(resp.Updates, ModUpdate{
					Name:                   m.Name,
					Provider:               m.Meta.Provider,
					ProjectID:              m.Meta.ProjectID,
					ProjectName:            m.Meta.ProjectName,
					InstalledVersionID:     m.Meta.VersionID,
					InstalledVersionNumber: m.Meta.VersionNumber,
					LatestVersionID:        res.latest.ID,
					LatestVersionNumber:    res.latest.VersionNumber,
					File:                   primaryFile(res.latest.Files),
				})
			}
		}
	}
	sort.Slice(resp.Updates, func(i, j int) bool { return resp.Updates[i].Name < resp.Updates[j].Name })
	sort.Slice(resp.Errors, func(i, j int) bool { return resp.Errors[i].Name < resp.Errors[j].Name })
	writeJSON(w, resp)
}

// latestFor returns the newest release compatible with the key's loader and
// game version, from cache when fresh. A missing release (no versions after
// filtering) is cached as a zero Version so absent-compat projects don't
// re-query upstream on every check.
func (h *modUpdatesHandler) latestFor(ctx context.Context, key modUpdateKey) (registry.Version, error) {
	h.mu.Lock()
	if e, ok := h.cache[key]; ok && time.Since(e.fetched) < h.ttl {
		h.mu.Unlock()
		return e.latest, nil
	}
	h.mu.Unlock()

	h.sem <- struct{}{}
	defer func() { <-h.sem }()

	p, ok := h.reg.For(ctx, registry.Config{
		Provider:    key.provider,
		Community:   key.community,
		SteamAppID:  key.steamAppID,
		GitHubOwner: key.githubOwner,
		GitHubRepo:  key.githubRepo,
	})
	if !ok {
		return registry.Version{}, errNoRegistry
	}
	versions, err := p.Versions(ctx, key.projectID, registry.Filter{
		Loader:      key.loader,
		GameVersion: key.gameVersion,
	})
	if err != nil {
		return registry.Version{}, err
	}
	var latest registry.Version
	if len(versions) > 0 {
		latest = versions[0] // providers return newest-first
	}

	h.mu.Lock()
	if len(h.cache) >= modUpdateCacheCap {
		for k, e := range h.cache {
			if time.Since(e.fetched) >= h.ttl {
				delete(h.cache, k)
			}
		}
		if len(h.cache) >= modUpdateCacheCap {
			h.cache = map[modUpdateKey]cachedLatest{}
		}
	}
	h.cache[key] = cachedLatest{latest: latest, fetched: time.Now()}
	h.mu.Unlock()
	return latest, nil
}

// primaryFile picks the version's primary artifact, defaulting to the first.
func primaryFile(files []registry.File) registry.File {
	for _, f := range files {
		if f.Primary {
			return f
		}
	}
	return files[0]
}
