# Feature Specification: Downloadable backup archives

**Feature Branch**: `spec-041/downloadable-backup-archives`

**Created**: 2026-11-25

**Status**: Draft (open decisions OD-1 to OD-6 await valgul, see `OPEN-DECISIONS.md`)

**Input**: Issue [#1050](https://github.com/GameplanePanel/Gameplane/issues/1050) "Backup destination configuration page is hard to find", second half: "it seems to only allow me to add a restic backup destination, not 'normal' on-disc/on-volume backups, is that by design?", and the reporter's follow-up: "Could the feature of saving backups as archives (ideally user downloadable) be added? Ideally a user could use that same archive to restore the backup, as well." A maintainer answered that this needs architecture changes in both the panel and the operator. The first half of #1050 (where destinations are managed in the dashboard) is handled separately and is not part of this spec.

## Background

Today a backup is one of two things:

- **Restic snapshot** (`Backup.spec.strategy: restic-snapshot`, the default and the only kind **Back up now** makes). The operator runs a restic Job against the server's data volume and writes into a repository named by a destination: a Kubernetes Secret labeled `gameplane.local/backup-destination=true` holding the repository URL and password (`api/internal/handlers/destinations.go`). Backup Jobs mount no volume for the repository, so in practice the repository is remote (S3, REST server, SFTP and so on); a `local:` path would land inside the Job's throwaway filesystem.
- **CSI volume snapshot** (`strategy: volume-snapshot`). The storage driver snapshots the data volume. No repository is used, and recovery always creates a new server (`operator/api/v1alpha1/restore_types.go`).

Restore (`Restore` CRD) takes a stored `Backup` and either overwrites an existing server (restic) or creates a new one (CSI). There is no way to:

1. keep backups as plain files on a disk or volume the cluster already has, without running a restic repository;
2. get a backup out of Gameplane as one file, for example to keep a copy at home, move a world to another host, or share it;
3. bring a backup file into Gameplane and restore a server from it.

The Files tab can already download chosen folders as a ZIP through the agent (`files/archive`), but that is a live copy of the running server, not a consistent backup, and it has no restore path.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Download a backup as one file (Priority: P1)

A server admin opens a server's **Backups** tab, picks a finished backup and clicks **Download**. Their browser saves one archive file that contains the server's data exactly as it was captured, plus a small description file saying which server, game, template and game version it came from and when.

**Why this priority**: This is the core of the request ("ideally user downloadable") and is useful on its own: off-site copies, moving a world to another host, debugging a world locally.

**Independent Test**: Make a backup of a Minecraft server, download it, unpack it on a laptop: the world folder and configs match the server's data at backup time, and the description file names the server and game version.

**Acceptance Scenarios**:

1. **Given** a finished backup the user may download (OD-2, OD-6), **When** they click **Download**, **Then** the browser starts saving a file named after the server and backup time, and the download shows its progress and total size when the size is known.
2. **Given** a backup of 10 GiB, **When** it is downloaded on a normal connection, **Then** the transfer is not cut off by the panel's request timeout and the file is complete.
3. **Given** a CSI volume-snapshot backup, **When** the user views it, **Then** no **Download** action is offered and a short note says snapshot backups stay inside the storage system.
4. **Given** a user without download permission, **When** they view the backups list, **Then** no **Download** action is shown and the download address refuses them.
5. **Given** any download, **When** an admin opens the audit log, **Then** an entry names the user, the server, the backup and that it was downloaded.
6. **Given** a backup on a remote cluster, **When** the user downloads it, **Then** it works the same as on the local cluster.

---

### User Story 2 - Keep backups as plain archive files on a volume (Priority: P1)

An admin who runs no restic repository adds an **archive volume** destination for a cluster (OD-1): either an existing volume claim or a new one with a chosen storage class and size, for example an NFS share or a second disk. They point **Back up now** and backup schedules at it. Each backup becomes one archive file on that volume. Retention removes old files as it removes old restic snapshots today.

**Why this priority**: This answers "is it by design that only restic is allowed?" and gives people an on-disk backup with no extra service to run. It also gives uploaded archives (Story 3) a place to live.

**Independent Test**: Add an archive volume destination, run **Back up now** on a server, mount the same volume in a debug pod: one archive file is there and unpacks to the server's data. Add a schedule with keep-last 2, let it fire three times: two files remain.

**Acceptance Scenarios**:

1. **Given** an archive volume destination, **When** a backup runs, **Then** it uses the same quiesce, power policy and volume coverage rules as a restic backup, and the backup's size is shown in the list.
2. **Given** a schedule with retention on an archive volume, **When** retention trims, **Then** the matching archive files are deleted and the volume's free space goes up.
3. **Given** a backup is deleted in the dashboard, **When** deletion finishes, **Then** its archive file is gone from the volume; if the file cannot be removed, the backup shows why instead of disappearing silently.
4. **Given** the archive volume is full, **When** a backup runs, **Then** the backup fails with a message that says the destination is out of space, no partial file is left behind as a valid backup, and the server keeps running.
5. **Given** a backup made to an archive volume, **When** the user restores it onto the same or another server in the same namespace, **Then** the restore works like a restic restore today.

---

### User Story 3 - Restore a server from an uploaded archive (Priority: P2)

A user who downloaded a backup earlier (or who has a world from another host, OD-4) opens a server's **Backups** tab, clicks **Upload backup**, picks the file and, once the upload is checked, restores it onto the server.

**Why this priority**: The reporter asked for it and it closes the loop, but downloads and on-volume backups are useful without it.

**Independent Test**: Download a backup from server A, delete A's world, upload the file to server A and restore: the world is back. Upload the same file to a new empty server B of the same game and restore: B starts with A's world.

**Acceptance Scenarios**:

1. **Given** a Gameplane archive of the same game, **When** the user uploads it, **Then** it appears as a backup marked **Uploaded** with its original server, game version and capture time, and it can be restored with the normal restore flow (OD-5).
2. **Given** an archive made for a different game than the target server, **When** the user tries to restore it, **Then** the restore is refused before the server is stopped, with a message naming both games.
3. **Given** an archive from an older game or template version, **When** the user restores it, **Then** the dashboard warns about the version difference and asks to confirm, as restore does today for a version mismatch.
4. **Given** a file that is not a valid archive, is truncated, or holds paths that escape the data folder (`..`, absolute paths, links pointing outside), **When** it is uploaded, **Then** it is rejected with a plain reason and nothing is written to any server.
5. **Given** an upload larger than the configured limit, **When** the user starts it, **Then** it is refused up front with the limit shown.
6. **Given** an interrupted upload, **When** the connection drops, **Then** no backup entry is left behind and the partial data is cleaned up.
7. **Given** any upload and any restore from it, **When** an admin opens the audit log, **Then** both are recorded with user, server and file name.

---

### Edge Cases

- A backup is deleted, pruned by retention or fails while someone is downloading it: the download either finishes from a consistent copy or stops with an error; it never delivers a file that looks complete but is not.
- Two people download the same backup at once: both get complete files.
- A restic backup whose repository is unreachable: **Download** fails with the destination error, not a generic one.
- Backups taken with volume policy `all-data` span several volumes: the archive keeps each volume under its own folder as recorded in the capture manifest, and restore puts each back on the matching volume.
- A restore from an uploaded archive whose manifest lists a volume the target server does not have: refused before any change, naming the missing volume.
- The archive volume destination is removed while backups still point at it: removal is refused while backups or schedules use it, as for restic destinations.
- The archive volume uses storage that only one node can mount and the server runs on another node: backup and restore still work (the work runs where the volume can be mounted) or fail with a message that says why.
- Panel login page: nothing about backups or archives is exposed there (core rule 3).

## Requirements *(mandatory)*

### Functional Requirements

**Archive volume destinations**

- **FR-001**: Admins MUST be able to add, view and remove a destination of a new kind, **archive volume**, per cluster and namespace, next to restic destinations (OD-1).
- **FR-002**: An archive volume destination MUST either use an existing volume claim in the namespace or create one from a chosen storage class and size.
- **FR-003**: **Back up now**, backup schedules and modpack "back up first" MUST accept an archive volume destination wherever they accept a restic destination today.
- **FR-004**: A backup to an archive volume MUST produce exactly one archive file per backup, in the format chosen in OD-3, holding the captured data and a description file (manifest) with: format version, server name and UID, namespace, cluster, template name and revision, game version, capture time, volume coverage from the capture manifest, and the Gameplane version that wrote it.
- **FR-005**: A backup to an archive volume MUST follow the existing quiesce, power policy (`preserve`, `stop-copy-restart`) and volume policy (`primary-only`, `all-data`) rules and record the same capture manifest as a restic backup.
- **FR-006**: An archive file MUST only count as a finished backup once it is completely written; a failed or interrupted backup MUST NOT leave a file that a later restore or download treats as valid.
- **FR-007**: Retention and deletion MUST remove the archive file of each removed backup, and a backup whose file cannot be removed MUST stay visible with the reason.
- **FR-008**: Restoring a backup from an archive volume MUST support overwriting an existing server, with the same suspend, fence and resume behaviour as a restic restore.
- **FR-009**: Removing an archive volume destination MUST be refused while a backup or schedule refers to it. Removing the destination MUST NOT delete a volume claim it did not create; deleting the files on a claim it did create needs an explicit second confirmation.

**Download**

- **FR-010**: Users with download permission (OD-6) MUST be able to download each finished backup that OD-2 makes downloadable as one archive file in the OD-3 format, including the manifest of FR-004.
- **FR-011**: Downloads MUST stream: the panel MUST NOT hold a whole archive in memory or require free disk space equal to its size, and MUST NOT cut long downloads off with its normal request timeout.
- **FR-012**: Downloads MUST work for servers on remote clusters through the existing cluster gateway.
- **FR-013**: CSI volume-snapshot backups MUST NOT offer a download.
- **FR-014**: Every download attempt, allowed or refused, MUST be written to the audit log with user, cluster, namespace, server, backup and outcome.

**Upload and restore from upload**

- **FR-015**: Users with upload permission (OD-6) MUST be able to upload an archive for a server; an accepted upload becomes a backup of that server marked as uploaded, stored as decided in OD-5.
- **FR-016**: The panel MUST check an uploaded archive before accepting it: valid format, complete, no path escaping its volume folders (absolute paths, `..`, links pointing outside), no device files, and total unpacked size under the configured limit. A failed check MUST reject the upload with a plain reason and keep nothing.
- **FR-017**: Uploads MUST be limited by a configurable maximum size (Helm value), refused up front when the declared size is larger, and stopped when the received size passes it.
- **FR-018**: Restore from an uploaded archive MUST refuse a target server of another game, refuse a manifest volume the target lacks, and warn and ask for confirmation on an older template or game version, all before the target server is stopped.
- **FR-019**: Which archives without a Gameplane manifest are accepted is set by OD-4.
- **FR-020**: Every upload and every restore from an uploaded archive MUST be written to the audit log.

**Permissions and visibility**

- **FR-021**: Download and upload MUST be authorized as decided in OD-6, per cluster and namespace like the existing `backups:*` permissions.
- **FR-022**: The dashboard MUST hide **Download** and **Upload backup** from users who lack the permission; the API MUST refuse them regardless of the dashboard.
- **FR-023**: Archive downloads expose every file of the server, including configs that may hold passwords. The download confirmation MUST say so.

**Dashboard**

- **FR-024**: Dashboard changes (Download action, Upload backup flow, archive volume destination form, **Uploaded** badge, storage kind shown per backup) MUST be designed in `design.fig` before code (core rule 1). Design prompts go to `/mnt/project-files/notes/design-prompts/`.
- **FR-025**: The API MUST stay a gateway: backups to archive volumes, retention, deletion and restores from archives MUST run through the operator's reconcilers and CRDs (core rule 10); the API only streams bytes for download and upload.

### Key Entities

- **Archive volume destination**: a per-namespace destination of kind archive volume, naming a volume claim (existing or created), and whether Gameplane created it.
- **Backup archive**: one file per backup on an archive volume (or produced on demand for download), holding the server's captured data per volume plus the manifest.
- **Archive manifest**: the description file inside each archive: format version, source server identity, template and game version, capture time, volume coverage, writer version.
- **Uploaded backup**: a Backup created from an uploaded archive; carries "uploaded" provenance (uploader, original file name, upload time) instead of a schedule origin, and the manifest values read from the archive.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can download a finished backup in 2 clicks from the server's Backups tab.
- **SC-002**: A 10 GiB backup downloads completely, with a checksum matching the source data, on both a local and a remote cluster.
- **SC-003**: Downloading a backup never raises the panel's memory use by more than 64 MiB, whatever the archive size.
- **SC-004**: A server restored from its own downloaded-then-uploaded archive has the same files (names, contents, permissions) as at backup time.
- **SC-005**: 100% of crafted unsafe archives in the test set (path escape, absolute paths, outside links, device files, truncated, over the size limit) are rejected with nothing written to any server.
- **SC-006**: An admin with no restic repository can set up on-volume backups for a server in under 3 minutes.
- **SC-007**: Every download, upload and restore from upload appears in the audit log.

## Assumptions

- The Destinations page work from #1050 part 1 lands separately; this spec adds a destination kind and relies on wherever destinations are managed at that time.
- Upload size limit: a new Helm value `backups.maxUploadSize`, default `20Gi`, the same default as `files.maxUploadSize`.
- Archives on an archive volume are plain, unencrypted files; whoever can read the volume can read the backups. Restic stays the choice for encrypted, deduplicated, off-site backups. The destination form says so.
- Archive volume backups are not deduplicated or incremental: each backup is a full copy.
- Only same-namespace restores, as today; cross-namespace and cross-cluster moves go through download and upload.
- Spec 039 (per-server access, not yet merged) lists "backup archive download" as an action with no baseline control. This spec adds the namespace-level permissions; when 039 is built it maps them to per-server actions (for example `backups.download`, `backups.upload`). The two specs are reconciled when 039 is implemented, as for spec 040.
- CSI volume-snapshot backups are out of scope for download and upload.
