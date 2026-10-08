import { useResourceClient, useResourceTarget, resourceKey } from "@/lib/resourceTarget";
import { useEffect, useState, useMemo, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Copy, Cpu, HardDrive, MemoryStick, AlertCircle } from "lucide-react";
import type { GameServer, GameTemplate, PlayersResp } from "@/types";

import { Card, CardHeader, CardContent, Alert } from "@heroui/react";
import { Sparkline } from "@/components/ui/Sparkline";
import { ServerActionsCard } from "@/components/server/ServerActionsCard";
import { ServerStatusCard } from "@/components/server/ServerStatusCard";
import { ServerSleepCard } from "@/components/server/ServerSleepCard";
import { EventList } from "@/components/server/EventList";
import { formatBytes } from "@/lib/utils";
import { mapServerEvent, type NormalizedServerEvent } from "@/lib/events";

export function OverviewTab({
  gs,
  name,
  tmpl,
  ns,
  onViewAllEvents,
  onOpenConsole,
}: {
  gs?: GameServer;
  name: string;
  tmpl?: GameTemplate;
  ns?: string;
  onViewAllEvents?: () => void;
  onOpenConsole?: () => void;
}) {
  const resourceTarget = useResourceTarget({ name, namespace: ns });
  const resourceClient = useResourceClient(resourceTarget);
  const { data: roster } = useQuery({
    queryKey: resourceKey(resourceTarget, "players", name, "overview", ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Players.snapshot(name, ns),
    enabled: !!name && (gs?.status?.phase === "Running"),
    refetchInterval: 10_000,
    retry: false,
  });

  // Kubernetes events for the pod/StatefulSet/GameServer — image pull,
  // scheduling, crash-loops. Poll faster while not Running so provisioning
  // diagnostics stay fresh; back off once the server is up.
  const { data: rawEvents } = useQuery({
    queryKey: resourceKey(resourceTarget, "events", name, ns),
    queryFn: ({ signal }) => resourceClient.withSignal(signal).Servers.events(name, ns),
    enabled: !!name,
    refetchInterval: gs?.status?.phase === "Running" ? 30_000 : 5_000,
    retry: false,
  });

  // ⚡ Bolt: Memoize raw events mapping to prevent unnecessary array recreation
  // Expected impact: Eliminates O(N) mapping on every render cycle when polling
  const events: NormalizedServerEvent[] = useMemo(() => {
    return (Array.isArray(rawEvents) ? rawEvents : []).map(mapServerEvent);
  }, [rawEvents]);

  if (!gs) return <div className="p-6 text-muted">Loading…</div>;

  const status = gs.status ?? {};
  const running = status.phase === "Running";
  const agent = status.agent;
  // Resource usage comes from the agent's heartbeat (cgroup + statfs).
  // null/undefined means "unknown" (unreadable source, or a stale
  // heartbeat the API blanked) — render "—", not a misleading 0.
  const cpuKnown = typeof agent?.cpuMillicores === "number";
  const cpuMilli = cpuKnown ? (agent?.cpuMillicores as number) : 0;
  const cpuLimitMilli =
    typeof agent?.cpuLimitMillicores === "number" ? (agent?.cpuLimitMillicores as number) : 0;
  const cpuPct = cpuKnown && cpuLimitMilli ? (cpuMilli / cpuLimitMilli) * 100 : 0;

  const memKnown = typeof agent?.memoryBytes === "number";
  const memUsed = memKnown ? (agent?.memoryBytes as number) : 0;
  const memLimit =
    typeof agent?.memoryLimitBytes === "number" ? (agent?.memoryLimitBytes as number) : 0;
  const memPct = memKnown && memLimit ? (memUsed / memLimit) * 100 : 0;

  const diskKnown =
    typeof agent?.diskUsedBytes === "number" && typeof agent?.diskTotalBytes === "number";
  const diskUsed = diskKnown ? (agent?.diskUsedBytes as number) : 0;
  const diskTotal = diskKnown ? (agent?.diskTotalBytes as number) : 0;
  const diskPct = diskKnown && diskTotal ? (diskUsed / diskTotal) * 100 : 0;

  // Player count is shown in the Players card (sidebar). null/undefined
  // means "unknown" (RCON unavailable or a stale heartbeat the API
  // blanked); fall back to 0 for the card's header.
  const players =
    typeof agent?.playersOnline === "number" && agent.playersOnline >= 0
      ? (agent.playersOnline as number)
      : 0;
  const endpoints = status.endpoints ?? [];
  const primary = endpoints[0];
  // The operator prepends tunnel endpoints ahead of the cluster-native ones
  // (see gameserver_status.go), so when a tunnel is active `endpoints[0]` is
  // the tunnel endpoint — the cluster address is the first entry after it
  // that isn't itself tunnel-provided.
  const clusterEndpoint = primary?.tunnelProvider
    ? endpoints.find((e) => !e.tunnelProvider)
    : primary;
  const tunnelReady = status.conditions?.find((c) => c.type === "TunnelReady");

  // Check for provisioning failures
  const provisioningFailure = status.conditions?.find(
    (c) => c.reason?.includes("Provisioning") || c.reason?.includes("Storage") || c.reason?.includes("PVC")
  );

  return (
    <div className="space-y-5 p-8">
      {/* Provisioning failure alert */}
      {provisioningFailure && (
        <Alert
          status="warning"
          className="flex items-start gap-3"
        >
          <Alert.Indicator className="mt-0.5">
            <AlertCircle className="h-5 w-5 shrink-0 text-warning" />
          </Alert.Indicator>
          <Alert.Content className="flex flex-1 flex-col gap-1">
            <Alert.Title className="font-semibold text-sm">
              Waiting on storage — the server can&apos;t provision yet
            </Alert.Title>
            <Alert.Description className="text-sm">
              Ready condition: {provisioningFailure.reason} — {provisioningFailure.message}
            </Alert.Description>
          </Alert.Content>
        </Alert>
      )}

      <div className="grid gap-6 lg:grid-cols-[1fr_360px]">
        <div className="space-y-5">
          <div className="grid gap-4 md:grid-cols-3">
            <ResourceCard
              label="CPU"
              icon={<Cpu className="h-4 w-4" />}
              value={
                cpuKnown ? (cpuLimitMilli ? `${cpuPct.toFixed(0)}%` : `${(cpuMilli / 1000).toFixed(2)} cores`) : "—"
              }
              sub={
                cpuKnown && cpuLimitMilli
                  ? `${(cpuMilli / 1000).toFixed(1)} / ${(cpuLimitMilli / 1000).toFixed(1)} cores`
                  : undefined
              }
              progress={cpuKnown && cpuLimitMilli ? cpuPct : undefined}
              trend={cpuKnown && cpuLimitMilli ? cpuPct : undefined}
              accent="primary"
            />
            <ResourceCard
              label="Memory"
              icon={<MemoryStick className="h-4 w-4" />}
              value={memKnown ? (memLimit ? `${memPct.toFixed(0)}%` : formatBytes(memUsed)) : "—"}
              sub={
                memKnown ? `${formatBytes(memUsed)} / ${memLimit ? formatBytes(memLimit) : "—"}` : undefined
              }
              progress={memKnown && memLimit ? memPct : undefined}
              trend={memKnown && memLimit ? memPct : undefined}
              accent="violet"
            />
            <ResourceCard
              label="Disk"
              icon={<HardDrive className="h-4 w-4" />}
              value={diskKnown ? (diskTotal ? `${diskPct.toFixed(0)}%` : formatBytes(diskUsed)) : "—"}
              sub={
                diskKnown ? `${formatBytes(diskUsed)} / ${diskTotal ? formatBytes(diskTotal) : "—"}` : undefined
              }
              progress={diskKnown && diskTotal ? diskPct : undefined}
              trend={diskKnown && diskTotal ? diskPct : undefined}
              accent="success"
            />
          </div>

          <Card className="border border-border bg-surface">
            <CardHeader className="flex flex-col gap-3 border-b border-border p-4">
              <div className="flex items-center justify-between">
                <h3 className="text-lg font-semibold text-foreground">Recent events</h3>
                <button
                  className="text-xs font-medium text-accent hover:underline"
                  onClick={onViewAllEvents}
                >
                  View all
                </button>
              </div>
            </CardHeader>
            <CardContent className="px-0">
              <EventList
                events={events}
                emptyMessage="No events yet. Lifecycle, backup, and agent activity will appear here."
              />
            </CardContent>
          </Card>
        </div>

        <div className="space-y-5">
          <Card className="border border-border bg-surface">
            <CardHeader className="border-b border-border px-4 py-3">
              <h3 className="text-lg font-semibold text-foreground">Connection</h3>
            </CardHeader>
            <CardContent className="space-y-3 px-4 py-3 text-sm">
              {/* Tunnel row — only when a tunnel endpoint actually exists;
                  per design (EZFW0) this row doesn't appear at all otherwise. */}
              {primary?.tunnelProvider && (
                <EndpointRow label="Tunnel">
                  {primary.host ? (
                    <>
                      <div className="flex min-w-0 flex-1 items-center gap-2">
                        <span className="truncate font-mono text-foreground">
                          {primary.host}
                          {primary.port !== undefined ? `:${primary.port}` : ""}
                        </span>
                        {primary.private && (
                          <span className="shrink-0 rounded-full bg-warning/20 px-2 py-0.5 text-[10px] font-medium text-warning">
                            Tailnet only
                          </span>
                        )}
                      </div>
                      <button
                        className="rounded p-1 text-muted hover:bg-border hover:text-foreground"
                        onClick={() => navigator.clipboard?.writeText(primary.host)}
                        title="Copy"
                      >
                        <Copy className="h-3.5 w-3.5" />
                      </button>
                    </>
                  ) : (
                    <span className="text-muted italic">
                      {tunnelReady?.message ?? "Waiting for tunnel address…"}
                    </span>
                  )}
                </EndpointRow>
              )}

              {/* External Address row — bare host, no port; a "pool: <name>"
                  annotation underneath when the address was allocated from a
                  pool (EZFW0: "Address 172.18.255.203" / "pool: pool-us-west"). */}
              <EndpointRow label="External Address">
                {primary?.host ? (
                  <>
                    <div className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate font-mono text-foreground">{primary.host}</span>
                      {primary.pool && (
                        <span className="text-xs text-muted">pool: {primary.pool}</span>
                      )}
                    </div>
                    <button
                      className="rounded p-1 text-muted hover:bg-border hover:text-foreground"
                      onClick={() => navigator.clipboard?.writeText(primary.host)}
                      title="Copy"
                    >
                      <Copy className="h-3.5 w-3.5" />
                    </button>
                  </>
                ) : (
                  <span className="text-muted italic">—</span>
                )}
              </EndpointRow>

              {/* Cluster Address — separate Host/Port fields (EZFW0). Omitted
                  entirely when clusterEndpoint resolves to the same endpoint
                  object as primary (no tunnel present): re-showing the
                  External Address's own host/port under a second label would
                  just repeat it, and there is no separate cluster-internal
                  service address in status to fall back to yet. */}
              {clusterEndpoint && clusterEndpoint !== primary && clusterEndpoint.host && (
                <div className="space-y-2">
                  <div className="px-1 text-xs font-medium text-muted">Cluster Address</div>
                  <EndpointRow label="Host">
                    <span className="truncate font-mono text-foreground">{clusterEndpoint.host}</span>
                    <button
                      className="rounded p-1 text-muted hover:bg-border hover:text-foreground"
                      onClick={() => navigator.clipboard?.writeText(clusterEndpoint.host)}
                      title="Copy"
                    >
                      <Copy className="h-3.5 w-3.5" />
                    </button>
                  </EndpointRow>
                  {clusterEndpoint.port !== undefined && (
                    <EndpointRow label="Port">
                      <span className="truncate font-mono text-foreground">{clusterEndpoint.port}</span>
                    </EndpointRow>
                  )}
                </div>
              )}
            </CardContent>
          </Card>

          <PlayersCard roster={roster} fallbackOnline={players} />

          <ServerStatusCard name={name} ns={ns} tmpl={tmpl} running={running} gs={gs} />

          <ServerSleepCard gs={gs} />

          <ServerActionsCard name={name} tmpl={tmpl} gs={gs} ns={ns} onOpenConsole={onOpenConsole} />
        </div>
      </div>
    </div>
  );
}

// Accumulate a rolling window of a metric's samples so the tile can draw a
// trend. Appends only when the value actually changes (the parent re-renders
// on every poll), capped to the last `max` points.
function useMetricHistory(value: number | undefined, max = 32): number[] {
  const [hist, setHist] = useState<number[]>([]);
  useEffect(() => {
    if (value === undefined || Number.isNaN(value)) return;
    const appendSample = () => {
      setHist((h) => {
        const next = h.concat(value);
        return next.length > max ? next.slice(-max) : next;
      });
    };
    appendSample();
  }, [value, max]);
  return hist;
}

function ResourceCard({
  label,
  icon,
  value,
  sub,
  progress,
  trend,
  accent,
}: {
  label: string;
  icon: ReactNode;
  value: string;
  sub?: string;
  progress?: number;
  trend?: number;
  accent?: "primary" | "success" | "warning" | "violet";
}) {
  const accentClass = {
    primary: "bg-accent",
    success: "bg-success",
    warning: "bg-warning",
    violet: "bg-violet",
  }[accent ?? "primary"];
  const accentText = {
    primary: "text-accent",
    success: "text-success",
    warning: "text-warning",
    violet: "text-violet",
  }[accent ?? "primary"];
  const history = useMetricHistory(trend);
  return (
    <Card className="border border-border bg-surface p-4">
      <div className="flex items-center justify-between text-xs font-semibold uppercase tracking-wider text-muted">
        <span>{label}</span>
        <span className="text-muted">{icon}</span>
      </div>
      <div className="pt-2 font-mono text-2xl font-bold text-foreground">{value}</div>
      <Sparkline data={history} className={`mt-2 ${accentText}`} />
      {progress !== undefined && (
        <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-surface/60">
          <div
            className={`h-full ${accentClass}`}
            style={{ width: `${Math.min(100, Math.max(0, progress))}%` }}
          />
        </div>
      )}
      {sub && <div className="pt-2 text-xs text-muted">{sub}</div>}
    </Card>
  );
}

// 8px address rows per design (assets/design-export/screenshots/EZFW0.png): compact
// bordered rows for each connection value, most with a copy affordance.
function EndpointRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex items-start gap-2 rounded-[8px] border border-border bg-surface/60 px-3 py-2">
      <span className="w-16 shrink-0 pt-1 text-xs font-medium text-muted">{label}</span>
      <div className="flex min-w-0 flex-1 items-center gap-2">{children}</div>
    </div>
  );
}

function PlayersCard({
  roster,
  fallbackOnline,
}: {
  roster?: PlayersResp;
  fallbackOnline: number;
}) {
  const online = roster?.online ?? fallbackOnline;
  // A negative online count is the agent's "unknown" sentinel (RCON
  // unavailable or a stale heartbeat) — render "—", matching the Players tab.
  const onlineUnknown = online < 0;
  const names = roster?.players ?? [];
  const supported =
    roster === undefined || roster.capabilities !== undefined;

  return (
    <Card className="border border-border bg-surface">
      <CardHeader className="border-b border-border px-4 py-3">
        <div className="flex flex-col gap-1">
          <h3 className="text-lg font-semibold text-foreground">
            Players online ·{" "}
            <span className="font-mono">{onlineUnknown ? "—" : online}</span>
          </h3>
          {names.length > 0 && (
            <span className="text-xs text-muted">{names.length} listed</span>
          )}
        </div>
      </CardHeader>
      <CardContent className="px-4 py-3">
        {!supported ? (
          <p className="text-sm text-muted">
            Player list not supported for this game.
          </p>
        ) : onlineUnknown ? (
          <p className="text-sm text-muted">Player count unknown.</p>
        ) : online === 0 ? (
          <p className="text-sm text-muted">No players connected.</p>
        ) : names.length === 0 ? (
          <p className="text-sm text-muted">
            {online} online · names not yet available.
          </p>
        ) : (
          <ul className="space-y-2 text-sm">
            {names.slice(0, 5).map((n) => (
              <li key={n} className="flex items-center gap-2">
                <div className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-surface font-mono text-[10px] text-muted">
                  {n.slice(0, 2).toUpperCase()}
                </div>
                <span className="truncate font-mono text-foreground">{n}</span>
              </li>
            ))}
            {names.length > 5 && (
              <li className="pl-8 text-xs text-muted">
                + {names.length - 5} more
              </li>
            )}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

