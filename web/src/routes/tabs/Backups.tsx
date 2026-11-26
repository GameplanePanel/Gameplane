import { useResourceAccess, useResourceClient, useResourceTarget, resourceKey, useResourcePermissions, resourceCan } from "@/lib/resourceTarget";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, CalendarClock, Clock, HardDrive, Upload } from "lucide-react";
import { Button, Table } from "@heroui/react";
import { StatCard } from "@/components/ui/StatCard";
import { PhaseChip, backupAwaitingSnapshot } from "@/components/ui/PhaseChip";

import { APIError } from "@/lib/api";
import { canUploadBackups, restoreSourceName } from "@/lib/backupArchives";
import { useBackupDestinations } from "@/lib/destinations";
import { backupReadOptions, isAuthorizationError, useBackupAccessKey } from "@/lib/backupReadOptions";
import { formatBytes, formatRelative, formatRelativeFuture, parseQuantityToBytes } from "@/lib/utils";
import { ErrorBanner } from "@/components/backups/ErrorBanner";
import { ScheduleForm } from "@/components/backups/ScheduleForm";
import { RestoreDialog } from "@/components/backups/RestoreDialog";
import { BackupDownloadDialog } from "@/components/backups/BackupDownloadDialog";
import { DownloadAction } from "@/components/backups/DownloadAction";
import { UploadBackupDialog } from "@/components/backups/UploadBackupDialog";
import { BackupDetailDrawer } from "@/components/backups/BackupDetailDrawer";
import { BackupSize } from "@/components/backups/BackupSize";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import type { Backup, BackupSchedule } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";

function scheduleMissingOrChanged(error: unknown): boolean {
  return error instanceof APIError && (error.status === 404 || error.status === 409);
}

export function BackupsTab({ name, ns, onOpenBackupSettings }: { name: string; ns?: string; onOpenBackupSettings?: () => void }) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const { Backups } = resourceClient;
  const qc = useQueryClient();
  const permissions = useResourcePermissions();
  const access = useResourceAccess();
  // The access snapshot belongs to the selected cluster and namespace. Keep
  // denied results isolated from later grants and from other resource scopes.
  const accessKey = useBackupAccessKey(resourceTarget, permissions);
  const canReadBackups = resourceCan(permissions, "backups:read");
  const canReadSchedules = resourceCan(permissions, "schedules:read");
  const [scheduleEditor, setScheduleEditor] = useState<"new" | BackupSchedule | null>(null);
  const [deleting, setDeleting] = useState<BackupSchedule | null>(null);
  const [restoringBackup, setRestoringBackup] = useState<Backup | null>(null);
  const [selectedBackup, setSelectedBackup] = useState<string | null>(null);
  const [downloadingBackup, setDownloadingBackup] = useState<Backup | null>(null);
  const [uploading, setUploading] = useState(false);

  const { data: backupsData, error: backupRequestError } = useQuery({
    queryKey: [...resourceKey(resourceTarget, "backups"), "access", accessKey],
    ...backupReadOptions(canReadBackups, 5000),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Backups.list(ns),
  });
  const { data: schedulesData, error: scheduleRequestError } = useQuery({
    queryKey: [...resourceKey(resourceTarget, "schedules"), "access", accessKey],
    ...backupReadOptions(canReadSchedules, false),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Schedules.list(ns),
  });
  const { data: restoresData, error: restoreRequestError } = useQuery({
    queryKey: [...resourceKey(resourceTarget, "restores"), "access", accessKey],
    ...backupReadOptions(canReadBackups, 5000),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Restores.list(ns),
  });
  const { data: destinations = [], error: destinationError, isSuccess: destinationsLoaded } = useBackupDestinations(resourceTarget, permissions);
  const backupsDenied = !canReadBackups || isAuthorizationError(backupRequestError);
  const schedulesDenied = !canReadSchedules || isAuthorizationError(scheduleRequestError);
  const restoresDenied = !canReadBackups || isAuthorizationError(restoreRequestError);
  const backupDenied = intl.formatMessage({ id: "web.routes.tabs.backups.backups_access_denied", defaultMessage: "You do not have permission to view backups in this cluster and namespace.", description: "Backups tab: access denied message for the selected server's backups" });
  const scheduleDenied = intl.formatMessage({ id: "web.routes.tabs.backups.schedules_access_denied", defaultMessage: "You do not have permission to view schedules in this cluster and namespace.", description: "Backups tab: access denied message for the selected server's schedules" });
  const restoreDenied = intl.formatMessage({ id: "web.routes.tabs.backups.restores_access_denied", defaultMessage: "You do not have permission to view restores in this cluster and namespace.", description: "Backups tab: access denied message for the selected server's restores" });
  const backupsError = backupsDenied ? new APIError(403, backupDenied) : backupRequestError;
  const schedulesError = schedulesDenied ? new APIError(403, scheduleDenied) : scheduleRequestError;
  const restoresError = restoresDenied ? new APIError(403, restoreDenied) : restoreRequestError;
  // A denied refetch must not keep displaying a previous successful response.
  const backups = backupsDenied ? undefined : backupsData;
  const schedules = schedulesDenied ? undefined : schedulesData;
  const restores = restoresDenied ? undefined : restoresData;
  const canManageSchedules = !schedulesDenied && resourceCan(permissions, "schedules:write");
  const removeSchedule = useMutation({
    mutationFn: (schedule: BackupSchedule) => resourceClient.Schedules.remove(schedule.metadata.name, ns, schedule.metadata),
    onError: async error => {
      if (scheduleMissingOrChanged(error)) {
        await qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "schedules") });
        setDeleting(null);
      }
    },
    onSuccess: () => {
      setDeleting(null);
      void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "schedules") });
      void qc.invalidateQueries({ queryKey: ["fleet", "schedules"] });
    },
  });

  // Multi-destination picking is handled on the dashboard's Backups page;
  // here we only act when there's exactly one configured destination.
  const lone = destinations.length === 1 ? destinations[0] : null;

  const createNow = useMutation({
    mutationFn: () =>
      Backups.create(
        {
          serverRef: { name },
          repoRef: { name: lone!.name, key: "repo" },
        },
        ns,
      ),
    onSuccess: () => Promise.all([qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "backups", ns) }), qc.invalidateQueries({ queryKey: ["fleet"] })]),
  });

  const backupNowDisabled = !resourceCan(permissions, "backups:write") || !lone || createNow.isPending;
  const backupNowHint =
    destinationsLoaded && destinations.length === 0
      ? intl.formatMessage({ id: "web.routes.tabs.backups.backup_now_hint_no_destination", defaultMessage: "No backup destination configured. See this server’s Settings → Scheduled backups.", description: "Backups tab: directs the user to the server settings section with the destination management link" })
      : destinations.length > 1
        ? intl.formatMessage({ id: "web.routes.tabs.backups.backup_now_hint_many_destinations", defaultMessage: "Multiple destinations configured — use the Backups page to pick one.", description: "Backups tab: hint under the Back up now button when several backup destinations exist; points to the dashboard Backups page to choose one" })
        : undefined;

  const serverBackups = backups?.items.filter((b) => b.spec.serverRef.name === name) ?? [];
  const serverSchedules = schedules?.items.filter((s) => s.spec.serverRef.name === name) ?? [];
  const serverRestores = restores?.items.filter((r) => r.spec.serverRef.name === name) ?? [];

  // Summary tiles, derived from the same lists the table renders.
  const succeeded = serverBackups.filter((b) => b.status?.phase === "Succeeded");
  const completions = succeeded
    .map((b) => b.status?.completionTime)
    .filter((t): t is string => Boolean(t))
    .sort();
  const lastBackupAt = completions[completions.length - 1];
  const nextRuns = serverSchedules
    .filter((s) => !s.spec.suspend)
    .map((s) => s.status?.nextScheduleTime)
    .filter((t): t is string => Boolean(t))
    .sort();
  const nextBackupAt = nextRuns[0];
  const totalBytes = succeeded.reduce((acc, b) => acc + parseQuantityToBytes(b.status?.size), 0);
  const unavailable = intl.formatMessage({ id: "web.routes.tabs.backups.unavailable", defaultMessage: "Unavailable", description: "Backups: stat value shown when the backup or schedule data could not be loaded" });
  const emptyValue = intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" });

  return (
    <div className="space-y-6 p-6">
      {backupsError && <ErrorBanner err={backupsError} />}
      {schedulesError && <ErrorBanner err={schedulesError} />}
      {restoresError && <ErrorBanner err={restoresError} />}
      {destinationError && <ErrorBanner err={destinationError} />}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <StatCard
          label={intl.formatMessage({ id: "web.routes.tabs.backups.last_backup", defaultMessage: "Last backup", description: "Backups tab: stat card label for the time of the most recent backup; the value shows that time relative to now, or Unavailable" })}
          value={backupsError ? unavailable : formatRelative(lastBackupAt, intl)}
          icon={<Clock size={16} />}
          accent="primary"
        />
        <StatCard
          label={intl.formatMessage({ id: "web.routes.tabs.backups.next_backup", defaultMessage: "Next backup", description: "Backups tab: stat card label for when the next scheduled backup will run; the value shows a relative time or a dash" })}
          value={schedulesError ? unavailable : nextBackupAt ? formatRelativeFuture(nextBackupAt, intl) : emptyValue}
          icon={<CalendarClock size={16} />}
          accent="success"
        />
        <StatCard
          label={intl.formatMessage({ id: "web.routes.tabs.backups.backups", defaultMessage: "Backups", description: "Backups tab: stat card label for the number of backups; also the section heading above the backup list" })}
          value={backups ? serverBackups.length : emptyValue}
          icon={<Archive size={16} />}
          accent="violet"
        />
        <StatCard
          label={intl.formatMessage({ id: "web.routes.tabs.backups.total_size", defaultMessage: "Total logical size", description: "Sum of the logical sizes reported by successful backups" })}
          value={backupsError ? unavailable : totalBytes ? formatBytes(totalBytes) : emptyValue}
          icon={<HardDrive size={16} />}
          accent="warning"
        />
      </div>

      <section>
        <div className="flex items-center justify-between pb-3">
          <h2 className="text-sm text-foreground/60">{intl.formatMessage({ id: "web.routes.tabs.backups.schedules", defaultMessage: "Schedules", description: "Backups tab: section heading above the list of backup schedules for this server" })}</h2>
          <Button
            size="sm"
            variant="outline"
            onPress={() => setScheduleEditor("new")}
            isDisabled={scheduleEditor !== null || !resourceCan(permissions, "schedules:write")}
          >{intl.formatMessage({ id: "web.routes.tabs.backups.new_schedule", defaultMessage: "New schedule", description: "Backups tab: button that opens the form to create a new backup schedule; keep it short" })}</Button>
        </div>
        {scheduleEditor && (scheduleEditor === "new" || canManageSchedules) && (
          <ScheduleForm
            key={scheduleEditor === "new" ? "new" : scheduleEditor.metadata.name}
            existing={scheduleEditor === "new" ? undefined : scheduleEditor}
            serverName={name}
            ns={ns}
            onClose={() => setScheduleEditor(null)}
          />
        )}
        {serverSchedules.length > 1 && (
          <div className="mb-3 rounded border border-warning/30 bg-warning/5 px-3 py-2 text-xs text-foreground/60">{intl.formatMessage({ id: "web.routes.tabs.backups.this_server_has_multiple_backup", defaultMessage: "This server has multiple backup schedules — consider consolidating to avoid duplicate backups.", description: "Backups tab: warning note shown when the server has more than one backup schedule, advising to combine them to avoid duplicate backups" })}</div>
        )}
        <div className="space-y-1 pt-2">
          {serverSchedules.map((s) => {
            const serverOwner = s.metadata.ownerReferences?.find(owner =>
              owner.controller && owner.kind === "GameServer" && owner.name === name);
            const canOpenSettings = access?.canWrite && onOpenBackupSettings &&
              (!resourceTarget.uid || serverOwner?.uid === resourceTarget.uid);
            return (
              <div
                key={s.metadata.name}
                className="flex flex-wrap items-center justify-between gap-3 rounded border border-border bg-surface/30 px-4 py-2 text-sm"
              >
                <div className="flex min-w-0 flex-wrap items-center gap-3">
                  <span className="break-all font-mono">{s.metadata.name}</span>
                  <span className="text-foreground/60">{s.spec.schedule}</span>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-foreground/60">{intl.formatMessage({ id: "web.routes.tabs.backups.next", defaultMessage: "Next:", description: "Backups tab: label in front of the next run time shown on a backup schedule row" })} {formatRelativeFuture(s.status?.nextScheduleTime, intl)}</span>
                  {serverOwner ? (
                    <Button size="sm" variant="outline" isDisabled={!canOpenSettings} onPress={onOpenBackupSettings}>
                      {intl.formatMessage({ id: "web.routes.tabs.backups.managed_by_server_settings", defaultMessage: "Managed by server settings", description: "Backups tab: opens the server settings that control this schedule" })}
                    </Button>
                  ) : (
                    <>
                      <Button size="sm" variant="outline" isDisabled={!canManageSchedules || scheduleEditor !== null} onPress={() => setScheduleEditor(s)}>
                        {intl.formatMessage({ id: "web.routes.tabs.backups.edit", defaultMessage: "Edit", description: "Server Backups tab: opens the selected backup schedule for editing" })}
                      </Button>
                      <Button size="sm" variant="outline" isDisabled={!canManageSchedules || scheduleEditor !== null || !s.metadata.uid || !s.metadata.resourceVersion} onPress={() => {
                        removeSchedule.reset();
                        setDeleting(s);
                      }}>
                        {intl.formatMessage({ id: "web.routes.backups.delete", defaultMessage: "Delete", description: "Backups: button that starts deleting a schedule row, and the confirm button of the delete schedule dialog; keep it short" })}
                      </Button>
                    </>
                  )}
                </div>
              </div>
            );
          })}
          {schedules && serverSchedules.length === 0 && !scheduleEditor && (
            <p className="text-sm text-foreground/60">{intl.formatMessage({ id: "web.routes.tabs.backups.no_schedules_yet", defaultMessage: "No schedules yet.", description: "Backups tab: empty-state text in the schedules section, shown when the server has no backup schedules" })}</p>
          )}
        </div>
        {!deleting && scheduleMissingOrChanged(removeSchedule.error) && (
          <ErrorBanner err={intl.formatMessage({ id: "web.routes.tabs.backups.delete_schedule_changed", defaultMessage: "The schedule changed or was removed. Review the current schedule before deleting.", description: "Server Backups tab: the schedule changed after the user opened the delete confirmation" })} />
        )}
        {deleting && canManageSchedules && (
          <ConfirmDialog
            open
            onOpenChange={open => { if (!open) setDeleting(null); }}
            title={intl.formatMessage({ id: "web.routes.backups.delete_schedule", defaultMessage: "Delete schedule?", description: "Backups: title of the dialog asking whether to delete a schedule" })}
            description={<>
              {intl.formatMessage({ id: "web.routes.tabs.backups.delete_schedule_hint", defaultMessage: "Deleting this schedule stops future runs. Backups already taken are kept.", description: "Server Backups tab: explains the effect of deleting a schedule" })}
              {removeSchedule.error && <ErrorBanner err={removeSchedule.error} />}
            </>}
            confirmPhrase={deleting.metadata.name}
            confirmLabel={intl.formatMessage({ id: "web.routes.backups.delete", defaultMessage: "Delete", description: "Backups: button that starts deleting a schedule row, and the confirm button of the delete schedule dialog; keep it short" })}
            destructive
            busy={removeSchedule.isPending}
            onConfirm={() => removeSchedule.mutate(deleting)}
          />
        )}
      </section>

      {serverRestores.length > 0 && (
        <section>
          <h2 className="pb-3 text-sm text-foreground/60">{intl.formatMessage({ id: "web.routes.tabs.backups.recent_restores", defaultMessage: "Recent restores", description: "Backups tab: section heading above the list of recent restores; shown only when there are restores" })}</h2>
          <div className="space-y-1">
            {serverRestores.map((r) => (
              <div
                key={r.metadata.name}
                className="flex justify-between rounded border border-border bg-surface/30 px-4 py-2 text-sm"
              >
                <span className="font-mono">{restoreSourceName(r)}</span>
                <span className="text-foreground/60">
                  <PhaseChip phase={r.status?.phase} />
                  {r.status?.completionTime &&
                    <> {intl.formatMessage({ id: "web.routes.tabs.backups.restore_completed_relative", defaultMessage: "· {time}", description: "Backups: restore row suffix; {time} is how long ago the restore completed" }, { time: formatRelative(r.status.completionTime, intl) })}</>}
                </span>
              </div>
            ))}
          </div>
        </section>
      )}

      <section>
        <div className="flex items-center justify-between pb-3">
          <h2 className="text-sm text-foreground/60">{intl.formatMessage({ id: "web.routes.tabs.backups.backups", defaultMessage: "Backups", description: "Backups tab: stat card label for the number of backups; also the section heading above the backup list" })}</h2>
          {/* The disabled reason lives on a wrapping element's title
              (react-aria's Button strips a bare `title` prop) so the
              button's accessible name stays its visible text. */}
          <div className="flex items-center gap-2">
            <span title={backupNowHint}>
              <Button
                size="sm"
                onPress={() => createNow.mutate()}
                isDisabled={backupNowDisabled}
              >
                {createNow.isPending
                  ? intl.formatMessage({ id: "web.routes.tabs.backups.starting", defaultMessage: "Starting…", description: "Backups: label of the Back up now button while the backup is being started" })
                  : intl.formatMessage({ id: "web.routes.tabs.backups.back_up_now", defaultMessage: "Back up now", description: "Backups: label of the button that starts a backup immediately" })}
              </Button>
            </span>
            {canUploadBackups(permissions) && (
              <Button size="sm" variant="outline" onPress={() => setUploading(true)}>
                <Upload className="h-4 w-4" aria-hidden="true" />
                {intl.formatMessage({ id: "web.routes.tabs.backups.upload_backup", defaultMessage: "Upload backup", description: "Backups tab: button that opens the dialog for restoring this server from an uploaded .tar.gz or .zip archive; keep it short" })}
              </Button>
            )}
          </div>
        </div>
        {createNow.error && <ErrorBanner err={createNow.error} />}
        <Table.Root>
          <Table.ScrollContainer>
            <Table.Content aria-label={intl.formatMessage({ id: "web.routes.tabs.backups.backups_table", defaultMessage: "Backups table", description: "Backups tab: accessible label of the backups table, read by screen readers; not shown as text" })} keyboardNavigationBehavior="arrow">
              <Table.Header>
                <Table.Column key="name" isRowHeader>{intl.formatMessage({ id: "web.routes.tabs.backups.name", defaultMessage: "Name", description: "Backups tab: column heading for the backup name in the backups table; keep it short" })}</Table.Column>
                <Table.Column key="phase">{intl.formatMessage({ id: "web.routes.tabs.backups.phase", defaultMessage: "Phase", description: "Backups tab: column heading for the current phase of each backup in the backups table; keep it short" })}</Table.Column>
                <Table.Column key="size">{intl.formatMessage({ id: "web.routes.tabs.backups.size", defaultMessage: "Size", description: "Backups tab: column heading for the size of each backup in the backups table; keep it short" })}</Table.Column>
                <Table.Column key="completed">{intl.formatMessage({ id: "web.routes.tabs.backups.completed", defaultMessage: "Completed", description: "Backups tab: column heading for when each backup finished in the backups table; keep it short" })}</Table.Column>
                <Table.Column key="actions" id="actions" className="text-end">{intl.formatMessage({ id: "web.routes.tabs.backups.actions", defaultMessage: "Actions", description: "Backups tab: column heading for the row action buttons at the end of the backups table; keep it short" })}</Table.Column>
              </Table.Header>
              <Table.Body>
                {serverBackups.map((b) => {
                  const restorable =
                    b.status?.phase === "Succeeded" && Boolean(b.status?.snapshotID);
                  return (
                    <Table.Row
                      key={b.metadata.name}
                      className="cursor-pointer"
                      onAction={() => setSelectedBackup(b.metadata.name)}
                    >
                      <Table.Cell className="font-mono">{b.metadata.name}</Table.Cell>
                      <Table.Cell>
                        <PhaseChip phase={b.status?.phase} awaitingSnapshot={backupAwaitingSnapshot(b.status)} />
                      </Table.Cell>
                      <Table.Cell className="font-mono"><BackupSize backup={b} /></Table.Cell>
                      <Table.Cell className="font-mono text-foreground/60">
                        {formatRelative(b.status?.completionTime, intl)}
                      </Table.Cell>
                      <Table.Cell onClick={(e) => e.stopPropagation()}>
                        <div className="flex items-center justify-end gap-2">
                          <Button
                            size="sm"
                            variant="outline"
                            isDisabled={!restorable || !resourceCan(permissions, "backups:restore")}
                            onPress={() => setRestoringBackup(b)}
                          >{intl.formatMessage({ id: "web.routes.tabs.backups.restore", defaultMessage: "Restore", description: "Backups tab: row button that opens the restore dialog for one backup; disabled when the backup cannot be restored; keep it short" })}</Button>
                          <DownloadAction backup={b} permissions={permissions} onDownload={setDownloadingBackup} />
                        </div>
                      </Table.Cell>
                    </Table.Row>
                  );
                })}
              </Table.Body>
            </Table.Content>
          </Table.ScrollContainer>
        </Table.Root>
      </section>

      <BackupDownloadDialog
        backup={backupsDenied ? null : downloadingBackup}
        target={resourceTarget}
        ns={ns}
        onClose={() => setDownloadingBackup(null)}
      />
      {uploading && canUploadBackups(permissions) && (
        <UploadBackupDialog serverName={name} target={resourceTarget} ns={ns} onClose={() => setUploading(false)} />
      )}
      <RestoreDialog
        backup={backupsDenied ? null : restoringBackup}
        defaultServer={name}
        ns={ns}
        onClose={() => setRestoringBackup(null)}
      />
      <BackupDetailDrawer
        name={backupsDenied ? null : selectedBackup}
        ns={ns}
        onClose={() => setSelectedBackup(null)}
        onRestore={(b) => {
          setSelectedBackup(null);
          setRestoringBackup(b);
        }}
      />
    </div>
  );
}
