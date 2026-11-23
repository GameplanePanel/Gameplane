package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// SFTPServicePrefix starts every SFTP service token (the SFTP endpoint's bearer credential).
const SFTPServicePrefix = "gpsftps_"

const (
	// SFTPSessionTokenPrefix starts every SFTP session token minted by /sftp/v1/login.
	// Neither prefix is a prefix of the other ("gpsftps_" differs at index 6).
	SFTPSessionTokenPrefix = "gpsftp_"

	sftpTokenRandomBytes = 32
	sftpTokenBodyLen     = 43 // base64 raw URL encoding of 32 bytes
	sftpSessionTTL       = 15 * time.Minute
	sftpSlideInterval    = lastUsedInterval // at most one expiry write per session per minute
	sftpTokenPrefixLen   = 12
)

// sftpLastSlide records the last expiry slide per SFTP session id.
var sftpLastSlide sync.Map

// newSFTPToken returns prefix followed by 32 random bytes in unpadded base64url (43 characters).
func newSFTPToken(prefix string) string {
	raw := make([]byte, sftpTokenRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}

// validSFTPToken reports whether raw has the prefix and the fixed length of a generated token.
// It runs before any query, so malformed bearer values never reach the database.
func validSFTPToken(raw, prefix string) bool {
	return strings.HasPrefix(raw, prefix) && len(raw) == len(prefix)+sftpTokenBodyLen
}

// errSFTPSeedFormat is returned by SeedSFTPServiceToken for a value that is not a generated service token.
var errSFTPSeedFormat = errors.New("sftp service token has the wrong format")

// SeedSFTPServiceToken registers a service token supplied by the deployment (the chart's local SFTP
// token, spec 040) for cluster, so the SFTP pod in the panel's own cluster can log in without an admin
// step. It is idempotent: a digest already stored, revoked or not, is left alone, so a token an admin
// revoked stays revoked across restarts. A new digest replaces the cluster's previously seeded token
// (and its sessions) in the same transaction, so offline renders that generate a fresh secret never
// count twice against the active limit. At the per-cluster active limit it logs and returns nil; the
// seeded token then stays unusable until an admin revokes one.
func (s *SessionStore) SeedSFTPServiceToken(ctx context.Context, cluster, raw string) error {
	if !validSFTPToken(raw, SFTPServicePrefix) {
		return fmt.Errorf("seed sftp service token: %w", errSFTPSeedFormat)
	}
	digest := sessionDigest(raw)
	exists, err := sftpExists(ctx, s.db.DB, `SELECT 1 FROM sftp_service_tokens WHERE digest = ?`, digest)
	if err != nil {
		return fmt.Errorf("seed sftp service token: lookup: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := s.insertServiceToken(ctx, cluster, digest, raw[:sftpTokenPrefixLen], true); err != nil {
		if errors.Is(err, ErrSFTPServiceTokenLimit) {
			slog.Warn("sftp: seeded service token not stored, the cluster is at its active token limit; revoke one to use it", "cluster", cluster)
			return nil
		}
		return fmt.Errorf("seed sftp service token: %w", err)
	}
	return nil
}

// CreateSFTPServiceToken makes a service token for cluster (e.g. "local"). The token is returned once.
func (s *SessionStore) CreateSFTPServiceToken(ctx context.Context, cluster string) (SFTPServiceToken, string, error) {
	token := newSFTPToken(SFTPServicePrefix)
	tok, err := s.InsertSFTPServiceToken(ctx, cluster, sessionDigest(token), token[:sftpTokenPrefixLen])
	if err != nil {
		return SFTPServiceToken{}, "", err
	}
	return tok, token, nil
}

// AuthenticateSFTPService resolves a raw "gpsftps_" service token to (tokenID, cluster).
// It returns sql.ErrNoRows for an unknown, revoked or malformed token.
func (s *SessionStore) AuthenticateSFTPService(ctx context.Context, raw string) (int64, string, error) {
	if !validSFTPToken(raw, SFTPServicePrefix) {
		return 0, "", sql.ErrNoRows
	}
	return s.LookupSFTPServiceToken(ctx, sessionDigest(raw))
}

// IssueSFTPSession mints a gpsftp_ session token for userID and cluster. credential is "password" or
// "key:<keyID>". epoch is the users.session_epoch read with the credential check. It returns the token
// and its expiry (now + 15 minutes).
func (s *SessionStore) IssueSFTPSession(ctx context.Context, userID int64, cluster, credential string, epoch int64) (string, time.Time, error) {
	return s.IssueSFTPSessionForToken(ctx, userID, cluster, credential, epoch, 0)
}

// IssueSFTPSessionForToken is IssueSFTPSession for a login made with service token serviceTokenID
// (0 for none). Revoking that service token ends the session.
func (s *SessionStore) IssueSFTPSessionForToken(ctx context.Context, userID int64, cluster, credential string, epoch, serviceTokenID int64) (string, time.Time, error) {
	if credential == "" {
		return "", time.Time{}, fmt.Errorf("issue sftp session: empty credential")
	}
	token := newSFTPToken(SFTPSessionTokenPrefix)
	expires := time.Now().Add(sftpSessionTTL)
	if _, err := s.InsertSFTPSessionForToken(ctx, userID, cluster, credential, sessionDigest(token), epoch, expires, serviceTokenID); err != nil {
		return "", time.Time{}, fmt.Errorf("issue sftp session: %w", err)
	}
	return token, expires, nil
}

// LookupSFTPSessionToken resolves a raw gpsftp_ session token to its owner and session row.
// Unknown, revoked, expired and epoch-mismatched tokens fail; the errors wrap sql.ErrNoRows,
// errSessionInvalid or errSessionExpired.
func (s *SessionStore) LookupSFTPSessionToken(ctx context.Context, raw string) (*User, SFTPSession, error) {
	if !validSFTPToken(raw, SFTPSessionTokenPrefix) {
		return nil, SFTPSession{}, sql.ErrNoRows
	}
	return s.LookupSFTPSession(ctx, sessionDigest(raw))
}

// slideSFTPSession moves a session's expiry forward, at most once per sftpSlideInterval.
// Errors are logged only; a failed slide must not fail the request.
func (s *SessionStore) slideSFTPSession(ctx context.Context, id int64) {
	now := time.Now()
	if v, ok := sftpLastSlide.Load(id); ok {
		if last, isTime := v.(time.Time); isTime && now.Sub(last) < sftpSlideInterval {
			return
		}
	}
	sftpLastSlide.Store(id, now)
	if err := s.SlideSFTPSession(ctx, id, now.Add(sftpSessionTTL)); err != nil {
		slog.Warn("sftp slide session", "err", err)
	}
}
