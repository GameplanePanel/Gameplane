package i18n

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// Stamp records the current English source hash (sourceHash, as in app packs)
// on every translated entry of the module's locales/<tag>.json files that does
// not have one yet. The English reference en.json is left alone. It returns the
// files it rewrote, sorted. A file that does not parse is an error and nothing
// after it is touched.
func Stamp(dir string) ([]string, error) {
	ref, err := reference(dir)
	if err != nil {
		return nil, err
	}
	localesDir := filepath.Join(dir, "locales")
	entries, err := os.ReadDir(localesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read locales directory: %w", err)
	}
	var stamped []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !e.Type().IsRegular() || !strings.HasSuffix(name, ".json") {
			continue
		}
		tag := strings.TrimSuffix(name, ".json")
		if tag == "en" {
			continue
		}
		if !pack.ValidTag(tag) {
			return stamped, fmt.Errorf("locales/%s: %q is not a valid language tag", name, tag)
		}
		path := filepath.Join(localesDir, name)
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return stamped, fmt.Errorf("read locales/%s: %w", name, err)
		}
		tr, err := pack.ParseEntries(data)
		if err != nil {
			return stamped, fmt.Errorf("locales/%s: %w", name, err)
		}
		out, changed := pack.Stamp(ref, tr)
		if !changed {
			continue
		}
		encoded, err := pack.Marshal(out)
		if err != nil {
			return stamped, fmt.Errorf("encode locales/%s: %w", name, err)
		}
		if err := writeInDir(localesDir, name, encoded); err != nil {
			return stamped, fmt.Errorf("write locales/%s: %w", name, err)
		}
		stamped = append(stamped, path)
	}
	sort.Strings(stamped)
	return stamped, nil
}

// writeInDir writes name inside dir through an os.Root, so the write cannot
// escape dir whatever the directory listing contained.
func writeInDir(dir, name string, data []byte) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open %s: %w", dir, err)
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile(name, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}
