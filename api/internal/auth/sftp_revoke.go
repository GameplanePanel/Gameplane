package auth

import (
	"context"
	"fmt"
)

// SFTPMaxEndDigests bounds one EndSFTPSessions call.
const SFTPMaxEndDigests = sftpMaxCheckDigests

// SFTPTokenDigest returns the stored digest of a raw gpsftp_ session token. It is the key
// CheckSFTPSessions takes, so callers can map digests back to the tokens they hold.
func SFTPTokenDigest(raw string) string {
	return sessionDigest(raw)
}

// RevokeSFTPSessionToken revokes the single session row of a raw gpsftp_ token. Login uses it when
// the success audit row cannot be written, so a token the caller never received cannot be used.
func (s *SessionStore) RevokeSFTPSessionToken(ctx context.Context, raw string) error {
	if _, err := s.db.DB.ExecContext(ctx,
		`UPDATE sftp_sessions SET revoked_at = ? WHERE digest = ? AND revoked_at IS NULL`,
		sftpNow(), sessionDigest(raw)); err != nil {
		return fmt.Errorf("revoke sftp session token: %w", err)
	}
	return nil
}

// EndSFTPSessions revokes the live sessions among digests that were issued for cluster, in one
// transaction. Unknown, foreign-cluster and already revoked digests are ignored.
func (s *SessionStore) EndSFTPSessions(ctx context.Context, cluster string, digests []string) error {
	if len(digests) == 0 {
		return nil
	}
	if len(digests) > SFTPMaxEndDigests {
		return fmt.Errorf("end sftp sessions: %d digests exceeds %d", len(digests), SFTPMaxEndDigests)
	}
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("end sftp sessions: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := sftpNow()
	for _, d := range digests {
		if _, err := tx.ExecContext(ctx,
			`UPDATE sftp_sessions SET revoked_at = ? WHERE digest = ? AND cluster = ? AND revoked_at IS NULL`,
			now, d, cluster); err != nil {
			return fmt.Errorf("end sftp sessions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("end sftp sessions: commit: %w", err)
	}
	return nil
}
