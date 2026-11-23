// Admin view of one user's SFTP credentials (spec 040 US3; design
// Screen/Users & RBAC — SFTP access (admin) 309702331-1). Opened from the
// user's row menu on Users & RBAC. An admin can remove a key or revoke the
// password; the user keeps their account. Needs users:manage (the Users page
// is already gated by it). Revocation ends open sessions server-side.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Button,
  Description,
  Modal,
  ModalBackdrop,
  ModalBody,
  ModalContainer,
  ModalDialog,
  ModalFooter,
  ModalHeader,
  ModalHeading,
} from "@heroui/react";
import { Trash2 } from "lucide-react";
import { errorTextIntl } from "@/lib/errors";
import { formatRelative } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import { getUserSftp, removeUserSftpKey, revokeUserSftpPassword } from "@/lib/api";

export function SftpAccessDrawer({
  userId,
  username,
  onClose,
}: {
  userId: number;
  username: string;
  onClose: () => void;
}) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const queryKey = ["users", userId, "sftp"] as const;

  const { data, error, isPending } = useQuery({
    queryKey,
    queryFn: () => getUserSftp(userId),
  });

  const refresh = () => {
    void qc.invalidateQueries({ queryKey });
  };
  const removeKey = useMutation({
    mutationFn: (keyId: number) => removeUserSftpKey(userId, keyId),
    onSuccess: refresh,
  });
  const revoke = useMutation({
    mutationFn: () => revokeUserSftpPassword(userId),
    onSuccess: refresh,
  });

  const actionError = removeKey.error ?? revoke.error;

  return (
    <Modal isOpen onOpenChange={(open) => !open && onClose()}>
      <ModalBackdrop>
        <ModalContainer>
          <ModalDialog className="ml-auto h-full w-[420px] max-w-full rounded-none">
            <ModalHeader>
              <ModalHeading>
                {intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.title", defaultMessage: "SFTP access · {username}", description: "Admin SFTP drawer: heading; {username} is the account name" }, { username })}
              </ModalHeading>
              <Description className="text-sm text-muted">
                {intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.description", defaultMessage: "Remove a key or revoke the password. The user keeps their account.", description: "Admin SFTP drawer: explains that the admin removes keys or revokes the password and the account stays" })}
              </Description>
            </ModalHeader>
            <ModalBody className="space-y-6">
              {error ? (
                <p role="alert" className="text-sm text-danger">
                  {errorTextIntl(intl, error, intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.load_failed", defaultMessage: "Could not load SFTP access.", description: "Admin SFTP drawer: error when the user's SFTP credentials cannot be loaded" }))}
                </p>
              ) : isPending || !data ? (
                <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.loading", defaultMessage: "Loading…", description: "Admin SFTP drawer: loading text" })}</p>
              ) : (
                <>
                  <section className="space-y-3">
                    <h3 className="text-sm font-semibold">
                      {intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.ssh_keys_count", defaultMessage: "SSH keys ({count})", description: "Admin SFTP drawer: heading of the key list with the number of keys; {count} is a number" }, { count: data.keys.length })}
                    </h3>
                    {data.keys.length === 0 && (
                      <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.no_keys", defaultMessage: "No SSH keys.", description: "Admin SFTP drawer: shown when the user has no SSH keys" })}</p>
                    )}
                    {data.keys.map((k) => (
                      <div key={k.id} className="flex items-start justify-between gap-3">
                        <div className="min-w-0 space-y-1">
                          <p className="font-mono text-sm break-all">{intl.formatMessage({ id: "web.components.sftp.key_name_type", defaultMessage: "{name} · {type}", description: "Admin SFTP access drawer: an SSH key row, its name then its key type" }, { name: k.name, type: k.type })}</p>
                          <p className="font-mono text-xs break-all">{k.fingerprint}</p>
                          <p className="text-xs text-muted">
                            {intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.key_added_last_used", defaultMessage: "Added {added} · {lastUsed}", description: "Admin SFTP drawer: when a key was added and last used; {added} and {lastUsed} are relative times" }, { added: formatRelative(k.createdAt, intl), lastUsed: k.lastUsedAt ? formatRelative(k.lastUsedAt, intl) : intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.never_used", defaultMessage: "Never used", description: "Admin SFTP drawer: a key that has never logged in" }) })}
                          </p>
                        </div>
                        <Button size="sm" variant="danger" className="shrink-0" isDisabled={removeKey.isPending} onPress={() => removeKey.mutate(k.id)}>
                          <Trash2 className="h-4 w-4" />{intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.remove", defaultMessage: "Remove", description: "Admin SFTP drawer: button that removes a user's SSH key or revokes the password; keep it short" })}
                        </Button>
                      </div>
                    ))}
                  </section>

                  <section className="space-y-3">
                    <h3 className="text-sm font-semibold">{intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.password", defaultMessage: "Password", description: "Admin SFTP drawer: heading of the password section" })}</h3>
                    {data.password ? (
                      <div className="flex items-start justify-between gap-3">
                        <p className="text-sm">
                          {intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.password_set", defaultMessage: "Set · Created {created} · {lastUsed}", description: "Admin SFTP drawer: password status line; {created} and {lastUsed} are relative times" }, { created: formatRelative(data.password.createdAt, intl), lastUsed: data.password.lastUsedAt ? formatRelative(data.password.lastUsedAt, intl) : intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.never_used", defaultMessage: "Never used", description: "Admin SFTP drawer: a key that has never logged in" }) })}
                        </p>
                        <Button size="sm" variant="danger" className="shrink-0" isDisabled={revoke.isPending} onPress={() => revoke.mutate()}>
                          <Trash2 className="h-4 w-4" />{intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.revoke", defaultMessage: "Revoke", description: "Admin SFTP drawer: button that revokes the user's SFTP password; keep it short" })}
                        </Button>
                      </div>
                    ) : (
                      <p className="text-sm text-muted">{intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.password_not_set", defaultMessage: "Not set.", description: "Admin SFTP drawer: the user has no SFTP password" })}</p>
                    )}
                  </section>
                </>
              )}
              {actionError && (
                <p role="alert" className="text-sm text-danger">
                  {errorTextIntl(intl, actionError, intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.action_failed", defaultMessage: "The change could not be saved.", description: "Admin SFTP drawer: fallback error when a remove or revoke fails" }))}
                </p>
              )}
            </ModalBody>
            <ModalFooter className="flex items-center justify-end">
              <Button size="sm" onPress={onClose}>{intl.formatMessage({ id: "web.components.ui.admin.sftp_access_drawer.done", defaultMessage: "Done", description: "Admin SFTP drawer: button that closes the drawer; keep it short" })}</Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
