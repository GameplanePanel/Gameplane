package db

import (
	"context"
	"fmt"
	"time"
)

// SetConfigValue stores the raw JSON value of an admin config key, replacing
// any previous value. It writes the same `config` table that ConfigValue reads,
// with the statement shape the telemetry and auth stores use, so it runs on
// SQLite and PostgreSQL unchanged.
func (s *Store) SetConfigValue(ctx context.Context, key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO config (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, now); err != nil {
		return fmt.Errorf("set config %s: %w", key, err)
	}
	return nil
}
