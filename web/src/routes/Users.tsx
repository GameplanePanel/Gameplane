import { useMemo, useState } from "react";
import { useInstallation } from "@/lib/useInstallation";
import {
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  Button,
  Card,
  Input,
  Tabs,
  Tab as TabComponent,
  Table,
  Dropdown,
  DropdownTrigger,
  DropdownMenu,
  DropdownPopover,
  DropdownSection,
  DropdownItem,
  Modal,
  ModalDialog,
  ModalHeader,
  ModalHeading,
  ModalBody,
  ModalFooter,
  AlertDialog,
  AlertDialogBackdrop,
  AlertDialogContainer,
  Alert,
  Select,
  ListBox,
  ListBoxItem,
} from "@heroui/react";
import {
  KeyRound,
  MoreHorizontal,
  Pencil,
  Plus,
  ScrollText,
  Search,
  Trash2,
} from "lucide-react";
import { PageHeader } from "@/components/ui/PageHeader";
import { RoleEditorModal } from "@/components/ui/RoleEditorModal";
import { InviteUserDialog } from "@/components/ui/admin/InviteUserDialog";
import { EditUserDialog } from "@/components/ui/admin/EditUserDialog";
import { ResetPasswordDialog } from "@/components/ui/admin/ResetPasswordDialog";
import { APIError } from "@/lib/api";
import { useMe, can } from "@/lib/auth";
import {
  Users as UsersAPI,
  Roles as RolesAPI,
  Clusters,
  type UserUpdate,
} from "@/lib/endpoints";
import { formatRelative } from "@/lib/utils";
import type { ExtendedUser, Role, RoleBinding } from "@/types";
import { englishIntl, useOptionalIntl } from "@/lib/i18n/format";
import type { IntlShape } from "react-intl";

type TabKey = "users" | "roles" | "service" | "idp";

const roleColor: Record<string, string> = {
  admin: "bg-warning/15 text-warning",
  operator: "bg-violet/15 text-violet",
  viewer: "",
};

function apiErrorText(error: unknown, intl: IntlShape = englishIntl): string | undefined {
  if (!error) return undefined;
  return error instanceof APIError
    ? error.body || intl.formatMessage({ id: "web.routes.users.request_failed_status", defaultMessage: "Request failed ({status})", description: "Users: fallback error text when the API sends no message; {status} is the HTTP status code" }, { status: error.status })
    : (error as Error).message;
}

function roleGrantsUserManagement(roles: Role[], name: string): boolean {
  const r = roles.find((x) => x.name === name);
  return !!r && (r.permissions.includes("*") || r.permissions.includes("users:manage"));
}

function useRolesQuery() {
  return useQuery({ queryKey: ["roles"], queryFn: () => RolesAPI.list() });
}

export function UsersPage() {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const { data: me } = useMe();
  const canAudit = can(me, "audit:read");
  const [tab, setTab] = useState<TabKey>("users");
  const [q, setQ] = useState("");
  const [inviting, setInviting] = useState(false);
  const [editing, setEditing] = useState<ExtendedUser | null>(null);
  const [resetting, setResetting] = useState<ExtendedUser | null>(null);
  const [deleting, setDeleting] = useState<ExtendedUser | null>(null);

  const { data: users = [], error } = useQuery({
    queryKey: ["users"],
    queryFn: () => UsersAPI.list(),
  });
  const { data: roles = [] } = useRolesQuery();

  const counts = useMemo(
    () => ({
      users: users.length,
      roles: roles.length,
    }),
    [users, roles],
  );

  const visible = users.filter((u) => {
    if (q) {
      const s = q.toLowerCase();
      return (
        u.username.toLowerCase().includes(s) ||
        (u.email ?? "").toLowerCase().includes(s) ||
        (u.displayName ?? "").toLowerCase().includes(s)
      );
    }
    return true;
  });

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["users"] });
  };

  const create = useMutation({
    mutationFn: UsersAPI.create,
    onSuccess: () => {
      invalidate();
      setInviting(false);
    },
  });
  const save = useMutation({
    mutationFn: ({ id, body }: { id: number; body: UserUpdate }) => UsersAPI.update(id, body),
    onSuccess: () => {
      invalidate();
      setEditing(null);
    },
  });
  const reset = useMutation({
    mutationFn: ({ id, password }: { id: number; password: string }) =>
      UsersAPI.resetPassword(id, password),
    onSuccess: () => setResetting(null),
  });

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title={intl.formatMessage({ id: "web.routes.users.users_rbac", defaultMessage: "Users & RBAC", description: "Users: page title, Users & RBAC, at the top of the users screen; keep it short" })}
        description={intl.formatMessage({ id: "web.routes.users.manage_access_to_the_gameplane_control", defaultMessage: "Manage access to the Gameplane control plane.", description: "Users: description under the title saying the page manages access to the Gameplane control plane" })}
        actions={
          <div className="flex items-center gap-2">
            {canAudit && (
              <Link
                to="/admin/audit"
                className="inline-flex h-9 items-center gap-1.5 rounded-[6px] border border-border px-3 text-sm font-medium text-foreground transition-colors hover:bg-surface"
              >
                <ScrollText className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.users.audit_log", defaultMessage: "Audit log", description: "Users: link button to the audit log screen, shown only to users allowed to audit; keep it short" })}</Link>
            )}
            <Button onPress={() => setInviting(true)} variant="primary">
              <Plus className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.users.invite_user", defaultMessage: "Invite user", description: "Users: button that opens the invite-user dialog; keep it short" })}</Button>
          </div>
        }
      />

      <Tabs
        selectedKey={tab}
        onSelectionChange={(key) => setTab(key as TabKey)}
        variant="secondary"
      >
        <div className="flex items-center gap-3">
          <Tabs.List className="users-rbac-tabs w-fit min-w-0">
            <TabComponent id="users">
              <div className="flex items-center gap-2">
                <span>{intl.formatMessage({ id: "web.routes.users.users", defaultMessage: "Users", description: "Users: name of the Users tab, followed by a count in brackets; keep it short" })}</span>
                <span className="text-xs text-foreground/60">{intl.formatMessage({ id: "web.routes.users.tab_count", defaultMessage: "({count})", description: "Users: item count shown after a tab name; {count} is the number of items" }, { count: counts.users })}</span>
              </div>
            </TabComponent>
            <TabComponent id="roles">
              <div className="flex items-center gap-2">
                <span>{intl.formatMessage({ id: "web.routes.users.roles", defaultMessage: "Roles", description: "Users: name of the Roles tab, followed by a count in brackets; keep it short" })}</span>
                <span className="text-xs text-foreground/60">{intl.formatMessage({ id: "web.routes.users.tab_count", defaultMessage: "({count})", description: "Users: item count shown after a tab name; {count} is the number of items" }, { count: counts.roles })}</span>
              </div>
            </TabComponent>
            <TabComponent id="service">{intl.formatMessage({ id: "web.routes.users.service_accounts", defaultMessage: "Service accounts", description: "Users: name of the Service accounts tab; keep it short" })}</TabComponent>
            <TabComponent id="idp">{intl.formatMessage({ id: "web.routes.users.identity_providers", defaultMessage: "Identity providers", description: "Users: name of the Identity providers tab; keep it short" })}</TabComponent>
          </Tabs.List>
          <div className="relative ml-auto w-64 shrink-0">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-foreground/60" />
            <Input
              className="pl-9"
              placeholder={tab === "users"
                ? intl.formatMessage({ id: "web.routes.users.search_users_placeholder", defaultMessage: "Search users…", description: "Users: placeholder of the search box on the users tab" })
                : intl.formatMessage({ id: "web.routes.users.search_placeholder", defaultMessage: "Search…", description: "Users: placeholder of the search box on the other tabs" })}
              value={q}
              onChange={(e) => setQ(e.target.value)}
              aria-label={intl.formatMessage({ id: "web.routes.users.search_users", defaultMessage: "Search users", description: "Users: accessible name of the search field that filters the list in the open tab" })}
            />
          </div>
        </div>

        <Tabs.Panel id="users">
            {error instanceof APIError && (
              <Alert status="danger">
                <Alert.Indicator />
                <Alert.Title>{intl.formatMessage({ id: "web.routes.users.failed_to_load_users", defaultMessage: "Failed to load users.", description: "Users: error title shown when the user list cannot be loaded" })}</Alert.Title>
                {error.body && <Alert.Description>{error.body}</Alert.Description>}
              </Alert>
            )}

            <Card className="overflow-hidden p-0">
            <Table.Root>
              <Table.ScrollContainer>
                <Table.Content aria-label={intl.formatMessage({ id: "web.routes.users.users_list", defaultMessage: "Users list", description: "Users: accessible name of the users table" })} keyboardNavigationBehavior="arrow">
                  <Table.Header>
                    <Table.Column id="user" isRowHeader>{intl.formatMessage({ id: "web.routes.users.user", defaultMessage: "User", description: "Users: column heading for the user name; keep it short" })}</Table.Column>
                    <Table.Column id="role">{intl.formatMessage({ id: "web.routes.users.role", defaultMessage: "Role", description: "Users: column heading for the role of each user; keep it short" })}</Table.Column>
                    <Table.Column id="provider">{intl.formatMessage({ id: "web.routes.users.provider", defaultMessage: "Provider", description: "Users: column heading for the sign-in provider of each user; keep it short" })}</Table.Column>
                    <Table.Column id="created">{intl.formatMessage({ id: "web.routes.users.created", defaultMessage: "Created", description: "Users: column heading for when each user account was created; keep it short" })}</Table.Column>
                    <Table.Column id="actions" className="text-right" aria-label={intl.formatMessage({ id: "web.routes.users.actions", defaultMessage: "Actions", description: "Users: accessible name of the actions column, which holds the menu for each row; keep it short" })} />
                  </Table.Header>
                  <Table.Body
                    renderEmptyState={() => <span>{intl.formatMessage({ id: "web.routes.users.no_entries", defaultMessage: "No entries.", description: "Users: empty-state text shown when the table has no rows" })}</span>}
                  >
                    {visible.map((u) => (
                  <Table.Row key={u.id}>
                    <Table.Cell>
                      <div className="flex items-center gap-3">
                        <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-primary/20 font-mono text-xs text-primary">
                          {(u.displayName || u.username).slice(0, 2).toUpperCase()}
                        </div>
                        <div className="min-w-0">
                          <div className="truncate font-mono text-sm text-foreground">
                            {u.username}
                            {me && me.id === u.id && (
                              <span className="ml-2 rounded bg-foreground/10 px-1.5 py-0.5 text-[10px] text-foreground/70">{intl.formatMessage({ id: "web.routes.users.you", defaultMessage: "you", description: "Users: small badge next to your own user name; keep it short" })}</span>
                            )}
                          </div>
                          <div className="truncate text-[11px] text-foreground/60">
                            {u.displayName || u.email || intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
                          </div>
                          {u.email && u.displayName && u.displayName !== u.email && (
                            <div className="truncate text-[11px] text-foreground/60">{u.email}</div>
                          )}
                        </div>
                      </div>
                    </Table.Cell>
                    <Table.Cell>
                      <span className={`rounded px-2 py-0.5 text-[10px] font-mono uppercase ${roleColor[u.role] ?? "bg-muted/20 text-muted"}`}>
                        {u.role}
                      </span>
                    </Table.Cell>
                    <Table.Cell>
                      <span className="rounded px-2 py-0.5 text-[10px] uppercase text-foreground/60 ring-1 ring-border">
                        {u.provider === "oidc"
                          ? intl.formatMessage({ id: "web.routes.users.provider_oidc", defaultMessage: "OIDC", description: "Users: badge for an account that signs in through OIDC" })
                          : u.provider === "pending"
                            ? intl.formatMessage({ id: "web.routes.users.provider_pending", defaultMessage: "Pending", description: "Users: badge for an invited account that has not signed in yet" })
                            : intl.formatMessage({ id: "web.routes.users.provider_local", defaultMessage: "Local", description: "Users: badge for an account that signs in with a local password" })}
                      </span>
                    </Table.Cell>
                    <Table.Cell className="text-foreground/60">{formatRelative(u.createdAt, intl)}</Table.Cell>
                    <Table.Cell>
                      <Dropdown>
                        <DropdownTrigger
                          className="inline-flex h-9 w-9 items-center justify-center rounded-md hover:bg-default-100 text-foreground/60 hover:text-foreground"
                          aria-label={intl.formatMessage({ id: "web.routes.users.actions_for_user", defaultMessage: "Actions for {name}", description: "Users: aria-label of the row actions menu; {name} is the username" }, { name: u.username })}
                        >
                          <>
                            <MoreHorizontal className="h-4 w-4" />
                          </>
                        </DropdownTrigger>
                        <DropdownPopover>
                          <DropdownMenu aria-label={intl.formatMessage({ id: "web.routes.users.actions_for_user", defaultMessage: "Actions for {name}", description: "Users: aria-label of the row actions menu; {name} is the username" }, { name: u.username })}>
                            <DropdownItem
                              key="edit"
                              onPress={() => setEditing(u)}
                            >
                              <div className="flex items-center gap-2">
                                <Pencil className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.users.edit_user", defaultMessage: "Edit user", description: "Users: menu item that opens the edit dialog for a user; keep it short" })}</div>
                            </DropdownItem>
                            <DropdownItem
                              key="reset"
                              isDisabled={u.provider === "oidc"}
                              onPress={() => setResetting(u)}
                              aria-label={u.provider === "oidc"
                                ? intl.formatMessage({ id: "web.routes.users.reset_password_oidc_label", defaultMessage: "Reset password: Account is OIDC-managed", description: "Users: aria-label of the disabled reset-password item on an OIDC account" })
                                : intl.formatMessage({ id: "web.routes.users.reset_password", defaultMessage: "Reset password", description: "Users: menu item that opens password reset for a user; disabled for OIDC-managed accounts; keep it short" })}
                            >
                              <div className="flex items-center gap-2" title={u.provider === "oidc" ? intl.formatMessage({ id: "web.routes.users.account_is_oidc_managed", defaultMessage: "Account is OIDC-managed", description: "Users: tooltip explaining that an OIDC account's password is managed elsewhere" }) : undefined}>
                                <KeyRound className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.users.reset_password", defaultMessage: "Reset password", description: "Users: menu item that opens password reset for a user; disabled for OIDC-managed accounts; keep it short" })}</div>
                            </DropdownItem>
                            <DropdownSection>
                              <DropdownItem
                                key="delete"
                                variant="danger"
                                isDisabled={Boolean(me && me.id === u.id)}
                                onPress={() => setDeleting(u)}
                              >
                                <div className="flex items-center gap-2">
                                  <Trash2 className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.users.delete_user", defaultMessage: "Delete user", description: "Users: menu item that opens the delete dialog for a user, and the dialog button that deletes the user; keep it short" })}</div>
                              </DropdownItem>
                            </DropdownSection>
                          </DropdownMenu>
                        </DropdownPopover>
                      </Dropdown>
                    </Table.Cell>
                  </Table.Row>
                    ))}
                  </Table.Body>
                </Table.Content>
              </Table.ScrollContainer>
            </Table.Root>
            </Card>
          </Tabs.Panel>

          <Tabs.Panel id="roles">
            <RolesTab />
          </Tabs.Panel>

          <Tabs.Panel id="service">
            <ServiceAccountsTab />
          </Tabs.Panel>

          <Tabs.Panel id="idp">
            <IdpTab />
          </Tabs.Panel>
      </Tabs>

      <InviteUserDialog
        open={inviting}
        onOpenChange={(open) => {
          if (!open) {
            setInviting(false);
            create.reset();
          }
        }}
        roles={roles.map((r) => r.name)}
        contactFieldsOptional
        disableSubmitUntilValid
        isLoading={create.isPending}
        apiError={apiErrorText(create.error, intl)}
        submitLabel={intl.formatMessage({ id: "web.routes.users.create_user", defaultMessage: "Create user", description: "Users: submit button label in the invite-user dialog" })}
        onInvite={(username, displayName, email, password, role) => {
          create.mutate({
            username,
            displayName: displayName || undefined,
            email: email || undefined,
            password: password || undefined,
            role: role ?? "viewer",
          });
        }}
      />
      {editing && (
        <EditUserDialog
          key={editing.id}
          open
          onOpenChange={(open) => {
            if (!open) {
              setEditing(null);
              save.reset();
            }
          }}
          username={editing.username}
          displayName={editing.displayName ?? ""}
          email={editing.email ?? ""}
          role={editing.role}
          roles={roles.map((r) => r.name)}
          isMe={!!me && me.id === editing.id}
          roleGrantsUserManagement={(r) => roleGrantsUserManagement(roles, r)}
          isLoading={save.isPending}
          apiError={apiErrorText(save.error, intl)}
          contactFieldsOptional
          extraContent={<NamespaceGrants userId={editing.id} roles={roles} />}
          onSave={(displayName, email, role) => {
            const dirty: UserUpdate = {};
            if (displayName !== (editing.displayName ?? "")) dirty.displayName = displayName;
            if (email !== (editing.email ?? "")) dirty.email = email;
            if (role !== editing.role) dirty.role = role;
            if (Object.keys(dirty).length === 0) return;
            save.mutate({ id: editing.id, body: dirty });
          }}
        />
      )}
      {resetting && (
        <ResetPasswordDialog
          open
          onOpenChange={(open) => {
            if (!open) {
              setResetting(null);
              reset.reset();
            }
          }}
          username={resetting.username}
          disableSubmitUntilValid
          isLoading={reset.isPending}
          apiError={apiErrorText(reset.error, intl)}
          submitLabel={intl.formatMessage({ id: "web.routes.users.set_new_password", defaultMessage: "Set new password", description: "Users: submit button label in the reset-password dialog" })}
          onReset={(password) => reset.mutate({ id: resetting.id, password })}
        />
      )}
      {deleting && (
        <DeleteUserDialog
          user={deleting}
          isMe={!!me && me.id === deleting.id}
          onClose={() => setDeleting(null)}
          onDeleted={() => {
            invalidate();
            setDeleting(null);
          }}
        />
      )}
    </div>
  );
}

function ErrorLine({ error }: { error: unknown }) {
  const intl = useOptionalIntl();
  if (!error) return null;
  const text =
    error instanceof APIError
      ? error.body || intl.formatMessage({ id: "web.routes.users.request_failed_status", defaultMessage: "Request failed ({status})", description: "Users: fallback error text when the API sends no message; {status} is the HTTP status code" }, { status: error.status })
      : (error as Error).message;
  return (
    <p role="alert" className="pt-2 text-xs text-danger">
      {text}
    </p>
  );
}

function DeleteUserDialog({
  user,
  isMe,
  onClose,
  onDeleted,
}: {
  user: ExtendedUser;
  isMe: boolean;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const intl = useOptionalIntl();
  const remove = useMutation({
    mutationFn: () => UsersAPI.remove(user.id),
    onSuccess: onDeleted,
  });

  return (
    <AlertDialog isOpen onOpenChange={(open) => !open && onClose()}>
      <AlertDialogBackdrop isDismissable={!remove.isPending} isKeyboardDismissDisabled={remove.isPending}>
        <AlertDialogContainer>
          <Modal>
            <ModalDialog role="alertdialog" className="max-w-md">
              <ModalHeader>
                <ModalHeading>{intl.formatMessage({ id: "web.routes.users.delete_username", defaultMessage: "Delete {username}?", description: "Users: sentence with username in ModalHeading" }, { username: user.username })}</ModalHeading>
              </ModalHeader>
              <ModalBody>
                {isMe ? (
                  <p className="text-sm text-danger">{intl.formatMessage({ id: "web.routes.users.you_can_t_delete_your_own_account", defaultMessage: "You can't delete your own account.", description: "Users: message in the delete dialog shown when you try to delete your own account" })}</p>
                ) : (
                  <div className="space-y-2">
                    <p className="text-sm">{intl.formatMessage({ id: "web.routes.users.their_sessions_will_be_revoked_and_they", defaultMessage: "Their sessions will be revoked and they'll lose access immediately. This cannot be undone.", description: "Users: text in the delete dialog: their sessions are revoked, they lose access immediately, and this cannot be undone" })}</p>
                    <ErrorLine error={remove.error} />
                  </div>
                )}
              </ModalBody>
              <ModalFooter className="flex items-center justify-end gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  onPress={onClose}
                  isDisabled={remove.isPending}
                >{intl.formatMessage({ id: "web.routes.users.cancel", defaultMessage: "Cancel", description: "Users: button that closes a dialog without making changes; keep it short" })}</Button>
                <Button
                  size="sm"
                  variant="danger"
                  isDisabled={isMe || remove.isPending}
                  onPress={() => remove.mutate()}
                >
                  {isMe
                    ? intl.formatMessage({ id: "web.routes.users.cannot_delete", defaultMessage: "Cannot delete", description: "Users: label of the delete button when the signed-in user is the account" })
                    : intl.formatMessage({ id: "web.routes.users.delete_user", defaultMessage: "Delete user", description: "Users: menu item that opens the delete dialog for a user, and the dialog button that deletes the user; keep it short" })}
                </Button>
              </ModalFooter>
            </ModalDialog>
          </Modal>
        </AlertDialogContainer>
      </AlertDialogBackdrop>
    </AlertDialog>
  );
}

function RolesTab() {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const { data: me } = useMe();
  const { data: roles = [] } = useRolesQuery();
  const { data: catalog } = useQuery({
    queryKey: ["permission-catalog"],
    queryFn: () => RolesAPI.catalog(),
  });
  const canManage = can(me, "roles:manage");
  const [editing, setEditing] = useState<Role | null>(null);
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<Role | null>(null);

  const refresh = () => void qc.invalidateQueries({ queryKey: ["roles"] });
  const groups = catalog?.groups ?? [];

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        {canManage && (
          <Button onPress={() => setCreating(true)} variant="primary">
            <Plus className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.users.new_role", defaultMessage: "New role", description: "Users: button that opens the dialog to create a new role; keep it short" })}</Button>
        )}
      </div>
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
        {roles.map((r) => (
          <Card key={r.name} className="flex flex-col gap-2 p-4">
            <div className="flex items-center justify-between gap-2">
              <span className="font-mono text-sm">{r.name}</span>
              {r.builtin && (
                <span className="rounded bg-foreground/10 px-1.5 py-0.5 text-[10px] uppercase text-foreground/70">{intl.formatMessage({ id: "web.routes.users.built_in", defaultMessage: "built-in", description: "Users: small badge marking a built-in role; keep it short" })}</span>
              )}
            </div>
            <p className="min-h-8 text-xs text-foreground/60">{r.description || intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</p>
            <div className="text-[11px] text-foreground/60">
              {r.permissions.includes("*")
                ? intl.formatMessage({ id: "web.routes.users.all_permissions", defaultMessage: "all permissions", description: "Users: role card summary when the role grants every permission" })
                : intl.formatMessage({ id: "web.routes.users.permission_count", defaultMessage: "{count, plural, one {{count} permission} other {{count} permissions}}", description: "Users: role card summary of the number of permissions; {count} is the number of permissions" }, { count: r.permissions.length })}
            </div>
            {canManage && (
              <div className="flex gap-2 pt-1">
                {r.name !== "admin" && (
                  <Button variant="ghost" className="h-7 px-2 text-xs" onPress={() => setEditing(r)}>
                    <Pencil className="h-3.5 w-3.5" /> {intl.formatMessage({ id: "web.routes.users.edit", defaultMessage: "Edit", description: "Users: button that opens the edit dialog for a role other than admin; keep it short" })}</Button>
                )}
                {!r.builtin && (
                  <Button
                    variant="ghost"
                    className="h-7 px-2 text-xs text-danger"
                    onPress={() => setDeleting(r)}
                  >
                    <Trash2 className="h-3.5 w-3.5" /> {intl.formatMessage({ id: "web.routes.users.delete", defaultMessage: "Delete", description: "Users: button that opens the delete dialog for a role; keep it short" })}</Button>
                )}
              </div>
            )}
          </Card>
        ))}
      </div>

      {(creating || editing) && (
        <RoleEditorModal
          open
          onOpenChange={(open) => {
            if (!open) {
              setCreating(false);
              setEditing(null);
            }
          }}
          role={editing}
          groups={groups}
          onSaved={() => {
            refresh();
            setCreating(false);
            setEditing(null);
          }}
        />
      )}
      {deleting && (
        <DeleteRoleDialog
          role={deleting}
          onClose={() => setDeleting(null)}
          onDeleted={() => {
            refresh();
            setDeleting(null);
          }}
        />
      )}
    </div>
  );
}

function DeleteRoleDialog({
  role,
  onClose,
  onDeleted,
}: {
  role: Role;
  onClose: () => void;
  onDeleted: () => void;
}) {
  const intl = useOptionalIntl();
  const remove = useMutation({
    mutationFn: () => RolesAPI.remove(role.name),
    onSuccess: onDeleted,
  });

  return (
    <AlertDialog isOpen onOpenChange={(open) => !open && onClose()}>
      <AlertDialogBackdrop isDismissable={!remove.isPending} isKeyboardDismissDisabled={remove.isPending}>
        <AlertDialogContainer>
          <Modal>
            <ModalDialog role="alertdialog" className="max-w-md">
              <ModalHeader>
                <ModalHeading>{intl.formatMessage({ id: "web.routes.users.delete_role_name", defaultMessage: "Delete role {name}?", description: "Users: sentence with name in ModalHeading" }, { name: role.name })}</ModalHeading>
              </ModalHeader>
              <ModalBody>
                <div className="space-y-2">
                  <p className="text-sm">{intl.formatMessage({ id: "web.routes.users.this_can_t_be_undone_roles_assigned_to", defaultMessage: "This can't be undone. Roles assigned to a user can't be deleted.", description: "Users: text in the delete role dialog: this cannot be undone, and roles still assigned to a user cannot be deleted" })}</p>
                  <ErrorLine error={remove.error} />
                </div>
              </ModalBody>
              <ModalFooter className="flex items-center justify-end gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  onPress={onClose}
                  isDisabled={remove.isPending}
                >{intl.formatMessage({ id: "web.routes.users.cancel", defaultMessage: "Cancel", description: "Users: button that closes a dialog without making changes; keep it short" })}</Button>
                <Button
                  size="sm"
                  variant="danger"
                  isDisabled={remove.isPending}
                  onPress={() => remove.mutate()}
                >{intl.formatMessage({ id: "web.routes.users.delete_role", defaultMessage: "Delete role", description: "Users: confirm button of the delete role dialog; keep it short" })}</Button>
              </ModalFooter>
            </ModalDialog>
          </Modal>
        </AlertDialogContainer>
      </AlertDialogBackdrop>
    </AlertDialog>
  );
}

function NamespaceGrants({ userId, roles }: { userId: number; roles: Role[] }) {
  const intl = useOptionalIntl();
  const installation = useInstallation();
  if (installation.isPending) return <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.users.loading_installation", defaultMessage: "Loading installation…", description: "Users: loading text in the namespace grants area while installation details are fetched" })}</p>;
  if (installation.isError) return <p className="text-xs text-danger">{intl.formatMessage({ id: "web.routes.users.couldn_t_load_installation_capabilities", defaultMessage: "Couldn't load installation capabilities.", description: "Users: error text in the namespace grants area when installation details fail to load" })}</p>;
  return <NamespaceGrantsForm userId={userId} roles={roles} standalone={installation.data.standalone} />;
}

function NamespaceGrantsForm({ userId, roles, standalone }: { userId: number; roles: Role[]; standalone: boolean }) {
  const intl = useOptionalIntl();
  const qc = useQueryClient();
  const [roleName, setRoleName] = useState(
    roles.find((r) => r.name === "operator")?.name ?? roles[0]?.name ?? "",
  );
  const [cluster, setCluster] = useState(standalone ? "" : "local");
  const [namespace, setNamespace] = useState("");

  const { data: bindings = [], error: bindingsError } = useQuery({
    queryKey: ["user-bindings", userId],
    queryFn: () => UsersAPI.bindings(userId),
  });
  const { data: clusters, error: clustersError } = useQuery({
    queryKey: ["clusters"],
    queryFn: () => Clusters.list(),
  });
  const { data: catalog, error: catalogError } = useQuery({
    queryKey: ["permission-catalog"],
    queryFn: () => RolesAPI.catalog(),
  });
  const namespacedPermissions = new Set(
    catalog?.groups.flatMap((group) => group.permissions.filter((p) => p.namespaced).map((p) => p.key)) ?? [],
  );
  const allowsRemoteWideGrant = (name: string) => {
    const role = roles.find((r) => r.name === name);
    return !!role && !!catalog &&
      role.permissions.every((p) => ["cluster:read", "modules:read", "modules:manage", "templates:read", "templates:write"].includes(p) || namespacedPermissions.has(p));
  };
  const allNamespacesAllowed = !!cluster && cluster !== "local" && cluster !== "*" && allowsRemoteWideGrant(roleName);
  const scopeError = namespace.trim() === "*" && !allNamespacesAllowed
    ? cluster === "local"
      ? intl.formatMessage({ id: "web.routes.users.local_primary_role_managed_above", defaultMessage: "The local primary role is managed above. Choose a namespace for an additional local grant.", description: "Users: grant error when all namespaces is entered for the local cluster" })
      : intl.formatMessage({ id: "web.routes.users.all_namespaces_requires_workload", defaultMessage: "All namespaces requires a role containing only workload cluster permissions. Central administration permissions are not allowed.", description: "Users: grant error when all namespaces is entered for a role with central administration permissions" })
    : undefined;
  const refresh = () => void qc.invalidateQueries({ queryKey: ["user-bindings", userId] });

  const add = useMutation({
    mutationFn: (b: RoleBinding) => UsersAPI.addBinding(userId, b),
    onSuccess: () => {
      setNamespace("");
      refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (b: RoleBinding) => UsersAPI.removeBinding(userId, b.roleName, b.namespace, b.cluster ?? "local"),
    onSuccess: refresh,
  });

  return (
    <div className="space-y-2">
      <div className="text-[11px] leading-[14px] font-semibold uppercase tracking-wider text-muted">{intl.formatMessage({ id: "web.routes.users.cluster_namespace_grants", defaultMessage: "Cluster & namespace grants", description: "Users: small heading of the cluster and namespace grants section for a user" })}</div>
      <p className="text-xs text-foreground/60">
        {standalone
          ? intl.formatMessage({ id: "web.routes.users.primary_role_manages_panel_access", defaultMessage: "The primary role above manages panel access.", description: "Users: note under the grants heading on standalone installs" })
          : intl.formatMessage({ id: "web.routes.users.primary_role_manages_local_access", defaultMessage: "The primary role above manages local access.", description: "Users: note under the grants heading on cluster installs" })} {intl.formatMessage({ id: "web.routes.users.additional_grants_apply_only_to_their", defaultMessage: "Additional grants apply only to their named cluster and namespace.", description: "Users: hint after the primary role note: additional grants apply only to their named cluster and namespace" })}</p>
      {bindings.length > 0 && (
        <ul className="space-y-1">
          {bindings.map((b) => {
            const bindingCluster = b.cluster ?? "local";
            const primary = bindingCluster === "local" && b.namespace === "*";
            const managed = primary || bindingCluster === "*" ||
              (b.namespace === "*" && !allowsRemoteWideGrant(b.roleName));
            return (
              <li key={`${bindingCluster}/${b.roleName}/${b.namespace}`} className="flex flex-wrap items-center gap-2 text-xs">
                <span className="font-mono">{b.roleName}</span>
                <span className="text-foreground/60">{intl.formatMessage({ id: "web.routes.users.in", defaultMessage: "in", description: "Users: small connecting word in a grant row, between the role and the namespace" })}</span>
                <span className="font-mono">{b.namespace === "*" ? intl.formatMessage({ id: "web.routes.users.all_namespaces", defaultMessage: "all namespaces", description: "Users: grant namespace shown when the grant covers every namespace" }) : b.namespace}</span>
                <span className="text-foreground/60">{intl.formatMessage({ id: "web.routes.users.on", defaultMessage: "on", description: "Users: small connecting word in a grant row, between the namespace and the cluster" })}</span>
                <span className="font-mono">{standalone && primary ? intl.formatMessage({ id: "web.routes.users.grant_panel", defaultMessage: "panel", description: "Users: grant cluster shown for a standalone panel grant" }) : bindingCluster}</span>
                {managed ? (
                  <span className="ml-auto text-foreground/60">{primary
                    ? intl.formatMessage({ id: "web.routes.users.primary_role_managed_above", defaultMessage: "Primary role (managed above)", description: "Users: grant row label for the primary role, which is edited above" })
                    : intl.formatMessage({ id: "web.routes.users.managed_outside_this_editor", defaultMessage: "Managed outside this editor", description: "Users: grant row label for a binding this editor cannot change" })}</span>
                ) : (
                  <button
                    type="button"
                    className="ml-auto rounded p-1 text-foreground/60 hover:text-danger disabled:opacity-50"
                    aria-label={intl.formatMessage({ id: "web.routes.users.remove_binding", defaultMessage: "Remove {role} in {namespace} on {cluster}", description: "Users: aria-label of the remove button on a grant; {role} is the role name, {namespace} the namespace, {cluster} the cluster" }, { role: b.roleName, namespace: b.namespace, cluster: bindingCluster })}
                    disabled={remove.isPending}
                    onClick={() => remove.mutate(b)}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                )}
              </li>
            );
          })}
        </ul>
      )}
      <div className="grid gap-2 sm:grid-cols-2">
        <Select
          aria-label={intl.formatMessage({ id: "web.routes.users.grant_cluster", defaultMessage: "Grant cluster", description: "Users: accessible name of the cluster dropdown for adding a grant" })}
          placeholder={intl.formatMessage({ id: "web.routes.users.select_a_workload_cluster", defaultMessage: "Select a workload cluster", description: "Users: grey placeholder of the cluster dropdown in the grants form, asking the user to pick a workload cluster" })}
          value={cluster}
          onChange={(v) => {
            setCluster(String(v));
            if (String(v) === "local" && namespace.trim() === "*") setNamespace("");
          }}
        >
          <Select.Trigger className="w-full items-center rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
            <Select.Value className="text-[var(--field-placeholder)]" />
            <Select.Indicator className="ml-auto h-4 w-4" />
          </Select.Trigger>
          <Select.Popover className="rounded border border-border">
            <ListBox className="p-0" aria-label={intl.formatMessage({ id: "web.routes.users.grant_cluster", defaultMessage: "Grant cluster", description: "Users: accessible name of the cluster dropdown for adding a grant" })}>
              {!standalone && <ListBoxItem id="local">{intl.formatMessage({ id: "web.routes.users.local_cluster", defaultMessage: "Local cluster", description: "Users: option in the grant cluster dropdown for the local cluster; keep it short" })}</ListBoxItem>}
              {(clusters?.items ?? []).filter((c) => c.name !== "local").map((c) => (
                <ListBoxItem key={c.name} id={c.name}>
                  {c.displayName ? intl.formatMessage({ id: "web.routes.users.cluster_option", defaultMessage: "{displayName} ({name})", description: "Users: cluster picker option; {displayName} is the cluster's display name, {name} its id" }, { displayName: c.displayName, name: c.name }) : c.name}
                </ListBoxItem>
              ))}
            </ListBox>
          </Select.Popover>
        </Select>
        <div data-grant-role>
          <Select
            aria-label={intl.formatMessage({ id: "web.routes.users.grant_role", defaultMessage: "Grant role", description: "Users: accessible name of the role dropdown for a new grant" })}
            value={roleName}
            onChange={(v) => setRoleName(String(v))}
          >
            <Select.Trigger className="w-full items-center rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80">
              <Select.Value className="text-[var(--field-placeholder)]" />
              <Select.Indicator className="ml-auto h-4 w-4" />
            </Select.Trigger>
            <Select.Popover className="rounded border border-border">
              <ListBox className="p-0" aria-label={intl.formatMessage({ id: "web.routes.users.grant_role", defaultMessage: "Grant role", description: "Users: accessible name of the role dropdown for a new grant" })}>
                {roles.map((r) => (
                  <ListBoxItem key={r.name} id={r.name}>{r.name}</ListBoxItem>
                ))}
              </ListBox>
            </Select.Popover>
          </Select>
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          className="min-w-0 flex-1"
          style={{ height: 32 }}
          value={namespace}
          onChange={(e) => setNamespace(e.target.value)}
          placeholder={intl.formatMessage({ id: "web.routes.users.namespace", defaultMessage: "namespace", description: "Users: grey placeholder of the namespace field in the grants form, showing the expected entry" })}
          aria-label={intl.formatMessage({ id: "web.routes.users.grant_namespace", defaultMessage: "Grant namespace", description: "Users: accessible name of the namespace field for a new grant" })}
        />
        <Button
          variant="ghost"
          size="sm"
          isDisabled={!allNamespacesAllowed}
          onPress={() => setNamespace("*")}
        >{intl.formatMessage({ id: "web.routes.users.all_namespaces_button", defaultMessage: "All namespaces", description: "Users: button that fills the namespace field with all namespaces; keep it short" })}</Button>
        <Button
          variant="ghost"
          size="sm"
          className="text-[13px]"
          isDisabled={!cluster || (standalone && !clusters?.items.some((item) => item.name === cluster)) || !roleName || !namespace.trim() || !!scopeError || !!clustersError || add.isPending}
          onPress={() => add.mutate({ roleName, namespace: namespace.trim(), cluster })}
        >{intl.formatMessage({ id: "web.routes.users.add", defaultMessage: "Add", description: "Users: button that adds the grant to the user; keep it short" })}</Button>
      </div>
      <p className="text-xs text-foreground/60">{intl.formatMessage({ id: "web.routes.users.for_remote_node_inventory_create_a_role", defaultMessage: "For remote node inventory, create a role in the Roles tab with “View nodes, version, and storage”, then grant it to all namespaces on that cluster.", description: "Users: hint under the grant form on remote node inventory: create a role with View nodes, version, and storage, then grant it to all namespaces on that cluster" })}</p>
      {scopeError && <p role="alert" className="text-xs text-danger">{scopeError}</p>}
      <ErrorLine error={bindingsError || clustersError || catalogError || add.error || remove.error} />
    </div>
  );
}

function IdpTab() {
  const intl = useOptionalIntl();
  return (
    <Card className="p-6 text-sm text-foreground/60">{intl.formatMessage({ id: "web.routes.users.oidc_identity_providers_configured_in", defaultMessage: "OIDC identity providers configured in Helm values appear here. UI configuration is tracked for v1.1.", description: "Users: note on the identity providers tab: OIDC providers from Helm values appear here and UI setup is planned for v1.1 (OIDC: technical term, usually left untranslated)" })}</Card>
  );
}

function ServiceAccountsTab() {
  const intl = useOptionalIntl();
  return (
    <Card className="p-6 text-sm text-foreground/60">{intl.formatMessage({ id: "web.routes.users.service_accounts_machine_to_machine_api", defaultMessage: "Service accounts (machine-to-machine API tokens) are tracked for v1.1.", description: "Users: note on the service accounts tab: machine-to-machine API tokens are planned for v1.1" })}</Card>
  );
}
