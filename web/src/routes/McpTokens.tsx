// Personal MCP tokens settings page (spec 037, contracts/ui-mcp-tokens.md;
// design Screen/MCP Tokens in assets/design-export: list 2044649516-13, empty
// -579, create -1099, one-time reveal -1687, revoke -2282, mobile -2862).
// Every signed-in user manages their own tokens here. The secret is returned
// once by the create call; it lives only in the reveal dialog and is dropped
// when that dialog closes.

import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Button,
  Card,
  Input,
  Label,
  ListBox,
  ListBoxItem,
  Modal,
  ModalBackdrop,
  ModalBody,
  ModalContainer,
  ModalDialog,
  ModalFooter,
  ModalHeader,
  ModalHeading,
  Select,
  Table,
} from "@heroui/react";
import { AlertTriangle, ExternalLink, Plus } from "lucide-react";
import { defineMessages } from "react-intl";
import { PageHeader } from "@/components/ui/PageHeader";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { SettingsNav } from "@/components/ui/SettingsNav";
import {
  createMcpToken,
  listMcpTokens,
  revokeMcpToken,
  type CreatedMcpToken,
  type McpToken,
  type McpTokenLifetimeDays,
} from "@/lib/api";
import { can, useMe } from "@/lib/auth";
import { errorTextIntl } from "@/lib/errors";
import { useFormat, useOptionalIntl } from "@/lib/i18n/format";
import { cn, formatRelative } from "@/lib/utils";

const LIFETIME_OPTIONS: McpTokenLifetimeDays[] = [7, 30, 90, 365];
const DEFAULT_LIFETIME: McpTokenLifetimeDays = 30;
const DATE_OPTIONS: Intl.DateTimeFormatOptions = { year: "numeric", month: "short", day: "numeric" };
const CLIENT_README_URL = "https://github.com/GameplanePanel/mcp#readme";

const messages = defineMessages({
  title: { id: "web.routes.mcp_tokens.mcp_tokens", defaultMessage: "MCP tokens", description: "MCP tokens: page title at the top of the screen; keep it short" },
  description: { id: "web.routes.mcp_tokens.description", defaultMessage: "Personal tokens let an AI assistant read Gameplane through the MCP server, with your permissions. A token can only read.", description: "MCP tokens: description under the page title" },
  createToken: { id: "web.routes.mcp_tokens.create_token", defaultMessage: "Create token", description: "MCP tokens: button that opens the create-token dialog; keep it short" },
  colName: { id: "web.routes.mcp_tokens.name", defaultMessage: "Name", description: "MCP tokens: column heading for the token name; keep it short" },
  colToken: { id: "web.routes.mcp_tokens.token", defaultMessage: "Token", description: "MCP tokens: column heading for the token prefix; keep it short" },
  colCreated: { id: "web.routes.mcp_tokens.created", defaultMessage: "Created", description: "MCP tokens: column heading for when the token was created; keep it short" },
  colExpires: { id: "web.routes.mcp_tokens.expires", defaultMessage: "Expires", description: "MCP tokens: column heading for when the token expires; keep it short" },
  colLastUsed: { id: "web.routes.mcp_tokens.last_used", defaultMessage: "Last used", description: "MCP tokens: column heading for when the token was last used; keep it short" },
  colActions: { id: "web.routes.mcp_tokens.actions", defaultMessage: "Actions", description: "MCP tokens: accessible name of the actions column; keep it short" },
  tableLabel: { id: "web.routes.mcp_tokens.tokens_list", defaultMessage: "MCP tokens list", description: "MCP tokens: accessible name of the tokens table" },
  never: { id: "web.routes.mcp_tokens.never", defaultMessage: "Never", description: "MCP tokens: shown in Last used when the token has never been used; keep it short" },
  expiredBadge: { id: "web.routes.mcp_tokens.expired", defaultMessage: "Expired", description: "MCP tokens: badge on a token past its expiry date; keep it short" },
  revokedBadge: { id: "web.routes.mcp_tokens.revoked", defaultMessage: "Revoked", description: "MCP tokens: badge on a revoked token; keep it short" },
  revoke: { id: "web.routes.mcp_tokens.revoke", defaultMessage: "Revoke", description: "MCP tokens: button that asks to revoke a token; keep it short" },
  emptyTitle: { id: "web.routes.mcp_tokens.no_mcp_tokens_yet", defaultMessage: "No MCP tokens yet", description: "MCP tokens: heading of the empty state shown when the user has no tokens" },
  emptyBody: { id: "web.routes.mcp_tokens.empty_body", defaultMessage: "A token lets an MCP client, such as an AI assistant, read Gameplane with your permissions. Create one to connect a client.", description: "MCP tokens: empty-state text explaining what a token is for" },
  connect: { id: "web.routes.mcp_tokens.how_to_connect", defaultMessage: "How to connect an MCP client", description: "MCP tokens: link to the MCP server README that explains how to connect a client" },
  loadFailed: { id: "web.routes.mcp_tokens.load_failed", defaultMessage: "Failed to load MCP tokens.", description: "MCP tokens: error shown when the token list cannot be loaded" },
  actionFailed: { id: "web.routes.mcp_tokens.request_failed", defaultMessage: "The request failed. Try again.", description: "MCP tokens: fallback error text when a create or revoke request fails without a message" },
  createTitle: { id: "web.routes.mcp_tokens.create_mcp_token", defaultMessage: "Create MCP token", description: "MCP tokens: title of the create-token dialog" },
  nameLabel: { id: "web.routes.mcp_tokens.name_field", defaultMessage: "Name", description: "MCP tokens: label above the token name field in the create dialog" },
  namePlaceholder: { id: "web.routes.mcp_tokens.name_placeholder", defaultMessage: "e.g. claude-desktop", description: "MCP tokens: placeholder of the token name field" },
  expiresIn: { id: "web.routes.mcp_tokens.expires_in", defaultMessage: "Expires in", description: "MCP tokens: label above the lifetime select in the create dialog" },
  createHint: { id: "web.routes.mcp_tokens.create_hint", defaultMessage: "The token can read what you can read now. Revoke it any time.", description: "MCP tokens: note in the create dialog about what the token can access" },
  cancel: { id: "web.routes.mcp_tokens.cancel", defaultMessage: "Cancel", description: "MCP tokens: button that closes a dialog without changes; keep it short" },
  create: { id: "web.routes.mcp_tokens.create", defaultMessage: "Create", description: "MCP tokens: submit button of the create-token dialog; keep it short" },
  createdTitle: { id: "web.routes.mcp_tokens.token_created", defaultMessage: "Token created", description: "MCP tokens: title of the dialog that shows the new token once" },
  copyWarning: { id: "web.routes.mcp_tokens.copy_warning", defaultMessage: "Copy this token now. It will not be shown again.", description: "MCP tokens: warning that the full token is shown only once" },
  tokenField: { id: "web.routes.mcp_tokens.token_field", defaultMessage: "Token", description: "MCP tokens: label above the new token in the reveal dialog" },
  copy: { id: "web.routes.mcp_tokens.copy", defaultMessage: "Copy", description: "MCP tokens: button that copies the token or the client configuration; keep it short" },
  configLabel: { id: "web.routes.mcp_tokens.client_configuration", defaultMessage: "MCP client configuration", description: "MCP tokens: label above the MCP client configuration snippet" },
  done: { id: "web.routes.mcp_tokens.done", defaultMessage: "Done", description: "MCP tokens: button that closes the token reveal dialog; keep it short" },
  revokeBody: { id: "web.routes.mcp_tokens.revoke_body", defaultMessage: "Any MCP client using it stops working at once.", description: "MCP tokens: body of the revoke confirmation" },
  revokeConfirm: { id: "web.routes.mcp_tokens.revoke_confirm", defaultMessage: "Revoke", description: "MCP tokens: confirm button of the revoke dialog; keep it short" },
});

function tokenState(t: McpToken, now: number): "active" | "expired" | "revoked" {
  if (t.revokedAt) return "revoked";
  if (Date.parse(t.expiresAt) < now) return "expired";
  return "active";
}

// middleEllipsis shortens a token for display; the full value is only copied.
function middleEllipsis(s: string, head = 16, tail = 8): string {
  return s.length > head + tail + 1 ? `${s.slice(0, head)}…${s.slice(-tail)}` : s;
}

function clientConfig(token: string, panelUrl: string): string {
  return JSON.stringify(
    {
      mcpServers: {
        gameplane: {
          command: "docker",
          args: [
            "run",
            "--rm",
            "-i",
            "-e",
            "GAMEPLANE_PANEL_URL",
            "-e",
            "GAMEPLANE_MCP_TOKEN",
            "ghcr.io/gameplanepanel/gameplane/mcp-server:edge",
            "serve",
          ],
          env: {
            GAMEPLANE_PANEL_URL: panelUrl,
            GAMEPLANE_MCP_TOKEN: token,
          },
        },
      },
    },
    null,
    2,
  );
}

async function copyText(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    // Clipboard blocked (insecure context or permission denied): the text stays selectable on screen.
  }
}

function ErrorLine({ error }: { error: unknown }) {
  const intl = useOptionalIntl();
  if (!error) return null;
  return (
    <p role="alert" className="pt-2 text-xs text-danger">
      {errorTextIntl(intl, error, intl.formatMessage(messages.actionFailed))}
    </p>
  );
}

function StateBadge({ label }: { label: string }) {
  return (
    <span className="inline-flex rounded-full border border-border px-2.5 py-0.5 text-xs text-muted">{label}</span>
  );
}

export function McpTokensPage() {
  const intl = useOptionalIntl();
  const fmt = useFormat();
  const { data: me } = useMe();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [name, setName] = useState("");
  const [days, setDays] = useState<McpTokenLifetimeDays>(DEFAULT_LIFETIME);
  const [created, setCreated] = useState<CreatedMcpToken | null>(null);
  const [revoking, setRevoking] = useState<McpToken | null>(null);

  const list = useQuery({ queryKey: ["mcp-tokens"], queryFn: listMcpTokens });
  const tokens = list.data?.items ?? [];
  // Fixed at mount: render must stay pure (react-hooks/purity).
  const [now] = useState(() => Date.now());

  const create = useMutation({
    mutationFn: () => createMcpToken(name.trim(), days),
    onSuccess: (token) => {
      void qc.invalidateQueries({ queryKey: ["mcp-tokens"] });
      setCreateOpen(false);
      setName("");
      setDays(DEFAULT_LIFETIME);
      setCreated(token);
      // Drop the mutation's copy of the secret; only the reveal state keeps it.
      create.reset();
    },
  });

  const revoke = useMutation({
    mutationFn: (id: number) => revokeMcpToken(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["mcp-tokens"] });
      setRevoking(null);
    },
  });

  const closeCreate = () => {
    setCreateOpen(false);
    setName("");
    setDays(DEFAULT_LIFETIME);
    create.reset();
  };

  const closeCreated = () => {
    setCreated(null);
  };

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title={intl.formatMessage(messages.title)}
        description={intl.formatMessage(messages.description)}
      />

      <div className="grid gap-6 lg:grid-cols-[220px_1fr]">
        <SettingsNav
          active="mcp"
          showAdminSections={can(me, "config:manage")}
          onSelect={(key) => void navigate({ to: "/admin", search: { section: key } })}
        />

        <div className="space-y-4">
          <div>
            <Button onPress={() => setCreateOpen(true)}>
              <Plus className="h-4 w-4" />
              {intl.formatMessage(messages.createToken)}
            </Button>
          </div>

          {list.error && (
            <p role="alert" className="text-sm text-danger">
              {errorTextIntl(intl, list.error, intl.formatMessage(messages.loadFailed))}
            </p>
          )}

          {list.isSuccess && tokens.length === 0 ? (
            <Card>
              <Card.Content className="space-y-2">
                <div className="text-sm font-medium">{intl.formatMessage(messages.emptyTitle)}</div>
                <p className="text-sm text-muted">{intl.formatMessage(messages.emptyBody)}</p>
              </Card.Content>
            </Card>
          ) : (
            <Card>
              <Table.Root>
                <Table.ScrollContainer>
                  <Table.Content aria-label={intl.formatMessage(messages.tableLabel)} keyboardNavigationBehavior="arrow">
                    <Table.Header>
                      <Table.Column id="name" isRowHeader>{intl.formatMessage(messages.colName)}</Table.Column>
                      <Table.Column id="token">{intl.formatMessage(messages.colToken)}</Table.Column>
                      <Table.Column id="created" className="hidden md:table-cell">{intl.formatMessage(messages.colCreated)}</Table.Column>
                      <Table.Column id="expires">{intl.formatMessage(messages.colExpires)}</Table.Column>
                      <Table.Column id="last_used" className="hidden md:table-cell">{intl.formatMessage(messages.colLastUsed)}</Table.Column>
                      <Table.Column id="actions" className="text-right" aria-label={intl.formatMessage(messages.colActions)} />
                    </Table.Header>
                    <Table.Body>
                      {tokens.map((t) => {
                        const state = tokenState(t, now);
                        const dim = state === "active" ? undefined : "text-foreground/50";
                        const shownPrefix = `${t.prefix}…`;
                        return (
                          <Table.Row key={t.id}>
                            <Table.Cell className={cn("font-medium", dim)}>{t.name}</Table.Cell>
                            <Table.Cell className={cn("font-mono text-xs text-muted", dim)}>{shownPrefix}</Table.Cell>
                            <Table.Cell className={cn("hidden md:table-cell", dim)}>{fmt.date(new Date(t.createdAt), DATE_OPTIONS)}</Table.Cell>
                            <Table.Cell className={dim}>{fmt.date(new Date(t.expiresAt), DATE_OPTIONS)}</Table.Cell>
                            <Table.Cell className={cn("hidden md:table-cell", dim)}>
                              {t.lastUsedAt ? formatRelative(t.lastUsedAt, intl) : intl.formatMessage(messages.never)}
                            </Table.Cell>
                            <Table.Cell className="text-right">
                              {state === "active" ? (
                                <Button
                                  size="sm"
                                  variant="ghost"
                                  aria-label={intl.formatMessage({ id: "web.routes.mcp_tokens.revoke_token_name", defaultMessage: "Revoke token {name}", description: "MCP tokens: accessible name of the Revoke button on a row; {name} is the token name" }, { name: t.name })}
                                  onPress={() => setRevoking(t)}
                                >
                                  {intl.formatMessage(messages.revoke)}
                                </Button>
                              ) : (
                                <StateBadge label={intl.formatMessage(state === "expired" ? messages.expiredBadge : messages.revokedBadge)} />
                              )}
                            </Table.Cell>
                          </Table.Row>
                        );
                      })}
                    </Table.Body>
                  </Table.Content>
                </Table.ScrollContainer>
              </Table.Root>
            </Card>
          )}

          <a
            href={CLIENT_README_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-sm text-primary hover:underline"
          >
            {intl.formatMessage(messages.connect)}
            <ExternalLink className="h-3.5 w-3.5" aria-hidden />
          </a>
        </div>
      </div>

      <Modal isOpen={createOpen} onOpenChange={(open) => { if (!open) closeCreate(); }}>
        <ModalBackdrop isDismissable={!create.isPending} isKeyboardDismissDisabled={create.isPending}>
          <ModalContainer>
            <ModalDialog className="w-[480px] max-w-[480px] rounded-xl">
              <ModalHeader>
                <ModalHeading>{intl.formatMessage(messages.createTitle)}</ModalHeading>
              </ModalHeader>
              <ModalBody className="gap-3">
                <Label htmlFor="mcp-token-name" className="text-xs">{intl.formatMessage(messages.nameLabel)}</Label>
                <Input
                  id="mcp-token-name"
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={intl.formatMessage(messages.namePlaceholder)}
                  disabled={create.isPending}
                  autoComplete="off"
                />
                <Select
                  className="flex flex-col gap-1.5"
                  value={String(days)}
                  onChange={(v) => {
                    const next = LIFETIME_OPTIONS.find((o) => String(o) === String(v));
                    if (next) setDays(next);
                  }}
                  isDisabled={create.isPending}
                >
                  <Label htmlFor="mcp-token-days" className="text-xs">{intl.formatMessage(messages.expiresIn)}</Label>
                  <Select.Trigger
                    id="mcp-token-days"
                    className="w-full items-center rounded border border-border bg-surface px-3 py-2 text-sm hover:bg-surface/80"
                  >
                    <Select.Value />
                    <Select.Indicator className="ml-auto h-4 w-4" />
                  </Select.Trigger>
                  <Select.Popover className="rounded border border-border">
                    <ListBox className="p-0" aria-label={intl.formatMessage(messages.expiresIn)}>
                      {LIFETIME_OPTIONS.map((o) => (
                        <ListBoxItem key={o} id={String(o)}>
                          {intl.formatMessage({ id: "web.routes.mcp_tokens.lifetime_days", defaultMessage: "{count} days", description: "MCP tokens: a token lifetime option; {count} is the number of days" }, { count: o })}
                        </ListBoxItem>
                      ))}
                    </ListBox>
                  </Select.Popover>
                </Select>
                <p className="text-sm text-muted">{intl.formatMessage(messages.createHint)}</p>
                <ErrorLine error={create.error} />
              </ModalBody>
              <ModalFooter className="flex items-center justify-end gap-2">
                <Button variant="ghost" size="sm" onPress={closeCreate} isDisabled={create.isPending}>
                  {intl.formatMessage(messages.cancel)}
                </Button>
                <Button size="sm" isDisabled={name.trim() === "" || create.isPending} onPress={() => create.mutate()}>
                  {intl.formatMessage(messages.create)}
                </Button>
              </ModalFooter>
            </ModalDialog>
          </ModalContainer>
        </ModalBackdrop>
      </Modal>

      {created && <CreatedTokenDialog token={created} onClose={closeCreated} />}

      {revoking && (
        <ConfirmDialog
          open
          onOpenChange={(open) => {
            if (!open) {
              setRevoking(null);
              revoke.reset();
            }
          }}
          title={intl.formatMessage({ id: "web.routes.mcp_tokens.revoke_token_title", defaultMessage: "Revoke token {name}?", description: "MCP tokens: title of the revoke confirmation; {name} is the token name" }, { name: revoking.name })}
          description={
            <>
              {intl.formatMessage(messages.revokeBody)}
              <ErrorLine error={revoke.error} />
            </>
          }
          confirmLabel={intl.formatMessage(messages.revokeConfirm)}
          destructive
          busy={revoke.isPending}
          onConfirm={() => revoke.mutate(revoking.id)}
        />
      )}
    </div>
  );
}

// CreatedTokenDialog shows the secret exactly once. It is mounted only while a
// freshly created token is held, and onClose drops that state.
function CreatedTokenDialog({ token, onClose }: { token: CreatedMcpToken; onClose: () => void }) {
  const intl = useOptionalIntl();
  const configText = clientConfig(token.token, window.location.origin);

  return (
    <Modal isOpen onOpenChange={(open) => { if (!open) onClose(); }}>
      <ModalBackdrop>
        <ModalContainer>
          <ModalDialog className="w-[560px] max-w-[560px] rounded-xl">
            <ModalHeader>
              <ModalHeading>{intl.formatMessage(messages.createdTitle)}</ModalHeading>
            </ModalHeader>
            <ModalBody className="gap-4">
              <div role="alert" className="flex items-start gap-2 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-sm text-warning">
                <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0" aria-hidden />
                <span>{intl.formatMessage(messages.copyWarning)}</span>
              </div>

              <div className="space-y-1.5">
                <div className="text-xs font-medium">{intl.formatMessage(messages.tokenField)}</div>
                <div className="flex items-center gap-2">
                  <code className="min-w-0 flex-1 truncate rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm">
                    {middleEllipsis(token.token)}
                  </code>
                  <Button size="sm" variant="secondary" onPress={() => void copyText(token.token)}>
                    {intl.formatMessage(messages.copy)}
                  </Button>
                </div>
              </div>

              <div className="space-y-1.5">
                <div className="flex items-center justify-between gap-2">
                  <div className="text-xs font-medium">{intl.formatMessage(messages.configLabel)}</div>
                  <Button size="sm" variant="secondary" onPress={() => void copyText(configText)}>
                    {intl.formatMessage(messages.copy)}
                  </Button>
                </div>
                <pre className="max-h-72 overflow-auto rounded-md border border-border bg-surface p-3 font-mono text-xs">
                  <code>{configText}</code>
                </pre>
              </div>
            </ModalBody>
            <ModalFooter className="flex items-center justify-end">
              <Button size="sm" onPress={onClose}>{intl.formatMessage(messages.done)}</Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
