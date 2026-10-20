package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
)

// errNoRegistry signals that a server's template declares no browsable mod
// registry (or the requested provider isn't declared/available). Mapped to
// 501 so the dashboard hides Browse and falls back to install-by-URL.
var errNoRegistry = errors.New("no mod registry for this server")

// registrySet is the subset of *registry.Set the handler needs, so tests
// can inject a fake. Available reports whether an engine is usable (e.g.
// CurseForge needs an API key). Both methods take context for lazy key
// resolution.
type registrySet interface {
	For(ctx context.Context, cfg registry.Config) (registry.Provider, bool)
	Available(ctx context.Context, provider string) bool
}

// MountRegistry wires read-only mod-registry browse onto r. Routes are
// server-scoped so the API resolves the active version's loader and
// game-version token from the cluster (the operator is authoritative). A
// game may declare multiple providers; the client picks one via ?provider=.
//
// This legacy mount holds a home-cluster client and rejects remote requests.
// Production uses MountRegistryWithRegistry for selected-cluster workloads.
func MountRegistry(r chi.Router, k *kube.Client, reg registrySet) {
	mountRegistry(r, &registryHandler{k: k, reg: reg})
}

// MountRegistryWithRegistry resolves servers and templates in the selected
// cluster. The provider set and its administrator-managed credentials remain
// central; selecting a cluster never selects a different credential store.
func MountRegistryWithRegistry(r chi.Router, clients *kube.Registry, reg registrySet) {
	mountRegistry(r, &registryHandler{clients: clients, reg: reg})
}

func mountRegistry(r chi.Router, h *registryHandler) {
	r.Get("/servers/{name}/mods/registry/providers", h.providers)
	r.Get("/servers/{name}/mods/registry/search", h.search)
	r.Get("/servers/{name}/mods/registry/projects/{project}/versions", h.versions)
	r.Get("/servers/{name}/mods/registry/projects/{project}/modpack", h.modpackDeps)
	r.Post("/servers/{name}/modpack", h.installModpack)
}

type registryHandler struct {
	clients *kube.Registry
	k       *kube.Client
	reg     registrySet
}

// providerInfo is one entry of the providers listing the dashboard uses to
// build its provider switch.
type providerInfo struct {
	Provider  string `json:"provider"`
	Available bool   `json:"available"`
	Modpacks  bool   `json:"modpacks"`
}

// providers lists the registries this server's template declares, marking
// which are usable (engine configured) and which offer modpacks. The
// dashboard shows a provider switch from this.
func (h *registryHandler) providers(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	declared := registryProviders(target.template)
	out := make([]providerInfo, 0, len(declared))
	for _, p := range declared {
		out = append(out, providerInfo{
			Provider:  p.provider,
			Available: h.reg.Available(req.Context(), p.provider),
			Modpacks:  p.modpacks != nil,
		})
	}
	writeJSON(w, out)
}

func (h *registryHandler) search(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	p, loader, gameVersion, err := h.resolve(req.Context(), target, req.URL.Query().Get("provider"))
	if err != nil {
		h.writeResolveErr(w, req, err)
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
	if offset < 0 {
		// A negative offset would reach a negative slice index in several
		// engines' client-side pagination (nexus.go, thunderstore.go,
		// factorio.go all do matched[q.Offset:] after the
		// q.Offset >= len(matched) bounds check, which a negative offset
		// slips past) — clamp here once rather than in every engine.
		offset = 0
	}
	res, err := p.Search(req.Context(), registry.SearchQuery{
		Term:        req.URL.Query().Get("q"),
		Loader:      loader,
		GameVersion: gameVersion,
		ProjectType: req.URL.Query().Get("type"),
		Category:    req.URL.Query().Get("category"),
		Sort:        req.URL.Query().Get("sort"),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadGateway, fmt.Errorf("mod registry search failed: %w", err))
		return
	}
	writeJSON(w, res)
}

func (h *registryHandler) versions(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	p, loader, gameVersion, err := h.resolve(req.Context(), target, req.URL.Query().Get("provider"))
	if err != nil {
		h.writeResolveErr(w, req, err)
		return
	}
	res, err := p.Versions(req.Context(), chi.URLParam(req, "project"), registry.Filter{
		Loader:      loader,
		GameVersion: gameVersion,
	})
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadGateway, fmt.Errorf("mod registry versions failed: %w", err))
		return
	}
	writeJSON(w, res)
}

// modpackDeps resolves a modpack into the dependency files the dashboard
// then installs one-by-one via /mods/install (deps-mode providers, e.g.
// Thunderstore).
func (h *registryHandler) modpackDeps(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	p, _, _, err := h.resolve(req.Context(), target, req.URL.Query().Get("provider"))
	if err != nil {
		h.writeResolveErr(w, req, err)
		return
	}
	files, err := p.ModpackDeps(req.Context(), chi.URLParam(req, "project"))
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadGateway, fmt.Errorf("resolve modpack: %w", err))
		return
	}
	writeJSON(w, files)
}

// installModpack applies an env-mode modpack (e.g. Modrinth on itzg): it
// patches the GameServer's env to pin the chosen pack, which the operator
// rolls out. Deps-mode providers (no refEnv) are installed via modpackDeps
// + /mods/install instead, so they get a 409 here.
func (h *registryHandler) installModpack(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	prov, ok := pickProvider(target.template, req.URL.Query().Get("provider"))
	if !ok || prov.modpacks == nil {
		httperr.WriteCode(w, req, http.StatusNotImplemented, errNoRegistry)
		return
	}
	if prov.modpacks.refEnv == "" {
		httperr.WriteCode(w, req, http.StatusConflict,
			errors.New("this provider installs modpacks per dependency; install the resolved mods instead"))
		return
	}

	var body struct {
		Ref string `json:"ref"`
	}
	if err := decodeBody(req, &body); err != nil || strings.TrimSpace(body.Ref) == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("ref is required"))
		return
	}

	apply := append([]envKV{{Name: prov.modpacks.refEnv, Value: strings.TrimSpace(body.Ref)}}, prov.modpacks.env...)
	if _, err := updateModTarget(req.Context(), target, func(current *unstructured.Unstructured) {
		setEnvVars(current, apply)
	}); err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// resolve picks the requested provider from the selected server's template,
// returning its central engine and the selected server's version filters.
func (h *registryHandler) resolve(ctx context.Context, target *modTarget, providerName string) (registry.Provider, string, string, error) {
	prov, ok := pickProvider(target.template, providerName)
	if !ok {
		return nil, "", "", errNoRegistry
	}
	p, ok := h.reg.For(ctx, registry.Config{
		Provider:         prov.provider,
		Community:        prov.community,
		SteamAppID:       prov.steamAppID,
		CurseForgeGameID: prov.curseforgeGameID,
		GitHubOwner:      prov.githubOwner,
		GitHubRepo:       prov.githubRepo,
	})
	if !ok {
		return nil, "", "", errNoRegistry
	}
	selected, _, _ := unstructured.NestedString(target.server.Object, "spec", "version")
	loader, gameVersion := activeVersion(target.template, selected)
	return p, loader, gameVersion, nil
}

func (h *registryHandler) writeResolveErr(w http.ResponseWriter, req *http.Request, err error) {
	if errors.Is(err, errNoRegistry) {
		httperr.WriteCode(w, req, http.StatusNotImplemented, err)
		return
	}
	httperr.Write(w, req, err)
}

type providerCfg struct {
	provider         string
	community        string
	steamAppID       int32
	curseforgeGameID int32
	githubOwner      string
	githubRepo       string
	modpacks         *modpackCfg
}

type modpackCfg struct {
	refEnv string
	env    []envKV
}

// registryProviders reads capabilities.mods.registry.providers[].
func registryProviders(tmpl *unstructured.Unstructured) []providerCfg {
	if tmpl == nil {
		return nil
	}
	list, found, err := unstructured.NestedSlice(tmpl.Object, "spec", "capabilities", "mods", "registry", "providers")
	if !found || err != nil {
		return nil
	}
	out := make([]providerCfg, 0, len(list))
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["provider"].(string)
		if name == "" {
			continue
		}
		cfg := providerCfg{provider: name}
		cfg.community, _ = m["community"].(string)
		cfg.steamAppID = nestedInt32(m["steamAppID"])
		cfg.curseforgeGameID = nestedInt32(m["curseforgeGameID"])
		if gh, ok := m["github"].(map[string]any); ok {
			cfg.githubOwner, _ = gh["owner"].(string)
			cfg.githubRepo, _ = gh["repo"].(string)
		}
		if mp, ok := m["modpacks"].(map[string]any); ok {
			mc := &modpackCfg{}
			mc.refEnv, _ = mp["refEnv"].(string)
			if envs, ok := mp["env"].([]any); ok {
				for _, e := range envs {
					if em, ok := e.(map[string]any); ok {
						n, _ := em["name"].(string)
						v, _ := em["value"].(string)
						if n != "" {
							mc.env = append(mc.env, envKV{Name: n, Value: v})
						}
					}
				}
			}
			cfg.modpacks = mc
		}
		out = append(out, cfg)
	}
	return out
}

// nestedInt32 reads a numeric field out of an unstructured map[string]any.
// The dynamic client's JSON decode represents whole numbers as int64 or
// json.Number depending on path, and a hand-built test fixture might use a
// plain int/float64 — so every shape apimachinery is documented to produce
// is handled rather than assuming one.
func nestedInt32(v any) int32 {
	switch n := v.(type) {
	case int64:
		if n >= math.MinInt32 && n <= math.MaxInt32 {
			return int32(n)
		}
	case int32:
		return n
	case int:
		if n >= math.MinInt32 && n <= math.MaxInt32 {
			return int32(n)
		}
	case float64:
		if n >= math.MinInt32 && n <= math.MaxInt32 {
			return int32(n)
		}
	case json.Number:
		if i, err := n.Int64(); err == nil && i >= math.MinInt32 && i <= math.MaxInt32 {
			return int32(i)
		}
	}
	return 0
}

// pickProvider returns the named provider's config, or the first declared
// when name is empty. ok is false when the template declares none.
func pickProvider(tmpl *unstructured.Unstructured, name string) (providerCfg, bool) {
	providers := registryProviders(tmpl)
	if len(providers) == 0 {
		return providerCfg{}, false
	}
	if name == "" {
		return providers[0], true
	}
	for _, p := range providers {
		if p.provider == name {
			return p, true
		}
	}
	return providerCfg{}, false
}

// activeVersion mirrors the operator's version selection (and the web's
// activeVersion): the entry whose id matches the server's selection, else
// the default entry, else the first. Returns the entry's loader id and
// clean game-version token. Both may be empty.
func activeVersion(tmpl *unstructured.Unstructured, selected string) (loader, gameVersion string) {
	versions, found, err := unstructured.NestedSlice(tmpl.Object, "spec", "versions")
	if !found || err != nil {
		return "", ""
	}
	var first, def map[string]any
	for _, raw := range versions {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if first == nil {
			first = m
		}
		if id, _ := m["id"].(string); selected != "" && id == selected {
			return loaderGameVersion(m)
		}
		if d, _ := m["default"].(bool); d && def == nil {
			def = m
		}
	}
	switch {
	case def != nil:
		return loaderGameVersion(def)
	case first != nil:
		return loaderGameVersion(first)
	}
	return "", ""
}

func loaderGameVersion(m map[string]any) (loader, gameVersion string) {
	loader, _ = m["loader"].(string)
	gameVersion, _ = m["gameVersion"].(string)
	return loader, gameVersion
}

type envKV struct {
	Name  string
	Value string
}

// setEnvVars merges apply into GameServer.spec.env in place: existing
// entries of the same name are overwritten, new ones appended (in order).
func setEnvVars(gs *unstructured.Unstructured, apply []envKV) {
	override := make(map[string]string, len(apply))
	order := make([]string, 0, len(apply))
	for _, kv := range apply {
		if _, seen := override[kv.Name]; !seen {
			order = append(order, kv.Name)
		}
		override[kv.Name] = kv.Value
	}

	existing, _, _ := unstructured.NestedSlice(gs.Object, "spec", "env")
	out := make([]any, 0, len(existing)+len(apply))
	done := map[string]bool{}
	for _, item := range existing {
		m, isMap := item.(map[string]any)
		if !isMap {
			out = append(out, item)
			continue
		}
		nm, _ := m["name"].(string)
		if v, ok := override[nm]; ok {
			out = append(out, map[string]any{"name": nm, "value": v})
			done[nm] = true
			continue
		}
		out = append(out, item)
	}
	for _, nm := range order {
		if !done[nm] {
			out = append(out, map[string]any{"name": nm, "value": override[nm]})
		}
	}
	_ = unstructured.SetNestedSlice(gs.Object, out, "spec", "env")
}

// decodeBody reads a small JSON request body into v.
func decodeBody(req *http.Request, v any) error {
	defer func() { _ = req.Body.Close() }()
	return json.NewDecoder(io.LimitReader(req.Body, 4<<10)).Decode(v)
}
