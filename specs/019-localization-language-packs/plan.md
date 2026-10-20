# Implementation Plan: App Localization & Community Language Packs

**Branch**: `019-localization-language-packs` | **Date**: 2026-10-11 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/019-localization-language-packs/spec.md`

Settled decisions (valgul, 2026-10-10, re-confirmed in thread 2026-10-11): French pilot (OD-001), Hosted Weblate (OD-002), 80% listing threshold (OD-003), per-destination notification language (OD-004). No open decisions remain; see [OPEN-DECISIONS.md](./OPEN-DECISIONS.md).

## Summary

Gameplane gets one translation system shared by the dashboard, the API and game modules:

1. **One catalog, one format.** A new shared Go module `i18n/` holds the app's language packs (`i18n/locales/<tag>/app.json` in Format.JS JSON with ICU MessageFormat, plus `meta.json`), an ICU-subset formatter, the pack validator and the `i18n-check` CI tool. The dashboard imports the same files at build time; the API embeds them for notifications (research R1, R2).
2. **Dashboard.** `react-intl` (FormatJS) with build-time extraction and compilation; every literal moves to `intl.formatMessage`/`<FormattedMessage>` with a translator `description`; `Intl`-based formatting replaces the hard-coded `en-US`; lazy per-language chunks; `<html lang dir>` set by a no-flash boot snippet; HeroUI/React Aria get the same locale. A personal-settings language picker, login/share pickers, an admin default-language setting and a per-sink language field are added after they are designed in `design.pen` (Constitution II).
3. **Server messages.** Error responses keep today's English `text/plain` body; clients that send `Accept: application/problem+json` (the dashboard) get RFC 9457 problem details with `messageId` + `args` (R3). Operator condition reasons become ids, with arguments in a new additive `status.conditionArgs` (R4).
4. **Preferences.** Nullable `user_preferences.language` (migration 018), `PUT /api/v1/users/me/language`, admin config section `i18n` for the instance default, unauthenticated `GET /auth/locale` exposing only the default (R5, R6).
5. **Notifications.** Each sink gets an optional `language`; `notify` renders `notify.*` entries with the Go ICU formatter (R9).
6. **Modules.** `modules/<name>/locales/<tag>.json` shipped as signed OCI layers; operator validates and stores them in a `<template>-locales` ConfigMap; API serves them; dashboard falls back to the template's own text (R11).
7. **Contribution.** File PRs checked by `i18n-check` (+ ESLint formatjs rules for bypassing literals); Hosted Weblate components for the app and every module, pushing approved work as PRs from `weblate/translations` (R10, R12). Contributor guide `docs/translating.md`.
8. **Launch content.** French 100% for app + server messages and for minecraft-java, terraria, valheim; pseudo-language `en-XA` for maintainers only (R13).

## Technical Context

**Language/Version**: Go 1.26 (`go.work`), TypeScript 6 strict, React 19, Vite 8 (versions per `web/package.json`).

**Primary Dependencies**:
- New web: `react-intl`, `@formatjs/cli` (dev), `eslint-plugin-formatjs` (dev). Existing: `@react-aria/i18n`, HeroUI 3, TanStack Router/Query.
- New Go: `golang.org/x/text` (CLDR plural rules, language matching) in `i18n/` only; `i18n/` is otherwise stdlib.
- External service: Hosted Weblate (libre plan) — configuration only, no code dependency.

**Storage**: SQLite/PostgreSQL `user_preferences.language` (migration `api/internal/db/migrations/common/018_user_language.sql`, portable SQL); `config` table keys `i18n` (new) and `notifications` (existing JSON, new optional field); Kubernetes ConfigMap per GameTemplate for module locales; GameServer CRD status field `conditionArgs`; browser `localStorage['gameplane-language']`. Details: [data-model.md](./data-model.md).

**Testing**: Go unit (`i18n/*`, `api/internal/{httperr,messages,notify,db,handlers}`, `gp-module`), envtest (operator GameTemplate locales, GameServer `conditionArgs`; API config/notification handlers), Go API E2E in `test/e2e` registered in `test/e2e/buckets.sh`, Playwright (`web/e2e/specs/live/` + screenshot suite under `en-XA`), Vitest. All on CI (Constitution VI).

**Target Platform**: Evergreen desktop/mobile browsers; Linux containers on k3s / multi-node Kubernetes.

**Project Type**: Multi-component web application (Go operator + API + shared modules, React SPA, OCI module repo).

**Performance Goals**: language switch < 1 s with no reload (SC-005); each language chunk lazy-loaded, French app catalog expected ≤ ~150 KB uncompressed; zero extra API calls on login-page render beyond `GET /auth/locale`.

**Constraints**: backward-compatible API bodies (SC-010); login privacy (CLAUDE.md rule 3, FR-030); no suppressions (Constitution III); design-first (Constitution II); module install never fails due to translations (FR-034); `go:embed` stays inside the `i18n/` module.

**Scale/Scope**: ~110 dashboard `.tsx` files to migrate (est. 2,000–3,000 strings), ~570 API error call sites, 66 operator condition reasons, ~15 notification strings, 3 reference modules translated at launch (of ~33).

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

| Principle | Status | How this plan satisfies it |
|---|---|---|
| **I. E2E-Tested Delivery** | PASS | Go API E2E tests (problem+json negotiation with unchanged plain-text body, `PUT /users/me/language`, `GET /auth/locale` privacy, French test notification to a capture webhook, module locale layer → `GET /modules/{name}/locales/fr`, invalid locale still installs) registered in `test/e2e/buckets.sh` with `t.Parallel()`, unique names and `ociPushMu` for module pushes; Playwright live specs for switching, persistence, login/share pickers, 0% fallback; screenshot suite under `en-XA`. |
| **II. Design-First** | PASS | Language picker (personal settings), login/share pickers, admin default-language field, sink language field and any layout changes found by the pseudo-language pass are designed in `design.pen` via Pencil MCP with exports to `assets/design-export/` before React code. Design runs on valgul's local machine (project practice); cloud work covers every non-UI task first. |
| **III. Language & Ecosystem** | PASS | Strict TS; ESLint formatjs rules added as real rules, enabled per migrated directory then repo-wide (adding a rule is not loosening); Go errors `%w`; CRD change followed by `make generate && make manifests` in the same commit; new module gets `.testcoverage.yml` (target 90, like `telemetryschema`). |
| **IV. Spec-Driven** | PASS | spec → plan (this) → tasks before any work. New module `i18n/specs.md`; `web/specs.md`, `api/specs.md`, `operator/specs.md`, `gp-module/specs.md` and each translated `modules/<game>/` `specs.md` updated in the same changes that alter behaviour. |
| **V. Delegate to Workflows** | PASS | Implementation slices are independent (i18n module; API messages; preferences/config/notify; operator + CRD; module packaging; dashboard infra; per-route string migration waves; French content; Weblate/CI) and run through Workflow scripts with explicit `model` per agent, starting at haiku, tier-up review of diffs (CLAUDE.md rules 13, 17, 18). String-migration waves are scripted per directory. |
| **VI. CI Bears the Heavy Lifting** | PASS | Locally only `go build ./...` and `npx tsc --noEmit`; all lint/tests/E2E on GitHub Actions. |

*Post-design re-check (after Phase 1, 2026-10-11)*: All principles still pass. One new Go module (`i18n/`) is justified below; the CRD change is additive and follows rule 7.

## Project Structure

### Documentation (this feature)

```text
specs/019-localization-language-packs/
├── spec.md
├── OPEN-DECISIONS.md        # all four resolved 2026-10-10
├── plan.md                  # this file
├── research.md              # Phase 0: R1–R14
├── data-model.md            # Phase 1
├── quickstart.md            # Phase 1: validation scenarios
├── contracts/
│   ├── pack-format.md       # pack files, message rules, generated artifacts
│   ├── server-messages.md   # problem+json negotiation, Go API, condition ids
│   ├── i18n-api.md          # user language, instance default, /auth/locale, sink language
│   ├── module-locales.md    # module ids, OCI layers, operator, API
│   └── check-tool.md        # i18n-check flags and finding codes
├── checklists/requirements.md
└── tasks.md                 # Phase 2 (/speckit-tasks)
```

### Source Code (repository root)

```text
i18n/                                   # NEW Go module (go.work, GO_MODULES, Makefile, .testcoverage.yml, specs.md)
├── go.mod
├── locales/{en,fr}/{app.json,meta.json}
├── icu/                                # ICU subset parse/format + placeholder extraction
├── pack/                               # load, validate, completeness, ListingThreshold = 80
├── catalog/                            # go:embed locales; Lookup(tag, id, args)
├── cmd/i18n-check/                     # CI tool
└── testdata/defective/<code>/          # SC-007 fixtures

api/
├── internal/messages/                  # NEW id registry (server.*)
├── internal/httperr/httperr.go         # UPDATE: Message type, problem+json negotiation
├── internal/handlers/*.go              # UPDATE: direct http.Error → httperr.WriteMessage
├── internal/handlers/users.go          # UPDATE: PUT /me/language, language in GET /me/preferences
├── internal/handlers/config.go         # UPDATE: section i18n + validateI18n; validateNotifications accepts language
├── internal/handlers/auth_providers.go # UPDATE: GET /auth/locale
├── internal/handlers/modules.go        # UPDATE: GET /modules/{name}/locales/{tag}
├── internal/notify/{format,sinks}.go   # UPDATE: Sink.language, localized rendering
├── internal/db/preferences.go          # UPDATE: language column
├── internal/db/migrations/common/018_user_language.sql   # NEW
└── Dockerfile                          # UPDATE: COPY i18n/

operator/
├── api/v1alpha1/gameserver_types.go    # UPDATE: ConditionArgs (+ make generate manifests → config/, charts/)
├── internal/oci/bundle.go              # UPDATE: locale layer media type
└── internal/controller/                # UPDATE: GameTemplate locales ConfigMap + LocalesValid; GameServer conditionArgs

gp-module/                              # UPDATE: `i18n extract`, validate locales, package locale layers
modules/ (submodule GameplanePanel/module)
├── build.sh                            # UPDATE: push locales/*.json layers
├── {minecraft-java,terraria,valheim}/locales/{en,fr}.json   # NEW
└── CI: run i18n-check --module

web/
├── index.html                          # UPDATE: boot snippet sets lang/dir
├── vite.config.ts                      # UPDATE: @i18n alias, compile + manifest step
├── Dockerfile                          # UPDATE: COPY i18n/locales
├── eslint.config.js                    # UPDATE: formatjs plugin rules
├── src/lib/i18n/                       # NEW: provider, resolution, loaders, format helpers, server-message mapping
├── src/lib/api.ts                      # UPDATE: Accept header, problem parsing
├── src/routes/**, src/components/**    # UPDATE: string migration
├── src/routes/ThemeSettings.tsx (or settings language section), Login.tsx, Share.tsx, AdminSettings.tsx   # UPDATE: pickers/fields
└── e2e/specs/live/i18n.spec.ts         # NEW

test/e2e/i18n_*_e2e_test.go + buckets.sh   # NEW
docs/translating.md                         # NEW contributor guide (FR-019)
.github/workflows/ci.yaml                   # UPDATE: i18n-check job, extract drift, job summary
```

**Structure Decision**: Existing multi-component layout; the only new top-level directory is the shared `i18n/` Go module, following the `telemetryschema/` precedent so the dashboard build, API, operator, `gp-module` and module-repo CI share one implementation of the pack rules.

## Delivery order (for tasks)

1. `i18n/` module + `i18n-check` + fixtures (unblocks everything).
2. API: messages registry + problem+json; preferences/config/`/auth/locale`; notifications.
3. Operator + CRD `conditionArgs`; module locale layers (operator, gp-module, build.sh, API).
4. Dashboard infrastructure (provider, resolution, boot, API error mapping, formatting) behind English-only catalog.
5. Design in `design.pen` (local machine) → pickers/fields UI.
6. Dashboard string migration, directory by directory, with ESLint rule enabled per directory.
7. French content (app, server, notifications, 3 modules), pseudo-language pass and layout fixes. E2E tests that assert French (T035, T037, T061, T062, T092) merge with or after the French pack (T096–T097); see tasks.md Dependencies.
8. Hosted Weblate setup, contributor guide, E2E completion.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| New Go module `i18n/` (17th in `go.work`) | Same pack rules must run in the API (notifications), operator (module locales), `gp-module`, CI and the module repo; `go:embed` cannot read files outside its module | Putting catalogs in `web/` leaves the API unable to embed them; duplicating validators per component lets rules drift |
| Additive CRD field `GameServerStatus.ConditionArgs` | FR-025 needs parameters for operator-produced reasons; `metav1.Condition` is fixed | Parsing args from English messages is fragile; JSON in `Message` breaks `kubectl describe` |
