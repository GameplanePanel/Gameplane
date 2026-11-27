# Contract: themes API (spec 043)

All routes under the API base path, JSON, session or bearer auth as today. Errors use the existing `httperr` shape. Follows OD-4 A (settled).

## Theme sources (admin, `themes:manage`)

| Method | Path | Body / result |
|---|---|---|
| GET | `/themes/sources` | list: `{name, type, spec, verify, credentialSecret, refreshInterval, enabled, builtin, lastSyncAt, lastError, themeCount}`; `credentialSecret` is a Secret name or null, never a Secret value |
| POST | `/themes/sources` | create: `{name, type, spec, credentialSecret?, verify?, refreshInterval?, enabled?}` → 201 + source; 409 on duplicate name |
| PUT | `/themes/sources/{name}` | replace (`credentialSecret` omitted = keep, `null` or `""` = clear; it stays when the location changes, as on a ModuleSource) |
| DELETE | `/themes/sources/{name}` | 204; catalog entries go, users' installed copies stay (FR-013) |
| POST | `/themes/sources/{name}/refresh` | 202; indexes now |

Validation mirrors `ModuleSource` CEL rules: type-specific spec required, `verify` only for `oci`, interval ≥ 5m. Source URLs pass `netguard.IsAllowed` at dial time, the same rule as module sources: private and in-cluster addresses are allowed, link-local and cloud metadata addresses are refused (valgul 2026-11-27). `oci.insecure` only allows plain HTTP to the registry.

`credentialSecret` names a Secret the admin creates in the credentials namespace, like a ModuleSource `pullSecretRef` or `secretRef` (OD-5, valgul 2026-11-27: "Secret name, like modules"). The panel only reads it, for each pull, and never creates, changes or deletes it. It must be a Kubernetes object name; a `local` source takes none (422). It holds `.dockerconfigjson` for an `oci` source, and `token` or `username` + `password` for a `git` or `http` source. A missing Secret or missing keys fail the source with a `lastError` that names the Secret, never its values. A restricted install (`nativeRBAC.remoteClusters: false`) lists the name in `nativeRBAC.runtimeSecretNames` so the API may read it.

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
| POST | `/users/me/themes` | `{catalogId, select?: bool}` → 201 + installed theme; 409 `already_installed`; 422 `entry_not_valid` if the entry is not `valid`; 409 `source_disabled` if its source is disabled (entries of a disabled source stay cached but are hidden from the catalog) |
| GET | `/users/me/themes/{id}` | installed theme + `tokens`, `css` (used to render) |
| POST | `/users/me/themes/{id}/update` | moves to the newer `valid` entry with the same `source_id` and name → 200; 409 `no_update` if none; 409 `source_disabled` if the source is disabled; 409 `source_removed` if the source was deleted (matching uses the source id, never the name, see data-model.md) |
| DELETE | `/users/me/themes/{id}` | 204; if selected, preferences fall back to Pink (FR-011) |

Every route acts on the caller's own rows only; another user's id returns 404.

## Preferences (spec 016, extended)

`PUT /users/me/preferences` accepts `themeType: "installed"` with `installedThemeId`; 422 if the id is not the caller's. The profile's preferences object gains `installedThemeId` and, when selected, `installedTheme: {tokens, css, version}` so the dashboard renders without an extra request at boot.

## Audit

Each write route records the events in data-model.md. Reads are not audited.
