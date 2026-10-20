// Mods-by-id: games whose server downloads its own mods given a list of
// provider-native ids (ARK: Survival Ascended's CurseForge ids appended to
// its launch string, Project Zomboid's semicolon-separated MOD_IDS,
// generic Steam Workshop id lists) rather than the agent dropping files
// into a mods directory. See GameTemplate.spec.capabilities.mods.idList
// and GameServer.spec.mods.ids (operator/api/v1alpha1/{gametemplate,
// gameserver}_types.go) — the operator (not this handler) projects the
// selected ids into the declared game-container env var.

package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// errNoIDList signals that a server's template declares no
// capabilities.mods.idList (or has no template at all), i.e. this game
// doesn't install mods by id. Mirrors errNoRegistry's shape; mapped to
// 501 so the dashboard falls back to the file-based mods UI.
var errNoIDList = errors.New("this game does not support mod ids")

// modIDPattern mirrors ModRef.ID's CRD validation
// (operator/api/v1alpha1/gameserver_types.go: MinLength=1, MaxLength=64,
// Pattern=^[A-Za-z0-9._-]+$) so a malformed id is rejected here with a 400
// instead of round-tripping to the apiserver for a less friendly 422.
var modIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// modIDNameMaxLen mirrors ModRef.Name's MaxLength.
const modIDNameMaxLen = 128

// modIDListMaxItems mirrors GameServerModsSpec.IDs' MaxItems.
const modIDListMaxItems = 200

// ModID mirrors ModRef (operator/api/v1alpha1/gameserver_types.go): one
// provider-native mod id, optionally labeled for display so the dashboard
// can render the list without a registry round-trip.
type ModID struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// MountModIDs wires the id-list mods surface onto r, alongside the other
// server-scoped mod routes (see MountRegistry, MountModUpdates).
//
// PUT replaces the whole list in one write — there is no per-id
// add/remove — because every write to the GameServer's env triggers a
// StatefulSet rollout (buildGameContainer / modIDListEnv in the
// operator): a per-id POST/DELETE pair would mean one server restart per
// mod. The dashboard batches edits locally and saves once, the same as
// the existing env-vars editor.
func MountModIDs(r chi.Router, k *kube.Client) {
	mountModIDs(r, &modIDsHandler{k: k})
}

// MountModIDsWithRegistry reads and updates the selected cluster's GameServer
// using that cluster's template. The legacy bare-client mount stays local-only.
func MountModIDsWithRegistry(r chi.Router, clients *kube.Registry) {
	mountModIDs(r, &modIDsHandler{clients: clients})
}

func mountModIDs(r chi.Router, h *modIDsHandler) {
	r.Get("/servers/{name}/mods/ids", h.get)
	r.Put("/servers/{name}/mods/ids", h.put)
}

type modIDsHandler struct {
	clients *kube.Registry
	k       *kube.Client
}

func (h *modIDsHandler) get(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	if !declaresIDList(target.template) {
		httperr.WriteCode(w, req, http.StatusNotImplemented, errNoIDList)
		return
	}
	writeJSON(w, readModIDs(target.server))
}

func (h *modIDsHandler) put(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	if !declaresIDList(target.template) {
		httperr.WriteCode(w, req, http.StatusNotImplemented, errNoIDList)
		return
	}

	// Decoded directly against req.Body (not the shared decodeBody's 4 KiB
	// cap, sized for small single-field bodies): 200 ids at the CRD's
	// MaxLength=64/128 could serialize past 4 KiB. The global bodyLimit
	// middleware (main.go, 1 MiB) still bounds it.
	defer func() { _ = req.Body.Close() }()
	var ids []ModID
	if err := json.NewDecoder(req.Body).Decode(&ids); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid body"))
		return
	}
	if ids == nil {
		ids = []ModID{}
	}
	if len(ids) > modIDListMaxItems {
		httperr.WriteCode(w, req, http.StatusBadRequest,
			fmt.Errorf("too many mod ids: %d exceeds the %d limit", len(ids), modIDListMaxItems))
		return
	}
	for _, m := range ids {
		if !modIDPattern.MatchString(m.ID) {
			httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("invalid mod id %q", m.ID))
			return
		}
		if len(m.Name) > modIDNameMaxLen {
			httperr.WriteCode(w, req, http.StatusBadRequest,
				fmt.Errorf("mod name for %q exceeds %d characters", m.ID, modIDNameMaxLen))
			return
		}
	}

	updated, err := updateModTarget(req.Context(), target, func(current *unstructured.Unstructured) {
		writeModIDs(current, ids)
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, readModIDs(updated))
}

// declaresIDList reports whether tmpl declares
// capabilities.mods.idList. A nil tmpl (no templateRef) declares nothing.
func declaresIDList(tmpl *unstructured.Unstructured) bool {
	if tmpl == nil {
		return false
	}
	_, found, err := unstructured.NestedMap(tmpl.Object, "spec", "capabilities", "mods", "idList")
	return found && err == nil
}

// readModIDs reads GameServer.spec.mods.ids, always returning a non-nil
// slice so the JSON response is "[]" rather than "null" for an empty list.
func readModIDs(gs *unstructured.Unstructured) []ModID {
	list, found, err := unstructured.NestedSlice(gs.Object, "spec", "mods", "ids")
	if !found || err != nil {
		return []ModID{}
	}
	out := make([]ModID, 0, len(list))
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := m["id"].(string)
		if id == "" {
			continue
		}
		name, _ := m["name"].(string)
		out = append(out, ModID{ID: id, Name: name})
	}
	return out
}

// writeModIDs replaces GameServer.spec.mods.ids in place with ids (which
// may be empty, clearing the server's selection).
func writeModIDs(gs *unstructured.Unstructured, ids []ModID) {
	out := make([]any, 0, len(ids))
	for _, m := range ids {
		entry := map[string]any{"id": m.ID}
		if m.Name != "" {
			entry["name"] = m.Name
		}
		out = append(out, entry)
	}
	_ = unstructured.SetNestedSlice(gs.Object, out, "spec", "mods", "ids")
}
