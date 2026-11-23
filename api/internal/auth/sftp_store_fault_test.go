package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// sftpCloseDB is a pseudo-statement that closes the store's database, so every later query fails.
const sftpCloseDB = "CLOSE"

// sftpFault returns a store with one user (alice) after applying stmts. "{uid}" in a statement is
// replaced by alice's id; sftpCloseDB closes the database.
func sftpFault(ctx context.Context, t *testing.T, stmts ...string) (*SessionStore, int64) {
	t.Helper()
	st, err := db.Open(ctx, "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	SetFastHashParams(t)
	hash, err := HashPassword(ctx, "hunter2")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := st.DB.ExecContext(ctx,
		`INSERT INTO users(username, display_name, email, role, pw_hash) VALUES (?,?,?,?,?)`,
		"alice", "alice", "alice@example.com", "admin", hash); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	s := NewSessionStore(st)
	u, _, err := s.SFTPUserByName(ctx, "alice")
	if err != nil {
		t.Fatalf("SFTPUserByName: %v", err)
	}
	uid := u.ID
	for _, q := range stmts {
		if q == sftpCloseDB {
			if err := st.DB.Close(); err != nil {
				t.Fatalf("close db: %v", err)
			}
			continue
		}
		q = strings.ReplaceAll(q, "{uid}", fmt.Sprint(uid))
		if _, err := st.DB.ExecContext(ctx, q); err != nil {
			t.Fatalf("setup %q: %v", q, err)
		}
	}
	return s, uid
}

const (
	sftpDropKeys      = `DROP TABLE sftp_ssh_keys`
	sftpDropPwds      = `DROP TABLE sftp_passwords`
	sftpDropTokens    = `DROP TABLE sftp_service_tokens`
	sftpDropSessions  = `DROP TABLE sftp_sessions`
	sftpKeyRow        = `INSERT INTO sftp_ssh_keys(id, user_id, name, public_key, fingerprint, created_at) VALUES (11, {uid}, 'k', 'pk', 'fp', '2020-01-01T00:00:00Z')`
	sftpKeyBadID      = `INSERT INTO sftp_ssh_keys(id, user_id, name, public_key, fingerprint, created_at) VALUES ('abc', {uid}, 'k', 'pk', 'fp', '2020-01-01T00:00:00Z')`
	sftpKeyBadCreate  = `INSERT INTO sftp_ssh_keys(id, user_id, name, public_key, fingerprint, created_at) VALUES (11, {uid}, 'k', 'pk', 'fp', 'bad')`
	sftpKeyBadUsed    = `INSERT INTO sftp_ssh_keys(id, user_id, name, public_key, fingerprint, created_at, last_used_at) VALUES (11, {uid}, 'k', 'pk', 'fp', '2020-01-01T00:00:00Z', 'bad')`
	sftpPwdRow        = `INSERT INTO sftp_passwords(user_id, hash, length, created_at) VALUES ({uid}, 'not-a-hash', 20, '2020-01-01T00:00:00Z')`
	sftpPwdBadCreate  = `INSERT INTO sftp_passwords(user_id, hash, length, created_at) VALUES ({uid}, 'h', 20, 'bad')`
	sftpPwdBadUsed    = `INSERT INTO sftp_passwords(user_id, hash, length, created_at, last_used_at) VALUES ({uid}, 'h', 20, '2020-01-01T00:00:00Z', 'bad')`
	sftpTokBadID      = `INSERT INTO sftp_service_tokens(id, cluster, digest, prefix, created_at) VALUES ('abc', 'local', 'd', 'p', '2020-01-01T00:00:00Z')`
	sftpTokBadCreate  = `INSERT INTO sftp_service_tokens(id, cluster, digest, prefix, created_at) VALUES (5, 'local', 'd', 'p', 'bad')`
	sftpTokBadUsed    = `INSERT INTO sftp_service_tokens(id, cluster, digest, prefix, created_at, last_used_at) VALUES (5, 'local', 'd', 'p', '2020-01-01T00:00:00Z', 'bad')`
	sftpTokBadRevoke  = `INSERT INTO sftp_service_tokens(id, cluster, digest, prefix, created_at, revoked_at) VALUES (5, 'local', 'd', 'p', '2020-01-01T00:00:00Z', 'bad')`
	sftpKeyInsertBoom = `CREATE TRIGGER sftp_key_boom BEFORE INSERT ON sftp_ssh_keys BEGIN SELECT RAISE(ABORT, 'boom'); END`
	sftpTokInsertBoom = `CREATE TRIGGER sftp_tok_boom BEFORE INSERT ON sftp_service_tokens BEGIN SELECT RAISE(ABORT, 'boom'); END`
)

func TestSFTPStore_DatabaseFailuresSurface(t *testing.T) {
	cases := []struct {
		name  string
		stmts []string
		call  func(ctx context.Context, s *SessionStore, uid int64) error
	}{
		{"insert key begin", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.InsertSFTPKey(ctx, uid, "n", "pk", "fp")
			return err
		}},
		{"insert key count", []string{sftpDropKeys}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.InsertSFTPKey(ctx, uid, "n", "pk", "fp")
			return err
		}},
		{"insert key fingerprint query", []string{`ALTER TABLE sftp_ssh_keys RENAME COLUMN fingerprint TO fp`}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.InsertSFTPKey(ctx, uid, "n", "pk", "fp")
			return err
		}},
		{"insert key name query", []string{`ALTER TABLE sftp_ssh_keys RENAME COLUMN name TO nm`}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.InsertSFTPKey(ctx, uid, "n", "pk", "fp")
			return err
		}},
		{"insert key exec", []string{sftpKeyInsertBoom}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.InsertSFTPKey(ctx, uid, "n", "pk", "fp")
			return err
		}},
		{"list keys query", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.ListSFTPKeys(ctx, uid)
			return err
		}},
		{"list keys scan", []string{sftpKeyBadID}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.ListSFTPKeys(ctx, uid)
			return err
		}},
		{"list keys created_at", []string{sftpKeyBadCreate}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.ListSFTPKeys(ctx, uid)
			return err
		}},
		{"list keys last_used_at", []string{sftpKeyBadUsed}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.ListSFTPKeys(ctx, uid)
			return err
		}},
		{"delete key begin", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.DeleteSFTPKey(ctx, uid, 11)
		}},
		{"delete key exec", []string{sftpDropKeys}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.DeleteSFTPKey(ctx, uid, 11)
		}},
		{"delete key revoke", []string{sftpKeyRow, sftpDropSessions}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.DeleteSFTPKey(ctx, uid, 11)
		}},
		{"find key", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, _, err := s.FindSFTPKey(ctx, "fp")
			return err
		}},
		{"set password begin", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.SetSFTPPassword(ctx, uid, "h", 20)
		}},
		{"set password exec", []string{sftpDropPwds}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.SetSFTPPassword(ctx, uid, "h", 20)
		}},
		{"set password revoke", []string{sftpDropSessions}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.SetSFTPPassword(ctx, uid, "h", 20)
		}},
		{"password hash query", []string{sftpDropPwds}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, _, err := s.GetSFTPPasswordHash(ctx, uid)
			return err
		}},
		{"password info query", []string{sftpDropPwds}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, _, err := s.GetSFTPPasswordInfo(ctx, uid)
			return err
		}},
		{"password info created_at", []string{sftpPwdBadCreate}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, _, err := s.GetSFTPPasswordInfo(ctx, uid)
			return err
		}},
		{"password info last_used_at", []string{sftpPwdBadUsed}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, _, err := s.GetSFTPPasswordInfo(ctx, uid)
			return err
		}},
		{"delete password begin", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.DeleteSFTPPassword(ctx, uid)
		}},
		{"delete password exec", []string{sftpDropPwds}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.DeleteSFTPPassword(ctx, uid)
		}},
		{"delete password revoke", []string{sftpPwdRow, sftpDropSessions}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.DeleteSFTPPassword(ctx, uid)
		}},
		{"revoke sessions exec", []string{sftpDropSessions}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.RevokeSFTPSessions(ctx, s.db.DB, uid, "password")
		}},
		{"revoke session token exec", []string{sftpDropSessions}, func(ctx context.Context, s *SessionStore, _ int64) error {
			return s.RevokeSFTPSessionToken(ctx, "gpsftp_"+strings.Repeat("a", sftpTokenBodyLen))
		}},
		{"delete user sessions revokes sftp", []string{sftpDropSessions}, func(ctx context.Context, s *SessionStore, uid int64) error {
			return s.DeleteForUserWith(ctx, s.db.DB, uid)
		}},
		{"insert token begin", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.InsertSFTPServiceToken(ctx, "local", "d", "p")
			return err
		}},
		{"insert token count", []string{sftpDropTokens}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.InsertSFTPServiceToken(ctx, "local", "d", "p")
			return err
		}},
		{"insert token exec", []string{sftpTokInsertBoom}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.InsertSFTPServiceToken(ctx, "local", "d", "p")
			return err
		}},
		{"create token", []string{sftpDropTokens}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, _, err := s.CreateSFTPServiceToken(ctx, "local")
			return err
		}},
		{"list tokens query", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.ListSFTPServiceTokens(ctx, "local")
			return err
		}},
		{"list tokens scan", []string{sftpTokBadID}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.ListSFTPServiceTokens(ctx, "local")
			return err
		}},
		{"list tokens created_at", []string{sftpTokBadCreate}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.ListSFTPServiceTokens(ctx, "local")
			return err
		}},
		{"list tokens last_used_at", []string{sftpTokBadUsed}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.ListSFTPServiceTokens(ctx, "local")
			return err
		}},
		{"list tokens revoked_at", []string{sftpTokBadRevoke}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.ListSFTPServiceTokens(ctx, "local")
			return err
		}},
		{"revoke token exec", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, _ int64) error {
			return s.RevokeSFTPServiceToken(ctx, "local", 5)
		}},
		{"lookup token", []string{sftpDropTokens}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, _, err := s.LookupSFTPServiceToken(ctx, "d")
			return err
		}},
		{"authenticate service", []string{sftpDropTokens}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, _, err := s.AuthenticateSFTPService(ctx, SFTPServicePrefix+strings.Repeat("a", sftpTokenBodyLen))
			return err
		}},
		{"insert session", []string{sftpDropSessions}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.InsertSFTPSession(ctx, uid, "local", "password", "d", 0, time.Now().Add(time.Hour))
			return err
		}},
		{"issue session", []string{sftpDropSessions}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, _, err := s.IssueSFTPSession(ctx, uid, "local", "password", 0)
			return err
		}},
		{"issue session empty credential", nil, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, _, err := s.IssueSFTPSession(ctx, uid, "local", "", 0)
			return err
		}},
		{"lookup session query", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, _, err := s.LookupSFTPSession(ctx, "d")
			return err
		}},
		{"slide session exec", []string{sftpDropSessions}, func(ctx context.Context, s *SessionStore, _ int64) error {
			return s.SlideSFTPSession(ctx, 1, time.Now())
		}},
		{"check sessions begin", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.CheckSFTPSessions(ctx, "local", []string{"d"})
			return err
		}},
		{"check sessions query", []string{sftpDropSessions}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.CheckSFTPSessions(ctx, "local", []string{"d"})
			return err
		}},
		{"check sessions too many", nil, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, err := s.CheckSFTPSessions(ctx, "local", make([]string, sftpMaxCheckDigests+1))
			return err
		}},
		{"user by name perms", []string{`DROP TABLE user_role_bindings`}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, _, err := s.SFTPUserByName(ctx, "alice")
			return err
		}},
		{"user by name unknown", nil, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, _, err := s.SFTPUserByName(ctx, "nobody")
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("want sql.ErrNoRows, got %w", err)
			}
			return err
		}},
		{"verify password query", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, _, _, err := s.VerifySFTPPassword(ctx, "alice", "pw")
			return err
		}},
		{"verify password malformed hash", []string{sftpPwdRow}, func(ctx context.Context, s *SessionStore, _ int64) error {
			_, _, _, err := s.VerifySFTPPassword(ctx, "alice", "pw")
			return err
		}},
		{"generate password store", []string{sftpCloseDB}, func(ctx context.Context, s *SessionStore, uid int64) error {
			_, err := s.GenerateAndStoreSFTPPassword(ctx, uid, SFTPPasswordMinLen)
			return err
		}},
		{"generate password hash", nil, func(ctx context.Context, s *SessionStore, uid int64) error {
			cancelled, cancel := context.WithCancel(ctx)
			cancel()
			_, err := s.GenerateAndStoreSFTPPassword(cancelled, uid, SFTPPasswordMinLen)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, uid := sftpFault(t.Context(), t, tc.stmts...)
			if err := tc.call(t.Context(), s, uid); err == nil {
				t.Fatal("call succeeded, want an error")
			}
		})
	}
}

func TestSFTPStore_TouchCredential(t *testing.T) {
	ctx := t.Context()
	s, uid := sftpFault(t.Context(), t)
	line, _ := sftpAuthorizedKey(t, "ed25519")
	key, err := s.AddSFTPKey(ctx, uid, "laptop", line)
	if err != nil {
		t.Fatalf("AddSFTPKey: %v", err)
	}
	if _, err := s.GenerateAndStoreSFTPPassword(ctx, uid, SFTPPasswordMinLen); err != nil {
		t.Fatalf("GenerateAndStoreSFTPPassword: %v", err)
	}
	keyCred := fmt.Sprintf("key:%d", key.ID)
	for _, cred := range []string{keyCred, sftpCredentialPassword, "key:not-a-number", "mystery"} {
		sftpLastTouch.Delete(fmt.Sprintf("cred/%d/%s", uid, cred))
		s.TouchSFTPCredential(ctx, uid, cred)
	}
	keys, err := s.ListSFTPKeys(ctx, uid)
	if err != nil || len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatalf("key last_used_at not written: %+v, %v", keys, err)
	}
	info, found, err := s.GetSFTPPasswordInfo(ctx, uid)
	if err != nil || !found || info.LastUsedAt == nil {
		t.Fatalf("password last_used_at not written: %+v found=%v err=%v", info, found, err)
	}

	// A second touch inside the interval writes nothing.
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE sftp_ssh_keys SET last_used_at = NULL WHERE id = ?`, key.ID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	s.TouchSFTPCredential(ctx, uid, keyCred)
	keys, err = s.ListSFTPKeys(ctx, uid)
	if err != nil || keys[0].LastUsedAt != nil {
		t.Fatalf("throttled touch wrote last_used_at: %+v, %v", keys, err)
	}

	// A failing write is logged, never returned or panicked on.
	if _, err := s.db.DB.ExecContext(ctx, sftpDropPwds); err != nil {
		t.Fatalf("drop: %v", err)
	}
	sftpLastTouch.Delete(fmt.Sprintf("cred/%d/%s", uid, sftpCredentialPassword))
	s.TouchSFTPCredential(ctx, uid, sftpCredentialPassword)
}

func TestSFTPStore_TouchServiceToken(t *testing.T) {
	ctx := t.Context()
	s, _ := sftpFault(t.Context(), t)
	tok, _, err := s.CreateSFTPServiceToken(ctx, "local")
	if err != nil {
		t.Fatalf("CreateSFTPServiceToken: %v", err)
	}
	s.TouchSFTPServiceToken(ctx, tok.ID)
	list, err := s.ListSFTPServiceTokens(ctx, "local")
	if err != nil || len(list) != 1 || list[0].LastUsedAt == nil {
		t.Fatalf("service token last_used_at not written: %+v, %v", list, err)
	}
	if _, err := s.db.DB.ExecContext(ctx, `UPDATE sftp_service_tokens SET last_used_at = NULL`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	s.TouchSFTPServiceToken(ctx, tok.ID)
	if list, _ = s.ListSFTPServiceTokens(ctx, "local"); list[0].LastUsedAt != nil {
		t.Fatal("throttled touch wrote last_used_at")
	}

	// A failing write is logged only.
	if _, err := s.db.DB.ExecContext(ctx, sftpDropTokens); err != nil {
		t.Fatalf("drop: %v", err)
	}
	s.TouchSFTPServiceToken(ctx, tok.ID+1)
}

func TestSFTPStore_ServiceTokenLimitAndRevokedLookup(t *testing.T) {
	ctx := t.Context()
	s, _ := sftpFault(t.Context(), t)
	var first SFTPServiceToken
	var firstRaw string
	for i := 0; i < SFTPMaxActiveServiceTokens; i++ {
		tok, raw, err := s.CreateSFTPServiceToken(ctx, "local")
		if err != nil {
			t.Fatalf("CreateSFTPServiceToken %d: %v", i, err)
		}
		if i == 0 {
			first, firstRaw = tok, raw
		}
	}
	if _, _, err := s.CreateSFTPServiceToken(ctx, "local"); !errors.Is(err, ErrSFTPServiceTokenLimit) {
		t.Fatalf("third token error = %v, want ErrSFTPServiceTokenLimit", err)
	}
	// Another cluster has its own allowance.
	if _, _, err := s.CreateSFTPServiceToken(ctx, "edge"); err != nil {
		t.Fatalf("other cluster token: %v", err)
	}
	if err := s.RevokeSFTPServiceToken(ctx, "local", first.ID); err != nil {
		t.Fatalf("RevokeSFTPServiceToken: %v", err)
	}
	if err := s.RevokeSFTPServiceToken(ctx, "local", first.ID); !errors.Is(err, ErrSFTPNotFound) {
		t.Fatalf("second revoke error = %v, want ErrSFTPNotFound", err)
	}
	if _, _, err := s.AuthenticateSFTPService(ctx, firstRaw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked token error = %v, want sql.ErrNoRows", err)
	}
}

func TestSFTPStore_LookupSessionRejectsBadRows(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		name    string
		mutate  func(t *testing.T, s *SessionStore)
		wantErr error
	}{
		{"unparsable expiry", func(t *testing.T, s *SessionStore) {
			t.Helper()
			mustExecSFTP(t.Context(), t, s, `UPDATE sftp_sessions SET expires_at = 'junk'`)
		}, errSessionInvalid},
		{"expired", func(t *testing.T, s *SessionStore) {
			t.Helper()
			mustExecSFTP(t.Context(), t, s, `UPDATE sftp_sessions SET expires_at = '2000-01-01T00:00:00Z'`)
		}, errSessionExpired},
		{"revoked", func(t *testing.T, s *SessionStore) {
			t.Helper()
			mustExecSFTP(t.Context(), t, s, `UPDATE sftp_sessions SET revoked_at = '2020-01-01T00:00:00Z'`)
		}, sql.ErrNoRows},
		{"epoch mismatch", func(t *testing.T, s *SessionStore) {
			t.Helper()
			mustExecSFTP(t.Context(), t, s, `UPDATE users SET session_epoch = session_epoch + 1`)
		}, sql.ErrNoRows},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, uid := sftpFault(ctx, t)
			token, _, err := s.IssueSFTPSession(ctx, uid, "local", "password", 0)
			if err != nil {
				t.Fatalf("IssueSFTPSession: %v", err)
			}
			tc.mutate(t, s)
			if _, _, err := s.LookupSFTPSessionToken(ctx, token); !errors.Is(err, tc.wantErr) {
				t.Fatalf("lookup error = %v, want %v", err, tc.wantErr)
			}
		})
	}

	t.Run("permission load failure", func(t *testing.T) {
		s, uid := sftpFault(ctx, t)
		token, _, err := s.IssueSFTPSession(ctx, uid, "local", "password", 0)
		if err != nil {
			t.Fatalf("IssueSFTPSession: %v", err)
		}
		mustExecSFTP(ctx, t, s, `DROP TABLE user_role_bindings`)
		_, _, err = s.LookupSFTPSessionToken(ctx, token)
		if err == nil || errors.Is(err, sql.ErrNoRows) || errors.Is(err, errSessionInvalid) || errors.Is(err, errSessionExpired) {
			t.Fatalf("lookup error = %v, want an unclassified database error", err)
		}
	})
}

func mustExecSFTP(ctx context.Context, t *testing.T, s *SessionStore, q string) {
	t.Helper()
	if _, err := s.db.DB.ExecContext(ctx, q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func TestSFTPStore_CheckSessionsBatch(t *testing.T) {
	ctx := t.Context()
	s, uid := sftpFault(t.Context(), t)

	if got, err := s.CheckSFTPSessions(ctx, "local", nil); err != nil || len(got) != 0 {
		t.Fatalf("empty batch = %v, %v", got, err)
	}

	live, _, err := s.IssueSFTPSession(ctx, uid, "local", "password", 0)
	if err != nil {
		t.Fatalf("live: %v", err)
	}
	otherCluster, _, err := s.IssueSFTPSession(ctx, uid, "edge", "password", 0)
	if err != nil {
		t.Fatalf("other cluster: %v", err)
	}
	revoked, _, err := s.IssueSFTPSession(ctx, uid, "local", "key:1", 0)
	if err != nil {
		t.Fatalf("revoked: %v", err)
	}
	expired, _, err := s.IssueSFTPSession(ctx, uid, "local", "key:2", 0)
	if err != nil {
		t.Fatalf("expired: %v", err)
	}
	garbled, _, err := s.IssueSFTPSession(ctx, uid, "local", "key:3", 0)
	if err != nil {
		t.Fatalf("garbled: %v", err)
	}
	mustExecSFTP(t.Context(), t, s, fmt.Sprintf(`UPDATE sftp_sessions SET revoked_at = '2020-01-01T00:00:00Z' WHERE digest = '%s'`, SFTPTokenDigest(revoked)))
	mustExecSFTP(t.Context(), t, s, fmt.Sprintf(`UPDATE sftp_sessions SET expires_at = '2000-01-01T00:00:00Z' WHERE digest = '%s'`, SFTPTokenDigest(expired)))
	mustExecSFTP(t.Context(), t, s, fmt.Sprintf(`UPDATE sftp_sessions SET expires_at = 'junk' WHERE digest = '%s'`, SFTPTokenDigest(garbled)))

	digests := []string{
		SFTPTokenDigest(live), SFTPTokenDigest(otherCluster), SFTPTokenDigest(revoked),
		SFTPTokenDigest(expired), SFTPTokenDigest(garbled), "unknown",
	}
	got, err := s.CheckSFTPSessions(ctx, "local", digests)
	if err != nil {
		t.Fatalf("CheckSFTPSessions: %v", err)
	}
	want := digests[1:]
	if len(got) != len(want) {
		t.Fatalf("invalid digests = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("invalid digests = %v, want %v", got, want)
		}
	}
}

func TestSFTPStore_RevokeAllCredentialsAndSessionToken(t *testing.T) {
	ctx := t.Context()
	s, uid := sftpFault(t.Context(), t)
	a, _, err := s.IssueSFTPSession(ctx, uid, "local", "password", 0)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, _, err := s.IssueSFTPSession(ctx, uid, "local", "key:9", 0)
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if err := s.RevokeSFTPSessionToken(ctx, a); err != nil {
		t.Fatalf("RevokeSFTPSessionToken: %v", err)
	}
	if _, _, err := s.LookupSFTPSessionToken(ctx, a); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked session lookup = %v, want sql.ErrNoRows", err)
	}
	if _, _, err := s.LookupSFTPSessionToken(ctx, b); err != nil {
		t.Fatalf("other session was revoked too: %v", err)
	}
	if err := s.DeleteForUserWith(ctx, s.db.DB, uid); err != nil {
		t.Fatalf("DeleteForUserWith: %v", err)
	}
	if _, _, err := s.LookupSFTPSessionToken(ctx, b); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("session survived DeleteForUserWith: %v", err)
	}
}

func TestSFTPStore_SlideSessionLogsFailure(t *testing.T) {
	s, _ := sftpFault(t.Context(), t, sftpDropSessions)
	// Fresh id, so the once-a-minute throttle does not skip the write that fails.
	s.slideSFTPSession(t.Context(), 987654321)
}

func TestParseSFTPPublicKey_SecurityKeyTypeRefused(t *testing.T) {
	wire := struct {
		Name        string
		KeyBytes    []byte
		Application string
	}{"sk-ssh-ed25519@openssh.com", make([]byte, 32), "ssh:"}
	line := "sk-ssh-ed25519@openssh.com " + sftpBase64(sshMarshal(wire))
	if _, err := ParseSFTPPublicKey(line); !errors.Is(err, ErrSFTPKeyType) {
		t.Fatalf("sk key error = %v, want ErrSFTPKeyType", err)
	}
}
