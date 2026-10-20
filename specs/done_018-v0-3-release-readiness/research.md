# Research: v0.3 Release Readiness Audit

Phase 0 of the plan. On 2026-09-23 four read-only scouts gathered facts on the feature inventory, release mechanics, security boundaries, and known bugs, with some direct checks added afterwards. Each entry below is a Decision, Rationale, and Alternatives considered.

## R1. Where audit records live and in what format

- **Decision**: Markdown files under `specs/done_018-v0-3-release-readiness/audit/`: `release-criteria.md`, `kubelab-baseline.md`, `inventory.md`, `coverage.md`, `findings.md`, `rounds.md`, `report.md`, `procedures/<area>.md`, and `evidence/<ID>/`. Table columns are fixed in [contracts/audit-records.md](contracts/audit-records.md).
- **Rationale**: FR-020 and OD-004 require files in the spec folder, versioned with the code and reviewable in the PR. Markdown tables diff cleanly, and `hack/check-links.sh` and `hack/check-specs.sh` already lint the spec folders.
- **Alternatives considered**: GitHub issues per finding. They aren't versioned with the code and were rejected under OD-004, though a finding may still *link* an issue. YAML or JSON records are machine-friendly, but PR reviewers can't read them well.

## R2. Inventory and component sources

- **Decision**: Enumerate the inventory from code, not from docs:
  - dashboard routes from `web/src/router/tree.tsx`
  - API routes from `api/cmd/main.go`, with the `api/internal/rbac/rbac.go` rule table
  - CRDs from `operator/api/v1alpha1/*_types.go`
  - agent capabilities from `agent/internal/*`
  - auxiliary components and install options from `charts/gameplane/values.yaml`
  - game modules from `modules/*/module.yaml` (initialise the submodule first)

  The component list is every `go.work` entry (15, including `gp-module` and `test/e2e`) plus `web/`, `charts/gameplane/`, `deploy/`, `hack/`, `.github/workflows/`, `docs/`, root docs (`README.md`, `CHANGELOG.md`, `SECURITY_AUDIT.md`, `CLAUDE.md`), and `design-export/`. The `modules/` and `website/` submodules get consistency-only review.
- **Rationale**: Docs have drifted before. For example, the CLAUDE.md repo map lists 14 Go modules and omits `gp-module`, which is seeded as finding F-000x (FR-013). Scout counts to check at enumeration time: 16 dashboard routes, about 25 API mount groups, 9 CRD kinds, 11 agent capabilities, 6 auxiliary components, about 50 Helm keys, and 16 modules in the default OCI catalog (`values.yaml`). `test/e2e/` has bot tests for more games than that catalog, so reconcile the two lists.
- **Alternatives considered**: Deriving the inventory from `docs/*.md` and the README. It misses undocumented capabilities and inherits doc errors.

## R3. Severity scale

- **Decision**: Four levels, used only to order fix work (FR-005, OD-002):
  - **S1**: data loss, security-boundary break, or upgrade/rollback corruption
  - **S2**: a core path broken, with no workaround
  - **S3**: degraded behaviour, or a workaround exists
  - **S4**: cosmetic, documentation, or wording

  Every level blocks the release.
- **Rationale**: The spec forbids severity-based deferral, so the scale only sets order: S1 first, and within a level, core paths first.
- **Alternatives considered**: A CVSS-style score, which is too heavy for non-security defects. Three levels would lump docs mismatches in with degraded behaviour.

## R4. Publishing release candidates

- **Decision**: Tag `v0.3.0-rc.N` on `master` after each fix round, using the existing `.github/workflows/release.yaml`:
  - trigger `v*` (`release.yaml:5-6`)
  - images to `ghcr.io/valgulnecron/gameplane/<component>`, tagged `v0.3.0-rc.N` and `0.3.0-rc.N`
  - chart rewritten to `0.3.0-rc.N` and pushed to `oci://ghcr.io/valgulnecron/charts/gameplane` (`release.yaml:148-149,180`)
  - cosign signing is mandatory
  - the GitHub release is marked prerelease because the tag contains `-` (`release.yaml:248-250`)

  Before each tag, add a `## [0.3.0-rc.N]` CHANGELOG section. Otherwise the notes fall back to `--generate-notes` (`release.yaml:226-236`). The maintainer approves each tag first (FR-019). The audit agent prepares the tag command, logs the approval request in `OPEN-DECISIONS.md`, and waits for approval.
- **Rationale**: FR-018 needs the audited artifacts to be the ones users pull. The pipeline already handles pre-release suffixes. Under docker/metadata-action semantics a pre-release version gets only its `{{version}}` tags, not `{{major}}.{{minor}}`, so the `0.3` tag isn't moved by an RC. Round 0 confirms this on rc.1 (evidence: `crane ls` or the GHCR tag list).
- **Alternatives considered**: Side-loading images onto kubelab. Rejected under OD-001. Using the `edge` channel (`publish-edge.yaml`) was also rejected: it's a moving tag, so it can't pin what was audited.

## R5. Deploying the candidate to kubelab without touching site settings

- **Decision**: Before the first change, record `helm get values -n <ns> <release>` (redacted), the chart version, the image references in use, and the module source into `audit/kubelab-baseline.md`. For each RC, run `helm upgrade <release> oci://ghcr.io/valgulnecron/charts/gameplane --version 0.3.0-rc.N --reuse-values`, overriding only the image registry and tag keys needed to move from the private side-loaded tag to public GHCR. Every override is listed in `rounds.md`. After the audit, the maintainer decides whether to keep the public images or restore the baseline values. Full procedure in [contracts/rc-deploy.md](contracts/rc-deploy.md).
- **Rationale**: FR-018 and the spec's assumptions: only the image version and intended settings change, and the original values are kept for restoration.
- **Alternatives considered**: A fresh `helm install` into a second namespace. The CRDs are cluster-scoped and shared, so a second install would clash with the live one. Using `--reset-values` would drop the site settings.

## R6. Upgrade-path test (FR-010, SC-005) and rollback

- **Decision**:
  1. Take a SQLite snapshot of the API database before every upgrade: `sqlite3 .backup` inside the API pod, then `kubectl cp` out. Store it off-cluster, not in git.
  2. Seed audit resources: an `audit018-` game server with a data marker file, and an `audit018-admin` account.
  3. Run the upgrade.
  4. Verify the marker, the server phase, the admin login, and the audit-chain `Verify`.
  5. Test rollback with `helm rollback <release> <rev>` plus a database-snapshot restore **if** the upgrade applied new migrations, since `api/internal/db/migrations/` is forward-only.
  6. Write the documented rollback procedure into `docs/install.md`. It's missing today, and that gap is seeded as a finding.

  The live **beta.8 → RC** step requires kubelab to be at public `v0.2.0-beta.8` first. If kubelab's current side-loaded build carries newer migrations than beta.8, moving it back would be a downgrade. That case is **OD-005**, resolved as option (b): snapshot the real database, reinstall at public beta.8 with a fresh database, seed, upgrade, verify, then restore the real database. CI's `e2e-upgrade` baseline moves from `0.2.0-beta.5` to `0.2.0-beta.8` (`deploy/kind/upgrade.sh:36`, `.github/workflows/ci.yaml:970`) whatever the answer.
- **Rationale**: FR-015 requires the upgrade from the last beta to be verified live. The existing CI test (`test/e2e/upgrade_e2e_test.go`) covers only kind and beta.5.
- **Alternatives considered**: Relying only on CI's kind upgrade test, which FR-010 rules out. Testing rollback without a database snapshot would lose data if migrations ran.

## R7. Isolating live tests and cleaning up (FR-009, SC-006)

- **Decision**: Every test resource gets the name prefix `audit018-`. Dashboard users and roles use the same prefix. Resources created through `kubectl` also get the label `gameplane.io/audit: "018"`. Before round 0 and after cleanup, snapshot pre-existing state: GameServers, PVCs, Backups, ModuleSources and Modules with UID, `metadata.generation` and phase, plus the API user list, role list and module-source list. The two snapshots must match, not counting `audit018-` objects and status-only fields. Cleanup deletes everything with the prefix and then asserts that none remain. Contract: [contracts/test-resources.md](contracts/test-resources.md).
- **Rationale**: kubelab is a long-lived cluster with real servers. Name prefixes are the one marker every path supports: API, dashboard, and CRD.
- **Alternatives considered**: A dedicated games namespace. `gamesNamespace` is set once per install (`values.yaml`), so real and test servers share it. Labels alone don't work because the API can't set arbitrary labels.

## R8. Multi-node checks (FR-011) that don't disturb real workloads

- **Decision**:
  - **Scheduling**: create `audit018-` servers and record which node each lands on. Use pod affinity or nodeSelector through the API if the CRD exposes it; otherwise observe.
  - **Drain**: `kubectl cordon <node>`, then send an Eviction for **only** the audit pod, observe the reschedule or wait state, then `kubectl uncordon`. A full `kubectl drain` evicts pre-existing pods, so it isn't used.
  - **Node loss**: a real node outage affects every workload on that node. It runs only with the maintainer's explicit approval, at run time, on a worker that holds no pre-existing stateful game server. **OD-006** approved this without a further prompt: stop `k3s-agent` for a few minutes on a worker that holds no pre-existing stateful game server, observe, then restart it and record recovery of the pre-existing workloads.
- **Rationale**: FR-011 has to be satisfied without breaking FR-009.
- **Alternatives considered**: A `NoExecute` taint. It evicts every pod without a matching toleration, including pre-existing ones.

## R9. Driving the live tests

- **Decision**:
  - API: `curl` with a per-role cookie jar and CSRF header. Log in once per role and reuse the session, to stay within the login rate limit (IP burst 10, 5/min; user burst 6, 3/min).
  - Dashboard: Chrome MCP on sonnet against the kubelab ingress host from the baseline, with screenshots as evidence.
  - CRDs and pods: `kubectl` with `KUBECONFIG=~/kubelab.yaml`.
  - Deliberate brute-force and privacy tests run **last** in a round, so throttling doesn't block other rows. A 429 inside those tests is the expected pass signal.
- **Rationale**: These are the users' normal paths (FR-002), and they fit the rate-limit budget.
- **Alternatives considered**: Playwright E2E against kubelab. Useful later as a candidate for automation (FR-012), but it's CI tooling aimed at kind.

## R10. Representative game modules

- **Decision**: Each module *category* (FR-001) is its console/RCON protocol family plus its join-protocol family, as declared in `module.yaml` and `template.yaml`. At least one module per category goes through the full create → start → join → console → backup → restore → delete cycle. `minecraft-java` is the primary one: it is light and has a real protocol probe in `gameproto`. Modules too heavy for kubelab's resources are recorded as **blocked** (named prerequisite: node memory/CPU), not failed.
- **Rationale**: Covering every module live is out of reach on three nodes. Categories cover every distinct code path in the agent and operator.
- **Alternatives considered**: Every module live, which runs out of kubelab's capacity. Only Minecraft, which leaves the other protocol paths untested.

## R11. Known-bug import (FR-006)

- **Decision**: Seed `findings.md` before round 0 from these sources. Each gets an `F-` ID, status `imported`, and needs live re-verification even if it's believed fixed.
  - Closed issues: #414, #377, #376, #375, #373 (live E2E `/shares` proxy rule), #306 (dump redaction).
  - Merged fix PRs: #416 (console WS 403 on non-default ports), #411, #410, #409, #396, #350 (security audit fixes), #327.
  - Every finding in `SECURITY_AUDIT.md`.
  - `specs/002-nuclear-option-ip-pool`: Nuclear Option UDP 7777 isn't bound; traffic goes through Steam networking.
  - `specs/012-docs-refresh-and-outreach/OPEN-DECISIONS.md` OD-8: OIDC Helm role mappings are documented but still sit under CHANGELOG "Unreleased".
  - `specs/done_015-top-steam-game-modules/OPEN-DECISIONS.md`: RCON protocol-correctness questions (Factorio, Project Zomboid, `cli`, `rest`).
  - Doc drift: the CLAUDE.md repo map omits `gp-module`; `docs/install.md` has no rollback procedure.

  There are no open GitHub issues right now (checked 2026-09-23 with `gh issue list --state open`).
- **Rationale**: FR-006 and US3 AS-2: nothing is silently assumed fixed.
- **Alternatives considered**: Importing only open bugs. That would lose the previously-fixed-but-never-live-verified class, which is the reason for this audit.

## R12. Component review method

- **Decision**: One review record per component. Each covers:
  1. correctness against that component's `specs.md`, or its spec folder for the chart, CI and docs
  2. security boundaries relevant to it
  3. error handling (`%w`) and TypeScript strictness
  4. dead or unreachable code
  5. docs-versus-behaviour drift

  Reviews run as `Workflow` fan-outs: the first pass on sonnet, since haiku reviewers produced speculative claims in this plan's scouting, and verification of each finding one tier up, on opus. A finding needs a file:line and a reproduction or observation before it's recorded. The six FR-008 boundaries also get a live violation attempt, defined in `procedures/security.md`. Code locations: login privacy `api/internal/auth/local.go:105-173`; RBAC `api/internal/rbac/rbac.go:54-254`; netguard `netguard/netguard.go:77-145`; console guard `gameaction/action.go:31-120`; audit chain `api/internal/audit/audit.go:240-520`; secrets `api/internal/handlers/{shares,auth_provider_secret,registry_secret}.go`.
- **Rationale**: FR-003, FR-008, SC-002, SC-007, and Constitution V (tier-up review).
- **Alternatives considered**: Starting the first pass on haiku. The haiku scouts added unverified details, for example the invented "billing" step in the create wizard, so the first review pass starts at sonnet. The tier-escalation rule in CLAUDE.md 13 allows escalation after a demonstrated failure, and this is that failure.

## R13. Fix delivery and re-verification rounds

- **Decision**: Round N works like this:
  1. The live pass and reviews run on `rc.N`.
  2. Fixes land on `fix/018-F-xxx` branches off `master`, each with a regression test, and go through PR → CI green → human approval → merge.
  3. A new CHANGELOG RC section is added, and `rc.N+1` is tagged after approval.
  4. The failing procedures are repeated, plus every inventory row that shares a component with a fix (regression sweep, spec edge case).

  The loop ends when `findings.md` has zero open rows. Then comes the go decision, the status-wording commit, and the `v0.3.0` tag, which also needs approval. The audit records are committed on this branch in each round and opened as a draft PR.
- **Rationale**: The protected-master ruleset (CLAUDE.md 12) and FR-007 / SC-004.
- **Alternatives considered**: Batching all fixes into this one branch. It mixes audit records with product code and makes human review much harder.

## R14. Status wording and version markers (FR-015, FR-016)

- **Decision**: The final wording is "Status: **pre-v1 release** (`v0.3.0`)", with the README section "Beta Status & Limitations" renamed to "Pre-v1 Status & Limitations". Remaining caveats stay in `docs/roadmap.md` under "Wanted for v1, not blocking". Every location is listed in [contracts/status-wording.md](contracts/status-wording.md). Historical "shipped v0.2.0-beta.X" entries in the roadmap and CHANGELOG are left as they are. `hack/check-doc-versions.sh` enforces consistency.
- **Rationale**: OD-003: not a v1 or production-support claim.
- **Alternatives considered**: "Stable". That overclaims against the roadmap's v1 hardening items.

## R15. kubelab reachability

- **Decision**: The live phases need `kubelab-api` to resolve from the machine running the audit. On 2026-09-23 it failed with `dial tcp: lookup kubelab-api ... no such host`. Round 0's first task checks connectivity. If the check fails, stop live work and log it (**OD-007**), but carry on with the component reviews, inventory enumeration and known-bug import, which need no cluster.
- **Rationale**: This lets the non-live work go ahead while connectivity is sorted out.
- **Alternatives considered**: None. The cluster is the named target.
