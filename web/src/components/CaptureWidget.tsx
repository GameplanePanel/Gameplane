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
import type { GameServer, NetworkCapture } from "@/types";

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

function durationBetween(startedAt: string, completedAt: string): string {
  if (!startedAt || !completedAt) return "—";
  const start = new Date(startedAt).getTime();
  const end = new Date(completedAt).getTime();
  if (Number.isNaN(start) || Number.isNaN(end)) return "—";
  return formatDuration((end - start) / 1000);
}

// Expiry badges recolor as the retention window closes in, so an admin
// scanning the table can tell "plenty of time" from "about to be
// GC'd" at a glance (see assets/design-export/json/m5kOm4.json's per-row colors).
function expiryLabel(expiresAt: string): { text: string; color: "default" | "warning" | "danger" } {
  const t = new Date(expiresAt).getTime();
  if (Number.isNaN(t)) return { text: "—", color: "default" };
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

interface Props {
  name: string;
  ns?: string;
  gs?: GameServer;
}

export function CaptureWidget({ name, ns, gs }: Props) {
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
    : capabilities.isPending ? "Checking capture support…"
    : capabilities.isError || capabilities.data?.state === "unavailable" ? "Capture access is temporarily unavailable at this location."
    : capabilities.data?.state === "unsupported" ? "This location needs a gateway update before captures can be managed here."
    : !capabilities.data?.enabled ? "New captures are disabled at this location. Existing capture files remain available."
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
            <p className="text-sm text-default-500">Checking access…</p>
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
            <p className="text-sm font-medium">{"You don't have access to packet capture on this server."}</p>
            <p className="max-w-md text-xs text-default-500">
              Viewing, starting and downloading captures requires capture access for this server. Ask an administrator if you need it.
            </p>
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
            <p className="text-sm font-medium">Capture is not enabled on this server.</p>
            <p className="max-w-md text-xs text-default-500">
              Network packet capture is an optional feature that records raw game protocol
              traffic. Enable it to capture packets from joining players for protocol analysis.
            </p>
            {enableMut.error && <ErrorBanner err={enableMut.error} />}
            {capabilityMessage && <p role="status" className="text-sm text-muted">{capabilityMessage}</p>}
            <Button
              variant="primary"
              onPress={() => enableMut.mutate()}
              isDisabled={!canStartCapture || enableMut.isPending}
            >
              {enableMut.isPending ? "Enabling…" : "Enable Capture"}
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
            {activeCapture ? "Capturing…" : "Ready"}
          </Chip>
          <span className="text-xs text-default-500">
            {activeCapture
              ? `Capture started ${formatDuration(elapsedSeconds(activeCapture.startedAt || activeCapture.createdAt))} ago`
              : `Captures will auto-delete after ${retentionHours} hour${retentionHours === 1 ? "" : "s"}`}
          </span>
        </div>
        {!activeCapture && (
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onPress={() => disableMut.mutate()}
              isDisabled={!canManage || disableMut.isPending}
            >
              Disable Capture
            </Button>
            <Button
              variant="primary"
              size="sm"
              isDisabled={!canStartCapture} onPress={() => setShowStartModal(true)}
            >
              Start Capture
            </Button>
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
                <div className="text-xs text-default-500">Filter: {activeCapture.filter || "default"}</div>
              </div>
              <Button
                size="sm"
                variant="outline"
                onPress={() => stopMut.mutate(activeCapture.captureId)}
                isDisabled={!canManage || stopMut.isPending}
              >
                Stop Capture
              </Button>
            </div>
            {stopMut.error && <ErrorBanner err={stopMut.error} />}
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <label className="text-xs font-medium">Max duration</label>
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
                      ? `Time remaining: ~${formatDuration(
                          Math.max(0, activeCaptureDetails.maxDurationSeconds - elapsedSeconds(activeCaptureDetails.startedAt || activeCaptureDetails.createdAt)),
                        )}`
                      : "—"}
                  </p>
                </div>
              </div>
              <div className="space-y-2">
                <label className="text-xs font-medium">Max size</label>
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
                      ? `Space remaining: ~${formatBytes(Math.max(0, activeCaptureDetails.maxSizeBytes - activeCaptureDetails.bytesWritten))}`
                      : "—"}
                  </p>
                </div>
              </div>
            </div>
            <div className="text-xs text-default-500">
              {(activeCaptureDetails?.packetsWritten ?? activeCapture.packetsWritten).toLocaleString()} packets captured
            </div>
          </CardContent>
        </Card>
      ) : items.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-center gap-2 p-10 text-center">
            <Inbox className="h-8 w-8 text-default-500" />
            <p className="text-sm text-default-500">No captures yet.</p>
          </CardContent>
        </Card>
      ) : (
        <section className="space-y-3">
          <h2 className="text-sm text-default-500 font-medium">Captures</h2>
          {fileMut.error && <ErrorBanner err={fileMut.error} />}
          <div className="overflow-x-auto rounded-lg border border-default-200">
            <Table.Root className="w-full">
              <Table.ScrollContainer>
                <Table.Content
                  aria-label="Completed and failed packet captures for this server"
                  keyboardNavigationBehavior="arrow"
                >
                  <TableHeader>
                    <TableColumn isRowHeader>ID</TableColumn>
                    <TableColumn>Status</TableColumn>
                    <TableColumn>Size</TableColumn>
                    <TableColumn>Packets</TableColumn>
                    <TableColumn>Duration</TableColumn>
                    <TableColumn>Completed at</TableColumn>
                    <TableColumn>Expires in</TableColumn>
                    <TableColumn>Filter</TableColumn>
                    <TableColumn>Actions</TableColumn>
                  </TableHeader>
                  <TableBody>
                    {completed.flatMap((c) => {
                      const expiry = c.expiresAt ? expiryLabel(c.expiresAt) : null;
                      const downloadable = c.phase === "Completed";
                      return [
                        <TableRow key={c.captureId}>
                          <TableCell className="font-mono text-sm">{c.captureId}</TableCell>
                          <TableCell>
                            <Chip
                              color={phaseColorMap[c.phase] ?? "default"}
                              size="sm"
                            >
                              {c.phase}
                            </Chip>
                          </TableCell>
                          <TableCell className="font-mono text-sm">{formatBytes(c.bytesWritten)}</TableCell>
                          <TableCell className="font-mono text-sm">{c.packetsWritten.toLocaleString()}</TableCell>
                          <TableCell className="font-mono text-sm text-default-500">
                            {durationBetween(c.startedAt, c.completedAt)}
                          </TableCell>
                          <TableCell className="font-mono text-sm text-default-500">
                            {formatRelative(c.completedAt || undefined)}
                          </TableCell>
                          <TableCell>
                            {expiry && (
                              <Chip color={expiry.color} size="sm">
                                {expiry.text}
                              </Chip>
                            )}
                          </TableCell>
                          <TableCell className="font-mono text-sm text-default-500">{c.filter || "default"}</TableCell>
                          <TableCell>
                            <div className="flex justify-end gap-1">
                              <Button
                                isIconOnly
                                size="sm"
                                variant="ghost"
                                aria-label={`Download capture ${c.captureId}`}
                                isDisabled={!canDownload || !downloadable || fileMut.isPending}
                                onPress={() => fileMut.mutate(c.captureId)}
                              >
                                <Download className="h-4 w-4" />
                              </Button>
                              <Button
                                isIconOnly
                                size="sm"
                                variant="ghost"
                                aria-label={`View capture ${c.captureId}`}
                                onPress={() => setExpandedId(expandedId === c.captureId ? null : c.captureId)}
                                aria-expanded={expandedId === c.captureId}
                              >
                                <Eye className="h-4 w-4" />
                              </Button>
                              <Button
                                isIconOnly
                                size="sm"
                                variant="ghost"
                                aria-label={`Delete capture ${c.captureId}`}
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
                                <dt className="font-medium">Created</dt>
                                <dd>{formatRelative(c.createdAt)}</dd>
                                <dt className="font-medium">Started</dt>
                                <dd>{c.startedAt ? formatRelative(c.startedAt) : "—"}</dd>
                                <dt className="font-medium">Server</dt>
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
          <p className="pt-2 text-xs text-default-500">
            Showing {completed.length} of {captures?.total ?? items.length} capture{(captures?.total ?? items.length) === 1 ? "" : "s"}
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
                <AlertDialogHeading>Delete capture?</AlertDialogHeading>
              </AlertDialogHeader>
              <AlertDialogBody className="flex flex-col gap-3">
                <p className="text-sm">
                  This permanently deletes capture{" "}
                  <span className="font-mono">{deleteTarget?.captureId}</span> and its
                  recorded packets. This cannot be undone.
                </p>
                {deleteMut.error && <ErrorBanner err={deleteMut.error} />}
              </AlertDialogBody>
              <AlertDialogFooter>
                <Button
                  variant="ghost"
                  onPress={() => setDeleteTarget(null)}
                >
                  Cancel
                </Button>
                <Button
                  variant="danger"
                  isDisabled={!canManage || deleteMut.isPending}
                  onPress={() => deleteTarget && deleteMut.mutate(deleteTarget.captureId)}
                >
                  {deleteMut.isPending ? "Working…" : "Delete capture"}
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
              <ModalHeading>Start Capture</ModalHeading>
            </ModalHeader>
            <ModalBody>
              <p className="text-sm text-default-500">
                Records raw network traffic on this server&rsquo;s advertised ports (or a custom
                filter) for later download.
              </p>
  
              <form
                className="space-y-4"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (available && canManage && validLimits && !start.isPending && !filterError) start.mutate();
                }}
              >
                <div className="space-y-2">
                  <Label htmlFor="packet-filter">Packet Filter</Label>
                  <div className="relative">
                    <Input
                      id="packet-filter"
                      aria-label="Packet Filter"
                      value={filter}
                      onChange={(e) => handleFilterChange(e.target.value)}
                      placeholder="tcp port 25565"
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
                  <Description>
                    Optional. Use pcap-filter syntax (e.g. &quot;tcp port 8080&quot;, &quot;host
                    192.168.1.5&quot;). Leave blank to capture only on this server&rsquo;s game
                    ports.
                  </Description>
                  {filterError && (
                    <p role="alert" className="text-xs text-danger">
                      {filterError}
                    </p>
                  )}
                </div>
  
                <div className="space-y-2">
                  <Label htmlFor="duration-value">Max Duration *</Label>
                  <div className="flex items-center gap-2">
                    <Input
                      id="duration-value"
                      type="number"
                      min={1}
                      max={durationUnit === "minutes" ? 60 : 3600}
                      value={String(durationValue)}
                      onChange={(e) => setDurationValue(Number(e.target.value))}
                      className="w-[140px]"
                      aria-label="Max duration value"
                      required
                    />
                    <Select
                      value={durationUnit}
                      onChange={(v) => setDurationUnit(String(v) as "seconds" | "minutes")}
                      className="w-[140px]"
                    >
                      <Select.Trigger className="rounded border border-default-200 bg-default-50 px-3 py-2 text-sm" aria-label="Max duration unit">
                        <Select.Value />
                        <Select.Indicator className="ml-auto h-4 w-4" />
                      </Select.Trigger>
                      <Select.Popover className="rounded border border-default-200">
                        <ListBox>
                          <ListBoxItem key="seconds" id="seconds">seconds</ListBoxItem>
                          <ListBoxItem key="minutes" id="minutes">minutes</ListBoxItem>
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </div>
                  <Description>
                    How long the capture runs before auto-stopping. Range 1–3600 seconds.
                  </Description>
                </div>
  
                <div className="space-y-2">
                  <Label htmlFor="size-value">Max Size *</Label>
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
                      aria-label="Max size value"
                      required
                    />
                    <Select
                      value={sizeUnit}
                      onChange={(v) => setSizeUnit(String(v) as "MB" | "GB")}
                      className="w-[140px]"
                    >
                      <Select.Trigger className="rounded border border-default-200 bg-default-50 px-3 py-2 text-sm" aria-label="Max size unit">
                        <Select.Value />
                        <Select.Indicator className="ml-auto h-4 w-4" />
                      </Select.Trigger>
                      <Select.Popover className="rounded border border-default-200">
                        <ListBox>
                          <ListBoxItem key="MB" id="MB">MB</ListBoxItem>
                          <ListBoxItem key="GB" id="GB">GB</ListBoxItem>
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </div>
                  <Description>
                    Maximum file size: {formatBytes(limits.defaultMaxSizeBytes)}. Capture stops when reached.
                  </Description>
                </div>
  
                <div className="space-y-2">
                  <Label htmlFor="retention-value">Retention</Label>
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
                      aria-label="Retention value"
                    />
                    <Select
                      value={retentionUnit}
                      onChange={(v) => setRetentionUnit(String(v) as "hours" | "days")}
                      className="w-[140px]"
                    >
                      <Select.Trigger className="rounded border border-default-200 bg-default-50 px-3 py-2 text-sm" aria-label="Retention unit">
                        <Select.Value />
                        <Select.Indicator className="ml-auto h-4 w-4" />
                      </Select.Trigger>
                      <Select.Popover className="rounded border border-default-200">
                        <ListBox>
                          <ListBoxItem key="hours" id="hours">hours</ListBoxItem>
                          <ListBoxItem key="days" id="days">days</ListBoxItem>
                        </ListBox>
                      </Select.Popover>
                    </Select>
                  </div>
                  <Description>
                    How long the capture is retained before auto-delete. Maximum: {formatDuration(limits.maxRetentionSeconds)}.
                  </Description>
                </div>
  
                {start.error && !filterError && <ErrorBanner err={start.error} />}
              </form>
            </ModalBody>
            <ModalFooter>
              <Button variant="ghost" onPress={onClose} isDisabled={start.isPending}>
                Cancel
              </Button>
              <Button
                variant="primary"
                isPending={start.isPending}
                isDisabled={!available || !canManage || start.isPending || !!filterError || !validLimits}
                onPress={() => start.mutate()}
              >
                {start.isPending ? "Starting…" : "Start Capture"}
              </Button>
            </ModalFooter>
          </ModalDialog>
        </ModalContainer>
      </ModalBackdrop>
    </Modal>
  );
}
