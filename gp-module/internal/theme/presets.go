// Package theme implements the `gp-module theme` command group (spec 043, US5):
// scaffolding, spec 016 export conversion, validation, local preview and OCI
// packaging of theme packages. Every rule is checked by the shared themepkg
// module, so a package this CLI accepts is one the panel accepts.
package theme

import (
	"fmt"

	"github.com/GameplanePanel/gameplane/themepkg"
)

// Built-in preset names accepted by `theme init --from-preset`.
const (
	PresetPink   = "pink"
	PresetLegacy = "legacy"
)

// presetTokens holds the 14 semantic tokens of each built-in preset and mode.
// The values are the hex equivalents of the oklch() custom properties in
// web/src/styles/globals.css (the `:root` and `.dark` blocks for Modern Pink,
// the `data-theme-preset="legacy"` blocks for Legacy Orange).
var presetTokens = map[string]map[string]themepkg.Tokens{
	PresetPink: {
		"light": {
			"background":             "#ffffff",
			"foreground":             "#2a0f1e",
			"surface":                "#fff7fb",
			"surface-secondary":      "#f8dde9",
			"surface-tertiary":       "#f8dde9",
			"overlay":                "#fff7fb",
			"accent":                 "#db2777",
			"accent-foreground":      "#ffffff",
			"accent-soft":            "#ffffff",
			"accent-soft-foreground": "#be185d",
			"muted":                  "#6e5262",
			"border":                 "#efc3d6",
			"focus":                  "#db2777",
			"link":                   "#2563eb",
		},
		"dark": {
			"background":             "#121114",
			"foreground":             "#f5f3f7",
			"surface":                "#1c1a20",
			"surface-secondary":      "#17151a",
			"surface-tertiary":       "#141317",
			"overlay":                "#201e24",
			"accent":                 "#ff4fa3",
			"accent-foreground":      "#ffffff",
			"accent-soft":            "#331525",
			"accent-soft-foreground": "#ff8ac4",
			"muted":                  "#9e98a6",
			"border":                 "#2c2932",
			"focus":                  "#ff4fa3",
			"link":                   "#7db4ff",
		},
	},
	PresetLegacy: {
		"light": {
			"background":             "#ffffff",
			"foreground":             "#0f172a",
			"surface":                "#f8fafc",
			"surface-secondary":      "#f1f5f9",
			"surface-tertiary":       "#e2e8f0",
			"overlay":                "#f8fafc",
			"accent":                 "#ea580c",
			"accent-foreground":      "#ffffff",
			"accent-soft":            "#ffe7d9",
			"accent-soft-foreground": "#ca3200",
			"muted":                  "#64748b",
			"border":                 "#e2e8f0",
			"focus":                  "#ea580c",
			"link":                   "#c2410c",
		},
		"dark": {
			"background":             "#0f0f0f",
			"foreground":             "#f5f5f5",
			"surface":                "#171717",
			"surface-secondary":      "#141414",
			"surface-tertiary":       "#111111",
			"overlay":                "#1c1c1c",
			"accent":                 "#f97316",
			"accent-foreground":      "#ffffff",
			"accent-soft":            "#381706",
			"accent-soft-foreground": "#ff9c68",
			"muted":                  "#949494",
			"border":                 "#292929",
			"focus":                  "#f97316",
			"link":                   "#fb923c",
		},
	},
}

// PresetNames lists the built-in presets in the order the CLI documents them.
func PresetNames() []string {
	return []string{PresetPink, PresetLegacy}
}

// presetFor returns a copy of one preset mode's tokens.
func presetFor(preset, mode string) (themepkg.Tokens, error) {
	modes, ok := presetTokens[preset]
	if !ok {
		return nil, fmt.Errorf("unknown preset %q (want pink or legacy)", preset)
	}
	src, ok := modes[mode]
	if !ok {
		return nil, fmt.Errorf("preset %q has no %s mode", preset, mode)
	}
	out := make(themepkg.Tokens, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out, nil
}
