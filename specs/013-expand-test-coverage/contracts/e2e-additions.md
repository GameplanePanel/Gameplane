# Contract: E2E Additions

## Go E2E (`test/e2e/`)

### Bucket `optional-components` (new, in `buckets.sh`)

```text
TestOptionalComponents_Lifecycle
```

- Added to the `e2e-go` and `e2e-go-arm64` matrices beside `telemetry`, and to the report regexes at `ci.yaml` (`e2e (operator|…|telemetry|optional-components) /`).
- Owns its cluster: runs `helm upgrade --reuse-values`, so it never shares a cluster with another bucket.
- Logins: ≤ 2 (one admin, one restricted user).
- Phases and assertions: research R7 steps 1–6. Every phase is a `t.Run` subtest, so a failure names the component.
- Fixtures: `test/e2e/fixtures/syslog-sink.yaml` (TCP :514, writes received lines to stdout) and `test/e2e/fixtures/frps.yaml` (frps + token Secret + dashboard API). Images are pinned by digest.

### Helper `TestE2E_ResticRepoReady` (bucket `operator`)

- `t.Parallel()`; calls `ensureResticRepo(t)` and asserts the warm-up Job succeeded. It is idempotent with every other caller.
- Also invoked by the `e2e-web-live` job before Playwright: `go test -tags=e2e -run '^TestE2E_ResticRepoReady$'`.

### Job `e2e-postgres` (new, `ci.yaml`)

| | |
|---|---|
| Runner | `ubuntu-latest` (amd64 only) |
| Needs | `changes`, `build-images` |
| Trigger | the same scope as `e2e-go` (OD-3 may narrow it) |
| Cluster | `deploy/kind/e2e.sh up "$CLUSTER" postgres` |
| Runs | `buckets.sh regex api-auth`, `api-roles` and `api-rbac`, as three `go test` invocations in that order |
| Report | in `report.needs`, `NEEDS_ORDER`, `JOB_MATCHERS` |

`deploy/kind/e2e.sh` profile `postgres` contract:
- applies a PostgreSQL 17 StatefulSet + Service + credentials Secret in the release namespace (image digest = the `api-postgres` service)
- waits for `pg_isready`
- installs the chart with `api.db.driver=postgres` and `api.db.dsn` from the Secret. The chart has one global `image.tag` for every component (`charts/gameplane/values.yaml:14-22`), so before `kind load` the profile retags `gameplane-test/api:e2e-postgres` as `gameplane-test/api:${TAG}`; no chart change is needed
- every other value matches the `e2e` profile

`api/Dockerfile`: `ARG GO_TAGS=""` → `go build -tags "${GO_TAGS}" …`. With the default empty value the output is byte-for-byte the current build.

## Build inputs

| File | Adds |
|---|---|
| `.github/actions/build-e2e-images` | targets `mcp-server`, `audit-syslog-bridge`, `tunnel-frp` (`tunnel/Dockerfile.frp`), `api-postgres` (`GO_TAGS=postgres`) |
| `.github/actions/e2e-images` | loads those images into kind |
| `deploy/kind/e2e.sh` image loop (`e2e.sh:166-172`) | `mcp-server`, `audit-syslog-bridge`, `tunnel-frp` added to the loaded set |
| `Makefile` `dev-load` | the same images (enforced by `make check-dev-load-images`) |

## Live Playwright (`web/e2e/specs/live/`)

Every spec follows the existing live pattern:
- a `test.skip(GAMEPLANE_E2E_TARGET !== "live")` describe guard
- seeds via `_seed.ts` with unique `e2e-<spec>-<random>` names
- `loginIfNeeded` after navigation
- cleanup in `afterEach`
- at least one reload-and-assert for persistence
- one real-API error path

| File | Must prove |
|---|---|
| `admin-notifications.spec.ts` | webhook target CRUD persists; SSRF-blocked URL error rendered |
| `users-and-roles.spec.ts` | user create + role assignment persists; the new user's session sees only permitted nav |
| `role-bindings.spec.ts` | custom role + binding persists; restricted context gets the forbidden state |
| `backup-restore.spec.ts` | backup → restore reverts a marker file; restore error toast |
| `mod-registries.spec.ts` | registry add/remove persists; duplicate → 409 shown inline |

**Login budget for `e2e-web-live`**: 1 (globalSetup) + 1 (`users-and-roles`) + 1 (`role-bindings`) + whatever existing specs already add. The first green run records the total in the gap record, and it MUST stay ≤ 10.

## Unit

| File | Covers | Bar |
|---|---|---|
| `api/internal/handlers/secrets_managed_test.go` | both functions, every branch (research R8) | 100 % statements of `secrets_managed.go` |
| `web/src/routes/tabs/ConsoleShell.test.tsx` | statuses, send, history, toolbar, closed state | ≥ 80 % lines of `ConsoleShell.tsx` |
| `api/internal/db/db_postgres_test.go` `TestPostgres_MigrateFromFrozenBaseline` | 001–012 → head with data intact | runs in `api-postgres` |
