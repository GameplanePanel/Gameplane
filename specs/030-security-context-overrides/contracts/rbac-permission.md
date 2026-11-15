# Contract: the `servers:security` permission

**Requirements**: FR-010, SC-003, OD-1. **Research**: R10.

## Catalog entry

`api/internal/rbac/catalog.go`, in the `servers` group (`:25-29`), after `servers:console`:

```go
{Key: "servers:security", Label: "Set or change a server's security overrides (run-as user and group, filesystem group, volume ownership check)", Namespaced: true},
```

| Property | Value |
|---|---|
| Key | `servers:security` |
| Scope | namespaced, like `servers:write` and `captures:manage`; held through a role binding on a namespace, or cluster-wide on a cluster, or `*` |
| Check | `User.Can("servers:security", true, cluster, ns)` (`api/internal/auth/perms.go:123`) |
| Default holders | the built-in admin role (it holds `*`). No other built-in role. Admins can add the key to a custom role or to `operator` through the existing role editor (the permission picker renders the catalog, so the key appears automatically). |
| Seed data | none. No migration: admin's `*` covers it, and the seeded roles keep their current rows. `TestCatalog_CoversSeededPermissions` and `TestCatalog_CoversRuleTablePermissions` (`rbac/catalog_test.go:68, 104`) only require that rule-table and seeded keys are catalog keys. |

## Rule table

No route rule is added. The permission is a **field-level** check inside `validateAndProtectGameServer` (`api/internal/handlers/resources.go:538`), the same placement as `captures:manage` for `spec.capture` (`resources.go:565-583`). `/servers` keeps `servers:write` as its route permission (`rbac.go:267`); holding `servers:security` alone does not grant the right to write servers.

Because the permission is not a route permission, `TestCatalog_CoversRuleTablePermissions` is unaffected and a catalog-order test is not needed. A new test asserts the key exists, is namespaced, and is held by `admin` only among the built-in roles.

## Privilege grant guard

`servers:security` is **not** added to `privilegedPermissions` (`api/internal/handlers/privileged_grants.go:21`): that list holds the permissions that lead to full administrator access, and `servers:security` does not; it lets a holder run one game as another uid, not become an administrator. A delegated role manager may therefore grant it, as they already may grant `servers:write` and `captures:manage`. This is a stated consequence of OD-1 (an admin can hand the key out deliberately), not a new escalation path; if the maintainers want it admin-grant-only, adding it to that list is a one-line change in `privileged_grants.go` (it must also be added to the two SQL `IN (...)` lists in that file).

## Owner and collaborator fallback

The owner and collaborator fallback (`rbac.go:122-150`) never covers a full-object `PUT /servers/{name}`, and `POST /servers` needs `servers:write` outright, so being the owner of a server never implies `servers:security`.

## Dashboard use

The Advanced section's editable state is `can("servers:security", namespace)` from the permission data the dashboard already loads for the signed-in user. The server refusal (403 `server.security.forbidden`) remains the authority; the client check only avoids showing controls that would fail.
