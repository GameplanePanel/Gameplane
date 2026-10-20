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
} from "@heroui/react";
import { useOptionalIntl } from "@/lib/i18n/format";

const MIN_PASSWORD_LEN = 12;

export interface InviteUserDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onInvite?: (
    username: string,
    displayName: string,
    email: string,
    password: string,
    role?: string,
  ) => void;
  isLoading?: boolean;
  /**
   * Role names offered in a "Role" select. When omitted (or empty), no role
   * field is rendered and `onInvite` is called with its original 4-arg
   * signature — this keeps every pre-existing caller/test unaffected.
   */
  roles?: string[];
  /**
   * When true, display name/email/password are optional (matching the
   * "create a local account, leave password blank for an OIDC invite"
   * flow) — only username is required, and password is validated for
   * minimum length only when non-empty. Defaults to false, preserving the
   * dialog's original all-fields-required behavior.
   */
  contactFieldsOptional?: boolean;
  /**
   * When true, the submit button is preemptively disabled while the
   * username is empty or the password is present-but-too-short, instead of
   * relying on a click to reveal the validation error. Defaults to false,
   * preserving the original click-to-validate behavior.
   */
  disableSubmitUntilValid?: boolean;
  /** Error text from an external (API) failure, shown alongside client validation. */
  apiError?: string;
  submitLabel?: string;
}

export function InviteUserDialog({
  open,
  onOpenChange,
  onInvite,
  isLoading = false,
  roles,
  contactFieldsOptional = false,
  disableSubmitUntilValid = false,
  apiError,
  submitLabel,
}: InviteUserDialogProps) {
  const intl = useOptionalIntl();
  const hasRoles = !!roles && roles.length > 0;
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState(roles?.includes("viewer") ? "viewer" : (roles?.[0] ?? "viewer"));
  const [error, setError] = useState<string>("");

  const passwordTooShort = password.length > 0 && password.length < MIN_PASSWORD_LEN;
  const liveError = disableSubmitUntilValid && passwordTooShort ? intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.at_least_characters", defaultMessage: "At least {min} characters.", description: "Invite user dialog: live validation under the password field while it is shorter than the minimum; {min} is the minimum password length" }, { min: MIN_PASSWORD_LEN }) : "";
  const submitDisabled =
    isLoading || (disableSubmitUntilValid && (!username.trim() || passwordTooShort));

  const handleInvite = () => {
    setError("");
    if (!username.trim()) {
      setError(intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.username_is_required", defaultMessage: "Username is required", description: "Invite user dialog: validation error when the username is empty" }));
      return;
    }
    if (!contactFieldsOptional) {
      if (!displayName.trim()) {
        setError(intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.display_name_is_required", defaultMessage: "Display name is required", description: "Invite user dialog: validation error when the display name is empty" }));
        return;
      }
      if (!email.trim()) {
        setError(intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.email_is_required", defaultMessage: "Email is required", description: "Invite user dialog: validation error when the email is empty" }));
        return;
      }
      if (!password.trim()) {
        setError(intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.password_is_required", defaultMessage: "Password is required", description: "Invite user dialog: validation error when the initial password is empty" }));
        return;
      }
    }
    if (passwordTooShort) {
      setError(
        contactFieldsOptional
          ? intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.at_least_characters", defaultMessage: "At least {min} characters.", description: "Invite user dialog: live validation under the password field while it is shorter than the minimum; {min} is the minimum password length" }, { min: MIN_PASSWORD_LEN })
          : intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.password_must_be_at_least_characters", defaultMessage: "Password must be at least {min} characters", description: "Invite user dialog: validation error when the initial password is shorter than the minimum; {min} is the minimum password length" }, { min: MIN_PASSWORD_LEN }),
      );
      return;
    }
    if (hasRoles) {
      onInvite?.(username, displayName, email, password, role);
    } else {
      onInvite?.(username, displayName, email, password);
    }
  };

  const handleClose = () => {
    setUsername("");
    setDisplayName("");
    setEmail("");
    setPassword("");
    setRole(roles?.includes("viewer") ? "viewer" : (roles?.[0] ?? "viewer"));
    setError("");
    onOpenChange(false);
  };

  const displayError = error || liveError || apiError;

  return (
    <Modal isOpen={open} onOpenChange={handleClose}>
      <ModalBackdrop isDismissable={!isLoading} isKeyboardDismissDisabled={isLoading}>
        <ModalContainer>
          <ModalDialog className="w-[480px] max-w-[480px] rounded-xl">
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.invite_user", defaultMessage: "Invite user", description: "Invite user dialog: heading and default submit button label" })}</ModalHeading>
              {contactFieldsOptional && (
                <Description className="text-sm leading-[21px] text-muted">{intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.create_a_local_account_leave_password", defaultMessage: "Create a local account. Leave password blank to send an OIDC invite later.", description: "Invite user dialog: explanation under the Invite user title saying a local account is created, and that a blank password sends an OIDC invite later; OIDC is a technical term, usually left untranslated" })}</Description>
              )}
            </ModalHeader>

            <ModalBody className="gap-3">

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="invite-username" className="text-xs">{intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.username", defaultMessage: "Username", description: "Invite user dialog: label above the username field" })}</Label>
              <Input
                id="invite-username"
                fullWidth
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder={intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.alice", defaultMessage: "alice", description: "Invite user dialog: example value shown inside the empty username field; a sample name, not a default value" })}
                disabled={isLoading}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="invite-display-name" className="text-xs">{intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.display_name", defaultMessage: "Display name", description: "Invite user dialog: label above the display name field" })}</Label>
              <Input
                id="invite-display-name"
                fullWidth
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                placeholder={intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.alice_operator", defaultMessage: "Alice Operator", description: "Invite user dialog: example value shown inside the empty display name field; a sample name, not a default value" })}
                disabled={isLoading}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="invite-email" className="text-xs">{intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.email", defaultMessage: "Email", description: "Invite user dialog: label above the email field" })}</Label>
              <Input
                id="invite-email"
                fullWidth
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder={intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.alice_example_com", defaultMessage: "alice@example.com", description: "Invite user dialog: example value shown inside the empty email field; a sample address, keep it as written" })}
                type="email"
                disabled={isLoading}
              />
            </div>

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="invite-password" className="text-xs">{intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.initial_password", defaultMessage: "Initial password", description: "Invite user dialog: label above the initial password field, which can be left blank" })}</Label>
              <Input
                id="invite-password"
                fullWidth
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.password_hint", defaultMessage: "At least {min} characters", description: "Invite user dialog: password field placeholder; {min} is the minimum password length" }, { min: MIN_PASSWORD_LEN })}
                type="password"
                disabled={isLoading}
              />
            </div>

            {hasRoles && (
              <div>
                <Select className="flex flex-col gap-1.5" value={role} onChange={(v) => setRole(String(v))} isDisabled={isLoading}>
                  <Label htmlFor="invite-role" className="text-xs">{intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.role", defaultMessage: "Role", description: "Invite user dialog: label above the role dropdown, shown only when roles are available" })}</Label>
                  <Select.Trigger
                    id="invite-role"
                    className="w-full items-center rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80"
                  >
                    <Select.Value className="text-[var(--field-placeholder)]" />
                    <Select.Indicator className="ml-auto h-4 w-4" />
                  </Select.Trigger>
                  <Select.Popover className="rounded border border-border">
                    <ListBox className="p-0" aria-label={intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.role", defaultMessage: "Role", description: "Invite user dialog: label above the role dropdown, shown only when roles are available" })}>
                      {roles?.map((r) => (
                        <ListBoxItem key={r} id={r}>
                          {r}
                        </ListBoxItem>
                      ))}
                    </ListBox>
                  </Select.Popover>
                </Select>
              </div>
            )}

            {displayError && (<p className="text-xs text-danger" role="alert">{displayError}</p>)}
          </ModalBody>

          <ModalFooter className="flex items-center justify-end gap-2">
            <Button variant="ghost" size="sm" className="text-xs" onPress={handleClose} isDisabled={isLoading}>{intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.cancel", defaultMessage: "Cancel", description: "Invite user dialog: button that closes the dialog without inviting the user; keep it short" })}</Button>
            <Button
              size="sm"
              variant="primary"
              className="text-xs"
              isDisabled={submitDisabled}
              onPress={handleInvite}
            >
              {isLoading ? intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.inviting", defaultMessage: "Inviting…", description: "Invite user dialog: submit button label while the invite is in progress" }) : (submitLabel ?? intl.formatMessage({ id: "web.components.ui.admin.invite_user_dialog.invite_user", defaultMessage: "Invite user", description: "Invite user dialog: heading and default submit button label" }))}
            </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
