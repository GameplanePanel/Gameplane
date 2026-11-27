package theme

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/themepkg"
)

// requireOnlyMetadataProblems fails the test unless every problem is a schema
// problem about the author or license, the two fields a fresh scaffold leaves
// blank on purpose.
func requireOnlyMetadataProblems(t *testing.T, probs []themepkg.Problem) {
	t.Helper()
	for _, p := range probs {
		if p.Rule != themepkg.RuleSchema || !(strings.Contains(p.Detail, "author") || strings.Contains(p.Detail, "license")) {
			t.Fatalf("unexpected problem in a fresh scaffold: %v", p)
		}
	}
}

func TestInitWritesBothModesFromPink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deep-ocean")
	if err := Init(dir, "deep-ocean", InitOptions{}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	m, probs := themepkg.DecodeManifest(readFile(t, filepath.Join(dir, "theme.yaml")))
	requireOnlyMetadataProblems(t, probs)
	if m.Name != "deep-ocean" || m.Version != DefaultVersion || m.DisplayName != "Deep Ocean" {
		t.Fatalf("identity = %q %q %q", m.Name, m.Version, m.DisplayName)
	}
	if got := m.Modes(); len(got) != 2 || got[0] != "light" || got[1] != "dark" {
		t.Fatalf("modes = %v, want [light dark]", got)
	}
	want, err := presetFor(PresetPink, "light")
	if err != nil {
		t.Fatalf("presetFor: %v", err)
	}
	if !tokensEqual(m.Tokens.Light, want) {
		t.Fatalf("light tokens do not match the pink preset")
	}
	if len(m.Tokens.Dark) != len(themepkg.TokenNames) {
		t.Fatalf("dark tokens = %d, want %d", len(m.Tokens.Dark), len(themepkg.TokenNames))
	}
	if css := readFile(t, filepath.Join(dir, "theme.css")); len(css) != 0 {
		t.Fatalf("theme.css should be empty, got %d bytes", len(css))
	}
}

func TestInitDarkOnlyLegacy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ember")
	if err := Init(dir, "ember", InitOptions{Modes: []string{"dark"}, Preset: PresetLegacy}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	m, _ := themepkg.DecodeManifest(readFile(t, filepath.Join(dir, "theme.yaml")))
	if m.Tokens.Light != nil {
		t.Fatalf("light tokens written for a dark-only scaffold")
	}
	want, _ := presetFor(PresetLegacy, "dark")
	if !tokensEqual(m.Tokens.Dark, want) {
		t.Fatalf("dark tokens do not match the legacy preset")
	}
}

func TestInitRefusesToOverwrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keep")
	writeFile(t, filepath.Join(dir, "theme.yaml"), []byte("name: mine\n"))
	err := Init(dir, "keep", InitOptions{})
	if !errors.Is(err, ErrExists) {
		t.Fatalf("Init over an existing package: err = %v, want ErrExists", err)
	}
	if got := string(readFile(t, filepath.Join(dir, "theme.yaml"))); got != "name: mine\n" {
		t.Fatalf("existing theme.yaml was changed to %q", got)
	}
}

func TestInitRejectsBadInput(t *testing.T) {
	root := t.TempDir()
	cases := map[string]InitOptions{
		"unknown preset": {Preset: "neon"},
		"unknown mode":   {Modes: []string{"sepia"}},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			if err := Init(filepath.Join(root, name), "x", opts); err == nil {
				t.Fatalf("Init(%s) succeeded, want error", name)
			}
		})
	}
	if err := Init(filepath.Join(root, "empty"), "", InitOptions{}); err == nil {
		t.Fatalf("Init with an empty name succeeded, want error")
	}
}

func TestDisplayNameFor(t *testing.T) {
	cases := map[string]string{
		"deep-ocean": "Deep Ocean",
		"ember":      "Ember",
		"a-b-c":      "A B C",
	}
	for in, want := range cases {
		if got := displayNameFor(in); got != want {
			t.Errorf("displayNameFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeModes(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, []string{"light", "dark"}},
		{[]string{"dark", "light"}, []string{"light", "dark"}},
		{[]string{"dark", "dark"}, []string{"dark"}},
	}
	for _, c := range cases {
		got, err := normalizeModes(c.in)
		if err != nil {
			t.Fatalf("normalizeModes(%v): %v", c.in, err)
		}
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("normalizeModes(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}
