// Package pack reads, writes and validates language pack files: the per-language
// app.json entry maps, their meta.json descriptors, and the English reference
// that translators work from.
package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// ListingThreshold is the minimum completeness percentage at which a
// translation is offered to users.
const ListingThreshold = 80.0

// Entry is one message in an app.json pack.
type Entry struct {
	DefaultMessage string `json:"defaultMessage"`
	Description    string `json:"description,omitempty"`
	SourceHash     string `json:"sourceHash,omitempty"`
}

// Credit names a contributor of a translation.
type Credit struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// Meta describes a language pack in meta.json.
type Meta struct {
	Tag         string   `json:"tag"`
	NativeName  string   `json:"nativeName"`
	EnglishName string   `json:"englishName"`
	Direction   string   `json:"direction"`
	Credits     []Credit `json:"credits,omitempty"`
}

var tagRE = regexp.MustCompile(`^[a-z]{2,3}(-[A-Z][a-z]{3})?(-([A-Z]{2}|[0-9]{3}))?$`)

// ValidTag reports whether tag is a well-formed BCP 47 language tag in the
// subset Gameplane accepts (language, optional script, optional region).
func ValidTag(tag string) bool {
	return tagRE.MatchString(tag)
}

// ParseEntries decodes an app.json file. The top level must be a JSON object
// whose values are entry objects; unknown entry keys are rejected.
func ParseEntries(data []byte) (map[string]Entry, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("pack: parse entries: %w", err)
	}
	if raw == nil {
		return nil, errors.New("pack: parse entries: top level must be a JSON object")
	}
	out := make(map[string]Entry, len(raw))
	for id, msg := range raw {
		dec := json.NewDecoder(bytes.NewReader(msg))
		dec.DisallowUnknownFields()
		var e Entry
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("pack: entry %q: %w", id, err)
		}
		if err := expectEOF(dec); err != nil {
			return nil, fmt.Errorf("pack: entry %q: %w", id, err)
		}
		if e.DefaultMessage == "" {
			return nil, fmt.Errorf("pack: entry %q has no defaultMessage", id)
		}
		out[id] = e
	}
	return out, nil
}

// ParseMeta decodes a meta.json file, rejecting unknown keys and trailing data.
func ParseMeta(data []byte) (Meta, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Meta
	if err := dec.Decode(&m); err != nil {
		return Meta{}, fmt.Errorf("pack: parse meta: %w", err)
	}
	if err := expectEOF(dec); err != nil {
		return Meta{}, fmt.Errorf("pack: parse meta: %w", err)
	}
	return m, nil
}

func expectEOF(dec *json.Decoder) error {
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after JSON value")
	}
	return nil
}

// Marshal encodes entries as app.json: keys sorted, two-space indentation,
// HTML characters left unescaped, and a trailing newline.
func Marshal(entries map[string]Entry) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(entries); err != nil {
		return nil, fmt.Errorf("pack: marshal entries: %w", err)
	}
	return buf.Bytes(), nil
}

// SourceHash returns the first 12 hex characters of the SHA-256 of the English
// message. It records which English text a translation was made from.
func SourceHash(english string) string {
	sum := sha256.Sum256([]byte(english))
	return hex.EncodeToString(sum[:])[:12]
}
