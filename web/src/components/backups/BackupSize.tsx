import type { Backup } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";

export function BackupSize({ backup }: { backup: Backup }) {
  const intl = useOptionalIntl();
  return <>
    {backup.status?.size ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
    {backup.status?.bytesAdded != null && <div className="text-xs text-muted">
      {intl.formatMessage({ id: "web.backups.bytes_added", defaultMessage: "{size} added", description: "Packed data added to the backup repository by this run. The size includes its unit." }, { size: backup.status.bytesAdded })}
    </div>}
  </>;
}
