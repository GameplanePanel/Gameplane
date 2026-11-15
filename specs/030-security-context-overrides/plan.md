# Implementation Plan: Module security defaults with a per-server override

**Branch**: `spec-030/security-context-overrides` (planning on `spec-030/plan-and-tasks`) | **Date**: 2026-11-13, revised 2026-11-15 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/030-security-context-overrides/spec.md`

**Binding rules for this revision**: [briefs/00-rulings.md](briefs/00-rulings.md) is read first. It holds the settled OD-7, OD-8 and OD-9 rulings and supersedes the earlier briefs where they differ.

## Summary

A game template already declares the uid, gid and filesystem group its game container runs with. This feature keeps those values as the module's defaults, adds a fourth module-level setting (the volume ownership check, defaulting to the faster "only when wrong"), and lets a server owner who holds a new `servers:security` permission override each of the four per server, from an "Advanced" section of the create wizard and the server Settings tab.

**Where the override lives.** A new optional `spec.security` block on `GameServer` (research R1). Each id setting is `{ value }` or `{ useImageDefault: true }`, absent meaning "module default" (R2). The template's `security` block gains `volumeOwnershipCheck` and a `risky` map (a presence flag plus an optional reason per setting) that only the dashboard reads (R3). Both CRD changes follow `make generate && make manifests`.

**Who computes what.** One pure resolver in the operator turns the template and the override into the desired settings and the source of each value (R4). The operator renders them into the game container, the pod `fsGroup` and `fsGroupChangePolicy`, and the config-init container, and publishes `status.security` with `desired`, `applied` and `pending` (R7). The API only stores, validates and audits (FR-004).

**The operator also enforces the setting (OD-4, OD-7).** The operator reads its own cluster's setting from a ConfigMap in its control-plane namespace (R9). While the setting is off, a stopped server whose saved override is a root value is not scaled up. The check runs before the server takes a wake slot, so a held sleeping server does not use one (OD-9 item 4). The server reports condition `SecurityAllowed=False`, reason `RootOverrideDisallowed`, naming the settings. Running servers are never touched. Flipping the setting enqueues every server that holds a root override, and the server starts again once the override is cleared or the setting is back on (R9, FR-005a).

**Applying on the next restart, without restarting.** The StatefulSet rolls on any pod-template difference, so the security-derived parts of the template are refreshed only on first create or when the workload goes from zero replicas to one or more, reusing the pattern the hostport affinity already uses (R5). The same rule keeps the ownership-check default flip from restarting existing servers when the operator upgrades (R8). A template rollout on a suspended or sleeping server refreshes security at its next start, not at the rollout (OD-9 item 5). The applied filesystem group (read from the pod or the StatefulSet) is what backup and restore jobs use (R6, FR-011). `spec.security` is excluded from the template baseline hash, so saving an override never rewrites a running pod (OD-9 item 6, task T075).

**API.** No new route for overrides. `servers:security` is a field-level check inside `validateAndProtectGameServer` next to the `captures:manage` precedent. The root rule reads the setting from the ConfigMap of the request's own cluster, only for a request that carries a new or changed root value, and is off unless that ConfigMap says exactly `"true"` (R9, R10). The cluster setting has its own routes, `GET` and `PUT /security-settings?cluster=<id>`, guarded by `config:read` and `config:manage` (OD-7). The API never creates the ConfigMap: Helm creates it with `"false"` and keeps its value on upgrade, and the API only reads and updates it by name (OD-8, R9). Every override change, refusal and setting flip writes one audit row whose `reason` carries before and after (and the risky marker), with no migration (R11, R12). The clone path writes the same row (OD-9 item 3).

**Dashboard.** One shared collapsed "Advanced" section for the wizard and the Settings tab, read-only without the permission, with the pending marker, the restart notice, the file-access warning and the risky-value confirmation (R13). The cluster's root switch is a "Security" panel under Clusters, cluster, Configuration, next to the backup destinations (OD-7; `SecuritySettingsPanel.tsx`). **All dashboard work is blocked until valgul designs the Advanced section and the Security panel in `design.pen` on the maintainer's machine through the Pencil MCP and exports them to `assets/design-export/`** (constitution II, CLAUDE.md rules 1 and 2; a cloud session cannot run Pencil). Backend, operator, API, module, docs and E2E work does not wait for it.

**Modules.** The template schema, the module repo's `validate.py`, the `gp-module` validator and preview, `docs/module-authoring.md` and the Palworld template change. The module repo changes ship as a **separate PR to `GameplanePanel/module`**, and the main repo takes them only through a **submodule pointer bump after that PR merges** (`submodule-bump` skill, CLAUDE.md "Game module" workflow). The main-repo changes work without them.

**Settled rulings (OD-1 to OD-9).** [OD-1](OPEN-DECISIONS.md) B: a new `servers:security` permission, admin only by default. [OD-2](OPEN-DECISIONS.md) C: root values need the cluster's setting on. [OD-3](OPEN-DECISIONS.md) A: changes apply on the next restart. [OD-4](OPEN-DECISIONS.md) A: when the setting is off, the reconciler refuses to start a server that holds a saved root override (status says why, `RootOverrideDisallowed`), and never restarts or stops a running one. [OD-5](OPEN-DECISIONS.md) A: the reconciler validates the effective values with the restricted-workload rule and fails the server with the existing message until fixed; a running pod is untouched (R4). [OD-6](OPEN-DECISIONS.md) C: the dashboard asks for the risky confirmation, the audit row records that the value was risky, and the API refuses nothing on that basis (R11, R13). [OD-7](OPEN-DECISIONS.md) C (2026-11-15): the root setting is separate per cluster, set under Clusters, cluster, Configuration, stored in that cluster's ConfigMap. [OD-8](OPEN-DECISIONS.md) A (2026-11-15): Helm creates the ConfigMap with `"false"`, keeps it on uninstall and keeps its value on upgrade; the API only reads and updates it by name. [OD-9](OPEN-DECISIONS.md) A (2026-11-15): the seven spec corrections are accepted (see briefs/00 section 6).

## Technical Context

**Language/Version**: Go 1.27 modules per `go.work` (`operator`, `api`, `gp-module`, `mcp-server`, `test/e2e`); TypeScript 7 strict and React 19 for `web`; Python 3 for the module repo's `validate.py`; Kubernetes CRDs `gameplane.local/v1alpha1`; Helm templates for the chart.

**Primary Dependencies**: existing ones only. Operator: controller-runtime, `k8s.io/api` (`PodSecurityContext.FSGroupChangePolicy` is a core field). API: chi, client-go (typed client for the ConfigMap), dynamic client, `database/sql` over SQLite or PostgreSQL. Web: React 19, TanStack Router and Query, HeroUI v3, Tailwind. No new module and no new dependency.

**Storage**: CRD fields (`GameServer.spec.security`, `GameServer.status.security`, `GameServer` condition `SecurityAllowed`, `GameTemplate.spec.security` additions); one ConfigMap per cluster, `gameplane-security-settings`, created by the chart (R9, OD-8); the existing `audit_events.reason` column. **No database migration** (R12); the next free portable number, `020`, is not used, and the `config` table is not used for this setting.

**Testing** (CI only, constitution VI; local work is compile checks). New test files only; no existing test file is edited (OD-9 item 2, briefs/00 section 8):
- `go test` with coverage gates: `operator` 72, `api` 80, `gp-module` 80, `mcp-server` 70.
- envtest: operator reconcile suite (`operator/internal/controller/security_overrides_envtest_test.go`) and API handler suites (`api/internal/handlers/security_overrides_envtest_test.go`, `security_settings_envtest_test.go`).
- vitest for the dashboard (`web` gates 92/76/82/92).
- The module repo's `test-validate-py-security.sh` for the new validator rules.
- Kind E2E: `TestGameServer_SecurityOverrideAppliesOnRestart` and `TestAPI_SecurityOverride_PermissionAndRootRule`, registered in `test/e2e/buckets.sh` (R16).
- `make lint` drift and doc checks: CRD sync, `i18n-check`, `check-specs`, `check-doc-versions`, `check-links`.

**Target Platform**: Kubernetes (k3s, kind, managed), linux/amd64 and arm64, single and multi-cluster; behavior identical on both.

**Project Type**: Multi-component: operator CRDs and reconciler, API gateway, React dashboard, a module-authoring CLI, Helm chart, and the separate module repo.

**Performance Goals**: SC-006: a server with 100,000 or more files and a filesystem group starts at least as fast as today, and with the default `OnRootMismatch` policy an unchanged restart adds no measurable ownership pass. The API reads the cluster's ConfigMap only for a request that carries a new or changed root value, and nothing for other requests.

**Constraints**:
- A server with no override runs with the same user, group and filesystem group as today (SC-001); an operator upgrade restarts no server (R5, R8).
- Saving an override never restarts the server (FR-008, OD-3). Turning the root setting off never restarts or stops a server (FR-005a, OD-4).
- Helper containers keep their fixed users (FR-012).
- The reconciler is the only place that computes desired and applied (FR-004); the API never bypasses it (CLAUDE.md rule 10).
- No suppression directives, `%w` error wrapping, strict TypeScript (constitution III).
- Dashboard text only through the shared language packs (FR-014).
- Pencil files are touched only through the Pencil MCP, by the maintainer.
- Existing function names and positional signatures that tests call stay as they are (briefs/00 section 11).

**Scale/Scope**:
- 2 CRDs changed (additive), 4 generated locations.
- 1 new permission, 2 new routes (`GET` and `PUT /security-settings`), 1 ConfigMap per cluster created by Helm, 1 new operator watch, chart RBAC for the API on the home cluster, standalone and each remote cluster (R9).
- 4 new messages: 3 `server.security.*` and `cluster.security_settings_missing`.
- 1 shared dashboard section used on 2 screens, 1 cluster Security panel, about 4 new design frames.
- About 20 module templates carry a `security` block; Palworld is the one known risky value.
- 2 new E2E tests (the first also covers the OD-4 refusal).

No NEEDS CLARIFICATION remains in the spec. The one open point in the design is the control-plane namespace the API uses for a remote cluster (briefs/00 section 7), which is stated as an open item, not guessed. Every other technical unknown is resolved in [research.md](research.md) (R1 to R16). OD-1 to OD-9 are all settled (valgul, 2026-11-07, 2026-11-13 and 2026-11-15); the plan builds on the rulings.

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-checked after Phase 1 design.*

| Principle | Gate | Pre-design | Post-design |
|---|---|---|---|
| **I. E2E-tested delivery (non-negotiable)** | A Kind E2E test exercises the real path, is registered in `test/e2e/buckets.sh`, uses `t.Parallel()`, unique names and the shared-state guards. | Planned: the spec's Assumptions name a Kind E2E that creates a server with an override and checks the running uid. | **PASS**: `TestGameServer_SecurityOverrideAppliesOnRestart` (running uid, no auto-restart, applied on restart, removal returns the module default, and the OD-4 refusal of a stopped root-override server when the setting ConfigMap says off) and `TestAPI_SecurityOverride_PermissionAndRootRule` (permission refusal, root refusal and allow after the setting's `PUT /security-settings`, audit rows, the ConfigMap value). Login budget: the first test does zero logins; the second goes in the `operator` bucket (one admin login plus one login as its own user, about six against a ceiling near seven; see R16 and tasks.md). No game-module join applies: no module's game behavior changes. |
| **II. Design-first for user-facing change** | Dashboard changes are designed in `design.pen` via the Pencil MCP and exported before React code. | Required: wizard Advanced section, Settings Advanced section, the cluster Security panel. | **PASS, with a hard gate**: the design is made by valgul locally; no web task starts until `assets/design-export/{json,screenshots}/` for the new frames is in the branch. A cloud session writes no `.pen` file and never opens one (CLAUDE.md rule 2). Backend and docs tasks are exempt from the gate and proceed. |
| **III. Language and ecosystem best practice** | No suppressions; `%w`; strict TS; CRD edits followed by `make generate && make manifests` with all artifacts in the same commit. | Required. | **PASS**: the CRD tasks list the generated deepcopy, `operator/config/crd/*.yaml`, `charts/gameplane/charts/gameplane-crds/crds/*.yaml` and `charts/gameplane/crd-manifests/*.yaml` in the same commit. No exclusion or loosened config is planned. New operator code stays under `operator/internal/controller/` (exempt from revive `exported:` only). |
| **IV. Spec-driven; per-module `specs.md`** | Behavior changes update the matching `specs.md` in the same change. | Required. | **PASS** (planned): `operator/specs.md`, `api/specs.md`, `web/specs.md`, `gp-module/specs.md`, `mcp-server/specs.md`, `test/e2e/specs.md`, `modules/palworld/specs.md` (module repo), plus `docs/module-authoring.md` (FR-013) and `docs/security.md` (new "Server security overrides" subsection under "Pod security"). OD-1 to OD-9 are settled in `OPEN-DECISIONS.md` and the specs follow the rulings. The folder becomes `done_030-...` only after merge and all tasks are complete or withdrawn. |
| **V. Delegate to Workflows and subagents** | The main loop delegates through `Workflow`, `model:` set on every `agent()`, haiku first, review one tier up, scout once. | Required. | **PASS** (process): the tasks phase produces scout briefs with exact `file:line` per slice; implementation waves are split by component (see Delivery slices); the CRD wave and the mechanical wave (i18n keys, docs) are scripted; reviews are one per batch against the brief. |
| **VI. CI bears the heavy lifting** | Locally only `go build` and `tsc --noEmit`; nothing is reported validated before CI is green. | Required. | **PASS**: [quickstart.md](quickstart.md) lists only compile checks locally; all tests, lint, envtest and E2E run on CI, and the PR is not called ready until they pass. |
| CLAUDE.md core rule 3 (login privacy) | Unauthenticated views reveal nothing. | n/a | **PASS**: all new UI is behind sign-in; refusal messages name no cluster data beyond the cluster id the caller chose. |
| CLAUDE.md core rules 9 and 10 | Kubernetes primitives first; the operator owns business logic. | Required. | **PASS**: the override is a field on an existing CRD; the ownership check is the kubelet's `fsGroupChangePolicy`; the cluster setting is a ConfigMap, a Kubernetes primitive that Helm creates and the operator watches; the operator computes and applies, and the API only validates, stores and audits. |
| CLAUDE.md core rules 9 and 10, re-checked for OD-4 and OD-7 | The operator owns business logic; Kubernetes primitives first. | The operator must learn the cluster setting. | **PASS**: the setting travels in a ConfigMap that the operator reads and the API reads and updates by name; the refusal to start is reconciler logic, and the API still never bypasses reconciliation. |
| CLAUDE.md system override 10 (unsettled values) | Unsettled values go in `OPEN-DECISIONS.md`. | Required. | **PASS**: OD-1 to OD-9 are settled. The one unresolved item (the control-plane namespace of a remote cluster, briefs/00 section 7) is recorded as an open item and blocks only the remote-cluster tasks. |

**Result**: no violations, and no principle is bent. The costs worth stating are a hard dependency, the design gate under principle II, which is a sequencing constraint, and the open remote-namespace item.

### Spec gaps found while planning

1. **No label for the ownership check's platform default.** FR-009 lists four sources (module default, override, "use image default" override, image default). The ownership check, when neither the module nor the server sets it, is neither "module default" nor "image default". The plan adds a fifth source, `BuiltInDefault`, used only for that setting ([data-model.md](data-model.md)). If the maintainers prefer another label this is a one-line change.
2. **Audit "before and after" has no field to live in.** FR-010 wants before and after in the audit log; the audit schema has none. The plan puts them in the existing `reason` column (R11) rather than adding a column.
3. **US1 scenario 3 ("on its next restart") and a template rollout.** With the default `Automatic` template rollout policy a module upgrade already stops and starts the server (`gameserver_template_revision.go`), so "next restart" is that rollout, not a separate event; with `OnRequest` it is the admitted rollout. A rollout on a suspended or sleeping server ends at zero replicas, so its refresh waits for the next start (OD-9 item 5). Noted so tests expect it.

## Project Structure

### Documentation (this feature)

```text
specs/030-security-context-overrides/
├── spec.md
├── OPEN-DECISIONS.md          # OD-1..OD-9 settled
├── plan.md                    # this file
├── research.md                # R1 to R16
├── data-model.md
├── quickstart.md
├── briefs/
│   ├── 00-rulings.md                  # binding rulings for OD-7..OD-9; read first
│   └── 01..04-*.md                    # scout briefs (facts); design statements superseded by 00
├── contracts/
│   ├── crd-gameserver-security.md     # spec.security + status.security, reconciler behavior
│   ├── crd-gametemplate-security.md   # ownership-check default, risky flag and reason, validators
│   ├── api-server-security.md         # create/update/clone semantics, error codes
│   ├── rbac-permission.md             # servers:security
│   ├── cluster-setting.md             # per-cluster ConfigMap, /security-settings routes, Helm creation
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
├── cmd/main.go                        ControlPlaneNamespace set on GameServerReconciler from controlPlaneNamespace (:517-540)
├── internal/controller/
│   ├── security_overrides.go          NEW: resolver (R4), pod encoding, applied reader, annotation codec, fsGroupChangePolicy mapping
│   ├── security_settings.go           NEW: ConfigMap reader (informer cache, fail closed), hasRootOverride, rootOverrideSettings, map func that re-enqueues servers holding a root override
│   ├── gameserver_controller.go       gameContainerSecurityContext :2496, gamePodSecurityContext :2612, buildConfigInitContainer :2336 (wrappers kept, *Resolved variants added);
│   │                                  reconcileStatefulSet gate around :1797-1935 (R5); validateRestrictedTemplate call :353; ControlPlaneNamespace field (:45-60); Watches near :609-611
│   ├── gameserver_idle.go             root hold checked before reserveWakeStart (:333) (OD-9 item 4)
│   ├── gameserver_template_revision.go  validateRestrictedTemplate calls :222, :288 take effective values; baselineRenderInputs :338-340 excludes spec.security (T075)
│   ├── gameserver_status.go           publish status.security (merge patch); SecurityAllowed condition
│   ├── workload_security.go           validateRestrictedTemplate :15, restrictedContainerSecurity :23, applyWorkloadSecurity :39 take effective values
│   ├── backup_controller.go           buildBackupPodSecurityContext :1156 (caller :1058) takes the applied fsGroup as a trailing variadic
│   ├── backup_worker.go               applied fsGroup threaded from bound GameServer (:306, :129-130)
│   ├── restore_controller.go          buildRestorePodSpec :425 (call :277, security :433) takes the applied fsGroup
│   ├── security_overrides_test.go            NEW unit tests (resolver, codec, gate, restricted mode, backup group)
│   ├── security_overrides_envtest_test.go    NEW envtest: override applies on restart, pending, removal, upgrade no-restart, root-override refusal (setting off), running server untouched, start after clear or after the setting is back on
│   ├── security_settings_test.go             NEW unit tests: hasRootOverride, rootOverrideSettings, ConfigMap reader
│   ├── security_builders_test.go             NEW unit tests: byte-identical unset pod, helper containers keep their users
│   ├── security_crd_validation_envtest_test.go  NEW CEL cases for SecurityIDOverride and the enums
│   └── security_backup_envtest_test.go       NEW backup and restore applied-group cases
└── specs.md

charts/gameplane/
├── templates/api-credentials.yaml     Role rule in the credentials namespace: configmaps `gameplane-security-settings` get/update/patch by resourceNames (no create) (R9, OD-8)
├── templates/api.yaml                 standalone equivalent Role and RoleBinding in the release namespace, inside the apiEnabled guard
├── templates/operator*.yaml           the ConfigMap `gameplane-security-settings` created by Helm with "false", keep annotation, lookup on upgrade (OD-8)
├── templates/                         remote-chart grant for the identity the API uses for a remote cluster (T076; identity to be found in the chart)
├── charts/gameplane-crds/crds/*.yaml  GENERATED (gameservers, gametemplates)
└── crd-manifests/*.yaml               GENERATED (gameservers, gametemplates)

api/
├── cmd/main.go                        MountResources receives the auditor; MountSecuritySettings mounted; MountLifecycle receives the auditor (variadic)
├── internal/rbac/catalog.go           servers:security (:25-29)
├── internal/rbac/rbac.go              rule entries for GET and PUT /security-settings next to the /admin/config rules (:209, :214)
├── internal/rbac/catalog_security_test.go   NEW: key exists, namespaced, held by admin only among the built-in roles
├── internal/handlers/
│   ├── security_overrides.go          NEW: shape validation, root rule (reads the request cluster's ConfigMap), restore-live, reason builder, refusal typing
│   ├── security_settings.go           NEW: GET and PUT /security-settings, ConfigMap get and update by name, flip audit row, MountSecuritySettings
│   ├── resources.go                   MountResources :51; validateAndProtectGameServer :570 new security step; create :250 and update :322 audit write
│   ├── lifecycle.go                   clone path :243 (validator call with live nil; audit row); MountLifecycle :40 takes the auditor variadic
│   ├── security_overrides_test.go            NEW unit tests
│   ├── security_overrides_envtest_test.go    NEW envtest (dedicated router)
│   ├── security_settings_test.go             NEW unit tests (validator, GET and PUT shape)
│   └── security_settings_envtest_test.go     NEW envtest (dedicated router, flip, 409, 502, rows)
├── internal/messages/                 server.security.forbidden, .root_disabled, .invalid; cluster.security_settings_missing
└── specs.md

i18n/locales/en/app.json, fr/app.json  server.security.* and cluster.security_settings_missing, web.routes.* and web.clusters.* keys

web/src/                                       BLOCKED until the design export lands
├── types.ts                                   GameServer.spec.security, GameServer.status.security (:557), GameTemplate.spec.security (:303)
├── lib/api.ts                                 server create/update already pass spec; add GET and PUT /security-settings client methods
├── components/SecurityAdvanced.tsx            NEW shared section + risky confirmation (and .test.tsx)
├── components/clusters/SecuritySettingsPanel.tsx   NEW cluster Security panel (switch, missing state) (and .test.tsx)
├── components/clusters/ClusterConfiguration.tsx    mounts SecuritySettingsPanel next to BackupDestinationsPanel.tsx
├── routes/CreateServer.tsx                    collapsed Advanced section in the wizard
├── routes/tabs/Settings.tsx, routes/tabs/settings/Security.tsx   NEW settings subsection for an existing server
└── specs.md

gp-module/
├── internal/validator/rules.go                risky and ownership-check rules (called from validator.go next to :169-170)
├── internal/preview/                          print resolved defaults and risky settings
├── internal/scaffold/scaffold.go              :167, :213 (README text) unchanged unless an archetype asks for risky
└── specs.md

mcp-server/specs.md, README.md, main_test.go   document and test the read-only exposure (no code change, R15)

test/e2e/
├── gameserver_security_e2e_test.go            NEW: TestGameServer_SecurityOverrideAppliesOnRestart
├── api_security_override_e2e_test.go          NEW: TestAPI_SecurityOverride_PermissionAndRootRule
├── buckets.sh                                 register both tests in `bucket_operator` (R16)
├── suite-components.json                      no entry needed: `^TestGameServer_` and `^TestAPI_` already map both tests
└── specs.md

docs/
├── module-authoring.md                        "Security context" :1157-1194 (FR-013)
└── security.md                                server security overrides under "Pod security" (:435, insert before "## Module supply chain" :564)

hack/gen-module-schema.py                      run via `make module-schema`, output in the module repo
assets/design-export/                           maintainer-produced export of the new frames (design gate)
design.pen                                      maintainer only, through the Pencil MCP; never touched here
/mnt/project-files/notes/design-prompts/030-security-overrides.md   design prompt for the maintainer's local agent

# Separate repository: GameplanePanel/module (checked out at modules/, local clone /home/user/module)
.schema/gametemplate.schema.json               regenerated from the CRD (make module-schema)
validate.py, test-validate-py-security.sh      new risky and ownership-check rules and tests
palworld/template.yaml, palworld/specs.md      risky.runAsUser with a reason
# Main repo afterwards: `git add modules` pointer bump (submodule-bump skill), only after the module PR merges
```

**Structure Decision**: no new module or service. The work follows the existing component boundaries (operator computes and applies; API validates, stores and audits; web renders; the chart creates the setting's ConfigMap; modules declare defaults). The new files are two operator source files, two API source files, two dashboard components and two E2E tests. Target folder shapes were checked against `docs/layout.md`.

### Delivery slices

Independent slices, fanned out per constitution V; each slice ends in compile checks locally and CI for everything else.

| Slice | Content | Depends on |
|---|---|---|
| A. CRD | Go types, `make generate && make manifests`, CEL tests | none |
| B. Operator | resolver, encoding, gate, status, backup/restore group, restricted mode (OD-5), setting reader and root-override start refusal (OD-4), wake hold (OD-9 item 4), baseline exclusion (T075) | A |
| C. API | permission, validator, the `/security-settings` routes and rules, the root rule, audit (incl. risky marker, OD-6, and the flip row), messages | A (types only for tests) |
| D. Chart | the Helm-created ConfigMap with lookup (T077), the API Role rules (T058), the remote grant (T076) | none |
| E. Module repo | schema regeneration, `validate.py`, Palworld, then a separate PR | A |
| F. gp-module and docs | validator rules, preview, `module-authoring.md`, `security.md`, `specs.md` files | A, E |
| G. E2E | the two tests and bucket registration (both in `operator`) | B, C, D |
| H. Dashboard | shared section, wizard, Settings, cluster Security panel, i18n, vitest | C, **design export** |
| I. Pointer bump | `git add modules` after the module PR merges | E merged |

## Complexity Tracking

No Constitution Check violations; nothing to justify. The design gate on slice H (principle II), the separate module-repo PR with a later pointer bump, and the open remote-namespace item are sequencing constraints or open questions, recorded above, not bent principles.
