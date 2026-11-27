# themepkg — Specification

**Status:** pre-v1 (spec 043)
**Module / package:** `github.com/GameplanePanel/gameplane/themepkg`
**Dependencies:** Go 1.27+ standard library and `gopkg.in/yaml.v3` (strict `theme.yaml` decode)

## Purpose

Shared theme package contract for the theme marketplace (spec 043). The API (user preferences, theme indexing and installs) and `gp-module theme` validate with this one package, so a package the CLI accepts is a package the panel accepts, and the custom CSS rules of spec 016 have one implementation.

## Responsibilities

1. Decode `theme.yaml` strictly (unknown keys refused) and check every `schema` rule of `specs/043-theme-marketplace/contracts/theme-package.md`: apiVersion, DNS-1123 name, semver without build metadata, field lengths, tags, SPDX license shape, https-only homepage, changelog size.
2. Hold the 14 theme token names (same order as `CUSTOM_THEME_TOKEN_NAMES` in `web/src/lib/theme-derivation.ts`), check names and `#rrggbb` format, compute WCAG relative luminance and contrast, and flag low contrast with the same floors as the dashboard (4.5:1 text, 3:1 accent on surface).
3. Port the dashboard's custom color derivation (`Derive`), checked against the shared fixture `testdata/derivation.json`, which the web Vitest suite also reads.
4. Sanitize custom CSS (`SanitizeCSS`): size, `@import`, external `url()`, braces, HTML delimiters, escapes and other external references, in the order and with the messages spec 016 used. The API's `sanitizeCustomCSS` is a wrapper over it. `CheckCSSComments` adds the unterminated comment check for theme packages only.
5. Check previews from their headers only (PNG and WebP, format and dimensions), the allowed file set and the size limits, and return the sanitized result (`Validate`).

## Non-goals / boundaries

- Does **not** fetch, verify signatures or store anything: fetching and cosign verification live in `bundlefetch`, storage in the API.
- Does **not** decode image pixels; previews are checked from their headers.
- Does **not** render themes; the dashboard applies tokens and CSS.

## Directory & package layout

Flat root package:

| File | Role |
|---|---|
| `problem.go` | `Problem` and the rule ids |
| `manifest.go` | `Manifest`, `DecodeManifest` |
| `tokens.go` | token names, `ValidateTokens`, luminance, contrast, `LowContrast`, `Derive` |
| `css.go` | `SanitizeCSS`, `CheckCSSComments` |
| `preview.go` | `PreviewInfo` |
| `package.go` | `Validate` and the package limits |
| `testdata/` | derivation fixture, a good package, one bad package per rule |

## Contracts

- Package format and rules: `specs/043-theme-marketplace/contracts/theme-package.md`.
- Every failure is a `Problem{Rule, Detail, Line}`; `Rule` is one of the ids in that contract.

## Testing

Unit tests only (pure package), coverage gate 90 % (`.testcoverage.yml`).
