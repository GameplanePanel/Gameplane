package theme

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/GameplanePanel/gameplane/themepkg"
)

// Spec 016 export format this command reads (contracts/theme-export.md).
const (
	exportFormat  = "gameplane-theme"
	exportVersion = 1
	// maxExportBytes bounds the export file; a real export is at most a few
	// tens of kilobytes (the custom CSS alone is capped at 32 KiB).
	maxExportBytes = 256 * 1024
)

// exportDoc is the subset of the spec 016 export this command needs.
type exportDoc struct {
	Format      string `json:"format"`
	Version     int    `json:"version"`
	Preferences struct {
		ThemeType    string `json:"themeType"`
		PresetID     string `json:"presetId"`
		CustomColors *struct {
			Accent  string `json:"accent"`
			Surface string `json:"surface"`
		} `json:"customColors"`
		CustomCSSEnabled bool    `json:"customCssEnabled"`
		CustomCSS        *string `json:"customCss"`
	} `json:"preferences"`
}

// FromExportResult describes what from-export wrote, so the CLI can report the
// choices it made without the caller re-reading the export.
type FromExportResult struct {
	// Mode is the token mode the derived colors were written under.
	Mode string
	// WroteCSS reports whether theme.css was written.
	WroteCSS bool
	// Notes lists choices the author should know about (for example a
	// disabled custom CSS that was left out).
	Notes []string
}

// FromExport turns a spec 016 gameplane-theme v1 export into a theme package in
// dir. Custom colors are expanded to the 14 tokens with themepkg.Derive, the
// same derivation the dashboard uses, and written under the mode the derivation
// produces (dark when the surface is dark). The custom CSS is written to
// theme.css only when the export has it enabled. The package still needs its
// author, license and previews; `theme validate` reports what is missing.
func FromExport(exportPath, dir, name, version string) (FromExportResult, error) {
	var res FromExportResult
	if name == "" {
		return res, errors.New("--name is required")
	}
	if version == "" {
		version = DefaultVersion
	}
	doc, err := readExport(exportPath)
	if err != nil {
		return res, err
	}
	if doc.Preferences.ThemeType != "custom_colors" {
		return res, fmt.Errorf("export has themeType %q: only custom_colors exports can become a theme package (a preset has no colors of its own to publish)", doc.Preferences.ThemeType)
	}
	if doc.Preferences.CustomColors == nil {
		return res, errors.New("export has themeType custom_colors but no customColors")
	}
	accent := doc.Preferences.CustomColors.Accent
	surface := doc.Preferences.CustomColors.Surface
	tokens, err := themepkg.Derive(accent, surface)
	if err != nil {
		return res, fmt.Errorf("derive tokens: %w", err)
	}
	dark, err := surfaceIsDark(surface)
	if err != nil {
		return res, err
	}
	res.Mode = "light"
	m := themepkg.Manifest{
		APIVersion:  themepkg.APIVersion,
		Name:        name,
		Version:     version,
		DisplayName: displayNameFor(name),
	}
	if dark {
		res.Mode = "dark"
		m.Tokens.Dark = tokens
	} else {
		m.Tokens.Light = tokens
	}

	var css []byte
	if c := doc.Preferences.CustomCSS; c != nil && *c != "" {
		if doc.Preferences.CustomCSSEnabled {
			css = []byte(*c)
			res.WroteCSS = true
		} else {
			res.Notes = append(res.Notes, "customCss is disabled in the export and was not packaged")
		}
	}
	if err := writePackage(dir, m, css); err != nil {
		return res, err
	}
	return res, nil
}

// readExport reads and decodes a spec 016 export, refusing other formats and
// versions the same way the dashboard import does.
func readExport(path string) (exportDoc, error) {
	var doc exportDoc
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return doc, fmt.Errorf("open export: %w", err)
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxExportBytes+1))
	if err != nil {
		return doc, fmt.Errorf("read export: %w", err)
	}
	if len(data) > maxExportBytes {
		return doc, fmt.Errorf("export is larger than %d KiB", maxExportBytes/1024)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("export is not valid JSON: %w", err)
	}
	if doc.Format != exportFormat {
		return doc, fmt.Errorf("format must be %q, got %q", exportFormat, doc.Format)
	}
	if doc.Version != exportVersion {
		return doc, fmt.Errorf("unsupported export version %d (this gp-module reads version %d)", doc.Version, exportVersion)
	}
	return doc, nil
}

// surfaceIsDark reports whether a surface color is dark by the same HSL rule
// themepkg.Derive uses (lightness below 50). Derive has already rejected
// malformed hex, so only the digits are read here.
func surfaceIsDark(hex string) (bool, error) {
	hex = strings.ToLower(strings.TrimSpace(hex))
	if len(hex) != 7 || hex[0] != '#' {
		return false, fmt.Errorf("surface %q is not #rrggbb", hex)
	}
	var ch [3]float64
	for i := range ch {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return false, fmt.Errorf("surface %q is not #rrggbb: %w", hex, err)
		}
		ch[i] = float64(v) / 255
	}
	maxC, minC := ch[0], ch[0]
	for _, c := range ch[1:] {
		if c > maxC {
			maxC = c
		}
		if c < minC {
			minC = c
		}
	}
	lightness := (maxC + minC) / 2 * 100
	return lightness < 50, nil
}
