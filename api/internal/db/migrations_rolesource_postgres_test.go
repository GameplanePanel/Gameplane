//go:build postgres

package db

import "testing"

// Migration 022 on PostgreSQL: the same OD-4 checks as on SQLite, on a
// fresh unmigrated schema of the test database.
func TestPostgres_Migration022_RoleSource(t *testing.T) {
	checkRoleSourceMigration(t, openPostgresSchema(t))
}
