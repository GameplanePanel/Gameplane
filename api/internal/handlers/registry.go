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

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
)

// errNoRegistry signals that a server's template declares no browsable mod
// registry (or the requested provider isn't declared/available). Mapped to
// 501 so the dashboard hides Browse and falls back to install-by-URL.
var errNoRegistry = errors.New("no mod registry for this server")

// Install-body validation errors (400).
var (
	errModpackProject     = errors.New("project (or ref) is required and may only contain letters, digits, '.', '_' and '-' (max 128)")
	errModpackVersion     = errors.New("version may only contain letters, digits, '.', '_' and '-' (max 64)")
	errModpackBackupFirst = errors.New("backupFirst must be true or false")
)

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
	MountRegistryWithOptions(r, clients, reg, RegistryOptions{})
}

// RegistryOptions carries the optional collaborators of the modpack routes.
// The zero value is valid: no explicit audit rows, key sharing always reads as
// off, and missing-file / legacy-adoption reads report "unknown".
type RegistryOptions struct {
	// Audit records one explicit row per modpack install/change/remove.
	Audit *audit.Auditor
	// Home is the control-plane client that holds the managed registry key
	// Secret (central, never the selected cluster); ControlNS is its namespace.
	Home      *kube.Client
	ControlNS string
	// Files reads small files from a server's data volume through its agent.
	Files AgentFileReader
}

// MountRegistryWithOptions is MountRegistryWithRegistry plus RegistryOptions.
func MountRegistryWithOptions(r chi.Router, clients *kube.Registry, reg registrySet, opts RegistryOptions) {
	mountRegistry(r, &registryHandler{
		clients: clients, reg: reg,
		audit: opts.Audit, home: opts.Home, controlNS: opts.ControlNS, files: opts.Files,
	})
}

func mountRegistry(r chi.Router, h *registryHandler) {
	r.Get("/servers/{name}/mods/registry/providers", h.providers)
	r.Get("/servers/{name}/mods/registry/search", h.search)
	r.Get("/servers/{name}/mods/registry/projects/{project}/versions", h.versions)
	r.Get("/servers/{name}/mods/registry/projects/{project}/modpack", h.modpackDeps)
	r.Get("/servers/{name}/modpack", h.getModpack)
	r.Post("/servers/{name}/modpack", h.installModpack)
	r.Delete("/servers/{name}/modpack", h.removeModpack)
}

type registryHandler struct {
	clients   *kube.Registry
	k         *kube.Client
	reg       registrySet
	audit     *audit.Auditor
	home      *kube.Client
	controlNS string
	files     AgentFileReader
}

// providerInfo is one entry of the providers listing the dashboard uses to
// build its provider switch.
type providerInfo struct {
	Provider              string `json:"provider"`
	Available             bool   `json:"available"`
	Modpacks              bool   `json:"modpacks"`
	ModsBrowse            bool   `json:"modsBrowse"`
	ModpacksBlockedReason string `json:"modpacksBlockedReason"`
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
			Provider:              p.provider,
			Available:             h.reg.Available(req.Context(), p.provider),
			Modpacks:              p.modpacks != nil,
			ModsBrowse:            !p.modpacksOnly,
			ModpacksBlockedReason: h.modpacksBlockedReason(req.Context(), target, p),
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
	typ := req.URL.Query().Get("type")
	if typ == "modpack" && allLoadersRequested(req) {
		// FR-014: "All loaders" lists packs for every loader and Minecraft
		// version, so the server's facets do not narrow the search.
		loader, gameVersion = "", ""
	}
	res, err := p.Search(req.Context(), registry.SearchQuery{
		Term:        req.URL.Query().Get("q"),
		Loader:      loader,
		GameVersion: gameVersion,
		ProjectType: typ,
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
	project := chi.URLParam(req, "project")
	if !validProjectID(project) {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid project id"))
		return
	}
	typ := req.URL.Query().Get("type")
	allLoaders := typ == "modpack" && allLoadersRequested(req)
	filter := registry.Filter{Loader: loader, GameVersion: gameVersion, ProjectType: typ}
	if allLoaders {
		filter.Loader, filter.GameVersion = "", ""
	}
	res, err := p.Versions(req.Context(), project, filter)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadGateway, fmt.Errorf("mod registry versions failed: %w", err))
		return
	}
	if allLoaders {
		// FR-014: keep only the pack versions this server's template can run,
		// each naming the template version it runs on. An entry the key check
		// blocks is never offered; its verdict is computed once per entry.
		selected, _, _ := unstructured.NestedString(target.server.Object, "spec", "version")
		prov, _ := pickProvider(target.template, req.URL.Query().Get("provider"))
		ctx := req.Context()
		unblocked := map[string]bool{}
		for _, e := range templateVersionEntries(target.template) {
			if id := entryID(e); id != "" {
				if _, seen := unblocked[id]; !seen {
					unblocked[id] = h.blockedReasonFor(ctx, target, prov, e) == ""
				}
			}
		}
		allowed := func(entry map[string]any) bool { return unblocked[entryID(entry)] }
		res = matchServerVersionsAllowed(target.template, selected, res, allowed)
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
	project := chi.URLParam(req, "project")
	if !validProjectID(project) {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid project id"))
		return
	}
	files, err := p.ModpackDeps(req.Context(), project)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadGateway, fmt.Errorf("resolve modpack: %w", err))
		return
	}
	writeJSON(w, files)
}

// installModpack records an env-mode modpack (e.g. Modrinth, CurseForge, FTB
// on itzg) as GameServer.spec.modpack. It never touches spec.env: the operator
// renders the pack env and rolls the server out (spec 035, core rule 10).
// Deps-mode providers (no refEnv) are installed via modpackDeps +
// /mods/install instead, so they get a 409 here. The body is either the new
// {provider, project, version, title, versionName, backupFirst} (version
// required) or the legacy {"ref"} with ?provider= (project = ref, no pinned
// version). 400s (shape, version, backup destination) come before the 409s
// (key not shared, change in progress).
func (h *registryHandler) installModpack(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	var body modpackInstallBody
	if err := decodeBody(req, &body); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, errModpackProject)
		return
	}
	providerName := body.Provider
	if providerName == "" {
		providerName = req.URL.Query().Get("provider")
	}
	prov, ok := pickProvider(target.template, providerName)
	if !ok || prov.modpacks == nil {
		httperr.WriteCode(w, req, http.StatusNotImplemented, errNoRegistry)
		return
	}
	if prov.modpacks.refEnv == "" {
		httperr.WriteCode(w, req, http.StatusConflict,
			errors.New("this provider installs modpacks per dependency; install the resolved mods instead"))
		return
	}
	legacy := strings.TrimSpace(body.Project) == "" && strings.TrimSpace(body.Ref) != ""
	if !normalizeModpackBody(w, req, &body) {
		return
	}
	if !legacy && body.Version == "" {
		httperr.WriteMessage(w, req, messages.New("server.modpack.version_required", nil))
		return
	}
	if body.BackupFirst && h.backupDestinationName(req, target) == "" {
		httperr.WriteMessage(w, req, messages.New("server.modpack.backup_unavailable", nil))
		return
	}
	// The server runs the entry it has now, unless serverVersion switches it
	// (FR-014, OD-13 B); the key check and the audit row follow that entry.
	currentID := currentServerVersionID(target)
	runEntry := activeVersionEntry(target.template, currentID)
	var serverSwitch string
	if body.ServerVersion != "" && body.ServerVersion != currentID {
		entry, err := h.serverVersionTarget(req.Context(), target, prov, body)
		if errors.Is(err, errServerVersionMismatch) {
			httperr.WriteMessage(w, req, messages.New("server.modpack.server_version_mismatch", nil))
			return
		}
		if errors.Is(err, errNoRegistry) {
			h.writeResolveErr(w, req, err)
			return
		}
		if err != nil {
			httperr.WriteCode(w, req, http.StatusBadGateway, err)
			return
		}
		runEntry = entry
		serverSwitch = currentID + "->" + body.ServerVersion
	}
	if h.blockedReasonFor(req.Context(), target, prov, runEntry) != "" {
		httperr.WriteMessage(w, req, messages.New("server.modpack.key_required", nil))
		return
	}
	if modpackChangeInProgress(target.server) {
		httperr.WriteMessage(w, req, messages.New("server.modpack.change_in_progress", nil))
		return
	}
	action := "install"
	if cur := currentModpack(target.server); cur != nil && cur.Provider == prov.provider && cur.Project == body.Project {
		action = "change"
	}
	spec := modpackSpecFrom(prov.provider, body)
	// A fresh id per request: a repeat POST of the same choice after
	// BackupFailed is then a new spec value (new generation), which tells the
	// operator to start a new change.
	requestID, err := newModpackRequestID()
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	spec["requestId"] = requestID
	updated, err := updateModTarget(req.Context(), target, func(current *unstructured.Unstructured) {
		// SetNestedMap only fails on a non-map parent; spec is always a map here.
		_ = unstructured.SetNestedMap(current.Object, spec, "spec", "modpack")
		if serverSwitch != "" {
			// The server switches to the matching version in the same write as the pack.
			_ = unstructured.SetNestedField(current.Object, body.ServerVersion, "spec", "version")
		}
		// A removal whose backup failed leaves its request behind, and an
		// adopted legacy pin would hold back the new pack; installing a pack
		// supersedes both.
		if ann := current.GetAnnotations(); ann != nil {
			delete(ann, modpackRemoveAnnotation)
			delete(ann, modpackAdoptedAnnotation)
			current.SetAnnotations(ann)
		}
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	h.auditModpack(req, target.server.GetName(), action, prov.provider, body.Project, body.Version, serverSwitch)
	writeJSONStatus(w, http.StatusAccepted, h.modpackViewFor(req, target, updated, false))
}

// providerFor returns the central engine for a template's declared provider,
// or false when the engine is not configured.
func (h *registryHandler) providerFor(ctx context.Context, prov providerCfg) (registry.Provider, bool) {
	return h.reg.For(ctx, registry.Config{
		Provider:         prov.provider,
		Community:        prov.community,
		SteamAppID:       prov.steamAppID,
		CurseForgeGameID: prov.curseforgeGameID,
		GitHubOwner:      prov.githubOwner,
		GitHubRepo:       prov.githubRepo,
	})
}

// resolve picks the requested provider from the selected server's template,
// returning its central engine and the selected server's version filters.
func (h *registryHandler) resolve(ctx context.Context, target *modTarget, providerName string) (registry.Provider, string, string, error) {
	prov, ok := pickProvider(target.template, providerName)
	if !ok {
		return nil, "", "", errNoRegistry
	}
	p, ok := h.providerFor(ctx, prov)
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
	modpacksOnly     bool
	modpacks         *modpackCfg
}

type modpackCfg struct {
	refEnv            string
	versionEnv        string
	apiKeyEnv         string
	needsKeyImageTags []string
	missingFilesPath  string
	adoptVersionFile  *adoptFileCfg
	env               []envKV
}

// adoptFileCfg mirrors ModpackSpec.adoptVersionFile: a JSON file below the data
// mount and the top-level field of it that holds the installed version.
type adoptFileCfg struct {
	path  string
	field string
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
		cfg.modpacksOnly, _ = m["modpacksOnly"].(bool)
		if gh, ok := m["github"].(map[string]any); ok {
			cfg.githubOwner, _ = gh["owner"].(string)
			cfg.githubRepo, _ = gh["repo"].(string)
		}
		if mp, ok := m["modpacks"].(map[string]any); ok {
			mc := &modpackCfg{}
			mc.refEnv, _ = mp["refEnv"].(string)
			mc.versionEnv, _ = mp["versionEnv"].(string)
			mc.apiKeyEnv, _ = mp["apiKeyEnv"].(string)
			mc.missingFilesPath, _ = mp["missingFilesPath"].(string)
			if tags, ok := mp["needsKeyImageTags"].([]any); ok {
				for _, t := range tags {
					if s, ok := t.(string); ok && s != "" {
						mc.needsKeyImageTags = append(mc.needsKeyImageTags, s)
					}
				}
			}
			if af, ok := mp["adoptVersionFile"].(map[string]any); ok {
				p, _ := af["path"].(string)
				f, _ := af["field"].(string)
				if p != "" && f != "" {
					mc.adoptVersionFile = &adoptFileCfg{path: p, field: f}
				}
			}
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

// activeVersionEntry mirrors the operator's version selection (and the web's
// activeVersion): the entry whose id matches the server's selection, else the
// default entry, else the first. Nil when the template has no versions.
func activeVersionEntry(tmpl *unstructured.Unstructured, selected string) map[string]any {
	if tmpl == nil {
		return nil
	}
	versions, found, err := unstructured.NestedSlice(tmpl.Object, "spec", "versions")
	if !found || err != nil {
		return nil
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
			return m
		}
		if d, _ := m["default"].(bool); d && def == nil {
			def = m
		}
	}
	if def != nil {
		return def
	}
	return first
}

// activeVersion returns the selected entry's loader id and clean game-version
// token. Both may be empty.
func activeVersion(tmpl *unstructured.Unstructured, selected string) (loader, gameVersion string) {
	m := activeVersionEntry(tmpl, selected)
	if m == nil {
		return "", ""
	}
	return loaderGameVersion(m)
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

// validProjectID rejects the dot segments "." and "..", which url.PathEscape
// leaves as they are and which would walk up a provider's API path.
func validProjectID(id string) bool {
	return id != "" && id != "." && id != ".."
}

// decodeBody reads a small JSON request body into v.
func decodeBody(req *http.Request, v any) error {
	defer func() { _ = req.Body.Close() }()
	return json.NewDecoder(io.LimitReader(req.Body, 4<<10)).Decode(v)
}
