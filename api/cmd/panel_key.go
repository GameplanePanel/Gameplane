package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/GameplanePanel/gameplane/api/internal/controlplane"
	"github.com/GameplanePanel/gameplane/api/internal/db"
)

func rotatePanelKey(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("rotate-panel-key", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	driver := fs.String("db-driver", envOr("GAMEPLANE_DB_DRIVER", "sqlite"), "sqlite or postgres")
	dsn := fs.String("db-dsn", envOr("GAMEPLANE_DB_DSN", "file:/data/gameplane.db?_pragma=journal_mode(WAL)"), "database DSN")
	oldFile := fs.String("old-key-file", envOr("GAMEPLANE_PANEL_KEY_FILE", ""), "existing management key file")
	newFile := fs.String("new-key-file", "", "separately named replacement management key file")
	provisioned := fs.Bool("new-key-provisioned", false, "require a read-only operator-provisioned replacement key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *oldFile == "" || *newFile == "" {
		return errors.New("rotate-panel-key requires --old-key-file and --new-key-file, with no positional arguments; stop the API before rotation")
	}
	store, err := db.Open(ctx, *driver, *dsn)
	if err != nil {
		return fmt.Errorf("open management database for key rotation: %w", err)
	}
	defer func() { _ = store.Close() }()
	if err := store.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate management database for key rotation: %w", err)
	}
	return controlplane.RotateKeyWithOptions(ctx, store, *oldFile, controlplane.KeyOptions{File: *newFile, Provisioned: *provisioned})
}
