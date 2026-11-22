# Contracts: API routes for SFTP

All bodies are JSON. Errors use the existing `httperr` message envelope. Refusals on login routes use one generic message (`server.sftp.login_failed`) whatever the cause (core rule 3).

## Called by the SFTP server (bearer: SFTP service token `gpsftps_…`)

### `POST /sftp/v1/login`

Request: `{"username": "alice" | "alice+survival" | "alice+mods/survival", "password": "…"}` or `{"username": "…", "publicKey": "<authorized_keys line>"}` (the SFTP server has already verified the client's signature for that key), plus `"remoteAddr": "203.0.113.7:51234"`.

Response 200: `{"sessionToken": "gpsftp_…", "expiresAt": "…", "user": "alice", "target": null | {"namespace": "mods", "server": "survival"}}`.
Response 401: generic. Response 429: rate limited (same limiter keys as `/auth/login`: username and source address).

The API splits `username` at the last `+` (R4). A target login also checks `servers:read` on the target; failure is the same 401.

### `POST /sftp/v1/sessions/check`

Request: `{"sessionTokens": ["gpsftp_…", …]}` (up to 500). Response: `{"revoked": ["gpsftp_…", …]}`. Called every 30 s.

## Called by the SFTP server (bearer: SFTP session token `gpsftp_…`)

Only these routes accept a session token (guard like `MCPTokenGuard`); `cluster` is forced to the token's cluster:

- `GET /servers?cluster=…` (root listing; filtered as for the dashboard)
- `GET /servers/{name}/files/{list,stat,download}`
- `POST /servers/{name}/files/{mkdir,rename}`, `DELETE /servers/{name}/files/delete`
- `PUT /servers/{name}/files/stream?path=…&offset=N` (OD-6)

`stat`, `rename` and `stream` are new and also mounted for the dashboard session (same RBAC as `read` / `write`). On a stopped or asleep server every file route first ensures file-only access (R3) and answers `503` with `server.files.starting` after 60 s if it is not ready.

## Called by the dashboard (cookie session)

| Route | Permission | Purpose |
|---|---|---|
| `GET /users/me/sftp` | signed in | address, port, host key fingerprint, username, keys, password state |
| `POST /users/me/sftp/keys` | signed in | add key `{name, publicKey}` |
| `DELETE /users/me/sftp/keys/{id}` | signed in | remove key, revokes sessions using it |
| `POST /users/me/sftp/password` | signed in | generate or regenerate `{length: 20..40}` → `{password}` shown once |
| `DELETE /users/me/sftp/password` | signed in | revoke |
| `GET /users/{id}/sftp`, `DELETE /users/{id}/sftp/keys/{keyId}`, `DELETE /users/{id}/sftp/password` | `users:manage` | admin view and revoke |
| `POST /clusters/{name}/sftp-token`, `DELETE /clusters/{name}/sftp-token/{id}` | `cluster:manage` | remote endpoint service token, shown once |

Session-token requests are refused on all of these. Endpoint address and host key fingerprint per cluster come from Helm values the API reads (`sftp.publicAddress`, host key Secret) for the local cluster, and from the cluster registration for remote clusters (`PUT /clusters/{name}/sftp` `{address, hostKeyFingerprint}`).

## Agent (mTLS, `/v1/targets/{uid}` prefix as today)

| Route | Notes |
|---|---|
| `GET /files/stat?path=` | `Entry` for one path; 404 if missing |
| `POST /files/rename` `{from, to, overwrite}` | same server only; refuses dotfiles, root, crossing the data root |
| `PUT /files/stream?path=&offset=N` | raw body; `offset=0` writes a new temp file; `offset=N` copies the first N bytes of the existing file first (N must be ≤ its size); commit by rename on success; free-space check before writing; limit from OD-6 |
