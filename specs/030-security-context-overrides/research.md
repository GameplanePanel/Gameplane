# Research: Module security defaults with a per-server override

**Feature**: [spec.md](spec.md) | **Plan**: [plan.md](plan.md) | **Date**: 2026-11-13

Each entry records a decision, why it was made, and what else was considered. Facts about current code cite `file:line` as of the `spec-030/plan-and-tasks` branch. Questions that need a product ruling are in [OPEN-DECISIONS.md](OPEN-DECISIONS.md) (OD-1 to OD-6, all settled by valgul, 2026-11-07 and 2026-11-13); each entry that a ruling shaped says which one.

## Baseline facts

- **Template side.** `GameTemplateSpec.Security *GameSecuritySpec` is at `operator/api/v1alpha1/gametemplate_types.go:177`; `GameSecuritySpec` (`RunAsUser`, `RunAsGroup`, `FSGroup`, each `*int64`, 0 to 4294967295) is at `:185-204`. About 20 module templates in the module repo set a `security:` block (`grep -l` over `/home/user/module/*/template.yaml`).
- **GameServer side.** `GameServerSpec` is `operator/api/v1alpha1/gameserver_types.go:41-162`; its last field is `Capture` at `:161`. `GameServerStatus` starts at `:558` and ends at `:621`. There is no security field on either.
- **How the pod is built.** `reconcileStatefulSet` (`operator/internal/controller/gameserver_controller.go:1587`) runs one `controllerutil.CreateOrUpdate` (`:1611`) that assigns the pod template wholesale on every pass:
  - game container: `buildGameContainer` (`:1808`, defined `:2371`), which ends with `c.SecurityContext = gameContainerSecurityContext(tmpl)` (`:2456`; function `:2470`, returns `nil` unless run-as user or group is set);
  - pod: `ss.Spec.Template.Spec.SecurityContext = gamePodSecurityContext(tmpl)` (`:1909`; function `:2586`, returns `nil` unless `FSGroup` is set);
  - config-init: `buildConfigInitContainer(r.ConfigInitImage, tmpl, ...)` (`:1869`; defined `:2310`). When the template has an `FSGroup` it sets `RunAsGroup` on that container and appends a `chgrp`/`chmod g+w` loop over the copied files (`:2319-2326`);
  - restricted mode: `applyWorkloadSecurity(...)` (`:1918`) replaces the game and init container contexts with `restrictedContainerSecurity(tmpl)` (`workload_security.go:23-37, 39-67`), and `validateRestrictedTemplate` (`workload_security.go:15`) is called at `gameserver_controller.go:334` and in `gameserver_template_revision.go:216, 278`.
- **Backup and restore.** `buildBackupPodSecurityContext(tmpl)` (`backup_controller.go:1133`) uses the template's `FSGroup`, else 65532. It is called from `backup_controller.go:1035` and `restore_controller.go:425`. Wipe, sentinel and tunnel pods use a fixed uid (`gameserver_wipe.go:185`, `gameserver_sentinel.go:321`, `gameserver_tunnel.go:281`) and are not touched.
- **Rollout.** The StatefulSet never sets `UpdateStrategy` in the normal path (only the hostport stop fence does, `gameserver_hostport_stop.go:106`), so it is the Kubernetes default, `RollingUpdate`. Any difference in the pod template therefore recreates the pod on the next reconcile. Template content changes are gated separately by the template rollout machinery (`gameserver_template_revision.go`, `GameServer.spec.templateRollout`), which pins an immutable snapshot of the template and drives a drained rollout. That machinery holds back *template* content, not GameServer spec fields.
- **A precedent for "change the pod template only when stopped".** `gameserver_controller.go:1889-1905` writes the hostport affinity and node annotation into the pod template only when `actualReplicas == 0 || ss.ResourceVersion == ""` (or when the pin is already present), with the comment "Do not change a live Pod's template merely because its node was recorded while a graceful stop is still saving the game."
- **A precedent for "applies on next restart".** `docs/module-authoring.md:694` and `:787` describe values that reach servers on their next restart (a changed upstream pin, resized memory limits); `operator/specs.md:739` records a pod-template change (the `captures` volume) that costs one rolling restart on upgrade. There is no existing GameServer-spec field that is *held back* until restart, so this feature introduces the rule; the hostport gate above is the code pattern it reuses.
- **API permissions.** The catalog is `api/internal/rbac/catalog.go:21-76` (`captures:manage` at `:73` is the closest precedent: a namespaced key for one feature). Routes map to a permission in the table in `api/internal/rbac/rbac.go`; `servers:write` is the catch-all for `/servers` (`:267`). Field-level permissions are enforced inside `validateAndProtectGameServer` (`api/internal/handlers/resources.go:538`), which already handles `spec.capture` (captures:manage), `spec.serviceAccountName` and `spec.image` (admin) and is called from create (`resources.go:218`), update (`:290`) and clone (`lifecycle.go:243`).
- **Settings.** Cluster settings are JSON blobs in the `config` table keyed by section name, validated by `newValidators` (`api/internal/handlers/config.go:462-470`), read by `GET /admin/config` and written by `PUT /admin/config/{section}`. The route needs `config:manage` through `rbac.go:223-228`. `config.go` already emits an extra synchronous audit row for a setting change (`:174-179`).
- **Audit.** Rows have `ts, actor, method, path, target, status, ip, reason` (`api/internal/audit/audit.go:820-830`). `reason` is a nullable column added by migration 007 and is part of the hash chain only when non-empty (`audit.go:318-340`). A handler writes its own row with `Auditor.WriteSync` and then calls `audit.MarkRecorded` so the middleware does not add a second one (`audit.go:594-611, 711-750`; used at `handlers/capture.go:1252-1258`).
- **Web.** `GameServer` and `GameTemplate` types are in `web/src/types.ts:557` and `:303`; neither has a security field. The wizard is `web/src/routes/CreateServer.tsx`, the settings form is `web/src/routes/tabs/Settings.tsx` (mounted from `ServerDetail.tsx:377`), API calls are in `web/src/lib/api.ts`, and the English strings are in `i18n/locales/en/app.json`.
- **Module repo.** `modules/.schema/gametemplate.schema.json` is generated from the CRD by `make module-schema` (`hack/gen-module-schema.py:21-22`), so the CRD change reaches the module repo's editor schema by regeneration. The module repo's `validate.py` has security rules (`validate.py:610-660, 903-958`), and `gp-module/internal/validator/` has none for the `security` block.
- **MCP server.** It lists and gets any Gameplane kind generically (`mcp-server/tools.go:64, 109, 135`) and applies no per-field filtering, so new spec and status fields are readable with no code change.

---

## R1. Where the override lives

**Decision**: a new optional `security` block on `GameServer.spec`, `GameServerSecuritySpec`, next to `capture` and `idle`. Four optional members: `runAsUser`, `runAsGroup`, `fsGroup` (each a small struct, see R2) and `volumeOwnershipCheck` (an enum).

**Rationale**:
- CLAUDE.md core rules 9 and 10: an existing CRD, reconciled by the operator, with the API as a UX gateway. A GameServer already carries per-server overrides of template values (`image`, `resources`, `probes`, `storage`), so this is the same shape.
- An override "owned by the server, survives module upgrades and reinstalls" (spec Key Entities) is exactly a field of the server's own object. It travels with the server in a backup of the manifest, in GitOps, and through a clone.
- The API stays a pass-through for the shape, validating and permission-checking it in `validateAndProtectGameServer` (FR-004).

**Alternatives considered**:
- *A separate `SecurityOverride` CRD.* One more CRD, RBAC, informer and lifecycle (orphaned objects, owner references) for four numbers.
- *Annotations on the GameServer.* No schema, no CEL validation, no typed client in the dashboard, and the API would have to parse strings. Existing annotations are operator-owned bookkeeping, not user input.
- *Putting it in `spec.env` or a generic config key.* Not a pod-spec input, and wrong layer.

## R2. Representing "not set / a number / use image default"

**Decision**: each of the three id settings is a struct, `SecurityIDOverride`, with `value *int64` (0 to 4294967295) and `useImageDefault bool`, with CEL rules that exactly one of the two is present. An absent member (the field itself omitted) means "use the module default".

```yaml
spec:
  security:
    runAsUser:  { value: 2000 }
    runAsGroup: { useImageDefault: true }
    # fsGroup omitted: module default
    volumeOwnershipCheck: EveryStart
```

**Rationale**: three states have to survive a JSON round trip through a dashboard that sends the whole spec back (`web/src/routes/tabs/Settings.tsx`) and through a JSON merge patch. `null` cannot carry the third state, because a merge patch treats `null` as "delete the field". A sentinel number (for example -1) would break the 0 to 4294967295 range rule and put a magic value in the CRD. A struct is explicit, CEL-checkable, and extensible.

**Alternatives considered**:
- *`*int64` plus a sibling `imageDefault: [runAsUser, runAsGroup]` list.* Two places that can disagree (a value and a list entry for the same setting); needs a cross-field rule anyway.
- *A string field (`"1000"`, `"image"`).* Validation moves from the schema into code, and the UI needs parsing.

`volumeOwnershipCheck` has only two states plus "not set", so it stays a plain enum, `EveryStart` or `OnlyWhenWrong` (FR-002a: its override is "either not set or one of the two values"); there is no "use image default" for it.

## R3. The template side: ownership-check default and the risky flag

**Decision**: extend `GameSecuritySpec` (`gametemplate_types.go:185`) with
- `volumeOwnershipCheck` (same enum; omitted means the built-in default `OnlyWhenWrong`), and
- `risky`, an optional object with one optional member per setting (`runAsUser`, `runAsGroup`, `fsGroup`, `volumeOwnershipCheck`), each `{ reason: string }` (max 200 characters). A member's presence marks that value risky to override; `reason` is optional.

```yaml
security:
  runAsUser: 25000
  fsGroup: 25000
  volumeOwnershipCheck: OnlyWhenWrong
  risky:
    runAsUser: { reason: "Image must start as root to drop privileges." }
```

**Rationale**: a presence-based map keeps the template compact, needs no list of names to keep in sync with the four settings, and matches FR-006a ("mark each of its security values as risky ... with an optional short reason", "Modules cannot lock a value"). The flag is advisory data for the dashboard; the operator ignores it. The module repo's JSON schema is regenerated from the CRD, so editors validate it for free (`make module-schema`). The reason is plain English supplied by the module author; localizing module text is the existing module-locales feature's concern, and FR-014 covers dashboard text only.

**Alternatives considered**:
- *`riskyToOverride: [runAsUser, fsGroup]` plus a parallel `riskReasons` map.* Two structures per value.
- *A boolean per setting.* No room for the reason without a second field per setting anyway.

## R4. How desired and applied are computed, and who computes them

**Decision**: a pure function in the operator, `resolveGameSecurity(tmpl, gs) -> GameSecurity`, returns for each setting a value and a source. Every consumer takes its result instead of reading `tmpl.Spec.Security`:
- `gameContainerSecurityContext` and `gamePodSecurityContext` (`:2470`, `:2586`) and `buildConfigInitContainer` (`:2310`), by signature change;
- `validateRestrictedTemplate` and `restrictedContainerSecurity` (`workload_security.go:15, 23`), on the effective values (OD-5, settled A: an effective value the rule refuses fails the server with the existing message until fixed; a running pod keeps running);
- `buildBackupPodSecurityContext` (`backup_controller.go:1133`), which receives the *applied* filesystem group (R6);
- the status writer (`gameserver_status.go`), which publishes `status.security`.

Resolution order per id setting (FR-003): override `useImageDefault` gives value none, source `ImageDefaultOverride`; override `value` gives that number, source `Override`; else the template's number, source `ModuleDefault`; else none, source `ImageDefault`. The ownership check resolves the same way with a final built-in default `OnlyWhenWrong`, source `BuiltInDefault` (see the spec gap noted in plan.md: FR-009's four sources have no label for a built-in default).

**Rationale**: FR-004 puts the computation in the reconciler only; a pure function with a table test is the cheapest way to cover all combinations, and one function removes the risk that the container, the config-init chgrp, the backup job and the status disagree.

**Alternatives considered**: *Mutate a copy of the template (overlay the override onto `tmpl.Spec.Security`) and leave the builders alone.* Smaller diff, but it loses the source of each value and cannot represent `useImageDefault` (a nil pointer already means "not set" there), so status and the "use image default" case could not be implemented honestly.

## R5. Applying a change on the next restart, without restarting

**Decision**: the security-derived parts of the pod template are written only when the workload is being created or started from zero replicas; while a pod is running they are copied forward unchanged from the StatefulSet that already exists. "Security-derived parts" means exactly: the pod `securityContext` (filesystem group, change policy), the game container `securityContext`, the config-init container (its `securityContext` and command, which embed the group) and one pod-template annotation, `gameplane.local/security-applied`, that records the resolved values and sources (R7).

The rule, inside the existing `CreateOrUpdate` mutate function in `reconcileStatefulSet` (`gameserver_controller.go:1611`), mirrors the hostport gate at `:1889-1905`:

```text
refresh := ss.ResourceVersion == ""                               // first create
        || (ss.Spec.Replicas is 0 and actualReplicas > 0)       // start from zero
if refresh { render from desired } else { reuse what ss already carries }
```

**Rationale**:
- `RollingUpdate` recreates the pod for any template diff, so the only way to honor OD-3 / FR-008 ("MUST NOT restart the server by itself") is to *not change the template* until a start. Gameplane's own restart is "an operator-owned scale-down, then scale-up" (`gameserver_controller.go:1283-1290`), suspend and wake go through zero replicas, and a template rollout stops and starts the server, so every real start passes through the refresh point and picks up the new values.
- The gate reuses an existing pattern in the same function instead of inventing a second mechanism (rule 9).
- It also makes the default flip (R8) safe: after an operator upgrade, running servers keep their old pod template until their next start, so SC-001 holds and the upgrade causes no fleet restart.
- One consequence to state plainly: an unrelated edit that rolls the pod while the server is running (for example an env change) does not pick up a pending security change either. The pod restarts with the applied values; the pending marker stays until a real stop and start. This is deterministic and visible in status, and it matches "applies on the next restart".

**Alternatives considered**:
- *Switch the StatefulSet to `OnDelete` for security changes.* The strategy is a StatefulSet-wide setting that other mechanisms save and restore (`gameserver_hostport_stop.go:34-160`); toggling it per field would collide with the hostport fence.
- *Keep the override only in the GameServer and render it at pod admission (a webhook).* There is no webhook in this stack.
- *Annotate-and-restart on save.* Rejected by OD-3.
- *A per-change "apply now" action.* Out of scope; a normal Restart already does it.

## R6. Applied filesystem group for backups, restores and the "applied" report

**Decision**: the applied values are the ones the data volume was last set up with, recorded by the reconciler on the StatefulSet object (not its pod template) in the annotation `gameplane.local/security-applied-observed`:
1. At a start (R5) the new values go into the pod template only. The observed annotation does not change yet.
2. The reconciler copies the pod template's `gameplane.local/security-applied` value into the observed annotation once an owned game pod built from that template has had its volumes set up. The kubelet applies the filesystem group and change policy during volume setup, before any init or game container starts, so the signal is the pod condition `PodReadyToStartContainers=True` or, on clusters without that condition, any init or game container status that has started. The pod lookup is the one the template revision code uses (`ownedTemplatePod`, `gameserver_template_revision.go:480`).
3. `status.security.applied`, `buildBackupPodSecurityContext` and the restore call site all read the observed annotation (FR-011), never the pod template and never the desired value.

So while a start is pending (template written, pod not yet past volume setup), backup and restore jobs keep using the previous applied filesystem group, and `status.security.applied` still shows the previous values with `pending=true`. A start that fails before volume setup (image pull error, unschedulable pod, `SecurityAllowed=False`) leaves the observed annotation, `applied` and `pending` unchanged. A start that fails after volume setup reports the new values as applied, which is true, because the volume was re-owned. A server that was never started has no observed annotation; backups of it use the desired value and `applied` is omitted.

The pod-template annotation `gameplane.local/security-applied` carries the sources (R7) and is what step 2 copies. A StatefulSet that predates this feature has neither annotation; the reader then reconstructs values from the running pod's spec, or from the StatefulSet pod template when no pod runs (it has not changed since the last start, because before this feature nothing gated it), and the reconciler writes the observed annotation from that on its first pass and labels every non-empty value `ModuleDefault` and every empty one `ImageDefault`, which is correct because overrides did not exist.

**Rationale**: the state lives on an operator-owned object, so it survives a status reset (the repo already treats status as a read model and keeps authority in operator-owned objects, see `gameserver_template_revision.go:1-60`). Using the *pending* group for a backup job would make the kubelet re-own the volume before the restart, which is exactly what FR-011 forbids.

**Alternatives considered**: *Store applied values in `status` and read them back.* A reset status (leader change, CRD re-apply) would lose the very value a backup needs. *Store them in a GameServer annotation.* Another writer of GameServer metadata and another reconcile trigger. *Read the StatefulSet pod template when no game container runs.* R5 writes the new template at scale-up, so during a pending start it would report, and hand backup jobs, a group the volume does not have yet (raised in review of PR #956).

## R7. How status reports desired, applied, sources and pending

**Decision**: a new `status.security` read model on `GameServerStatus`: `desired` and `applied` (each four settings with `value` and `source`), and `pending bool`. `pending` is true when `applied` exists and differs from `desired` in any value. `applied` is omitted while there is nothing to report (never started). It is patched with the same `MergeFrom` pattern as the rest of the status (`gameserver_status.go:42-60`), so the agent's concurrent `status.agent` heartbeat is untouched. Field list and sources are in [data-model.md](data-model.md) and [contracts/crd-gameserver-security.md](contracts/crd-gameserver-security.md).

**Rationale**: FR-009 and SC-004 need what the container runs with, where each value came from, and what is queued. Reading `applied` from the pod/StatefulSet (R6) and `desired` from the resolver (R4) makes SC-004 true by construction: the numbers shown are the ones in the pod spec.

**Alternatives considered**: *Conditions only* (a `SecurityPending` condition). It carries a message but not structured values, and the dashboard would parse text. A condition `SecurityApplied` can be added later for alerting; it is not needed for the spec.

## R8. The volume ownership check and the default flip

**Decision**: map to `PodSecurityContext.FSGroupChangePolicy` (a Kubernetes core field, values `Always` and `OnRootMismatch`): `EveryStart` is `Always`, `OnlyWhenWrong` is `OnRootMismatch`. The policy is written only when a filesystem group applies (it has no effect otherwise, FR-002a). When neither the template nor the server sets it, `OnlyWhenWrong` applies.

Effect on existing servers (FR-001, SC-001, SC-006): the policy appears in the pod template of servers that already have a filesystem group, which would normally recreate their pods on operator upgrade. R5 prevents that: a running server keeps its existing template (no policy, which Kubernetes treats as `Always`) until its next start, then switches. Status shows the server as `pending` with the ownership check `Always` applied and `OnlyWhenWrong` desired during that window. The user and group never change.

The config-init container already makes copied files group-writable itself (`gameserver_controller.go:2319-2326`) and does not depend on the kubelet's recursive pass, so the faster policy does not strand config files. The kubelet's `OnRootMismatch` check looks at the volume's top folder; if it already has the right group and permissions the recursive pass is skipped.

**Rationale**: it is the Kubernetes primitive for exactly this (rule 9), and SC-006 asks for no measurable ownership pass on an unchanged restart.

**Alternatives considered**: *A chown init container.* The existing design deliberately replaced that (`gametemplate_types.go:199-201`). *Leave the policy unset and document it.* Fails FR-002a.

**Residual risk**: `OnRootMismatch` does not repair files created with the wrong group inside an otherwise correct top folder (for example by an earlier run as another uid). The dashboard's `EveryStart` choice is the remedy; the data-model and quickstart both cover it. A per-feature note in `docs/module-authoring.md` (FR-013) explains when a module should set `EveryStart`.

## R9. The cluster-wide "allow root overrides" setting, and how the operator learns it

**Decision**: a new config section `security` with one boolean, `allowRootOverrides`, default false (absent row means false). It is added to `newValidators` (`config.go:462`), is read through `GET /admin/config`, and is written through `PUT /admin/config/security`, which the existing rule table already gates with `config:manage` (`rbac.go:223-228`). A change writes its own audit row (R11), following `config.go:174-179`.

**The operator must read it (OD-4, settled A).** FR-005a: while the setting is off, the reconciler must not start a server that holds a saved root override, and must say why. The operator has no access to the API's database and must not call the API (CLAUDE.md rule 10), so the setting reaches it as a Kubernetes object:

- The API owns one ConfigMap, `gameplane-security-settings`, in the gameplane (control-plane) namespace of each cluster, with the single key `allowRootOverrides` (`"true"` or `"false"`). The API is its only writer; the config table stays the source of truth.
- `PUT /admin/config/security` writes the ConfigMap first, then the config row, and fails the request if the ConfigMap write fails, so the two cannot drift silently. In a multi-cluster install the central API writes the ConfigMap into every registered cluster it can reach; a start-up sync in the API rewrites it from the config row, so a restored database, a newly registered cluster or a manually deleted ConfigMap converge.
- **Fail closed**: a missing ConfigMap, a missing key or any value other than `"true"` means the setting is off. The default install therefore refuses root overrides on start with no extra step.
- The operator reads it from its informer cache (it already has cluster-wide `get;list;watch` on ConfigMaps, `operator/internal/controller/gameserver_controller.go:258`; the cache cost is one more watched object) and adds a `Watches(&corev1.ConfigMap{})` entry next to the Pod and GameTemplate watches (`:583`), filtered to that name and namespace, with a map function that enqueues every GameServer holding a root override in any namespace (so a flip, in either direction, is picked up at once; unrelated servers are not enqueued).
- The API's chart Role gains `get;create;update;patch` on that one ConfigMap by `resourceNames` (`charts/gameplane/templates/api.yaml`); the operator needs no new rule.

**What the reconciler does with it** (FR-005a). A *root override* is a saved `spec.security` whose `runAsUser` or `runAsGroup` is `value: 0` or `useImageDefault: true`, or whose `fsGroup` is `value: 0`. While the setting is off and the server holds one:
- a server that is not running (zero replicas, so the next scale-up would be a start) is **not scaled up**: the zero-to-one transition in `reconcileStatefulSet` (the R5 refresh point) is held at zero and the server reports condition `SecurityAllowed` `False`, reason `RootOverrideDisallowed`, message naming the offending settings and how to clear them;
- a server that is already running is **never touched**: no scale-down, no pod-template change, no restart, and its phase is unchanged; the same condition is set to `False` with a message saying the server keeps running but will not start again until the override is cleared or the setting is on, so the dashboard can warn before the next restart;
- turning the setting off never restarts or stops any server, and turning it back on (or clearing the override) lets the next reconcile start the server with no other step. The module's own root defaults (a template that sets user 0) are not overrides and are not affected.

The check runs before the R5 refresh, so a refused start leaves the StatefulSet exactly as it was. The reconciler sets the condition from the same pure helper the tests exercise (`hasRootOverride(gs)` and `rootOverrideSettings(gs)` in `security_settings.go`).

The API still reads the setting from the config table per request inside the create and update path (one indexed `SELECT` on `config`) and refuses only a request that *sets or changes* a root value; an unchanged one passes through (so a whole-spec PUT from the dashboard still works) and removing one is always allowed. The reconciler's refusal is the second line, for overrides saved before the setting was turned off, and for writers that bypass the API (`kubectl`).

**Rationale**: the config store is the established home for admin switches, has validation, audit hooks, dashboard plumbing (`AdminSettings.tsx`) and RBAC for free, and the spec says an admin turns it on and off, audited (FR-005). A ConfigMap is the Kubernetes primitive for small cluster configuration the operator must watch (CLAUDE.md rule 9), is namespaced, RBAC-controlled and cached, and keeps the operator from learning the API's database or calling it (rule 10).

**Alternatives considered**:
- *The operator calls the API for the setting.* A runtime dependency in the wrong direction (operator to API), and it fails if the API is down exactly when a server must be started.
- *A Helm value or operator flag.* Needs a rollout to change and cannot be changed or audited as an admin action.
- *A field on a cluster-scoped CRD (a new `ClusterSecurityPolicy`).* A new CRD, schema, RBAC and generated copies for one boolean; a ConfigMap is enough.
- *Copying the flag onto each GameServer (`status` or an annotation) from the API.* Fans out writes to every server on each flip and adds another writer of GameServer objects.

## R10. Where each permission and validation check runs

**Decision**: new catalog entry `servers:security` (`Namespaced: true`), under the `servers` group in `api/internal/rbac/catalog.go:25-29`. It gets no route rule: like `spec.capture` it is a **field-level** check inside `validateAndProtectGameServer`. The built-in admin role holds `*`, so no migration or seed row is needed, and the other built-in roles do not hold it (OD-1); the catalog tests only require rule-table and seeded keys to be catalog keys (`rbac/catalog_test.go:68-119`), which a catalog-only key satisfies.

The new block in `validateAndProtectGameServer` (after the capture step), for `desired.spec.security` against `live`:
1. **Unchanged** (deep-equal to live, both absent counts as equal): accept without the permission, so a `servers:write` holder can still save other fields with the whole spec sent back.
2. **Absent in the request, present in live**, caller lacks `servers:security`: restore live's value into the request (the same move the capture and image steps make), so a client that drops the field cannot wipe it by accident.
3. **Changed** and the caller lacks `servers:security`: refuse (403, `server.security.forbidden`).
4. **Shape**: each id 0 to 4294967295, exactly one of `value`/`useImageDefault`, enum values valid: refuse (422, `server.security.invalid`). The CRD's CEL and bounds are the second line.
5. **Root rule** (FR-005, OD-2): a new or changed `runAsUser`/`runAsGroup` with value 0 or `useImageDefault`, or `fsGroup` with value 0, is refused (403, `server.security.root_disabled`) unless `allowRootOverrides` is true. Only new or changed values are checked at write time; an existing root override is checked by the reconciler on start (R9, OD-4 settled A).

Clone (`lifecycle.go:243`) reaches the same function with `live == nil`, so a clone of a server that has overrides is refused for a caller without `servers:security`, with the same message; a holder can clone. That is the intended reading of "no inheriting a privilege on a clone" already used for the capture node exception (`lifecycle.go:232-234`).

**Rationale**: matches the existing structure, needs no new route, and keeps one enforcement point for create, update and clone. The owner and collaborator fallback in `rbac.go:122-150` never grants a full-object PUT, so it cannot bypass this.

**Alternatives considered**: *A dedicated route (`PUT /servers/{name}:security`) with its own rule.* Cleaner RBAC lines, but the wizard needs to send overrides in the create request (US2), a second route means a non-atomic create-then-patch, and the settings page already saves one object.

## R11. Audit event shape

**Decision**: no database migration. For a create or update that changes `spec.security`, the handler writes one synchronous row with `Auditor.WriteSync` **before** the Kubernetes write, then calls `audit.MarkRecorded` (the capture precedent, `handlers/capture.go:1252-1258`), so the request has a single row that carries the detail. Write order and failures are in [contracts/audit-events.md](contracts/audit-events.md) "Write order":
- `method`, `path`: the request's own;
- `target`: the server name;
- `status`: the response status;
- `reason`: `security:` followed by compact JSON `{"before":{...},"after":{...}}` with only the settings that changed, in the same value notation as the spec (`{"runAsUser":1000}`, `{"runAsGroup":"image"}`, `{"volumeOwnershipCheck":"EveryStart"}`, and `null` for not set).

**Risky marker (OD-6, settled C).** The change row also records which of the changed settings the pinned template had marked risky at the time of the change: the JSON gains `"risky":["runAsUser"]` after `after` (the setting names only, never the module's reason text). The handler learns it from the template it already loads for the server (the same `GameTemplate` read the create and update path uses for the template reference); a template without a `risky` map, or a change touching no risky setting, adds no key. Nothing is refused on this basis: the dashboard asks, a direct API call is accepted from any `servers:security` holder.

A root refusal and a permission refusal also write a row (`status` 403, `reason` `security_forbidden` or `security_root_disabled`), as the capture handler records failures. The cluster setting change writes its own row: `PUT /admin/config/security`, `target` `security`, `reason` `allowRootOverrides:false->true`. The reconciler's refusal to start a server is a status condition and an event, not an audit row (the audit log records API actions).

**Rationale**: FR-010 and SC-005 need who, when, before and after. The `reason` column is the existing structured, hash-chained, webhook-forwarded and S3-forwarded field (`audit.go:711-750`), so the entry reaches every sink with no schema or chain change. The values are four small numbers, bounded.

**Alternatives considered**: *A new `detail` column.* A migration, a change to `canonicalize` (to keep old rows verifying), the CSV and JSON exports and the syslog bridge schema, for a payload that fits the existing field. *A separate table.* The same cost, and breaks the single tamper-evident chain. If the dashboard's audit page later needs to render before and after as a table, the JSON in `reason` is parseable.

## R12. Database migration

**Decision**: none. No new table or column: the override lives on the CRD, the setting is a new key in the existing `config` table (a row created on first write), and the audit detail uses the existing `reason` column. The next free portable migration number is 020 (`api/internal/db/migrations/common/`); it is not consumed.

## R13. The dashboard

**Decision**: one reusable "Advanced security" section component used in the create wizard and the server Settings tab, both behind a collapsed disclosure (FR-006, FR-007). It renders, per setting: the module default (or "image default"), the applied value and source (existing servers), the pending value, and an override control (value, "use image default", or clear). It reads permission from the existing user permission data to render read-only without `servers:security` (US4 scenario 1) and surfaces the three server refusals (R10) as inline errors. Risky values trigger a confirmation dialog showing the template's `reason` (OD-6, settled C: the confirmation is the dashboard's only; the API refuses nothing on that basis, and the audit row records that the value was risky at change time, R11). A saved change on an existing server shows the "applies on the next restart" notice and, for run-as user or group on a server with data, the file-access warning (FR-007, FR-008).

**Design-first (constitution II, CLAUDE.md rule 1)**: the section, its states and the dialogs are designed in `design.pen` on the maintainer's local machine through the Pencil MCP and exported to `assets/design-export/` **before any web code**. A cloud session cannot run Pencil, so web tasks are blocked until that export is committed. Backend, operator, API, module and docs work is independent of the design and proceeds in parallel. All strings go through the shared packs (FR-014): `web.routes.create_server.advanced_*` and `web.routes.server_settings.security_*` keys in `i18n/locales/en/app.json`, plus `server.security.*` messages in `api/internal/messages/entries_server.go` for the three API refusals.

**Alternatives considered**: *A new admin page for overrides.* Contradicts the spec's assumption that Advanced is a collapsed section on the existing screens.

## R14. Module side: template schema, validators, docs

**Decision**: the new template members (`volumeOwnershipCheck`, `risky`) live in the CRD (R3); then:
- `make module-schema` regenerates `modules/.schema/gametemplate.schema.json` (`hack/gen-module-schema.py:21-22`) in the module repo working tree;
- `validate.py` in the module repo gets a rule that a `risky.<setting>` entry names a setting the template actually sets or may be overridden (a `reason` over 200 characters is an error), and the existing runAsUser rules (`validate.py:610-660, 903-958`) stay as they are;
- `gp-module`'s validator (`gp-module/internal/validator/rules.go`) gains the same check, and the scaffold (`internal/scaffold/scaffold.go:167, 213`) learns to write a `risky` example only when an archetype asks for it (none do today);
- Palworld marks `runAsUser` risky with a reason, since its image must start as root (spec Edge Cases); the module repo review decides any other module;
- `docs/module-authoring.md` "Security context" (`:1137-1173`) is rewritten per FR-013: the block is a default, servers may override it, when to mark risky and how to word the reason, and the ownership-check default.

**Delivery**: module repo files ship as a separate PR to `GameplanePanel/module` and are brought into this repo with the `submodule-bump` skill after that PR merges. The main-repo CRD, operator and API changes work without them (a template with no `risky` block simply has no risky values).

## R15. MCP server

**Decision**: no code change. `mcp-server` lists and gets any Gameplane kind as unstructured objects (`tools.go:64, 109, 135`), so `spec.security` and `status.security` are readable as soon as the CRD ships; the server stays read-only (spec Assumptions). `mcp-server/specs.md` gets one sentence, and `mcp-server/README.md` an example, so the exposure is documented. A test fixture in `mcp-server/main_test.go` asserts a GameServer with `status.security` round-trips unredacted.

## R16. End-to-end proof

**Decision**: two E2E tests in `test/e2e/`, registered in `test/e2e/buckets.sh`, both `t.Parallel()` with unique names:
- `TestGameServer_SecurityOverrideAppliesOnRestart` (operator bucket, zero logins, busybox fixture from `test_helpers_e2e_test.go:42-90`): create with a run-as user override, assert the running uid with `Env.KubectlExec(... id -u)` (`env.go:259`); change the override, assert no restart (same pod UID, `status.security.pending` true, applied unchanged); restart, assert the new uid and `pending` false; remove the override, restart, assert the module default. Then the OD-4 phase, still without the API: save a root `fsGroup` override, stop the server, make sure the ConfigMap `gameplane-security-settings` says off (absent or `"false"`), start it and assert it stays at zero replicas with condition `SecurityAllowed=False` / `RootOverrideDisallowed`; set the ConfigMap to `"true"` with `kubectl` and assert the server starts; set it back to `"false"` and assert the running pod keeps its UID and restart count.
- `TestAPI_SecurityOverride_PermissionAndRootRule` (needs admin and a non-admin login): a user without `servers:security` is refused (403) on create and on update; with the permission, accepted; a root value is refused (403) while `allowRootOverrides` is off and accepted after an admin enables it; the audit log has the rows, and each flip of the setting is mirrored into the ConfigMap (`allowRootOverrides` `"true"` or `"false"`).

**Bucket (decided when writing tasks).** `bucket_operator` carries about four admin logins (`buckets.sh:355-362`: "operator: 4 admin logins with the EventStream test") against a per-job ceiling near seven, and `api-auth`, `api-roles`, `api-rbac`, `api-agent` and `api-mods` are documented as at their ceilings. The second test costs one admin login plus one login as its own non-admin user, which brings `operator` to about six, so both tests go in `bucket_operator`, with a comment recording the tally. No new bucket, CI matrix entry or `suite-components.json` entry is needed: `^TestGameServer_` and `^TestAPI_` already map both tests to their components (`test/e2e/suite-components.json:32, 46`). No game-module join is involved (constitution I's join rule is about game modules; this feature changes no module's game behavior).

## Resolved unknowns

| Unknown | Where resolved |
|---|---|
| Where the override lives | R1 |
| Tri-state representation | R2 |
| Risky flag shape | R3 |
| Desired vs applied vs sources | R4, R6, R7 |
| Apply on restart, no auto-restart | R5 |
| Ownership check mapping and default flip | R8 |
| Cluster setting home, and how the operator learns it (OD-4) | R9 |
| Permission and validation placement | R10 |
| Audit shape | R11 |
| Migration | R12 |
| Dashboard and design gate | R13 |
| Module repo and docs | R14 |
| MCP exposure | R15 |
| E2E | R16 |

Product rulings (not unknowns about the code): OD-1 to OD-6, all settled in [OPEN-DECISIONS.md](OPEN-DECISIONS.md).
