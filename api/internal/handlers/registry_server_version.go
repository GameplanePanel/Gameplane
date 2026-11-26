package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/registry"
)

// Spec 035 FR-014 ("All loaders"): a modpack browse can list every pack
// version and name the template version each one runs on, and an install can
// switch the server to that template version in the same write as the pack.

// errServerVersionMismatch means the POST serverVersion cannot run the chosen
// pack version (unknown or moving entry, or the pack has no such version for
// that entry). Mapped to server.modpack.server_version_mismatch (400).
var errServerVersionMismatch = errors.New("the chosen pack version does not run on that server version")

// allLoadersRequested reports whether the request asked for the "All loaders"
// modpack browse (?allLoaders=true). It only has an effect with type=modpack.
func allLoadersRequested(req *http.Request) bool {
	return req.URL.Query().Get("allLoaders") == "true"
}

// currentServerVersionID is the server's selected template version id; when the
// server selects none, the default entry the operator would run.
func currentServerVersionID(target *modTarget) string {
	if selected, _, _ := unstructured.NestedString(target.server.Object, "spec", "version"); selected != "" {
		return selected
	}
	return entryID(activeVersionEntry(target.template, ""))
}

// serverVersionTarget validates a POST serverVersion that switches the server
// away from its current version. It returns the template entry the server
// switches to when the pack version body.Version exists for that entry's
// loader and Minecraft version. errServerVersionMismatch means the request
// cannot be honoured; any other error is a provider failure.
func (h *registryHandler) serverVersionTarget(ctx context.Context, target *modTarget, prov providerCfg, body modpackInstallBody) (map[string]any, error) {
	entry := templateEntryByID(target.template, body.ServerVersion)
	loader, gameVersion := loaderGameVersion(entry)
	if entry == nil || gameVersion == "" || body.Version == "" {
		return nil, errServerVersionMismatch
	}
	p, ok := h.providerFor(ctx, prov)
	if !ok {
		return nil, errNoRegistry
	}
	versions, err := p.Versions(ctx, body.Project, registry.Filter{
		Loader:      loader,
		GameVersion: gameVersion,
		ProjectType: "modpack",
	})
	if err != nil {
		return nil, fmt.Errorf("mod registry versions failed: %w", err)
	}
	if !slices.ContainsFunc(versions, func(v registry.Version) bool { return v.ID == body.Version }) {
		return nil, errServerVersionMismatch
	}
	return entry, nil
}

// matchServerVersions keeps the pack versions that some template version entry
// can run, and names that entry on each kept version. An entry fits a version
// when its loader is one of the version's loaders (case-insensitive) and its
// gameVersion is one of the version's game versions; moving entries without a
// gameVersion never fit. The server's current entry wins when it fits, else the
// first fitting entry in template order. Versions with no fitting entry are
// dropped.
func matchServerVersions(tmpl *unstructured.Unstructured, selected string, versions []registry.Version) []registry.Version {
	entries := templateVersionEntries(tmpl)
	current := activeVersionEntry(tmpl, selected)
	out := make([]registry.Version, 0, len(versions))
	for _, v := range versions {
		entry := current
		if entry == nil || !entryFits(entry, v) {
			entry = nil
			for _, e := range entries {
				if entryFits(e, v) {
					entry = e
					break
				}
			}
		}
		if entry == nil {
			continue
		}
		v.ServerVersion, _ = entry["id"].(string)
		v.ServerVersionName, _ = entry["displayName"].(string)
		out = append(out, v)
	}
	return out
}

// entryFits reports whether a template version entry can run a pack version.
func entryFits(entry map[string]any, v registry.Version) bool {
	loader, gameVersion := loaderGameVersion(entry)
	if loader == "" || gameVersion == "" {
		return false
	}
	loaderOK := slices.ContainsFunc(v.Loaders, func(l string) bool { return strings.EqualFold(l, loader) })
	return loaderOK && slices.Contains(v.GameVersions, gameVersion)
}

// templateVersionEntries returns the template's spec.versions maps in order.
func templateVersionEntries(tmpl *unstructured.Unstructured) []map[string]any {
	if tmpl == nil {
		return nil
	}
	raw, found, err := unstructured.NestedSlice(tmpl.Object, "spec", "versions")
	if !found || err != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// templateEntryByID returns the template version entry with the given id, or nil.
func templateEntryByID(tmpl *unstructured.Unstructured, id string) map[string]any {
	if id == "" {
		return nil
	}
	for _, e := range templateVersionEntries(tmpl) {
		if entryID(e) == id {
			return e
		}
	}
	return nil
}

// entryID returns a template version entry's id, "" for nil.
func entryID(entry map[string]any) string {
	id, _ := entry["id"].(string)
	return id
}
