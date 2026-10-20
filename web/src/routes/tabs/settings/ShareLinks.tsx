import { useResourceClient, useResourceAccess, useResourceTarget, resourceKey } from "@/lib/resourceTarget";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Button,
  Modal,
  ModalBackdrop,
  ModalContainer,
  ModalDialog,
  ModalHeader,
  ModalHeading,
  ModalBody,
  ModalFooter,
  AlertDialog,
  AlertDialogBackdrop,
  AlertDialogContainer,
  AlertDialogDialog,
  AlertDialogHeader,
  AlertDialogHeading,
  AlertDialogBody,
  AlertDialogFooter,
  AlertDialogIcon,
  Table,
  Select,
  ListBox,
  ListBoxItem,
  Label,
  Description,
  Switch,
  Chip,
  Input,
} from "@heroui/react";
import { AlertCircle, Calendar as CalendarIcon, Copy, Link2 } from "lucide-react";
import type { ShareLink } from "@/types";
import { type ShareLinkCreateBody } from "@/lib/api";
import { errorTextIntl } from "@/lib/errors";
import { useFormat, type FormatApi } from "@/lib/i18n/format";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type MessageDescriptor } from "react-intl";

interface ShareLinksProps {
  name: string;
  ns?: string;
}

// Expiry options for the create dialog (FR-001): four day-count presets,
// "No expiry" (FR-002), and "Custom" (FR-003/FR-004). 30 days is the default.
const EXPIRY_LABELS = defineMessages({
  days15: { id: "web.routes.tabs.settings.share_links.expiry_15_days", defaultMessage: "15 days", description: "Share links: create dialog expiry option, a link valid for 15 days" },
  days30: { id: "web.routes.tabs.settings.share_links.expiry_30_days", defaultMessage: "30 days", description: "Share links: create dialog expiry option, a link valid for 30 days" },
  days60: { id: "web.routes.tabs.settings.share_links.expiry_60_days", defaultMessage: "60 days", description: "Share links: create dialog expiry option, a link valid for 60 days" },
  days90: { id: "web.routes.tabs.settings.share_links.expiry_90_days", defaultMessage: "90 days", description: "Share links: create dialog expiry option, a link valid for 90 days" },
  never: { id: "web.routes.tabs.settings.share_links.expiry_never", defaultMessage: "No expiry", description: "Share links: create dialog expiry option, the link never expires" },
  custom: { id: "web.routes.tabs.settings.share_links.expiry_custom", defaultMessage: "Custom", description: "Share links: create dialog expiry option, pick an explicit expiry date" },
});

const EXPIRY_OPTIONS: { label: MessageDescriptor; value: string }[] = [
  { label: EXPIRY_LABELS.days15, value: "15" },
  { label: EXPIRY_LABELS.days30, value: "30" },
  { label: EXPIRY_LABELS.days60, value: "60" },
  { label: EXPIRY_LABELS.days90, value: "90" },
  { label: EXPIRY_LABELS.never, value: "never" },
  { label: EXPIRY_LABELS.custom, value: "custom" },
];

// Display labels for link status values. The status strings stay English for
// logic; these are only what users read.
const STATUS_LABELS = defineMessages({
  Active: { id: "web.routes.tabs.settings.share_links.status_active", defaultMessage: "Active", description: "Share links: status chip for a link that still works" },
  Expired: { id: "web.routes.tabs.settings.share_links.status_expired", defaultMessage: "Expired", description: "Share links: status chip for a link past its expiry date" },
  Revoked: { id: "web.routes.tabs.settings.share_links.status_revoked", defaultMessage: "Revoked", description: "Share links: status chip for a link the owner revoked" },
});

const CAN_START_LABELS = defineMessages({
  canStart: { id: "web.routes.tabs.settings.share_links.can_start_value", defaultMessage: "Can start", description: "Share links: table cell for a link that allows starting the server" },
  viewOnly: { id: "web.routes.tabs.settings.share_links.view_only", defaultMessage: "View only", description: "Share links: table cell for a link that only allows viewing the server status" },
});

const DEFAULT_EXPIRY_CHOICE = "30";

// Maps a preset choice's value to its day count.
const PRESET_DAYS: Record<string, number> = { "15": 15, "30": 30, "60": 60, "90": 90 };

// OD-6: the long-lived-token warning shows at 365+ days from today.
const LONG_LIVED_THRESHOLD_DAYS = 365;

const ONE_DAY_MS = 24 * 60 * 60 * 1000;

// Today's (or `date`'s) calendar date as "YYYY-MM-DD" in the browser's local
// time zone. Used for the custom-date picker's minimum and for comparing
// against the chosen custom date; safe to compare lexicographically since
// both sides always use this same zero-padded format.
function localISODate(date: Date): string {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, "0");
  const d = String(date.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

// The earliest selectable custom date: tomorrow, local time (FR-003 requires
// a date strictly after today).
function tomorrowISODate(): string {
  const d = new Date();
  d.setDate(d.getDate() + 1);
  return localISODate(d);
}

// A custom date is valid once it is strictly after today, local time.
function isCustomDateValid(customDate: string): boolean {
  return customDate !== "" && customDate > localISODate(new Date());
}

// Converts a "YYYY-MM-DD" custom date into the UTC instant at which the link
// stops working: the end of that calendar day in the browser's local time
// zone (OD-2's ruling), i.e. 23:59:59.999 local time on the chosen date —
// not midnight.
function customDateToExpiresAt(customDate: string): string {
  const [year, month, day] = customDate.split("-").map(Number);
  return new Date(year, month - 1, day, 23, 59, 59, 999).toISOString();
}

// The end-of-day expiresAt instant for a preset of N calendar days from
// today (OD-1's ruling, 2026-09-20): a preset is a CALENDAR date, not N x
// 24h from the request time, so it goes through the same end-of-day path
// as a custom date (OD-2) — a "30 days" link expires at 23:59:59.999 local
// on that calendar date, not at whatever wall-clock time it was created.
// `setDate` advances by whole calendar days, which is correct across a DST
// boundary, unlike `Date.now() + N * ONE_DAY_MS`.
function presetDaysToExpiresAt(days: number): string {
  const d = new Date();
  d.setDate(d.getDate() + days);
  return customDateToExpiresAt(localISODate(d));
}

// Whether a custom date is 365 days or more from today (OD-6), measured as
// the whole-day difference between local midnight today and local midnight
// on the chosen date.
function isLongLivedCustomDate(customDate: string): boolean {
  const [year, month, day] = customDate.split("-").map(Number);
  const chosen = new Date(year, month - 1, day);
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const diffDays = Math.round((chosen.getTime() - today.getTime()) / ONE_DAY_MS);
  return diffDays >= LONG_LIVED_THRESHOLD_DAYS;
}

// Status badge coloring
function statusColor(status: string): "success" | "warning" | "danger" | "default" {
  if (status === "Active") return "success";
  if (status === "Expired") return "warning";
  if (status === "Revoked") return "danger";
  return "default";
}

// Determine link status based on revocation first, then expiry. A null expiry means the link
// never expires (FR-007), so it can never be reported "Expired". Revoked links are always Revoked
// regardless of expiry (FR-010).
function getLinkStatus(expiresAt: string | null, revokedAt?: string | null): string {
  if (revokedAt !== null && revokedAt !== undefined) return "Revoked";
  if (expiresAt === null) return "Active";
  const expiry = new Date(expiresAt);
  const now = new Date();
  if (expiry <= now) return "Expired";
  return "Active";
}

// Format date for display (e.g., "Jul 28, 2026"), or "Never" for a
// no-expiry link (SC-004).
function formatDate(dateStr: string | null, fmt: FormatApi, neverLabel: string): string {
  if (dateStr === null) return neverLabel;
  const date = new Date(dateStr);
  return fmt.date(date, {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

// CreateDialog: Modal for entering link details
interface CreateDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  serverName: string;
  ns?: string;
  onLinkCreated?: (link: ShareLink) => void;
}

function CreateDialog({
  open,
  onOpenChange,
  serverName,
  ns,
  onLinkCreated,
}: CreateDialogProps) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name: serverName, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const { Shares } = resourceClient;
  const [expiryChoice, setExpiryChoice] = useState(DEFAULT_EXPIRY_CHOICE);
  const [customDate, setCustomDate] = useState("");
  const [canStart, setCanStart] = useState(false);

  const customDateValid = expiryChoice !== "custom" || isCustomDateValid(customDate);
  const showLongLivedWarning =
    expiryChoice === "custom" && customDate !== "" && isLongLivedCustomDate(customDate);

  const create = useMutation({
    mutationFn: () => {
      const body: ShareLinkCreateBody =
        expiryChoice === "never"
          ? { neverExpires: true, canStart }
          : expiryChoice === "custom"
            ? { expiresAt: customDateToExpiresAt(customDate), canStart }
            : {
                expiresAt: presetDaysToExpiresAt(PRESET_DAYS[expiryChoice]),
                canStart,
              };
      return Shares.create(serverName, body, ns);
    },
    onSuccess: (link) => {
      onOpenChange(false);
      onLinkCreated?.(link);
    },
  });

  // Reset state when dialog opens
  const [prevOpen, setPrevOpen] = useState(open);
  if (open !== prevOpen) {
    setPrevOpen(open);
    if (open) {
      setExpiryChoice(DEFAULT_EXPIRY_CHOICE);
      setCustomDate("");
      setCanStart(false);
      create.reset();
    }
  }

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop
        isDismissable={!create.isPending}
        isKeyboardDismissDisabled={create.isPending}
      >
      <ModalContainer>
        <ModalDialog className="w-[480px] max-w-[480px]">
          <ModalHeader>
            <ModalHeading>{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.create_share_link_for_servername", defaultMessage: "Create share link for {serverName}", description: "Share links: sentence with serverName in ModalHeading" }, { serverName: serverName })}</ModalHeading>
            <Description className="text-sm leading-[21px] text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.anyone_with_this_link_can_check_the", defaultMessage: "Anyone with this link can check the server's status and connection address without signing in.", description: "Share links: description under the Create share link heading saying anyone with the link can view the server status and address without signing in" })}</Description>
          </ModalHeader>

          <ModalBody className="gap-4">
            <div className="flex flex-col gap-1">
              <Select
                value={expiryChoice}
                onChange={(key) => setExpiryChoice(String(key))}
              >
                <Label className="text-xs">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.expires_in", defaultMessage: "Expires in", description: "Label for the expiry picker in the create-link dialog" })}</Label>
                <Select.Trigger>
                  <Select.Value className="text-[var(--field-placeholder)]" />
                  <Select.Indicator />
                </Select.Trigger>
                <Select.Popover>
                  <ListBox>
                    {EXPIRY_OPTIONS.map((opt) => (
                      <ListBoxItem key={opt.value} id={opt.value}>
                        {intl.formatMessage(opt.label)}
                      </ListBoxItem>
                    ))}
                  </ListBox>
                </Select.Popover>
              </Select>

              {expiryChoice === "never" && (
                <p className="text-xs text-warning">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.this_link_works_until_you_revoke_it", defaultMessage: "This link works until you revoke it.", description: "Share links: warning under the expiry picker shown when the link never expires; it works until revoked" })}</p>
              )}

              {expiryChoice === "custom" && (
                <>
                  <Label className="text-xs">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.expires_on", defaultMessage: "Expires on", description: "Label for the custom expiry date input in the create-link dialog" })}</Label>
                  <div className="relative">
                    <Input
                      type="date"
                      aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.share_links.expires_on", defaultMessage: "Expires on", description: "Label for the custom expiry date input in the create-link dialog" })}
                      min={tomorrowISODate()}
                      value={customDate}
                      onChange={(e) => setCustomDate(e.target.value)}
                      className="date-input--modal pr-9"
                    />
                    <CalendarIcon className="pointer-events-none absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" />
                  </div>
                  {showLongLivedWarning && (
                    <p className="text-xs text-warning">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.long_lived_link_it_stays_valid_for_over", defaultMessage: "Long-lived link — it stays valid for over a year unless you revoke it.", description: "Share links: warning under the expiry date field shown when the chosen date is more than a year away" })}</p>
                  )}
                </>
              )}
            </div>

            <div className="flex items-center gap-3">
              <Switch
                isSelected={canStart}
                onChange={setCanStart}
                aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.share_links.allow_starting_the_server", defaultMessage: "Allow starting the server", description: "Share links: switch that lets the link holder wake the server from sleep; the text is its accessible name" })}
              >
                <Switch.Content>
                  <Switch.Control>
                    <Switch.Thumb />
                  </Switch.Control>
                </Switch.Content>
              </Switch>
              <div className="flex-1">
                <Label className="block text-sm">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.allow_starting_the_server", defaultMessage: "Allow starting the server", description: "Share links: switch that lets the link holder wake the server from sleep; the text is its accessible name" })}</Label>
                <Description className="block text-xs text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.the_link_holder_can_wake_servername", defaultMessage: "The link holder can wake {serverName} from asleep. Without this, they can only view its status.", description: "Share links: sentence with serverName in Description" }, { serverName: serverName })}</Description>
              </div>
            </div>

            {create.isError && (
              <div className="rounded-lg bg-danger/10 p-3 text-sm text-danger">
                {errorTextIntl(intl, create.error, intl.formatMessage({ id: "web.routes.tabs.settings.share_links.create_failed", defaultMessage: "Failed to create link", description: "Share links: fallback error shown when creating a link fails" }))}
              </div>
            )}
          </ModalBody>

          <ModalFooter className="flex items-center justify-end gap-2">
            <Button
              variant="ghost"
              size="sm"
              className="gap-[5px] text-[13px]"
              onPress={() => onOpenChange(false)}
              isDisabled={create.isPending}
            >{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.cancel", defaultMessage: "Cancel", description: "Button that closes the share link dialog without changing anything" })}</Button>
            <Button
              size="sm"
              variant="primary"
              className="gap-[5px] text-[13px]"
              isDisabled={create.isPending || !customDateValid}
              onPress={() => create.mutate()}
            >
              <Link2 className="h-3.5 w-3.5" />
              {create.isPending
                ? intl.formatMessage({ id: "web.routes.tabs.settings.share_links.creating", defaultMessage: "Creating…", description: "Share links: create dialog submit button while the link is being created" })
                : intl.formatMessage({ id: "web.routes.tabs.settings.share_links.create_link_submit", defaultMessage: "Create link", description: "Share links: create dialog submit button" })}
            </Button>
          </ModalFooter>
        </ModalDialog>
      </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}

// CreatedDialog: Modal showing the generated link with copy button
interface CreatedDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  link: ShareLink | null;
}

function CreatedDialog({ open, onOpenChange, link }: CreatedDialogProps) {
  const intl = useOptionalIntl();
  const [copied, setCopied] = useState(false);

  if (!link?.token) return null;

  const url = `${window.location.origin}/share/${link.token}`;

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(url);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Silently fail if clipboard is not available
    }
  };

  return (
    <Modal isOpen={open} onOpenChange={onOpenChange}>
      <ModalBackdrop isDismissable>
        <ModalContainer>
          <ModalDialog className="w-[480px] max-w-[480px]">
            <ModalHeader>
              <ModalHeading>{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.share_link_created", defaultMessage: "Share link created", description: "Share links: heading of the dialog shown after a share link is created" })}</ModalHeading>
              <Description className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.send_this_to_your_friend_it_works", defaultMessage: "Send this to your friend. It works without a Gameplane account.", description: "Share links: description in the created-link dialog telling the user to send the link to a friend, who needs no Gameplane account" })}</Description>
            </ModalHeader>

            <ModalBody className="gap-4">

              <div className="rounded-lg border-2 border-warning bg-warning/10 p-3">
                <div className="flex items-start gap-2">
                  <AlertCircle className="h-5 w-5 shrink-0 text-warning-soft-foreground" />
                  <div className="text-sm text-warning-soft-foreground">
                    <strong>{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.you_will_not_see_this_link_again", defaultMessage: "You will not see this link again", description: "Share links: bold warning title in the created-link dialog saying the link cannot be seen again" })}</strong>
                    <p className="mt-1">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.gameplane_stores_only_a_one_way_hash_of", defaultMessage: "Gameplane stores only a one-way hash of the token, so it can't be shown or recovered after you close this dialog. Copy it now.", description: "Share links: warning text under the title saying only a hash of the token is stored, so the link cannot be shown again and must be copied now" })}</p>
                  </div>
                </div>
              </div>

              <div className="relative rounded-lg border border-divider bg-surface p-2.5 px-3.5 pr-10">
                <div className="break-all font-mono text-[13px] text-foreground">{url}</div>
                <Button
                  variant="ghost"
                  size="sm"
                  className="absolute right-1 top-1 h-7 w-7 min-w-0 p-0"
                  aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.share_links.copy_url", defaultMessage: "Copy URL", description: "Share links: icon-only button in the created-link box that copies the URL; read by screen readers; keep it short" })}
                  onPress={handleCopy}
                >
                  <Copy className="h-4 w-4" />
                </Button>
              </div>
            </ModalBody>

            <ModalFooter className="flex items-center justify-end gap-2">
              <Button
                size="sm"
                variant="outline"
                className="gap-[5px] text-[13px]"
                onPress={handleCopy}
              >
                <Copy className="h-3.5 w-3.5" />
                {copied
                  ? intl.formatMessage({ id: "web.routes.tabs.settings.share_links.copied", defaultMessage: "Copied!", description: "Share links: copy button label briefly shown after the link was copied" })
                  : intl.formatMessage({ id: "web.routes.tabs.settings.share_links.copy_link", defaultMessage: "Copy link", description: "Share links: button that copies the created share link" })}
              </Button>
              <Button
                size="sm"
                variant="primary"
                className="gap-[5px] text-[13px]"
                onPress={() => onOpenChange(false)}
              >{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.done", defaultMessage: "Done", description: "Button that closes the link-created dialog" })}</Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}

// RevokeDialog: AlertDialog for confirming revocation
interface RevokeDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  linkId: string | null;
  serverName: string;
  ns?: string;
  onRevoked?: () => void;
}

function RevokeDialog({
  open,
  onOpenChange,
  linkId,
  serverName,
  ns,
  onRevoked,
}: RevokeDialogProps) {
  const intl = useOptionalIntl();
  const resourceTarget = useResourceTarget({ name: serverName, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const { Shares } = resourceClient;
  const revoke = useMutation({
    mutationFn: () => (linkId ? Shares.revoke(serverName, linkId, ns) : Promise.reject()),
    onSuccess: () => {
      onOpenChange(false);
      onRevoked?.();
    },
  });

  return (
    <AlertDialog isOpen={open} onOpenChange={onOpenChange}>
      <AlertDialogBackdrop
        isDismissable={!revoke.isPending}
        isKeyboardDismissDisabled={revoke.isPending}
      >
      <AlertDialogContainer>
        <AlertDialogDialog className="w-[440px] max-w-[440px] gap-0 rounded-[12px] p-5">
          <AlertDialogHeader className="m-0 p-0">
            <div className="flex items-center gap-3">
              <AlertDialogIcon status="danger" className="shrink-0">
                <AlertCircle className="h-5 w-5" />
              </AlertDialogIcon>
              <AlertDialogHeading>{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.revoke_this_share_link", defaultMessage: "Revoke this share link?", description: "Share links: title of the confirmation dialog that asks before a share link is revoked" })}</AlertDialogHeading>
            </div>
          </AlertDialogHeader>

          <AlertDialogBody className="m-0 p-0 pt-4">
            <div className="space-y-4">
              <Description className="text-sm leading-[21px] text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.anyone_using_this_link_will_immediately", defaultMessage: "Anyone using this link will immediately lose access to {serverName}'s status page. This action cannot be undone.", description: "Share links: sentence with serverName in Description" }, { serverName: serverName })}</Description>
              {revoke.isError && (
                <div className="rounded-lg bg-danger/10 p-3 text-sm text-danger">
                  {errorTextIntl(intl, revoke.error, intl.formatMessage({ id: "web.routes.tabs.settings.share_links.revoke_failed", defaultMessage: "Failed to revoke link", description: "Share links: fallback error shown when revoking a link fails" }))}
                </div>
              )}
            </div>
          </AlertDialogBody>

          <AlertDialogFooter className="m-0 flex items-center justify-end gap-2 p-0 pt-5">
            <Button
              variant="ghost"
              size="sm"
              isDisabled={revoke.isPending}
              onPress={() => onOpenChange(false)}
            >{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.cancel", defaultMessage: "Cancel", description: "Button that closes the share link dialog without changing anything" })}</Button>
            <Button
              size="sm"
              variant="danger"
              isDisabled={revoke.isPending}
              onPress={() => revoke.mutate()}
            >
              {revoke.isPending
                ? intl.formatMessage({ id: "web.routes.tabs.settings.share_links.revoking", defaultMessage: "Revoking…", description: "Share links: revoke dialog submit button while the link is being revoked" })
                : intl.formatMessage({ id: "web.routes.tabs.settings.share_links.revoke_link_submit", defaultMessage: "Revoke link", description: "Share links: revoke dialog submit button" })}
            </Button>
          </AlertDialogFooter>
        </AlertDialogDialog>
      </AlertDialogContainer>
      </AlertDialogBackdrop>
    </AlertDialog>
  );
}

// Main component
export function ShareLinksSection({ name, ns }: ShareLinksProps) {
  const intl = useOptionalIntl();
  const fmt = useFormat();
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const access = useResourceAccess();
  const qc = useQueryClient();
  const [createOpen, setCreateOpen] = useState(false);
  const [createdLink, setCreatedLink] = useState<ShareLink | null>(null);
  const [createdOpen, setCreatedOpen] = useState(false);
  const [revokeOpen, setRevokeOpen] = useState(false);
  const [revokeId, setRevokeId] = useState<string | null>(null);

  const { data: links, isLoading } = useQuery({
    queryKey: resourceKey(resourceTarget, "sharelinks", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Shares.list(name, ns),
    enabled: !!name,
  });

  const neverLabel = intl.formatMessage({ id: "web.routes.tabs.settings.share_links.never", defaultMessage: "Never", description: "Share links: table cell for a link's expiry when the link never expires" });

  const handleLinkCreated = (link: ShareLink) => {
    setCreatedLink(link);
    setCreatedOpen(true);
    void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "sharelinks", name, ns) });
  };

  const handleRevoked = () => {
    void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "sharelinks", name, ns) });
  };

  const empty = !isLoading && (!links || links.length === 0);

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between">
        <div>
          <h3 className="text-base font-medium">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.share_links", defaultMessage: "Share links", description: "Share links: section heading and table label for the list of share links; the table name is read by screen readers" })}</h3>
          <p className="mt-1 text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.let_people_without_a_gameplane_account", defaultMessage: "Let people without a Gameplane account check this server's status and connection address. Only the owner can create or revoke links.", description: "Share links: hint under the Share links heading saying people without an account can check the status and address, and only the owner can create or revoke links" })}</p>
        </div>
        <Button variant="primary" size="sm" isDisabled={!access?.isOwner} onPress={() => setCreateOpen(true)}>
          <Link2 className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.create_link", defaultMessage: "Create link", description: "Button that opens the create-link dialog" })}</Button>
      </div>

      {empty ? (
        <div className="rounded-lg border border-dashed border-divider p-8 text-center">
          <Link2 className="mx-auto h-12 w-12 text-muted" />
          <h4 className="mt-3 font-medium">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.no_share_links_yet", defaultMessage: "No share links yet", description: "Empty-state heading when the server has no share links" })}</h4>
          <p className="mt-1 text-sm text-muted">{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.create_a_link_so_a_friend_without_a", defaultMessage: "Create a link so a friend without a Gameplane account can check this server's status and connect — without giving them access to the dashboard.", description: "Share links: empty-state text shown when the server has no share links, explaining a link lets someone check status without dashboard access" })}</p>
          <Button
            variant="primary"
            size="sm"
            className="mt-4"
            isDisabled={!access?.isOwner} onPress={() => setCreateOpen(true)}
          >
            <Link2 className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.create_link", defaultMessage: "Create link", description: "Button that opens the create-link dialog" })}</Button>
        </div>
      ) : (
        <div className="overflow-x-auto rounded-lg border border-divider">
          <Table.Root className="table--mono">
            <Table.Content aria-label={intl.formatMessage({ id: "web.routes.tabs.settings.share_links.share_links", defaultMessage: "Share links", description: "Share links: section heading and table label for the list of share links; the table name is read by screen readers" })} keyboardNavigationBehavior="arrow">
              <Table.Header>
                <Table.Column isRowHeader>{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.created", defaultMessage: "Created", description: "Table column header for the share link creation date" })}</Table.Column>
                <Table.Column>{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.expires", defaultMessage: "Expires", description: "Table column header for the share link expiry date" })}</Table.Column>
                <Table.Column>{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.can_start", defaultMessage: "Can start", description: "Table column header for whether the share link allows starting the server" })}</Table.Column>
                <Table.Column>{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.status", defaultMessage: "Status", description: "Table column header for the share link status (Active, Expired or Revoked)" })}</Table.Column>
                <Table.Column />
              </Table.Header>
              <Table.Body>
                {links?.map((link) => (
                  <Table.Row key={link.id}>
                    <Table.Cell>{formatDate(link.createdAt, fmt, neverLabel)}</Table.Cell>
                    <Table.Cell>{formatDate(link.expiresAt, fmt, neverLabel)}</Table.Cell>
                    <Table.Cell>
                      <Chip
                        variant="soft"
                        size="sm"
                        className={link.canStart ? "text-success" : "text-foreground"}
                      >
                        {link.canStart
                          ? intl.formatMessage(CAN_START_LABELS.canStart)
                          : intl.formatMessage(CAN_START_LABELS.viewOnly)}
                      </Chip>
                    </Table.Cell>
                    <Table.Cell>
                      <Chip
                        variant="soft"
                        size="sm"
                        color={statusColor(getLinkStatus(link.expiresAt, link.revokedAt))}
                      >
                        {intl.formatMessage(STATUS_LABELS[getLinkStatus(link.expiresAt, link.revokedAt) as keyof typeof STATUS_LABELS])}
                      </Chip>
                    </Table.Cell>
                    <Table.Cell>
                      {getLinkStatus(link.expiresAt, link.revokedAt) === "Active" ? (
                        <Button
                          variant="danger-soft"
                          size="sm"
                          onPress={() => {
                            if (!access?.isOwner) return;
                            setRevokeId(link.id);
                            setRevokeOpen(true);
                          }}
                        >{intl.formatMessage({ id: "web.routes.tabs.settings.share_links.revoke", defaultMessage: "Revoke", description: "Table row button that revokes this share link" })}</Button>
                      ) : (
                        <span className="text-muted">{intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</span>
                      )}
                    </Table.Cell>
                  </Table.Row>
                ))}
              </Table.Body>
            </Table.Content>
          </Table.Root>
        </div>
      )}

      <CreateDialog
        open={createOpen}
        onOpenChange={setCreateOpen}
        serverName={name}
        ns={ns}
        onLinkCreated={handleLinkCreated}
      />

      <CreatedDialog open={createdOpen} onOpenChange={setCreatedOpen} link={createdLink} />

      <RevokeDialog
        open={revokeOpen}
        onOpenChange={setRevokeOpen}
        linkId={revokeId}
        serverName={name}
        ns={ns}
        onRevoked={handleRevoked}
      />
    </div>
  );
}
