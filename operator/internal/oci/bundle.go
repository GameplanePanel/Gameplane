// Package oci wraps oras-go to pull Gameplane module bundles from any
// OCI-compliant registry. Bundles are single OCI artifacts whose layers
// carry the module metadata + GameTemplate manifest. This package is
// pure transport — bundle parsing and validation live in
// internal/modsrc, which consumes the raw layers returned by Pull.
package oci

import (
	"bytes"
	"sort"
)

// Media types for the Gameplane module artifact format. The full
// specification is in docs/module-authoring.md.
const (
	ArtifactType      = "application/vnd.gameplane.module.v1+json"
	MediaTypeConfig   = "application/vnd.gameplane.module.config.v1+json"
	MediaTypeMetadata = "application/vnd.gameplane.module.metadata.v1+yaml"
	MediaTypeTemplate = "application/vnd.gameplane.module.template.v1+yaml"
	MediaTypeReadme   = "application/vnd.gameplane.module.readme.v1+md"
	MediaTypeIconPNG  = "image/png"
	MediaTypeLocale   = "application/vnd.gameplane.module.locale.v1+json"

	// AnnotationTitle is the OCI annotation that carries each layer's
	// filename inside the bundle (e.g. "module.yaml"). Pull keys the
	// returned layer map by this value; the canonical filenames are
	// modsrc.File* (the title annotation must match them exactly).
	AnnotationTitle = "org.opencontainers.image.title"

	// LayerNameMetadata, etc. duplicate the modsrc.File* canonical
	// filenames at the OCI layer-annotation level (this package cannot
	// import modsrc — modsrc wraps this client).
	LayerNameMetadata = "module.yaml"
	LayerNameTemplate = "template.yaml"
	LayerNameReadme   = "README.md"
	LayerNameIcon     = "icon.png"
)

// Locale layers carry module translations. Each is titled
// "locales/<tag>.json" and typed MediaTypeLocale.
const (
	LocaleLayerPrefix = "locales/"
	LocaleLayerSuffix = ".json"

	// MaxLocaleLayerSize bounds one locale layer. Pull refuses to read a
	// larger one, so a hostile bundle cannot exhaust the operator's memory.
	MaxLocaleLayerSize = 900 * 1024

	// MaxLocaleTotalSize bounds all locale files of one bundle together.
	// Languages are counted in file-name order; one that would push the running
	// total over this bound is recorded as oversized and not read, like a file
	// over MaxLocaleLayerSize.
	MaxLocaleTotalSize = 4 * 1024 * 1024
)

// LocaleBudget tracks the total bytes of locale files read for one bundle.
type LocaleBudget struct {
	used int64
}

// Take reserves size bytes and reports whether they fit within
// MaxLocaleTotalSize. A size that does not fit reserves nothing, so a smaller
// later language can still fit.
func (b *LocaleBudget) Take(size int64) bool {
	if size < 0 || b.used+size > MaxLocaleTotalSize {
		return false
	}
	b.used += size
	return true
}

// OversizedLocale is stored in Pull's title-keyed map in place of a locale
// layer larger than MaxLocaleLayerSize. The blob is never read; the marker lets
// the reconciler report the language instead of losing it silently. A real file
// equal to this text is not valid JSON, so it cannot collide with content.
var OversizedLocale = []byte("gameplane:oversized-locale")

// IsOversizedLocale reports whether data is the OversizedLocale marker.
func IsOversizedLocale(data []byte) bool {
	return bytes.Equal(data, OversizedLocale)
}

// SplitLocaleLayers separates the oversized markers from the locale layers in
// files (Pull's title-keyed map). locales holds tag -> bytes like LocaleLayers
// without the markers; oversized lists the tags whose file was too large, sorted.
// The English reference "en" is skipped in both.
func SplitLocaleLayers(files map[string][]byte) (locales map[string][]byte, oversized []string) {
	locales = map[string][]byte{}
	for tag, data := range LocaleLayers(files) {
		if IsOversizedLocale(data) {
			oversized = append(oversized, tag)
			continue
		}
		locales[tag] = data
	}
	sort.Strings(oversized)
	return locales, oversized
}

// LocaleTag returns the language tag of a locale layer title of the form
// "locales/<tag>.json". The tag is not validated here.
func LocaleTag(title string) (string, bool) {
	if len(title) <= len(LocaleLayerPrefix)+len(LocaleLayerSuffix) {
		return "", false
	}
	if title[:len(LocaleLayerPrefix)] != LocaleLayerPrefix || title[len(title)-len(LocaleLayerSuffix):] != LocaleLayerSuffix {
		return "", false
	}
	tag := title[len(LocaleLayerPrefix) : len(title)-len(LocaleLayerSuffix)]
	for i := 0; i < len(tag); i++ {
		if tag[i] == '/' {
			return "", false
		}
	}
	return tag, true
}

// LocaleLayers returns tag -> bytes for every locale layer in files (Pull's
// title-keyed map). The English reference "en" is skipped: it is derived from
// the template, never shipped.
func LocaleLayers(files map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for title, data := range files {
		tag, ok := LocaleTag(title)
		if !ok || tag == "en" {
			continue
		}
		out[tag] = data
	}
	return out
}
