# Authoring a theme package

A **theme package** is a versioned set of colors, plus an optional CSS overlay,
that a user can install and pick in Theme settings next to Modern Pink and
Legacy Orange. It is the publishing side of spec 043 (theme marketplace). A
theme never changes layout or behaviour: it carries the same semantic color
tokens a preset does, and its CSS goes through the same sanitizer as a user's
own custom CSS (spec 016).

The `gp-module theme` commands write, check, preview and package a theme. The
rules they enforce live in the shared `themepkg` module, so a package the CLI
accepts is one the panel accepts.

## Package layout

```text
deep-ocean/
├── theme.yaml          required: metadata and tokens (16 KiB)
├── theme.css           optional: CSS overlay (32 KiB)
└── previews/
    ├── light.png       required when tokens.light is present (or .webp)
    └── dark.png        required when tokens.dark is present (or .webp)
```

Nothing else may be in the folder: any other file makes the package invalid
(`files.extra`). Previews are PNG or WebP, at most 512 KiB and 1600 × 1000 px.
The whole package stays under 1.5 MiB.

## `theme.yaml`

```yaml
apiVersion: gameplane.local/theme/v1
name: deep-ocean            # DNS-1123 label, at most 63 characters
version: 1.2.0              # MAJOR.MINOR.PATCH[-prerelease]; no leading v, no +build
displayName: Deep Ocean
author: Jane Doe
description: Calm blues for long sessions.
tags: [dark, blue, calm]    # up to 8 tags, lowercase a-z 0-9 -
license: MIT                # SPDX identifier
tokens:
  dark:
    background: "#0b1220"           # all 14 tokens, lowercase #rrggbb
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

Unknown keys are an error. Include `light`, `dark`, or both under `tokens`; each
mode that is present needs all 14 token names and a preview. The optional
`homepage` must be `https`, and `changelog` is plain text of at most 4 KiB.

## Rules

`gp-module theme validate` reports each failure with its rule id (and a line
number for CSS and YAML problems):

| Rule | Check |
|---|---|
| `schema` | strict decode, required fields, lengths, name and version format |
| `tokens.names` | exactly the 14 token names in each present mode |
| `tokens.format` | lowercase `#rrggbb` values |
| `css.size` | `theme.css` is at most 32 KiB |
| `css.import` | no `@import` |
| `css.url` | no external `url()`; inline `data:` URIs are allowed |
| `css.remote` | no other absolute or protocol-relative references |
| `css.syntax` | balanced braces and comments; no HTML delimiters |
| `preview.missing` | a preview for each mode that has tokens |
| `preview.format` | PNG or WebP bytes, within the dimension limit |
| `files.extra` | no file outside the table above |
| `size.total` | the package is at most 1.5 MiB |

`lowContrast` is a warning, not a failure. It appears when a text pair is below
4.5:1 (foreground on background, accent-foreground on accent, muted on surface)
or the accent on the surface is below 3:1.

## Workflow

```sh
# 1. Start from a built-in preset (pink or legacy), or from a Settings export
gp-module theme init deep-ocean --from-preset pink
#    or: export the theme from the Theme settings page (Export), then
gp-module theme from-export gameplane-theme.json --name deep-ocean --version 0.1.0

# 2. Fill in author and license in deep-ocean/theme.yaml, add previews/

# 3. Check every rule (use --json in CI)
gp-module theme validate deep-ocean

# 4. Look at it: serves a sample page on 127.0.0.1 (light/dark switch)
gp-module theme preview deep-ocean --port 8787 --mode dark

# 5a. Write an OCI image layout for review or a later push
gp-module theme package deep-ocean --out build/deep-ocean-oci

# 5b. Or push to a registry with the credentials in your Docker config (needs oras)
gp-module theme package deep-ocean --push ghcr.io/example/themes
```

`theme init` writes both token modes unless you pass `--dark` or `--light`. It
never overwrites an existing `theme.yaml`. `theme from-export` derives the 14
tokens from the export's accent and surface with the same rule the dashboard
uses. The derived set goes under `dark` when the surface is dark, and under
`light` otherwise. It writes `theme.css` only when the export has the custom CSS
enabled. Exit codes are 0 for success, 1 for a validation or runtime failure,
and 2 for a usage error.

### `theme preview`

The preview validates first and refuses to start on any problem. It binds to
`127.0.0.1` only and serves a static sample page (buttons, navigation, cards, a
table, a form and alerts). The page uses the package's tokens and `theme.css`,
and `Switch mode` toggles light and dark when the package has both. Nothing is
published.

## OCI artifact

| Item | Value |
|---|---|
| Artifact type | `application/vnd.gameplane.theme.v1+json` |
| Config media type | `application/vnd.gameplane.theme.config.v1+json` (content `{}`) |
| `theme.yaml` layer | `application/vnd.gameplane.theme.metadata.v1+yaml` |
| `theme.css` layer | `text/css` |
| Preview layers | `image/png` or `image/webp` |
| Reference | `<registry>/<name>:<version>` |

Each layer is titled with its path inside the package. Pushing uses the `oras`
CLI, which reads the registry credentials from the Docker config. Sign the
pushed artifact with `cosign sign`, as for modules.

## Sharing a theme

The marketplace is the spec 043 catalog. A theme is picked up from a theme
source, and each user installs it for their own account. For the contract and
the design decisions behind these rules, see
[`specs/043-theme-marketplace/contracts/theme-package.md`](../specs/043-theme-marketplace/contracts/theme-package.md)
and [`specs/043-theme-marketplace/contracts/gp-module-theme-cli.md`](../specs/043-theme-marketplace/contracts/gp-module-theme-cli.md).
