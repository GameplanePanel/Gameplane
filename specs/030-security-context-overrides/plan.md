# Implementation Plan: Module security defaults with a per-server override

**Branch**: `spec-030/security-context-overrides` (planning on `spec-030/plan-and-tasks`) | **Date**: 2026-11-13 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/030-security-context-overrides/spec.md`

## Summary

A game template already declares the uid, gid and filesystem group its game container runs with. This feature keeps those values as the module's defaults, adds a fourth module-level setting (the volume ownership check, defaulting to the faster "only when wrong"), and lets a server owner who holds a new `servers:security` permission override each of the four per server, from an "Advanced" section of the create wizard and the server Settings tab.

**Where the override lives.** A new optional `spec.security` block on `GameServer` (research R1). Each id setting is `{ value }` or `{ useImageDefault: true }`, absent meaning "module default" (R2). The template's `security` block gains `volumeOwnershipCheck` and a `risky` map (a presence flag plus an optional reason per setting) that only the dashboard reads (R3). Both CRD changes follow `make generate && make manifests`.

**Who computes what.** One pure resolver in the operator turns the template and the override into the desired settings and the source of each value (R4). The operator renders them into the game container, the pod `fsGroup` and `fsGroupChangePolicy`, and the config-init container, and publishes `status.security` with `desired`, `applied` and `pending` (R7). The API only stores, validates and audits (FR-004).

**Applying on the next restart, without restarting.** The StatefulSet rolls on any pod-template difference, so the security-derived parts of the template are refreshed only on first create or when the workload goes from zero replicas to one or more, reusing the pattern the hostport affinity already uses (R5). The same rule keeps the ownership-check default flip from restarting existing servers when the operator upgrades (R8). The applied filesystem group (read from the pod or the StatefulSet) is what backup and restore jobs use (R6, FR-011).

**API.** No new route. `servers:security` is a field-level check inside `validateAndProtectGameServer` next to the `captures:manage` precedent, with the root rule driven by a new `security` config section, `allowRootOverrides`, off by default and changed through `PUT /admin/config/security` (R9, R10). Every change, refusal and setting flip writes one audit row whose `reason` carries before and after, with no migration (R11, R12).

**Dashboard.** One shared collapsed "Advanced" section for the wizard and the Settings tab, read-only without the permission, with the pending marker, the restart notice, the file-access warning and the risky-value confirmation (R13). **All dashboard work is blocked until valgul designs the Advanced section in `design.pen` on the maintainer's machine through the Pencil MCP and exports it to `assets/design-export/`** (constitution II, CLAUDE.md rules 1 and 2; a cloud session cannot run Pencil). Backend, operator, API, module, docs and E2E work does not wait for it.

**Modules.** The template schema, the module repo's `validate.py`, the `gp-module` validator and preview, `docs/module-authoring.md` and the Palworld template change. The module repo changes ship as a **separate PR to `GameplanePanel/module`**, and the main repo takes them only through a **submodule pointer bump after that PR merges** (`submodule-bump` skill, CLAUDE.md "Game module" workflow). The main-repo changes work without them.

Three product questions are open and isolated; the plan builds on each recommended option where it can: [OD-4](OPEN-DECISIONS.md) (existing root overrides when the setting is turned off; its answer decides whether the operator must read the setting), [OD-5](OPEN-DECISIONS.md) (restricted workload mode), [OD-6](OPEN-DECISIONS.md) (is the risky confirmation enforced by the API).

## Technical Context

**Language/Version**: Go 1.27 modules per `go.work` (`operator`, `api`, `gp-module`, `mcp-server`, `test/e2e`); TypeScript 7 strict and React 19 for `web`; Python 3 for the module repo's `validate.py`; Kubernetes CRDs `gameplane.local/v1alpha1`.

**Primary Dependencies**: existing ones only. Operator: controller-runtime, `k8s.io/api` (`PodSecurityContext.FSGroupChangePolicy` is a core field). API: chi, client-go dynamic client, `database/sql` over SQLite or PostgreSQL. Web: React 19, TanStack Router and Query, HeroUI v3, Tailwind. No new module and no new dependency.

**Storage**: CRD fields (`GameServer.spec.security`, `GameServer.status.security`, `GameTemplate.spec.security` additions); one new key in the existing API `config` table (`security`); the existing `audit_events.reason` column. **No database migration** (R12); the next free portable number, `020`, is not used.

**Testing** (CI only, constitution VI; local work is compile checks):
- `go test` with coverage gates: `operator` 72, `api` 80, `gp-module` 80, `mcp-server` 70.
- envtest: operator reconcile suite (`gameserver_security_envtest_test.go`) and API handler suite (`resources_security_envtest_test.go`, config section).
- vitest for the dashboard (`web` gates 92/76/82/92).
- The module repo's `test-validate-py.sh` for the new validator rules.
- Kind E2E: `TestGameServer_SecurityOverrideAppliesOnRestart` and `TestAPI_SecurityOverride_PermissionAndRootRule`, registered in `test/e2e/buckets.sh` (R16).
- `make lint` drift and doc checks: CRD sync, `i18n-check`, `check-specs`, `check-doc-versions`, `check-links`.

**Target Platform**: Kubernetes (k3s, kind, managed), linux/amd64 and arm64, single and multi-cluster; behavior identical on both.

**Project Type**: Multi-component: operator CRDs and reconciler, API gateway, React dashboard, a module-authoring CLI, and the separate module repo.

**Performance Goals**: SC-006: a server with 100,000 or more files and a filesystem group starts at least as fast as today, and with the default `OnRootMismatch` policy an unchanged restart adds no measurable ownership pass. The API adds one indexed `config` read to a server create or update that carries a root value, and nothing to other requests.

**Constraints**:
- A server with no override runs with the same user, group and filesystem group as today (SC-001); an operator upgrade restarts no server (R5, R8).
- Saving an override never restarts the server (FR-008, OD-3).
- Helper containers keep their fixed users (FR-012).
- The reconciler is the only place that computes desired and applied (FR-004); the API never bypasses it (CLAUDE.md rule 10).
- No suppression directives, `%w` error wrapping, strict TypeScript (constitution III).
- Dashboard text only through the shared language packs (FR-014).
- Pencil files are touched only through the Pencil MCP, by the maintainer.

**Scale/Scope**:
- 2 CRDs changed (additive), 4 generated locations.
- 1 new permission, 1 new config section, 3 new API messages, 0 new routes.
- 1 shared dashboard section used on 2 screens, 1 admin switch, about 3 new design frames.
- About 20 module templates carry a `security` block; Palworld is the one known risky value.
- 2 new E2E tests.

No NEEDS CLARIFICATION remains. Every technical unknown is resolved in [research.md](research.md) (R1 to R16). OD-1 to OD-3 are settled; OD-4 to OD-6 are open product questions with a recommended option the plan uses.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Gate | Pre-design | Post-design |
|---|---|---|---|
| **I. E2E-tested delivery (non-negotiable)** | A Kind E2E test exercises the real path, is registered in `test/e2e/buckets.sh`, uses `t.Parallel()`, unique names and the shared-state guards. | Planned: the spec's Assumptions name a Kind E2E that creates a server with an override and checks the running uid. | **PASS**: `TestGameServer_SecurityOverrideAppliesOnRestart` (running uid, no auto-restart, applied on restart, removal returns the module default) and `TestAPI_SecurityOverride_PermissionAndRootRule` (permission refusal, root refusal and allow, audit rows). Login budget: the first test does zero logins; the second's bucket is decided when tasks are written against the roughly seven admin-login ceiling (R16). No game-module join applies: no module's game behavior changes. |
| **II. Design-first for user-facing change** | Dashboard changes are designed in `design.pen` via the Pencil MCP and exported before React code. | Required: wizard Advanced section, Settings Advanced section, admin switch. | **PASS, with a hard gate**: the design is made by valgul locally; no web task starts until `assets/design-export/{json,screenshots}/` for the new frames is in the branch. A cloud session writes no `.pen` file and never opens one (CLAUDE.md rule 2). Backend and docs tasks are exempt from the gate and proceed. |
| **III. Language and ecosystem best practice** | No suppressions; `%w`; strict TS; CRD edits followed by `make generate && make manifests` with all artifacts in the same commit. | Required. | **PASS**: the CRD tasks list the generated deepcopy, `operator/config/crd/*.yaml`, `charts/gameplane/charts/gameplane-crds/crds/*.yaml` and `charts/gameplane/crd-manifests/*.yaml` in the same commit. No exclusion or loosened config is planned. New operator code stays under `operator/internal/controller/` (exempt from revive `exported:` only). |
| **IV. Spec-driven; per-module `specs.md`** | Behavior changes update the matching `specs.md` in the same change. | Required. | **PASS** (planned): `operator/specs.md`, `api/specs.md`, `web/specs.md`, `gp-module/specs.md`, `mcp-server/specs.md`, `test/e2e/specs.md`, `modules/palworld/specs.md` (module repo), plus `docs/module-authoring.md` (FR-013) and `docs/security.md` (new "Server security overrides" subsection under "Pod security"). OD-4 to OD-6 are recorded in `OPEN-DECISIONS.md`, never committed as settled. The folder becomes `done_030-...` only after merge and all tasks are complete or withdrawn. |
| **V. Delegate to Workflows and subagents** | The main loop delegates through `Workflow`, `model:` set on every `agent()`, haiku first, review one tier up, scout once. | Required. | **PASS** (process): the tasks phase produces scout briefs with exact `file:line` per slice; implementation waves are split by component (see Delivery slices); the CRD wave and the mechanical wave (i18n keys, docs) are scripted; reviews are one per batch against the brief. |
| **VI. CI bears the heavy lifting** | Locally only `go build` and `tsc --noEmit`; nothing is reported validated before CI is green. | Required. | **PASS**: [quickstart.md](quickstart.md) lists only compile checks locally; all tests, lint, envtest and E2E run on CI, and the PR is not called ready until they pass. |
| CLAUDE.md core rule 3 (login privacy) | Unauthenticated views reveal nothing. | n/a | **PASS**: all new UI is behind sign-in; refusal messages name no cluster data. |
| CLAUDE.md core rules 9 and 10 | Kubernetes primitives first; the operator owns business logic. | Required. | **PASS**: the override is a field on an existing CRD; the ownership check is the kubelet's `fsGroupChangePolicy`; the operator computes and applies, and the API only validates and stores. |
| CLAUDE.md system override 10 (unsettled values) | Unsettled values go in `OPEN-DECISIONS.md`. | Required. | **PASS**: OD-4, OD-5 and OD-6 are open and listed; the plan names the option it builds on and where each one touches. |

**Result**: no violations, and no principle is bent. The one cost worth stating is a hard dependency, the design gate under principle II, which is a sequencing constraint, not a violation.

### Spec gaps found while planning

1. **No label for the ownership check's platform default.** FR-009 lists four sources (module default, override, "use image default" override, image default). The ownership check, when neither the module nor the server sets it, is neither "module default" nor "image default". The plan adds a fifth source, `BuiltInDefault`, used only for that setting ([data-model.md](data-model.md)). If the maintainers prefer another label this is a one-line change.
2. **Audit "before and after" has no field to live in.** FR-010 wants before and after in the audit log; the audit schema has none. The plan puts them in the existing `reason` column (R11) rather than adding a column.
3. **US1 scenario 3 ("on its next restart") and a template rollout.** With the default `Automatic` template rollout policy a module upgrade already stops and starts the server (`gameserver_template_revision.go`), so "next restart" is that rollout, not a separate event; with `OnRequest` it is the admitted rollout. No spec change needed; noted so tests expect it.

## Project Structure

### Documentation (this feature)

```text
specs/030-security-context-overrides/
├── spec.md
├── OPEN-DECISIONS.md          # OD-1..OD-3 settled; OD-4..OD-6 open
├── plan.md                    # this file
├── research.md                # R1 to R16
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── crd-gameserver-security.md     # spec.security + status.security, reconciler behavior
│   ├── crd-gametemplate-security.md   # ownership-check default, risky flag and reason, validators
│   ├── api-server-security.md         # create/update/clone semantics, error codes
│   ├── rbac-permission.md             # servers:security
│   ├── cluster-setting.md             # security.allowRootOverrides
│   └── audit-events.md                # who/when/before/after in reason
├── checklists/requirements.md
└── tasks.md                   # /speckit-tasks (not created here)
```

### Source Code (repository root)

```text
operator/
├── api/v1alpha1/
│   ├── gametemplate_types.go          GameSecuritySpec (+ volumeOwnershipCheck, risky; new SecurityRiskSpec, SecurityRiskNote)   :177, :185
│   ├── gameserver_types.go            GameServerSpec.Security (after Capture :161); GameServerStatus.Security (:558); new GameServerSecuritySpec,
│   │                                  SecurityIDOverride, GameServerSecurityStatus, SecuritySettings, SecurityIDStatus, SecurityCheckStatus
│   └── zz_generated.deepcopy.go       GENERATED
├── config/crd/gameplane.local_gameservers.yaml, gameplane.local_gametemplates.yaml   GENERATED (make manifests)
├── internal/controller/
│   ├── gameserver_security.go         NEW: resolver (R4), pod encoding, applied reader, annotation codec, fsGroupChangePolicy mapping
│   ├── gameserver_controller.go       gameContainerSecurityContext :2470, gamePodSecurityContext :2586, buildConfigInitContainer :2310,
│   │                                  reconcileStatefulSet gate around :1807-1918 (R5), validateRestrictedTemplate call :334
│   ├── workload_security.go           validateRestrictedTemplate :15, restrictedContainerSecurity :23, applyWorkloadSecurity :39 take effective values
│   ├── gameserver_template_revision.go  validateRestrictedTemplate calls :216, :278 take effective values
│   ├── gameserver_status.go           publish status.security (merge patch of that field only)
│   ├── backup_controller.go           buildBackupPodSecurityContext :1133 takes the applied fsGroup; call :1035
│   ├── restore_controller.go          call :425
│   ├── gameserver_security_test.go            NEW: resolver table, encoding, gate, annotation round trip
│   ├── gameserver_security_envtest_test.go    NEW: override applies on restart, pending, removal, upgrade no-restart, backup group
│   ├── workload_security_test.go, build_game_container_test.go, backup_envtest_test.go   extended
│   └── crd_validation_envtest_test.go         CEL cases for SecurityIDOverride and the enums
└── specs.md

charts/gameplane/
├── charts/gameplane-crds/crds/*.yaml          GENERATED (gameservers, gametemplates)
└── crd-manifests/*.yaml                       GENERATED (gameservers, gametemplates)

api/
├── cmd/main.go                                MountResources now receives the auditor (:382)
├── internal/rbac/catalog.go                   servers:security (:25-29)
├── internal/rbac/catalog_test.go, rbac_test.go   key exists, namespaced, admin-only among built-ins
├── internal/handlers/
│   ├── resources.go                           MountResources :51; validateAndProtectGameServer :538 new security step; create :218 and update :290 audit write
│   ├── resources_security.go                  NEW: shape validation, root rule, restore-live, reason builder
│   ├── lifecycle.go                           clone path :243 (no change expected; covered by the shared validator)
│   ├── config.go                              "security" validator in newValidators :462; audit row on flip (:174 pattern)
│   ├── resources_security_envtest_test.go     NEW; config_envtest_test.go extended
│   └── resources_security_test.go             existing file; extended or a sibling unit test
├── internal/messages/entries_server.go        server.security.forbidden, .root_disabled, .invalid
└── specs.md

i18n/locales/en/app.json                       server.security.* and web.routes.* keys (other locales follow docs/translating.md)

web/src/                                       BLOCKED until the design export lands
├── types.ts                                   GameServer.spec.security, GameServer.status.security (:557), GameTemplate.spec.security (:303)
├── lib/api.ts                                 server create/update already pass spec; add the security config section get/put
├── components/SecurityAdvanced.tsx            NEW shared section + risky confirmation (and .test.tsx)
├── routes/CreateServer.tsx                    collapsed Advanced section in the wizard
├── routes/tabs/Settings.tsx, routes/tabs/settings/Security.tsx   NEW settings subsection for an existing server
├── routes/AdminSettings.tsx                   "Security" section with the allow-root switch
└── specs.md

gp-module/
├── internal/validator/rules.go                risky and ownership-check rules
├── internal/preview/                          print resolved defaults and risky settings
├── internal/scaffold/scaffold.go              :167, :213 (README text) unchanged unless an archetype asks for risky
└── specs.md

mcp-server/specs.md, README.md, main_test.go   document and test the read-only exposure (no code change, R15)

test/e2e/
├── gameserver_security_e2e_test.go            NEW: TestGameServer_SecurityOverrideAppliesOnRestart
├── api_security_override_e2e_test.go          NEW: TestAPI_SecurityOverride_PermissionAndRootRule
├── buckets.sh                                 register both (operator bucket, plus the bucket chosen for the API test)
├── suite-components.json                      only if a new bucket is added
└── specs.md

docs/
├── module-authoring.md                        "Security context" :1137-1173 (FR-013)
└── security.md                                server security overrides under "Pod security" (:435)

hack/gen-module-schema.py                      run via `make module-schema`, output in the module repo
assets/design-export/                           maintainer-produced export of the new frames (design gate)
design.pen                                      maintainer only, through the Pencil MCP; never touched here

# Separate repository: GameplanePanel/module (checked out at modules/, local clone /home/user/module)
.schema/gametemplate.schema.json               regenerated from the CRD (make module-schema)
validate.py, test-validate-py.sh                new risky and ownership-check rules and tests
palworld/template.yaml, palworld/specs.md       risky.runAsUser with a reason
# Main repo afterwards: `git add modules` pointer bump (submodule-bump skill), only after the module PR merges
```

**Structure Decision**: no new module or service. The work follows the existing component boundaries (operator computes and applies; API validates, stores and audits; web renders; modules declare defaults). The only new files are one operator source file, one API source file, one shared web component and two E2E tests. Target folder shapes were checked against `docs/layout.md`.

### Delivery slices

Independent slices, fanned out per constitution V; each slice ends in compile checks locally and CI for everything else.

| Slice | Content | Depends on |
|---|---|---|
| A. CRD | Go types, `make generate && make manifests`, CEL tests | none |
| B. Operator | resolver, encoding, gate, status, backup/restore group, restricted mode | A |
| C. API | permission, validator, config section, audit, messages | A (types only for tests) |
| D. Module repo | schema regeneration, `validate.py`, Palworld, then a separate PR | A |
| E. gp-module and docs | validator rules, preview, `module-authoring.md`, `security.md`, `specs.md` files | A, D |
| F. E2E | the two tests and bucket registration | B, C |
| G. Dashboard | shared section, wizard, Settings, admin switch, i18n, vitest | C, **design export** |
| H. Pointer bump | `git add modules` after the module PR merges | D merged |

## Complexity Tracking

No Constitution Check violations; nothing to justify. The design gate on slice G (principle II) and the separate module-repo PR with a later pointer bump are sequencing constraints, recorded above, not bent principles.
