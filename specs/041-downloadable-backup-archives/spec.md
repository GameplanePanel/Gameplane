# Feature Specification: Downloadable backup archives

**Feature Branch**: `spec-041/downloadable-backup-archives`

**Created**: 2026-11-25

**Status**: Draft (all open decisions OD-1 to OD-7 settled 2026-11-26, see `OPEN-DECISIONS.md`)

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

A server admin opens a server's **Backups** tab, picks a finished backup and clicks **Download**, choosing `.tar.gz` (default) or `.zip` (OD-3). Their browser saves one archive file that contains the server's data exactly as it was captured, plus a small description file saying which server, game, template and game version it came from and when.

**Why this priority**: This is the core of the request ("ideally user downloadable") and is useful on its own: off-site copies, moving a world to another host, debugging a world locally.

**Independent Test**: Make a restic backup of a Minecraft server, download it as `.tar.gz` and as `.zip`, unpack it on a laptop: the world folder and configs match the server's data at backup time, and the description file names the server and game version.

**Acceptance Scenarios**:

1. **Given** a finished restic backup (OD-2) and a user with `backups:read` (OD-6), **When** they click **Download**, **Then** the browser starts saving a file named after the server and backup time, and the download shows its progress and total size when the size is known.
2. **Given** a backup of 10 GiB, **When** it is downloaded on a normal connection, **Then** the transfer is not cut off by the panel's request timeout and the file is complete.
3. **Given** a CSI volume-snapshot backup, **When** the user views it, **Then** no **Download** action is offered and a short note says snapshot backups stay inside the storage system.
4. **Given** a user without `backups:read` for the server's namespace, **When** they try to reach a backup, **Then** they see no backups and the download address refuses them.
5. **Given** any download, **When** an admin opens the audit log, **Then** an entry names the user, the server, the backup and that it was downloaded.
6. **Given** a backup on a remote cluster, **When** the user downloads it, **Then** it works the same as on the local cluster.

---

### User Story 2 - Restore a server from an uploaded archive (Priority: P2)

A user who downloaded a backup earlier, or who has a world from another host (OD-4), opens a server's **Backups** tab, clicks **Upload backup**, picks the file picks `.tar.gz` or `.zip` (OD-3) and, once the upload is checked, restores it onto the server.

**Why this priority**: The reporter asked for it and it closes the loop, but downloads are useful without it.

**Independent Test**: Download a backup from server A, delete A's world, upload the file to server A and restore: the world is back, and no new entry appears in A's backups list. Upload the same file to a new empty server B of the same game and restore: B starts with A's world.

**Acceptance Scenarios**:

1. **Given** a Gameplane archive of the same game, **When** the user uploads it, **Then** after the checks pass the dashboard shows its original server, game version and capture time from the manifest, asks the user to confirm the overwrite, and writes it straight into the server (OD-5); it is not kept and does not appear in the backups list.
2. **Given** a Gameplane archive made for a different game than the target server, **When** the user tries to restore it, **Then** the restore is refused before the server is stopped, with a message naming both games.
3. **Given** a Gameplane archive from an older game or template version, **When** the user restores it, **Then** the dashboard warns about the version difference and asks to confirm, as restore does today for a version mismatch.
4. **Given** a file that is not a valid archive, is truncated, or holds paths that escape the data folder (`..`, absolute paths, links pointing outside), **When** it is uploaded, **Then** it is rejected with a plain reason and nothing is written to any server.
5. **Given** an upload larger than the configured limit, **When** the user starts it, **Then** it is refused up front with the limit shown.
6. **Given** an interrupted upload, **When** the connection drops, **Then** the server's data is unchanged, the server is back in its previous power state, and the partial upload is deleted.
7. **Given** a plain archive without a Gameplane manifest (OD-4), **When** the user uploads it, **Then** they must confirm which game it is for, and the dashboard warns that Gameplane cannot check its contents beyond the safety checks.
8. **Given** a `.zip` upload, **When** it is restored, **Then** file permissions are reset to the server's defaults, and the dashboard says so before restore.
9. **Given** any upload and any restore from it, **When** an admin opens the audit log, **Then** both are recorded with user, server and file name.

---

### Edge Cases

- A backup is deleted, pruned by retention or fails while someone is downloading it: the download either finishes from a consistent copy or stops with an error; it never delivers a file that looks complete but is not.
- Two people download the same backup at once: both get complete files.
- A restic backup whose repository is unreachable: **Download** fails with the destination error, not a generic one.
- Backups taken with volume policy `all-data` span several volumes: the archive keeps each volume under its own folder as recorded in the capture manifest, and restore puts each back on the matching volume. A plain archive (OD-4) restores onto the primary data volume only.
- A restore from an uploaded archive whose manifest lists a volume the target server does not have: refused before any change, naming the missing volume.
- Panel login page: nothing about backups or archives is exposed there (core rule 3).

## Requirements *(mandatory)*

### Functional Requirements

**Storage**

- **FR-001**: Restic MUST stay the only store for backups; this feature adds no new destination kind (OD-1). CSI volume-snapshot backups keep working as today.

**Download**

- **FR-002**: Users MUST be able to download each finished restic backup as one archive file (OD-2). CSI volume-snapshot backups MUST NOT offer a download.
- **FR-003**: The user MUST be able to choose `.tar.gz` (default) or `.zip` for each download (OD-3).
- **FR-004**: Every archive MUST contain the backup's data, with each captured volume under its own folder as recorded in the capture manifest, and a manifest file with: format version, server name and UID, namespace, cluster, template name and revision, game version, capture time, volume coverage, the restic snapshot ID and the Gameplane version that wrote it.
- **FR-005**: Downloads MUST stream: the panel MUST NOT hold a whole archive in memory or need free disk space equal to its size, and MUST NOT cut long downloads off with its normal request timeout.
- **FR-006**: A download MUST either deliver the complete archive or end with a visible error; it MUST NOT deliver a file that looks complete but is not, including when the backup is deleted or pruned meanwhile.
- **FR-007**: Downloads MUST work for servers on remote clusters through the existing cluster gateway.
- **FR-008**: Every download attempt, allowed or refused, MUST be written to the audit log with user, cluster, namespace, server, backup, format and outcome.

**Upload and restore from upload**

- **FR-009**: Users MUST be able to upload a `.tar.gz` or `.zip` archive for a server (OD-3). An accepted upload is written straight into the target server and is not kept (OD-5): it creates no backup entry and needs no restic destination.
- **FR-010**: Uploads MUST accept Gameplane archives (with manifest) and plain archives of a server's data folder (OD-4). A plain archive MUST require the user to confirm the target game, with a warning that Gameplane cannot check its contents, and restores onto the primary data volume only.
- **FR-011**: The panel MUST check every uploaded archive before any server data changes: valid format, complete, no path escaping its volume folders (absolute paths, `..`, links pointing outside), no device files, and total unpacked size under the configured limit. A failed check MUST reject the upload with a plain reason and keep nothing.
- **FR-012**: Uploads MUST be limited by a configurable maximum size (Helm value), refused up front when the declared size is larger, and stopped when the received size passes it.
- **FR-013**: Restore from a Gameplane archive MUST refuse a target server of another game, refuse a manifest volume the target lacks, and warn and ask for confirmation on an older template or game version, all before the target server is stopped.
- **FR-014**: Restore from an uploaded archive MUST suspend, fence and resume the target server the same way a restic restore does today, and MUST leave the server's data unchanged if the upload is interrupted or a check fails. The upload MUST be unpacked and checked on a temporary volume from a storage class before anything is copied onto the server (OD-7); the temporary volume MUST be deleted when the restore ends, on success and on failure. If the final copy itself fails, the restore MUST end Failed, say that the server's data may be partly replaced, and leave the server stopped; the dashboard MUST say this risk before the restore starts. With no storage class available the upload MUST be refused before the server stops.
- **FR-015**: Restoring a `.zip` archive MUST reset file permissions to the server's defaults, and the dashboard MUST say so before the restore starts.
- **FR-016**: Every upload and every restore from an uploaded archive MUST be written to the audit log with user, server and original file name.

**Permissions**

- **FR-017**: Downloading a backup MUST require `backups:read`, and uploading an archive MUST require `backups:restore`, in the backup's or server's cluster and namespace (OD-6). No new permission is added.
- **FR-018**: The dashboard MUST hide **Download** and **Upload backup** from users who lack the permission; the API MUST refuse them regardless of the dashboard.
- **FR-019**: Because anyone who can view backups can download them, the download confirmation MUST say that the archive holds every server file, including configs that may hold passwords.

**Dashboard and architecture**

- **FR-020**: Dashboard changes (Download action with format choice, Upload backup flow, game confirmation for plain archives, overwrite confirmation) MUST be designed in `design.fig` before code (core rule 1). Design prompts go to `/mnt/project-files/notes/design-prompts/`.
- **FR-021**: The API MUST stay a gateway: restores from uploads MUST run through the operator's reconcilers and CRDs (core rule 10); the API only streams bytes for download and upload.

### Key Entities

- **Backup archive**: one `.tar.gz` or `.zip` file produced on demand from a restic backup, holding the captured data per volume plus the manifest.
- **Archive manifest**: the description file inside each Gameplane archive: format version, source server identity, template and game version, capture time, volume coverage, restic snapshot ID, writer version.
- **Uploaded archive**: an archive a user uploads for a server, either a Gameplane archive or a plain data-folder archive; it is written into that server and not kept (OD-5).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can download a finished backup in 3 clicks or fewer from the server's Backups tab, format choice included.
- **SC-002**: A 10 GiB backup downloads completely, with a checksum matching the source data, on both a local and a remote cluster.
- **SC-003**: Downloading a backup never raises the panel's memory use by more than 64 MiB, whatever the archive size.
- **SC-004**: A server restored from its own downloaded-then-uploaded archive has the same files (names, contents, and for `.tar.gz` permissions) as at backup time.
- **SC-005**: 100% of crafted unsafe archives in the test set (path escape, absolute paths, outside links, device files, truncated, over the size limit) are rejected with nothing written to any server.
- **SC-006**: Every download, upload and restore from upload appears in the audit log.

## Assumptions

- The Destinations page work from #1050 part 1 lands separately; this spec adds no destination kind.
- Upload size limit: a new Helm value `backups.maxUploadSize`, default `20Gi`, the same default as `files.maxUploadSize`.
- Upload staging: Helm value `backups.uploadStagingStorageClass`, empty by default (use the cluster's default storage class).
- A downloaded archive is a plain, unencrypted file; once it leaves Gameplane, keeping it safe is the user's job.
- Only same-namespace restores, as today; cross-namespace and cross-cluster moves go through download and upload.
- Spec 039 (per-server access, not yet merged) lists "backup archive download" as an action with no baseline control. This spec reuses `backups:read` and `backups:restore` (OD-6); when 039 is built it decides whether download and upload get their own per-server actions. The two specs are reconciled when 039 is implemented, as for spec 040.
- CSI volume-snapshot backups are out of scope for download and upload.
