package db

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"
)

func newShareLinksStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func insertTestUser(t *testing.T, s *Store, username string) int64 {
	t.Helper()
	res, err := s.DB.ExecContext(context.Background(), `INSERT INTO users(username, role) VALUES (?, ?)`, username, "admin")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func TestCreateShareLink_Success(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "alice")

	// Create a link.
	expiresAt := time.Now().Add(24 * time.Hour)
	rawToken, link, err := s.CreateShareLink(ctx, "local", "default", "minecraft-server", userID, true, &expiresAt)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Validate the returned link metadata.
	if link.Cluster != "local" {
		t.Errorf("cluster=%q, want local", link.Cluster)
	}
	if link.Namespace != "default" {
		t.Errorf("namespace=%q, want default", link.Namespace)
	}
	if link.ServerName != "minecraft-server" {
		t.Errorf("server_name=%q, want minecraft-server", link.ServerName)
	}
	if link.CreatedBy != userID {
		t.Errorf("created_by=%d, want %d", link.CreatedBy, userID)
	}
	if !link.CanStart {
		t.Errorf("can_start=%v, want true", link.CanStart)
	}
	if link.RevokedAt != nil {
		t.Errorf("revoked_at=%v, want nil", link.RevokedAt)
	}

	// Validate the raw token format (32 bytes base64-url-encoded).
	// Base64 encoding of 32 bytes produces ~43 characters.
	if len(rawToken) < 40 || len(rawToken) > 50 {
		t.Errorf("raw token length=%d, expected ~43 (32 bytes base64)", len(rawToken))
	}

	// Verify the token is URL-safe base64 (no + or /).
	if strings.ContainsAny(rawToken, "+/") {
		t.Errorf("raw token contains non-URL-safe base64: %q", rawToken)
	}
}

func TestCreateShareLink_ExpiryInPast_Rejected(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "bob")

	// Try to create with an expiry in the past.
	expiresAt := time.Now().Add(-1 * time.Hour)
	_, _, err := s.CreateShareLink(ctx, "local", "default", "server", userID, false, &expiresAt)
	if err == nil {
		t.Fatal("expected error for expiry in past, got nil")
	}
	if !errors.Is(err, ErrShareLinkExpiryInvalid) {
		t.Errorf("got %v, want ErrShareLinkExpiryInvalid", err)
	}
}

func TestCreateShareLink_ExpiryZero_Rejected(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "bob2")

	// Try to create with a zero expiry (a non-nil pointer to the zero
	// time.Time is an explicit, invalid instant — distinct from nil, which
	// means "never expires").
	zero := time.Time{}
	_, _, err := s.CreateShareLink(ctx, "local", "default", "server", userID, false, &zero)
	if err == nil {
		t.Fatal("expected error for zero expiry, got nil")
	}
	if !errors.Is(err, ErrShareLinkExpiryInvalid) {
		t.Errorf("got %v, want ErrShareLinkExpiryInvalid", err)
	}
}

// TestCreateShareLink_FarBeyondOldCap_Accepted replaces the old
// ExpiryExceedsMaximum_Rejected/ExpiryAtMaximumBoundary_Accepted pair now
// that MaxShareLinkExpiryDays (90) is removed (OD-1/OD-5; sign-off recorded
// in spec.md's Assumptions). A custom expiry far beyond the old 90-day cap
// (200 days, per SC-002) must now succeed with no clamp applied.
func TestCreateShareLink_FarBeyondOldCap_Accepted(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "bob3")

	expiresAt := time.Now().AddDate(0, 0, 200)
	rawToken, link, err := s.CreateShareLink(ctx, "local", "default", "server", userID, false, &expiresAt)
	if err != nil {
		t.Fatalf("create with 200-day expiry: %v", err)
	}
	if rawToken == "" {
		t.Error("expected non-empty raw token")
	}
	if link.ExpiresAt == nil {
		t.Fatal("expected non-nil ExpiresAt")
	}
	if !link.ExpiresAt.Equal(expiresAt) {
		t.Errorf("ExpiresAt=%v, want %v", link.ExpiresAt, expiresAt)
	}

	// Re-read the link independently of the pointer passed in, to prove the
	// 200-day expiry actually round-tripped through the database rather than
	// just echoing back the caller's own *time.Time.
	looked, err := s.LookupShareLink(ctx, rawToken)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if looked.ExpiresAt == nil {
		t.Fatal("looked up link: expected non-nil ExpiresAt")
	}
	wantPersisted := expiresAt.Truncate(time.Second)
	if !looked.ExpiresAt.Equal(wantPersisted) {
		t.Errorf("looked up link: ExpiresAt=%v, want %v", looked.ExpiresAt, wantPersisted)
	}
}

// TestCreateShareLink_NilExpiry_NeverExpires covers a nil expiresAt
// (OD-1/OD-3): it is accepted, persists as NULL, and the link is still
// revocable (User Story 3).
func TestCreateShareLink_NilExpiry_NeverExpires(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "bob4")

	rawToken, link, err := s.CreateShareLink(ctx, "local", "default", "server", userID, false, nil)
	if err != nil {
		t.Fatalf("create with nil expiry: %v", err)
	}
	if rawToken == "" {
		t.Error("expected non-empty raw token")
	}
	if link.ExpiresAt != nil {
		t.Errorf("ExpiresAt=%v, want nil", link.ExpiresAt)
	}

	// Verify it round-trips as NULL in the database.
	var expiresAtStr sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT expires_at FROM share_links WHERE id = ?`, link.ID).Scan(&expiresAtStr); err != nil {
		t.Fatalf("query expires_at: %v", err)
	}
	if expiresAtStr.Valid {
		t.Errorf("expires_at=%q, want NULL", expiresAtStr.String)
	}

	// A never-expiring link must still be revocable.
	if err := s.RevokeShareLink(ctx, "local", link.Namespace, link.ServerName, link.ID); err != nil {
		t.Fatalf("revoke never-expiring link: %v", err)
	}
	if _, err := s.LookupShareLink(ctx, rawToken); !errors.Is(err, ErrShareLinkInvalid) {
		t.Errorf("lookup after revoke: got %v, want ErrShareLinkInvalid", err)
	}
}

func TestCreateAndLookup_RoundTrip(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "carol")

	// Create a link.
	expiresAt := time.Now().Add(24 * time.Hour)
	rawToken, created, err := s.CreateShareLink(ctx, "local", "default", "server", userID, true, &expiresAt)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Lookup the link by raw token.
	looked, err := s.LookupShareLink(ctx, rawToken)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	// Validate returned link matches created link.
	if looked.ID != created.ID {
		t.Errorf("id=%q, want %q", looked.ID, created.ID)
	}
	if looked.Namespace != created.Namespace {
		t.Errorf("namespace=%q, want %q", looked.Namespace, created.Namespace)
	}
	if looked.ServerName != created.ServerName {
		t.Errorf("server_name=%q, want %q", looked.ServerName, created.ServerName)
	}
	if looked.CreatedBy != created.CreatedBy {
		t.Errorf("created_by=%d, want %d", looked.CreatedBy, created.CreatedBy)
	}
	if looked.CanStart != created.CanStart {
		t.Errorf("can_start=%v, want %v", looked.CanStart, created.CanStart)
	}
}

func TestLookupShareLink_RawTokenNotStored(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "dave")

	// Create a link.
	expiresAt := time.Now().Add(24 * time.Hour)
	rawToken, _, err := s.CreateShareLink(ctx, "local", "default", "server", userID, false, &expiresAt)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Query the raw row to verify the token_hash is a hash, not the token itself.
	var tokenHash string
	err = s.DB.QueryRowContext(context.Background(), `SELECT token_hash FROM share_links LIMIT 1`).Scan(&tokenHash)
	if err != nil {
		t.Fatalf("query token_hash: %v", err)
	}

	// The token_hash must be a SHA256 hex string (64 hex chars).
	// It should NOT be the raw token (which is much shorter base64).
	if tokenHash == rawToken {
		t.Error("token_hash is the raw token (should be hashed)")
	}

	// The token_hash should look like a SHA256 hex string (lowercase hex, 64 chars).
	if len(tokenHash) != 64 {
		t.Errorf("token_hash length=%d, want 64 (SHA256 hex)", len(tokenHash))
	}

	// Verify it's valid hex.
	if _, err := hex.DecodeString(tokenHash); err != nil {
		t.Errorf("token_hash is not valid hex: %v", err)
	}

	// Verify the stored hash matches the expected hash of the raw token.
	expectedHash := hashShareLinkToken(rawToken)
	if tokenHash != expectedHash {
		t.Errorf("token_hash=%q, expected %q", tokenHash, expectedHash)
	}
}

func TestLookupShareLink_Unknown_Invalid(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()

	// Lookup a completely bogus token.
	_, err := s.LookupShareLink(ctx, "nonexistent-token-xxx")
	if !errors.Is(err, ErrShareLinkInvalid) {
		t.Errorf("lookup unknown token: got %v, want ErrShareLinkInvalid", err)
	}
}

func TestLookupShareLink_Expired_Invalid(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "eve")

	// Create a link that expires immediately.
	expiresAt := time.Now().Add(-1 * time.Second)
	// Temporarily allow this by inserting directly to bypass validation.
	rawToken := generateShareLinkToken()
	tokenHash := hashShareLinkToken(rawToken)
	createdAt := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO share_links(id, namespace, server_name, created_by, can_start, token_hash, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"test-id", "default", "server", userID, 0, tokenHash, expiresAt.Format(time.RFC3339), createdAt)
	if err != nil {
		t.Fatalf("insert expired link: %v", err)
	}

	// Lookup must fail with ErrShareLinkInvalid.
	_, err = s.LookupShareLink(ctx, rawToken)
	if !errors.Is(err, ErrShareLinkInvalid) {
		t.Errorf("lookup expired: got %v, want ErrShareLinkInvalid", err)
	}
}

func TestLookupShareLink_Revoked_Invalid(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "frank")

	// Create a valid link.
	expiresAt := time.Now().Add(24 * time.Hour)
	rawToken, link, err := s.CreateShareLink(ctx, "local", "default", "server", userID, false, &expiresAt)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Revoke it.
	if err := s.RevokeShareLink(ctx, "local", link.Namespace, link.ServerName, link.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// Lookup must fail with ErrShareLinkInvalid.
	_, err = s.LookupShareLink(ctx, rawToken)
	if !errors.Is(err, ErrShareLinkInvalid) {
		t.Errorf("lookup revoked: got %v, want ErrShareLinkInvalid", err)
	}
}

func TestLookupShareLink_UnknownExpiredRevoked_Same_Error(t *testing.T) {
	// Verify that unknown, expired, and revoked all return the same error,
	// so an attacker cannot distinguish between them.
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "grace")

	// Unknown token.
	_, unknownErr := s.LookupShareLink(ctx, "unknown-xxx")

	// Expired link.
	expiresAt := time.Now().Add(-1 * time.Second)
	expiredToken := generateShareLinkToken()
	expiredHash := hashShareLinkToken(expiredToken)
	createdAt := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`INSERT INTO share_links(id, namespace, server_name, created_by, can_start, token_hash, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"expired-id", "default", "server", userID, 0, expiredHash, expiresAt.Format(time.RFC3339), createdAt)
	if err != nil {
		t.Fatalf("insert expired: %v", err)
	}
	_, expiredErr := s.LookupShareLink(ctx, expiredToken)

	// Revoked link.
	validToken := generateShareLinkToken()
	validHash := hashShareLinkToken(validToken)
	revokedAt := time.Now().UTC().Format(time.RFC3339)
	_, err = s.DB.ExecContext(ctx,
		`INSERT INTO share_links(id, namespace, server_name, created_by, can_start, token_hash, expires_at, created_at, revoked_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"revoked-id", "default", "server", userID, 0, validHash, time.Now().Add(24*time.Hour).Format(time.RFC3339), createdAt, revokedAt)
	if err != nil {
		t.Fatalf("insert revoked: %v", err)
	}
	_, revokedErr := s.LookupShareLink(ctx, validToken)

	// All three must return the same error.
	if !errors.Is(unknownErr, ErrShareLinkInvalid) {
		t.Errorf("unknown: got %v, want ErrShareLinkInvalid", unknownErr)
	}
	if !errors.Is(expiredErr, ErrShareLinkInvalid) {
		t.Errorf("expired: got %v, want ErrShareLinkInvalid", expiredErr)
	}
	if !errors.Is(revokedErr, ErrShareLinkInvalid) {
		t.Errorf("revoked: got %v, want ErrShareLinkInvalid", revokedErr)
	}
}

func TestListShareLinks_Scoped(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "henry")

	// Create multiple links for different servers.
	exp := time.Now().Add(24 * time.Hour)

	// Server A in default namespace.
	_, _, err := s.CreateShareLink(ctx, "local", "default", "server-a", userID, true, &exp)
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}

	// Server A in default namespace (another link).
	_, _, err = s.CreateShareLink(ctx, "local", "default", "server-a", userID, false, &exp)
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}

	// Server B in default namespace.
	_, _, err = s.CreateShareLink(ctx, "local", "default", "server-b", userID, true, &exp)
	if err != nil {
		t.Fatalf("create 3: %v", err)
	}

	// Server A in other namespace.
	_, _, err = s.CreateShareLink(ctx, "local", "other", "server-a", userID, false, &exp)
	if err != nil {
		t.Fatalf("create 4: %v", err)
	}

	// List for default/server-a must return exactly 2 links.
	links, err := s.ListShareLinks(ctx, "local", "default", "server-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(links) != 2 {
		t.Errorf("list default/server-a: got %d links, want 2", len(links))
	}

	// Verify all returned links match the namespace and server.
	for _, link := range links {
		if link.Namespace != "default" || link.ServerName != "server-a" {
			t.Errorf("list scoping failed: got %q/%q, want default/server-a", link.Namespace, link.ServerName)
		}
	}

	// List for default/server-b must return exactly 1 link.
	links, err = s.ListShareLinks(ctx, "local", "default", "server-b")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(links) != 1 {
		t.Errorf("list default/server-b: got %d links, want 1", len(links))
	}

	// List for nonexistent server must return empty.
	links, err = s.ListShareLinks(ctx, "local", "default", "nonexistent")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("list nonexistent: got %d links, want 0", len(links))
	}
}

func TestTouchShareLink_Updates_LastUsed(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "ivy")

	// Create a link.
	expiresAt := time.Now().Add(24 * time.Hour)
	_, link, err := s.CreateShareLink(ctx, "local", "default", "server", userID, false, &expiresAt)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Initially last_used must be NULL.
	var lastUsedStr sql.NullString
	err = s.DB.QueryRowContext(context.Background(), `SELECT last_used FROM share_links WHERE id = ?`, link.ID).Scan(&lastUsedStr)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if lastUsedStr.Valid {
		t.Error("expected last_used to be NULL initially")
	}

	// Touch the link.
	if err := s.TouchShareLink(ctx, link.ID); err != nil {
		t.Fatalf("touch: %v", err)
	}

	// Now last_used should be set.
	err = s.DB.QueryRowContext(context.Background(), `SELECT last_used FROM share_links WHERE id = ?`, link.ID).Scan(&lastUsedStr)
	if err != nil {
		t.Fatalf("query after touch: %v", err)
	}
	if !lastUsedStr.Valid {
		t.Error("expected last_used to be set after touch")
	}

	// Parse the timestamp to ensure it's reasonable.
	lastUsed, err := time.Parse(time.RFC3339, lastUsedStr.String)
	if err != nil {
		t.Fatalf("parse last_used: %v", err)
	}

	// Verify it's recent (within 5 seconds).
	if time.Since(lastUsed) > 5*time.Second {
		t.Errorf("last_used=%v is too old", lastUsed)
	}
}

func TestLookupShareLink_DoesNotUpdateLastUsed(t *testing.T) {
	// Verify that LookupShareLink is a pure read and does NOT update last_used.
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "jack")

	// Create a link.
	expiresAt := time.Now().Add(24 * time.Hour)
	rawToken, link, err := s.CreateShareLink(ctx, "local", "default", "server", userID, false, &expiresAt)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Initially last_used must be NULL.
	var lastUsedBefore sql.NullString
	err = s.DB.QueryRowContext(context.Background(), `SELECT last_used FROM share_links WHERE id = ?`, link.ID).Scan(&lastUsedBefore)
	if err != nil {
		t.Fatalf("query before: %v", err)
	}
	if lastUsedBefore.Valid {
		t.Error("expected last_used to be NULL before lookup")
	}

	// Lookup the link.
	_, err = s.LookupShareLink(ctx, rawToken)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}

	// last_used should still be NULL (lookup is a pure read).
	var lastUsedAfter sql.NullString
	err = s.DB.QueryRowContext(context.Background(), `SELECT last_used FROM share_links WHERE id = ?`, link.ID).Scan(&lastUsedAfter)
	if err != nil {
		t.Fatalf("query after: %v", err)
	}
	if lastUsedAfter.Valid {
		t.Error("expected last_used to remain NULL after lookup (lookup must not update it)")
	}
}

func TestSetShareLinkCanStartScopeAndLiveness(t *testing.T) {
	ctx := t.Context()
	s := newShareLinksStore(t)
	owner := insertTestUser(t, s, "edit-scope")
	now := time.Now().UTC().Truncate(time.Second)
	token, link, err := s.CreateShareLinkForServer(ctx, "remote", "games", "mc", "uid-a", owner, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ cluster, namespace, server, id, uid string }{
		{"other", "games", "mc", link.ID, "uid-a"}, {"remote", "other", "mc", link.ID, "uid-a"},
		{"remote", "games", "other", link.ID, "uid-a"}, {"remote", "games", "mc", "unknown", "uid-a"},
		{"remote", "games", "mc", link.ID, "uid-b"},
	} {
		if err := s.SetShareLinkCanStart(ctx, tc.cluster, tc.namespace, tc.server, tc.id, tc.uid, now, true); !errors.Is(err, ErrShareLinkNotFound) {
			t.Fatalf("set for %+v: %v, want not found", tc, err)
		}
	}
	if got, err := s.LookupShareLink(ctx, token); err != nil || got.CanStart {
		t.Fatalf("after refused edits: canStart=%v, err=%v", got.CanStart, err)
	}
	if err := s.RevokeShareLink(ctx, "remote", "games", "mc", link.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetShareLinkCanStart(ctx, "remote", "games", "mc", link.ID, "uid-a", now, true); !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("edit revoked link: %v, want not found", err)
	}
	_, legacy, err := s.CreateShareLink(ctx, "", "games", "mc", owner, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	set := func(serverCreated time.Time) error {
		return s.SetShareLinkCanStart(ctx, "", "games", "mc", legacy.ID, "uid-b", serverCreated, true)
	}
	if err := set(now.Add(time.Hour)); !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("edit legacy link after recreation: %v, want not found", err)
	}
	if err := set(now); err != nil {
		t.Fatalf("edit active legacy link: %v", err)
	}
	// An offset timestamp whose wall-clock time appears in the future is still expired.
	past := now.Add(-time.Hour).In(time.FixedZone("UTC+10", 10*60*60))
	if _, err := s.DB.ExecContext(ctx, "UPDATE share_links SET expires_at = ? WHERE id = ?", past.Format(time.RFC3339), legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := set(now); !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("edit expired link: %v, want not found", err)
	}
}

func TestRevokeShareLink_Success(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "karl")

	// Create a link.
	expiresAt := time.Now().Add(24 * time.Hour)
	_, link, err := s.CreateShareLink(ctx, "local", "default", "server", userID, false, &expiresAt)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Revoke it.
	if err := s.RevokeShareLink(ctx, "local", link.Namespace, link.ServerName, link.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// Verify revoked_at is now set.
	var revokedAtStr sql.NullString
	err = s.DB.QueryRowContext(context.Background(), `SELECT revoked_at FROM share_links WHERE id = ?`, link.ID).Scan(&revokedAtStr)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !revokedAtStr.Valid {
		t.Error("expected revoked_at to be set after revoke")
	}

	// Parse the timestamp.
	revokedAt, err := time.Parse(time.RFC3339, revokedAtStr.String)
	if err != nil {
		t.Fatalf("parse revoked_at: %v", err)
	}

	// Verify it's recent.
	if time.Since(revokedAt) > 5*time.Second {
		t.Errorf("revoked_at=%v is too old", revokedAt)
	}
}

func TestRevokeShareLink_NotFound(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()

	// Try to revoke a nonexistent link.
	err := s.RevokeShareLink(ctx, "local", "default", "server", "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent link, got nil")
	}
}

// TestRevokeShareLink_NotFound_IsErrShareLinkNotFound is the F-082
// regression test: RevokeShareLink's unknown-id error must satisfy
// errors.Is(err, ErrShareLinkNotFound) so httperr.classify maps it to 404
// instead of the opaque 500 default. See httperr_test.go's
// "share link not found (F-082)" case for the classification side.
func TestRevokeShareLink_NotFound_IsErrShareLinkNotFound(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()

	err := s.RevokeShareLink(ctx, "local", "default", "server", "nonexistent-id")
	if !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("RevokeShareLink error = %v, want errors.Is(err, ErrShareLinkNotFound)", err)
	}

	// Same for the cluster-scoped path (cluster provided but no matching row).
	err = s.RevokeShareLink(ctx, "some-cluster", "default", "server", "nonexistent-id")
	if !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("cluster-scoped RevokeShareLink error = %v, want errors.Is(err, ErrShareLinkNotFound)", err)
	}
}

func TestShareLinks_ClusterScoping(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "cluster-user")

	exp := time.Now().Add(24 * time.Hour)
	// Create link in cluster-a
	_, linkA, err := s.CreateShareLink(ctx, "cluster-a", "default", "srv-test", userID, true, &exp)
	if err != nil {
		t.Fatalf("create link A: %v", err)
	}

	// Create link in cluster-b with same namespace and server name
	_, linkB, err := s.CreateShareLink(ctx, "cluster-b", "default", "srv-test", userID, false, &exp)
	if err != nil {
		t.Fatalf("create link B: %v", err)
	}

	// List in cluster-a must only return linkA
	listA, err := s.ListShareLinks(ctx, "cluster-a", "default", "srv-test")
	if err != nil {
		t.Fatalf("list A: %v", err)
	}
	if len(listA) != 1 || listA[0].ID != linkA.ID {
		t.Fatalf("list A mismatch: expected [%s], got %+v", linkA.ID, listA)
	}

	// List in cluster-b must only return linkB
	listB, err := s.ListShareLinks(ctx, "cluster-b", "default", "srv-test")
	if err != nil {
		t.Fatalf("list B: %v", err)
	}
	if len(listB) != 1 || listB[0].ID != linkB.ID {
		t.Fatalf("list B mismatch: expected [%s], got %+v", linkB.ID, listB)
	}

	// Revoke linkB with cluster-a must fail
	if err := s.RevokeShareLink(ctx, "cluster-a", linkB.Namespace, linkB.ServerName, linkB.ID); err == nil {
		t.Fatal("expected error revoking cluster-b link with cluster-a, got nil")
	}

	// Revoke linkB with cluster-b must succeed
	if err := s.RevokeShareLink(ctx, "cluster-b", linkB.Namespace, linkB.ServerName, linkB.ID); err != nil {
		t.Fatalf("revoke linkB with cluster-b failed: %v", err)
	}
}

// TestLookupShareLink_NilExpiry_UnexpiredAtYearPlusRange covers SC-001: a
// link with a nil (never-expires) ExpiresAt remains valid arbitrarily far in
// the future, since the expiry check is skipped entirely for a nil value.
func TestLookupShareLink_NilExpiry_UnexpiredAtYearPlusRange(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "liam")

	rawToken, link, err := s.CreateShareLink(ctx, "local", "default", "server", userID, false, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if link.ExpiresAt != nil {
		t.Fatalf("ExpiresAt=%v, want nil", link.ExpiresAt)
	}

	looked, err := s.LookupShareLink(ctx, rawToken)
	if err != nil {
		t.Fatalf("lookup should succeed for a never-expiring link: %v", err)
	}
	if looked.ExpiresAt != nil {
		t.Errorf("looked.ExpiresAt=%v, want nil", looked.ExpiresAt)
	}
}

// TestListShareLinks_MixedExpiryShapes covers FR-007: a dated expiry and a
// nil (never-expires) expiry both round-trip correctly through ListShareLinks.
func TestListShareLinks_MixedExpiryShapes(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "mia")

	dated := time.Now().Add(48 * time.Hour)
	_, datedLink, err := s.CreateShareLink(ctx, "local", "default", "mixed-server", userID, false, &dated)
	if err != nil {
		t.Fatalf("create dated link: %v", err)
	}
	_, neverLink, err := s.CreateShareLink(ctx, "local", "default", "mixed-server", userID, false, nil)
	if err != nil {
		t.Fatalf("create never-expiring link: %v", err)
	}

	links, err := s.ListShareLinks(ctx, "local", "default", "mixed-server")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2", len(links))
	}

	var sawDated, sawNever bool
	for _, l := range links {
		switch l.ID {
		case datedLink.ID:
			sawDated = true
			if l.ExpiresAt == nil {
				t.Error("dated link: ExpiresAt=nil, want non-nil")
			} else if !l.ExpiresAt.Equal(dated.Truncate(time.Second)) {
				t.Errorf("dated link: ExpiresAt=%v, want %v", l.ExpiresAt, dated.Truncate(time.Second))
			}
		case neverLink.ID:
			sawNever = true
			if l.ExpiresAt != nil {
				t.Errorf("never-expiring link: ExpiresAt=%v, want nil", l.ExpiresAt)
			}
		}
	}
	if !sawDated {
		t.Error("dated link not found in list")
	}
	if !sawNever {
		t.Error("never-expiring link not found in list")
	}
}

// TestRevocation_IndependentOfExpiryShape covers User Story 3: revocation
// works identically regardless of how a link's expiry was shaped — a short
// expiry, a long/custom (far future) expiry, and a nil (never-expires)
// expiry all become ErrShareLinkInvalid after RevokeShareLink.
func TestRevocation_IndependentOfExpiryShape(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "noah")

	short := time.Now().Add(1 * time.Hour)
	long := time.Now().AddDate(1, 0, 0) // just over a year out
	custom := time.Now().AddDate(0, 0, 45)

	shapes := []struct {
		name      string
		expiresAt *time.Time
	}{
		{"short", &short},
		{"long", &long},
		{"custom", &custom},
		{"never", nil},
	}

	for _, shape := range shapes {
		rawToken, link, err := s.CreateShareLink(ctx, "local", "default", "revoke-shape-server", userID, false, shape.expiresAt)
		if err != nil {
			t.Fatalf("%s: create: %v", shape.name, err)
		}
		if err := s.RevokeShareLink(ctx, "local", link.Namespace, link.ServerName, link.ID); err != nil {
			t.Fatalf("%s: revoke: %v", shape.name, err)
		}
		if _, err := s.LookupShareLink(ctx, rawToken); !errors.Is(err, ErrShareLinkInvalid) {
			t.Errorf("%s: lookup after revoke: got %v, want ErrShareLinkInvalid", shape.name, err)
		}
	}
}

// TestMigration010_ExpiresAtNullable proves the 010 rebuild (OD-4) preserves
// existing data untouched and accepts NULL going forward (User Story 2's
// Independent Test, SC-003). It applies migrations up through 009 by hand,
// seeds a row under the pre-010 schema (expires_at NOT NULL) with a non-null
// value, then applies 010 and confirms that value is byte-for-byte
// unchanged, and that a row inserted after 010 with a NULL expires_at
// round-trips as NULL.
func TestMigration010_ExpiresAtNullable(t *testing.T) {
	s, err := Open(context.Background(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	if _, err := s.DB.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`,
	); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	// Apply every migration up through 009 by hand, stopping before 010.
	names := []string{
		"001_init.sql", "002_config.sql", "003_roles.sql", "004_cluster_rbac.sql",
		"005_audit_chain.sql", "006_share_links.sql", "007_audit_reason.sql",
		"008_captures_rbac.sql", "009_share_links_cluster.sql",
	}
	for _, name := range names {
		content, err := migrations.ReadFile("migrations/sqlite/" + name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := s.runMigration(ctx, name, string(content)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	// Seed a user and a pre-010 row (expires_at NOT NULL at this point)
	// with a known, non-null expires_at value.
	userID := insertTestUser(t, s, "pre010-user")
	const wantExpiresAt = "2027-06-15T12:00:00Z"
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO share_links(id, cluster, namespace, server_name, created_by, can_start, token_hash, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"pre010-id", "local", "default", "server", userID, 0, "deadbeef", wantExpiresAt, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("seed pre-010 row: %v", err)
	}

	// Now apply 010.
	content, err := migrations.ReadFile("migrations/sqlite/010_share_links_expiry_nullable.sql")
	if err != nil {
		t.Fatalf("read 010: %v", err)
	}
	if err := s.runMigration(ctx, "010_share_links_expiry_nullable.sql", string(content)); err != nil {
		t.Fatalf("apply 010: %v", err)
	}

	// The pre-existing row's expires_at must be byte-for-byte unchanged.
	var gotExpiresAt string
	if err := s.DB.QueryRowContext(ctx, `SELECT expires_at FROM share_links WHERE id = ?`, "pre010-id").Scan(&gotExpiresAt); err != nil {
		t.Fatalf("query pre-010 row after migration: %v", err)
	}
	if gotExpiresAt != wantExpiresAt {
		t.Errorf("expires_at=%q after migration, want unchanged %q", gotExpiresAt, wantExpiresAt)
	}

	// The three indexes must exist post-rebuild.
	for _, idx := range []string{"idx_share_links_token", "idx_share_links_server", "idx_share_links_cluster_server"} {
		var n int
		if err := s.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&n); err != nil {
			t.Fatalf("check index %s: %v", idx, err)
		}
		if n != 1 {
			t.Errorf("index %s missing after migration 010", idx)
		}
	}

	// A row inserted post-migration with a NULL expires_at must round-trip as NULL.
	if _, _, err := s.CreateShareLink(ctx, "local", "default", "post010-server", userID, false, nil); err != nil {
		t.Fatalf("create post-010 row with nil expiry: %v", err)
	}
	var postExpiresAt sql.NullString
	if err := s.DB.QueryRowContext(ctx,
		`SELECT expires_at FROM share_links WHERE server_name = ?`, "post010-server").Scan(&postExpiresAt); err != nil {
		t.Fatalf("query post-010 row: %v", err)
	}
	if postExpiresAt.Valid {
		t.Errorf("post-010 NULL expiry round-tripped as %q, want NULL", postExpiresAt.String)
	}
}

// TestRevokeShareLink_ScopedToServer covers revocation scope: a link is
// revoked only through the cluster, namespace and server it was created for.
func TestRevokeShareLink_ScopedToServer(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "scoped-revoke")

	rawToken, link, err := s.CreateShareLink(ctx, "local", "default", "srv-a", userID, false, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mismatched := []struct {
		name, cluster, ns, server string
	}{
		{"other namespace", "local", "other", "srv-a"},
		{"other server", "local", "default", "srv-b"},
		{"other cluster", "remote", "default", "srv-a"},
	}
	for _, m := range mismatched {
		if err := s.RevokeShareLink(ctx, m.cluster, m.ns, m.server, link.ID); !errors.Is(err, ErrShareLinkNotFound) {
			t.Errorf("%s: got %v, want ErrShareLinkNotFound", m.name, err)
		}
	}

	var revokedAt sql.NullString
	if err := s.DB.QueryRowContext(ctx, `SELECT revoked_at FROM share_links WHERE id = ?`, link.ID).Scan(&revokedAt); err != nil {
		t.Fatalf("query revoked_at: %v", err)
	}
	if revokedAt.Valid {
		t.Fatalf("revoked_at = %q after mismatched revokes, want NULL", revokedAt.String)
	}
	if _, err := s.LookupShareLink(ctx, rawToken); err != nil {
		t.Fatalf("link no longer resolves after mismatched revokes: %v", err)
	}

	if err := s.RevokeShareLink(ctx, "local", "default", "srv-a", link.ID); err != nil {
		t.Fatalf("revoke with matching scope: %v", err)
	}
	if _, err := s.LookupShareLink(ctx, rawToken); !errors.Is(err, ErrShareLinkInvalid) {
		t.Fatalf("lookup after revoke: got %v, want ErrShareLinkInvalid", err)
	}
}

func TestRevokeShareLink_UnknownIDReturnsNotFound(t *testing.T) {
	s := newShareLinksStore(t)
	err := s.RevokeShareLink(context.Background(), "local", "default", "server", "nonexistent-id")
	if !errors.Is(err, ErrShareLinkNotFound) {
		t.Fatalf("got %v, want ErrShareLinkNotFound", err)
	}
}

func TestRevokeShareLinksForServer(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	uid := insertTestUser(t, s, "owner")

	tokA, _, err := s.CreateShareLink(ctx, "local", "ns", "srv-a", uid, false, nil)
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	tokB, _, err := s.CreateShareLink(ctx, "local", "ns", "srv-b", uid, false, nil)
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	tokOtherCluster, _, err := s.CreateShareLink(ctx, "remote", "ns", "srv-a", uid, false, nil)
	if err != nil {
		t.Fatalf("create remote: %v", err)
	}

	// An empty cluster means "local".
	if err := s.RevokeShareLinksForServer(ctx, "", "ns", "srv-a"); err != nil {
		t.Fatalf("RevokeShareLinksForServer: %v", err)
	}
	if _, err := s.LookupShareLink(ctx, tokA); !errors.Is(err, ErrShareLinkInvalid) {
		t.Fatalf("srv-a link after revoke = %v, want ErrShareLinkInvalid", err)
	}
	if _, err := s.LookupShareLink(ctx, tokB); err != nil {
		t.Fatalf("srv-b link must stay valid: %v", err)
	}
	if _, err := s.LookupShareLink(ctx, tokOtherCluster); err != nil {
		t.Fatalf("same-name link on another cluster must stay valid: %v", err)
	}

	// Revoking again (nothing active) is not an error, and the row is kept.
	if err := s.RevokeShareLinksForServer(ctx, "local", "ns", "srv-a"); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	links, err := s.ListShareLinks(ctx, "local", "ns", "srv-a")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(links) != 1 || links[0].RevokedAt == nil {
		t.Fatalf("revoked link row must be kept with revoked_at set, got %+v", links)
	}
}

// TestCreateShareLinkForServer_PersistsServerUID verifies the UID is stored
// and returned by both Lookup and List, that CreateShareLink leaves it empty,
// and that a row written without the column (pre-013 shape) reads back as "".
func TestCreateShareLinkForServer_PersistsServerUID(t *testing.T) {
	s := newShareLinksStore(t)
	ctx := context.Background()
	userID := insertTestUser(t, s, "uid-owner")

	token, created, err := s.CreateShareLinkForServer(ctx, "local", "default", "srv-uid", "uid-1234", userID, false, nil)
	if err != nil {
		t.Fatalf("CreateShareLinkForServer: %v", err)
	}
	if created.ServerUID != "uid-1234" {
		t.Errorf("created.ServerUID = %q, want uid-1234", created.ServerUID)
	}
	looked, err := s.LookupShareLink(ctx, token)
	if err != nil {
		t.Fatalf("LookupShareLink: %v", err)
	}
	if looked.ServerUID != "uid-1234" {
		t.Errorf("looked.ServerUID = %q, want uid-1234", looked.ServerUID)
	}

	if _, plain, err := s.CreateShareLink(ctx, "local", "default", "srv-plain", userID, false, nil); err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	} else if plain.ServerUID != "" {
		t.Errorf("CreateShareLink ServerUID = %q, want empty", plain.ServerUID)
	}

	// Legacy-shaped row: INSERT omits server_uid, so the column default applies.
	if _, err := s.DB.ExecContext(ctx,
		`INSERT INTO share_links(id, cluster, namespace, server_name, created_by, can_start, token_hash, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?)`,
		"legacy-id", "local", "default", "srv-legacy", userID, 0, hashShareLinkToken("legacy-token"), time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	legacy, err := s.LookupShareLink(ctx, "legacy-token")
	if err != nil {
		t.Fatalf("lookup legacy: %v", err)
	}
	if legacy.ServerUID != "" {
		t.Errorf("legacy ServerUID = %q, want empty", legacy.ServerUID)
	}

	links, err := s.ListShareLinks(ctx, "local", "default", "srv-uid")
	if err != nil {
		t.Fatalf("ListShareLinks: %v", err)
	}
	if len(links) != 1 || links[0].ServerUID != "uid-1234" {
		t.Fatalf("ListShareLinks = %+v, want one link with ServerUID uid-1234", links)
	}
}
