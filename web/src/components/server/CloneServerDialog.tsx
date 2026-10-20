import { useResourceClient, useResourceTarget, type ResourceTarget, serverLink, resourceKey } from "@/lib/resourceTarget";
import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
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
  TextField,
  Input,
  Label,
  Description,
  FieldError,
} from "@heroui/react";

import { APIError } from "@/lib/api";
import { isValidK8sName } from "@/lib/validation";
import { errorTextIntl } from "@/lib/errors";
import type { IntlShape } from "react-intl";
import { useOptionalIntl } from "@/lib/i18n/format";

interface Props {
  target?: ResourceTarget;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  sourceName: string;
  ns?: string;
  onCloned?: () => void;
}

function cloneErrorMessage(err: unknown, name: string, intl: IntlShape): string {
  if (err instanceof APIError) {
    if (err.status === 409) return intl.formatMessage({ id: "web.components.server.clone_server_dialog.name_taken", defaultMessage: "A server named {name} already exists.", description: "Clone server dialog: error when a server with the new name already exists; {name} is the requested name" }, { name });
    if (err.status === 403) return intl.formatMessage({ id: "web.components.server.clone_server_dialog.forbidden", defaultMessage: "Your role does not allow cloning servers.", description: "Clone server dialog: error when the user's role may not clone servers" });
    return err.body.slice(0, 240) || intl.formatMessage({ id: "web.components.server.clone_server_dialog.clone_failed_status", defaultMessage: "Clone failed ({status}).", description: "Clone server dialog: fallback error with the HTTP status; {status} is the status code" }, { status: err.status });
  }
  return errorTextIntl(intl, err, intl.formatMessage({ id: "web.components.server.clone_server_dialog.unknown_error", defaultMessage: "Unknown error", description: "Clone server dialog: fallback error when the cause is not known" }));
}

export function CloneServerDialog({
  target: explicitTarget,
  open,
  onOpenChange,
  sourceName,
  ns,
  onCloned,
}: Props) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name: sourceName, namespace: ns }, explicitTarget);
  const resourceClient = useResourceClient(resourceTarget);
  const { Servers } = resourceClient;
  const qc = useQueryClient();
  const nav = useNavigate();
  const [newName, setNewName] = useState("");
  // Reset the suggested name whenever the dialog (re)opens or the source
  // server changes. Adjusted directly during render (not in an effect),
  // gated on the previously-seen (open, sourceName) pair.
  const [resetFor, setResetFor] = useState<{ open: boolean; sourceName: string }>({
    open: false,
    sourceName,
  });
  if (open !== resetFor.open || sourceName !== resetFor.sourceName) {
    setResetFor({ open, sourceName });
    if (open) setNewName(`${sourceName.slice(0, 58)}-copy`);
  }

  const clone = useMutation({
    mutationFn: () => Servers.clone(sourceName, newName, ns),
    onSuccess: async (created) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      await qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "servers") });
      await qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "my-servers") });
      onOpenChange(false);
      onCloned?.();
      await nav(serverLink({ ...resourceTarget, name: created.metadata.name, namespace: created.metadata.namespace ?? resourceTarget.namespace }));
    },
  });

  const valid = isValidK8sName(newName);

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop isDismissable={!clone.isPending} isKeyboardDismissDisabled={clone.isPending}>
        <ModalContainer>
          <ModalDialog>
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.components.server.clone_server_dialog.clone_server", defaultMessage: "Clone server", description: "Clone server dialog: heading and submit button label" })}</ModalHeading>
              <Description className="text-sm text-muted">{intl.formatMessage({ id: "web.components.server.clone_server_dialog.creates_a_new_server_with_the_same", defaultMessage: "Creates a new server with the same configuration. World data is not copied.", description: "Clone server dialog: explanation under the Clone server title saying the copy gets the same configuration and that world data is not copied" })}</Description>
            </ModalHeader>

            <ModalBody className="gap-4">

              <TextField isInvalid={!valid || clone.isError}>
                <Label className="text-xs">{intl.formatMessage({ id: "web.components.server.clone_server_dialog.new_name", defaultMessage: "New name", description: "Clone server dialog: label above the new name field for the copied server" })}</Label>
                <Input
                  autoFocus
                  value={newName}
                  onChange={(e) => setNewName(e.target.value)}
                  spellCheck={false}
                  placeholder={intl.formatMessage({ id: "web.components.server.clone_server_dialog.mc_survival_copy", defaultMessage: "mc-survival-copy", description: "Clone server dialog: example value shown inside the empty new name field; a sample name, not a default value" })}
                  className="mt-1"
                />
                {!valid && (
                  <FieldError className="mt-1 text-xs">{intl.formatMessage({ id: "web.components.server.clone_server_dialog.name_must_be_lowercase_letters_digits", defaultMessage: "Name must be lowercase letters, digits, dashes (max 63)", description: "Clone server dialog: validation error under the new name field shown when the name has invalid characters or is longer than the limit" })}</FieldError>
                )}
                {clone.isError && (
                  <FieldError className="mt-1 text-xs">
                    {cloneErrorMessage(clone.error, newName, intl)}
                  </FieldError>
                )}
              </TextField>
            </ModalBody>

            <ModalFooter className="flex items-center justify-end gap-2">
              <Button
                variant="ghost"
                size="sm"
                onPress={() => onOpenChange(false)}
                isDisabled={clone.isPending}
              >{intl.formatMessage({ id: "web.components.server.clone_server_dialog.cancel", defaultMessage: "Cancel", description: "Clone server dialog: button that closes the dialog without cloning; keep it short" })}</Button>
              <Button
                size="sm"
                variant="primary"
                isDisabled={!valid || clone.isPending}
                onPress={() => clone.mutate()}
              >
                {clone.isPending ? intl.formatMessage({ id: "web.components.server.clone_server_dialog.cloning", defaultMessage: "Cloning…", description: "Clone server dialog: submit button label while the clone is created" }) : intl.formatMessage({ id: "web.components.server.clone_server_dialog.clone_server", defaultMessage: "Clone server", description: "Clone server dialog: heading and submit button label" })}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
