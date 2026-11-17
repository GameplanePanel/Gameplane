package rbac

// Permission is one entry in the fixed, server-defined catalog. Custom
// roles may be granted any subset of these; the "*" wildcard is reserved
// for the built-in admin role and is never grantable through the API.
type Permission struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Namespaced bool   `json:"namespaced"`
}

// PermGroup groups permissions by the resource they act on, for the
// dashboard's permission picker.
type PermGroup struct {
	Resource    string       `json:"resource"`
	Label       string       `json:"label"`
	Permissions []Permission `json:"permissions"`
}

// Catalog is the single source of truth for which permissions exist.
// The RBAC rule table (rbac.go) and the seeded built-in roles
// (migrations/sqlite/003_roles.sql and its postgres/ twin) must only reference keys that appear here;
// the cross-checks in the tests enforce that.
var Catalog = []PermGroup{
	{Resource: "servers", Label: "Game servers", Permissions: []Permission{
		{Key: "servers:read", Label: "View servers, logs, players, and files", Namespaced: true},
		{Key: "servers:write", Label: "Create, edit, and control servers (owner-only actions need the server's owner or an admin)", Namespaced: true},
		{Key: "servers:console", Label: "Use the live console (RCON / PTY)", Namespaced: true},
		{Key: "servers:security", Label: "Set or change a server's security overrides (run-as user and group, filesystem group, volume ownership check)", Namespaced: true},
	}},
	{Resource: "backups", Label: "Backups", Permissions: []Permission{
		{Key: "backups:read", Label: "View backups and restores", Namespaced: true},
		{Key: "backups:write", Label: "Create and delete backups", Namespaced: true},
		{Key: "backups:restore", Label: "Restore from a backup", Namespaced: true},
	}},
	{Resource: "schedules", Label: "Backup schedules", Permissions: []Permission{
		{Key: "schedules:read", Label: "View schedules", Namespaced: true},
		{Key: "schedules:write", Label: "Create, edit, and delete schedules", Namespaced: true},
	}},
	{Resource: "templates", Label: "Game templates", Permissions: []Permission{
		{Key: "templates:read", Label: "View templates", Namespaced: false},
		{Key: "templates:write", Label: "Create, edit, and delete templates", Namespaced: false},
	}},
	{Resource: "modules", Label: "Modules", Permissions: []Permission{
		{Key: "modules:read", Label: "View the module catalog and sources", Namespaced: false},
		{Key: "modules:manage", Label: "Install, upgrade, and uninstall modules and sources", Namespaced: false},
	}},
	{Resource: "destinations", Label: "Backup destinations", Permissions: []Permission{
		{Key: "destinations:read", Label: "View backup destinations", Namespaced: true},
		{Key: "destinations:manage", Label: "Create, edit, and delete backup destinations", Namespaced: true},
	}},
	{Resource: "cluster", Label: "Cluster", Permissions: []Permission{
		{Key: "cluster:read", Label: "View nodes, version, and storage", Namespaced: false},
		{Key: "cluster:manage", Label: "Add nodes and mint kubeconfig", Namespaced: false},
	}},
	{Resource: "users", Label: "Users", Permissions: []Permission{
		{Key: "users:read", Label: "View users", Namespaced: false},
		{Key: "users:manage", Label: "Create, edit, and delete users and their role bindings", Namespaced: false},
	}},
	{Resource: "roles", Label: "Roles", Permissions: []Permission{
		{Key: "roles:read", Label: "View roles and the permission catalog", Namespaced: false},
		{Key: "roles:manage", Label: "Create, edit, and delete roles", Namespaced: false},
	}},
	{Resource: "audit", Label: "Audit log", Permissions: []Permission{
		{Key: "audit:read", Label: "View the audit log", Namespaced: false},
	}},
	{Resource: "config", Label: "Global settings", Permissions: []Permission{
		{Key: "config:read", Label: "View global settings", Namespaced: false},
		{Key: "config:manage", Label: "Change global settings", Namespaced: false},
	}},
	{Resource: "captures", Label: "Network captures", Permissions: []Permission{
		{Key: "captures:manage", Label: "Enable, start, stop, download, and delete packet captures", Namespaced: true},
	}},
}

// permIndex maps every catalog permission key to its definition.
var permIndex = func() map[string]Permission {
	m := make(map[string]Permission)
	for _, g := range Catalog {
		for _, p := range g.Permissions {
			m[p.Key] = p
		}
	}
	return m
}()

// ValidPermission reports whether key is a real catalog permission. The
// "*" wildcard is intentionally NOT valid — it is reserved for the
// built-in admin role and must never be grantable via the API.
func ValidPermission(key string) bool {
	_, ok := permIndex[key]
	return ok
}

// Namespaced reports whether a permission is scoped to a namespace.
// Unknown keys (and "*") are treated as cluster-scoped — the safe
// default, since cluster-scoped permissions require a cluster-wide
// binding.
func Namespaced(key string) bool {
	return permIndex[key].Namespaced
}
