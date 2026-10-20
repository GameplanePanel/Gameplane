import { useResourceClient, useResourceTarget, resourceKey, useResourcePermissions, resourceCan, type ResourceTarget } from "@/lib/resourceTarget";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Modal,
  ModalBackdrop,
  ModalContainer,
  ModalDialog,
  ModalHeader,
  ModalHeading,
  ModalBody,
  ModalFooter,
  Button,
  Input,
  Label,
  Description,
  ListBox,
  ListBoxItem,
  Popover,
  PopoverTrigger,
  PopoverContent,
  Alert,
} from "@heroui/react";
import { ChevronDown, AlertCircle } from "lucide-react";

import { errorTextIntl } from "@/lib/errors";
import type { Backup } from "@/types";
import { cn } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";

interface Props {
  target?: ResourceTarget;
  permissions?: string[];
  backup: Backup | null;
  defaultServer?: string;
  ns?: string;
  onClose: () => void;
}

export function RestoreDialog({ backup, defaultServer, ns, onClose, target: explicitTarget, permissions: explicitPermissions }: Props) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name: backup?.metadata.name ?? "", namespace: ns ?? backup?.metadata.namespace }, explicitTarget);
  const resourceClient = useResourceClient(resourceTarget);
  const { Restores } = resourceClient;
  const permissions = useResourcePermissions(explicitPermissions);
  const qc = useQueryClient();
  const open = backup !== null;
  // Volume-snapshot backups can't be restored in place — they provision a
  // brand-new server seeded from the CSI snapshot. So the dialog collects a
  // NEW (unused) server name instead of an overwrite target.
  const isVolumeSnapshot = backup?.spec.strategy === "volume-snapshot";
  const { data: servers } = useQuery({
    queryKey: resourceKey(resourceTarget, "servers"),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.list(),
    enabled: open,
  });
  const [target, setTarget] = useState("");
  const [popoverOpen, setPopoverOpen] = useState(false);
  // Tracks which backup `target` was last initialized for, so the reset
  // below (adjusted directly during render, not in an effect) fires exactly
  // once per newly-opened backup: restic restores default to the in-place
  // server; volume-snapshot restores suggest a fresh "<original>-restored"
  // name.
  const [initializedFor, setInitializedFor] = useState<Backup | null>(null);
  if (backup && backup !== initializedFor) {
    setInitializedFor(backup);
    setTarget(
      backup.spec.strategy === "volume-snapshot"
        ? `${backup.spec.serverRef.name}-restored`
        : (defaultServer ?? ""),
    );
  }

  const nameTaken =
    isVolumeSnapshot &&
    (servers?.items ?? []).some((s) => s.metadata.name === target);
  const invalid = !target || nameTaken;

  const create = useMutation({
    mutationFn: () =>
      Restores.create(
        {
          backupRef: { name: backup!.metadata.name },
          serverRef: { name: target },
        },
        ns,
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "restores", ns) });
      onClose();
    },
  });

  const selectedServer = servers?.items?.find((s) => s.metadata.name === target);

  return (
    <Modal isOpen={open} onOpenChange={(o) => !o && onClose()}>
      <ModalBackdrop isDismissable={!create.isPending} isKeyboardDismissDisabled={create.isPending}>
        <ModalContainer>
          <ModalDialog className="w-[480px] max-w-[480px]">
          <ModalHeader>
            <ModalHeading>{intl.formatMessage({ id: "web.components.backups.restore_dialog.restore_backup", defaultMessage: "Restore backup", description: "Restore dialog: title of the dialog for restoring a backup onto a server" })}</ModalHeading>
            <Description className="text-sm text-muted">
              {isVolumeSnapshot
                ? intl.formatMessage({ id: "web.components.backups.restore_dialog.new_server_note", defaultMessage: "A new server will be provisioned from this snapshot. The original server is left untouched.", description: "Restore dialog: explains that restoring creates a new server" })
                : intl.formatMessage({ id: "web.components.backups.restore_dialog.in_place_note", defaultMessage: "The target server will be suspended, the volume restored from the snapshot, then resumed.", description: "Restore dialog: explains an in-place restore" })}
            </Description>
          </ModalHeader>

          <ModalBody className="gap-4">

            <div className="space-y-4">
              <div>
                <Label htmlFor="source-backup" className="text-xs">{intl.formatMessage({ id: "web.components.backups.restore_dialog.source_backup", defaultMessage: "Source backup", description: "Restore dialog: field label above the read-only name of the backup being restored" })}</Label>
                <div className="mt-1.5 flex items-center rounded-lg border border-[var(--field-border)] bg-[var(--field-background)] px-3 py-2 font-mono text-xs text-fg">
                  {backup?.metadata.name}
                </div>
              </div>

              {isVolumeSnapshot ? (
                <div>
                  <Label htmlFor="new-server-name" className="text-xs">{intl.formatMessage({ id: "web.components.backups.restore_dialog.new_server_name", defaultMessage: "New server name", description: "Restore dialog: field label for the name of the new server to create from the backup; shown in the volume snapshot restore mode" })}</Label>
                  <Input
                    id="new-server-name"
                    autoFocus
                    value={target}
                    onChange={(e) => setTarget(e.target.value)}
                    spellCheck={false}
                    autoComplete="off"
                    placeholder={intl.formatMessage({ id: "web.components.backups.restore_dialog.my_restored_server", defaultMessage: "my-restored-server", description: "Restore dialog: example value shown inside the empty new server name field; a sample name, not a default value" })}
                    className="mt-1"
                  />
                  {nameTaken && (
                    <p role="alert" className="mt-2 text-xs text-danger">{intl.formatMessage({ id: "web.components.backups.restore_dialog.a_server_named_target_already_exists", defaultMessage: "A server named \"{target}\" already exists — choose a new name.", description: "Restore dialog: sentence with target in p" }, { target: target })}</p>
                  )}
                </div>
              ) : (
                <div>
                  <Label htmlFor="target-server" className="text-xs">{intl.formatMessage({ id: "web.components.backups.restore_dialog.target_game_server", defaultMessage: "Target game server", description: "Restore dialog: field label for choosing the existing server to restore the backup onto" })}</Label>
                  <Popover isOpen={popoverOpen} onOpenChange={setPopoverOpen}>
                    <PopoverTrigger
                      id="target-server"
                      className={cn(
                        "mt-1.5 flex items-center justify-between gap-2 rounded-lg border border-[var(--field-border)] bg-[var(--field-background)] px-3 py-2 text-sm",
                        "hover:bg-surface transition-colors cursor-pointer",
                      )}
                    >
                      <span className={target ? "text-fg" : "text-muted"}>
                        {selectedServer?.metadata.name || intl.formatMessage({ id: "web.components.backups.restore_dialog.select_server", defaultMessage: "Select a server…", description: "Restore dialog: placeholder of the target server select when none is chosen" })}
                      </span>
                      <ChevronDown className="h-4 w-4 text-muted shrink-0" />
                    </PopoverTrigger>
                    <PopoverContent className="min-w-[200px]">
                      <ListBox
                        aria-label={intl.formatMessage({ id: "web.components.backups.restore_dialog.select_target_server", defaultMessage: "Select target server", description: "Restore dialog: screen-reader name of the list of existing servers shown when picking the restore target; not visible on screen" })}
                        selectionMode="single"
                        onSelectionChange={(selected) => {
                          const [id] = selected;
                          if (id !== undefined) setTarget(String(id));
                          setPopoverOpen(false);
                        }}
                      >
                        {(servers?.items ?? []).map((s) => (
                          <ListBoxItem key={s.metadata.name} id={s.metadata.name}>
                            {s.metadata.name}
                          </ListBoxItem>
                        ))}
                      </ListBox>
                    </PopoverContent>
                  </Popover>
                </div>
              )}

              {isVolumeSnapshot ? (
                <div className="rounded-md border border-border bg-surface/40 p-3 text-xs text-muted">{intl.formatMessage({ id: "web.components.backups.restore_dialog.the_new_server_copies_name_s", defaultMessage: "The new server copies {name}'s configuration and starts with the snapshot's data. Nothing on the original server changes.", description: "Restore dialog: sentence with name in div" }, { name: backup?.spec.serverRef.name })}</div>
              ) : (
                <Alert status="danger" className="text-xs">
                  <div className="flex gap-2">
                    <AlertCircle className="h-4 w-4 flex-shrink-0 text-danger" />
                    <span className="text-danger">{intl.formatMessage({ id: "web.components.backups.restore_dialog.this_will_overwrite_all_data_on_the", defaultMessage: "This will overwrite all data on the target server. Players will be disconnected during the restore.", description: "Restore dialog: red warning under the restore mode choice saying the target server's data will be overwritten and players will be disconnected during the restore" })}</span>
                  </div>
                </Alert>
              )}

              {create.isError && (
                <div className="rounded-md border border-danger/60 bg-danger/10 p-2 text-xs text-danger">
                  {errorTextIntl(intl, create.error, intl.formatMessage({ id: "web.components.backups.restore_dialog.restore_failed", defaultMessage: "Restore failed", description: "Restore dialog: fallback error text when the restore request fails without a server message" }))}
                </div>
              )}
            </div>
          </ModalBody>

          <ModalFooter className="flex items-center justify-end gap-2">
            <Button
              variant="ghost"
              size="sm"
              className="text-xs"
              onPress={onClose}
              isDisabled={create.isPending}
            >{intl.formatMessage({ id: "web.components.backups.restore_dialog.cancel", defaultMessage: "Cancel", description: "Restore dialog: button that closes the restore dialog without restoring; keep it short" })}</Button>
            <Button
              size="sm"
              variant={isVolumeSnapshot ? "primary" : "danger"}
              className="text-xs"
              isDisabled={invalid || create.isPending || !resourceCan(permissions, "backups:restore")}
              onPress={() => create.mutate()}
            >
              {create.isPending
                ? intl.formatMessage({ id: "web.components.backups.restore_dialog.starting", defaultMessage: "Starting…", description: "Restore dialog: submit button label while the restore starts" })
                : isVolumeSnapshot
                  ? intl.formatMessage({ id: "web.components.backups.restore_dialog.restore_to_new_server", defaultMessage: "Restore to new server", description: "Restore dialog: submit button label when restoring to a new server" })
                  : intl.formatMessage({ id: "web.components.backups.restore_dialog.restore", defaultMessage: "Restore", description: "Restore dialog: submit button label for an in-place restore" })}
            </Button>
          </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
