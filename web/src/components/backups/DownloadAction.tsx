import { Button } from "@heroui/react";
import { Download } from "lucide-react";
import { canDownloadBackups, isDownloadableBackup, isSnapshotBackup } from "@/lib/backupArchives";
import { useOptionalIntl } from "@/lib/i18n/format";
import type { Backup } from "@/types";

interface Props {
  backup: Backup;
  permissions: readonly string[];
  onDownload: (backup: Backup) => void;
}

// DownloadAction is the per-row Download control of a backup list. Restic
// backups that finished get an outline button. Volume-snapshot backups show a
// dash with a note, because they stay inside the storage system (FR-002).
// Rows that are not downloadable yet, and users without backups:read, get
// nothing (FR-018).
export function DownloadAction({ backup, permissions, onDownload }: Props) {
  const intl = useOptionalIntl();
  if (!canDownloadBackups(permissions)) return null;
  if (isSnapshotBackup(backup)) {
    const note = intl.formatMessage({ id: "web.components.backups.download_action.snapshot_note", defaultMessage: "Snapshot backups stay inside the storage system.", description: "Backups list: tooltip on the dash shown instead of a Download button for a volume snapshot backup" });
    return <span title={note} aria-label={note} className="px-2 text-muted">{intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</span>;
  }
  if (!isDownloadableBackup(backup)) return null;
  return (
    <Button size="sm" variant="outline" onPress={() => onDownload(backup)}>
      <Download className="h-4 w-4" aria-hidden="true" />
      {intl.formatMessage({ id: "web.components.backups.download_action.download", defaultMessage: "Download", description: "Backups list: row button that opens the download dialog for one backup; keep it short" })}
    </Button>
  );
}
