# Contract: theme package (spec 043)

One published version of one theme (OD-2). Validated by the shared `themepkg` module everywhere: `gp-module theme validate`, panel indexing, and install.

## Files

| File | Required | Limit | OCI layer media type |
|---|---|---|---|
| `theme.yaml` | yes | 16 KiB | `application/vnd.gameplane.theme.metadata.v1+yaml` |
| `theme.css` | no | 32 KiB | `text/css` |
| `previews/light.png` or `.webp` | yes if `tokens.light` | 512 KiB, ≤ 1600×1000 px | `image/png` / `image/webp` |
| `previews/dark.png` or `.webp` | yes if `tokens.dark` | 512 KiB, ≤ 1600×1000 px | `image/png` / `image/webp` |

Whole package ≤ 1.5 MiB. Any other file or layer makes the package `invalid`. OCI: artifact type `application/vnd.gameplane.theme.v1+json`, config media type `application/vnd.gameplane.theme.config.v1+json` (empty `{}`), pushed as `<source url>/<name>:<version>` (the version is always a valid OCI tag because build metadata is refused, see `schema`), optionally cosign-signed.

## theme.yaml

```yaml
apiVersion: gameplane.local/theme/v1
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
```

Unknown keys are an error (strict decode).

## Rules (each failure reports `rule` and, for CSS, `line`)

| Rule id | Check |
|---|---|
| `schema` | strict decode, required fields, lengths, semver without build metadata (`+` refused, so every version is a valid OCI tag), DNS-1123 name |
| `tokens.names` | exactly the 14 token names per present mode |
| `tokens.format` | lowercase `#rrggbb` |
| `css.size` | ≤ 32 KiB |
| `css.import` | no `@import` (same as spec 016 FR-013) |
| `css.url` | no external `url()` (http, https, protocol-relative); `data:` allowed |
| `css.remote` | no bare remote references the spec 016 sanitizer refuses |
| `css.syntax` | balanced braces and comments |
| `preview.missing` | preview present for each mode with tokens |
| `preview.format` | PNG or WebP magic bytes, dimensions within limit |
| `files.extra` | no file or layer outside the table |
| `size.total` | ≤ 1.5 MiB |

Flag, not a failure: `lowContrast` when, in any present mode, a text pair is below 4.5:1 (foreground/background, accent-foreground/accent, muted/surface) or accent/surface is below 3:1 (same floors as `theme-derivation.ts`).
