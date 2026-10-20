import { useResourceClient, useResourceTarget, type ResourceTarget, resourceKey } from "@/lib/resourceTarget";
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
  ListBox,
  ListBoxItem,
  Popover,
  PopoverTrigger,
  PopoverContent,
} from "@heroui/react";
import { ChevronDown } from "lucide-react";
import { Users } from "@/lib/endpoints";
import { errorTextIntl } from "@/lib/errors";
import { OWNER_ANNOTATION } from "@/lib/annotations";
import { cn } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";

interface Props {
  target?: ResourceTarget;
  name: string;
  ns?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Called after ownership is reassigned. */
  onTransferred?: () => void;
}

// TransferServerDialog reassigns a server's (informational) owner to another
// user. Self-contained for reuse across the Settings danger zone and the
// server action menus.
export function TransferServerDialog({
  target: explicitTarget, name, ns, open, onOpenChange, onTransferred }: Props) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name, namespace: ns }, explicitTarget);
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const qc = useQueryClient();
  const [userId, setUserId] = useState("");
  const [popoverOpen, setPopoverOpen] = useState(false);
  const { data: server } = useQuery({
    queryKey: resourceKey(resourceTarget, "server", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.get(name, ns),
    enabled: open,
  });
  const { data: users = [], error: usersError } = useQuery({
    queryKey: resourceKey(resourceTarget, "users"),
    queryFn: () => Users.list(),
    enabled: open,
  });
  const currentOwner = server?.metadata.annotations?.[OWNER_ANNOTATION];

  const transfer = useMutation({
    mutationFn: () => Servers.transfer(name, Number(userId), ns),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "server", name, ns) });
      onOpenChange(false);
      onTransferred?.();
    },
  });

  const selectedUser = users.find((u) => String(u.id) === userId);

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop isDismissable={!transfer.isPending} isKeyboardDismissDisabled={transfer.isPending}>
        <ModalContainer>
          <ModalDialog>
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.components.server.transfer_server_dialog.transfer_name", defaultMessage: "Transfer {name}", description: "Transfer server dialog: sentence with name in ModalHeading" }, { name: name })}</ModalHeading>
            </ModalHeader>

            <ModalBody>
              <div className="space-y-4">
                <div className="text-sm text-muted">{intl.formatMessage({ id: "web.components.server.transfer_server_dialog.current_owner", defaultMessage: "Current owner: {owner}.", description: "Transfer server dialog: current owner line; {owner} is the owner username or the unassigned label" }, { owner: currentOwner || intl.formatMessage({ id: "web.components.server.transfer_server_dialog.unassigned", defaultMessage: "unassigned", description: "Transfer server dialog: shown when the server has no owner" }) })}
                </div>

                {usersError ? (
                  <div className="text-xs text-danger">{intl.formatMessage({ id: "web.components.server.transfer_server_dialog.you_need_permission_to_list_users_to", defaultMessage: "You need permission to list users to pick a recipient.", description: "Transfer server dialog: error text shown when the user lacks permission to list users, so no new owner can be picked" })}</div>
                ) : (
                  <Popover isOpen={popoverOpen} onOpenChange={setPopoverOpen}>
                    <PopoverTrigger
                      className={cn(
                        "flex items-center justify-between gap-2 rounded-lg border border-border bg-card px-3 py-2 text-sm",
                        "hover:bg-surface transition-colors cursor-pointer",
                      )}
                    >
                      <span className={selectedUser ? "text-fg" : "text-muted"}>
                        {selectedUser?.username || intl.formatMessage({ id: "web.components.server.transfer_server_dialog.select_user", defaultMessage: "Select a user…", description: "Transfer server dialog: placeholder of the user select when none is chosen" })}
                      </span>
                      <ChevronDown className="h-4 w-4 text-muted shrink-0" />
                    </PopoverTrigger>
                    <PopoverContent className="min-w-[200px]">
                      <ListBox
                        aria-label={intl.formatMessage({ id: "web.components.server.transfer_server_dialog.transfer_to", defaultMessage: "Transfer to", description: "Transfer server dialog: screen-reader name of the list of users to pick as the new owner; not visible on screen" })}
                        selectionMode="single"
                        onSelectionChange={(selected) => {
                          const [id] = selected === "all" ? [] : selected;
                          if (id !== undefined) setUserId(String(id));
                          setPopoverOpen(false);
                        }}
                      >
                        {users.map((u) => (
                          <ListBoxItem key={u.id} id={String(u.id)}>
                            {u.username}
                          </ListBoxItem>
                        ))}
                      </ListBox>
                    </PopoverContent>
                  </Popover>
                )}

                {transfer.isError && (
                  <div className="text-xs text-danger">
                    {errorTextIntl(intl, transfer.error, intl.formatMessage({ id: "web.components.server.transfer_server_dialog.transfer_failed", defaultMessage: "transfer failed", description: "Transfer server dialog: fallback error text when the transfer request fails without a message" }))}
                  </div>
                )}
              </div>
            </ModalBody>

            <ModalFooter className="flex items-center justify-end gap-2">
              <Button
                variant="ghost"
                size="sm"
                isDisabled={transfer.isPending}
                onPress={() => onOpenChange(false)}
              >{intl.formatMessage({ id: "web.components.server.transfer_server_dialog.cancel", defaultMessage: "Cancel", description: "Transfer server dialog: button that closes the dialog without transferring the server; keep it short" })}</Button>
              <Button
                size="sm"
                isDisabled={!userId || transfer.isPending}
                onPress={() => transfer.mutate()}
              >
                {transfer.isPending ? intl.formatMessage({ id: "web.components.server.transfer_server_dialog.transferring", defaultMessage: "Transferring…", description: "Transfer server dialog: submit button label while the transfer runs" }) : intl.formatMessage({ id: "web.components.server.transfer_server_dialog.transfer", defaultMessage: "Transfer", description: "Transfer server dialog: submit button label" })}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
