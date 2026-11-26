# API contract: Downloadable backup archives

All routes take the usual cluster and namespace selection (`resolveCluster`, `resolveNS`). Errors use `httperr` message keys added to the shared language packs (`i18n/`). Every route writes an audit row (FR-008, FR-016): action, user, cluster, namespace, server, backup or restore, format, file name (upload), outcome.

## `GET /backups/{name}:download?format=tar.gz|zip`

Permission: `backups:read` (OD-6).

1. Load Backup; 404 if missing. 409 `backup.download.not_ready` unless `phase=Succeeded`. 400 `backup.download.snapshot_strategy` for `volume-snapshot`.
2. 429 `backup.download.busy` when the namespace already has `backups.maxConcurrentExports` active exports.
3. Create `BackupExport{backupRef, backupUID, format}`; wait up to 120 s for `Ready` (fail 504 `backup.download.start_timeout` and delete it).
4. Respond `200`, `Content-Type: application/gzip` or `application/zip`, `Content-Disposition: attachment; filename="<server>-<capturedAt UTC yyyyMMdd-HHmmss>.<ext>"`, `X-Gameplane-Estimated-Bytes: <status.size>` when known, no `Content-Length`; stream from the worker.
5. On worker error mid-stream: abort the connection (no clean end), so the browser marks the download failed (FR-006).
6. Always delete the BackupExport when the request ends.

Remote clusters: steps 1–6 run in the cluster gateway; the panel API forwards the request with the target identity (`gatewayprotocol`, new `BackupExportTarget{Target, Backup, BackupUID, Format}`).

## `POST /restores:upload`

Permission: `backups:restore` (OD-6). Query: `server` (required), `format=tar.gz|zip` (required), `fileName` (required), `template` (required when the file is a plain archive; the dashboard asks for it when the user ticks "not a Gameplane archive"). Body: raw file bytes; `Content-Length` required.

1. 413 `restore.upload.too_large` when `Content-Length` > `backups.maxUploadSize`; 411 without `Content-Length`.
2. 404 when the server is missing; 409 `restore.upload.server_busy` if a backup, restore or wipe owns it.
3. Create `Restore{serverRef, upload:{fileName, format, declaredBytes, confirmedTemplate}}`; wait up to 120 s for `Receiving` (or `Failed` with `NoStagingStorageClass` → 409 `restore.upload.no_storage_class`).
4. Stream the body to the receiver, stopping at `declaredBytes`; abort on overflow.
5. Respond `202 {"restore": "<name>"}` once the receiver has the whole file. Checks continue in the operator; the client polls `GET /restores/{name}`.
6. If the client disconnects, delete the Restore (operator cleans up; server never touched).

## `POST /restores/{name}:confirm`

Permission: `backups:restore`. Body `{"generation": <observed generation>}`. 409 unless phase `AwaitingConfirmation` and generation matches. Sets `spec.upload.confirmed=true`.

## `GET /restores/{name}`

Existing generic route; now includes `status.upload` (kind, manifest summary, unpacked bytes, warnings).

## Web client (`web/src/lib/api.ts`)

- `backupDownloadURL(cluster, ns, name, format)`: link target for a plain browser download (cookie auth, `credentials: "include"` as today).
- `uploadRestore(cluster, ns, server, file, {format, template?}, onProgress)`: XHR with upload progress (fetch has none).
- `confirmRestore(cluster, ns, name, generation)`.

## Worker endpoints (in-cluster only, mTLS with the agent CA)

- Export worker `GET /export/{exportUID}` → archive stream. One successful transfer, then exit 0.
- Receiver `PUT /upload/{restoreUID}` → raw body, `Content-Length` required. Returns 200 after the file is on the staging volume; exits after writing the check summary to `/dev/termination-log`.
- Path UIDs must match the worker's own `EXPORT_UID` / `RESTORE_UID`; anything else is 404.
