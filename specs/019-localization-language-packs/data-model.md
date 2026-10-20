# Data Model: App Localization & Community Language Packs

**Feature**: `019-localization-language-packs` | **Date**: 2026-10-11 | **Plan**: [plan.md](./plan.md)

Spec entities (spec.md, Key Entities) mapped to storage. File formats are defined in [contracts/pack-format.md](./contracts/pack-format.md).

## Language

| Field | Type | Rules |
|---|---|---|
| `tag` | BCP 47 string | e.g. `fr`, `pt-BR`; lower-case language, upper-case region; unique |
| `nativeName` | string | 1–64 chars, shown in the picker (`Français`) |
| `englishName` | string | 1–64 chars (`French`) |
| `direction` | `ltr` \| `rtl` | `rtl` packs are never listed in v1 (spec edge case) |
| `credits` | list of `{name, url?}` | optional |
| `completeness` | percent, derived | completeness of the **app** text source for this language (module packs never affect it; module text falls back per entry). This is the single value shown in the picker and written to `languages.json` |
| `listed` | bool, derived | `completeness ≥ 80 && direction == ltr && tag != en-XA` (OD-003), using the app completeness above |

Stored in `i18n/locales/<tag>/meta.json`. `en` is always listed (completeness 100 by definition). `en-XA` (pseudo) has no meta file in releases.

## Text Source

| Value | Location | Reference file |
|---|---|---|
| `app` | `GameplanePanel/Gameplane` | `i18n/locales/en/app.json` |
| `module:<name>` | `GameplanePanel/module` | `modules/<name>/locales/en.json` |

## Reference Entry

| Field | Type | Rules |
|---|---|---|
| `id` | string | App ids: `^[a-z][a-z0-9_]*(\.[a-z0-9_-]+)+$` with namespace prefix `web.`, `server.`, `condition.`, `notify.`, `audit.`. Module ids: the module id rule in [module-locales.md](./contracts/module-locales.md) §1 (upper case, digits, `_`, `-`, `~0`/`~1` escapes). Stable once shipped |
| `defaultMessage` | ICU MessageFormat | parses; only designated rich-text tags (`<b>`, `<link>`) |
| `description` | string | required (translator note: where shown, placeholder meaning, length limit) |
| placeholders | derived | set of argument names (incl. inside `plural`/`select`) |

## Language Pack (translation of one text source into one language)

| Field | Type | Rules |
|---|---|---|
| entries | map `id → {defaultMessage, description?, sourceHash?}` | `defaultMessage` here is the translation |
| per-entry status | derived | `translated` (present, placeholders match, hash current) · `needs-review` (hash stale, placeholders still match → shown) · `invalid` (placeholders mismatch or markup → not shown) · `missing` |
| completeness | derived | `translated + needs-review` ÷ reference entries, per text source; never stored by hand |

State transitions for one entry: `missing → translated` (contribution merged) → `needs-review` (English changed) → `translated` (re-approved) · any state → `invalid` when English placeholders change and the translation is not updated (English shown until fixed).

## Language Preference

| Where | Field | Rules |
|---|---|---|
| `user_preferences.language` (new nullable column, migration `api/internal/db/migrations/common/018_user_language.sql`) | TEXT | BCP 47 tag or NULL (= no saved choice). Kept even if the language disappears from a release (spec edge case); resolution then skips it |
| browser `localStorage['gameplane-language']` | JSON `{"choice": tag \| null, "applied": tag}` | `choice` is the unauthenticated/browser choice (FR-005); `applied` drives the no-flash boot snippet |

Resolution (FR-004): user `language` → browser `choice` → first available of `navigator.languages` (regional → base) → instance default → `en`.

## Instance Language Settings

`config` row key `i18n`, value `{"defaultLanguage": "<tag>"}`; absent row = `en` (FR-036). Must be a listed language when saved.

## Notification Destination Language

`config` row key `notifications`, each `Sink` gains `"language": "<tag>"` (optional, omitted = instance default). No migration (JSON value). If the tag is no longer shipped: instance default → `en`.

## Localizable Server Message

| Field | Type | Rules |
|---|---|---|
| `messageId` | string | `server.*` id registered in `api/internal/messages` |
| `args` | object `string → string \| number` | names equal the English entry's placeholders |
| `detail` | string | the existing English text, unchanged (FR-026) |

## GameServer condition arguments (CRD, additive)

`GameServerStatus.ConditionArgs map[string]map[string]string` (`json:"conditionArgs,omitempty"`), key = condition `Type`, value = args for that condition's current `Reason`. Cleared when the condition's reason changes to one without args. Translation id: `condition.<type>.<reason>` (lower-cased).

## Module locale storage (cluster)

ConfigMap `<gametemplate-name>-locales` in the GameTemplate's namespace, owner reference → GameTemplate, data key `<tag>.json` = validated entries only. GameTemplate condition `LocalesValid` (`True` / `False` with reason `InvalidEntries`, message lists counts per language) + Warning event naming the bad entries.

## Audit events

Unchanged schema (`audit_events` already stores method/path/target/status only). Display labels come from `audit.*` ids in the dashboard; unknown combinations show the raw method + path as today (pre-feature rows stay readable, FR-029).
