package rbac

import (
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// TestCatalog_ServersSecurityPermission pins the servers:security catalog
// entry: it exists, it is namespaced, the built-in admin role holds it
// (through "*"), and no other built-in role holds it. The privileged-grant
// guard (privilegedPermissions in handlers/privileged_grants.go) is
// unexported in another package and is not checked here.
func TestCatalog_ServersSecurityPermission(t *testing.T) {
	const perm = "servers:security"

	if !ValidPermission(perm) {
		t.Fatalf("ValidPermission(%q) = false, want true", perm)
	}
	if !Namespaced(perm) {
		t.Fatalf("Namespaced(%q) = false, want true", perm)
	}

	store := migratedStore(t)
	builtin := []string{RoleAdmin, RoleOperator, RoleViewer}
	for _, role := range builtin {
		u := userForRole(t, store, role)
		got := u.Can(perm, true, scope.DefaultCluster, scope.DefaultNamespace)
		want := role == RoleAdmin
		if got != want {
			t.Errorf("role %q: Can(%q) = %v, want %v", role, perm, got, want)
		}
		if _, seeded := loadRolePerms(t, store, role)[perm]; seeded && role != RoleAdmin {
			t.Errorf("role %q: seeded permission %q, want it held only by admin", role, perm)
		}
	}
}
