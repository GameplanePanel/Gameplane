<!--
Sync Impact Report
- Version change: 2.1.0 → 2.2.0
- Bump rationale: MINOR. Principle IV is materially expanded with a completed-feature
  folder-naming rule: a finished feature's `specs/<NNN>-<slug>/` is renamed to
  `specs/done_<NNN>-<slug>/`. Nothing previously compliant becomes non-compliant — the
  convention was already in use for five folders (done_001, done_003, done_004,
  done_005, done_006) and is now written down with its completion criteria, its
  reference-update requirement, and the explicit statement that a `done_` folder stays
  binding rather than becoming skippable.
- Modified principles: IV. Spec-Driven Development — adds the `done_` rename rule.
- Downstream consistency: CLAUDE.md gains rule 16 stating the same rule operationally;
  rule 15 (read the whole specs/<feature>/ folder) is unaffected and reinforced by the
  "still binding" bullet.

Previous report (2.0.1 → 2.1.0)
- Bump rationale: MINOR. Principle V's scope is narrowed to the MAIN LOOP and the
  blanket prohibition is lifted: subagents and workflows may now use the `Agent` tool.
  Nothing previously compliant becomes non-compliant; a restriction is relaxed. (The preceding 2.0.0 bump was MAJOR: it removed Principle V's permitted
  exception, prohibiting delegation patterns that were explicitly compliant under
  1.6.0 — a single blocking `Agent` call for a narrow, already-scoped lookup.)
- Modified principles: V. Delegate to Workflows & Subagents — the delegation rule is
  now scoped to the MAIN LOOP, which hands work off via `Workflow` rather than the
  `Agent` tool; subagents and workflows may use the `Agent` tool freely. Fix waves
  after a tier-up review remain workflow work. Retained from 2.0.0: every `agent()`
  call in a workflow script sets `model` explicitly, because omitting it silently
  inherits the session model and defeats the start-at-the-smallest-tier rule, with
  `effort` called out as not a substitute for tier. Added: a caution against restating
  this rule in progressively more absolute terms across multiple documents.
- Version history: 1.3.1 → 1.4.0 (VI exception) → 1.4.1 (II encryption fix) → 1.5.0
  (II re-export requirement) → 1.6.0 (III config-level exclusion carve-out) → 2.0.0
  (V Agent-tool ban, narrow-lookup exception removed, explicit-model requirement)
  → 2.0.1 (V scope clarified: workflow-internal `agent()` is compliant)
  → 2.1.0 (V scoped to the main loop; subagents/workflows may use the Agent tool)
- Added sections: none
- Removed sections: none (one sentence struck within Principle V)
- Deferred / TODO placeholders: none — all template tokens resolved
- 2.1.0: Principle V is now scoped to the MAIN LOOP — it delegates via `Workflow`
  rather than the `Agent` tool. Subagents and workflows may use the `Agent` tool
  freely. The 2.0.0/2.0.1 absolutist phrasing ("MUST NOT be used, for any task, at any
  size", restated across CLAUDE.md, a memory file, the constitution and the prompt
  hook) was read as prohibiting subagents in general and caused every workflow-spawned
  subagent in a session to be refused, stalling all delegated work. The principle now
  also warns against restating the rule in progressively more absolute terms.
- Downstream consistency: CLAUDE.md rule 13 and the repo's UserPromptSubmit hook were
  already updated to match ahead of this amendment; both previously pointed at the
  `Agent` tool and contradicted this principle.
-->

# Gameplane Constitution

## Core Principles

### I. E2E-Tested Delivery (NON-NEGOTIABLE)
Every feature MUST be verifiable end-to-end before it is considered done. A change is
incomplete — regardless of unit or integration coverage — until it has a corresponding
E2E test exercising the real user- or operator-facing path (dashboard flow, API contract,
CRD reconciliation loop, or agent behavior) in `test/e2e/`, added to a bucket in
`test/e2e/buckets.sh` per the project's e2e conventions. Unit tests (`make test-go`,
`make test-web`) and envtest integration tests (`make test-integration`) are required
supporting layers, not substitutes. New E2E tests MUST call `t.Parallel()`, use
per-test unique resource names, and respect existing shared-state guards
(`ociPushMu`, `ensureResticRepo`, per-bucket login budgets).

Every game module's E2E coverage MUST include a real join: a hand-rolled client that
speaks that game's actual wire protocol to connect to a booted GameServer, not a bare
TCP/UDP dial or a mocked handshake — a probe MUST be proven to both fail against a dead
address and succeed against a real listener before it is trusted. When a game is too
resource-heavy to run inside a GitHub Actions runner (memory/CPU/disk), its E2E test
MUST still be written and committed to `test/e2e/` — it is simply excluded from every
CI bucket in `test/e2e/buckets.sh` rather than skipped or deleted, and the bucket
exclusion MUST be commented with why. A heavy test that exists but never runs in CI is
still runnable on demand (locally against a real cluster, or in a manually triggered
job) and stays exempt from the "e2e bucket coverage" CI gate that would otherwise fail
on an unbucketed test.
Rationale: Gameplane's promise is that the same operational model works identically on
a single-node k3s homelab and a multi-node production cluster. Only a real E2E run
against a live control plane catches drift between CRD, operator, API, agent, and
dashboard — unit tests in isolation cannot. A protocol-level join is the only proof a
server is actually reachable and playable, not merely "a port is open"; and heavy game
servers (multi-GB images, sustained CPU) would blow CI runner budgets, so the tradeoff
is deferred execution, never deferred authorship.

### II. Design-First for User-Facing Change
Any change to the web dashboard's visual surface, and any change to the public website's
screens, MUST be designed first in the relevant Pencil source (`design.pen` for the
dashboard, `website.pen` for the public website's screens) via the `pencil` MCP server,
and translated to React only after the design is committed. Backend-only, API-only, and operator-only changes are exempt. The `.pen`
files are Pencil-owned design sources and MUST NOT be read, hand-edited, or deleted with
generic file tools (`Read`, `Grep`, `sed`, `cat`, `rm`) — they MUST be accessed and
modified through the `pencil` MCP server only. They are multi-megabyte machine-generated
JSON documents: hand-editing risks structural corruption Pencil cannot recover, and
reading one floods an agent's context while revealing nothing useful about the design,
for which `get_screenshot` and `export_nodes` are the correct tools.
Every design edit — a new screen or a change to an existing one, in either `.pen` file —
MUST be followed, in the same change, by re-exporting the touched object(s) to the
matching plain-file snapshot: `assets/design-export/{json,screenshots}/` for `design.pen`,
`website/website-export/{json,screenshots}/` for `website.pen`. The export is produced
via the `pencil` MCP server (a JSON dump per touched node and a screenshot per touched
node), scoped to what changed, not a full re-run of every screen. A design change without
a matching export update is incomplete.
Rationale: the `.pen` files are the source of truth for the product's designed screens.
Code-led redesigns bypass that source of truth, drift from it silently, and have been
reverted before. They are large, complex machine-generated JSON structures where
uncontrolled hand-edits risk corruption that Pencil's recovery mechanisms cannot undo.
The export snapshot exists because the `.pen` format cannot be read directly (Principle
II above) and the Pencil MCP server's own whole-document read tools have proven
unreliable for bulk enumeration — a git-tracked, plain-file mirror is what lets any
agent or human inspect the current design state without a live Pencil session, and a
stale mirror actively misleads, so it cannot be allowed to drift from the source.

### III. Language & Ecosystem Best Practice
Code MUST follow the idioms of its language and the project's established tooling
rather than working around them. Go code wraps errors with `%w` so `errors.Is`/`errors.As`
keep working up the call stack; TypeScript runs under `strict` mode with
`@typescript-eslint/no-explicit-any` and `@typescript-eslint/no-floating-promises`
enforced — an unavoidable `any` gets a one-line comment explaining why, and a
fire-and-forget `Promise` is resolved with `await` or explicit `void`, never suppressed.
When `golangci-lint` or ESLint flags something, the code MUST be fixed, not silenced.
In-source suppression directives are absolutely forbidden — no `//nolint`, no `//#nosec`,
no `//lint:ignore`, no `// eslint-disable-next-line`, no `// @ts-ignore`. Narrowly-scoped,
maintainer-authorized config-level exclusions in `.golangci.yml` are permitted in two
categories:

(1) **Path-scoped false-positive exclusions**: Single path pattern, single linter or rule,
    with inline justification, targeting a false positive that the code handles correctly
    despite what the linter reports.

(2) **Redundant-rule disabling**: Disabling a linter rule globally when that rule is a
    duplicate or strict subset of another enabled linter rule, such that no class of actual
    defect goes unchecked. The disabled rule must be wholly subsumed by a stricter, more
    configurable enabled linter such that the underlying requirement (e.g., error-checking)
    remains enforced. Exclusions of this type are rare and require maintainer sign-off;
    specific instances are documented in `specs/done_004-lint-backlog-wave2/contracts/exclusion-policy.md`.

Global or broad rule-weakening — removing a linter from the enabled set, or repo-wide
exclusions for findings that would otherwise go unchecked — remains forbidden.

CRD Go type edits MUST be followed by `make generate && make manifests` in the same
commit, with the regenerated artifacts included.
Rationale: suppression directives and workarounds hide defects instead of fixing them,
and this project has already accumulated cases where that erased real signal. Fixing
the underlying issue is the only change that survives the next refactor.

### IV. Spec-Driven Development
Non-trivial work MUST move through the spec-kit lifecycle before code is written:
`/speckit-constitution` (this document) → `/speckit-specify` (what and why) →
`/speckit-plan` (how) → `/speckit-tasks` (breakdown) → `/speckit-implement` (execution).
A feature branch's spec and plan artifacts are the source of truth for intent; code
review checks the diff against them, not against an implementer's memory of a
conversation. Trivial fixes (typos, one-line corrections, config tweaks with no
behavioral ambiguity) are exempt from the full lifecycle but still get a clear commit
message stating intent.

Every module folder (each Go module directory such as `netguard/`, `gameaction/`,
`gameproto/`, `operator/`, `api/`, `agent/`, `audit-syslog-bridge/`,
`telemetry-receiver/`, `sentinel/`, `mcp-server/`; the `web/` tree; and each game
directory under `modules/<name>/`) MUST maintain a `specs.md` describing in detail how
that module actually works: its responsibilities and boundaries, the protocols or
contracts it implements (e.g. the wire-protocol handshake a `modules/<game>/` directory
speaks, or the CRDs a controller reconciles), its inputs/outputs, and the invariants
other modules depend on. `specs.md` MUST be updated in the same change that alters the
behavior it documents — a behavior change without a matching `specs.md` update is
incomplete.

Rationale: this repo has repeatedly lost work to agents re-deriving intent from scratch
mid-session or drifting from what was actually agreed. A written spec is the artifact
that survives context resets and hand-offs between sessions and agents. Per-module
`specs.md` files extend that same guarantee to a module's ongoing behavior, not just its
initial feature spec — critical for game protocols in particular, which are often
undocumented upstream and were previously reconstructed from scratch each time.

A feature's spec folder MUST be renamed from `specs/<NNN>-<slug>/` to
`specs/done_<NNN>-<slug>/` once that feature is complete — meaning every task in its
`tasks.md` is checked off or explicitly withdrawn, and the feature's branch has been
merged into `master`. The rename is the completion marker; an in-flight feature keeps the
bare `<NNN>-<slug>` name. Requirements:

- Rename with `git mv` so history follows the folder, and commit it as its own
  `docs:` unit of work — never bundled into an unrelated change.
- Update every in-repo reference to the old path in the same commit. Spec folders are
  cross-referenced from `CLAUDE.md`, this constitution, source comments, `.coderabbit.yaml`,
  and each other; a rename that leaves a dangling `specs/<NNN>-...` path is incomplete.
- Never rename a folder whose feature is unmerged, partially implemented, or blocked. A
  feature parked on an external blocker stays un-prefixed however long it waits.
- The `done_` prefix is terminal. Reopening work on a completed feature means a new
  numbered spec folder that cites the old one, not un-prefixing the old folder.
- A `done_`-prefixed folder is still binding and still read in full: its artifacts govern
  the shipped behavior, and the prefix marks the work as finished, not the document as
  expired or safe to skip.

Rationale: `specs/` is the durable record of intent and grows monotonically — without a
completion marker in the folder name itself, every fresh session must open each folder
and reconstruct whether its work still needs doing, and converge runs have re-litigated
features that shipped weeks earlier. The prefix moves that signal into the directory
listing, where it costs nothing to read. It is a rename rather than a deletion or an
archive move because the artifacts remain binding: a completed feature's `data-model.md`
and `contracts/` still carry the exceptions that govern today's code, so they must stay
in place, in `specs/`, and in git history.

### V. Delegate to Workflows & Subagents
The main agent loop's job is decomposition, orchestration, judgment, and verification —
not the legwork. Work MUST be split into the largest number of genuinely independent
tasks it supports and fanned out to subagents concurrently. Delegated tasks start at
the smallest capable model tier and escalate one tier at a time only on demonstrated
failure of the smaller tier, never pre-emptively. Once a wave of subagents completes,
its combined output MUST be reviewed by an agent one tier above the tier the work ran
at before being accepted; fixes from that review are applied by relaunching small
agents, not by fixing in the main loop. Using the highest-capability model tier for
review or execution requires explicit human authorization first.

**This is a rule about the MAIN LOOP, not about subagents.** Scope it precisely:

- **The main agent loop** delegates through the `Workflow` tool. When the main loop has
  work to hand off, it writes a workflow rather than reaching for the `Agent` tool.
- **Subagents and workflows** may use the `Agent` tool freely. Nothing in this principle
  restricts what a subagent does, and a workflow spawning its subagents via `agent()`
  inside `parallel()` / `pipeline()` is the ordinary, expected way workflows are written.

The reason the main loop prefers `Workflow`: in Claude Code an ad hoc `Agent` call blocks
the calling turn until that subagent finishes, serializing the session behind the slowest
task, while `Workflow` runs in the background, returns immediately, notifies on
completion, and expresses this principle's fan-out, tiered escalation and tier-up review
as deterministic script logic rather than ad hoc prompting. Fix waves following a tier-up
review are delegated work like any other and belong in a workflow.

Keep this rule stated once and stated plainly. Do not restate it in progressively more
absolute terms across multiple documents: an earlier revision escalated it to "the Agent
tool MUST NOT be used, for any task, at any size" in four reinforcing places, and the
resulting absolutism caused every workflow-spawned subagent in a session to be refused as
if it were the prohibited call — stalling all delegated work. Precision about scope is
what makes a rule enforceable; emphasis is not.

Every `agent()` call in a workflow script MUST set `model` explicitly. Omitting it
silently inherits the session's own model — the highest tier in ordinary use — which
defeats the start-at-the-smallest-tier rule above without any warning at author time
or run time. `effort` is not a tier and MUST NOT be treated as a substitute.
Rationale: single-threaded, single-model execution is slower and costlier than
parallel cheap agents for well-scoped work, and a subagent's own "done" report is a
claim, not evidence — the tier-up review step is what catches the gap between the two.
A blocking subagent call still ties up the session for its full duration even when run
concurrently with others in one turn; non-blocking workflow orchestration is what lets
the main loop keep making progress instead of idling on the slowest branch. The
previously-permitted "narrow lookup" exception is removed because it proved
unbounded in practice: it was the stated justification for more than fifteen blocking
`Agent` calls in a single session, each individually defensible as narrow. A rule with
a judgement-call exemption is not enforceable by the agent applying it to itself.

### VI. CI Bears the Heavy Lifting
Builds, tests, lint, coverage, envtest, and E2E suites run on GitHub Actions CI, not on
a developer's or agent's local machine. A quick compilation check (`go build ./...`,
`tsc --noEmit`) to catch obviously broken code before pushing is permitted; running the
actual test or lint suites locally is not. Work is verified by pushing to a branch,
watching the CI run (`gh` CLI / the Actions UI), and fixing failures with follow-up
commits. A change MUST NOT be reported as validated, working, or ready to merge until
CI has actually run it green — a local build succeeding is not evidence a test suite
would pass.

The one exception: if the operator (the human directing the work) explicitly provides
a remote host or cluster for running builds/tests — e.g. a dedicated test VM or a live
Kubernetes cluster reachable over SSH/kubeconfig — tests and builds MAY be run there.
This is not a loophole for running suites on the agent's or developer's own local
machine; it applies only to infrastructure the operator has named and handed over for
that purpose, and CI remains the system of record for merge readiness regardless.
Rationale: CI is the only environment guaranteed to match production's toolchain,
cluster access, and resource limits; local runs on heterogeneous developer machines
have produced false confidence in the past, and heavy suites (kind/e2e) need CI-scale
resources this project's local dev environments don't reliably have. An operator-provided
remote host is a deliberate, known-good exception to that unreliability, not an
erosion of it — it exists for cases CI structurally cannot cover, such as measurements
against a real live cluster or exercising CI-down fallback paths.

## Additional Constraints

Technology choices, coverage thresholds, module boundaries, and generated-artifact
rules are governed in detail by `CLAUDE.md` at the repo root, which this constitution
takes precedence over in case of conflict. In particular: Go 1.26 across the
`go.work` modules, React 18 + TypeScript strict + Vite for the dashboard, the
`controller-runtime`/`client-go` operator stack, `chi` + `coder/websocket` for the API,
per-module coverage gates defined in each module's `.testcoverage.yml` (and
`web/vitest.config.ts` for the frontend), and the CRD/RBAC/Helm codegen pipeline
(`make generate`, `make manifests`) that MUST stay in sync with any CRD type change.

## Development Workflow

1. Constitution and spec artifacts govern before implementation begins (Principle IV).
2. UI/design work is designed in Pencil before code is written (Principle II).
3. Implementation is delegated and fanned out to subagents per Principle V, with
   tier-appropriate model escalation and mandatory tier-up review before acceptance.
4. Every logical unit of work is committed separately and signed (`git commit -s`),
   using conventional-commit prefixes, on its own branch; the branch is deleted
   (remote + local) once merged into `main`.
5. Verification happens on CI, not locally (Principle VI); a change is not "done"
   until its E2E coverage (Principle I) is green on a pushed branch.
6. Merges to `main` require all CI checks green, including the E2E tier — never
   merge on a partial-green or "flaky, ignore it" basis.

## Governance

This constitution supersedes ad hoc practice and prior undocumented conventions for
any Gameplane repository work. `CLAUDE.md` remains the detailed, evolving operational
reference (commands, file paths, workflows); where the two conflict, this constitution's
principles win and `CLAUDE.md` MUST be updated to match.

Amendments are made by editing this file directly, incrementing `CONSTITUTION_VERSION`
per semantic versioning (MAJOR: a principle is removed or redefined incompatibly;
MINOR: a principle or section is added or materially expanded; PATCH: wording or
clarification only), updating `Last Amended`, and recording the change in a Sync
Impact Report comment at the top of the file. Every PR and every spec/plan review MUST
check the proposed change against these principles; a violation MUST be justified
explicitly in the plan's Complexity Tracking section or the change MUST be redesigned
to comply. Use `CLAUDE.md` for the day-to-day runtime guidance this constitution
intentionally leaves at a higher level of abstraction.

**Version**: 2.2.0 | **Ratified**: 2026-08-11 | **Last Amended**: 2026-09-01
