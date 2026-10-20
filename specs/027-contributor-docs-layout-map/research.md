# Research: Contributor Docs and Layout Map

All facts measured on `master` at `8a67f2e` (2026-10-20) unless stated.

## R1. Agent instruction files

- `git ls-files -s`: `AGENT.md` and `AGENTS.md` are mode `120000` (symlink) with target `CLAUDE.md`; `CLAUDE.md` is a regular file (15,993 bytes). Equal md5 sums come from the links, not from copies.
- Website submodule: `AGENTS.md` is the regular file (2,248 bytes), `CLAUDE.md` is a symlink to it. No `AGENT.md`.
- References to `AGENT.md` outside `specs/`, `modules/`, `website/`: none. References to `AGENTS.md`: `CLAUDE.md:57`, `CLAUDE.md:110`, `CLAUDE.md:140`, `.claude/skills/submodule-bump/SKILL.md:6`, `.claude/agents/web-builder.md:71`. `docs/contributing.md:151-158` names `CLAUDE.md` only.
- `.specify/integration.json`: integration `agy`. It reads the spec-kit files under `.specify/`, not `AGENT.md`.
- Decision: OD-1.

## R2. Spec and design homes

| Home | Count | Role | Governance |
|---|---|---|---|
| `specs/NNN-<slug>/`, `specs/done_NNN-<slug>/` | 5 open, 20 done | feature specs (a change) | constitution IV, CLAUDE.md rules 15–16 |
| `<module>/specs.md` | 19 (the 17 `go.work` modules incl. `test/e2e`, plus `test/e2e/internal` and `web`; 67–1,274 lines) | how a module works today | constitution IV ("Every module folder … MUST maintain a `specs.md`"); `hack/check-specs.sh` in `make lint`; routed by `docs/agent-architecture.md` |
| `docs/superpowers/plans/` | 3 files, 103 unticked steps | July 2026 implementation plans | none |
| `docs/superpowers/specs/` | 2 files, "approved, not yet implemented" | July 2026 designs | none |

The superpowers designs shipped: console modes (`consoleMode` in `operator/api/v1alpha1/gametemplate_types.go:107-115`), action stdin, multi-category modules (`modulesource_types.go`), wake-on-connect (`sentinel/`). They reference only each other; spec 024 explicitly skipped them when grepping docs (`specs/done_024-…/tasks.md` T023).

Two copies of `specs.md` also live inside `specs/done_018-…/audit/held/briefs/files/`; they are frozen audit evidence and stay.

Decision: OD-2.

## R3. Go module shapes

| Module | Kind | Shape today |
|---|---|---|
| api | service | `cmd/main.go` (+ more files in `cmd/`) + `internal/` (18 pkgs; `handlers/` flat, 146 files) |
| operator | service | `cmd/main.go` + `internal/` (5 pkgs; `controller/` flat, 150 files) + `api/v1alpha1/` |
| agent | service | `cmd/main.go` + `internal/` (17 pkgs) |
| capture-sidecar | service | `cmd/main.go` + `internal/` (3 pkgs) |
| tunnel | service | root `package main`, `main.go` 1,052 lines (+1 file) |
| sentinel | service | root `package main`, `main.go` 1,017 lines |
| telemetry-receiver | service | root `package main`, 12 source + 25 test files; `web/`, `design-export/` assets |
| audit-syslog-bridge | service | root `package main`, 3 files |
| mcp-server | service | root `package main`, 3 files + `internal/` |
| gp-module | CLI | `cmd/gp-module/` + `internal/` (7 pkgs) |
| i18n | library + CLI | root package + `cmd/i18n-check/` |
| netguard, gameaction, gameproto, svcutil, telemetryschema | library | one flat root package |
| test/e2e | test suite | root package + `internal/` (33 pkgs) |

Decision: OD-3.

## R4. Web layout

`web/src/`: `routes/` (49 files incl. tests, plus `tabs/`), `components/` (flat files plus `backups/`, `modules/`, `server/`, `settings/`, `ui/`), `lib/` (API client, helpers, `i18n/`), `router/`, `styles/`, `test/`, `__tests__/`, `api/types.ts` (generated, 4,393 lines), `types.ts` (hand-written, 1,132 lines). Largest screens: `AdminSettings.tsx` 2,478, `CreateServer.tsx` 1,351, `tabs/Mods.tsx` 1,290. Tests are colocated (`X.test.tsx`, `X_<topic>.test.tsx`).

Decision: OD-4.

## R5. CI wall-clock times (for the first-PR table)

From the 2026-10-20 assessment (GitHub Actions job timings): docs-only PR 1.8 min (run 2507); spec-only PR 5.3 min (run 2525); web-only PR 18.3 min (run 2524); any Go change 29–75 min, median 37.5 min over 11 PR runs; master push median 42.5 min. Spec 023 (in progress) will change these; the table carries this date and the runs.

## R6. Existing contributor docs

- `docs/contributing.md` sections: Dev loop, Design first, UI components, Code style, CI and workflows, Testing, E2E testing, AI-assisted development, Submitting a change, Translating, Code review, Release process, Community visibility. No first-PR path.
- Website `src/content/docs/contributing.mdx`, `contribution-design-workflow.mdx`, `local-development-setup.mdx` exist; FR-017 adds a link, not a copy.
- `docs/agent-architecture.md`: component table + one task row (i18n).

## R7. Enforcement

`make lint` already runs `check-specs`, `check-doc-versions`, `check-links`. A layout check added before code moves would fail on tunnel, sentinel, telemetry-receiver, audit-syslog-bridge, mcp-server (under OD-3's recommended option). Decision: OD-5.
