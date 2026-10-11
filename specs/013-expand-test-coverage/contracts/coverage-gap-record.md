# Contract: Coverage Gap Record

**File**: `specs/013-expand-test-coverage/coverage-gaps.md` (moves with the folder to `done_013-…` on completion).

## Layout

```markdown
# Coverage Gap Record

**Summary**: <closed>/<total> closed · <closed-unproven> closed unproven · <in-progress> in progress · <open> open · <by-design> unit-only by design
**Last updated**: YYYY-MM-DD

| ID | Category | Gap | Story | FR | Priority | Status | Evidence | Notes |
|---|---|---|---|---|---|---|---|---|
| G-01 | static | Go dependency vulnerability scanning | US1 | FR-001 | P1 | open | | |
```

## Rules (enforced by `hack/check-coverage-gaps.sh`, run in the `docs` job)

1. Exactly one table, with the nine columns in the order above.
2. `ID` matches `^G-\d{2}$` and is unique.
3. `Category` ∈ {`static`, `e2e-dashboard`, `e2e-optional`, `unit`, `e2e-postgres`}.
4. `Priority` ∈ {`P1`, `P2`, `P3`}.
5. `Status` ∈ {`open`, `in-progress`, `closed`, `closed-unproven`, `unit-only-by-design`}.
6. `closed` rows have non-empty `Evidence` containing a repo path or job name **and** an `https://github.com/GameplanePanel/` link.
7. `unit-only-by-design` rows have non-empty `Notes`. `closed-unproven` rows have `Evidence` naming the job and the PR that added it, and `Notes` citing the ruling that waived the failure proof (only G-04, OD-4).
8. The `Summary` counts equal the counts computed from the table.

**Proof**: `hack/check-coverage-gaps_test.sh` runs the validator on `hack/testdata/coverage-gaps/{pass,fail}/*.md`. The fail cases are a bad status, a closed row without evidence, a wrong summary count, and a `closed-unproven` row without a ruling in Notes.

## Seed rows (from research R0)

| ID | Category | Gap | Priority | Initial status |
|---|---|---|---|---|
| G-01 | static | govulncheck gate | P1 | open |
| G-02 | static | container image scan gate | P1 | open |
| G-03 | static | Dockerfile lint gate | P1 | open |
| G-04 | static | dependency-review gate | P1 | open |
| G-05 | static | codegen / tidy drift gate | P1 | open |
| G-06 | static | submodule pointer freshness gate | P1 | open |
| G-07 | e2e-dashboard | live: notification webhook config | P1 | open |
| G-08 | e2e-dashboard | live: user create + role assignment | P1 | open |
| G-09 | e2e-dashboard | live: custom role + binding | P1 | open |
| G-10 | e2e-dashboard | live: backup restore | P1 | open |
| G-11 | e2e-dashboard | live: mod registry config | P1 | open |
| G-12 | e2e-dashboard | live: login, create/delete server, settings | P1 | closed (existing `web/e2e/specs/live/*`) |
| G-13 | e2e-optional | MCP server | P2 | open |
| G-14 | e2e-optional | audit syslog bridge | P2 | open |
| G-15 | e2e-optional | tunnel (frp) supervision | P2 | open |
| G-16 | e2e-optional | tunnel (tailscale, playit) | P2 | unit-only-by-design |
| G-17 | e2e-optional | telemetry receiver | P2 | closed (`TestTelemetryLifecycle`, spec 022) |
| G-18 | e2e-optional | disabled state + no interference | P2 | open |
| G-19 | unit | `secrets_managed.go` 100 % | P2 | open |
| G-20 | unit | `ConsoleShell.test.tsx` ≥ 80 % lines | P2 | open |
| G-21 | unit | `CloneServerDialog` ≥ 80 % lines + cancel/error | P2 | in-progress (test exists; verify) |
| G-22 | unit | `ServerActionsMenu` ≥ 80 % lines + cancel/error | P2 | in-progress (test exists; verify) |
| G-23 | e2e-postgres | kind E2E on Postgres-backed API | P3 | open |
| G-24 | e2e-postgres | upgrade from frozen 001–012 baseline | P3 | open |
