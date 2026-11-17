# Brief 00: binding rulings (read this first)

**Status**: binding for every fix agent. Written 2026-11-15, after valgul settled OD-7 (C), OD-8 (A), OD-9 (A, all seven items) and OD-10 (A) in [OPEN-DECISIONS.md](../OPEN-DECISIONS.md).

Read this file before briefs 01 to 04. Where this file and an earlier brief disagree, this file wins. Briefs 01 to 04 stay valid for their facts (exact `file:line` after the corrections in their own discrepancy lists). Their design statements listed in section 10 are superseded. Do not edit the briefs; do not edit any existing test file.

---

## 1. The root setting: per cluster, one ConfigMap, no config row (OD-7 C)

- "Allow root overrides" is a separate setting for each cluster. It is off by default.
- Its only store is the ConfigMap `gameplane-security-settings`, key `allowRootOverrides`, value `"true"` or `"false"`. The ConfigMap lives in that cluster's operator control-plane namespace, which is the operator's `--control-plane-namespace` (the chart's `gameplane.operatorClusterCredentialsNamespace` helper). On a default install with the API enabled that is the credentials namespace `<release>-credentials`. In standalone or no-API mode it is the release namespace.
- Missing ConfigMap, missing key, or any value other than exact `"true"` reads as off (fail closed).
- Removed everywhere: the config-table row, a `security` section in `newValidators`, `PUT /admin/config/security`, the API start-up sync, and `api/internal/handlers/security_settings_sync.go`.
- Set in the dashboard under Clusters, cluster, Configuration (a "Security" panel), not in Admin Settings.

## 2. Routes, validation and RBAC (API)

- Routes: `GET /security-settings?cluster=<id>` and `PUT /security-settings?cluster=<id>`. The cluster is chosen with `resolveCluster` (`api/internal/handlers/resources.go:113`), the same way `/backup-destinations` does it.
- PUT body: `{"allowRootOverrides": bool}`.
  - Empty body: 400 `server.empty_body` (OD-9 item 1; the same as every config section, `config.go:126-129`).
  - Invalid JSON, or a non-boolean value: 422.
- Permission: a new rule-table entry in `api/internal/rbac/rbac.go`, placed next to the `/admin/config` rules (`:209` is GET `config:read`, `:214` is `config:manage`). PUT needs `config:manage`. GET needs `config:read`. Copy the shape of the neighbouring rules.
- GET 200 body: `{"cluster": "<id>", "allowRootOverrides": bool, "present": bool}`.
  - ConfigMap NotFound: `allowRootOverrides` false, `present` false.
  - Any other read error: 502.
- PUT 200 body: the GET body, with `present` true.
- PUT order:
  1. Read the ConfigMap. NotFound: 409 `server.cluster.security_settings_missing`, no audit row (nothing was attempted).
  2. Value unchanged: 200, nothing written, no audit row.
  3. Value changes: write the flip audit row first (section 5), then update the ConfigMap. Never create it.
  4. Update fails: 502, and a `security_save_failed` row (section 5).
- Messages (`api/internal/messages/`, cluster entries, with en and fr keys per FR-014):
  - `server.cluster.security_settings_missing`, 409, English: "the security settings ConfigMap is missing on this cluster; run helm upgrade to recreate it".
  - `server.security.root_disabled` text now says the administrator allows root overrides "in that cluster's Configuration" (see `contracts/api-server-security.md`).
- Files: new production file `api/internal/handlers/security_settings.go` with `MountSecuritySettings(r chi.Router, reg *kube.Registry, auditor *audit.Auditor)`, mounted from `api/cmd/main.go`. Route rules and envtests use dedicated routers, never `mountedR`.
- The server root rule (case 5) lives in `api/internal/handlers/security_overrides.go` (renamed from `resources_security.go`). It reads the ConfigMap of the request's own cluster through that cluster's registry client (`resolveCluster`), and only for a request that carries a new or changed root value. Any read error or any value other than exact `"true"` is off, so the request is refused. The rule no longer reads the config store, so `MountResources` needs no `*db.Store` for it. It still takes the auditor.

## 3. Operator (OD-7; research R9, unchanged in substance)

- Informer cache, fail closed, `Watches` with a map function, condition `SecurityAllowed` with reason `RootOverrideDisallowed`: all unchanged from R9.
- New field on `GameServerReconciler`: `ControlPlaneNamespace string`, set in `operator/cmd/main.go` from the `controlPlaneNamespace` variable (brief 02 D7). Do not use `WakeStartNamespace`. An empty value means off, so the 159 existing constructions keep working.
- Production files:
  - `operator/internal/controller/security_overrides.go`: resolver, pod encoding, annotation codec, applied reader. Replaces the planned `gameserver_security.go`.
  - `operator/internal/controller/security_settings.go`: ConfigMap reader, `hasRootOverride`, `rootOverrideSettings`, map function.
- Hold position: before `reserveWakeStart` (section 6, item 4) and before the R5 refresh (brief 01 section 3.1).

## 4. Helm and chart RBAC (OD-8 A)

- The operator chart creates the ConfigMap `gameplane-security-settings` in the operator's control-plane namespace, wherever the operator chart renders (home install and remote operator installs). Data `allowRootOverrides: "false"`. Annotation `helm.sh/resource-policy: keep`, so uninstall keeps it.
- On upgrade the template reads the current object with Helm `lookup` and re-renders its current value, so an upgrade never flips it. When `lookup` finds nothing (first install, or the object was deleted), the value renders as `"false"`. Shape to verify with `helm template`: read `allowRootOverrides` from the looked-up `data` when the key exists, else `"false"`.
- Home cluster API grant: add a rule to the existing Role in `charts/gameplane/templates/api-credentials.yaml` (the credentials namespace, `:14-37`):

  ```yaml
    - apiGroups: [""]
      resources: [configmaps]
      resourceNames: [gameplane-security-settings]
      verbs: [get, update, patch]
  ```

  No `create` verb. Kubernetes does not apply `resourceNames` to `create`, so the old `create` placeholder in brief 04 T058 is dropped.
- Standalone: no Role and no ConfigMap (corrected 2026-11-16, valgul approved: a standalone panel has no local operator and mounts no service account token, so it renders neither the ConfigMap nor an API Role for it; `charts/tests/check_standalone.py` asserts it renders no Roles).
- Remote clusters: the remote chart grants the identity the API uses for that cluster the same three verbs on the same ConfigMap, in the control-plane namespace recorded on that cluster's registration (`Cluster.spec.controlPlaneNamespace`, section 12) (task T076). The identity must be found in the chart. Do not guess (section 7).
- A deleted ConfigMap reads as off until the next `helm upgrade` renders it again as `"false"`.

## 5. Audit (contracts/audit-events.md)

- Flip row: `method` PUT, `path` `/security-settings`, `target` `security/<cluster id>`, `status` 200, `reason` `allowRootOverrides:false->true` (or `true->false`).
- Written with `Auditor.WriteSync` before the ConfigMap update. If `WriteSync` fails: 500, nothing changes. If the update then fails: a second row, `reason` `security_save_failed`, and the 502 response.
- Override rows (create, update, clone, refusals) are unchanged from `contracts/audit-events.md`, written from `security_overrides.go`.
- This supersedes the `config.go:175-178` row pattern and brief 03 T056 (`target` `security`, route `/admin/config/security`).

## 6. OD-9 items (accepted 2026-11-15)

1. Empty PUT body: 400 `server.empty_body` (section 2). Invalid JSON or non-boolean: 422.
2. No existing test file is edited. New files only (section 8).
3. Clone writes the security audit row too. `MountLifecycle(r, reg, auditor ...*audit.Auditor)` takes the auditor as an optional variadic parameter, so the four existing call sites compile unchanged. `cloneHandler` writes the row like create does.
4. A held sleeping server takes no wake slot. In the idle wake path, check the root hold before `r.reserveWakeStart(ctx, gs)` (`operator/internal/controller/gameserver_idle.go:333`). A server held by the root rule (setting off, root override saved, zero replicas) is not admitted: no `reserveWakeStart`, no "cold start admitted" status. It gets `SecurityAllowed=False`. This resolves brief 02 D15.
5. T044: a template rollout on a suspended or sleeping server refreshes the security values at its next start, not at the rollout. Until then `pending` stays true. Tests must not assert a start for those servers. This replaces the "premise correction" in brief 01 section 4.3 and the T044 wording in tasks.md.
6. `baselineRenderInputs` (`operator/internal/controller/gameserver_template_revision.go:338-340`, the line range OD-9 cites; verify before editing) excludes `spec.security`, so saving an override never rewrites a running pod. New task T075 (Phase 3).
7. Spec `file:line` citations are stale. Use the citations in briefs 01 to 04. tasks.md now states this in its binding rules.

## 7. Open items: stop and ask, do not guess

- **Remote cluster namespace: settled by OD-10 (A, 2026-11-15), see section 12.** The API reads a remote cluster's ConfigMap in the namespace on its registration. The identity the API uses on a remote cluster must still be found in the chart (T076); if it cannot be found, stop and ask. Do not hard-code `<release>-credentials` for remote clusters. The home cluster uses `moduleConfigMapNS` (brief 03 D12).
- Helm chart tests: none exist for RBAC or `lookup` (brief 04 Q3). Adding one needs sign-off.
- The web and module test file names in section 8 are proposals. OD-9 does not name them. The maintainer accepts or renames them.
- Palworld `risky` and `volumeOwnershipCheck` (brief 04 Q4) is still open. T037 does not change Palworld until the module review decides.

## 8. Test files: names to create (no existing test file is edited)

Never edit these existing files: `operator/internal/controller/gameserver_security_test.go`, `api/internal/handlers/resources_security_test.go`, and every other existing test file. Create the new names below.

| Area | New file | Content |
|---|---|---|
| operator unit | `operator/internal/controller/security_overrides_test.go` | resolver, codec, pending, gate, restricted-mode effective values, backup group cases (T015, T026, T040 unit part) |
| operator unit | `operator/internal/controller/security_settings_test.go` | `hasRootOverride`, `rootOverrideSettings`, ConfigMap reader (T050) |
| operator envtest | `operator/internal/controller/security_overrides_envtest_test.go` | reconcile behavior, OD-4 refusal and hold (T017, T027, T039, T051) |
| operator envtest | `operator/internal/controller/security_crd_validation_envtest_test.go` | CEL and bounds (T008) |
| operator unit | `operator/internal/controller/security_builders_test.go` | byte-identical unset, helper containers (T016) |
| operator envtest | `operator/internal/controller/security_backup_envtest_test.go` | backup and restore applied group (T040 envtest part) |
| API unit | `api/internal/handlers/security_overrides_test.go` | cases 1 to 5, shape, reason builder (T013, T028 unit, T048 unit, T049 unit) |
| API envtest | `api/internal/handlers/security_overrides_envtest_test.go` | permission, shape, root rule, audit rows (T014, T028, T048, T049 envtest) |
| API unit | `api/internal/handlers/security_settings_test.go` | GET and PUT shapes, empty body 400, invalid 422, flip logic (T047 unit) |
| API envtest | `api/internal/handlers/security_settings_envtest_test.go` | dedicated router, RBAC, flip, 409, 502, `security_save_failed` (T047 envtest) |
| RBAC unit | `api/internal/rbac/catalog_security_test.go` | `servers:security` exists, namespaced, admin only (T010) |
| E2E | `test/e2e/gameserver_security_e2e_test.go`, `test/e2e/api_security_override_e2e_test.go` | unchanged names |
| gp-module (proposed) | `gp-module/internal/validator/rules_security_test.go`, `gp-module/internal/preview/preview_security_test.go` | T038 |
| module repo (proposed) | `test-validate-py-security.sh` | T036 |
| web (proposed) | `web/src/components/SecurityAdvanced.test.tsx`, `web/src/routes/CreateServerSecurity.test.tsx`, `web/src/routes/tabs/settings/Security.test.tsx`, `web/src/components/clusters/SecuritySettingsPanel.test.tsx` | T029, T041, T052 |

## 9. Production file names

- Operator: `operator/internal/controller/security_overrides.go` (replaces `gameserver_security.go`) and `security_settings.go`.
- API: `api/internal/handlers/security_overrides.go` (replaces `resources_security.go`) and `security_settings.go`. `security_settings_sync.go` is not created.
- Web: `web/src/components/clusters/SecuritySettingsPanel.tsx` replaces `web/src/routes/AdminSettings.tsx` (not touched). Wired into `web/src/components/clusters/ClusterConfiguration.tsx`, next to `BackupDestinationsPanel.tsx`.

## 10. Earlier brief statements that are superseded

| Brief | Statement | Superseded by |
|---|---|---|
| 01 section 3.1, T044 | Rollout on a suspended or idle server "premise" (section 4.3) | section 6 item 5 |
| 01 section 6, T016 | Extend `build_game_container_test.go`, `workload_security_test.go` | `security_builders_test.go` (section 8) |
| 01 T039, D4 | Baseline fix noted in passing | T075 (section 6 item 6) |
| 01 T019, D6 | Change positional signatures of `gamePodSecurityContext`, `buildConfigInitContainer`, `buildGameContainer` | section 11 (keep callers compiling) |
| 02 D15 | Wake admission decision open | section 6 item 4 |
| 02 D12, T051 | Envtest file name `gameserver_security_envtest_test.go` | `security_overrides_envtest_test.go` (section 8) |
| 02 T040, T050 | Add cases to existing `gameserver_security_test.go` | new files (section 8) |
| 02 D7 | Namespace field source | section 3: `ControlPlaneNamespace` from `controlPlaneNamespace` |
| 03 D3 | Empty body 422 vs 400 open | section 6 item 1: 400 |
| 03 D6 | Clone audit open | section 6 item 3: yes |
| 03 D11, D13 | Store and auditor parameters on the validator and `MountConfig` | section 2: no store for the root rule; `MountSecuritySettings` takes the registry and auditor |
| 03 D12 | ConfigMap namespace is `moduleConfigMapNS` | section 1 and 2: the cluster's control-plane namespace (home value is the same) |
| 03 D15 | Config row write | section 1: no config row |
| 03 D16 | Remote ConfigMap writes not designed | section 4 (T076) and section 7 (namespace still open) |
| 03 T013, T014, T028, T047, T048, T049, T010 | Extend or add files named `resources_security*`, `config_envtest_test.go`, `catalog_test.go`, `rbac_test.go` | section 8 |
| 03 T047, T055, T056, T057, T058 | Config section, store read, `newValidators`, ConfigMap mirror and sync, Role with `create` | sections 1 to 5 |
| 03 T058 snippet | Role verbs `get, create, update, patch` | section 4: `get, update, patch` |
| 04 discrepancy 2, Q1 | Open choice of `create` verb | closed: no `create` |
| 04 T036, T038 | Add to `validate.py` test script, `validator_test.go`, `preview_test.go` | new files (section 8) |
| 04 T058 YAML | Role with a `create` placeholder inside `api.yaml` | section 4 |
| tasks.md T052, T063, design prompt | `AdminSettings.tsx` placement | section 9 |

## 11. Signatures and compatibility (no existing test call site changes)

- Keep every existing function name and positional signature that tests call. Add variants instead:
  - `gamePodSecurityContext(tmpl)`, `gameContainerSecurityContext(tmpl)`, `buildConfigInitContainer(...)`: keep as wrappers that resolve from the template. Add `...Resolved` variants that take the resolved values. Production callers use the variants.
  - `buildBackupPodSecurityContext(tmpl)` gains a trailing `appliedFSGroup ...*int64` variadic. No value means the current behavior.
  - `buildRestorePodSpec` and `buildPreparedBackupPodSpec`: same trailing variadic, or a new function. Test call sites (brief 02 D16) stay unchanged.
  - `reconcileStatefulSet` keeps its signature. The hold decision goes through a new internal function that returns the hold flag, and `reconcileStatefulSet` wraps it.
  - `reconcileStatus` already takes variadic conditions: add the `SecurityAllowed` condition through that variadic.
  - `MountLifecycle` and `MountConfig`: optional variadic parameters only.
- Verify each call site with `grep` before you change a signature. CI is the compile verifier for tests, and nobody may edit the tests to make them compile.

## 12. Remote cluster control-plane namespace (OD-10 A, 2026-11-15)

- The cluster registration records the namespace: an optional field `spec.controlPlaneNamespace` on the Cluster object, default `gameplane`, set in the register-cluster form and shown on the cluster page (tasks T078 and T079).
- The API reads a remote cluster's settings ConfigMap in that value. The home cluster keeps `moduleConfigMapNS` (section 2). A remote cluster with no value uses `gameplane`.
- The value must be the operator control-plane namespace of that remote install. A remote install that does not use `gameplane` must be registered with its real namespace. The remote chart's grant (T076) and the ConfigMap Helm creates there (T077) use the same namespace.
- A wrong value cannot turn the setting on. The ConfigMap is not found there, so it reads as off: GET reports `present: false`, and PUT returns 409 `server.cluster.security_settings_missing`.
- The API needs no extra permission on the remote cluster to learn the namespace; it comes from the registration.
- Rejected: a fixed namespace name on every install (OD-10 B), and a label lookup across all namespaces (OD-10 C), which needs cluster-wide ConfigMap list permission.
