import { useState } from "react";
import {
  Alert,
  Button,
  Checkbox,
  Modal,
  ModalBackdrop,
  ModalBody,
  ModalContainer,
  ModalDialog,
  ModalFooter,
  ModalHeader,
  ModalHeading,
} from "@heroui/react";
import { AlertTriangle, Package } from "lucide-react";
import { useOptionalIntl } from "@/lib/i18n/format";

// ModpackConfirmDialog asks for confirmation before a modpack is installed,
// switched, downgraded or removed. It reports whether the backup box is
// ticked; the caller performs the change.
export function ModpackConfirmDialog({
  open,
  onOpenChange,
  mode,
  packTitle,
  providerLabel,
  versionName,
  fromVersionName,
  serverName,
  backupAvailable,
  backupDestination,
  pending,
  error,
  serverVersionChange,
  onConfirm,
}: {
  open: boolean;
  onOpenChange(o: boolean): void;
  mode: "install" | "change" | "downgrade" | "remove";
  packTitle: string;
  providerLabel: string;
  versionName?: string;
  fromVersionName?: string;
  serverName: string;
  backupAvailable: boolean;
  // Name of the backup destination the backup goes to, when known.
  backupDestination?: string;
  pending: boolean;
  error?: string;
  // Set when the picked pack version makes the server switch its version:
  // from is the server's current version name, to the one it switches to.
  serverVersionChange?: { from: string; to: string };
  onConfirm(opts: { backupFirst: boolean }): void;
}) {
  const intl = useOptionalIntl();
  // Backup is ticked by default when a destination exists. Re-initialised
  // every time the dialog opens so a previous untick does not stick.
  const [backupWanted, setBackupWanted] = useState(backupAvailable);

  const [prevInit, setPrevInit] = useState({ open, backupAvailable });
  if (prevInit.open !== open || prevInit.backupAvailable !== backupAvailable) {
    setPrevInit({ open, backupAvailable });
    if (open) setBackupWanted(backupAvailable);
  }

  const backupFirst = backupAvailable && backupWanted;
  const isRemove = mode === "remove";
  const isDowngrade = mode === "downgrade";

  const title = (() => {
    switch (mode) {
      case "install":
        return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.install_title", defaultMessage: "Install {packTitle}?", description: "Modpack confirm dialog: title when installing a modpack; {packTitle} is the modpack title" }, { packTitle });
      case "change":
        return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.change_title", defaultMessage: "Switch to {versionName}?", description: "Modpack confirm dialog: title when switching the modpack to a newer version; {versionName} is the version name" }, { versionName: versionName ?? "" });
      case "downgrade":
        return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.downgrade_title", defaultMessage: "Go back to {versionName}?", description: "Modpack confirm dialog: title when moving the modpack to an older version; {versionName} is the version name" }, { versionName: versionName ?? "" });
      case "remove":
        return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.remove_title", defaultMessage: "Remove {packTitle}?", description: "Modpack confirm dialog: title when removing the modpack from the server; {packTitle} is the modpack title" }, { packTitle });
    }
  })();

  const description = (() => {
    switch (mode) {
      case "install":
        return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.install_desc", defaultMessage: "{serverName} restarts to install the pack. It stays on this version until you change it.", description: "Modpack confirm dialog: explains the server restarts to install the pack and keeps this version until changed; {serverName} is the server name" }, { serverName });
      case "remove":
        return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.remove_desc", defaultMessage: "The pack is removed and {serverName} restarts as a plain server.", description: "Modpack confirm dialog: explains the modpack is removed and the server restarts without it; {serverName} is the server name" }, { serverName });
      case "change":
      case "downgrade":
        if (fromVersionName) {
          return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.switch_desc_from", defaultMessage: "{serverName} restarts on {packTitle} {versionName} instead of {fromVersionName}.", description: "Modpack confirm dialog: explains the server restarts on the new version instead of the current one; {serverName}, {packTitle}, {versionName} and {fromVersionName} are names" }, { serverName, packTitle, versionName: versionName ?? "", fromVersionName });
        }
        return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.switch_desc", defaultMessage: "{serverName} restarts on {packTitle} {versionName}.", description: "Modpack confirm dialog: explains the server restarts on the chosen modpack version; {serverName}, {packTitle} and {versionName} are names" }, { serverName, packTitle, versionName: versionName ?? "" });
    }
  })();

  const primaryLabel = pending
    ? intl.formatMessage({ id: "web.components.modpack_confirm_dialog.working", defaultMessage: "Working…", description: "Modpack confirm dialog: label of the primary button while the change is being submitted" })
    : (() => {
        switch (mode) {
          case "install":
            return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.install_and_restart", defaultMessage: "Install and restart", description: "Modpack confirm dialog: primary button that installs the modpack and restarts the server" });
          case "change":
            return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.change_and_restart", defaultMessage: "Change version and restart", description: "Modpack confirm dialog: primary button that switches the modpack version and restarts the server" });
          case "downgrade":
            return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.downgrade_and_restart", defaultMessage: "Downgrade and restart", description: "Modpack confirm dialog: primary button that moves to an older modpack version and restarts the server" });
          case "remove":
            return intl.formatMessage({ id: "web.components.modpack_confirm_dialog.remove_modpack", defaultMessage: "Remove modpack", description: "Modpack confirm dialog: danger primary button that removes the modpack" });
        }
      })();

  const backupHelp = backupAvailable && backupDestination
    ? intl.formatMessage({ id: "web.components.modpack_confirm_dialog.backup_help_destination", defaultMessage: "Takes a backup to the cluster’s backup destination ({destination}) before the change. If the backup fails, nothing changes.", description: "Modpack confirm dialog: help text under the back-up checkbox naming the backup destination; the backup is taken first and the change is cancelled if it fails; {destination} is the destination name" }, { destination: backupDestination })
    : backupAvailable
    ? intl.formatMessage({ id: "web.components.modpack_confirm_dialog.backup_help", defaultMessage: "Takes a backup to this server's backup destination before the change. If the backup fails, nothing changes.", description: "Modpack confirm dialog: help text under the back-up checkbox explaining the backup is taken first and the change is cancelled if it fails" })
    : intl.formatMessage({ id: "web.components.modpack_confirm_dialog.no_backup_destination", defaultMessage: "No backup destination is set for this server's cluster.", description: "Modpack confirm dialog: help text under the disabled back-up checkbox when the cluster has no backup destination" });

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop isDismissable={!pending} isKeyboardDismissDisabled={pending}>
        <ModalContainer>
          <ModalDialog className="w-[480px] max-w-full">
            <ModalHeader>
              <ModalHeading>{title}</ModalHeading>
            </ModalHeader>
            <ModalBody className="gap-4">
              <p className="text-sm text-muted">{description}</p>
              {!isRemove && (
                <div className="flex items-center gap-3 rounded-lg border border-border px-3 py-2">
                  <span className="grid h-9 w-9 shrink-0 place-items-center rounded-md bg-primary/10 text-primary">
                    <Package className="h-4 w-4" aria-hidden="true" />
                  </span>
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">{packTitle}</p>
                    <p className="truncate text-xs text-muted">
                      {versionName
                        ? intl.formatMessage({ id: "web.components.modpack_confirm_dialog.provider_version", defaultMessage: "{providerLabel} · {versionName}", description: "Modpack confirm dialog: pack summary line with the registry label and the version name; {providerLabel} is the registry, {versionName} the version" }, { providerLabel, versionName })
                        : providerLabel}
                    </p>
                  </div>
                </div>
              )}
              {isDowngrade && (
                <Alert status="warning" className="text-xs">
                  <div className="flex gap-2">
                    <AlertTriangle className="h-4 w-4 flex-shrink-0" aria-hidden="true" />
                    <div className="space-y-1">
                      <p className="font-medium">
                        {intl.formatMessage({ id: "web.components.modpack_confirm_dialog.downgrade_warning_title", defaultMessage: "This is an older version", description: "Modpack confirm dialog: heading of the warning shown when the chosen version is older than the current one" })}
                      </p>
                      <p>
                        {intl.formatMessage({ id: "web.components.modpack_confirm_dialog.downgrade_warning_body", defaultMessage: "Going back to an older version can break the world, for example when it removes blocks or items the world already uses. Keep the backup.", description: "Modpack confirm dialog: body of the downgrade warning explaining older versions can break the world; keep the backup" })}
                      </p>
                    </div>
                  </div>
                </Alert>
              )}
              {isRemove && (
                <Alert status="danger" className="text-xs">
                  <div className="flex gap-2">
                    <AlertTriangle className="h-4 w-4 flex-shrink-0" aria-hidden="true" />
                    <p>
                      {intl.formatMessage({ id: "web.components.modpack_confirm_dialog.remove_warning", defaultMessage: "Every mod in the mods folder is deleted, including ones added by hand. The world folder is not touched.", description: "Modpack confirm dialog: danger warning when removing the modpack; every mod in the mods folder is deleted, including hand-added ones, and the world folder is kept" })}
                    </p>
                  </div>
                </Alert>
              )}
              <div className="space-y-1">
                <Checkbox
                  id="modpack-backup-first"
                  isSelected={backupFirst}
                  onChange={(isSelected) => setBackupWanted(isSelected)}
                  isDisabled={!backupAvailable || pending}
                  className="flex-row items-center gap-2"
                >
                  <Checkbox.Control>
                    <Checkbox.Indicator />
                  </Checkbox.Control>
                  <Checkbox.Content className="text-sm font-normal">
                    {intl.formatMessage({ id: "web.components.modpack_confirm_dialog.back_up_first", defaultMessage: "Back up first", description: "Modpack confirm dialog: checkbox label that takes a backup before the change" })}
                  </Checkbox.Content>
                </Checkbox>
                <p className="text-xs text-muted">{backupHelp}</p>
              </div>
              {serverVersionChange && (
                <Alert status="warning" className="text-xs">
                  <div className="flex gap-2">
                    <AlertTriangle className="h-4 w-4 flex-shrink-0" aria-hidden="true" />
                    <div className="space-y-1">
                      <p className="font-medium">
                        {intl.formatMessage({ id: "web.components.modpack_confirm_dialog.server_version_warning_title", defaultMessage: "The server version changes", description: "Modpack confirm dialog: heading of the warning shown when the chosen pack version makes the server switch its version" })}
                      </p>
                      <p>
                        {intl.formatMessage({ id: "web.components.modpack_confirm_dialog.server_version_warning_body", defaultMessage: "The server version changes from {from} to {to}.", description: "Modpack confirm dialog: body of the server version warning; {from} is the server's current version name, {to} the version it switches to" }, { from: serverVersionChange.from, to: serverVersionChange.to })}
                      </p>
                    </div>
                  </div>
                </Alert>
              )}
              {error && <p role="alert" className="text-sm text-danger">{error}</p>}
            </ModalBody>
            <ModalFooter>
              <Button variant="outline" isDisabled={pending} onPress={() => onOpenChange(false)}>
                {intl.formatMessage({ id: "web.components.modpack_confirm_dialog.cancel", defaultMessage: "Cancel", description: "Modpack confirm dialog: button that closes the dialog without changing the modpack" })}
              </Button>
              <Button variant={isRemove ? "danger" : "primary"} isDisabled={pending} onPress={() => onConfirm({ backupFirst })}>
                {primaryLabel}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
