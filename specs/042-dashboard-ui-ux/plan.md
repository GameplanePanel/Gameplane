# Implementation Plan: Dashboard UI and UX polish, screen by screen

**Branch**: `spec-042/dashboard-ui-ux` | **Date**: 2026-11-26 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/042-dashboard-ui-ux/spec.md`

## Summary

A design-first polish pass on four screens (Servers, Server detail › Logs, › Backups,
› Settings › Resources) plus six shared rules that apply app-wide (corner shapes, tabs
that never wrap, one frame per table, status pill, slider fill, theme-token colours).
All but one change is web presentation. The exception is backup size: a new optional
`Backup.status.dataSize` recorded by the operator from the Restic summary or the agent's
disk sample, so the dashboard can show the data a backup holds instead of the volume
capacity ([research.md](research.md) R7).

## Technical Context

**Language/Version**: TypeScript (strict) + React 19 for web; Go for the operator field.

**Primary Dependencies**: HeroUI v3, Tailwind, TanStack Router/Query, react-intl (web);
controller-runtime (operator).

**Storage**: one additive CRD status field (`Backup.status.dataSize`); no DB migration.

**Testing**: Vitest + Testing Library (web, coverage floors 92/76/82/92), envtest for the
Backup reconciler, E2E backup tests (`test/e2e/backup_e2e_test.go`, in whichever bucket runs it) for the Restic and CSI paths. CI only.

**Target Platform**: browsers (desktop + mobile widths), Kubernetes operator.

**Project Type**: web dashboard + operator.

**Performance Goals**: none new; `dataSize` reuses data already measured.

**Constraints**: design.fig first (constitution II); light and dark themes; i18n strings
through react-intl with descriptions (`make check-i18n`).

**Scale/Scope**: 4 screens + ~10 tab lists, ~11 tables, 8 status-pill call sites, 2
sliders, 1 CRD field.

## Constitution Check

| Principle | Status |
|---|---|
| I. E2E-tested delivery | PASS: `dataSize` asserted in the existing backup E2E tests for Restic and CSI; UI changes covered by unit tests + browser smoke test. |
| II. Design-first | PASS: phase A is the design pass in `design.fig` + `fig-export.ts`; no React before valgul approves the export (SC-007). |
| III. Best practice | PASS: tokens instead of raw palette classes; no lint suppressions. |
| IV. Spec-driven | PASS with one open item: OD-8 (old backups) and OD-9 (sleep colour) must be ruled before `/speckit-tasks`. |
| V. Delegate to workflows | PASS: implementation runs as Workflow scripts at haiku, reviewed one tier up. |
| VI. CI bears the load | PASS: local compile checks only. |
| CRD sync (rule 7) | PASS: generate + manifests in the same commit as the field. |

Post-design re-check: unchanged, no violations.

## Phases

- **A. Design** (`design.fig`, one pass, then `hack/design/fig-export.ts`): tokens and
  components first (status pill, tabs, stat card with warning badge, slider, table
  frame, toolbar button shapes, storage number + unit), then the screens listed in
  research R9. Hand-off for valgul's approval.
- **B. Shared web rules**: tokens (R1), tab CSS (R2), table frames (R3), PhaseChip (R4),
  slider class (R5). Each one commit; tests updated to the spec (TEST EDITS rule).
- **C. Screens**: Servers (cards, actions, toolbar), Logs (two-row toolbar, level
  colours), Resources + Create server (number box, labels, storage input R6).
- **D. Backup data size**: CRD field + reconciler (R7) + envtest + E2E assertion; then
  web Size column and card (data-model.md), OD-8 fallback.
- **E. Changelog** `changelog/unreleased/<PR>-dashboard-ui-polish.md` and
  `…-backup-data-size.md`.

PRs (when valgul says go): stacked, design first, then B+C, then D.

## Project Structure

### Documentation (this feature)

```text
specs/042-dashboard-ui-ux/
├── spec.md
├── OPEN-DECISIONS.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── backup-status-datasize.md
│   └── ui-rules.md
├── checklists/requirements.md
└── tasks.md            # next: /speckit-tasks
```

### Source Code (repository root)

```text
design.fig + assets/design-export/            # phase A
web/src/styles/globals.css                    # tokens, tabs, slider
web/src/components/ui/{StatCard,PhaseChip,ResourceInput}.tsx
web/src/components/sftp/SftpDialogs.tsx
web/src/components/backups/{BackupSize,BackupRow}.tsx
web/src/routes/{Servers,Backups,Users,CreateServer}.tsx
web/src/routes/tabs/{Logs,Backups}.tsx, tabs/settings/Resources.tsx
web/src/types.ts
operator/api/v1alpha1/backup_types.go         # dataSize
operator/internal/controller/{backup_controller,backup_volumesnapshot}.go
test/e2e/backup_e2e_test.go
```

**Structure Decision**: existing web + operator layout; no new packages.

## Complexity Tracking

None.
