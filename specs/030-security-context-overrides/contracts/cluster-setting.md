# Contract: per-cluster "allow root overrides" setting

**Requirements**: FR-005, FR-005a, SC-003, OD-2, OD-4, OD-7, OD-8, OD-9. **Research**: R9, R11. **Briefs**: `briefs/00-rulings.md` sections 1 to 5 are the binding form of this contract.

## Scope and store

- The setting is separate for each cluster and off by default (OD-7 C). Each cluster is set on its own, under Clusters, cluster, Configuration.
- Its only store is one ConfigMap per cluster:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: gameplane-security-settings
  namespace: <operator control-plane namespace of that cluster>
  annotations: { helm.sh/resource-policy: keep }
  labels: <chart labels>
data:
  allowRootOverrides: "false"   # "true" or "false"; nothing else is on
```

- The namespace is the operator's `--control-plane-namespace` on that cluster. On a default install with the API enabled it is the credentials namespace `<release>-credentials`. In standalone or no-API mode it is the release namespace.
- There is no config-table row, no `security` config section, and no API start-up sync (OD-7).

## Namespace the API uses (OD-10 A)

- Home cluster: the cluster's own control-plane namespace, the same `moduleConfigMapNS` as today (`api/cmd/main.go:231`). On a default install that is the credentials namespace.
- Remote cluster: the namespace recorded on that cluster's registration, `Cluster.spec.controlPlaneNamespace`. The default is `gameplane`. It is set in the register-cluster form and shown on the cluster page (tasks T078 and T079).
- The API reads and updates the ConfigMap in that namespace through the cluster's registry client, by name only.
- A ConfigMap not found in the resolved namespace reads as off. GET returns `present: false`, and PUT returns 409 `cluster.security_settings_missing` with no audit row.
- A wrong namespace cannot turn the setting on; it only shows the setting as missing. The namespace must be the operator control-plane namespace of that remote install.

## Creation: Helm (OD-8 A)

- The operator chart creates the ConfigMap wherever it renders: the home install and every remote operator install.
- On first install it renders `"false"`. On upgrade it reads the current object with Helm `lookup` and re-renders the value it finds, so an upgrade never flips the setting. When `lookup` finds nothing, the value renders as `"false"`.
- The annotation `helm.sh/resource-policy: keep` keeps the object on uninstall.
- The API never creates the ConfigMap. If it is deleted, the setting reads as off until the next `helm upgrade`.

## Who may read and write

| Actor | Access | Scope |
|---|---|---|
| Operator (on its own cluster) | `get;list;watch` on ConfigMaps (already held, `operator/internal/controller/gameserver_controller.go:261`, chart `operator.yaml:66`) | cluster |
| API, home cluster | `get`, `update`, `patch` by `resourceNames: [gameplane-security-settings]` in the credentials namespace (`charts/gameplane/templates/api-credentials.yaml`) | one object |
| API, standalone home | the same three verbs, Role and RoleBinding in the release namespace (`charts/gameplane/templates/api.yaml`, inside the `apiEnabled` guard) | one object |
| API, remote cluster | the same three verbs granted by the remote chart to the identity the API uses for that cluster (task T076), in the namespace on its registration (OD-10 A) | one object |

No `create` verb is granted anywhere: Kubernetes does not limit `create` by name (OD-8).

## Routes

| Route | Permission | Cluster |
|---|---|---|
| `GET /security-settings?cluster=<id>` | `config:read` | `resolveCluster` (`api/internal/handlers/resources.go:113`), as `/backup-destinations` |
| `PUT /security-settings?cluster=<id>` | `config:manage` | same |

Both rules are new entries in `api/internal/rbac/rbac.go`, next to the `/admin/config` rules (`:209` GET `config:read`, `:214` `config:manage`).

Body of PUT:

```http
PUT /security-settings?cluster=<id>
{ "allowRootOverrides": true }
```

| Condition | Result |
|---|---|
| Body empty | 400 `server.empty_body` (OD-9 item 1) |
| Body is not JSON, or `allowRootOverrides` is missing or not a boolean | 422 |
| ConfigMap NotFound | 409 `cluster.security_settings_missing`, no audit row |
| Value unchanged | 200, nothing written, no audit row |
| Value changes, audit row written, update succeeds | 200 |
| Audit write fails | 500, nothing changes |
| Update fails after the audit row | 502, plus a `security_save_failed` row |

GET body: `{"cluster": "<id>", "allowRootOverrides": bool, "present": bool}`. A NotFound ConfigMap reads as `allowRootOverrides: false, present: false`. Any other read error returns 502. PUT returns the same body with `present: true`.

Message `cluster.security_settings_missing`: 409, English "the security settings ConfigMap is missing on this cluster; run helm upgrade to recreate it". Registered in `api/internal/messages/` (cluster entries), with en and fr keys (FR-014).

Handler: `api/internal/handlers/security_settings.go`, mounted by `MountSecuritySettings(r chi.Router, reg *kube.Registry, auditor *audit.Auditor)` from `api/cmd/main.go`.

## Operator reading (FR-005a)

- The operator reads the ConfigMap in its own control-plane namespace from its informer cache (`ControlPlaneNamespace`, briefs/00 section 3). Missing ConfigMap, missing key, or any value other than `"true"` is off.
- While the setting is off, the reconciler does not start a server that holds a saved root override. Replicas stay at zero and the server reports condition `SecurityAllowed=False`, reason `RootOverrideDisallowed` (details in [data-model.md](../data-model.md), "Start refusal"). The hold is checked before the server takes a wake slot (OD-9 item 4).
- Turning the setting off never restarts or stops a running server and never edits a stored override. Turning it on, or clearing the override, lets the next reconcile start a stopped server. The ConfigMap watch re-enqueues the servers that hold a root override.

## API reading (case 5)

- A request that carries a new or changed root value (run-as user or group set to 0 or "use image default", or filesystem group set to 0) reads the ConfigMap of the request's own cluster through that cluster's registry client, in the namespace resolved for that cluster (see "Namespace the API uses"). The setting is on only for exact `"true"`. Missing, unreadable or any other value refuses the request with 403 `server.security.root_disabled`.
- A request that does not change a root value does not read the ConfigMap.

## Multi-cluster

- The setting belongs to one cluster. A flip touches only that cluster's ConfigMap. Other clusters keep their own value, and a new cluster starts off.
- The API reads a remote cluster's ConfigMap in the namespace on its registration, `Cluster.spec.controlPlaneNamespace` (OD-10 A; briefs/00 section 12). The remote chart grants the API identity access in that namespace (T076).

## Audit

- A flip writes one row before the ConfigMap update: `PUT /security-settings`, `target` `security/<cluster id>`, `reason` `allowRootOverrides:false->true` (or `true->false`). See [audit-events.md](audit-events.md), "Write order".
- A save that leaves the value as it was writes only the generic middleware row.

## Dashboard

- A "Security" panel in `web/src/components/clusters/ClusterConfiguration.tsx` (Clusters, cluster, Configuration), next to `BackupDestinationsPanel.tsx`. The component is `web/src/components/clusters/SecuritySettingsPanel.tsx`. It is not in Admin Settings.
- One switch, off by default. Text: turning it off does not stop running servers on this cluster, but stopped servers with a root override will not start until the override is cleared or the switch is back on.
- A "settings missing, run helm upgrade" state when GET reports `present: false` or PUT returns 409.
- Designed in `design.pen` first (plan.md, design gate).

## Tests

Test files are new (briefs/00 section 8).

- Unit (`api/internal/handlers/security_settings_test.go`): validator accepts `{"allowRootOverrides":true}`, rejects non-boolean, returns 400 for an empty body, drops unknown members.
- Envtest (`api/internal/handlers/security_settings_envtest_test.go`, dedicated router): GET round trip; a viewer without `config:manage` gets 403 on PUT; a flip updates the ConfigMap and writes one row; a failing audit write returns 500 and leaves the ConfigMap unchanged; a failing update returns 502 and writes `security_save_failed`; a missing ConfigMap returns 409 and writes no row. Nothing creates the ConfigMap.
- Operator envtest (`operator/internal/controller/security_overrides_envtest_test.go`): with the ConfigMap absent or `"false"`, a stopped server holding a root override stays at zero replicas with `SecurityAllowed=False` / `RootOverrideDisallowed`; a running one is untouched; flipping the ConfigMap to `"true"` starts the stopped one; clearing the override starts it too.
- Chart: no test fixture exists (briefs/04 Q3). Rendering and RBAC are checked by CI `helm template` and by the E2E.
