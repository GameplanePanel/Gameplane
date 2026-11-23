package auth

import (
	"context"
	"fmt"
)

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
