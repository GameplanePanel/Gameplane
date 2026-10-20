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
} from "@heroui/react";
import { useOptionalIntl } from "@/lib/i18n/format";

const MIN_PASSWORD_LEN = 12;

export interface ResetPasswordDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  username?: string;
  onReset?: (password: string) => void;
  isLoading?: boolean;
  /**
   * When true, the submit button is preemptively disabled while the
   * password is shorter than the minimum length, instead of relying on a
   * click to reveal the validation error. Defaults to false, preserving
   * the original click-to-validate behavior.
   */
  disableSubmitUntilValid?: boolean;
  /** Error text from an external (API) failure, shown alongside client validation. */
  apiError?: string;
  submitLabel?: string;
}

export function ResetPasswordDialog({
  open,
  onOpenChange,
  username = "",
  onReset,
  isLoading = false,
  disableSubmitUntilValid = false,
  apiError,
  submitLabel,
}: ResetPasswordDialogProps) {
  const intl = useOptionalIntl();
  const [password, setPassword] = useState("");
  const [validationError, setValidationError] = useState<string>("");

  const submitDisabled =
    isLoading || (disableSubmitUntilValid && password.length < MIN_PASSWORD_LEN);

  const liveError = disableSubmitUntilValid && password.length > 0 && password.length < MIN_PASSWORD_LEN ? intl.formatMessage({ id: "web.components.ui.admin.reset_password_dialog.at_least_characters", defaultMessage: "At least {min} characters.", description: "Reset password dialog: live validation under the password field while it is shorter than the minimum; {min} is the minimum password length" }, { min: MIN_PASSWORD_LEN }) : "";

  const handleReset = () => {
    setValidationError("");
    if (!password.trim()) {
      setValidationError(intl.formatMessage({ id: "web.components.ui.admin.reset_password_dialog.password_is_required", defaultMessage: "Password is required", description: "Reset password dialog: validation error when the password is empty" }));
      return;
    }
    if (password.length < MIN_PASSWORD_LEN) {
      setValidationError(intl.formatMessage({ id: "web.components.ui.admin.reset_password_dialog.must_be_at_least_characters", defaultMessage: "Must be at least {min} characters.", description: "Reset password dialog: validation error when the password is shorter than the minimum; {min} is the minimum password length" }, { min: MIN_PASSWORD_LEN }));
      return;
    }
    onReset?.(password);
  };

  const handleClose = () => {
    setPassword("");
    setValidationError("");
    onOpenChange(false);
  };

  const displayError = validationError || liveError || apiError;

  return (
    <Modal isOpen={open} onOpenChange={handleClose}>
      <ModalBackdrop isDismissable={!isLoading} isKeyboardDismissDisabled={isLoading}>
        <ModalContainer>
          <ModalDialog className="w-[480px] max-w-[480px]">
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.components.ui.admin.reset_password_dialog.reset_password_for_username", defaultMessage: "Reset password for {username}", description: "Reset password dialog: heading; {username} is the account whose password is being reset" }, { username: username })}</ModalHeading>
              <Description className="text-sm leading-[21px] text-muted">{intl.formatMessage({ id: "web.components.ui.admin.reset_password_dialog.they_will_need_to_sign_in_again_with", defaultMessage: "They will need to sign in again with the new password.", description: "Reset password dialog: explanation under the Reset password title saying the user must sign in again with the new password" })}</Description>
            </ModalHeader>

            <ModalBody className="gap-4">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="reset-password" className="text-xs">{intl.formatMessage({ id: "web.components.ui.admin.reset_password_dialog.new_password", defaultMessage: "New password", description: "Reset password dialog: label above the new password field" })}</Label>
              <Input
                id="reset-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={intl.formatMessage({ id: "web.components.ui.admin.reset_password_dialog.password_hint", defaultMessage: "At least {min} characters", description: "Reset password dialog: password field placeholder; {min} is the minimum password length" }, { min: MIN_PASSWORD_LEN })}
                type="password"
                disabled={isLoading}
              />
              {displayError && (
                <p className="mt-1 text-xs text-danger" role="alert">{displayError}</p>
              )}
            </div>
          </ModalBody>

          <ModalFooter className="flex items-center justify-end gap-2">
            <Button variant="ghost" size="sm" className="text-xs" onPress={handleClose} isDisabled={isLoading}>{intl.formatMessage({ id: "web.components.ui.admin.reset_password_dialog.cancel", defaultMessage: "Cancel", description: "Reset password dialog: button that closes the dialog without resetting the password; keep it short" })}</Button>
            <Button
              size="sm"
              variant="primary"
              className="text-xs"
              isDisabled={submitDisabled}
              onPress={handleReset}
            >
              {isLoading ? intl.formatMessage({ id: "web.components.ui.admin.reset_password_dialog.resetting", defaultMessage: "Resetting…", description: "Reset password dialog: submit button label while the reset is in progress" }) : (submitLabel ?? intl.formatMessage({ id: "web.components.ui.admin.reset_password_dialog.reset_password", defaultMessage: "Reset password", description: "Reset password dialog: default submit button label" }))}
            </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
