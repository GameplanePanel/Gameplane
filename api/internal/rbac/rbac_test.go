package rbac

import (
	"context"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// userForRole builds an authenticated User whose cluster-wide ("*")
// binding carries exactly the seeded permissions of the named built-in
// role. Loading from a freshly-migrated DB makes the test a proof that
// the SQL seed reproduces the historical matrix below — not a restatement
// of it.
func userForRole(t *testing.T, store *db.Store, role string) *auth.User {
	t.Helper()
	return &auth.User{Role: role, Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {
			"*": loadRolePerms(t, store, role),
		},
	}}
}

func loadRolePerms(t *testing.T, store *db.Store, role string) map[string]struct{} {
	t.Helper()
	rows, err := store.DB.QueryContext(t.Context(), `SELECT permission FROM role_permissions WHERE role_name = ?`, role)
	if err != nil {
		t.Fatalf("load perms for %q: %v", role, err)
	}
	defer rows.Close()
	set := map[string]struct{}{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatalf("scan: %v", err)
		}
		set[p] = struct{}{}
	}
	return set
}

func migratedStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func TestAllow(t *testing.T) {
	store := migratedStore(t)
	users := map[string]*auth.User{
		RoleViewer:   userForRole(t, store, RoleViewer),
		RoleOperator: userForRole(t, store, RoleOperator),
		RoleAdmin:    userForRole(t, store, RoleAdmin),
	}
	// Cluster-wide bindings ignore ns; use the default for every case.
	const ns = scope.DefaultNamespace

	cases := []struct {
		role, method, path, cluster string
		want                        bool
	}{
		{RoleViewer, "GET", "/servers", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/servers", scope.DefaultCluster, false},
		{RoleViewer, "POST", "/servers/foo/files/create", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/servers/foo/files/create", scope.DefaultCluster, true},
		{RoleOperator, "POST", "/servers", scope.DefaultCluster, true},
		{RoleOperator, "DELETE", "/users/1", scope.DefaultCluster, false},
		{RoleAdmin, "DELETE", "/users/1", scope.DefaultCluster, true},
		// Own profile: every authenticated role reads /users/me; the rest
		// of /users stays admin-only, and /users/me stays read-only.
		{RoleViewer, "GET", "/users/me", scope.DefaultCluster, true},
		{RoleOperator, "GET", "/users/me", scope.DefaultCluster, true},
		{RoleAdmin, "GET", "/users/me", scope.DefaultCluster, true},
		{RoleViewer, "GET", "/users", scope.DefaultCluster, false},
		{RoleOperator, "GET", "/users", scope.DefaultCluster, false},
		{RoleAdmin, "GET", "/users", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/users/me", scope.DefaultCluster, false},
		{RoleViewer, "PUT", "/users/me", scope.DefaultCluster, false},
		// Own theme preferences (feature 016): every authenticated role
		// reads, writes and resets its own preferences.
		{RoleViewer, "GET", "/users/me/preferences", scope.DefaultCluster, true},
		{RoleViewer, "PUT", "/users/me/preferences", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/users/me/preferences/reset", scope.DefaultCluster, true},
		{RoleOperator, "PUT", "/users/me/preferences", scope.DefaultCluster, true},
		{RoleViewer, "DELETE", "/users/me/preferences", scope.DefaultCluster, false},
		{RoleOperator, "GET", "/admin/audit", scope.DefaultCluster, false},
		{RoleAdmin, "GET", "/admin/audit", scope.DefaultCluster, true},
		{RoleViewer, "PATCH", "/servers/foo", scope.DefaultCluster, false},
		// Prefix-confusion regression: /serverz must not inherit /servers.
		{RoleAdmin, "POST", "/serverz", scope.DefaultCluster, false},
		// Verb-suffix paths: /servers/foo:start resolves to segment "servers".
		{RoleOperator, "POST", "/servers/foo:start", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/servers/foo:start", scope.DefaultCluster, false},
		// WebSocket console forwards to RCON.Exec — viewers denied even
		// though the upgrade handshake is a GET.
		{RoleViewer, "GET", "/ws/servers/foo/console", scope.DefaultCluster, false},
		{RoleOperator, "GET", "/ws/servers/foo/console", scope.DefaultCluster, true},
		{RoleAdmin, "GET", "/ws/servers/foo/console", scope.DefaultCluster, true},
		// WS logs tail is read-only → viewer ok.
		{RoleViewer, "GET", "/ws/servers/foo/logs", scope.DefaultCluster, true},
		{RoleOperator, "GET", "/ws/servers/foo/logs", scope.DefaultCluster, true},
		// PTY console attaches stdin → operator+, like RCON console.
		{RoleViewer, "GET", "/ws/servers/foo/console-pty", scope.DefaultCluster, false},
		{RoleOperator, "GET", "/ws/servers/foo/console-pty", scope.DefaultCluster, true},
		{RoleAdmin, "GET", "/ws/servers/foo/console-pty", scope.DefaultCluster, true},
		// Guard against the console-suffix rule triggering on a spoofed
		// resource path "/servers/foo/console" (segment "servers", not "ws").
		{RoleViewer, "GET", "/servers/foo/console", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/servers/foo/console", scope.DefaultCluster, false},
		// Player moderation are POSTs under /servers → operator+; banlist read.
		{RoleViewer, "POST", "/servers/foo/players/kick", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/servers/foo/players/kick", scope.DefaultCluster, true},
		{RoleAdmin, "POST", "/servers/foo/players/kick", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/servers/foo/players/ban", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/servers/foo/players/ban", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/servers/foo/players/unban", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/servers/foo/players/unban", scope.DefaultCluster, true},
		{RoleViewer, "GET", "/servers/foo/players/banned", scope.DefaultCluster, true},
		{RoleViewer, "GET", "/servers/foo/players", scope.DefaultCluster, true},
		// Module-declared actions run console commands → operator+. Status read.
		{RoleViewer, "POST", "/servers/foo/actions/run", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/servers/foo/actions/run", scope.DefaultCluster, true},
		{RoleAdmin, "POST", "/servers/foo/actions/run", scope.DefaultCluster, true},
		{RoleViewer, "GET", "/servers/foo/status", scope.DefaultCluster, true},
		{RoleOperator, "GET", "/servers/foo/status", scope.DefaultCluster, true},
		// Mods: listing viewer+, install/remove operator+.
		{RoleViewer, "GET", "/servers/foo/mods", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/servers/foo/mods/install", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/servers/foo/mods/install", scope.DefaultCluster, true},
		{RoleViewer, "DELETE", "/servers/foo/mods", scope.DefaultCluster, false},
		{RoleOperator, "DELETE", "/servers/foo/mods", scope.DefaultCluster, true},
		// ID-managed mods (ARK/Zomboid-style): same read/write split as the
		// file-based mods surface above — GET viewer+, the bulk-replace
		// PUT operator+ (it's a mod-management write, not a read).
		{RoleViewer, "GET", "/servers/foo/mods/ids", scope.DefaultCluster, true},
		{RoleViewer, "PUT", "/servers/foo/mods/ids", scope.DefaultCluster, false},
		{RoleOperator, "PUT", "/servers/foo/mods/ids", scope.DefaultCluster, true},
		{RoleAdmin, "PUT", "/servers/foo/mods/ids", scope.DefaultCluster, true},
		// Module + source management admin-only; reads viewer+.
		{RoleViewer, "GET", "/modules/catalog", scope.DefaultCluster, true},
		{RoleViewer, "GET", "/modules/sources", scope.DefaultCluster, true},
		{RoleOperator, "POST", "/modules", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/modules", scope.DefaultCluster, true},
		{RoleOperator, "POST", "/modules/sources", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/modules/sources", scope.DefaultCluster, true},
		{RoleOperator, "PUT", "/modules/sources/upstream", scope.DefaultCluster, false},
		{RoleAdmin, "PUT", "/modules/sources/upstream", scope.DefaultCluster, true},
		{RoleOperator, "DELETE", "/modules/sources/upstream", scope.DefaultCluster, false},
		{RoleAdmin, "DELETE", "/modules/sources/upstream", scope.DefaultCluster, true},
		// Backup destinations: read viewer+, write admin-only.
		{RoleViewer, "GET", "/backup-destinations", scope.DefaultCluster, true},
		{RoleOperator, "POST", "/backup-destinations", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/backup-destinations", scope.DefaultCluster, true},
		// Cluster reads viewer+; credential-minting POSTs admin-only.
		{RoleViewer, "GET", "/cluster", scope.DefaultCluster, true},
		{RoleViewer, "GET", "/cluster/stats", scope.DefaultCluster, true},
		{RoleOperator, "POST", "/cluster/nodes:join", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/cluster/nodes:join", scope.DefaultCluster, true},
		{RoleAdmin, "POST", "/cluster/kubeconfig", scope.DefaultCluster, true},
		// Config: admin-only, both read and write.
		{RoleViewer, "GET", "/admin/config", scope.DefaultCluster, false},
		{RoleAdmin, "GET", "/admin/config", scope.DefaultCluster, true},
		{RoleOperator, "PUT", "/admin/config/general", scope.DefaultCluster, false},
		{RoleAdmin, "PUT", "/admin/config/general", scope.DefaultCluster, true},
		// Notification test-sends share the config manage permission.
		{RoleViewer, "POST", "/admin/notifications/sinks/x/test", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/admin/notifications/sinks/x/test", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/admin/notifications/sinks/x/test", scope.DefaultCluster, true},
		// Mod-registry provider key secrets share the config manage permission.
		{RoleViewer, "PUT", "/admin/registries/curseforge/secret", scope.DefaultCluster, false},
		{RoleOperator, "PUT", "/admin/registries/curseforge/secret", scope.DefaultCluster, false},
		{RoleAdmin, "PUT", "/admin/registries/curseforge/secret", scope.DefaultCluster, true},
		{RoleAdmin, "DELETE", "/admin/registries/curseforge/secret", scope.DefaultCluster, true},
		// Telemetry notice (spec 022): reads need config:read, writes config:manage.
		{RoleViewer, "GET", "/admin/telemetry/notice", scope.DefaultCluster, false},
		{RoleAdmin, "GET", "/admin/telemetry/notice", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/admin/telemetry/notice", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/admin/telemetry/notice", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/admin/telemetry/notice", scope.DefaultCluster, true},
		// Telemetry status and install-ID reset (spec 022, prefix entries in rbac.go).
		{RoleViewer, "GET", "/admin/telemetry", scope.DefaultCluster, false},
		{RoleAdmin, "GET", "/admin/telemetry", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/admin/telemetry/install-id", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/admin/telemetry/install-id", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/admin/telemetry/install-id", scope.DefaultCluster, true},
		// Restores: read viewer+, create operator+ (backups:restore).
		{RoleViewer, "GET", "/restores", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/restores", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/restores", scope.DefaultCluster, true},
		{RoleAdmin, "POST", "/restores", scope.DefaultCluster, true},
		// Events SSE is a namespaced read.
		{RoleViewer, "GET", "/events", scope.DefaultCluster, true},
		// Roles: read for all built-ins; manage admin-only.
		{RoleViewer, "GET", "/roles", scope.DefaultCluster, true},
		{RoleOperator, "GET", "/roles/permissions", scope.DefaultCluster, true},
		{RoleOperator, "POST", "/roles", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/roles", scope.DefaultCluster, true},
		// Network captures: admin-only via captures:manage. Operator must be
		// denied on all 8 routes to prevent silent privilege escalation via the
		// servers:write catch-all if these rules are reordered.
		{RoleViewer, "POST", "/servers/foo:capture-enable", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/servers/foo:capture-enable", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/servers/foo:capture-enable", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/servers/foo:capture-disable", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/servers/foo:capture-disable", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/servers/foo:capture-disable", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/servers/foo:capture-start", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/servers/foo:capture-start", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/servers/foo:capture-start", scope.DefaultCluster, true},
		{RoleViewer, "POST", "/servers/foo:capture-stop", scope.DefaultCluster, false},
		{RoleOperator, "POST", "/servers/foo:capture-stop", scope.DefaultCluster, false},
		{RoleAdmin, "POST", "/servers/foo:capture-stop", scope.DefaultCluster, true},
		{RoleViewer, "GET", "/servers/foo:captures", scope.DefaultCluster, false},
		{RoleOperator, "GET", "/servers/foo:captures", scope.DefaultCluster, false},
		{RoleAdmin, "GET", "/servers/foo:captures", scope.DefaultCluster, true},
		{RoleViewer, "GET", "/servers/foo:capture-file", scope.DefaultCluster, false},
		{RoleOperator, "GET", "/servers/foo:capture-file", scope.DefaultCluster, false},
		{RoleAdmin, "GET", "/servers/foo:capture-file", scope.DefaultCluster, true},
		{RoleViewer, "GET", "/servers/foo:capture", scope.DefaultCluster, false},
		{RoleOperator, "GET", "/servers/foo:capture", scope.DefaultCluster, false},
		{RoleAdmin, "GET", "/servers/foo:capture", scope.DefaultCluster, true},
		{RoleViewer, "DELETE", "/servers/foo:capture", scope.DefaultCluster, false},
		{RoleOperator, "DELETE", "/servers/foo:capture", scope.DefaultCluster, false},
		{RoleAdmin, "DELETE", "/servers/foo:capture", scope.DefaultCluster, true},
	}
	for _, tc := range cases {
		got := allow(users[tc.role], tc.method, tc.path, tc.cluster, ns)
		if got != tc.want {
			t.Errorf("allow(%s,%s,%s,%s) = %v, want %v", tc.role, tc.method, tc.path, tc.cluster, got, tc.want)
		}
	}
}

// TestAllow_PerNamespace proves that a role bound to a single namespace
// authorizes namespaced actions only there, and never confers
// cluster-scoped authority.
func TestAllow_PerNamespace(t *testing.T) {
	store := migratedStore(t)
	opPerms := loadRolePerms(t, store, RoleOperator)

	// Operator in "team-a" only — no cluster-wide binding.
	opInTeamA := &auth.User{Role: RoleOperator, Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {
			"team-a": opPerms,
		},
	}}
	// Admin in "team-a" only — namespaced wildcard, but not cluster-wide.
	adminInTeamA := &auth.User{Role: RoleAdmin, Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {
			"team-a": {"*": {}},
		},
	}}

	cases := []struct {
		name                      string
		u                         *auth.User
		method, path, cluster, ns string
		want                      bool
	}{
		{"op writes servers in its ns", opInTeamA, "POST", "/servers", scope.DefaultCluster, "team-a", true},
		{"op denied servers in other ns", opInTeamA, "POST", "/servers", scope.DefaultCluster, scope.DefaultNamespace, false},
		{"op denied servers read in other ns", opInTeamA, "GET", "/servers", scope.DefaultCluster, scope.DefaultNamespace, false},
		{"op reads servers in its ns", opInTeamA, "GET", "/servers", scope.DefaultCluster, "team-a", true},
		// Cluster-scoped action — a namespace binding never grants it.
		{"op denied user mgmt", opInTeamA, "POST", "/users", scope.DefaultCluster, "", false},
		{"ns-admin writes servers in its ns", adminInTeamA, "POST", "/servers", scope.DefaultCluster, "team-a", true},
		{"ns-admin denied servers elsewhere", adminInTeamA, "POST", "/servers", scope.DefaultCluster, scope.DefaultNamespace, false},
		// Namespace wildcard is namespaced-only: cluster authority denied.
		{"ns-admin denied user mgmt", adminInTeamA, "POST", "/users", scope.DefaultCluster, "", false},
		{"ns-admin denied audit", adminInTeamA, "GET", "/admin/audit", scope.DefaultCluster, "", false},
	}
	for _, tc := range cases {
		if got := allow(tc.u, tc.method, tc.path, tc.cluster, tc.ns); got != tc.want {
			t.Errorf("%s: allow=%v want %v", tc.name, got, tc.want)
		}
	}
}

// TestAllow_NilUserDenied is a fail-closed guard.
func TestAllow_NilUserDenied(t *testing.T) {
	if allow(nil, "GET", "/servers", scope.DefaultCluster, scope.DefaultNamespace) {
		t.Error("nil user allowed")
	}
}

// TestCaptures_RuleOrdering verifies that capture routes resolve to
// captures:manage, not servers:write/servers:read, and that the capture
// rules' slice index is structurally less than the servers:write catch-all.
// This is a regression guard for the ordering requirement: if capture rules
// are moved after servers:write, they fall through to that catch-all, which
// the operator role holds, silently granting capture access to operators and
// defeating FR-005/SC-005. This test fails immediately on any reordering,
// without waiting for production traffic or runtime test failures.
func TestCaptures_RuleOrdering(t *testing.T) {
	testCases := []struct {
		method, path string
	}{
		{"POST", "/servers/foo:capture-enable"},
		{"POST", "/servers/foo:capture-disable"},
		{"POST", "/servers/foo:capture-start"},
		{"POST", "/servers/foo:capture-stop"},
		{"GET", "/servers/foo:captures"},
		{"GET", "/servers/foo:capture-file"},
		{"GET", "/servers/foo:capture"},
		{"DELETE", "/servers/foo:capture"},
	}

	for _, tc := range testCases {
		r, ok := match(tc.method, tc.path)
		if !ok {
			t.Errorf("match(%s, %s) = not matched, want captures:manage", tc.method, tc.path)
			continue
		}
		if r.perm != "captures:manage" {
			t.Errorf("match(%s, %s) = %q, want captures:manage (actual: %+v)", tc.method, tc.path, r.perm, r)
		}
	}

	// Verify capture rules come before servers:write catch-all by checking
	// their indices in the rules slice. This is a structural assertion that
	// a future edit moving the capture block fails immediately.
	captureRuleIndex := -1
	serversWriteIndex := -1
	for i, r := range rules {
		if r.segment == "servers" && r.suffix == ":capture-enable" && r.perm == "captures:manage" {
			if captureRuleIndex < 0 {
				captureRuleIndex = i
			}
		}
		if r.segment == "servers" && r.suffix == "" && r.method == "" && r.perm == "servers:write" {
			serversWriteIndex = i
		}
	}
	if captureRuleIndex < 0 {
		t.Error("capture :capture-enable rule not found in rules slice")
	}
	if serversWriteIndex < 0 {
		t.Error("servers:write catch-all rule not found in rules slice")
	}
	if captureRuleIndex >= 0 && serversWriteIndex >= 0 && captureRuleIndex >= serversWriteIndex {
		t.Errorf("capture rules (index %d) must precede servers:write catch-all (index %d); "+
			"a reordering grants capture access to operator role via servers:write fallthrough",
			captureRuleIndex, serversWriteIndex)
	}
}

// TestReadPermission verifies that ReadPermission returns the correct
// permission for each resource kind and fails-closed (ok=false) for
// unlisted kinds (falling back to the catch-all does not count).
func TestReadPermission(t *testing.T) {
	cases := []struct {
		segment  string
		wantPerm string
		wantOk   bool
	}{
		{"servers", "servers:read", true},
		{"templates", "templates:read", true},
		{"backups", "backups:read", true},
		{"schedules", "schedules:read", true},
		{"restores", "backups:read", true},
		{"not-a-kind", "", false},
	}
	for _, tc := range cases {
		perm, ok := ReadPermission(tc.segment)
		if perm != tc.wantPerm || ok != tc.wantOk {
			t.Errorf("ReadPermission(%q) = (%q, %v), want (%q, %v)",
				tc.segment, perm, ok, tc.wantPerm, tc.wantOk)
		}
	}
}
