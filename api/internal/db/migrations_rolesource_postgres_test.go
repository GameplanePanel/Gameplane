//go:build postgres

package db

import "testing"

// Migration 020 on PostgreSQL: the same OD-4 checks as on SQLite, on a
// fresh unmigrated schema of the test database.
func TestPostgres_Migration020_RoleSource(t *testing.T) {
	checkRoleSourceMigration(t, openPostgresSchema(t))
}
