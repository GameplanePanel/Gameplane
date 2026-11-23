package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// sftpSeedCount returns how many rows of sftp_service_tokens hold the digest of raw.
func sftpSeedCount(t *testing.T, s *SessionStore, raw string) int {
	t.Helper()
	var n int
	if err := s.db.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sftp_service_tokens WHERE digest = ?`, sessionDigest(raw)).Scan(&n); err != nil {
		t.Fatalf("count seeded token rows: %v", err)
	}
	return n
}

func TestSeedSFTPServiceToken_IdempotentAndAuthenticates(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore(newAuthDB(t))
	raw := SFTPServicePrefix + "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v"
	if !validSFTPToken(raw, SFTPServicePrefix) {
		t.Fatalf("fixture token %q is not well formed", raw)
	}

	for i := 0; i < 2; i++ {
		if err := s.SeedSFTPServiceToken(ctx, "local", raw); err != nil {
			t.Fatalf("SeedSFTPServiceToken pass %d: %v", i+1, err)
		}
	}
	if n := sftpSeedCount(t, s, raw); n != 1 {
		t.Fatalf("stored rows = %d, want 1 after two seeds", n)
	}
	id, cluster, err := s.AuthenticateSFTPService(ctx, raw)
	if err != nil {
		t.Fatalf("AuthenticateSFTPService after seed: %v", err)
	}
	if id == 0 || cluster != "local" {
		t.Fatalf("authenticated as id=%d cluster=%q, want non-zero id in local", id, cluster)
	}
}

func TestSeedSFTPServiceToken_RejectsMalformed(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore(newAuthDB(t))
	for _, raw := range []string{"", "gpsftp_" + "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v", SFTPServicePrefix + "short"} {
		err := s.SeedSFTPServiceToken(ctx, "local", raw)
		if !errors.Is(err, errSFTPSeedFormat) {
			t.Fatalf("SeedSFTPServiceToken(%q) error = %v, want errSFTPSeedFormat", raw, err)
		}
	}
	var total int
	if err := s.db.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sftp_service_tokens`).Scan(&total); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if total != 0 {
		t.Fatalf("malformed seeds stored %d rows, want 0", total)
	}
}

func TestSeedSFTPServiceToken_RevokedStaysRevoked(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore(newAuthDB(t))
	raw := SFTPServicePrefix + "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v"
	if err := s.SeedSFTPServiceToken(ctx, "local", raw); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var id int64
	if err := s.db.DB.QueryRowContext(ctx,
		`SELECT id FROM sftp_service_tokens WHERE digest = ?`, sessionDigest(raw)).Scan(&id); err != nil {
		t.Fatalf("find seeded row: %v", err)
	}
	if err := s.RevokeSFTPServiceToken(ctx, "local", id); err != nil {
		t.Fatalf("RevokeSFTPServiceToken: %v", err)
	}

	// A restart re-runs the seed with the same chart token: it must not resurrect it.
	if err := s.SeedSFTPServiceToken(ctx, "local", raw); err != nil {
		t.Fatalf("re-seed after revoke: %v", err)
	}
	if _, _, err := s.AuthenticateSFTPService(ctx, raw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked seeded token authenticated, err = %v", err)
	}
	if n := sftpSeedCount(t, s, raw); n != 1 {
		t.Fatalf("stored rows = %d, want 1", n)
	}
}

func TestSeedSFTPServiceToken_AtLimitLogsAndContinues(t *testing.T) {
	ctx := context.Background()
	s := NewSessionStore(newAuthDB(t))
	for i := 0; i < SFTPMaxActiveServiceTokens; i++ {
		if _, _, err := s.CreateSFTPServiceToken(ctx, "local"); err != nil {
			t.Fatalf("create token %d: %v", i+1, err)
		}
	}
	raw := SFTPServicePrefix + "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v"
	if err := s.SeedSFTPServiceToken(ctx, "local", raw); err != nil {
		t.Fatalf("seed at the limit must not fail startup, got %v", err)
	}
	if _, _, err := s.AuthenticateSFTPService(ctx, raw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("token stored past the limit, authenticate err = %v", err)
	}
}
