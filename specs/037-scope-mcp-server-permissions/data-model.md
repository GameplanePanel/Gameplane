# Data model: Scope MCP server permissions (spec 037)

## MCP token (new table `mcp_tokens`, migration `api/internal/db/migrations/common/023_mcp_tokens.sql`)

Portable SQL per `api/internal/db/migrations/README.md` (no dialect functions; timestamps bound from Go as RFC 3339 UTC text, like `sessions.expires_at`).

| Column | Type | Rules |
|---|---|---|
| `id` | integer primary key | Generated. Shown to the user as the token's stable ID; never the secret. |
| `user_id` | integer, FK `users(id)` ON DELETE CASCADE, indexed | Owner. Deleting the user deletes the tokens. |
| `name` | text, not null | 1 to 64 characters after trimming, unique per user. |
| `digest` | text, not null, unique | SHA-256 hex of the full token string. The token itself is never stored. |
| `prefix` | text, not null | First 10 characters (`gpmcp_` + 4), shown in lists so a user can tell tokens apart. |
| `session_epoch` | integer, not null | Copied from `users.session_epoch` at creation. A mismatch (password reset) makes the token invalid. |
| `created_at` | text, not null | RFC 3339 UTC. |
| `expires_at` | text, not null | RFC 3339 UTC. Allowed lifetimes: see OD-4 (pending). No non-expiring tokens. |
| `last_used_at` | text, null | Updated at most once a minute per token, so reads do not write on every call. |
| `revoked_at` | text, null | Set by Revoke; a revoked row is kept for 30 days for the token list, then removed by the existing GC loop with expired rows. |

**Validity** (checked on every request): digest matches, `revoked_at` is null, `expires_at` is in the future, `session_epoch` equals the user's current epoch, and the user still exists.

**Limits:** at most 10 active tokens per user (creating an 11th is refused with a clear message).

**State transitions:** `active` → `revoked` (user or admin) · `active` → `expired` (time) · `active` → `invalid` (password reset) · any → deleted (user deleted, or GC after 30 days).

## Token-authenticated request (context only, not stored)

- `auth.User` as today (ID, username, role, `Perms` loaded per request).
- `ViaMCPToken bool` and `MCPTokenID int64` on the request context, read by the token guard, the audit actor (`<username> (mcp)`) and `last_used_at` updates.
- `X-Gameplane-MCP-Session` header: random ID per `serve` process; the API audits the first request of each (token, session ID) pair once (in-memory set with a 24-hour TTL), plus every refused token request (FR-015).

## Audit events (existing `audit_events` table, new reasons)

| Event | Method / path recorded | When |
|---|---|---|
| Token created | `POST /users/me/mcp-tokens` | User creates a token (name, expiry; never the secret). |
| Token revoked | `DELETE /users/me/mcp-tokens/{id}` or `DELETE /users/{id}/mcp-tokens/{tokenId}` | User or admin revokes. |
| MCP session started | `GET <first path>`, reason `mcp-session` | First request of a new `X-Gameplane-MCP-Session` for a token. |
| MCP request refused | method and path, reason `mcp-refused`, status 401/403 | Any refused token request (guard, RBAC or invalid token). Invalid-token rows record actor `anonymous`. |

## MCP server configuration (mcp-server, not stored)

| Setting | Source | Mode |
|---|---|---|
| `GAMEPLANE_PANEL_URL` / `--panel-url` | env or flag | Per-user mode when set. HTTPS required except loopback. |
| `GAMEPLANE_MCP_TOKEN` | env only | Required in per-user mode; `serve` refuses to start without it. |
| `--panel-ca` | flag | Optional PEM bundle for a private CA. |
| `GAMEPLANE_MCP_NAMESPACES` | env, rendered by the chart | Break-glass mode namespace allow-list (release namespace + managed game namespaces). Unset = unrestricted standalone kubeconfig run. |

## Chart RBAC objects (break-glass, replaces `<release>-mcp-server-read` ClusterRole + binding)

| Object | Scope | Rules |
|---|---|---|
| ClusterRole + ClusterRoleBinding `<release>-mcp-server-catalog-read` | cluster | get/list/watch on `gametemplates`, `modules`, `modulesources` only. |
| Role + RoleBinding `<release>-mcp-server-read` in the release namespace | namespace | get/list/watch on `pods`, `events`; get on `pods/log`. |
| Role + RoleBinding `<release>-mcp-server-read` in each managed game namespace | namespace | get/list/watch on `pods`, `events`, `gameservers`, `backups`, `backupschedules`, `restores`; get on `pods/log`. |

Managed game namespaces come from `gameplane.operatorGameNamespaces`. No rule anywhere has a write verb.
