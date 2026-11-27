package themepkg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// APIVersion is the only theme.yaml apiVersion this module accepts.
const APIVersion = "gameplane.local/theme/v1"

// MaxManifestBytes caps theme.yaml; larger files are refused before decoding.
const MaxManifestBytes = 16 * 1024

// ModeTokens holds the optional light and dark token maps of a theme. A nil
// map means the mode is not offered.
type ModeTokens struct {
	Light Tokens `yaml:"light,omitempty" json:"light,omitempty"`
	Dark  Tokens `yaml:"dark,omitempty" json:"dark,omitempty"`
}

// Manifest is the decoded theme.yaml (contracts/theme-package.md).
type Manifest struct {
	APIVersion  string     `yaml:"apiVersion" json:"apiVersion"`
	Name        string     `yaml:"name" json:"name"`
	Version     string     `yaml:"version" json:"version"`
	DisplayName string     `yaml:"displayName" json:"displayName"`
	Author      string     `yaml:"author" json:"author"`
	Description string     `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        []string   `yaml:"tags,omitempty" json:"tags,omitempty"`
	License     string     `yaml:"license" json:"license"`
	Homepage    string     `yaml:"homepage,omitempty" json:"homepage,omitempty"`
	Changelog   string     `yaml:"changelog,omitempty" json:"changelog,omitempty"`
	Tokens      ModeTokens `yaml:"tokens" json:"tokens"`
}

var (
	yamlLineRE = regexp.MustCompile(`line (\d+)`)
	dns1123RE  = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	semverRE   = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
	tagRE      = regexp.MustCompile(`^[a-z0-9-]+$`)
	spdxRE     = regexp.MustCompile(`^[A-Za-z0-9.+-]+( (AND|OR|WITH) [A-Za-z0-9.+-]+)*$`)
)

// Modes lists "light" and/or "dark", in that order, for each mode whose token
// map is non-nil.
func (m Manifest) Modes() []string {
	var modes []string
	if m.Tokens.Light != nil {
		modes = append(modes, "light")
	}
	if m.Tokens.Dark != nil {
		modes = append(modes, "dark")
	}
	return modes
}

// DecodeManifest strictly decodes theme.yaml and reports every rule it breaks.
// A YAML decode failure stops at that one problem; otherwise all field rules
// are collected. The decoded Manifest is returned even when problems exist.
func DecodeManifest(data []byte) (Manifest, []Problem) {
	if len(data) > MaxManifestBytes {
		return Manifest{}, []Problem{schemaProblem("theme.yaml exceeds 16 KiB")}
	}
	var m Manifest
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return Manifest{}, []Problem{schemaProblem("theme.yaml is empty")}
		}
		return m, []Problem{decodeProblem(err)}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return m, []Problem{schemaProblem("theme.yaml must hold one document")}
	}

	var probs []Problem
	probs = append(probs, checkIdentity(m)...)
	probs = append(probs, checkLength("displayName", m.DisplayName, true, 64)...)
	probs = append(probs, checkLength("author", m.Author, true, 64)...)
	probs = append(probs, checkLength("description", m.Description, false, 280)...)
	probs = append(probs, checkTags(m.Tags)...)
	probs = append(probs, checkLicense(m.License)...)
	probs = append(probs, checkHomepage(m.Homepage)...)
	probs = append(probs, checkChangelog(m.Changelog)...)
	probs = append(probs, checkTokens(m)...)
	return m, probs
}

// schemaProblem builds a RuleSchema problem without a line.
func schemaProblem(detail string) Problem {
	return Problem{Rule: RuleSchema, Detail: detail}
}

// decodeProblem turns a yaml.v3 decode error into one RuleSchema problem,
// taking the line from the error text when it names one.
func decodeProblem(err error) Problem {
	p := schemaProblem("theme.yaml: " + err.Error())
	if sub := yamlLineRE.FindStringSubmatch(err.Error()); sub != nil {
		if n, convErr := strconv.Atoi(sub[1]); convErr == nil {
			p.Line = n
		}
	}
	return p
}

// checkIdentity checks apiVersion, name and version.
func checkIdentity(m Manifest) []Problem {
	var out []Problem
	if m.APIVersion != APIVersion {
		out = append(out, schemaProblem("apiVersion must be "+APIVersion))
	}
	switch {
	case m.Name == "":
		out = append(out, schemaProblem("name is required"))
	case len(m.Name) > 63 || !dns1123RE.MatchString(m.Name):
		out = append(out, schemaProblem("name must be a DNS-1123 label of at most 63 characters"))
	}
	return append(out, checkVersion(m.Version)...)
}

// checkVersion accepts MAJOR.MINOR.PATCH with optional prerelease, refusing a
// leading v, build metadata and leading zeros in numeric prerelease ids.
func checkVersion(v string) []Problem {
	switch {
	case v == "":
		return []Problem{schemaProblem("version is required")}
	case strings.Contains(v, "+"):
		return []Problem{schemaProblem("version must not carry +build metadata")}
	case !semverRE.MatchString(v):
		return []Problem{schemaProblem("version must be MAJOR.MINOR.PATCH with optional -prerelease and no leading v")}
	}
	if _, pre, ok := strings.Cut(v, "-"); ok && numericLeadingZero(pre) {
		return []Problem{schemaProblem("version prerelease identifiers must not have leading zeros")}
	}
	return nil
}

// numericLeadingZero reports whether any dot-separated prerelease identifier
// is numeric with a leading zero.
func numericLeadingZero(pre string) bool {
	for _, id := range strings.Split(pre, ".") {
		if len(id) > 1 && id[0] == '0' && strings.Trim(id, "0123456789") == "" {
			return true
		}
	}
	return false
}

// checkLength checks a free-text field against a rune-count limit, and
// optionally requires it to be non-empty.
func checkLength(field, value string, required bool, maxRunes int) []Problem {
	switch {
	case value == "" && required:
		return []Problem{schemaProblem(field + " is required")}
	case utf8.RuneCountInString(value) > maxRunes:
		return []Problem{schemaProblem(fmt.Sprintf("%s must be at most %d characters", field, maxRunes))}
	}
	return nil
}

// checkTags enforces at most 8 unique tags of 1-24 lowercase [a-z0-9-] characters.
func checkTags(tags []string) []Problem {
	var out []Problem
	if len(tags) > 8 {
		out = append(out, schemaProblem("tags must hold at most 8 entries"))
	}
	seen := make(map[string]bool, len(tags))
	for i, tag := range tags {
		if len(tag) > 24 || !tagRE.MatchString(tag) {
			out = append(out, schemaProblem(fmt.Sprintf("tags[%d] must be 1-24 characters of a-z, 0-9 or -", i)))
			continue
		}
		if seen[tag] {
			out = append(out, schemaProblem(fmt.Sprintf("tags[%d] duplicates %q", i, tag)))
		}
		seen[tag] = true
	}
	return out
}

// checkLicense requires an SPDX-shaped expression of at most 64 characters.
func checkLicense(license string) []Problem {
	if license == "" {
		return []Problem{schemaProblem("license is required")}
	}
	out := checkLength("license", license, false, 64)
	if !spdxRE.MatchString(license) {
		out = append(out, schemaProblem("license must be an SPDX expression such as MIT or Apache-2.0 OR MIT"))
	}
	return out
}

// checkHomepage accepts an absent homepage, or an https URL with a host and no user info.
func checkHomepage(raw string) []Problem {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return []Problem{schemaProblem("homepage must be a valid URL")}
	}
	var out []Problem
	if u.Scheme != "https" {
		out = append(out, schemaProblem("homepage must use https"))
	}
	if u.Host == "" {
		out = append(out, schemaProblem("homepage must include a host"))
	}
	if u.User != nil {
		out = append(out, schemaProblem("homepage must not include user info"))
	}
	return out
}

// checkChangelog enforces at most 4 KiB of plain text (tab, LF and CR allowed).
func checkChangelog(s string) []Problem {
	var out []Problem
	if len(s) > 4096 {
		out = append(out, schemaProblem("changelog must be at most 4 KiB"))
	}
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			out = append(out, schemaProblem("changelog must be plain text without control characters"))
			break
		}
	}
	return out
}

// checkTokens requires at least one mode and validates each present mode.
func checkTokens(m Manifest) []Problem {
	modes := m.Modes()
	if len(modes) == 0 {
		return []Problem{schemaProblem("tokens must define light, dark or both")}
	}
	var out []Problem
	for _, mode := range modes {
		out = append(out, ValidateTokens(mode, modeTokens(m, mode))...)
	}
	return out
}

// modeTokens returns the token map of one mode of m.
func modeTokens(m Manifest, mode string) Tokens {
	if mode == "light" {
		return m.Tokens.Light
	}
	return m.Tokens.Dark
}
