// Package i18n extracts the translatable message ids of a module directory.
// The derivation itself lives in the shared i18n/modlocale package so the
// operator applies exactly the same id set at install time.
package i18n

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/GameplanePanel/gameplane/i18n/modlocale"
	"github.com/GameplanePanel/gameplane/i18n/pack"
	"gopkg.in/yaml.v3"
)

// DefaultOutput is where Extract's result is written, relative to the module.
const DefaultOutput = "locales/en.json"

// Extract derives the English reference pack of the module in dir and returns
// it as en.json content, descriptions included.
func Extract(dir string) ([]byte, error) {
	ref, err := reference(dir)
	if err != nil {
		return nil, err
	}
	data, err := pack.Marshal(ref)
	if err != nil {
		return nil, fmt.Errorf("encode reference pack: %w", err)
	}
	return data, nil
}

// reference derives the English reference pack of the module in dir. The
// reference carries no sourceHash: only translations record one.
func reference(dir string) (map[string]pack.Entry, error) {
	module, err := readYAML(filepath.Join(dir, "module.yaml"), true)
	if err != nil {
		return nil, err
	}
	template, err := readYAML(filepath.Join(dir, "template.yaml"), true)
	if err != nil {
		return nil, err
	}
	return modlocale.References(modlocale.Derive(module, template)), nil
}

// WriteExtract runs Extract and writes the result to output, creating its
// directory. An empty output means DefaultOutput inside dir.
func WriteExtract(dir, output string) (string, error) {
	data, err := Extract(dir)
	if err != nil {
		return "", err
	}
	if output == "" {
		output = filepath.Join(dir, DefaultOutput)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o750); err != nil {
		return "", fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(output, data, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", output, err)
	}
	return output, nil
}

func readYAML(path string, required bool) (map[string]any, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if !required && errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return out, nil
}
