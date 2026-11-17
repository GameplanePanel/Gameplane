# Feature 009 — Code Scanning Vulnerability Remediation & Dependabot PR Integration — Plan

**Branch**: `009-remediate-security-dependabot`  
**Date**: 2026-08-28  
**Spec**: [spec.md](./spec.md)

## Summary

This feature has two independent halves sharing a branch but no functional overlap: **(a)** remediate 14 CodeQL security alerts, and **(b)** merge 21 Dependabot PRs individually. The work is primarily making existing, correct defences legible to CodeQL, not adding missing ones.

**Triage finding**: the spec was written assuming all 14 alerts are real defects requiring fixes. Inspection of the live code contradicts that: **13 of 14 are verified false positives with guards already in place** (path confinement, SSRF validation, loopback checks, ZIP-slip detection, redaction), **1 is a real unguarded `InsecureSkipVerify` in an e2e helper** (`test/e2e/internal/satisfactory/app.go:188`). Additionally, **1 separate latent gap** exists (not counted in the 14 alerts): `agent/internal/rcon/websocket.go` dials an admin-supplied URL with no netguard policy (defence-in-depth, not active vulnerability). The false-positive policy is to refactor each site into a shape CodeQL's built-in sanitizer models recognize, or dismiss via the code-scanning API with a documented justification. No in-source suppressions are permitted.

**Dependabot PR count correction**: the spec lists 20 PRs (#262–#281). Triage found 21: the list omits #283, a real security bump (brace-expansion CVE-2026-13149 + js-yaml DoS), with green checks and in scope for Phase B. The spec's version targets still match the live PRs; the PR count becomes 21, not 20.

## Technical Context

**Language/Version**: Go 1.26.0 across a 14-module workspace (netguard, gameaction, gameproto, operator, api, agent, audit-syslog-bridge, telemetry-receiver, sentinel, capture-sidecar, mcp-server, svcutil, tunnel, test/e2e). TypeScript 6.0.3 (upgrading to 7.0.2 in a deferred phase) + React 19.2.8 + Vite 8.1.5 in web/; Vitest 4.1.10, Playwright 1.62.0.

**Primary Dependencies**: controller-runtime v0.24.1, client-go v0.36.3, k8s.io/api v0.36.3, chi v5, coder/websocket v1.8.12, sigstore/cosign v2.6.4, minio-go v7.2.1, modernc.org/sqlite (production path), oras-go v2 (module distribution).

**Storage**: SQLite via modernc.org/sqlite for audit_events table (pagination is the audit alert's confinement point).

**Testing**: go test + envtest across 14 Go modules (operator/api built with `--build-tags=envtest`), Vitest + Playwright for web, kind e2e across 12 buckets (test/e2e/buckets.sh, serial ratelimit bucket last, login-budget constraints per bucket).

**Target Platform**: Kubernetes 1.28+.

**Project Type**: Multi-module Go control plane + React dashboard, federation of CRDs and controllers, operator-authoritative pattern, API as UX layer.

**Performance Goals**: N/A — this is a security and dependency-hygiene feature with no new hot path. The audit page's 500-row cap is a resource bound (already specified, tested by TestAPI_AuditPaginationAndFilter in the api-auth bucket).

**Constraints**: No in-source lint suppressions or scanner directives; CI is the sole verifier; per-module coverage gates must not regress. CodeQL dismissal is a GitHub-side action (code-scanning API) with an audit trail, not an in-source suppression, and sits outside Principle III's prohibition.

**Scale/Scope**: 14 CodeQL alerts (13 false positives + 1 real defect), plus 1 separate latent gap in `agent/internal/rcon/websocket.go` (defence-in-depth). 21 Dependabot PRs (18 green, 1 diagnosis-pending, 2 major migrations deferred to Phase D). Roughly 6 source files touched for alert remediation: agent/internal/mods/mods.go, agent/internal/rcon/satisfactory.go, agent/internal/rcon/websocket.go, api/internal/kube/watch.go, api/internal/audit/audit.go, api/internal/handlers/audit.go, test/e2e/internal/satisfactory/app.go.

## Constitution Check

| Principle | Status | Justification |
|-----------|--------|---|
| **I. E2E-Tested Delivery** | PASS | Path-confinement negative cases (traversal archive rejection) go in the api-mods bucket alongside existing mod tests. The audit pagination bound is already covered by TestAPI_AuditPaginationAndFilter in api-auth. `agent/internal/rcon/satisfactory.go` is correctly guarded (false positive); the real defect is the unguarded helper in `test/e2e/internal/satisfactory/app.go`, covered by a unit test rather than an e2e test because `satisfactory_bot_e2e_test.go` sits in the bot-heavy bucket that never runs in CI. |
| **II. Design-First** | N/A | This feature touches no dashboard visual surface. Backend + operator + test-helper code only. No Pencil pass or design-export update required. |
| **III. Language & Ecosystem Best Practice** | PASS | The whole false-positive policy is built on never adding an in-source suppression directive. Code-scanning API dismissals are repository metadata with an audit trail, not source-level `//nolint` or `// @ts-ignore`, and therefore sit outside Principle III's prohibition. Each false-positive site is refactored into a shape CodeQL recognizes, or dismissed with a documented justification in contracts/. The real defect and latent gap are fixed, not silenced. |
| **IV. Spec-Driven Development** | PASS | This plan documents intent. Contracts/ will house the per-alert triage and dismissal justifications. Behaviour changes to agent/internal/rcon/ and api/internal/audit/ will be reflected in their module specs.md. |
| **V. Delegate via Workflow** | PASS | Implementation is delegated through Workflow; haiku starts, tier+1 review follows. Alert remediation and Dependabot PR merging are independent tasks fanned out concurrently on separate branches. |
| **VI. CI Bears the Heavy Lifting** | PASS | All verification happens on GitHub Actions CI. No local execution — neither builds, tests, lint, nor codegen. Every gate (lint matrix, go test matrix, web build/test, e2e buckets, CodeQL re-analysis) is a CI job named in the brief or in `.github/workflows/ci.yaml`. |

## Project Structure

### Documentation

- **plan.md** — this file
- **research.md** — written; Phase 0 decision record and the 14-alert triage
- **data-model.md** — written; models the Security Alert, Path Confinement Contract, Bounded Query Window, Dependency Upgrade Unit and TLS Trust Boundary entities
- **quickstart.md** — workflow for reviewing false-positive refactors and merging PRs individually
- **contracts/** — three files:
  - **alert-disposition.md** — per-alert triage, verdicts, dispositions and the exact dismissal justification text
  - **path-confinement.md** — the behavioural contract for the confinement helper
  - **dependency-upgrade.md** — per-PR merge gate, mechanics, ordering, exceptions
- **tasks.md** — detailed breakdown per phase (to be generated)

### Source Code

**agent/internal/mods/**: Mod download, extraction, staging, removal. Confinement helpers: `safeName()`, `archiveFolderName()`. No changes to interfaces or public contract; refactor may improve guard clarity to CodeQL (e.g., extracting a dedicated `ensurePathInBounds()` helper if CodeQL's taint model recognizes it).

**agent/internal/rcon/**: Console protocols. `satisfactory.go` has the TLS loopback guard (`isLoopbackHost`); package doc records rationale. `websocket.go` (~line 292, `ensureLocked`) dials an admin-supplied URL with no netguard — a latent gap, not an active defect (CodeQL has not flagged it, and the URL is CRD-controlled). Add netguard policy for defence-in-depth.

**agent/internal/files/**: File operations. `resolve()` is the deepest confinement helper in the repo (Clean + prefix-check + symlink evaluation on target and ancestors). No changes expected; already correct.

**api/internal/kube/**: Cluster watcher. `watch.go` line 40 and 54 log a field name (taint source is the string `secretKey`, not secret bytes). Refactor to make taint-flow separation clear to CodeQL (e.g., document that kubeconfig bytes never enter the log call).

**api/internal/audit/**: Auditor and pagination. `audit.go:834` allocates with a clamped limit (lines 820–822). Handler `handlers/audit.go:25` parses raw input without clamping. Refactor: move the clamp to the handler so the allocation site is obviously bounded.

**api/internal/handlers/**: REST handlers. `audit.go` line 25 parses raw pagination limit; move the clamp here (or compose with the Auditor's clamp as a defensive second bound).

**test/e2e/internal/satisfactory/**: Satisfactory game bot. `app.go:188` has the real defect: `InsecureSkipVerify: true` unconditional, no loopback guard. Fix by adding a loopback guard mirroring `isLoopbackHost`, so `InsecureSkipVerify` is set only for loopback addresses and non-loopback addresses are rejected outright.

**test/e2e/**: `buckets.sh` lists the 12 e2e buckets. Negative cases for path confinement (traversal archive, etc.) go in the api-mods bucket per login budget. No new bucket; no modifications to bucket division.

**web/**: `package.json` only, in the deferred Phase D. No changes in Phase A or B.

### Structure Decision

This feature adds no new module or package boundary. The confinement helpers (safeName, archiveFolderName, resolve, ensurePathInBounds if extracted) stay within their respective modules (agent/internal/mods, agent/internal/files) rather than being promoted to shared workspace packages. **Rationale**: only agent code needs them today; a premature shared package would add a coverage gate and a release surface for one caller. **Precedent**: netguard and gameaction are shared packages because multiple consumers exist (operator + agent for netguard; api + agent for gameaction). If a second consumer of confinement helpers appears, they will be promoted to a shared package at that time, with all the attendant coverage and release scrutiny.

## Phasing

### Phase A — Alert Remediation

**Scope**: the 12 refactor-or-dismiss targets, plus the 1 real defect (satisfactory.go), plus the 1 latent gap (websocket.go).

**Branch**: feature branch `009-remediate-security-dependabot`, owned by the remediation phase.

**Work**:
- Refactor false-positive sites (6 path-injection, 1 zip-slip, 2 request-forgery, 2 clear-text-logging, 1 disabled-certificate-check, 1 uncontrolled-allocation-size) into shapes CodeQL's taint and alias models recognize.
- Dismiss via code-scanning API those that cannot be refactored (TLS cert bypass; CodeQL's analysis of InsecureSkipVerify does not model surrounding guards or `VerifyPeerCertificate` alternatives).
- Fix the real defect (satisfactory.go: add loopback guard or restructure).
- Add the latent defence (websocket.go: netguard policy for admin-supplied URLs).
- Write dismissal justifications in contracts/alert-disposition.md.
- Add negative e2e cases (traversal archive rejection) in the api-mods bucket.
- Update agent/internal/rcon/specs.md and api/internal/audit/specs.md to document the TLS rationale and pagination bound.

**Gate**: CI green, then CodeQL re-analysis on master confirms all alerts transition to `fixed` or `dismissed` state.

### Phase B — Dependabot PR Integration

**Scope**: 21 Dependabot PRs, 18 green + 1 pending diagnosis + 2 major migrations deferred.

**Branches**: one branch per individual PR merge (rebasing each onto master as it moves; Dependabot-created branches are the source, not an integration branch).

**Work**: Merge the 18 green PRs individually (`gh pr merge --admin`, merge-commit) in ascending blast-radius order by count of go.mod directories touched. Go PRs merge sequentially; npm PRs merge in parallel with the Go sequence (they touch disjoint files):
- Go PRs in order: #276 (2 dirs), #279 (3), #281 (5), #267 (5), #274 (7), #271 (7), #269 (7), #273 (8), #265 (8)
- npm PRs in parallel: #283, #280, #278, #277, #275, #270, #266, #264, #262

**Diagnosis task**: PR #263 (sigstore/sigstore v1.10.9) has 1 failing check; the single failing check has not yet been diagnosed and that diagnosis is Phase C's scope. This task runs in parallel with B or as a blocker if the check reveals a real incompatibility.

**Gate**: Each PR green before its own merge. All 18 merged → Phase C cleared.

### Phase C — Sigstore Diagnosis

**Scope**: PR #263 (sigstore/sigstore v1.10.9) — 1 failing check, cause unknown.

**Work**: Inspect the failing check output, identify the incompatibility, and either (a) land a fix commit on the feature branch, merge the PR, or (b) document the blocker.

**Gate**: PR #263 either merged or documented as blocked.

### Phase D — Major Migrations (Gated, Separate Cycle)

**Scope**: PR #272 (typescript 6.0.3 → 7.0.2, 4 failing checks) and PR #268 (eslint 9 → 10, 1 failing check). These are NOT started until Phase B is complete. Phase D's tasks are enumerated in tasks.md.

**Branch**: separate feature branch off master (after Phase A+B merge), e.g., `009-ts7-eslint10`.

**Work**: 
- Upgrade TypeScript to 7.0.2. Address all 4 failing type-check errors. No type errors may be suppressed with `// @ts-ignore`.
- Upgrade ESLint to 10.0.1 (drops eslintrc config format, removes deprecated SourceCode and rule-context methods, raises Node floor to ^20.19 || ^22.13 || >=24). Address all resulting lint errors. No lint errors may be suppressed with `// eslint-disable-next-line`.
- Revalidate all test suites pass.

**Gate**: CI fully green on the migration branch, then merge.

**Rationale**: Two major migrations with unknown failure modes must not gate 18 green, already-tested dependency bumps. Parallel phasing (Phase A + B concurrent) permits work to start immediately; B's completion clears the gate for D. The phases use separate branches and are tied together only by this spec.

## Complexity Tracking

No constitution principles are violated. Two spec deviations are recorded:

| Spec Claim | Correction | Reason |
|---|---|---|
| "20 PRs (#262–#281)" + "all 14 alerts are real defects" | 21 PRs (adds #283 security bump) + 13 false positives, 1 real defect | Live PR listing shows #283 (brace-expansion CVE + js-yaml DoS) with green checks, in scope for Phase B. Triage of the 14 CodeQL alerts found 13 with existing guards (path confinement, SSRF validation, loopback, zip-slip, redaction, pagination clamp); 1 unguarded e2e defect (satisfactory TLS). Separate from the 14: 1 latent gap in websocket netguard (defence-in-depth). |
| "FR-018 (TS 7.0.2) and FR-020 (ESLint 10) in main scope" | Deferred to Phase D, separate gated cycle, not started until Phase B merged | Maintainer decision: two major migrations with failing checks must not block 18 green dependency bumps. Phase A + B can run concurrently; B's completion gates Phase D. |
