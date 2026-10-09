# Quickstart: Validating Expand Test Coverage

All validation happens in CI (Constitution VI). Locally, run only compile checks:

```sh
go build ./...                                  # in any touched Go module
(cd test/e2e && go vet -tags e2e ./...)          # e2e compiles
(cd web && npx tsc --noEmit && npm run typecheck:e2e)
bash -n hack/check-*.sh                          # gate scripts parse
```

Push the branch and read the CI run. The `ci report` sticky comment lists every job, including the new ones.

## US1: static gates

For each gate in [contracts/static-gates.md](contracts/static-gates.md):

1. **Proof step green**: the gate's `hack/check-<gate>_test.sh` step passes. It exits 0 only when the gate failed on the fail fixture and passed on the pass fixture.
2. **Gate green on the branch**: the gate job itself is green, which means every pre-existing finding was fixed.
3. **Gate blocks for real**: the Coverage Gap Record row links a run where the gate was red on a real violation. That is the gate PR's first run (hadolint, Trivy, govulncheck with pre-existing findings) or the dependency-review validation PR.
4. **No suppressions**: `git diff master -- . ':!specs'` contains no `nolint`, `eslint-disable`, `@ts-ignore`, `hadolint ignore`, `.trivyignore`, `allow-ghsas` or `-exclude`.

Expected at release time (acceptance scenario 3): one `master` CI run with `vuln (go)`, `vuln (images)`, `workflow-lint` (hadolint), `codegen drift` and `submodule freshness` all green, and `dependency review` green on the release PR.

## US2: live dashboard flows

- `e2e web live / amd64 (kind)` and `/ arm64` are green, and the Playwright report lists the five new specs as passed, not skipped.
- To see the specs can fail, a reviewer can check each spec's error-path assertion targets a real API status (4xx/409/SSRF rejection), not an MSW handler. `grep -n "page.route\|msw" web/e2e/specs/live/*.spec.ts` returns nothing.
- Login budget: the job log's login count (recorded in the gap record) is ≤ 10.

## US3: optional components

- `e2e optional-components / amd64 (kind)` and `/ arm64` are green, with subtests `disabled`, `mcp`, `syslog`, `tunnel-frp` and `core-unaffected` each passing.
- `e2e telemetry / *` is still green (unchanged owner of the telemetry receiver).
- `e2e bucket coverage` is green: `buckets.sh verify` accepts the new bucket and the restic helper.

## US4: unit gaps

- `go (api / amd64)` is green, and its coverage artifact shows `secrets_managed.go` at 100 %.
- `web` is green, and its lcov shows `ConsoleShell.tsx` ≥ 80 % lines and `CloneServerDialog.tsx`/`ServerActionsMenu.tsx` ≥ 80 % lines.
- Module thresholds (`api` 80, web 92/76/82/92) are unchanged in the diff.

## US5: Postgres

- `api (postgres)` is green, including `TestPostgres_MigrateFromFrozenBaseline`.
- `e2e postgres / amd64 (kind)` is green for all three bucket runs, and its log shows `--db-driver=postgres` in the API pod args (from the dump on failure, or a `kubectl get deploy -o yaml` step).

## FR-009: gap record

- `docs` job green: `hack/check-coverage-gaps.sh` and its `_test.sh` pass.
- Opening `coverage-gaps.md` shows every row's status and evidence. The feature is complete when no row is `open` or `in-progress`.
