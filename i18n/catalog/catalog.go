// Package catalog loads the embedded language packs and resolves message ids
// to formatted text at runtime.
package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sync"

	"golang.org/x/text/language"

	"github.com/GameplanePanel/gameplane/i18n"
	"github.com/GameplanePanel/gameplane/i18n/icu"
	"github.com/GameplanePanel/gameplane/i18n/pack"
)

// Catalog is an immutable set of validated language packs plus a cache of
// parsed messages. It is safe for concurrent use.
type Catalog struct {
	reference    string
	packs        map[string]map[string]pack.Entry // tag -> valid entries; reference included
	meta         map[string]pack.Meta
	completeness map[string]float64

	mu    sync.RWMutex
	cache map[cacheKey]icu.Message
}

type cacheKey struct{ tag, id string }

type languagePack struct {
	tag     string
	entries map[string]pack.Entry
	meta    pack.Meta
}

func newCatalog(reference string) *Catalog {
	return &Catalog{
		reference:    reference,
		packs:        map[string]map[string]pack.Entry{},
		meta:         map[string]pack.Meta{},
		completeness: map[string]float64{},
		cache:        map[cacheKey]icu.Message{},
	}
}

// Load reads every language pack under the locales/ directory of fsys (pass
// i18n.Locales in production). Every directory with an app.json is a language.
// The reference pack must parse and may omit meta.json; other packs are skipped
// when their app.json or meta.json is missing or invalid. Translations are
// reduced to the entries pack.Valid keeps.
func Load(fsys fs.FS, reference string) (*Catalog, error) {
	dirs, err := fs.ReadDir(fsys, "locales")
	if err != nil {
		return nil, fmt.Errorf("catalog: read locales: %w", err)
	}

	var (
		refEntries map[string]pack.Entry
		refMeta    pack.Meta
		others     []languagePack
	)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		tag := d.Name()
		entries, err := readEntries(fsys, tag)
		meta, metaErr := readMeta(fsys, tag)
		if tag == reference {
			if err != nil {
				return nil, fmt.Errorf("catalog: reference %q: %w", tag, err)
			}
			if metaErr != nil && !errors.Is(metaErr, fs.ErrNotExist) {
				return nil, fmt.Errorf("catalog: reference %q meta: %w", tag, metaErr)
			}
			refEntries = entries
			refMeta = pack.Meta{Tag: tag, Direction: "ltr"}
			if metaErr == nil {
				refMeta = meta
			}
			continue
		}
		if err != nil || metaErr != nil || meta.Tag != tag {
			continue
		}
		others = append(others, languagePack{tag: tag, entries: entries, meta: meta})
	}
	if refEntries == nil {
		return nil, fmt.Errorf("catalog: reference %q not found under locales/", reference)
	}

	c := newCatalog(reference)
	c.packs[reference] = refEntries
	c.meta[reference] = refMeta
	c.completeness[reference] = 100
	for _, p := range others {
		c.packs[p.tag] = pack.Valid(refEntries, p.entries)
		c.meta[p.tag] = p.meta
		c.completeness[p.tag] = pack.Completeness(refEntries, p.entries)
	}
	return c, nil
}

func readEntries(fsys fs.FS, tag string) (map[string]pack.Entry, error) {
	data, err := fs.ReadFile(fsys, path.Join("locales", tag, "app.json"))
	if err != nil {
		return nil, err
	}
	return pack.ParseEntries(data)
}

func readMeta(fsys fs.FS, tag string) (pack.Meta, error) {
	data, err := fs.ReadFile(fsys, path.Join("locales", tag, "meta.json"))
	if err != nil {
		return pack.Meta{}, err
	}
	return pack.ParseMeta(data)
}

// defaultOnce guards the lazily loaded embedded catalog.
var (
	defaultOnce sync.Once
	defaultCat  *Catalog
)

// Default returns the catalog built from the embedded packs in package i18n.
// If the embedded packs cannot be loaded it returns an empty catalog, so every
// Lookup reports false and callers fall back to their own text.
func Default() *Catalog {
	defaultOnce.Do(func() {
		c, err := Load(i18n.Locales, "en")
		if err != nil {
			c = newCatalog("en")
		}
		defaultCat = c
	})
	return defaultCat
}

// Tags returns every loaded language tag, the reference first and the rest
// sorted.
func (c *Catalog) Tags() []string {
	others := make([]string, 0, len(c.packs))
	for tag := range c.packs {
		if tag != c.reference {
			others = append(others, tag)
		}
	}
	slices.Sort(others)
	if _, ok := c.packs[c.reference]; ok {
		return append([]string{c.reference}, others...)
	}
	return others
}

// Listed returns the tags offered to users, in the order of Tags.
func (c *Catalog) Listed() []string {
	var out []string
	for _, tag := range c.Tags() {
		if c.IsListed(tag) {
			out = append(out, tag)
		}
	}
	return out
}

// IsListed reports whether tag is offered to users. The reference is always
// listed.
func (c *Catalog) IsListed(tag string) bool {
	comp, ok := c.completeness[tag]
	if !ok {
		return false
	}
	if tag == c.reference {
		return true
	}
	return pack.Listed(tag, c.meta[tag], comp)
}

// Completeness returns the percentage of reference messages that tag translates.
func (c *Catalog) Completeness(tag string) (float64, bool) {
	comp, ok := c.completeness[tag]
	return comp, ok
}

// Lookup formats message id for tag. It tries the exact tag, then its base
// language, then the reference. Entries that fail to parse fall through to the
// next pack. ok is false only when the reference does not have id.
func (c *Catalog) Lookup(tag language.Tag, id string, args map[string]any) (string, bool) {
	base, _ := tag.Base()
	keys := []string{tag.String(), base.String(), c.reference}
	for i, key := range keys {
		if slices.Contains(keys[:i], key) {
			continue
		}
		entry, ok := c.packs[key][id]
		if !ok {
			continue
		}
		msg, err := c.parse(key, id, entry.DefaultMessage)
		if err != nil {
			continue
		}
		return icu.Format(msg, language.Make(key), args), true
	}
	return "", false
}

// parse returns the parsed message for key/id, caching successful parses.
func (c *Catalog) parse(key, id, text string) (icu.Message, error) {
	ck := cacheKey{tag: key, id: id}
	c.mu.RLock()
	msg, ok := c.cache[ck]
	c.mu.RUnlock()
	if ok {
		return msg, nil
	}
	msg, err := icu.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("catalog: parse %s/%s: %w", key, id, err)
	}
	c.mu.Lock()
	c.cache[ck] = msg
	c.mu.Unlock()
	return msg, nil
}

// Tags returns the tags of the default catalog.
func Tags() []string { return Default().Tags() }

// Listed returns the listed tags of the default catalog.
func Listed() []string { return Default().Listed() }

// IsListed reports whether tag is listed in the default catalog.
func IsListed(tag string) bool { return Default().IsListed(tag) }

// Lookup formats message id using the default catalog.
func Lookup(tag language.Tag, id string, args map[string]any) (string, bool) {
	return Default().Lookup(tag, id, args)
}
