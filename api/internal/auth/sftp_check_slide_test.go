package auth

import (
	"context"
	"testing"
	"time"
)

// TestCheckSFTPSessions_LiveSessionSlides checks that a live session the endpoint asks about gets its
// expiry pushed out, so a long transfer does not outlive its session (FR-018).
func TestCheckSFTPSessions_LiveSessionSlides(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	token, _, err := s.IssueSFTPSession(ctx, sftpUserID(t, s, "alice"), "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}

	// A session close to expiry, still live.
	nearExpiry := time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339)
	if _, err := st.DB.ExecContext(ctx, `UPDATE sftp_sessions SET expires_at = ? WHERE digest = ?`,
		nearExpiry, SFTPTokenDigest(token)); err != nil {
		t.Fatalf("set near expiry: %v", err)
	}

	revoked, err := s.CheckSFTPSessions(ctx, "local", []string{SFTPTokenDigest(token)})
	if err != nil {
		t.Fatalf("CheckSFTPSessions: %v", err)
	}
	if len(revoked) != 0 {
		t.Fatalf("live session reported as revoked: %v", revoked)
	}

	var got string
	if err := st.DB.QueryRowContext(ctx, `SELECT expires_at FROM sftp_sessions WHERE digest = ?`,
		SFTPTokenDigest(token)).Scan(&got); err != nil {
		t.Fatalf("read expiry: %v", err)
	}
	expires, err := parseSFTPTime(got)
	if err != nil {
		t.Fatalf("parse expiry %q: %v", got, err)
	}
	if !expires.After(time.Now().Add(sftpSessionTTL - time.Minute)) {
		t.Fatalf("expiry after check = %s, want it slid to about now + %s", expires, sftpSessionTTL)
	}

	// The session still authenticates after the slide.
	if _, _, err := s.LookupSFTPSessionToken(ctx, token); err != nil {
		t.Fatalf("lookup after slide: %v", err)
	}
}

// TestCheckSFTPSessions_RevokedSessionStillReported checks that a revoked session is reported, and
// that the check does not bring it back.
func TestCheckSFTPSessions_RevokedSessionStillReported(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	token, _, err := s.IssueSFTPSession(ctx, sftpUserID(t, s, "alice"), "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}
	if err := s.RevokeSFTPSessionToken(ctx, token); err != nil {
		t.Fatalf("RevokeSFTPSessionToken: %v", err)
	}

	revoked, err := s.CheckSFTPSessions(ctx, "local", []string{SFTPTokenDigest(token)})
	if err != nil {
		t.Fatalf("CheckSFTPSessions: %v", err)
	}
	if len(revoked) != 1 || revoked[0] != SFTPTokenDigest(token) {
		t.Fatalf("revoked digests = %v, want the revoked session's digest", revoked)
	}
	if _, _, err := s.LookupSFTPSessionToken(ctx, token); err == nil {
		t.Fatal("revoked session authenticates after the check")
	}
}
