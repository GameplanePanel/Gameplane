// Personal SFTP access page at /settings/sftp (spec 040 US3; design
// Screen/Settings — SFTP 1006926387-249, (Light) -789, (Not enabled) -1274,
// (No password) -1734, (Non-admin) 1111719913-1384). Every signed-in user
// manages their own SSH keys and SFTP password here. The password is shown
// once in PasswordShownOnceDialog and never cached.

import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Card } from "@heroui/react";
import { Copy, KeyRound, Plus, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/ui/PageHeader";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { SettingsNav } from "@/components/ui/SettingsNav";
import { AddKeyDialog, GeneratePasswordDialog, PasswordShownOnceDialog } from "@/components/sftp/SftpDialogs";
import { can, useMe } from "@/lib/auth";
import { errorTextIntl } from "@/lib/errors";
import { formatRelative } from "@/lib/utils";
import { useOptionalIntl } from "@/lib/i18n/format";
import { getSftpSelf, deleteSftpKey, revokeSftpPassword, type SftpKey, type SftpPasswordInfo, type SftpSelf } from "@/lib/api";

const SFTP_QUERY_KEY = ["users", "me", "sftp"] as const;

function copyText(text: string) {
  try {
    void navigator.clipboard.writeText(text).catch(() => undefined);
  } catch {
    // Clipboard unavailable (insecure context): the text stays selectable on screen.
  }
}

export function ProfileSftpPage() {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const { data: me } = useMe();
  const showAdmin = can(me, "config:manage");

  const { data, error, isPending } = useQuery<SftpSelf>({
    queryKey: SFTP_QUERY_KEY,
    queryFn: getSftpSelf,
  });

  const [addingKey, setAddingKey] = useState(false);
  const [removingKey, setRemovingKey] = useState<SftpKey | null>(null);
  const [generating, setGenerating] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const [shown, setShown] = useState<{ password: string; length: number } | null>(null);

  const refresh = () => void qc.invalidateQueries({ queryKey: SFTP_QUERY_KEY });

  const removeKey = useMutation({
    mutationFn: (id: number) => deleteSftpKey(id),
    onSuccess: () => {
      setRemovingKey(null);
      refresh();
    },
  });
  const revoke = useMutation({
    mutationFn: () => revokeSftpPassword(),
    onSuccess: () => {
      setRevoking(false);
      refresh();
    },
  });

  const title = intl.formatMessage({ id: "web.routes.profile_sftp.sftp_access", defaultMessage: "SFTP access", description: "SFTP page: page title, at the top of the screen" });

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title={title}
        description={intl.formatMessage({ id: "web.routes.profile_sftp.description", defaultMessage: "Your personal SFTP credentials for game server files. Admins can see and revoke them.", description: "SFTP page: description under the page title" })}
      />

      <div className="grid gap-6 lg:grid-cols-[220px_1fr]">
        <SettingsNav active="sftp" showAdminSections={showAdmin} onSelect={() => undefined} />

        <div className="space-y-6">
          {error ? (
            <Card role="alert" className="p-6 text-sm text-danger">
              {errorTextIntl(intl, error, intl.formatMessage({ id: "web.routes.profile_sftp.load_failed", defaultMessage: "Could not load your SFTP access.", description: "SFTP page: error shown when the credentials cannot be loaded" }))}
            </Card>
          ) : isPending || !data ? (
            <Card className="p-6 text-sm text-muted">
              {intl.formatMessage({ id: "web.routes.profile_sftp.loading", defaultMessage: "Loading SFTP access…", description: "SFTP page: loading text shown while the credentials are fetched" })}
            </Card>
          ) : (
            <>
              <AddressCard data={data} />
              <KeysCard
                keys={data.keys}
                onAdd={() => setAddingKey(true)}
                onRemove={setRemovingKey}
              />
              <PasswordCard
                password={data.password}
                onGenerate={() => setGenerating(true)}
                onRevoke={() => setRevoking(true)}
              />
            </>
          )}
        </div>
      </div>

      <AddKeyDialog
        open={addingKey}
        onClose={() => setAddingKey(false)}
        onAdded={() => {
          setAddingKey(false);
          refresh();
        }}
      />

      <ConfirmDialog
        open={removingKey !== null}
        onOpenChange={(o) => !o && setRemovingKey(null)}
        title={intl.formatMessage({ id: "web.routes.profile_sftp.remove_key_question", defaultMessage: "Remove SSH key?", description: "SFTP page: title of the confirm dialog that removes a key" })}
        description={intl.formatMessage({ id: "web.routes.profile_sftp.remove_key_description", defaultMessage: "Open SFTP sessions that use this key end within one minute. You can add it again later.", description: "SFTP page: explains what removing an SSH key does" })}
        confirmLabel={intl.formatMessage({ id: "web.routes.profile_sftp.remove", defaultMessage: "Remove", description: "SFTP page: button that removes an SSH key or revokes the password; keep it short" })}
        destructive
        busy={removeKey.isPending}
        onConfirm={() => removingKey && removeKey.mutate(removingKey.id)}
      />

      <GeneratePasswordDialog
        open={generating}
        replacing={data?.password != null}
        onClose={() => setGenerating(false)}
        onGenerated={(password, length) => {
          setGenerating(false);
          setShown({ password, length });
          refresh();
        }}
      />

      <PasswordShownOnceDialog
        password={shown?.password ?? null}
        length={shown?.length ?? 0}
        onDone={() => setShown(null)}
      />

      <ConfirmDialog
        open={revoking}
        onOpenChange={(o) => !o && setRevoking(false)}
        title={intl.formatMessage({ id: "web.routes.profile_sftp.revoke_password_question", defaultMessage: "Revoke SFTP password?", description: "SFTP page: title of the confirm dialog that revokes the password" })}
        description={intl.formatMessage({ id: "web.routes.profile_sftp.revoke_password_description", defaultMessage: "The password stops working at once and cannot be used again. Open SFTP sessions that use it end within one minute.", description: "SFTP page: explains what revoking the password does" })}
        confirmLabel={intl.formatMessage({ id: "web.routes.profile_sftp.revoke", defaultMessage: "Revoke", description: "SFTP page: button that revokes the SFTP password; keep it short" })}
        destructive
        busy={revoke.isPending}
        onConfirm={() => revoke.mutate()}
      />
    </div>
  );
}

function AddressCard({ data }: { data: SftpSelf }) {
  const intl = useOptionalIntl();
  const ep = data.endpoint;
  const label = "text-sm text-muted";
  const value = "font-mono text-sm break-all";
  return (
    <Card className="space-y-4 p-6">
      <Card.Header className="space-y-1">
        <h2 className="text-base font-semibold">{intl.formatMessage({ id: "web.routes.profile_sftp.sftp_address", defaultMessage: "SFTP address", description: "SFTP page: heading of the address card" })}</h2>
        <p className="text-sm text-muted">{ep
          ? intl.formatMessage({ id: "web.routes.profile_sftp.use_these_settings", defaultMessage: "Use these settings in any SFTP client. Check the host key matches the fingerprint your client shows on first connect.", description: "SFTP page: instruction under the address heading" })
          : intl.formatMessage({ id: "web.routes.profile_sftp.not_enabled", defaultMessage: "SFTP is not enabled on this cluster. An administrator can turn it on with the sftp.enabled Helm value. Your SSH keys and password are still managed here.", description: "SFTP page: shown when the cluster has no SFTP endpoint; sftp.enabled is a Helm value name, keep it as is" })}</p>
      </Card.Header>
      {ep && (
        <dl className="grid gap-3 text-sm">
          <div className="flex items-center justify-between gap-4">
            <dt className={label}>{intl.formatMessage({ id: "web.routes.profile_sftp.host", defaultMessage: "Host", description: "SFTP page: label of the host name row" })}</dt>
            <dd className="flex items-center gap-3"><span className={value}>{ep.address}</span>
              <Button variant="ghost" size="sm" aria-label={intl.formatMessage({ id: "web.routes.profile_sftp.copy_host", defaultMessage: "Copy host", description: "SFTP page: button that copies the host name" })} onPress={() => copyText(ep.address)}><Copy className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.profile_sftp.copy", defaultMessage: "Copy", description: "SFTP page: button that copies a value; keep it short" })}</Button>
            </dd>
          </div>
          <div className="flex items-center justify-between gap-4">
            <dt className={label}>{intl.formatMessage({ id: "web.routes.profile_sftp.port", defaultMessage: "Port", description: "SFTP page: label of the port row" })}</dt>
            <dd className={value}>{ep.port}</dd>
          </div>
          <div className="flex items-center justify-between gap-4">
            <dt className={label}>{intl.formatMessage({ id: "web.routes.profile_sftp.host_key_sha256", defaultMessage: "Host key (SHA256)", description: "SFTP page: label of the host key fingerprint row; SHA256 is a technical term" })}</dt>
            <dd className="flex items-center gap-3"><span className={value}>{ep.hostKeyFingerprint}</span>
              <Button variant="ghost" size="sm" aria-label={intl.formatMessage({ id: "web.routes.profile_sftp.copy_host_key", defaultMessage: "Copy host key", description: "SFTP page: button that copies the host key fingerprint" })} onPress={() => copyText(ep.hostKeyFingerprint)}><Copy className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.profile_sftp.copy", defaultMessage: "Copy", description: "SFTP page: button that copies a value; keep it short" })}</Button>
            </dd>
          </div>
        </dl>
      )}
      <div className="space-y-2 border-t border-default-200 pt-4">
        <h3 className="text-sm font-semibold">{intl.formatMessage({ id: "web.routes.profile_sftp.username", defaultMessage: "Username", description: "SFTP page: heading of the username forms" })}</h3>
        <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.profile_sftp.log_in_with_one_of", defaultMessage: "Log in with one of these usernames and your SSH key or SFTP password.", description: "SFTP page: instruction above the username forms" })}</p>
        <ul className="space-y-2 text-sm">
          <li><code className="font-mono">{data.username}</code> <span className="text-muted">{intl.formatMessage({ id: "web.routes.profile_sftp.username_all_servers", defaultMessage: "Lists every server you can read, one folder each", description: "SFTP page: explains the plain username form" })}</span></li>
          <li><code className="font-mono">{intl.formatMessage({ id: "web.routes.profile_sftp.username_server_form", defaultMessage: "{username}+'<'server'>'", description: "SFTP page: the user+server username form; <server> is a placeholder the user replaces with a server name" }, { username: data.username })}</code> <span className="text-muted">{intl.formatMessage({ id: "web.routes.profile_sftp.username_one_server", defaultMessage: "Opens straight in the files of that server", description: "SFTP page: explains the user+server username form" })}</span></li>
          <li><code className="font-mono">{intl.formatMessage({ id: "web.routes.profile_sftp.username_namespace_form", defaultMessage: "{username}+mods/'<'server'>'", description: "SFTP page: the user+namespace/server username form; mods is an example namespace, <server> a placeholder" }, { username: data.username })}</code> <span className="text-muted">{intl.formatMessage({ id: "web.routes.profile_sftp.username_namespace", defaultMessage: "Opens a server in another namespace (mods)", description: "SFTP page: explains the user+namespace/server username form; mods is an example namespace name" })}</span></li>
        </ul>
      </div>
    </Card>
  );
}

function KeysCard({ keys, onAdd, onRemove }: { keys: SftpKey[]; onAdd: () => void; onRemove: (key: SftpKey) => void }) {
  const intl = useOptionalIntl();
  return (
    <Card className="space-y-4 p-6">
      <Card.Header className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <h2 className="text-base font-semibold">{intl.formatMessage({ id: "web.routes.profile_sftp.ssh_keys", defaultMessage: "SSH keys", description: "SFTP page: heading of the SSH keys card" })}</h2>
          <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.profile_sftp.public_keys_that_can_log_in", defaultMessage: "Public keys that can log in over SFTP. Gameplane stores only the public part.", description: "SFTP page: explains that only public keys are stored" })}</p>
        </div>
        <Button size="sm" onPress={onAdd}><Plus className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.profile_sftp.add_key", defaultMessage: "Add key", description: "SFTP page: button that opens the add SSH key dialog; keep it short" })}</Button>
      </Card.Header>
      {keys.length === 0 ? (
        <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.profile_sftp.no_keys", defaultMessage: "No SSH keys yet.", description: "SFTP page: shown when the user has no SSH keys" })}</p>
      ) : (
        <ul className="divide-y divide-default-200">
          {keys.map((k) => (
            <li key={k.id} className="flex items-start justify-between gap-4 py-3">
              <div className="min-w-0 space-y-1">
                <p className="font-medium">{k.name}</p>
                <p className="break-all font-mono text-xs">{intl.formatMessage({ id: "web.routes.profile_sftp.key_type_fingerprint", defaultMessage: "{type} · {fingerprint}", description: "SFTP page: an SSH key row, its key type then its fingerprint" }, { type: keyType(k.publicKey), fingerprint: k.fingerprint })}</p>
                <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.profile_sftp.key_added_last_used", defaultMessage: "Added {added} · {lastUsed}", description: "SFTP page: when a key was added and when it was last used; {added} and {lastUsed} are relative times" }, { added: formatRelative(k.createdAt, intl), lastUsed: k.lastUsedAt ? formatRelative(k.lastUsedAt, intl) : intl.formatMessage({ id: "web.routes.profile_sftp.never_used", defaultMessage: "Never used", description: "SFTP page: a key or password that has never logged in" }) })}</p>
              </div>
              <Button size="sm" variant="danger" className="shrink-0" onPress={() => onRemove(k)}>
                <Trash2 className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.profile_sftp.remove", defaultMessage: "Remove", description: "SFTP page: button that removes an SSH key or revokes the password; keep it short" })}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function PasswordCard({ password, onGenerate, onRevoke }: { password: SftpPasswordInfo | null; onGenerate: () => void; onRevoke: () => void }) {
  const intl = useOptionalIntl();
  return (
    <Card className="space-y-4 p-6">
      <Card.Header className="space-y-1">
        <h2 className="text-base font-semibold">{intl.formatMessage({ id: "web.routes.profile_sftp.sftp_password", defaultMessage: "SFTP password", description: "SFTP page: heading of the password card" })}</h2>
        {password ? (
          <>
            <p className="text-sm">{intl.formatMessage({ id: "web.routes.profile_sftp.password_set", defaultMessage: "Set · Created {created} · {lastUsed}", description: "SFTP page: password status line; {created} and {lastUsed} are relative times" }, { created: formatRelative(password.createdAt, intl), lastUsed: password.lastUsedAt ? formatRelative(password.lastUsedAt, intl) : intl.formatMessage({ id: "web.routes.profile_sftp.never_used", defaultMessage: "Never used", description: "SFTP page: a key or password that has never logged in" }) })}</p>
            <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.profile_sftp.password_help", defaultMessage: "Shown once when generated and stored only as a hash. It never expires. Regenerating or revoking it stops the old password at once.", description: "SFTP page: explains the password is shown once, stored as a hash, never expires, and that regenerating or revoking stops the old one" })}</p>
            <div className="flex flex-wrap gap-3">
              <Button variant="ghost" size="sm" onPress={onGenerate}><KeyRound className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.profile_sftp.regenerate", defaultMessage: "Regenerate", description: "SFTP page: button that replaces the password with a new one; keep it short" })}</Button>
              <Button variant="danger" size="sm" onPress={onRevoke}><Trash2 className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.profile_sftp.revoke", defaultMessage: "Revoke", description: "SFTP page: button that revokes the SFTP password; keep it short" })}</Button>
            </div>
          </>
        ) : (
          <>
            <p className="text-sm">{intl.formatMessage({ id: "web.routes.profile_sftp.password_not_set", defaultMessage: "Not set · You have no SFTP password yet", description: "SFTP page: password status when none is set" })}</p>
            <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.profile_sftp.password_not_set_help", defaultMessage: "Generate one to log in with a password, or add an SSH key above. A password is shown once and never expires.", description: "SFTP page: explains how to get a password when none is set" })}</p>
            <div>
              <Button size="sm" onPress={onGenerate}><Plus className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.profile_sftp.generate_password", defaultMessage: "Generate password", description: "SFTP page: button that opens the generate password dialog; keep it short" })}</Button>
            </div>
          </>
        )}
      </Card.Header>
    </Card>
  );
}

// keyType is the algorithm word of an OpenSSH public key line (the first token).
function keyType(publicKey: string): string {
  return publicKey.trim().split(/\s+/)[0] ?? "";
}
