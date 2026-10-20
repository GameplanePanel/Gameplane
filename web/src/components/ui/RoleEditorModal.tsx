import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
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
  Checkbox,
} from "@heroui/react";
import type { Role, PermissionGroup } from "@/types";
import { Roles } from "@/lib/endpoints";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface RoleEditorModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  role: Role | null;
  groups: PermissionGroup[];
  onSaved?: () => void;
}

export function RoleEditorModal({
  open,
  onOpenChange,
  role,
  groups,
  onSaved,
}: RoleEditorModalProps) {
  const intl = useOptionalIntl();
  const creating = role === null;
  const [name, setName] = useState(() => role?.name ?? "");
  const [description, setDescription] = useState(() => role?.description ?? "");
  const [selectedPerms, setSelectedPerms] = useState<Set<string>>(
    () => new Set(role?.permissions ?? []),
  );

  const save = useMutation({
    mutationFn: () => {
      const permissions = Array.from(selectedPerms);
      return role
        ? Roles.update(role.name, { description, permissions })
        : Roles.create({ name, description, permissions });
    },
    onSuccess: () => {
      onOpenChange(false);
      onSaved?.();
    },
  });

  const nameValid = /^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/.test(name);
  const submitDisabled = (creating && !nameValid) || save.isPending;

  const togglePermission = (perm: string) => {
    setSelectedPerms((prev) => {
      const next = new Set(prev);
      if (next.has(perm)) {
        next.delete(perm);
      } else {
        next.add(perm);
      }
      return next;
    });
  };

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange} key={role?.name ?? "new"}>
      <ModalBackdrop isDismissable={!save.isPending} isKeyboardDismissDisabled={save.isPending}>
        <ModalContainer>
          <ModalDialog className="w-[480px] max-w-[480px]">
            <ModalHeader>
              <ModalHeading>{role ? intl.formatMessage({ id: "web.components.ui.role_editor_modal.edit_role", defaultMessage: "Edit role: {name}", description: "Role editor modal: heading when editing an existing role; {name} is the role name" }, { name: role.name }) : intl.formatMessage({ id: "web.components.ui.role_editor_modal.new_role", defaultMessage: "New role", description: "Role editor modal: heading when creating a role" })}</ModalHeading>
              <Description className="text-sm leading-[18px] text-muted">{intl.formatMessage({ id: "web.components.ui.role_editor_modal.grant_a_curated_set_of_permissions", defaultMessage: "Grant a curated set of permissions.", description: "Role editor dialog: explanation under the title saying the role grants a curated set of permissions" })}</Description>
            </ModalHeader>

            <ModalBody className="gap-3">

            {save.error && <ErrorBanner err={save.error} onDismiss={() => save.reset()} />}

            {creating && (
              <div className="space-y-2">
                <Label htmlFor="role-name" className="text-xs">{intl.formatMessage({ id: "web.components.ui.role_editor_modal.name", defaultMessage: "Name", description: "Role editor dialog: label above the role name field; shown only when creating a new role, not when editing" })}</Label>
                <Input
                  id="role-name"
                  autoFocus
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={intl.formatMessage({ id: "web.components.ui.role_editor_modal.support", defaultMessage: "support", description: "Role editor dialog: example value shown inside the empty role name field; a sample role name, not a default value" })}
                />
              </div>
            )}

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="role-description" className="text-xs">{intl.formatMessage({ id: "web.components.ui.role_editor_modal.description", defaultMessage: "Description", description: "Role editor dialog: label above the optional description field for the role" })}</Label>
              <Input
                id="role-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                placeholder={intl.formatMessage({ id: "web.components.ui.role_editor_modal.manage_game_servers_backups_templates", defaultMessage: "Manage game servers, backups, templates, and schedules.", description: "Role editor dialog: example value shown inside the empty role description field, hinting what a role can cover" })}
              />
            </div>

            <div className="border border-border rounded-md p-3 h-72 overflow-y-auto space-y-3">
              {groups.map((group) => (
                <div key={group.resource} className="space-y-1">
                  <div className="text-[11px] leading-[14px] font-semibold text-muted uppercase tracking-[0.55px]">
                    {group.label}
                  </div>
                  <div className="grid grid-cols-2 gap-3">
                    {group.permissions.map((perm) => (
                      <Checkbox
                        key={perm.key}
                        isSelected={selectedPerms.has(perm.key)}
                        onChange={() => togglePermission(perm.key)}
                        isDisabled={save.isPending}
                        className="flex-row items-center gap-2"
                      >
                        <Checkbox.Control className="size-4">
                          <Checkbox.Indicator />
                        </Checkbox.Control>
                        <Checkbox.Content>
                          <span className="font-mono text-xs">{perm.key}</span>
                          {perm.namespaced && (
                            <span className="text-[9px] text-muted bg-[#94949426] px-1 rounded">{intl.formatMessage({ id: "web.components.ui.role_editor_modal.ns", defaultMessage: "ns", description: "Role editor dialog: small tag shown next to a permission name to mark it as namespaced; ns is short for namespace; keep it short" })}</span>
                          )}
                        </Checkbox.Content>
                      </Checkbox>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </ModalBody>

          <ModalFooter className="flex items-center justify-end gap-2">
            <Button
              variant="ghost"
              size="sm"
              className="h-9 px-4"
              onPress={() => onOpenChange(false)}
              isDisabled={save.isPending}
            >{intl.formatMessage({ id: "web.components.ui.role_editor_modal.cancel", defaultMessage: "Cancel", description: "Role editor dialog: button that closes the role editor without saving; keep it short" })}</Button>
            <Button
              size="sm"
              variant="primary"
              className="h-9 px-4"
              isDisabled={submitDisabled}
              onPress={() => save.mutate()}
            >
              {save.isPending
                ? intl.formatMessage({ id: "web.components.ui.role_editor_modal.saving", defaultMessage: "Saving…", description: "Role editor modal: save button label while the role is being saved" })
                : creating
                  ? intl.formatMessage({ id: "web.components.ui.role_editor_modal.create_role", defaultMessage: "Create role", description: "Role editor modal: save button label when creating a role" })
                  : intl.formatMessage({ id: "web.components.ui.role_editor_modal.save_role", defaultMessage: "Save role", description: "Role editor modal: save button label when editing a role" })}
            </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
