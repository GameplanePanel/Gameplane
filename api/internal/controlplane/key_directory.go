package controlplane

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func syncKeyDirectory(path string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return fmt.Errorf("open key directory root for sync: %w", err)
	}
	defer func() { _ = root.Close() }()
	dir, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open key directory for sync: %w", err)
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("persist key directory: %w", err)
	}
	return nil
}

// Publish each new directory durably before creating a child beneath it.
func createKeyDirectory(path string, syncDirectory func(string) error) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return errors.New("management key parent is not a directory")
		}
		// Visibility does not prove durability: an earlier attempt may have
		// created this directory and failed while syncing its parent. Recheck
		// publication before a retry can create encrypted database records.
		return syncDirectory(filepath.Dir(path))
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect key directory: %w", err)
	}
	parent := filepath.Dir(path)
	if err := createKeyDirectory(parent, syncDirectory); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0700); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create private key directory: %w", err)
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.IsDir() {
			return errors.New("management key directory changed during creation")
		}
	}
	return syncDirectory(parent)
}
