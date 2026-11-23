package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

const (
	// SFTPMaxKeysPerUser is the number of SFTP public keys one user may register.
	SFTPMaxKeysPerUser = 20
	// SFTPMaxActiveServiceTokens is the number of unrevoked SFTP service tokens one cluster may hold.
	SFTPMaxActiveServiceTokens = 2
	sftpCredentialPassword     = "password" // sftp_sessions.credential; keys use "key:<id>"
	sftpMaxCheckDigests        = 500
)

var (
	// ErrSFTPNotFound is returned when an SFTP key, token or session does not exist.
	ErrSFTPNotFound = errors.New("sftp: not found")
	// ErrSFTPKeyLimit is returned when a user already holds SFTPMaxKeysPerUser keys.
	ErrSFTPKeyLimit = errors.New("sftp: too many keys")
	// ErrSFTPKeyNameInUse is returned when a user already has a key with the same name.
	ErrSFTPKeyNameInUse = errors.New("sftp: key name already in use")
	// ErrSFTPKeyDuplicate is returned when the same public key is already registered.
	ErrSFTPKeyDuplicate = errors.New("sftp: key already registered")
	// ErrSFTPServiceTokenLimit is returned when a cluster already holds SFTPMaxActiveServiceTokens tokens.
	ErrSFTPServiceTokenLimit = errors.New("sftp: too many active service tokens")
)

// SFTPKey is the listed view of an SFTP public key.
type SFTPKey struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	PublicKey   string     `json:"publicKey"`
	Fingerprint string     `json:"fingerprint"`
	CreatedAt   time.Time  `json:"createdAt"`
	LastUsedAt  *time.Time `json:"lastUsedAt"`
}

// SFTPPasswordInfo is the display view of a user's SFTP password. The hash is never part of it.
type SFTPPasswordInfo struct {
	Length     int        `json:"length"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

// SFTPServiceToken is the listed view of an SFTP service token. The secret is never part of it.
type SFTPServiceToken struct {
	ID         int64      `json:"id"`
	Cluster    string     `json:"cluster"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

// SFTPSession is a stored SFTP session row.
type SFTPSession struct {
	ID           int64
	UserID       int64
	Cluster      string
	Credential   string
	SessionEpoch int64
	ExpiresAt    time.Time
}

// sftpLastTouch records the last last_used_at write per SFTP credential key.
var sftpLastTouch sync.Map

// sftpRowQuerier is satisfied by *sql.DB and *sql.Tx.
type sftpRowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// newRandomID returns a random positive 53-bit integer for SFTP row ids.
func newRandomID() int64 { return newMCPTokenID() }

// sftpNow returns the current time in the RFC 3339 UTC form the tables store.
func sftpNow() string { return time.Now().UTC().Format(time.RFC3339) }

// parseSFTPTime parses a stored RFC 3339 timestamp.
func parseSFTPTime(v string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse sftp time: %w", err)
	}
	return t, nil
}

// sftpExists reports whether the query, which must select one row, returns a row.
func sftpExists(ctx context.Context, q sftpRowQuerier, query string, args ...any) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// sftpTouchDue reports whether a last_used_at write for key is due, at most once per lastUsedInterval.
func sftpTouchDue(key string) bool {
	now := time.Now()
	if v, ok := sftpLastTouch.Load(key); ok {
		if last, isTime := v.(time.Time); isTime && now.Sub(last) < lastUsedInterval {
			return false
		}
	}
	sftpLastTouch.Store(key, now)
	return true
}

// InsertSFTPKey stores a normalized key for userID. One transaction checks the 20-key limit,
// the per-user name uniqueness and the global fingerprint uniqueness.
func (s *SessionStore) InsertSFTPKey(ctx context.Context, userID int64, name, publicKey, fingerprint string) (SFTPKey, error) {
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return SFTPKey{}, fmt.Errorf("insert sftp key: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sftp_ssh_keys WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return SFTPKey{}, fmt.Errorf("insert sftp key: count: %w", err)
	}
	if count >= SFTPMaxKeysPerUser {
		return SFTPKey{}, ErrSFTPKeyLimit
	}
	dup, err := sftpExists(ctx, tx, `SELECT 1 FROM sftp_ssh_keys WHERE fingerprint = ?`, fingerprint)
	if err != nil {
		return SFTPKey{}, fmt.Errorf("insert sftp key: fingerprint: %w", err)
	}
	if dup {
		return SFTPKey{}, ErrSFTPKeyDuplicate
	}
	nameUsed, err := sftpExists(ctx, tx, `SELECT 1 FROM sftp_ssh_keys WHERE user_id = ? AND name = ?`, userID, name)
	if err != nil {
		return SFTPKey{}, fmt.Errorf("insert sftp key: name: %w", err)
	}
	if nameUsed {
		return SFTPKey{}, ErrSFTPKeyNameInUse
	}

	now := time.Now().UTC().Truncate(time.Second)
	key := SFTPKey{
		ID:          newRandomID(),
		Name:        name,
		PublicKey:   publicKey,
		Fingerprint: fingerprint,
		CreatedAt:   now,
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sftp_ssh_keys(id, user_id, name, public_key, fingerprint, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		key.ID, userID, name, publicKey, fingerprint, now.Format(time.RFC3339)); err != nil {
		return SFTPKey{}, fmt.Errorf("insert sftp key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SFTPKey{}, fmt.Errorf("insert sftp key: commit: %w", err)
	}
	return key, nil
}

// ListSFTPKeys returns userID's keys, newest first.
func (s *SessionStore) ListSFTPKeys(ctx context.Context, userID int64) ([]SFTPKey, error) {
	rows, err := s.db.DB.QueryContext(ctx,
		`SELECT id, name, public_key, fingerprint, created_at, last_used_at FROM sftp_ssh_keys WHERE user_id = ? ORDER BY created_at DESC, id DESC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("list sftp keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []SFTPKey{}
	for rows.Next() {
		var (
			k        SFTPKey
			created  string
			lastUsed sql.NullString
		)
		if err := rows.Scan(&k.ID, &k.Name, &k.PublicKey, &k.Fingerprint, &created, &lastUsed); err != nil {
			return nil, fmt.Errorf("scan sftp key: %w", err)
		}
		if k.CreatedAt, err = parseSFTPTime(created); err != nil {
			return nil, err
		}
		if k.LastUsedAt, err = parseOptionalTime(lastUsed); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sftp keys: %w", err)
	}
	return out, nil
}

// DeleteSFTPKey removes a key owned by userID and, in the same transaction, revokes that
// user's sessions with credential "key:<id>". ErrSFTPNotFound when no such key.
func (s *SessionStore) DeleteSFTPKey(ctx context.Context, userID, keyID int64) error {
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete sftp key: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `DELETE FROM sftp_ssh_keys WHERE id = ? AND user_id = ?`, keyID, userID)
	if err != nil {
		return fmt.Errorf("delete sftp key: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete sftp key rows: %w", err)
	}
	if n == 0 {
		return ErrSFTPNotFound
	}
	if err := s.RevokeSFTPSessions(ctx, tx, userID, fmt.Sprintf("key:%d", keyID)); err != nil {
		return fmt.Errorf("delete sftp key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete sftp key: commit: %w", err)
	}
	return nil
}

// FindSFTPKey resolves a fingerprint to its row (login by public key).
func (s *SessionStore) FindSFTPKey(ctx context.Context, fingerprint string) (userID, keyID int64, err error) {
	err = s.db.DB.QueryRowContext(ctx,
		`SELECT user_id, id FROM sftp_ssh_keys WHERE fingerprint = ?`, fingerprint,
	).Scan(&userID, &keyID)
	if err != nil {
		return 0, 0, fmt.Errorf("find sftp key: %w", err)
	}
	return userID, keyID, nil
}

// SetSFTPPassword replaces userID's password row and revokes their "password" sessions, in one tx.
func (s *SessionStore) SetSFTPPassword(ctx context.Context, userID int64, hash string, length int) error {
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set sftp password: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sftp_passwords(user_id, hash, length, created_at) VALUES (?, ?, ?, ?) ON CONFLICT (user_id) DO UPDATE SET hash = excluded.hash, length = excluded.length, created_at = excluded.created_at, last_used_at = NULL`,
		userID, hash, length, sftpNow()); err != nil {
		return fmt.Errorf("set sftp password: %w", err)
	}
	if err := s.RevokeSFTPSessions(ctx, tx, userID, sftpCredentialPassword); err != nil {
		return fmt.Errorf("set sftp password: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set sftp password: commit: %w", err)
	}
	return nil
}

// GetSFTPPasswordHash returns the stored hash. found=false when none.
func (s *SessionStore) GetSFTPPasswordHash(ctx context.Context, userID int64) (hash string, found bool, err error) {
	err = s.db.DB.QueryRowContext(ctx, `SELECT hash FROM sftp_passwords WHERE user_id = ?`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get sftp password hash: %w", err)
	}
	return hash, true, nil
}

// GetSFTPPasswordInfo is the display view (no hash).
func (s *SessionStore) GetSFTPPasswordInfo(ctx context.Context, userID int64) (SFTPPasswordInfo, bool, error) {
	var (
		info     SFTPPasswordInfo
		created  string
		lastUsed sql.NullString
	)
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT length, created_at, last_used_at FROM sftp_passwords WHERE user_id = ?`, userID,
	).Scan(&info.Length, &created, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return SFTPPasswordInfo{}, false, nil
	}
	if err != nil {
		return SFTPPasswordInfo{}, false, fmt.Errorf("get sftp password info: %w", err)
	}
	if info.CreatedAt, err = parseSFTPTime(created); err != nil {
		return SFTPPasswordInfo{}, false, err
	}
	if info.LastUsedAt, err = parseOptionalTime(lastUsed); err != nil {
		return SFTPPasswordInfo{}, false, err
	}
	return info, true, nil
}

// SFTPUsername returns the username of userID for the admin SFTP routes. The wrapped error is
// sql.ErrNoRows when no such user exists.
func (s *SessionStore) SFTPUsername(ctx context.Context, userID int64) (string, error) {
	var name string
	if err := s.db.DB.QueryRowContext(ctx, `SELECT username FROM users WHERE id = ?`, userID).Scan(&name); err != nil {
		return "", fmt.Errorf("sftp username: %w", err)
	}
	return name, nil
}

// DeleteSFTPPassword deletes the row and revokes "password" sessions. ErrSFTPNotFound if none.
func (s *SessionStore) DeleteSFTPPassword(ctx context.Context, userID int64) error {
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete sftp password: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `DELETE FROM sftp_passwords WHERE user_id = ?`, userID)
	if err != nil {
		return fmt.Errorf("delete sftp password: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete sftp password rows: %w", err)
	}
	if n == 0 {
		return ErrSFTPNotFound
	}
	if err := s.RevokeSFTPSessions(ctx, tx, userID, sftpCredentialPassword); err != nil {
		return fmt.Errorf("delete sftp password: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete sftp password: commit: %w", err)
	}
	return nil
}

// RevokeSFTPSessions marks sessions revoked_at=now for userID; credential "" = all credentials.
// ex lets callers run it inside their own transaction (db.Execer).
func (s *SessionStore) RevokeSFTPSessions(ctx context.Context, ex db.Execer, userID int64, credential string) error {
	query := `UPDATE sftp_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`
	args := []any{sftpNow(), userID}
	if credential != "" {
		query += ` AND credential = ?`
		args = append(args, credential)
	}
	if _, err := ex.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("revoke sftp sessions: %w", err)
	}
	return nil
}

// TouchSFTPCredential writes last_used_at on the key row or password row at most once a minute
// per credential (in-process sync.Map like mcpLastTouch, mcptokens.go:79). Errors are logged only.
func (s *SessionStore) TouchSFTPCredential(ctx context.Context, userID int64, credential string) {
	if !sftpTouchDue(fmt.Sprintf("cred/%d/%s", userID, credential)) {
		return
	}
	now := sftpNow()
	var (
		query string
		args  []any
	)
	if credential == sftpCredentialPassword {
		query, args = `UPDATE sftp_passwords SET last_used_at = ? WHERE user_id = ?`, []any{now, userID}
	} else if keyPart, ok := strings.CutPrefix(credential, "key:"); ok {
		keyID, err := strconv.ParseInt(keyPart, 10, 64)
		if err != nil {
			slog.Warn("sftp touch credential", "err", err)
			return
		}
		query, args = `UPDATE sftp_ssh_keys SET last_used_at = ? WHERE id = ? AND user_id = ?`, []any{now, keyID, userID}
	} else {
		return
	}
	if _, err := s.db.DB.ExecContext(ctx, query, args...); err != nil {
		slog.Warn("sftp touch credential", "err", err)
	}
}

// InsertSFTPServiceToken stores a service token digest for cluster. The transaction refuses
// a third active token for the same cluster with ErrSFTPServiceTokenLimit.
func (s *SessionStore) InsertSFTPServiceToken(ctx context.Context, cluster, digest, prefix string) (SFTPServiceToken, error) {
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return SFTPServiceToken{}, fmt.Errorf("insert sftp service token: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var active int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sftp_service_tokens WHERE cluster = ? AND revoked_at IS NULL`, cluster,
	).Scan(&active); err != nil {
		return SFTPServiceToken{}, fmt.Errorf("insert sftp service token: count: %w", err)
	}
	if active >= SFTPMaxActiveServiceTokens {
		return SFTPServiceToken{}, ErrSFTPServiceTokenLimit
	}
	now := time.Now().UTC().Truncate(time.Second)
	tok := SFTPServiceToken{
		ID:        newRandomID(),
		Cluster:   cluster,
		Prefix:    prefix,
		CreatedAt: now,
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sftp_service_tokens(id, cluster, digest, prefix, created_at) VALUES (?, ?, ?, ?, ?)`,
		tok.ID, cluster, digest, prefix, now.Format(time.RFC3339)); err != nil {
		return SFTPServiceToken{}, fmt.Errorf("insert sftp service token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SFTPServiceToken{}, fmt.Errorf("insert sftp service token: commit: %w", err)
	}
	return tok, nil
}

// ListSFTPServiceTokens returns the cluster's service tokens, newest first, revoked ones included
// until the sweeper drops them.
func (s *SessionStore) ListSFTPServiceTokens(ctx context.Context, cluster string) ([]SFTPServiceToken, error) {
	rows, err := s.db.DB.QueryContext(ctx,
		`SELECT id, cluster, prefix, created_at, last_used_at, revoked_at FROM sftp_service_tokens WHERE cluster = ? ORDER BY created_at DESC, id DESC`,
		cluster)
	if err != nil {
		return nil, fmt.Errorf("list sftp service tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []SFTPServiceToken{}
	for rows.Next() {
		var (
			t        SFTPServiceToken
			created  string
			lastUsed sql.NullString
			revoked  sql.NullString
		)
		if err := rows.Scan(&t.ID, &t.Cluster, &t.Prefix, &created, &lastUsed, &revoked); err != nil {
			return nil, fmt.Errorf("scan sftp service token: %w", err)
		}
		if t.CreatedAt, err = parseSFTPTime(created); err != nil {
			return nil, err
		}
		if t.LastUsedAt, err = parseOptionalTime(lastUsed); err != nil {
			return nil, err
		}
		if t.RevokedAt, err = parseOptionalTime(revoked); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sftp service tokens: %w", err)
	}
	return out, nil
}

// RevokeSFTPServiceToken revokes an active service token of cluster. ErrSFTPNotFound otherwise.
func (s *SessionStore) RevokeSFTPServiceToken(ctx context.Context, cluster string, id int64) error {
	res, err := s.db.DB.ExecContext(ctx,
		`UPDATE sftp_service_tokens SET revoked_at = ? WHERE id = ? AND cluster = ? AND revoked_at IS NULL`,
		sftpNow(), id, cluster)
	if err != nil {
		return fmt.Errorf("revoke sftp service token: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revoke sftp service token rows: %w", err)
	}
	if n == 0 {
		return ErrSFTPNotFound
	}
	return nil
}

// LookupSFTPServiceToken resolves a token digest to its row. sql.ErrNoRows when unknown or revoked.
func (s *SessionStore) LookupSFTPServiceToken(ctx context.Context, digest string) (id int64, cluster string, err error) {
	err = s.db.DB.QueryRowContext(ctx,
		`SELECT id, cluster FROM sftp_service_tokens WHERE digest = ? AND revoked_at IS NULL`, digest,
	).Scan(&id, &cluster)
	if err != nil {
		return 0, "", fmt.Errorf("lookup sftp service token: %w", err)
	}
	return id, cluster, nil
}

// TouchSFTPServiceToken writes last_used_at for a service token at most once a minute.
// Errors are logged only.
func (s *SessionStore) TouchSFTPServiceToken(ctx context.Context, id int64) {
	if !sftpTouchDue(fmt.Sprintf("svc/%d", id)) {
		return
	}
	if _, err := s.db.DB.ExecContext(ctx,
		`UPDATE sftp_service_tokens SET last_used_at = ? WHERE id = ?`, sftpNow(), id); err != nil {
		slog.Warn("sftp touch service token", "err", err)
	}
}

// InsertSFTPSession stores a session row (digest only) and returns its id.
func (s *SessionStore) InsertSFTPSession(ctx context.Context, userID int64, cluster, credential, digest string, epoch int64, expires time.Time) (int64, error) {
	id := newRandomID()
	if _, err := s.db.DB.ExecContext(ctx,
		`INSERT INTO sftp_sessions(id, user_id, cluster, credential, digest, session_epoch, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, userID, cluster, credential, digest, epoch, expires.UTC().Format(time.RFC3339)); err != nil {
		return 0, fmt.Errorf("insert sftp session: %w", err)
	}
	return id, nil
}

// LookupSFTPSession joins users and requires revoked_at IS NULL, session_epoch = users.session_epoch
// and not expired. sql.ErrNoRows / errSessionExpired / errSessionInvalid like lookupMCPToken (mcptokens.go:294).
func (s *SessionStore) LookupSFTPSession(ctx context.Context, digest string) (*User, SFTPSession, error) {
	var (
		u       User
		sess    SFTPSession
		expires string
	)
	err := s.db.DB.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.cluster, s.credential, s.session_epoch, s.expires_at,
		       u.username, u.display_name, u.email, u.role
		FROM sftp_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.digest = ? AND s.revoked_at IS NULL AND s.session_epoch = u.session_epoch`,
		digest,
	).Scan(&sess.ID, &sess.UserID, &sess.Cluster, &sess.Credential, &sess.SessionEpoch, &expires,
		&u.Username, &u.DisplayName, &u.Email, &u.Role)
	if err != nil {
		return nil, SFTPSession{}, err
	}
	u.ID = sess.UserID
	exp, perr := parseSFTPTime(expires)
	if perr != nil {
		slog.Warn("sftp session expires_at parse", "err", perr)
		return nil, SFTPSession{}, errSessionInvalid
	}
	sess.ExpiresAt = exp
	if time.Now().After(exp) {
		return nil, SFTPSession{}, errSessionExpired
	}
	perms, err := s.LoadPerms(ctx, u.ID)
	if err != nil {
		return nil, SFTPSession{}, err
	}
	u.Perms = perms
	return &u, sess, nil
}

// SlideSFTPSession sets expires_at on a live session (RevokedAt must be NULL). Callers throttle to once a minute.
func (s *SessionStore) SlideSFTPSession(ctx context.Context, id int64, expires time.Time) error {
	if _, err := s.db.DB.ExecContext(ctx,
		`UPDATE sftp_sessions SET expires_at = ? WHERE id = ? AND revoked_at IS NULL`,
		expires.UTC().Format(time.RFC3339), id); err != nil {
		return fmt.Errorf("slide sftp session: %w", err)
	}
	return nil
}

// CheckSFTPSessions returns the digests among digests (max 500) that are no longer valid for cluster:
// missing, other cluster, revoked, expired or epoch-mismatched. Build the IN list with "?" repeated.
func (s *SessionStore) CheckSFTPSessions(ctx context.Context, cluster string, digests []string) ([]string, error) {
	revoked := []string{}
	if len(digests) == 0 {
		return revoked, nil
	}
	if len(digests) > sftpMaxCheckDigests {
		return nil, fmt.Errorf("check sftp sessions: %d digests exceeds %d", len(digests), sftpMaxCheckDigests)
	}
	// One digest per query, all inside one transaction so the checks share a snapshot.
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("check sftp sessions: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now()
	for _, d := range digests {
		live, err := sftpSessionLive(ctx, tx, cluster, d, now)
		if err != nil {
			return nil, err
		}
		if !live {
			revoked = append(revoked, d)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("check sftp sessions: commit: %w", err)
	}
	return revoked, nil
}

// sftpSessionLive reports whether digest names an unrevoked, unexpired session on cluster whose
// user's session_epoch still matches.
func sftpSessionLive(ctx context.Context, tx *sql.Tx, cluster, digest string, now time.Time) (bool, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT s.expires_at
		FROM sftp_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.cluster = ? AND s.revoked_at IS NULL AND s.session_epoch = u.session_epoch
		  AND s.digest = ?`, cluster, digest)
	if err != nil {
		return false, fmt.Errorf("check sftp sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	live := false
	for rows.Next() {
		var expires string
		if err := rows.Scan(&expires); err != nil {
			return false, fmt.Errorf("scan sftp session: %w", err)
		}
		exp, perr := parseSFTPTime(expires)
		if perr != nil {
			slog.Warn("sftp session expires_at parse", "err", perr)
			continue
		}
		if now.Before(exp) {
			live = true
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("iterate sftp sessions: %w", err)
	}
	return live, nil
}
