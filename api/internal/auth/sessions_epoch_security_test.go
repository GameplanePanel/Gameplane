package auth

import (
	"testing"
)

// A session created at an older epoch is refused once the account's
// session_epoch moves on, even though its row still exists. This is the
// race a password reset closes: a login that verified the old password
// while the reset ran writes its session at the old epoch.
func TestSessions_LookupRejectsStaleEpoch(t *testing.T) {
	s := newAuthDB(t)
	seedUser(t, s, "alice", "pw", "admin")
	store := NewSessionStore(s)

	tok, _, err := store.createAtEpoch(t.Context(), 1, 0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := store.lookup(t.Context(), tok); err != nil {
		t.Fatalf("session at the current epoch refused: %v", err)
	}
	if _, err := s.DB.ExecContext(t.Context(),
		`UPDATE users SET session_epoch = session_epoch + 1 WHERE id = ?`, 1); err != nil {
		t.Fatalf("bump epoch: %v", err)
	}
	if _, _, err := store.lookup(t.Context(), tok); err == nil {
		t.Fatal("session at a stale epoch still accepted")
	}
}
