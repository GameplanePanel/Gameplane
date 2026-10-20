---

description: "Task list for the v0.3 release readiness audit"
---

# Tasks: v0.3 Release Readiness Audit

**Input**: Design documents from `specs/done_018-v0-3-release-readiness/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/ (audit-records, test-resources, rc-deploy, status-wording), quickstart.md, OPEN-DECISIONS.md (OD-001 to OD-009, all resolved)

**Tests**: The spec asks for no TDD test tasks. Two rules still apply. Every finding's fix carries its own regression test (Constitution I; E2E in a `test/e2e/buckets.sh` bucket for user- or operator-facing paths). CI stays the verification authority (FR-017, CLAUDE.md Rule 8). Never run test or lint suites locally.

**Organization**: Tasks are grouped by user story. `audit/` is short for `specs/done_018-v0-3-release-readiness/audit/`. `KUBECONFIG=~/kubelab.yaml` is assumed for every `kubectl` and `helm` command.

**Execution rules for implementers**:
- Delegate through `Workflow` with an explicit `model:` on every `agent()` (CLAUDE.md 13). Record-keeping and enumeration start at `haiku`. Code review starts at `sonnet` (research R12). Verification runs one tier up. Browser runs use `sonnet` via Chrome MCP.
- Tasks marked **⛔ approval** stop until the maintainer approves. Log the pending approval in `OPEN-DECISIONS.md`, then carry on with independent tasks.
- Every resource created on kubelab is named `audit018-…` (contracts/test-resources.md). Never write to a pre-existing resource.
- No secrets in git: no cookies, CSRF tokens, share tokens, client secrets, kubeconfig or DB snapshots.
- Dashboard **visual** fixes: edit `design.pen` via Pencil MCP first, working blind (screenshots don't work on this machine). Export the JSON to `design-export/json/<id>.json`. Log the edit in `OPEN-DECISIONS.md` and wait for the maintainer's OK before writing React.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies)
- **[Story]**: US1 to US5 from spec.md

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Create the audit record skeleton and confirm the environment

- [X] T001 Create the audit record skeleton, using the exact table headers from contracts/audit-records.md: `audit/release-criteria.md`, `audit/kubelab-baseline.md`, `audit/inventory.md` (one empty `## <Area>` section each for WEB, API, CRD, AGT, AUX, HELM, MOD, UPG, NODE, SEC), `audit/coverage.md`, `audit/findings.md`, `audit/rounds.md` (with a `## rc.0` section), `audit/report.md` (section headings only, in contract order), `audit/procedures/.gitkeep`, `audit/evidence/.gitkeep`
- [X] T002 [P] Run `git submodule update --init modules`. List every `modules/*/module.yaml` with its console/RCON protocol family and join-protocol family, which defines the module **category** (research R10). Write the list to `audit/evidence/rc.0/module-categories.md`
- [X] T003 [P] Create the off-git snapshot directory `~/gameplane-audit-018/db-snapshots/` with mode `700`. Record the path, and only the path, under `## rc.0` in `audit/rounds.md`
- [X] T004 [P] Check kubelab connectivity (OD-007) with `getent hosts kubelab-api && kubectl get nodes -o wide`, and record the result under `## rc.0` in `audit/rounds.md`. If the check fails, mark every live task (T013–T015, Phase 3 run tasks, T045–T049, Phase 6) as waiting on OD-007 and continue with the non-live tasks

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Fix the release criteria, import known bugs, snapshot kubelab, and get rc.1 onto kubelab. These must all happen before any live pass.

**⚠️ CRITICAL**: No live inventory run (US1/US2 live rows/US4) may start until T013–T015 complete. The component reviews (US2 code-review tasks) only need T005 to T011.

- [X] T005 Write `audit/release-criteria.md` with RC-01 to RC-08 in the columns `| ID | Criterion | Source | Evidence | Met |`. Every `Met` starts as `pending`. Criteria, verbatim from data-model.md:
  - RC-01: every inventory row has an outcome, zero rows `fail`, and every `blocked` row names its prerequisite and is listed as not live-verified (SC-001)
  - RC-02: every component has a `complete` review record (SC-002)
  - RC-03: zero findings in `open`, `fixing` or `fixed-unverified` (SC-003, SC-004)
  - RC-04: each FR-008 boundary has at least one active violation attempt recorded (SC-007)
  - RC-05: live beta.8 → RC upgrade with zero data loss and zero lost accounts (SC-005, OD-005)
  - RC-06: the baseline snapshot matches the post-cleanup snapshot, with zero `audit018-` resources remaining (SC-006)
  - RC-07: every `not-a-defect` and `out-of-scope` closure is justified, and each out-of-scope one cites a roadmap line (SC-009)
  - RC-08: CI is green on the tagged commit (FR-017)

  Add an empty `## Change log`. Commit this file on its own **before** any live task (FR-015)
- [X] T006 [P] Write `audit/tools/snapshot.sh`, a read-only script that takes an output directory. It must capture everything listed in contracts/test-resources.md § Baseline snapshot:
  - name, namespace, UID, `metadata.generation` and `status.phase` for GameServer, GameTemplate, Backup, BackupSchedule, Restore, Module, ModuleSource, NetworkCapture and Cluster across all namespaces
  - PVCs in the games and API namespaces
  - nodes
  - `helm list -A`, plus `helm get values` for the Gameplane release, with any key matching `(?i)secret|token|password|key|dsn` replaced by `<redacted>`

  Output one JSON file per scope. It must use only `kubectl get` and `helm get`/`helm list`
- [X] T007 [P] Write `audit/tools/snapshot-diff.sh <before-dir> <after-dir>`. It skips objects whose name starts with `audit018-`, status-only fields, and the Gameplane Helm release with its own Deployments. It exits non-zero on any UID or `generation` mismatch, and prints the mismatches
- [X] T008 [P] Write `audit/tools/cleanup-check.sh`. It lists every remaining `audit018-` object: `kubectl get gameservers,backups,restores,backupschedules,networkcaptures,modulesources,modules,pvc -A -o name | grep audit018-`. It exits non-zero if any remain
- [X] T009 (done 2026-09-24: F-001..F-013; checked by an opus tier-up review, 29 corrections applied) Import the known bugs into `audit/findings.md` with status `imported` (FR-006, research R11). One `F-NNN` row per source, plus a `### F-NNN` subsection with Repro, Expected, Actual and Evidence:
  - issues #414, #377, #376, #375, #373, #306
  - merged fix PRs #416, #411, #410, #409, #396, #350, #327
  - Get each title and body from `gh issue view <n>` or `gh pr view <n>`. `Origin` is `imported:#<n>`
- [X] T010 (done 2026-09-24: F-014..F-025; none held, since PR #350 fixed the SECURITY_AUDIT.md items) Import every finding in `SECURITY_AUDIT.md` into `audit/findings.md` as `imported:SECURITY_AUDIT.md#<n>`. Then import the spec-derived items:
  - Nuclear Option UDP 7777 not bound (`imported:specs/002-nuclear-option-ip-pool/spec.md`)
  - OIDC Helm role mappings sitting under CHANGELOG "Unreleased" (`imported:specs/012-docs-refresh-and-outreach/OPEN-DECISIONS.md#OD-8`)
  - each open RCON-protocol question in `specs/done_015-top-steam-game-modules/OPEN-DECISIONS.md` (Factorio, Project Zomboid, `cli`, `rest`), one finding each
- [X] T011 (done 2026-09-24: F-026..F-030) Seed the drift findings found during planning into `audit/findings.md`, with origin `review:<component>` and the listed severity:
  - (a) `hack/check-doc-versions.sh:19,94,103,151` recognises only `-beta.N` versions, and uses single-digit `[0-9]` where the header comment says `[0-9]+`. S3
  - (b) `docs/install.md` has no rollback procedure. S3
  - (c) `charts/gameplane/values.yaml:473` git module source is pinned to `ref: v0.2.0-beta.6`. S3
  - (d) the CI upgrade baseline is still `0.2.0-beta.5` in `deploy/kind/upgrade.sh:36`, `.github/workflows/ci.yaml:968-970` and `.claude/agents/ci-triager.md:65`. S3
  - (e) the `CLAUDE.md` repository map says "14 Go modules" and omits `gp-module/`. S4
- [~] T012 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). The API user list was never captured; RC-06 is recorded as not met. (2026-09-24: `audit018-admin` created via bootstrap-admin (OD-015). Recorded: roles, module sources, auth-provider and notification-sink names, and DB migration level 011 (inferred from a read-only probe of a migration-011 table; beta.8 ships up to 006, so OD-005 path (b)). Still missing: the API user list. Auto mode's safety check denied that call; fetch it in the default permission mode) Capture the baseline: run `audit/tools/snapshot.sh audit/evidence/baseline/`, then write `audit/kubelab-baseline.md` with the chart version, the image refs in use, the redacted `helm get values`, the module source, the ingress host and the API namespace. Record the API user list, role list, module-source list, auth-provider names and notification-sink names, fetched with a single admin login. Also record the API DB's highest applied migration and compare it with the highest migration file present at tag `v0.2.0-beta.8` (`git ls-tree v0.2.0-beta.8 api/internal/db/migrations/`). This comparison decides the OD-005 path used in T061. Check with `grep -iE 'secret|token|password' audit/kubelab-baseline.md`: it must show key names only
- [X] T013 ⛔ approval. Prepare rc.1 (done 2026-09-23: PR #423; RC-TAG-1 logged, waiting on merge and tag approval):
  1. On branch `chore/018-rc1-changelog` off `master`, add a `## [0.3.0-rc.1]` section to `CHANGELOG.md` that summarises the unreleased entries.
  2. Open a PR with labels `type: chore` and `area: specs`.
  3. Log `RC-TAG-1: pending` in `specs/done_018-v0-3-release-readiness/OPEN-DECISIONS.md`, with the commit SHA to tag once the PR is merged and CI is green.
- [~] T014 **WITHDRAWN 2026-10-04, superseded by rc.2** (maintainer decision 2026-10-04; live rounds target rc.2, RC-TAG-2). `release.yaml` run 36051057887 for `v0.3.0-rc.1` was cancelled: the operator image was never built, and there is no chart and no GitHub release (recorded under `## rc.1` in `audit/rounds.md`). The deviation is F-257, fixed by #435. Original task text kept below for the record. ⛔ approval. After approval, run `git tag -a v0.3.0-rc.1 <sha> -m "v0.3.0-rc.1" && git push origin v0.3.0-rc.1` (annotated, unsigned, per OD-013). Then check the `release.yaml` run against contracts/rc-deploy.md §1:
  - it is green
  - images `ghcr.io/valgulnecron/gameplane/<component>:v0.3.0-rc.1` exist and pass `cosign verify --key cosign.pub`
  - chart `oci://ghcr.io/valgulnecron/charts/gameplane:0.3.0-rc.1` exists and is signed
  - the GitHub release is marked prerelease
  - no `0.3` image tag was created or moved

  Record the results under `## rc.1` in `audit/rounds.md`. Any deviation becomes a finding
- [~] T015 **WITHDRAWN 2026-10-04, superseded by rc.2** (maintainer decision 2026-10-04; live rounds target rc.2, RC-TAG-2). `release.yaml` run 36051057887 for `v0.3.0-rc.1` was cancelled: the operator image was never built, and there is no chart and no GitHub release (recorded under `## rc.1` in `audit/rounds.md`). The deviation is F-257, fixed by #435. Original task text kept below for the record. Deploy rc.1 to kubelab (contracts/rc-deploy.md §2):
  1. Take a DB snapshot through the temporary `audit018-db-tool` pod, not `kubectl exec` into the API pod — the API image is `gcr.io/distroless/static:nonroot` with no shell and no `sqlite3` binary (same fact `audit/procedures/upgrade.md`'s baseline-beta8 step 1 documents and works around): scale `gameplane-api` to 0, apply a throwaway pod that mounts the `gameplane-api-data` PVC, `apk add --no-cache sqlite`, run `sqlite3 /data/gameplane.db ".backup /tmp/rc1-pre.db"`, `kubectl cp` it out to `~/gameplane-audit-018/db-snapshots/rc1-pre.db`, delete the pod, then scale `gameplane-api` back to 1.
  2. Run `helm upgrade <release> oci://ghcr.io/valgulnecron/charts/gameplane --version 0.3.0-rc.1 -n <ns> --reuse-values` with only the image registry and tag overrides needed to leave the private tag.
  3. Record each override key under `## rc.1` in `audit/rounds.md`.
  4. Check that `helm get values` minus those overrides equals `audit/kubelab-baseline.md`.

**Checkpoint**: Criteria are committed, known bugs imported, baseline captured and rc.1 running. The US1–US4 work can start.

---

## Phase 3: User Story 1 - Every shipped feature is proven on a real cluster (Priority: P1) 🎯 MVP

**Goal**: A complete feature inventory, enumerated from code, with each row run live on kubelab and given an outcome and evidence.

**Independent Test**: `grep -cE '\| (untested|fail) \|' audit/inventory.md` returns 0. Every `blocked` row has a reason naming its prerequisite. Every row links `evidence/<ID>/`.

### Enumeration: each writes its own procedures file and a draft rows file

- [X] T016 [P] [US1] (done 2026-09-24: audit/procedures/web.md and evidence/rc.0/inventory-WEB.md; merged by T023) Enumerate the dashboard capabilities from `web/src/router/tree.tsx` and each route file in `web/src/routes/`: one row per screen action. Write `audit/procedures/web.md`, with one `### <slug>` per row carrying Preconditions, Resources created (all `audit018-`), Steps, Expected, Cleanup and Automatable?. Write the draft rows to `audit/evidence/rc.0/inventory-WEB.md` with IDs `INV-WEB-NNN` and a `Source` `file:line` for each
- [X] T017 [P] [US1] (done 2026-09-24: audit/procedures/api.md and evidence/rc.0/inventory-API.md; merged by T023) Enumerate the API capabilities from `api/cmd/main.go` and the rule table in `api/internal/rbac/rbac.go:162-254`: one row per route group and action, with the required permission noted. Write `audit/procedures/api.md` and `audit/evidence/rc.0/inventory-API.md` (`INV-API-NNN`). Each procedure uses one cookie jar per role and sends the CSRF header. Note each procedure's login cost against the budget: IP burst 10 (5/min), user burst 6 (3/min)
- [X] T018 [P] [US1] (done 2026-09-24: audit/procedures/crd.md and evidence/rc.0/inventory-CRD.md; opus re-run and review rounds applied, OD-021) Enumerate the CRD lifecycles from `operator/api/v1alpha1/*_types.go` and `operator/internal/controller/*_controller.go`: one row per kind per lifecycle transition, for GameServer, GameTemplate, Backup, Restore, Module, ModuleSource, Cluster, NetworkCapture and BackupSchedule. Write `audit/procedures/crd.md` and `audit/evidence/rc.0/inventory-CRD.md` (`INV-CRD-NNN`)
- [X] T019 [P] [US1] (done 2026-09-24: audit/procedures/agent.md and evidence/rc.0/inventory-AGT.md; merged by T023) Enumerate the agent capabilities from `agent/internal/*` and the agent's mounted endpoints in `agent/cmd/main.go`: console (PTY and each RCON protocol family), files, logs, players, lifecycle, quiesce, actions, status, mods, heartbeat, usage. Write `audit/procedures/agent.md` and `audit/evidence/rc.0/inventory-AGT.md` (`INV-AGT-NNN`)
- [X] T020 [P] [US1] (done 2026-09-24: audit/procedures/auxiliary-services.md and evidence/rc.0/inventory-AUX.md, blocked candidates name their prerequisites; merged by T023) Enumerate the auxiliary components (sentinel wake-on-connect, capture-sidecar, tunnel frp/tailscale/playit, audit-syslog-bridge, telemetry-receiver, mcp-server) and how each is enabled in `charts/gameplane/values.yaml`. Write `audit/procedures/auxiliary-services.md` and `audit/evidence/rc.0/inventory-AUX.md` (`INV-AUX-NNN`). Where a component needs something kubelab lacks (an external syslog collector, a tunnel account, a playit key), draft the row as a `blocked` candidate that names the prerequisite and the closest alternative
- [X] T021 [P] [US1] (done 2026-09-24: audit/procedures/helm.md and evidence/rc.0/inventory-HELM.md; merged by T023) Enumerate the install options from the top-level toggles in `charts/gameplane/values.yaml`: OIDC, audit webhook/S3/syslog, telemetry, clusterOps, capture, mcpServer, networkPolicies, podSecurity, default and upload module sources, serviceMonitors, prometheusRules, grafanaDashboards, `api.storage.existingClaim`. Write `audit/procedures/helm.md` and `audit/evidence/rc.0/inventory-HELM.md` (`INV-HELM-NNN`). Toggling an option on kubelab counts as an intended setting change and must be listed per round in `audit/rounds.md`. Options needing an external IdP or S3 are `blocked` candidates with a named prerequisite
- [X] T022 [P] [US1] (done 2026-09-24: audit/procedures/modules.md and evidence/rc.0/inventory-MOD.md; merged by T023) Enumerate the game-module rows from `audit/evidence/rc.0/module-categories.md` (T002): one row per module category. Each row runs the full cycle create → start → protocol join (use the `test/e2e` probe for that game where one exists) → console command → backup → restore → delete, on one representative module. Use `minecraft-java` for its category. Write `audit/procedures/modules.md` and `audit/evidence/rc.0/inventory-MOD.md` (`INV-MOD-NNN`). A module too heavy for kubelab's node resources is a `blocked` candidate with prerequisite "node memory/CPU"
- [X] T023 [US1] (done 2026-09-24: merged, 502 rows. 2026-09-24 later: the INV-UPG and INV-NODE rows now sit under their own headings; the INV-SEC rows and `procedures/security.md` are held off-git in `audit/held/` (OD-019). A sonnet correctness pass fixed eight procedures files; `crd.md` is re-running on opus, and OD-021 lists the design questions left open) Merge the drafts from T016–T022 into the matching `## <Area>` sections of `audit/inventory.md`, set every `Outcome` to `untested`, and fill the `CI test` column by matching each row to the `test/e2e/*_e2e_test.go` function that covers it (`none` if there isn't one). Check that the row counts per area equal the draft counts
- [X] T024 [US1] (done 2026-09-24: 7 rows added, 1 withdrawn, 51 Source lines fixed; see `audit/evidence/rc.0/inventory-review.md`) Tier-up completeness review (`sonnet`): compare `audit/inventory.md` against `web/src/router/tree.tsx`, `api/cmd/main.go`, `operator/api/v1alpha1/`, `agent/internal/` and `charts/gameplane/values.yaml`. Add any capability that was missed, listing each addition in `audit/evidence/rc.0/inventory-review.md`

### Live execution on rc.N (needs T015). Each writes only its own evidence folders

- [~] T025 **WITHDRAWN 2026-10-05, superseded by the 2026-10-04 live sweep** (maintainer decision 2026-10-05, [OD-029](OPEN-DECISIONS.md): the live round ran on the latest `master` commit instead of rc.2; results in `audit/rounds.md` § live-sweep 2026-10-04 and F-273..F-293). [P] [US1] Run every `INV-API-*` row from `audit/procedures/api.md` live against the kubelab ingress host (from `audit/kubelab-baseline.md`), logging in once per role. Save redacted request/response logs to `audit/evidence/INV-API-NNN/` and write the outcomes to `audit/evidence/rc.1/outcomes-API.md`. Rows that deliberately spend the login budget are deferred to T031 (2026-10-04 informal live sweep on master recorded in audit/rounds.md ## live-sweep 2026-10-04; formal per-row round still pending rc.2)
- [~] T026 **WITHDRAWN 2026-10-05, superseded by the 2026-10-04 live sweep** (maintainer decision 2026-10-05, [OD-029](OPEN-DECISIONS.md): the live round ran on the latest `master` commit instead of rc.2; results in `audit/rounds.md` § live-sweep 2026-10-04 and F-273..F-293). [P] [US1] Run every `INV-CRD-*` row from `audit/procedures/crd.md` using the API or `kubectl`. Every created object is named `audit018-…` and, if made with `kubectl`, labelled `gameplane.io/audit: "018"`. Save `kubectl get -o yaml` and `kubectl get events` extracts to `audit/evidence/INV-CRD-NNN/` and write the outcomes to `audit/evidence/rc.1/outcomes-CRD.md`
- [~] T027 **WITHDRAWN 2026-10-05, superseded by the 2026-10-04 live sweep** (maintainer decision 2026-10-05, [OD-029](OPEN-DECISIONS.md): the live round ran on the latest `master` commit instead of rc.2; results in `audit/rounds.md` § live-sweep 2026-10-04 and F-273..F-293). [P] [US1] Run every `INV-AGT-*` row from `audit/procedures/agent.md` against `audit018-` game servers, through the API/WS paths the dashboard uses. Save the evidence to `audit/evidence/INV-AGT-NNN/` and write the outcomes to `audit/evidence/rc.1/outcomes-AGT.md`
- [~] T028 **WITHDRAWN 2026-10-05, superseded by the 2026-10-04 live sweep** (maintainer decision 2026-10-05, [OD-029](OPEN-DECISIONS.md): the live round ran on the latest `master` commit instead of rc.2; results in `audit/rounds.md` § live-sweep 2026-10-04 and F-273..F-293). [P] [US1] Run every `INV-MOD-*` row from `audit/procedures/modules.md`, doing the full cycle per category with `audit018-<module>` servers. Save the join-probe output, console transcript and backup/restore marker check to `audit/evidence/INV-MOD-NNN/`, and write the outcomes to `audit/evidence/rc.1/outcomes-MOD.md`
- [~] T029 **WITHDRAWN 2026-10-05, superseded by the 2026-10-04 live sweep** (maintainer decision 2026-10-05, [OD-029](OPEN-DECISIONS.md): the live round ran on the latest `master` commit instead of rc.2; results in `audit/rounds.md` § live-sweep 2026-10-04 and F-273..F-293). [P] [US1] Run every `INV-AUX-*` and `INV-HELM-*` row from `audit/procedures/auxiliary-services.md` and `audit/procedures/helm.md`. Each Helm toggle is its own `helm upgrade --reuse-values --set <key>=<value>`, and the original value is restored right after. Record every toggle under `## rc.N` in `audit/rounds.md`. Save the evidence to `audit/evidence/INV-AUX-NNN/` and `audit/evidence/INV-HELM-NNN/`, and write the outcomes to `audit/evidence/rc.1/outcomes-AUX-HELM.md`
- [~] T030 **WITHDRAWN 2026-10-05, superseded by the 2026-10-04 live sweep** (maintainer decision 2026-10-05, [OD-029](OPEN-DECISIONS.md): the live round ran on the latest `master` commit instead of rc.2; results in `audit/rounds.md` § live-sweep 2026-10-04 and F-273..F-293). [P] [US1] Run every `INV-WEB-*` row from `audit/procedures/web.md` in the dashboard via Chrome MCP (`sonnet`). Create a new tab and avoid triggering JS dialogs. Save screenshots and the relevant console log lines to `audit/evidence/INV-WEB-NNN/`, and write the outcomes to `audit/evidence/rc.1/outcomes-WEB.md`. A visual defect becomes a finding; its fix follows the blind design-first rule in the header
- [~] T031 **WITHDRAWN 2026-10-05, superseded by the 2026-10-04 live sweep** (maintainer decision 2026-10-05, [OD-029](OPEN-DECISIONS.md): the live round ran on the latest `master` commit instead of rc.2; results in `audit/rounds.md` § live-sweep 2026-10-04 and F-273..F-293). [US1] Run the deferred rows that spend the login budget last in the round, against `audit018-` users only. A 429 inside a throttling test is the expected pass signal. Write the outcomes to `audit/evidence/rc.1/outcomes-API.md`
- [~] T032 **WITHDRAWN 2026-10-05, superseded by the 2026-10-04 live sweep** (maintainer decision 2026-10-05, [OD-029](OPEN-DECISIONS.md): the live round ran on the latest `master` commit instead of rc.2; results in `audit/rounds.md` § live-sweep 2026-10-04 and F-273..F-293). [US1] Classify intermittent results: repeat every row whose outcome differed between attempts at least 5 times, record `k/n` in the `flaky` note, and raise a finding for each (spec edge case)
- [~] T033 **WITHDRAWN 2026-10-05, superseded by the 2026-10-04 live sweep** (maintainer decision 2026-10-05, [OD-029](OPEN-DECISIONS.md): the live round ran on the latest `master` commit instead of rc.2; results in `audit/rounds.md` § live-sweep 2026-10-04 and F-273..F-293). [US1] Merge the outcomes from T025–T032 into `audit/inventory.md`, filling Outcome, Reason, Evidence, Round and Findings. For every `fail`, create an `F-NNN` in `audit/findings.md` with origin `live:INV-…`. For every row whose live outcome disagrees with the latest CI result for its `CI test`, create a finding (US1 AS-2). `blocked` rows must name the missing prerequisite and the closest alternative that was tested
- [X] T034 [US1] (done 2026-10-06 on public v0.3.0 instead of rc.1, recorded under `## v0.3.0` § Final kubelab state and cleanup in `audit/rounds.md`: cleanup-check exit 0; snapshot-diff 13 pre-upgrade changes accepted by the maintainer as intended, no finding; `gameplane-test-restic` leftover: maintainer decided to delete it; step 4 not checked, no admin login) Clean up after the round:
  1. Delete every `audit018-` resource through the path that created it.
  2. Run `audit/tools/cleanup-check.sh`, which must exit 0.
  3. Run `audit/tools/snapshot.sh audit/evidence/rc.1/after/` and `audit/tools/snapshot-diff.sh audit/evidence/baseline audit/evidence/rc.1/after`, which must exit 0.
  4. Check the API user, role and share lists hold no `audit018-` entries.
  5. Record the results and the test-resource table under `## rc.1` in `audit/rounds.md`. A leftover resource or a diff mismatch becomes an S1 finding.

**Checkpoint**: Every inventory row has a live outcome on rc.1, and the failures are in findings.md.

---

## Phase 4: User Story 2 - Every part of the code base receives a documented review (Priority: P1)

**Goal**: Each component has a complete review record, and each FR-008 security boundary has been tested with an active live violation attempt.

**Independent Test**: `grep -cE '\| (not-started|in-review) \|' audit/coverage.md` returns 0. Every component in contracts/audit-records.md § coverage.md has a row. All six boundaries have `INV-SEC-*` rows with a recorded attempt.

### Coverage table and code reviews (need only T005–T011; no cluster)

- [X] T035 [US2] (done 2026-09-24) Fill `audit/coverage.md` with one row per required component, status `not-started`:
  - `agent/`, `api/`, `audit-syslog-bridge/`, `capture-sidecar/`, `gameaction/`, `gameproto/`, `gp-module/`, `mcp-server/`, `netguard/`, `operator/`, `sentinel/`, `svcutil/`, `telemetry-receiver/`, `test/e2e/`, `tunnel/`
  - `web/`, `charts/gameplane/`, `deploy/`, `hack/`, `.github/workflows/`, `docs/`, root docs (`README.md`, `CHANGELOG.md`, `SECURITY_AUDIT.md`, `CLAUDE.md`), `design-export/`
  - `modules/` and `website/`, both with method `consistency-only`
- [X] T036 [P] [US2] (done 2026-09-24 on opus; sonnet reviewers were stopped by the safeguard in session 1; OD-020) Review `operator/` (`sonnet`) against `operator/specs.md` and its CRDs: correctness, security boundaries, `%w` wrapping, dead code, docs drift. Save the notes, and each candidate finding with its `file:line` and repro/observation, to `audit/evidence/review-operator/notes.md`
- [X] T037 [P] [US2] (done 2026-09-24 on opus, OD-020) Review `api/` (`sonnet`) against `api/specs.md`: auth, RBAC, handlers, ws, db migrations, audit, notify. Save to `audit/evidence/review-api/notes.md`
- [X] T038 [P] [US2] (done 2026-09-24 on opus, OD-020) Review `agent/` (`sonnet`) against `agent/specs.md`. Save to `audit/evidence/review-agent/notes.md`
- [X] T039 [P] [US2] (done 2026-09-24 on opus, OD-020) Review `web/` (`sonnet`) against `web/specs.md` and `design-export/`: TS strictness, no unjustified `any`, promise handling, login-privacy on unauthenticated views. Save to `audit/evidence/review-web/notes.md`
- [X] T040 [P] [US2] (done 2026-09-24 on opus, OD-020) Review `netguard/`, `gameaction/` and `gameproto/` (`sonnet`), each against its `specs.md`. Save to `audit/evidence/review-netguard/notes.md`, `audit/evidence/review-gameaction/notes.md` and `audit/evidence/review-gameproto/notes.md`
- [X] T041 [P] [US2] (done 2026-09-24 on opus, OD-020) Review `gp-module/`, `svcutil/`, `sentinel/`, `tunnel/`, `capture-sidecar/`, `audit-syslog-bridge/`, `telemetry-receiver/` and `mcp-server/` (`sonnet`). Save to `audit/evidence/review-<component>/notes.md` per component
- [X] T042 [P] [US2] (done 2026-09-24 on opus, OD-020) Review `charts/gameplane/`, `deploy/`, `hack/` and `.github/workflows/` (`sonnet`): Helm defaults exposure, RBAC, NetworkPolicies, CRD sync with `operator/config/crd/`, release pipeline correctness for `-rc.N` and final tags. Save to `audit/evidence/review-<component>/notes.md` per component
- [X] T043 [P] [US2] (done 2026-09-23, verified by opus) Review `test/e2e/` (`sonnet`): bucket registration in `test/e2e/buckets.sh`, `t.Parallel()` with unique names, shared-state guards, unbucketed heavy tests commented with the reason. Save to `audit/evidence/review-test-e2e/notes.md`
- [X] T044 [P] [US2] (done 2026-09-23, verified by opus) Review the docs (`sonnet`): `docs/*.md`, `README.md`, `CHANGELOG.md`, `SECURITY_AUDIT.md`, `CLAUDE.md`, and consistency with `design-export/`. Also do the consistency-only review of the `modules/` and `website/` submodules against product behaviour. Save to `audit/evidence/review-docs/notes.md`, `audit/evidence/review-modules/notes.md` and `audit/evidence/review-website/notes.md`
- [X] T045 [US2] (done 2026-10-04: all coverage rows complete; verification index in audit/evidence/review-verification.md) Tier-up verification (`opus`): check every candidate finding from T036–T044 against the code at its `file:line`, and reject the speculative ones. Record the kept findings in `audit/findings.md` with origin `review:<component>` and a severity from S1 to S4 (research R3). Record the rejected ones in `audit/evidence/review-verification.md` with the reason. Update each `audit/coverage.md` row to `complete`, with Scope reviewed, Method, Reviewer tier (`sonnet → opus`), Date and Findings

### Security boundaries: active violation attempts (FR-008, SC-007; need T015)

- [~] T046 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). The security procedure and INV-SEC rows stay held off-git (OD-019); RC-04 is recorded as not met. [US2] (drafted 2026-09-24 but defective and unreviewed; DO NOT RUN. Held off-git with the INV-SEC rows in `audit/held/` per OD-019; the rewrite as control checks stays there too. 2026-09-24: an opus rewrite attempt was stopped by the auto-mode safety check before writing anything; redo it in the default permission mode) Write `audit/procedures/security.md` with one `### <slug>` per boundary, and add the `INV-SEC-NNN` rows to `audit/inventory.md`. The six boundaries:
  - (a) login privacy: `api/internal/auth/local.go:105-173`, `/auth/providers`, and identical errors for an unknown user and a wrong password
  - (b) RBAC: `api/internal/rbac/rbac.go:54-254`. Viewer write → 403, the collaborator-only paths, cross-namespace access
  - (c) netguard: `netguard/netguard.go:77-145`. `audit018-` ModuleSource at `http://169.254.169.254/…` and `metadata.google.internal`, and an agent mod fetch to CGNAT `100.64.0.0/10`
  - (d) console guard: `gameaction/action.go:31-120`. CRLF inside an action param, and a param longer than 512 characters
  - (e) audit-chain tamper: `api/internal/audit/audit.go:240-520`, per OD-008/OD-009
  - (f) secret handling: the share token shown only on create and redacted in audit, and the client secret and registry API key never echoed
- [~] T047 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). RC-04 is recorded as not met. [US2] Run the live violation attempts for boundaries (a), (b), (c), (d) and (f) from `audit/procedures/security.md` using `audit018-` users and resources. Save the redacted evidence to `audit/evidence/INV-SEC-NNN/` and update the outcomes in `audit/inventory.md`. Any boundary that doesn't hold is an S1 finding
- [~] T048 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). RC-04 is recorded as not met. [US2] Run the live audit-chain tamper test for boundary (e) in a quiet window with no other admin activity (OD-008, OD-009, contracts/test-resources.md). For **each** variant — UPDATE an `audit018-` row, DELETE a middle `audit018-` row, truncate the tail:
  1. Scale the API to 0.
  2. `sqlite3 .backup` to `~/gameplane-audit-018/db-snapshots/tamper-<variant>.db`.
  3. Scale the API up.
  4. Apply the tamper.
  5. Record that `Verify` detects the break (and that the dashboard shows the tamper banner).
  6. Scale the API to 0, restore the snapshot, and scale up.
  7. Record that `Verify` is ok again.

  Save the evidence to `audit/evidence/INV-SEC-<e>/` and write the outcomes into `audit/inventory.md`

**Checkpoint**: Coverage is 100% complete, and every security boundary has been attacked live.

---

## Phase 5: User Story 3 - Discovered bugs are fixed and re-verified live (Priority: P1)

**Goal**: Every finding ends as `verified`, `closed-already-fixed`, `not-a-defect` or `out-of-scope`, each with its proof or justification.

**Independent Test**: `grep -cE '\| (imported|open|fixing|fixed-unverified) \|' audit/findings.md` returns 0. Every `verified` row links a passing live repeat on a named `rc.N`. Every `out-of-scope` row cites a `docs/roadmap.md` line.

- [~] T049 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). The 25 `imported` findings stay `imported`; RC-03 is recorded as not met. [US3] Reproduce each `imported` finding live on rc.1 using its `### F-NNN` repro. Set it to `open`, or to `closed-already-fixed` when the repro passes, with evidence in `audit/evidence/F-NNN/`. Findings that can't be reproduced live (docs or CI-only) are checked by reading the current `master` file at the cited lines
- [X] T050 [US3] (done 2026-09-24: fix plan in `audit/evidence/rc.1/fix-plan.md`, checked by opus; the held plan is off-git; sign-off requested in OD-024) Triage `audit/findings.md`:
  - Order the `open` findings S1 → S4, and within each severity put core paths first.
  - Close items as `not-a-defect` only with a written justification.
  - Close items as `out-of-scope` only for capabilities `docs/roadmap.md` places after v0.3 (for example "Postgres driver" `docs/roadmap.md:228-234` or "Explicitly out of scope for v1"), citing the line.
  - Write the fix plan to `audit/evidence/rc.1/fix-plan.md`: groups of findings that share a component become one branch each.
- [X] T051 [P] [US3] (done 2026-09-24: PR #422 merged, labels type: ci and area: e2e; F-029 fixed-unverified, Fix #422) Fix F-(d), the upgrade baseline, on branch `fix/018-upgrade-baseline`. Set `FROM_VERSION` default to `0.2.0-beta.8` in `deploy/kind/upgrade.sh:36`, `GAMEPLANE_UPGRADE_FROM: 0.2.0-beta.8` and its comment in `.github/workflows/ci.yaml:968-970`, and the text in `.claude/agents/ci-triager.md:65`. Open a PR labelled `type: ci` and `area: e2e` via the REST API (CLAUDE.md 14), and set the finding to `fixing` with the PR number
- [X] T052 [P] [US3] (done 2026-09-24: PR #420 merged, labels type: fix and area: shared; bare versions plus the dependency marker (OD-011) and the fixture harness `make test-doc-versions` run in CI (OD-012); F-026 fixed-unverified, Fix #420) Fix F-(a), the doc-version checker, on branch `fix/018-doc-versions-checker`. Change `hack/check-doc-versions.sh` so it recognises `X.Y.Z` and `X.Y.Z-(beta|rc).N`, using the multi-digit `[0-9]+` everywhere, including lines 94, 103 and 151. It must still accept historical-marked lines and the current `appVersion`. Add a fixture case under the checker's existing test harness if one exists, otherwise log the test gap in `OPEN-DECISIONS.md` (tests need sign-off). Open a PR labelled `type: fix` and `area: shared`
- [X] T053 [P] [US3] (done 2026-09-24: PR #421 merged, labels type: docs and area: shared; F-030 fixed-unverified, Fix #421) Fix F-(e), the repo map, on branch `docs/018-claude-md-gp-module`. Add `gp-module/` to the repository map in `CLAUDE.md` and correct the Go-module count to match `go.work`. Open a PR labelled `type: docs` and `area: shared`
- [X] T054 (done 2026-10-06: the maintainer named `gameplane-module` tag `v0.3.0` (commit `b42eea2`); `charts/gameplane/values.yaml` `ref: v0.3.0` landed in #587 instead of a separate fix branch) [US3] (logged 2026-09-23 in OPEN-DECISIONS.md § T054; the bump waits on the maintainer naming the tag) Fix F-(c), the module source ref: log in `OPEN-DECISIONS.md` that `charts/gameplane/values.yaml:473` `ref: v0.2.0-beta.6` needs a `gameplane-module` tag tested with v0.3.0. Once the maintainer names the tag, bump the ref on branch `fix/018-module-source-ref` and open a PR labelled `type: fix` and `area: chart`
- [~] T055 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). Remaining `open` findings stay open after v0.3.0; RC-03 is recorded as not met. [US3] For each remaining `open` finding, in the order of `fix-plan.md`, run a fix wave in a `Workflow`:
  - `haiku` makes the fix from a scout brief (CLAUDE.md 18), escalating a tier only on demonstrated failure; review runs one tier up.
  - Branch: `fix/018-F-NNN`.
  - Add a regression test: E2E in a `test/e2e/buckets.sh` bucket for user- or operator-facing paths. Never weaken an existing test.
  - Update the owning `specs.md` when behaviour changes.
  - Visual web fixes follow the blind design-first rule in the header.
  - Push the branch, open the PR with its `type:` and `area:` labels, and set the finding to `fixing`.
- [~] T056 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). Fix PRs merged before the decision are recorded in `audit/findings.md`. [US3] Watch CI on every fix PR (`gh pr checks`) and fix failures with follow-up commits. When a PR is merged (after human approval), set its finding to `fixed-unverified` with `Fix` = the PR number, and delete the merged branch locally and on the remote
- [~] T057 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). The maintainer chose to skip rc.2 and tag `v0.3.0` directly (T073). [US3] ⛔ approval. Cut the next release candidate:
  1. Add a `## [0.3.0-rc.N+1]` section to `CHANGELOG.md` on branch `chore/018-rcN-changelog` and open a PR.
  2. Log `RC-TAG-N+1: pending` in `OPEN-DECISIONS.md`.
  3. After merge and approval, tag and verify as in T014.
  4. Deploy as in T015, under a new `## rc.N+1` section in `audit/rounds.md`.
- [~] T058 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). No rc.N+1 was cut (T057). [US3] Re-verify on rc.N+1:
  - Repeat the live repro of every `fixed-unverified` finding. On a pass, set it to `verified` with `Re-verified` = `rc.N+1` and an evidence link. On a fail, set it back to `open`.
  - Regression sweep: reset to `untested`, and re-run, every `audit/inventory.md` row whose component had a merged fix.
  - Clean up as in T034.
- [~] T059 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). The loop ended at the go decision with RC-03 not met. [US3] Loop: repeat T050 and T055–T058 until `grep -cE '\| (imported|open|fixing|fixed-unverified) \|' audit/findings.md` returns 0 **and** no inventory row is `fail` or `untested`. Record each round's totals in `audit/rounds.md`

**Checkpoint**: Zero blocking findings, and every fix has been re-verified live.

---

## Phase 6: User Story 4 - Upgrade and lifecycle safety on a long-lived cluster (Priority: P2)

**Goal**: The beta.8 → RC upgrade, restart, rollback, multi-node scheduling, drain and node loss are all shown safe for data and accounts.

**Independent Test**: The `INV-UPG-*` and `INV-NODE-*` rows in `audit/inventory.md` are `pass`, with evidence. The data marker, the seeded admin login and the audit `Verify` all survive the upgrade and the rollback. The snapshot diff is clean after the node-loss test.

- [X] T060 [P] [US4] (done 2026-09-24: audit/procedures/upgrade.md with all six sections (baseline-beta8, seed, upgrade-to-rc, restart, rollback, restore-real-db), INV-UPG-001..005 in inventory.md; OD-023 reinstall caveat noted in the file) Write `audit/procedures/upgrade.md`, following contracts/rc-deploy.md §2a and §3:
  - `### baseline-beta8`: the OD-005 path picked by T012
  - `### seed`: `audit018-upg` server with a marker file `/data/audit018-marker` containing a random string recorded in evidence, plus an `audit018-admin` account
  - `### upgrade-to-rc`, `### restart` (API and operator), `### rollback`, `### restore-real-db`

  Add the rows `INV-UPG-001` to `INV-UPG-005` to `audit/inventory.md`
- [~] T061 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). RC-05 is recorded as not met; INV-UPG rows stay `untested`. [US4] Run the upgrade round on the latest RC:
  1. Take a real DB snapshot to `~/gameplane-audit-018/db-snapshots/upg-real.db`.
  2. Bring kubelab to public `v0.2.0-beta.8`. If T012 showed kubelab's migrations are ahead of beta.8, `helm uninstall` without deleting the CRDs or game PVCs, then reinstall beta.8 with a fresh API DB (OD-005 option b). Otherwise run a `helm upgrade` to beta.8.
  3. Seed, then upgrade to the RC.
  4. Verify that the `audit018-upg` server comes back Running, the marker is byte-identical, `audit018-admin` can log in, and audit `Verify` is ok. Check that pre-existing GameServers stayed running throughout.
  5. Save the evidence to `audit/evidence/INV-UPG-NNN/`. Any loss is an S1 finding.
- [~] T062 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). RC-05 is recorded as not met. [US4] Restart and rollback on the upgraded install:
  1. Restart the API and operator deployments, and verify the seeded state.
  2. `helm rollback <release> <beta.8-revision>`. If new migrations were applied, scale the API to 0, restore the pre-upgrade snapshot, and scale up.
  3. Verify that the previous release serves logins, lists `audit018-upg`, and the marker is intact.
  4. Upgrade forward to the RC again.
  5. Restore the real DB snapshot `upg-real.db` (contracts/rc-deploy.md §2a step 4), then run `audit/tools/snapshot-diff.sh`.

  Write the exact working rollback steps to `audit/evidence/INV-UPG-rollback/steps.md`
- [~] T063 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). No upgrade round ran, so there are no rollback steps to document. [US4] Fix F-(b) with the rollback docs on branch `docs/018-install-rollback`: add a "Rolling back an upgrade" section to `docs/install.md` built from `audit/evidence/INV-UPG-rollback/steps.md`, including the DB-snapshot requirement for forward-only migrations. Open a PR labelled `type: docs` and `area: chart`
- [X] T064 [P] [US4] (done 2026-09-24: audit/procedures/nodes.md with scheduling, drain and node-loss; INV-NODE-001..003 in inventory.md) Write `audit/procedures/nodes.md`:
  - `### scheduling`: several `audit018-` servers, recording each pod's node
  - `### drain`: `kubectl cordon <node>`, Eviction of the `audit018-` pod only, observe, `kubectl uncordon`. A full `kubectl drain` is never used.
  - `### node-loss`: stop `k3s-agent` for a few minutes on a worker holding no pre-existing stateful game server (OD-006), observe the `audit018-` server, restart `k3s-agent`, and verify pre-existing pods recovered

  Add the rows `INV-NODE-001` to `INV-NODE-003` to `audit/inventory.md`
- [~] T065 **WITHDRAWN 2026-10-06, superseded by the v0.3.0 go decision** (maintainer decision 2026-10-05, T070, [report.md#decision](audit/report.md#decision): go with the not-met criteria accepted as known risk; `v0.3.0` shipped 2026-10-06, `audit/rounds.md` § v0.3.0). INV-NODE rows stay `untested`. [US4] Run `audit/procedures/nodes.md` live:
  - Pick the node-loss worker with `kubectl get pods -A -o wide`, avoiding any node that hosts a pre-existing GameServer pod, and record the node and time window.
  - Compare the observed behaviour (rescheduling, waiting, or a clear error state) with `docs/architecture.md` and `operator/specs.md`. A mismatch becomes a finding (FR-013).
  - Save the evidence to `audit/evidence/INV-NODE-NNN/`.
  - Afterwards run `audit/tools/snapshot.sh` and `audit/tools/snapshot-diff.sh` against the baseline.

**Checkpoint**: Upgrade, rollback and node behaviour are verified, and kubelab is back at the RC with its real data.

---

## Phase 7: User Story 5 - A release decision backed by a clear report (Priority: P2)

**Goal**: One report that a cold reader can use to reach go/no-go in under 15 minutes, and a status-wording change set that's ready to apply.

**Independent Test**: A reviewer who didn't run the audit reads `audit/report.md` alone and states the decision and its reasons correctly within 15 minutes (SC-008).

- [X] T066 (done 2026-10-05: `audit/report.md`, merged in #586) [US5] Build `audit/report.md` in the section order from contracts/audit-records.md § report.md:
  - Totals: inventory by outcome, findings by severity and status, coverage percentage.
  - Open findings, which must be empty for a go.
  - Not live-verified: every `blocked` row, with its prerequisite and the alternative tested.
  - By component (from `audit/coverage.md`) and by area.
  - Proposed E2E additions: every procedure marked `Automatable? yes`, with its proposed bucket (FR-012).
  - The release-criteria table with `Met` filled from the evidence.
- [X] T067 (done 2026-10-05: re-check recorded in `contracts/status-wording.md`, applied in #587) [US5] Re-run the re-check command in `contracts/status-wording.md` on the current `master` and update that contract's location list with any new hits. Copy the final change set into the "Status-wording change set" section of `audit/report.md`
- [X] T068 (done 2026-10-05: `audit/evidence/release-notes-v0.3.0.md`, merged in #586) [US5] Draft the release notes in `audit/evidence/release-notes-v0.3.0.md`: highlights from the CHANGELOG RC sections, the "Not live-verified" list copied word for word (FR-002, SC-001), and the pre-v1 caveats from `docs/roadmap.md` "Wanted for v1, not blocking"
- [X] T069 (done 2026-10-05: `audit/evidence/cold-read.md`, merged in #586) [US5] Cold-read check (`sonnet`, a fresh agent that has seen none of the audit): give it only `audit/report.md` and ask for the decision, the reasons, and the time taken. Record the answer in `audit/evidence/cold-read.md`. If it can't reach the right conclusion, revise `audit/report.md` and repeat
- [X] T070 (done 2026-10-05: go, maintainer valgulnecron, recorded in `audit/report.md` § Decision and `audit/release-criteria.md`, merged in #586) [US5] ⛔ approval. Request the go/no-go decision from the maintainer. Record the maintainer's name, date and decision in `audit/report.md` § Decision, and set `Met` in `audit/release-criteria.md`

**Checkpoint**: The decision is recorded. On a go, continue to Phase 8.

---

## Phase 8: Polish & Cross-Cutting Concerns (on a go decision)

**Purpose**: Ship v0.3.0 with consistent status wording, and close out the audit.

- [X] T071 (done 2026-10-05: #587, merged 2026-10-06) Apply `contracts/status-wording.md` in one commit on branch `chore/018-v0.3.0-status`:
  - `charts/gameplane/Chart.yaml:5-6` → `0.3.0`
  - `web/package.json:4` → `0.3.0`, and regenerate `web/package-lock.json` with `npm install --package-lock-only`
  - status wording in `README.md` (including the `#beta-status--limitations` anchor), `CLAUDE.md:6`, `docs/roadmap.md`, `docs/install.md`, `docs/dependencies.md`, `telemetry-receiver/README.md`, `web/specs.md:740`, every `*/specs.md` and `test/e2e/**/spec.md` status line, and the `.github/workflows/publish-edge.yaml:3` comment
  - leave everything under "Left as-is" untouched

  Re-run the contract's re-check command and confirm every remaining hit is on the allow-list
- [X] T072 (done 2026-10-05: `## [0.3.0]` in `CHANGELOG.md`, #587, merged 2026-10-06) Add a `## [0.3.0]` section to `CHANGELOG.md` in the same branch, moving the relevant "Unreleased" entries into it. Open a PR labelled `type: chore` and `area: shared` and wait for CI green and human approval
- [X] T073 (done 2026-10-06: `v0.3.0` tagged on `040e10e`, release run 37411911079 green; recorded under `## v0.3.0` in `audit/rounds.md`) ⛔ approval. After merge, tag `v0.3.0` (`git tag -a v0.3.0 <sha> -m "v0.3.0"`, annotated, unsigned, per OD-013). Check `release.yaml` is green, the images and chart are signed, the GitHub release is **not** marked prerelease, the notes come from the `## [0.3.0]` section, and the `0.3` image tag now exists. Record the results under `## v0.3.0` in `audit/rounds.md`
- [X] T074 ⛔ approval. (2026-10-06: maintainer answered "stay on public `v0.3.0`"; done 2026-10-06: Helm rev 10, chart 0.3.0; results under `## v0.3.0` § Final kubelab state and cleanup in `audit/rounds.md`) Ask the maintainer whether kubelab stays on public `v0.3.0` or goes back to the baseline values (contracts/rc-deploy.md §4). Apply the answer, then run a final `audit/tools/cleanup-check.sh` and `audit/tools/snapshot-diff.sh audit/evidence/baseline <final>`, and record the result in `audit/rounds.md` (SC-006)
- [X] T075 (done 2026-10-06: `audit/evidence/final-validation.txt`; checks 1 and 2 not met, accepted at T070) Run the final validation greps from `quickstart.md` § Final validation and record the output in `audit/evidence/final-validation.txt`
- [X] T076 (done 2026-10-06: every task is `[X]` or withdrawn; archived to `specs/done_018-v0-3-release-readiness`) Mark every task in this file `[X]`, or withdrawn with a citation. Once this feature's PR is merged into `master`, `git mv specs/018-v0-3-release-readiness specs/done_018-v0-3-release-readiness` and update every in-repo reference in the same `docs:` commit (Constitution IV, CLAUDE.md 16)

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: T005–T011 need only Phase 1. T012–T015 are live and depend on T004 (OD-007 connectivity) plus the maintainer's approval for T013 and T014.
- **US1 (Phase 3)**: Enumeration (T016–T024) needs only Phase 1. The live run (T025–T034) needs T015.
- **US2 (Phase 4)**: The code reviews (T035–T045) need only T009–T011, so they can start straight away, even while kubelab is unreachable. The security live tasks (T046–T048) need T015.
- **US3 (Phase 5)**: Needs findings from US1 and US2. T051–T053 can start as soon as T011 is done.
- **US4 (Phase 6)**: Needs T012 (the OD-005 path) and at least one published RC. Run it on the latest RC, ideally the final candidate.
- **US5 (Phase 7)**: Needs US1–US4 complete, with zero blocking findings.
- **Polish (Phase 8)**: Needs a go decision (T070).

### User Story Dependencies

- **US1 and US2** are independent of each other. Both feed US3.
- **US3** depends on the findings from US1, US2 and US4, and loops until they're all closed.
- **US4** is independent of US1 and US2, apart from the shared RC deploy.
- **US5** depends on all the others.

### Within Each User Story

- Enumeration comes before the live run, the live run before merging outcomes, and merging before cleanup.
- Candidate findings come before tier-up verification, and verification before `findings.md`.
- A fix comes before its PR, then CI green, then human merge, then RC, then the live re-verification.

### Parallel Opportunities

- Phase 1: T002, T003 and T004.
- Phase 2: T006, T007 and T008 (separate scripts). T005 runs alongside them.
- US1: the enumeration tasks T016–T022 (separate procedures and draft files), and the live runs T025–T030 (separate evidence folders and outcome files).
- US2: the reviews T036–T044 (separate `review-*` folders).
- US3: T051, T052 and T053 (separate branches and files).
- US4: T060 and T064.
- US1 and US2 code reviews run at the same time as the Phase 2 approval waits.

---

## Parallel Example: User Story 1

```bash
# One Workflow, haiku, each agent writing only its own files:
Task: "T016 Enumerate dashboard capabilities → audit/procedures/web.md + audit/evidence/rc.0/inventory-WEB.md"
Task: "T017 Enumerate API capabilities → audit/procedures/api.md + audit/evidence/rc.0/inventory-API.md"
Task: "T018 Enumerate CRD lifecycles → audit/procedures/crd.md + audit/evidence/rc.0/inventory-CRD.md"
Task: "T019 Enumerate agent capabilities → audit/procedures/agent.md + audit/evidence/rc.0/inventory-AGT.md"
Task: "T020 Enumerate auxiliary components → audit/procedures/auxiliary-services.md + audit/evidence/rc.0/inventory-AUX.md"
Task: "T021 Enumerate install options → audit/procedures/helm.md + audit/evidence/rc.0/inventory-HELM.md"
Task: "T022 Enumerate module categories → audit/procedures/modules.md + audit/evidence/rc.0/inventory-MOD.md"
```

## Parallel Example: User Story 2

```bash
# One Workflow, sonnet reviewers, then an opus verifier (T045):
Task: "T036 Review operator/ → audit/evidence/review-operator/notes.md"
Task: "T037 Review api/ → audit/evidence/review-api/notes.md"
Task: "T038 Review agent/ → audit/evidence/review-agent/notes.md"
Task: "T039 Review web/ → audit/evidence/review-web/notes.md"
Task: "T040 Review netguard/gameaction/gameproto"
Task: "T041 Review auxiliary Go modules"
Task: "T042 Review chart/deploy/hack/workflows"
Task: "T043 Review test/e2e/"
Task: "T044 Review docs + submodule consistency"
```

---

## Implementation Strategy

### MVP First (User Story 1)

1. Phase 1 plus T005–T011. None of this needs the cluster.
2. T012–T015, which need kubelab connectivity and the rc.1 approval.
3. US1 enumeration and the live run on rc.1.
4. **Stop and validate**: every inventory row has an outcome and evidence. At this point there is a trustworthy statement of what works today.

### Incremental Delivery

1. While kubelab is unreachable or approvals are pending, run the US1 enumeration and the US2 code reviews. Neither needs the cluster.
2. rc.1 live: the US1 run, plus the US2 security attacks.
3. US3 fix rounds, rc.2 and later, until there are zero blocking findings.
4. US4 on the final candidate.
5. US5 report, then the go decision, then the Phase 8 release.

### Commit Cadence

Commit the `audit/` records on branch `018-v0-3-release-readiness` after each completed task group, signed (`git commit -s`), and keep them in a draft PR. Product fixes never land on this branch. They go to their own `fix/018-*` branches off `master`.

---

## Notes

- [P] = different files, no dependency on an incomplete task.
- ⛔ approval = the maintainer must approve: RC or final tag publication (FR-019), the go/no-go decision, the kubelab end state, and any test or design-spec change (CLAUDE.md override 1).
- Findings F-(a) to F-(e) mean the drift findings seeded in T011. Their real `F-NNN` IDs are assigned there.
- Never delete or weaken a test to close a finding. Fix the code.
