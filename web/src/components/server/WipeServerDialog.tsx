import { useResourceClient, useResourceTarget, type ResourceTarget } from "@/lib/resourceTarget";
import { useState, type ReactNode } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  AlertDialog,
  AlertDialogBackdrop,
  AlertDialogContainer,
  AlertDialogDialog,
  AlertDialogHeader,
  AlertDialogHeading,
  AlertDialogBody,
  AlertDialogFooter,
  AlertDialogIcon,
  Button,
  Checkbox,
} from "@heroui/react";
import { AlertCircle } from "lucide-react";

import { errorTextIntl } from "@/lib/errors";
import { useOptionalIntl } from "@/lib/i18n/format";

interface Props {
  target?: ResourceTarget;
  name: string;
  ns?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Called after the world data is wiped — e.g. to refresh server state. */
  onWiped?: () => void;
}

// WipeServerDialog suspends the server and asks the operator to wipe its data
// volume. Self-contained (owns its mutation + error) for reuse across the
// Settings danger zone and the server action menus.
export function WipeServerDialog({
  target: explicitTarget, name, ns, open, onOpenChange, onWiped }: Props) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const resourceTarget = useResourceTarget({ name, namespace: ns }, explicitTarget);
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const [confirmed, setConfirmed] = useState(false);
  // Track previous open state to reset confirmation when dialog opens
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (open) setConfirmed(false);
  }

  const wipe = useMutation({
    // The API's :wipe-data body echoes the server name as a typed confirmation.
    mutationFn: () => Servers.wipeData(name, name, ns),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      onOpenChange(false);
      onWiped?.();
    },
  });

  const description: ReactNode = (
    <div className="space-y-4">
      <div className="text-sm text-muted">{intl.formatMessage({ id: "web.components.server.wipe_server_dialog.wipe_warning", defaultMessage: "This permanently deletes the world data for <b>{serverName}</b>. Backups are not removed. This cannot be undone.", description: "Wipe server dialog: warning that the world data is permanently deleted, backups are kept and the action cannot be undone; {serverName} is the server name, shown in monospace inside the <b> tag, which must be kept around it" }, { serverName: name, b: (chunks) => <span className="font-mono text-foreground">{chunks}</span> })}</div>
      {wipe.isError && (
        <div className="rounded-lg bg-danger/10 p-3 text-sm text-danger">
          {errorTextIntl(intl, wipe.error, intl.formatMessage({ id: "web.components.server.wipe_server_dialog.wipe_failed", defaultMessage: "wipe failed", description: "Wipe server dialog: fallback error text when the wipe request fails without a message" }))}
        </div>
      )}
    </div>
  );

  return (
    <AlertDialog isOpen={open} onOpenChange={(v) => {
      if (!v) wipe.reset();
      onOpenChange(v);
    }}>
      <AlertDialogBackdrop isDismissable={!wipe.isPending} isKeyboardDismissDisabled={wipe.isPending}>
        <AlertDialogContainer>
          <AlertDialogDialog>
            <AlertDialogHeader>
              <div className="flex items-start gap-4">
                <AlertDialogIcon
                  status="danger"
                  className="shrink-0"
                >
                  <AlertCircle className="h-5 w-5" />
                </AlertDialogIcon>
                <AlertDialogHeading>{intl.formatMessage({ id: "web.components.server.wipe_server_dialog.wipe_world", defaultMessage: "Wipe world?", description: "Wipe server dialog: title of the danger-styled confirmation dialog asking whether to wipe the world" })}</AlertDialogHeading>
              </div>
            </AlertDialogHeader>

            <AlertDialogBody>
              <div className="space-y-4">
                {description}

                <Checkbox
                  isSelected={confirmed}
                  onChange={() => setConfirmed(!confirmed)}
                >
                  <Checkbox.Control />
                  <Checkbox.Content className="text-sm text-foreground">{intl.formatMessage({ id: "web.components.server.wipe_server_dialog.i_understand_this_will_permanently", defaultMessage: "I understand this will permanently delete the world data", description: "Wipe server dialog: checkbox label where the user confirms that the world data will be permanently deleted" })}</Checkbox.Content>
                </Checkbox>
              </div>
            </AlertDialogBody>

            <AlertDialogFooter className="flex items-center justify-end gap-2">
              <Button
                variant="ghost"
                size="sm"
                isDisabled={wipe.isPending}
                onPress={() => onOpenChange(false)}
              >{intl.formatMessage({ id: "web.components.server.wipe_server_dialog.cancel", defaultMessage: "Cancel", description: "Wipe server dialog: button that closes the dialog without wiping the world; keep it short" })}</Button>
              <Button
                size="sm"
                variant="danger"
                isDisabled={!confirmed || wipe.isPending}
                onPress={() => wipe.mutate()}
              >
                {wipe.isPending ? intl.formatMessage({ id: "web.components.server.wipe_server_dialog.working", defaultMessage: "Working…", description: "Wipe server dialog: submit button label while the wipe runs" }) : intl.formatMessage({ id: "web.components.server.wipe_server_dialog.wipe_world_button", defaultMessage: "Wipe world", description: "Wipe server dialog: submit button label" })}
              </Button>
            </AlertDialogFooter>
          </AlertDialogDialog>
        </AlertDialogContainer>
      </AlertDialogBackdrop>
    </AlertDialog>
  );
}
