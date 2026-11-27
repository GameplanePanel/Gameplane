package themepkg

import (
	"encoding/json"
	"maps"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
)

type derivationFixture struct {
	Cases []struct {
		Accent  string `json:"accent"`
		Surface string `json:"surface"`
		Tokens  Tokens `json:"tokens"`
	} `json:"cases"`
}

func validTokens() Tokens {
	t := Tokens{}
	for _, name := range TokenNames {
		t[name] = "#101010"
	}
	return t
}

// goodTokens is a mode where every text and accent pair passes LowContrast.
func goodTokens() Tokens {
	t := validTokens()
	t["background"] = "#ffffff"
	t["foreground"] = "#000000"
	t["surface"] = "#ffffff"
	t["muted"] = "#000000"
	t["accent"] = "#0000ff"
	t["accent-foreground"] = "#ffffff"
	return t
}

func TestDeriveMatchesFixture(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/derivation.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx derivationFixture
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if len(fx.Cases) == 0 {
		t.Fatal("fixture has no cases")
	}
	for _, c := range fx.Cases {
		got, err := Derive(c.Accent, c.Surface)
		if err != nil {
			t.Fatalf("Derive(%s, %s): %v", c.Accent, c.Surface, err)
		}
		if !maps.Equal(got, c.Tokens) {
			t.Errorf("Derive(%s, %s) = %v, want %v", c.Accent, c.Surface, got, c.Tokens)
		}
	}
}

func TestDeriveNormalizesAndRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		accent  string
		surface string
		wantErr bool
		accentA string
	}{
		{name: "uppercase and spaces normalised", accent: "  #EC4899 ", surface: "#0F0F12", accentA: "#ec4899"},
		{name: "bad accent", accent: "#zzzzzz", surface: "#0f0f12", wantErr: true},
		{name: "short accent", accent: "#fff", surface: "#0f0f12", wantErr: true},
		{name: "bad surface", accent: "#ec4899", surface: "black", wantErr: true},
		{name: "dark surface", accent: "#000000", surface: "#000000"},
		{name: "light surface", accent: "#ffffff", surface: "#ffffff"},
		{name: "mid grey surface", accent: "#123456", surface: "#808080"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := Derive(tc.accent, tc.surface)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Derive err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if len(got) != len(TokenNames) {
				t.Fatalf("got %d tokens, want %d", len(got), len(TokenNames))
			}
			if tc.accentA != "" && got["accent"] != tc.accentA {
				t.Errorf("accent = %s, want %s", got["accent"], tc.accentA)
			}
			if problems := ValidateTokens("dark", got); len(problems) != 0 {
				t.Errorf("derived tokens invalid: %v", problems)
			}
		})
	}
}

func TestValidateTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(Tokens)
		want   []Problem
	}{
		{
			name:   "valid",
			mutate: func(Tokens) {},
		},
		{
			name:   "missing token",
			mutate: func(tk Tokens) { delete(tk, "link") },
			want:   []Problem{{Rule: RuleTokensNames, Detail: "tokens.dark is missing link"}},
		},
		{
			name:   "unknown token",
			mutate: func(tk Tokens) { tk["zeta"] = "#000000"; tk["alpha"] = "#000000" },
			want: []Problem{
				{Rule: RuleTokensNames, Detail: "tokens.dark has unknown token alpha"},
				{Rule: RuleTokensNames, Detail: "tokens.dark has unknown token zeta"},
			},
		},
		{
			name:   "uppercase value",
			mutate: func(tk Tokens) { tk["accent"] = "#ABCDEF" },
			want:   []Problem{{Rule: RuleTokensFormat, Detail: "tokens.dark.accent must be a lowercase #rrggbb color"}},
		},
		{
			name:   "short value",
			mutate: func(tk Tokens) { tk["muted"] = "#fff" },
			want:   []Problem{{Rule: RuleTokensFormat, Detail: "tokens.dark.muted must be a lowercase #rrggbb color"}},
		},
		{
			name:   "missing and bad format together keep order",
			mutate: func(tk Tokens) { delete(tk, "background"); tk["link"] = "blue" },
			want: []Problem{
				{Rule: RuleTokensNames, Detail: "tokens.dark is missing background"},
				{Rule: RuleTokensFormat, Detail: "tokens.dark.link must be a lowercase #rrggbb color"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tk := validTokens()
			tc.mutate(tk)
			got := ValidateTokens("dark", tk)
			if !slices.Equal(got, tc.want) {
				t.Errorf("ValidateTokens = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRelativeLuminanceAndContrast(t *testing.T) {
	t.Parallel()
	lum := []struct {
		hex  string
		want float64
	}{
		{"#ffffff", 1},
		{"#000000", 0},
		{" #FFFFFF ", 1},
	}
	for _, tc := range lum {
		got, err := RelativeLuminance(tc.hex)
		if err != nil || math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("RelativeLuminance(%q) = %v, %v; want %v", tc.hex, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "#fff", "red", "#gggggg", "#12345678"} {
		if _, err := RelativeLuminance(bad); err == nil {
			t.Errorf("RelativeLuminance(%q) want error", bad)
		}
	}

	ratio := []struct {
		a, b string
		want float64
	}{
		{"#ffffff", "#000000", 21},
		{"#000000", "#ffffff", 21},
		{"#336699", "#336699", 1},
	}
	for _, tc := range ratio {
		got, err := ContrastRatio(tc.a, tc.b)
		if err != nil {
			t.Fatalf("ContrastRatio(%s, %s): %v", tc.a, tc.b, err)
		}
		if diff := got - tc.want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("ContrastRatio(%s, %s) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
	if _, err := ContrastRatio("#ffffff", "nope"); err == nil {
		t.Error("ContrastRatio with bad b want error")
	}
	if _, err := ContrastRatio("nope", "#ffffff"); err == nil {
		t.Error("ContrastRatio with bad a want error")
	}
}

func TestLowContrast(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(Tokens)
		want   bool
	}{
		{
			name:   "black on white is fine",
			mutate: func(tk Tokens) { tk["foreground"] = "#000000"; tk["background"] = "#ffffff" },
			want:   false,
		},
		{
			name:   "low foreground background",
			mutate: func(tk Tokens) { tk["foreground"] = "#777777"; tk["background"] = "#808080" },
			want:   true,
		},
		{
			name:   "low accent-foreground on accent",
			mutate: func(tk Tokens) { tk["accent-foreground"] = "#777777"; tk["accent"] = "#808080" },
			want:   true,
		},
		{
			name:   "low muted on surface",
			mutate: func(tk Tokens) { tk["muted"] = "#808080" },
			want:   true,
		},
		{
			name:   "accent too close to surface",
			mutate: func(tk Tokens) { tk["accent"] = "#aaaaaa"; tk["accent-foreground"] = "#000000" },
			want:   true,
		},
		{
			name:   "unparsable token counts as low",
			mutate: func(tk Tokens) { tk["foreground"] = "oops" },
			want:   true,
		},
		{
			name:   "missing token counts as low",
			mutate: func(tk Tokens) { delete(tk, "muted") },
			want:   true,
		},
		{
			name:   "accent at 3:1 passes",
			mutate: func(tk Tokens) { tk["accent"] = "#949494"; tk["accent-foreground"] = "#000000" },
			want:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tk := goodTokens()
			tc.mutate(tk)
			if got := LowContrast(tk); got != tc.want {
				t.Errorf("LowContrast = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHSLConversions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		c    rgb
		want hsl
	}{
		{"grey", rgb{128, 128, 128}, hsl{0, 0, 50}},
		{"black", rgb{0, 0, 0}, hsl{0, 0, 0}},
		{"red", rgb{255, 0, 0}, hsl{0, 100, 50}},
		{"rose wraps negative hue", rgb{255, 0, 128}, hsl{330, 100, 50}},
		{"green", rgb{0, 255, 0}, hsl{120, 100, 50}},
		{"blue", rgb{0, 0, 255}, hsl{240, 100, 50}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := rgbToHSL(tc.c); got != tc.want {
				t.Errorf("rgbToHSL(%v) = %v, want %v", tc.c, got, tc.want)
			}
		})
	}

	// hslToRGB hue sectors, negative and wrapped hues, and clamped inputs.
	back := []struct {
		name    string
		h, s, l float64
		want    rgb
	}{
		{"sector 0", 30, 100, 50, rgb{255, 128, 0}},
		{"sector 1", 90, 100, 50, rgb{128, 255, 0}},
		{"sector 2", 150, 100, 50, rgb{0, 255, 128}},
		{"sector 3", 200, 100, 50, rgb{0, 170, 255}},
		{"sector 4", 270, 100, 50, rgb{128, 0, 255}},
		{"sector 5", 330, 100, 50, rgb{255, 0, 128}},
		{"negative hue", -120, 100, 50, rgb{0, 0, 255}},
		{"hue above 360", 480, 100, 50, rgb{0, 255, 0}},
		{"clamped saturation and lightness", 0, 300, 150, rgb{255, 255, 255}},
		{"clamped low", 0, -10, -10, rgb{0, 0, 0}},
	}
	for _, tc := range back {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hslToRGB(tc.h, tc.s, tc.l); got != tc.want {
				t.Errorf("hslToRGB(%v,%v,%v) = %v, want %v", tc.h, tc.s, tc.l, got, tc.want)
			}
		})
	}
}

func TestParseHexAndFormat(t *testing.T) {
	t.Parallel()
	c, err := parseHex("  #0A1b2C ")
	if err != nil {
		t.Fatalf("parseHex: %v", err)
	}
	if c != (rgb{10, 27, 44}) {
		t.Errorf("parseHex = %v", c)
	}
	if got := formatHex(c); got != "#0a1b2c" {
		t.Errorf("formatHex = %s", got)
	}
	if _, err := parseHex("#12345g"); err == nil || !strings.Contains(err.Error(), "invalid hex color") {
		t.Errorf("parseHex bad digit err = %v", err)
	}
}

func TestJsRound(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want float64 }{
		{0.5, 1}, {1.49, 1}, {2.5, 3}, {-0.4, 0}, {-1.5, -1},
	}
	for _, tc := range tests {
		if got := jsRound(tc.in); got != tc.want {
			t.Errorf("jsRound(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
