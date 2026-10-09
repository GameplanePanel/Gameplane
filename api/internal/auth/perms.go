package auth

import (
	"context"
	"fmt"
	"sort"

	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// LoadPerms resolves the user's effective permission set from their role
// bindings, three-level map: cluster → namespace → permission set.
// It joins each binding's role to its permission rows, so a permission
// edit takes effect on the user's next request without re-issuing their
// session.
func (s *SessionStore) LoadPerms(ctx context.Context, userID int64) (map[string]map[string]map[string]struct{}, error) {
	rows, err := s.db.DB.QueryContext(ctx, `
		SELECT b.cluster, b.namespace, rp.permission
		FROM user_role_bindings b
		JOIN role_permissions rp ON rp.role_name = b.role_name
		WHERE b.user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("load permissions for user %d: %w", userID, err)
	}
	defer func() { _ = rows.Close() }()

	perms := map[string]map[string]map[string]struct{}{}
	for rows.Next() {
		var cluster, ns, perm string
		if err := rows.Scan(&cluster, &ns, &perm); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		if perms[cluster] == nil {
			perms[cluster] = map[string]map[string]struct{}{}
		}
		if perms[cluster][ns] == nil {
			perms[cluster][ns] = map[string]struct{}{}
		}
		perms[cluster][ns][perm] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate permissions: %w", err)
	}
	return perms, nil
}

// PermsToJSON flattens the in-memory three-level permission set into a
// JSON-friendly namespace→permissions map. It merges permissions across
// all clusters, so the frontend sees the union of what the user can do
// (permissions are cluster-agnostic for most features). It is the single
// source of truth for the `permissions` field on both /users/me and the
// /auth/login response. Returns nil for an empty set so callers can omitempty.
func PermsToJSON(perms map[string]map[string]map[string]struct{}) map[string][]string {
	if len(perms) == 0 {
		return nil
	}
	merged := make(map[string]map[string]struct{})
	for _, clusterPerms := range perms {
		for ns, permSet := range clusterPerms {
			if merged[ns] == nil {
				merged[ns] = make(map[string]struct{})
			}
			for p := range permSet {
				merged[ns][p] = struct{}{}
			}
		}
	}
	out := make(map[string][]string, len(merged))
	for ns, set := range merged {
		keys := make([]string, 0, len(set))
		for p := range set {
			keys = append(keys, p)
		}
		sort.Strings(keys)
		out[ns] = keys
	}
	return out
}

// PermsByClusterToJSON preserves the cluster dimension of the permission set,
// returning cluster→namespace→permissions. Unlike PermsToJSON (which merges
// across clusters for backward compatibility), this structure allows the
// frontend to exactly mirror the server's User.Can() logic and determine
// cluster-specific access without triggering 403 errors. It returns nil for
// an empty set so callers can omitempty.
func PermsByClusterToJSON(perms map[string]map[string]map[string]struct{}) map[string]map[string][]string {
	if len(perms) == 0 {
		return nil
	}
	out := make(map[string]map[string][]string, len(perms))
	for cluster, clusterPerms := range perms {
		out[cluster] = make(map[string][]string, len(clusterPerms))
		for ns, permSet := range clusterPerms {
			keys := make([]string, 0, len(permSet))
			for p := range permSet {
				keys = append(keys, p)
			}
			sort.Strings(keys)
			out[cluster][ns] = keys
		}
	}
	return out
}

// Can reports whether the user holds perm. namespaced indicates whether the
// permission is scoped to a namespace+cluster (servers, backups, …) or is
// cluster-scoped control-plane state (users, roles, config, …).
//
//   - Cluster-scoped perms are NOT partitioned per target cluster: a
//     cluster-wide (namespace "*") binding in ANY cluster grants them. This is
//     why a backfilled admin (bound only on "local") keeps users:manage after
//     a second cluster is registered.
//   - Namespaced perms are gated by the request's target cluster (or the "*"
//     wildcard cluster): a cluster-wide binding on that cluster, or a binding
//     in the exact (cluster, namespace), grants them. A binding on one cluster
//     never confers the permission on another — that prevents cross-cluster
//     privilege escalation.
//   - The "*" permission wildcard (the built-in admin role) matches any perm
//     but is still subject to the same cluster gating for namespaced perms.
//   - Inventory, modules and templates always require a cluster-wide grant on the
//     selected cluster or wildcard cluster, regardless of namespaced. An
//     omitted cluster selects the home cluster; namespace grants never qualify.
func (u *User) Can(perm string, namespaced bool, cluster, ns string) bool {
	if u == nil {
		return false
	}
	// cwHolds: does the user hold perm cluster-wide (namespace "*") on cluster ck?
	cwHolds := func(ck string) bool {
		return permSetHas(u.Perms[ck]["*"], "*") || permSetHas(u.Perms[ck]["*"], perm)
	}
	if perm == "cluster:read" || perm == "modules:read" || perm == "modules:manage" || perm == "templates:read" || perm == "templates:write" {
		if cluster == "" {
			cluster = scope.DefaultCluster
		}
		return cwHolds(cluster) || cwHolds("*")
	}
	if !namespaced {
		// Control-plane perm: any cluster's cluster-wide binding grants it.
		for ck := range u.Perms {
			if cwHolds(ck) {
				return true
			}
		}
		return false
	}
	// Namespaced perm: gated by the target cluster or the "*" wildcard cluster.
	for _, ck := range []string{cluster, "*"} {
		if cwHolds(ck) {
			return true
		}
		if ns != "" && ns != "*" {
			if permSetHas(u.Perms[ck][ns], "*") || permSetHas(u.Perms[ck][ns], perm) {
				return true
			}
		}
	}
	return false
}

// CanDiscoverCluster permits selecting a cluster without granting node inventory
// access. A server reader in one namespace still needs to find that cluster.
func (u *User) CanDiscoverCluster(cluster string) bool {
	if u == nil {
		return false
	}
	// Existing control-plane administrators need assignment/registration
	// targets before a target-cluster grant exists. This is metadata only.
	if u.Can("users:manage", false, "", "") || u.Can("cluster:manage", false, "", "") {
		return true
	}
	if u.Can("cluster:read", true, cluster, "") || u.Can("modules:read", true, cluster, "") || u.Can("modules:manage", true, cluster, "") {
		return true
	}
	for _, ck := range []string{cluster, "*"} {
		for _, perms := range u.Perms[ck] {
			if permSetHas(perms, "*") || permSetHas(perms, "servers:read") {
				return true
			}
		}
	}
	return false
}

// permSetHas is nil-safe: indexing a nil map yields ok=false.
func permSetHas(set map[string]struct{}, key string) bool {
	_, ok := set[key]
	return ok
}
