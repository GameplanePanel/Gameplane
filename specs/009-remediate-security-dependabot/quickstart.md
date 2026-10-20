# Feature 009: Validation & Run Guide

How a maintainer proves feature 009 (security remediation + Dependabot merges) actually worked.
This is NOT an implementation guide — no fix code, no migration bodies, no full test suites.
Every test/lint/build result is observed via GitHub Actions CI runs, queried with the `gh` CLI.

## Prerequisites

- `gh` authenticated with `security_events` scope to read and dismiss code-scanning alerts
- Write access to `GameplanePanel/Gameplane`
- Most `gh` invocations include `-R GameplanePanel/Gameplane` (cwd drift into `modules/` submodule silently retargets `gh` at the wrong repo). **Exception**: `gh api` commands do NOT accept `-R`; the endpoint path already specifies the repo.
- The feature branch `009-remediate-security-dependabot` is checked out and has received commits for the fixes
- GitHub Actions CI is running and accessible

## Baseline Capture

Record the starting state before any changes merge to master.

### Code Scanning Alerts

List all open code-scanning alerts:

```bash
gh api repos/GameplanePanel/Gameplane/code-scanning/alerts \
  --jq '.[].number' \
  --paginate
```

**Expected baseline (2026-08-28)**: 14 open alerts (rules 1–14 from BRIEF.md).

For details on each alert, query individually:

```bash
gh api repos/GameplanePanel/Gameplane/code-scanning/alerts/N \
  --jq '{number: .number, rule: .rule.id, state: .state, location: .most_recent_instance.location}'
```

Replace `N` with alert numbers 1–14 to confirm rules, locations, and all states are `open`.

### Dependabot Pull Requests

List all open Dependabot PRs:

```bash
gh pr list \
  -R GameplanePanel/Gameplane \
  --author='dependabot[bot]' \
  --state=open \
  --json number \
  --jq '.[].number' \
  -L 100
```

**Historical baseline (2026-08-28)**: 21 open PRs:
- Go: 10 PRs (#263, #265, #267, #269, #271, #273, #274, #276, #279, #281)
- npm: 11 PRs (#262, #264, #266, #268, #270, #272, #275, #277, #278, #280, #283)

**Current state (2026-08-29)**: 1 open PR remains:
- **#272** (typescript 6.0.3→7.0.2) — npm, upstream-blocked (no @typescript-eslint release accepts TS 7; npm ci dies at ERESOLVE)

**Merged/closed (2026-08-29)**: 20 PRs addressed:
- 19 merged: #283, #281, #280, #279, #278, #277, #276, #275, #274, #273, #271, #270, #268, #267, #266, #265, #264, #263, #262
- 1 closed without merge: #269 (golang.org/x/net — outcome achieved transitively via other merges)

## Scenario A: Alert Remediation Verified

Alert #4 (real defect) requires a code fix. Alerts #1–#2 and #7–#14 (false positives) are addressed by refactoring; dismissal is deferred until post-merge CodeQL analysis on master confirms whether the refactor cleared the alert. Alerts #5 and #6 are false positives where refactoring is attempted, but dismissal is the expected outcome. Alert #3 is closed by dismissal only, with no code change. After each commit merges to master, CodeQL runs on the default branch and reports alert state as either `fixed` (refactored) or `dismissed`.

### Observing CI on the Feature Branch

Watch the CI run on `009-remediate-security-dependabot` to confirm the fixes do not break existing tests:

```bash
gh run list \
  -R GameplanePanel/Gameplane \
  --branch=009-remediate-security-dependabot \
  --workflow=ci.yaml \
  -L 1 \
  --json databaseId,status,conclusion \
  --jq '.[0] | {id: .databaseId, status: .status, conclusion: .conclusion}'
```

Wait for this run to complete. The `lint` job must be green for all 14 Go modules (netguard, gameaction, gameproto, operator, api, agent, audit-syslog-bridge, telemetry-receiver, sentinel, capture-sidecar, mcp-server, svcutil, tunnel, test/e2e with appropriate build tags).

The `go` matrix job must be green for the modules that changed (at minimum: agent, api). Coverage gates must still pass:
- `agent`: ≥90%
- `api`: ≥80%

### Verifying Alert Closure on Master

Once the fixes are merged to master, CodeQL's default-branch analysis must run. By design, GitHub's default-setup CodeQL runs weekly (Monday 03:00 UTC); on-commit triggers are not available.

**Within ~48 hours of merge to master**, query alert state again:

```bash
gh api repos/GameplanePanel/Gameplane/code-scanning/alerts \
  --jq '.[] | select(.state != "open") | {number: .number, rule: .rule.id, state: .state}'
```

**Expected outcome (per SC-001)**: All 14 alerts transition to `fixed`.

If alerts remain `open` after the next CodeQL run (force a manual run via the Actions UI if needed), query alert details to diagnose:

```bash
gh api repos/GameplanePanel/Gameplane/code-scanning/alerts/N
```

## Scenario B: TLS Verification Fix Verified

**Alert #4** (go/disabled-certificate-check in test/e2e/internal/satisfactory/app.go:188) is a real defect: the code sets `InsecureSkipVerify: true` unconditionally with no loopback guard. The fix adds a loopback guard mirroring agent/internal/rcon/satisfactory.go:220-226.

**Unit test coverage**: The guard is exercised by a unit test in test/e2e/internal/satisfactory/ that dials a loopback and a non-loopback address and verifies the TLS config is set correctly.

**Note on e2e**: The satisfactory bot test (satisfactory_bot_e2e_test.go) lives in the `bot-heavy` bucket, which never runs in CI (it's resource-heavy). The unit test is what CI actually verifies, run by the `go-e2e-unit` job.

**Observing CI**:

```bash
gh run list \
  -R GameplanePanel/Gameplane \
  --branch=009-remediate-security-dependabot \
  --workflow=ci.yaml \
  -L 1 \
  --json url \
  --jq '.[0].url'
```

Follow the job URL and confirm `go-e2e-unit` passes. This job runs:

```
cd test/e2e && go test -short -race -tags=e2e ./...
```

The `-short` flag skips full e2e suites but includes unit tests for guard logic.

**Expected outcome**: `go-e2e-unit` job is green. The guard rejects non-loopback addresses and permits loopback ones, as evidenced by the passing unit test.

## Scenario C: Path Confinement Verified

The fixes to agent/internal/mods/mods.go (alerts #7–13) strengthen path validation in unzip, remove, download, and archive-swap operations. Existing e2e tests in the `api-mods` bucket (api_mods_e2e_test.go and api_mods_upload_e2e_test.go) must still pass.

**New test coverage** (mandatory per Principle I): Add negative-case e2e tests for:
1. Zip archive with path traversal entries (`../../../etc/passwd`) — extraction must reject and confine output to the sandbox
2. Symlink entries pointing outside the sandbox — extraction must reject or resolve-check the target
3. Absolute paths in archive members — extraction must reject

These tests MUST be added to a bucket in test/e2e/buckets.sh or the e2e-buckets CI job will fail. The `api-mods` bucket is appropriate (it already covers mod upload/download). Per convention:
- Tests MUST call `t.Parallel()`
- Each test MUST use unique resource names (e.g., suffix with test name)
- Respect the ~7-admin-login budget for the bucket

**Observing CI**:

```bash
gh run list \
  -R GameplanePanel/Gameplane \
  --branch=009-remediate-security-dependabot \
  --workflow=ci.yaml \
  -L 1 \
  --json url \
  --jq '.[0].url'
```

Confirm the `e2e-buckets` job is green (it verifies all tests are bucketed and counts match).

Confirm `e2e-go e2e-mods` job (the api-mods bucket) is green on both amd64 and arm64. It runs:

```
cd test/e2e && go test -race -tags=e2e -run ^TestAPI_Mods ./internal/bot
```

**Expected outcome**: api_mods_e2e_test.go and api_mods_upload_e2e_test.go pass, new negative tests pass, and e2e-buckets verification is satisfied.

## Scenario D: Audit Pagination Bound Verified

**Alert #14** (go/uncontrolled-allocation-size in api/internal/audit/audit.go:834) is technically a false positive at the `Auditor.Page` function level (it clamps `limit` before the allocation), but there's a real defect: the handler at api/internal/handlers/audit.go:25 parses the raw `limit` parameter with `strconv.Atoi` and does NOT clamp it before passing to `Auditor.Page`. The fix clamps the handler's parsed limit.

**Existing test coverage**: TestAPI_AuditPaginationAndFilter (api-auth bucket) exercises pagination with valid and invalid limits.

**Observing the fix**:

The handler now clamps the limit parameter before calling `Auditor.Page`. Verify in the diff that api/internal/handlers/audit.go:25 (or nearby) adds bounds-checking.

**Observing CI**:

```bash
gh run list \
  -R GameplanePanel/Gameplane \
  --branch=009-remediate-security-dependabot \
  --workflow=ci.yaml \
  -L 1 \
  --json url \
  --jq '.[0].url'
```

Confirm `e2e-go e2e-auth` job (the api-auth bucket, which contains TestAPI_AuditPaginationAndFilter) is green.

**Query the audit endpoint against deployed test cluster to verify**:

Once merged to master and deployed to a test cluster, query the audit endpoint via kubectl port-forward with extreme limits and confirm the response is bounded:

```bash
# These queries run against a deployed test cluster instance (never on CI, per Principle VI).
# Assuming the API is port-forwarded to :8000 and an admin session is established.

# Query with limit=-1 (should be clamped to default 100)
curl -s http://localhost:8000/api/admin/audit?limit=-1 | jq '.[] | length'

# Query with limit=0 (should be clamped to default 100)
curl -s http://localhost:8000/api/admin/audit?limit=0 | jq '.[] | length'

# Query with limit=999999999 (should be clamped to max 500)
curl -s http://localhost:8000/api/admin/audit?limit=999999999 | jq '.[] | length'

# Normal query with limit=50
curl -s http://localhost:8000/api/admin/audit?limit=50 | jq '.[] | length'
```

All responses must be bounded at max 500 entries. The API pod memory must not spike during the 999999999 request.

**Expected outcome**: Audit responses are bounded, API pod is stable, TestAPI_AuditPaginationAndFilter passes.

## Scenario E: Dependency Bump Verification

All 21 Dependabot PRs must be addressed individually (per user decision) and verified green on CI before acceptance. TypeScript 7 (#272) is deferred as upstream-blocked after the other 20 are resolved—19 merged and 1 (#269) closed without merging.

### Per-PR Verification Pattern

For each Dependabot PR (except #272):

1. **Check PR status**:

```bash
gh pr view PR_NUMBER \
  -R GameplanePanel/Gameplane \
  --json number,title,state,statusCheckRollup \
  --jq '{number: .number, title: .title, state: .state, checks: .statusCheckRollup}'
```

Expected: `state` is `OPEN`, `checks` shows all required CI checks green (or blue, if not yet run).

2. **Wait for CI to complete** (if not already complete):

```bash
gh run list \
  -R GameplanePanel/Gameplane \
  --branch="dependabot/go_modules_..." \
  --workflow=ci.yaml \
  -L 1 \
  --json status,conclusion \
  --jq '.[0] | {status: .status, conclusion: .conclusion}'
```

Expected: `conclusion` is `success`.

3. **Merge the PR** (maintainer-only):

```bash
gh pr merge PR_NUMBER \
  -R GameplanePanel/Gameplane \
  --admin \
  --merge
```

The `--admin` flag is required because master has a ruleset with an "update" rule that blocks plain merges. The user has authorized `--admin` for green PRs.

4. **Verify the version landed** in the appropriate go.mod files:

After merge, query the master branch. Note: this repository is a Go workspace (go.work at the root) with no root go.mod, so you must specify a module directory in the path.

```bash
gh api repos/GameplanePanel/Gameplane/contents/<module>/go.mod \
  --jq '.content' | base64 -d | grep "<library-name>"
```

Example for #281 (sqlite 1.55.0 → 1.57.0):

```bash
gh api repos/GameplanePanel/Gameplane/contents/api/go.mod \
  --jq '.content' | base64 -d | grep "modernc.org/sqlite"
```

Expected: line shows `modernc.org/sqlite v1.57.0` (or a compatible indirect version).

### Go Dependencies (10 PRs)

| PR | Library | Modules |
|---|---|---|
| #281 | modernc.org/sqlite 1.55.0→1.57.0 | api, capture-sidecar, mpc-server, operator, test/e2e |
| #279 | sigstore/cosign/v2 2.6.4→2.6.5 | capture-sidecar, operator, test/e2e |
| #276 | gopacket 1.6.1→1.7.1 | capture-sidecar, test/e2e |
| #274 | golang.org/x/mod 0.38.0→0.40.0 | agent, api, capture-sidecar, mcp-server, operator, sentinel, test/e2e |
| #273 | minio/minio-go/v7 7.2.1→7.3.0 | agent, api, capture-sidecar, mcp-server, operator, sentinel, telemetry-receiver, test/e2e |
| #271 | k8s.io/api 0.36.3→0.36.4 | agent, api, capture-sidecar, mcp-server, operator, sentinel, test/e2e |
| #269 | golang.org/x/net 0.57.0→0.58.0 | agent, api, capture-sidecar, mcp-server, operator, sentinel, test/e2e |
| #267 | go-chi/chi/v5 5.3.1→5.3.2 | agent, api, capture-sidecar, operator, test/e2e |
| #265 | google/go-containerregistry 0.21.7→0.22.0 | agent, api, capture-sidecar, mcp-server, operator, sentinel, telemetry-receiver, test/e2e |
| #263 | sigstore/sigstore 1.10.8→1.10.9 | capture-sidecar, operator, test/e2e |

**Special case: #263 was initially blocked by 1 failing check** (`lint (operator)`: staticcheck SA1019 on deprecated sigstore/pkg/fulcioroots API). The check was cleared by PR #287 (T071), which migrated operator/internal/verify/verify.go to sigstore-go/pkg/tuf. #263 merged on 2026-08-29 after T071 landed. **No action needed; included in 19 merged PRs above.**

### npm Dependencies (11 PRs)

| PR | Library | Status |
|---|---|---|
| #283 | brace-expansion 1.1.14→1.1.18, js-yaml 4.3.0→4.3.2 (security) | green — MERGE |
| #280 | @types/react-dom 19.2.3→19.2.4 | green — MERGE |
| #278 | vitest 4.1.10→4.1.11 | green — MERGE |
| #277 | @vitejs/plugin-react 6.0.4→6.1.0 | green — MERGE |
| #275 | @types/node 26.1.2→26.2.0 | green — MERGE |
| #272 | typescript 6.0.3→7.0.2 (major) | 4 FAILING CHECKS — DEFER |
| #270 | @tanstack/react-router 1.170.18→1.170.32 | green — MERGE |
| #268 | @eslint/js 9.39.5→10.0.1 (major) | green — MERGE |
| #266 | @playwright/test 1.62.0→1.62.1 | green — MERGE |
| #264 | @testing-library/jest-dom 7.0.0→7.0.1 | green — MERGE |
| #262 | @typescript-eslint/parser 8.65.0→8.67.0 | green — MERGE |

**Exception**: PR #272 (TypeScript 7, upstream-blocked) is deferred to a separate feature branch and phase. The other 20 Dependabot PRs are resolved: 19 merged and 1 (#269) closed without merging. #272 will be handled in a follow-up once the upstream blocker is resolved.

**Observing CI on a merged npm PR**:

```bash
gh run list \
  -R GameplanePanel/Gameplane \
  --branch=master \
  --workflow=ci.yaml \
  -L 1 \
  --json status,conclusion \
  --jq '.[0] | {status: .status, conclusion: .conclusion}'
```

After merging an npm PR, confirm the next CI run on master is green, especially the `web` job (npm ci, build, vitest+coverage) and `web-e2e-mock` job (Playwright).

## Final Acceptance

Once all commits are merged to master and CI has run, verify the feature is complete against the success criteria.

### SC-001: Alerts Resolved

Query open alerts:

```bash
gh api repos/GameplanePanel/Gameplane/code-scanning/alerts \
  --jq '[.[] | select(.state == "open")] | length'
```

**Expected**: 0 open alerts.

If any alerts remain open:
- Query their details to confirm they are not regressions
- Verify the most recent CodeQL run on master has completed (query Actions)
- If the run has completed and alerts persist, diagnose the code path — the fix may not have cleared the alert as expected

### SC-002: Dependabot PRs Resolved

Query open Dependabot PRs:

```bash
gh pr list \
  -R GameplanePanel/Gameplane \
  --author='dependabot[bot]' \
  --state=open \
  --json title,number \
  --jq '[.[] | select(.title | test("^chore:"))] | length'
```

**Expected**: 0 (all merged) or 1 (#272 deferred, upstream-blocked).

If 1 is open, confirm it is #272:

```bash
gh pr list \
  -R GameplanePanel/Gameplane \
  --author='dependabot[bot]' \
  --state=open \
  --json number \
  --jq '.[].number'
```

Map to spec success criterion SC-002: "100% of open Dependabot PRs are merged or resolved." The baseline is 21 PRs (#262–#281 + #283 security). As of 2026-08-29:
- 19 PRs merged (9 Go + 10 npm): #283, #281, #280, #279, #278, #277, #276, #275, #274, #273, #271, #270, #268, #267, #266, #265, #264, #263, #262
- 1 PR closed without merge: #269 (golang.org/x/net 0.57.0→0.58.0; outcome achieved transitively)
- 1 PR deferred (upstream-blocked): #272 (TypeScript 7, no @typescript-eslint release yet)
- Total: 21 of 21 accounted for — 19 merged, 1 closed without merging (#269, superseded transitively), 1 deferred as upstream-blocked (#272 TypeScript 7)

### SC-003, SC-004: Tests Green

Query the master branch's latest CI run:

```bash
gh run list \
  -R GameplanePanel/Gameplane \
  --branch=master \
  --workflow=ci.yaml \
  -L 1 \
  --json status,conclusion,createdAt \
  --jq '.[0] | {status: .status, conclusion: .conclusion, created_at: .createdAt}'
```

Confirm the run is recent (within the last few hours, indicating it ran post-merge) and `conclusion` is `success`.

Expected jobs (per CI config):
- `lint`: 14 Go modules, all green
- `go`: 13 modules x (amd64 + arm64), all green
- `web`: npm build, vitest, coverage ≥92%
- `web-e2e-mock`: Playwright, all green
- `helm`, `chart-template`: all green
- `go-e2e-unit`: all green
- `e2e-buckets`: verification pass
- `e2e-go`: 6 buckets x 2 arches, all green

SC-003 (unit tests) and SC-004 (e2e tests) are satisfied if `conclusion: success`.

### SC-005: Static Analysis Clean

Query the linting artifacts from the most recent master CI run. If available via the Actions API, retrieve the lint output and confirm zero `golangci-lint` warnings, zero `go vet` errors, zero ESLint errors, and zero TypeScript errors.

Alternatively, inspect the feature branch diff to confirm no new suppressions (no `//nolint`, `// eslint-disable-next-line`, etc.) were added to source files. (Matches inside `specs/**/*.md` are expected — the docs quote these directives while explaining the rule.)

```bash
git diff master...009-remediate-security-dependabot -- '*.go' '*.ts' '*.tsx' \
  | grep -E "//nolint|// eslint-disable|// @ts-ignore"
```

**Expected**: No matches (no suppressions added to source files).

### SC-006: Build Duration

Query the master CI run's total duration:

```bash
gh api repos/GameplanePanel/Gameplane/actions/runs \
  --jq '.workflow_runs[0] | {run_number: .run_number, run_time_minutes: ((.run_number * 0) + 42)}'  # Placeholder; actual query varies by run metadata
```

(Exact query depends on GitHub Actions API; use the UI if the CLI endpoint is not available.)

**Expected**: Total duration remains within historical budgets (typically ~60–90 minutes for a full matrix). No hung processes or timeouts.

## Verification Checklist

- [ ] **Baseline**: 14 open alerts, 21 open Dependabot PRs recorded
- [ ] **Scenario A**: Feature branch CI lint/go jobs green; alerts transition to `fixed` after merge to master (within ~48h CodeQL run)
- [ ] **Scenario B**: TLS guard unit test passes; `go-e2e-unit` job green
- [ ] **Scenario C**: api-mods e2e tests pass; new negative tests bucketed and passing; `e2e-buckets` verification green
- [ ] **Scenario D**: audit handler clamping confirmed in diff; TestAPI_AuditPaginationAndFilter passes; extreme limit queries bounded at 500
- [ ] **Scenario E**: 19 Go/npm PRs merged and 1 (#269) closed without merge; versions confirmed in go.mod/package.json; #272 deferred (upstream-blocked); CI green after each merge
- [ ] **Final**: 0 open alerts, 0 open Dependabot PRs (or 1 deferred), master CI green across all jobs
- [ ] **SC-001** through **SC-006**: All success criteria met
