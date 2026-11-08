package pack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// ReviewFile is the name of the file that records, for each translated id, the
// hash of the English text the translation was made from. App packs keep it
// beside app.json (locales/<tag>/.review.json); module packs keep one for all
// languages in locales/.review.json. It is separate from the translation files
// because translation platforms such as Hosted Weblate rewrite those and drop
// unknown keys (research R8).
const ReviewFile = ".review.json"

// ParseReview decodes an app review file: a JSON object that maps each
// translated id to the 12-hex source hash of the English text it was made from.
// Every value must be a non-empty string and trailing data is rejected.
func ParseReview(data []byte) (map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var hashes map[string]string
	if err := dec.Decode(&hashes); err != nil {
		return nil, fmt.Errorf("pack: parse review: %w", err)
	}
	if hashes == nil {
		return nil, errors.New("pack: parse review: top level must be a JSON object")
	}
	if err := expectEOF(dec); err != nil {
		return nil, fmt.Errorf("pack: parse review: %w", err)
	}
	for _, id := range slices.Sorted(maps.Keys(hashes)) {
		if hashes[id] == "" {
			return nil, fmt.Errorf("pack: parse review: id %q has an empty hash", id)
		}
	}
	return hashes, nil
}

// MarshalReview encodes an app review file with the same layout as Marshal:
// keys sorted, two-space indentation, and a trailing newline. A nil map
// encodes as an empty object.
func MarshalReview(hashes map[string]string) ([]byte, error) {
	if hashes == nil {
		hashes = map[string]string{}
	}
	data, err := encodeJSON(hashes)
	if err != nil {
		return nil, fmt.Errorf("pack: marshal review: %w", err)
	}
	return data, nil
}

// ParseModuleReview decodes a module review file: a JSON object that maps each
// language tag to an object of translated id -> source hash. Tags must be valid
// language tags, every hash must be a non-empty string, and trailing data is
// rejected.
func ParseModuleReview(data []byte) (map[string]map[string]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var review map[string]map[string]string
	if err := dec.Decode(&review); err != nil {
		return nil, fmt.Errorf("pack: parse module review: %w", err)
	}
	if review == nil {
		return nil, errors.New("pack: parse module review: top level must be a JSON object")
	}
	if err := expectEOF(dec); err != nil {
		return nil, fmt.Errorf("pack: parse module review: %w", err)
	}
	for _, tag := range slices.Sorted(maps.Keys(review)) {
		if !ValidTag(tag) {
			return nil, fmt.Errorf("pack: parse module review: %q is not a valid language tag", tag)
		}
		hashes := review[tag]
		if hashes == nil {
			return nil, fmt.Errorf("pack: parse module review: tag %q must map to an object", tag)
		}
		for _, id := range slices.Sorted(maps.Keys(hashes)) {
			if hashes[id] == "" {
				return nil, fmt.Errorf("pack: parse module review: tag %q id %q has an empty hash", tag, id)
			}
		}
	}
	return review, nil
}

// MarshalModuleReview encodes a module review file with the same layout as
// MarshalReview. Nil inner maps encode as empty objects.
func MarshalModuleReview(review map[string]map[string]string) ([]byte, error) {
	out := make(map[string]map[string]string, len(review))
	for tag, hashes := range review {
		if hashes == nil {
			hashes = map[string]string{}
		}
		out[tag] = hashes
	}
	data, err := encodeJSON(out)
	if err != nil {
		return nil, fmt.Errorf("pack: marshal module review: %w", err)
	}
	return data, nil
}

// ApplyReview returns a copy of entries in which the SourceHash of every id that
// hashes also names is set from hashes. A review hash overrides an inline one.
// Ids that appear only in hashes are ignored, and entries is not modified.
func ApplyReview(entries map[string]Entry, hashes map[string]string) map[string]Entry {
	out := make(map[string]Entry, len(entries))
	for id, e := range entries {
		out[id] = e
	}
	for id, hash := range hashes {
		if e, ok := out[id]; ok {
			e.SourceHash = hash
			out[id] = e
		}
	}
	return out
}

// SplitReview returns a copy of entries with every SourceHash cleared, and the
// hashes that were cleared, keyed by id. Entries without a hash are left out of
// the map, which is never nil. entries is not modified.
func SplitReview(entries map[string]Entry) (map[string]Entry, map[string]string) {
	stripped := make(map[string]Entry, len(entries))
	hashes := map[string]string{}
	for id, e := range entries {
		if e.SourceHash != "" {
			hashes[id] = e.SourceHash
		}
		e.SourceHash = ""
		stripped[id] = e
	}
	return stripped, hashes
}

// HasInlineHashes reports whether any entry still carries a sourceHash key.
func HasInlineHashes(entries map[string]Entry) bool {
	for _, e := range entries {
		if e.SourceHash != "" {
			return true
		}
	}
	return false
}

// encodeJSON encodes v with the layout used for pack files: sorted keys,
// two-space indentation, HTML characters unescaped, and a trailing newline.
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
