import { formatBytes, parseQuantityToBytes } from "@/lib/utils";
import type { Backup } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";

// Main value is the data size recorded at capture (spec 042 FR-031). Backups taken
// before dataSize existed show their volume size instead, labelled as such (OD-8).
export function BackupSize({ backup }: { backup: Backup }) {
  const intl = useOptionalIntl();
  const status = backup.status;
  const hasDataSize = Boolean(status?.dataSize);
  const main = hasDataSize
    ? formatBytes(parseQuantityToBytes(status?.dataSize))
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
