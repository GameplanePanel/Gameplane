import { useState } from "react";
import { ChevronDown } from "lucide-react";
import {
  Input,
  Popover,
  PopoverTrigger,
  PopoverContent,
  ListBox,
  ListBoxItem,
  Switch,
} from "@heroui/react";
import { RetentionFields, buildRetention, type RetentionForm } from "@/components/backups/RetentionFields";
import { useBackupDestinations } from "@/lib/destinations";
import type { InlineBackupPolicy } from "@/types";
import { cn } from "@/lib/utils";
import { Field } from "./Field";
import type { SectionProps } from "./types";
import { useOptionalIntl } from "@/lib/i18n/format";

export function BackupsSection({ draft, onChange }: SectionProps) {
  const intl = useOptionalIntl();
  const policy = draft.spec.backupPolicy;
  const { data: destinations = [] } = useBackupDestinations();
  const [destPopoverOpen, setDestPopoverOpen] = useState(false);

  const setPolicy = (next: InlineBackupPolicy | undefined) => {
    onChange({
      ...draft,
      spec: {
        ...draft.spec,
        backupPolicy: next,
      },
    });
  };

  const setPolicyField = <K extends keyof InlineBackupPolicy>(
    key: K,
    value: InlineBackupPolicy[K],
  ) => {
    if (!policy) return;
    setPolicy({ ...policy, [key]: value });
  };

  const setRetention = (retention: RetentionForm) => {
    if (!policy) return;
    const built = buildRetention(retention);
    setPolicyField("retention", built);
  };

  const retentionForm: RetentionForm = policy?.retention ?? {};
  const selectedDestName = policy?.repoRef.name;

  return (
    <div className="space-y-6">
      <Field
        label={intl.formatMessage({ id: "web.routes.tabs.settings.backups.enable_scheduled_backups", defaultMessage: "Enable scheduled backups", description: "Settings backups section: label of the switch that turns scheduled backups on or off; the same text is its accessible name" })}
        hint={intl.formatMessage({ id: "web.routes.tabs.settings.backups.automatically_create_backups_on_a_cron", defaultMessage: "Automatically create backups on a cron schedule. The operator manages the BackupSchedule CR.", description: "Settings backups section: hint under the scheduled backups switch saying backups are created on a cron schedule and the operator manages the schedule resource" })}
      >
        <div className="flex items-center gap-3 pt-1">
          <Switch
            isSelected={!!policy}
            isDisabled={!policy && destinations.length === 0}
            onChange={(enabled) => {
              if (enabled) {
                // Seed with defaults
                setPolicy({
                  schedule: "0 */6 * * *",
                  repoRef: {
                    name: destinations[0]?.name ?? "",
                    key: "repo",
                  },
                });
              } else {
                setPolicy(undefined);
              }
            }}
            aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.backups.enable_scheduled_backups", defaultMessage: "Enable scheduled backups", description: "Settings backups section: label of the switch that turns scheduled backups on or off; the same text is its accessible name" })}
          >
            <Switch.Content>
              <Switch.Control>
                <Switch.Thumb />
              </Switch.Control>
            </Switch.Content>
          </Switch>
          <span className="text-sm text-muted">
            {policy
              ? intl.formatMessage({ id: "web.routes.tabs.settings.backups.enabled", defaultMessage: "Enabled", description: "Backups: status label next to the scheduled backups switch when on" })
              : intl.formatMessage({ id: "web.routes.tabs.settings.backups.disabled", defaultMessage: "Disabled", description: "Backups: status label next to the scheduled backups switch when off" })}
          </span>
        </div>
        {!policy && destinations.length === 0 && (
          <div className="pt-1 text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.backups.configure_a_backup_destination_first", defaultMessage: "Configure a backup destination first.", description: "Settings backups section: note shown when no backup destination exists yet, so scheduled backups cannot be set up" })}</div>
        )}
      </Field>

      {policy && (
        <>
          <Field
            label={intl.formatMessage({ id: "web.routes.tabs.settings.backups.schedule_cron", defaultMessage: "Schedule (cron)", description: "Settings backups section: label of the cron expression field that sets when backups run; the hint shows an example expression" })}
            hint={intl.formatMessage({ id: "web.routes.tabs.settings.backups.when_to_run_backups_example_0_6_for", defaultMessage: "When to run backups. Example: '0 */6 * * *' for every 6 hours.", description: "Backups: hint under the cron schedule field saying when backups run, with the example 0 */6 * * * for every 6 hours" })}
          >
            <Input
              type="text"
              value={policy.schedule}
              onChange={(e) => setPolicyField("schedule", e.target.value)}
              placeholder={intl.formatMessage({ id: "web.routes.tabs.settings.backups.schedule_placeholder", defaultMessage: "0 */6 * * *", description: "Backups: example cron schedule shown as the schedule input placeholder" })}
              spellCheck={false}
            />
          </Field>

          <Field label={intl.formatMessage({ id: "web.routes.tabs.settings.backups.destination", defaultMessage: "Destination", description: "Backups: field label for the backup destination picker, which opens a list of configured destinations" })}>
            <Popover isOpen={destPopoverOpen} onOpenChange={setDestPopoverOpen}>
              <PopoverTrigger
                className={cn(
                  "flex items-center justify-between gap-2 rounded-lg border border-border bg-card px-3 py-2 text-sm",
                  "hover:bg-surface transition-colors cursor-pointer",
                )}
              >
                <span className={selectedDestName ? "text-fg" : "text-muted"}>
                  {selectedDestName || intl.formatMessage({ id: "web.routes.tabs.settings.backups.select_destination", defaultMessage: "Select destination…", description: "Backups: placeholder of the backup destination picker when none is chosen" })}
                </span>
                <ChevronDown className="h-4 w-4 text-muted shrink-0" />
              </PopoverTrigger>
              <PopoverContent className="min-w-[200px]">
                <ListBox
                  aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.backups.backup_destination", defaultMessage: "Backup destination", description: "Backups: accessible label of the list of backup destinations shown when the destination picker opens; not shown as text" })}
                  onSelectionChange={(selected) => {
                    setPolicyField("repoRef", { name: String(selected), key: "repo" });
                    setDestPopoverOpen(false);
                  }}
                >
                  {destinations.map((d) => (
                    <ListBoxItem key={d.name} id={d.name}>
                      {d.name}
                    </ListBoxItem>
                  ))}
                </ListBox>
              </PopoverContent>
            </Popover>
          </Field>

          <div>
            <div className="mb-3 text-sm font-medium text-fg">{intl.formatMessage({ id: "web.routes.tabs.settings.backups.retention_policy", defaultMessage: "Retention policy", description: "Backups: small heading above the retention fields that decide how many backups are kept" })}</div>
            <RetentionFields value={retentionForm} onChange={setRetention} />
          </div>

          <Field
            label={intl.formatMessage({ id: "web.routes.tabs.settings.backups.suspend_schedule", defaultMessage: "Suspend schedule", description: "Backups: label of the switch that pauses scheduled backups; the same text is its accessible name" })}
            hint={intl.formatMessage({ id: "web.routes.tabs.settings.backups.when_enabled_scheduled_backups_will_not", defaultMessage: "When enabled, scheduled backups will not run, but the schedule is preserved.", description: "Backups: hint under the suspend switch saying scheduled backups stop running while it is on, but the schedule itself is kept" })}
          >
            <div className="flex items-center gap-3 pt-1">
              <Switch
                isSelected={policy.suspend ?? false}
                onChange={(v) => setPolicyField("suspend", v)}
                aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.backups.suspend_schedule", defaultMessage: "Suspend schedule", description: "Backups: label of the switch that pauses scheduled backups; the same text is its accessible name" })}
              >
                <Switch.Content>
                  <Switch.Control>
                    <Switch.Thumb />
                  </Switch.Control>
                </Switch.Content>
              </Switch>
              <span className="text-sm text-muted">
                {policy.suspend
                  ? intl.formatMessage({ id: "web.routes.tabs.settings.backups.suspended", defaultMessage: "Suspended", description: "Backups: status label next to the suspend switch when on" })
                  : intl.formatMessage({ id: "web.routes.tabs.settings.backups.active", defaultMessage: "Active", description: "Backups: status label next to the suspend switch when off" })}
              </span>
            </div>
          </Field>
        </>
      )}
    </div>
  );
}
