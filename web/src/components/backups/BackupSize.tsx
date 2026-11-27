import { formatBytes, parseQuantityToBytes } from "@/lib/utils";
import type { Backup } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";

// The data a backup holds (spec 042 FR-031): status.dataSize when recorded. A Restic
// backup taken before dataSize existed already reported the bytes it processed as
// status.size, so that is its data size too. Only an older volume snapshot has no
// data size: its size is the volume capacity (OD-8).
export function backupDataSize(backup: Backup): string | undefined {
  const status = backup.status;
  if (status?.dataSize) return status.dataSize;
  if (backup.spec.strategy !== "volume-snapshot" && status?.size) return status.size;
  return undefined;
}

export function BackupSize({ backup }: { backup: Backup }) {
  const intl = useOptionalIntl();
  const status = backup.status;
  const dataSize = backupDataSize(backup);
  const hasDataSize = Boolean(dataSize);
  const main = hasDataSize
    ? formatBytes(parseQuantityToBytes(dataSize))
    : status?.size ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" });
  return <>
    {main}
    {!hasDataSize && status?.size != null && <div className="text-xs text-muted">
      {intl.formatMessage({ id: "web.backups.volume_size", defaultMessage: "volume size", description: "Label under the size of a backup taken before data size was recorded: the size shown is the volume capacity, not the data stored" })}
    </div>}
    {status?.bytesAdded != null && <div className="text-xs text-muted">
      {intl.formatMessage({ id: "web.backups.bytes_added", defaultMessage: "{size} added", description: "Packed data added to the backup repository by this run. The size includes its unit." }, { size: formatBytes(parseQuantityToBytes(status.bytesAdded)) })}
    </div>}
  </>;
}
