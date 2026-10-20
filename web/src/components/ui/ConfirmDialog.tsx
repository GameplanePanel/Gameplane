import { useState, type ReactNode } from "react";
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
} from "@heroui/react";
import { Button, Input } from "@heroui/react";
import { AlertCircle } from "lucide-react";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: ReactNode;
  /** When set, user must type this value exactly to enable the confirm button. */
  confirmPhrase?: string;
  confirmLabel?: string;
  destructive?: boolean;
  onConfirm: () => void;
  busy?: boolean;
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmPhrase,
  confirmLabel,
  destructive,
  onConfirm,
  busy,
}: ConfirmDialogProps) {
  const intl = useOptionalIntl();
  const [typed, setTyped] = useState("");
  // Clear the typed confirmation whenever the dialog transitions to open.
  // Adjusted directly during render (not in an effect) — the React-endorsed
  // pattern for resetting state on a prop change, gated on the previous
  // `open` value tracked in state.
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (open) setTyped("");
  }

  const matches = !confirmPhrase || typed === confirmPhrase;

  return (
    <AlertDialog isOpen={open} onOpenChange={onOpenChange}>
      <AlertDialogBackdrop isDismissable={!busy} isKeyboardDismissDisabled={!!busy}>
        <AlertDialogContainer>
          <AlertDialogDialog className="w-[440px] max-w-[440px] gap-0 rounded-[12px] p-5">
            <AlertDialogHeader className="m-0 p-0">
              <div className="flex items-center gap-3">
                <AlertDialogIcon
                  status={destructive ? "danger" : "accent"}
                  className="shrink-0"
                >
                  <AlertCircle className="h-5 w-5" />
                </AlertDialogIcon>
                <AlertDialogHeading>{title}</AlertDialogHeading>
              </div>
            </AlertDialogHeader>

            <AlertDialogBody className="m-0 p-0 pt-4">
              <div className="space-y-4">
                <div className="text-sm text-muted">{description}</div>

                {confirmPhrase && (
                  <div>
                    <label className="block pb-2 text-xs text-muted">{intl.formatMessage({ id: "web.components.ui.confirm_dialog.type_phrase_to_confirm", defaultMessage: "Type <b>{phrase}</b> to confirm", description: "Confirm dialog: label above the text input asking the user to type {phrase}, the exact phrase shown in monospace inside <b>, to enable the confirm button" }, { phrase: confirmPhrase, b: (chunks) => <span className="font-mono text-foreground">{chunks}</span> })}</label>
                    <Input
                      autoFocus
                      value={typed}
                      onChange={(e) => setTyped(e.target.value)}
                      spellCheck={false}
                      placeholder={intl.formatMessage({ id: "web.components.ui.confirm_dialog.type_to_confirm", defaultMessage: "Type to confirm", description: "Confirm dialog: placeholder inside the field where the user retypes the phrase shown above to confirm" })}
                    />
                  </div>
                )}
              </div>
            </AlertDialogBody>

            <AlertDialogFooter className="m-0 flex items-center justify-end gap-2 p-0 pt-5">
              <Button
                variant="ghost"
                size="sm"
                isDisabled={busy}
                onPress={() => onOpenChange(false)}
              >{intl.formatMessage({ id: "web.components.ui.confirm_dialog.cancel", defaultMessage: "Cancel", description: "Confirm dialog: button that closes the confirmation dialog without confirming; keep it short" })}</Button>
              <Button
                size="sm"
                variant={destructive ? "danger" : "primary"}
                isDisabled={!matches || busy}
                onPress={onConfirm}
              >
                {busy ? intl.formatMessage({ id: "web.components.ui.confirm_dialog.working", defaultMessage: "Working…", description: "Confirm dialog: confirm button label while the action is running" }) : (confirmLabel ?? intl.formatMessage({ id: "web.components.ui.confirm_dialog.confirm", defaultMessage: "Confirm", description: "Confirm dialog: default confirm button label when the caller gives none" }))}
              </Button>
            </AlertDialogFooter>
          </AlertDialogDialog>
        </AlertDialogContainer>
      </AlertDialogBackdrop>
    </AlertDialog>
  );
}
