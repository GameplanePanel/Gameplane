# Contract: GameServer `spec.security` and `status.security`

**Requirements**: FR-002, FR-002a, FR-003, FR-004, FR-005, FR-005a, FR-008, FR-009, FR-011, FR-012. **Research**: R1, R2, R4 to R9. **Data model**: [data-model.md](../data-model.md).

Go types: `operator/api/v1alpha1/gameserver_types.go` (`GameServerSecuritySpec`, `SecurityIDOverride`, `GameServerSecurityStatus`, `SecuritySettings`, `SecurityIDStatus`, `SecurityCheckStatus`). After editing them run `make generate && make manifests` and commit the deepcopy, `operator/config/crd/*.yaml`, `charts/gameplane/charts/gameplane-crds/crds/*.yaml` and `charts/gameplane/crd-manifests/*.yaml` in the same changeset (CLAUDE.md rule 7). The CRD stays `gameplane.local/v1alpha1`; the change is additive and optional, so no conversion is needed.

## `spec.security`

```yaml
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata: { name: survival, namespace: gameplane-games }
spec:
  templateRef: { name: minecraft-java }
  security:
    runAsUser:  { value: 2000 }          # force uid 2000
    runAsGroup: { useImageDefault: true } # apply no run-as group, even if the module sets one
    # fsGroup omitted -> module default
    volumeOwnershipCheck: EveryStart      # Always: re-apply group to every file on each start
```

| Field | Type | Schema rule |
|---|---|---|
| `security` | object, optional | Absent means no override. |
| `security.runAsUser`, `.runAsGroup`, `.fsGroup` | object, optional | `value` int64 0 to 4294967295; `useImageDefault` bool. CEL: exactly one is present; `useImageDefault` must be `true` when present. |
| `security.volumeOwnershipCheck` | enum, optional | `EveryStart` or `OnlyWhenWrong`. |

Cross-field CEL messages (so `kubectl apply` shows them): `security.<id>: set exactly one of value or useImageDefault`, `security.<id>.useImageDefault must be true when set`.

The CRD does **not** enforce the root rule or the permission: those need the cluster setting and the caller identity, which only the API has (FR-004, research R10). A user who can write GameServers directly in the cluster with `kubectl` bypasses the API's rules, exactly as for `spec.capture` and `spec.image` today; operators who need to prevent that use Kubernetes RBAC on the namespace.

## `status.security`

Written only by the reconciler (merge patch of this field alone, so the agent's `status.agent` heartbeat is untouched).

```yaml
status:
  security:
    desired:
      runAsUser:            { value: 2000, source: Override }
      runAsGroup:           { source: ImageDefaultOverride }   # no value: nothing applied
      fsGroup:              { value: 1000, source: ModuleDefault }
      volumeOwnershipCheck: { value: EveryStart, source: Override }
    applied:
      runAsUser:            { value: 1000, source: ModuleDefault }
      runAsGroup:           { value: 1000, source: ModuleDefault }
      fsGroup:              { value: 1000, source: ModuleDefault }
      volumeOwnershipCheck: { value: OnlyWhenWrong, source: BuiltInDefault }
    pending: true
```

| Field | Rule |
|---|---|
| `desired` | Always set after the first reconcile. A pure function of the pinned template and `spec.security`. |
| `applied` | Read from the StatefulSet annotation `gameplane.local/security-applied-observed`, which the reconciler copies from the pod template once a pod built from it has had its volumes set up (research R6). A pending or failed-before-volume-setup start leaves it unchanged. Omitted before the first start. |
| `pending` | `applied` present and any value differs from `desired`. Source-only changes do not count. |
| `source` | `ModuleDefault`, `Override`, `ImageDefaultOverride`, `ImageDefault` (ids), `BuiltInDefault` (ownership check only). |
| `value` | absent for an id with `ImageDefault` or `ImageDefaultOverride`. |

## Reconciler behavior

- **Resolution** (research R4): per id, `useImageDefault` gives none; else the override value; else the template value; else none. Ownership check: override, else template, else `OnlyWhenWrong`.
- **Pod encoding** (data-model.md "Pod-level encoding"): game container `runAsUser`/`runAsGroup`; pod `fsGroup`; pod `fsGroupChangePolicy` (`Always` for `EveryStart`, `OnRootMismatch` for `OnlyWhenWrong`) only when `fsGroup` applies; config-init uses the resolved filesystem group. With nothing applied the pod spec is byte-identical to today (no empty `securityContext: {}`), as the existing builders already guarantee.
- **Applying** (research R5): the security-derived parts of the pod template change only when the StatefulSet is created or goes from zero replicas to one or more. Saving a change never restarts the server (FR-008).
- **Backup and restore** (FR-011): jobs take their pod `fsGroup` from `status.security.applied.fsGroup`'s source of truth (the same pod or StatefulSet read), never from `desired`.
- **Restricted workload mode** (OD-5, settled A): when the operator runs with `--restricted-workloads`, the effective (desired) values, not the template alone, are validated with the existing rule (`validateRestrictedTemplate`, `workload_security.go:15`; a positive run-as user and filesystem group). A violation (a root value, or "use image default" for run-as user) sets the existing failure phase message through `setPhase` and the server stays failed until the override is fixed; a pod that is already running keeps running with its applied values. The refusal is the same one an invalid template gets (`gameserver_controller.go:334`).
- **Root overrides while the cluster setting is off** (OD-4, settled A; FR-005a): a stopped server holding a root override is not started and reports condition `SecurityAllowed=False`, reason `RootOverrideDisallowed`; a running one is untouched; the server starts once the override is cleared or the setting is on. The setting is read from the ConfigMap mirror (research R9, [cluster-setting.md](cluster-setting.md)); the condition is described in [data-model.md](../data-model.md), "Start refusal".
- **Unchanged**: agent, config-init user, sentinel, tunnel, wipe and capture helper security contexts (FR-012).

## Compatibility

| Case | Result |
|---|---|
| Existing server, no `spec.security` | `desired` equals today's resolution; pod spec unchanged while running. `applied` is rebuilt from the pod without the annotation (module defaults). |
| Existing server with a module `fsGroup` | After the operator upgrade `desired.volumeOwnershipCheck` is `OnlyWhenWrong` and `pending` is true until the next start; no restart is caused by the upgrade. |
| Older operator reading a GameServer with `spec.security` | The field is unknown to it and ignored; the CRD's structural schema keeps it stored. |
| Operator upgrade, setting never written | The ConfigMap is absent, so the setting reads as off; no existing server holds an override yet, so nothing is refused and nothing restarts. |
