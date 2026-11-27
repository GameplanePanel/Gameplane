package theme

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GameplanePanel/gameplane/themepkg"
	"gopkg.in/yaml.v3"
)

// DefaultVersion is the version written into a new package.
const DefaultVersion = "0.1.0"

// ErrExists reports that the target folder already holds a theme.yaml, so
// scaffolding would overwrite the author's work.
var ErrExists = errors.New("theme.yaml already exists")

// InitOptions selects what `theme init` scaffolds.
type InitOptions struct {
	// Modes lists the token modes to include ("light", "dark"). Empty means both.
	Modes []string
	// Preset is the built-in preset the tokens are copied from. Empty means pink.
	Preset string
}

// Init scaffolds a theme package in dir: a theme.yaml with the chosen preset's
// tokens and an empty theme.css. The author and license fields are left blank
// on purpose, so `theme validate` reports them until they are filled in.
func Init(dir, name string, opts InitOptions) error {
	if name == "" {
		return errors.New("a theme name is required")
	}
	preset := opts.Preset
	if preset == "" {
		preset = PresetPink
	}
	modes, err := normalizeModes(opts.Modes)
	if err != nil {
		return err
	}
	tokens := themepkg.ModeTokens{}
	for _, mode := range modes {
		t, err := presetFor(preset, mode)
		if err != nil {
			return err
		}
		if mode == "light" {
			tokens.Light = t
		} else {
			tokens.Dark = t
		}
	}
	m := themepkg.Manifest{
		APIVersion:  themepkg.APIVersion,
		Name:        name,
		Version:     DefaultVersion,
		DisplayName: displayNameFor(name),
		Tokens:      tokens,
	}
	// An empty, non-nil CSS slice makes writePackage create theme.css (empty).
	return writePackage(dir, m, []byte{})
}

// normalizeModes turns the requested modes into an ordered, de-duplicated list.
// Empty input means both modes.
func normalizeModes(in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{"light", "dark"}, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range in {
		if m != "light" && m != "dark" {
			return nil, fmt.Errorf("unknown mode %q (want light or dark)", m)
		}
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	if seen["light"] && seen["dark"] {
		return []string{"light", "dark"}, nil
	}
	return out, nil
}

// displayNameFor turns a DNS-1123 name such as deep-ocean into "Deep Ocean".
func displayNameFor(name string) string {
	parts := strings.Split(name, "-")
	for i, p := range parts {
		if p != "" {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// writePackage writes theme.yaml (created exclusively, so an existing package
// is never overwritten) and theme.css when css is non-nil. The folder is
// created if needed. Previews are not written: the author adds them.
func writePackage(dir string, m themepkg.Manifest, css []byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	manifestPath := filepath.Join(dir, "theme.yaml")
	f, err := os.OpenFile(filepath.Clean(manifestPath), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s: %w", manifestPath, ErrExists)
		}
		return fmt.Errorf("create %s: %w", manifestPath, err)
	}
	if _, err := f.Write(renderManifest(m)); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", manifestPath, err)
	}
	if css != nil {
		if err := os.WriteFile(filepath.Join(dir, "theme.css"), css, 0o600); err != nil {
			return fmt.Errorf("write theme.css: %w", err)
		}
	}
	return nil
}

// renderManifest writes theme.yaml in the field order of
// contracts/theme-package.md. Scalars go through yaml.v3 so any value is
// quoted safely; optional fields are omitted when empty.
func renderManifest(m themepkg.Manifest) []byte {
	var b bytes.Buffer
	b.WriteString("# Theme package (spec 043). Run `gp-module theme validate <dir>` after editing.\n")
	field := func(key, value string) {
		fmt.Fprintf(&b, "%s: %s\n", key, yamlScalar(value))
	}
	field("apiVersion", m.APIVersion)
	field("name", m.Name)
	field("version", m.Version)
	field("displayName", m.DisplayName)
	field("author", m.Author)
	if m.Description != "" {
		field("description", m.Description)
	}
	if len(m.Tags) > 0 {
		quoted := make([]string, len(m.Tags))
		for i, t := range m.Tags {
			quoted[i] = yamlScalar(t)
		}
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(quoted, ", "))
	}
	field("license", m.License)
	if m.Homepage != "" {
		field("homepage", m.Homepage)
	}
	b.WriteString("tokens:\n")
	for _, mode := range []struct {
		name   string
		tokens themepkg.Tokens
	}{{"light", m.Tokens.Light}, {"dark", m.Tokens.Dark}} {
		if mode.tokens == nil {
			continue
		}
		fmt.Fprintf(&b, "  %s:\n", mode.name)
		for _, name := range themepkg.TokenNames {
			fmt.Fprintf(&b, "    %s: %s\n", name, yamlScalar(mode.tokens[name]))
		}
	}
	return b.Bytes()
}

// yamlScalar returns s as a YAML scalar, quoted when YAML requires it.
func yamlScalar(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		// Marshalling a string cannot fail; fall back to a quoted form anyway.
		return fmt.Sprintf("%q", s)
	}
	return strings.TrimSuffix(string(out), "\n")
}
