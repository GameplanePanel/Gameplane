# Research: Split big packages and files

Measured on master `8a67f2e` (2026-10-20) unless stated. Decisions reference
[OPEN-DECISIONS.md](./OPEN-DECISIONS.md).

## Re-measure (T003, master `54d5819`, 2026-10-22)

- Production files over 500 lines (FR-018 exemptions applied): 30 Go, 18 web. The 30 Go
  files are exactly the ones FR-018, R3, R4 and R7 name (the earlier "34" over-counted).
  The 18 web files are the 17 in R6 plus `web/scripts/i18n-migrate.ts` (1,195), a spec 019
  dev codemod outside `web/src/` that no FR names; OD-11 (2026-10-22): split it too (T061).
- `api/internal/handlers`: 146 `.go` files, 48 production. `operator/internal/controller`:
  150 `.go` files, 41 production.
- Spec 027's `docs/layout.md` merged via PR #795 (2026-10-22); every brief cites it (T002).

## R1. What a "move" may change (FR-002, FR-005, OD-6)

- **Decision**: a PR may contain only these change kinds. Production files: `git mv`;
  moving whole declarations (func, method, type, var/const block) between files; package
  clause and import edits; exporting an identifier that crosses a new package line
  (`foo` → `Foo`, every call site updated mechanically); path references in configs and
  docs. Test files (OD-6): package clause, import lines, qualified names
  (`foo` → `pkg.Foo`), relative import and `vi.mock` path strings, one new `TestMain` per
  new Go sub-package that copies the existing setup (relative CRD paths per OD-9).
- **Rationale**: these are the edits a compiler forces. Anything else is a behaviour
  risk and goes out of scope.
- **Alternatives**: allow "small cleanups while moving" (rejected: mixes review of a
  move with review of logic; FR-002).

## R2. Proving nothing changed (FR-006, FR-008..FR-011)

Four checks, all from git and CI, no local test runs (CLAUDE.md rule 8):

1. **Full CI run** (`ci: full-run` label, 023 OD-7) on both architectures. Needed because
   spec 023's changed-lines gate would see every moved line as added; files that were
   already below the module minimum (e.g. `ws/attach.go`, `events.go` in api) would fail it
   with no real change. The full run keeps the module-wide gate (`make cover-go-check`,
   Vitest thresholds) exactly as today.
2. **Test-name parity**: `hack/move_parity.py names <base> <head>` lists, by static scan,
   every `func Test*/Fuzz*/Example*` (Go, with package import path stripped to the
   module) and every Vitest/Playwright `describe`/`it`/`test` title chain, before and
   after. The lists must be equal. Static scan is used because `go test -list` is a test
   run and is CI-only.
3. **Test-edit check**: `hack/move_parity.py test-edits <base> <head> --brief <brief>`
   pairs each changed test file with its source (git rename detection plus the brief's
   move list), deletes the allowed line kinds from both sides (R1), applies the brief's
   identifier map, and fails if anything else differs. It also checks a new
   `suite_envtest_test.go` against the original setup.
4. **Generated outputs**: in CI, `make generate && make manifests` then
   `git diff --exit-code` (already part of the lint job), `api/docs/openapi.yaml`
   unchanged (it is hand-maintained and must not appear in the diff), and the visual-diff
   and screenshot jobs green for web PRs.

- **Alternatives**: a mutation or golden-HTTP run (rejected: new test infrastructure,
  out of scope); coverage diff per file (rejected: coverage moves with the code and the
  module gate already covers it).

## R3. API handler groups (OD-2: by resource)

Production files today (lines). Proposed groups; the per-PR scout confirms each list
against the call graph and records any file it reassigns, with the reason, in its brief.

| Sub-package `api/internal/handlers/<group>/` | Files | Lines |
|---|---|---|
| `capture` | capture (1,569, split), capture_remote | 1,759 |
| `access` | users (957, split), roles, binding_permissions, auth_providers, auth_provider_secret, oidc_routes, ownership | 2,051 |
| `shares` | shares (617, split) | 617 |
| `clusters` | cluster (517, split), cluster_actions, clusters, clusters_gateway, clusters_kubeconfig, namespaces, fleet (780, split) | 2,535 |
| `modules` | modules, modules_builder, modules_cluster, modules_locales, module_sources, module_upload, registry, registry_secret | 2,467 |
| `mods` | mod_ids, mod_target, mod_updates | 705 |
| `servers` | lifecycle, lifecycle_suspend, server_annotations, server_capabilities, server_identity, events, pod_events, resources (759, split), tunnelcreds (523, split) | 2,152 |
| `settings` | config (855, split), config_redact, notifications, destinations, secrets_managed, telemetry, installation, systemlogs, audit | 2,434 |
| shared helper `api/internal/handlers/httpx` (name confirmed by the first scout) | semver, validate, and the JSON/error/request helpers that several groups call | — |

`api/cmd/main.go` (914) mounts every handler; it is split into files of `package main`
(for example `routes_*.go` per group) when the api phase ends, with the same mount order.

## R4. Operator controller groups (OD-2: by resource)

| Sub-package `operator/internal/controller/<group>/` | Files | Lines |
|---|---|---|
| `gameserver` | gameserver_* (controller 2,740 and status 1,446 split; tunnel 747 split), agent_certs, agent_rbac, tunnel_rbac | 8,463 |
| `backup` | backup_controller (1,105, split), backup_forget, backup_guard, backup_volumesnapshot, backupschedule_controller, restore_controller, restore_lifecycle, restore_volumesnapshot (794, split), retention, restic_summary | 4,269 |
| `networkcapture` | networkcapture_controller (1,138, split), networkcapture_filter | 1,203 |
| `module` | module_controller (630, split), module_locales (502, split), modulesource_controller, gametemplate_controller | 1,399 |
| `cluster` | cluster_controller | 176 |
| shared helper (name confirmed by the first scout) | helpers, metrics, conflict, owned_delete, semver | 387 |

Known coupling to check first: `backup_guard`, `gameserver_wipe_guard` and `conflict`
implement cross-operation exclusion (audit issue #718), so backup and gameserver call
each other. Per the spec edge case, the shared part moves to the helper package; a cut
that would still cycle stays in the parent package and is recorded here.

`.golangci.yml` exempts `(^|/)internal/controller/` from revive `exported:`; the regex
already matches sub-folders, so no config change. Kubebuilder RBAC markers move with
their reconciler; `make manifests` must give the same ClusterRole (FR-010).

## R5. envtest per sub-package

Each sub-package with envtest tests gets its own `TestMain` copied from
`suite_envtest_test.go` (OD-6, OD-9). `go test ./...` runs packages in parallel, so the
extra control-plane starts mostly overlap; the first operator and api PRs record the
envtest job time before and after in the PR description. If a job grows by more than
two minutes, the thread stops and asks before continuing.

## R6. Web (027 OD-4; OD-7, OD-8)

- Screens move from `routes/<Screen>.tsx` to `pages/<Screen>.tsx` with their tests. Each page gets `routes/<Screen>.ts` with its route definition moved out of `router/tree.tsx`, which keeps only the tree assembly (OD-7). `routes/tabs/` becomes `pages/ServerDetail/tabs/` and `routes/tabs/settings/` becomes `pages/ServerDetail/tabs/settings/`, same file names (OD-8).
  Screens over 500 lines: AdminSettings 2,478, CreateServer 1,351, ThemeSettings 886,
  Users 825, Servers 680, Backups 642, Dashboard 596; tabs: Mods 1,290, Files 670;
  settings tabs: Networking 1,045, ShareLinks 658.
- Section names come from the screen's own top-level JSX blocks (each card, panel or
  step), so the split follows the existing structure. A part used by two or more screens
  goes to `components/`.
- Non-screen files over 500: `types.ts` 1,132 (OD-5: split by domain behind a
  re-exporting `types.ts`), `lib/endpoints.ts` 1,046, `components/modules/BuildModuleDialog.tsx`
  973, `components/CaptureWidget.tsx` 826, `components/server/ServerActionsCard.tsx` 658,
  `components/modules/SourceDialog.tsx` 573.
- Imports use the `@/` alias almost everywhere, so test edits are mostly
  `@/routes/X` → `@/pages/X` (OD-6).
- The DOM must not change: visual-diff and the Playwright screenshot suite are the proof.

## R7. Small services (027 OD-3)

| Module | Today | Target |
|---|---|---|
| `audit-syslog-bridge` | `main.go` 460, `peek_*.go` at root | `cmd/main.go` + `internal/<concern>/` (syslog framing, forwarder, HTTP handler) |
| `mcp-server` | `main.go`, `fixadvice.go`, `tools.go` at root, `internal/kube/` | `cmd/main.go` + `internal/tools/`, `internal/fixadvice/` |
| `telemetry-receiver` | 30 files at root (`views.go` 733) | `cmd/main.go` + `internal/{ingest,store,views,charts,pow,ratelimit,…}/`; `design-export/`, `web/` and `telemetry-dashboard.pen` stay put |
| `tunnel` | `main.go` 1,052, `playit_reporter.go` 525 | `cmd/main.go` + `internal/<relay>/`; waits for #721–#723 (OD-1) |
| `sentinel` | `main.go` 1,017 | `cmd/main.go` + `internal/<concern>/`; waits for #729 (OD-1) |

Each Dockerfile's `go build` target and any Makefile/chart reference to the entry point
move in the same PR. No `^cmd/` exclusion is added to these modules' `.testcoverage.yml`
(agent and api have one, the small services don't): adding it would change what the gate
measures, so the thin `cmd/main.go` stays counted (FR-009).

Other modules with production files over 500 lines (FR-018) are split within their
current package (pure moves, no test edits): `api/internal/audit`, `api/internal/auth`,
`capture-sidecar/internal/httpserver`, `agent/internal/{mods,rcon,files,players}`,
`i18n/cmd/i18n-check`, `operator/cmd`.

## R8. Spec 023 mapping (FR-016)

- Components in 023 are whole module directories read from `go.work`, so no move
  inside a module changes them.
- 023 R4 (package-level selection) scans import lines, so it follows new sub-packages
  without edits; smaller packages make it select fewer tests.
- File-level entries to re-check in every PR: `test/e2e/suite-components.json` `paths`
  (once 023 adds it), `ci_scope.py` `file_consumers` (`operator/config/crd`, `modules`),
  `.testcoverage.yml` `exclude.paths` (`^internal/db/db_postgres\.go$` in api),
  `.golangci.yml` path rules (`internal/handlers/capture\.go$` gosec rule moves to the new
  capture path with the same scope; `internal/mods/mods\.go$`, `internal/ws/dialer\.go$`,
  `internal/auth/sessions\.go$` likewise if their files split).

## R9. Line-ceiling and layout check (FR-019, OD-4, 027 OD-5)

- **Decision**: `hack/check_layout.py` (Python stdlib, like `ci_scope.py`), with unit
  tests `hack/test_check_layout.py` run in CI next to `make test-ci-scope`, added to
  `make lint` as `check-layout` in the last PR. Rules: (1) production `.go`/`.ts`/`.tsx`
  files ≤ 500 lines, FR-018 exemptions, test files exempt; (2) service modules
  (027 OD-3 list) have no `.go` file at the module root; code lives under `cmd/` and
  `internal/`; (3) no screen code under `web/src/routes/` (OD-7), screens under
  `web/src/pages/`. Failures name the file and the `docs/layout.md` rule.
- **Alternatives**: golangci-lint `funlen`/`lll` (per function or line, not per file;
  doesn't cover web); ESLint `max-lines` for web plus a Go script (two tools for one rule).

## R10. Conflicts with in-flight work

Open PRs on 2026-10-20: #752 (spec 023, `hack/`, `.github/`), #745 (i18n,
`web/src/routes/Login_language.test.tsx`), Dependabot PRs. Each move PR's first task
lists open PRs touching its paths; if one exists, the thread asks valgul whether to wait.
