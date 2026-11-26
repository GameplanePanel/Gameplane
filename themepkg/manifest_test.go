package themepkg

import (
	"strings"
	"testing"
)

// mfValid is the theme.yaml of the contract, without its comments.
const mfValid = `apiVersion: gameplane.local/theme/v1
name: deep-ocean
version: 1.2.0
displayName: Deep Ocean
author: Jane Doe
description: Calm blues for long sessions.
tags: [dark, blue, calm]
license: MIT
homepage: https://example.org/deep-ocean
changelog: |
  1.2.0: higher contrast borders
tokens:
  dark:
    background: "#0b1220"
    foreground: "#e6edf7"
    surface: "#111a2e"
    surface-secondary: "#16213a"
    surface-tertiary: "#1c2946"
    overlay: "#0f172a"
    accent: "#38bdf8"
    accent-foreground: "#04121d"
    accent-soft: "#0c2a3d"
    accent-soft-foreground: "#a5e3ff"
    muted: "#8a9bb8"
    border: "#24324f"
    focus: "#38bdf8"
    link: "#7dd3fc"
`

// mfContract is the contract's example verbatim, comments included.
const mfContract = `apiVersion: gameplane.local/theme/v1
name: deep-ocean            # DNS-1123 label, ≤ 63
version: 1.2.0              # semver MAJOR.MINOR.PATCH[-prerelease], no leading v, no +build metadata
displayName: Deep Ocean     # ≤ 64
author: Jane Doe            # ≤ 64
description: Calm blues for long sessions.   # ≤ 280
tags: [dark, blue, calm]    # ≤ 8, each 1–24 chars, lowercase [a-z0-9-]
license: MIT                # SPDX id
homepage: https://example.org/deep-ocean     # optional, https only
changelog: |                # optional, ≤ 4 KiB, plain text
  1.2.0: higher contrast borders
tokens:                     # at least one of light/dark
  dark:
    background: "#0b1220"   # all 14 names from CUSTOM_THEME_TOKEN_NAMES, lowercase #rrggbb
    foreground: "#e6edf7"
    surface: "#111a2e"
    surface-secondary: "#16213a"
    surface-tertiary: "#1c2946"
    overlay: "#0f172a"
    accent: "#38bdf8"
    accent-foreground: "#04121d"
    accent-soft: "#0c2a3d"
    accent-soft-foreground: "#a5e3ff"
    muted: "#8a9bb8"
    border: "#24324f"
    focus: "#38bdf8"
    link: "#7dd3fc"
`

// mfLight is a complete light token map, indented for the tokens block.
const mfLight = `  light:
    background: "#ffffff"
    foreground: "#111827"
    surface: "#f8fafc"
    surface-secondary: "#f1f5f9"
    surface-tertiary: "#e2e8f0"
    overlay: "#ffffff"
    accent: "#0369a1"
    accent-foreground: "#ffffff"
    accent-soft: "#e0f2fe"
    accent-soft-foreground: "#075985"
    muted: "#475569"
    border: "#cbd5e1"
    focus: "#0369a1"
    link: "#0369a1"
`

func mfReplace(t *testing.T, s, old, repl string) string {
	t.Helper()
	if !strings.Contains(s, old) {
		t.Fatalf("fixture lacks %q", old)
	}
	return strings.Replace(s, old, repl, 1)
}

func mfLineOf(s, sub string) int {
	return strings.Count(s[:strings.Index(s, sub)], "\n") + 1
}

func mfHasRule(probs []Problem, rule string) bool {
	for _, p := range probs {
		if p.Rule == rule {
			return true
		}
	}
	return false
}

func TestDecodeManifestValid(t *testing.T) {
	t.Parallel()
	m, probs := DecodeManifest([]byte(mfValid))
	if probs != nil {
		t.Fatalf("unexpected problems: %v", probs)
	}
	if m.APIVersion != APIVersion || m.Name != "deep-ocean" || m.Version != "1.2.0" {
		t.Fatalf("identity fields not decoded: %+v", m)
	}
	if m.Tokens.Dark["accent"] != "#38bdf8" {
		t.Fatalf("dark tokens not decoded: %v", m.Tokens.Dark)
	}
	if len(m.Tags) != 3 || m.Homepage != "https://example.org/deep-ocean" {
		t.Fatalf("optional fields not decoded: tags=%v homepage=%q", m.Tags, m.Homepage)
	}
}

func TestDecodeManifestContractExample(t *testing.T) {
	t.Parallel()
	if _, probs := DecodeManifest([]byte(mfContract)); len(probs) != 0 {
		t.Fatalf("contract example should be valid, got %v", probs)
	}
}

func TestDecodeManifestBothModes(t *testing.T) {
	t.Parallel()
	both := strings.Replace(mfValid, "tokens:\n", "tokens:\n"+mfLight, 1)
	m, probs := DecodeManifest([]byte(both))
	if len(probs) != 0 {
		t.Fatalf("unexpected problems: %v", probs)
	}
	if got := strings.Join(m.Modes(), ","); got != "light,dark" {
		t.Fatalf("Modes() = %q, want light,dark", got)
	}
}

func TestDecodeManifestOptionalFieldsOmitted(t *testing.T) {
	t.Parallel()
	data := mfValid
	data = mfReplace(t, data, "description: Calm blues for long sessions.\n", "")
	data = mfReplace(t, data, "homepage: https://example.org/deep-ocean\n", "")
	data = mfReplace(t, data, "changelog: |\n  1.2.0: higher contrast borders\n", "")
	if _, probs := DecodeManifest([]byte(data)); probs != nil {
		t.Fatalf("optional fields omitted should be valid, got %v", probs)
	}
}

func TestDecodeManifestValidVersions(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"1.2.0", "0.0.1-rc.1", "0.0.1-0.3.7", "10.20.30-alpha-1.x"} {
		data := mfReplace(t, mfValid, "version: 1.2.0", "version: "+v)
		if _, probs := DecodeManifest([]byte(data)); probs != nil {
			t.Fatalf("version %q should be valid, got %v", v, probs)
		}
	}
}

func TestDecodeManifestRules(t *testing.T) {
	t.Parallel()
	head := mfValid[:strings.Index(mfValid, "tokens:")]
	cases := []struct {
		name string
		yaml string
		rule string
	}{
		{"apiVersion wrong", mfReplace(t, mfValid, "apiVersion: gameplane.local/theme/v1", "apiVersion: gameplane.local/theme/v2"), RuleSchema},
		{"name uppercase", mfReplace(t, mfValid, "name: deep-ocean", "name: Deep_Ocean"), RuleSchema},
		{"name too long", mfReplace(t, mfValid, "name: deep-ocean", "name: "+strings.Repeat("a", 64)), RuleSchema},
		{"name empty", mfReplace(t, mfValid, "name: deep-ocean", `name: ""`), RuleSchema},
		{"version leading v", mfReplace(t, mfValid, "version: 1.2.0", "version: v1.2.0"), RuleSchema},
		{"version build metadata", mfReplace(t, mfValid, "version: 1.2.0", "version: 1.2.0+build.1"), RuleSchema},
		{"version leading zero prerelease", mfReplace(t, mfValid, "version: 1.2.0", "version: 1.2.0-rc.01"), RuleSchema},
		{"version short", mfReplace(t, mfValid, "version: 1.2.0", "version: 1.2"), RuleSchema},
		{"version empty", mfReplace(t, mfValid, "version: 1.2.0", `version: ""`), RuleSchema},
		{"displayName empty", mfReplace(t, mfValid, "displayName: Deep Ocean", `displayName: ""`), RuleSchema},
		{"displayName too long", mfReplace(t, mfValid, "displayName: Deep Ocean", "displayName: "+strings.Repeat("D", 65)), RuleSchema},
		{"author empty", mfReplace(t, mfValid, "author: Jane Doe", `author: ""`), RuleSchema},
		{"author too long", mfReplace(t, mfValid, "author: Jane Doe", "author: "+strings.Repeat("J", 65)), RuleSchema},
		{"description too long", mfReplace(t, mfValid, "description: Calm blues for long sessions.", "description: "+strings.Repeat("a", 281)), RuleSchema},
		{"tags too many", mfReplace(t, mfValid, "tags: [dark, blue, calm]", "tags: [a, b, c, d, e, f, g, h, i]"), RuleSchema},
		{"tag uppercase", mfReplace(t, mfValid, "tags: [dark, blue, calm]", "tags: [Dark, blue, calm]"), RuleSchema},
		{"tag too long", mfReplace(t, mfValid, "tags: [dark, blue, calm]", "tags: ["+strings.Repeat("a", 25)+"]"), RuleSchema},
		{"tag empty", mfReplace(t, mfValid, "tags: [dark, blue, calm]", `tags: [""]`), RuleSchema},
		{"tag duplicate", mfReplace(t, mfValid, "tags: [dark, blue, calm]", "tags: [dark, dark]"), RuleSchema},
		{"license empty", mfReplace(t, mfValid, "license: MIT", `license: ""`), RuleSchema},
		{"license not SPDX", mfReplace(t, mfValid, "license: MIT", "license: MIT and"), RuleSchema},
		{"license too long", mfReplace(t, mfValid, "license: MIT", "license: "+strings.Repeat("A", 65)), RuleSchema},
		{"homepage http", mfReplace(t, mfValid, "https://example.org/deep-ocean", "http://example.org/deep-ocean"), RuleSchema},
		{"homepage no host", mfReplace(t, mfValid, "https://example.org/deep-ocean", "https:///deep-ocean"), RuleSchema},
		{"homepage user info", mfReplace(t, mfValid, "https://example.org/deep-ocean", "https://user@example.org/deep-ocean"), RuleSchema},
		{"homepage unparsable", mfReplace(t, mfValid, "https://example.org/deep-ocean", "https://[::1"), RuleSchema},
		{"changelog too long", mfReplace(t, mfValid, "  1.2.0: higher contrast borders", "  "+strings.Repeat("x", 4097)), RuleSchema},
		{"changelog control character", mfReplace(t, mfValid, "changelog: |\n  1.2.0: higher contrast borders", `changelog: "a\u0001b"`), RuleSchema},
		{"tokens missing", head + "tokens: {}\n", RuleSchema},
		{"light tokens incomplete", head + "tokens:\n  light:\n    background: \"#ffffff\"\n", RuleTokensNames},
		{"token missing", mfReplace(t, mfValid, "    link: \"#7dd3fc\"\n", ""), RuleTokensNames},
		{"token unknown", mfReplace(t, mfValid, "    link: \"#7dd3fc\"\n", "    link: \"#7dd3fc\"\n    extra: \"#000000\"\n"), RuleTokensNames},
		{"token format", mfReplace(t, mfValid, `"#0b1220"`, `"#0B1220"`), RuleTokensFormat},
		{"unknown key", mfReplace(t, mfValid, "license: MIT", "licence: MIT"), RuleSchema},
		{"tags wrong type", mfReplace(t, mfValid, "tags: [dark, blue, calm]", "tags: 5"), RuleSchema},
		{"two documents", mfValid + "---\napiVersion: x\n", RuleSchema},
		{"empty document", "", RuleSchema},
		{"comment only", "# nothing here\n", RuleSchema},
		{"oversized", strings.Repeat("a", MaxManifestBytes+1), RuleSchema},
		{"control byte", "name: a\x01b\n", RuleSchema},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, probs := DecodeManifest([]byte(tc.yaml))
			if !mfHasRule(probs, tc.rule) {
				t.Fatalf("want rule %s, got %v", tc.rule, probs)
			}
			for _, p := range probs {
				if p.Detail == "" {
					t.Fatalf("problem without detail: %+v", p)
				}
			}
		})
	}
}

func TestDecodeManifestProblemLines(t *testing.T) {
	t.Parallel()
	t.Run("unknown key reports its line", func(t *testing.T) {
		t.Parallel()
		data := mfReplace(t, mfValid, "license: MIT", "licence: MIT")
		_, probs := DecodeManifest([]byte(data))
		if len(probs) != 1 || probs[0].Rule != RuleSchema {
			t.Fatalf("want one schema problem, got %v", probs)
		}
		if want := mfLineOf(mfValid, "license: MIT"); probs[0].Line != want {
			t.Fatalf("Line = %d, want %d", probs[0].Line, want)
		}
	})
	t.Run("type error reports its line", func(t *testing.T) {
		t.Parallel()
		data := mfReplace(t, mfValid, "tags: [dark, blue, calm]", "tags: 5")
		_, probs := DecodeManifest([]byte(data))
		if len(probs) != 1 || probs[0].Rule != RuleSchema {
			t.Fatalf("want one schema problem, got %v", probs)
		}
		if want := mfLineOf(mfValid, "tags:"); probs[0].Line != want {
			t.Fatalf("Line = %d, want %d", probs[0].Line, want)
		}
	})
	t.Run("error without a line has zero line", func(t *testing.T) {
		t.Parallel()
		_, probs := DecodeManifest([]byte("name: a\x01b\n"))
		if len(probs) != 1 || probs[0].Line != 0 {
			t.Fatalf("want one problem with no line, got %v", probs)
		}
	})
}

func TestDecodeManifestReturnsPartialManifest(t *testing.T) {
	t.Parallel()
	data := mfReplace(t, mfValid, "name: deep-ocean", "name: Deep_Ocean")
	m, probs := DecodeManifest([]byte(data))
	if !mfHasRule(probs, RuleSchema) {
		t.Fatalf("want schema problem, got %v", probs)
	}
	if m.DisplayName != "Deep Ocean" || m.Tokens.Dark == nil {
		t.Fatalf("decoded manifest not returned alongside problems: %+v", m)
	}
}

func TestManifestModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		tokens ModeTokens
		want   string
	}{
		{"none", ModeTokens{}, ""},
		{"light only", ModeTokens{Light: Tokens{}}, "light"},
		{"dark only", ModeTokens{Dark: Tokens{}}, "dark"},
		{"both", ModeTokens{Light: Tokens{}, Dark: Tokens{}}, "light,dark"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := Manifest{Tokens: tc.tokens}
			if got := strings.Join(m.Modes(), ","); got != tc.want {
				t.Fatalf("Modes() = %q, want %q", got, tc.want)
			}
		})
	}
}
