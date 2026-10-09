// Package rbac enforces permission-based access control on API routes.
//
// Roles are named sets of permissions (see catalog.go); users are bound
// to roles per namespace ("*" = cluster-wide). The rule table below maps
// each request to the single permission required to perform it, and the
// middleware checks the caller's resolved permission set — within the
// request's target namespace for namespaced permissions, cluster-wide
// for the rest. The built-in admin/operator/viewer roles are seeded so
// this reproduces the historical role matrix exactly.
//
// Owner and collaborator access is an additional fallback: when a
// namespace permission is denied and the request targets a specific
// GameServer, the middleware fetches the server and grants access if
// the caller is the owner or a collaborator. The owner-only operations
// (:transfer, :collaborators, :wipe-data and DELETE of the server) are
// never granted to collaborators, and they need the server's owner or an
// admin ("*" in the resolved cluster and namespace) even when the caller
// holds the namespace permission.
package rbac

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// ServerFetcher fetches a GameServer so the middleware can evaluate
// owner/collaborator access, and lists the registered cluster IDs so the
// middleware can validate the `?cluster=` selector. nil disables the
// fallback (and cluster validation defaults to scope.DefaultCluster).
// *kube.Registry satisfies this.
type ServerFetcher interface {
	GetServer(ctx context.Context, cluster, ns, name string) (*unstructured.Unstructured, error)
	IDs() []string
}

// Built-in role names. Custom roles may take any other (valid) name.
const (
	RoleAdmin    = "admin"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// Ownership role constants for server access fallback.
const (
	roleOwner = iota
	roleCollaborator
	roleNone
)

// Middleware blocks requests the caller isn't permitted to make. It
// finds the matching rule, resolves the target namespace for namespaced
// permissions, and checks the caller's permission set. When that check
// fails on a namespaced permission and a concrete GameServer is
// extractable from the path, it fetches the server and allows the
// request if the caller is owner or collaborator. Owner-only operations
// (verb :transfer, :collaborators, :wipe-data, or DELETE on bare /servers/{name})
// are denied to collaborators, and when the permission check passes they
// still need the server's owner or an admin ("*" in the resolved cluster
// and namespace). Invalid paths (trailing segments after a verb,
// e.g. /servers/a:transfer/extra) fail closed — the fallback does not apply.
// No matching rule means deny (fail-closed).
//
// For namespaced permissions, the `?cluster=` selector is also validated
// against fetch.IDs() (when fetch is non-nil) so the owner/collaborator
// fallback fetches from the right cluster; an unknown cluster is a 400.
// The cluster selector ALSO gates the permission check (u.Can): a binding on
// one cluster never grants access to another — this prevents cross-cluster
// privilege escalation.
func Middleware(fetch ServerFetcher) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			u := auth.UserFromContext(req.Context())
			if u == nil {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}
			r, ok := match(req.Method, req.URL.Path)
			if !ok {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			if r.perm == "" { // any authenticated user
				next.ServeHTTP(w, req)
				return
			}
			ns := ""
			cl := scope.DefaultCluster
			if clusterInventoryRead(req.Method, req.URL.Path) || strings.HasPrefix(req.URL.Path, "/modules") || strings.HasPrefix(req.URL.Path, "/templates") {
				// Authorize before discovery, including registered clusters whose
				// credentials could not load. The handler validates registration.
				cl = scope.RequestedCluster(req)
			}
			if Namespaced(r.perm) {
				resolved, err := scope.Resolve(req)
				if err != nil {
					http.Error(w, "namespace not permitted", http.StatusBadRequest)
					return
				}
				ns = resolved
				// rbac.Middleware must be mounted with a non-nil fetcher: cluster gating
				// for namespaced perms depends on resolving cl here, so a nil fetcher
				// would leave cl at the default cluster while a handler could dispatch elsewhere.
				if fetch != nil {
					resolvedCluster, err := scope.ResolveCluster(req, fetch)
					if err != nil {
						http.Error(w, "cluster not permitted", http.StatusBadRequest)
						return
					}
					cl = resolvedCluster
				}
			}
			if !allow(u, req.Method, req.URL.Path, cl, ns) {
				// Try owner/collaborator fallback for namespaced server permissions.
				if Namespaced(r.perm) && fetch != nil &&
					(r.perm == "servers:read" || r.perm == "servers:write" || r.perm == "servers:console") {
					name, verb, isExact, ok := parseServerPath(req.URL.Path)
					if ok {
						// Generic server mutations on the exact server object (e.g. PUT /servers/{name})
						// are denied on ownership/collaborator fallback. Full-object server updates
						// require explicit servers:write permission. Only GET (read) and DELETE
						// (owner-only deletion) are permitted on the exact server path.
						// Sub-resource writes (files, players, mods) continue through ownership fallback.
						if isExact && req.Method != http.MethodGet && req.Method != http.MethodHead && req.Method != http.MethodDelete {
							http.Error(w, "forbidden", http.StatusForbidden)
							return
						}
						obj, err := fetch.GetServer(req.Context(), cl, ns, name)
						if err == nil && obj != nil {
							role := ownershipRole(obj, u.ID)
							if role != roleNone {
								// Grant to owner, or to collaborator if not owner-only.
								if role == roleOwner || !ownerOnlyOperation(req.Method, verb, isExact) {
									identity := ServerIdentity{Cluster: cl, Namespace: ns, Name: name, UID: string(obj.GetUID())}
									req = req.WithContext(context.WithValue(req.Context(), serverIdentityKey{}, identity))
									next.ServeHTTP(w, req)
									return
								}
							}
						}
					}
				}
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			// Owner-only server operations need the server's owner or an
			// admin ("*" in the resolved cluster and namespace), even when
			// the caller holds the route's namespace permission.
			if Namespaced(r.perm) && !u.Can("*", true, cl, ns) {
				if name, verb, isExact, ok := parseServerPath(req.URL.Path); ok && ownerOnlyOperation(req.Method, verb, isExact) {
					if fetch == nil {
						http.Error(w, "forbidden", http.StatusForbidden)
						return
					}
					obj, err := fetch.GetServer(req.Context(), cl, ns, name)
					if err != nil || obj == nil || ownershipRole(obj, u.ID) != roleOwner {
						http.Error(w, "forbidden", http.StatusForbidden)
						return
					}
					// This grant still depends on owning this exact object, even
					// though the namespace permission check already passed.
					identity := ServerIdentity{Cluster: cl, Namespace: ns, Name: name, UID: string(obj.GetUID())}
					req = req.WithContext(context.WithValue(req.Context(), serverIdentityKey{}, identity))
				}
			}
			next.ServeHTTP(w, req)
		})
	}
}

// rule maps a request shape to the permission required to perform it.
// The first matching rule wins. method "" matches any method; segment ""
// matches any first path segment; prefix/suffix narrow further when a
// first segment isn't specific enough (e.g. /admin/audit vs /admin/config,
// or the write-capable WS /console vs the read-only logs tail).
type rule struct {
	method  string
	segment string
	prefix  string // strings.HasPrefix(path, prefix)
	suffix  string // strings.HasSuffix(path, suffix)
	perm    string // required permission; "" = any authenticated user
}

var rules = []rule{
	// Fleet handlers filter every scope and object themselves, including
	// owner/collaborator-only servers. No collection-wide grant is inferred.
	{method: "GET", segment: "fleet", suffix: "/fleet/servers", perm: ""},
	{method: "GET", segment: "fleet", suffix: "/fleet/backups", perm: ""},
	{method: "GET", segment: "fleet", suffix: "/fleet/schedules", perm: ""},
	{method: "GET", segment: "fleet", suffix: "/fleet/restores", perm: ""},
	{method: "GET", segment: "fleet", suffix: "/fleet/inventory", perm: ""},
	{method: "GET", segment: "fleet", suffix: "/fleet/placements", perm: ""},
	// Own profile: every authenticated user reads /users/me and their own
	// servers (/users/me/servers), and reads/writes/resets their own theme
	// preferences (feature 016). The rest of /users is gated; must precede
	// the segment-wide users rules.
	{method: "GET", segment: "users", suffix: "/users/me", perm: ""},
	{method: "GET", segment: "users", suffix: "/users/me/servers", perm: ""},
	{method: "GET", segment: "users", suffix: "/users/me/preferences", perm: ""},
	{method: "PUT", segment: "users", suffix: "/users/me/preferences", perm: ""},
	{method: "POST", segment: "users", suffix: "/users/me/preferences/reset", perm: ""},
	{method: "GET", segment: "users", perm: "users:read"},
	{segment: "users", perm: "users:manage"},

	// Roles + the permission catalog.
	{method: "GET", segment: "roles", perm: "roles:read"},
	{segment: "roles", perm: "roles:manage"},

	// Admin area. Audit is a read; config splits read vs manage; anything
	// else under /admin defaults to the admin wildcard (fail-closed-ish).
	{method: "GET", segment: "admin", suffix: "/admin/installation", perm: ""},
	{method: "GET", segment: "admin", prefix: "/admin/audit", perm: "audit:read"},
	{method: "GET", segment: "admin", prefix: "/admin/config", perm: "config:read"},
	{segment: "admin", prefix: "/admin/config", perm: "config:manage"},
	// Notification sink test-sends pair with editing the notifications
	// config section, so they share its manage permission.
	{segment: "admin", prefix: "/admin/notifications", perm: "config:manage"},
	// Identity-provider secrets pair with editing the auth config section.
	{segment: "admin", prefix: "/admin/auth", perm: "config:manage"},
	// Mod-registry provider key secrets pair with editing the
	// modRegistries config section.
	{segment: "admin", prefix: "/admin/registries", perm: "config:manage"},
	// Telemetry status and notice (spec 022): reads need config:read, writes
	// config:manage. The notice GET handler itself answers {"pending":false}
	// to callers without config:manage.
	{method: "GET", segment: "admin", prefix: "/admin/telemetry", perm: "config:read"},
	{segment: "admin", prefix: "/admin/telemetry", perm: "config:manage"},
	{segment: "admin", perm: "*"},

	// Network capture (feature 003-network-capture-sidecar): admin-only via
	// captures:manage. MUST precede the servers:read rule (below) and the
	// servers:write catch-all (line 184) — firstSegment makes every
	// /servers/{name}:verb path match segment "servers" same as ordinary
	// server CRUD, so an unordered insertion here falls through to
	// servers:write, which the operator role already holds. A reordering
	// that moves these rules after the servers catch-all silently grants
	// capture access to the operator role, defeating FR-005/SC-005.
	{method: "POST", segment: "servers", suffix: ":capture-enable", perm: "captures:manage"},
	{method: "POST", segment: "servers", suffix: ":capture-disable", perm: "captures:manage"},
	{method: "POST", segment: "servers", suffix: ":capture-start", perm: "captures:manage"},
	{method: "POST", segment: "servers", suffix: ":capture-stop", perm: "captures:manage"},
	{method: "GET", segment: "servers", suffix: ":captures", perm: "captures:manage"},
	{method: "GET", segment: "servers", suffix: ":capture-file", perm: "captures:manage"},
	{method: "GET", segment: "servers", suffix: ":capture", perm: "captures:manage"},
	{method: "DELETE", segment: "servers", suffix: ":capture", perm: "captures:manage"},

	// Namespaced game resources. Reads vs writes; the catch-all GET below
	// never fires for these because the explicit read rule matches first.
	{method: "GET", segment: "servers", perm: "servers:read"},
	{segment: "servers", perm: "servers:write"},
	{method: "GET", segment: "templates", perm: "templates:read"},
	{segment: "templates", perm: "templates:write"},
	{method: "GET", segment: "backups", perm: "backups:read"},
	{segment: "backups", perm: "backups:write"},
	{method: "GET", segment: "schedules", perm: "schedules:read"},
	{segment: "schedules", perm: "schedules:write"},
	{method: "GET", segment: "restores", perm: "backups:read"},
	{segment: "restores", perm: "backups:restore"},

	// Backup destinations are restic-repo Secrets: reads expose only the
	// (already-public) URL + a hasPassword bool; writes touch credentials.
	{method: "GET", segment: "backup-destinations", perm: "destinations:read"},
	{segment: "backup-destinations", perm: "destinations:manage"},

	// Module install/upgrade/uninstall + source management are cluster-wide.
	{method: "GET", segment: "modules", perm: "modules:read"},
	{segment: "modules", perm: "modules:manage"},

	// Cluster reads (nodes, version, storage) vs credential-minting ops.
	{method: "GET", segment: "cluster", perm: "cluster:read"},
	{segment: "cluster", perm: "cluster:manage"},

	// Cluster registration (multi-cluster): list/create/delete remote clusters
	// (plural /clusters endpoint).
	// Discovery filters each registration by the caller's grants. Namespace
	// server readers may select their cluster without gaining node inventory.
	{method: "GET", segment: "clusters", perm: ""},
	{segment: "clusters", perm: "cluster:manage"},

	// Events SSE is a namespaced, multiplexed read. The route needs
	// servers:read; the handler then streams each kind only to callers who
	// hold that kind's own read permission (see ReadPermission).
	{method: "GET", segment: "events", perm: "servers:read"},

	// WebSocket console runs RCON/PTY commands (write-capable, despite the
	// GET upgrade); the logs tail is read-only.
	{method: "GET", segment: "ws", suffix: "/console", perm: "servers:console"},
	{method: "GET", segment: "ws", suffix: "/console-pty", perm: "servers:console"},
	{method: "GET", segment: "ws", perm: "servers:read"},

	// Any other authenticated read of an unlisted path.
	{method: "GET", segment: "", perm: ""},
	{method: "HEAD", segment: "", perm: ""},
}

// firstSegment returns the first path segment, stripping any trailing
// verb suffix ("/servers/foo:start" → "servers").
func firstSegment(path string) string {
	trimmed := strings.TrimPrefix(path, "/")
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		return trimmed[:i]
	}
	return trimmed
}

// match returns the first rule that applies to (method, path).
func match(method, path string) (rule, bool) {
	seg := firstSegment(path)
	for _, r := range rules {
		if r.method != "" && r.method != method {
			continue
		}
		if r.segment != "" && r.segment != seg {
			continue
		}
		if r.prefix != "" && !strings.HasPrefix(path, r.prefix) {
			continue
		}
		if r.suffix != "" && !strings.HasSuffix(path, r.suffix) {
			continue
		}
		return r, true
	}
	return rule{}, false
}

// serverNameFromPath extracts the GameServer name from a path like
// /servers/{name}, /servers/{name}:verb, /servers/{name}/..., or
// /ws/servers/{name}/... Empty name ⇒ false (list endpoints have no name).
func serverNameFromPath(path string) (string, bool) {
	name, _, _, ok := parseServerPath(path)
	return name, ok
}

// parseServerPath extracts the server name, verb, and whether the path targets
// the exact server object from a path like /servers/{name}, /servers/{name}:verb,
// /servers/{name}/..., or /ws/servers/{name}/...
// Returns (name, verb, isExact, ok) where:
//   - verb is "" if no verb is present
//   - isExact is true when the path is exactly /servers/{name} or /ws/servers/{name}
//   - ok is false if the path has unexpected trailing segments after a verb
func parseServerPath(path string) (string, string, bool, bool) {
	// Normalize to remove leading /
	trimmed := strings.TrimPrefix(path, "/")
	// Handle /ws/servers/... → servers/...
	trimmed = strings.TrimPrefix(trimmed, "ws/")
	// Must start with servers/
	if !strings.HasPrefix(trimmed, "servers/") {
		return "", "", false, false
	}
	rest := strings.TrimPrefix(trimmed, "servers/")
	// Empty rest means /servers (list endpoint)
	if rest == "" {
		return "", "", false, false
	}

	// Find the end of the server name (before / or :)
	nameEndIdx := strings.IndexAny(rest, "/:")
	if nameEndIdx < 0 {
		// No / or :, the rest is the name (exact server object path)
		return rest, "", true, true
	}

	name := rest[:nameEndIdx]
	if name == "" {
		return "", "", false, false
	}

	// Check what follows the name
	if rest[nameEndIdx] == ':' {
		// Extract the verb (everything between : and the next /)
		verbStart := nameEndIdx + 1
		verbEndIdx := strings.IndexByte(rest[verbStart:], '/')
		if verbEndIdx < 0 {
			// No trailing /, the rest is the verb
			verb := rest[verbStart:]
			if verb == "" {
				return "", "", false, false
			}
			return name, verb, false, true
		}
		// Trailing / after verb means trailing segments after verb (invalid, fail closed)
		return "", "", false, false
	}
	// rest[nameEndIdx] == '/', trailing segments without verb (sub-resource path, e.g. /servers/a/files)
	return name, "", false, true
}

// ownerOnlyOperation reports whether a server request is one of the
// owner-only operations: ownership transfer, collaborator edits, data
// wipe, or DELETE of the server object itself.
func ownerOnlyOperation(method, verb string, isExact bool) bool {
	return verb == "transfer" || verb == "collaborators" || verb == "wipe-data" ||
		(method == http.MethodDelete && isExact)
}

// ownershipRole returns the ownership role of a user in the server:
// owner (annotation gameplane.local/owner-id matches), collaborator
// (user ID in comma-separated gameplane.local/collaborators), or none.
func ownershipRole(obj *unstructured.Unstructured, userID int64) int {
	ann := obj.GetAnnotations()
	if ann == nil {
		return roleNone
	}
	// Check owner
	ownerIDStr := ann["gameplane.local/owner-id"]
	if ownerID, ok := parseUserID(ownerIDStr); ok && ownerID == userID {
		return roleOwner
	}
	// Check collaborators (comma-separated IDs)
	collabsStr := ann["gameplane.local/collaborators"]
	if collabsStr != "" {
		for _, id := range strings.Split(collabsStr, ",") {
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if collabID, ok := parseUserID(id); ok && collabID == userID {
				return roleCollaborator
			}
		}
	}
	return roleNone
}

// parseUserID parses a string as int64 for ownership checks.
func parseUserID(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// allow is the pure authorization check, factored out for testing. cluster
// is the resolved target cluster; ns is the already-resolved target namespace
// (ignored for cluster-scoped permissions).
func allow(u *auth.User, method, path, cluster, ns string) bool {
	r, ok := match(method, path)
	if !ok {
		return false
	}
	if r.perm == "" {
		return true
	}
	if clusterInventoryRead(method, path) {
		// Empty namespace allows only cluster-wide grants on this cluster
		// or the wildcard cluster, never grants scoped to a game namespace.
		return u.Can(r.perm, true, cluster, "")
	}
	return u.Can(r.perm, Namespaced(r.perm), cluster, ns)
}

func clusterInventoryRead(method, path string) bool {
	return method == http.MethodGet && (path == "/cluster" || path == "/cluster/info" || path == "/cluster/stats")
}

// ReadPermission returns the permission the rule table requires for a GET
// of /<segment> (for example "servers" → "servers:read", "restores" →
// "backups:read"). ok is false when no explicit rule covers the segment;
// the catch-all rules do not count. The events stream uses it to send each
// resource kind only to callers who may read that kind.
func ReadPermission(segment string) (perm string, ok bool) {
	r, ok := match(http.MethodGet, "/"+segment)
	if !ok || r.segment == "" {
		return "", false
	}
	return r.perm, true
}
