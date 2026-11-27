//go:build postgres

package db

import "testing"

// Migration 024 on PostgreSQL: the same SFTP checks as on SQLite, on a
// fresh unmigrated schema of the test database.
func TestPostgres_Migration024_SFTP(t *testing.T) {
	checkSFTPMigration(t, openPostgresSchema(t))
}
