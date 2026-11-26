package themepkg

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// Tokens maps a theme token name to its lowercase #rrggbb value.
type Tokens map[string]string

// TokenNames lists the 14 token names of one theme mode, in the order of
// CUSTOM_THEME_TOKEN_NAMES in web/src/lib/theme-derivation.ts.
var TokenNames = [...]string{
	"background",
	"foreground",
	"surface",
	"surface-secondary",
	"surface-tertiary",
	"overlay",
	"accent",
	"accent-foreground",
	"accent-soft",
	"accent-soft-foreground",
	"muted",
	"border",
	"focus",
	"link",
}

// MinTextContrast is the WCAG AA contrast floor for text pairs.
const MinTextContrast = 4.5

// MinAccentSurfaceContrast is the WCAG non-text contrast floor for an accent
// sitting on the surface.
const MinAccentSurfaceContrast = 3.0

const (
	whiteHex = "#ffffff"
	blackHex = "#000000"
	darkFg   = "#f5f5f5"
	lightFg  = "#0f172a"
	hexDigit = "0123456789abcdef"
)

var (
	hexRE        = regexp.MustCompile(`^#([0-9a-fA-F]{6})$`)
	tokenValueRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)
)

// rgb holds the 0-255 channels of a color.
type rgb [3]int

// hsl holds the rounded hue (degrees), saturation and lightness (percent) of
// a color, as produced by the TypeScript hexToHsl.
type hsl struct {
	h, s, l float64
}

// jsRound matches JavaScript Math.round for the values the derivation feeds it.
// Callers wrap products in float64(...) so the compiler cannot fuse the
// multiply into the +0.5 and change the rounding.
func jsRound(x float64) float64 {
	return math.Floor(x + 0.5)
}

// toChannel rounds and clamps a channel value to 0-255 like rgbToHex.
func toChannel(v float64) int {
	return int(math.Min(255, math.Max(0, jsRound(v))))
}

// parseHex accepts #RRGGBB in either case after trimming whitespace.
func parseHex(hex string) (rgb, error) {
	m := hexRE.FindStringSubmatch(strings.TrimSpace(hex))
	if m == nil {
		return rgb{}, fmt.Errorf("invalid hex color: %s", hex)
	}
	digits := strings.ToLower(m[1])
	v := 0
	for i := 0; i < len(digits); i++ {
		v = v*16 + strings.IndexByte(hexDigit, digits[i])
	}
	return rgb{(v >> 16) & 0xff, (v >> 8) & 0xff, v & 0xff}, nil
}

// formatHex renders channels as lowercase #rrggbb.
func formatHex(c rgb) string {
	return fmt.Sprintf("#%02x%02x%02x", c[0], c[1], c[2])
}

// rgbToHSL is the TypeScript hexToHsl on already parsed channels.
func rgbToHSL(c rgb) hsl {
	rn := float64(c[0]) / 255
	gn := float64(c[1]) / 255
	bn := float64(c[2]) / 255
	mx := math.Max(rn, math.Max(gn, bn))
	mn := math.Min(rn, math.Min(gn, bn))
	l := (mx + mn) / 2
	if mx == mn {
		return hsl{h: 0, s: 0, l: jsRound(float64(l * 100))}
	}
	d := mx - mn
	var s float64
	if l > 0.5 {
		s = d / (2 - mx - mn)
	} else {
		s = d / (mx + mn)
	}
	var h float64
	switch mx {
	case rn:
		shift := 0.0
		if gn < bn {
			shift = 6
		}
		h = ((gn-bn)/d + shift) * 60
	case gn:
		h = ((bn-rn)/d + 2) * 60
	default:
		h = ((rn-gn)/d + 4) * 60
	}
	return hsl{
		h: jsRound(float64(h)),
		s: jsRound(float64(s * 100)),
		l: jsRound(float64(l * 100)),
	}
}

// hslToRGB is the TypeScript hslToHex on numbers, returning clamped channels.
func hslToRGB(h, s, l float64) rgb {
	hn := math.Mod(math.Mod(h, 360)+360, 360)
	sn := math.Min(100, math.Max(0, s)) / 100
	ln := math.Min(100, math.Max(0, l)) / 100
	c := (1 - math.Abs(float64(2*ln)-1)) * sn
	x := c * (1 - math.Abs(math.Mod(hn/60, 2)-1))
	m := ln - c/2
	var r, g, b float64
	switch {
	case hn < 60:
		r, g = c, x
	case hn < 120:
		r, g = x, c
	case hn < 180:
		g, b = c, x
	case hn < 240:
		g, b = x, c
	case hn < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return rgb{
		toChannel(float64((r + m) * 255)),
		toChannel(float64((g + m) * 255)),
		toChannel(float64((b + m) * 255)),
	}
}

// luminance is the WCAG relative luminance of parsed channels.
func luminance(c rgb) float64 {
	f := func(n int) float64 {
		v := float64(n) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return float64(0.2126*f(c[0])) + float64(0.7152*f(c[1])) + float64(0.0722*f(c[2]))
}

// contrastRGB is the WCAG contrast ratio of two parsed colors.
func contrastRGB(a, b rgb) float64 {
	la := luminance(a)
	lb := luminance(b)
	hi := math.Max(la, lb)
	lo := math.Min(la, lb)
	return (hi + 0.05) / (lo + 0.05)
}

// mixRGB alpha-composites fg over bg in sRGB space (frac of fg).
func mixRGB(fg, bg rgb, frac float64) rgb {
	mix := func(x, y int) int {
		return toChannel(float64(float64(x)*frac) + float64(float64(y)*(1-frac)))
	}
	return rgb{mix(fg[0], bg[0]), mix(fg[1], bg[1]), mix(fg[2], bg[2])}
}

// legibleTone walks the lightness of an (h, s) tone from base toward the mode
// extreme until it reaches the 4.5:1 text floor against base. It returns the
// extreme when no reachable lightness passes.
func legibleTone(base rgb, h, s float64, towardLight bool) rgb {
	start := rgbToHSL(base).l
	extreme := 0.0
	step := -1.0
	if towardLight {
		extreme = 100
		step = 1
	}
	for l := start; ; l += step {
		candidate := hslToRGB(h, s, l)
		if contrastRGB(candidate, base) >= MinTextContrast || l == extreme {
			return candidate
		}
	}
}

// isTokenName reports whether name is one of TokenNames.
func isTokenName(name string) bool {
	for _, known := range TokenNames {
		if known == name {
			return true
		}
	}
	return false
}

// ValidateTokens checks one mode's token map: all 14 names present, no unknown
// names, and every known value a lowercase #rrggbb color. mode ("light" or
// "dark") is used in the Detail text. Problems come in TokenNames order, then
// unknown names sorted.
func ValidateTokens(mode string, t Tokens) []Problem {
	var problems []Problem
	for _, name := range TokenNames {
		if _, ok := t[name]; !ok {
			problems = append(problems, Problem{
				Rule:   RuleTokensNames,
				Detail: fmt.Sprintf("tokens.%s is missing %s", mode, name),
			})
		}
	}
	var unknown []string
	for name := range t {
		if !isTokenName(name) {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		problems = append(problems, Problem{
			Rule:   RuleTokensNames,
			Detail: fmt.Sprintf("tokens.%s has unknown token %s", mode, name),
		})
	}
	for _, name := range TokenNames {
		value, ok := t[name]
		if !ok {
			continue
		}
		if !tokenValueRE.MatchString(value) {
			problems = append(problems, Problem{
				Rule:   RuleTokensFormat,
				Detail: fmt.Sprintf("tokens.%s.%s must be a lowercase #rrggbb color", mode, name),
			})
		}
	}
	return problems
}

// RelativeLuminance returns the WCAG relative luminance of a #RRGGBB color.
// It errors on anything else.
func RelativeLuminance(hex string) (float64, error) {
	c, err := parseHex(hex)
	if err != nil {
		return 0, err
	}
	return luminance(c), nil
}

// ContrastRatio returns the WCAG contrast ratio of two #RRGGBB colors.
func ContrastRatio(a, b string) (float64, error) {
	ca, err := parseHex(a)
	if err != nil {
		return 0, err
	}
	cb, err := parseHex(b)
	if err != nil {
		return 0, err
	}
	return contrastRGB(ca, cb), nil
}

// LowContrast reports whether a mode's tokens fall below the legibility floors:
// foreground/background, accent-foreground/accent and muted/surface under 4.5:1,
// or accent/surface under 3:1. A missing or unparsable token counts as low.
func LowContrast(t Tokens) bool {
	pairs := []struct {
		text, bg string
		floor    float64
	}{
		{"foreground", "background", MinTextContrast},
		{"accent-foreground", "accent", MinTextContrast},
		{"muted", "surface", MinTextContrast},
		{"accent", "surface", MinAccentSurfaceContrast},
	}
	for _, p := range pairs {
		ratio, err := ContrastRatio(t[p.text], t[p.bg])
		if err != nil || ratio < p.floor {
			return true
		}
	}
	return false
}

// Derive computes the 14 semantic tokens of a custom-colors theme from an
// accent and a surface color (both #RRGGBB, trimmed and lowercased first). It
// is a port of deriveCustomThemeTokens in web/src/lib/theme-derivation.ts and
// must reproduce testdata/derivation.json exactly.
func Derive(accent, surface string) (Tokens, error) {
	accent = strings.ToLower(strings.TrimSpace(accent))
	surface = strings.ToLower(strings.TrimSpace(surface))
	accentRGB, err := parseHex(accent)
	if err != nil {
		return nil, fmt.Errorf("derive accent: %w", err)
	}
	surfaceRGB, err := parseHex(surface)
	if err != nil {
		return nil, fmt.Errorf("derive surface: %w", err)
	}
	accentHSL := rgbToHSL(accentRGB)
	surfaceHSL := rgbToHSL(surfaceRGB)
	dark := surfaceHSL.l < 50
	dir := -1.0
	if dark {
		dir = 1
	}

	accentForeground := blackHex
	if contrastRGB(accentRGB, rgb{255, 255, 255}) >= contrastRGB(accentRGB, rgb{}) {
		accentForeground = whiteHex
	}

	overlay := surface
	if dark {
		overlay = formatHex(hslToRGB(surfaceHSL.h, surfaceHSL.s, surfaceHSL.l+2))
	}

	accentSoftRGB := mixRGB(accentRGB, surfaceRGB, 0.15)
	accentSoftForeground := legibleTone(accentSoftRGB, accentHSL.h, accentHSL.s, dark)
	muted := legibleTone(surfaceRGB, surfaceHSL.h, surfaceHSL.s, dark)

	foreground := lightFg
	if dark {
		foreground = darkFg
	}

	return Tokens{
		"background":             surface,
		"foreground":             foreground,
		"surface":                surface,
		"surface-secondary":      formatHex(hslToRGB(surfaceHSL.h, surfaceHSL.s, surfaceHSL.l-3)),
		"surface-tertiary":       formatHex(hslToRGB(surfaceHSL.h, surfaceHSL.s, surfaceHSL.l-5)),
		"overlay":                overlay,
		"accent":                 accent,
		"accent-foreground":      accentForeground,
		"accent-soft":            formatHex(accentSoftRGB),
		"accent-soft-foreground": formatHex(accentSoftForeground),
		"muted":                  formatHex(muted),
		"border":                 formatHex(hslToRGB(surfaceHSL.h, surfaceHSL.s, surfaceHSL.l+10*dir)),
		"focus":                  accent,
		"link":                   formatHex(hslToRGB(accentHSL.h, accentHSL.s, accentHSL.l+8*dir)),
	}, nil
}
