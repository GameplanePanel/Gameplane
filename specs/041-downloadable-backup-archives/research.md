# Research: Downloadable backup archives

All decisions here are planning choices made inside the settled open decisions (OD-1 to OD-7, `OPEN-DECISIONS.md`). None changes what valgul decided.

## R1. Where the archive bytes come from (download)

**Decision**: `restic dump <snapshot> / --archive tar` in a short-lived worker pod, transcoded on the fly into `.tar.gz` or `.zip` by a Go helper that also writes the manifest as the first entry.

**Why**: OD-1 keeps restic as the only store, so every downloadable backup is a restic snapshot (OD-2). `restic dump` streams a whole snapshot as tar to stdout without a local copy (restic 0.17.1, `DefaultResticImage` in `operator/internal/controller/backup_controller.go:225`). Transcoding in Go gives one code path for both formats (OD-3), puts the manifest in, and maps snapshot paths to archive folders (R3).

**Rejected**: `restic dump --archive zip` directly (no manifest, and the tar path is needed anyway); restoring the snapshot to a scratch volume and zipping it (needs a volume the size of the backup, and OD-1 rejected new storage).

## R2. Which process serves the bytes, and how the API reaches it

**Decision**: a new CRD `BackupExport` (namespaced, one per download). The API creates it; a new `BackupExportReconciler` starts one worker Pod plus a ClusterIP Service, with a per-export server certificate signed by the existing agent CA (`operator/internal/controller/agent_certs.go`). The API (local cluster) or the cluster gateway (remote clusters, `api/internal/gateway/`) connects with the agent client certificate it already holds, exactly like the capture download (`api/internal/gateway/capture.go`, `api/internal/handlers/capture.go:994`). The export ends after one transfer, on client disconnect (the API deletes it), or after a TTL.

**Why**: core rule 10 keeps Pods that read repository credentials under the operator; core rule 9 favours a CRD plus Pod/Service over a custom channel; mTLS with the agent CA reuses an existing trust path, so no new secret is distributed to the API or gateway.

**Rejected**: API creating Jobs directly (bypasses the operator, needs repository Secret reads in the API); streaming through pod logs or `exec` (no back-pressure, log size limits); a long-running export Deployment (always-on cost for a rare action).

## R3. Archive layout and manifest

**Decision**: archive root holds `gameplane-backup.json` (manifest, first entry), then one folder per captured volume: `volumes/<logicalName>/…` for capture manifest format version 2, `data/…` for legacy (`formatVersion` absent). Manifest fields per FR-004, plus `formatVersion: 1` for the archive format itself. Plain archives (OD-4) have no manifest and are treated as the primary data volume's contents; a single top-level folder wrapping everything (common when zipping a folder) is stripped.

**Why**: mirrors restic snapshot roots used by restore today (`restore_controller.go` restores `/data` or `<snapshot>:/volumes`), so an archive round-trips to the same restore layout.

## R4. Format details (OD-3)

**Decision**: `.tar.gz` keeps mode, owner IDs and modification times, no device files or hard links outside the archive. `.zip` stores mode bits only; restore resets owner to the server's run-as user and permissions to `0644` files / `0755` directories (FR-015). Go `archive/zip` writes with data descriptors, so no seek is needed. No `Content-Length` (size unknown before streaming); the dashboard shows bytes received and the backup's recorded size as an estimate.

## R5. Upload path (OD-5 B, OD-7 B)

**Decision**: extend `Restore` with `spec.upload` (mutually exclusive with `spec.backupRef`, CEL). Lifecycle:

1. API receives `POST /restores:upload` and creates the Restore (phase `Pending`).
2. Operator creates a staging PVC owned by the Restore from `backups.uploadStagingStorageClass` or the cluster default (refused with `NoStagingStorageClass` when neither exists, before the server is touched), sized to the target's captured volume capacities plus the declared upload size, and a receiver Pod + Service (mTLS as R2) mounting only the staging PVC, scheduled with the target volume's node topology. Phase `Receiving`.
3. API streams the request body to the receiver. The receiver writes the file to the staging PVC, then unpacks into `staging/unpacked/` with the checks of FR-011 (reuse the path rules of `agent/internal/files/rooted_linux.go` and the extraction guards in `agent/internal/mods/mods.go`), and reports a summary (manifest or "plain", unpacked bytes, volume list) in its termination message.
4. Operator checks game/template and volumes against the target (FR-013). Refusal → `Failed`, server never stopped. Otherwise phase `AwaitingConfirmation` with the summary in status.
5. The user confirms (`POST /restores/{name}:confirm`, sets `spec.upload.confirmed: true`). Unconfirmed after 30 minutes → `Failed` and cleanup.
6. Operator runs the existing suspend and fence path (`restore_lifecycle.go`), then a copy Pod mounting staging + target volumes replaces each volume's contents, then resumes as today.
7. Cleanup deletes the receiver, copy Pod, Service and staging PVC on every terminal phase (owner references as a backstop).

**Why**: one Restore lifecycle, so suspend, fence, writer drain and resume stay identical (FR-014); everything that touches volumes runs in operator-owned Pods (core rule 10). Receiving and checking before the server stops satisfies FR-013 and keeps the server running during a long upload.

**Risk recorded by OD-7**: a failure during step 6 can leave data partly replaced; status message and dashboard text say so (FR-014).

## R6. Images

**Decision**: the download, receiver and copy workers are a new `archive-worker` subcommand of the agent binary (agent image, distroless nonroot), with the `restic` binary copied into an `emptyDir` by an init container running the configured restic image. No new image.

**Why**: the agent already owns rooted, symlink-safe filesystem code and archive writing (`agent/internal/files/archive_linux.go`); one more image would need its own signing and release wiring.

## R7. HTTP routes, permissions and timeouts

- `GET /backups/{name}:download?format=tar.gz|zip` → `backups:read` through the existing GET `backups` segment rule (`api/internal/rbac/rbac.go:346`). OD-6.
- `POST /restores:upload?server=&format=&fileName=[&template=]` and `POST /restores/{name}:confirm` → `backups:restore` through the existing non-GET `restores` rule (`rbac.go:351`). OD-6.
- Both stream routes join `isLargeTransferPath` (`api/cmd/main.go:1066`) so the 60 s timeout does not cut them.
- Size limit `backups.maxUploadSize` (default `20Gi`) checked on `Content-Length` and on bytes received (FR-017).
- Concurrency: at most `backups.maxConcurrentExports` (default 3) active `BackupExport`s per namespace; extra downloads get 429 with a retry message.

## R8. Spec 039 overlap

Spec 039 (`feat/039-per-server-access`, unmerged) lists "backup archive download" as having no baseline control. This spec reuses `backups:read` / `backups:restore` (OD-6). Its server-scoped `backups.view` and `restores.applyExisting` map onto these routes when 039 is built; whether download gets its own ID is 039's call.
