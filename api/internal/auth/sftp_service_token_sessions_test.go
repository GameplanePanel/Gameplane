package auth

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// sftpActiveServiceTokens counts the unrevoked service tokens of cluster.
func sftpActiveServiceTokens(t *testing.T, s *SessionStore, cluster string) int {
	t.Helper()
	var n int
	if err := s.db.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sftp_service_tokens WHERE cluster = ? AND revoked_at IS NULL`, cluster).Scan(&n); err != nil {
		t.Fatalf("count active service tokens: %v", err)
	}
	return n
}

// TestRevokeSFTPServiceToken_EndsSessionsMintedThroughIt checks that revoking a service token ends the
// sessions minted through it, and only those.
func TestRevokeSFTPServiceToken_EndsSessionsMintedThroughIt(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	alice := sftpUserID(t, s, "alice")

	tok, _, err := s.CreateSFTPServiceToken(ctx, "local")
	if err != nil {
		t.Fatalf("CreateSFTPServiceToken: %v", err)
	}
	other, _, err := s.CreateSFTPServiceToken(ctx, "local")
	if err != nil {
		t.Fatalf("CreateSFTPServiceToken other: %v", err)
	}

	minted, _, err := s.IssueSFTPSessionForToken(ctx, alice, "local", "password", 0, tok.ID)
	if err != nil {
		t.Fatalf("IssueSFTPSessionForToken: %v", err)
	}
	mintedOther, _, err := s.IssueSFTPSessionForToken(ctx, alice, "local", "password", 0, other.ID)
	if err != nil {
		t.Fatalf("IssueSFTPSessionForToken other: %v", err)
	}
	direct, _, err := s.IssueSFTPSession(ctx, alice, "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}

	if err := s.RevokeSFTPServiceToken(ctx, "local", tok.ID); err != nil {
		t.Fatalf("RevokeSFTPServiceToken: %v", err)
	}

	if _, _, err := s.LookupSFTPSessionToken(ctx, minted); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("session minted through the revoked token still resolves, err = %v", err)
	}
	for name, raw := range map[string]string{"other token": mintedOther, "direct session": direct} {
		if _, _, err := s.LookupSFTPSessionToken(ctx, raw); err != nil {
			t.Fatalf("%s was ended by an unrelated revoke: %v", name, err)
		}
	}
}

// TestSeedSFTPServiceToken_NewDigestReplacesPreviousSeed checks that a re-rendered seed secret replaces
// the earlier seeded token and ends its sessions, so the active count never includes both.
func TestSeedSFTPServiceToken_NewDigestReplacesPreviousSeed(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	alice := sftpUserID(t, s, "alice")

	first := SFTPServicePrefix + "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v"
	second := SFTPServicePrefix + "Z9y8X7w6V5u4T3s2R1q0P9o8N7m6L5k4J3i2H1g0F9e"
	if err := s.SeedSFTPServiceToken(ctx, "local", first); err != nil {
		t.Fatalf("seed first: %v", err)
	}
	firstID, _, err := s.AuthenticateSFTPService(ctx, first)
	if err != nil {
		t.Fatalf("authenticate first seed: %v", err)
	}
	session, _, err := s.IssueSFTPSessionForToken(ctx, alice, "local", "password", 0, firstID)
	if err != nil {
		t.Fatalf("issue session through first seed: %v", err)
	}

	if err := s.SeedSFTPServiceToken(ctx, "local", second); err != nil {
		t.Fatalf("seed second: %v", err)
	}

	if _, _, err := s.AuthenticateSFTPService(ctx, first); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("previous seed still authenticates, err = %v", err)
	}
	if _, _, err := s.LookupSFTPSessionToken(ctx, session); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("session of the previous seed still resolves, err = %v", err)
	}
	if _, _, err := s.AuthenticateSFTPService(ctx, second); err != nil {
		t.Fatalf("new seed does not authenticate: %v", err)
	}
	if n := sftpActiveServiceTokens(t, s, "local"); n != 1 {
		t.Fatalf("active service tokens = %d, want 1 after re-seed", n)
	}

	// The replaced seed is revoked for good: re-seeding it does not bring it back.
	if err := s.SeedSFTPServiceToken(ctx, "local", first); err != nil {
		t.Fatalf("re-seed first: %v", err)
	}
	if _, _, err := s.AuthenticateSFTPService(ctx, first); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("replaced seed came back, err = %v", err)
	}
}

// TestSeedSFTPServiceToken_LeavesAdminTokensAlone checks that the replacement only touches seeded tokens:
// an admin-created token keeps working across a re-seed.
func TestSeedSFTPServiceToken_LeavesAdminTokensAlone(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	s := NewSessionStore(st)

	admin, adminRaw, err := s.CreateSFTPServiceToken(ctx, "local")
	if err != nil {
		t.Fatalf("CreateSFTPServiceToken: %v", err)
	}
	seeded := SFTPServicePrefix + "A1b2C3d4E5f6G7h8I9j0K1l2M3n4O5p6Q7r8S9t0U1v"
	if err := s.SeedSFTPServiceToken(ctx, "local", seeded); err != nil {
		t.Fatalf("seed: %v", err)
	}
	next := SFTPServicePrefix + "Z9y8X7w6V5u4T3s2R1q0P9o8N7m6L5k4J3i2H1g0F9e"
	if err := s.SeedSFTPServiceToken(ctx, "local", next); err != nil {
		t.Fatalf("re-seed: %v", err)
	}

	if id, _, err := s.AuthenticateSFTPService(ctx, adminRaw); err != nil || id != admin.ID {
		t.Fatalf("admin token after re-seed: id=%d err=%v, want id %d", id, err, admin.ID)
	}
	if n := sftpActiveServiceTokens(t, s, "local"); n != 2 {
		t.Fatalf("active service tokens = %d, want 2 (admin token and the new seed)", n)
	}
}
