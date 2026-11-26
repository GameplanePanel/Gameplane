# Tasks: Theme marketplace

**Input**: Design documents from `specs/043-theme-marketplace/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/ (theme-package.md, themes-api.md, gp-module-theme-cli.md), quickstart.md. OD-1 to OD-4 settled by valgul on 2026-11-26; no decision is open.

**Tests**: Required. Constitution Principle I needs E2E coverage, and the coverage gates (api 80, operator 72, gp-module 80, web 92/76/82/92, new `themepkg` and `bundlefetch` 90) need unit, envtest and Vitest tests. Every test task adds **new** test functions or files. Editing a test so it matches this spec needs no sign-off, but every such edit gets a peer review from a fresh agent given only the test diff (weakened / deleted / only replaced); moving, deleting, weakening or bypassing tests needs valgul's written sign-off from T002 first. Tests run in CI only (Principle VI); locally only `go build ./...` and `npx tsc --noEmit`.

**Organization**: Setup and Foundational first, then user stories in spec priority order: US1, US2 (P1), US3, US4 (P2), US5 (P3).

**Delivery** (plan.md, stacked PRs, one branch each, all into `master` in this order): PR-A `feat/043-themepkg` (T005–T009), PR-B `refactor/043-bundlefetch` (T010–T014), PR-C `design/043-theme-marketplace` (T004), PR-D `feat/043-theme-api` (T015–T019 and every API task in the stories), PR-E `feat/043-gp-module-theme` (US5 CLI tasks), PR-F `feat/043-theme-web` (every web task), PR-G `test/043-theme-e2e` (E2E, docs, changelog), then the content PR in `GameplanePanel/themes` (T060–T063). Before every CI wait, check live that the PR still merges cleanly with `master` and fix conflicts at once; mark a PR ready as soon as it is green and reviewed. Implementation runs through `Workflow` scripts (Principle V): haiku agents with an explicit `model:`, one diff review per batch one tier up.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task).
- **[Story]**: US1 to US5 from spec.md. Setup, Foundational and Polish tasks carry none.
- Paths are in the Gameplane repository unless they start with `themes/`, which means the new `GameplanePanel/themes` repository.

---

## Phase 1: Setup

**Purpose**: branches, test sign-off, the one design pass every web task depends on.

- [ ] T001 Create branch `feat/043-themepkg` from fresh `master`; record the base SHA in its PR description. Later branches in **Delivery** are cut from the previous one when it is opened.
- [ ] T002 List every existing test this spec moves or changes (file, test name, why, before/after) in `specs/043-theme-marketplace/T002-test-signoff.md` and get valgul's written sign-off before touching any. Known: all `operator/internal/oci/*_test.go` and `operator/internal/verify/*_test.go` move to `bundlefetch/oci/` and `bundlefetch/verify/` (unchanged bodies, new package path); `operator/internal/modsrc/*_test.go` that import `operator/internal/oci` (`upload_locales_test.go`, `dir_locales_*_test.go`, `bundle_locales_test.go`, others found by `grep -l internal/oci operator/internal/modsrc/*_test.go`) change only their import path; `api/internal/handlers/users_customcss_security_test.go` keeps calling `sanitizeCustomCSS`, which stays as a one-line wrapper over `themepkg.SanitizeCSS` (no edit expected); `web/src/lib/useThemePreferences.test.ts`, `web/src/routes/ThemeSettings.test.tsx`, `web/src/lib/theme-export.test.ts` and `api/internal/handlers/users_preferences_test.go` if the new `installed` theme type changes any asserted shape. Write "none needed" with the reason for each file that needs no change.
- [ ] T003 Ask valgul to create the empty repository `GameplanePanel/themes` (sessions cannot create repositories, OD-3) and to add the Claude GitHub App to it; record the answer here. Blocks T060–T063 only.
- [ ] T004 Design in `design.fig` through OpenPencil (never edit or read the file directly), following `/mnt/project-files/notes/design-prompts/043-theme-marketplace.md`: Theme catalog (grid, search, tag and source filters, Installed / Update available / Low contrast / Signature not verified / Invalid / Source removed badges, unreachable-source banner), theme detail (light and dark previews, description, changelog, license, homepage, Install / Update / Remove / Preview, problems list for invalid entries), preview banner (Keep, Exit preview), Settings → Theme "Installed themes" section and the installed option in the preset picker, admin Settings → Theme sources (list with status, last sync, error, theme count; add/edit dialog mirroring the module source dialog; delete and refresh confirms), mobile variants of catalog and detail. Run `hack/design/fig-export.ts`, commit `assets/design-export/` with `design.fig` on `design/043-theme-marketplace`, and get valgul's sign-off before any web screen task (T029, T030, T031, T038, T044, T049).

---

## Phase 2: Foundational (blocks every story)

**Purpose**: the shared validator, the shared fetcher, and the storage and API skeleton every story builds on.

**⚠️ CRITICAL**: no user story work starts until this phase is complete.

### `themepkg` (PR-A)

- [ ] T005 Create stdlib-only module `themepkg/` (`go.mod` module `github.com/GameplanePanel/gameplane/themepkg`, `doc.go`, `specs.md`, `.testcoverage.yml` at 90, `system-deps.toml` copied from `telemetryschema/`); add `./themepkg` to `go.work`, `themepkg` to `GO_MODULES` in `Makefile`, the repo map and the coverage line in `CLAUDE.md`, and the module to the change-based test selection components (spec `done_023`, `contracts/suite-components.md` and the tool it names).
- [ ] T006 [P] `themepkg/manifest.go`: `Manifest` type and strict YAML decode of `theme.yaml` per `contracts/theme-package.md` (apiVersion `gameplane.local/theme/v1`, name DNS-1123 ≤ 63, semver without `+` build metadata, field lengths, tags rule, SPDX license string, https-only homepage, changelog ≤ 4 KiB, unknown keys rejected); `Problem{Rule, Detail, Line}` type; tests in `themepkg/manifest_test.go`.
- [ ] T007 [P] `themepkg/tokens.go`: the 14 token names (same order as `CUSTOM_THEME_TOKEN_NAMES` in `web/src/lib/theme-derivation.ts`), `#rrggbb` lowercase check, WCAG relative luminance and contrast ratio, `LowContrast(tokens)` over foreground/background, accent-foreground/accent, muted/surface at 4.5:1 and accent/surface at 3:1; `Derive(accent, surface, mode)` porting `theme-derivation.ts`; shared fixture `themepkg/testdata/derivation.json` (inputs → expected 14 tokens) read by `themepkg/tokens_test.go` and by a new Vitest `web/src/lib/theme-derivation.fixture.test.ts`.
- [ ] T008 [P] `themepkg/css.go`: move the rules of `sanitizeCustomCSS` from `api/internal/handlers/users.go:287-340` into `SanitizeCSS(css string) (Problem, bool)` with the same messages, plus line numbers and the `css.syntax` balance check; `api/internal/handlers/users.go` keeps `sanitizeCustomCSS` as a one-line wrapper (behaviour and messages unchanged); new tests in `themepkg/css_test.go` include every case of `users_customcss_security_test.go`.
- [ ] T009 [P] `themepkg/package.go`: `Validate(files map[string][]byte) (Result, []Problem)` applying every rule in `contracts/theme-package.md` (file set, per-file and total limits, preview PNG/WebP magic bytes and dimensions from headers only, preview present per mode with tokens) and returning sanitized CSS, tokens, metadata and the `LowContrast` flag; bad-package fixtures in `themepkg/testdata/bad/` (one per rule, SC-003) and tests in `themepkg/package_test.go`.

### `bundlefetch` (PR-B, needs T002 sign-off)

- [ ] T010 Create module `bundlefetch/` (`go.mod`, `specs.md`, `.testcoverage.yml` at 90, `system-deps.toml`), add it to `go.work`, `GO_MODULES`, `CLAUDE.md` repo map and coverage line, and the test selection components.
- [ ] T011 `git mv operator/internal/oci bundlefetch/oci` and `git mv operator/internal/verify bundlefetch/verify` with their tests; update package import paths only; move the needed `require` lines from `operator/go.mod` to `bundlefetch/go.mod`; `operator/go.mod` requires `bundlefetch` (replace via `go.work`).
- [ ] T012 Split the generic fetch half of `operator/internal/modsrc` (`git.go`, `http.go`, `local.go`, `dir.go`, the OCI listing in `oci.go`) into `bundlefetch/source/` returning `Files{Name, Version, Digest, Files map[string][]byte}` with no CRD types; `operator/internal/modsrc` keeps `Fetcher`, `ForSource`, `Bundle`, module metadata parsing and `ModuleEntry` building as a thin adapter. Module behaviour and messages stay identical.
- [ ] T013 Update imports in `operator/internal/modsrc/*_test.go` and `operator/internal/controller/` per the T002 sign-off (import path changes only), then run the test-diff peer review (fresh agent, test diff only) and record its verdict in `T002-test-signoff.md`.
- [ ] T014 Update `operator/specs.md` (modsrc now uses bundlefetch) and write `bundlefetch/specs.md` (packages, fetch contract, verify contract, netguard use at dial time).

### API skeleton (PR-D)

- [ ] T015 Migration `api/internal/db/migrations/common/028_theme_marketplace.sql` per `data-model.md`: tables `theme_sources`, `theme_catalog_entries`, `user_installed_themes`; `user_preferences.installed_theme_id` (nullable FK, ON DELETE SET NULL); `theme_type` accepts `installed`; portable SQL only (README rules). If 028 is taken when this lands, use the next free number.
- [ ] T016 [P] Store layer `api/internal/db/themes.go`: CRUD for the three tables with Go-bound timestamps; credential column encrypted with the panel key via the existing kubeconfig credential helper; tests `api/internal/db/themes_test.go` on SQLite and PostgreSQL like `preferences_test.go`.
- [ ] T017 [P] Permission `themes:manage` ("Manage theme sources", not namespaced) in `api/internal/rbac/catalog.go`, granted to the built-in admin role; route rules for `/themes/sources` in `api/internal/rbac/rbac.go`; new cases in a new `api/internal/rbac/themes_test.go`.
- [ ] T018 [P] Audit event names from `data-model.md` in `api/internal/audit/` (new constants only) with a new test.
- [ ] T019 Package `api/internal/themes/` with `Service` (store, bundlefetch, themepkg, clock); wire it in `api/cmd/main.go` (constructed in combined and standalone modes, no cluster dependency); mount an empty router under `/themes` and `/users/me/themes`.

**Checkpoint**: `go build ./...` for themepkg, bundlefetch, operator, api; CI green on PR-A and PR-B with operator module tests unchanged apart from T013 import paths.

---

## Phase 3: User Story 1 — Browse the catalog and try a theme (P1) 🎯 MVP

**Goal**: a signed-in user sees every valid theme from enabled sources, searches and filters, opens a detail view and previews a theme without saving.

**Independent test**: with one seeded source, the catalog lists its themes, search and tag filter narrow it, Preview switches the dashboard and Exit preview restores the saved theme exactly; with a second source down, the first still lists and the down one is flagged.

- [ ] T020 [US1] Indexer `api/internal/themes/indexer.go`: per enabled source on `refresh_interval_s` and on demand; list versions via `bundlefetch/source`, pull the latest version only when its digest changed, verify with `bundlefetch/verify` when `verify` is set, validate with `themepkg.Validate`, upsert `theme_catalog_entries` (`valid` / `invalid` / `unverified`, problems, low_contrast); one source failing sets only its `last_error`; dial through `netguard` like module sources.
- [ ] T021 [P] [US1] Seed migration-time `official` source (`type=oci`, `ghcr.io/gameplanepanel/gameplane-themes`, keyless verify pinned to the `GameplanePanel/themes` release workflow identity) in `api/internal/themes/seed.go`, skipped when env `GAMEPLANE_THEMES_OFFICIAL_SOURCE=false`; Helm value `themes.officialSource.enabled` (default true) wired to that env in `charts/gameplane/values.yaml`, `values.schema.json` and the API Deployment template.
- [ ] T022 [US1] Catalog handlers `api/internal/handlers/themes_catalog.go`: `GET /themes/catalog` (q, tag, source filters; hidden entries of disabled sources; unreachable sources list), `GET /themes/catalog/{id}` (tokens and css only for `valid`), `GET /themes/catalog/{id}/preview/{mode}` (stored content type, `nosniff`, private cache) per `contracts/themes-api.md`; client methods in `web/src/lib/endpoints.ts` and types in `web/src/types.ts`.
- [ ] T023 [P] [US1] Envtest `api/internal/handlers/themes_catalog_envtest_test.go`: fake OCI registry with one valid, one invalid and one unsigned theme; filters; unreachable second source still lists the first (SC-006); preview headers; signed-in non-admin can read.
- [ ] T024 [P] [US1] Unit tests `api/internal/themes/indexer_test.go`: unchanged digest skipped, changed digest re-pulled, per-source failure isolation, refresh interval floor.
- [ ] T025 [US1] i18n strings for catalog, detail and preview in `i18n/locales/` (English source pack; other packs follow the spec 019 flow).
- [ ] T026 [US1] Preview state in `web/src/lib/useThemePreview.ts`: apply catalog tokens and css in memory through the same injection path as `useThemePreferences.ts`, restore saved preferences on Exit, route change away from theme pages, or reload; nothing written server-side.
- [ ] T027 [P] [US1] Vitest `web/src/lib/useThemePreview.test.ts`: apply, exit restores exactly, reload restores, safe mode blocks preview CSS.
- [ ] T028 [US1] Routes `/settings/theme/catalog` and `/settings/theme/catalog/$id` registered in `web/src/router/tree.tsx`.
- [ ] T029 [US1] `web/src/routes/ThemeCatalog.tsx` from the T004 export: grid, search, tag and source filters, badges, unreachable-source banner, empty and loading states.
- [ ] T030 [US1] `web/src/routes/ThemeDetail.tsx` from the T004 export: previews, metadata, changelog, problems list, Preview button (Install/Update/Remove wired in US2/US3).
- [ ] T031 [US1] Preview banner component `web/src/components/ThemePreviewBanner.tsx` (Keep is wired in US2; until then only Exit).
- [ ] T032 [P] [US1] Vitest `web/src/routes/ThemeCatalog.test.tsx` and `ThemeDetail.test.tsx`: filters, badges, preview flow, unreachable banner.

**Checkpoint**: US1 works on its own: browse and preview with no install.

---

## Phase 4: User Story 2 — Install a theme and use it (P1)

**Goal**: a user installs a catalog theme for their own account and selects it in Settings → Theme like a preset.

**Independent test**: user A installs and selects a theme, it follows A to another browser and switches with Light/Dark/System; A's own CSS overlay stays on top; user B sees nothing new; login and share pages stay Modern Pink.

- [ ] T033 [US2] Install service `api/internal/themes/install.go`: copy the cached `valid` entry (tokens, re-sanitized css, metadata, version, digest, `source_id`, `source_name`) into `user_installed_themes`; refuse `already_installed`, `entry_not_valid`, `source_disabled`; max 50 installs per user; audit `theme.install`.
- [ ] T034 [US2] Handlers `api/internal/handlers/themes_installed.go`: `GET /users/me/themes`, `POST /users/me/themes` (optional `select`), `GET /users/me/themes/{id}`; another user's id → 404; derived `updateAvailable` and `sourceRemoved` by `source_id`; client methods in `web/src/lib/endpoints.ts`.
- [ ] T035 [US2] Preferences: `PUT /users/me/preferences` accepts `themeType: "installed"` + `installedThemeId` (422 if not the caller's) in `api/internal/handlers/users.go`; profile preferences gain `installedThemeId` and `installedTheme {tokens, css, version}`; `api/internal/db/preferences.go` reads and writes the new column.
- [ ] T036 [P] [US2] Envtest `api/internal/handlers/themes_installed_envtest_test.go`: install, select, isolation between two users, 404 on another user's id, limit, refusal codes, audit row, profile carries the rendered theme.
- [ ] T037 [US2] Render installed themes in `web/src/lib/useThemePreferences.ts`: tokens for the active mode into `gameplane-custom-theme-vars`, theme css into new `<style id="gameplane-installed-theme-css">` placed before the user overlay; single-mode themes used for both modes; safe mode skips both and renders Modern Pink; `web/src/lib/enforceUnauthenticatedTheme.ts` also removes the new style element.
- [ ] T038 [US2] Settings → Theme in `web/src/routes/ThemeSettings.tsx`: "Installed themes" section and the installed option in the preset picker from the T004 export, "Browse themes" link to the catalog; Install and Keep wired in `ThemeDetail.tsx` and `ThemePreviewBanner.tsx`.
- [ ] T039 [US2] `web/src/lib/theme-export.ts`: an installed theme exports as `{source, name, version}`; import onto an instance without it falls back to Pink with a notice.
- [ ] T040 [P] [US2] Vitest: new `web/src/lib/useThemePreferences.installed.test.ts` (render order, modes, safe mode, unauthenticated removal) and `web/src/routes/ThemeSettings.installed.test.tsx`; any edit to existing theme tests follows T002.

**Checkpoint**: US1 + US2 = the MVP marketplace (browse, preview, install, use).

---

## Phase 5: User Story 3 — Update and remove installed themes (P2)

**Goal**: per-user updates when a newer valid version appears; removal falls back to Modern Pink.

**Independent test**: version 1 installed, version 2 published, "Update available" appears for that user only, update applies 2 for that user only; disable the source and the installed copy keeps working as "Source removed"; remove the selected theme and the user is on Modern Pink.

- [ ] T041 [US3] `api/internal/themes/install.go`: `Update` moves to the newer `valid` entry with the same `source_id` and name (409 `no_update`, `source_disabled`, `source_removed`); `Remove` deletes the row and, if selected, sets `theme_type=preset`, `preset_id=pink`, `installed_theme_id=NULL` in the same transaction; audit `theme.update` and `theme.remove` with from/to versions.
- [ ] T042 [US3] Handlers `POST /users/me/themes/{id}/update` and `DELETE /users/me/themes/{id}` in `api/internal/handlers/themes_installed.go`; client methods.
- [ ] T043 [P] [US3] Envtest `api/internal/handlers/themes_update_envtest_test.go`: two users on different versions, update affects one; remove selected → Pink; source deleted and recreated with the same name does not offer updates to old installs; disabled source refuses update.
- [ ] T044 [US3] Web: Update and Remove buttons in `ThemeDetail.tsx` and the Installed themes section, "Update available" with changelog, "Source removed" state with placeholder preview, one-time notice after a removal fallback.
- [ ] T045 [P] [US3] Vitest `web/src/routes/ThemeSettings.update.test.tsx`: update, remove fallback, source-removed state.

---

## Phase 6: User Story 4 — Add and manage theme sources (P2)

**Goal**: admins with `themes:manage` add, edit, enable, disable, refresh and delete sources; signature policy is enforced.

**Independent test**: add a second source and see its theme; require a key and see "Signature not verified" until signed; disable it and its themes leave the catalog while installed copies keep working.

- [ ] T046 [US4] Source service `api/internal/themes/sources.go`: validation mirroring the `ModuleSource` CEL rules (type-specific spec, verify only for oci, interval ≥ 5m, name rules, `official` reserved); editing `verify` re-checks every entry of that source at once (failures → `unverified`); delete cascades catalog entries and nulls `user_installed_themes.source_id`; audit `theme_source.*`.
- [ ] T047 [US4] Handlers `api/internal/handlers/theme_sources.go`: `GET/POST /themes/sources`, `PUT/DELETE /themes/sources/{name}`, `POST /themes/sources/{name}/refresh` per `contracts/themes-api.md`; credentials never returned; client methods.
- [ ] T048 [P] [US4] Envtest `api/internal/handlers/theme_sources_envtest_test.go`: RBAC (non-admin 403), validation errors, verify edit re-check, disable hides entries and refuses installs, delete keeps installed copies, credential never in responses.
- [ ] T049 [US4] `web/src/routes/ThemeSources.tsx` at `/settings/theme-sources` from the T004 export (list, add/edit dialog, refresh, delete confirm), shown in the settings nav only with `themes:manage`; route in `web/src/router/tree.tsx`.
- [ ] T050 [P] [US4] Vitest `web/src/routes/ThemeSources.test.tsx`.

---

## Phase 7: User Story 5 — Publish a theme (P3)

**Goal**: authors scaffold, convert, preview, validate and package themes with `gp-module theme`.

**Independent test**: a spec 016 export becomes a package, previews locally, validates, pushes to a test registry and installs from the catalog.

- [ ] T051 [US5] `gp-module/internal/theme/` and the `theme` command group in `gp-module/cmd/gp-module/`: `init`, `from-export` (uses `themepkg.Derive`), `validate` (`--json`), `package` (`--out` OCI layout or `--push` with Docker config credentials, artifact and media types from `contracts/theme-package.md`), exit codes per `contracts/gp-module-theme-cli.md`.
- [ ] T052 [US5] `gp-module theme preview` in `gp-module/internal/theme/preview.go`: embedded static sample page (buttons, navigation, cards, table, form, alerts) served on `--port`, light/dark switch, theme tokens and css applied; validation first.
- [ ] T053 [P] [US5] Tests `gp-module/internal/theme/*_test.go`: each command, bad fixtures from `themepkg/testdata/bad/`, round trip export → package → validate.
- [ ] T054 [US5] Update `gp-module/specs.md` and add a "Theme packages" section to `docs/module-authoring.md` or a new `docs/themes.md` linked from it.

---

## Phase 8: E2E, docs and changelog (PR-G)

- [ ] T055 New bucket `themes` in `test/e2e/buckets.sh` and `test/e2e/api_themes_e2e_test.go` (`t.Parallel()`, unique names, `ociPushMu` around pushes, ≤ 7 logins): push a signed and an unsigned theme to the Kind registry, add a source, catalog lists both with the unsigned one flagged after enabling verify, user A installs and selects, user B unaffected, update, disable source (installed copy keeps working), remove (fallback to Pink).
- [ ] T056 [P] Standalone case in `test/e2e/standalone_panel_e2e_test.go` (new test function): add a local or OCI theme source and install a theme with no operator (OD-4).
- [ ] T057 [P] Update `api/specs.md` (themes service, routes, indexer, tables) and `web/specs.md` (catalog, preview, installed render order, safe mode).
- [ ] T058 [P] `changelog/unreleased/<PR>-theme-marketplace.md` per `changelog/unreleased/README.md` (feature, new Helm value `themes.officialSource.enabled`, new permission `themes:manage`).
- [ ] T059 Run `quickstart.md` sections 1–6 on a dev cluster and a standalone panel (valgul's manual walk); record results in `specs/043-theme-marketplace/quickstart-results.md`.

---

## Phase 9: Official themes repository (`GameplanePanel/themes`, after T003)

- [ ] T060 Scaffold `themes/` like `GameplanePanel/module`: `README.md`, `CLAUDE.md`, `AGENTS.md`, `themes/<name>/{theme.yaml,theme.css,previews/}` layout, `build.sh` (`gp-module theme validate` then `gp-module theme package --push ghcr.io/gameplanepanel/gameplane-themes/<name>:<version>`), CI workflow that validates every theme on PRs.
- [ ] T061 Release workflow `themes/.github/workflows/release.yaml`: on tag, push changed themes and keyless-sign each with cosign (GitHub OIDC); the workflow identity must match the one pinned in T021.
- [ ] T062 Seed themes from `example/custom-css/*.css` on branch `feat/example-theme-gallery`: run each through `gp-module theme validate`, keep those that pass, write `theme.yaml` with tokens taken from each overlay's CSS variables, capture light and dark previews with a Playwright script against the dev cluster (`themes/hack/capture-previews.ts`); at least 10 themes (SC-002).
- [ ] T063 First release tag in `themes`; confirm the `official` source on a fresh install lists every seeded theme as `valid` and signed.

---

## Dependencies & execution order

- Setup: T001 first; T002 before T011–T013 and before any existing-test edit; T003 before Phase 9; T004 before every web task.
- Foundational: T005 → T006–T009 [P]; T010 → T011 → T012 → T013 → T014; T015 → T016–T018 [P] → T019. PR-B needs PR-A merged only for `go.work` ordering, not code.
- Stories: US1 (T020–T032) needs Phase 2. US2 needs US1's catalog (T022). US3 and US4 need US2's install service (T033) but not each other. US5 needs only `themepkg` (T005–T009) and can run beside US1–US4.
- E2E (T055–T056) needs US1–US4 API tasks; T059 needs everything; Phase 9 needs T003, T051 and T021.

## Parallel examples

- Phase 2: T006, T007, T008, T009 in one Workflow `parallel()` (separate files), then one sonnet diff review.
- US1: T023, T024 (API tests) with T026–T027 (web preview hook) once T022 lands.
- US3 and US4 API tasks (T041–T043, T046–T048) in parallel branches of one Workflow.

## Implementation strategy

1. MVP = Phase 1–2 + US1 + US2: users browse, preview, install and use themes from the official source.
2. Then US3 (updates, removal) and US4 (admin sources), then US5 (authoring CLI), then E2E/docs, then the official themes repository goes live.
3. Each PR keeps master green within 2 hours (master CI rule); stacked PRs are retargeted by re-selecting the base after the previous one merges.
