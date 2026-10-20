# Feature Specification: Expand Test Coverage

**Feature Branch**: `013-expand-test-coverage`

**Created**: 2026-09-02

**Status**: Draft

**Input**: User description: "More test E2E, static, etc...."

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Static analysis gates close high-priority security gaps (Priority: P1)

A maintainer preparing a release needs confidence that the codebase is free of known
vulnerabilities and follows established static analysis standards before code reaches
production. Go dependency vulnerabilities, unsafe Docker practices, and unchecked module
freshness MUST be caught automatically in CI, not discovered in a security audit or
incident.

**Why this priority**: Static analysis is the lowest-cost, highest-ROI verification layer.
The project has GitHub Actions runner capacity for these gates; the gaps (Go dependency
vulnerability scanning, container image scanning, Dockerfile linting) are high-value blockers
that prevent known CVEs from merging; and supply-chain integrity (dependency drift, image
provenance) is directly tied to release readiness.

**Independent Test**: Can be tested independently by confirming each new gate blocks a
commit containing the known-bad condition it is designed to catch (e.g., a go.mod with
a vulnerable transitive, an insecure Dockerfile instruction), and that every gate's
blocking behavior is proven in CI before the gate is declared complete.

**Acceptance Scenarios**:

1. **Given** a new static analysis gate configured in CI, **When** a test commit
   introducing a violation of that gate is pushed, **Then** the gate fails and blocks
   merge until the violation is fixed.
2. **Given** a gate that gates on findings pre-existing in the codebase, **When** that
   gate is activated, **Then** it is either scoped to new code only (delta mode) or
   pre-existing findings are fixed, so the gate does not become a paper gate that
   cannot fail.
3. **Given** all static gates green, **When** a release is prepared, **Then** the
   maintainer can point to a single CI run showing zero findings across all gates
   (dependency vulnerabilities, container images, Dockerfiles, module freshness, generated-file drift)
   rather than running separate manual checks.

---

### User Story 2 - Dashboard E2E against a real cluster covers user-facing flows (Priority: P1)

An operator launching a Gameplane cluster needs to know that the dashboard's login-to-
action flows work end-to-end against a real cluster before they trust the UI for
production. Mock-only tests catch component-level bugs but miss API integration,
auth lifecycle, and real-world error handling. Every major user-facing flow MUST have
a live-cluster E2E test that proves the UI works against a real API and that state
changes persist.

**Why this priority**: The dashboard is the user-facing gateway to Gameplane; a UI bug
that only appears under real-cluster conditions (auth token refresh, permission
changes, API error retries) is invisible to mock tests and can disable an operator
until a fix ships. Constitution Principle I (E2E-Tested Delivery) mandates live
verification of user-facing paths. The current 4 live Playwright tests vs. 17 mock-only
tests is an 80% coverage gap.

**Independent Test**: Can be tested independently by creating a new live browser E2E
test against a fresh kind cluster (the existing live browser E2E job), performing one complete
flow (login → create server → open console → stop server → verify state persists), and
confirming it passes on a real API and fails when API/auth behaves incorrectly (e.g.,
revoked token, permission denied).

**Acceptance Scenarios**:

1. **Given** a major dashboard flow (create server, modify admin settings, manage users,
   assign roles, delete server, restore backup), **When** a live browser E2E test runs
   it against a real cluster, **Then** the flow completes successfully and the state
   change is visible on a fresh page load (auth token refresh, RBAC enforcement, data
   persistence verified).
2. **Given** the same flow, **When** the real API is in an error state (4xx auth error,
   5xx failure, rate limit), **Then** the UI renders an appropriate error message and
   the test fails deterministically (not a mock-only happy-path test).
3. **Given** an admin dashboard flow (configure notifications, manage role bindings,
   update mod registries), **When** the flow is tested live against a real cluster,
   **Then** state changes (new webhook URL, user role change) survive a cluster restart
   or page reload.

---

### User Story 3 - Optional components (MCP server, tunnel, syslog, telemetry) are E2E tested (Priority: P2)

A maintainer needs to know that optional components (read-only MCP server, relay tunnel
supervisor, audit syslog bridge, telemetry receiver) actually work in deployed clusters
before they are documented as supported. These are production features with real users
relying on them; they MUST be tested E2E, not just assumed-working based on unit tests.

**Why this priority**: These are now documented, distributed features (README.md lines
121-122 advertise them). A broken optional component is a production incident for users
who enable it, and there is currently zero E2E verification they work at all. This is
a medium-term risk that should be closed before more users enable these features.

**Independent Test**: Can be tested independently per component by confirming each has
at least one E2E test exercising the component's primary operation in a real cluster
(MCP server read-only tool, tunnel pod lifecycle and relay config, syslog delivery to
a real syslog sink, telemetry receiver metrics collection), and that the test runs in
a CI bucket.

**Acceptance Scenarios**:

1. **Given** a deployed optional component (mcp-server with read-only ClusterRole,
   tunnel with relay config, syslog-bridge with syslog endpoint, telemetry-receiver),
   **When** its E2E test runs against the kind cluster, **Then** the component's primary
   function works (MCP tool returns data, relay process supervises without crashing,
   syslog messages are delivered, metrics are collected).
2. **Given** the same component, **When** it is disabled (config.enabled: false),
   **Then** the test verifies it does not run (no pod created, no events emitted).
3. **Given** all optional components enabled, **When** a full E2E run executes,
   **Then** none of them interfere with core Gameplane functionality (server creation,
   scheduling, RBAC still work).

---

### User Story 4 - Unit coverage gaps are closed for critical untested components (Priority: P2)

A maintainer reviewing code needs to know that critical paths like secret management
(upsertLabelledSecret), RBAC enforcement, and interactive UI components (console shell,
server actions menu) are covered by tests. Gaps in these areas are high-risk: secrets
are a security boundary, RBAC is an authorization gate, and UI components are the
user's primary interface to the system.

**Why this priority**: These are not "nice to have" tests; they are high-risk code paths
that defects in would directly impact security, correctness, or user experience. The
survey identifies 92-line secret management code and three major untested UI components
(ConsoleShell, CloneServerDialog, ServerActionsMenu) that should have test coverage.

**Independent Test**: Can be tested independently by confirming a new test file exists
and passes for each component, exercising the component's happy path and at least one
error scenario (e.g., secret upsert succeeds, secret upsert fails and error is
handled; console shell connects, console shell disconnects ungracefully).

**Acceptance Scenarios**:

1. **Given** api/internal/handlers/secrets_managed.go (92 lines, 2 exported functions),
   **When** new test coverage is added, **Then** both upsertLabelledSecret() and
   deleteManagedSecret() have test cases covering success and error paths.
2. **Given** web/src/routes/tabs/ConsoleShell.tsx (77 lines, interactive console component),
   **When** a new ConsoleShell.test.tsx is written, **Then** the test exercises
   connection, command dispatch, and disconnection scenarios.
3. **Given** the other untested interactive UI components (CloneServerDialog,
   ServerActionsMenu), **When** test files are added, **Then** each tests the
   open/submit/cancel flow and at least one error case (e.g., "cancel" discards changes).

---

### User Story 5 - Postgres database support is E2E tested (Priority: P3)

A maintainer deploying Gameplane in an enterprise environment may prefer PostgreSQL
over SQLite for HA or compliance reasons. Today, PostgreSQL is build-tagged but not
E2E verified — code paths assuming SQLite-specific syntax could break silently when
Postgres is enabled. E2E coverage against a real Postgres instance MUST verify
migrations, queries, and connection pooling work identically to SQLite before Postgres
is documented as supported.

**Why this priority**: Postgres support is labeled work-in-progress (docs/roadmap.md
lines 228-234); it is not yet promised. However, given that the build infrastructure
exists and some users may already be experimenting, adding e2e coverage now prevents
future regressions and enables faster stabilization toward a Postgres-ready release.

**Independent Test**: Can be tested independently by running the existing e2e suite
against a Postgres-enabled API build in a separate CI bucket (or gated by a manual
trigger), exercising the same GameServer/Backup/RBAC flows as SQLite, and confirming
migrations and queries work identically.

**Acceptance Scenarios**:

1. **Given** the API built with `-tags=postgres` against a Postgres instance, **When**
   migrations run at startup, **Then** all tables are created and the API is ready.
2. **Given** the same Postgres-enabled API in an e2e test, **When** CRUD operations
   (create user, create GameServer, list backups, audit log) are executed, **Then**
   results match the behavior of SQLite-backed runs.
3. **Given** a Postgres instance with an existing schema from a prior API version,
   **When** the API starts and runs migrations, **Then** the upgrade path succeeds and
   data is not corrupted.

---

### Edge Cases

- **New gates that cannot fail are untrustworthy.** Every new static analysis gate MUST
  be proven able to fail (i.e., it blocks a test commit with a known violation) before
  it is declared passing. A gate that never fails is not a gate; it is lint noise.
- **Flaky E2E tests destroy confidence.** Any E2E test added MUST not be flaky; it must
  pass deterministically when the code is correct and fail deterministically when the
  code is broken. If a test is inherently flaky due to timing or resource constraints,
  it MUST be documented as such and either fixed or deferred from CI per Constitution
  Principle I.
- **Heavy tests are deferred from default CI, not skipped.** Per Constitution Principle
  I, a test for a resource-heavy Postgres or optional component MAY be excluded from
  the default CI bucket execution but MUST still be committed and runnable on demand. A
  test that is completely skipped (deleted or `skip()`-marked) is the same as no test.
- **No new suppression directives.** Constitution Principle III forbids in-source
  suppression directives (`//nolint`, `// eslint-disable-next-line`, etc.). If a new
  static gate flags pre-existing code, the code MUST be fixed or the gate scoped to
  new code only — never silenced.
- **ARM64 E2E resource budget.** A new arm64 e2e test MUST respect the existing bucket
  constraints: the `e2e-go` job (covering the operator, api-auth, api-roles, api-rbac,
  api-agent, and api-mods buckets), `e2e-multicluster`, `e2e-upgrade`, and `e2e-web-live`
  already run both amd64/arm64; `bot-fast` (the `e2e-game-bot` job) is amd64-only because
  the Terraria server image's arm64 support is unverified upstream. Expanding arm64
  coverage too broadly risks doubling CI runtime; new arm64 tests MUST be added to
  existing buckets that already build the matrix, not create new arm64-only buckets, and
  MUST NOT assume bot-fast gets an arm64 leg without first confirming an arm-capable
  Terraria image.
- **Login budget exhaustion in multi-test buckets.** Per CLAUDE.md rule 8, each bucket
  has a per-IP login rate limit (5/min, burst 10) and per-user limit (3/min, burst 6).
  A new dashboard login-based E2E test that exhausts the bucket's login budget will
  break existing tests in that bucket. New tests MUST reuse logins where possible or be
  added to a bucket with headroom.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: Static analysis gates MUST block merge on all high-value vulnerability
  detection (known Go dependency vulnerabilities, Dockerfile security issues, module
  freshness drift) with any pre-existing findings either fixed or the gate scoped to new code.
  Each gate MUST be independently verifiable to fail when a violation is introduced and pass
  when the violation is fixed.
- **FR-002**: The dashboard's user-facing flows (login, create server, modify settings,
  assign roles, delete server, restore backup, manage notifications) MUST each have at
  least one E2E test that exercises that flow end-to-end against a real Kubernetes
  cluster and verifies state persistence. Mock-only tests do not satisfy this
  requirement.
- **FR-003**: Every optional component documented in README.md (MCP server, relay
  tunnel, syslog bridge, telemetry receiver) MUST have at least one E2E test
  demonstrating its primary function (read capability, relay supervision, delivery,
  metrics collection) works correctly in a deployed cluster.
- **FR-004**: Secret management code (upsertLabelledSecret, deleteManagedSecret) and
  major interactive UI components (ConsoleShell, CloneServerDialog, ServerActionsMenu)
  MUST have unit test coverage exercising both success and error paths.
- **FR-005**: When Postgres support is enabled via build tag, the E2E suite MUST verify
  that migrations, queries, and data persistence work identically to SQLite-backed
  builds before Postgres is promoted from work-in-progress status.
- **FR-006**: Every new static analysis gate MUST include a mechanism to verify it can
  actually fail (a test commit with a known violation that triggers the gate, run as
  part of the gate's validation).
- **FR-007**: E2E tests added as part of this feature MUST follow existing conventions:
  call `t.Parallel()`, use per-test unique resource names, respect shared-state guards
  (ociPushMu, ensureResticRepo, per-bucket login budgets), and be added to a named
  bucket in test/e2e/buckets.sh (or documented as excluded and why, per Constitution
  Principle I).
- **FR-008**: No suppression directives (//nolint, // eslint-disable-next-line, @ts-ignore)
  MUST be introduced to satisfy new static gates. Pre-existing violations MUST be fixed
  or the gate scoped to new code only.
- **FR-009**: A single tracked Coverage Gap Record artifact MUST be created and kept
  current as gaps from the survey are closed, tracking each gap's category, priority,
  and status (open, in-progress, closed), so a maintainer can determine overall coverage
  status by reading one artifact.

### Key Entities

- **Static Analysis Gate**: A CI job that automatically scans code for known-bad
  patterns, CVEs, style issues, etc., and blocks merge if violations are found.
  Examples: a Go dependency vulnerability scanner, a Dockerfile linter, a TypeScript linter.
- **E2E Test**: An automated test that exercises a feature end-to-end in a real or
  near-real deployment environment (kind cluster, real Kubernetes API), distinct from
  mocks or unit tests.
- **Coverage Gap**: A documented, surveyed deficiency in test coverage (a missing gate,
  an untested code path, a missing e2e scenario).
- **Coverage Gap Record**: A single tracked artifact (planned deliverable) listing all
  identified gaps, their priority, and remediation status (open, in-progress, closed).
- **Optional Component**: A built-in but non-essential Gameplane subsystem enabled via
  Helm config, documented in README.md as supported but not bundled by default.
- **Test Tier**: One of unit (go test, npm test, vitest), integration (envtest), E2E
  (kind), or static (lint, security scan). This feature touches all four.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Every high-value static analysis gap identified in the survey (Go dependency
  vulnerability scanning, container image scanning, Dockerfile security linting,
  dependency-review gating, module freshness drift, generated-file drift) has a blocking
  CI gate, and each gate is proven to fail on a test violation before being declared
  complete. (Example proof: a commit introducing a known-vulnerable transitive dependency
  blocks the dependency vulnerability gate and must be fixed.)
- **SC-002**: 100% of major dashboard user-facing flows (login, create server, admin
  settings, user management, server deletion, backup restore, notification webhook
  config) have a browser-driven E2E test against a real cluster. Current state: 4 live
  tests, 17 mock-only. Target state: 7+ live tests, mock-only tests retained for
  component-level coverage.
- **SC-003**: All four optional components (mcp-server, tunnel, audit-syslog-bridge,
  telemetry-receiver) have at least one E2E test in a CI bucket, and the test verifies
  the component's primary operation works correctly.
- **SC-004**: api/internal/handlers/secrets_managed.go (92 lines, upsertLabelledSecret,
  deleteManagedSecret) has 100% test coverage of reachable statements (the
  `existing.Labels == nil` branch in upsertLabelledSecret cannot run, because the
  feature-label check above it has already returned); web components ConsoleShell,
  CloneServerDialog, ServerActionsMenu each have a .test.tsx file with ≥ 80% line
  coverage.
- **SC-005**: When the Postgres build tag is enabled, a named e2e bucket runs the same
  test scenarios against Postgres that currently run against SQLite, and passes with
  identical behavior.
- **SC-006**: Zero new in-source suppression directives (//nolint, // eslint-disable,
  @ts-ignore) are introduced to satisfy static gates. All findings are fixed in code.
- **SC-007**: A single Coverage Gap Record artifact is maintained (location and format
  per plan phase), tracking all gaps identified in the survey by priority and status
  (open/in-progress/closed). A maintainer can determine coverage status for each gap
  type by reading this artifact in under 10 minutes.

## Assumptions

- **Scope is test coverage layers only.** This spec addresses automated verification
  (static gates, e2e tests, unit coverage). Manual testing, performance profiling,
  security audits, and user acceptance testing are out of scope and tracked separately.
- **TypeScript 7 blocker is independent.** Feature 009's TypeScript 7 upgrade is
  currently blocked (CLAUDE.md top-of-file check). This spec does not re-specify that
  blocker or attempt to unblock it; it remains a separate prerequisite for any web
  work that touches TypeScript types.
- **CodeQL workflow is a dependency, not re-specified.** The branch
  `fix-codeql-lint-gate-fixtures` is actively addressing CodeQL workflow configuration
  and fixture analysis issues. This spec assumes that work is completed and does not
  duplicate or redirect CodeQL effort here; both branches proceed in parallel or in
  sequence as decided by the maintainer.
- **No threshold lowering, no new suppression directives.** Constitution Principle III
  applies: existing coverage thresholds in `.testcoverage.yml` and `vitest.config.ts`
  are not lowered; in-source suppressions are not introduced. New gaps found by new
  gates are fixed in code, not silenced.
- **Heavy tests are deferred, not skipped.** Per Constitution Principle I, tests for
  resource-intensive scenarios (heavy game bots, full Postgres upgrade path) are
  committed and runnable on demand but excluded from default CI buckets per documented
  reasons. They are never deleted or skipped.
- **ARM64 E2E expansion is opt-in and budget-aware.** The amd64/arm64 matrix already
  runs for the operator and api-* buckets (e2e-go job), multicluster, upgrade and web-live;
  bot-fast is amd64-only. New arm64 tests are added to existing matrix buckets, not new
  arm64-only buckets, to avoid doubling CI runtime.
- **Optional component e2e tests assume the components remain optional.** MCP server,
  tunnel, syslog bridge, and telemetry receiver are tested as opt-in (config.enabled:
  false is a valid, supported state). Tests verify both enabled and disabled behavior.
- **Postgres e2e is for stabilization only, not pre-GA.** Postgres support is
  work-in-progress and remains unsupported until a future release. E2E coverage added
  now accelerates stabilization but does not promise Postgres as GA in this release.
- **The exact static gates added are finalized in plan phase.** This spec names
  high-value categories (dependency scanning, container scanning, Dockerfile lint,
  module freshness, generated-file drift); the exact tools and CI job structure are
  determined during planning (e.g., Trivy vs. Grype, integration with existing
  cosign/publish-edge workflow).
- **The Coverage Gap Record format and location are planning-phase decisions.** This
  spec requires a single tracked artifact (FR-009, SC-007); the exact file path, YAML
  vs. Markdown format, and update workflow are not specified here — those are plan
  deliverables.
- **Login budget is already accounted for in bucket design.** CLAUDE.md rule 8 documents
  per-IP and per-user login rate limits per bucket. New dashboard login tests MUST
  respect these limits; the burden of verifying headroom is on the implementing team,
  not a new approval gate.

