---
description: "Task list for 013-expand-test-coverage"
---

# Tasks: Expand Test Coverage

**Input**: Design documents from `specs/013-expand-test-coverage/`. These are [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/](contracts/), [quickstart.md](quickstart.md) and [OPEN-DECISIONS.md](OPEN-DECISIONS.md) (OD-1 to OD-3 are ruled; OD-4 and OD-5 are open).

**Tests**: tests *are* this feature's deliverables (FR-002 to FR-005). Every user-story phase therefore consists mostly of test, fixture and CI tasks. Each gate also has a "proof it can fail" task (FR-006).

**Organization**: tasks are grouped by user story (US1 to US5, in the spec's priority order), so each story can ship and be verified on its own.

## Binding rules for every implementer (repeat these in every Workflow brief)

- **Delegation** (CLAUDE.md rule 13, Constitution V):
  - Implementation runs through `Workflow` scripts.
  - Every `agent()` call sets `model:` explicitly.
  - Start at `haiku` and escalate one tier only on functional failure. `fable` needs explicit human permission.
  - Review is one tier up, on the batch `git diff`, against this file.
  - Rule-shaped edits (pin bumps, matrix entries, report regexes) are a script run by one agent.
- **Scout once, brief many** (CLAUDE.md rule 18): fix agents get the task text and the cited contract section only.
- **Verification** (CLAUDE.md rule 8, Constitution VI):
  - Locally, run only `go build ./...`, `go vet -tags e2e ./...` (in `test/e2e`), `npx tsc --noEmit`, `npm run typecheck:e2e` and `bash -n`.
  - Never run `make test|lint|cover`, `go test`, `npm test`, Playwright, hadolint, Trivy or govulncheck locally.
  - CI is the only proof.
- **No suppressions or loosened config** (CLAUDE.md rule 4, FR-008): no `//nolint`, `eslint-disable`, `@ts-ignore`, hadolint `ignored:`, `.trivyignore`, `allow-ghsas` or govulncheck excludes.
- **Sign-off** (CLAUDE.md override 1): new tests and Dockerfile fixes count as test or production changes. Get the human's go-ahead before each PR's implementation starts.
- **Commits** (CLAUDE.md rule 11): use `git commit -s` with a conventional prefix. End with the `Co-Authored-By:` trailer. Add no session links and no "Requested by" block to commits or PR bodies (project preference).
- **PRs** (CLAUDE.md rules 12 and 14): one branch per gate or story slice, as listed in each phase's "PR" line. Apply `type:` and `area:` labels through the REST API. Never push or open a PR without asking first.
- **CI wiring** (research R11): every new `ci.yaml` job:
  - sets `permissions: contents: read` (plus only what it needs)
  - uses actions pinned by SHA with a version comment
  - has a display name that does not start with `go (`
  - is added to the `report` job's `needs:` list and to its `NEEDS_ORDER` array and `JOB_MATCHERS` map (`hack/check-ci-report-coverage.sh` enforces this)
- **Gap record**: every task that closes a gap flips its row in `specs/013-expand-test-coverage/coverage-gaps.md` in the same commit (contracts/coverage-gap-record.md).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: US1–US5 from spec.md

---

## Phase 1: Setup

**Purpose**: rebase onto current `master` and create the Coverage Gap Record that every later task updates.

- [ ] T001 Merge `origin/master` into this feature branch with a merge commit (no rebase). Then re-anchor every path in plan.md and contracts/ that `master` moved:
  - `docker-bake.hcl` is now `containers/docker-bake.hcl`
  - `cosign.pub` is now `signing/cosign.pub`
  - `e2e-capture-overhead` is a new job and new `report` member, and is the template for the `e2e-postgres` job in T057

  Edit `specs/013-expand-test-coverage/{plan.md,research.md,contracts/e2e-additions.md}` where a cited path or line changed.
- [ ] T002 Create `specs/013-expand-test-coverage/coverage-gaps.md` using the layout and the 24 seed rows in contracts/coverage-gap-record.md. Fill these rows' Evidence:
  - G-12: the `web/e2e/specs/live/*` specs
  - G-17: `TestTelemetryLifecycle` / `e2e telemetry`
  - G-16: Notes say the Tailscale/playit providers need external accounts (research R7)

  Fill the Summary and Last updated lines.
- [ ] T003 [P] Write `hack/check-coverage-gaps.sh`, which validates a gap-record file (default `specs/013-expand-test-coverage/coverage-gaps.md`, overridable with `COVERAGE_GAPS_FILE`) against rules 1–8 of contracts/coverage-gap-record.md. It exits non-zero with a `file:line` message per violation.
- [ ] T004 [P] Add the fixtures `hack/testdata/coverage-gaps/pass/ok.md` and `hack/testdata/coverage-gaps/fail/{bad-status,closed-no-evidence,wrong-summary}.md`.
- [ ] T005 Write `hack/check-coverage-gaps_test.sh`. It must fail unless every `fail/*.md` is rejected and `pass/ok.md` is accepted (depends on T003, T004).
- [ ] T006 Add a `check-coverage-gaps` target (running both scripts) to `Makefile`, include it in the `lint:` prerequisites, and add a `run: make check-coverage-gaps` step to the `docs` job in `.github/workflows/ci.yaml`, next to `make check-specs`.

**Checkpoint**: the gap record exists, is validated in CI, and is proven able to fail.

---

## Phase 2: Foundational (blocking prerequisites)

**Purpose**: the image and change-scope inputs that US1 (Trivy), US3 and US5 all need.

**⚠️** T007–T011 must land before T029 (Trivy), Phase 5 and Phase 7.

- [ ] T007 Add two filters and outputs to the `changes` job's paths filter in `.github/workflows/ci.yaml`. Follow the `capture_overhead` filter added on `master` as the pattern.
  - `submodules`: paths `modules` and `website`
  - `codegen`: paths `operator/api/v1alpha1/**`, `**/go.mod`, `**/go.sum`, `Makefile`, `hack/sync-chart-crds.sh`, `operator/config/**`, `charts/gameplane/crds/**`, `charts/gameplane/crd-manifests/**`
- [ ] T008 [P] Add `ARG GO_TAGS=""` to `api/Dockerfile` and pass it as `-tags "${GO_TAGS}"` on the existing `go build` line. The default empty value must leave the build unchanged (contracts/e2e-additions.md, "Job e2e-postgres").
- [ ] T009 Add four targets to `containers/docker-bake.hcl`, all tagged `gameplane-test/<name>:e2e` like the existing targets:
  - `e2e-mcp-server` (`mcp-server/Dockerfile`)
  - `e2e-audit-syslog-bridge` (`audit-syslog-bridge/Dockerfile`)
  - `e2e-tunnel-frp` (`tunnel/Dockerfile.frp`)
  - `e2e-api-postgres` (`api/Dockerfile`, `args = { GO_TAGS = "postgres" }`, tag `gameplane-test/api:e2e-postgres`)

  Add the first three to `group "e2e"`. Leave `e2e-api-postgres` out of the group; it is baked on its own in T010. Depends on T008.
- [ ] T010 Update `.github/actions/build-e2e-images/action.yml`:
  - extend the step name and `docker save` list with the three new `e2e` group images
  - add a separate `bake` step for `e2e-api-postgres` (amd64 inputs only, same pattern as the `e2e-gameprobe` step)
  - save the result into the same tar

  Depends on T009.
- [ ] T011 Extend the image loop in `deploy/kind/e2e.sh` (the `loading gameplane-test/{…}` block) with `mcp-server`, `audit-syslog-bridge` and `tunnel-frp`. Confirm with `make -n dev-load` that `make check-dev-load-images` still holds: `dev-load` already iterates `ALL_IMAGE_NAMES`, so no Makefile change is expected.

**Checkpoint**: the e2e image artifact carries every image the later phases scan or deploy.

---

## Phase 3: User Story 1 – Static gates (Priority: P1) 🎯 MVP

**Goal**: six blocking gates, each proven able to fail, with zero pre-existing findings left (contracts/static-gates.md G1–G6).

**Independent Test**: for each gate, its `hack/check-<gate>_test.sh` step is green, the gate job is green, and the gap record links a run where the gate was red on a real violation (quickstart.md, "US1").

**PR boundaries**: one PR per gate, in the order G5 → G1 → G3 → G2 → G4 → G6 (plan "Phase sequencing").

### G5 codegen drift: PR `ci: codegen drift gate`

- [ ] T012 [P] [US1] Write `hack/check-codegen-drift_test.sh`. In a `git worktree add` temp checkout it:
  1. Appends a doc comment to a field in `operator/api/v1alpha1/gameserver_types.go`, edits the struct tag on that field, runs `make generate manifests && make tidy && git diff --exit-code`, and expects a non-zero exit.
  2. Resets the checkout, reruns the same command, and expects zero.

  It removes the worktree on exit.
- [ ] T013 [US1] Add a `codegen-drift` job named `codegen drift` to `.github/workflows/ci.yaml`. Settings:
  - `needs: [changes]`
  - `if: needs.changes.outputs.codegen == 'true' || github.event_name == 'push'`
  - setup: `actions/checkout`, `./.github/actions/go-cache`, `azure/setup-helm`
  - steps: `make generate manifests`, then `make tidy`, then `git diff --exit-code`, then `./hack/check-codegen-drift_test.sh`

  Register the job in `report`. Depends on T007 and T012.
- [ ] T014 [US1] If the first CI run of T013 shows drift, commit the regenerated files the job printed: deepcopy, `operator/config/**`, chart CRDs, `go.mod`/`go.sum`. Flip gap row G-05 with the job name and run link.

### G1 govulncheck: PR `ci: go vulnerability gate`

- [ ] T015 [P] [US1] Create a synthetic offline OSV database under `hack/testdata/govulncheck/db/`, in govulncheck's `-db file://` layout:
  - `index/db.json`, `index/modules.json`, `index/vulns.json`
  - `ID/GO-0000-0001.json`, with one advisory for module `example.invalid/vulnfixture` that marks function `vulnfixture.Bad` as affected below `v1.0.1`
- [ ] T016 [P] [US1] Create the fixture modules:
  - `hack/testdata/govulncheck/vulnfixture/` (module `example.invalid/vulnfixture` v1.0.0, declaring `func Bad()` and `func Good()`)
  - `hack/testdata/govulncheck/fail/` (its `main.go` calls `vulnfixture.Bad`)
  - `hack/testdata/govulncheck/pass/` (its `main.go` calls only `vulnfixture.Good`)

  Both `fail` and `pass` reach `vulnfixture` through a `replace` directive, so no network fetch happens. None of these modules goes in `go.work`.
- [ ] T017 [US1] Write `hack/check-govulncheck_test.sh`. It runs `govulncheck -db "file://$PWD/hack/testdata/govulncheck/db" ./...` in `fail/` and expects exit 3, then in `pass/` and expects exit 0. Depends on T015 and T016.
- [ ] T018 [P] [US1] Write `hack/govulncheck-all.sh`. It runs `govulncheck ./...` in every module listed in `go.work` with these tags:
  - `operator` and `api`: `-tags envtest`
  - `test/e2e`: `-tags e2e`
  - `api` again: `-tags postgres`

  It reports all findings and exits non-zero if any reachable finding exists.
- [ ] T019 [US1] Add a `vuln-go` job named `vuln (go)` to `.github/workflows/ci.yaml`. Settings:
  - `if: needs.changes.outputs.go == 'true' || github.event_name == 'push'`
  - installs govulncheck with `go install golang.org/x/vuln/cmd/govulncheck@<pinned vX.Y.Z>`, with `GOSUMDB` left on
  - runs `hack/check-govulncheck_test.sh`, then `hack/govulncheck-all.sh`

  Register the job in `report`. Depends on T017 and T018.
- [ ] T020 [P] [US1] Add `.github/workflows/vuln-weekly.yaml`. Settings:
  - `on: schedule` (weekly, Monday 04:00 UTC) and `workflow_dispatch`
  - `permissions: contents: read`
  - one job running the same install step and `hack/govulncheck-all.sh` against `master`

  Then add the file to the `workflow-lint` job's inputs if they are enumerated.
- [ ] T021 [US1] Fix every reachable finding the first `vuln (go)` run reports. Bump the affected dependency in the module's `go.mod`/`go.sum`, or the `go` toolchain line, never by excluding anything. Flip G-01.

### G3 hadolint: PR `ci: dockerfile lint gate`

- [ ] T022 [P] [US1] Create `.hadolint.yaml` containing only `failure-threshold: warning`: no `trustedRegistries` (OD-2) and no `ignored:` list.
- [ ] T023 [P] [US1] Add fixtures:
  - `hack/testdata/hadolint/fail/Dockerfile`: `FROM alpine` with no tag or digest, `RUN apk add curl` without `--no-cache`, `ADD https://example.invalid/x /x`, and `USER` absent
  - `hack/testdata/hadolint/pass/Dockerfile`: a digest-pinned distroless `FROM`, `COPY` only, `USER nonroot`
- [ ] T024 [US1] Write `hack/check-hadolint_test.sh`, which expects a failure on `fail/Dockerfile` and a success on `pass/Dockerfile`, both with `--config .hadolint.yaml`. Depends on T022 and T023.
- [ ] T025 [US1] Add steps to the existing `workflow-lint` job in `.github/workflows/ci.yaml`:
  1. Download hadolint, pinned to a release tag, using the actionlint pattern: `curl -fsSL -o` plus a `sha256sum -c` against a committed hash.
  2. Run `hadolint --config .hadolint.yaml $(git ls-files '*Dockerfile*' ':!:hack/testdata/**' ':!:*.dockerignore')`.
  3. Run `./hack/check-hadolint_test.sh`.

  Depends on T024.
- [ ] T026 [US1] Fix every finding from T025's first CI run, editing the affected files among:
  - `agent/Dockerfile`, `api/Dockerfile`, `audit-syslog-bridge/Dockerfile`, `capture-sidecar/Dockerfile`, `mcp-server/Dockerfile`, `operator/Dockerfile`, `sentinel/Dockerfile`, `telemetry-receiver/Dockerfile`, `web/Dockerfile`
  - `tunnel/Dockerfile.{frp,playit,tailscale}`, `test/e2e/Dockerfile`, `test/e2e/Dockerfile.fakeoidc`
  - `images/common/steamcmd/Dockerfile`, `images/games/nuclear-option/Dockerfile`

  Pin images by digest, add `--no-install-recommends`/`--no-cache`, and set `USER` and `SHELL -o pipefail` as each rule asks. Get sign-off on the diff first (production files). Flip G-03.

### G2 Trivy: PR `ci: container image vulnerability gate`

- [ ] T027 [P] [US1] Write `hack/check-image-scan_test.sh`. It runs `trivy image --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1` on:
  - a digest-pinned end-of-life `alpine` (pick a 3.1x digest with known fixed HIGH CVEs and record it in the script with the CVE IDs it expects), expecting exit 1
  - `gameplane-test/telemetry-receiver:e2e`, expecting exit 0
- [ ] T028 [P] [US1] Write `hack/trivy-scan-images.sh`. Given image references as arguments, it runs the same Trivy flags on each and exits non-zero if any image fails, after scanning them all.
- [ ] T029 [US1] Add a `vuln-images` job named `vuln (images)` to `.github/workflows/ci.yaml`. Settings:
  - `needs: [changes, build-images]`, with the same `if:` as `build-images`
  - loads the amd64 artifact with `./.github/actions/e2e-images`
  - caches the Trivy DB with `actions/cache` keyed on the week
  - installs Trivy through `aquasecurity/setup-trivy` or `trivy-action`, SHA-pinned
  - runs `hack/check-image-scan_test.sh`, then `hack/trivy-scan-images.sh` over every `gameplane-test/*:e2e` image, plus `gameplane-test/api:e2e-postgres`

  Register the job in `report`. Depends on T010, T027 and T028.
- [ ] T030 [US1] In `.github/workflows/publish-edge.yaml` and `.github/workflows/release.yaml`, insert a step running `hack/trivy-scan-images.sh` on each pushed digest before the `cosign sign` loop. Same flags, Trivy install SHA-pinned.
- [ ] T031 [US1] Fix every finding from T029's first run with a base-image digest bump or a Go/npm dependency bump in the image's module, never an ignore file. Get sign-off on the diff. Flip G-02.

### G4 dependency-review: PR `ci: dependency review gate`

- [ ] T032 [US1] Add a `dependency-review` job named `dependency review` to `.github/workflows/ci.yaml`. Settings:
  - `if: github.event_name == 'pull_request'`
  - `permissions: contents: read, pull-requests: write`
  - uses `actions/dependency-review-action`, SHA-pinned, with `fail-on-severity: high` and `comment-summary-in-pr: on-failure`

  Register it in `report`, and make the report treat `skipped` on push events as neutral, the same way other PR-only jobs are tallied.
- [ ] T033 [US1] Open the throwaway validation PR (plan Complexity Tracking). It must wait on **OD-4**. The PR adds a dependency with a known high-severity GHSA to `web/package.json`; the GHSA must be chosen at the time and named in the PR. Record that `dependency review` is red, close the PR without merging, delete its branch, and put the PR link in G-04's Evidence.

### G6 submodule freshness: PR `ci: submodule freshness gate` (OD-1 ruled)

- [ ] T034 [P] [US1] Write `hack/check-submodule-freshness.sh`. For each gitlink (`modules` → `https://github.com/GameplanePanel/module`, `website` → `https://github.com/GameplanePanel/website`; URLs overridable through env for tests) it:
  1. Reads the committed SHA with `git ls-tree HEAD <path>`.
  2. Fetches the upstream `main` into a temp bare repo.
  3. Fails unless `git merge-base --is-ancestor <sha> FETCH_HEAD` succeeds, naming the submodule and the SHA in the error.
- [ ] T035 [P] [US1] Write `hack/check-submodule-freshness_test.sh`. It builds an upstream bare repo and a superproject in `mktemp -d`, and checks two cases:
  - a pointer on `main` (and one behind its tip): expects pass
  - a pointer on an unmerged side branch: expects fail
- [ ] T036 [US1] Add a `submodule-freshness` job named `submodule freshness` to `.github/workflows/ci.yaml`. Settings:
  - `if: needs.changes.outputs.submodules == 'true' || github.event_name == 'push'`
  - checks out with `fetch-depth: 0`, without submodule checkout
  - runs `hack/check-submodule-freshness_test.sh`, then `hack/check-submodule-freshness.sh`

  Register it in `report`. Depends on T007, T034 and T035. Flip G-06.

**Checkpoint**: all six gates are blocking and proven. The gap rows G-01 to G-06 are `closed` with run links (quickstart.md "US1" items 1–4).

---

## Phase 4: User Story 2 – Live dashboard E2E (Priority: P1)

**Goal**: five new live Playwright specs, each with a reload-persistence check and one real-API error path (research R6, contracts/e2e-additions.md "Live Playwright").

**Independent Test**: in `e2e web live / amd64` and `/ arm64`, the five specs pass and none is skipped. `grep -n "page.route\|msw" web/e2e/specs/live/*.spec.ts` returns nothing.

**PR**: `test(web): live e2e for admin, users, roles, backups, registries`.

- [ ] T037 [US2] Add `TestE2E_ResticRepoReady` to `test/e2e/test_helpers_e2e_test.go`. It calls `t.Parallel()` and `ensureResticRepo(t)`, then asserts that the warm-up Job from `fixtures/restic-warmup-job.yaml` reports `succeeded >= 1`. Register it in `bucket_operator` in `test/e2e/buckets.sh`. Use the `e2e-test-authoring` skill.
- [ ] T038 [US2] In `.github/workflows/ci.yaml`, in the `e2e-web-live` steps anchor (`&e2e-web-live-steps`), add a step after "bootstrap admin" that runs `go test -tags=e2e -timeout 10m -v -run '^TestE2E_ResticRepoReady$' ./...` with the same env as the bootstrap step. Depends on T037.
- [ ] T039 [P] [US2] Write `web/e2e/specs/live/admin-notifications.spec.ts`:
  1. Seed nothing, then use the UI to add a webhook notification target with a unique name and an in-cluster URL.
  2. Reload and assert the target is listed.
  3. Edit the URL, reload and assert the new URL.
  4. Delete the target.

  Error path: saving `http://169.254.169.254/` renders the API's SSRF rejection inline. Clean up in `afterEach`. Selectors come from `web/src/routes/AdminSettings.tsx` and the existing `AdminSettings_notifications.test.tsx`.
- [ ] T040 [P] [US2] Write `web/e2e/specs/live/users-and-roles.spec.ts`:
  1. Create a user with a unique name through the Users page, and assign it a built-in non-admin role.
  2. Reload and assert the role.
  3. In a fresh `browser.newContext()` with no `storageState`, sign in as that user, which is the spec's one extra login, and assert the sidebar shows only the role's nav entries.

  Error path: assigning a role to a user deleted through `_seed.ts` surfaces the 404 message. `afterEach` deletes the user.
- [ ] T041 [P] [US2] Write `web/e2e/specs/live/role-bindings.spec.ts`:
  1. Create a custom role with `RoleEditorModal` and bind it to a namespace-scoped user seeded through `_seed.ts`.
  2. Reload and assert the binding.
  3. Revoke the binding and reload.

  Error path: in a fresh context signed in as the restricted user (one login), opening `/users` shows the forbidden state, backed by a 403 response. Clean up the role and the user.
- [ ] T042 [P] [US2] Write `web/e2e/specs/live/backup-restore.spec.ts`:
  1. Seed a busybox GameServer through `_seed.ts`, pointing at the restic repo Secret from `test/e2e/fixtures/backup-restic-secret.yaml`.
  2. Take a backup from the Backups tab and wait for `Completed`.
  3. Write a marker file through the Files tab.
  4. Restore the backup, reload, and assert the marker is gone.

  Error path: restoring a backup whose server was deleted through the API shows the error toast. Clean up the server and the backups. Depends on T038 at runtime only.
- [ ] T043 [P] [US2] Write `web/e2e/specs/live/mod-registries.spec.ts`:
  1. Add a mod registry with a unique name in Admin Settings.
  2. Reload and assert it is listed.
  3. Remove it.

  Error path: adding the same name twice shows the 409 message inline. Selectors come from `AdminSettings_modregistries.test.tsx`.
- [ ] T044 [US2] Run `npm run typecheck:e2e` in `web/`, then push. After the first green live run, record the job's login count and duration in `coverage-gaps.md` (contracts/e2e-additions.md requires ≤ 10 logins), and flip G-07 to G-11.

**Checkpoint**: SC-002 is met. Every major dashboard flow has a live spec.

---

## Phase 5: User Story 3 – Optional components E2E (Priority: P2)

**Goal**: E2E coverage for the MCP server, the syslog bridge and the frp tunnel: disabled state, primary operation, and no interference with core features (research R7). Telemetry is already covered (G-17).

**Independent Test**: `e2e optional-components / amd64` and `/ arm64` are green, with the subtests `disabled`, `mcp`, `syslog`, `tunnel-frp` and `core-unaffected` all passing.

**PR**: `test(e2e): optional components lifecycle`. Depends on Phase 2.

- [ ] T045 [P] [US3] Add `test/e2e/fixtures/syslog-sink.yaml`: a Deployment and Service on TCP 514 running a digest-pinned image (e.g. `alpine/socat`) that prints every received line to stdout. Use a fixed name, `e2e-syslog-sink`.
- [ ] T046 [P] [US3] Add `test/e2e/fixtures/frps.yaml`: an frp server Deployment and Service, with a digest-pinned image whose version matches the `frpc` that `tunnel/Dockerfile.frp` installs. Include a token Secret, the bind port, and the dashboard API port with fixed credentials.
- [ ] T047 [US3] Write `test/e2e/optional_components_e2e_test.go` with `TestOptionalComponents_Lifecycle`. It calls `t.Parallel()`, owns its cluster, and runs these subtests in order:
  - `disabled`: none of the three components has a Deployment or Service.
  - `enable`: apply T045 and T046, create the shared auth Secret, then `helm upgrade --reuse-values` with `mcpServer.enabled=true`, `api.audit.webhook.syslogBridge.enabled=true`, `…syslog.addr=e2e-syslog-sink:514`, and `…authSecretRef.name`. Wait for rollout.
  - `mcp`: exec `/mcp-server serve` over stdio. `initialize` succeeds. `tools/list` returns exactly the six read tools. `list_gameplane_resources` for gameservers includes a seeded GameServer. `get_gameplane_resource` for `clusters` returns an error result.
  - `syslog`: create a role through the API with one admin login, then poll the sink's logs for an RFC 5424 line with APP-NAME `gameplane-audit` naming that role. An unauthenticated POST to the bridge returns 401.
  - `tunnel-frp`: create a busybox GameServer with `spec.tunnel` pointing at frp `e2e-frps`. Assert:
    - `TunnelReady=True`
    - the frps dashboard API lists the proxy
    - the pod has 0 restarts over 60 s
    - after `kubectl exec … kill` of `frpc`, the process returns and the pod still has 0 restarts
  - `core-unaffected`: a GameServer reaches `Running` through the existing helper, and a restricted user gets 403 on an admin route (second login).

  Use unique names and `t.Cleanup`. Use the `e2e-test-authoring` skill.
- [ ] T048 [US3] Add `bucket_optional_components` to `test/e2e/buckets.sh`, containing `TestOptionalComponents_Lifecycle`. Add a comment that it owns its cluster through `helm upgrade` and its login budget is 2, and add the bucket to `bucket_names`. Depends on T047.
- [ ] T049 [US3] In `.github/workflows/ci.yaml`:
  - add `optional-components` to the `bucket:` lists of `e2e-go` and `e2e-go-arm64`, with an `include` entry that mirrors `telemetry`'s (its own cluster and timeout)
  - extend the three `e2e (operator|…|telemetry) /` regexes in the `report` script (`JOB_MATCHERS` and the bucket parser) with `|optional-components`

  Depends on T048.
- [ ] T050 [US3] After the first green run on both arches, flip G-13, G-14, G-15 and G-18.

**Checkpoint**: SC-003 is met. All four documented optional components have E2E coverage.

---

## Phase 6: User Story 4 – Unit coverage gaps (Priority: P2)

**Goal**: 100 % of `secrets_managed.go`, and ≥ 80 % lines for ConsoleShell, CloneServerDialog and ServerActionsMenu (research R8, SC-004).

**Independent Test**: the `go (api / amd64)` coverage artifact shows `secrets_managed.go` at 100 %, and the `web` job's lcov shows each of the three components at ≥ 80 % lines.

**PR**: `test: unit coverage for managed secrets and console shell`. Independent of every other phase.

- [ ] T051 [P] [US4] Write `api/internal/handlers/secrets_managed_test.go`. Build a `kube.Client` on `k8s.io/client-go/kubernetes/fake`, following `registry_secret_test.go`. Inject errors with `PrependReactor`. Cover every case in research R8:
  - create carries both labels
  - `AlreadyExists` with the feature label → merge patch and `managed-by` re-asserted
  - `AlreadyExists` without the label → `errNotManagedSecret`, Secret untouched
  - Create, Get and Patch errors propagate
  - delete with both labels
  - delete with either label missing → `apierrors.IsNotFound`
  - Get and Delete errors propagate

  Use table-driven tests with `t.Parallel()`.
- [ ] T052 [P] [US4] Write `web/src/routes/tabs/ConsoleShell.test.tsx`. Render `ConsoleShell` with a stub `ConsoleHandle` (`vi.fn()` for `clear`, `download`, `toggleFullscreen` and `sendCommand`, and a `hostRef`). Cover:
  - each `WSStatus` label: LIVE, connecting…, reconnecting…, offline
  - Enter sends the trimmed command and clears the input; an empty input sends nothing
  - ArrowUp/ArrowDown recall, including restoring the unsent draft
  - the history cap of 100
  - each toolbar button calls the matching handle function
  - the `closed` status state

  Use `@testing-library/user-event` the same way `Console.test.tsx` does.
- [ ] T053 [US4] After T051 and T052 are green in CI, read the `web` job's lcov for `src/components/server/CloneServerDialog.tsx` and `src/components/server/ServerActionsMenu.tsx`.
  - Any file under 80 % lines, or missing a "cancel discards changes" case or an API-error case: add only the missing cases to `CloneServerDialog.test.tsx` or `ServerActionsMenu.test.tsx`.
  - Otherwise: record the measured percentages as evidence.

  Flip G-19 to G-22.

**Checkpoint**: SC-004 is met. Module thresholds stay unchanged.

---

## Phase 7: User Story 5 – Postgres E2E (Priority: P3)

**Goal**: the `api-auth`, `api-roles` and `api-rbac` buckets re-run against a Postgres-backed API in kind on every e2e-scoped PR (OD-3). The upgrade path is proven at the DB layer (research R9).

**Independent Test**: `e2e postgres / amd64 (kind)` is green for all three runs. `api (postgres)` is green, including the new baseline-upgrade test.

**PR**: `test: postgres-backed e2e`. Depends on Phase 2 (T008–T010).

- [ ] T054 [P] [US5] Add `TestPostgres_MigrateFromFrozenBaseline` to `api/internal/db/db_postgres_test.go`. Under the existing `GAMEPLANE_TEST_POSTGRES_DSN` skip guard it:
  1. Creates a fresh schema.
  2. Applies only the frozen `migrations/postgres/001`–`012` files.
  3. Inserts a user, a role binding, a share link and an audit row with the 012-era columns.
  4. Runs `Store.Migrate` to head.
  5. Asserts the rows read back unchanged through the current store API and that `schema_migrations` lists every version.
- [ ] T055 [P] [US5] Add `deploy/kind/postgres.yaml`: a single-replica PostgreSQL 17 StatefulSet, Service and Secret (`gameplane-postgres`) in the release namespace. Pin the image by the same digest as the `api-postgres` job's service.
- [ ] T056 [US5] Add a `postgres` profile to `deploy/kind/e2e.sh`:
  1. Apply `postgres.yaml` and wait for `pg_isready` with `kubectl exec`.
  2. Retag `gameplane-test/api:e2e-postgres` as `gameplane-test/api:${TAG}` before `kind load`.
  3. Install with every `e2e` value plus `--set api.db.driver=postgres` and `--set api.db.dsn=<from Secret>`.

  Follow contracts/e2e-additions.md. Depends on T055.
- [ ] T057 [US5] Add an `e2e-postgres` job named `e2e postgres / amd64 (kind)` to `.github/workflows/ci.yaml`, modeled on `e2e-capture-overhead`'s step list but without `continue-on-error`. Settings:
  - `needs: [changes, build-images]`
  - `if: needs.changes.outputs.e2e == 'true'`
  - brings the cluster up with `./deploy/kind/e2e.sh up "$CLUSTER" postgres`
  - one step asserting the API Deployment args contain `--db-driver=postgres`
  - three `go test -tags=e2e` steps, using `buckets.sh regex api-auth`, then `api-roles`, then `api-rbac`
  - the dump-on-failure step

  Register it in `report`. Depends on T056.
- [ ] T058 [US5] After the first green run, flip G-23 and G-24.

**Checkpoint**: SC-005 is met.

---

## Phase 8: Polish & cross-cutting

- [ ] T059 [P] Update `docs/contributing.md`, under "CI and workflows", with one short paragraph per new gate: what fails it, and how to fix it without suppressing it. Update "E2E testing" for the `optional-components` bucket and the `e2e-postgres` job. CI's `make check-links` verifies the new anchors.
- [ ] T060 [P] Grep the full feature diff for forbidden directives: `git diff origin/master -- . ':!specs' | grep -nE "nolint|eslint-disable|@ts-ignore|hadolint ignore|trivyignore|allow-ghsas"` must print nothing (SC-006).
- [ ] T061 Confirm `coverage-gaps.md` has no `open` or `in-progress` rows and that `make check-coverage-gaps` passes in CI. Then run `/speckit-analyze` on this folder.
- [ ] T062 Once every PR above is merged and every task is checked or withdrawn, `git mv specs/013-expand-test-coverage specs/done_013-expand-test-coverage` and update every in-repo reference (including `hack/check-coverage-gaps.sh`'s default path) in one `docs:` commit (Constitution IV).

---

## Dependencies & execution order

- **Setup (T001–T006)** comes first; T001 blocks everything.
- **Foundational (T007–T011)** blocks T013, T029–T031, T036, Phase 5 and Phase 7. It does not block Phase 4 or Phase 6.
- **US1** gates are sequential PRs (G5 → G1 → G3 → G2 → G4 → G6), but their script and fixture tasks marked [P] can all be written up front.
- **US2** depends only on Setup. T037 → T038, then T039–T043 in parallel.
- **US3** depends on Foundational. T045/T046 run in parallel, then T047 → T048 → T049.
- **US4** depends on nothing beyond Setup. T051 and T052 run in parallel.
- **US5** depends on T008–T010. T054 and T055 run in parallel, then T056 → T057.
- **Polish** runs after the stories it documents.

```text
T001 ─┬─ T002..T006 (gap record)
      ├─ T007..T011 (foundation) ─┬─ US1 gates needing images/scope (T013, T029, T036)
      │                           ├─ US3
      │                           └─ US5
      ├─ US1 scripts/fixtures [P]
      ├─ US2
      └─ US4
```

## Parallel examples

- **US1**: one haiku workflow writes T012, T015, T016, T018, T022, T023, T027, T028, T034 and T035 at once (separate files); one sonnet reviewer checks the batch diff against contracts/static-gates.md.
- **US2**: T039–T043 are five independent files and go to five haiku agents in one `parallel()`; one sonnet reviewer checks the diff.
- **US3**: T045 and T046 in parallel, then T047 alone.
- **US4**: T051 and T052 in parallel (Go and TS, no shared files).
- **US5**: T054 and T055 in parallel.

## Implementation strategy

1. **MVP**: Setup, Foundational, then US1 (the static gates). This is the lowest cost and highest value, and every later PR runs under the new gates.
2. US2 and US4 next, in parallel with the later US1 gate PRs: P1 dashboard flows and cheap unit wins.
3. US3, then US5, each as one PR.
4. Polish and archival.

Each PR closes its gap rows, so `coverage-gaps.md` always shows true progress.
