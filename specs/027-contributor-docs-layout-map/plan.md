# Implementation Plan: Contributor Docs and Layout Map

**Branch**: `claude/project-thread-yulzhu` (spec docs) | **Date**: 2026-10-20 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/027-contributor-docs-layout-map/spec.md`

## Summary

Docs and structure rules only. Write down which agent instructions file is real (and fix the links), name the two design-doc homes and retire `docs/superpowers/`, add a layout doc (`docs/layout.md`) that gives every Go module and `web/src` a target shape and its current gap for spec 028 to apply, add a "Your first PR" section to `docs/contributing.md` with measured CI times, and extend the task-to-doc map. No source file or package moves (FR-019).

Decision status: **OD-1..OD-5 settled** (see [OPEN-DECISIONS.md](./OPEN-DECISIONS.md)); parts that apply a decision are marked (OD-n).

## Technical Context

**Language/Version**: Markdown docs; git symlinks; Bash only if OD-5 picks a check in this spec

**Primary Dependencies**: existing doc gates `hack/check-links.sh`, `hack/check-specs.sh`, `hack/check-doc-versions.sh` (all in `make lint`, CI only)

**Storage**: N/A

**Testing**: CI doc gates only. No e2e test: docs-only spec, same Principle I docs exception as specs 012 and 024. Locally only `git ls-files -s` / `bash -n` style checks and, for the website, `npm run build`.

**Target Platform**: GitHub repo readers (humans and AI agents); website docs page

**Project Type**: docs change across GP (`GameplanePanel/Gameplane`) and WEB (`GameplanePanel/website`)

**Performance Goals**: N/A (SC-001: a new reader answers three sample questions in under 5 minutes)

**Constraints**: no code moves; no new check that fails on current `master` (FR-013); constitution amended in the same PR if a rule changes Principle IV

**Scale/Scope**: 17 Go modules + `web`; 19 `specs.md`; 5 superpowers files; ~6 docs touched in GP, 1 page in WEB

## Constitution Check

| Principle | Status |
|---|---|
| I. E2E-tested delivery | Docs exception (no runtime behavior changes), as specs 012/024. Verified by CI doc gates. |
| II. Design-first | Not applicable: no dashboard or website visuals change. |
| III. Ecosystem best practice | Layout follows common Go practice (`cmd/` + `internal/`, flat library packages). |
| IV. Spec-driven development | This spec. Recommended OD-2 keeps `<module>/specs.md` as-is, so Principle IV text is unchanged; the docs rule restates it. If OD-2 picks a rename, Principle IV is amended (MINOR bump + Sync Impact Report) in the same PR. |
| V. Delegate to workflows | Implementation via one `Workflow` script, `haiku` writers, one `sonnet` diff review. |
| VI. CI does the heavy lifting | No local test/lint runs. |

No violations; Complexity Tracking empty.

## Design

### D1. Agent instructions (OD-1, settled: `AGENTS.md` canonical in both)

- GP: replace the `AGENTS.md` symlink with the regular file (content of today's `CLAUDE.md`, unchanged), turn `CLAUDE.md` into a symlink to `AGENTS.md`, and `git rm AGENT.md`. Do it in one commit so `git log --follow AGENTS.md` shows the rename. Paths that cite `CLAUDE.md` (constitution, `docs/contributing.md`, `.claude/` agents and skills, `.coderabbit.yaml`) still resolve through the link and are left as they are, except where a doc names "the real file".
- `docs/contributing.md` "AI-assisted development": one paragraph naming `AGENTS.md` as the real file, `CLAUDE.md` as a link for tools that read that name, and the Windows note (with `core.symlinks=false` the link checks out as a one-line text file, so edit `AGENTS.md`).
- WEB: already `AGENTS.md` real, `CLAUDE.md` link. No change.

### D2. Design-doc homes (OD-2, settled: archive superpowers, keep `specs.md` names)

- Docs rule, added to `docs/contributing.md` (new short section "Where design docs go") and CLAUDE.md (one line under rule 15):
  - A change → `specs/NNN-<slug>/` through speckit (renamed `done_` when finished).
  - How a module works today → `<module>/specs.md`, updated in the same PR as the behavior (constitution IV).
  - Nothing else holds designs or plans.
- `git mv docs/superpowers docs/archive/superpowers`; prepend to each of the 5 files a banner: historical, superseded by the shipped code; current behavior in `<module>/specs.md` (each banner names the right one: `operator/specs.md`, `agent/specs.md`, `sentinel/specs.md`, `gp-module/specs.md` as applicable). Fix the five internal path references (`docs/superpowers/specs/…` inside the plans).
- `docs/archive/README.md` (5 lines): what the folder is, that nothing in it is current.

### D3. Layout doc `docs/layout.md` (OD-3 settled: `cmd/main.go` + `internal/`; OD-4 settled: `pages/` with 500-line section split)

Structure per [contracts/layout-doc.md](./contracts/layout-doc.md):

1. Kinds and target shapes:
   - **Service** (long-running binary): `cmd/main.go` thin wiring (flags/env via `svcutil`, construct, run); behavior in `internal/<concern>/`; tests next to the code they test; `specs.md`, `README.md`, `Dockerfile` at the module root; no other `.go` at the root.
   - **Library**: one flat root package named after the module; subpackages only for a separately importable concern.
   - **CLI tool**: `cmd/<binary>/main.go` + `internal/<concern>/`.
   - **Web (`web/src`)**: `pages/<Screen>.tsx` holds each screen; a screen over 500 lines is split into section files under `pages/<Screen>/` (e.g. `Footer.tsx`, `UpperSection.tsx`) that `pages/<Screen>.tsx` composes; `routes/` only exposes pages (route definitions, no screen code); `components/` only for parts used by 2+ screens (`components/ui/` for primitives); tests next to the file they test; `lib/` for API client, hooks and helpers; `router/` for the route tree; generated `api/types.ts` untouched, hand-written `types.ts` stays.
2. Layout map: one row per `go.work` module and `web` (18 rows): kind, target, gap today ("matches" or the concrete move), from research R3/R4.
3. Exceptions: `test/e2e` (test suite, keeps `internal/<game>/` per-game packages), `operator/api/v1alpha1` (kubebuilder convention), `i18n` (library with a CLI under `cmd/i18n-check/`, matches both kinds).
4. Enforcement (OD-5, settled): "Spec 028 adds a folder-shape check to `make lint` once every module matches; until then review enforces this doc."
5. Out of scope: per-package split boundaries and file-size thresholds belong to spec 028.

### D4. First-PR guide

New section near the top of `docs/contributing.md` per [contracts/first-pr-section.md](./contracts/first-pr-section.md): a 6-step path (fork/branch, find the owning folder, compile check, commit `-s`, open PR with labels, wait for CI), the change-type table (FR-015) with CI times from research R5 dated 2026-10-20, and the gates list (FR-016). README's contributing link points at the section anchor. WEB `contributing.mdx` gets a short "Your first PR" pointer linking to the GitHub doc (FR-017), then a pointer bump in GP after the WEB PR merges.

### D5. Task-to-doc map

`docs/agent-architecture.md` "Task to doc map": one row per CLAUDE.md "Common workflows" entry (CRD field, API route, dashboard screen, game module, website, DB migration) plus "change CI" and "where new code goes" (→ `docs/layout.md`), keeping the i18n row. Component table gains a pointer to `docs/layout.md`.

## Project Structure

### Documentation (this feature)

```text
specs/027-contributor-docs-layout-map/
├── spec.md
├── OPEN-DECISIONS.md
├── research.md
├── data-model.md
├── plan.md
├── contracts/
│   ├── layout-doc.md
│   └── first-pr-section.md
├── checklists/requirements.md
└── tasks.md
```

### Files changed by implementation

```text
GP:
  AGENT.md                          (removed, OD-1)
  AGENTS.md                         (becomes the regular file, OD-1; rule 15 docs-rule line; repo map: docs/layout.md, docs/archive/)
  CLAUDE.md                         (becomes a symlink to AGENTS.md, OD-1)
  README.md                         (contributing link → first-PR anchor)
  docs/contributing.md              (Your first PR; Where design docs go; AI-assisted development)
  docs/layout.md                    (new)
  docs/agent-architecture.md        (task rows, layout link)
  docs/superpowers/ → docs/archive/superpowers/  (OD-2, banners)
  docs/archive/README.md            (new)
WEB:
  src/content/docs/contributing.mdx (pointer to first-PR section)
GP (after WEB merges):
  website                           (submodule pointer bump)
```

## Delivery

- PR A (GP, this branch): spec docs only (`type: docs`, `area: specs`).
- PR B (GP): implementation, one commit per user story (`type: docs`, `area: specs` + `area: shared`).
- PR C (WEB): contributing page pointer. Then PR D (GP): website pointer bump.

## Complexity Tracking

None.
