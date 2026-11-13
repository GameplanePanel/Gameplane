# Contract: cluster-wide "allow root overrides" setting

**Requirements**: FR-005, FR-005a, SC-003, OD-2, OD-4. **Research**: R9, R11.

## Storage and routes

A new section `security` in the API's config store (`config` table, key `security`). No migration: the row is created on first write; an absent row means `false`.

| Route | Permission | Behavior |
|---|---|---|
| `GET /admin/config` | `config:read` | Includes `"security": {"allowRootOverrides": false}` once the section has been written; absent otherwise (the dashboard treats absent as false). |
| `PUT /admin/config/security` | `config:manage` | Validates and stores the section. Both rules already exist (`api/internal/rbac/rbac.go:223-228`); no rule-table change. |

The validator is registered in `newValidators` (`api/internal/handlers/config.go:462`), so the section name joins the closed allowlist and unknown members are dropped.

```http
PUT /admin/config/security
{ "allowRootOverrides": true }

200 { "section": "security", "value": { "allowRootOverrides": true } }
```

| Field | Type | Rule |
|---|---|---|
| `allowRootOverrides` | bool | Required. Invalid JSON or a non-boolean value returns 422 like the other sections. |

## Operator mirror

The operator must know the setting (FR-005a), and it neither reads the API database nor calls the API (CLAUDE.md rule 10). The API therefore mirrors the value into a ConfigMap:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: gameplane-security-settings
  namespace: <gameplane control-plane namespace>
  labels: { app.kubernetes.io/managed-by: gameplane-api }
data:
  allowRootOverrides: "false"   # "true" or "false"
```

- `PUT /admin/config/security` writes the ConfigMap, then the config row; if the ConfigMap write fails the request fails (502 with the existing error body) and the config row is unchanged, so the two never silently disagree. A multi-cluster API writes the ConfigMap into every registered cluster it can reach.
- On start the API rewrites the ConfigMap from the config row (it converges after a database restore, a new cluster or a deleted ConfigMap). No other writer exists; a hand edit is overwritten at the next sync.
- **Fail closed**: the operator reads a missing ConfigMap, a missing key or any value other than `"true"` as off.
- RBAC: the chart gives the API `get;create;update;patch` on this one ConfigMap by `resourceNames` in the release namespace (`charts/gameplane/templates/api.yaml`); the operator already holds `get;list;watch` on ConfigMaps.

## What it governs

1. **API write path** (see [api-server-security.md](api-server-security.md), case 5): each request that carries a new or changed root value is refused while the setting is off. A root value is run-as user or run-as group set to 0 or to "use image default", or filesystem group set to 0.
2. **Reconciler start path** (OD-4, settled A; FR-005a): while the setting is off, the operator does not start a server that holds a saved root override. Replicas stay at zero and the server reports condition `SecurityAllowed=False` with reason `RootOverrideDisallowed` (details in [data-model.md](../data-model.md), "Start refusal"). It starts once the override is cleared or the setting is on again.

Turning it **off** never restarts or stops a running server and never edits a stored override; it only makes future writes of root values fail and makes a stopped server holding one refuse to start. Turning it **on** lets such a server start on the next reconcile (the operator watches the ConfigMap, so there is no delay beyond the reconcile).

Multi-cluster: the setting is the central API's, and applies to servers created or edited through that API on any registered cluster. Per-cluster values are out of scope.

## Audit

A successful change that flips the value writes one extra row, see [audit-events.md](audit-events.md) (`PUT /admin/config/security`, `target` `security`, `reason` `allowRootOverrides:false->true`). A save that leaves the value as it was writes only the generic middleware row.

## Dashboard

The Admin Settings screen (`web/src/routes/AdminSettings.tsx`) gains a "Security" section with one switch, off by default, with text that states what it allows and that it does not change existing servers. Designed in `design.pen` first (plan.md, design gate).

## Tests

- Unit: validator accepts `{"allowRootOverrides":true}`, rejects non-boolean and empty body, drops unknown members.
- Envtest (`config_envtest_test.go` pattern): the section round-trips through `GET /admin/config`; a viewer without `config:manage` gets 403 on `PUT`; a flip writes the ConfigMap with the expected value and a failing ConfigMap write leaves the config row unchanged; the start-up sync recreates a deleted ConfigMap.
- Operator envtest (`gameserver_security_envtest_test.go`): with the ConfigMap absent or `"false"`, a stopped server holding a root override stays at zero replicas with `SecurityAllowed=False` / `RootOverrideDisallowed`; a running one is untouched; flipping the ConfigMap to `"true"` starts the stopped one; clearing the override starts it too.
- The server-path behavior with the setting on and off is covered by the handler envtest and the E2E in [api-server-security.md](api-server-security.md).
