# Brief 02: operator backup/restore fsGroup, cluster root setting, mcp-server fixture

Scope: T040, T043 (backup/restore), T050, T051, T060, T061 (operator root setting), T022 (mcp-server). Facts verified against the working tree at /home/user/Gameplane on feat/030-security-context-overrides, read-only. Line numbers are current; spec citations that are stale are corrected in Discrepancies.

## Discrepancies

D1. Backup security context is defined at `operator/internal/controller/backup_controller.go:1156`, not `:1133`. Its only caller is `backup_controller.go:1058` (inside `buildBackupPodSpec`, which starts at `:1045`). Spec's `:1035` is inside the doc comment of `buildBackupPodSpec` (`:1033-1044`).
D2. The live backup path does not pass through `backup_controller.go:1058` directly. Chain: `backup_worker.go:306` (`Spec: r.buildPreparedBackupPodSpec(b, tmpl, source, bound.UID, preparation)`) -> `backup_worker.go:129` `buildPreparedBackupPodSpec` -> `backup_worker.go:130` `spec := r.buildBackupPodSpec(b, tmpl)` -> `backup_controller.go:1058`. In that chain there is no GameServer or StatefulSet in scope; `bound` (a GameServer, from `r.ownedBackupTarget` at `backup_worker.go:251`) is in scope only at `:300-310` in the caller. T043 must therefore thread the applied fsGroup through three signatures, or pass `bound` / the applied value from `backup_worker.go:306`.
D3. Restore: `buildRestorePodSpec` is defined at `restore_controller.go:425` and its security call is `restore_controller.go:433` (`SecurityContext: buildBackupPodSecurityContext(tmpl),`). Spec's `:425` is the def line, which is fine. The caller is `restore_controller.go:277`. `gs` (GameServer) is in scope there (`effectiveGameTemplate(ctx, r.apiReader(), &gs)` at `:216`), so the applied value can be read from `&gs` plus its StatefulSet.
D4. ConfigMap RBAC marker is `operator/internal/controller/gameserver_controller.go:261` (`// +kubebuilder:rbac:groups=core,resources=services;persistentvolumeclaims;configmaps,verbs=get;list;watch`), not `:258`. Events markers are `:268-269`. Chart ClusterRole grant is `charts/gameplane/templates/operator.yaml:66` (configmaps get/list/watch) and events create/get/list/patch/watch at `operator.yaml:74-75`. The spec's "no new RBAC marker" holds.
D5. `SetupWithManager` is `gameserver_controller.go:594-613`. The Pod and GameTemplate watches are at `:609-611`. `Owns(&corev1.ConfigMap{})` already exists at `:605`. Consequence: the cluster-wide ConfigMap informer already exists, so research R9's "one more watched object" is zero extra informers. `operator/internal/secretscope/cache.go:26-28` restricts only `corev1.Secret`, not ConfigMaps. The new `Watches(&corev1.ConfigMap{}, ...)` (T060) reuses the existing informer. Do not add `GenerationChangedPredicate` (ConfigMaps have no generation bump semantics that help here); filter by name and namespace in the map function or a predicate.
D6. Control-plane namespace: the operator does NOT get it from the release namespace by default. In a default chart install the operator is started with `--control-plane-namespace={{ include "gameplane.operatorClusterCredentialsNamespace" . }}` (`charts/gameplane/templates/operator.yaml:396`). `gameplane.operatorClusterCredentialsNamespace` (`_helpers.tpl:220-226`) returns `gameplane.credentialsNamespace` when the API is enabled and not standalone, which is `api.credentialsNamespace` or `operator.controlPlaneNamespace` or `<release>-credentials` (`_helpers.tpl:212-218`); the release namespace only in standalone or no-API mode. The API's own namespace is `GAMEPLANE_NAMESPACE` = pod namespace (`charts/gameplane/templates/api.yaml:398-400`), and it also gets `GAMEPLANE_CREDENTIALS_NAMESPACE` (`api.yaml:402-403`). Consequence for T057/T058: the ConfigMap `gameplane-security-settings` must be written to and read from the credentials/control-plane namespace the operator watches (`--control-plane-namespace`), not `.Release.Namespace`. T058's Role "in the release namespace" is wrong for the default install; it must be in the namespace the API writes, which is `GAMEPLANE_CREDENTIALS_NAMESPACE`. Flag this to the orchestrator.
D7. `WakeStartNamespace` is set from `controlPlaneNamespaceDefault` (`operator/cmd/main.go:538`), not from the flag variable `controlPlaneNamespace` (declared `:144`, flag at `:277`, value from `POD_NAMESPACE` env else `gameplane-system` at `:273-276`). The two diverge when the flag is overridden. `GameServerReconciler` has no control-plane namespace field at all (struct `gameserver_controller.go:45-60`). T060 must add `ControlPlaneNamespace string` to the struct and set it from `controlPlaneNamespace` in the construction at `cmd/main.go:517-540` (`ClusterStatusReconciler` already gets `controlPlaneNamespace` at `cmd/main.go:661`). Reading the namespace with the `WakeStartNamespace` value is the wrong source.
D8. The GameServer controller has NO event recorder and emits no events today. `GameServerReconciler` fields (`gameserver_controller.go:45-60`) have no `EventRecorder`. grep for `Eventf(` / `.Event(` in `operator/internal/controller/*.go` (non-test) finds only `backup_controller.go:876` and `module_locales.go:501`. T061's "emit an event" needs a new field. Pattern to copy: `backup_controller.go:142-148` (`EventRecorder recorder.EventRecorder`), `cmd/main.go:581` (`EventRecorder: mgr.GetEventRecorder("backup-controller")`), helper `backup_controller.go:869-876` (`recordEvent`). Note 159 test sites construct `GameServerReconciler{...}` without a recorder (e.g. `gameserver_status_envtest_test.go:384`, `:2525`), so any emit must be nil-safe or the tests panic.
D9. Stale citations for the GameServer controller, all corrected here: `validateRestrictedTemplate` caller is `gameserver_controller.go:353` (not `:334`); template-revision callers are `gameserver_template_revision.go:222` and `:288` (not `:216`, `:278`); `buildConfigInitContainer` def `:2336` (not `:2310`), call `:1895`; `gameContainerSecurityContext` def `:2496` (call `:2482` inside `buildGameContainer` def `:2397`, not `:2371`); `gamePodSecurityContext` def `:2612` (call `:1935`, not `:2586`/`:1909`); `applyWorkloadSecurity` call `:1944` (not `:1918`); `buildGameContainer` call `:1834` (not `:1808`); `reconcileStatefulSet` def `:1613`, its `CreateOrUpdate` mutate at `:1637` (not `:1611`); the hostport gate that R5 mirrors is `:1916-1925` (not `:1889-1905`; the condition is `:1920`).
D10. `ownedTemplatePod` is `operator/internal/controller/gameserver_template_revision.go:498` (not `:480`). Signature `ownedTemplatePod(ctx, reader client.Reader, gs *GameServer) (*corev1.Pod, error)`.
D11. The spec assumes the R5 "copy forward" is a single gate. It is not: the mutate function assigns the whole pod template wholesale after the replica decision. Order inside the mutate func (`gameserver_controller.go`): replicas set `:1797` -> held-return `:1798-1801` -> `preserveBaseline` return `:1803-1804` -> `ServiceName`/`Selector`/labels `:1807-1816` -> `ss.Spec.Template.Annotations = ann` `:1832` -> `Containers = [buildGameContainer, buildAgentContainer]` `:1833-1836` -> InitContainers `:1894-1896` (nil at else branch) -> hostport affinity gate `:1919-1925` -> pod `SecurityContext = gamePodSecurityContext(tmpl)` `:1935` -> `applyWorkloadSecurity(...)` `:1944`. T020 must snapshot the existing StatefulSet's security-derived parts (pod `SecurityContext`, game container `SecurityContext`, config-init container, `gameplane.local/security-applied` annotation) before `:1832-1944` and restore them when not refreshing. The hostport gate only covers affinity and an annotation, so it is a pattern, not a drop-in.
D12. T039 says "Extend `operator/internal/controller/gameserver_security_envtest_test.go`", but that file does not exist (only `gameserver_security_test.go` exists, with `TestValidateServerEnvSecrets` and `TestReconcileTunnel_CredentialsSecretOwnership`). T051 creates it. Not in my scope but the orchestrator should know T039 is wrong.
D13. T022 says "`mcp-server/tools.go` redaction". There is NO redaction in tools.go. The only transform is `stripManagedFields` (`tools.go:353-375`), which deletes `metadata.managedFields`. Spec and status are returned verbatim, so the round-trip assertion holds with no code change, as research R15 says. Citations: generic list handler `tools.go:114` (registered `:63`), generic get handler `tools.go:140` (registered `:70`). R15's `tools.go:64, 109, 135` is stale.
D14. T022 fixture: `newUnstructuredCRD(kind, ns, name string, status map[string]any)` (`mcp-server/main_test.go:147-161`) sets only `status`, has no spec parameter. `testClientWithFixtures` (`main_test.go:163-182`) hard-codes one GameServer with `status.phase: Running`. The new fixture needs a separate unstructured object with `spec.security` and `status.security`, passed to `dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrToListKindMap(), obj)` and `kube.NewFrom(typed, dyn, scheme)` (`main_test.go:181`). Do not change the shared fixture's existing assertions.
D15. Interaction not in the spec: the idle wake reservation runs BEFORE the StatefulSet mutate. `gameserver_idle.go:333` calls `r.reserveWakeStart(ctx, gs)` (`gameserver_wake_admission.go:23`) and sets status "cold start admitted; waiting for game readiness" (`gameserver_idle.go:341`). A sleeping server with a root override, held by T061 at zero, still consumes a fleet wake admission and reports cold start admitted while the StatefulSet stays at zero. T061 should either skip admission for held servers or set a status reason. Decision needed; flag to orchestrator.
D16. Test call sites of changed signatures (will not compile after T043/T019/T060-T061 unless updated; CI is the verifier):
  - `buildRestorePodSpec` (T043): `operator/internal/controller/recovery_test.go:104`, `recovery_controller_test.go:991`, `auxiliary_resources_test.go:63`, `restore_delete_flag_test.go:24`.
  - `buildPreparedBackupPodSpec` (T043, if its signature changes): `backup_budgets_test.go:556, 570, 584`.
  - `buildConfigInitContainer` (T019): `gameserver_files_test.go:16, 48, 61, 77, 143, 181, 194`; `auxiliary_resources_test.go:69`.
  - `gamePodSecurityContext` (T019): `build_game_container_test.go:170, 179`; `workload_security_test.go:93`.
  - `buildGameContainer` (T019): `build_game_container_test.go` (5 calls in total in `*_test.go`).
  - `reconcileStatus` (T061 if it gets a new param): 8 test call sites (e.g. `gameserver_status_envtest_test.go:385`).
  - `reconcileStatefulSet` (T020/T061): 54 test call sites in `*_test.go`.
  Prefer adding an argument-free accessor or a new variadic param over changing positional signatures.

## T040 (test cases, `backup_envtest_test.go` and `gameserver_security_test.go`)

- Existing envtest file: `operator/internal/controller/backup_envtest_test.go` (funcs `TestBackup_*`, e.g. `:22`). Suite: `suite_envtest_test.go` (`TestMain` at `:37`).
- Existing unit file: `operator/internal/controller/gameserver_security_test.go` (`package controller`, funcs at `:16`, `:333`). Add cases there; do not rename.
- Cases to add (from spec T040 / research R6): stopped server with pending fsGroup change; start in progress whose pod has not passed volume setup. Assert `buildBackupPodSecurityContext` (after T043 signature, takes applied group) and restore pod (`restore_controller.go:433`) use applied.fsGroup, not desired. Start failing before volume setup leaves `applied` and `pending` unchanged. Never-started server (no observed annotation) uses desired.
- Observed-applied source (T018, not written yet): StatefulSet annotation `gameplane.local/security-applied-observed`, read from the StatefulSet object (not pod template). Helper name per T018 is `resolveGameSecurity` family in `gameserver_security.go` (does not exist yet).

## T043 (implementation, `backup_controller.go`, `backup_worker.go`, `restore_controller.go`)

Current code, `backup_controller.go:1156-1166`:
```go
func buildBackupPodSecurityContext(tmpl *gameplanev1alpha1.GameTemplate) *corev1.PodSecurityContext {
	nonRoot := true
	uid := int64(65532)
	fsGroup := uid
	if tmpl.Spec.Security != nil && tmpl.Spec.Security.FSGroup != nil {
		fsGroup = *tmpl.Spec.Security.FSGroup
	}
	return &corev1.PodSecurityContext{
		RunAsNonRoot:   &nonRoot,
		RunAsUser:      &uid,
		RunAsGroup:     &uid,
		FSGroup:        &fsGroup,
		SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
	}
}
```
Intended change: take the applied filesystem group as a `*int64` (nil = never started, fall back to the template value as today; non-nil = applied value, which may be 0 or any uid). Shape:
```go
func buildBackupPodSecurityContext(tmpl *GameTemplate, appliedFSGroup *int64) *corev1.PodSecurityContext
// fsGroup := uid; if appliedFSGroup != nil { fsGroup = *appliedFSGroup } else if tmpl...FSGroup != nil { fsGroup = *tmpl...FSGroup }
```
Note: the "never started" fallback must be decided from `status.security.applied` absence, not from a zero value. Keep the default 65532 unchanged.

Restore (`restore_controller.go:423-433`): `buildRestorePodSpec(rs, src, tmpl)` gains the applied group (read from `&gs` plus its StatefulSet in the same reconcile); call site `restore_controller.go:277`. Test call sites: see D16.

Backup (`backup_controller.go:1045`, `backup_worker.go:129-130`, `backup_worker.go:306`): thread the applied group from `bound` (GameServer, `backup_worker.go:251`) at `:306`, through `buildPreparedBackupPodSpec` (`:129`) into `buildBackupPodSpec` (`:1045`) and then `:1058`. The applied reader from T018 needs `r.backupAPIReader()` or the same reader used at `:300`.

Depends on T018 (applied reader and annotation codec). Do not implement the reader in T043.

## T050 (unit tests, `operator/internal/controller/gameserver_security_test.go`)

- New file `operator/internal/controller/security_settings.go` (NEW, does not exist). Functions to test: `hasRootOverride(gs *GameServer) bool`, `rootOverrideSettings(gs *GameServer) []string` (names offending settings), and the ConfigMap reader (absent, missing key, `"false"`, garbage, `"true"`; only exact `"true"` is on, everything else off).
- Root forms per FR-005/R9 (table): override `runAsUser` value 0 or `useImageDefault: true`; `runAsGroup` value 0 or `useImageDefault: true`; `fsGroup` value 0. Non-root values and a module default that is root (template `security.runAsUser: 0`) are NOT overrides and must return false. `fsGroup` `useImageDefault` is not in the rule table (R3/R10 list only runAsUser/runAsGroup useImageDefault and fsGroup value 0); test should assert exactly that.
- Template field names: `GameTemplateSpec.Security` (`operator/api/v1alpha1/gametemplate_types.go:177`), `GameSecuritySpec` `:185-204`. GameServer override field does not exist yet (T006/T007).

## T051 (envtest, NEW `operator/internal/controller/gameserver_security_envtest_test.go`)

- Pattern to copy: any `*_envtest_test.go` (e.g. `gameserver_status_envtest_test.go`). Reconciler construction in envtests: `&GameServerReconciler{Client: k8sClient, APIReader: k8sClient, Scheme: scheme, ...}` (`gameserver_status_envtest_test.go:384`, `:2525`). Set the new `ControlPlaneNamespace` field (D7) in the test to the namespace where the test writes the ConfigMap.
- Empty `ControlPlaneNamespace` (as in the 159 existing constructions) must mean "setting off" (fail closed), so existing tests keep working.
- Assertions per T051: ConfigMap absent or `"false"` + stopped server with root override stays at 0 replicas with `SecurityAllowed=False` reason `RootOverrideDisallowed` naming the settings; running server keeps pod and restart count and only gets the informational condition; flipping ConfigMap to `"true"` starts the stopped server (the watch re-enqueues it) with no other event; clearing the override starts it; flipping off never restarts or stops any server; unrelated servers not enqueued by the flip.

## T060 (NEW `operator/internal/controller/security_settings.go`, `gameserver_controller.go:605-611`)

- ConfigMap name: `gameplane-security-settings`, key `allowRootOverrides`. Namespace: the operator's `--control-plane-namespace` (D6, D7). Read from the informer cache (`r.Get` on `corev1.ConfigMap`, cluster-wide informer already exists via `Owns` at `:605`) or from `r.APIReader` as the wake path does (`gameserver_wake_admission.go:26`). Fail closed on any error or missing object.
- Watch: add next to the existing watches at `gameserver_controller.go:609-611`:
```go
Watches(&corev1.ConfigMap{}, handler.EnqueueRequestsFromMapFunc(r.mapSecuritySettingsToGameServers)).
```
  Map function filters `obj.GetName() == "gameplane-security-settings" && obj.GetNamespace() == r.ControlPlaneNamespace`, then lists GameServers (`r.List`, cached, like `mapTemplateToGameServers` at `:618-`) and enqueues those for which `hasRootOverride` is true, across all namespaces. Unrelated servers not enqueued.
- No new RBAC marker: `gameserver_controller.go:261` already covers configmaps get/list/watch; chart `operator.yaml:66` covers it.
- Add `ControlPlaneNamespace string` to `GameServerReconciler` (`gameserver_controller.go:45-60`) and set it in `operator/cmd/main.go:517-540` from `controlPlaneNamespace` (D7). Do not reuse `WakeStartNamespace`.

## T061 (`reconcileStatefulSet`, `gameserver_status.go`, events)

Where to hold (research R9): before the R5 refresh, so the StatefulSet is left as it was.
- Existing precedent inside the same mutate func: guard holds set `actualReplicas = 0` (`gameserver_controller.go:1645`, `:1686`, `:1710`, `:1740`, `:1757`, `:1765`, `:1772`); `ss.Spec.Replicas = &actualReplicas` at `:1797`; return-early before template assignment at `:1798-1804` (`held` branch returns at `:1798-1801`; `preserveBaseline` return at `:1803-1804`).
- Proposed placement: immediately BEFORE `ss.Spec.Replicas = &actualReplicas` at `:1797` (after `preserveBaselinePodTemplate` at `:1793`), because `CreateOrUpdate` has already loaded the existing StatefulSet into `ss` and `:1797` overwrites `ss.Spec.Replicas`; checking the existing replicas after `:1797` would read the new value, not the live one:
```go
existing := !ss.CreationTimestamp.IsZero() || ss.ResourceVersion != ""
wasStopped := existing && (ss.Spec.Replicas == nil || *ss.Spec.Replicas == 0)
if holdRoot /* !securityAllowed && hasRootOverride(&live) && actualReplicas > 0 */ && wasStopped {
    return nil // StatefulSet left exactly as it was (zero replicas, old template)
}
// not existing (create): fall through, render at zero: actualReplicas = 0 (a StatefulSet cannot be created without containers).
```
  Guard against holding a RUNNING server: the condition must be `ss.Spec.Replicas` already zero (or create). A running server (`*ss.Spec.Replicas > 0` and desired > 0) must be left untouched: no zeroing, no template change. Desired zero (suspend/idle, `desiredReplicas` at `gameserver_controller.go:1320`) is never changed.
- Hold must not stop a running server: when `ss.Spec.Replicas > 0`, skip the hold entirely (even when the ConfigMap is off).
- Condition source: `reconcileStatus` (`gameserver_status.go:42-46`) takes `hostportConditions ...*metav1.Condition` as its last param; the existing caller passes `plan.hostportCondition` at `gameserver_controller.go:562`. `reconcileStatefulSet` returns only `error` (`:1613`). The hold decision must reach status: add a field on the reconcile-local state or a second return. Helpers to mirror: `applyHostportCondition` (`gameserver_status.go:330`), `removeCondition` (`gameserver_status.go:1287`), status applied at `:197-201`.
- Condition `SecurityAllowed`: `False` / reason `RootOverrideDisallowed` with settings named (`rootOverrideSettings`) and what clears it; `True` once allowed or cleared. For a running server: `False` with the "keeps running but will not start again" message (R9).
- Event: needs `EventRecorder` on `GameServerReconciler` (D8). Nil-safe emit (159 test constructions have no recorder). Pattern: `backup_controller.go:869-876`. Reason suggestion: `RootOverrideDisallowed`. RBAC for events already present (`gameserver_controller.go:268`).
- Never restart or stop on a flip: the ConfigMap watch (T060) only enqueues. Turning the setting off must not change `ss.Spec.Replicas` of a running server.
- Interaction D15 (wake admission) must be decided.

## T020 interaction note (for the R5 copy-forward used by T061's ordering)

Copy-forward must happen after `:1803` and before `:1832-1944` (D11). Do not place the T061 hold inside the copy-forward branch.

## T050/T051 test helpers and bucket

- Bucket: `test/e2e/buckets.sh` (`bucket_operator`) for the E2E tests in T042/R16 (not my task; R16 decides `bucket_operator`).

## T022 (mcp-server)

- Test file: `mcp-server/main_test.go` (540 lines). Helpers: `newUnstructuredCRD` `:147-161`; `gvrToListKindMap` `:139-145`; `testClientWithFixtures` `:163-182`; `callToolText` `:108-137`; `connectInMemory` `:54-73`; happy path `TestToolsListAndGetHappyPath` `:184-259` (uses `list_gameplane_resources` and `get_gameplane_resource` on GameServer `games/my-server`).
- Add a new test func (e.g. `TestGameServer_SecurityFieldsUnredacted`) that builds a GameServer unstructured object with `spec.security` (runAsUser/runAsGroup/fsGroup value and useImageDefault forms) and `status.security` (`desired`, `applied`, `pending`), gives it a fake dynamic client via `kube.NewFrom` (`main_test.go:181`), calls `get_gameplane_resource` and `list_gameplane_resources` and asserts the JSON text contains the security keys and values unchanged.
- Do not change `tools.go`: no redaction exists (D13), and the T022 task says no code change.
- Docs: `mcp-server/README.md` one example (tool table at `README.md:64`, `:67`; "read-only" sentence at `:37`); `mcp-server/specs.md` one sentence (generic exposure is at `specs.md:57`, Security considerations at `specs.md:150`).
- Read-only: `TestRegisteredToolsAreReadOnly` (`main_test.go:75`) and `TestClientHasNoMutatingMethods` (`main_test.go:42`) must keep passing; the new test adds no write tool.

## Facts summary (exact current locations)

| What | Where |
|---|---|
| buildBackupPodSecurityContext def / call | backup_controller.go:1156 / :1058 |
| buildBackupPodSpec def | backup_controller.go:1045 (doc 1033-1044) |
| buildPreparedBackupPodSpec def / call to buildBackupPodSpec | backup_worker.go:129 / :130 |
| buildPreparedBackupPodSpec call (job) | backup_worker.go:306 |
| buildRestorePodSpec def / security call / job call | restore_controller.go:425 / :433 / :277 |
| GameServerReconciler struct | gameserver_controller.go:45-60 (WakeStartNamespace :54) |
| SetupWithManager / Owns(ConfigMap) / Pod+Template Watches | :594 / :605 / :609-611 |
| RBAC configmaps / events markers | :261 / :268-269 |
| reconcileStatefulSet def / CreateOrUpdate | :1613 / :1637 |
| replicas assignment / preserveBaseline return | :1797 / :1803 |
| containers / init / pod SecurityContext / applyWorkloadSecurity | :1833 / :1894-1896 / :1935 / :1944 |
| hostport gate | :1916-1925 |
| desiredReplicas def / Suspend check / idle sleep check | :1320 / :1352 / :1414, :1423 |
| validateRestrictedTemplate caller | :353 |
| gameContainerSecurityContext def / call | :2496 / :2482 (inside buildGameContainer def :2397, call site :1834) |
| buildConfigInitContainer def / call | :2336 / :1895 |
| gamePodSecurityContext def | :2612 |
| reconcileStatus def / caller | gameserver_status.go:42 / gameserver_controller.go:562 |
| applyHostportCondition / removeCondition | gameserver_status.go:330 / :1287 |
| ownedTemplatePod | gameserver_template_revision.go:498 |
| validateRestrictedTemplate template-revision callers | gameserver_template_revision.go:222, :288 |
| wake reservation (before mutate) | gameserver_idle.go:333; gameserver_wake_admission.go:23-26 |
| Operator flag control-plane-namespace | operator/cmd/main.go:273-278 (flag :277) |
| WakeStartNamespace construction | operator/cmd/main.go:538 (uses controlPlaneNamespaceDefault) |
| GameServerReconciler construction | operator/cmd/main.go:517-540 |
| ClusterStatusReconciler gets controlPlaneNamespace | operator/cmd/main.go:661 |
| Backup EventRecorder pattern | operator/cmd/main.go:581; backup_controller.go:142-148, :869-876 |
| Secret-only cache restriction | operator/internal/secretscope/cache.go:18-30 |
| Chart operator controlPlane flag | charts/gameplane/templates/operator.yaml:396 (helper _helpers.tpl:220-226; credentials ns _helpers.tpl:212-218) |
| Chart operator ClusterRole configmaps / events | charts/gameplane/templates/operator.yaml:66 / :74-75 |
| Chart API GAMEPLANE_NAMESPACE / CREDENTIALS | charts/gameplane/templates/api.yaml:398 / :402-403 |
| mcp-server shared fixture / helpers | mcp-server/main_test.go:163-182 / :147-161 |
| mcp-server generic handlers | mcp-server/tools.go:114 (list), :140 (get), registration :63, :70 |
| mcp-server managedFields strip (only transform) | mcp-server/tools.go:353-375 |


## Reviewer additions

- R1. The T061 snippet in the first draft read `ss.Spec.Replicas` after `:1797` (already overwritten); corrected above to evaluate before the assignment. `live` (the GameServer read inside the mutate, `var live gameplanev1alpha1.GameServer` at `:1650`) is the variable to pass to `hasRootOverride`; the outer `gs` may be stale.
- R2. ConfigMap informer is cluster-wide: `operator/cmd/main.go` cache options (`secretscope.CacheOptions`, `cache.go:18-30`) restrict only Secrets, so a ConfigMap in the control-plane namespace is readable from cache. Confirmed.
- R3. T060/T040/T001 spec citations stale: T060 cites `gameserver_controller.go:583` (actual Watches `:609-611`) and `:258` (actual `:261`); T040 cites `backup_controller.go:1133`/`:1035`/`restore_controller.go:425` (see D1, D3).
