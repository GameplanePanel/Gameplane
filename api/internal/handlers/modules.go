// Modules wires the OCI-backed module distribution surface:
//
//   - GET    /modules/sources           — list ModuleSources (registry config)
//   - GET    /modules/catalog           — merged catalog: per-module aggregate
//                                         across sources, with installation state
//   - GET    /modules                   — list installed Module CRs
//   - POST   /modules                   — install: create a Module CR
//   - GET    /modules/{name}/locales/{tag} — module translations for a language
//   - PATCH  /modules/{name}            — upgrade: patch spec.version
//   - DELETE /modules/{name}            — uninstall: delete the Module CR
//
// Catalog data is read directly from ModuleSource.status.modules — the
// operator pulls the registry on a schedule, the API just serves the
// cache. Installs go through the K8s API (Module CR creation) so the
// flow is identical whether driven from this handler or kubectl.

package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// MountModules wires the /modules surface onto r. namespace is the
// operator namespace, where uploaded bundle ConfigMaps live.
func MountModules(r chi.Router, k *kube.Client, namespace string) {
	h := modulesHandler{k: k, namespace: namespace}
	r.Route("/modules", func(r chi.Router) {
		r.Get("/", h.listInstalled)
		r.Post("/", h.install)
		r.Get("/sources", h.listSources)
		r.Post("/sources", h.createSource)
		r.Put("/sources/{name}", h.updateSource)
		r.Delete("/sources/{name}", h.deleteSource)
		r.Post("/sources/{name}/upload", h.uploadBundle)
		r.Delete("/sources/{name}/upload/{module}", h.deleteUpload)
		r.Get("/catalog", h.catalog)
		r.Route("/builder", func(r chi.Router) {
			r.Get("/archetypes", h.builderArchetypes)
			r.Post("/scaffold", h.builderScaffold)
			r.Post("/validate", h.builderValidate)
			r.Post("/preview", h.builderPreview)
			r.Post("/export", h.builderExport)
		})
		r.Get("/{name}/locales/{tag}", h.getLocales)
		r.Get("/{name}", h.getInstalled)
		r.Patch("/{name}", h.upgrade)
		r.Delete("/{name}", h.uninstall)
	})
}

type modulesHandler struct {
	k         *kube.Client
	namespace string
}

// SourceRef names one ModuleSource offering a catalog entry, with its
// type so the UI can badge where the module comes from.
type SourceRef struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// CatalogEntry is one row in the merged catalog response.
type CatalogEntry struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Game        string   `json:"game,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	// Sources has no omitempty — the web client types it as a required
	// array and iterates it unguarded — so every construction site must
	// initialize it to []SourceRef{}, never leave it nil, or it marshals
	// as "sources": null and crashes the Modules page.
	Sources          []SourceRef `json:"sources"`
	Versions         []string    `json:"versions,omitempty"`
	LatestVersion    string      `json:"latestVersion,omitempty"`
	Digest           string      `json:"digest,omitempty"`
	Installed        bool        `json:"installed"`
	InstalledVersion string      `json:"installedVersion,omitempty"`
	InstalledFrom    string      `json:"installedFrom,omitempty"` // ModuleSource name
	ModuleName       string      `json:"moduleName,omitempty"`    // Module CR name (= template name)
	Phase            string      `json:"phase,omitempty"`
	LastError        string      `json:"lastError,omitempty"`
	// PinnedVersion is the Module's desired spec.version (what it's pinned
	// to), which can differ from InstalledVersion when a pin fails to
	// apply. Empty means "track latest".
	PinnedVersion string `json:"pinnedVersion,omitempty"`
	// Reason is the machine-readable Ready-condition reason (e.g.
	// "VersionUnavailable", "PullFailed"). Lets the UI act on WHY a module
	// failed — e.g. offer an update when the pinned version left the catalog.
	Reason string `json:"reason,omitempty"`
	// AppliedDigest is the bundle digest backing the installed version,
	// for auditability. Mirrors Module.status.appliedDigest.
	AppliedDigest string `json:"appliedDigest,omitempty"`
	// VerifiedDigest and VerifyPolicy mirror Module.status: the digest that
	// was actually signature-checked and under which policy, empty when no
	// verification ran. The web client uses VerifiedDigest (not the
	// source's current policy) to decide whether the solid "verified"
	// badge is warranted.
	VerifiedDigest string `json:"verifiedDigest,omitempty"`
	VerifyPolicy   string `json:"verifyPolicy,omitempty"`
	// PreviousVersion and PreviousDigest record the last-known-good the
	// operator rolls back to (Module.status). Surfaced read-only so the
	// UI can show a rollback target; the operator owns the rollback itself.
	PreviousVersion string `json:"previousVersion,omitempty"`
	PreviousDigest  string `json:"previousDigest,omitempty"`
}

type catalogResponse struct {
	Items []CatalogEntry `json:"items"`
}

type sourceListResponse struct {
	Items []*unstructured.Unstructured `json:"items"`
}

type installRequest struct {
	// Source is the ModuleSource name.
	Source string `json:"source"`
	// Module is the module name within the source.
	Module string `json:"module"`
	// Name is the resulting Module CR name (and GameTemplate name).
	// Defaults to Module when omitted.
	Name string `json:"name,omitempty"`
	// Version is an optional pin; empty tracks latest.
	Version string `json:"version,omitempty"`
}

type upgradeRequest struct {
	Version string `json:"version"`
}

func (h modulesHandler) listSources(w http.ResponseWriter, req *http.Request) {
	list, err := h.k.Dynamic.Resource(kube.GVRModuleSource).List(req.Context(), metav1.ListOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	out := sourceListResponse{Items: make([]*unstructured.Unstructured, 0, len(list.Items))}
	for i := range list.Items {
		out.Items = append(out.Items, &list.Items[i])
	}
	writeJSON(w, out)
}

func (h modulesHandler) listInstalled(w http.ResponseWriter, req *http.Request) {
	list, err := h.k.Dynamic.Resource(kube.GVRModule).List(req.Context(), metav1.ListOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, list)
}

func (h modulesHandler) getInstalled(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	mod, err := h.k.Dynamic.Resource(kube.GVRModule).Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, mod)
}

func (h modulesHandler) install(w http.ResponseWriter, req *http.Request) {
	var in installRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		httperr.Write(w, req, err)
		return
	}
	if in.Source == "" || in.Module == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("source and module are required"))
		return
	}
	name := in.Name
	if name == "" {
		name = in.Module
	}
	if !dnsLabelRE.MatchString(name) {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("name must be a DNS label (lowercase, digits, hyphens)"))
		return
	}

	desired := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Module",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"source":  map[string]any{"name": in.Source},
			"name":    in.Module,
			"version": in.Version,
		},
	}}
	created, err := h.k.Dynamic.Resource(kube.GVRModule).Create(req.Context(), desired, metav1.CreateOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSONCreated(w, created)
}

func (h modulesHandler) upgrade(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	var in upgradeRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		httperr.Write(w, req, err)
		return
	}
	patch := map[string]any{"spec": map[string]any{"version": in.Version}}
	body, _ := json.Marshal(patch)
	updated, err := h.k.Dynamic.Resource(kube.GVRModule).Patch(
		req.Context(), name, types.MergePatchType, body, metav1.PatchOptions{},
	)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, updated)
}

func (h modulesHandler) uninstall(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	// Surface the in-use blocker (set by the operator's finalizer) as a
	// 409 so the UI can render a useful error. Otherwise the Delete call
	// would succeed (DeletionTimestamp set) and the user would never be
	// told the resource is stuck on a finalizer.
	if blocker, err := h.uninstallBlocker(req, name); err == nil && blocker != "" {
		httperr.WriteCode(w, req, http.StatusConflict, errors.New(blocker))
		return
	}
	if err := h.k.Dynamic.Resource(kube.GVRModule).Delete(req.Context(), name, metav1.DeleteOptions{}); err != nil {
		httperr.Write(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// uninstallBlocker returns a non-empty string when the Module is still
// blocking its template's removal — typically a GameServer references
// the GameTemplate. The operator surfaces this on Module.status.lastError
// + Conditions[type=Ready].
func (h modulesHandler) uninstallBlocker(req *http.Request, name string) (string, error) {
	mod, err := h.k.Dynamic.Resource(kube.GVRModule).Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	// We only refuse early when the Module has already been observed to
	// be in-use AND a delete is being initiated; checking Phase=Failed
	// reason=InUse would be authoritative once a delete is in flight,
	// but pre-delete we use Status.AppliedTemplate + GameServer scan.
	tmpl, _, _ := unstructured.NestedString(mod.Object, "status", "appliedTemplate")
	if tmpl == "" {
		tmpl = name
	}
	servers, err := h.k.Dynamic.Resource(kube.GVRs["servers"]).List(req.Context(), metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	var users []string
	for i := range servers.Items {
		ref, _, _ := unstructured.NestedString(servers.Items[i].Object, "spec", "templateRef", "name")
		if ref == tmpl {
			ns := servers.Items[i].GetNamespace()
			users = append(users, ns+"/"+servers.Items[i].GetName())
		}
	}
	if len(users) == 0 {
		return "", nil
	}
	return "GameTemplate \"" + tmpl + "\" is still in use by: " + joinNames(users), nil
}

func joinNames(in []string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

// readyConditionReason returns the machine-readable reason of the Module's
// Ready condition (e.g. "VersionUnavailable"), or "" if absent.
func readyConditionReason(obj map[string]any) string {
	conds, found, _ := unstructured.NestedSlice(obj, "status", "conditions")
	if !found {
		return ""
	}
	for _, c := range conds {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := cm["type"].(string); t == "Ready" {
			r, _ := cm["reason"].(string)
			return r
		}
	}
	return ""
}

//   - every Module CR (installed state)
//
// into a single per-name aggregate.
func (h modulesHandler) catalog(w http.ResponseWriter, req *http.Request) {
	sources, err := h.k.Dynamic.Resource(kube.GVRModuleSource).List(req.Context(), metav1.ListOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	mods, err := h.k.Dynamic.Resource(kube.GVRModule).List(req.Context(), metav1.ListOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}

	merged := map[string]*CatalogEntry{}
	for i := range sources.Items {
		src := &sources.Items[i]
		srcType, _, _ := unstructured.NestedString(src.Object, "spec", "type")
		if srcType == "" {
			srcType = "oci"
		}
		entries, _, _ := unstructured.NestedSlice(src.Object, "status", "modules")
		for _, raw := range entries {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if name == "" {
				continue
			}
			e, ok := merged[name]
			if !ok {
				e = &CatalogEntry{Name: name}
				merged[name] = e
			}
			if dn, _ := m["displayName"].(string); dn != "" && e.DisplayName == "" {
				e.DisplayName = dn
			}
			if s, _ := m["summary"].(string); s != "" && e.Summary == "" {
				e.Summary = s
			}
			if g, _ := m["game"].(string); g != "" && e.Game == "" {
				e.Game = g
			}
			cats, _, _ := unstructured.NestedStringSlice(m, "categories")
			e.Categories = mergeCategories(e.Categories, cats)
			if ic, _ := m["icon"].(string); ic != "" && e.Icon == "" {
				e.Icon = ic
			}
			e.Sources = appendUniqueSource(e.Sources, SourceRef{Name: src.GetName(), Type: srcType})
			vers, _, _ := unstructured.NestedStringSlice(m, "versions")
			e.Versions = mergeVersions(e.Versions, vers)
			if lv, _ := m["latestVersion"].(string); lv != "" {
				if e.LatestVersion == "" || semverDescending(lv, e.LatestVersion) {
					e.LatestVersion = lv
					if d, _ := m["digest"].(string); d != "" {
						e.Digest = d
					}
				}
			}
		}
	}

	for i := range mods.Items {
		mod := &mods.Items[i]
		modName, _, _ := unstructured.NestedString(mod.Object, "spec", "name")
		srcName, _, _ := unstructured.NestedString(mod.Object, "spec", "source", "name")
		appliedVersion, _, _ := unstructured.NestedString(mod.Object, "status", "appliedVersion")
		phase, _, _ := unstructured.NestedString(mod.Object, "status", "phase")
		lastError, _, _ := unstructured.NestedString(mod.Object, "status", "lastError")
		appliedDigest, _, _ := unstructured.NestedString(mod.Object, "status", "appliedDigest")
		verifiedDigest, _, _ := unstructured.NestedString(mod.Object, "status", "verifiedDigest")
		verifyPolicy, _, _ := unstructured.NestedString(mod.Object, "status", "verifyPolicy")
		previousVersion, _, _ := unstructured.NestedString(mod.Object, "status", "previousVersion")
		previousDigest, _, _ := unstructured.NestedString(mod.Object, "status", "previousDigest")
		pinnedVersion, _, _ := unstructured.NestedString(mod.Object, "spec", "version")
		reason := readyConditionReason(mod.Object)

		e, ok := merged[modName]
		if !ok {
			// Installed but not (yet) in any catalog. Surface anyway so
			// the UI doesn't lose track of pending installs while the
			// source is mid-index. Sources must start non-nil — the field
			// has no `omitempty` (web/ types it as a required array), so a
			// nil slice here would marshal as "sources": null and crash
			// the Modules page.
			e = &CatalogEntry{Name: modName, Sources: []SourceRef{}}
			merged[modName] = e
		}
		e.Installed = true
		e.InstalledVersion = appliedVersion
		e.InstalledFrom = srcName
		e.ModuleName = mod.GetName()
		e.Phase = phase
		e.LastError = lastError
		e.AppliedDigest = appliedDigest
		e.VerifiedDigest = verifiedDigest
		e.VerifyPolicy = verifyPolicy
		e.PreviousVersion = previousVersion
		e.PreviousDigest = previousDigest
		e.PinnedVersion = pinnedVersion
		e.Reason = reason
	}

	out := catalogResponse{Items: make([]CatalogEntry, 0, len(merged))}
	for _, e := range merged {
		// Belt-and-suspenders: whatever path built e, Sources must never
		// serialize as null (see the CatalogEntry doc comment on Sources).
		if e.Sources == nil {
			e.Sources = []SourceRef{}
		}
		out.Items = append(out.Items, *e)
	}
	sort.Slice(out.Items, func(i, j int) bool {
		return out.Items[i].Name < out.Items[j].Name
	})
	writeJSON(w, out)
}

func appendUniqueSource(s []SourceRef, v SourceRef) []SourceRef {
	for _, x := range s {
		if x.Name == v.Name {
			return s
		}
	}
	return append(s, v)
}

// mergeVersions returns the union of a and b, semver-descending.
func mergeVersions(a, b []string) []string {
	seen := map[string]struct{}{}
	for _, v := range a {
		seen[v] = struct{}{}
	}
	for _, v := range b {
		seen[v] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return semverDescending(out[i], out[j]) })
	return out
}

// mergeCategories unions a module's categories across the ModuleSources
// serving it. Dedupe is case-insensitive so "Survival" and "survival" do
// not become two chips; the first spelling seen wins, and input order is
// preserved so the dashboard's chip list is stable across reloads.
func mergeCategories(dst, src []string) []string {
	seen := make(map[string]struct{}, len(dst)+len(src))
	out := make([]string, 0, len(dst)+len(src))
	for _, list := range [][]string{dst, src} {
		for _, c := range list {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			k := strings.ToLower(c)
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, c)
		}
	}
	return out
}
