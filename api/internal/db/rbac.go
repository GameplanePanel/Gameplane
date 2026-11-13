package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Execer is satisfied by *sql.DB and *sql.Tx, so role-binding writes can
// run either standalone or inside a caller's transaction.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SetClusterRoleBinding repoints the user's cluster-wide ("*") role binding
// at the given cluster/role, leaving any per-namespace bindings intact. Call it
// whenever a user's primary role is set so RBAC resolves their cluster-wide
// permissions from the new role. Pass ex to run inside a transaction, or nil
// to use the store's connection.
func (s *Store) SetClusterRoleBinding(ctx context.Context, ex Execer, userID int64, cluster, role string) error {
	if ex == nil {
		ex = s.DB
	}
	if _, err := ex.ExecContext(ctx,
		`DELETE FROM user_role_bindings WHERE user_id = ? AND cluster = ? AND namespace = '*'`, userID, cluster); err != nil {
		return fmt.Errorf("clear cluster role binding: %w", err)
	}
	if _, err := ex.ExecContext(ctx,
		`INSERT INTO user_role_bindings(user_id, role_name, cluster, namespace) VALUES (?, ?, ?, '*')`,
		userID, role, cluster); err != nil {
		return fmt.Errorf("set cluster role binding: %w", err)
	}
	return nil
}

// DeleteUserBindings removes every role binding for a user. modernc
// sqlite runs with foreign_keys OFF, so ON DELETE CASCADE doesn't fire —
// callers must clean up bindings explicitly on user delete.
func (s *Store) DeleteUserBindings(ctx context.Context, ex Execer, userID int64) error {
	if ex == nil {
		ex = s.DB
	}
	if _, err := ex.ExecContext(ctx, `DELETE FROM user_role_bindings WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete user bindings: %w", err)
	}
	return nil
}

// Reader is the common read interface of a pool and a transaction.
type Reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Roles reads authorization state from the caller's chosen transaction or pool.
type Roles struct{ Reader }

// RoleExists reports whether a role with the given name exists.
func (s *Store) RoleExists(ctx context.Context, name string) (bool, error) {
	return (Roles{s.DB}).RoleExists(ctx, name)
}

// RoleExists reports whether the named role exists in this snapshot.
func (r Roles) RoleExists(ctx context.Context, name string) (bool, error) {
	var x int
	err := r.QueryRowContext(ctx, `SELECT 1 FROM roles WHERE name = ?`, name).Scan(&x)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("role exists: %w", err)
	}
	return true, nil
}

// RoleGrantsUserManagement reports whether the named role can manage
// users (holds users:manage or the "*" wildcard).
func (s *Store) RoleGrantsUserManagement(ctx context.Context, role string) (bool, error) {
	return (Roles{s.DB}).RoleGrantsUserManagement(ctx, role)
}

// RoleGrantsUserManagement checks the role's user-management permission.
func (r Roles) RoleGrantsUserManagement(ctx context.Context, role string) (bool, error) {
	var n int
	err := r.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM role_permissions WHERE role_name = ? AND permission IN ('users:manage', '*')`,
		role).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("role grants user management: %w", err)
	}
	return n > 0, nil
}

// UserManagesUsers reports whether the given user's primary (cluster-wide)
// role can manage users. No cluster filter: control-plane permissions are
// not partitioned per cluster, so any cluster-wide binding confers users:manage.
func (s *Store) UserManagesUsers(ctx context.Context, userID int64) (bool, error) {
	return (Roles{s.DB}).UserManagesUsers(ctx, userID)
}

// UserManagesUsers checks the user's current primary role.
func (r Roles) UserManagesUsers(ctx context.Context, userID int64) (bool, error) {
	var n int
	err := r.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM users u
		JOIN role_permissions rp ON rp.role_name = u.role
		WHERE u.id = ? AND rp.permission IN ('users:manage', '*')`, userID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("user manages users: %w", err)
	}
	return n > 0, nil
}

// UserManagerCount counts users whose primary (cluster-wide) role can
// manage users. Used to refuse demoting/deleting the last such user so
// the cluster never loses all user administration.
func (s *Store) UserManagerCount(ctx context.Context) (int, error) {
	return (Roles{s.DB}).UserManagerCount(ctx)
}

// UserManagerCount counts the users with user-management permission.
func (r Roles) UserManagerCount(ctx context.Context) (int, error) {
	var n int
	err := r.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT u.id)
		FROM users u
		JOIN role_permissions rp ON rp.role_name = u.role
		WHERE rp.permission IN ('users:manage', '*')`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count user managers: %w", err)
	}
	return n, nil
}

// UserManagerCountExcludingRole counts users whose primary (cluster-wide)
// role can manage users, leaving out users whose primary role is role.
// A role edit that removes users:manage uses it to check that some other
// user can still manage users afterwards.
func (s *Store) UserManagerCountExcludingRole(ctx context.Context, role string) (int, error) {
	return (Roles{s.DB}).UserManagerCountExcludingRole(ctx, role)
}

// UserManagerCountExcludingRole counts the managers whose primary role differs.
func (r Roles) UserManagerCountExcludingRole(ctx context.Context, role string) (int, error) {
	var n int
	err := r.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT u.id)
		FROM users u
		JOIN role_permissions rp ON rp.role_name = u.role
		WHERE rp.permission IN ('users:manage', '*') AND u.role <> ?`, role).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count user managers outside role: %w", err)
	}
	return n, nil
}
