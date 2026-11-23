package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

const (
	// SFTPPasswordMinLen is the shortest generated SFTP password length.
	SFTPPasswordMinLen = 20
	// SFTPPasswordMaxLen is the longest generated SFTP password length.
	SFTPPasswordMaxLen = 40
	sftpRSAMinBits     = 3072
	maxSFTPKeyNameLen  = 64
)

var (
	// ErrSFTPKeyInvalid is returned for a line that is not one valid authorized_keys public key.
	ErrSFTPKeyInvalid = errors.New("sftp key: not a valid authorized_keys public key")
	// ErrSFTPKeyType is returned for a key algorithm outside the supported set.
	ErrSFTPKeyType = errors.New("sftp key: unsupported key type")
	// ErrSFTPKeyWeak is returned for an RSA key with fewer than 3072 bits.
	ErrSFTPKeyWeak = errors.New("sftp key: RSA keys must be at least 3072 bits")
	// ErrSFTPKeyName is returned when a key name is empty or longer than 64 characters.
	ErrSFTPKeyName = errors.New("sftp key: name must be 1 to 64 characters")
	// ErrSFTPPasswordLen is returned when a requested password length is outside 20 to 40.
	ErrSFTPPasswordLen = errors.New("sftp password: length must be 20 to 40")
)

// ParsedSFTPKey is a validated, normalized public key.
type ParsedSFTPKey struct {
	Normalized  string
	Fingerprint string
	Type        string
}

// ParseSFTPPublicKey parses one authorized_keys line. It accepts ssh-ed25519, ecdsa-sha2-nistp256/384/521
// and ssh-rsa with a modulus of at least 3072 bits; it rejects options (command=...), comments are dropped,
// multiple lines, DSA and sk-* keys. Normalized is "<type> <base64>" (ssh.MarshalAuthorizedKey, trimmed);
// Fingerprint is ssh.FingerprintSHA256(key) ("SHA256:...").
func ParseSFTPPublicKey(line string) (ParsedSFTPKey, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.ContainsAny(line, "\r\n") {
		return ParsedSFTPKey{}, ErrSFTPKeyInvalid
	}
	pub, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return ParsedSFTPKey{}, ErrSFTPKeyInvalid
	}
	if len(options) > 0 || len(bytes.TrimSpace(rest)) > 0 {
		return ParsedSFTPKey{}, ErrSFTPKeyInvalid
	}
	switch pub.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
	case ssh.KeyAlgoRSA:
		cpk, ok := pub.(ssh.CryptoPublicKey)
		if !ok {
			return ParsedSFTPKey{}, ErrSFTPKeyInvalid
		}
		rsaKey, ok := cpk.CryptoPublicKey().(*rsa.PublicKey)
		if !ok {
			return ParsedSFTPKey{}, ErrSFTPKeyInvalid
		}
		if rsaKey.N.BitLen() < sftpRSAMinBits {
			return ParsedSFTPKey{}, ErrSFTPKeyWeak
		}
	default:
		return ParsedSFTPKey{}, ErrSFTPKeyType
	}
	return ParsedSFTPKey{
		Normalized:  strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))),
		Fingerprint: ssh.FingerprintSHA256(pub),
		Type:        pub.Type(),
	}, nil
}

// SFTPFingerprintForLogin returns the SHA256 fingerprint of a public key sent by the SFTP server at login.
// The same parse rules apply; the SFTP server already verified the client's signature.
func SFTPFingerprintForLogin(publicKeyLine string) (string, error) {
	parsed, err := ParseSFTPPublicKey(publicKeyLine)
	if err != nil {
		return "", err
	}
	return parsed.Fingerprint, nil
}

// GenerateSFTPPassword returns length characters drawn uniformly from the 94 printable ASCII
// characters '!'..'~' (space excluded), using crypto/rand.
func GenerateSFTPPassword(length int) (string, error) {
	if length < SFTPPasswordMinLen || length > SFTPPasswordMaxLen {
		return "", ErrSFTPPasswordLen
	}
	buf := make([]byte, length)
	for i := range buf {
		// rand.Int returns a value in [0, len(sftpPasswordAlphabet)), so indexing cannot go out of range.
		n, err := rand.Int(rand.Reader, sftpPasswordAlphabetLen)
		if err != nil {
			return "", fmt.Errorf("generate sftp password: %w", err)
		}
		buf[i] = sftpPasswordAlphabet[n.Int64()]
	}
	return string(buf), nil
}

// sftpPasswordAlphabet is the 94 printable ASCII characters '!'..'~' (space excluded), in code order.
var sftpPasswordAlphabet = func() string {
	alphabet := make([]byte, 0, 94)
	for c := byte('!'); c <= '~'; c++ {
		alphabet = append(alphabet, c)
	}
	return string(alphabet)
}()

// sftpPasswordAlphabetLen is the bound passed to rand.Int when drawing a password character.
var sftpPasswordAlphabetLen = big.NewInt(int64(len(sftpPasswordAlphabet)))

// AddSFTPKey validates name (trimmed, 1 to 64 runes) and the key, then stores it for userID.
func (s *SessionStore) AddSFTPKey(ctx context.Context, userID int64, name, publicKeyLine string) (SFTPKey, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxSFTPKeyNameLen {
		return SFTPKey{}, ErrSFTPKeyName
	}
	parsed, err := ParseSFTPPublicKey(publicKeyLine)
	if err != nil {
		return SFTPKey{}, err
	}
	return s.InsertSFTPKey(ctx, userID, name, parsed.Normalized, parsed.Fingerprint)
}

// GenerateAndStoreSFTPPassword generates a password of the given length, hashes it with HashPassword,
// replaces the user's row (SetSFTPPassword, which revokes old password sessions) and returns the
// clear text exactly once.
func (s *SessionStore) GenerateAndStoreSFTPPassword(ctx context.Context, userID int64, length int) (string, error) {
	pw, err := GenerateSFTPPassword(length)
	if err != nil {
		return "", err
	}
	hash, err := HashPassword(ctx, pw)
	if err != nil {
		return "", fmt.Errorf("sftp password: hash: %w", err)
	}
	if err := s.SetSFTPPassword(ctx, userID, hash, length); err != nil {
		return "", err
	}
	return pw, nil
}

// VerifySFTPPassword checks pw for username. It ALWAYS spends an argon2 verify (VerifyDummy when the
// user or the password row is missing). It returns the user's id and session_epoch, read in the same query.
func (s *SessionStore) VerifySFTPPassword(ctx context.Context, username, pw string) (userID, epoch int64, ok bool, err error) {
	var (
		id   int64
		ep   int64
		hash sql.NullString
	)
	err = s.db.DB.QueryRowContext(ctx, `
		SELECT u.id, u.session_epoch, p.hash
		FROM users u LEFT JOIN sftp_passwords p ON p.user_id = u.id
		WHERE u.username = ?`, username,
	).Scan(&id, &ep, &hash)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !hash.Valid) {
		VerifyDummy(ctx, pw)
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("verify sftp password: %w", err)
	}
	good, err := VerifyPassword(ctx, pw, hash.String)
	if err != nil {
		return 0, 0, false, fmt.Errorf("verify sftp password: %w", err)
	}
	if !good {
		return 0, 0, false, nil
	}
	return id, ep, true, nil
}

// SFTPUserByName returns the user (with Perms loaded) and users.session_epoch.
// It returns sql.ErrNoRows for an unknown username.
func (s *SessionStore) SFTPUserByName(ctx context.Context, username string) (*User, int64, error) {
	var (
		u  User
		ep int64
	)
	err := s.db.DB.QueryRowContext(ctx,
		`SELECT id, username, display_name, email, role, session_epoch FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.Role, &ep)
	if err != nil {
		return nil, 0, err
	}
	perms, err := s.LoadPerms(ctx, u.ID)
	if err != nil {
		return nil, 0, fmt.Errorf("sftp user perms: %w", err)
	}
	u.Perms = perms
	u.sessionEpoch = ep
	return &u, ep, nil
}

// ResolveSFTPLoginName maps the name typed at the SFTP login to a panel username. A panel username
// always wins. Otherwise the name may be a display name, so an SSO user does not have to type their
// email address; it resolves only when exactly one account has that display name, because display
// names are not unique. An unknown or ambiguous name comes back unchanged, and the lookup that follows
// refuses it like any other unknown user. The credential is still checked against the resolved account.
func (s *SessionStore) ResolveSFTPLoginName(ctx context.Context, name string) (string, error) {
	var found string
	err := s.db.DB.QueryRowContext(ctx, `SELECT username FROM users WHERE username = ?`, name).Scan(&found)
	if err == nil {
		return found, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("resolve sftp login name: %w", err)
	}
	rows, err := s.db.DB.QueryContext(ctx, `SELECT username FROM users WHERE display_name = ? LIMIT 2`, name)
	if err != nil {
		return "", fmt.Errorf("resolve sftp login name: display name: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var matches []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return "", fmt.Errorf("resolve sftp login name: scan: %w", err)
		}
		matches = append(matches, u)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("resolve sftp login name: rows: %w", err)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return name, nil
}
