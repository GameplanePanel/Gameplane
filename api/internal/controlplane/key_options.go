package controlplane

import (
	"errors"
	"fmt"
	"os"
)

// KeyOptions selects management key custody. Provisioned files are read-only
// operator inputs: the API never creates their directory, file, or replacement.
type KeyOptions struct {
	File        string
	Provisioned bool
}

func loadConfiguredKey(opts KeyOptions, required bool) ([]byte, error) {
	if opts.File == "" {
		return nil, errors.New("management key file path is required")
	}
	if !opts.Provisioned {
		return loadKey(opts.File, required)
	}
	key, err := readKey(opts.File)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("provisioned management key is missing; mount the original operator-provisioned key")
	}
	if err != nil {
		return nil, fmt.Errorf("read provisioned management key: %w", err)
	}
	return key, nil
}
