package db

import (
	"context"
	"testing"
	"time"
)

// seedSFTPRows gives userID one SFTP key, one password row and one session.
func seedSFTPRows(t *testing.T, s *Store, userID int64, tag string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sftp_ssh_keys(id, user_id, name, public_key, fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			[]any{userID*10 + 1, userID, "key-" + tag, "ssh-ed25519 AAAA-" + tag, "SHA256:fp-" + tag, now}},
		{`INSERT INTO sftp_passwords(user_id, hash, length, created_at) VALUES (?, ?, ?, ?)`,
			[]any{userID, "hash-" + tag, 24, now}},
		{`INSERT INTO sftp_sessions(id, user_id, cluster, credential, digest, session_epoch, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			[]any{userID*10 + 2, userID, "local", "password", "sess-" + tag, 0, now}},
	} {
		if _, err := s.DB.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed %q: %v", stmt.sql, err)
		}
	}
}

// sftpRowCount counts userID's rows in one SFTP table.
func sftpRowCount(t *testing.T, s *Store, table string, userID int64) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM `+table+` WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestDeleteUser_RemovesSFTPCredentials checks that deleting an account removes its SFTP
// keys, password and sessions, and leaves another account's SFTP rows untouched.
func TestDeleteUser_RemovesSFTPCredentials(t *testing.T) {
	s := newShareLinksStore(t)
	doomed := insertTestUser(t, s, "sftp-doomed")
	kept := insertTestUser(t, s, "sftp-kept")
	seedSFTPRows(t, s, doomed, "doomed")
	seedSFTPRows(t, s, kept, "kept")

	if err := s.DeleteUser(context.Background(), doomed); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	for _, table := range []string{"sftp_ssh_keys", "sftp_passwords", "sftp_sessions"} {
		if n := sftpRowCount(t, s, table, doomed); n != 0 {
			t.Errorf("%s: %d rows left for deleted user, want 0", table, n)
		}
		if n := sftpRowCount(t, s, table, kept); n != 1 {
			t.Errorf("%s: %d rows for other user, want 1", table, n)
		}
	}
}
