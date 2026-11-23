// SFTP dialogs shared by the personal SFTP page (routes/ProfileSftp.tsx) and
// the admin SFTP drawer (components/ui/admin/SftpAccessDrawer.tsx). Designs:
// Gameplane/Dialog/Add SSH key (1006926387-2214, -2231 invalid key),
// Generate SFTP password (-2253), SFTP password shown once (-2271).
// The generated password exists only in the caller's state; it is never
// put in a query cache.

import { useState } from "react";
import {
  Button,
  Description,
  Input,
  Label,
  Modal,
  ModalBackdrop,
  ModalBody,
  ModalContainer,
  ModalDialog,
  ModalFooter,
  ModalHeader,
  ModalHeading,
} from "@heroui/react";
import { AlertCircle, Copy } from "lucide-react";
import { errorTextIntl } from "@/lib/errors";
import { useOptionalIntl } from "@/lib/i18n/format";
import {
  addSftpKey,
  generateSftpPassword,
  SFTP_PASSWORD_DEFAULT_LENGTH,
  SFTP_PASSWORD_MAX_LENGTH,
  SFTP_PASSWORD_MIN_LENGTH,
  type SftpKey,
} from "@/lib/api";

const dialogClass = "w-[480px] max-w-[480px] rounded-xl";

// AddKeyDialog stores one OpenSSH public key. The server does the parsing;
// its refusal (server.sftp.key_invalid, key_duplicate, ...) shows inline.
export function AddKeyDialog({
  open,
  onClose,
  onAdded,
  addKey = addSftpKey,
}: {
  open: boolean;
  onClose: () => void;
  onAdded: (key: SftpKey) => void;
  /** Injection seam for tests; production uses the real client. */
  addKey?: (name: string, publicKey: string) => Promise<SftpKey>;
}) {
  const intl = useOptionalIntl();
  const [name, setName] = useState("");
  const [publicKey, setPublicKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reset = () => {
    setName("");
    setPublicKey("");
    setError(null);
  };
  const close = () => {
    if (busy) return;
    reset();
    onClose();
  };
  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const key = await addKey(name.trim(), publicKey.trim());
      reset();
      onAdded(key);
    } catch (err) {
      setError(errorTextIntl(intl, err, intl.formatMessage({ id: "web.components.sftp.add_key_failed", defaultMessage: "Could not add the key.", description: "SFTP add-key dialog: fallback error when the server gives no message" })));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal isOpen={open} onOpenChange={(o) => !o && close()}>
      <ModalBackdrop isDismissable={!busy} isKeyboardDismissDisabled={busy}>
        <ModalContainer>
          <ModalDialog className={dialogClass}>
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.components.sftp.add_ssh_key", defaultMessage: "Add SSH key", description: "SFTP add-key dialog: heading and the title of the dialog" })}</ModalHeading>
              <Description className="text-sm text-muted">{intl.formatMessage({ id: "web.components.sftp.paste_one_openssh_public_key", defaultMessage: "Paste one OpenSSH public key.", description: "SFTP add-key dialog: instruction under the heading" })}</Description>
            </ModalHeader>
            <ModalBody className="space-y-4">
              {error && (
                <div role="alert" className="flex items-start gap-2 rounded-lg border border-danger/40 p-3 text-sm text-danger">
                  <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>{error}</span>
                </div>
              )}
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="sftp-key-name" className="text-xs">{intl.formatMessage({ id: "web.components.sftp.name", defaultMessage: "Name", description: "SFTP add-key dialog: label above the key name field" })}</Label>
                <Input
                  id="sftp-key-name"
                  fullWidth
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={intl.formatMessage({ id: "web.components.sftp.laptop", defaultMessage: "laptop", description: "SFTP add-key dialog: example key name shown in the empty name field; a sample name, not a default value" })}
                  disabled={busy}
                />
              </div>
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="sftp-key-public" className="text-xs">{intl.formatMessage({ id: "web.components.sftp.public_key", defaultMessage: "Public key", description: "SFTP add-key dialog: label above the public key text box" })}</Label>
                <textarea
                  id="sftp-key-public"
                  rows={4}
                  value={publicKey}
                  onChange={(e) => setPublicKey(e.target.value)}
                  placeholder={intl.formatMessage({ id: "web.components.sftp.public_key_placeholder", defaultMessage: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIN7d... laptop", description: "SFTP add-key dialog: example public key in the empty key box; keep the key text, only the trailing name may be translated" })}
                  disabled={busy}
                  className="w-full rounded-xl border border-default-300 bg-default-100 p-3 font-mono text-xs text-foreground placeholder:text-foreground/40"
                />
                <Description className="text-xs text-muted">{intl.formatMessage({ id: "web.components.sftp.paste_one_line_from_your_pub", defaultMessage: "Paste one line from your .pub file. Ed25519, ECDSA and RSA keys are accepted.", description: "SFTP add-key dialog: hint under the public key box listing the accepted key types" })}</Description>
              </div>
            </ModalBody>
            <ModalFooter className="flex items-center justify-end gap-2">
              <Button variant="ghost" size="sm" onPress={close} isDisabled={busy}>{intl.formatMessage({ id: "web.components.sftp.cancel", defaultMessage: "Cancel", description: "SFTP dialogs: button that closes the dialog without changes; keep it short" })}</Button>
              <Button size="sm" onPress={() => void submit()} isDisabled={busy || name.trim() === "" || publicKey.trim() === ""}>
                {intl.formatMessage({ id: "web.components.sftp.add_key", defaultMessage: "Add key", description: "SFTP add-key dialog: submit button label; keep it short" })}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}

// GeneratePasswordDialog picks a length (20 to 40, default 32) and asks the
// server to replace the password. The clear text comes back once, through
// onGenerated.
export function GeneratePasswordDialog({
  open,
  replacing,
  onClose,
  onGenerated,
  generate = generateSftpPassword,
}: {
  open: boolean;
  /** true when a password already exists: the copy says it is replaced. */
  replacing: boolean;
  onClose: () => void;
  onGenerated: (password: string, length: number) => void;
  generate?: (length: number) => Promise<{ password: string; length: number }>;
}) {
  const intl = useOptionalIntl();
  const [length, setLength] = useState<number>(SFTP_PASSWORD_DEFAULT_LENGTH);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const close = () => {
    if (busy) return;
    setLength(SFTP_PASSWORD_DEFAULT_LENGTH);
    setError(null);
    onClose();
  };
  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const out = await generate(length);
      setLength(SFTP_PASSWORD_DEFAULT_LENGTH);
      onGenerated(out.password, out.length);
    } catch (err) {
      setError(errorTextIntl(intl, err, intl.formatMessage({ id: "web.components.sftp.generate_failed", defaultMessage: "Could not generate a password.", description: "SFTP generate dialog: fallback error when the server gives no message" })));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal isOpen={open} onOpenChange={(o) => !o && close()}>
      <ModalBackdrop isDismissable={!busy} isKeyboardDismissDisabled={busy}>
        <ModalContainer>
          <ModalDialog className={dialogClass}>
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.components.sftp.generate_sftp_password", defaultMessage: "Generate SFTP password", description: "SFTP generate dialog: heading and the title of the dialog" })}</ModalHeading>
              {replacing && (
                <Description className="text-sm text-muted">{intl.formatMessage({ id: "web.components.sftp.replaces_your_current_password", defaultMessage: "Replaces your current password.", description: "SFTP generate dialog: note that the new password replaces the existing one" })}</Description>
              )}
            </ModalHeader>
            <ModalBody className="space-y-4">
              {error && (
                <div role="alert" className="flex items-start gap-2 rounded-lg border border-danger/40 p-3 text-sm text-danger">
                  <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>{error}</span>
                </div>
              )}
              <div className="space-y-2">
                <div className="flex items-baseline justify-between">
                  <Label htmlFor="sftp-password-length" className="text-xs">{intl.formatMessage({ id: "web.components.sftp.length", defaultMessage: "Length", description: "SFTP generate dialog: label of the password length slider" })}</Label>
                  <span className="text-sm">{intl.formatMessage({ id: "web.components.sftp.length_characters", defaultMessage: "{count} characters", description: "SFTP generate dialog: the chosen password length; {count} is a number" }, { count: length })}</span>
                </div>
                <input
                  id="sftp-password-length"
                  type="range"
                  min={SFTP_PASSWORD_MIN_LENGTH}
                  max={SFTP_PASSWORD_MAX_LENGTH}
                  step={1}
                  value={length}
                  onChange={(e) => setLength(Number(e.target.value))}
                  disabled={busy}
                  className="w-full accent-primary"
                />
                <div className="flex justify-between text-xs text-muted">
                  <span>{SFTP_PASSWORD_MIN_LENGTH}</span>
                  <span>{SFTP_PASSWORD_MAX_LENGTH}</span>
                </div>
              </div>
              <Description className="text-xs text-muted">{intl.formatMessage({ id: "web.components.sftp.each_character_is_drawn", defaultMessage: "Each character is drawn at random from the 94 printable ASCII characters (no spaces) with a cryptographically secure generator.", description: "SFTP generate dialog: explains how the password is generated" })}</Description>
            </ModalBody>
            <ModalFooter className="flex items-center justify-end gap-2">
              <Button variant="ghost" size="sm" onPress={close} isDisabled={busy}>{intl.formatMessage({ id: "web.components.sftp.cancel", defaultMessage: "Cancel", description: "SFTP dialogs: button that closes the dialog without changes; keep it short" })}</Button>
              <Button size="sm" onPress={() => void submit()} isDisabled={busy}>
                {intl.formatMessage({ id: "web.components.sftp.generate", defaultMessage: "Generate", description: "SFTP generate dialog: submit button label; keep it short" })}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}

// PasswordShownOnceDialog shows the clear text once. It cannot be dismissed
// by clicking outside or pressing Escape; only Done closes it.
export function PasswordShownOnceDialog({
  password,
  length,
  onDone,
}: {
  password: string | null;
  length: number;
  onDone: () => void;
}) {
  const intl = useOptionalIntl();
  const [copied, setCopied] = useState(false);
  const copy = () => {
    if (!password) return;
    try {
      void navigator.clipboard.writeText(password).then(
        () => setCopied(true),
        () => setCopied(false),
      );
    } catch {
      setCopied(false);
    }
  };

  return (
    <Modal isOpen={password !== null}>
      <ModalBackdrop isDismissable={false} isKeyboardDismissDisabled>
        <ModalContainer>
          <ModalDialog className={dialogClass}>
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.components.sftp.your_sftp_password", defaultMessage: "Your SFTP password", description: "SFTP shown-once dialog: heading" })}</ModalHeading>
              <Description className="text-sm text-muted">{intl.formatMessage({ id: "web.components.sftp.copy_it_now", defaultMessage: "Copy it now. Gameplane stores only a hash and cannot show it again.", description: "SFTP shown-once dialog: instruction under the heading" })}</Description>
            </ModalHeader>
            <ModalBody className="space-y-4">
              <div role="alert" className="flex items-start gap-2 rounded-lg border border-danger/40 p-3 text-sm text-danger">
                <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                <span>{intl.formatMessage({ id: "web.components.sftp.you_will_not_see_this_again", defaultMessage: "You will not see this again. Store it in a password manager before you close this dialog.", description: "SFTP shown-once dialog: warning that the password cannot be shown again" })}</span>
              </div>
              <code data-testid="sftp-password-value" className="block break-all rounded-lg bg-default-100 p-3 font-mono text-sm">{password}</code>
              <div className="flex items-center gap-3">
                <Button variant="ghost" size="sm" onPress={copy}>
                  <Copy className="h-4 w-4" />
                  {copied
                    ? intl.formatMessage({ id: "web.components.sftp.copied", defaultMessage: "Copied", description: "SFTP shown-once dialog: button label after the password was copied" })
                    : intl.formatMessage({ id: "web.components.sftp.copy", defaultMessage: "Copy", description: "SFTP shown-once dialog: button that copies the password to the clipboard; keep it short" })}
                </Button>
                <span className="text-sm text-muted">{intl.formatMessage({ id: "web.components.sftp.characters", defaultMessage: "{count} characters", description: "SFTP shown-once dialog: password length; {count} is a number" }, { count: length })}</span>
              </div>
            </ModalBody>
            <ModalFooter className="flex items-center justify-end">
              <Button size="sm" onPress={onDone}>{intl.formatMessage({ id: "web.components.sftp.done", defaultMessage: "Done", description: "SFTP dialogs: button that closes the dialog; keep it short" })}</Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
