package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SFTPLoginNameMaxRunes bounds a chosen SFTP login name.
const SFTPLoginNameMaxRunes = 64

// ErrSFTPLoginNameInvalid and ErrSFTPLoginNameTaken are returned by SetSFTPLoginName.
var (
	ErrSFTPLoginNameInvalid = errors.New("sftp login name invalid")
	ErrSFTPLoginNameTaken   = errors.New("sftp login name taken")
)

// ValidSFTPLoginName reports whether name can be typed as an SFTP login name: non-empty valid UTF-8 of
// at most SFTPLoginNameMaxRunes runes, with no whitespace, control characters, "/" or "+" ("+" starts the
// server part of the login).
func ValidSFTPLoginName(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > SFTPLoginNameMaxRunes {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || r == '/' || r == '+'
	})
}

// queryer is satisfied by *sql.DB and *sql.Tx.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// nameClaims counts how many accounts other than userID hold name as their panel username, chosen SFTP
// login name or display name.
func nameClaims(ctx context.Context, q queryer, name string, userID int64) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM users
		WHERE id <> ? AND (username = ? OR sftp_username = ? OR display_name = ?)`,
		userID, name, name, name).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sftp login name claims: %w", err)
	}
	return n, nil
}

// SFTPLoginName returns the name userID logs in with over SFTP: the chosen name when set; otherwise the
// display name when it is a valid login name and no other account holds it as a username, chosen name or
// display name; otherwise the panel username. custom reports whether the name was chosen.
func (s *SessionStore) SFTPLoginName(ctx context.Context, userID int64) (name string, custom bool, err error) {
	var (
		username string
		chosen   sql.NullString
		display  sql.NullString
	)
	if err := s.db.DB.QueryRowContext(ctx,
		`SELECT username, sftp_username, display_name FROM users WHERE id = ?`, userID,
	).Scan(&username, &chosen, &display); err != nil {
		return "", false, fmt.Errorf("sftp login name: %w", err)
	}
	if chosen.Valid {
		return chosen.String, true, nil
	}
	if display.Valid && display.String != username && ValidSFTPLoginName(display.String) {
		n, err := nameClaims(ctx, s.db.DB, display.String, userID)
		if err != nil {
			return "", false, err
		}
		if n == 0 {
			return display.String, false, nil
		}
	}
	return username, false, nil
}

// SetSFTPLoginName sets the SFTP login name userID chose; an empty name goes back to the default. A name
// another account holds as its panel username, chosen name or display name is refused with
// ErrSFTPLoginNameTaken, so a login name never names two accounts.
func (s *SessionStore) SetSFTPLoginName(ctx context.Context, userID int64, name string) error {
	if name == "" {
		if _, err := s.db.DB.ExecContext(ctx, `UPDATE users SET sftp_username = NULL WHERE id = ?`, userID); err != nil {
			return fmt.Errorf("clear sftp login name: %w", err)
		}
		return nil
	}
	if !ValidSFTPLoginName(name) {
		return ErrSFTPLoginNameInvalid
	}
	tx, err := s.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set sftp login name: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	n, err := nameClaims(ctx, tx, name, userID)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrSFTPLoginNameTaken
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET sftp_username = ? WHERE id = ?`, name, userID); err != nil {
		// The unique index refuses a concurrent claim of the same chosen name.
		return fmt.Errorf("set sftp login name: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("set sftp login name: commit: %w", err)
	}
	return nil
}

// ResolveSFTPLoginName maps the name typed at the SFTP login to a panel username. A chosen SFTP login name
// matches first, then a panel username (it always works), then a display name that is the account's
// default login name (see SFTPLoginName). An unknown name comes back unchanged, and the lookup that
// follows refuses it like any other unknown user. The credential is still checked against that account.
func (s *SessionStore) ResolveSFTPLoginName(ctx context.Context, name string) (string, error) {
	var username string
	err := s.db.DB.QueryRowContext(ctx, `SELECT username FROM users WHERE sftp_username = ?`, name).Scan(&username)
	if err == nil {
		return username, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("resolve sftp login name: %w", err)
	}
	err = s.db.DB.QueryRowContext(ctx, `SELECT username FROM users WHERE username = ?`, name).Scan(&username)
	if err == nil {
		return username, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("resolve sftp login name: username: %w", err)
	}
	if !ValidSFTPLoginName(name) {
		return name, nil
	}
	var (
		id    sql.NullInt64
		count int
	)
	if err := s.db.DB.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(id) FROM users WHERE display_name = ?`, name).Scan(&count, &id); err != nil {
		return "", fmt.Errorf("resolve sftp login name: display name: %w", err)
	}
	if count != 1 || !id.Valid {
		return name, nil
	}
	// The display name logs in only while it is that account's default login name.
	effective, _, err := s.SFTPLoginName(ctx, id.Int64)
	if err != nil {
		return "", err
	}
	if effective != name {
		return name, nil
	}
	if err := s.db.DB.QueryRowContext(ctx, `SELECT username FROM users WHERE id = ?`, id.Int64).Scan(&username); err != nil {
		return "", fmt.Errorf("resolve sftp login name: account: %w", err)
	}
	return username, nil
}
