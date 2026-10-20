package db

import (
	"context"
	"fmt"
	"time"
)

// DeleteUser removes a user account and every row tied to it, in one
// transaction: the account's SSO links, preferences, sessions, API tokens,
// role bindings, and telemetry notice acks are deleted, and the share links
// it created are revoked (revocation, not a delete, keeps their audit trail).
// The rows are removed explicitly rather than left to ON DELETE CASCADE,
// because the shipped SQLite DSN runs with foreign keys off. Deleting an
// unknown id is a no-op.
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete user: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	revokedAt := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`UPDATE share_links SET revoked_at = ? WHERE created_by = ? AND revoked_at IS NULL`,
		revokedAt, userID); err != nil {
		return fmt.Errorf("delete user: revoke share links: %w", err)
	}
	for _, stmt := range []string{
		`DELETE FROM oidc_links WHERE user_id = ?`,
		`DELETE FROM user_preferences WHERE user_id = ?`,
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM api_tokens WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, userID); err != nil {
			return fmt.Errorf("delete user: %s: %w", stmt, err)
		}
	}
	if err := s.DeleteUserBindings(ctx, tx, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if err := s.DeleteNoticeAcksForUser(ctx, tx, userID); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return fmt.Errorf("delete user: users row: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete user: commit: %w", err)
	}
	return nil
}
