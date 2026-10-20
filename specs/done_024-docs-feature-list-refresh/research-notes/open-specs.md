# Open specs snapshot (T007)

Snapshot of every `specs/<NNN>-*` folder without a `done_` prefix in the main repo (GP) checkout, plus the session-start blocked-upgrade entries from `CLAUDE.md`. Counts come from `- [X]`/`- [x]` and `- [ ]` lines in each `tasks.md`. Blockers are quoted from the cited file.

| Spec | Title | Done/Total | Status in plain words | Blocker and what unblocks it |
|---|---|---|---|---|
| 002-nuclear-option-ip-pool | Nuclear Option Module & Load-Balancer IP Pool Override (`specs/002-nuclear-option-ip-pool/spec.md:1`) | 136/143 | Draft (`spec.md:7`). Most tasks are checked. Open: the Nuclear Option join test and its bucket (T058, T059), the join-to-ready timing check (T060), the moderation and invalid-config assertions (T080, T117), the live join run (T126) and the coverage row (T130). T029 is ticked `[X]` but its text says "withdrawn" (`tasks.md:121`). | T058 and T126/T130 are skipped, "awaiting packet capture" (2026-10-11, maintainer decision). The server does not bind UDP 7777, so no join client can be built until a real-client packet capture documents the handshake (`tasks.md:323`, `tasks.md:327`). The unblocker is that capture. Other open items are not named as blocked. |
| 009-remediate-security-dependabot | Code Scanning Vulnerability Remediation & Dependabot PR Integration (`spec.md:1`) | 80/82 | Draft (`spec.md:7`). Security remediation and the Dependabot sweep are done. Only the TypeScript 7 migration and the merge of PR #272 remain (T055, T056, `tasks.md:163-164`). | TypeScript 7 is still blocked. `@typescript-eslint/parser` latest 8.71.2 and canary 8.71.3-alpha.4 both declare `typescript` peer `>=4.8.4 <6.1.0`. Unblocks when a release accepts TS 7. Then do T055/T056 per `CLAUDE.md`. |
| 013-expand-test-coverage | Expand Test Coverage (`spec.md:1`) | 39/62 | Draft (`spec.md:7`). Open work is the live web e2e specs (T037 to T044), the optional-components e2e (T045 to T050), the Postgres e2e (T054 to T058), docs and checks (T059 to T061), and the archive step (T062, `tasks.md:391`). OPEN-DECISIONS.md has 0 open items (`OPEN-DECISIONS.md:3`). | No external blocker is named. T044, T050 and T058 wait for the first green CI run of their job. T062 waits until every PR is merged and every task is checked or withdrawn. None is withdrawn. |
| 019-localization-language-packs | App Localization & Community Language Packs (`spec.md:1`) | 0/105 | Draft (`spec.md:7`). Nothing is checked yet. The four decisions are settled (`plan.md:7`). The first step is the `i18n/` module and checker, which the plan says "unblocks everything" (`plan.md:138`). | No blocker named. Start with T001 (create `i18n/` module). Archive step T105 waits for all tasks to be complete. |
| 021-settings-game-config | Edit game configuration from server Settings (`spec.md:1`) | 20/24 | Implemented, pending CI and review (`spec.md:7`). Open items are CI green and labels (T017, T018, T024), plus the archive step (T019, `tasks.md:38-40`). | Needs CI green and human PR approval (rules 12 and 14). Archive to `done_021` after merge. |
| 022-default-telemetry-dashboard | Default telemetry destination, extended telemetry, and telemetry dashboard (`spec.md:1`) | 109/119 | Draft (clarified), amended 2026-10-14 with the version trend (`spec.md:7`). The shipped feature is unchanged until the new slice lands (`plan.md:223`). Open: T101 to T110 (version-trend tables, views, chart rename, docs, tests, PR). OPEN-DECISIONS.md has 0 open (10 ruled, `OPEN-DECISIONS.md:3`). | T104 needs Pencil design on the machine where Pencil runs (`tasks.md:591`). T109 needs the user's sign-off before editing existing tests `lifecycle_test.go:11` and `charts_test.go:83` (`tasks.md:599`). T110 needs the PR merged before archive. |
| 023-change-based-test-selection | Change-Based Test Selection (`spec.md:1`) | 0/52 | Draft (`spec.md:7`). Nothing is checked yet. No open decisions remain (`OPEN-DECISIONS.md:20`). | No blocker named. T001 creates the `ci: full-run` label. T052 waits for 30 merged PRs to measure runner minutes (`tasks.md:164`). |
| 023-standalone-panel | Standalone panel and API (`spec.md:1`) | n/a (no tasks.md) | Spec only: Intent and Design sections, no status line, no task list (`spec.md:1-31`). | Not tracked by any task list. Needs `tasks.md` before it can be counted. |
| 024-docs-feature-list-refresh | Documentation Refresh, Feature Catalog, and Roadmap Status (`spec.md:1`) | 0/45 | Draft (`spec.md:7`). This is the feature being implemented. OD-2 is settled and no open decisions remain (`OPEN-DECISIONS.md:11`). | No blocker named. |

## Session-start blocked upgrades (`CLAUDE.md`, "Session start: blocked dependency upgrades")

Checked with `npm view` on this session.

- **TypeScript 7: still blocked.** `@typescript-eslint/parser` latest 8.71.2 and canary 8.71.3-alpha.4 both declare `typescript` peer `>=4.8.4 <6.1.0`. Unblocks when a release accepts TS 7. Then bump `typescript` and `@typescript-eslint/*`, do T055/T056 in spec 009, merge #272, and delete the entry.
- **ESLint 10: still blocked.** `eslint-plugin-react` latest 7.37.5 declares `eslint` peer `^3 || ... || ^9.7`, which lacks `^10`. `@eslint/js` latest is 10.0.1. Unblocks when `eslint-plugin-react` accepts `^10`. Then bump `eslint` and `@eslint/js` together, merge #386 (and #387 if open), and delete the entry.

## Notes

- Spec 002 has a stale `[X]` on withdrawn task T029 (`tasks.md:121`). Its count includes it as done.
- Spec 022 has 0 open decisions but two gates on T104 and T109 that are not decisions.
- Folders `done_*` were excluded as instructed.
