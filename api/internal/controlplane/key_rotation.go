package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// RotateKey rotates into a separately named generated key file. An existing
// safely permissioned destination is retained for interrupted-command retries.
// Stop the API first and retain both key files for their matching DB backups.
func RotateKey(ctx context.Context, store *db.Store, oldFile, newFile string) error {
	return RotateKeyWithOptions(ctx, store, oldFile, KeyOptions{File: newFile})
}

// RotateKeyWithOptions also accepts an operator-provisioned, read-only new key.
// Its durable custody remains with the operator; the API never writes/syncs it.
func RotateKeyWithOptions(ctx context.Context, store *db.Store, oldFile string, newOptions KeyOptions) error {
	if oldFile == "" || newOptions.File == "" {
		return errors.New("old and new management key file paths are required")
	}
	oldPath, err := filepath.Abs(oldFile)
	if err != nil {
		return fmt.Errorf("resolve old management key path: %w", err)
	}
	newPath, err := filepath.Abs(newOptions.File)
	if err != nil {
		return fmt.Errorf("resolve new management key path: %w", err)
	}
	if oldPath == newPath {
		return errors.New("rotation requires separate old and new key file paths")
	}
	oldKey, err := loadConfiguredKey(KeyOptions{File: oldFile, Provisioned: true}, true)
	if err != nil {
		return err
	}
	oldBackend, err := backendForKey(store.DB, oldKey)
	if err != nil {
		return err
	}
	// Authenticate the old key before creating a destination. If a previous
	// commit succeeded but its result was lost, authenticate the new state
	// before reporting the same old/new request as already completed.
	completed, err := validateRotationSource(ctx, oldBackend, newOptions)
	if err != nil || completed {
		return err
	}
	newKey, err := loadConfiguredKey(newOptions, false)
	if err != nil {
		return err
	}
	newBackend, err := backendForKey(store.DB, newKey)
	if err != nil {
		return err
	}
	if oldBackend.keyID == newBackend.keyID {
		return errors.New("rotation requires different old and new key material")
	}
	// Generated keys are already durably published before opening this
	// transaction. No failure path removes either key or overwrites the old one.
	tx, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin management key rotation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := oldBackend.lockKeyState(ctx, tx); err != nil {
		return err
	}
	if err := oldBackend.validateStorage(ctx, tx); err != nil {
		return err
	}
	records, err := readRotationRecords(ctx, tx)
	if err != nil {
		return err
	}
	for _, record := range records {
		oldObjects := &objects{backend: oldBackend, kind: "secrets", ns: record.namespace}
		obj, err := oldObjects.decode(record.name, record.uid, record.version, record.payload)
		if err != nil {
			return err
		}
		newObjects := &objects{backend: newBackend, kind: "secrets", ns: record.namespace}
		payload, err := newObjects.encode(obj, record.version)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE management_objects SET payload = ? WHERE kind = 'secrets' AND namespace = ? AND name = ? AND uid = ? AND version = ?`, payload, record.namespace, record.name, record.uid, record.version)
		if err != nil {
			return fmt.Errorf("re-encrypt management credential: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("inspect rotated credential write: %w", err)
		}
		if count != 1 {
			return errors.New("management credential changed during key rotation")
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE management_key_state SET key_id = ?, previous_key_id = ? WHERE singleton = 1`, newBackend.keyID, oldBackend.keyID); err != nil {
		return fmt.Errorf("activate rotated management key: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit management key rotation: %w", err)
	}
	return nil
}

func validateRotationSource(ctx context.Context, old *backend, newOptions KeyOptions) (bool, error) {
	tx, err := old.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin rotation source validation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := old.initializeKeyState(ctx, tx); err != nil {
		// A differing active key is safe only for an authenticated retry of
		// the immediately preceding rotation with these exact key identities.
		var active, previous string
		if readErr := tx.QueryRowContext(ctx, `SELECT key_id, previous_key_id FROM management_key_state WHERE singleton = 1`).Scan(&active, &previous); readErr != nil {
			return false, err
		}
		newKey, readErr := loadConfiguredKey(newOptions, true)
		if readErr != nil {
			return false, err
		}
		current, buildErr := backendForKey(old.db, newKey)
		if buildErr != nil || active != current.keyID || previous != old.keyID {
			return false, err
		}
		if lockErr := current.lockKeyState(ctx, tx); lockErr != nil {
			return false, lockErr
		}
		if validateErr := current.validateStorage(ctx, tx); validateErr != nil {
			return false, validateErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return false, fmt.Errorf("confirm completed key rotation: %w", commitErr)
		}
		return true, nil
	}
	if err := old.validateStorage(ctx, tx); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit rotation source validation: %w", err)
	}
	return false, nil
}

type rotationRecord struct {
	namespace, name, uid, payload string
	version                       int64
}

func readRotationRecords(ctx context.Context, tx *sql.Tx) ([]rotationRecord, error) {
	rows, err := tx.QueryContext(ctx, `SELECT namespace, name, uid, version, payload FROM management_objects WHERE kind = 'secrets' ORDER BY namespace, name`)
	if err != nil {
		return nil, fmt.Errorf("read key rotation credentials: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var records []rotationRecord
	for rows.Next() {
		var record rotationRecord
		if err := rows.Scan(&record.namespace, &record.name, &record.uid, &record.version, &record.payload); err != nil {
			return nil, fmt.Errorf("scan key rotation credential: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read key rotation credentials: %w", err)
	}
	return records, nil
}
