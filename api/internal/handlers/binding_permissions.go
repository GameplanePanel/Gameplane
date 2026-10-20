package handlers

import (
	"context"
	"fmt"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

// remoteWidePermissionsAllowed keeps supplemental remote grants away from
// control-plane administration. Can always scopes cluster:read to the selected
// cluster. Other Can(false) permissions accept a global grant from any cluster,
// so only target-scoped inventory, modules, templates and catalogued namespaced permissions are safe here;
// '*' is never allowed.
func remoteWidePermissionsAllowed(permissions []string) bool {
	for _, permission := range permissions {
		if permission != "cluster:read" && permission != "modules:read" && permission != "modules:manage" && permission != "templates:read" && permission != "templates:write" && !rbac.Namespaced(permission) {
			return false
		}
	}
	return true
}

// Callers hold LockUserManagement through this read and their binding write.
func remoteWideRoleAllowed(ctx context.Context, store *db.Store, role string) (bool, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT permission FROM role_permissions WHERE role_name = ?`, role)
	if err != nil {
		return false, fmt.Errorf("load remote role permissions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var permissions []string
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return false, fmt.Errorf("scan remote role permission: %w", err)
		}
		permissions = append(permissions, permission)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate remote role permissions: %w", err)
	}
	return remoteWidePermissionsAllowed(permissions), nil
}
