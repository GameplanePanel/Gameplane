# Implementation Plan: Theme marketplace

**Branch**: `spec-043/theme-marketplace` | **Date**: 2026-11-26 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/043-theme-marketplace/spec.md`

**Open decisions**: OD-1 to OD-3 are settled (each user installs for themselves; colors + sanitized CSS + previews; signed OCI from a new `GameplanePanel/themes` repo). Planning raised **OD-4** (where theme sources live and who fetches them): settled A, panel database with fetch code shared through `bundlefetch`. No decision is open. See [OPEN-DECISIONS.md](OPEN-DECISIONS.md).

## Summary

Admins add theme sources to the panel (OCI, git, HTTP or local, with optional cosign verification), and the official `GameplanePanel/themes` source is seeded by default. The API indexes each source on a schedule, validates every theme's latest version with a new shared `themepkg` module (spec 016's CSS sanitizer moves there, plus token, contrast, preview and size rules), and caches it in the database, so the catalog never waits on a registry. Any signed-in user browses the catalog, previews a theme in memory, and installs it as a snapshot on their own account; installed themes are selected in Settings → Theme like presets and render through spec 016's token path, with the user's own CSS overlay still last. Updates are per user and explicit; installed copies survive source removal; safe mode skips theme CSS. Fetch and signature code is shared with module sources through a new `bundlefetch` module moved out of the operator (OD-4 A). `gp-module` gains `theme` commands, including turning a spec 016 export into a package. Details: [research.md](research.md), [data-model.md](data-model.md), [contracts/](contracts/), manual walk: [quickstart.md](quickstart.md).

## Technical Context

**Language/Version**: Go (versions from `.devcontainer/versions.toml`) for `themepkg/`, `bundlefetch/`, api, operator, gp-module; TypeScript strict for web; Helm 3 templates.

**Primary Dependencies**: existing chi, oras-go v2, sigstore-go / cosign v3 (moved with `bundlefetch` from the operator; the API gains them as dependencies), `golang.org/x/mod/semver`, `golang.org/x/image/webp` for preview headers (or stdlib `image/png` + a header parser; decided in tasks by govulncheck/size). React 19, TanStack Router/Query, HeroUI v3.

**Storage**: SQLite / PostgreSQL through the API store: one portable migration `common/028_theme_marketplace.sql` (3 tables, 2 columns on `user_preferences`). Source credentials encrypted with the panel key. No CRD change under OD-4 A.

**Testing**: CI only (rule 8). `themepkg` and `bundlefetch` unit tests; existing operator module envtests unchanged as the regression net for the move; API envtests (sources, indexing with a fake registry, install/update/remove, preferences, RBAC, audit); web Vitest; `gp-module` tests; E2E bucket `themes` plus a standalone case (R11).

**Target Platform**: panel on Kubernetes (combined or panel-only profile) and standalone panel (spec 025).

**Project Type**: multi-repo: Gameplane (new shared modules, api, operator refactor, web, gp-module, chart, E2E) plus the new `GameplanePanel/themes` content repository (OD-3).

**Performance Goals**: SC-006 catalog under 3 s with a source down: catalog reads hit only the database. Indexing pulls only changed digests.

**Constraints**: CSS rules identical to spec 016 (one implementation); unauthenticated pages stay Modern Pink (rule 3, spec 016 FR-011); source dials go through `netguard`; no suppressions (rule 4); design-first (rule 1).

**Scale/Scope**: tens of sources, hundreds of themes per source, per-user installs (a user may install up to 50 themes; limit enforced by the API).

## Constitution Check

*GATE: checked before research and again after Phase 1.*

| Principle | Status |
|---|---|
| I. E2E-tested delivery | PASS (planned): bucket `themes` registered in `test/e2e/buckets.sh`, `t.Parallel()`, unique names, `ociPushMu` around pushes, ≤ 7 logins per bucket; standalone case in the `standalone` bucket. |
| II. Design-first | PASS (planned): catalog, detail, preview banner, installed list, theme sources screens designed in `design.fig` and exported before React (R10). Design prompt for the local agent goes to `/mnt/project-files/notes/design-prompts/` with the tasks. |
| III. Ecosystem best practice | PASS: `%w` wrapping, strict TS, no suppressions; the code move keeps lint clean without new exclusions. |
| IV. Spec-driven + `specs.md` | PASS (planned): new `themepkg/specs.md`, `bundlefetch/specs.md`; updates to `api/specs.md`, `operator/specs.md` (modsrc now uses bundlefetch), `web/specs.md`, `gp-module/specs.md`. |
| V. Delegate to workflows | PASS (planned): implementation through Workflow scripts starting at haiku, reviews one tier up (rule 13). |
| VI. CI bears the heavy lifting | PASS: local compile checks only. |

New Go modules (`themepkg`, `bundlefetch`) need entries in `go.work`, `GO_MODULES` in the Makefile, the CLAUDE.md repo map and coverage minimums (proposed 90 each, like other shared libraries), `.testcoverage.yml` and the change-based test selection map (spec 023). Re-checked after Phase 1: no violations; nothing for Complexity Tracking.

## Project Structure

### Documentation (this feature)

```text
specs/043-theme-marketplace/
├── spec.md
├── OPEN-DECISIONS.md
├── plan.md              # this file
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── theme-package.md
│   ├── themes-api.md
│   └── gp-module-theme-cli.md
├── checklists/requirements.md
└── tasks.md             # /speckit-tasks
```

### Source Code (repository root)

```text
themepkg/                      NEW module (stdlib + gopkg.in/yaml.v3 for the strict theme.yaml decode): schema, tokens, derivation port, SanitizeCSS, contrast, previews, limits; specs.md
bundlefetch/                   NEW module (OD-4 A): oci client, git/http/local fetch, cosign verify (moved from operator/internal/{oci,verify} and the fetch half of modsrc); specs.md
operator/internal/modsrc/      thin adapter over bundlefetch (module.yaml → ModuleEntry); behaviour unchanged
api/internal/themes/           NEW: source store, indexer (scheduler + on-demand), catalog queries, install/update/remove
api/internal/handlers/themes.go, theme_sources.go   NEW routes (contracts/themes-api.md)
api/internal/handlers/users.go sanitizeCustomCSS → themepkg.SanitizeCSS; preferences accept themeType=installed
api/internal/db/migrations/common/028_theme_marketplace.sql
api/internal/rbac/catalog.go   + themes:manage
api/cmd/main.go                mount routes, start indexer, seed official source
gp-module/internal/theme/      NEW theme commands; cmd wiring
web/src/routes/ThemeCatalog.tsx, ThemeDetail.tsx, ThemeSources.tsx   NEW (after design)
web/src/routes/ThemeSettings.tsx  + Installed themes section, installed option
web/src/lib/useThemePreferences.ts, enforceUnauthenticatedTheme.ts, theme-export.ts  installed theme render, safe mode, export
web/src/router/tree.tsx, web/src/lib/endpoints.ts, web/src/types.ts
i18n/ packs                    new strings
charts/gameplane/              themes.officialSource.enabled value + API env
test/e2e/api_themes_e2e_test.go, test/e2e/buckets.sh
design.fig + assets/design-export/   new screens (local agent)
docs/themes.md                 NEW how-to (sources, authoring); website docs page via the website refresh
changelog/unreleased/<PR>-theme-marketplace.md
GameplanePanel/themes          NEW repo (valgul creates): themes/<name>/, build.sh, release workflow (cosign, project key), seeded from feat/example-theme-gallery overlays
```

**Structure Decision**: Theme marketplace lives in the API and web (panel data, OD-1 and OD-4 A); validation is one shared module so CLI and panel agree; fetch/verify is shared with module sources rather than duplicated.

### Suggested PR order (stacked, one thread = one branch per PR)

1. `themepkg` module + move user CSS sanitizer to it (no behaviour change).
2. `bundlefetch` extraction; operator `modsrc` uses it (no behaviour change; module envtests are the guard).
3. API: migration, sources, indexer, catalog, installs, preferences, RBAC, audit, chart value, envtests.
4. `gp-module theme` commands.
5. Design (`design.fig` + export), then web screens.
6. E2E bucket `themes` + standalone case; docs; changelog.
7. `GameplanePanel/themes` repo content and release workflow (after valgul creates the repo); official source goes live.

## Complexity Tracking

No constitution violations to justify.
