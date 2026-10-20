package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// Owner annotations record which user a GameServer belongs to. Ownership
// is informational (display + transfer + audit), and can grant per-server
// access to the owner and collaborators.
const (
	ownerIDAnnotation           = "gameplane.local/owner-id"
	ownerAnnotation             = "gameplane.local/owner"
	collaboratorsAnnotation     = "gameplane.local/collaborators"
	collaboratorNamesAnnotation = "gameplane.local/collaborator-names"
)

// stampOwner records the authenticated caller as the owner of obj. Called
// on GameServer creation; overrides any client-supplied owner annotations
// so ownership can't be spoofed. Also strips client-supplied collaborator
// annotations to prevent spoofing the collaborator list.
func stampOwner(obj *unstructured.Unstructured, req *http.Request) {
	u := auth.UserFromContext(req.Context())
	if u == nil {
		return
	}
	ann := obj.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	ann[ownerIDAnnotation] = strconv.FormatInt(u.ID, 10)
	ann[ownerAnnotation] = u.Username
	// Strip client-supplied collaborator annotations.
	delete(ann, collaboratorsAnnotation)
	delete(ann, collaboratorNamesAnnotation)
	obj.SetAnnotations(ann)
}

// requireOwnerOrAdmin admits the caller to an owner-only server operation
// (ownership transfer, collaborator edits, data wipe) only when they own
// the server or hold the admin wildcard ("*") in the target cluster and
// namespace. rbac.Middleware applies the same rule before the handler
// runs; this repeats it at the handler. It returns the live server so the
// caller can reuse it. ok=false means a response was already written.
func requireOwnerOrAdmin(w http.ResponseWriter, req *http.Request, reg *kube.Registry, k *kube.Client, ns, name string) (*unstructured.Unstructured, bool) {
	u := auth.UserFromContext(req.Context())
	if u == nil {
		httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
		return nil, false
	}
	cl, err := scope.ResolveCluster(req, reg)
	if err != nil {
		httperr.Write(w, req, err)
		return nil, false
	}
	obj, ok := authorizedServer(w, req, k, ns, name)
	if !ok {
		return nil, false
	}
	if !u.Can("*", true, cl, ns) && !isServerOwner(obj, u.ID) {
		httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
		return nil, false
	}
	return obj, true
}

// ownerOnlyPatchAttempts bounds how many times an owner-only mutation
// re-reads and re-authorizes the server after a resourceVersion conflict.
const ownerOnlyPatchAttempts = 3

// patchServerAsOwner applies the merge patch that build returns for obj,
// the server requireOwnerOrAdmin just authorized against. The patch
// carries obj's UID and resourceVersion, so the API server rejects it with 409
// Conflict if the server changed after the check read it (for example an
// ownership transfer by someone else). On a conflict it re-reads the
// server and repeats the owner-or-admin check before rebuilding the patch,
// rejecting any replacement UID even for an unbound administrator,
// so a caller who lost ownership in the meantime is refused. After
// ownerOnlyPatchAttempts conflicts the client gets 409. Objects served by
// a real API server always carry a resourceVersion; one without it (test
// fixtures) is patched unconditionally. ok=false means a response was
// already written.
func patchServerAsOwner(w http.ResponseWriter, req *http.Request, reg *kube.Registry, k *kube.Client, ns, name string,
	obj *unstructured.Unstructured, build func(obj *unstructured.Unstructured) map[string]any,
) bool {
	uid := obj.GetUID()
	for attempt := 1; ; attempt++ {
		patch := conditionObjectPatch(build(obj), obj)
		body, err := json.Marshal(patch)
		if err != nil {
			httperr.Write(w, req, err)
			return false
		}
		_, err = k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace(ns).
			Patch(req.Context(), name, types.MergePatchType, body, metav1.PatchOptions{})
		if err == nil {
			return true
		}
		if !apierrors.IsConflict(err) || attempt >= ownerOnlyPatchAttempts {
			httperr.Write(w, req, err)
			return false
		}
		var ok bool
		if obj, ok = requireOwnerOrAdmin(w, req, reg, k, ns, name); !ok {
			return false
		}
		if uid != "" && obj.GetUID() != uid {
			httperr.Write(w, req, apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), name))
			return false
		}
	}
}

// MountOwnership wires the server ownership and collaborator endpoints.
func MountOwnership(r chi.Router, reg *kube.Registry, store *db.Store) {
	h := &ownershipHandler{reg: reg, db: store}
	r.Post("/servers/{name}:transfer", h.transfer)
	r.Put("/servers/{name}:collaborators", h.setCollaborators)
	r.Get("/users/me/servers", h.getOwnedServers)
}

type ownershipHandler struct {
	reg *kube.Registry
	db  *db.Store
}

type transferReq struct {
	UserID int64 `json:"userId"`
}

type setCollaboratorsReq struct {
	UserIDs   []int64  `json:"userIds"`
	Usernames []string `json:"usernames"`
}

// transfer reassigns a server's owner annotations to another user after
// validating the target exists. The audit middleware records the actor.
func (h *ownershipHandler) transfer(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	obj, ok := requireOwnerOrAdmin(w, req, h.reg, k, ns, name)
	if !ok {
		return
	}
	var body transferReq
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<16)).Decode(&body); err != nil || body.UserID <= 0 {
		httperr.WriteMessage(w, req, messages.New("server.ownership.user_id_required", nil))
		return
	}
	var username string
	err := h.db.DB.QueryRowContext(req.Context(),
		`SELECT username FROM users WHERE id = ?`, body.UserID).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		httperr.WriteMessage(w, req, messages.New("server.common.user_not_found", nil))
		return
	}
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if !patchServerAsOwner(w, req, h.reg, k, ns, name, obj, func(*unstructured.Unstructured) map[string]any {
		return map[string]any{
			"metadata": map[string]any{
				"annotations": map[string]any{
					ownerIDAnnotation: strconv.FormatInt(body.UserID, 10),
					ownerAnnotation:   username,
				},
			},
		}
	}) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setCollaborators updates the collaborators list for a GameServer.
// Validates all user IDs exist, dedupes, drops the owner ID, and
// merges both ID and name annotations. Empty list clears both.
func (h *ownershipHandler) setCollaborators(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	name := chi.URLParam(req, "name")
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	// The owner-or-admin check returns the live server; the owner-ID
	// filter in the patch reads it.
	obj, ok := requireOwnerOrAdmin(w, req, h.reg, k, ns, name)
	if !ok {
		return
	}
	var body setCollaboratorsReq
	if err := json.NewDecoder(io.LimitReader(req.Body, 1<<16)).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.ownership.invalid_request", nil))
		return
	}

	// Build a set of provided user IDs to resolve all usernames.
	seenIDs := make(map[int64]struct{})
	for _, id := range body.UserIDs {
		seenIDs[id] = struct{}{}
	}
	for _, name := range body.Usernames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var id int64
		err := h.db.DB.QueryRowContext(req.Context(),
			`SELECT id FROM users WHERE username = ?`, name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("user not found: "+name))
			return
		}
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		seenIDs[id] = struct{}{}
	}

	// Validate all IDs exist and resolve their usernames.
	idToName := make(map[int64]string, len(seenIDs))
	for id := range seenIDs {
		var username string
		err := h.db.DB.QueryRowContext(req.Context(),
			`SELECT username FROM users WHERE id = ?`, id).Scan(&username)
		if errors.Is(err, sql.ErrNoRows) {
			httperr.WriteMessage(w, req, messages.New("server.ownership.user_not_found_400", nil))
			return
		}
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		idToName[id] = username
	}

	// Patch the server. The owner filter reads the server the ownership
	// check authorized against, so it is rebuilt if that check is repeated.
	if !patchServerAsOwner(w, req, h.reg, k, ns, name, obj, func(cur *unstructured.Unstructured) map[string]any {
		collabIDsStr, collabNamesStr := collaboratorAnnotations(idToName, cur.GetAnnotations()[ownerIDAnnotation])
		return map[string]any{
			"metadata": map[string]any{
				"annotations": map[string]any{
					collaboratorsAnnotation:     collabIDsStr,
					collaboratorNamesAnnotation: collabNamesStr,
				},
			},
		}
	}) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// collaboratorAnnotations drops the owner ID from idToName, sorts the
// remaining IDs numerically ascending and returns the comma-joined ID and
// username annotation values, aligned by position. Both are empty when no
// collaborator remains.
func collaboratorAnnotations(idToName map[int64]string, ownerIDStr string) (string, string) {
	finalIDs := make([]int64, 0, len(idToName))
	for id := range idToName {
		if ownerIDStr != "" && ownerIDStr == strconv.FormatInt(id, 10) {
			continue
		}
		finalIDs = append(finalIDs, id)
	}
	if len(finalIDs) == 0 {
		return "", ""
	}
	sort.Slice(finalIDs, func(i, j int) bool { return finalIDs[i] < finalIDs[j] })
	idStrs := make([]string, len(finalIDs))
	nameStrs := make([]string, len(finalIDs))
	for i, id := range finalIDs {
		idStrs[i] = strconv.FormatInt(id, 10)
		nameStrs[i] = idToName[id]
	}
	return strings.Join(idStrs, ","), strings.Join(nameStrs, ",")
}

// getOwnedServers returns GameServers where the caller is owner or collaborator.
func (h *ownershipHandler) getOwnedServers(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	u := auth.UserFromContext(req.Context())
	if u == nil {
		httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
		return
	}

	// List all servers across all namespaces within the resolved cluster.
	list, err := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace("").
		List(req.Context(), metav1.ListOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}

	// Filter to owned/collaborated servers.
	filtered := &unstructured.UnstructuredList{
		Object: list.Object,
		Items:  make([]unstructured.Unstructured, 0, len(list.Items)),
	}
	for i := range list.Items {
		ann := list.Items[i].GetAnnotations()
		if ann == nil {
			continue
		}
		// Check if caller is owner.
		if ownerIDStr := ann["gameplane.local/owner-id"]; ownerIDStr != "" {
			if ownerIDStr == strconv.FormatInt(u.ID, 10) {
				gateStaleAgent(&list.Items[i])
				filtered.Items = append(filtered.Items, list.Items[i])
				continue
			}
		}
		// Check if caller is in collaborators list.
		if collabsStr := ann["gameplane.local/collaborators"]; collabsStr != "" {
			userIDStr := strconv.FormatInt(u.ID, 10)
			for _, id := range strings.Split(collabsStr, ",") {
				id = strings.TrimSpace(id)
				if id == userIDStr {
					gateStaleAgent(&list.Items[i])
					filtered.Items = append(filtered.Items, list.Items[i])
					break
				}
			}
		}
	}

	newConfigRuleCache(k).redactList(req.Context(), filtered)
	writeJSON(w, filtered)
}
