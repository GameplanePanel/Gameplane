package db

import (
	"database/sql"
	"testing"
	"time"
)

// checkSFTPMigration seeds a pre-024 database, runs migration 024 and checks
// that the SFTP tables accept rows, enforce their unique keys and that the
// audit_events.source column is added without touching existing rows.
func checkSFTPMigration(t *testing.T, s *Store) {
	t.Helper()
	ctx := t.Context()
	migrateBefore(t, s, "024")

	var userID int64
	if err := s.DB.QueryRowContext(ctx,
		`INSERT INTO users(username, role) VALUES (?, ?) RETURNING id`, "sftp-mig-user", "operator").Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	ts := time.Now().UTC().Format(time.RFC3339)
	var oldAuditID int64
	if err := s.DB.QueryRowContext(ctx,
		`INSERT INTO audit_events(ts, actor, method, path, status) VALUES (?, ?, ?, ?, ?) RETURNING id`,
		ts, "sftp-mig-actor", "GET", "/api/v1/servers", 200).Scan(&oldAuditID); err != nil {
		t.Fatalf("insert pre-024 audit row: %v", err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var applied int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, "024_sftp_credentials.sql").Scan(&applied); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("024_sftp_credentials.sql recorded %d times, want 1", applied)
	}

	var oldSource sql.NullString
	if err := s.DB.QueryRowContext(ctx,
		`SELECT source FROM audit_events WHERE id = ?`, oldAuditID).Scan(&oldSource); err != nil {
		t.Fatalf("read pre-024 audit source: %v", err)
	}
	if oldSource.Valid {
		t.Fatalf("pre-024 audit row source = %q, want NULL", oldSource.String)
	}
	var newAuditID int64
	if err := s.DB.QueryRowContext(ctx,
		`INSERT INTO audit_events(ts, actor, method, path, status, source) VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		ts, "sftp-mig-actor", "GET", "/sftp/v1/login", 200, "sftp").Scan(&newAuditID); err != nil {
		t.Fatalf("insert sftp audit row: %v", err)
	}
	var newSource string
	if err := s.DB.QueryRowContext(ctx,
		`SELECT source FROM audit_events WHERE id = ?`, newAuditID).Scan(&newSource); err != nil {
		t.Fatalf("read sftp audit source: %v", err)
	}
	if newSource != "sftp" {
		t.Fatalf("sftp audit source = %q, want sftp", newSource)
	}

	const fingerprint = "SHA256:sftp-mig-fp"
	insertKey := func(id int64, fp string) error {
		_, err := s.DB.ExecContext(ctx,
			`INSERT INTO sftp_ssh_keys(id, user_id, name, public_key, fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			id, userID, "laptop", "ssh-ed25519 AAAA", fp, ts)
		return err
	}
	if err := insertKey(9101, fingerprint); err != nil {
		t.Fatalf("insert sftp key: %v", err)
	}
	if err := insertKey(9102, fingerprint); err == nil {
		t.Fatal("second sftp key with the same fingerprint accepted, want UNIQUE violation")
	}

	insertPassword := func() error {
		_, err := s.DB.ExecContext(ctx,
			`INSERT INTO sftp_passwords(user_id, hash, length, created_at) VALUES (?, ?, ?, ?)`,
			userID, "argon2id-hash", 24, ts)
		return err
	}
	if err := insertPassword(); err != nil {
		t.Fatalf("insert sftp password: %v", err)
	}
	if err := insertPassword(); err == nil {
		t.Fatal("second sftp password for one user accepted, want primary key violation")
	}

	insertToken := func(id int64, digest string) error {
		_, err := s.DB.ExecContext(ctx,
			`INSERT INTO sftp_service_tokens(id, cluster, digest, prefix, created_at) VALUES (?, ?, ?, ?, ?)`,
			id, "local", digest, "gpsvc_", ts)
		return err
	}
	if err := insertToken(9201, "digest-svc"); err != nil {
		t.Fatalf("insert service token: %v", err)
	}
	if err := insertToken(9202, "digest-svc"); err == nil {
		t.Fatal("second service token with the same digest accepted, want UNIQUE violation")
	}

	insertSession := func(id int64, digest string) error {
		_, err := s.DB.ExecContext(ctx,
			`INSERT INTO sftp_sessions(id, user_id, cluster, credential, digest, session_epoch, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, userID, "local", "password", digest, 0, ts)
		return err
	}
	if err := insertSession(9301, "digest-sess"); err != nil {
		t.Fatalf("insert sftp session: %v", err)
	}
	if err := insertSession(9302, "digest-sess"); err == nil {
		t.Fatal("second sftp session with the same digest accepted, want UNIQUE violation")
	}
}

// TestMigration024_SFTP_SQLite runs the SFTP migration checks on SQLite.
func TestMigration024_SFTP_SQLite(t *testing.T) {
	s, err := Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	checkSFTPMigration(t, s)
}
