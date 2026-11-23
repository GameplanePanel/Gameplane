# Contracts: API routes for SFTP

All bodies are JSON. Errors use the existing `httperr` message envelope. Refusals on login routes use one generic message (`server.sftp.login_failed`) whatever the cause (core rule 3).

## Called by the SFTP server (bearer: SFTP service token `gpsftps_…`)

### `POST /sftp/v1/login`

Request: `{"username": "alice" | "alice+survival" | "alice+mods/survival", "password": "…"}` or `{"username": "…", "publicKey": "<authorized_keys line>"}` (the SFTP server has already verified the client's signature for that key), plus `"remoteAddr": "203.0.113.7:51234"`.

Response 200: `{"sessionToken": "gpsftp_…", "expiresAt": "…", "user": "alice", "target": null | {"namespace": "mods", "server": "survival"}}`.
Response 401: generic. Response 429: rate limited (same limiter keys as `/auth/login`: username and source address).

The API splits `username` at the last `+` (R4). A target login also asks the file-access check (research R8) whether the user may list the target; failure is the same 401.

### `POST /sftp/v1/sessions/check`

Request: `{"sessionTokens": ["gpsftp_…", …]}` (up to 500). Response: `{"revoked": ["gpsftp_…", …]}`. Called every 30 s.

### `POST /sftp/v1/sessions/end`

Request: `{"sessionTokens": ["gpsftp_…", …]}` (up to 500). Response 204, no body. Revokes each listed session issued for the service token's cluster, in one transaction; unknown, foreign-cluster and already revoked tokens are ignored without error. The SFTP server calls it when it turns away a session after login (per-user session cap reached, SSH handshake failed) so the API session does not linger. Not audited.

## Called by the SFTP server (bearer: SFTP session token `gpsftp_…`)

Only these routes accept a session token (guard like `MCPTokenGuard`); `cluster` is forced to the token's cluster:

- `GET /sftp/v1/servers` (root listing: `{namespace, server, write}` items from the file-access check's `VisibleServers` for the token's cluster; `write` is the same check's `create` decision for that server, so the endpoint refuses an upload when the file is opened)
- `GET /servers/{name}/files/{list,stat,download}`
- `POST /servers/{name}/files/{mkdir,rename}`, `DELETE /servers/{name}/files/delete`
- `PUT /servers/{name}/files/stream?path=…&offset=N` (OD-6)

`stat`, `rename` and `stream` are new and also mounted for the dashboard session; every file route, old and new, is authorized by the file-access check below; the dashboard Files tab uploads through `stream` (OD-6). The multipart `files/upload` route stays for one release. On a stopped or asleep server every file route first ensures file-only access (R3) and answers `503` with `server.files.starting` after 60 s if it is not ready.

## Called by the dashboard (cookie session)

| Route | Permission | Purpose |
|---|---|---|
| `GET /users/me/sftp` | signed in | address, port, host key fingerprint, username, keys, password state |
| `POST /users/me/sftp/keys` | signed in | add key `{name, publicKey}` |
| `DELETE /users/me/sftp/keys/{id}` | signed in | remove key, revokes sessions using it |
| `POST /users/me/sftp/password` | signed in | generate or regenerate `{length: 20..40}` → `{password}` shown once |
| `DELETE /users/me/sftp/password` | signed in | revoke |
| `GET /users/{id}/sftp`, `DELETE /users/{id}/sftp/keys/{keyId}`, `DELETE /users/{id}/sftp/password` | `users:manage` | admin view and revoke |
| `GET /clusters/{name}/sftp` | `cluster:manage` | endpoint (address, host key fingerprint) and active service tokens of one cluster (drawer view, see below) |
| `PUT /clusters/{name}/sftp` | `cluster:manage` | set a remote cluster's endpoint `{address, hostKeyFingerprint}` → `200 {cluster, address, hostKeyFingerprint, updatedAt}`; refused for the panel's own cluster (`server.sftp.endpoint_local`) |
| `POST /clusters/{name}/sftp-token` | `cluster:manage` | new service token → `201 {id, cluster, prefix, createdAt, lastUsedAt, revokedAt, token}`; `token` is shown once; 409 `server.sftp.token_limit_reached` at 2 active tokens |
| `DELETE /clusters/{name}/sftp-token/{id}` | `cluster:manage` | revoke one active token of that cluster; 404 `server.sftp.token_not_found` otherwise; `204` |

`GET /clusters/{name}/sftp` response, the same for every cluster:

```json
{
  "cluster": "edge-1",
  "local": false,
  "address": "sftp.edge-1.example.com:2222",
  "hostKeyFingerprint": "SHA256:…",
  "tokens": [{"id": 12, "cluster": "edge-1", "prefix": "gpsftps_ab12…", "createdAt": "…", "lastUsedAt": null, "revokedAt": null}]
}
```

`address` and `hostKeyFingerprint` are `null` when unset. `tokens` lists active tokens only, newest first. A token has no name column: the drawer labels it by `prefix` and `createdAt`. Unknown clusters answer 404 (`server.not_found`) on every cluster route.

Session-token requests are refused on all of these (403 `server.forbidden`). The panel's own cluster (`local: true`) reads its address from Helm values (`sftp.publicAddress`, passed to the API as `GAMEPLANE_SFTP_PUBLIC_ADDRESS`) and its fingerprint from the host key Secret (`GAMEPLANE_SFTP_HOST_KEY_SECRET`, key `host_key`). Remote clusters read both from the stored endpoint (`PUT`), see data-model.md (`sftp_cluster_endpoints`). The fingerprint is the OpenSSH `SHA256:` form; `PUT` rejects anything else (`server.sftp.fingerprint_invalid`) and addresses that are not `host` or `host:port` (`server.sftp.endpoint_invalid`).

## Agent (mTLS, `/v1/targets/{uid}` prefix as today)

| Route | Notes |
|---|---|
| `GET /files/stat?path=` | `Entry` for one path; 404 if missing |
| `POST /files/rename` `{from, to, overwrite}` | same server only; refuses dotfiles, root, crossing the data root |
| `PUT /files/stream?path=&offset=N` | raw body; `offset=0` writes a new temp file; `offset=N` copies the first N bytes of the existing file first (N must be ≤ its size); commit by rename on success; free-space check before writing; limit `files.maxUploadSize` (OD-6) |

## File-access check (API-internal, research R8)

Every route above and the existing Files tab routes call one helper; none tests a permission key directly.

```go
// api/internal/rbac/fileaccess.go
type FileAction string // list, stat, read, download, create, write, upload, rename, mkdir, delete

type FileTarget struct{ Cluster, Namespace, Server string; Paths []string }

type FileAccess interface {
    // VisibleServers returns the servers on cluster whose files the user may list.
    VisibleServers(ctx context.Context, user *auth.User, cluster string) ([]ServerRef, error)
    // Allow decides one action on target; rename passes {from, to}.
    Allow(ctx context.Context, user *auth.User, action FileAction, target FileTarget) error
    // FilterEntries drops listing entries the user may not list (identity today).
    FilterEntries(ctx context.Context, user *auth.User, target FileTarget, names []string) ([]string, error)
}
```

Today's implementation: list/stat/read/download → `servers:read`; create/write/upload/rename/mkdir/delete → `servers:write` or ownership/collaboration, as the Files tab routes check now. Spec 039 replaces the implementation, not the interface.
