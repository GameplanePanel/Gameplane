# Data Model: Expand Test Coverage

This feature stores no runtime data. Its "entities" are CI and test artifacts, and the fields below are what tasks and reviewers check them against.

## Static Analysis Gate

A CI job (or a step inside one) that blocks merge on a finding.

| Field | Type | Rule |
|---|---|---|
| `id` | kebab-case slug | Unique. One of `vuln-go`, `vuln-images`, `hadolint`, `dependency-review`, `codegen-drift`, `submodule-freshness` |
| `job_name` | string | The display name in `ci.yaml`. MUST NOT start with `go (` (report prefix matcher) |
| `trigger` | set of {`pull_request`, `push:master`, `schedule`, `workflow_call` from publish} | At least `pull_request` |
| `scope_output` | `ci_scope.py` output name | Required unless the gate runs on every PR |
| `failing_condition` | text | Concrete and tool-level, e.g. "reachable vuln", "HIGH/CRITICAL with a fix available" |
| `proof` | → Gate Fixture, or a URL for `dependency-review` | Required (FR-006) |
| `suppressions` | always empty | Any ignore file or inline directive is a contract violation (FR-008) |
| `in_report` | bool | MUST be true: listed in the `report` job's `needs`, `NEEDS_ORDER` and `JOB_MATCHERS` |

**State**: `planned` → `proposed` (PR open, first CI run lists pre-existing findings) → `proven` (the fixture proof is green, and the gate is red on the fail fixture and green on the pass fixture) → `blocking` (merged, every pre-existing finding fixed). A gate never merges in `proposed`.

## Gate Fixture

The input that proves a gate can fail.

| Field | Rule |
|---|---|
| `gate_id` | → Static Analysis Gate |
| `pass_input` / `fail_input` | Paths under `hack/testdata/<gate>/{pass,fail}/`, or a digest-pinned public image (`vuln-images`), or temp repos the script builds (`codegen-drift`, `submodule-freshness`) |
| `script` | `hack/check-<gate>_test.sh`. Exit 0 only when fail → non-zero **and** pass → zero |
| `ci_step` | The step that runs `script`, in the same job as the gate or in `e2e-buckets`-style proof steps |

**Invariant**: a fixture never introduces a real vulnerable dependency into a manifest that GitHub's dependency graph indexes (research R1, R4).

## E2E Test (added by this feature)

| Field | Rule |
|---|---|
| `name` | Go: `Test<Area>_<Behavior>`. Playwright: a file under `web/e2e/specs/live/` |
| `bucket` | Go: exactly one in `test/e2e/buckets.sh`. Playwright: runs in `e2e-web-live` |
| `arches` | Inherited from the bucket's job. Never an arm64-only job |
| `logins` | Counted; the per-cluster total stays ≤ per-IP burst 10 and ≤ 6 per username |
| `cleanup` | `t.Cleanup` / `afterEach` deletes every object the test created |
| `error_path` | US2 specs: one real-API error assertion |

## Coverage Gap

One row in the Coverage Gap Record.

| Field | Type | Values |
|---|---|---|
| `id` | `G-NN` | Stable, never reused |
| `category` | enum | `static`, `e2e-dashboard`, `e2e-optional`, `unit`, `e2e-postgres` |
| `gap` | text | What is missing |
| `story` / `fr` | refs | `US1`…`US5`, `FR-00x` |
| `priority` | enum | `P1`, `P2`, `P3` (from the story) |
| `status` | enum | `open`, `in-progress`, `closed`, `unit-only-by-design` |
| `evidence` | text | Required when `closed`: a test path and/or CI job name, plus a proof link (PR or run URL) |
| `notes` | text | Optional |

**Transitions**: `open` → `in-progress` (PR opened) → `closed` (PR merged, CI green). `unit-only-by-design` is reachable only from `open`, and only with a reason in `notes` (e.g. the Tailscale/playit tunnel providers, research R7). A `closed` row is never reopened. A regression is filed as a new row citing the old one.

## Coverage Gap Record

The single file `specs/013-expand-test-coverage/coverage-gaps.md` (format: [contracts/coverage-gap-record.md](contracts/coverage-gap-record.md)). It contains every Coverage Gap row, seeded from research R0, plus a status summary line computed by hand on each update and checked by `hack/check-coverage-gaps.sh`.

## Optional Component

| Component | Chart toggle | Default | E2E owner |
|---|---|---|---|
| mcp-server | `mcpServer.enabled` | false | `TestOptionalComponents_Lifecycle` |
| audit-syslog-bridge | `api.audit.webhook.syslogBridge.enabled` | false | `TestOptionalComponents_Lifecycle` |
| tunnel (frp) | `GameServer.spec.tunnel` | absent | `TestOptionalComponents_Lifecycle` |
| tunnel (tailscale, playit) | `GameServer.spec.tunnel` | absent | unit only by design |
| telemetry-receiver | `api.telemetry.receiver.enabled` | false | `TestTelemetryLifecycle` (spec 022) |

## Relationships

```text
Static Analysis Gate 1──1 Gate Fixture
Coverage Gap Record 1──* Coverage Gap
Coverage Gap *──0..1 Static Analysis Gate | E2E Test | unit test file   (via evidence)
Optional Component 1──1..* E2E Test
```
