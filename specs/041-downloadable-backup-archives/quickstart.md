# Quickstart: Downloadable backup archives

Manual walk for valgul on a dev cluster (`make dev-up`) once the implementation PR is deployed. Automated coverage is the `backup-archives` E2E bucket.

1. **Setup**: a restic destination for namespace `games`; a Minecraft Java server `mc` with a world; a second empty Minecraft server `mc2`; a Valheim server `vh`.
2. **Download tar.gz**: `mc` → Backups → Back up now → wait Succeeded → Download → `.tar.gz`. Unpack: `gameplane-backup.json` names `mc`, its template and game version; the world is under `volumes/<name>/` (or `data/` for an old backup). Audit log shows the download.
3. **Download zip**: same backup → Download → `.zip`; opens in the OS archive tool.
4. **Viewer**: a user with only `backups:read` can download; a user without it sees no backups.
5. **CSI**: a volume-snapshot backup shows no Download action.
6. **Upload restore (Gameplane archive)**: delete the world on `mc2`; `mc2` → Backups → Upload backup → pick the `.tar.gz` from step 2 → check summary shows `mc`, game version → Confirm → `mc2` restarts with `mc`'s world. No new entry in `mc2`'s backup list.
7. **Wrong game**: upload the same file on `vh` → refused before `vh` stops, naming both games.
8. **Plain archive**: zip a world folder by hand, upload on `mc2` with "not a Gameplane archive" and game Minecraft Java → warning shown → Confirm → world present; permissions reset note shown for zip.
9. **Unsafe archive**: upload a tar.gz with `../evil` → rejected, `mc2` untouched and still running.
10. **Interrupted upload**: start a large upload, close the tab → Restore ends Failed, `mc2` unchanged and still running, staging PVC gone (`kubectl get pvc -n games`).
11. **No storage class**: on a cluster without a default class and `backups.uploadStagingStorageClass` unset → upload refused before the server stops, naming the Helm value.
12. **Remote cluster**: repeat step 2 for a server on a remote cluster.
