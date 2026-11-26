# Contract: themes API (spec 043)

All routes under the API base path, JSON, session or bearer auth as today. Errors use the existing `httperr` shape. Follows OD-4 A (settled).

## Theme sources (admin, `themes:manage`)

| Method | Path | Body / result |
|---|---|---|
| GET | `/themes/sources` | list: `{name, type, spec, verify, refreshInterval, enabled, lastSyncAt, lastError, themeCount}`; no credentials |
| POST | `/themes/sources` | create: `{name, type, spec, credential?, verify?, refreshInterval?, enabled?}` → 201 + source; 409 on duplicate name |
| PUT | `/themes/sources/{name}` | replace (credential omitted = keep, `null` = clear) |
| DELETE | `/themes/sources/{name}` | 204; catalog entries go, users' installed copies stay (FR-013) |
| POST | `/themes/sources/{name}/refresh` | 202; indexes now |

Validation mirrors `ModuleSource` CEL rules: type-specific spec required, `verify` only for `oci`, interval ≥ 5m. Source URLs pass `netguard.IsPublic` at dial time unless the admin marks `oci.insecure` for a private registry (same rule as module sources).

## Catalog (any signed-in user)

| Method | Path | Result |
|---|---|---|
| GET | `/themes/catalog?q=&tag=&source=` | `[{id, source, name, displayName, author, description, tags, license, version, modes: ["light","dark"], status, lowContrast, installed: {version}?, updateAvailable}]` plus `sources: [{name, lastError}]` for unreachable ones (US1.4) |
| GET | `/themes/catalog/{id}` | entry + `changelog`, `homepage`, `problems`, and for `valid` entries `tokens` and `css` (used by preview, R6) |
| GET | `/themes/catalog/{id}/preview/{light\|dark}` | image bytes, `Cache-Control: private, max-age=3600`, `Content-Type` from the stored type, `X-Content-Type-Options: nosniff` |

## Installed themes (own account only)

| Method | Path | Body / result |
|---|---|---|
| GET | `/users/me/themes` | `[{id, source, name, displayName, version, modes, updateAvailable, sourceRemoved, selected}]` |
| POST | `/users/me/themes` | `{catalogId, select?: bool}` → 201 + installed theme; 409 if already installed; 422 if entry not `valid` |
| GET | `/users/me/themes/{id}` | installed theme + `tokens`, `css` (used to render) |
| POST | `/users/me/themes/{id}/update` | moves to the catalog's current valid version → 200; 409 if no newer version |
| DELETE | `/users/me/themes/{id}` | 204; if selected, preferences fall back to Pink (FR-011) |

Every route acts on the caller's own rows only; another user's id returns 404.

## Preferences (spec 016, extended)

`PUT /users/me/preferences` accepts `themeType: "installed"` with `installedThemeId`; 422 if the id is not the caller's. The profile's preferences object gains `installedThemeId` and, when selected, `installedTheme: {tokens, css, version}` so the dashboard renders without an extra request at boot.

## Audit

Each write route records the events in data-model.md. Reads are not audited.
