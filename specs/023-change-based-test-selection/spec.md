# Feature Specification: Change-Based Test Selection

**Feature Branch**: `claude/project-thread-4uisie`

**Created**: 2026-10-09

**Status**: Draft

**Input**: User description: "Re imagine the test. currently they are very slow. only run the needed test based on the edit."

## Context (baseline, measured 2026-10-09)

Pull-request CI already skips whole jobs by top-level area (web, Go modules, chart,
docs, workflows) and selects Go modules plus their reverse dependencies. Inside the
selected area, everything still runs, and three things make a typical run slow:

- **Any Go change in any module turns on the entire Kubernetes end-to-end tier**: every
  end-to-end bucket on both CPU architectures, multicluster, upgrade, live dashboard,
  every game-bot join and capture overhead. A one-file agent fix pays for the operator,
  RBAC, multicluster and game-bot suites.
- **Any edit to shared CI configuration runs everything.** Run 2201 (PR "archive 007
  capture overhead guard as done") changed four lines, one of them a comment in the CI
  workflow. It ran all 89 jobs, about 298 runner-minutes, and took 51 minutes.
- **Selected modules run their whole test suite**, even when the change touches one
  package that only a handful of tests exercise.

Recent PR runs take 35 to 51 minutes from push to result. The slowest single jobs are
the end-to-end telemetry bucket (about 24 minutes per architecture), capture overhead
(13), multicluster (11 per architecture) and the game-bot joins (8 each).

## User Scenarios & Testing *(mandatory)*

The "users" here are the people and agents who push branches and wait on CI: the
maintainer, outside contributors, Claude threads and Dependabot.

### User Story 1 - End-to-end suites follow the changed component (Priority: P1)

A contributor changes code in one component (for example the agent, the sentinel, or
one API handler area). CI runs only the end-to-end suites that exercise that component,
instead of every end-to-end suite in the repository.

**Why this priority**: The end-to-end tier is where nearly all the wall-clock time and
runner minutes go. Narrowing it gives the biggest speed-up for the most common PRs.

**Independent Test**: Open a PR that changes only one component with a known,
narrow set of end-to-end consumers; confirm CI runs that set and reports every other
end-to-end suite as skipped with a reason.

**Acceptance Scenarios**:

1. **Given** a PR that changes only the agent's console code, **When** CI runs, **Then**
   only the end-to-end suites mapped to the agent run, and the unrelated suites
   (for example multicluster or upgrade) show as skipped with "not affected by this change".
2. **Given** a PR that changes only the sentinel, **When** CI runs, **Then** the
   operator, RBAC and authentication suites do not run.
3. **Given** a PR that changes a component every suite depends on (the operator's
   resource definitions or the Helm chart), **When** CI runs, **Then** every end-to-end
   suite runs, exactly as today.
4. **Given** a PR that adds or edits one end-to-end test file, **When** CI runs, **Then**
   at least the suite that contains that test runs.

---

### User Story 2 - Unit and integration tests follow the changed packages (Priority: P2)

A contributor changes one package inside a large module (for example one handler in
the API). CI runs the tests for that package and for every package that depends on it,
rather than the module's full test suite.

**Why this priority**: Module test jobs are the next-largest cost (the operator unit
job is about 7.5 minutes) and every Go PR pays it, but the saving is smaller than P1.

**Independent Test**: Open a PR changing one leaf package of the API; confirm CI tests
that package and its dependents only, and that a deliberately broken dependent package
is still caught.

**Acceptance Scenarios**:

1. **Given** a PR that changes one leaf package, **When** CI runs, **Then** that
   package's tests run and unrelated packages in the same module do not.
2. **Given** a PR that changes a package other packages import, **When** CI runs,
   **Then** the tests of every importing package also run.
3. **Given** a PR that changes only test data or fixtures a test reads from disk,
   **When** CI runs, **Then** the tests that read them run.
4. **Given** a PR whose web change touches one screen, **When** CI runs, **Then** the
   dashboard tests related to that screen and the files it depends on run, not the whole
   dashboard suite. [Coverage thresholds: see FR-009.]

---

### User Story 3 - Shared CI configuration edits stop forcing everything (Priority: P3)

A contributor edits shared CI configuration (a comment, one job's settings, a helper
script). CI runs the jobs that configuration actually feeds, plus a check of the
configuration itself, instead of all 89 jobs.

**Why this priority**: These PRs are less frequent, but each one is the most expensive
run there is. The fail-safe exists for good reasons, so narrowing it carries more risk
than P1 and P2.

**Independent Test**: Open a PR that changes only a comment in the CI workflow; confirm
CI runs the workflow checks and nothing heavy.

**Acceptance Scenarios**:

1. **Given** a PR that changes only a comment in the CI workflow, **When** CI runs,
   **Then** workflow validation runs and the end-to-end tier does not.
   [NEEDS CLARIFICATION: Q3 below]
2. **Given** a PR that changes the selection rules themselves, **When** CI runs, **Then**
   the selection rules' own tests run and the full suite runs.

---

### User Story 4 - Anyone can see why a test ran or was skipped (Priority: P2)

A reviewer looks at a PR's CI summary and can tell, for each suite, whether it ran and
which changed file caused it to run, or why it was skipped.

**Why this priority**: Skipping tests is only trustworthy if the decision is visible;
otherwise a green PR can hide a suite that should have run.

**Independent Test**: Open any PR; read the CI summary and confirm every suite lists
"ran because of <file>" or "skipped: not affected".

**Acceptance Scenarios**:

1. **Given** any PR, **When** CI finishes, **Then** the run summary lists every suite with
   its selection decision and the changed paths behind it.
2. **Given** a maintainer who doubts a skip, **When** they request a full run on that PR,
   **Then** every suite runs regardless of the changed files.

---

### Edge Cases

- The PR's base cannot be determined, the base is not an ancestor, or the diff cannot be
  computed: run everything (today's behaviour).
- A file changes that no selection rule claims: run everything, and fail a CI check that
  reports the unmapped path so the rules get extended.
- A test reads files outside its own package (fixtures, the module catalogue, generated
  resource definitions): those files must be listed as inputs of that test.
- Dependency manifest or lockfile changes (Go modules, npm): run everything that
  consumes the changed manifest.
- Generated files change (resource definitions, deep-copy code): treated as changes to
  the component that owns them.
- A new end-to-end test is added without being mapped to a component: the existing
  bucket-coverage check already fails on an unbucketed test; the selection rules must
  equally fail on a suite with no component mapping.
- A skipped suite was already broken on master: the next master run catches it; the PR
  is not blamed.
- Required-check rules on the protected branch: a skipped job must report as passing so
  the PR can merge.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: CI MUST decide, from the set of files a PR changes against its base, which
  test suites run, at three levels: end-to-end suite, unit/integration package, and
  dashboard test file.
- **FR-002**: The decision MUST be conservative: when in doubt (unknown base, unmapped
  path, shared input, selection-rule change), run everything.
- **FR-003**: Every end-to-end suite MUST declare the components and files it exercises,
  kept next to the existing bucket definitions, and CI MUST fail when a suite has no
  declaration or a component has no suite.
- **FR-004**: A change to a component MUST select every end-to-end suite that declares
  that component, plus the suites of every component that depends on it.
- **FR-005**: Unit and integration selection MUST include the changed packages and every
  package that imports them, across module boundaries, plus tests that read changed files
  from disk.
- **FR-006**: Every push to the default branch MUST keep running the full suite, so
  anything a PR skipped is still verified before release.
- **FR-007**: A maintainer MUST be able to force a full run on a PR without changing its
  files (for example with a label).
- **FR-008**: The run summary MUST list, for every suite, whether it ran and the changed
  paths that selected it, or that it was skipped and why.
- **FR-009**: Per-module coverage minimums MUST stay enforced.
  [NEEDS CLARIFICATION: Q1 below. A partial test run cannot compute a whole-module
  coverage figure.]
- **FR-010**: CPU-architecture coverage on PRs MUST follow the maintainer's choice.
  [NEEDS CLARIFICATION: Q2 below.]
- **FR-011**: The selection rules MUST have their own tests (the existing scope script's
  test file is extended), and a change to the rules MUST run those tests and the full suite.
- **FR-012**: No test may be deleted, weakened, disabled or excluded from the full suite
  to gain speed. Selection only changes which tests a PR runs, never which tests exist or
  what they assert.
- **FR-013**: Skipped jobs MUST report a passing status so required checks and the
  aggregate report job still gate merges correctly.

### Key Entities

- **Changed path set**: the files a PR changes relative to its merge base, including both
  sides of a rename.
- **Component**: a deployable or library unit (operator, API, agent, sentinel, chart,
  dashboard, game modules, …) with the paths that belong to it and the components it
  depends on.
- **Suite mapping**: for each end-to-end bucket, game-bot test and special job, the
  components and extra files it exercises.
- **Selection result**: per suite, ran or skipped, plus the paths that caused it; shown in
  the run summary and consumed by the jobs.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A PR that changes a single non-core component (agent, sentinel, tunnel,
  telemetry receiver, one dashboard screen) gets its full CI result in 20 minutes or less,
  down from 35 to 51 minutes today.
- **SC-002**: Median runner-minutes per PR run drops by at least 50% over the first 30
  merged PRs after rollout, compared with the 30 merged PRs before it.
- **SC-003**: A PR that changes only a comment in shared CI configuration uses under 30
  runner-minutes, down from about 298 (run 2201). [Depends on Q3.]
- **SC-004**: Over the first 60 days, no defect reaches the default branch that a skipped
  suite would have caught on the PR; each master-run failure is checked against the PR's
  selection result, and any miss leads to a rule fix within the same week.
- **SC-005**: 100% of PR runs show a per-suite ran/skipped reason in the run summary.
- **SC-006**: Every test that exists today still runs on every default-branch push.

## Open questions

Each is asked in the project thread with options and a recommendation; answers replace
the markers above.

- **Q1 (FR-009) Coverage on partial runs.** Options: (A) enforce module coverage only on
  full runs (master pushes and forced full runs), PRs check changed lines only;
  (B) always run a module's whole suite when coverage applies, so only the end-to-end
  tier gets narrowed; (C) drop coverage from PRs entirely. Recommended: A.
- **Q2 (FR-010) arm64 on PRs.** Options: (A) PRs run amd64 end-to-end only, arm64 runs
  on master and when a PR touches arch-sensitive files (Dockerfiles, build tags, native
  code); (B) keep both architectures for every selected suite on PRs. Recommended: B,
  since it keeps today's coverage and selection alone meets SC-001.
- **Q3 (US3) Shared CI config edits.** Options: (A) map each shared file to the jobs it
  feeds, run everything only for the selection script, Makefile, workspace and composite
  actions; (B) keep "any CI config edit runs everything". Recommended: A.

## Assumptions

- This is CI-only. Local test runs stay forbidden by the repo rules; nothing here changes
  what contributors run locally.
- The existing change-detection job and scope script are the base to extend, not replace.
- "All CI checks green" in the constitution is satisfied by skipped-but-passing jobs plus
  the full run on every default-branch push.
- The heavy game tests already excluded from CI buckets stay excluded; this feature does
  not change bucket membership.
- Merge queue is not in use; if it is adopted later, a merge-queue run counts as a full run.
- Website and module submodule repositories are out of scope; they have their own CI.
