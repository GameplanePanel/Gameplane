# Research: App Localization & Community Language Packs

**Feature**: `019-localization-language-packs` | **Date**: 2026-10-11 | **Plan**: [plan.md](./plan.md)

Settled product decisions (valgul, 2026-10-10, re-confirmed 2026-10-11): French pilot language (OD-001), Hosted Weblate (OD-002), 80% listing threshold (OD-003), per-destination notification language (OD-004). Everything below is a technical decision made during planning; none changes the spec's scope.

## Current state (verified 2026-10-11 on `master` @ 8388366)

- `web/`: React 19, TS 6 strict, Vite 8, HeroUI 3 (React Aria). No i18n library; `@react-aria/i18n` is already a dependency (component-level locale only). 110 non-test `.tsx` files hold English literals. `web/index.html` hard-codes `<html lang="en">`. Dates are formatted with `toLocale*`/`Intl` and hard-coded `en-US` in 8 files (`NotificationsPanel.tsx`, `registry-browser.tsx`, `CaptureWidget.tsx`, `ShareLinks.tsx`, `NetworkCapture.tsx`, `Logs.tsx`, `ThemeSettings.tsx`, `AdminSettings.tsx`).
- `api/`: every error body is `text/plain` written by `http.Error` — 566 call sites through `api/internal/httperr` (`Write`, `WriteCode`, `classify`) or direct `http.Error` (193 direct calls). `web/src/lib/api.ts` (`APIError`, line 64) reads the body as text.
- Per-user preferences exist (spec done_016): table `user_preferences`, `api/internal/db/preferences.go`, `GET/PUT /api/v1/users/me/preferences` (contract `specs/done_016-user-theme-customization/contracts/user-preferences-api.md`). Latest common migration is `015_telemetry_state.sql`.
- Notifications: `api/internal/notify` — sinks are a JSON list stored in the `config` table under key `notifications` (`Sink{Name, Kind, Enabled, ConfigRef, Events}`); English copy is built in `format.go` (`title`, `detail`, `formatDiscord|Slack|Webhook|Ntfy|Email`).
- Audit: `audit_events(actor, method, path, target, status, ip)` already stores no prose, so it is language-independent. Only the dashboard's labels for it need translating.
- Modules: OCI bundles pushed by `modules/build.sh` and `gp-module/internal/packager` with one layer per file (`module.yaml`, `template.yaml`, `README.md`, `icon.png`), pulled by `operator/internal/oci/bundle.go`. cosign signs the manifest digest, so every layer is covered by the signature.
- GameServer conditions are `metav1.Condition` with operator-authored `Reason` (66 distinct reason literals in `operator/internal/controller/`) and free English `Message`.
- Build contexts: every Dockerfile builds from the repo root (`web/Dockerfile` header), so a top-level directory can be copied into both the web and Go images.

## R1 — Message format and dashboard runtime

- **Decision**: ICU MessageFormat strings, rendered in the dashboard by FormatJS (`react-intl`) and compiled at build time with `@formatjs/cli`. `@react-aria/i18n`'s `I18nProvider` receives the same locale so HeroUI/React Aria internals (date pickers, number fields) match.
- **Rationale**: ICU covers FR-007 (CLDR plurals, `select`, placeholder reordering); FormatJS is runtime-only (no Babel/SWC macro, so it works unchanged with `@vitejs/plugin-react` 6); it gives `formatDate/Time/Number/RelativeTime/List/DisplayName` on top of `Intl` (FR-006); its extractor emits id + `defaultMessage` + `description`, which is exactly FR-013/FR-024; `eslint-plugin-formatjs` provides `enforce-description`, `enforce-placeholders` and `no-literal-string-in-jsx` (FR-022) as real lint rules, not suppressions. Hosted Weblate reads the extractor's output natively (Format.JS JSON format, Weblate ≥ 5.4, ICU checks built in).
- **Alternatives considered**: i18next/react-i18next (own plural/interpolation syntax, weaker placeholder checks, would need i18next-icu anyway); Lingui (PO + ICU, excellent extraction, but needs a compiler macro plugin on the Vite/oxc toolchain); hand-rolled `Intl` wrapper (re-implements plural/select parsing and extraction).

## R2 — Pack layout and one catalog for app and server

- **Decision**: New top-level stdlib-plus-`golang.org/x/text` Go module `i18n/` (17th `go.work` module, added to `GO_MODULES`) owning:
  - `i18n/locales/<lang>/app.json` — the **app** text source (dashboard + server messages + notifications), Format.JS JSON (`{"<id>": {"defaultMessage": "...", "description": "..."}}`). `en` is the reference.
  - `i18n/locales/<lang>/meta.json` — pack metadata (FR-012): language tag, native name, English name, direction, credits.
  - Go packages: `i18n/icu` (ICU subset parser/formatter: arguments, `plural` incl. `=n` and `#`, `select`, number/date skeleton passthrough), `i18n/pack` (load, validate, completeness), `i18n/catalog` (`//go:embed locales/*/*.json`, used by `api` for notifications and message ids), `i18n/cmd/i18n-check` (CI checker, contract [check-tool.md](./contracts/check-tool.md)).
  - The web build imports the same files (`web/vite.config.ts` alias `@i18n` → `../i18n/locales`; `web/Dockerfile` adds `COPY i18n/locales ../i18n/locales`).
- **Rationale**: one pack per language per text source, plain diff-friendly JSON, metadata beside it (FR-012); one catalog means translators see server messages in the same Weblate component as the dashboard; the Go module mirrors the existing `telemetryschema` pattern (shared contract consumed by several modules) and keeps `go:embed` inside its own module (embed cannot reach outside a module directory). Ids are namespaced: `web.*` dashboard, `server.*` API messages, `condition.*` operator condition reasons, `notify.*` notifications, `audit.*` audit labels.
- **Alternatives considered**: catalogs under `web/src/locales` (the API could not embed them); separate server catalog (two Weblate components and two formats for one product); YAML packs (no Weblate ICU checks).

## R3 — Server message identifiers without breaking clients (FR-025/026, SC-010)

- **Decision**: Content negotiation. When a request's `Accept` explicitly lists `application/problem+json` with a quality value above 0 (wildcards and `q=0` never select it), error responses are RFC 9457 problem details with extension members: `{"type":"about:blank","status":403,"title":"Forbidden","detail":"namespace not permitted","messageId":"server.namespace_forbidden","args":{...}}`. `detail` equals today's plain-text body minus the trailing newline `http.Error` adds. Any other `Accept` (all existing scripts, `curl`, integrations) gets today's `text/plain` body unchanged. The dashboard's fetch wrapper always sends `Accept: application/json, application/problem+json`. Contract: [server-messages.md](./contracts/server-messages.md).
- Implementation shape: `httperr` gains a typed `*Message{ID, Args, English}` error; `classify` maps known sentinels to ids; the 193 direct `http.Error` calls move to `httperr.WriteMessage`. A Go registry `api/internal/messages` (id → English ICU) is the source; `i18n-check --sync-server` writes the `server.*` entries into `i18n/locales/en/app.json`, and CI fails when they drift.
- **Rationale**: zero change for existing consumers (SC-010) and a standard, self-describing body for the dashboard.
- **Alternatives considered**: always-JSON bodies (breaks integrations reading text); custom `Gameplane-Message-Id` headers (non-ASCII args need encoding, invisible in logs and dev tools).

## R4 — Operator condition reasons (FR-025 "status reasons produced by Gameplane components")

- **Decision**: `Condition.Reason` is the stable identifier (`condition.<Type>.<Reason>`). Parameters go in a new optional status field on `GameServer`: `status.conditionArgs map[string]map[string]string` keyed by condition type, set by the reconciler in the same patch as the condition. The English `Message` keeps being written unchanged. `make generate && make manifests` in the same commit (Constitution III, CLAUDE.md rule 7). The dashboard renders `condition.*` from the catalog and falls back to `Message`.
- **Rationale**: `metav1.Condition` cannot be extended; a sibling map is additive, ignored by older dashboards and `kubectl`, and keeps business logic in the reconciler (rule 10).
- **Alternatives considered**: parsing args back out of English messages (fragile); encoding JSON in `Message` (breaks `kubectl describe`).

## R5 — Language resolution, first paint and persistence (FR-002..005, FR-009)

- **Decision**: Resolution order exactly FR-004. Signed-in choice stored as a nullable `language` column on `user_preferences` (migration `018_user_language.sql`, portable `ALTER TABLE ... ADD COLUMN language TEXT`), exposed through a dedicated `PUT /api/v1/users/me/language` and returned in `GET /api/v1/users/me/preferences` (additive field). Browser choice and the last applied language are cached in `localStorage` (`gameplane-language`) so a synchronous boot snippet in `index.html` sets `<html lang dir>` before React mounts. After sign-in the app shell waits for preferences (already fetched by `useMe()`) before first render, so no flash. Language catalogs are lazy-loaded chunks; switching re-renders through `IntlProvider` without unmounting routes, so form state survives.
- **Rationale**: matches done_016's storage and boot pattern; a dedicated endpoint avoids coupling to theme validation rules in `PUT /preferences`.
- **Alternatives considered**: `Accept-Language` server-side rendering (the SPA is static); cookie-only storage (fails FR-003).

## R6 — Instance default language (FR-036, US7)

- **Decision**: `config` key `i18n` = `{"defaultLanguage":"en"}`, edited through the existing admin config section mechanism (`PUT /api/v1/admin/config/i18n`, admin RBAC, audited). The unauthenticated `GET /auth/locale` returns only `{"defaultLanguage":"fr"}` — no version, cluster or count data (CLAUDE.md rule 3). Contract: [i18n-api.md](./contracts/i18n-api.md).

## R7 — Available languages and the 80% threshold (FR-001, FR-016, FR-017)

- **Decision**: Completeness is computed at build time by `i18n-check --manifest`, which writes `languages.json` (tag, native name, English name, direction, completeness, `listed`). `listed = completeness ≥ 80 && direction == "ltr" && tag != pseudo`. The threshold is one constant in `i18n/pack` (`ListingThreshold = 80`) that maintainers change in a normal PR. Completeness counts entries that are translated and not `needs-review`-with-mismatched-placeholders.
- **Rationale**: languages only change with releases (FR-021), so a build-time manifest is exact and needs no API call; the login page can render the picker without hitting the server.

## R8 — Changed English text (FR-018, US4 AS2)

- **Decision**: Weblate marks translations "needs editing" when the source changes. In the repo, `i18n-check` stores a hash of the English source per entry in each translation (Format.JS JSON allows an extra `sourceHash` member that Weblate preserves; if it does not, the hashes live in `i18n/locales/<lang>/.review.json`, verified at setup — see R10). A translation whose hash is stale is "needs review": still shown if its placeholders match the current English, otherwise dropped at build so English shows.

## R9 — Notifications per destination (FR-028, OD-004)

- **Decision**: `Sink` gains optional `language` (BCP 47). `notify` renders `notify.*` entries from `i18n/catalog` with the Go ICU formatter in the sink's language → instance default → `en`. Discord/Slack/ntfy/email get translated title/body; the generic webhook keeps every existing machine field unchanged and adds `title` and `text` in the sink's language plus `language`. Event `Reason`/`Message` coming from Kubernetes stay verbatim (FR-011).

## R10 — Hosted Weblate integration (FR-020, US3, OD-002)

- **Decision**: Hosted Weblate libre plan, project `gameplane`:
  - Component `app`: repo `GameplanePanel/Gameplane`, file mask `i18n/locales/*/app.json`, monolingual base `i18n/locales/en/app.json`, format `formatjs`, new-language template empty file; push branch `weblate/translations` with "GitHub pull request" push mode (never pushes to `master`; ruleset 18692396 still applies).
  - One component per module (`GameplanePanel/module`, file mask `<module>/locales/*.json`, base `<module>/locales/en.json`), created with the Component discovery add-on so new modules appear without manual steps.
  - GitHub webhook to Weblate on push → new English text is translatable within minutes (≤ 24 h, SC-006).
  - Review workflow on ("Reviewers" group per language); suggestions/machine translation never auto-approved (US3 AS3). Weblate's `Contributors` file add-on is off; credits live in `meta.json`.
  - Translator context (FR-020): Weblate's docs disagree on whether the Format.JS format imports `description` (Weblate docs, Format.JS JSON page, 5.4 vs latest). Setup task verifies on the live instance; if descriptions are not imported, a CI job pushes `description` text into Weblate "explanations" through the Weblate API.
- **Rationale**: settled by valgul; libre plan is free for OSS and owned by maintainers.

## R11 — Module translations (US6, FR-031..035)

- **Decision**: Module packs are `modules/<name>/locales/<lang>.json` (Format.JS JSON). They carry no metadata file of their own: language metadata (native name, direction) comes from the app's `i18n/locales/<lang>/meta.json`, and module contributor credits go in the module's `README.md`. Ids are derived from the template structure (`module.displayName`, `module.summary`, `template.description`, `actions.<id>.displayName`, `actions.<id>.params.<name>.displayName`, `config.<key>.displayName|description|options.<value>`, `versions.<id>.displayName`, …) and the English reference is generated by `gp-module i18n extract`. Each locale file is pushed as its own OCI layer (`application/vnd.gameplane.module.locale.v1+json`, annotation `org.opencontainers.image.title=locales/<lang>.json`) by `modules/build.sh` and `gp-module package`, so the existing cosign signature covers them (FR-033). The operator validates each file with `i18n/pack`; invalid entries are dropped, the rest stored in a ConfigMap `<template>-locales` (owner: the GameTemplate) and a `LocalesValid` condition plus a Warning event report problems (FR-034). The API serves `GET /api/v1/modules/{name}/locales/{tag}`. Contract: [module-locales.md](./contracts/module-locales.md).
- **Rationale**: Kubernetes primitives first (rule 9): a ConfigMap keeps translations out of the GameTemplate object (etcd size) and lets the API read them with existing RBAC.

## R12 — Automated checks (FR-015, FR-022, FR-023, SC-007)

- **Decision**: `i18n-check` (Go, contract [check-tool.md](./contracts/check-tool.md)) runs in CI on every PR in both repos: pack well-formedness, metadata validity, ICU parse, placeholder set equality per plural branch, markup rejection (any `<tag>` other than designated rich-text placeholders `<b>`, `<link>`; no HTML entities, no `javascript:`), unknown ids (stale), English extraction drift (`formatjs extract` output must equal `i18n/locales/en/app.json`), server registry drift, unused ids, completeness table with before/after against the PR base in the job summary. Dashboard bypass detection is ESLint (`formatjs/no-literal-string-in-jsx` plus a small local rule for literal `aria-label`/`title`/`placeholder` props), enabled for each directory as it is migrated and for all of `web/src` once migration ends. A seeded fixture set under `i18n/testdata/defective/` proves SC-007.

## R13 — Pseudo-language (FR-038, SC-009)

- **Decision**: `en-XA` generated at build by `formatjs compile --pseudo-locale en-XA` style transform (accented, ~40% longer, bracketed). Bundled only when `VITE_I18N_PSEUDO=1` (dev server and the Playwright pseudo project); never in `languages.json` of release builds.

## R14 — Testing (Constitution I, VI)

- Go unit: `i18n/*` (≥ 90% coverage target, same tier as `telemetryschema`), `api/internal/httperr`, `api/internal/notify`, `api/internal/db` migration.
- envtest: operator `LocalesValid` + ConfigMap, `conditionArgs`.
- Go API E2E (`test/e2e`, registered in `buckets.sh`): problem+json negotiation and unchanged text/plain body; `PUT /users/me/language` persistence; `GET /auth/locale` leaks nothing; notification test send in French to a capture webhook; module with French locale layer → `GET /modules/{name}/locales/fr`; invalid locale entry still installs and reports.
- Playwright (`web/e2e/specs/live/`): switch language without reload and keep form input; persistence across contexts; login/share pickers; 0%-language fallback (SC-008); pseudo-language screens (SC-009) via the existing screenshot suite.
- Vitest: resolution order, formatting, fallback, `APIError` problem parsing.

## Sources

- Weblate docs, Format.JS JSON file: https://docs.weblate.org/en/latest/formats/formatjs.html (and 5.4 page https://docs.weblate.org/hu/weblate-5.4/formats/formatjs.html)
