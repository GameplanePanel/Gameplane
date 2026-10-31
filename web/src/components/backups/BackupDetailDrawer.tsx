import { useResourceClient, useResourceTarget, resourceKey, useResourcePermissions, resourceCan, type ResourceTarget } from "@/lib/resourceTarget";
import type { ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Drawer, Button } from "@heroui/react";
import { RotateCcw, Trash2 } from "lucide-react";

import { formatRelative } from "@/lib/utils";
import type { Backup } from "@/types";
import { PhaseChip } from "@/components/ui/PhaseChip";
import { ErrorBanner } from "./ErrorBanner";
import { useOptionalIntl } from "@/lib/i18n/format";
import { useConditionMessage } from "@/lib/i18n/conditionMessage";

interface Props {
  target?: ResourceTarget;
  permissions?: string[];
  name: string | null;
  ns?: string;
  onClose: () => void;
  onRestore: (backup: Backup) => void;
}

export function BackupDetailDrawer({ name, ns, onClose, onRestore, target: explicitTarget, permissions: explicitPermissions }: Props) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name: name ?? "", namespace: ns }, explicitTarget);
  const resourceClient = useResourceClient(resourceTarget);
  const { Backups } = resourceClient;
  const permissions = useResourcePermissions(explicitPermissions);
  const qc = useQueryClient();
  const open = name !== null;
  const { data: backup, error } = useQuery({
    queryKey: resourceKey(resourceTarget, "backup", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Backups.get(name!, ns),
    enabled: open,
    refetchInterval: 5000,
  });
  const remove = useMutation({
    mutationFn: () => Backups.remove(name!, ns),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "backups", ns) });
      onClose();
    },
  });

  const restorable =
    backup?.status?.phase === "Succeeded" && Boolean(backup.status.snapshotID);
  const consistency = useConditionMessage(backup?.status?.conditions?.find((c) => c.type === "Consistency"));
  const manifest = backup?.status?.captureManifest;

  return (
    <Drawer
      isOpen={open}
      onOpenChange={(o) => {
        if (!o) onClose();
      }}
    >
      <Drawer.Backdrop>
        <Drawer.Content placement="right" className="me-4">
          <Drawer.Dialog className="flex flex-col w-[440px] h-[760px] p-0">
            <Drawer.Header className="shrink-0 border-b border-border p-5 flex flex-row items-center justify-between gap-3">
              <div className="space-y-1">
                <Drawer.Heading className="text-base font-semibold">{intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.backup_details", defaultMessage: "Backup details", description: "Backup details drawer: heading at the top of the side panel for one backup; the backup name is shown below it" })}</Drawer.Heading>
                <div className="font-mono text-xs text-muted">{name}</div>
              </div>
              <Button
                variant="ghost"
                size="sm"
                isDisabled={!restorable || !resourceCan(permissions, "backups:restore")}
                onPress={() => backup && onRestore(backup)}
                className="gap-1.5 shrink-0"
                aria-label={intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.restore_backup", defaultMessage: "Restore backup", description: "Backup details drawer: screen-reader name of the restore button in the drawer header, read instead of its visible word Restore; not visible on screen" })}
              >
                <RotateCcw className="h-4 w-4" />{intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.restore", defaultMessage: "Restore", description: "Backup details drawer: visible text of the restore button in the drawer header and of the primary restore button in the footer; keep it short" })}</Button>
            </Drawer.Header>

            <Drawer.Body className="min-h-0 space-y-4 overflow-y-auto p-5 text-sm">
              {error && <ErrorBanner err={error} />}
              {backup && (
                <>
                  <Field label={intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.phase", defaultMessage: "Phase", description: "Backup details drawer: field label above the backup's current phase, shown as a status chip" })}>
                    <PhaseChip phase={backup.status?.phase} />
                  </Field>
                  <Field label={intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.server", defaultMessage: "Server", description: "Backup details drawer: field label above the name of the server the backup belongs to" })}>{backup.spec.serverRef.name}</Field>
                  <Field label={intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.consistency", defaultMessage: "Achieved consistency", description: "Backup details: actual consistency reported by the controller, independent of successful copying" })}>
                    {consistency ?? intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.consistency_unknown", defaultMessage: "Consistency has not been reported.", description: "Backup details: no achieved-consistency condition is available" })}
                  </Field>
                  <Field label={intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.coverage", defaultMessage: "Volume coverage", description: "Backup details: persistent claims recorded before the backup copy began" })}>
                    {manifest?.state === "Recorded" ? (
                      <div className="space-y-3">
                        {manifest.recordedAt && <div className="text-xs text-muted">{intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.coverage_recorded", defaultMessage: "Recorded: {time}", description: "Backup details: timestamp of the immutable capture manifest" }, { time: manifest.recordedAt })}</div>}
                        <p className="text-xs text-muted">{intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.coverage_scope", defaultMessage: "Only the primary data claim is copied. Rebuildable is a declaration, not proof that recovery can reconstruct its contents.", description: "Backup details: explains the current single-volume backup scope and rebuildable declarations" })}</p>
                        <ul className="space-y-3">
                          {manifest.volumes?.map((volume) => (
                            <li key={volume.claimName} className="rounded border border-border p-2 space-y-1">
                              <div className="font-mono text-xs break-all">{volume.claimName}</div>
                              <div>{volume.included
                                ? intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.included", defaultMessage: "Included", description: "Backup volume: this claim is selected for copying; success is reported separately" })
                                : intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.not_covered", defaultMessage: "Not covered", description: "Backup volume: contents of this claim are not included" })}</div>
                              <div className="text-xs text-muted">{intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.classification", defaultMessage: "{role, select, data {Data} rebuildable {Rebuildable} other {Unclassified}}", description: "Backup volume: optional declaration from the module or server at capture time" }, { role: volume.classification })}</div>
                              {volume.mountPaths?.map((path) => <div key={path} className="font-mono text-xs break-all">{path}</div>)}
                              {volume.reason === "RetainedUnmountedClaim" && <div className="text-xs text-muted">{intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.retained_unmounted", defaultMessage: "Retained claim; not mounted in the captured workload.", description: "Backup volume: an older server-owned claim was retained but was not part of the observed pod layout" })}</div>}
                            </li>
                          ))}
                        </ul>
                      </div>
                    ) : intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.legacy_coverage", defaultMessage: "Capture-time volume metadata is unavailable. Inspect the original backup and module configuration before recovery.", description: "Backup details: older backups have no reliable volume manifest, so the current template is not used to invent coverage" })}
                  </Field>
                  <Field label={intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.snapshot_id", defaultMessage: "Snapshot ID", description: "Backup details drawer: field label above the snapshot identifier of the backup; a dash is shown when there is none" })}>
                    <span className="font-mono text-xs">{backup.status?.snapshotID ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</span>
                  </Field>
                  <Field label={intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.size", defaultMessage: "Size", description: "Backup details drawer: field label above the backup's size; a dash is shown when the size is unknown" })}>{backup.status?.size ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</Field>
                  <Field label={intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.started", defaultMessage: "Started", description: "Backup details drawer: field label above the time the backup started, shown in relative time" })}>
                    {formatRelative(backup.status?.startTime, intl)}
                    {backup.status?.startTime && (
                      <span className="pl-2 font-mono text-xs text-muted">
                        {backup.status.startTime}
                      </span>
                    )}
                  </Field>
                  <Field label={intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.completed", defaultMessage: "Completed", description: "Backup details drawer: field label above the time the backup completed, shown in relative time" })}>
                    {formatRelative(backup.status?.completionTime, intl)}
                    {backup.status?.completionTime && (
                      <span className="pl-2 font-mono text-xs text-muted">
                        {backup.status.completionTime}
                      </span>
                    )}
                  </Field>
                </>
              )}
              {remove.error && <ErrorBanner err={remove.error} />}
            </Drawer.Body>

            <Drawer.Footer className="shrink-0 flex items-center justify-end gap-2 border-t border-border p-4">
              <Button
                variant="danger"
                size="sm"
                isDisabled={!backup || remove.isPending || !resourceCan(permissions, "backups:write")}
                onPress={() => remove.mutate()}
              >
                <Trash2 className="h-3.5 w-3.5" />
                {remove.isPending ? intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.deleting", defaultMessage: "Deleting…", description: "Backup detail drawer: delete button label while the delete request is pending" }) : intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.delete", defaultMessage: "Delete", description: "Backup detail drawer: delete button label" })}
              </Button>
              <Button
                variant="primary"
                size="sm"
                isDisabled={!restorable || !resourceCan(permissions, "backups:restore")}
                onPress={() => backup && onRestore(backup)}
              >{intl.formatMessage({ id: "web.components.backups.backup_detail_drawer.restore", defaultMessage: "Restore", description: "Backup details drawer: visible text of the restore button in the drawer header and of the primary restore button in the footer; keep it short" })}</Button>
            </Drawer.Footer>
          </Drawer.Dialog>
        </Drawer.Content>
      </Drawer.Backdrop>
    </Drawer>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="space-y-1">
      <div className="text-xs uppercase tracking-wide text-muted">{label}</div>
      <div className="text-sm">{children}</div>
    </div>
  );
}
