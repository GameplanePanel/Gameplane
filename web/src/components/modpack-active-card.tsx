import { Button, Chip } from "@heroui/react";
import { Package, RotateCcw, Trash2 } from "lucide-react";
import type { ModpackView } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { providerLabel } from "@/components/registry-browser";

type ChipColor = "default" | "accent" | "success" | "warning" | "danger";

// ModpackActiveCard summarises the pack the server is running: its version,
// the phase of the last change and the actions that are allowed right now.
// It renders nothing when no pack is active.
export function ModpackActiveCard({
  view,
  mcLabel,
  canManage,
  onChangeVersion,
  onRemove,
}: {
  view: ModpackView;
  mcLabel: string;
  canManage: boolean;
  onChangeVersion(): void;
  onRemove(): void;
}) {
  const intl = useOptionalIntl();
  const active = view.active;
  if (!active) return null;

  const phase = view.phase;
  const removing = active.pendingRemoval === true;
  const backupFailed = phase === "BackupFailed";
  const transitional = phase === "Pending" || phase === "BackingUp" || phase === "Applying";
  // While a removal is queued the only useful action is retrying a failed backup.
  const removalBlocksActions = removing && !backupFailed;
  const actionsDisabled = !canManage || transitional || removalBlocksActions;
  const notPinned = !active.pinned;

  let chip: { label: string; color: ChipColor };
  let message = view.message;
  let messageClass = "text-muted";
  if (removing && !backupFailed) {
    chip = { label: intl.formatMessage({ id: "web.components.modpack_active_card.chip_removing", defaultMessage: "Removing", description: "Modpack active card: status chip while the modpack removal is queued" }), color: "warning" };
    message = intl.formatMessage({ id: "web.components.modpack_active_card.message_removing", defaultMessage: "Removing the pack and emptying the mods folder at the next start…", description: "Modpack active card: line shown while a modpack removal is queued for the next server start" });
  } else if (backupFailed) {
    chip = { label: intl.formatMessage({ id: "web.components.modpack_active_card.chip_backup_failed", defaultMessage: "Backup failed", description: "Modpack active card: status chip when the backup taken before a change or removal failed" }), color: "danger" };
    messageClass = "text-danger";
  } else if (phase === "BackingUp") {
    chip = { label: intl.formatMessage({ id: "web.components.modpack_active_card.chip_backing_up", defaultMessage: "Backing up", description: "Modpack active card: status chip while a backup is taken before the modpack changes" }), color: "accent" };
    message = message || intl.formatMessage({ id: "web.components.modpack_active_card.message_backing_up", defaultMessage: "Backing up before switching. The server keeps running.", description: "Modpack active card: default line shown while the backup before a modpack change runs" });
  } else if (phase === "Applying" || phase === "Pending") {
    chip = { label: intl.formatMessage({ id: "web.components.modpack_active_card.chip_applying", defaultMessage: "Applying", description: "Modpack active card: status chip while the modpack change is being applied" }), color: "accent" };
    if (phase === "Applying") {
      message = message || intl.formatMessage({ id: "web.components.modpack_active_card.message_applying", defaultMessage: "Restarting with the new version…", description: "Modpack active card: default line shown while the server restarts with the new modpack version" });
    }
  } else if (notPinned) {
    chip = { label: intl.formatMessage({ id: "web.components.modpack_active_card.chip_not_pinned", defaultMessage: "Not pinned", description: "Modpack active card: status chip when the installed modpack version could not be read" }), color: "warning" };
    message = intl.formatMessage({ id: "web.components.modpack_active_card.message_not_pinned", defaultMessage: "The installed version could not be read. Pick a version to pin this pack; until then a restart may upgrade it.", description: "Modpack active card: line explaining that the installed modpack version is unknown and a restart may upgrade it" });
  } else {
    chip = { label: intl.formatMessage({ id: "web.components.modpack_active_card.chip_applied", defaultMessage: "Applied", description: "Modpack active card: status chip when the modpack is pinned and applied" }), color: "success" };
  }

  const versionText = notPinned
    ? intl.formatMessage({ id: "web.components.modpack_active_card.version_unknown", defaultMessage: "Version unknown · {mc}", description: "Modpack active card: second line when the installed version is unknown; {mc} is the Minecraft version and loader label" }, { mc: mcLabel })
    : intl.formatMessage({ id: "web.components.modpack_active_card.version_pinned", defaultMessage: "Version {version} · pinned · {mc}", description: "Modpack active card: second line with the pinned modpack version; {version} is the version name, {mc} the Minecraft version and loader label" }, { version: active.versionName || active.version, mc: mcLabel });

  const providerChip = providerLabel(active.provider);
  const changeLabel = notPinned
    ? intl.formatMessage({ id: "web.components.modpack_active_card.pick_version", defaultMessage: "Pick version", description: "Modpack active card: button that opens the version picker when the installed version is unknown" })
    : intl.formatMessage({ id: "web.components.modpack_active_card.change_version", defaultMessage: "Change version", description: "Modpack active card: button that opens the version picker to switch the modpack version" });
  const removeLabel = backupFailed && removing
    ? intl.formatMessage({ id: "web.components.modpack_active_card.retry_remove", defaultMessage: "Retry remove", description: "Modpack active card: button that retries removing the modpack after its backup failed" })
    : intl.formatMessage({ id: "web.components.modpack_active_card.remove", defaultMessage: "Remove", description: "Modpack active card: button that removes the active modpack" });

  const tooltip = canManage ? undefined : intl.formatMessage({ id: "web.routes.tabs.modpacks.requires_operator_role", defaultMessage: "Requires operator role", description: "Modpacks: tooltip on a disabled install control for users without the operator role" });

  return (
    <section className="flex flex-col gap-3 rounded-xl border border-border bg-surface p-4">
      <div className="text-[11px] font-medium uppercase tracking-wide text-muted">
        {intl.formatMessage({ id: "web.components.modpack_active_card.label", defaultMessage: "Active modpack", description: "Modpack active card: small label above the active modpack summary" })}
      </div>
      <div className="flex flex-wrap items-start gap-3">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-default text-muted">
          <Package className="h-5 w-5" aria-hidden="true" />
        </div>
        <div className="min-w-0 flex-1 space-y-0.5">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="truncate text-sm font-medium">{active.title}</h3>
            <Chip size="sm" variant="tertiary">{providerChip}</Chip>
            <Chip size="sm" variant="soft" color={chip.color}>{chip.label}</Chip>
          </div>
          <p className="text-xs text-muted">{versionText}</p>
          {message && <p className={`text-xs ${messageClass}`}>{message}</p>}
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <span title={tooltip} tabIndex={tooltip ? 0 : undefined}>
          <Button variant="outline" size="sm" isDisabled={actionsDisabled} onPress={onChangeVersion}>
            <RotateCcw className="h-3.5 w-3.5" aria-hidden="true" />
            {changeLabel}
          </Button>
        </span>
        <span title={tooltip} tabIndex={tooltip ? 0 : undefined}>
          <Button variant="danger" size="sm" isDisabled={actionsDisabled} onPress={onRemove}>
            <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
            {removeLabel}
          </Button>
        </span>
      </div>
    </section>
  );
}
