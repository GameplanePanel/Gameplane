import type { ReactNode } from "react";
import { useState } from "react";
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
  Select,
  ListBox,
  ListBoxItem,
  Separator,
} from "@heroui/react";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface EditUserDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  displayName?: string;
  email?: string;
  role?: string;
  roles?: string[];
  onSave?: (displayName: string, email: string, role: string) => void;
  isLoading?: boolean;
  /** Username shown under the heading, matching the inline form's Description. */
  username?: string;
  /** True when the row being edited is the signed-in user's own account. */
  isMe?: boolean;
  /**
   * Given a role name, whether that role grants user management. Used
   * (together with `isMe`) to warn about and block self-demotion. Defaults
   * to always-true, so the warning never triggers unless the caller opts
   * in — this keeps every pre-existing caller/test unaffected.
   */
  roleGrantsUserManagement?: (roleName: string) => boolean;
  /** Extra content rendered below the role select (e.g. namespace grants). */
  extraContent?: ReactNode;
  /** Error text from an external (API) failure, shown alongside client validation. */
  apiError?: string;
  /** When true, display name and email may be cleared (matching the inline form). */
  contactFieldsOptional?: boolean;
}

export function EditUserDialog({
  open,
  onOpenChange,
  displayName: initialDisplayName = "",
  email: initialEmail = "",
  role: initialRole = "viewer",
  roles = ["admin", "operator", "viewer"],
  onSave,
  isLoading = false,
  username,
  isMe = false,
  roleGrantsUserManagement = () => true,
  extraContent,
  apiError,
  contactFieldsOptional = false,
}: EditUserDialogProps) {
  const intl = useOptionalIntl();
  const [displayName, setDisplayName] = useState(initialDisplayName);
  const [email, setEmail] = useState(initialEmail);
  const [role, setRole] = useState(initialRole);
  const [error, setError] = useState<string>("");

  const wouldDemoteSelf = isMe && role !== initialRole && !roleGrantsUserManagement(role);
  const noChanges =
    displayName === initialDisplayName && email === initialEmail && role === initialRole;

  const handleSave = () => {
    setError("");
    if (!contactFieldsOptional) {
      if (!displayName.trim()) {
        setError(intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.display_name_is_required", defaultMessage: "Display name is required", description: "Edit user dialog: validation error when the display name is empty" }));
        return;
      }
      if (!email.trim()) {
        setError(intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.email_is_required", defaultMessage: "Email is required", description: "Edit user dialog: validation error when the email is empty" }));
        return;
      }
    }
    if (!role) {
      setError(intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.role_is_required", defaultMessage: "Role is required", description: "Edit user dialog: validation error when no role is selected" }));
      return;
    }
    onSave?.(displayName, email, role);
  };

  const handleClose = () => {
    setDisplayName(initialDisplayName);
    setEmail(initialEmail);
    setRole(initialRole);
    setError("");
    onOpenChange(false);
  };

  const displayError = error || apiError;

  return (
    <Modal isOpen={open} onOpenChange={handleClose}>
      <ModalBackdrop isDismissable={!isLoading} isKeyboardDismissDisabled={isLoading}>
        <ModalContainer>
          <ModalDialog className="w-[480px] max-w-[480px]">
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.edit_user", defaultMessage: "Edit user", description: "Edit user dialog: title of the dialog for editing a user; the username appears under the title" })}</ModalHeading>
              {username && <Description className="text-sm leading-[21px] text-muted">{username}</Description>}
            </ModalHeader>

            <ModalBody className="gap-3">

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="edit-display-name" className="text-xs">{intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.display_name", defaultMessage: "Display name", description: "Edit user dialog: label above the display name field; the same words are the field's placeholder" })}</Label>
              <Input
                id="edit-display-name"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                placeholder={intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.display_name", defaultMessage: "Display name", description: "Edit user dialog: label above the display name field; the same words are the field's placeholder" })}
                disabled={isLoading}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="edit-email" className="text-xs">{intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.email", defaultMessage: "Email", description: "Edit user dialog: label above the email address field" })}</Label>
              <Input
                id="edit-email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder={intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.email_example_com", defaultMessage: "email@example.com", description: "Edit user dialog: example value shown inside the empty email field; a sample address, keep it as written" })}
                type="email"
                disabled={isLoading}
              />
            </div>

            <div className="space-y-1">
              <Select
                className="flex flex-col gap-1.5"
                value={role}
                onChange={(v) => setRole(String(v))}
                isDisabled={isLoading}
              >
                <Label htmlFor="edit-role" className="text-xs">{intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.primary_role_cluster_wide", defaultMessage: "Primary role (cluster-wide)", description: "Edit user dialog: label above the primary role dropdown; the role applies across the whole cluster" })}</Label>
                <Select.Trigger
                  id="edit-role"
                  className="w-full items-center rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80"
                >
                  <Select.Value className="text-[var(--field-placeholder)]" />
                  <Select.Indicator className="ml-auto h-4 w-4" />
                </Select.Trigger>
                <Select.Popover className="rounded border border-border">
                  <ListBox className="p-0" aria-label={intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.primary_role", defaultMessage: "Primary role", description: "Edit user dialog: screen-reader name of the list of roles in the primary role dropdown; not visible on screen" })}>
                    {roles.map((r) => (
                      <ListBoxItem key={r} id={r}>
                        {r}
                      </ListBoxItem>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>
              {wouldDemoteSelf && (
                <p className="pt-1 text-[11px] text-danger">{intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.you_can_t_remove_your_own_ability_to", defaultMessage: "You can’t remove your own ability to manage users.", description: "Edit user dialog: warning shown under the role dropdown when the change would remove the user's own ability to manage users" })}</p>
              )}
            </div>

            {extraContent && (
              <>
                <Separator className="my-0" />
                {extraContent}
              </>
            )}

            {displayError && (<p className="text-xs text-danger" role="alert">{displayError}</p>)}
          </ModalBody>

          <ModalFooter className="flex items-center justify-end gap-2">
            <Button variant="ghost" size="sm" className="text-xs" onPress={handleClose} isDisabled={isLoading}>{intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.cancel", defaultMessage: "Cancel", description: "Edit user dialog: button that closes the dialog without saving; keep it short" })}</Button>
            <Button
              size="sm"
              variant="primary"
              className="text-xs"
              isDisabled={isLoading || noChanges || wouldDemoteSelf}
              onPress={handleSave}
            >
              {isLoading
                ? intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.saving", defaultMessage: "Saving…", description: "Edit user dialog: save button label while the update is in progress" })
                : intl.formatMessage({ id: "web.components.ui.admin.edit_user_dialog.save_changes", defaultMessage: "Save changes", description: "Edit user dialog: save button label" })}
            </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
