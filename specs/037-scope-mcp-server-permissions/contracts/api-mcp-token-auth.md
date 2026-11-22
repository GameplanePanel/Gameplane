# Contract: MCP token management and token authentication (API)

## Token management routes (session cookie only; a token can never manage tokens)

| Method and path | Permission | Body / result |
|---|---|---|
| `GET /users/me/mcp-tokens` | signed-in user | `[{id, name, prefix, createdAt, expiresAt, lastUsedAt, revokedAt}]`, newest first. |
| `POST /users/me/mcp-tokens` | any signed-in user (OD-5) | `{name, expiresInDays}` → `201 {id, name, prefix, createdAt, expiresAt, token}`. `token` appears only in this response. `expiresInDays` must be 7, 30, 90 or 365 (OD-4). `400` for a bad name or lifetime, `409` for a duplicate name, `422` when the user already has 10 active tokens. |
| `DELETE /users/me/mcp-tokens/{id}` | owner of the token | `204`. Unknown or someone else's ID → `404`. |
| `GET /users/{id}/mcp-tokens` | `users:read` | Same list shape for that user. |
| `DELETE /users/{id}/mcp-tokens/{tokenId}` | `users:manage` | `204`. |

All mutating routes keep the CSRF check. RBAC rules: new self rules next to `/users/me/preferences` (`rbac.go:192-197`), with an exact-suffix match on `/users/me/mcp-tokens` and a prefix match for `DELETE /users/me/mcp-tokens/{id}`; `/users/{id}/mcp-tokens` under the `users` segment rules.

## Authentication

- Header: `Authorization: Bearer gpmcp_<43 base64url chars>`.
- Tried only when the request has no session cookie. A request with both uses the cookie and ignores the header.
- Invalid, expired, revoked, epoch-mismatched or unknown tokens all get the same `401 server.common.unauthenticated` body as a missing session (FR-014). Store failures get `503 server.generic.503`, as for sessions.
- Rate limit: token requests count against the existing per-user limiter; failed token lookups count against the per-IP auth limiter, like failed logins.

## Token guard (`mcpTokenGuard`, runs after `Authenticate`, before `rbac.Middleware`)

A token-authenticated request passes only when the method is GET or HEAD, it is not a WebSocket upgrade, and its route pattern is on this allow-list:

```
/users/me
/clusters
/namespaces
/servers                 /servers/{name}
/servers/{name}/events   /servers/{name}/pods   /servers/{name}/logs/pod/tail
/templates               /templates/{name}
/backups                 /backups/{name}
/schedules               /schedules/{name}
/restores                /restores/{name}
/modules                 /modules/{name}         /modules/sources
/users/me/servers
/pods                    /pods/{name}
/kube-events
/admin/system-logs/{component}
```

Everything else → `403 server.forbidden`, audited with reason `mcp-refused`. The list is a Go slice next to the guard, and a unit test fails if a route on it is not GET-only in the router.

After the guard, `rbac.Middleware`, owner/collaborator fallback and the handlers run exactly as for a cookie session. A token never grants more than the user's current permissions (FR-016).

## Session header

`X-Gameplane-MCP-Session: <22 base64url chars>` (optional). See data-model.md, audit events.
