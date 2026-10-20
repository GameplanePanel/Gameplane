# Contract: Language Preference & Instance Language API

**Feature**: `019-localization-language-packs` | **Binding modules**: `api/internal/handlers/users.go`, `api/internal/handlers/config.go`, `api/internal/handlers/auth_providers.go`, `api/internal/db/`, `api/internal/notify`, `web/src/lib/endpoints.ts` | **Status**: Binding

## 1. `PUT /api/v1/users/me/language`

Auth: session + CSRF. Any authenticated user, own record only.

```json
{ "language": "fr" }      // or { "language": null } to clear
```

- `language`: null or a **listed** language of this release (`i18n/catalog.Listed()`, the same predicate as the picker and `languages.json`: app completeness ≥ 80%, ltr, not `en-XA`). A tag that is unknown or shipped but unlisted → `400`, `server.i18n.unknown_language`, args `{language}` (FR-017).
- `204 No Content` on success; audit event recorded as usual.

`GET /api/v1/users/me/preferences` (done_016 contract) gains `"language": "fr" | null`. `PUT /api/v1/users/me/preferences` ignores `language` (unchanged validation).

## 2. Instance default language: admin config section `i18n`

Uses the existing admin config mechanism (`api/internal/handlers/config.go`, `GET /api/v1/admin/config/`, `PUT /api/v1/admin/config/{section}`, admin RBAC, audited). New section `i18n` with validator `validateI18n`:

```json
{ "defaultLanguage": "fr" }
```

- `defaultLanguage` MUST be a listed language of this release, else `400 server.i18n.unlisted_language`, args `{language}`.
- Absent section = `en`.
- The language list (tag, native name, completeness, listed) is not served by the API; the dashboard reads it from its build-time `languages.json` (research R7).

## 3. `GET /auth/locale` (unauthenticated)

```json
{ "defaultLanguage": "fr" }
```

Only this field. No version, cluster, user or count data (CLAUDE.md rule 3). Same rate limit class as `/auth/providers`.

## 4. Notification sinks

The admin config section `notifications` (`PUT /api/v1/admin/config/notifications`, validator `validateNotifications`) accepts `"language": "<tag>"` per sink; omitted = instance default. Unknown or unlisted tag → `400 server.i18n.unknown_language`. Test send (`POST /api/v1/admin/notifications/sinks/{name}/test`) renders in the sink's language.

Webhook payload (additive):

```json
{ "type": "backup.failed", "ts": "…", "kind": "Backup", "name": "nightly", "namespace": "game-servers",
  "reason": "…", "message": "…", "instance": "prod",
  "language": "fr", "title": "[prod] échec de la sauvegarde : game-servers/nightly", "text": "…" }
```

Existing fields keep their names and English/machine values.
