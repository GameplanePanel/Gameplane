---
description: "Task list for 019 App Localization & Community Language Packs"
---

# Tasks: App Localization & Community Language Packs

**Input**: Design documents from `specs/019-localization-language-packs/` (spec.md, plan.md, research.md, data-model.md, contracts/, quickstart.md)

**Prerequisites**: All four open decisions resolved (OD-001 French, OD-002 Hosted Weblate, OD-003 80%, OD-004 per destination; valgul 2026-10-10, re-confirmed 2026-10-11).

**Tests**: Included. Constitution I requires E2E coverage in `test/e2e/` (registered in `test/e2e/buckets.sh`) plus unit/envtest layers. Tests run on CI only (CLAUDE.md rule 8); locally only `go build ./...` and `npx tsc --noEmit`.

**Binding rules for every task** (CLAUDE.md): implementation through Workflow scripts with explicit `model` per agent starting at haiku, tier-up diff review; signed conventional commits (`git commit -s`), never amend pushed commits; no `//nolint`/`eslint-disable`/`@ts-ignore`; Go errors wrapped with `%w`; never delete or weaken tests; dashboard UI designed in `design.pen` via Pencil MCP first (local machine only) and exported to `assets/design-export/`; `.pen` files never read or edited directly; CRD edits followed by `make generate && make manifests` in the same commit; module work committed in `GameplanePanel/module` first, root pointer bumped only after that PR merges; update each touched module's `specs.md` in the same change.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1–US7 from spec.md

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create the shared `i18n/` Go module and add dashboard dependencies (plan.md "Source Code", research R1, R2).

- [X] T001 Create Go module `i18n/` (`i18n/go.mod`, module path `github.com/GameplanePanel/gameplane/i18n`, go 1.26.0, dependency `golang.org/x/text` only) and add `./i18n` to `go.work`
- [X] T002 Add `i18n` to `GO_MODULES` in `Makefile` and create `i18n/.testcoverage.yml` with a 90% threshold (same tier as `telemetryschema`); add `i18n 90` to the coverage list in `CLAUDE.md` and the repository map entry `i18n/  shared language packs, ICU formatter, pack validator, i18n-check`
- [X] T003 [P] Create `i18n/specs.md` describing responsibilities, pack format (link `specs/019-localization-language-packs/contracts/pack-format.md`), consumers (web, api, operator, gp-module, module repo CI) and invariants
- [X] T004 [P] Create the English reference skeleton `i18n/locales/en/app.json` (`{}`) and `i18n/locales/en/meta.json` (`tag en`, `nativeName English`, `englishName English`, `direction ltr`) per contracts/pack-format.md §1
- [X] T005 [P] Add `react-intl` (dependency) and `@formatjs/cli`, `eslint-plugin-formatjs` (devDependencies) to `web/package.json` and regenerate `web/package-lock.json` with `npm install` (no `--legacy-peer-deps`)
- [X] T006 [P] Add `COPY i18n/ ./i18n/` to `api/Dockerfile` and `operator/Dockerfile` (and `gp-module` image if present) before the Go build, and `COPY i18n/locales ../i18n/locales` to `web/Dockerfile` before `npm run build`

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Pack library, ICU formatter, checker, server-message plumbing and dashboard i18n runtime that every story depends on.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

### `i18n/` library and checker

- [X] T007 [P] Implement ICU subset parser in `i18n/icu/parse.go`: literals, `{arg}`, `{arg, number|date|time[, style]}`, `{arg, plural, =n {…} one {…} … other {…}}` with `#`, `{arg, select, … other {…}}`, apostrophe escaping, rich tags `<b>…</b>`/`<link>…</link>`; return AST + error with offset
- [X] T008 [P] Implement `i18n/icu/placeholders.go` returning the set of argument names (recursing into plural/select branches) and the set of rich tags used
- [X] T009 Implement `i18n/icu/format.go` formatting an AST with `map[string]any` args and a `language.Tag`, using `golang.org/x/text/feature/plural` for CLDR categories; unknown args render as `{name}` and never panic
- [X] T010 [P] Unit tests `i18n/icu/parse_test.go`, `placeholders_test.go`, `format_test.go` covering French/Polish/Arabic plural categories, reordering, escaping and malformed input
- [X] T011 Implement `i18n/pack/pack.go`: load Format.JS JSON (`id → {defaultMessage, description?, sourceHash?}`) and `meta.json` with unknown-key rejection; types `Pack`, `Meta`, `Entry`; `ListingThreshold = 80`
- [X] T012 Implement `i18n/pack/validate.go` producing findings with codes `malformed-pack`, `invalid-meta`, `icu-syntax`, `missing-placeholder`, `extra-placeholder`, `markup`, `unknown-id`, `missing-description`, `needs-review` exactly as contracts/check-tool.md; each finding names file, id and a plain-language problem (FR-015)
- [X] T013 Implement `i18n/pack/completeness.go` (per-entry status translated / needs-review / invalid / missing per data-model.md; completeness = (translated + needs-review) ÷ reference; `Listed()` = completeness ≥ ListingThreshold && direction ltr && tag != `en-XA`) and `sourceHash` (first 12 hex of SHA-256 of the English message)
- [X] T014 [P] Unit tests `i18n/pack/pack_test.go`, `validate_test.go`, `completeness_test.go`
- [X] T015 Create seeded defective fixtures `i18n/testdata/defective/<code>/` (one directory per error code in contracts/check-tool.md, each with a minimal `en/app.json`, `xx/app.json`, `xx/meta.json`; the `extract-drift`, `server-drift` and `unused-id` fixtures also carry `extracted.json`, `registry.json` or `used-ids.txt` plus an `args` file with the flags to pass, per contracts/check-tool.md) and `i18n/pack/fixtures_test.go` asserting each fixture yields exactly its code (SC-007)
- [X] T016 Implement `i18n/catalog/catalog.go` with `//go:embed locales/*/*.json`, `Tags()`, `Listed()` (app completeness ≥ 80, ltr, not `en-XA`, same rule as `pack.Listed`), `Lookup(tag language.Tag, id string, args map[string]any) (string, bool)` resolving regional → base → `en`, and dropping invalid translations at load (English shown instead). *Done note: the `//go:embed` lives in the root package `i18n` (`i18n/embed.go`, `i18n.Locales`) because embed paths cannot use `..`; `catalog` loads from it.*
- [X] T017 [P] Unit tests `i18n/catalog/catalog_test.go` (fallback chain, invalid-entry drop, missing id)
- [X] T018 Implement CLI `i18n/cmd/i18n-check/main.go` with every flag in contracts/check-tool.md (`--reference`, `--base`, `--extracted`, `--sync-server`, `--check-server`, `--used-ids`, `--manifest`, `--stamp`, `--module`, `--summary`), exit codes 0/1/2, and a markdown completeness table (tag, base → head, listed). *Done note: `--registry <file>` added for the two server flags (contracts/check-tool.md updated); `--module` validates the flat `en.json` + `<tag>.json` layout now, and the template-derived id check plugs in with T084.*
- [X] T019 [P] Unit tests `i18n/cmd/i18n-check/main_test.go` (flag parsing, manifest output shape from contracts/pack-format.md §4, summary before/after)

### API server-message plumbing (contract server-messages.md)

- [X] T020 Create registry `api/internal/messages/messages.go` (`type ID string`, `Entry{ID, English, Description}`, `New(id, args) *httperr.Message`, `All() []Entry`) seeded with the ids for every sentinel in `api/internal/httperr/httperr.go` `classify` (`server.namespace_forbidden`, `server.cluster_forbidden`, `server.not_found`, `server.already_exists`, `server.conflict_retry`, `server.forbidden`, `server.unauthorized`, `server.invalid`, `server.bad_request`, `server.empty_body`, `server.internal`, `server.remote_cluster_not_implemented`) plus `server.generic.<status>` for 400/401/403/404/409/413/422/429/500/501/502/503 and `server.auth.invalid_credentials`; English text byte-identical to today's strings
- [X] T021 Extend `api/internal/httperr/httperr.go`: `Message{ID, Args, Status, English}` implementing `error`; `WriteMessage`; negotiation helper that parses comma-separated `Accept` media ranges and `q` values (RFC 9110) and selects problem details only when `application/problem+json` is listed explicitly with q > 0 (wildcards and `q=0` never select it) → RFC 9457 body with `type`, `status`, `title`, `detail`, `messageId`, `args`; otherwise unchanged `http.Error` text/plain); `Write`/`WriteCode`/`classify` map sentinels to ids and honour `errors.As(*Message)`; `detail` omits the trailing newline of the plain-text body; 5xx `detail` stays status text
- [X] T022 [P] Unit tests `api/internal/httperr/httperr_test.go` additions: plain-text body byte-identical with and without the change for every classify branch (SC-010); problem body fields; `server.generic.*` for unmapped errors; 5xx never leaks error text; `Accept: application/problem+json;q=0` and `*/*` get text/plain
- [X] T023 Wire `i18n-check --sync-server` to the registry: add `api/internal/messages/export.go` (JSON dump of `All()`), a Makefile target `i18n-sync` (runs the export then `i18n-check --sync-server` into `i18n/locales/en/app.json`), and commit the resulting `server.*` entries
- [X] T024 Add `web/src/lib/api.ts` changes: always send `Accept: application/json, application/problem+json`; `APIError` gains `messageId?: string` and `args?: Record<string, string | number>` parsed from problem bodies; `message` keeps using `detail` so existing callers are unchanged; tests in `web/src/lib/api.test.ts`

### Dashboard i18n runtime (research R1, R5, R7)

- [X] T025 Add Vite alias `@i18n` → `../i18n/locales` and a build step in `web/vite.config.ts` (and `web/package.json` scripts `i18n:extract`, `i18n:compile`, `i18n:manifest`) that runs `formatjs extract 'src/**/*.{ts,tsx}' --format simple`-compatible output with descriptions, `formatjs compile --ast` per language into `web/src/generated/i18n/<tag>.json`, and `i18n-check --manifest web/src/generated/i18n/languages.json`; add `web/src/generated/` to `web/.gitignore`
- [X] T026 Create `web/src/lib/i18n/resolve.ts` implementing FR-004 order (user choice → browser choice → `navigator.languages` regional→base → instance default → `en`) over the listed languages from `languages.json`; unit tests `web/src/lib/i18n/resolve.test.ts`
- [X] T027 Create `web/src/lib/i18n/storage.ts` for `localStorage['gameplane-language']` `{choice, applied}` with try/catch around every access; tests `storage.test.ts`
- [X] T028 Create `web/src/lib/i18n/I18nProvider.tsx`: lazy-load compiled catalog chunk, wrap `react-intl` `IntlProvider` (`defaultLocale en`, `onError` logs missing ids in dev only, never renders ids) and `@react-aria/i18n` `I18nProvider`, update `document.documentElement.lang`/`dir` on change, switch without unmounting children (FR-002, FR-009); tests `I18nProvider.test.tsx` (switch keeps form state, fallback to English per message, no raw ids — FR-008)
- [X] T029 Add the synchronous boot snippet to `web/index.html` that reads `gameplane-language.applied` and sets `<html lang dir>` before React mounts (same pattern as the done_016 theme boot script); test in `web/src/__tests__/i18nBoot.test.ts`
- [X] T030 Mount the provider in the app root (`web/src/main.tsx` / `web/src/router/tree.tsx` root route) so it wraps every route including `/login` and `/share/*`
- [X] T031 Create formatting helpers `web/src/lib/i18n/format.ts` (`useFormat()` returning date, time, number, relative time, duration and list formatters bound to the active locale, FR-006) and replace hard-coded `en-US`/`toLocale*` calls in `web/src/components/ui/NotificationsPanel.tsx`, `web/src/components/registry-browser.tsx`, `web/src/components/CaptureWidget.tsx`, `web/src/routes/tabs/settings/ShareLinks.tsx`, `web/src/routes/tabs/settings/NetworkCapture.tsx`, `web/src/routes/tabs/Logs.tsx`, `web/src/routes/ThemeSettings.tsx`, `web/src/routes/AdminSettings.tsx`; tests `format.test.ts`
- [X] T032 Create `web/src/lib/i18n/serverMessage.ts` (`useServerMessage(err)` → translated text for `APIError.messageId` with args, appending `detail` for `server.generic.*` ids, else `detail`/`message`) and use it in `web/src/lib/errors.ts`; tests `serverMessage.test.ts`
- [X] T033 Add `eslint-plugin-formatjs` to `web/eslint.config.js` with `formatjs/enforce-description`, `formatjs/enforce-placeholders`, `formatjs/no-literal-string-in-jsx` (including `aria-label`, `title`, `placeholder`, `alt` props) as errors, scoped through a `MIGRATED_I18N_PATHS` files list that starts with `src/lib/i18n/**` and grows with each migration task (FR-022)
- [X] T034 Add CI job `i18n` in `.github/workflows/ci.yaml`: run `npm run i18n:extract` and `i18n-check --extracted … --check-server --used-ids … --base <base checkout>/i18n/locales --summary $GITHUB_STEP_SUMMARY i18n/locales`; include `i18n` in `make lint` (Makefile target `check-i18n`)

**Checkpoint**: Foundation ready. English-only catalog renders the dashboard unchanged; API plain-text bodies unchanged.

---

## Phase 3: User Story 1 - Choose my display language (Priority: P1) 🎯 MVP

**Goal**: A user picks a language (native name + completeness), the whole dashboard switches without reload, persists across devices, formats dates/numbers per locale; login and share pages offer a browser-local picker.

**Independent Test**: quickstart.md scenarios 1–3 and 5.

### Tests for User Story 1

- [ ] T035 [P] [US1] Go API E2E `test/e2e/i18n_language_pref_e2e_test.go`: `PUT /api/v1/users/me/language` `fr` → `GET /users/me/preferences` returns `language: "fr"`; `null` clears; unknown tag → 400 with `server.i18n.unknown_language` problem body; register in `test/e2e/buckets.sh` (bucket `api-auth`, respect its login budget)
- [X] T036 [P] [US1] Go API E2E `test/e2e/i18n_auth_locale_e2e_test.go`: unauthenticated `GET /auth/locale` returns exactly `{"defaultLanguage": …}` and no other keys (rule 3); register in `test/e2e/buckets.sh`
- [ ] T037 [P] [US1] Playwright `web/e2e/specs/live/i18n.spec.ts`: switch to French in personal settings with create-server dialog open and a typed name (kept, no reload, `<html lang="fr">`), reload and second browser context keep French with no English flash, login and share pages offer a picker remembered per browser, 0%-fixture language shows English everywhere (SC-008)

### Implementation for User Story 1

- [X] T038 [P] [US1] Migration `api/internal/db/migrations/common/018_user_language.sql`: `ALTER TABLE user_preferences ADD COLUMN language TEXT;` (portable, per `api/internal/db/migrations/README.md`)
- [X] T039 [US1] Add `Language *string` to `UserPreferences` and `SetLanguage(ctx, userID, *string)` (upsert keeping other columns / defaults) in `api/internal/db/preferences.go`; tests in `api/internal/db/preferences_test.go`
- [X] T040 [US1] Add `PUT /me/language` and `language` in `GET /me/preferences` in `api/internal/handlers/users.go` per contracts/i18n-api.md §1 (validate against `i18n/catalog.Listed()`, the same predicate as the picker; unknown or unlisted tag → `server.i18n.unknown_language`; handler test covers a shipped-but-unlisted tag); register the new message id in `api/internal/messages/messages.go`; handler tests `api/internal/handlers/users_language_test.go`
- [X] T041 [US1] Add `GET /auth/locale` in `api/internal/handlers/auth_providers.go`, mounted in the `/auth` route group of `api/cmd/main.go`, returning only `defaultLanguage` from config section `i18n` (absent → `en`); tests in `api/internal/handlers/auth_providers_test.go`
- [X] T042 [US1] Add `Users.setLanguage` and `Auth.locale` client methods in `web/src/lib/endpoints.ts`, `language` on the preferences type in `web/src/types.ts`; hydrate the user's language in `useMe()` so the shell renders only after it is known (US1 AS2)
- [X] T043 [US1] DESIGN (local machine, Pencil MCP): add to `design.pen` a Language section in personal settings (native names + completeness, current choice), a compact language picker for `/login` and `/share/*` (no internal info), and a mobile variant; export touched nodes to `assets/design-export/json/<id>.json` and `assets/design-export/screenshots/<id>.png` (design-export skill). *Done note (redrawn 2026-10-15 on the regrouped canvas, after the first pass from 2026-10-12 was dropped with master's PR #702 regroup): the Language card is the first card on `lWvcv` and `T1wkiT`, as instances of the new component `Ii9Hy` Gameplane/Language Card (OD-016); mobile screen `lCTci`; the compact picker component `z3mtgc` sits in the top-right corner of every login and share card (wrapper component `S0P4bl` on `N1GkB`/`gX7um` because `J14ME` cannot take a child, and a row inside `g2HLxz`, `vp8BJ` and `HKEl2`; OD-017); menu-open screen `GsofJ`; details in `assets/design-export/MANIFEST.md`.*
- [X] T044 [US1] Implement the personal-settings language section (component `web/src/components/settings/LanguagePicker.tsx`, mounted in `web/src/routes/ThemeSettings.tsx` settings page per the T043 design) calling `Users.setLanguage`, listing only `listed` languages from `languages.json`; tests `LanguagePicker.test.tsx` *Done note: `LanguagePicker` lists the listed languages from `languages.json` plus "Automatic (browser language)", saves through `Users.setLanguage` and updates the cached `["me"]` profile, so the dashboard switches without a reload or remount; a saved tag that is no longer listed shows as Automatic without being overwritten.*
- [X] T045 [US1] Add the browser-local picker to `web/src/routes/Login.tsx` and `web/src/routes/Share.tsx` (writes `gameplane-language.choice`, reads `Auth.locale` for the default); tests in `Login.test.tsx`, `Share.test.tsx` *Done note: `CompactLanguagePicker` (native names only) sits in the top-right corner of the login card and of every share card; it calls the provider's `setLanguage`, which writes `gameplane-language.choice`; the instance default comes from `Auth.locale` through `AppI18nProvider`, so the pages make no extra request. New tests are in `Login_language.test.tsx` and `Share_language.test.tsx` (the per-file `languages` mock would otherwise change the existing cases).*
- [X] T046 [US1] Add the `0%` fixture language behind `VITE_I18N_FIXTURES=1` in the build step of `web/vite.config.ts` (never in release `languages.json`) for SC-008 tests
- [X] T047 [US1] Update `web/specs.md` and `api/specs.md` (language resolution, endpoints, boot snippet, problem+json)

**Checkpoint**: Picker works with English + any listed pack; strings migrate in US4.

---

## Phase 4: User Story 2 - Contribute a language pack as files (Priority: P1)

**Goal**: A translator adds `i18n/locales/<tag>/` through a PR; checks validate and report completeness before/after; merged packs appear in the picker when ≥ 80%.

**Independent Test**: quickstart.md scenario 11.

### Tests for User Story 2

- [X] T048 [P] [US2] CI self-test job step in `.github/workflows/ci.yaml` running `i18n-check` over each `i18n/testdata/defective/*` fixture and asserting a non-zero exit with the expected code (SC-007)

### Implementation for User Story 2

- [X] T049 [US2] Implement `--stamp` and needs-review handling end to end: translations carry `sourceHash`; build drops `invalid` entries so English shows (FR-018); verify the manifest marks a pack under 80% `listed: false` (FR-017)
- [X] T050 [US2] Write contributor guide `docs/translating.md` (FR-019): both contribution paths (file PR and Weblate), copying `i18n/locales/en/` to a new tag, writing `meta.json`, ICU plural/select examples, safe tags, running the dev server with a local pack to preview (`make web-dev`), style guide and glossary of fixed/untranslated Gameplane terms (GameServer, module, sentinel, …), what the CI report means; link it from `docs/contributing.md` and `README.md`
- [X] T051 [US2] Add a "translation" PR template section or label rule (`type: docs`, `area: shared`) in `.github/` auto-label config so pack PRs are labelled automatically

**Checkpoint**: A partial test pack passes CI and shows in the job summary.

---

## Phase 5: User Story 4 - Developers add translatable text easily (Priority: P2)

**Goal**: Every dashboard string goes through the catalog; bypassing literals and unused ids are caught. (Placed before US3/US5 because the French launch content needs the migrated catalog.)

**Independent Test**: quickstart.md scenario 12.

### Implementation for User Story 4

- [X] T052 [US4] Create the scripted migration helper `web/scripts/i18n-migrate.ts` (codemod: wraps JSX text and `aria-label`/`title`/`placeholder`/`alt` literals in `FormattedMessage`/`intl.formatMessage` with generated ids `web.<path>.<slug>` and a description stub naming the screen); script only, run by one agent per wave (CLAUDE.md rule 13 "scripts over fan-out"). *Done note: `npm run i18n:migrate -- [--dry-run] [--report <file>] <paths>`; emits `useOptionalIntl()` + `intl.formatMessage` (never `FormattedMessage`, which throws in tests without a provider), keeps sentences with simple computed values as one ICU message, and reports what it leaves for manual work (ternaries, rich text, non-component JSX). `npm run i18n:extract` now rebuilds `i18n/locales/en/app.json` as documented (it used to write back to `src/generated/i18n/extracted.json`).*
- [X] T053 [P] [US4] Migrate shell and shared components `web/src/components/**` (layout, sidebar, top bar, dialogs, toasts, empty states, validation messages); fill every `description`; add the paths to `MIGRATED_I18N_PATHS` in `web/eslint.config.js`; update affected tests *Done note: every message in `web/src/components/**` has a translator description (the codemod stubs are replaced); the last raw strings in `ConfigFields.tsx` (empty select options, password removal note) render through intl; `src/components/**` is in `MIGRATED_I18N_PATHS`. No test changed: the English is byte-identical.*
- [X] T054 [P] [US4] Migrate `web/src/routes/Login.tsx`, `Share.tsx`, `NotFound.tsx`, `Dashboard.tsx`, `Servers.tsx`, `Fleet` and `Clusters.tsx`/`Cluster.tsx`; add paths to `MIGRATED_I18N_PATHS`; update tests *Done note: every message in these routes has a translator description (the codemod stubs are replaced); the routes are in `MIGRATED_I18N_PATHS`. There is no separate `Fleet` route file (the fleet view lives in `Servers.tsx`). No test changed: the English is byte-identical.*
- [X] T055 [P] [US4] Migrate `web/src/routes/CreateServer.tsx` and `web/src/routes/ServerDetail.tsx` plus `web/src/routes/tabs/**` (console, files, logs, players, mods, backups, settings tabs); add paths; update tests *Done note: every message in these files has a translator description; the Backup now hints (`tabs/Backups.tsx`), the console connect and disconnect lines (`tabs/Console.tsx`) and the mod provider subtitle (`tabs/Mods.tsx`) render through intl; the files are in `MIGRATED_I18N_PATHS`. No test changed: the English is byte-identical.*
- [X] T056 [P] [US4] Migrate `web/src/routes/Modules.tsx`, `Backups.tsx`, `Users.tsx`, `AuditLog.tsx` (audit labels as `audit.*` ids derived from method + path; unknown combinations show raw method + path, FR-029), `AdminLogs.tsx`, `AdminSettings.tsx`, `ThemeSettings.tsx`; add paths; update tests *Done note: every message in these routes has a translator description; audit labels use `audit.verb.*`, `audit.noun.*`, `audit.action.*` and `audit.access.*` ids composed from method + path, and unknown routes show the verb and the last path segment as before, with the raw method + path in the row tooltip (FR-029); the install-ID reset error in `AdminSettings.tsx` renders through intl; the routes are in `MIGRATED_I18N_PATHS`. No test changed: the English is byte-identical.*
- [X] T057 [US4] Map operator condition reasons in the dashboard: `condition.<type>.<reason>` lookup with `status.conditionArgs[type]` args, fallback to the English `message`, in `web/src/lib/servers.ts` (or the server status component); tests *Done note: `conditionMessageText` / `useConditionMessage` in `web/src/lib/i18n/conditionMessage.ts` (next to `serverMessage.ts`) translate a condition when the active catalog has `condition.<type>.<reason>`, formatted with `status.conditionArgs[type]`; English locales, a missing entry, or an entry that needs arguments the operator did not record show the English `message`, so the English is unchanged. Used for the Progressing and Ready messages on ServerDetail, the Overview tab (Ready alert, tunnel endpoint) and the Networking address assignment (the English message is still parsed for the conflicting server and address). Tests: `conditionMessage.test.ts`.*
- [X] T058 [US4] Switch `MIGRATED_I18N_PATHS` to all of `src/**/*.{ts,tsx}` in `web/eslint.config.js` (FR-022, SC-001) and fix every remaining finding without suppressions *Done note: `MIGRATED_I18N_PATHS` is `src/**/*.{ts,tsx}`; test files and `src/test/**` (MSW handlers and fixtures) are exempt. `i18n-migrate --check src` reports nothing, so no code change was needed; the CI ESLint step confirms it.*
- [X] T059 [US4] Enable `--used-ids` unused-id reporting in the CI `i18n` job (FR-023) and delete unused reference entries it reports *Done note: the CI `i18n checks` step and `make check-i18n` build the used-id list from the extracted dashboard ids, the API registry ids and the operator condition ids (new `condition-ids -ids <file>` flag; CI now also runs `condition-ids -check`) and pass it as `i18n-check --used-ids`. A local run reported no unused reference entries, so none were deleted.*
- [X] T060 [US4] Document the developer workflow (write English + id + description, run `npm run i18n:extract`, commit `i18n/locales/en/app.json`) in `docs/translating.md` and `web/specs.md` *Done note: `docs/translating.md` has a developer section (intl pattern, id and description rules, extract and commit, codemod and `--check`, lint and CI, helpers for API, condition and module text); `web/specs.md` covers the lint scope, the unused-id check, condition and module text, audit labels and the workflow.*

**Checkpoint**: Zero bypassing literals; English reference complete.

---

## Phase 6: User Story 5 - Server messages and notifications in my language (Priority: P2)

**Goal**: API errors, operator condition reasons and notifications are localized; existing integrations unchanged; sign-in messages stay generic.

**Independent Test**: quickstart.md scenarios 7 and 8.

### Tests for User Story 5

- [X] T061 [P] [US5] Go API E2E `test/e2e/i18n_problem_json_e2e_test.go`: forbidden-namespace and validation errors return byte-identical text/plain bodies without the Accept header and problem bodies with `detail`, `messageId`, `args` with it; failed login returns only `server.auth.invalid_credentials` for both unknown user and wrong password (FR-030); register in `test/e2e/buckets.sh`
- [ ] T062 [P] [US5] Go API E2E `test/e2e/i18n_notify_language_e2e_test.go`: webhook sink with `language: "fr"` and one without; test send to both via `POST /api/v1/admin/notifications/sinks/{name}/test`; first payload has `language: "fr"` and French `title`/`text`, second uses instance default; all pre-existing payload fields unchanged; register in `test/e2e/buckets.sh`
- [X] T063 [P] [US5] envtest `operator/internal/controller/gameserver_envtest_test.go` additions: `status.conditionArgs` set with the condition and cleared when the reason no longer has args (done in the new file `gameserver_condition_args_envtest_test.go`, next to the condition-args code)

### Implementation for User Story 5

- [X] T064 [US5] Migrate the 193 direct `http.Error` calls in `api/internal/handlers/*.go`, `api/internal/auth/*.go` and `api/internal/ws/*.go` to `httperr.WriteMessage` with registry ids (English text unchanged), batched per file by a scripted transform; add each id to `api/internal/messages/messages.go`; run `make i18n-sync`
- [X] T065 [US5] Ensure unauthenticated paths (`api/internal/auth/local.go`, OIDC handlers, `MountPublicShares`) only emit `server.auth.invalid_credentials` / `server.generic.*` ids (FR-030, rule 3); tests in `api/internal/auth/local_test.go`
- [X] T066 [US5] Add `ConditionArgs map[string]map[string]string` (`json:"conditionArgs,omitempty"`) to `GameServerStatus` in `operator/api/v1alpha1/gameserver_types.go`; run `make generate && make manifests`; commit deepcopy, `operator/config/crd/*.yaml`, `charts/gameplane/crds/*.yaml` / `crd-manifests/` in the same commit (rule 7)
- [X] T067 [US5] Set `status.conditionArgs` alongside every operator-authored condition that interpolates values into its message in `operator/internal/controller/gameserver_controller.go` (and helpers), keeping the English `message` unchanged; update `operator/specs.md`
- [X] T068 [US5] Add `condition.<type>.<reason>` English entries for all operator reasons (66 literals in `operator/internal/controller/`) to `i18n/locales/en/app.json` via a generator `operator/hack/condition-ids.go` invoked by `make i18n-sync`, with descriptions
- [X] T069 [US5] Add mirrored `conditionArgs` to the GameServer type in `web/src/types.ts`
- [X] T070 [US5] Add a `Language` field (JSON `language,omitempty`) to `Sink` in `api/internal/notify/sinks.go`; accept and validate it in `validateNotifications` (`api/internal/handlers/config.go`, unknown tag → `server.i18n.unknown_language`)
- [X] T071 [US5] Localize `api/internal/notify/format.go`: `title`/`detail`/test copy rendered from `notify.*` entries via `i18n/catalog.Lookup` in sink language → instance default (config `i18n`) → `en`; webhook payload adds `language`, `title`, `text` and keeps existing fields; Kubernetes `Reason`/`Message` verbatim (FR-011); add the `notify.*` English entries with descriptions to `i18n/locales/en/app.json`; tests in `api/internal/notify/format_test.go`, `sinks_test.go`
- [X] T072 [US5] DESIGN (local machine, Pencil MCP): add a Language field to the notification destination form in `design.pen`; export touched nodes to `assets/design-export/`. *Done note (redrawn 2026-10-15): the Add sink form is the new screen `SUcad` with the Language select (default "Instance default"), and each sink row on `n6Xlo` has its own Language select through the new component `TSXYO` Gameplane/Sink Language Row (OD-015).*
- [X] T073 [US5] Implement the sink language field in the notifications section of `web/src/routes/AdminSettings.tsx` per T072 design (default "Instance default"); tests `AdminSettings_notifications.test.tsx` *Done note: Add sink form and each sink row have a Language select; "Instance default" sends no `language` key; a saved tag that is no longer listed is shown as its raw tag. With valgul's approval (2026-10-15) the General-section Save clicks in `AdminSettings.test.tsx` (11 lines) and `AdminSettings_sections.test.tsx` (2 lines) are scoped to the General card, because the Language card adds a second Save changes button.*
- [X] T074 [US5] Update `api/specs.md` and `docs/notifications.md` (per-destination language, webhook fields)

**Checkpoint**: French dashboard shows French server errors; French sink receives French notifications.

---

## Phase 7: User Story 3 - Translate in the browser on Hosted Weblate (Priority: P2)

**Goal**: Translators work on Hosted Weblate; approved work returns as PRs passing the same checks; new English text appears within 24 h.

**Independent Test**: quickstart.md scenario 13.

- [ ] T075 [US3] Apply for the Hosted Weblate libre plan and create project `gameplane` (maintainer account; record the project URL in `docs/translating.md` and `README.md`)
- [ ] T076 [US3] Create component `app`: repo `GameplanePanel/Gameplane`, file mask `i18n/locales/*/app.json`, monolingual base `i18n/locales/en/app.json`, format `formatjs`, push branch `weblate/translations` in GitHub pull request mode, review workflow on, machine suggestions never auto-approved; add the Weblate GitHub webhook (research R10)
- [ ] T077 [US3] Verify on the live instance whether Weblate imports `description` from the Format.JS base; if not, add CI job `weblate-explanations` in `.github/workflows/` that pushes descriptions as Weblate explanations through the Weblate API (token in a repository secret)
- [ ] T078 [US3] Verify Weblate preserves `sourceHash`; if it does not, move hashes to `i18n/locales/<tag>/.review.json` in `i18n/pack` and `i18n-check --stamp` (research R8) and update contracts/pack-format.md
- [ ] T079 [US3] Create per-module components in `GameplanePanel/module` with the Component discovery add-on (file mask `*/locales/*.json`, base `*/locales/en.json`)
- [ ] T080 [US3] Configure a French "Reviewers" group and document the reviewer role and approval rules in `docs/translating.md`
- [ ] T081 [US3] Trial: submit and approve one translation on Weblate, confirm a PR from `weblate/translations` arrives and passes the `i18n` CI job (US3 AS1–4)

---

## Phase 8: User Story 6 - Game modules in my language (Priority: P3)

**Goal**: Modules ship signed translations; the dashboard shows module text in the user's language with fallback; bad entries never block install.

**Independent Test**: quickstart.md scenarios 9 and 10.

### Tests for User Story 6

- [X] T082 [P] [US6] Go API E2E `test/e2e/i18n_module_locales_e2e_test.go`: push a test module with `locales/fr.json` (hold `ociPushMu`), install, `GET /api/v1/modules/{name}/locales/fr` returns entries; a second module with a bad placeholder installs, reports `LocalesValid=False` and a Warning event, and serves only valid entries; register in `test/e2e/buckets.sh`
- [X] T083 [P] [US6] envtest `operator/internal/controller/gametemplate_envtest_test.go` additions: ConfigMap `<template>-locales` owned by the GameTemplate, invalid entries dropped, condition and event set

### Implementation for User Story 6

- [X] T084 [US6] Implement module id derivation `gp-module/internal/i18n/ids.go` per contracts/module-locales.md §1 (reversible `~0`/`~1` escaping) (shared with the operator through `i18n/pack` helpers if needed) and command `gp-module i18n extract` writing `locales/en.json` with descriptions; tests
- [X] T085 [US6] Extend `gp-module validate` to run `i18n/pack` validation in module mode and `gp-module package` to add one layer per `locales/<tag>.json` (media type `application/vnd.gameplane.module.locale.v1+json`, title annotation `locales/<tag>.json`, `en.json` excluded) in `gp-module/internal/packager/packager.go`; update `gp-module/specs.md`; tests
- [X] T086 [US6] Add `MediaTypeLocale` and locale-layer extraction to `operator/internal/oci/bundle.go`; tests
- [X] T087 [US6] In the GameTemplate reconciler (`operator/internal/controller/`), validate locale layers against ids from the bundled template, write valid entries to ConfigMap `<template>-locales` (owner ref), set `LocalesValid` condition and emit a Warning event listing up to 20 problems; enforce the 900 KiB aggregate bound with reason `LocalesTooLarge` (contracts/module-locales.md §3); module install never fails on locales (FR-034); RBAC markers for ConfigMaps if missing, then `make manifests`; update `operator/specs.md`
- [X] T088 [US6] Add `GET /modules/{name}/locales/{tag}` in `api/internal/handlers/modules.go` (same RBAC as `GET /modules/{name}`, regional → base → `{}` resolution); client method in `web/src/lib/endpoints.ts`; handler tests
- [X] T089 [US6] Use module locale maps in `web/src/routes/CreateServer.tsx` and the server-settings tab (`web/src/routes/tabs/settings/**`) with fallback to template text (FR-032); tests *Done note: `useModuleText` / `useModuleTexts` and `moduleIds` in `web/src/lib/i18n/moduleText.ts` fetch `Modules.locales(name, tag, cluster)` (module name = template name) and format its ICU messages, with `~0`/`~1` id escaping as in `i18n/modlocale`; English fetches nothing and every missing id shows the template text. CreateServer translates the template cards (and search), version cards, configuration fields, Review row and preview title; `ConfigFields` takes an optional `text` accessor (labels, help text, enum choices; values unchanged); the settings tab passes `moduleText` to the Version and Game configuration sections. Hooks run only in components that already use react-query. Tests: `moduleText.test.tsx`, `ConfigFields_moduleText.test.tsx`.*
- [X] T090 [US6] [module repo] Push locale layers in `modules/build.sh` (`locales/*.json` except `en.json`) and run `i18n-check --module` in the module repo CI; update `modules/README.md`
- [X] T091 [US6] [module repo] Generate `locales/en.json` for `modules/minecraft-java/`, `modules/terraria/`, `modules/valheim/` with `gp-module i18n extract`; update each module's `specs.md`; open the module-repo PR; bump the root `modules` pointer only after it merges

---

## Phase 9: User Story 7 - Administrator sets the instance default language (Priority: P3)

**Goal**: Admin picks the instance default used for users without a choice, unauthenticated pages and sinks without a language.

**Independent Test**: quickstart.md scenario 4.

- [ ] T092 [P] [US7] Go API E2E `test/e2e/i18n_instance_default_e2e_test.go`: `PUT /api/v1/admin/config/i18n` `{defaultLanguage:"fr"}` → `GET /auth/locale` returns `fr`; unlisted tag → 400 `server.i18n.unlisted_language`; non-admin → 403; register in `test/e2e/buckets.sh`
- [X] T093 [US7] Add config section `i18n` with `validateI18n` (listed languages only) to the validators map in `api/internal/handlers/config.go`; register `server.i18n.unlisted_language`; tests `config_validators_test.go`
- [X] T094 [US7] DESIGN (local machine, Pencil MCP): add a Default language field to admin settings in `design.pen`; export touched nodes to `assets/design-export/`. *Done note (redrawn 2026-10-15): on `WZdnw` the General card and a new Language card sit in a Panel Column; the Language card has the Default language select and its own Save changes button, because the value is saved to the separate config section `i18n`.*
- [X] T095 [US7] Implement the Default language field in `web/src/routes/AdminSettings.tsx` per T094; tests `AdminSettings_sections.test.tsx` *Done note: Language card under General, saved to config section `i18n`; save errors go through `errorTextIntl`, so a 400 `server.i18n.unlisted_language` shows the catalog text.*

---

## Phase 10: Launch content (FR-037, FR-038, SC-002, SC-009)

**Purpose**: French at 100% and the pseudo-language pass. Depends on US4, US5, US6 English references being complete.

- [ ] T096 Create `i18n/locales/fr/meta.json` (`fr`, `Français`, `French`, `ltr`, credits)
- [ ] T097 Translate `i18n/locales/fr/app.json` to 100% (web, server, condition, notify, audit); drafts may be machine-assisted but every entry is approved by a native French reviewer on Weblate or in PR review; `i18n-check` shows fr 100% and listed
- [ ] T098 [module repo] Translate `modules/minecraft-java/locales/fr.json`, `modules/terraria/locales/fr.json`, `modules/valheim/locales/fr.json` to 100%; module-repo PR; root pointer bump after merge
- [X] T099 Implement pseudo-language `en-XA` generation (accented, ~40% longer, bracketed) behind `VITE_I18N_PSEUDO=1` in the `web/vite.config.ts` build step; never present in release `languages.json` (FR-038)
- [X] T100 Add an `en-XA` project to `web/playwright.config.ts` running the screenshot suite (`web/e2e/screenshots/`), and fix clipping/overlap found (truncation must reveal full text on hover/focus, FR-010); any visual change goes through `design.pen` first (local machine) *Done note: `npm run screenshots:pseudo` runs the screenshot suite as the Playwright `en-XA` project (`VITE_I18N_PSEUDO=1`). The specs keep driving the UI in English; `e2e/screenshots/capture.ts` switches the dashboard to `en-XA` for each capture through `window.gameplaneI18nTest` (installed by `I18nProvider` only when `en-XA` is listed), then soft-fails on text cut off without a `title` (OD-018, OD-019). The CI job "en-XA screenshots" in `visual-diff.yaml` uploads the captures and clipping reports. The first run found only data text already truncated in English (mobile Servers names and addresses, Overview endpoint hosts, module summaries, file names); each now has a `title` with the full text and nothing changes visually, so `design.pen` is unchanged (OD-020). The run is green (122 passed). Screenshots that specs take with `page.screenshot` directly stay English.*

---

## Phase 11: Polish & Cross-Cutting Concerns

- [X] T101 [P] Update `docs/architecture.md`, `docs/agent-architecture.md` (task → doc map entry for i18n) and `docs/module-authoring.md` (module translations section)
- [X] T102 [P] Add a CHANGELOG / release-notes entry describing language selection, French, problem+json negotiation (opt-in) and module translations
- [ ] T103 Run quickstart.md scenarios 1–13 against a `make dev-up` cluster and record results in the PR description
- [ ] T104 Confirm the coverage gates (`i18n` 90, api 80, operator 72, gp-module 80, web thresholds) pass on CI; add tests where they do not (never lower thresholds)
- [ ] T105 After merge and with every task complete or withdrawn: `git mv specs/019-localization-language-packs specs/done_019-localization-language-packs` and update in-repo references (Constitution IV, rule 16)

---

## Dependencies & Execution Order

### Phase dependencies

- Setup (Phase 1) → Foundational (Phase 2) → user stories.
- US1 (P1) and US2 (P1) start after Phase 2 and are independent of each other.
- US4 (string migration) depends on Phase 2; it does not need US1's UI, but the picker is only useful once strings are migrated.
- US5 depends on Phase 2 (T020–T024). Its UI task T073 waits on design T072.
- US3 depends on US2 (pack checks in CI) and needs at least the `en` reference from US4 to be meaningful.
- US6 depends on Phase 2 (`i18n/pack`); module-repo tasks T090/T091 depend on T085.
- US7 depends on T041 (`/auth/locale`) from US1.
- Launch content (Phase 10) depends on US4, US5 (T068, T071) and US6 (T091).
- French-dependent E2E tests need a listed French pack: T035, T037, T061, T062 and T092 are written in their story phases but are merged together with, or after, T096–T097 (French ≥ 80%). Until then they stay on the feature branch of their story and that story is not marked done. US1 and US2 checkpoints that do not need French (English-only picker, file contribution) are validated without them.
- Design tasks T043, T072, T094 and visual fixes in T100 run on valgul's local machine; cloud work proceeds on every non-UI task meanwhile.

### Within each story

Tests are written alongside implementation and must pass on CI before the story is done; models → services → endpoints → UI; commit each logical unit.

### Parallel opportunities

- Phase 1: T003–T006 in parallel.
- Phase 2: T007/T008 in parallel, then T009; T011–T013 then T014/T015; T020–T022 alongside the web runtime T025–T033.
- US4: migration waves T053–T056 in parallel (different directories), each a scripted run of T052's codemod.
- US5: T064 (API migration) parallel with T066–T069 (operator) and T070–T071 (notify).
- US6: gp-module (T084–T085) parallel with operator (T086–T087) and API (T088).
- All E2E test tasks marked [P] are independent files.

## Parallel Example: User Story 4

```text
Workflow parallel():
  agent(model: haiku) T053 migrate web/src/components/**
  agent(model: haiku) T054 migrate login/share/dashboard/servers/clusters routes
  agent(model: haiku) T055 migrate CreateServer, ServerDetail, tabs
  agent(model: haiku) T056 migrate modules/backups/users/audit/admin/theme routes
then one sonnet reviewer checks the combined git diff against T052's rules
```

## Implementation Strategy

### MVP (US1 + US2 on top of Phase 2)

1. Phases 1–2.
2. US1: picker, persistence, login/share picker (English-only catalog still works).
3. US2: file contributions with CI checks.
4. Validate quickstart 1–3, 5, 11 on CI and a dev cluster.

### Incremental delivery

1. US4 migration makes every screen translatable.
2. US5 localizes server errors and notifications.
3. US3 opens Weblate.
4. US6 module translations; US7 instance default.
5. Launch content: French 100% + pseudo-language pass → release.

Each increment is its own branch and PR (rule 12) with `type:`/`area:` labels (rule 14).
