package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

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

// MountShareLinks wires authenticated share link operations (create, list, revoke).
// Must be mounted inside the protected group with auth + rbac middleware.
// Ownership is verified per-request; only the server owner can manage shares.
func MountShareLinks(r chi.Router, reg *kube.Registry, store *db.Store) {
	r.Route("/servers/{name}:shares", func(r chi.Router) {
		r.Post("/", createShareHandler(reg, store))
		r.Get("/", listSharesHandler(reg, store))
	})
	r.Delete("/servers/{name}/shares/{id}", revokeShareHandler(reg, store))
}

// MountPublicShares wires unauthenticated share link resolution and start.
// Must be mounted at the root level with only the share rate limiter.
// Token is a path segment to keep it out of Referer headers and browser history;
// audit redaction masks it in recorded paths.
func MountPublicShares(r chi.Router, reg *kube.Registry, store *db.Store, shareLimiter *auth.TokenBucket) {
	r.Route("/shares/{token}", func(r chi.Router) {
		r.With(shareLimiter.Middleware).Get("/", resolveShareHandler(reg, store))
		r.With(shareLimiter.Middleware).Post("/start", startShareHandler(reg, store))
	})
}

type createShareReq struct {
	// ExpiresIn is deprecated as of the expiresAt/neverExpires fields below and
	// is kept working for one release only (OD-1/OD-5). It carries a Go
	// duration string (e.g. "24h", "168h"); an omitted or empty value defaults
	// to 7 days, but only on this deprecated path.
	ExpiresIn *string `json:"expiresIn"`
	// ExpiresAt is an RFC3339 absolute instant computed by the client (preset
	// or custom date). Mutually exclusive with NeverExpires.
	ExpiresAt *string `json:"expiresAt"`
	// NeverExpires, when true, creates a share link with no expiry (NULL).
	// Mutually exclusive with ExpiresAt.
	NeverExpires *bool `json:"neverExpires"`
	CanStart     bool  `json:"canStart"`
}

type shareResp struct {
	ID        string  `json:"id"`
	CreatedAt string  `json:"createdAt"`
	ExpiresAt *string `json:"expiresAt"` // RFC3339 string, or null when the link never expires
	RevokedAt *string `json:"revokedAt"` // RFC3339 string, or null when the link is active
	CanStart  bool    `json:"canStart"`
	Token     string  `json:"token,omitempty"` // only in create response
}

// formatExpiresAt renders a nullable expiry as an RFC3339 string pointer, or
// nil (which marshals as JSON null) when the link never expires.
func formatExpiresAt(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// formatRevokedAt renders a nullable revocation timestamp as an RFC3339 string pointer, or
// nil (which marshals as JSON null) when the link is active.
func formatRevokedAt(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

// createShareHandler stamps a new share link for the server. Only the owner can
// do this. The token is returned *once* in the response; list operations never
// include it.
func createShareHandler(reg *kube.Registry, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		u := auth.UserFromContext(req.Context())
		if u == nil {
			httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
			return
		}
		name := chi.URLParam(req, "name")
		ns, ok := resolveNS(w, req)
		if !ok {
			return
		}
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}

		// Verify ownership before allowing share creation.
		obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace(ns).
			Get(req.Context(), name, metav1.GetOptions{})
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		if !isServerOwner(obj, u.ID) {
			httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
			return
		}

		var body createShareReq
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<16)).Decode(&body); err != nil {
			httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
			return
		}

		// Determine the expiry to persist. Exactly one of expiresAt or
		// neverExpires:true must be present; the deprecated expiresIn field is
		// honored only when neither of those new fields is present (OD-1/OD-5/OD-7).
		hasExpiresAt := body.ExpiresAt != nil && *body.ExpiresAt != ""
		hasNeverExpires := body.NeverExpires != nil && *body.NeverExpires

		var expiresAt *time.Time
		switch {
		case hasExpiresAt && hasNeverExpires:
			httperr.WriteMessage(w, req, messages.New("server.shares.expiry_mutually_exclusive", nil))
			return
		case hasExpiresAt:
			parsed, err := time.Parse(time.RFC3339, *body.ExpiresAt)
			if err != nil {
				httperr.WriteMessage(w, req, messages.New("server.shares.invalid_expires_at", nil))
				return
			}
			parsed = parsed.UTC()
			expiresAt = &parsed
		case hasNeverExpires:
			expiresAt = nil
		case body.ExpiresIn != nil:
			// Deprecated path (kept for one release, OD-1/OD-5): an omitted or
			// empty value defaults to 7 days, only here.
			at := time.Now().UTC().AddDate(0, 0, 7)
			if *body.ExpiresIn != "" {
				d, err := time.ParseDuration(*body.ExpiresIn)
				if err != nil {
					httperr.WriteMessage(w, req, messages.New("server.shares.invalid_expires_in", nil))
					return
				}
				at = time.Now().UTC().Add(d)
			}
			expiresAt = &at
		default:
			httperr.WriteMessage(w, req, messages.New("server.shares.expiry_required", nil))
			return
		}

		cl, _ := scope.ResolveCluster(req, reg)
		rawToken, link, err := store.CreateShareLinkForServer(req.Context(), cl, ns, name, string(obj.GetUID()), u.ID, body.CanStart, expiresAt)
		if err != nil {
			if errors.Is(err, db.ErrShareLinkExpiryInvalid) {
				httperr.WriteMessage(w, req, messages.New("server.shares.expires_at_in_past", nil))
				return
			}
			httperr.Write(w, req, err)
			return
		}

		resp := shareResp{
			ID:        link.ID,
			CreatedAt: link.CreatedAt.UTC().Format(time.RFC3339),
			ExpiresAt: formatExpiresAt(link.ExpiresAt),
			RevokedAt: formatRevokedAt(link.RevokedAt),
			CanStart:  link.CanStart,
			Token:     rawToken, // only in create response
		}
		writeOrErr(w, req, resp, nil)
	}
}

// listSharesHandler lists all share links for a server (owner-only).
// The returned list never includes the raw token, only ID + metadata.
func listSharesHandler(reg *kube.Registry, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		u := auth.UserFromContext(req.Context())
		if u == nil {
			httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
			return
		}
		name := chi.URLParam(req, "name")
		ns, ok := resolveNS(w, req)
		if !ok {
			return
		}
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}

		// Verify ownership.
		obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace(ns).
			Get(req.Context(), name, metav1.GetOptions{})
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		if !isServerOwner(obj, u.ID) {
			httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
			return
		}

		cl, _ := scope.ResolveCluster(req, reg)
		links, err := store.ListShareLinks(req.Context(), cl, ns, name)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}

		// Convert to response format, excluding tokens.
		resps := make([]shareResp, len(links))
		for i, link := range links {
			resps[i] = shareResp{
				ID:        link.ID,
				CreatedAt: link.CreatedAt.UTC().Format(time.RFC3339),
				ExpiresAt: formatExpiresAt(link.ExpiresAt),
				RevokedAt: formatRevokedAt(link.RevokedAt),
				CanStart:  link.CanStart,
				// Token deliberately omitted in list response
			}
		}
		writeOrErr(w, req, resps, nil)
	}
}

// revokeShareHandler revokes a share link by ID (owner-only).
func revokeShareHandler(reg *kube.Registry, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		u := auth.UserFromContext(req.Context())
		if u == nil {
			httperr.WriteMessage(w, req, messages.New("server.common.unauthenticated", nil))
			return
		}
		name := chi.URLParam(req, "name")
		id := chi.URLParam(req, "id")
		ns, ok := resolveNS(w, req)
		if !ok {
			return
		}
		k, ok := resolveCluster(w, req, reg)
		if !ok {
			return
		}

		// Verify ownership.
		obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace(ns).
			Get(req.Context(), name, metav1.GetOptions{})
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		if !isServerOwner(obj, u.ID) {
			httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
			return
		}

		cl, _ := scope.ResolveCluster(req, reg)
		if err := store.RevokeShareLink(req.Context(), cl, ns, name, id); err != nil {
			if errors.Is(err, db.ErrShareLinkNotFound) {
				httperr.WriteMessage(w, req, messages.New("server.not_found", nil))
				return
			}
			httperr.Write(w, req, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

type sharePublicResp struct {
	ServerName    string     `json:"serverName"`
	Status        string     `json:"status"`
	Address       *shareAddr `json:"address,omitempty"`
	PlayersOnline *int32     `json:"playersOnline,omitempty"`
}

type shareAddr struct {
	Host string `json:"host"`
	Port int32  `json:"port"`
}

// resolveShareHandler looks up a share link by token and returns a minimal
// public view of the server (no namespace, cluster, owner, version, etc.).
// Token is a path segment; audit redaction masks it in the recorded path.
// All failures return 404 with identical body so probing reveals nothing.
func resolveShareHandler(reg *kube.Registry, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		token := chi.URLParam(req, "token")
		if token == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}

		link, err := store.LookupShareLink(req.Context(), token)
		if err != nil {
			if errors.Is(err, db.ErrShareLinkInvalid) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			httperr.Write(w, req, err)
			return
		}

		// Touch LastUsed and fetch the server.
		_ = store.TouchShareLink(req.Context(), link.ID)

		// Resolve to the originating cluster recorded in the share link.
		targetCluster := link.Cluster
		if targetCluster == "" {
			targetCluster = scope.DefaultCluster
		}
		k, ok := reg.Get(targetCluster)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}

		obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace(link.Namespace).
			Get(req.Context(), link.ServerName, metav1.GetOptions{})
		if err != nil || serverReplacedSinceLink(obj, link) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}

		// Extract public view: status, address, player count.
		status := getPhase(obj)
		address := getPublicAddress(obj)
		players := getPlayersOnline(obj)

		resp := sharePublicResp{
			ServerName:    link.ServerName,
			Status:        status,
			Address:       address,
			PlayersOnline: players,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// startShareHandler wakes a sleeping server if the share link has canStart=true.
// Token is a path segment; audit redaction masks it in the recorded path.
// If canStart is false, returns 404 (not 403).
func startShareHandler(reg *kube.Registry, store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		token := chi.URLParam(req, "token")
		if token == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}

		link, err := store.LookupShareLink(req.Context(), token)
		if err != nil {
			if errors.Is(err, db.ErrShareLinkInvalid) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			httperr.Write(w, req, err)
			return
		}

		// Verify canStart; fail with 404 (not 403) to avoid confirming the link exists.
		if !link.CanStart {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}

		// Touch LastUsed.
		_ = store.TouchShareLink(req.Context(), link.ID)

		// Wake the server on the originating cluster recorded in the share link.
		targetCluster := link.Cluster
		if targetCluster == "" {
			targetCluster = scope.DefaultCluster
		}
		k, ok := reg.Get(targetCluster)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}

		obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace(link.Namespace).
			Get(req.Context(), link.ServerName, metav1.GetOptions{})
		if err != nil || serverReplacedSinceLink(obj, link) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
			return
		}

		// Bind the wake to the object checked above, including legacy links.
		// Never retry against a server recreated under the same name.
		wakeToken := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
		patch, _ := json.Marshal(conditionObjectPatch(map[string]any{
			"metadata": map[string]any{
				"annotations": map[string]any{idleWakeRequestedAnnotation: wakeToken},
			},
		}, obj))
		if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).
			Namespace(link.Namespace).
			Patch(req.Context(), link.ServerName, types.MergePatchType, patch, metav1.PatchOptions{}); err != nil {
			if apierrors.IsConflict(err) || apierrors.IsInvalid(err) || apierrors.IsNotFound(err) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			httperr.Write(w, req, err)
			return
		}

		w.WriteHeader(http.StatusAccepted)
	}
}

// serverReplacedSinceLink reports whether obj is not the GameServer the link
// was minted for, i.e. that server was deleted and a new one now holds the
// same name. A link carrying a ServerUID is valid only while the live
// server's UID equals it; the UID is authoritative and the timestamp is not
// consulted (creationTimestamp has 1s precision, so a same-second
// delete+recreate would pass a timestamp comparison). A legacy link
// (ServerUID == "", minted before migration 013) falls back to the
// creationTimestamp check: replaced when the server was created after the
// link. An empty UID is never treated as a match by itself. A zero
// creationTimestamp (never the case for a real apiserver object) is not
// treated as newer.
func serverReplacedSinceLink(obj *unstructured.Unstructured, link db.ShareLink) bool {
	if link.ServerUID != "" {
		return string(obj.GetUID()) != link.ServerUID
	}
	created := obj.GetCreationTimestamp().Time
	return !created.IsZero() && created.After(link.CreatedAt)
}

// getPhase extracts the server's phase (status.phase).
func getPhase(obj *unstructured.Unstructured) string {
	phase, ok, err := unstructured.NestedString(obj.Object, "status", "phase")
	if err != nil || !ok {
		return "Unknown"
	}
	return phase
}

// getPlayersOnline extracts player count from status.agent.playersOnline if present.
func getPlayersOnline(obj *unstructured.Unstructured) *int32 {
	val, ok, err := unstructured.NestedFieldNoCopy(obj.Object, "status", "agent", "playersOnline")
	if err != nil || !ok {
		return nil
	}
	// NestedFieldNoCopy returns interface{}, could be float64 (JSON number) or int64.
	switch v := val.(type) {
	case float64:
		if v < 0 || v > 2147483647 {
			return nil
		}
		n := int32(v)
		return &n
	case int64:
		if v < 0 || v > 2147483647 {
			return nil
		}
		n := int32(v)
		return &n
	case int:
		if v < 0 || v > 2147483647 {
			return nil
		}
		n := int32(v)
		return &n
	}
	return nil
}

// getPublicAddress returns the first publicly reachable (non-private) endpoint
// that is served by a tunnel provider. If no tunnel endpoint, falls back to the
// first non-private regular endpoint. Returns nil if no suitable endpoint exists.
func getPublicAddress(obj *unstructured.Unstructured) *shareAddr {
	endpoints, ok, err := unstructured.NestedSlice(obj.Object, "status", "endpoints")
	if err != nil || !ok || len(endpoints) == 0 {
		return nil
	}

	// First pass: prefer tunnel endpoints that are not private.
	for _, ep := range endpoints {
		epMap, ok := ep.(map[string]interface{})
		if !ok {
			continue
		}
		provider, _, _ := unstructured.NestedString(epMap, "tunnelProvider")
		if provider == "" {
			continue
		}
		private, _, _ := unstructured.NestedBool(epMap, "private")
		if private {
			continue
		}
		host, _, _ := unstructured.NestedString(epMap, "host")
		port := nestedPort(epMap)
		if host != "" && port > 0 {
			return &shareAddr{Host: host, Port: port}
		}
	}

	// Second pass: fall back to any non-private endpoint (tunnel or service).
	for _, ep := range endpoints {
		epMap, ok := ep.(map[string]interface{})
		if !ok {
			continue
		}
		private, _, _ := unstructured.NestedBool(epMap, "private")
		if private {
			continue
		}
		host, _, _ := unstructured.NestedString(epMap, "host")
		port := nestedPort(epMap)
		if host != "" && port > 0 {
			return &shareAddr{Host: host, Port: port}
		}
	}

	return nil
}

// nestedPort extracts the "port" field from an endpoint map as an int32.
// unstructured.NestedInt64 only accepts a value already typed as int64, but
// endpoint maps are not guaranteed to come from that exact path: objects
// decoded from raw JSON (encoding/json into map[string]interface{}) yield
// float64 for every number, and objects built directly in Go (e.g. test
// fixtures) may use a plain int. Handle all three so callers get a port
// regardless of how the unstructured object was constructed. Valid ports are
// 0-65535; invalid values return 0.
func nestedPort(epMap map[string]interface{}) int32 {
	switch v := epMap["port"].(type) {
	case int64:
		if v < 0 || v > 65535 {
			return 0
		}
		return int32(v)
	case float64:
		if v < 0 || v > 65535 {
			return 0
		}
		return int32(v)
	case int32:
		if v < 0 || v > 65535 {
			return 0
		}
		return v
	case int:
		if v < 0 || v > 65535 {
			return 0
		}
		return int32(v)
	}
	return 0
}

// isServerOwner checks if a user is the owner of a GameServer by examining the
// gameplane.local/owner-id annotation. This duplicates logic from rbac.ownershipRole
// to avoid circular imports; it only checks ownership, not collaborator status.
func isServerOwner(obj *unstructured.Unstructured, userID int64) bool {
	ann := obj.GetAnnotations()
	if ann == nil {
		return false
	}
	ownerIDStr := ann["gameplane.local/owner-id"]
	if ownerIDStr == "" {
		return false
	}
	ownerID, err := strconv.ParseInt(ownerIDStr, 10, 64)
	if err != nil {
		return false
	}
	return ownerID == userID
}
