import { useResourcePermissions, resourceCan, type ResourceTarget } from "@/lib/resourceTarget";
import { Button, Table } from "@heroui/react";
import { formatRelative } from "@/lib/utils";
import type { Backup } from "@/types";
import { PhaseChip } from "@/components/ui/PhaseChip";
import { useOptionalIntl } from "@/lib/i18n/format";

interface Props {
  target?: ResourceTarget;
  permissions?: string[];
  backup: Backup;
  showServer: boolean;
  onSelect: (b: Backup) => void;
  onRestore: (b: Backup) => void;
}

export function BackupRow({ backup, showServer, onSelect, onRestore, target, permissions: explicitPermissions }: Props) {
  const intl = useOptionalIntl();
  const permissions = useResourcePermissions(explicitPermissions);
  const restorable =
    backup.status?.phase === "Succeeded" && Boolean(backup.status.snapshotID);
  return (
    <Table.Row
      className="cursor-pointer"
      onAction={() => onSelect(backup)}
    >
      <Table.Cell className="font-mono text-xs">{backup.metadata.name}{target && <div className="text-muted">{intl.formatMessage({ id: "web.components.backups.backup_row.cluster_namespace", defaultMessage: "{cluster} / {namespace}", description: "Backup row: cluster and namespace of the backup's target server; {cluster} is the cluster name, {namespace} is the namespace" }, { cluster: target.cluster, namespace: target.namespace })}</div>}</Table.Cell>
      <Table.Cell>
        {showServer && backup.spec.serverRef.name}
      </Table.Cell>
      <Table.Cell>
        <PhaseChip phase={backup.status?.phase} />
      </Table.Cell>
      <Table.Cell className="font-mono">{backup.status?.size ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</Table.Cell>
      <Table.Cell>
        {formatRelative(backup.status?.completionTime, intl)}
      </Table.Cell>
      <Table.Cell className="text-right">
        <Button
          size="sm"
          variant="outline"
          isDisabled={!restorable || !resourceCan(permissions, "backups:restore")}
          onPress={() => onRestore(backup)}
        >{intl.formatMessage({ id: "web.components.backups.backup_row.restore", defaultMessage: "Restore", description: "Backup row: button in each backup table row that starts restoring that backup; keep it short" })}</Button>
      </Table.Cell>
    </Table.Row>
  );
}
