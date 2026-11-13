# Contract: cluster-wide "allow root overrides" setting

**Requirements**: FR-005, SC-003, OD-2, OD-4. **Research**: R9, R11.

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

## What it governs

The API's server create, update and clone path (see [api-server-security.md](api-server-security.md), case 5) reads this value on each request that carries a new or changed root value. A root value is run-as user or run-as group set to 0 or to "use image default", or filesystem group set to 0. Nothing else reads it: the operator and the agent never see it, because the operator only applies stored values (FR-004).

Turning it **off** does not touch stored overrides and does not restart anything (OD-4, option A, pending ruling). It only refuses future root values.

Multi-cluster: the setting is the central API's, and applies to servers created or edited through that API on any registered cluster. Per-cluster values are out of scope.

## Audit

A successful change that flips the value writes one extra row, see [audit-events.md](audit-events.md) (`PUT /admin/config/security`, `target` `security`, `reason` `allowRootOverrides:false->true`). A save that leaves the value as it was writes only the generic middleware row.

## Dashboard

The Admin Settings screen (`web/src/routes/AdminSettings.tsx`) gains a "Security" section with one switch, off by default, with text that states what it allows and that it does not change existing servers. Designed in `design.pen` first (plan.md, design gate).

## Tests

- Unit: validator accepts `{"allowRootOverrides":true}`, rejects non-boolean and empty body, drops unknown members.
- Envtest (`config_envtest_test.go` pattern): the section round-trips through `GET /admin/config`; a viewer without `config:manage` gets 403 on `PUT`.
- The server-path behavior with the setting on and off is covered by the handler envtest and the E2E in [api-server-security.md](api-server-security.md).
