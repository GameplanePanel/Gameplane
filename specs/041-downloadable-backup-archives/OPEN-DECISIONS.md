# Open decisions: spec 041 downloadable backup archives

Only a decision valgul answers is written here as settled. OD-1 to OD-6 were raised on 2026-11-25 while writing `spec.md` and asked as decision cards in the spec thread. All six are open.

## OD-1: Where do plain archive backups live? (OPEN)

- A. New destination kind **archive volume**: a volume claim per namespace (existing, or created from a storage class such as NFS or a second disk). Each backup is one plain archive file on it; download and upload use it too. (Recommended: answers "normal on-disk/on-volume backups" with nothing extra to run, and gives uploads a home.)
- B. No new storage: restic stays the only store; add download (restic snapshot turned into an archive on the fly) and restore from upload only.
- C. A, plus plain archives on S3-compatible object storage.

## OD-2: Which backups can be downloaded? (OPEN)

CSI volume snapshots are excluded in every option (they never leave the storage system).

- A. Archive volume backups and restic backups (a restic snapshot is turned into an archive while it streams). (Recommended: every existing backup becomes downloadable, no migration needed.)
- B. Archive volume backups only.

## OD-3: Which archive format? (OPEN)

- A. `.tar.gz` (keeps Linux file permissions and owners; opens on Linux, macOS and current Windows 11). (Recommended: a restore puts permissions back exactly.)
- B. `.zip` (opens everywhere, but loses Unix permissions, so restore must reset them).
- C. Stored as `.tar.gz`, user picks `.tar.gz` or `.zip` at download; upload accepts both.

## OD-4: Which uploaded archives are accepted? (OPEN)

- A. Gameplane archives (with manifest) and plain archives of a server's data folder (for example a world from another host). A plain archive needs the user to confirm the target game, and the dashboard warns that Gameplane cannot check it. (Recommended: covers moving in from another host, which is a common ask.)
- B. Only archives Gameplane made (manifest required).

## OD-5: Where does an uploaded archive go? (OPEN)

- A. It is stored as an uploaded backup on an archive volume destination of that namespace, then restored with the normal restore flow (can be restored again later, shows in the list, retention never prunes it). (Recommended: auditable, retryable, one restore path.)
- B. It streams straight into the target server, overwriting its data, and is not kept.

## OD-6: Who may download and upload? (OPEN)

- A. Two new permissions, `backups:download` and `backups:upload`, separate from `backups:read` and `backups:restore`; admins get them by default, other roles only when granted. (Recommended: a download exposes every file, including configs with passwords; an upload can replace a server's data.)
- B. Reuse existing ones: download needs `backups:read`, upload needs `backups:restore`.
