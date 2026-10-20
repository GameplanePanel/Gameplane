package controlplane

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

func backendForKey(database *sql.DB, key []byte) (*backend, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential encryption: %w", err)
	}
	identity := sha256.New()
	_, _ = identity.Write([]byte("Gameplane management encryption key identity\x00"))
	_, _ = identity.Write(key)
	return &backend{db: database, aead: aead, keyID: hex.EncodeToString(identity.Sum(nil))}, nil
}

// lockKeyState both checks key identity and takes a database write lock held
// until tx commits. UPDATE works on SQLite and PostgreSQL without dialect-specific
// SELECT FOR UPDATE. Rotation cannot retire a key underneath a checked writer.
func (b *backend) lockKeyState(ctx context.Context, tx *sql.Tx) error {
	result, err := tx.ExecContext(ctx, `UPDATE management_key_state SET key_id = key_id WHERE singleton = 1 AND key_id = ?`, b.keyID)
	if err != nil {
		return fmt.Errorf("lock management encryption key state: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect management encryption key state: %w", err)
	}
	if count != 1 {
		return errors.New("management encryption key is inactive; restart with the current key file")
	}
	return nil
}

func (b *backend) initializeKeyState(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO management_key_state (singleton, key_id, previous_key_id) VALUES (1, ?, '') ON CONFLICT (singleton) DO NOTHING`, b.keyID); err != nil {
		return fmt.Errorf("initialize management encryption key state: %w", err)
	}
	return b.lockKeyState(ctx, tx)
}

func (b *backend) validateStorage(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT kind, namespace, name, uid, version, payload FROM management_objects`)
	if err != nil {
		return fmt.Errorf("validate management storage: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var kind, ns, name, uid, payload string
		var version int64
		if err := rows.Scan(&kind, &ns, &name, &uid, &version, &payload); err != nil {
			return fmt.Errorf("read management row: %w", err)
		}
		if kind != "secrets" && kind != "clusters" {
			return errors.New("unknown management object kind")
		}
		if _, err := (&objects{b, kind, ns}).decode(name, uid, version, payload); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read management storage: %w", err)
	}
	return nil
}

func (b *backend) validateAndInitialize(ctx context.Context) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin management key validation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := b.initializeKeyState(ctx, tx); err != nil {
		return err
	}
	if err := b.validateStorage(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit management key validation: %w", err)
	}
	return nil
}

func (b *backend) write(ctx context.Context, secret bool, query string, args ...any) (sql.Result, error) {
	if !secret {
		return b.db.ExecContext(ctx, query, args...)
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin credential write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := b.lockKeyState(ctx, tx); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("write credential: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit credential write: %w", err)
	}
	return result, nil
}
