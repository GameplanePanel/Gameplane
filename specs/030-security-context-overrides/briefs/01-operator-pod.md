# Brief 01: operator pod building and lifecycle

Scope: T005, T006, T007, T015 to T021, T031, T032, T039, T044 (plus T016, T017 test specifics). Facts verified against the working tree on branch `feat/030-security-context-overrides` (read only). Line numbers are current; "stale" means the spec citation differs from the code today. All `file` paths are under `/home/user/Gameplane/`. Controller package = `operator/internal/controller/` (abbreviated `ctl/`).

## Spec references

- Spec: `spec.md` (FR-001..FR-013, US1..US4). Plan: `plan.md` (gap 3 at `plan.md:86-104`). Research: `research.md` R4 (resolver, `:85`), R5 (gate, `:99`), R6 (applied source, `:123`), R7 (status, `:138`), R8 (ownership check, `:146`), R9 (cluster setting, `:160`). Data model: `data-model.md` "Pod-level encoding" (`:133`), "Start refusal" (`:147`), "State transitions" (`:168`), "Invariants" (`:195`). Contracts: `contracts/crd-gametemplate-security.md`, `contracts/crd-gameserver-security.md`.

---

## 1. CRD types (T005, T006, T007)

### 1.1 Template side (T005)

- `operator/api/v1alpha1/gametemplate_types.go:186` `Security *GameSecuritySpec \`json:"security,omitempty"\`` (field in `GameTemplateSpec`). Doc comment `:181-185`.
- `operator/api/v1alpha1/gametemplate_types.go:194` `type GameSecuritySpec struct`. Members (all `*int64`, `+kubebuilder:validation:Minimum=0`, `Maximum=4294967295`, `+optional`): `RunAsUser` `:199`, `RunAsGroup` `:206`, `FSGroup` `:213` (line numbers of the field declarations, approx; struct ends `:215`).
- **Stale**: spec T005 says "`gametemplate_types.go:185`". `:185` is a comment line; the type is `:194`, the field `:186`.
- Intended change (T005, contract `crd-gametemplate-security.md`):
  - add `VolumeOwnershipCheck *SecurityOwnershipCheck` (or `*string`) with `+kubebuilder:validation:Enum=EveryStart;OnlyWhenWrong`, `+optional`, doc: omitted means `OnlyWhenWrong`; only effective when `fsGroup` is set.
  - add `Risky *SecurityRiskSpec \`json:"risky,omitempty"\`` (`+optional`).
  - new `type SecurityRiskSpec struct` with four optional pointer members `RunAsUser`, `RunAsGroup`, `FSGroup`, `VolumeOwnershipCheck` of type `*SecurityRiskNote`; `type SecurityRiskNote struct { Reason string \`json:"reason,omitempty"\` }` with `+kubebuilder:validation:MaxLength=200`. Contract says `reason?` is optional, so each member is a pointer (presence = "risky"). Contract: a `risky` member may name a setting the template does not set.
  - Task refs: T005; contract `crd-gametemplate-security.md` "Shape", "Behavior"; data-model `:16-45` (`risky` at `:28`); research R3.
- Operator reads only the four values, never `Risky` (contract "Behavior").

### 1.2 GameServer side (T006)

- `operator/api/v1alpha1/gameserver_types.go:161` `Capture *CaptureConfiguration \`json:"capture,omitempty"\`` is the last field of `GameServerSpec` (`:41`). Insert `Security *GameServerSecuritySpec` immediately after `:161` (T006 says "after `Capture` (`:161`)": CORRECT).
- Condition-type const block: `gameserver_types.go:24-38` (`// GameServer condition types.` `const (...)` starts `:25`; the `:13-21` block is phase constants, do not add there; existing `GameServerConditionAddressAssignment` `:30`, `GameServerConditionDataWipe` `:37`). Add `GameServerConditionSecurityAllowed = "SecurityAllowed"` here with a doc comment in the same style (type constants are prefixed `GameServerCondition...`).
- Reason strings are NOT constants in this package. Convention is literal strings: `Reason: "JobFailed"` (literal in tests `ctl/gameserver_wipe_poll_test.go:23`; production `ctl/gameserver_wipe.go:341`, compared at `ctl/gameserver_wipe_guard.go:78`; `ctl/backup_controller.go:594`). For `RootOverrideDisallowed` either a literal (matches convention) or a new `const` next to the type const. Note in the brief: the operator writes the status only via the Patch path; reason is consumed by web i18n id `condition.<type>.<reason>` (per the comment at `gameserver_types.go:576-580`, and `web/src/lib/i18n/conditionMessage.test.ts`). A new reason therefore needs a translation entry (out of this area; flag to the web owner).
- `GameServerSpec.Security *GameServerSecuritySpec` new type `GameServerSecuritySpec` with fields `RunAsUser`, `RunAsGroup`, `FSGroup` of type `*SecurityIDOverride` and `VolumeOwnershipCheck *string` (enum `EveryStart|OnlyWhenWrong`). `SecurityIDOverride{ Value *int64 (Min 0, Max 4294967295); UseImageDefault bool }`. CEL rules per `contracts/crd-gameserver-security.md` "spec.security": exactly one of `value`/`useImageDefault`; `useImageDefault` must be `true` when present. Messages verbatim: `security.<id>: set exactly one of value or useImageDefault`, `security.<id>.useImageDefault must be true when set`. Existing CEL style example: `gameserver_types.go` (`+kubebuilder:validation:XValidation:rule="self == oldSelf",...` on `TemplateRef`, at `:49`).
- `GameServerStatus` starts at `gameserver_types.go:558`. `Conditions` `:574` (`patchStrategy:"merge"`, `patchMergeKey:"type"`). Add `Security *GameServerSecurityStatus` (`+optional`) after `Capture` (`:611`) or next to `Conditions`; `GameServerSecurityStatus{ Desired, Applied *SecuritySettings (Applied omitted before first start); Pending bool }`, `SecuritySettings{ RunAsUser, RunAsGroup, FSGroup SecurityIDStatus; VolumeOwnershipCheck SecurityCheckStatus }`, `SecurityIDStatus{ Value *int64 (omitempty); Source string enum }`, `SecurityCheckStatus{ Value string; Source string }`. Source enum: `ModuleDefault|Override|ImageDefaultOverride|ImageDefault|BuiltInDefault` (data-model `:127`ff).
- Spec citation "`GameServerStatus.Security` (`:558`)": CORRECT (struct start).

### 1.3 Regeneration (T007)

- `make generate && make manifests` (CLAUDE.md rule 7). Files to commit: `operator/api/v1alpha1/zz_generated.deepcopy.go`, `operator/config/crd/gameplane.local_gameservers.yaml`, `operator/config/crd/gameplane.local_gametemplates.yaml`, `charts/gameplane/charts/gameplane-crds/crds/*.yaml` (both CRDs), `charts/gameplane/crd-manifests/*.yaml` (both CRDs). Verify the exact filenames exist with `ls operator/config/crd charts/gameplane/crd-manifests` before the brief's agent runs `make` (read-only check only; do not run make locally: CLAUDE.md rule 8 allows only compile checks; `make generate` is codegen, so the agent running it must be CI-safe; the parent decides).
- CRD path check: `ctl/crd_validation_envtest_test.go` loads `operator/config/crd` (`suite_envtest_test.go:44`), so T007 output must be present before envtest-based T017, T039.

---

## 2. Resolver, encoding and annotation codec (T018, T031, T015)

### 2.1 New file (T018, T031)

- `operator/internal/controller/gameserver_security.go` does NOT exist (checked). Create it (package `controller`).
- Annotation keys: no collision. Existing `gameplane.local/...` keys in `ctl/` are `config-hash`, `hostport-node`, `effective-template-revision`, `template-revision`, `template-baseline*`, `wipe-*`, `stop-requested`, `capture-*`, `protected-config-keys`, `idle-*`, `restart-*`, `node`. None is `security*`. New keys: `gameplane.local/security-applied` (pod template, carries resolved values+sources as compact JSON, R5/data-model) and `gameplane.local/security-applied-observed` (on the StatefulSet object, R6).
- Pod template annotation writes: follow the existing pattern in `reconcileStatefulSet` (`ctl/gameserver_controller.go:1820-1823`):
  ```go
  ann := ss.Spec.Template.Annotations
  if revision := gs.Annotations[effectiveTemplateAnnotation]; revision != "" {
      if ann == nil { ann = map[string]string{} }
      ann[templateRevisionAnnotation] = revision
  }
  ```
  (current code at `:1805-1816` region; the template revision annotation is the precedent).
- Existing "resolved by one function" precedent: none. Use R4 design: pure `resolveGameSecurity(tmpl *GameTemplate, gs *GameServer) GameSecurity` returning per-id `{Value *int64; Source string}` and ownership check `{Value string; Source string}`.

### 2.2 Resolution rules (R4, FR-003; contract "Reconciler behavior")

- ids (`runAsUser`, `runAsGroup`, `fsGroup`), in order: `gs.Spec.Security.<id>.UseImageDefault==true` → value nil, source `ImageDefaultOverride`; else `gs...Value != nil` → value, source `Override`; else `tmpl.Spec.Security.<id> != nil` → value, source `ModuleDefault`; else value nil, source `ImageDefault`.
- ownership check: override (`gs.Spec.Security.VolumeOwnershipCheck`), else template (`tmpl.Spec.Security.VolumeOwnershipCheck`) → source `Override`/`ModuleDefault`; else `OnlyWhenWrong`, source `BuiltInDefault` (gap 1 in plan.md:88).
- `pending` (contract "status"): `applied != nil && any value differs` (source-only change is not pending).

### 2.3 Pod encoding (data-model "Pod-level encoding", `:133-145`)

| Setting | Pod field | Current code |
|---|---|---|
| runAsUser / runAsGroup | game container `SecurityContext` | `ctl/gameserver_controller.go:2496-2506` |
| fsGroup | pod `SecurityContext.FSGroup`, config-init `RunAsGroup` + chgrp target | `:2612-2617`, `:2345-2352` |
| volumeOwnershipCheck | pod `SecurityContext.FSGroupChangePolicy`: `EveryStart→Always`, `OnlyWhenWrong→OnRootMismatch`, written only when fsGroup applies | new |

Current code (`ctl/gameserver_controller.go:2496-2506`):
```go
func gameContainerSecurityContext(tmpl *gameplanev1alpha1.GameTemplate) *corev1.SecurityContext {
	sec := tmpl.Spec.Security
	if sec == nil || (sec.RunAsUser == nil && sec.RunAsGroup == nil) {
		return nil
	}
	return &corev1.SecurityContext{
		RunAsUser:  sec.RunAsUser,
		RunAsGroup: sec.RunAsGroup,
	}
}
```
Definition is at `:2496` (spec T019 says `:2470`: STALE, the call site inside `buildGameContainer` is `:2482`, the definition `:2496`).

Current `gamePodSecurityContext` (`ctl/gameserver_controller.go:2612-2617`):
```go
func gamePodSecurityContext(tmpl *gameplanev1alpha1.GameTemplate) *corev1.PodSecurityContext {
	sec := tmpl.Spec.Security
	if sec == nil || sec.FSGroup == nil {
		return nil
	}
	return &corev1.PodSecurityContext{FSGroup: sec.FSGroup}
}
```
Spec T019 says `:2586`: STALE. Definition `:2612`.

Current config-init (`ctl/gameserver_controller.go:2336`, definition; spec T019 says `:2310`: STALE):
```go
// :2345-2348
if tmpl.Spec.Security != nil && tmpl.Spec.Security.FSGroup != nil {
    group := *tmpl.Spec.Security.FSGroup
    security = &corev1.SecurityContext{RunAsGroup: ptrTo(group)}
// :2352 chgrp inside cpCmd uses fmt.Sprintf(..., group, group) -> "[ \"$group\" = %d ] || chgrp %d"
```
Intended: signature takes the resolved filesystem group (`*int64`) instead of reading `tmpl.Spec.Security`; keep `security == nil` when no group (byte-identical). Config-init has no runAsUser; it only gets `RunAsGroup` + the chgrp command.

Pod field `FSGroupChangePolicy` (new): needs `corev1.PodFSGroupChangePolicy` constants `corev1.FSGroupChangeAlways` and `corev1.FSGroupChangeOnRootMismatch` (k8s.io/api core/v1 names; verify with the module source before relying on them, see section 9).

### 2.4 Annotation codec / readers (R5, R6, data-model "Pod-level encoding" last para)

- Pre-feature reader: no annotation → non-empty template values `ModuleDefault`, empty ids `ImageDefault`, ownership check `EveryStart` with `BuiltInDefault` (data-model `:144`). Note the contradictory-looking pair: an unannotated pod has no change policy, Kubernetes treats it as `Always` (= `EveryStart`); the reader reports `EveryStart`, and `desired` is `OnlyWhenWrong` when fsGroup applies, so the pre-feature server shows `pending=true` until its next start (plan/data-model).
- `ownedTemplatePod` (`ctl/gameserver_template_revision.go:498-514`): spec says `:480` (STALE). It returns nil when the StatefulSet is not controlled by gs, when pod `<gs.Name>-0` is absent, or when the pod is not controlled by the StatefulSet or is deleting. Use it for the observed-annotation copy (R6 step 2) and for the applied-values reader.
- `observedTemplateRevision` (`:516-531`) is the precedent: it checks `container.Name == "game" && container.State.Running != nil`. R6 wants the start signal `PodReadyToStartContainers=True` or any started init/game container status. Note: `ownedTemplatePod` returns the pod; use `pod.Status.Conditions` for `corev1.PodReadyToStartContainers` (see section 9 for the k8s.io/api check).
- Observed annotation lives on the StatefulSet object (`ss.Annotations`), not the pod template.

---

## 3. reconcileStatefulSet: mutate function and zero-to-one gate (T019, T020)

Current structure (`ctl/gameserver_controller.go`):

| Line | Current code |
|---|---|
| `:1613` | `func (r *GameServerReconciler) reconcileStatefulSet(` (spec `:1611`: stale by 2) |
| `:1637` | `_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ss, func() error {` (the mutate function starts here) |
| `:1643-1646` | `actualReplicas := replicas` (restore/wipe guard sets 0) |
| `:1650-1657` | live GameServer re-read; UID and effective-template fences |
| `:1698` | `op, held, err := lifecycleOwner(live)`; held-operation branches `:1698-1790` |
| `:1793` | `preserveBaseline, err := r.preserveBaselinePodTemplate(ctx, &live, ss)` |
| `:1797` | `ss.Spec.Replicas = &actualReplicas` (overwrites the live replica count; the gate must read `ss.Spec.Replicas` BEFORE this line) |
| `:1798-1801` | `if held && (...) && (!ss.CreationTimestamp.IsZero() || ss.ResourceVersion != "") { return nil }` |
| `:1803-1805` | `if preserveBaseline { return nil }` |
| `:1833-1834` | `ss.Spec.Template.Spec.Containers = []corev1.Container{ buildGameContainer(gs, tmpl, image, ver, mc), ...` |
| `:1895` | `ss.Spec.Template.Spec.InitContainers = []corev1.Container{buildConfigInitContainer(r.ConfigInitImage, tmpl, r.AgentResources)}` |
| `:1920-1930` | hostport affinity gate: `if actualReplicas == 0 \|\| ss.ResourceVersion == "" \|\| ...hostportNodeAnnotation != ""` (spec says "gate near :1889-1905": STALE, the real hostport gate is `:1920`, and it is the pattern R5 cites) |
| `:1935` | `ss.Spec.Template.Spec.SecurityContext = gamePodSecurityContext(tmpl)` (comment `:1933-1934`: unconditional assign) |
| `:1944` | `applyWorkloadSecurity(&ss.Spec.Template.Spec, tmpl, r.RestrictedWorkloads, r.AgentTokenExpirationSeconds)` |
| `:1959` | `return controllerutil.SetControllerReference(gs, ss, r.Scheme)` |

Current `gameContainerSecurityContext` is called from `buildGameContainer` at `:2482` (`c.SecurityContext = gameContainerSecurityContext(tmpl)`). `buildGameContainer` is at `:2397`.

### 3.1 Gate (R5)

Intended, inside the mutate function, before `:1797`:
```go
prevReplicas := ss.Spec.Replicas   // live value, read before :1797 overwrites it
refresh := ss.ResourceVersion == "" ||
    (prevReplicas != nil && *prevReplicas == 0 && actualReplicas > 0)
```
- Note `ss.Spec.Replicas` is nil on a fresh object; `ss.ResourceVersion == ""` already covers create.
- When `!refresh`: copy forward from a snapshot taken at the top of the mutate function (before the containers/initContainers/pod SecurityContext assignments at `:1833`, `:1895`, `:1935`): the game container `SecurityContext` (from the existing `ss.Spec.Template.Spec.Containers` entry named `gameContainerName`), `ss.Spec.Template.Spec.SecurityContext`, the config-init container (`InitContainers`), and the `gameplane.local/security-applied` annotation. Simplest shape: `prev := ss.Spec.Template.Spec.DeepCopy()` and `prevAnn := ss.Spec.Template.Annotations[securityAppliedAnnotation]` before the template rebuild; then restore the security fields from `prev` when `!refresh`.
- Caution: `applyWorkloadSecurity` (`:1944`) also writes into the game container's and pod's SecurityContext in restricted mode (`workload_security.go:56-57, 60-62`). The copy-forward must run after `applyWorkloadSecurity` or cover that path, otherwise the restricted-mode values are rebuilt from the template (see D8 in section 9).
- Early returns before the template rebuild (`:1798-1801` held ops, `:1803-1805` preserveBaseline) skip ALL template writes, so security is not written on those paths either. That is consistent with R5, but the observed annotation and `status.security.applied` then are not refreshed on those paths. See section 5.3 and D4.

### 3.2 Gate scope vs. zero-to-one through the rest of the code (R5 "rollout", T044)

- `desiredReplicas` (`:1320`) returns the replica count. A restart (`restartPhase`) is scale-down then scale-up (ack at `:1408`; spec says `:1283-1290`: stale). Suspend and idle stop return 0 via `softStop` (tail `:1421-1428`: `stopping := gs.Spec.Suspend || restart == restartDraining || idle == idleAsleep`).
- Refresh point is the first reconcile where live replicas are 0 and desired `actualReplicas` is 1. The `softStop` path (`:1493`) runs while the pod still exists, so there is one reconcile at zero where the pod template is not yet refreshed. Good.

---

## 4. Template revision: pin, rollouts and validation call sites (T032, T044)

### 4.1 Restricted-workload validation call sites

- `ctl/gameserver_controller.go:352-355` (Reconcile):
  ```go
  if r.RestrictedWorkloads {
      if err := validateRestrictedTemplate(&tmpl); err != nil {
          return ctrl.Result{}, r.setPhase(ctx, &gs, err.Error())
      }
  }
  ```
  Spec T032 cites `gameserver_controller.go:334`: STALE. `:334` is inside the `reconcileTemplateRevision` error branch (`:331` setPhase with RequeueAfter). The validation call is `:353` and the setPhase `:354`.
- `ctl/gameserver_template_revision.go:221-225` (rollout admission on the "op.Token != pin" branch, inside `if held && op.Kind == "Rollout"`). Spec says `:216`: STALE.
- `ctl/gameserver_template_revision.go:287-291` (admit branch). Spec says `:278`: STALE.
- Both call `validateRestrictedTemplate(target)` where `target` is a `GameTemplate`. With overrides, the check must use the resolved desired values for this GameServer, so the template-only call is no longer enough (T032). At `:222` and `:288` the GameServer is `gs` (available), so `resolveGameSecurity(target, gs)` is possible there.

### 4.2 Workload security helpers (`ctl/workload_security.go`)

- `:15-21` `validateRestrictedTemplate(tmpl)`: current check is on `tmpl.Spec.Security.RunAsUser > 0`, `FSGroup > 0`, and `RunAsGroup` > 0 when set. Message at `:18`: `restricted workload requires template security.runAsUser and security.fsGroup greater than zero, and a positive security.runAsGroup when specified; use an image compatible with that identity`. Spec: "`workload_security.go:15, 23`": CORRECT.
- `:23-34` `restrictedContainerSecurity(tmpl)`: reads `tmpl.Spec.Security.RunAsUser`, `RunAsGroup` (fallback `FSGroup`). Needs the resolved values (T032). CORRECT line.
- `:39-67` `applyWorkloadSecurity(pod, tmpl, restricted, expirationSeconds)`: `restricted` branch `:60-66`. Line `:61` `pod.SecurityContext.RunAsNonRoot = ptrTo(true)` dereferences `pod.SecurityContext`; it is non-nil today only because `validateRestrictedTemplate` forces template `fsGroup > 0`, which is what gives `gamePodSecurityContext` a non-nil return. See D8.
- `:63-65` applies `restrictedContainerSecurity(tmpl)` to EVERY init container (not just config-init by name). See D9.
- Intended (T032, OD-5 settled A): the three functions take the resolved (desired) values, not `tmpl.Spec.Security`. Validation of the effective values uses the same rule. A violation fails the server through `setPhase` with the existing message until the override is fixed; a running pod is untouched (contract "Restricted workload mode").

### 4.3 Rollout pin semantics (T044)

- Pin: `gs.Annotations[effectiveTemplateAnnotation]` (`ctl/gameserver_template_revision.go` `:~110-205`; `reconcileTemplateRevision` at `:~100`). Installed catalog vs pinned revision: `installed` GET at `:~188-189`.
- Rollout policy default: `gameserver_template_revision.go:129` sets `TemplateRollout{Policy: "OnRequest"}` only on the held Rollout-restore branch. The admit computation is `:267`: `admit := pin == "" || migrateBaseline || policy == nil || policy.Policy == "" || policy.Policy == "Automatic" || policy.RequestedRevision == revision`. So `OnRequest` admits only when `RequestedRevision == revision` (API-requested), `Automatic` admits always.
- Admission: `claimLifecycleOperation` with `Kind: "Rollout"` (`:~305-309`). During the Rollout op, `desiredReplicas` routes through `softStop` (`ctl/gameserver_controller.go:1331-1340`, `held && op.Kind == "Rollout" ... op.Token != live.Annotations[effectiveTemplateAnnotation]`).
- The effective pin is updated at `:~228-229` and `:~295-297`: `gs.Annotations[effectiveTemplateAnnotation] = op.Token` followed by `r.Update(ctx, gs)`.

**T044 premise is partly false (evidence).** The task says "confirm both `OnRequest` and `Automatic` rollouts end in a start that refreshes the template" (plan gap 3, `plan.md:86-104`). Code:
- For a running, awake server, a rollout drains (softStop) and then restarts: `desiredReplicas` after the op returns 1 (`:1423` `stopping := gs.Spec.Suspend || restart == restartDraining || idle == idleAsleep`, `!stopping` returns 1).
- For a **suspended** server (`gs.Spec.Suspend == true`) or an **idle-asleep** one, `stopping` is true, so the rollout ends at zero replicas (`:1421-1428`). No start occurs. The template refresh (and `applied`) waits until the next wake or resume. The status writer must show `pending=true` in that window (not a bug, but T044's claim "ends in a start" must not be written as a fact).
- For `OnRequest` without a `RequestedRevision` match, `admit=false` (`:267-268`): no rollout, no start. Pending is still recomputed from the pinned template.

### 4.4 Status writer and pin change (T044)

- `reconcileStatus` is called with the effective (pinned) template at `ctl/gameserver_controller.go:562` (`r.reconcileStatus(ctx, &gs, idle, idleStatus, idleScheduleErr, tunnelPlan, &tmpl, svcEvents, conflictingServer, plan.hostportCondition)`). `desired` must be computed from `&tmpl` (effective pin), not from `installed`. T044 recompute is then automatic each reconcile. The writer does not touch the pod (T044).

---

## 5. Backup, restore and failure paths (T043 context, T039 expectations)

Not in my T-list but affected by the operator resolver signatures:

- `ctl/backup_controller.go:1058` `SecurityContext: buildBackupPodSecurityContext(tmpl),` (spec `:1035`: stale). Definition `:1156` (spec `:1133`: stale). Reads FS group directly: `:1160-1161` `if tmpl.Spec.Security != nil && tmpl.Spec.Security.FSGroup != nil { fsGroup = *tmpl.Spec.Security.FSGroup }`.
- `ctl/restore_controller.go:433` `SecurityContext: buildBackupPodSecurityContext(tmpl),` (spec `:425`: stale).

### 5.1 `reconcileStatus` (gameserver_status.go) and MergeFrom

- `ctl/gameserver_status.go:42` `func (r *GameServerReconciler) reconcileStatus(` with variadic `hostportConditions ...*metav1.Condition` (`:46`), a precedent for passing a new condition.
- `:47-54` comment block explaining MergeFrom; `:55` `base := gs.DeepCopy()`; `:56` `orig := gs.Status.DeepCopy()`.
- `:316` `if err := r.Status().Patch(ctx, gs, client.MergeFrom(base)); err != nil { return 0, err }`, guarded by `if !reflect.DeepEqual(orig, &gs.Status)` at `:315`.
- Discrepancy with T021's wording: the patch is a MergeFrom of the WHOLE status (diff of `gs` vs `base`), not "that field only". It leaves `status.agent` alone because `gs.Status.Agent` is not changed in this reconcile (the agent's heartbeat is a separate writer). Keep it that way: do not write `gs.Status.Agent` from the reconciler.
- Conditions: `upsertCondition` (`ctl/helpers.go:18`) sets `LastTransitionTime` only on status change; `removeCondition` (`ctl/gameserver_status.go:1287`). The hostport precedent: `applyHostportCondition` at `:330-335` and call at `:198-203`.
- For `SecurityAllowed` use the same shape: compute before `reconcileStatus` (the root-override check needs the settings ConfigMap, a read), pass as a new variadic param or compute inside, then `upsertCondition`/`removeCondition` in the same block as `:198-203`. Adding a NON-variadic positional parameter breaks 7 test callers (D7).
- Test callers of `reconcileStatus` (all positional): `gameserver_capture_status_test.go:64`, `gameserver_envtest_test.go:385`, `gameserver_idle_condition_test.go:74,83`, `gameserver_status_test.go:1461,1476,1519,1537`.

### 5.2 Failure path

- `setPhase` at `ctl/gameserver_controller.go:2783`. Used for the restricted-workload failure (`:354`). `reconcileTemplateRevision` error goes to `setPhase` at `:331`.

### 5.3 Preserved-baseline and held early returns (affects T020, T039, T044)

- `ctl/gameserver_template_revision.go:416` `preserveBaselinePodTemplate(ctx, gs, ss)`. First line checks pin, baseline markers and `ss.Spec.Template.Annotations[templateRevisionAnnotation] != ""`. Returns `true` (preserve the whole template) when `r.GameWorkloadPolicy != nil` (`:424-427`) or when `baselineRenderInputs` matches `gs.Annotations[baselineInputsAnnotation]`.
- `ctl/gameserver_template_revision.go:337-357` `baselineRenderInputs(gs, revision)`: `spec := gs.Spec.DeepCopy()` (`:338`), then `spec.StopGracePeriodSeconds, spec.TemplateRollout, spec.BackupPolicy, spec.Idle, spec.Capture = nil, nil, nil, nil, nil` (`:340`). The whole spec is hashed (`:343-352`).

**D4 (must address).** `GameServerSpec.Security` is NOT in the exclusion list at `:340`. A baseline-pinned server whose user saves an override changes its `baselineRenderInputs` hash, so `preserveBaselinePodTemplate` returns `false` (`:431-433` path) and `reconcileStatefulSet` rewrites the pod template, which restarts the server. This violates FR-008 / R5. Fix: add `spec.Security = nil` to the list at `:340` (the list already excludes `Capture`, a per-server opt-in, with the same rationale in the comment at `:333-335`). Expected test: T039 (save override on a baseline-pinned server, pod template unchanged).

Also: when `preserveBaseline` is true the mutate function returns at `:1803-1805` BEFORE any security write. A preserved StatefulSet therefore never gets the `security-applied` annotation or the security-derived template parts until it is refreshed. Document this in T018 (the observed-annotation reader then falls back to the pre-feature rule for that StatefulSet).

---

## 6. Test files to extend and envtest helpers

### 6.1 Unit tests (no build tag)

- `ctl/build_game_container_test.go` (package `controller`):
  - `TestBuildGameContainer_SecurityContext_Unset` `:72-88`: asserts `c.SecurityContext == nil` with no security block.
  - `TestBuildGameContainer_SecurityContext_RunAsUserGroup` `:90-123`: asserts only RunAsUser/RunAsGroup set on the game container (no ReadOnly/Capabilities/Seccomp).
  - `TestGamePodSecurityContext` `:167-` (asserts nil when unset; FSGroup otherwise). Lines `:170`, `:179` call `gamePodSecurityContext(tmpl)`.
  - Five `buildGameContainer(gs, tmpl, image, nil, &materializedConfig{})` calls at `:36, :61, :79, :103, :138` (5-arg signature at `ctl/gameserver_controller.go:2397`).
- `ctl/workload_security_test.go`: `TestRestrictedHostportFailsBeforeWorkloadCreation` `:17`, `TestRestrictedTemplateIdentity` `:66` (calls `validateRestrictedTemplate(tmpl)` at `:82`), `TestWorkloadTokenOnlyMountedInAgent` `:89` (calls `gamePodSecurityContext(tmpl)` `:93` and `applyWorkloadSecurity(&pod, tmpl, true, 3600)` `:94`). T016 extends this file.
- `ctl/gameserver_security_test.go`: ALREADY EXISTS (`:1-` package `controller`, no build tag). It holds `TestValidateServerEnvSecrets` (`:16`) and `TestReconcileTunnel_CredentialsSecretOwnership` (`:333`). This is D1: T015 says "Table test `operator/internal/controller/gameserver_security_test.go`". Creating that path would collide. Use a new name, for example `ctl/gameserver_security_resolve_test.go`, and update T015, or get sign-off to append. Do not overwrite the existing file.
- `ctl/gameserver_files_test.go` calls `buildConfigInitContainer` at `:16, :48, :61, :77, :143, :181, :194`; `ctl/auxiliary_resources_test.go:69` also calls it.
- `ctl/gameserver_template_revision_test.go` calls `applyWorkloadSecurity(&ss.Spec.Template.Spec, ...)` at `:624` and `:749`.

### 6.2 Envtest (build tag `envtest`)

- `ctl/suite_envtest_test.go`: `//go:build envtest` (line 1). `TestMain` at `:37`. CRD dirs: `operator/config/crd` and `test/crds/snapshot` (`:44-48`), `ErrorIfCRDPathMissing: true`. Shared globals `cfg`, `k8sClient`, `scheme` (`:27-31`). Envtest has no kube-controller-manager: no StatefulSet or Pod controllers (comment `:14-15`).
- `ctl/helpers_envtest_test.go` (`//go:build envtest`, line 1):
  - `withGameServerReconciler(t, ns)` `:92-104`: builds `GameServerReconciler{Client, APIReader, Scheme, AgentImage, AgentCASecretName, AgentCASecretNamespace}`. Does NOT set `RestrictedWorkloads`, `ConfigInitImage`, or a control-plane namespace. T060/T061 need a settings-namespace field; set it here (test helper change; needs sign-off per CLAUDE.md rule 1 if it changes existing behavior; additive only).
  - `startMgr(t, ns, setups...)` `:160`: manager cache `DefaultNamespaces: {ns: {}}` (`:169-172`). ConfigMaps in another namespace (the control-plane namespace) are NOT in the cache in envtest. The settings ConfigMap read must use `APIReader` or the test must place it in `ns`. This is D10.
  - `newNamespace(t)` `:208`, `eventually(t, cond)` `:241`, `eventuallyWith` `:246`, `consistently(t, window, cond)` `:267`, `buildGameServer(ns, name, tmplName)` `:318`, `buildGameTemplate(name)` `:330`, `getGameServer(t, ns, name)` `:763`, `getTemplateByName` `:754`, `getJob` `:774`, `markGameServerPhase` `:618`, `seedGameServer` `:863`, `uniqueName(prefix)` `:412`, `deleteCleanup` `:421`.
- `ctl/crd_validation_envtest_test.go`: `//go:build envtest` (line 1). Pattern for CEL/schema tests: `validGameServer(ns, name)` `:25-32`; `TestCRDValidation_GameServerEmptyTemplateRef` `:34-42` does `k8sClient.Create` and `apierrors.IsInvalid(err)`. CEL tests for `spec.security` follow this pattern (T0xx, not in my list, but the CRD is needed).
- New envtest file: `ctl/gameserver_security_envtest_test.go` (does not exist; create it with `//go:build envtest`). T017 and T039 target it.

---

## 7. Per-task brief

### T005 (template types)
- Edit `operator/api/v1alpha1/gametemplate_types.go`: `GameSecuritySpec` at `:194`; add fields per section 1.1. Doc comments are required by the repo's CRD doc generation style (copy the `FSGroup` comment style, `:208-212`).
- Justification: FR-001, FR-002a, FR-006a, FR-013; contract `crd-gametemplate-security.md`; research R3, R8, R14; data-model `:16-45`.

### T006 (GameServer types)
- Edit `operator/api/v1alpha1/gameserver_types.go`: insert `Security` after `:161`; condition const at `:22-38` block; status field next to `:558`/`:611`. Justification: FR-002, FR-002a, FR-003, FR-004, FR-005, FR-009; contract `crd-gameserver-security.md`; data-model "Server security override" `:46-100`, "Desired and applied" `:100-146`, "Start refusal" `:147-166`.

### T007 (generated)
- Commit list in section 1.3. Justification: CLAUDE.md rule 7.

### T015 (table test, pure resolver)
- New test file name (see D1). Cases: every id source (module default, image default with neither set, override `value`, override `useImageDefault`); ownership check override > template > `BuiltInDefault`; `fsGroupChangePolicy` mapping `EveryStart`→`Always`, `OnlyWhenWrong`→`OnRootMismatch`, unset when no fsGroup applies (FR-002a, R8); annotation encode/decode round trip for `gameplane.local/security-applied`; pre-feature reader (no annotation: non-empty values `ModuleDefault`, empty ids `ImageDefault`, ownership check `EveryStart`/`BuiltInDefault`, data-model `:144`); `pending` (source-only change is not pending, contract "status" table).

### T016 (byte-identical unset; helpers keep fixed users)
- Extend `build_game_container_test.go` and `workload_security_test.go` (section 6.1). Assert with nothing applied: `SecurityContext == nil` on game container and `Spec.Template.Spec.SecurityContext == nil` (from `gamePodSecurityContext` returning nil). Helper containers keep fixed users: agent (`buildAgentContainer`, uid 65532, `ctl/gameserver_controller.go:2621`, its SecurityContext at `:2730`), config-init (`:2336-2360`: only `RunAsGroup`, and only with fsGroup), sentinel (`ctl/gameserver_sentinel.go`), tunnel and wipe: none derive from `desired`/`applied` (FR-012).
- CAUTION (D9): in restricted mode `applyWorkloadSecurity` (`workload_security.go:63-65`) gives config-init the game identity. The test must state that, not assert "fixed user" for config-init in restricted mode.

### T017 (envtest: defaults, pre-feature StatefulSet not recreated)
- New `ctl/gameserver_security_envtest_test.go` (`//go:build envtest`). Use `startMgr`, `withGameServerReconciler`, `newNamespace`, `buildGameTemplate`, `buildGameServer`, `getGameServer`. Envtest has no StatefulSet controller, so "not recreated" is asserted by reading the StatefulSet's `ResourceVersion`/`UID` and `Spec.Template` before and after reconcile (pod template untouched, `pending=true`, `applied.volumeOwnershipCheck=EveryStart`, `desired=OnlyWhenWrong`). Then scale to zero and back (set `spec.suspend` or fake replicas via `Status`), pod template gets `fsGroupChangePolicy: OnRootMismatch` and `pending=false`.
- A pre-feature StatefulSet can be seeded by creating it without the new annotations with `k8sClient.Create` before the reconciler runs.
- Interaction: `reconcileStatefulSet` calls `validateServerEnvSources` (`:1625`) and `validateTemplateEnvSources` (`:1630`) and `r.wipeReader()`; the envtest reconciler must have what these need (existing helpers already set this for other envtest files).

### T018 (new `gameserver_security.go`)
- Create file (section 2). Contents: `resolveGameSecurity`, pod encoding helpers (`fsGroupChangePolicy` mapping, `runAsUser`/`runAsGroup` pass-through), annotation codec for `gameplane.local/security-applied` (compact JSON of resolved value+source), `gameplane.local/security-applied-observed` reader and writer, applied-values reader (observed annotation on the StatefulSet; copied from pod-template annotation once `ownedTemplatePod` (`gameserver_template_revision.go:498`) returns a pod with `PodReadyToStartContainers=True` or any started container; pre-feature fallback to the running pod, else the StatefulSet template; R6). Depends on T006, T007.
- The observed-annotation copy happens in the reconcile loop AFTER the pod exists, so it must be done in a separate step (not inside the mutate function, which runs before the pod is started). Decide placement in T018's brief with the parent; the existing `ownedTemplatePod` call sites show the pattern (`gameserver_template_revision.go:517`, `:584`).

### T019 (signature change)
- `gameContainerSecurityContext` `ctl/gameserver_controller.go:2496`; `gamePodSecurityContext` `:2612`; `buildConfigInitContainer` `:2336`. Callers: `buildGameContainer` `:2482` (its own signature `:2397` must carry the resolved security, or the resolved values are passed through a new param; this changes 5 test calls, D6); `gamePodSecurityContext` call at `:1935`; `buildConfigInitContainer` call at `:1895`.
- Keep `nil` when nothing is set (byte-identical, R8, FR-002a).
- Depends on T018.

### T020 (mutate gate)
- Section 3. Gate placement: read prior `ss.Spec.Replicas` before `:1797`; refresh when `ss.ResourceVersion == ""` or prior replicas 0 and `actualReplicas > 0`; otherwise copy security-derived parts forward (game container SC, pod SC, config-init, `security-applied` annotation). Note the early returns `:1798-1805` (D4 and section 5.3).
- Justification: R5, R8, FR-001, FR-008, SC-001.

### T021 (status.security)
- In `reconcileStatus` (`ctl/gameserver_status.go:42`), compute `desired` from `resolveGameSecurity(tmpl, gs)` where `tmpl` is the effective template (`:562` call). `applied` from the observed annotation (T018); omitted when no start has happened (`applied` absent when the annotation is absent and no pod has started). `pending` per contract. Write through the existing `base`/`orig` patch (`:55-56`, `:315-316`); no separate patch.
- D11: the patch is the whole status diff. Do not write `gs.Status.Agent`.

### T031 (override layer)
- Extend `resolveGameSecurity` (section 2.2). Justification: R4, FR-003, FR-009.

### T032 (restricted validation on effective values)
- `workload_security.go:15, 23, 39` take resolved values. Call sites: `ctl/gameserver_controller.go:353` (Reconcile), `ctl/gameserver_template_revision.go:222` and `:288` (admission). Violation: `setPhase` with the existing message (`workload_security.go:18`), server stays failed until fixed, running pod untouched (contract "Restricted workload mode", OD-5 settled A).
- D8: the validator must enforce effective fsGroup > 0 (else `applyWorkloadSecurity`'s `pod.SecurityContext` deref at `:61` can nil-panic).
- Spec `:334` / `:216` / `:278` citations: stale (see section 4.1).

### T039 (envtest: override saves, pending, rollouts)
- Extend `ctl/gameserver_security_envtest_test.go` (created by T017). Cases: changed override does not change pod template generation or the pod (`pending=true`, `applied` unchanged). An unrelated env change (`spec.env`) rolls the pod (template changes) but must not pick up the pending security change: i.e. `security` parts copied forward (section 3.1 copy-forward applies when not refreshing; a plain env change does not reset replicas from zero, so `refresh` is false). Scale to zero and up refreshes and clears `pending`. Edit back to the applied value clears `pending` (source-only change not pending). Removal returns `ModuleDefault`. Template rollout keeps overridden setting and others follow the new default (US3 scenario 3). Never-started server: `applied` absent.
- D4 must be fixed (`baselineRenderInputs` exclusion) for the baseline-pinned case.

### T044 (status recompute on pin change, rollout ends in start)
- Recompute is automatic (section 4.4): `pending` is derived each reconcile from the effective template. Writer does not touch the pod.
- Premise correction (section 4.3): a rollout ends in a start only for a running awake server; suspended or idle-asleep servers end at zero. Tests must not assert a start for those. This is a spec-accuracy point for the parent; OD-5/US3 unaffected.

---

## 8. Concrete line index (quick reference)

| Symbol | File:line (current) | Spec cites |
|---|---|---|
| `GameSecuritySpec` type | `operator/api/v1alpha1/gametemplate_types.go:194` (field `:186`) | `:185` (stale) |
| `GameServerSpec.Capture` | `operator/api/v1alpha1/gameserver_types.go:161` | `:161` OK |
| `GameServerStatus` | `operator/api/v1alpha1/gameserver_types.go:558` (Conditions `:574`) | `:558` OK |
| condition const block | `operator/api/v1alpha1/gameserver_types.go:25-38` | n/a |
| `reconcileStatefulSet` | `operator/internal/controller/gameserver_controller.go:1613` (CreateOrUpdate `:1637`) | `:1611` stale |
| replicas assign | `ctl/gameserver_controller.go:1797` | n/a |
| preserveBaseline early return | `ctl/gameserver_controller.go:1803-1805` | n/a |
| containers rebuild | `ctl/gameserver_controller.go:1833-1834` | n/a |
| config-init assign | `ctl/gameserver_controller.go:1895` | n/a |
| hostport gate | `ctl/gameserver_controller.go:1920-1930` | `:1889-1905` stale |
| pod SC assign | `ctl/gameserver_controller.go:1935` | n/a |
| applyWorkloadSecurity call | `ctl/gameserver_controller.go:1944` | n/a |
| `buildConfigInitContainer` | `ctl/gameserver_controller.go:2336` (fsGroup block `:2345-2352`) | `:2310` stale |
| `buildGameContainer` | `ctl/gameserver_controller.go:2397` (SC call `:2482`) | n/a |
| `gameContainerSecurityContext` | `ctl/gameserver_controller.go:2496` | `:2470` stale |
| `gamePodSecurityContext` | `ctl/gameserver_controller.go:2612` | `:2586` stale |
| `buildAgentContainer` | `ctl/gameserver_controller.go:2621` | n/a |
| Reconcile restricted check | `ctl/gameserver_controller.go:353` (setPhase `:354`) | `:334` stale |
| reconcileStatus call | `ctl/gameserver_controller.go:562` | n/a |
| SetupWithManager | `ctl/gameserver_controller.go:594` (Owns ConfigMap `:605`, Watches Pod `:609`, Template `:610`) | n/a |
| template rollout restricted check (admit A) | `ctl/gameserver_template_revision.go:221-225` | `:216` stale |
| template rollout restricted check (admit B) | `ctl/gameserver_template_revision.go:287-291` | `:278` stale |
| admit computation | `ctl/gameserver_template_revision.go:267` | n/a |
| `baselineRenderInputs` exclusion | `ctl/gameserver_template_revision.go:338-340` | n/a |
| `preserveBaselinePodTemplate` | `ctl/gameserver_template_revision.go:416` | n/a |
| `ownedTemplatePod` | `ctl/gameserver_template_revision.go:498` | `:480` stale |
| `observedTemplateRevision` | `ctl/gameserver_template_revision.go:516` | n/a |
| `validateRestrictedTemplate` | `ctl/workload_security.go:15` | `:15` OK |
| `restrictedContainerSecurity` | `ctl/workload_security.go:23` | `:23` OK |
| `applyWorkloadSecurity` | `ctl/workload_security.go:39` | `:39` OK |
| `reconcileStatus` | `ctl/gameserver_status.go:42` (MergeFrom base `:55`, patch `:316`) | `:42-60` OK |
| `buildBackupPodSecurityContext` | `operator/internal/controller/backup_controller.go:1156` (caller `:1058`) | `:1133` / `:1035` stale |
| restore call | `ctl/restore_controller.go:433` | `:425` stale |
| `ownedTemplatePod` pod name | `gs.Name + "-0"` (`gameserver_template_revision.go:506`) | n/a |
| control-plane namespace flag | `operator/cmd/main.go:277` (default from `POD_NAMESPACE`, else `gameplane-system`) | n/a |
| secret-only cache scoping | `operator/internal/secretscope/cache.go:18-27` (scopes `corev1.Secret` only) | n/a |

---

## 9. Discrepancies and environment checks

**D1. Test file name collision (T015).** `operator/internal/controller/gameserver_security_test.go` already exists with unrelated tests (`TestValidateServerEnvSecrets` `:16`, `TestReconcileTunnel_CredentialsSecretOwnership` `:333`). T015 must use another file name or get sign-off to append. Evidence: `ls` of the controller directory.

**D2. Stale citations.** Full list in section 8 ("Spec cites" column). Most significant: T019 `:2470/:2586/:2310`, T020 `:1611`, T020/T021 `:1889-1905`, T032 `:334`, `:216`, `:278`, T043 `:1133/:1035/:425` (backup/restore), `ownedTemplatePod :480`, T005 `:185`.

**D3. Gate placement (T020).** Spec says "gate near :1889-1905". The real gate pattern R5 cites is the hostport gate at `:1920-1930`. The zero-to-one decision must read `ss.Spec.Replicas` before `:1797` overwrites it.

**D4. `baselineRenderInputs` would restart a baseline-pinned server on save (T039, FR-008).** `ctl/gameserver_template_revision.go:338-340` hashes the whole `GameServerSpec` except a fixed exclusion list. Adding `GameServerSpec.Security` without excluding it changes the hash, so `preserveBaselinePodTemplate` (`:416`) returns false and the template rewrites. Fix: add `spec.Security = nil` to the list at `:340` (precedent: `Capture`, same comment rationale at `:333-335`). Also: when `preserveBaseline` is true, the mutate function returns at `:1803` before any security write (section 5.3). Document it in T018.

**D5. T044 premise (rollout always ends in a start) is false for suspended or idle servers.** Evidence: `ctl/gameserver_controller.go:1421-1428` (`stopping := gs.Spec.Suspend || restart == restartDraining || idle == idleAsleep` → `softStop`, replicas 0). Plan gap 3 (`plan.md:86-104`) assumes a start. Correct wording: a refresh waits for the next start; `pending=true` in the meantime.

**D6. Signature churn breaks test compile (needs sign-off, CLAUDE.md rule 1).** Callers that change if signatures change:
- `buildConfigInitContainer`: `ctl/auxiliary_resources_test.go:69`; `ctl/gameserver_files_test.go:16, 48, 61, 77, 143, 181, 194`; prod `:1895`.
- `buildGameContainer` (5-arg): `ctl/build_game_container_test.go:36, 61, 79, 103, 138`; prod `:1834`.
- `gamePodSecurityContext`: `ctl/build_game_container_test.go:170, 179`; `ctl/workload_security_test.go:93`; prod `:1935`.
- `applyWorkloadSecurity`: `ctl/gameserver_template_revision_test.go:624, 749`; `ctl/workload_security_test.go:94`; prod `:1944`.
- `validateRestrictedTemplate`: `ctl/workload_security_test.go:82`; prod `ctl/gameserver_controller.go:353`, `ctl/gameserver_template_revision.go:222, 288`.
- `restrictedContainerSecurity`: only `ctl/workload_security.go` (`:57`, `:64`).
Recommendation for the parent: keep the existing functions' names; the change is to the argument type. Each test edit is a test change and needs human sign-off.

**D7. `reconcileStatus` positional callers.** Seven test call sites pass positional args (section 5.1). A new non-variadic parameter breaks them; use a variadic like `hostportConditions` (`gameserver_status.go:46`).

**D8. Restricted-mode nil dereference dependency.** `ctl/workload_security.go:61` `pod.SecurityContext.RunAsNonRoot = ptrTo(true)` assumes `pod.SecurityContext` non-nil. Today it is non-nil only because `validateRestrictedTemplate` requires template `fsGroup > 0`. With overrides, validation of effective values (T032) must still require effective `fsGroup > 0`, or a restricted server with `fsGroup` `useImageDefault` panics.

**D9. Restricted mode gives config-init the game identity.** `ctl/workload_security.go:63-65` applies `restrictedContainerSecurity` to every `pod.InitContainers` entry. Non-restricted config-init only gets `RunAsGroup` (`:2347`). T016's "helper containers keep fixed users" must cover this: in restricted mode config-init runs as the game uid (existing behavior).

**D10. Envtest cache scope and namespace (T060/T061, T039).** `ctl/helpers_envtest_test.go:169-172` restricts the manager cache to the test namespace. Production caches ConfigMaps cluster-wide (only Secrets are scoped, `operator/internal/secretscope/cache.go:18-27`). The settings ConfigMap lives in the control-plane namespace (`operator/cmd/main.go:277`). `GameServerReconciler` has no control-plane namespace field (grep: none in `ctl/`). The field must be injectable; the envtest helper must set it to the test namespace or read via `APIReader`. `Owns(&corev1.ConfigMap{})` (`ctl/gameserver_controller.go:605`) does not enqueue for an unowned ConfigMap, so R9's `Watches(&corev1.ConfigMap{}, ...)` entry is required (`:609-611` shows the existing `Watches` pattern and `mapTemplateToGameServers` at `:617`).

**D11. MergeFrom is a whole-status diff (T021 wording).** `ctl/gameserver_status.go:55-56` and `:315-316`: `client.MergeFrom(base)` diffs the entire `gs` status, not only `status.security`. `status.agent` is untouched only because the reconciler does not change it. The wording "that field only" in T021 is not how the code works; the requirement (do not clobber the heartbeat) holds.

**D12. Condition reasons are literal strings (T006).** No reason constants in the package; literals like `"JobFailed"` (`ctl/gameserver_wipe.go:341`). The const block holds only condition TYPE names (`gameserver_types.go:22-38`).

**D13. i18n for the new condition reason.** `gameserver_types.go:576-580` says the UI resolves condition messages by id `condition.<type>.<reason>` (lower-cased). `SecurityAllowed` / `RootOverrideDisallowed` needs a translation entry in the web/i18n packs (out of this brief's scope; flag to the web owner). Evidence of the consumer: `web/src/lib/i18n/conditionMessage.test.ts`.

**D17. Held branches before the gate.** Verified: the held-operation branches that assign `ss.Spec.Replicas` (`:1726-1733`, `:1789`) all `return nil` before `:1797`, so reading `ss.Spec.Replicas` immediately before `:1797` still yields the live value. Replica count and gate are safe.

**D14. k8s.io/api version and `PodReadyToStartContainers` (unverified).** `operator/go.mod:22` `k8s.io/api v0.37.2`, `:23` `k8s.io/apimachinery v0.37.2`, `:27` `sigs.k8s.io/controller-runtime v0.25.2`. The module source is NOT in the local cache (`/root/go/pkg/mod` holds only `cache/` and `golang.org`), so I could not confirm `corev1.PodReadyToStartContainers` or `corev1.FSGroupChangeAlways` / `corev1.FSGroupChangeOnRootMismatch` by reading source. The constant has been in k8s.io/api core/v1 since v0.29 (PodReadyToStartContainers condition, k8s 1.29). Treat it as expected, not verified; the T018 agent must confirm with `go doc k8s.io/api/core/v1 PodReadyToStartContainers` or by compiling (CLAUDE.md rule 8: compile checks only, which the parent decides).

**D15. `reconcileStatefulSet` early returns skip security writes (T020).** Held-operation returns (`:1798-1801`) and `preserveBaseline` (`:1803-1805`) happen before any template write. R5's copy-forward is therefore not reached on those paths; the observed annotation and `applied` stay unchanged there. This is the intended behavior for held operations, but T020/T039 must not assume the template is rebuilt on every reconcile.

**D16. Status writer must use the pinned template.** `reconcileStatus` receives `&tmpl` (effective pin) at `:562`. T021 and T044 must compute `desired` from that object, not from a fresh GET of the installed GameTemplate (which would show the new catalog default before the pin moves).

---

Checked and correct (no discrepancy): `workload_security.go:15, 23, 39`; `gameserver_status.go:42-60` MergeFrom pattern; `gameserver_types.go:161` (Capture) and `:558` (GameServerStatus); `gameserver_controller.go:1613`-ish function existence; `helpers_envtest_test.go` helper names used in T017/T039 exist (`startMgr`, `withGameServerReconciler`, `newNamespace`, `eventually`, `consistently`, `buildGameServer`, `buildGameTemplate`, `getGameServer`, `markGameServerPhase`, `seedGameServer`); `build_game_container_test.go` exists with the three security tests cited in section 6.1; `crd_validation_envtest_test.go` exists with `validGameServer` at `:25`.

Not read (out of scope or not verified): `docs/agent-architecture.md`, `gameserver_wipe.go` beyond condition lines, `gameserver_sentinel.go` and tunnel/wipe security contexts (only FR-012 claim), `operator/config/crd` and `charts/` file list (not listed; run `ls` before T007).
