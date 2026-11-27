//go:build postgres

package db

import "testing"

// TestPostgres_ThemeStore runs the shared theme store checks on PostgreSQL.
func TestPostgres_ThemeStore(t *testing.T) {
	s := newPostgresStore(t)
	checkThemeStore(t, s, func(t *testing.T, name string) int64 {
		return pgInsertUser(t, s, name, "admin")
	})
}
