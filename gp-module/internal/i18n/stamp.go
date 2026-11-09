package i18n

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// Stamp records the current English source hash of every translated entry of
// the module's locales/<tag>.json files that does not have one yet. Hashes live
// in locales/.review.json, keyed by tag, because translation platforms rewrite
// the translation files and drop unknown keys; a sourceHash still inside a
// <tag>.json is read and moved there. The English reference en.json is left
// alone. It returns the files it rewrote, sorted. Nothing is written until
// every locale parses, and the review file is written before any locale file
// loses its inline hashes, so a failure never drops a hash.
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
	review, err := readModuleReview(localesDir)
	if err != nil {
		return nil, err
	}
	type pending struct {
		name string
		data []byte
	}
	var writes []pending
	reviewChanged := false
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !e.Type().IsRegular() || !strings.HasSuffix(name, ".json") || name == pack.ReviewFile {
			continue
		}
		tag := strings.TrimSuffix(name, ".json")
		if tag == "en" {
			continue
		}
		if !pack.ValidTag(tag) {
			return nil, fmt.Errorf("locales/%s: %q is not a valid language tag", name, tag)
		}
		path := filepath.Join(localesDir, name)
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return nil, fmt.Errorf("read locales/%s: %w", name, err)
		}
		tr, err := pack.ParseEntries(data)
		if err != nil {
			return nil, fmt.Errorf("locales/%s: %w", name, err)
		}
		inline := pack.HasInlineHashes(tr)
		tr = pack.ApplyReview(tr, review[tag])
		out, changed := pack.Stamp(ref, tr)
		if !changed && !inline {
			continue
		}
		stripped, hashes := pack.SplitReview(out)
		review[tag] = hashes
		reviewChanged = true
		if !inline {
			continue
		}
		// Move the hashes out of the translation file, which translation
		// platforms rewrite.
		encoded, err := pack.Marshal(stripped)
		if err != nil {
			return nil, fmt.Errorf("encode locales/%s: %w", name, err)
		}
		writes = append(writes, pending{name: name, data: encoded})
	}
	var stamped []string
	if reviewChanged {
		encoded, err := pack.MarshalModuleReview(review)
		if err != nil {
			return stamped, fmt.Errorf("encode locales/%s: %w", pack.ReviewFile, err)
		}
		if err := writeInDir(localesDir, pack.ReviewFile, encoded); err != nil {
			return stamped, fmt.Errorf("write locales/%s: %w", pack.ReviewFile, err)
		}
		stamped = append(stamped, filepath.Join(localesDir, pack.ReviewFile))
	}
	for _, w := range writes {
		if err := writeInDir(localesDir, w.name, w.data); err != nil {
			return stamped, fmt.Errorf("write locales/%s: %w", w.name, err)
		}
		stamped = append(stamped, filepath.Join(localesDir, w.name))
	}
	sort.Strings(stamped)
	return stamped, nil
}

// readModuleReview reads locales/.review.json. A missing file yields an empty
// review; a file that does not parse is an error.
func readModuleReview(localesDir string) (map[string]map[string]string, error) {
	data, err := os.ReadFile(filepath.Clean(filepath.Join(localesDir, pack.ReviewFile)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]map[string]string{}, nil
		}
		return nil, fmt.Errorf("read locales/%s: %w", pack.ReviewFile, err)
	}
	review, err := pack.ParseModuleReview(data)
	if err != nil {
		return nil, fmt.Errorf("locales/%s: %w", pack.ReviewFile, err)
	}
	return review, nil
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
