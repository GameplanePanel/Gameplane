package handlers

import (
	"context"
	"fmt"
	"net/http"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

// privilegedPermissions lead to full administrator access: managing users,
// roles and global settings (which include sign-in providers), minting
// cluster credentials, and the "*" wildcard itself. Only a full
// administrator may grant them, assign a role that carries them, or change
// an account that holds them, so a delegated manager can never raise
// anyone, themselves included, to administrator.
var privilegedPermissions = []string{"*", "users:manage", "roles:manage", "config:manage", "cluster:manage"}

// isPrivilegedPermission reports whether perm is one of privilegedPermissions.
func isPrivilegedPermission(perm string) bool {
	for _, p := range privilegedPermissions {
		if p == perm {
			return true
		}
	}
	return false
}

// isFullAdmin reports whether u holds the "*" wildcard (the built-in admin role).
func isFullAdmin(u *auth.User) bool {
	return u != nil && (u.Role == rbac.RoleAdmin || u.Can("*", false, "", ""))
}

// roleIsPrivileged reports whether role grants a privileged permission.
func roleIsPrivileged(ctx context.Context, store *db.Store, role string) (bool, error) {
	var n int
	if err := store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM role_permissions
		 WHERE role_name = ? AND permission IN ('*', 'users:manage', 'roles:manage', 'config:manage', 'cluster:manage')`,
		role).Scan(&n); err != nil {
		return false, fmt.Errorf("check privileged role %q: %w", role, err)
	}
	return n > 0, nil
}

// userIsPrivileged reports whether any role binding of the user grants a
// privileged permission.
func userIsPrivileged(ctx context.Context, store *db.Store, userID int64) (bool, error) {
	var n int
	if err := store.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_role_bindings b
		 JOIN role_permissions rp ON rp.role_name = b.role_name
		 WHERE b.user_id = ? AND rp.permission IN ('*', 'users:manage', 'roles:manage', 'config:manage', 'cluster:manage')`,
		userID).Scan(&n); err != nil {
		return false, fmt.Errorf("check privileged user %d: %w", userID, err)
	}
	return n > 0, nil
}

// addsPrivileged reports whether perms contains a privileged permission that
// role does not grant yet.
func addsPrivileged(ctx context.Context, store *db.Store, role string, perms []string) (bool, error) {
	for _, p := range perms {
		if !isPrivilegedPermission(p) {
			continue
		}
		var n int
		if err := store.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM role_permissions WHERE role_name = ? AND permission = ?`,
			role, p).Scan(&n); err != nil {
			return false, fmt.Errorf("check permission %q on role %q: %w", p, role, err)
		}
		if n == 0 {
			return true, nil
		}
	}
	return false, nil
}

// allowRoleGrant answers 403 and returns false when a caller who is not a
// full administrator tries to give someone a privileged role.
func allowRoleGrant(w http.ResponseWriter, req *http.Request, store *db.Store, role string) bool {
	if isFullAdmin(auth.UserFromContext(req.Context())) {
		return true
	}
	privileged, err := roleIsPrivileged(req.Context(), store, role)
	if err != nil {
		httperr.Write(w, req, err)
		return false
	}
	if privileged {
		httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
		return false
	}
	return true
}

// allowTargetChange answers 403 and returns false when a caller who is not a
// full administrator tries to change another account that holds a privileged
// permission. Callers may still change their own account; the self-demotion
// guards apply there.
func allowTargetChange(w http.ResponseWriter, req *http.Request, store *db.Store, targetID int64) bool {
	u := auth.UserFromContext(req.Context())
	if isFullAdmin(u) || (u != nil && u.ID == targetID) {
		return true
	}
	privileged, err := userIsPrivileged(req.Context(), store, targetID)
	if err != nil {
		httperr.Write(w, req, err)
		return false
	}
	if privileged {
		httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
		return false
	}
	return true
}
