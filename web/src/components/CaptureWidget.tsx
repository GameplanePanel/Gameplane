import { useResourceClient, useResourceTarget, resourceKey, useResourcePermissions, resourceCan } from "@/lib/resourceTarget";
import { useServerCapabilities, type CaptureCapabilities } from "@/lib/useServerCapabilities";
// Network-capture surface for a GameServer's "Capture" tab. Mirrors the
// Backups tab's structure (own TanStack Query calls, own mutations, plain
// <table> for the list) per CLAUDE.md's "Add a new dashboard page" recipe.
// Design source: assets/design-export/json/{f0s9zG,Bbnga,dBILX,xvlB6,m5kOm4,
// O08uaD,b4eaUf}.json — read directly (never design.pen; see CLAUDE.md
// rule 2). Endpoints and error-body shape follow
// specs/done_003-network-capture-sidecar/contracts/rest-api.md (plain-text
// httperr bodies, `:verb` route suffixes, capture id as a query param).
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  CircleCheckBig,
  CircleX,
  Download,
  Eye,
  Inbox,
  Loader2,
  Lock,
  Trash2,
} from "lucide-react";
import {
  Button,
  Card,
  CardContent,
  Input,
  Label,
  Select,
  ListBox,
  ListBoxItem,
  Modal,
  ModalBackdrop,
  ModalContainer,
  ModalDialog,
  ModalHeader,
  ModalHeading,
  ModalBody,
  ModalFooter,
  Table,
  TableHeader,
  TableColumn,
  TableBody,
  TableRow,
  TableCell,
  Description,
  AlertDialog,
  AlertDialogBackdrop,
  AlertDialogContainer,
  AlertDialogDialog,
  AlertDialogHeader,
  AlertDialogHeading,
  AlertDialogBody,
  AlertDialogFooter,
} from "@heroui/react";
import { APIError, CaptureStartBody } from "@/lib/api";
import { captureListRefetchMs, isCaptureActive } from "@/lib/capturePolling";
import { useMe } from "@/lib/auth";
import { IdentityUnavailable } from "@/components/RequireRole";
import { CaptureWarningBanner } from "@/components/ui/CaptureWarningBanner";
import { ErrorBanner } from "@/components/ui/ErrorBanner";
import { Chip } from "@/components/ui/PhaseChip";
import { formatBytes, formatRelative } from "@/lib/utils";
import { useFormat } from "@/lib/i18n/format";
import type { GameServer, NetworkCapture } from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type IntlShape } from "react-intl";

const DEFAULT_RETENTION_SECONDS = 86400; // 24h — FR-007's engineering default, not a legal mandate.
// Display-only defaults while access is disabled or capabilities are unresolved.
// A ready response must contain validated site limits before actions are enabled.
const CAPTURE_DISPLAY_DEFAULTS: CaptureCapabilities = {
  enabled: false, files: false, state: "unavailable",
  defaultRetentionSeconds: DEFAULT_RETENTION_SECONDS, maxRetentionSeconds: 604800,
  defaultMaxDurationSeconds: 300, defaultMaxSizeBytes: 5 * 1024 * 1024 * 1024,
};

function formatDuration(totalSeconds: number): string {
  const s = Math.max(0, Math.floor(totalSeconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const sec = s % 60;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${sec}s`;
  return `${sec}s`;
}

function elapsedSeconds(startedAt: string): number {
  const t = new Date(startedAt).getTime();
  if (Number.isNaN(t)) return 0;
  return Math.max(0, Math.floor((Date.now() - t) / 1000));
}

function durationBetween(startedAt: string, completedAt: string, empty: string): string {
  if (!startedAt || !completedAt) return empty;
  const start = new Date(startedAt).getTime();
  const end = new Date(completedAt).getTime();
  if (Number.isNaN(start) || Number.isNaN(end)) return empty;
  return formatDuration((end - start) / 1000);
}

// Expiry badges recolor as the retention window closes in, so an admin
// scanning the table can tell "plenty of time" from "about to be
// GC'd" at a glance (see assets/design-export/json/m5kOm4.json's per-row colors).
function expiryLabel(expiresAt: string, empty: string): { text: string; color: "default" | "warning" | "danger" } {
  const t = new Date(expiresAt).getTime();
  if (Number.isNaN(t)) return { text: empty, color: "default" };
  const secondsLeft = Math.max(0, Math.floor((t - Date.now()) / 1000));
  const h = Math.floor(secondsLeft / 3600);
  const m = Math.floor((secondsLeft % 3600) / 60);
  const text = h ? `${h}h${m ? ` ${m}m` : ""}` : `${m}m`;

  let color: "default" | "warning" | "danger" = "default";
  if (secondsLeft <= 3600) color = "danger";
  else if (secondsLeft <= 21600) color = "warning";

  return { text, color };
}

const phaseColorMap: Record<string, "default" | "success" | "warning" | "danger"> = {
  Pending: "default",
  Running: "warning",
  Completed: "success",
  Failed: "danger",
  Expired: "default",
};

// Display labels for capture phase values. The phase value keeps driving the
// color lookup above; only the text shown in the table is translated.
const phaseMessages = defineMessages({
  Pending: { id: "web.components.capture_widget.phase_pending", defaultMessage: "Pending", description: "Capture table: status chip for a capture that is waiting to start" },
  Running: { id: "web.components.capture_widget.phase_running", defaultMessage: "Running", description: "Capture table: status chip for a capture that is recording" },
  Completed: { id: "web.components.capture_widget.phase_completed", defaultMessage: "Completed", description: "Capture table: status chip for a capture that finished" },
  Failed: { id: "web.components.capture_widget.phase_failed", defaultMessage: "Failed", description: "Capture table: status chip for a capture that failed" },
  Expired: { id: "web.components.capture_widget.phase_expired", defaultMessage: "Expired", description: "Capture table: status chip for a capture whose retention window ended" },
});

function phaseLabel(intl: IntlShape, value: string): string {
  return Object.prototype.hasOwnProperty.call(phaseMessages, value)
    ? intl.formatMessage(phaseMessages[value as keyof typeof phaseMessages])
    : value;
}

interface Props {
  name: string;
  ns?: string;
  gs?: GameServer;
}

export function CaptureWidget({ name, ns, gs }: Props) {
  const intl = useOptionalIntl();
  const fmt = useFormat();
  const { data: me, isLoading: meLoading, error: meError, refetch: refetchMe } = useMe();
  const permissions = useResourcePermissions();
  const canManage = !!me && resourceCan(permissions, "captures:manage");
  const canRead = canManage;
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const capabilities = useServerCapabilities(resourceTarget, canManage);
  const capabilityReady = capabilities.isSuccess && capabilities.data.state === "ready";
  const canStartCapture = canManage && capabilityReady && capabilities.data.enabled;
  const canDownload = canRead && capabilityReady && capabilities.data.files;
  const capabilityMessage = !canManage ? null
    : capabilities.isPending ? intl.formatMessage({ id: "web.components.capture_widget.checking_capture_support", defaultMessage: "Checking capture support…", description: "Capture widget: status line while capture support loads" })
    : capabilities.isError || capabilities.data?.state === "unavailable" ? intl.formatMessage({ id: "web.components.capture_widget.capture_access_unavailable", defaultMessage: "Capture access is temporarily unavailable at this location.", description: "Capture widget: status line when capture access is unavailable at this location" })
    : capabilities.data?.state === "unsupported" ? intl.formatMessage({ id: "web.components.capture_widget.gateway_update_needed", defaultMessage: "This location needs a gateway update before captures can be managed here.", description: "Capture widget: status line when the location's gateway is too old for captures" })
    : !capabilities.data?.enabled ? intl.formatMessage({ id: "web.components.capture_widget.captures_disabled_at_location", defaultMessage: "New captures are disabled at this location. Existing capture files remain available.", description: "Capture widget: status line when new captures are disabled at this location" })
    : null;
  const { Captures } = resourceClient;
  const qc = useQueryClient();

  const enabled = gs?.spec.capture?.enabled === true;
  const captureLimits = capabilities.data?.state === "ready" ? capabilities.data : CAPTURE_DISPLAY_DEFAULTS;
  const retentionSeconds = gs?.spec.capture?.retentionSeconds ?? captureLimits.defaultRetentionSeconds;
  const retentionHours = retentionSeconds / 3600;

  const [bannerDismissed, setBannerDismissed] = useState(false);
  const [showStartModal, setShowStartModal] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<NetworkCapture | null>(null);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  // The capture the user last asked to stop. A stop only requests it: the
  // operator completes the capture once its sidecar has stopped (F-259), so
  // the list is polled faster until that capture leaves Pending/Running —
  // see captureListRefetchMs.
  const [stoppingId, setStoppingId] = useState<string | null>(null);

  // All hooks above this point must run unconditionally on every render —
  // useMe() resolves from `undefined` to a real user after mount, which
  // would otherwise flip this early return between renders and violate the
  // Rules of Hooks (mount-time "fewer hooks than previous render" crash) for
  // every legitimately-authorized user. Only the JSX below this point may
  // branch on canManage. Mirrors NetworkCaptureSection's shape (calls every
  // hook first, gates only the returned JSX).
  const { data: captures } = useQuery({
    queryKey: resourceKey(resourceTarget, "captures", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Captures.list(name, ns),
    enabled: enabled && canRead,
    refetchInterval: (query) => captureListRefetchMs(query.state.data, stoppingId),
  });

  const activeCapture = (captures?.captures ?? []).find(isCaptureActive);
  const { data: activeCaptureDetails } = useQuery({
    queryKey: resourceKey(resourceTarget, "capture", name, activeCapture?.captureId, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Captures.get(name, activeCapture!.captureId, ns),
    enabled: !!activeCapture && canManage,
  });

  const enableMut = useMutation({
    mutationFn: () => Captures.enable(name, ns),
    onSuccess: () => qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "server", name, ns) }),
  });
  const disableMut = useMutation({
    mutationFn: () => Captures.disable(name, ns),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "server", name, ns) });
      void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "captures", name, ns) });
    },
  });
  const stopMut = useMutation({
    mutationFn: (captureId: string) => Captures.stop(name, captureId, ns),
    onSuccess: (_stopped, captureId) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      setStoppingId(captureId);
      return qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "captures", name, ns) });
    },
  });
  const deleteMut = useMutation({
    mutationFn: (captureId: string) => Captures.remove(name, captureId, ns),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "captures", name, ns) });
      setDeleteTarget(null);
    },
  });
  const fileMut = useMutation({
    mutationFn: (captureId: string) => Captures.download(name, captureId, ns),
    onSuccess: (blob, captureId) => {
      void qc.invalidateQueries({ queryKey: ["fleet"] });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `capture-${captureId}.pcapng`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
    },
  });

  if (meLoading) {
    return (
      <div className="p-6">
        <Card>
          <CardContent className="flex flex-col items-center gap-3 p-10 text-center">
            <Loader2 className="h-7 w-7 animate-spin text-default-500" />
            <p className="text-sm text-default-500">{intl.formatMessage({ id: "web.components.capture_widget.checking_access", defaultMessage: "Checking access…", description: "Capture widget: loading text shown in the capture panel while the user's packet capture access is being checked" })}</p>
          </CardContent>
        </Card>
      </div>
    );
  }

  // /users/me failed after its retries: the caller's permissions are
  // unknown, not absent, so show the retryable identity-unavailable state
  // (controls stay closed) rather than claiming access is denied.
  if (!me) {
    return <IdentityUnavailable error={meError} onRetry={() => void refetchMe()} />;
  }

  if (!canManage) {
    return (
      <div className="p-6">
        <Card>
          <CardContent className="flex flex-col items-center gap-3 p-10 text-center">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-default-100">
              <Lock className="h-7 w-7 text-default-500" />
            </div>
            <p className="text-sm font-medium">{intl.formatMessage({ id: "web.components.capture_widget.you_don_t_have_access_to_packet_capture", defaultMessage: "You don't have access to packet capture on this server.", description: "Capture widget: heading in the empty-state card shown when the user has no packet capture access on this server" })}</p>
            <p className="max-w-md text-xs text-default-500">{intl.formatMessage({ id: "web.components.capture_widget.viewing_starting_and_downloading", defaultMessage: "Viewing, starting and downloading captures requires capture access for this server. Ask an administrator if you need it.", description: "Capture widget: hint under the no-access heading saying capture access is needed to view, start or download captures; users should ask an administrator" })}</p>
          </CardContent>
        </Card>
      </div>
    );
  }

  if (!enabled) {
    return (
      <div className="p-6">
        <Card>
          <CardContent className="flex flex-col items-center gap-3 p-10 text-center">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-default-100">
              <Inbox className="h-7 w-7 text-default-500" />
            </div>
            <p className="text-sm font-medium">{intl.formatMessage({ id: "web.components.capture_widget.capture_is_not_enabled_on_this_server", defaultMessage: "Capture is not enabled on this server.", description: "Capture widget: heading in the empty-state card shown when packet capture is turned off for this server" })}</p>
            <p className="max-w-md text-xs text-default-500">{intl.formatMessage({ id: "web.components.capture_widget.network_packet_capture_is_an_optional", defaultMessage: "Network packet capture is an optional feature that records raw game protocol traffic. Enable it to capture packets from joining players for protocol analysis.", description: "Capture widget: explanation under the not-enabled heading of what packet capture does, recording raw game protocol traffic for protocol analysis once enabled" })}</p>
            {enableMut.error && <ErrorBanner err={enableMut.error} />}
            {capabilityMessage && <p role="status" className="text-sm text-muted">{capabilityMessage}</p>}
            <Button
              variant="primary"
              onPress={() => enableMut.mutate()}
              isDisabled={!canStartCapture || enableMut.isPending}
            >
              {enableMut.isPending ? intl.formatMessage({ id: "web.components.capture_widget.enabling", defaultMessage: "Enabling…", description: "Capture widget: enable button label while the request is pending" }) : intl.formatMessage({ id: "web.components.capture_widget.enable_capture", defaultMessage: "Enable Capture", description: "Capture widget: enable capture button label" })}
            </Button>
          </CardContent>
        </Card>
      </div>
    );
  }

  const items = captures?.captures ?? [];
  const completed = items.filter((c) => c !== activeCapture);

  return (
    <div className="space-y-4 p-6">
      {capabilityMessage && <p role="status" className="text-sm text-muted">{capabilityMessage}</p>}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <Chip
            color={activeCapture ? "warning" : "success"}
            size="sm"
          >
            {activeCapture ? intl.formatMessage({ id: "web.components.capture_widget.capturing", defaultMessage: "Capturing…", description: "Capture widget: status while a capture is running" }) : intl.formatMessage({ id: "web.components.capture_widget.ready", defaultMessage: "Ready", description: "Capture widget: status when no capture is running" })}
          </Chip>
          <span className="text-xs text-default-500">
            {activeCapture
              ? intl.formatMessage({ id: "web.components.capture_widget.capture_started_ago", defaultMessage: "Capture started {duration} ago", description: "Capture widget: how long ago the active capture started; {duration} is a formatted duration" }, { duration: formatDuration(elapsedSeconds(activeCapture.startedAt || activeCapture.createdAt)) })
              : intl.formatMessage({ id: "web.components.capture_widget.auto_delete_after", defaultMessage: "Captures will auto-delete after {hours, plural, one {# hour} other {# hours}}", description: "Capture widget: retention window before captures auto-delete; {hours} is the retention in hours" }, { hours: retentionHours })}
          </span>
        </div>
        {!activeCapture && (
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onPress={() => disableMut.mutate()}
              isDisabled={!canManage || disableMut.isPending}
            >{intl.formatMessage({ id: "web.components.capture_widget.disable_capture", defaultMessage: "Disable Capture", description: "Capture widget: button that turns capture off for this server; keep it short" })}</Button>
            <Button
              variant="primary"
              size="sm"
              isDisabled={!canStartCapture} onPress={() => setShowStartModal(true)}
            >{intl.formatMessage({ id: "web.components.capture_widget.start_capture", defaultMessage: "Start Capture", description: "Capture widget: label of the button that starts a capture" })}</Button>
          </div>
        )}
      </div>

      {disableMut.error && <ErrorBanner err={disableMut.error} />}

      {!bannerDismissed && (
        <CaptureWarningBanner
          retentionHours={retentionHours}
          onDismiss={() => setBannerDismissed(true)}
        />
      )}

      {activeCapture ? (
        <Card>
          <CardContent className="space-y-4 p-4">
            <div className="flex items-start justify-between gap-3">
              <div className="space-y-1">
                <div className="font-mono text-sm">{activeCapture.captureId}</div>
                <div className="text-xs text-default-500">{intl.formatMessage({ id: "web.components.capture_widget.filter", defaultMessage: "Filter:", description: "Capture widget: label before the packet filter text of the active capture card, or the word 'default' when no filter is set" })} {activeCapture.filter || intl.formatMessage({ id: "web.components.capture_widget.default_filter", defaultMessage: "default", description: "Capture widget and table: shown when a capture has no packet filter, lowercase" })}</div>
              </div>
              <Button
                size="sm"
                variant="outline"
                onPress={() => stopMut.mutate(activeCapture.captureId)}
                isDisabled={!canManage || stopMut.isPending}
              >{intl.formatMessage({ id: "web.components.capture_widget.stop_capture", defaultMessage: "Stop Capture", description: "Capture widget: button that stops the running capture; keep it short" })}</Button>
            </div>
            {stopMut.error && <ErrorBanner err={stopMut.error} />}
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <label className="text-xs font-medium">{intl.formatMessage({ id: "web.components.capture_widget.max_duration", defaultMessage: "Max duration", description: "Capture widget: label above the time-remaining value on the active capture card, showing the run-time limit" })}</label>
                <div className="space-y-1">
                  <div className="h-2 overflow-hidden rounded-full bg-default-200">
                    <div
                      className="h-full bg-warning transition-all"
                      style={{
                        width: `${
                          activeCaptureDetails?.maxDurationSeconds
                            ? (elapsedSeconds(activeCaptureDetails.startedAt || activeCaptureDetails.createdAt) / activeCaptureDetails.maxDurationSeconds) * 100
                            : 0
                        }%`,
                      }}
                    />
                  </div>
                  <p className="text-xs text-default-500">
                    {activeCaptureDetails?.maxDurationSeconds
                      ? intl.formatMessage({ id: "web.components.capture_widget.time_remaining", defaultMessage: "Time remaining: ~{duration}", description: "Capture widget: time left before the capture reaches its max duration; {duration} is a formatted duration" }, { duration: formatDuration(Math.max(0, activeCaptureDetails.maxDurationSeconds - elapsedSeconds(activeCaptureDetails.startedAt || activeCaptureDetails.createdAt))) })
                      : intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
                  </p>
                </div>
              </div>
              <div className="space-y-2">
                <label className="text-xs font-medium">{intl.formatMessage({ id: "web.components.capture_widget.max_size", defaultMessage: "Max size", description: "Capture widget: label above the space-remaining value on the active capture card, showing the file size limit" })}</label>
                <div className="space-y-1">
                  <div className="h-2 overflow-hidden rounded-full bg-default-200">
                    <div
                      className="h-full bg-violet-500 transition-all"
                      style={{
                        width: `${activeCaptureDetails?.maxSizeBytes ? (activeCaptureDetails.bytesWritten / activeCaptureDetails.maxSizeBytes) * 100 : 0}%`,
                      }}
                    />
                  </div>
                  <p className="text-xs text-default-500">
                    {activeCaptureDetails?.maxSizeBytes
                      ? intl.formatMessage({ id: "web.components.capture_widget.space_remaining", defaultMessage: "Space remaining: ~{size}", description: "Capture widget: space left before a capture reaches its size cap; {size} is a formatted size" }, { size: formatBytes(Math.max(0, activeCaptureDetails.maxSizeBytes - activeCaptureDetails.bytesWritten)) })
                      : intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
                  </p>
                </div>
              </div>
            </div>
            <div className="text-xs text-default-500">
              {fmt.number(activeCaptureDetails?.packetsWritten ?? activeCapture.packetsWritten)} {intl.formatMessage({ id: "web.components.capture_widget.packets_captured", defaultMessage: "packets captured", description: "Capture widget: unit word shown right after the packet count of the active capture; the number before it is the packets written so far" })}</div>
          </CardContent>
        </Card>
      ) : items.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-2 p-10 text-center">
            <Inbox className="h-8 w-8 text-default-500" />
            <p className="text-sm text-default-500">{intl.formatMessage({ id: "web.components.capture_widget.no_captures_yet", defaultMessage: "No captures yet.", description: "Capture widget: empty-state text in the captures list shown when this server has no captures yet" })}</p>
          </CardContent>
        </Card>
      ) : (
        <section className="space-y-3">
          <h2 className="text-sm text-default-500 font-medium">{intl.formatMessage({ id: "web.components.capture_widget.captures", defaultMessage: "Captures", description: "Capture widget: section heading above the table of completed and failed captures for this server" })}</h2>
          {fileMut.error && <ErrorBanner err={fileMut.error} />}
          <div className="overflow-x-auto rounded-lg border border-default-200">
            <Table.Root className="w-full">
              <Table.ScrollContainer>
                <Table.Content
                  aria-label={intl.formatMessage({ id: "web.components.capture_widget.completed_and_failed_packet_captures", defaultMessage: "Completed and failed packet captures for this server", description: "Capture widget: accessible table name read by screen readers for the list of this server's completed and failed packet captures; not visible on screen" })}
                  keyboardNavigationBehavior="arrow"
                >
                  <TableHeader>
                    <TableColumn isRowHeader>{intl.formatMessage({ id: "web.components.capture_widget.id", defaultMessage: "ID", description: "Capture widget: column heading for the capture ID in the captures table; keep it short" })}</TableColumn>
                    <TableColumn>{intl.formatMessage({ id: "web.components.capture_widget.status", defaultMessage: "Status", description: "Capture widget: column heading for the state of each capture in the captures table; keep it short" })}</TableColumn>
                    <TableColumn>{intl.formatMessage({ id: "web.components.capture_widget.size", defaultMessage: "Size", description: "Capture widget: column heading for the file size of each capture in the captures table; keep it short" })}</TableColumn>
                    <TableColumn>{intl.formatMessage({ id: "web.components.capture_widget.packets", defaultMessage: "Packets", description: "Capture widget: column heading for the number of packets recorded by each capture in the captures table; keep it short" })}</TableColumn>
                    <TableColumn>{intl.formatMessage({ id: "web.components.capture_widget.duration", defaultMessage: "Duration", description: "Capture widget: column heading for how long each capture ran in the captures table; keep it short" })}</TableColumn>
                    <TableColumn>{intl.formatMessage({ id: "web.components.capture_widget.completed_at", defaultMessage: "Completed at", description: "Capture widget: column heading for when each capture finished in the captures table; keep it short" })}</TableColumn>
                    <TableColumn>{intl.formatMessage({ id: "web.components.capture_widget.expires_in", defaultMessage: "Expires in", description: "Capture widget: column heading for the time left before each capture is auto-deleted; keep it short" })}</TableColumn>
                    <TableColumn>{intl.formatMessage({ id: "web.components.capture_widget.filter_2", defaultMessage: "Filter", description: "Capture widget: column heading for the packet filter used by each capture in the captures table; keep it short" })}</TableColumn>
                    <TableColumn>{intl.formatMessage({ id: "web.components.capture_widget.actions", defaultMessage: "Actions", description: "Capture widget: column heading for the per-capture row actions in the captures table; keep it short" })}</TableColumn>
                  </TableHeader>
                  <TableBody>
                    {completed.flatMap((c) => {
                      const expiry = c.expiresAt ? expiryLabel(c.expiresAt, intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })) : null;
                      const downloadable = c.phase === "Completed";
                      return [
                        <TableRow key={c.captureId}>
                          <TableCell className="font-mono text-sm">{c.captureId}</TableCell>
                          <TableCell>
                            <Chip
                              color={phaseColorMap[c.phase] ?? "default"}
                              size="sm"
                            >
                              {phaseLabel(intl, c.phase)}
                            </Chip>
                          </TableCell>
                          <TableCell className="font-mono text-sm">{formatBytes(c.bytesWritten)}</TableCell>
                          <TableCell className="font-mono text-sm">{fmt.number(c.packetsWritten)}</TableCell>
                          <TableCell className="font-mono text-sm text-default-500">
                            {durationBetween(c.startedAt, c.completedAt, intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" }))}
                          </TableCell>
                          <TableCell className="font-mono text-sm text-default-500">
                            {formatRelative(c.completedAt || undefined, intl)}
                          </TableCell>
                          <TableCell>
                            {expiry && (
                              <Chip color={expiry.color} size="sm">
                                {expiry.text}
                              </Chip>
                            )}
                          </TableCell>
                          <TableCell className="font-mono text-sm text-default-500">{c.filter || intl.formatMessage({ id: "web.components.capture_widget.default_filter", defaultMessage: "default", description: "Capture widget and table: shown when a capture has no packet filter, lowercase" })}</TableCell>
                          <TableCell>
                            <div className="flex justify-end gap-1">
                              <Button
                                isIconOnly
                                size="sm"
                                variant="ghost"
                                aria-label={intl.formatMessage({ id: "web.components.capture_widget.download_capture_aria", defaultMessage: "Download capture {id}", description: "Capture table: aria-label of the download button; {id} is the capture id" }, { id: c.captureId })}
                                isDisabled={!canDownload || !downloadable || fileMut.isPending}
                                onPress={() => fileMut.mutate(c.captureId)}
                              >
                                <Download className="h-4 w-4" />
                              </Button>
                              <Button
                                isIconOnly
                                size="sm"
                                variant="ghost"
                                aria-label={intl.formatMessage({ id: "web.components.capture_widget.view_capture_aria", defaultMessage: "View capture {id}", description: "Capture table: aria-label of the view button; {id} is the capture id" }, { id: c.captureId })}
                                onPress={() => setExpandedId(expandedId === c.captureId ? null : c.captureId)}
                                aria-expanded={expandedId === c.captureId}
                              >
                                <Eye className="h-4 w-4" />
                              </Button>
                              <Button
                                isIconOnly
                                size="sm"
                                variant="ghost"
                                aria-label={intl.formatMessage({ id: "web.components.capture_widget.delete_capture_aria", defaultMessage: "Delete capture {id}", description: "Capture table: aria-label of the delete button; {id} is the capture id" }, { id: c.captureId })}
                                className="text-danger"
                                isDisabled={!canManage} onPress={() => setDeleteTarget(c)}
                              >
                                <Trash2 className="h-4 w-4" />
                              </Button>
                            </div>
                          </TableCell>
                        </TableRow>,
                        expandedId === c.captureId && (
                          <TableRow key={`${c.captureId}-detail`}>
                            <TableCell colSpan={9} className="bg-default-50 px-4 py-3 text-xs text-default-500">
                              <dl className="grid grid-cols-2 gap-x-6 gap-y-1 sm:grid-cols-4">
                                <dt className="font-medium">{intl.formatMessage({ id: "web.components.capture_widget.created", defaultMessage: "Created", description: "Capture widget: label in the expanded details of a capture row, followed by when the capture was created" })}</dt>
                                <dd>{formatRelative(c.createdAt, intl)}</dd>
                                <dt className="font-medium">{intl.formatMessage({ id: "web.components.capture_widget.started", defaultMessage: "Started", description: "Capture widget: label in the expanded details of a capture row, followed by when recording started or a dash if it has not started" })}</dt>
                                <dd>{c.startedAt ? formatRelative(c.startedAt, intl) : intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</dd>
                                <dt className="font-medium">{intl.formatMessage({ id: "web.components.capture_widget.server", defaultMessage: "Server", description: "Capture widget: label in the expanded details of a capture row, followed by the name of the server the capture belongs to" })}</dt>
                                <dd className="font-mono">{c.serverName}</dd>
                              </dl>
                            </TableCell>
                          </TableRow>
                        ),
                      ];
                    })}
                  </TableBody>
                </Table.Content>
              </Table.ScrollContainer>
            </Table.Root>
          </div>
          <p className="pt-2 text-xs text-default-500">{intl.formatMessage({ id: "web.components.capture_widget.showing_length_of", defaultMessage: "Showing {length} of {total} {total, plural, one {capture} other {captures}}", description: "Capture widget: count line under the capture table; {length} is the rows shown, {total} is the total capture count" }, { length: completed.length, total: captures?.total ?? items.length })}
          </p>
        </section>
      )}

      {showStartModal && <StartCaptureModal
        open={showStartModal}
        available={canStartCapture}
        limits={captureLimits}
        retentionSeconds={retentionSeconds}
        onClose={() => setShowStartModal(false)}
        onStart={(body) => Captures.start(name, body, ns)}
        onStarted={() => {
          setShowStartModal(false);
          void qc.invalidateQueries({ queryKey: resourceKey(resourceTarget, "captures", name, ns) });
        }}
      />}

      <AlertDialog
        isOpen={deleteTarget !== null}
        onOpenChange={(o) => {
          if (!o) setDeleteTarget(null);
        }}
      >
        <AlertDialogBackdrop>
          <AlertDialogContainer>
            <AlertDialogDialog className="max-w-md">
              <AlertDialogHeader className="flex flex-col gap-1">
                <AlertDialogHeading>{intl.formatMessage({ id: "web.components.capture_widget.delete_capture", defaultMessage: "Delete capture?", description: "Capture widget: title of the confirmation dialog that asks whether the selected capture should be deleted" })}</AlertDialogHeading>
              </AlertDialogHeader>
              <AlertDialogBody className="flex flex-col gap-3">
                <p className="text-sm">{intl.formatMessage({ id: "web.components.capture_widget.delete_warning", defaultMessage: "This permanently deletes capture <b>{captureId}</b> and its recorded packets. This cannot be undone.", description: "Capture widget: warning in the delete confirmation dialog that the capture and its recorded packets are permanently removed and the action cannot be undone; {captureId} is the capture ID, shown in monospace inside the <b> tag, which must be kept around it" }, { captureId: deleteTarget?.captureId, b: (chunks) => <span className="font-mono">{chunks}</span> })}</p>
                {deleteMut.error && <ErrorBanner err={deleteMut.error} />}
              </AlertDialogBody>
              <AlertDialogFooter>
                <Button
                  variant="ghost"
                  onPress={() => setDeleteTarget(null)}
                >{intl.formatMessage({ id: "web.components.capture_widget.cancel", defaultMessage: "Cancel", description: "Capture widget: button that closes the delete confirmation or the Start Capture dialog without making changes; keep it short" })}</Button>
                <Button
                  variant="danger"
                  isDisabled={!canManage || deleteMut.isPending}
                  onPress={() => deleteTarget && deleteMut.mutate(deleteTarget.captureId)}
                >
                  {deleteMut.isPending ? intl.formatMessage({ id: "web.components.capture_widget.working", defaultMessage: "Working…", description: "Capture widget: delete confirmation button label while the request is pending" }) : intl.formatMessage({ id: "web.components.capture_widget.delete_capture_button", defaultMessage: "Delete capture", description: "Capture widget: delete button label in the delete confirmation" })}
                </Button>
              </AlertDialogFooter>
            </AlertDialogDialog>
          </AlertDialogContainer>
        </AlertDialogBackdrop>
      </AlertDialog>
    </div>
  );
}

// Isolated so the (frequent, per-keystroke) form state doesn't re-render
// the whole widget, and so the invalid-filter error state — this file's
// other design-mandated constraint alongside the dual progress bars — is
// scoped to just the field that produced it.
function StartCaptureModal({
  open,
  available,
  limits,
  retentionSeconds,
  onClose,
  onStart,
  onStarted,
}: {
  open: boolean;
  available: boolean;
  limits: CaptureCapabilities;
  retentionSeconds: number;
  onClose: () => void;
  onStart: (body: CaptureStartBody) => Promise<NetworkCapture>;
  onStarted: () => void;
}) {
  const intl = useOptionalIntl();
  const permissions = useResourcePermissions();
  const canManage = resourceCan(permissions, "captures:manage");
  const [filter, setFilter] = useState("");
  const [durationValue, setDurationValue] = useState(limits.defaultMaxDurationSeconds);
  const [durationUnit, setDurationUnit] = useState<"seconds" | "minutes">("seconds");
  const [sizeValue, setSizeValue] = useState(limits.defaultMaxSizeBytes / (1024 * 1024));
  const [sizeUnit, setSizeUnit] = useState<"MB" | "GB">("MB");
  const [retentionValue, setRetentionValue] = useState(retentionSeconds / 3600);
  const [retentionUnit, setRetentionUnit] = useState<"hours" | "days">("hours");
  const durationSeconds = durationUnit === "minutes" ? durationValue * 60 : durationValue;
  const sizeMultiplier = sizeUnit === "GB" ? 1024 * 1024 * 1024 : 1024 * 1024;
  const sizeBytes = Math.round(sizeValue * sizeMultiplier);
  const retentionMultiplier = retentionUnit === "days" ? 86400 : 3600;
  const retentionTTL = Math.round(retentionValue * retentionMultiplier);
  const validLimits = Number.isSafeInteger(durationSeconds) && durationSeconds >= 1 && durationSeconds <= 3600 &&
    Number.isSafeInteger(sizeBytes) && sizeBytes >= 1 && sizeBytes <= limits.defaultMaxSizeBytes &&
    Number.isSafeInteger(retentionTTL) && retentionTTL >= 60 && retentionTTL <= limits.maxRetentionSeconds;

  const start = useMutation({
    mutationFn: () =>
      onStart({
        filter: filter.trim() || undefined,
        maxDurationSeconds: durationSeconds,
        maxSizeBytes: sizeBytes,
        ttlSecondsAfterFinished: retentionTTL,
      }),
    onSuccess: onStarted,
  });

  // The design's invalid-filter state (red border, red X, inline error,
  // greyed Start button) is driven by the API's filter-validation error
  // (FR-003) rather than client-side BPF parsing — pcap-filter syntax isn't
  // re-implemented in the browser. Any edit to the filter clears the stale
  // error so the field doesn't keep flagging text the user has since fixed.
  const filterError =
    start.error instanceof APIError && /filter/i.test(start.error.body) ? start.error.body : null;

  function handleFilterChange(v: string) {
    setFilter(v);
    if (start.isError) start.reset();
  }

  return (
    <Modal isOpen={open} onOpenChange={onClose}>
      <ModalBackdrop>
        <ModalContainer>
          <ModalDialog>
            <ModalHeader className="flex flex-col gap-1">
              <ModalHeading>{intl.formatMessage({ id: "web.components.capture_widget.start_capture", defaultMessage: "Start Capture", description: "Capture widget: label of the button that starts a capture" })}</ModalHeading>
            </ModalHeader>
            <ModalBody>
              <p className="text-sm text-default-500">{intl.formatMessage({ id: "web.components.capture_widget.records_raw_network_traffic_on_this", defaultMessage: "Records raw network traffic on this server’s advertised ports (or a custom filter) for later download.", description: "Capture widget: sentence under the Start Capture dialog heading saying the capture records raw network traffic on the server's ports or a custom filter for later download" })}</p>
  
              <form
                className="space-y-4"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (available && canManage && validLimits && !start.isPending && !filterError) start.mutate();
                }}
              >
                <div className="space-y-2">
                  <Label htmlFor="packet-filter">{intl.formatMessage({ id: "web.components.capture_widget.packet_filter", defaultMessage: "Packet Filter", description: "Capture widget: label for the optional packet filter text field in the Start Capture dialog" })}</Label>
                  <div className="relative">
                    <Input
                      id="packet-filter"
                      aria-label={intl.formatMessage({ id: "web.components.capture_widget.packet_filter", defaultMessage: "Packet Filter", description: "Capture widget: label for the optional packet filter text field in the Start Capture dialog" })}
                      value={filter}
                      onChange={(e) => handleFilterChange(e.target.value)}
                      placeholder={intl.formatMessage({ id: "web.components.capture_widget.tcp_port_25565", defaultMessage: "tcp port 25565", description: "Capture widget: example text shown inside the empty packet filter field in the Start Capture dialog; it is a sample filter, not a default value" })}
                      spellCheck={false}
                      aria-invalid={!!filterError}
                    />
                    {filter && (
                      <div className="absolute right-3 top-1/2 -translate-y-1/2">
                        {filterError ? (
                          <CircleX className="h-[18px] w-[18px] shrink-0 text-danger" aria-hidden="true" />
                        ) : (
                          <CircleCheckBig className="h-[18px] w-[18px] shrink-0 text-success" aria-hidden="true" />
                        )}
                      </div>
                    )}
                  </div>
                  <Description>{intl.formatMessage({ id: "web.components.capture_widget.optional_use_pcap_filter_syntax_e_g_tcp", defaultMessage: "Optional. Use pcap-filter syntax (e.g. \"tcp port 8080\", \"host 192.168.1.5\"). Leave blank to capture only on this server’s game ports.", description: "Capture widget: hint under the packet filter field saying the filter is optional, uses pcap-filter syntax with example expressions, and that blank captures only the server's game ports" })}</Description>
                  {filterError && (
                    <p role="alert" className="text-xs text-danger">
                      {filterError}
                    </p>
                  )}
                </div>
  
                <div className="space-y-2">
                  <Label htmlFor="duration-value">{intl.formatMessage({ id: "web.components.capture_widget.max_duration_2", defaultMessage: "Max Duration *", description: "Capture widget: label of the run-time field in the Start Capture dialog; the asterisk marks the field as required" })}</Label>
                  <div className="flex items-center gap-2">
                    <Input
                      id="duration-value"
                      type="number"
                      min={1}
                      max={durationUnit === "minutes" ? 60 : 3600}
                      value={String(durationValue)}
                      onChange={(e) => setDurationValue(Number(e.target.value))}
                      className="w-[140px]"
                      aria-label={intl.formatMessage({ id: "web.components.capture_widget.max_duration_value", defaultMessage: "Max duration value", description: "Capture widget: screen-reader label for the number box that sets how long the capture runs; not visible on screen" })}
                      required
                    />
                    <Select
                      value={durationUnit}
                      onChange={(v) => setDurationUnit(String(v) as "seconds" | "minutes")}
                      className="w-[140px]"
                    >
                      <Select.Trigger className="rounded border border-default-200 bg-default-50 px-3 py-2 text-sm" aria-label={intl.formatMessage({ id: "web.components.capture_widget.max_duration_unit", defaultMessage: "Max duration unit", description: "Capture widget: screen-reader label for the unit dropdown (seconds or minutes) next to the run-time number; not visible on screen" })}>
                        <Select.Value />
                        <Select.Indicator className="ml-auto h-4 w-4" />
                      </Select.Trigger>
                      <Select.Popover className="rounded border border-default-200">
                        <ListBox>
                          <ListBoxItem key="seconds" id="seconds">{intl.formatMessage({ id: "web.components.capture_widget.seconds", defaultMessage: "seconds", description: "Capture widget: option in the run-time unit dropdown meaning seconds; keep it short" })}</ListBoxItem>
                          <ListBoxItem key="minutes" id="minutes">{intl.formatMessage({ id: "web.components.capture_widget.minutes", defaultMessage: "minutes", description: "Capture widget: option in the run-time unit dropdown meaning minutes; keep it short" })}</ListBoxItem>
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </div>
                  <Description>{intl.formatMessage({ id: "web.components.capture_widget.how_long_the_capture_runs_before_auto", defaultMessage: "How long the capture runs before auto-stopping. Range 1–3600 seconds.", description: "Capture widget: hint under the run-time field saying how long the capture runs before it stops automatically; the allowed range is 1 to 3600 seconds" })}</Description>
                </div>
  
                <div className="space-y-2">
                  <Label htmlFor="size-value">{intl.formatMessage({ id: "web.components.capture_widget.max_size_2", defaultMessage: "Max Size *", description: "Capture widget: label of the maximum file size field in the Start Capture dialog; the asterisk marks the field as required" })}</Label>
                  <div className="flex items-center gap-2">
                    <Input
                      id="size-value"
                      type="number"
                      min={1 / sizeMultiplier}
                      max={limits.defaultMaxSizeBytes / sizeMultiplier}
                      step="any"
                      value={String(sizeValue)}
                      onChange={(e) => setSizeValue(Number(e.target.value))}
                      className="w-[140px]"
                      aria-label={intl.formatMessage({ id: "web.components.capture_widget.max_size_value", defaultMessage: "Max size value", description: "Capture widget: screen-reader label for the number box that sets the maximum capture file size; not visible on screen" })}
                      required
                    />
                    <Select
                      value={sizeUnit}
                      onChange={(v) => setSizeUnit(String(v) as "MB" | "GB")}
                      className="w-[140px]"
                    >
                      <Select.Trigger className="rounded border border-default-200 bg-default-50 px-3 py-2 text-sm" aria-label={intl.formatMessage({ id: "web.components.capture_widget.max_size_unit", defaultMessage: "Max size unit", description: "Capture widget: screen-reader label for the size unit dropdown (MB or GB) next to the maximum size number; not visible on screen" })}>
                        <Select.Value />
                        <Select.Indicator className="ml-auto h-4 w-4" />
                      </Select.Trigger>
                      <Select.Popover className="rounded border border-default-200">
                        <ListBox>
                          <ListBoxItem key="MB" id="MB">{intl.formatMessage({ id: "web.components.capture_widget.mb", defaultMessage: "MB", description: "Capture widget: option in the size unit dropdown meaning megabytes; keep it short" })}</ListBoxItem>
                          <ListBoxItem key="GB" id="GB">{intl.formatMessage({ id: "web.components.capture_widget.gb", defaultMessage: "GB", description: "Capture widget: option in the size unit dropdown meaning gigabytes; keep it short" })}</ListBoxItem>
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </div>
                  <Description>{intl.formatMessage({ id: "web.components.capture_widget.maximum_file_size", defaultMessage: "Maximum file size:", description: "Capture widget: first part of the hint under the maximum size field, followed by the server's size limit formatted with a unit" })} {formatBytes(limits.defaultMaxSizeBytes)}{intl.formatMessage({ id: "web.components.capture_widget.capture_stops_when_reached", defaultMessage: ". Capture stops when reached.", description: "Capture widget: second part of the maximum size hint, after the size limit; says the capture stops once that size is reached" })}</Description>
                </div>
  
                <div className="space-y-2">
                  <Label htmlFor="retention-value">{intl.formatMessage({ id: "web.components.capture_widget.retention", defaultMessage: "Retention", description: "Capture widget: label of the retention field in the Start Capture dialog, which sets how long the capture is kept before auto-delete" })}</Label>
                  <div className="flex items-center gap-2">
                    <Input
                      id="retention-value"
                      type="number"
                      min={60 / retentionMultiplier}
                      max={limits.maxRetentionSeconds / retentionMultiplier}
                      step="any"
                      value={String(retentionValue)}
                      onChange={(e) => setRetentionValue(Number(e.target.value))}
                      className="w-[140px]"
                      aria-label={intl.formatMessage({ id: "web.components.capture_widget.retention_value", defaultMessage: "Retention value", description: "Capture widget: screen-reader label for the number box that sets how long the capture is kept; not visible on screen" })}
                    />
                    <Select
                      value={retentionUnit}
                      onChange={(v) => setRetentionUnit(String(v) as "hours" | "days")}
                      className="w-[140px]"
                    >
                      <Select.Trigger className="rounded border border-default-200 bg-default-50 px-3 py-2 text-sm" aria-label={intl.formatMessage({ id: "web.components.capture_widget.retention_unit", defaultMessage: "Retention unit", description: "Capture widget: screen-reader label for the retention unit dropdown (hours or days) next to the retention number; not visible on screen" })}>
                        <Select.Value />
                        <Select.Indicator className="ml-auto h-4 w-4" />
                      </Select.Trigger>
                      <Select.Popover className="rounded border border-default-200">
                        <ListBox>
                          <ListBoxItem key="hours" id="hours">{intl.formatMessage({ id: "web.components.capture_widget.hours", defaultMessage: "hours", description: "Capture widget: retention unit dropdown option meaning hours; keep it short" })}</ListBoxItem>
                          <ListBoxItem key="days" id="days">{intl.formatMessage({ id: "web.components.capture_widget.days", defaultMessage: "days", description: "Capture widget: retention unit dropdown option meaning days; keep it short" })}</ListBoxItem>
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </div>
                  <Description>{intl.formatMessage({ id: "web.components.capture_widget.how_long_the_capture_is_retained_before", defaultMessage: "How long the capture is retained before auto-delete. Maximum: {duration}.", description: "Capture widget: retention help text; {duration} is the maximum retention" }, { duration: formatDuration(limits.maxRetentionSeconds) })}
                  </Description>
                </div>
  
                {start.error && !filterError && <ErrorBanner err={start.error} />}
              </form>
            </ModalBody>
            <ModalFooter>
              <Button variant="ghost" onPress={onClose} isDisabled={start.isPending}>{intl.formatMessage({ id: "web.components.capture_widget.cancel", defaultMessage: "Cancel", description: "Capture widget: button that closes the delete confirmation or the Start Capture dialog without making changes; keep it short" })}</Button>
              <Button
                variant="primary"
                isPending={start.isPending}
                isDisabled={!available || !canManage || start.isPending || !!filterError || !validLimits}
                onPress={() => start.mutate()}
              >
                {start.isPending ? intl.formatMessage({ id: "web.components.capture_widget.starting", defaultMessage: "Starting…", description: "Capture widget: start button label while the request is pending" }) : intl.formatMessage({ id: "web.components.capture_widget.start_capture", defaultMessage: "Start Capture", description: "Capture widget: label of the button that starts a capture" })}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
