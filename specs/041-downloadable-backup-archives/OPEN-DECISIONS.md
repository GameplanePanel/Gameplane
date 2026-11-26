# Open decisions: spec 041 downloadable backup archives

Only a decision valgul answers is written here as settled. OD-1 to OD-6 were raised on 2026-11-25 while writing `spec.md` and asked as decision cards in the spec thread. All six are settled (2026-11-26). The OD-1 answer removed the archive volume, so OD-5 was asked again with new options.

## OD-1: Where do plain archive backups live? (SETTLED: B, valgul 2026-11-26)

- A. New destination kind **archive volume**: a volume claim per namespace (existing, or created from a storage class such as NFS or a second disk). Each backup is one plain archive file on it; download and upload use it too. (Recommended: answers "normal on-disk/on-volume backups" with nothing extra to run, and gives uploads a home.)
- B. No new storage: restic stays the only store; add download (restic snapshot turned into an archive on the fly) and restore from upload only.
- C. A, plus plain archives on S3-compatible object storage.

**Settled (valgul picked B on the decision card, 2026-11-26):** no new storage. Restic stays the only store for backups; the feature adds download (a restic snapshot turned into an archive while it streams) and restore from upload. User Story 2 and the archive volume destination are dropped from the spec.

## OD-2: Which backups can be downloaded? (SETTLED: A, valgul 2026-11-26)

CSI volume snapshots are excluded in every option (they never leave the storage system).

- A. Archive volume backups and restic backups (a restic snapshot is turned into an archive while it streams). (Recommended: every existing backup becomes downloadable, no migration needed.)
- B. Archive volume backups only.

**Settled (valgul picked A on the decision card, 2026-11-26):** archive and restic backups. With OD-1 B there are no archive volume backups, so this means every finished restic backup can be downloaded. CSI volume snapshots cannot.

## OD-3: Which archive format? (SETTLED: C, valgul 2026-11-26)

- A. `.tar.gz` (keeps Linux file permissions and owners; opens on Linux, macOS and current Windows 11). (Recommended: a restore puts permissions back exactly.)
- B. `.zip` (opens everywhere, but loses Unix permissions, so restore must reset them).
- C. Stored as `.tar.gz`, user picks `.tar.gz` or `.zip` at download; upload accepts both.

**Settled (valgul picked C on the decision card, 2026-11-26):** the user picks `.tar.gz` or `.zip` when downloading, and upload accepts both. With OD-1 B nothing is stored as an archive file (backups stay restic snapshots), so `.tar.gz` is the default choice at download and the format the manifest is written for. A `.zip` download or upload carries no Unix permissions, so restoring one resets permissions to the server's defaults.

## OD-4: Which uploaded archives are accepted? (SETTLED: A, valgul 2026-11-26)

- A. Gameplane archives (with manifest) and plain archives of a server's data folder (for example a world from another host). A plain archive needs the user to confirm the target game, and the dashboard warns that Gameplane cannot check it. (Recommended: covers moving in from another host, which is a common ask.)
- B. Only archives Gameplane made (manifest required).

**Settled (valgul picked A on the decision card, 2026-11-26):** Gameplane archives with a manifest and plain archives of a server's data folder are both accepted. A plain archive needs the user to confirm the target game, and the dashboard warns that Gameplane cannot check it. The path, link, device-file and size checks apply to both.

## OD-5: Where does an uploaded archive go before restore? (SETTLED: B, valgul 2026-11-26, asked again after OD-1)

The first version (A: store it on an archive volume; B: stream it straight into the server) was withdrawn on 2026-11-26 because OD-1 B removed the archive volume.

- A. It is saved as a new snapshot in the namespace's restic destination, listed as an **Uploaded** backup, and restored with the normal restore flow (can be restored again later; retention never prunes it). (Recommended: one restore path through the operator, auditable, and a failed restore can be retried without uploading again.)
- B. It streams straight into the target server, overwriting its data, and is not kept. Works without a restic destination.

**Settled (valgul picked B on the decision card, 2026-11-26):** an upload goes straight into the target server and is not kept as a backup; it needs no restic destination and never appears in the backups list. Restoring it again means uploading it again. The safety checks (FR-011) must still pass before any server data changes, so the upload may be held only temporarily while it is checked and written, and that temporary copy is always deleted afterwards, whether the restore succeeds or fails. How it is held is a planning question, not a new kept copy.

## OD-6: Who may download and upload? (SETTLED: B, valgul 2026-11-26)

- A. Two new permissions, `backups:download` and `backups:upload`, separate from `backups:read` and `backups:restore`; admins get them by default, other roles only when granted. (Recommended: a download exposes every file, including configs with passwords; an upload can replace a server's data.)
- B. Reuse existing ones: download needs `backups:read`, upload needs `backups:restore`.

**Settled (valgul picked B on the decision card, 2026-11-26):** no new permissions. Downloading a backup needs `backups:read`; uploading an archive needs `backups:restore`, in the backup's cluster and namespace. Anyone who can view backups can download them, so the download confirmation warns that the archive holds every server file, including configs with passwords.
