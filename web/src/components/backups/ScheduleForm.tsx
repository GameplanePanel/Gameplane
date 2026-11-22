import { useResourceClient, useResourceTarget, resourceKey, useResourcePermissions, resourceCan, type ResourceTarget } from "@/lib/resourceTarget";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Button,
  Card,
  CardContent,
  Input,
  Label,
  Description,
  Select,
  ListBox,
  ListBoxItem,
} from "@heroui/react";

import { useBackupDestinations } from "@/lib/destinations";
import { APIError } from "@/lib/api";
import { mergeValue, SettingsConflictError } from "@/lib/settingsMerge";
import type { BackupSchedule } from "@/types";
import { ErrorBanner } from "./ErrorBanner";
import { RetentionFields, buildRetention, type RetentionForm } from "./RetentionFields";
import { useOptionalIntl } from "@/lib/i18n/format";

interface Props {
  existing?: BackupSchedule;
  target?: ResourceTarget;
  permissions?: string[];
  serverName: string;
  ns?: string;
  onClose: () => void;
}

function requiresRestic(spec?: BackupSchedule["spec"]): boolean {
  return spec?.powerPolicy === "stop-copy-restart" || spec?.volumePolicy === "all-data";
}

export function ScheduleForm({ existing, serverName, ns, onClose, target: explicitTarget, permissions: explicitPermissions }: Props) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name: serverName, namespace: ns }, explicitTarget);
  const resourceClient = useResourceClient(resourceTarget);
  const { Schedules } = resourceClient;
  const permissions = useResourcePermissions(explicitPermissions);
  const qc = useQueryClient();
  const { data: destinations = [], error: destinationError, isSuccess: destinationsLoaded } = useBackupDestinations(resourceTarget, permissions);
  const [form, setForm] = useState({
    schedule: existing?.spec.schedule ?? "0 */6 * * *",
    strategy: existing?.spec.strategy ?? "restic-snapshot",
    repoName: existing?.spec.repoRef?.name ?? "",
    repoKey: existing?.spec.repoRef?.key ?? "repo",
  });
  const [retention, setRetention] = useState<RetentionForm>(existing ? existing.spec.retention ?? {} : { keepLast: 7 });
  const isVolumeSnapshot = form.strategy === "volume-snapshot";
  // Default the destination to the first one once the list resolves. The
  // user can still pick a different one if more than one exists. Adjusted
  // directly during render (not in an effect) — the condition becomes false
  // as soon as repoName is set, so this can't loop.
  if (!form.repoName && !isVolumeSnapshot && destinations.length > 0 && (!existing || existing.spec.strategy === "volume-snapshot")) {
    setForm((f) => ({ ...f, repoName: destinations[0].name }));
  }

  const strategyChanged = form.strategy !== (existing?.spec.strategy ?? "restic-snapshot");
  const repositoryChanged = form.repoName !== (existing?.spec.repoRef?.name ?? "") || form.repoKey !== (existing?.spec.repoRef?.key ?? "repo");
  const needsDestination = !isVolumeSnapshot && (!existing || strategyChanged || repositoryChanged);
  const save = useMutation({
    mutationFn: () => {
      const repoRef = isVolumeSnapshot ? undefined : { name: form.repoName, key: form.repoKey };
      const editedRetention = buildRetention(retention);
      if (!existing) {
        return Schedules.create({ serverRef: { name: serverName }, schedule: form.schedule, strategy: form.strategy, repoRef, retention: editedRetention }, ns);
      }
      const draft = { ...existing.spec, schedule: form.schedule };
      if (strategyChanged) draft.strategy = form.strategy;
      if (repositoryChanged || strategyChanged) draft.repoRef = repoRef;
      if (retention !== existing.spec.retention) draft.retention = editedRetention;
      return Schedules.patchSpec(existing.metadata.name, current => {
        try {
          if (existing.metadata.uid !== current.metadata.uid || current.spec.serverRef.name !== serverName ||
              current.metadata.ownerReferences?.some(ref => ref.controller && ref.kind === "GameServer")) {
            throw new SettingsConflictError();
          }
          const merged = mergeValue(existing.spec, draft, current.spec) as BackupSchedule["spec"];
          if (merged.strategy === "volume-snapshot" && requiresRestic(merged)) throw new SettingsConflictError();
          return merged;
        } catch (error) {
          if (!(error instanceof SettingsConflictError)) throw error;
          throw new APIError(409, intl.formatMessage({ id: "web.components.backups.schedule_form.changed", defaultMessage: "This schedule changed. Reopen it to edit the latest version.", description: "Schedule editor: conflict while saving a changed or replaced schedule" }));
        }
      }, ns);
    },
    onError: error => {
      if (error instanceof APIError && (error.status === 409 || error.status === 404)) {
        void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "schedules") });
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "schedules", ns) });
      onClose();
    },
  });

  return (
    <Card className="space-y-3">
      <CardContent className="space-y-4 pt-6">
        <div>
          <h3 className="text-sm font-medium">{existing
            ? intl.formatMessage({ id: "web.components.backups.schedule_form.edit_schedule", defaultMessage: "Edit {name}", description: "Schedule editor heading, where {name} is the existing backup schedule" }, { name: existing.metadata.name })
            : intl.formatMessage({ id: "web.components.backups.schedule_form.new_backup_schedule", defaultMessage: "New backup schedule", description: "Schedule form: heading of the card for creating a new backup schedule" })}</h3>
          <Description className="text-xs">
            {isVolumeSnapshot
              ? intl.formatMessage({ id: "web.components.backups.schedule_form.csi_hint", defaultMessage: "Cron-based recurring CSI volume snapshot. No restic repository needed.", description: "Schedule form: hint for the CSI snapshot schedule type" })
              : intl.formatMessage({ id: "web.components.backups.schedule_form.restic_hint", defaultMessage: "Cron-based recurring snapshot, stored in a configured restic repository.", description: "Schedule form: hint for the restic schedule type" })}
          </Description>
        </div>

        <div className="space-y-2">
          <Select
            value={form.strategy}
            disabledKeys={requiresRestic(existing?.spec) ? ["volume-snapshot"] : undefined}
            onChange={(v) =>
              setForm({ ...form, strategy: v as typeof form.strategy })
            }
          >
            <Label className="text-xs">{intl.formatMessage({ id: "web.components.backups.schedule_form.backup_type", defaultMessage: "Backup type", description: "Schedule form: label above the backup type dropdown that chooses between a repository snapshot and a volume snapshot; also the screen-reader name of its option list" })}</Label>
            <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
              <Select.Value />
              <Select.Indicator className="ml-auto h-4 w-4" />
            </Select.Trigger>
            <Select.Popover className="rounded border border-border">
              <ListBox aria-label={intl.formatMessage({ id: "web.components.backups.schedule_form.backup_type", defaultMessage: "Backup type", description: "Schedule form: label above the backup type dropdown that chooses between a repository snapshot and a volume snapshot; also the screen-reader name of its option list" })}>
                <ListBoxItem id="restic-snapshot">{intl.formatMessage({ id: "web.components.backups.schedule_form.restic_snapshot_to_a_repository", defaultMessage: "Restic snapshot (to a repository)", description: "Schedule form: option in the backup type dropdown for backups stored in a configured restic repository; restic is a technical term, usually left untranslated; keep it short" })}</ListBoxItem>
                <ListBoxItem id="volume-snapshot">{intl.formatMessage({ id: "web.components.backups.schedule_form.volume_snapshot_csi", defaultMessage: "Volume snapshot (CSI)", description: "Schedule form: option in the backup type dropdown for a cluster volume snapshot that needs no repository; CSI is a technical term, usually left untranslated; keep it short" })}</ListBoxItem>
              </ListBox>
            </Select.Popover>
            {requiresRestic(existing?.spec) && (
              <Description className="text-xs">{intl.formatMessage({ id: "web.components.backups.schedule_form.csi_policy_hint", defaultMessage: "Volume snapshots require preserving server power and backing up only the primary volume.", description: "Schedule editor: explains why the current restic backup policies prevent switching to CSI" })}</Description>
            )}
          </Select>
        </div>

        <div className="space-y-4">
          <div className="grid gap-3 md:grid-cols-[1fr_1fr]">
            <div className="space-y-2">
              <Label htmlFor="schedule-input" className="text-xs">{intl.formatMessage({ id: "web.components.backups.schedule_form.schedule_cron", defaultMessage: "Schedule (cron)", description: "Schedule form: label above the cron schedule text field that sets when backups run; cron is a technical term, usually left untranslated" })}</Label>
              <Input
                id="schedule-input"
                value={form.schedule}
                onChange={(e) => setForm({ ...form, schedule: e.target.value })}
                placeholder={intl.formatMessage({ id: "web.components.backups.schedule_form.cron_placeholder", defaultMessage: "0 */6 * * *", description: "Schedule form: placeholder example of a cron expression" })}
                className="h-9"
              />
            </div>
            {!isVolumeSnapshot && (
              <div className="space-y-2">
                <Select
                  isDisabled={Boolean(destinationError)}
                  value={form.repoName}
                  onChange={(v) => setForm({ ...form, repoName: v as string })}
                >
                  <Label className="text-xs">{intl.formatMessage({ id: "web.components.backups.schedule_form.destination", defaultMessage: "Destination", description: "Schedule form: label above the dropdown that picks the backup destination" })}</Label>
                  <Select.Trigger className="w-full rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
                    <Select.Value />
                    <Select.Indicator className="ml-auto h-4 w-4" />
                  </Select.Trigger>
                  <Select.Popover className="rounded border border-border">
                    <ListBox aria-label={intl.formatMessage({ id: "web.components.backups.schedule_form.destination", defaultMessage: "Destination", description: "Schedule form: label above the dropdown that picks the backup destination" })}>
                      {existing?.spec.repoRef?.name && !destinations.some(d => d.name === existing.spec.repoRef?.name) && (
                        <ListBoxItem id={existing.spec.repoRef.name}>{existing.spec.repoRef.name}</ListBoxItem>
                      )}
                      {destinationsLoaded && destinations.length === 0 ? (
                        <ListBoxItem id="" isDisabled>{intl.formatMessage({ id: "web.components.backups.schedule_form.no_destinations_configured", defaultMessage: "No destinations configured", description: "Schedule form: disabled option in the destination dropdown shown when no backup destinations have been set up" })}</ListBoxItem>
                      ) : (
                        destinations.map((d) => (
                          <ListBoxItem key={d.name} id={d.name}>{d.name}</ListBoxItem>
                        ))
                      )}
                    </ListBox>
                  </Select.Popover>
                </Select>
              </div>
            )}
          </div>

          {!isVolumeSnapshot && (
            <div className="space-y-2">
              <Label htmlFor="repo-key-input" className="text-xs">{intl.formatMessage({ id: "web.components.backups.schedule_form.repo_secret_key", defaultMessage: "Repo secret · key", description: "Schedule form: label above the field that names the key inside the repository secret; not shown for volume snapshot schedules" })}</Label>
              <Input
                id="repo-key-input"
                value={form.repoKey}
                onChange={(e) => setForm({ ...form, repoKey: e.target.value })}
                className="h-9"
              />
            </div>
          )}

          <div className="space-y-3">
            <Label className="text-xs">{intl.formatMessage({ id: "web.components.backups.schedule_form.retention_policy", defaultMessage: "Retention policy", description: "Schedule form: label above the retention settings that decide how many old backups are kept" })}</Label>
            <RetentionFields value={retention} onChange={setRetention} />
          </div>
        </div>

        {!isVolumeSnapshot && destinationError && <ErrorBanner err={destinationError} />}
        {save.error && <ErrorBanner err={save.error} />}

        <div className="flex justify-end gap-2 pt-2">
          <Button variant="ghost" isDisabled={save.isPending} onPress={onClose} size="sm">{intl.formatMessage({ id: "web.components.backups.schedule_form.cancel", defaultMessage: "Cancel", description: "Schedule form: button that closes the form without creating a schedule; keep it short" })}</Button>
          <Button
            onPress={() => save.mutate()}
            isDisabled={
              !resourceCan(permissions, "schedules:write") || !form.schedule.trim() || (needsDestination && (!form.repoName || !form.repoKey || Boolean(destinationError))) || save.isPending
            }
            size="sm"
          >
            {existing
              ? intl.formatMessage({ id: "web.components.backups.schedule_form.save_changes", defaultMessage: "Save changes", description: "Schedule editor: save changes to the existing schedule" })
              : save.isPending ? intl.formatMessage({ id: "web.components.backups.schedule_form.creating", defaultMessage: "Creating…", description: "Schedule form: submit button label while the schedule is created" }) : intl.formatMessage({ id: "web.components.backups.schedule_form.create_schedule", defaultMessage: "Create schedule", description: "Schedule form: submit button label" })}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
