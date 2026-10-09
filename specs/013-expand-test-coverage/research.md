# Research: Expand Test Coverage

**Feature**: `013-expand-test-coverage` | **Date**: 2026-10-09 | **Plan**: [plan.md](plan.md)

The spec was written on 2026-09-02. Several of its survey numbers are stale, so R0 re-baselines them against `master` at `cd5f177` before anything is designed. Every later decision builds on R0, not on the spec's original counts.

---

## R0: Current baseline (re-survey of the spec's gaps)

**Decision**: Treat the gaps below as the authoritative starting state. They seed the Coverage Gap Record ([contracts/coverage-gap-record.md](contracts/coverage-gap-record.md)).

| Spec gap | State on `master` (2026-10-09) | Evidence |
|---|---|---|
| Go dependency vulnerability scanning | **Open**: no `govulncheck`/OSV step in any workflow | `grep -E "govulncheck\|osv" .github/workflows/` returns nothing |
| Container image scanning | **Open**: images are cosign-signed but never scanned | `images.yaml:115`, `publish-edge.yaml:189` sign; no `trivy`/`grype` anywhere |
| Dockerfile linting | **Open**: 17 real Dockerfiles, no hadolint | `git ls-files \| grep Dockerfile` |
| Dependency-review gating | **Open**: Dependabot opens update PRs (`.github/dependabot.yml`) but nothing reviews the dependencies a PR adds | no `dependency-review-action` |
| Generated-file drift | **Partial**: chart CRD copies are diffed (`ci.yaml:1068-1077`); `make generate` (deepcopy), `make manifests` (`operator/config/{crd,rbac}`) and `go mod tidy` are not | no `controller-gen`/`tidy` in `ci.yaml` |
| Module freshness drift | **Open**: nothing checks the `modules/` and `website/` submodule pointers | see R5 |
| Live dashboard E2E | **Partial**: 9 live Playwright specs now exist (`web/e2e/specs/live/`), up from the spec's 4. Login, create/delete server, server settings, share links, theme, console PTY and data screens are live. Admin notification config, user create + role assignment, backup restore, mod registry config and role bindings are still mock-only | `web/e2e/specs/{adminSettings,users,restoreFlow,rbacEnforcement}.spec.ts` all `test.skip(GAMEPLANE_E2E_TARGET === "live")` |
| Telemetry receiver E2E | **Closed** by spec 022: `TestTelemetryLifecycle` in bucket `telemetry` (`test/e2e/buckets.sh:355`), amd64 + arm64 in `e2e-go` | `ci.yaml:1310` |
| MCP server E2E | **Open**: no e2e references; image not in the e2e image bake | `.github/actions/build-e2e-images` builds operator/api/agent/sentinel/capture-sidecar/telemetry-receiver/fakeoidc only |
| Tunnel E2E | **Partial**: CRD validation (3 tests) and `TestGameServer_TunnelCredentialRefusalSurfaced` exist; nothing boots a relay and proves supervision | `test/e2e/gameserver_e2e_test.go:1728` |
| Audit syslog bridge E2E | **Open**: no e2e references; image not baked | as above |
| `secrets_managed.go` unit tests | **Open**: no `_test.go` calls `upsertLabelledSecret`/`deleteManagedSecret` directly; they are reached only through feature handlers | `grep -l upsertLabelledSecret api/internal/handlers/*_test.go` is empty |
| `ConsoleShell.tsx` test | **Open**: no `ConsoleShell.test.tsx`; exercised only indirectly by `Console.test.tsx`. File is 138 lines, not 77 | `web/src/routes/tabs/` |
| `CloneServerDialog` test | **Exists**: `web/src/components/server/CloneServerDialog.test.tsx`. Remaining work is only to confirm ≥ 80 % lines and the cancel + error case | |
| `ServerActionsMenu` test | **Exists**: `web/src/components/server/ServerActionsMenu.test.tsx`. Same as above | |
| Postgres E2E | **Partial**: `api-postgres` job (`ci.yaml:1207`) builds/vets `-tags postgres` and runs `api/internal/db` against a real PostgreSQL 17 service (schema parity, RBAC, prefs, shares, delete-user). No kind cluster runs a Postgres-backed API | |

**Rationale**: closed or partially closed gaps must not be re-implemented, and partially closed ones need only their remainder planned.

**Alternatives considered**: planning straight from the spec's survey. Rejected because it would duplicate the telemetry E2E and the two existing component tests.

---

## R1: Go dependency vulnerability gate

**Decision**: a `vuln (go)` job running `govulncheck` (pinned `golang.org/x/vuln/cmd/govulncheck@vX.Y.Z`, installed with `go install`, so `GOSUMDB` verifies it) once per Go module in `go.work`, in source mode (`govulncheck ./...`, `-tags envtest` for `operator`/`api`, `-tags e2e` for `test/e2e`, and a second pass with `-tags postgres` for `api`). It fails on any finding whose vulnerable symbol is reachable. Imported-but-unreachable findings are reported, not blocking.

**Triggers**: PRs whose change scope includes Go (`ci_scope.py` `go` output), every push to `master`, and a weekly `schedule` so a new advisory against unchanged code still surfaces.

**Proof it can fail (FR-006)**: `hack/check-govulncheck_test.sh` runs `govulncheck -db file://hack/testdata/govulncheck/db` against two fixture modules, `hack/testdata/govulncheck/{pass,fail}/`. The local OSV database holds one synthetic advisory for a synthetic fixture module (`example.invalid/vulnfixture`), and only the `fail` module calls its vulnerable function. The script expects exit 3 for `fail` and 0 for `pass`.

**Rationale**:
- `govulncheck` is the Go team's tool. Its call-graph reachability keeps the false-positive rate low enough to block on without suppressions (FR-008).
- A synthetic local database proves failure without committing a real vulnerable dependency. A real one would raise Dependabot/GitHub security alerts on the repo and could be picked up by the image builds.

**Alternatives considered**:
- `osv-scanner`: module-level only, so it flags unreachable code and would need an ignore file for false positives (a suppression in all but name).
- Trivy `fs`: same module-level problem.
- A fixture pinned to a real vulnerable `golang.org/x/net`: rejected for the alert noise above.

---

## R2: Container image vulnerability gate

**Decision**: a `vuln (images)` job that `needs: build-images`, loads the amd64 `e2e-images` artifact, and runs Trivy (`aquasecurity/trivy-action`, SHA-pinned) on every image Gameplane builds. Settings: `--severity HIGH,CRITICAL --ignore-unfixed --exit-code 1`, and the vulnerability DB cached with `actions/cache`. The images it scans:
- operator, api, agent, sentinel, capture-sidecar, telemetry-receiver
- mcp-server, audit-syslog-bridge, the three tunnel images (added to the bake by R7)
- web

`publish-edge.yaml` and `release.yaml` run the same scan on the pushed digests before `cosign sign`, so an unsigned image never ships with a known fixed CVE.

**Proof it can fail**: `hack/check-image-scan_test.sh` scans a deliberately old public base pinned by digest (an end-of-life `alpine` with known fixed HIGH CVEs) and expects exit 1. It scans the job's own freshly built `svcutil`-sized distroless image and expects exit 0. No vulnerable artifact is committed to the repo.

**Rationale**:
- `--ignore-unfixed` keeps the gate actionable: every finding can be fixed by a base bump or a Go dependency bump.
- Scanning the already-built e2e artifact adds no rebuild.
- Gating the publish workflows closes the supply-chain path the spec ties to release readiness.

**Alternatives considered**:
- Grype: comparable, but Trivy also lints Kubernetes manifests, which is a possible later use on the chart.
- Scanning only in `publish-edge`: findings would land after merge, which fails acceptance scenario 1.
- Blocking on unfixed CVEs: produces paper red with no available fix, so it would push toward an ignore file.

**Arch**: amd64 only. arm64 images share the same Go and base package sets per digest family, and doubling the scan adds runtime without new findings. The publish-path scan covers each pushed per-arch digest.

---

## R3: Dockerfile lint gate

**Decision**: a `hadolint` step in the `workflow-lint` job (it already runs actionlint and zizmor; Dockerfiles are build config of the same kind). It uses a SHA-pinned hadolint binary verified by `sha256sum -c`, the same pattern as actionlint at `ci.yaml:1136-1151`, and runs over `git ls-files '*Dockerfile*'`, excluding `hack/testdata/**`. `failure-threshold: warning`. A root `.hadolint.yaml` holds only `trustedRegistries` and the `failure-threshold`, and ignores no rules.

**Pre-existing findings**: fixed in the same PR that turns the gate on (FR-008: fix, never silence). Rule 8 forbids running hadolint locally, so the gate PR's first CI run lists the findings, and the fixes follow as commits on that PR. The gate is never merged in a red or report-only state.

**Proof it can fail**: `hack/check-hadolint_test.sh` lints `hack/testdata/hadolint/fail/Dockerfile` (unpinned `FROM`, `apt-get` without `--no-install-recommends`, `ADD` of a URL) and expects failure. It lints `.../pass/Dockerfile` and expects success.

**Rationale**: one Go-free binary, no runtime services, about 2 s for 17 files, and its findings map to concrete code fixes.

**Alternatives considered**:
- `hadolint/hadolint-action`: wraps a Docker image and adds an action dependency for zizmor to track.
- Trivy `config`: overlaps but has weaker Dockerfile style rules.
- Delta mode: rejected because the file set is small enough to fix outright.

**Open**: whether `trustedRegistries` should be enforced (it would flag `FROM` lines from Docker Hub) is OD-2.

---

## R4: Dependency-review gate

**Decision**: a `dependency review` job on `pull_request` only, using `actions/dependency-review-action` (SHA-pinned) with `fail-on-severity: high` and `comment-summary-in-pr: on-failure`. It sets no `allow-ghsas`. It covers the gomod (16 modules), npm (`web/`, `website/` pointer excluded) and GitHub Actions ecosystems from the dependency graph.

**Proof it can fail**: this gate reads the PR's dependency diff from GitHub's dependency graph, so it cannot be run against a local fixture. The proof is a one-time, never-merged validation PR that adds a dependency with a known high-severity advisory and shows the job red. The PR stays closed and its link is recorded in the Coverage Gap Record row for this gate. This is the documented exception to FR-006's "run as part of the gate's validation" (see plan Complexity Tracking).

**Rationale**:
- Dependabot only proposes upgrades; it does not stop a contributor from adding a vulnerable or newly-introduced dependency.
- dependency-review is GitHub's native gate for that, free on public repositories.

**Alternatives considered**:
- `govulncheck`/Trivy already cover Go and images, but not npm or Actions.
- `npm audit`: npm only, and too noisy on dev dependencies to block without an ignore file.

---

## R5: Generated-file and module freshness drift gates

**Decision (generated files)**: a `codegen drift` job runs `make generate manifests` and then `make tidy`, followed by `git diff --exit-code`. Its proof script, `hack/check-codegen-drift_test.sh`, works in a throwaway `git worktree`: it appends a field comment to `operator/api/v1alpha1/gameserver_types.go` without regenerating and expects failure, then expects the untouched tree to pass.

**Decision (module freshness)**: "module freshness drift" is read as *a submodule pointer in the main repo that is not on its upstream default branch*. This enforces the project convention that pointer bumps happen only after the submodule PR merges. `hack/check-submodule-freshness.sh` fetches `GameplanePanel/module` `main` and `GameplanePanel/website` `main` and fails when either committed gitlink is not an ancestor of its branch tip (`git merge-base --is-ancestor`). Being *behind* the tip is allowed: Dependabot-style forced bumps are not wanted, only unmerged pointers. The gate runs when `modules` or `website` changes. Its proof is a fixture pair of bare repositories built in a temp dir by `hack/check-submodule-freshness_test.sh`.

**Rationale**:
- Today only the chart CRD copy is diffed, so a hand-edited `zz_generated.deepcopy.go` or RBAC role, or an untidy `go.mod`, merges silently.
- The submodule rule turns an unwritten review convention into a gate.

**Alternatives considered**:
- Reading "module freshness" as "Go modules not on latest": Dependabot already owns that, and as a merge gate it would turn every PR red on each upstream release.
- Requiring pointers to equal the tip: blocks unrelated PRs whenever the submodule moves.

The user confirmed this interpretation on 2026-10-09 (OD-1, ruled).

---

## R6: Live dashboard E2E additions

**Decision**: add five live Playwright specs under `web/e2e/specs/live/`. Each seeds through `_seed.ts`, drives the real UI, reloads to prove persistence, cleans up in `afterEach`, and has one deterministic error-path assertion against the real API (not MSW).

| Spec | Flow | Error path asserted |
|---|---|---|
| `admin-notifications.spec.ts` | add a webhook notification target, reload, see it; edit URL; delete | SSRF-blocked URL (`netguard.IsAllowed` rejects `http://169.254.169.254/`) renders the API's 4xx message inline |
| `users-and-roles.spec.ts` | create user, assign a built-in role, reload; sign in as that user in a fresh context and see the role's nav only | assigning a role to a deleted user surfaces 404 |
| `role-bindings.spec.ts` | create a custom role with a namespace-scoped binding, reload, revoke | non-admin context gets 403 on Users page and sees the forbidden state |
| `backup-restore.spec.ts` | seed busybox server, take a backup, write a marker file via the Files tab, restore, see the marker gone after reload | restoring a backup whose server was deleted shows the error toast |
| `mod-registries.spec.ts` | add a mod registry, reload, remove | duplicate name returns 409 and is shown inline |

**Login budget**: `globalSetup` logs in once and every spec reuses `storageState`. Only `users-and-roles` and `role-bindings` add logins: one each, as the created non-admin user (a different username, so a different per-user bucket). The per-IP total stays within burst 10.

**Backup dependency**: restore needs a restic repo, and `deploy/kind/e2e.sh` does not provision one. The Go suite creates it lazily through `ensureResticRepo(t)` (`test/e2e/test_helpers_e2e_test.go:459`), which applies `fixtures/restic-server.yaml`, `fixtures/backup-restic-secret.yaml` and a warm-up Job. The live job therefore gains one step before Playwright: a `go test -run '^TestE2E_ResticRepoReady$'` helper test (bucketed in `operator`, since it is idempotent and login-free) that calls `ensureResticRepo`. That reuses the same fixtures and warm-up retry logic rather than duplicating them in TypeScript, and the spec then points its backup at that repo's Secret.

**Runtime**: `e2e-web-live` has a 60-minute timeout and runs `workers: 1`. Five specs at an estimated 1–3 minutes each fit, and the job's duration is recorded in the gap record after the first green run.

**Rationale**:
- These are the remaining flows in FR-002/SC-002.
- Seeding through the API (already the house pattern, `_seed.ts:7-11`) keeps each spec independent of Go-suite ordering.

**Alternatives considered**:
- Flipping the existing mock specs to run live: they depend on MSW fixtures (fixed IDs, injected 5xx) that a real cluster cannot reproduce, which is why they `test.skip` live.
- A Go-side API test instead: does not exercise the UI, which fails FR-002.

**Design-first**: tests only, no UI change, so Principle II does not apply. If a spec exposes a UI defect whose fix changes visuals, that fix goes through `design.pen` first, as its own task.

---

## R7: Optional component E2E (MCP server, syslog bridge, tunnel)

**Decision**: a new bucket `optional-components` in the existing `e2e-go` matrix, on amd64 and arm64 like `telemetry`. It holds one sequential lifecycle test, `TestOptionalComponents_Lifecycle`, that owns its cluster:

1. **Disabled state**: on the stock e2e install (all optional components off), assert there is no `gameplane-mcp-server` Deployment, no audit-syslog-bridge Deployment or Service, and no tunnel Deployment for a GameServer without `spec.tunnel`.
2. **Enable**: deploy an in-cluster syslog sink and an in-cluster `frps` (both images pinned by digest). Then `helm upgrade --reuse-values --set mcpServer.enabled=true --set api.audit.webhook.syslogBridge.enabled=true --set api.audit.webhook.syslogBridge.syslog.addr=<sink>:514`, with a shared `authSecretRef` token.
3. **MCP server**: `kubectl exec -i deploy/gameplane-mcp-server -- /mcp-server serve`, then send `initialize`, `tools/list` and `tools/call list_gameplane_resources` for GameServers. Assert the seeded GameServer is returned and that `tools/list` returns exactly the six read tools (`list_gameplane_resources`, `get_gameplane_resource`, `list_pods`, `get_pod`, `get_pod_logs`, `list_events`). Then call `get_gameplane_resource` for a kind outside the ClusterRole (`clusters`) and assert an error result, not data.
4. **Syslog bridge**: perform one audited admin action (a role create), then poll the sink's log for an RFC 5424 line with `APP-NAME gameplane-audit` carrying that action. Send an unauthenticated POST to the bridge and assert 401.
5. **Tunnel**: create a busybox GameServer with `spec.tunnel` set to frp, pointing at the in-cluster `frps` with its token Secret. Assert `TunnelReady=True`, that the `frps` dashboard API lists the proxy, and that the tunnel pod has zero restarts over 60 s. Then kill the relay process with `kubectl exec … kill` and assert the supervisor restarts it without the pod restarting (backoff path, `tunnel/specs.md` responsibility 6).
6. **No interference**: with everything enabled, the existing helper that creates a GameServer and waits for `Running` succeeds, and an RBAC-restricted user still gets 403 on an admin route.

The telemetry receiver is already covered by `TestTelemetryLifecycle` (R0). This bucket does not re-test it; the gap record links that test instead.

**Image prerequisites**: add `mcp-server`, `audit-syslog-bridge` and `tunnel` (frp variant only; playit and tailscale need external accounts) to `.github/actions/build-e2e-images` and `.github/actions/e2e-images`. `make check-dev-load-images` then forces the matching `dev-load` update.

**Login budget**: one admin login, plus one restricted-user login in step 6. Two in total, like `telemetry`.

**Rationale**:
- A separate cluster is required because `helm upgrade` would restart the API under every other bucket's tests (the same reason as `telemetry`, research R17 of spec 022).
- Adding a bucket to the existing `e2e-go` matrix is not an arm64-only bucket, so it meets the spec's ARM64 edge case.
- frp is the only relay provider that runs fully offline in kind.

**Alternatives considered**:
- Enabling the components in the shared e2e profile for all buckets: proves non-interference for free, but makes the disabled state untestable in E2E and adds two pods to every cluster.
- One bucket per component: three more matrix legs × 2 arches for about 5 minutes of assertions.
- Testing Tailscale/playit: needs real accounts and outbound relay access, so they cannot be CI-deterministic. Their unit coverage stays the authority, and the gap record marks them "unit only, by design".

---

## R8: Unit coverage gaps

**Decision**:
- `api/internal/handlers/secrets_managed_test.go` drives both functions against a fake `kube.Client` backed by `k8s.io/client-go/kubernetes/fake`, the same pattern as `registry_secret_test.go`. Cases:
  - `upsertLabelledSecret`: fresh create carries the feature + `managed-by` labels; `AlreadyExists` on a feature-labelled Secret merge-patches `stringData` and re-asserts `managed-by` (a kubectl-created labelled Secret becomes UI-deletable); `AlreadyExists` on a Secret without the feature label returns `errNotManagedSecret` and leaves it untouched; injected errors from Create (non-AlreadyExists), Get and Patch are returned
  - `deleteManagedSecret`: deletes when both labels are present; returns `NotFound` (not a distinct error, so existence does not leak) when either label is missing; propagates a Get error and a Delete error
  - errors asserted with `apierrors.Is*` / `errors.Is`, injected through the fake clientset's reactor chain

  The target is 100 % of the file's statements (SC-004).
- `web/src/routes/tabs/ConsoleShell.test.tsx` renders `ConsoleShell` with a stub `ConsoleHandle` and covers:
  - each `WSStatus` label and dot
  - Enter dispatching `sendCommand` and clearing the input; empty input not dispatching
  - Up/Down history recall, including restoring the unsent draft and the 100-entry cap
  - the Clear, Download and Fullscreen buttons calling the handle
  - the input disabled or the indicator offline when `status === "closed"`, the ungraceful-disconnect path

  The target is ≥ 80 % lines.
- `CloneServerDialog.test.tsx` and `ServerActionsMenu.test.tsx` already exist. CI's per-file lcov output is read; only missing cases (cancel discards input, one API error rendered) are added, and only if the file is under 80 % lines.

**Rationale**: these are direct unit tests of security- and UX-critical code that today is covered only incidentally, so a refactor of a calling handler could drop the coverage unnoticed.

**Alternatives considered**: an envtest suite for `secrets_managed.go`. Rejected because the functions are pure client calls, the fake clientset is faster, and envtest adds nothing.

**Coverage thresholds**: unchanged (`api` 80, web 92/76/82/92). New tests can only raise them.

---

## R9: Postgres-backed E2E

**Decision**:
- **Build**: `api/Dockerfile` gains `ARG GO_TAGS=""`, passed as `go build -tags "${GO_TAGS}"`. The e2e bake adds a `gameplane-test/api:e2e-postgres` target with `GO_TAGS=postgres`. Published images are unchanged.
- **Cluster**: `deploy/kind/e2e.sh` gains a `postgres` profile. It installs a single-replica PostgreSQL 17 (same digest-pinned image as the `api-postgres` service) as a plain StatefulSet + Service in the release namespace, then installs the chart with `api.db.driver=postgres` and `api.db.dsn` from a Secret. The chart uses one global `image.tag`, so the profile retags `gameplane-test/api:e2e-postgres` as `gameplane-test/api:${TAG}` before `kind load` instead of adding a per-component tag to the chart.
- **Job**: a new `e2e-postgres` job, amd64 only, reuses the existing bucket regexes rather than creating a bucket. A test can only live in one bucket (`buckets.sh verify`), so this is a second *execution* of `api-auth`, `api-roles` and `api-rbac`, not a copy of their tests. Those buckets cover user CRUD, sessions, roles, RBAC and the audit log: the DB-backed paths SC-005 names. The job lives in `ci.yaml` and joins the `report` job (plus `NEEDS_ORDER`/`JOB_MATCHERS`, which `check-ci-report-coverage.sh` enforces).
- **Upgrade path (acceptance scenario 3)**: no published image was ever built with `-tags postgres`, so there is no "prior API version" Postgres deployment to upgrade from. It is covered instead by a new `TestPostgres_MigrateFromFrozenBaseline` in `api/internal/db/db_postgres_test.go`, run by the existing `api-postgres` job. The test applies only the frozen `migrations/postgres/001-012` set, inserts representative rows, runs `Migrate` to head, and asserts the rows and `schema_migrations` are intact. The E2E job then proves the current head end to end.

**Rationale**:
- Re-running existing buckets gives "the same scenarios" (SC-005) by construction and cannot drift from the SQLite runs.
- amd64-only keeps the cost at one extra leg. Postgres remains experimental (spec assumption), and arm64 adds nothing DB-specific.

**Alternatives considered**:
- A separate `postgres` bucket with copied tests: violates disjointness and drifts.
- A matrix dimension `db: [sqlite, postgres]` on all of `e2e-go`: doubles the largest job.
- A manual-only `workflow_dispatch` trigger: the spec allows it, but a gate that never runs on PRs catches nothing, so it was rejected while the cost stays at one leg. If the leg proves too slow, OD-3 lets the user move it to `master` pushes plus dispatch.

---

## R10: Coverage Gap Record

**Decision**: one Markdown file, `specs/013-expand-test-coverage/coverage-gaps.md`, with one table row per gap. Columns and allowed values are fixed in [contracts/coverage-gap-record.md](contracts/coverage-gap-record.md). It is seeded from R0 by the first implementation task, and every PR that closes a gap flips its row in the same commit. A lightweight `hack/check-coverage-gaps.sh`, run in the `docs` job, validates the table: allowed statuses only, and `closed` rows must cite a test path or CI job plus proof link.

**Rationale**:
- One readable file meets SC-007's ten-minute read.
- Keeping it in the spec folder means it archives with the feature under `done_013-…` (Constitution IV), and nothing else in the repo needs a new location.
- The validator stops the record from going stale silently.

**Alternatives considered**:
- YAML: machine-friendly, but a person reading it for status is the stated user.
- GitHub issues per gap: spread across many places, fails "one artifact".
- `docs/testing.md`: outlives the feature and would need a separate owner.

---

## R11: CI plumbing common to every new job

**Decision**: every new job:
- gets a `ci_scope.py` output (with a `test_ci_scope.py` case)
- has least-privilege `permissions: contents: read`
- uses SHA-pinned actions with version comments, which zizmor and actionlint already enforce
- is added to the `report` job's `needs`, `NEEDS_ORDER` and `JOB_MATCHERS`

Job display names must not start with `go (`, because the report's `go` matcher is a prefix match (`ci.yaml:1205` comment).

**Rationale**: `check-ci-report-coverage.sh` fails otherwise, and the PR comment would silently omit the job.
