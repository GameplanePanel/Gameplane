# Data Model: Module security defaults with a per-server override

**Feature**: [spec.md](spec.md) | **Research**: [research.md](research.md)

Five places hold data. No database table or column is added (research R12).

- **GameTemplate** (CRD, cluster-scoped): the module's defaults and the risky flags. YAML in [contracts/crd-gametemplate-security.md](contracts/crd-gametemplate-security.md).
- **GameServer** (CRD, namespaced): the per-server override in `spec`, the read model in `status`. YAML in [contracts/crd-gameserver-security.md](contracts/crd-gameserver-security.md).
- **API config store**: one new section, `security`, in the existing `config` table, mirrored by the API into one ConfigMap per cluster that the operator reads (OD-4). See [contracts/cluster-setting.md](contracts/cluster-setting.md).
- **Audit log**: one structured `reason` string per change. See [contracts/audit-events.md](contracts/audit-events.md).

The four settings are named `runAsUser`, `runAsGroup`, `fsGroup` (numeric ids) and `volumeOwnershipCheck` (an enum). "Setting" below means any of the four.

---

## Module security defaults (`GameTemplate.spec.security`)

Existing type `GameSecuritySpec` (`operator/api/v1alpha1/gametemplate_types.go:185`), extended.

| Field | Type | Rule | Change |
|---|---|---|---|
| `runAsUser` | int64, optional | 0 to 4294967295 | existing |
| `runAsGroup` | int64, optional | 0 to 4294967295 | existing |
| `fsGroup` | int64, optional | 0 to 4294967295 | existing |
| `volumeOwnershipCheck` | enum, optional | `EveryStart` or `OnlyWhenWrong`. Omitted means the built-in default, `OnlyWhenWrong` | **new** |
| `risky` | object, optional | see below | **new** |

### `risky` (`SecurityRiskSpec`)

| Field | Type | Rule |
|---|---|---|
| `runAsUser`, `runAsGroup`, `fsGroup`, `volumeOwnershipCheck` | `SecurityRiskNote`, each optional | Presence marks that setting risky to override. |

`SecurityRiskNote`:

| Field | Type | Rule |
|---|---|---|
| `reason` | string, optional | 0 to 200 characters. Shown by the dashboard in the confirmation. Plain text; the dashboard never renders it as markup. |

A template can mark a setting risky even when it sets no default for it (Palworld sets only `fsGroup` and marks `runAsUser`). Modules cannot lock a setting; the operator ignores `risky`.

An empty or absent `security` block means today's behavior: no security context applied, plus the built-in ownership-check default (which has an effect only when a filesystem group applies).

---

## Server security override (`GameServer.spec.security`)

New type `GameServerSecuritySpec`, an optional pointer on `GameServerSpec` (after `capture`, `operator/api/v1alpha1/gameserver_types.go:161`).

| Field | Type | Rule |
|---|---|---|
| `runAsUser` | `SecurityIDOverride`, optional | see below |
| `runAsGroup` | `SecurityIDOverride`, optional | see below |
| `fsGroup` | `SecurityIDOverride`, optional | see below |
| `volumeOwnershipCheck` | enum, optional | `EveryStart` or `OnlyWhenWrong`. Omitted means "not set" (use the module's, else the built-in default). |

`SecurityIDOverride`:

| Field | Type | Rule |
|---|---|---|
| `value` | int64, optional | 0 to 4294967295 (CRD `Minimum`/`Maximum`, the same as the template). |
| `useImageDefault` | bool, optional | When true, the setting is not applied to the game container or pod even if the module sets it. |

CRD rule (CEL on `SecurityIDOverride`): exactly one of `value` and `useImageDefault` is present, and `useImageDefault`, when present, is `true` (an explicit `false` is invalid, so a stored override always means something).

Per-setting override states, which are the whole truth table of FR-002:

| Stored | Meaning |
|---|---|
| field omitted | not set: use the module default |
| `{ value: N }` | force the number N |
| `{ useImageDefault: true }` | apply nothing for this setting (id settings only) |

The `volumeOwnershipCheck` override has no "use image default" form (FR-002a).

### Validation (API) in addition to the CRD

Order of checks in `validateAndProtectGameServer` (research R10); the first failing check decides the response.

| # | Check | Response |
|---|---|---|
| 1 | The block is unchanged from the stored one | accepted, no permission needed |
| 2 | The caller holds `servers:security` on this cluster and namespace (or `*`) for any other change | else 403 `server.security.forbidden` |
| 3 | Shape: ids are whole numbers 0 to 4294967295; exactly one of `value`/`useImageDefault`; enum values valid; no unknown members | else 422 `server.security.invalid` |
| 4 | Root rule: a **new or changed** `runAsUser` or `runAsGroup` that is `value: 0` or `useImageDefault: true`, or a new or changed `fsGroup` that is `value: 0`, needs `allowRootOverrides` to be true | else 403 `server.security.root_disabled` |

Root rule table (FR-005, clarifications 2026-11-13):

| Setting | `value: 0` | `useImageDefault: true` | other value |
|---|---|---|---|
| `runAsUser` | root, needs setting | root, needs setting | free |
| `runAsGroup` | root, needs setting | root, needs setting | free |
| `fsGroup` | root, needs setting | free (applies no chown) | free |
| `volumeOwnershipCheck` | n/a | n/a | free |

An id override that equals the module default is allowed and stored (it then survives a module upgrade, US3 scenario 3). The API checks only new or changed root values. An existing root override is checked by the reconciler when the server would start, and the server is refused while the setting is off (OD-4 settled A, FR-005a; see "Start refusal" below).

---

## Desired and applied settings (`GameServer.status.security`)

New read model on `GameServerStatus` (`gameserver_types.go:558`), written only by the reconciler with a merge patch of this field alone.

| Field | Type | Meaning |
|---|---|---|
| `desired` | `SecuritySettings` | What the next start will use: the override over the module default over nothing (FR-003). Always present once the server has been reconciled. |
| `applied` | `SecuritySettings`, optional | What the pod last started with (research R6). Omitted before the first start. |
| `pending` | bool | True when `applied` exists and differs from `desired` in a value. A change of source with the same value is not pending. |

`SecuritySettings`:

| Field | Type |
|---|---|
| `runAsUser`, `runAsGroup`, `fsGroup` | `SecurityIDStatus` |
| `volumeOwnershipCheck` | `SecurityCheckStatus` |

`SecurityIDStatus`: `value` (int64, omitted when no id is applied) and `source`. `SecurityCheckStatus`: `value` (`EveryStart` or `OnlyWhenWrong`) and `source`.

`source` values (FR-009, plus one):

| `source` | Meaning | Used by |
|---|---|---|
| `ModuleDefault` | the template's value | all four |
| `Override` | the server's `value` | all four |
| `ImageDefaultOverride` | the server's `useImageDefault: true` | ids |
| `ImageDefault` | neither the module nor the server sets it; the image decides | ids |
| `BuiltInDefault` | neither sets it; the platform default `OnlyWhenWrong` | `volumeOwnershipCheck` only |

`BuiltInDefault` is the one source not in FR-009's list; FR-009 gives no label for the ownership check's platform default (spec gap, see plan.md).

`volumeOwnershipCheck` is meaningful only when `fsGroup.value` is present. The status always reports it; the dashboard marks it "no effect" when no filesystem group applies (FR-002a).

### Pod-level encoding

The values are carried into the pod as follows (research R4, R8). An absent id applies nothing.

| Setting | Pod field |
|---|---|
| `runAsUser` | game container `securityContext.runAsUser` |
| `runAsGroup` | game container `securityContext.runAsGroup` |
| `fsGroup` | pod `securityContext.fsGroup`; config-init `runAsGroup` and its `chgrp` target |
| `volumeOwnershipCheck` | pod `securityContext.fsGroupChangePolicy`: `EveryStart` is `Always`, `OnlyWhenWrong` is `OnRootMismatch`; written only when `fsGroup` applies |
| sources | pod-template annotation `gameplane.local/security-applied` (compact JSON of the resolved value and source for each setting) |

A pod or StatefulSet without the annotation (created before this feature) is read as module defaults: every non-empty value `ModuleDefault`, every empty id `ImageDefault`, and the ownership check `EveryStart` (a pod with no change policy gets the Kubernetes default, `Always`) with source `BuiltInDefault`. When a filesystem group applies, `desired` is `OnlyWhenWrong` (unless the module says otherwise), so such a server reports `pending` until its next start.

### Start refusal (`SecurityAllowed` condition, OD-4)

A GameServer condition, written by the reconciler next to `Ready` with the same merge-patch pattern as the rest of the status.

| Field | Value |
|---|---|
| `type` | `SecurityAllowed` |
| `status` | `False` while the cluster setting is off and `spec.security` holds a root override; `True` otherwise (also `True` or absent for servers with no override) |
| `reason` | `RootOverrideDisallowed` when `False` |
| `message` | names the settings (for example `runAsGroup: use image default`) and says what clears it: remove or change the override, or turn on "allow root overrides" |

A *root override* is `runAsUser` or `runAsGroup` with `value: 0` or `useImageDefault: true`, or `fsGroup` with `value: 0` (same rule as the API's root rule, one shared definition in the operator).

| Server state when the setting is off | Behavior |
|---|---|
| not running (zero replicas) | not started: replicas stay at zero, `SecurityAllowed=False`, `RootOverrideDisallowed` |
| running | untouched: no restart, no scale-down, no pod-template change; condition `False` with a message that it will not start again until fixed |
| setting turned on, or override cleared | the next reconcile (the ConfigMap watch re-enqueues the server) sets `SecurityAllowed=True` and starts a stopped server normally |

The setting is read from the ConfigMap `gameplane-security-settings` (key `allowRootOverrides`); absent or anything but `"true"` means off. Turning the setting off never restarts or stops a server (FR-005a). Restricted workload mode (OD-5, settled A) is a separate check: the effective values are validated with `validateRestrictedTemplate`'s rule and a violation uses the existing failure phase message; a running pod is untouched.

### State transitions

A change is `none`, then `pending`, then `applied`.

```text
        save override                      server (re)starts
none ───────────────────▶ pending ─────────────────────────────▶ applied
 ▲                          │ │  save again (any value)             │
 │                          │ └──▶ pending (desired updated)        │
 │   save equals applied    │                                       │
 └──────────────────────────┘                                       │
 ▲                                                                  │
 └──────────────────────────────────────────────────────────────────┘
        no further change: stays applied (pending=false)
```

| From | Event | To |
|---|---|---|
| none (`pending=false`) | override added, changed or removed so `desired` differs from `applied` | pending |
| pending | override edited again | pending (new `desired`) |
| pending | override edited so `desired` equals `applied` again | none |
| pending | server starts from zero replicas (restart, wake, resume, rollout) | applied (`pending=false`, `applied` takes the new values) |
| any | the module's template changes defaults (template rollout admits it) | recomputed; `pending` per the rule above, applied on the next start |
| none, never started | any change | none (`applied` omitted; `desired` is what the first start uses) |

A save never changes the pod template while a pod runs (research R5). The pod template is refreshed only on first create or when the StatefulSet goes from zero replicas to one or more.

### Invariants

1. `desired` is a pure function of the template (as pinned by the template rollout machinery) and the override; two reconciles with the same inputs give the same `desired`.
2. `applied` changes only once a pod built from the new template has had its volumes set up; it never reports a value the data volume was not set up with (research R6, StatefulSet annotation `gameplane.local/security-applied-observed`).
3. Helper containers (agent, sentinel, tunnel, wipe) and their security contexts are never derived from `desired` or `applied` (FR-012). Backup and restore jobs use `applied.fsGroup` for their pod `fsGroup` (FR-011).
4. The override is never deleted by a module upgrade, uninstall or reinstall; it is reapplied if the template returns.
5. The ownership check never changes the numeric ids; it changes only how often the kubelet re-applies the group.

---

## Cluster setting (API config section `security`)

| Field | Type | Default | Rule |
|---|---|---|---|
| `allowRootOverrides` | bool | false | Absent row means false. Unknown members are dropped by the validator, like every section. |

Stored as the JSON blob `{"allowRootOverrides":false}` in `config` under key `security`; written only through `PUT /admin/config/security` (`config:manage`).

### Operator mirror (ConfigMap)

| Property | Value |
|---|---|
| Name and namespace | `gameplane-security-settings` in the gameplane (control-plane) namespace of each cluster |
| Data | `allowRootOverrides`: `"true"` or `"false"` |
| Writer | the API only (on each flip, and a sync on API start); readers: the operator (watch) |
| Missing or malformed | read as off (fail closed) |
| Labels | `app.kubernetes.io/managed-by: gameplane-api` |

The config row is the source of truth; the ConfigMap is a derived copy (research R9).

## Permission

| Key | Namespaced | Held by default |
|---|---|---|
| `servers:security` | yes | the built-in admin role (through `*`); no other built-in role |

## Audit entries

Described in [contracts/audit-events.md](contracts/audit-events.md); nothing is added to the audit schema. A change row also lists, under `risky`, the changed settings the template had marked risky at that time (OD-6, settled C).
