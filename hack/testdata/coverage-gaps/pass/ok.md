# Coverage Gap Record

**Summary**: 1/4 closed · 1 closed unproven · 0 in progress · 1 open · 1 unit-only by design
**Last updated**: 2026-10-11

| ID | Category | Gap | Story | FR | Priority | Status | Evidence | Notes |
|---|---|---|---|---|---|---|---|---|
| G-01 | static | govulncheck gate | US1 | FR-001 | P1 | open | | |
| G-02 | static | Dockerfile lint gate ≥ 1 check | US1 | FR-002 | P1 | closed | `job x` https://github.com/GameplanePanel/Gameplane/actions/runs/1 | |
| G-03 | unit | `secrets_managed.go` 100 % | US2 | FR-004 | P2 | unit-only-by-design | | Covered by envtest; no unit path |
| G-04 | static | dependency-review gate | US1 | FR-003 | P1 | closed-unproven | `dependency review` https://github.com/GameplanePanel/Gameplane/pull/1 | OD-4: configuration review only |
