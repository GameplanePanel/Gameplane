# Data model: Theme marketplace (spec 043)

Follows OD-4 A (settled 2026-11-26): theme sources are panel data in the API database. All tables arrive in one portable migration `api/internal/db/migrations/common/028_theme_marketplace.sql` (portable SQL per `api/internal/db/migrations/README.md`: timestamps bound from Go, no dialect-specific syntax).

## theme_sources

One admin-managed source (FR-001, FR-002). Mirrors `ModuleSourceSpec`.

| Column | Type | Rules |
|---|---|---|
| `id` | integer PK | |
| `name` | text, unique | DNS-1123 label, max 63; `official` reserved for the seeded source |
| `type` | text | `oci` \| `git` \| `http` \| `local` |
| `spec` | text (JSON) | type-specific settings: `oci {url, themes[], insecure}`, `git {url, ref, path}`, `http {url}`, `local {path}`; plus `allow[]` globs |
| `credential` | blob, nullable | encrypted with the panel key (same scheme as kubeconfig credentials, spec 025); never returned by the API |
| `verify` | text (JSON), nullable | `{key: PEM}` or `{keyless: {issuer, identity}}`, `requireTransparencyLog`; only for `oci` |
| `refresh_interval_s` | integer | default 3600, min 300 |
| `enabled` | integer (0/1) | default 1 |
| `last_sync_at` | text, nullable | RFC 3339 |
| `last_error` | text, nullable | human-readable, no credentials |
| `created_at`, `updated_at` | text | RFC 3339 |

Seed: one row `official`, `type=oci`, `url=ghcr.io/gameplanepanel/gameplane-themes`, keyless verify pinned to the `GameplanePanel/themes` release workflow; skipped when Helm `themes.officialSource.enabled=false`.

## theme_catalog_entries

The cached latest version of each theme in each source (R4). Rebuilt by indexing; never edited by users.

| Column | Type | Rules |
|---|---|---|
| `id` | integer PK | stable per (`source_id`, `name`) |
| `source_id` | FK → theme_sources, cascade delete | |
| `name` | text | DNS-1123 label; unique with `source_id` (FR-013) |
| `version` | text | semver of the cached version |
| `versions` | text (JSON) | all versions found, semver-sorted descending |
| `digest` | text | OCI manifest digest, `git:<sha>` or `sha256:<content hash>` |
| `metadata` | text (JSON) | display name, author, description, tags, license, changelog, homepage (contracts/theme-package.md) |
| `tokens` | text (JSON) | `{light?: {14 tokens}, dark?: {14 tokens}}`, at least one mode |
| `css` | text, nullable | sanitized by `themepkg.SanitizeCSS`, ≤ 32 KiB |
| `preview_light`, `preview_dark` | blob, nullable | PNG or WebP, ≤ 512 KiB each |
| `status` | text | `valid` \| `invalid` \| `unverified` |
| `problems` | text (JSON) | list of `{rule, detail, line?}` from validation or verification |
| `low_contrast` | integer (0/1) | flag only (edge case "Contrast") |
| `indexed_at` | text | RFC 3339 |

Only `valid` entries can be installed. `unverified` = the source requires signatures and this version has none or a wrong one (US4.2).

## user_installed_themes

A theme one user installed for their own account (OD-1, FR-006, FR-010, FR-013). A snapshot: it does not change when the catalog does.

| Column | Type | Rules |
|---|---|---|
| `id` | integer PK | |
| `user_id` | FK → users, cascade delete | |
| `source_id` | FK → theme_sources, nullable, `ON DELETE SET NULL` | the durable source identity used for update and removal matching |
| `source_name` | text | copied for display, so the row survives source removal |
| `theme_name` | text | unique with `user_id` + `source_id` + `theme_name` while `source_id` is set |
| `version`, `digest` | text | installed version |
| `metadata` | text (JSON) | display name, author, description at install time |
| `tokens` | text (JSON) | as in catalog |
| `css` | text, nullable | re-sanitized at install |
| `installed_at`, `updated_at` | text | RFC 3339 |

Derived per request, not stored: `updateAvailable` (the catalog entry with the same `source_id` and theme name is `valid`, its source is enabled, and its semver is higher), `sourceRemoved` (`source_id` is NULL, the source is disabled, or it has no entry for this theme). Matching never uses `source_name`: a source deleted and later recreated under the same name gets a new id, so old installs stay `sourceRemoved` and never pick up updates from the new source.

## user_preferences (changed, spec 016)

| Change | Rules |
|---|---|
| `theme_type` accepts `installed` | in addition to `preset` and `custom_colors` |
| new `installed_theme_id` integer, nullable | FK → user_installed_themes (same user), `ON DELETE SET NULL`; required when `theme_type=installed` |

Removing the selected theme sets `theme_type=preset`, `preset_id=pink`, `installed_theme_id=NULL` in the same transaction (FR-011). Custom colors and custom CSS stay as spec 016 keeps them. The spec 016 export (`theme-export.ts`) records an installed theme as `{source, name, version}` only; import onto an instance without it falls back to Pink and says so.

## State transitions

```text
Catalog entry:   (indexed) -> valid | invalid | unverified ; re-index replaces it ;
                 source verify settings edited -> every entry re-checked (failures -> unverified) ;
                 source disabled -> entries kept but hidden, not installable ; source deleted -> gone
Installed theme: (install from valid entry) -> installed -> (update from newer valid entry) -> installed@new
                 installed -> (remove) -> gone  [if selected: user falls back to Pink]
                 source disabled/removed -> installed, sourceRemoved=true (keeps working, no update)
Preview:         client-only; no server state
```

## Audit events (R7)

`theme_source.create`, `theme_source.update`, `theme_source.delete`, `theme_source.refresh` (actor, source name, type), `theme.install`, `theme.update`, `theme.remove` (actor, source, theme, from/to version, digest).
