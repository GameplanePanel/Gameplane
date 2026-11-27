import type { ArchiveFormat, Backup, Restore } from "@/types";
import { resourceCan } from "@/lib/resourceTarget";

// Volume-snapshot backups stay inside the storage system: they have no restic
// snapshot to export, so they are never offered as a download (spec 041 FR-002).
export function isSnapshotBackup(backup: Backup): boolean {
  return backup.spec.strategy === "volume-snapshot";
}

// A backup can be downloaded once it finished and recorded its restic snapshot.
export function isDownloadableBackup(backup: Backup): boolean {
  return !isSnapshotBackup(backup) && backup.status?.phase === "Succeeded" && Boolean(backup.status.snapshotID);
}

// Downloading needs backups:read (FR-017); uploading needs backups:restore.
export function canDownloadBackups(permissions: readonly string[]): boolean {
  return resourceCan(permissions, "backups:read");
}

export function canUploadBackups(permissions: readonly string[]): boolean {
  return resourceCan(permissions, "backups:restore");
}

// archiveFormatFromName detects the upload format from the file name; null for
// anything that is neither a .tar.gz (or .tgz) nor a .zip file.
export function archiveFormatFromName(fileName: string): ArchiveFormat | null {
  const lower = fileName.toLowerCase();
  if (lower.endsWith(".zip")) return "zip";
  if (lower.endsWith(".tar.gz") || lower.endsWith(".tgz")) return "tar.gz";
  return null;
}

// restoreSourceName is what a restore row shows as its source: the backup name,
// or the uploaded file name for an upload restore.
export function restoreSourceName(restore: Restore): string {
  return restore.spec.backupRef?.name ?? restore.spec.upload?.fileName ?? "";
}
