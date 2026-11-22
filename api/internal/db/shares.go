package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ShareLink is a signed, expiring, revocable token granting read access to a
// single GameServer's status and connection address, optionally with start capability.
type ShareLink struct {
	ID         string     // unique identifier
	Cluster    string     // cluster identifier
	Namespace  string     // Kubernetes namespace
	ServerName string     // GameServer name
	ServerUID  string     // GameServer metadata.uid at mint time; "" for legacy links minted before migration 013
	CreatedBy  int64      // user ID who created it
	CanStart   bool       // whether the link grants start capability
	ExpiresAt  *time.Time // nil = never expires; matches RevokedAt/LastUsed's pattern
	RevokedAt  *time.Time // NULL = active; set for audit trail
	CreatedAt  time.Time  // when created
	LastUsed   *time.Time // when last accessed via LookupShareLink
}

// ErrShareLinkInvalid is returned by LookupShareLink when a token is unknown,
// expired, or revoked. The caller cannot distinguish between these cases —
// an attacker probing tokens must not learn that one existed.
var ErrShareLinkInvalid = errors.New("invalid share link")

// ErrShareLinkExpiryInvalid is returned by CreateShareLink when a given
// expiry timestamp is invalid (zero or not strictly in the future). A nil
// expiry (never expires) is always valid and skips this check entirely.
var ErrShareLinkExpiryInvalid = errors.New("share link expiry invalid")

// ErrShareLinkNotFound is returned by RevokeShareLink when no share link with
// that id belongs to the given cluster, namespace and server. httperr
// classifies it as 404.
var ErrShareLinkNotFound = errors.New("share link not found")

// CreateShareLink mints a share link that is not bound to a GameServer UID
// (server_uid is stored empty). Such a link is validated by the public
// handlers with the creationTimestamp check only; the API create handler uses
// CreateShareLinkForServer instead.
func (s *Store) CreateShareLink(ctx context.Context, cluster, ns, serverName string, createdBy int64, canStart bool, expiresAt *time.Time) (rawToken string, link ShareLink, err error) {
	return s.CreateShareLinkForServer(ctx, cluster, ns, serverName, "", createdBy, canStart, expiresAt)
}

// CreateShareLinkForServer mints a new share link bound to serverUID (the
// GameServer's metadata.uid) and returns the raw token, which is never stored
// and never recoverable afterwards. expiresAt is optional: nil means the link
// never expires. When non-nil it must be strictly in the future; there is no
// maximum lifetime. An empty serverUID stores a legacy-style unbound link.
func (s *Store) CreateShareLinkForServer(ctx context.Context, cluster, ns, serverName, serverUID string, createdBy int64, canStart bool, expiresAt *time.Time) (rawToken string, link ShareLink, err error) {
	if cluster == "" {
		cluster = "local"
	}
	// Validate expiry, when one is given. A nil expiresAt means "never
	// expires" and bypasses this check entirely.
	now := time.Now()
	if expiresAt != nil {
		if expiresAt.IsZero() {
			return "", ShareLink{}, fmt.Errorf("share link expiry: %w", ErrShareLinkExpiryInvalid)
		}
		if !expiresAt.After(now) {
			return "", ShareLink{}, fmt.Errorf("share link expiry in the past: %w", ErrShareLinkExpiryInvalid)
		}
	}

	// Generate a random ID (12 bytes base64-encoded).
	id := generateShareLinkID()

	// Generate a random token (32 bytes base64-encoded).
	rawToken = generateShareLinkToken()

	// Hash the token with SHA-256.
	tokenHash := hashShareLinkToken(rawToken)

	// Insert into the database.
	canStartInt := 0
	if canStart {
		canStartInt = 1
	}
	// created_at is generated here in Go (RFC3339 UTC), not via SQL
	// datetime('now') — see the migration's header comment for why.
	createdAt := now.UTC()
	var expiresAtArg any
	if expiresAt != nil {
		expiresAtArg = expiresAt.Format(time.RFC3339)
	}
	if serverUID == "" {
		// An unbound link leaves server_uid to its column default (''), so this
		// insert is the same one CreateShareLink issued before migration 013.
		_, err = s.DB.ExecContext(ctx,
			`INSERT INTO share_links(id, cluster, namespace, server_name, created_by, can_start, token_hash, expires_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, cluster, ns, serverName, createdBy, canStartInt, tokenHash, expiresAtArg, createdAt.Format(time.RFC3339))
	} else {
		_, err = s.DB.ExecContext(ctx,
			`INSERT INTO share_links(id, cluster, namespace, server_name, server_uid, created_by, can_start, token_hash, expires_at, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, cluster, ns, serverName, serverUID, createdBy, canStartInt, tokenHash, expiresAtArg, createdAt.Format(time.RFC3339))
	}
	if err != nil {
		return "", ShareLink{}, fmt.Errorf("insert share link: %w", err)
	}

	// Return the raw token (never persisted) and the link metadata. Copy the
	// caller's expiresAt value rather than storing their pointer, so the
	// returned struct is not aliased to memory the caller may still mutate.
	var expiresAtCopy *time.Time
	if expiresAt != nil {
		v := *expiresAt
		expiresAtCopy = &v
	}
	link = ShareLink{
		ID:         id,
		Cluster:    cluster,
		Namespace:  ns,
		ServerName: serverName,
		ServerUID:  serverUID,
		CreatedBy:  createdBy,
		CanStart:   canStart,
		ExpiresAt:  expiresAtCopy,
		CreatedAt:  createdAt,
	}

	return rawToken, link, nil
}

// LookupShareLink resolves a raw token. It returns ErrShareLinkInvalid for an
// unknown, expired, or revoked token — the caller cannot distinguish these cases.
// This is a pure read operation. Call TouchShareLink separately to update last_used.
func (s *Store) LookupShareLink(ctx context.Context, rawToken string) (ShareLink, error) {
	// Hash the token.
	tokenHash := hashShareLinkToken(rawToken)

	// Query by hash for O(1) lookup.
	var link ShareLink
	var canStartInt int
	var revokedAtStr sql.NullString
	var lastUsedStr sql.NullString
	var expiresAtStr sql.NullString
	var createdAtStr string

	err := s.DB.QueryRowContext(ctx,
		`SELECT id, cluster, namespace, server_name, server_uid, created_by, can_start, expires_at, revoked_at, created_at, last_used
		 FROM share_links WHERE token_hash = ?`,
		tokenHash).Scan(
		&link.ID,
		&link.Cluster,
		&link.Namespace,
		&link.ServerName,
		&link.ServerUID,
		&link.CreatedBy,
		&canStartInt,
		&expiresAtStr,
		&revokedAtStr,
		&createdAtStr,
		&lastUsedStr,
	)

	if errors.Is(err, sql.ErrNoRows) {
		return ShareLink{}, ErrShareLinkInvalid
	}
	if err != nil {
		return ShareLink{}, fmt.Errorf("lookup share link: %w", err)
	}

	// Parse timestamps. expires_at is nullable: nil means the link never expires.
	if expiresAtStr.Valid {
		expiresAt, err := time.Parse(time.RFC3339, expiresAtStr.String)
		if err != nil {
			return ShareLink{}, fmt.Errorf("parse expires_at: %w", err)
		}
		link.ExpiresAt = &expiresAt
	}

	createdAt, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		return ShareLink{}, fmt.Errorf("parse created_at: %w", err)
	}
	link.CreatedAt = createdAt

	// Parse revoked_at if present.
	if revokedAtStr.Valid {
		revokedAt, err := time.Parse(time.RFC3339, revokedAtStr.String)
		if err != nil {
			return ShareLink{}, fmt.Errorf("parse revoked_at: %w", err)
		}
		link.RevokedAt = &revokedAt
	}

	// Parse last_used if present.
	if lastUsedStr.Valid {
		lastUsed, err := time.Parse(time.RFC3339, lastUsedStr.String)
		if err != nil {
			return ShareLink{}, fmt.Errorf("parse last_used: %w", err)
		}
		link.LastUsed = &lastUsed
	}

	// Convert can_start from int.
	link.CanStart = canStartInt != 0

	// Check if the link is revoked.
	if link.RevokedAt != nil {
		return ShareLink{}, ErrShareLinkInvalid
	}

	// Check if the link is expired. A nil ExpiresAt means it never expires,
	// so the expiry check is skipped entirely.
	if link.ExpiresAt != nil && time.Now().After(*link.ExpiresAt) {
		return ShareLink{}, ErrShareLinkInvalid
	}

	return link, nil
}

// ListShareLinks returns all share links for a given server (cluster, namespace, server_name),
// active and revoked alike. Results are ordered by created_at descending.
func (s *Store) ListShareLinks(ctx context.Context, cluster, ns, serverName string) ([]ShareLink, error) {
	if cluster == "" {
		cluster = "local"
	}
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, cluster, namespace, server_name, server_uid, created_by, can_start, expires_at, revoked_at, created_at, last_used
		 FROM share_links WHERE cluster = ? AND namespace = ? AND server_name = ?
		 ORDER BY created_at DESC`,
		cluster, ns, serverName)
	if err != nil {
		return nil, fmt.Errorf("list share links: %w", err)
	}
	defer func() {
		_ = rows.Close() // rows.Close error is already checked via rows.Err() below
	}()

	var links []ShareLink
	for rows.Next() {
		var link ShareLink
		var canStartInt int
		var revokedAtStr sql.NullString
		var lastUsedStr sql.NullString
		var expiresAtStr sql.NullString
		var createdAtStr string

		if err := rows.Scan(
			&link.ID,
			&link.Cluster,
			&link.Namespace,
			&link.ServerName,
			&link.ServerUID,
			&link.CreatedBy,
			&canStartInt,
			&expiresAtStr,
			&revokedAtStr,
			&createdAtStr,
			&lastUsedStr,
		); err != nil {
			return nil, fmt.Errorf("scan share link: %w", err)
		}

		// Parse timestamps. expires_at is nullable: nil means the link never expires.
		if expiresAtStr.Valid {
			expiresAt, err := time.Parse(time.RFC3339, expiresAtStr.String)
			if err != nil {
				return nil, fmt.Errorf("parse expires_at: %w", err)
			}
			link.ExpiresAt = &expiresAt
		}

		createdAt, err := time.Parse(time.RFC3339, createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
		link.CreatedAt = createdAt

		// Parse revoked_at if present.
		if revokedAtStr.Valid {
			revokedAt, err := time.Parse(time.RFC3339, revokedAtStr.String)
			if err != nil {
				return nil, fmt.Errorf("parse revoked_at: %w", err)
			}
			link.RevokedAt = &revokedAt
		}

		// Parse last_used if present.
		if lastUsedStr.Valid {
			lastUsed, err := time.Parse(time.RFC3339, lastUsedStr.String)
			if err != nil {
				return nil, fmt.Errorf("parse last_used: %w", err)
			}
			link.LastUsed = &lastUsed
		}

		// Convert can_start from int.
		link.CanStart = canStartInt != 0

		links = append(links, link)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate share links: %w", err)
	}

	return links, nil
}

// SetShareLinkCanStart changes the wake permission on an active link for this
// incarnation of the server. Legacy links use the same creation-time fallback
// as public share resolution. The token, expiry and revocation are untouched.
func (s *Store) SetShareLinkCanStart(ctx context.Context, cluster, ns, serverName, id, serverUID string, serverCreatedAt time.Time, canStart bool) error {
	if cluster == "" {
		cluster = "local"
	}
	value := 0
	if canStart {
		value = 1
	}
	// expires_at is RFC3339 text and can carry an offset. Compare instants,
	// not strings, in the same statement that rejects a concurrent revoke.
	const update = `UPDATE share_links SET can_start = ?
		WHERE cluster = ? AND namespace = ? AND server_name = ? AND id = ? AND revoked_at IS NULL
		AND ((server_uid <> '' AND server_uid = ?) OR (server_uid = '' AND created_at >= ?))
		AND (expires_at IS NULL OR `
	query := update + `julianday(expires_at) > julianday(?))`
	if s.Driver == "postgres" {
		query = update + `expires_at::timestamptz > CAST(? AS text)::timestamptz)`
	}
	result, err := s.DB.ExecContext(ctx, query, value, cluster, ns, serverName, id, serverUID,
		serverCreatedAt.UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("update share link wake permission: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("updated share link count: %w", err)
	}
	if n == 0 {
		return ErrShareLinkNotFound
	}
	return nil
}

// RevokeShareLink marks a share link as revoked by setting revoked_at to the
// current timestamp. The link must belong to the given cluster, namespace and
// server; an empty cluster means "local", as in CreateShareLink. Revocation is
// auditable (never a delete). It returns ErrShareLinkNotFound when no such
// link exists.
func (s *Store) RevokeShareLink(ctx context.Context, cluster, ns, serverName, id string) error {
	if cluster == "" {
		cluster = "local"
	}
	revokedAt := time.Now().UTC().Format(time.RFC3339)
	res, err := s.DB.ExecContext(ctx,
		`UPDATE share_links SET revoked_at = ?
		 WHERE id = ? AND cluster = ? AND namespace = ? AND server_name = ?`,
		revokedAt, id, cluster, ns, serverName)
	if err != nil {
		return fmt.Errorf("revoke share link: %w", err)
	}

	// Check if the row was found.
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("revoke share link %q: %w", id, ErrShareLinkNotFound)
	}

	return nil
}

// RevokeShareLinksForServer revokes every still-active share link on one
// server (cluster, namespace, server_name), keeping the rows for the audit
// trail. Called when the server is deleted so a link can never resolve against
// a later server that reuses the name. An empty cluster means "local", as in
// CreateShareLink. Revoking nothing is not an error.
func (s *Store) RevokeShareLinksForServer(ctx context.Context, cluster, ns, serverName string) error {
	if cluster == "" {
		cluster = "local"
	}
	revokedAt := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE share_links SET revoked_at = ?
		 WHERE cluster = ? AND namespace = ? AND server_name = ? AND revoked_at IS NULL`,
		revokedAt, cluster, ns, serverName); err != nil {
		return fmt.Errorf("revoke share links for server: %w", err)
	}
	return nil
}

// TouchShareLink updates the last_used timestamp for a share link, recording
// when it was last accessed. Used by LookupShareLink internally.
func (s *Store) TouchShareLink(ctx context.Context, id string) error {
	lastUsed := time.Now().UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`UPDATE share_links SET last_used = ? WHERE id = ?`,
		lastUsed, id)
	if err != nil {
		return fmt.Errorf("touch share link: %w", err)
	}
	return nil
}

// generateShareLinkID generates a random 12-byte string encoded as base64-url.
func generateShareLinkID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// generateShareLinkToken generates a random 32-byte token encoded as base64-url.
func generateShareLinkToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashShareLinkToken returns the hex-encoded SHA-256 hash of the raw token.
func hashShareLinkToken(rawToken string) string {
	h := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(h[:])
}
