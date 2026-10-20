import { useMemo } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Button, Card } from "@heroui/react";
import {
  Activity,
  Archive,
  CheckCircle2,
  Cpu,
  HardDrive,
  Pencil,
  Play,
  Plus,
  RotateCw,
  Server as ServerIcon,
  Square,
  Trash2,
  UserPlus,
  Users as UsersIcon,
  type LucideIcon,
} from "lucide-react";

import { StatCard } from "@/components/ui/StatCard";
import { Meter } from "@/components/ui/Meter";
import { PhaseChip } from "@/components/ui/PhaseChip";
import { GameIcon } from "@/components/ui/GameIcon";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { PageHeader } from "@/components/PageHeader";
import {
  cn,
  describeStorageProvisioned,
  formatBytes,
  formatCores,
  formatRelative,
  type StorageReading,
} from "@/lib/utils";
import { useMe, can } from "@/lib/auth";
import { Audit } from "@/lib/endpoints";
import { useFleetLocation, Fleet, inventoryTotals, playerCoverage, templateKey, useFleetGameCodes, located, resourceTarget, sumNodeUsage, targetKey, targetLabel, targetSearch, useFleetPlacements, useFleetServers } from "@/lib/fleet";
import { FleetCoverage, FleetScopeFilter } from "@/components/FleetScope";
import { countByState, phaseGroups, type PhaseGroups } from "@/lib/servers";
import type {
  AuditEvent,
  Backup,
  ClusterNode,
  GameServer,
  GameTemplate,
} from "@/types";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type MessageDescriptor } from "react-intl";

const COUNT_LABELS = defineMessages({
  ofReturned: { id: "web.routes.dashboard.of_returned", defaultMessage: "of {count} returned", description: "Dashboard: sub-label on the Running card when the server list is partial; {count} is the number of returned servers" },
  ofTotal: { id: "web.routes.dashboard.of_total", defaultMessage: "of {count} total", description: "Dashboard: sub-label on the Running card; {count} is the total number of servers" },
  serversCount: { id: "web.routes.dashboard.servers_count", defaultMessage: "{count} servers", description: "Dashboard: server count in the Fleet status card; {count} is the number of servers" },
  returnedServersCount: { id: "web.routes.dashboard.returned_servers_count", defaultMessage: "{count} returned servers", description: "Dashboard: server count in the Fleet status card when the list is partial; {count} is the number of returned servers" },
});

export function DashboardPage() {
  const intl = useOptionalIntl();
  const { data: placements } = useFleetPlacements();
  const canCreate = (placements?.items.length ?? 0) > 0;
  const [location, setLocation] = useFleetLocation();
  const navigate = useNavigate();
  const { data: me } = useMe();
  const canAudit = can(me, "audit:read");
  const { data: scopes } = useFleetServers();
  const { data: fleet, isLoading: serversLoading, error: serversError } = useFleetServers({ cluster: location || undefined });
  const { gameCodes, byName } = useFleetGameCodes(fleet?.items);
  const { data: inventory, isLoading: clusterLoading, error: clusterError } = useQuery({
    queryKey: ["fleet", "inventory", location], queryFn: ({ signal }) => Fleet.inventory({ cluster: location || undefined }, signal), staleTime: 30_000,
  });
  const { data: backupsFleet, isLoading: backupsLoading, error: backupsError } = useQuery({
    queryKey: ["fleet", "backups", location], queryFn: ({ signal }) => Fleet.backups({ cluster: location || undefined }, signal), staleTime: 30_000,
  });
  const serversData = useMemo(() => ({ items: (fleet?.items ?? []).filter((item) => !location || item.target.cluster === location).map(located) }), [fleet, location]);
  const backupsData = useMemo(() => ({ items: (backupsFleet?.items ?? []).filter((item) => !location || item.target.cluster === location).map(located) }), [backupsFleet, location]);
  const clusterView = inventoryTotals((inventory?.items ?? []).filter((item) => !location || item.cluster === location));
  const canCluster = (inventory?.items.length ?? 0) > 0;
  const { data: audit, isLoading: auditLoading } = useQuery({
    queryKey: ["audit", "dashboard"],
    queryFn: () => Audit.page(8, 0),
    enabled: canAudit,
    staleTime: 30_000,
  });

  const servers = useMemo(() => serversData?.items ?? [], [serversData?.items]);
  const countsUnavailable = !!serversError || (fleet?.partial === true && servers.length === 0);
  const counts = useMemo(() => countByState(servers), [servers]);
  const players = playerCoverage(servers);
  const groups = useMemo(() => phaseGroups(servers), [servers]);
  const recentBackups = useMemo(
    () => sortBackups(backupsData?.items ?? []).slice(0, 6),
    [backupsData?.items],
  );

  const nodes = clusterError ? [] : clusterView.nodes;
  const nodesReady = clusterView.ready;
  const nodesTotal = clusterView.total;
  const cpu = inventory?.partial ? { known: false, pct: 0 } : sumUsage(intl, nodes, "cpu");
  const mem = inventory?.partial ? { known: false, pct: 0 } : sumUsage(intl, nodes, "memory");
  const vcpus = nodes.reduce((sum, n) => sum + (n.cpu?.capacity ?? 0), 0);
  const emptyValue = intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" });
  const inventoryUnavailable = intl.formatMessage({ id: "web.routes.dashboard.inventory_unavailable", defaultMessage: "inventory unavailable", description: "Dashboard: sub-label on the storage and nodes cards when inventory cannot be read" });
  const storage = clusterView.usedStorageBytes === undefined ? { valueText: emptyValue, subText: inventoryUnavailable, overcommitted: false } : describeStorageProvisioned(clusterView.usedStorageBytes, clusterView.totalStorageBytes, intl);
  const runningSubLabel: MessageDescriptor = fleet?.partial ? COUNT_LABELS.ofReturned : COUNT_LABELS.ofTotal;
  const storageKnown = clusterView.usedStorageBytes !== undefined;
  const storagePct = clusterView.totalStorageBytes ? ((clusterView.usedStorageBytes ?? 0) / clusterView.totalStorageBytes) * 100 : 0;
  const isLoading = serversLoading || clusterLoading || backupsLoading || (canAudit && auditLoading);

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title={intl.formatMessage({ id: "web.routes.dashboard.dashboard", defaultMessage: "Dashboard", description: "Dashboard: page title at the top of the dashboard; keep it short" })}
        subtitle={intl.formatMessage({ id: "web.routes.dashboard.your_game_servers_and_resources_across", defaultMessage: "Your game servers and resources across authorized locations.", description: "Dashboard: subtitle under the page title saying the page shows game servers and resources in the locations the user may access" })}
        actions={canCreate ?
          <Button
            variant="primary"
            className="rounded-full"
            onPress={() => void navigate({ to: "/servers/new" })}
          >
            <Plus className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.dashboard.create_server", defaultMessage: "Create server", description: "Dashboard: button at the top right that opens the form to create a new game server; keep it short" })}</Button> : undefined
        }
      />

      <FleetScopeFilter value={location} onChange={setLocation} clusters={[...(scopes?.scopes ?? []).map((scope) => scope.cluster), ...(scopes?.items ?? []).map((item) => item.target.cluster), ...(scopes?.issues ?? []).map((issue) => issue.cluster), ...(inventory?.items ?? []).map((item) => item.cluster)]} />
      <FleetCoverage partial={fleet?.partial} issues={fleet?.issues} error={serversError} label={intl.formatMessage({ id: "web.routes.dashboard.server_totals", defaultMessage: "Server totals", description: "Dashboard: label naming the server data in the coverage status line; that line warns when server data is partial or failed" })} />
      <FleetCoverage partial={inventory?.partial} issues={inventory?.issues} error={clusterError} label={intl.formatMessage({ id: "web.routes.dashboard.inventory", defaultMessage: "Inventory", description: "Dashboard: label naming the cluster inventory data in the coverage status line; that line warns when inventory is partial or failed" })} />
      <FleetCoverage partial={backupsFleet?.partial} issues={backupsFleet?.issues} error={backupsError} label={intl.formatMessage({ id: "web.routes.dashboard.backups", defaultMessage: "Backups", description: "Dashboard: label naming the backup data in the coverage status line; that line warns when backup data is partial or failed" })} />

      {isLoading ? (
        <LoadingCard message={intl.formatMessage({ id: "web.routes.dashboard.loading_dashboard", defaultMessage: "Loading dashboard…", description: "Dashboard: loading text shown in place of the dashboard cards while the data loads" })} />
      ) : (
        <>
          <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-5">
            <StatCard
              label={intl.formatMessage({ id: "web.routes.dashboard.running", defaultMessage: "Running", description: "Dashboard: label of the summary card counting running game servers" })}
              icon={<Activity className="h-4 w-4" />}
              value={countsUnavailable ? emptyValue : counts.running}
              sub={countsUnavailable
                ? intl.formatMessage({ id: "web.routes.dashboard.server_status_unavailable", defaultMessage: "server status unavailable", description: "Dashboard: sub-label on the Running card when server data cannot be read" })
                : intl.formatMessage(runningSubLabel, { count: groups.total })}
              accent={countsUnavailable ? "warning" : "success"}
            />
            <StatCard
              label={intl.formatMessage({ id: "web.routes.dashboard.players_online", defaultMessage: "Players online", description: "Dashboard: label of the summary card for players online; the line under the number shows the peak or a note when counts are unavailable" })}
              icon={<UsersIcon className="h-4 w-4" />}
              value={countsUnavailable || !players.known ? emptyValue : counts.players}
              sub={countsUnavailable || !players.known
                ? intl.formatMessage({ id: "web.routes.dashboard.player_status_unavailable", defaultMessage: "player status unavailable", description: "Dashboard: sub-label on the Players online card when player counts cannot be read" })
                : players.unknown
                  ? intl.formatMessage({ id: "web.routes.dashboard.player_counts_unavailable", defaultMessage: "{count} server player counts unavailable", description: "Dashboard: sub-label on the Players online card when some servers report no player count; {count} is how many" }, { count: players.unknown })
                  : intl.formatMessage({ id: "web.routes.dashboard.peak_players", defaultMessage: "peak {count}", description: "Dashboard: sub-label on the Players online card with the peak player count; {count} is the peak" }, { count: counts.playersMax })}
              accent="primary"
            />
            <StatCard
              label={intl.formatMessage({ id: "web.routes.dashboard.vcpus", defaultMessage: "vCPUs", description: "Dashboard: label of the summary card with the total vCPU count; vCPU is a technical term, usually left untranslated" })}
              icon={<Cpu className="h-4 w-4" />}
              value={vcpus > 0 ? vcpus : emptyValue}
              sub={inventory?.partial
                ? intl.formatMessage({ id: "web.routes.dashboard.returned_inventory_cores", defaultMessage: "returned inventory cores", description: "Dashboard: sub-label on the vCPUs card when the inventory is partial" })
                : intl.formatMessage({ id: "web.routes.dashboard.authorized_inventory_cores", defaultMessage: "authorized inventory cores", description: "Dashboard: sub-label on the vCPUs card" })}
              accent="warning"
            />
            <StatCard
              label={intl.formatMessage({ id: "web.routes.dashboard.storage_provisioned", defaultMessage: "Storage provisioned", description: "Dashboard: label of the summary card with the total provisioned storage; the line under it says whether the figure covers returned or all authorized inventory" })}
              icon={<HardDrive className="h-4 w-4" />}
              value={storage.valueText}
              sub={storage.subText ?? emptyValue}
              accent={storage.overcommitted ? "warning" : "violet"}
            />
            <StatCard
              label={intl.formatMessage({ id: "web.routes.dashboard.nodes_ready", defaultMessage: "Nodes ready", description: "Dashboard: label of the summary card counting ready nodes; the value shows ready nodes out of all nodes" })}
              icon={<ServerIcon className="h-4 w-4" />}
              value={!clusterError && nodesTotal > 0 ? intl.formatMessage({ id: "web.routes.dashboard.ready_of_total", defaultMessage: "{ready}/{total}", description: "Dashboard: nodes ready shown as ready/total; both are counts" }, { ready: nodesReady, total: nodesTotal }) : emptyValue}
              sub={
                clusterError
                  ? inventoryUnavailable
                  : nodesTotal === 0
                  ? intl.formatMessage({ id: "web.routes.dashboard.no_node_data", defaultMessage: "no node data", description: "Dashboard: sub-label on the Nodes ready card when no nodes are reported" })
                  : inventory?.partial
                    ? intl.formatMessage({ id: "web.routes.dashboard.returned_nodes_only", defaultMessage: "returned nodes only", description: "Dashboard: sub-label on the Nodes ready card when the inventory is partial" })
                  : nodesReady === nodesTotal
                    ? intl.formatMessage({ id: "web.routes.dashboard.all_healthy", defaultMessage: "all healthy", description: "Dashboard: sub-label on the Nodes ready card when every node is ready" })
                    : intl.formatMessage({ id: "web.routes.dashboard.nodes_need_attention", defaultMessage: "needs attention", description: "Dashboard: sub-label on the Nodes ready card when some nodes are not ready" })
              }
              accent="warning"
            />
          </div>

          <div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
            {countsUnavailable
              ? <ErrorCard message={intl.formatMessage({ id: "web.routes.dashboard.servers_load_failed", defaultMessage: "Couldn't load servers. Status and player totals are unavailable.", description: "Dashboard: error card when the server list cannot be loaded" })} />
              : <FleetStatusCard complete={!fleet?.partial} groups={groups} gameCodes={gameCodes} byName={byName} />}
            <ClusterResourcesCard
              cpu={cpu}
              mem={mem}
              storage={storage}
              storageKnown={storageKnown}
              storagePct={storagePct}
              nodes={nodes}
              nodesReady={nodesReady}
              nodesTotal={nodesTotal}
              canViewCluster={canCluster}
            />
          </div>

          <div className="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
            {canAudit && <div className="space-y-2"><p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.dashboard.central_gameplane_audit_activity", defaultMessage: "Central Gameplane audit activity", description: "Dashboard: small caption above the recent audit events card, shown only to users who can view the audit log" })}</p><RecentActivityCard events={audit ?? []} /></div>}
            {backupsError
              ? <ErrorCard message={intl.formatMessage({ id: "web.routes.dashboard.backups_load_failed", defaultMessage: "Couldn't load backups. Backup status is unavailable.", description: "Dashboard: error card when the backup list cannot be loaded" })} />
              : <RecentBackupsCard complete={!backupsFleet?.partial} backups={recentBackups} gameCodes={gameCodes} byName={byName} servers={serversData?.items} />}
          </div>
        </>
      )}
    </div>
  );
}

function FleetStatusCard({
  complete,
  groups,
  gameCodes,
  byName,
}: {
  complete: boolean;
  groups: PhaseGroups;
  gameCodes: Map<string, string>;
  byName: Map<string, GameTemplate>;
}) {
  const intl = useOptionalIntl();
  const serverCountLabel: MessageDescriptor = complete ? COUNT_LABELS.serversCount : COUNT_LABELS.returnedServersCount;
  const stopped = groups.stopped + groups.other;
  const segments = [
    { n: groups.running, cls: "bg-success" },
    { n: stopped, cls: "bg-muted" },
    { n: groups.failed, cls: "bg-danger" },
  ].filter((s) => s.n > 0);
  const attention = groups.attention.slice(0, 4);

  return (
    <Card className="min-w-0 space-y-4 p-5">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: "web.routes.dashboard.fleet_status", defaultMessage: "Fleet status", description: "Dashboard: heading of the card showing how many game servers are running, stopped or failed and which ones need attention" })}</h3>
        <span className="text-xs text-muted">{intl.formatMessage(serverCountLabel, { count: groups.total })}</span>
      </div>

      <div className="flex h-2 overflow-hidden rounded-full bg-surface">
        {groups.total > 0 &&
          segments.map((s, i) => (
            <div key={i} className={s.cls} style={{ width: `${(s.n / groups.total) * 100}%` }} />
          ))}
      </div>

      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 text-xs">
        <LegendDot cls="bg-success" label={intl.formatMessage({ id: "web.routes.dashboard.legend_running", defaultMessage: "Running {count}", description: "Dashboard: legend label for running servers; {count} is the number" }, { count: groups.running })} />
        <LegendDot cls="bg-muted" label={intl.formatMessage({ id: "web.routes.dashboard.legend_stopped", defaultMessage: "Stopped {count}", description: "Dashboard: legend label for stopped servers; {count} is the number" }, { count: stopped })} />
        <LegendDot cls="bg-danger" label={intl.formatMessage({ id: "web.routes.dashboard.legend_failed", defaultMessage: "Failed {count}", description: "Dashboard: legend label for failed servers; {count} is the number" }, { count: groups.failed })} />
      </div>

      <div className="space-y-3 border-t border-border pt-4">
        <div className="text-[10px] font-medium uppercase tracking-wider text-muted">{intl.formatMessage({ id: "web.routes.dashboard.needs_attention", defaultMessage: "Needs attention", description: "Dashboard: small uppercase label above the list of up to four servers that need attention in the fleet status card" })}</div>
        {attention.length === 0 ? (
          <div className="flex items-center gap-2 text-sm text-muted">
            <CheckCircle2 className="h-4 w-4 text-success" /> {complete
              ? intl.formatMessage({ id: "web.routes.dashboard.everything_healthy", defaultMessage: "Everything looks healthy.", description: "Dashboard: Needs attention message when the fleet is complete and nothing needs attention" })
              : intl.formatMessage({ id: "web.routes.dashboard.no_issues_partial", defaultMessage: "No issues among returned servers; some locations are unavailable.", description: "Dashboard: Needs attention message when the fleet is partial and no returned server needs attention" })}
          </div>
        ) : (
          attention.map((gs) => (
            <AttentionRow key={targetKey(resourceTarget(gs))} gs={gs} gameCodes={gameCodes} byName={byName} />
          ))
        )}
      </div>
    </Card>
  );
}

function AttentionRow({
  gs,
  gameCodes,
  byName,
}: {
  gs: GameServer;
  gameCodes: Map<string, string>;
  byName: Map<string, GameTemplate>;
}) {
  const intl = useOptionalIntl();
  const phase = gs.status?.phase;
  // phaseGroups.attention only ever holds Failed-phase or stale-agent
  // (excluding expected-down) servers, so the reason is one of exactly
  // these two.
  const reason = phase === "Failed"
    ? intl.formatMessage({ id: "web.routes.dashboard.reason_failed", defaultMessage: "Failed — check logs", description: "Dashboard: reason shown on a server that needs attention because it failed" })
    : intl.formatMessage({ id: "web.routes.dashboard.reason_agent_stale", defaultMessage: "Agent heartbeat stale", description: "Dashboard: reason shown on a server whose agent heartbeat is stale" });
  return (
    <Link
      to="/servers/$name"
      params={{ name: gs.metadata.name }}
      search={targetSearch(resourceTarget(gs))}
      className="group flex items-center gap-3"
    >
      <GameIcon
        game={gs.spec.templateRef.name}
        icon={byName.get(templateKey(resourceTarget(gs).cluster, gs.spec.templateRef.name))?.spec.icon}
        code={gameCodes.get(templateKey(resourceTarget(gs).cluster, gs.spec.templateRef.name))}
        size="sm"
      />
      <div className="min-w-0 flex-1">
        <div className="truncate font-mono text-sm text-foreground group-hover:text-primary">
          {gs.metadata.name}
        </div>
        <div className="truncate text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.dashboard.target_and_reason", defaultMessage: "{target} · {reason}", description: "Dashboard: line under a server that needs attention; {target} is the cluster and namespace label, {reason} the problem" }, { target: targetLabel(resourceTarget(gs)), reason })}</div>
      </div>
      <PhaseChip phase={phase} asleep={gs.status?.idle?.asleep === true} />
    </Link>
  );
}

interface Usage {
  pct: number;
  sub?: string;
  // known is false when no node reported a `used` reading (no
  // metrics-server) — the meter must show "—", not a false 0%.
  known: boolean;
}

function ClusterResourcesCard({
  cpu,
  mem,
  storage,
  storageKnown,
  storagePct,
  nodes,
  nodesReady,
  nodesTotal,
  canViewCluster,
}: {
  cpu: Usage;
  mem: Usage;
  storage: StorageReading;
  storageKnown: boolean;
  storagePct: number;
  nodes: ClusterNode[];
  nodesReady: number;
  nodesTotal: number;
  canViewCluster: boolean;
}) {
  const intl = useOptionalIntl();
  return (
    <Card className="min-w-0 space-y-4 p-5">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: "web.routes.dashboard.cluster_resources", defaultMessage: "Cluster resources", description: "Dashboard: heading of the card showing CPU, memory and storage use across the clusters the user can see" })}</h3>
        {canViewCluster ? (
          <Link to="/clusters" className="text-xs text-primary hover:underline">{intl.formatMessage({ id: "web.routes.dashboard.view_clusters", defaultMessage: "View clusters", description: "Dashboard: link at the top right of the cluster resources card that opens the clusters page; keep it short" })}</Link>
        ) : (
          <span className="text-xs text-muted">
            {nodesTotal > 0 ? intl.formatMessage({ id: "web.routes.dashboard.nodes_ready_of_total", defaultMessage: "{ready}/{total} nodes ready", description: "Dashboard: node readiness in the Cluster resources card; {ready} and {total} are counts" }, { ready: nodesReady, total: nodesTotal }) : intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
          </span>
        )}
      </div>

      <div className="space-y-3">
        <Meter label={intl.formatMessage({ id: "web.routes.dashboard.cpu", defaultMessage: "CPU", description: "Dashboard: label of the CPU usage bar in the cluster resources card; CPU is a technical term, usually left untranslated" })} pct={cpu.pct} sub={cpu.sub} unknown={!cpu.known} accent="primary" />
        <Meter label={intl.formatMessage({ id: "web.routes.dashboard.memory", defaultMessage: "Memory", description: "Dashboard: label of the memory usage bar in the cluster resources card" })} pct={mem.pct} sub={mem.sub} unknown={!mem.known} accent="violet" />
        <Meter
          label={intl.formatMessage({ id: "web.routes.dashboard.storage", defaultMessage: "Storage", description: "Dashboard: label of the storage usage bar in the cluster resources card" })}
          pct={storagePct}
          sub={storage.subText}
          unknown={!storageKnown}
          accent={storage.overcommitted ? "warning" : "success"}
        />
      </div>

      {nodes.length > 0 && (
        <div className="space-y-2 border-t border-border pt-4">
          <div className="text-[10px] font-medium uppercase tracking-wider text-muted">{intl.formatMessage({ id: "web.routes.dashboard.nodes", defaultMessage: "Nodes", description: "Dashboard: small uppercase label above the list of up to four nodes in the cluster resources card" })}</div>
          {nodes.slice(0, 4).map((n) => (
            <NodeRow key={n.name} node={n} />
          ))}
        </div>
      )}
    </Card>
  );
}

function NodeRow({ node }: { node: ClusterNode }) {
  const intl = useOptionalIntl();
  const ready = node.status === "Ready";
  const cpuKnown = node.cpu?.used !== undefined;
  const cpuPct = pctOf(node.cpu?.used, node.cpu?.capacity);
  const meta = [
    node.pods?.used !== undefined ? intl.formatMessage({ id: "web.routes.dashboard.node_pods", defaultMessage: "{count} pods", description: "Dashboard: pod count on a node row; {count} is the number of pods" }, { count: node.pods.used }) : null,
    cpuKnown && node.cpu?.capacity ? intl.formatMessage({ id: "web.routes.dashboard.node_cpu", defaultMessage: "cpu {percent}%", description: "Dashboard: CPU usage on a node row; {percent} is the rounded percentage" }, { percent: Math.round(cpuPct) }) : null,
  ]
    .filter(Boolean)
    .join(intl.formatMessage({ id: "web.routes.dashboard.meta_separator", defaultMessage: " · ", description: "Separator between items on one line of metadata" }));
  return (
    <div className="flex min-w-0 items-center gap-2 text-xs">
      <span className={cn("h-2 w-2 shrink-0 rounded-full", ready ? "bg-success" : "bg-danger")} />
      <span className="min-w-0 flex-1 break-all font-mono text-foreground">{node.name}</span>
      <span className="shrink-0 text-muted">{meta || intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</span>
    </div>
  );
}

function RecentActivityCard({ events }: { events: AuditEvent[] }) {
  const intl = useOptionalIntl();
  return (
    <Card className="min-w-0 space-y-4 p-5">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: "web.routes.dashboard.recent_activity", defaultMessage: "Recent activity", description: "Dashboard: heading of the card listing the latest audit events; a link to the full audit log sits beside it" })}</h3>
        <Link to="/admin/audit" className="text-xs text-primary hover:underline">{intl.formatMessage({ id: "web.routes.dashboard.view_all", defaultMessage: "View all", description: "Dashboard: link in the recent activity card to the full audit log, and in the recent backups card to the backups page; keep it short" })}</Link>
      </div>
      {events.length === 0 ? (
        <div className="py-6 text-center text-sm text-muted">{intl.formatMessage({ id: "web.routes.dashboard.no_recent_activity", defaultMessage: "No recent activity.", description: "Dashboard: empty-state text in the recent activity card shown when there are no audit events" })}</div>
      ) : (
        <div className="space-y-3">
          {events.map((e) => (
            <ActivityRow key={e.id} event={e} />
          ))}
        </div>
      )}
    </Card>
  );
}

function ActivityRow({ event }: { event: AuditEvent }) {
  const intl = useOptionalIntl();
  const { icon: Icon, accent } = actionIcon(event);
  return (
    <div className="flex items-center gap-3">
      <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded-md bg-surface">
        <Icon className={cn("h-3.5 w-3.5", accent)} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm text-foreground">{describeAudit(intl, event)}</div>
        <div className="truncate font-mono text-[11px] text-muted">
          {event.method} {event.path}
        </div>
      </div>
      <span className="shrink-0 text-xs text-muted">{formatRelative(event.ts, intl)}</span>
    </div>
  );
}

function RecentBackupsCard({
  complete,
  backups,
  gameCodes,
  byName,
  servers,
}: {
  complete: boolean;
  backups: Backup[];
  gameCodes: Map<string, string>;
  byName: Map<string, GameTemplate>;
  servers?: GameServer[];
}) {
  const intl = useOptionalIntl();
  return (
    <Card className="min-w-0 space-y-4 p-5">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: "web.routes.dashboard.recent_backups", defaultMessage: "Recent backups", description: "Dashboard: heading of the card listing the latest backups; a link to the backups page sits beside it" })}</h3>
        <Link to="/backups" className="text-xs text-primary hover:underline">{intl.formatMessage({ id: "web.routes.dashboard.view_all", defaultMessage: "View all", description: "Dashboard: link in the recent activity card to the full audit log, and in the recent backups card to the backups page; keep it short" })}</Link>
      </div>
      {backups.length === 0 ? (
        <div className="py-6 text-center text-sm text-muted">{complete
          ? intl.formatMessage({ id: "web.routes.dashboard.no_backups_yet", defaultMessage: "No backups yet.", description: "Dashboard: empty Recent backups card when the backup list is complete" })
          : intl.formatMessage({ id: "web.routes.dashboard.no_backup_data", defaultMessage: "No backup data available.", description: "Dashboard: empty Recent backups card when the backup list is partial" })}</div>
      ) : (
        <div className="space-y-3">
          {backups.map((b) => (
            <BackupRow key={targetKey(resourceTarget(b))} backup={b} gameCodes={gameCodes} byName={byName} servers={servers} />
          ))}
        </div>
      )}
    </Card>
  );
}

function BackupRow({
  backup,
  gameCodes,
  byName,
  servers,
}: {
  backup: Backup;
  gameCodes: Map<string, string>;
  byName: Map<string, GameTemplate>;
  servers?: GameServer[];
}) {
  const intl = useOptionalIntl();
  // Resolve backup server ref to template ref
  const target = resourceTarget(backup);
  const server = servers?.find((s) => s.metadata.name === backup.spec.serverRef.name && resourceTarget(s).cluster === target.cluster && resourceTarget(s).namespace === target.namespace);
  const templateRef = server?.spec.templateRef.name ?? "";
  const when = backup.status?.completionTime ?? backup.status?.startTime;
  return (
    <div className="flex items-center gap-3">
      <GameIcon
        game={templateRef || backup.spec.serverRef.name}
        icon={byName.get(templateKey(target.cluster, templateRef))?.spec.icon}
        code={gameCodes.get(templateKey(target.cluster, templateRef))}
        size="sm"
      />
      <div className="min-w-0 flex-1">
        <div className="truncate font-mono text-sm text-foreground">{backup.spec.serverRef.name}</div>
        <div className="truncate text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.dashboard.target_and_time", defaultMessage: "{target} · {when}", description: "Dashboard: line under a backup; {target} is the cluster and namespace label, {when} the relative time" }, { target: targetLabel(target), when: formatRelative(when, intl) })}</div>
      </div>
      {backup.status?.size && (
        <span className="shrink-0 font-mono text-xs text-foreground">{backup.status.size}</span>
      )}
      <PhaseChip phase={backup.status?.phase} />
    </div>
  );
}

function LegendDot({ cls, label }: { cls: string; label: string }) {
  return (
    <span className="flex items-center gap-1.5 text-muted">
      <span className={cn("h-2 w-2 rounded-full", cls)} />
      {label}
    </span>
  );
}

// sumUsage aggregates a resource across nodes into a percentage and a
// human-readable "used / capacity" subtitle (bytes for memory, cores for
// CPU). `known` is false when not a single node reported a `used` reading
// (no metrics-server anywhere in the cluster) — callers must render that as
// "—", not a 0% bar that implies a measured idle cluster.
function sumUsage(intl: ReturnType<typeof useOptionalIntl>, nodes: ClusterNode[], key: "cpu" | "memory"): Usage {
  const reading = sumNodeUsage(nodes, key);
  if (!reading.known) return { pct: 0, known: false };
  const sub = key === "memory"
    ? intl.formatMessage({ id: "web.routes.dashboard.memory_used_of_capacity", defaultMessage: "{used} / {capacity}", description: "Dashboard: memory usage sub-label on the Cluster resources card; both are formatted byte sizes" }, { used: formatBytes(reading.used), capacity: formatBytes(reading.capacity) })
    : intl.formatMessage({ id: "web.routes.dashboard.cpu_used_of_capacity", defaultMessage: "{used} / {capacity} cores", description: "Dashboard: CPU usage sub-label on the Cluster resources card; both are formatted core counts" }, { used: formatCores(reading.used), capacity: formatCores(reading.capacity) });
  return { pct: reading.pct, sub, known: true };
}

function pctOf(used?: number, cap?: number): number {
  if (used === undefined || !cap) return 0;
  return (used / cap) * 100;
}

function sortBackups(items: Backup[]): Backup[] {
  return [...items].sort((a, b) =>
    (b.status?.startTime ?? "").localeCompare(a.status?.startTime ?? ""),
  );
}

// describeAudit turns an audit row into a one-line human summary. Lifecycle
// sub-resource paths (":start" etc.) and sign-ins get specific verbs; otherwise the verb
// derives from the HTTP method.
const auditVerbs = defineMessages({
  deleted: { id: "web.routes.dashboard.audit_verb_deleted", defaultMessage: "deleted", description: "Dashboard: action word in an activity line for a DELETE request" },
  created: { id: "web.routes.dashboard.audit_verb_created", defaultMessage: "created", description: "Dashboard: action word in an activity line for a POST request" },
  updated: { id: "web.routes.dashboard.audit_verb_updated", defaultMessage: "updated", description: "Dashboard: action word in an activity line for a PUT or PATCH request" },
});

function describeAudit(intl: ReturnType<typeof useOptionalIntl>, e: AuditEvent): string {
  const target = e.target ? ` ${e.target}` : "";
  const actor = e.actor;
  if (e.path.endsWith("/auth/login")) {
    return e.status >= 200 && e.status < 300
      ? intl.formatMessage({ id: "web.routes.dashboard.audit_signed_in", defaultMessage: "{actor} signed in", description: "Dashboard: activity line for a successful sign-in; {actor} is the username" }, { actor })
      : intl.formatMessage({ id: "web.routes.dashboard.audit_failed_sign_in", defaultMessage: "{actor} failed to sign in", description: "Dashboard: activity line for a failed sign-in; {actor} is the username" }, { actor });
  }
  if (e.path.includes(":start")) return intl.formatMessage({ id: "web.routes.dashboard.audit_started", defaultMessage: "{actor} started{target}", description: "Dashboard: activity line for a server start; {actor} is the username, {target} is the audited object preceded by a space, or empty" }, { actor, target });
  if (e.path.includes(":stop")) return intl.formatMessage({ id: "web.routes.dashboard.audit_stopped", defaultMessage: "{actor} stopped{target}", description: "Dashboard: activity line for a server stop; {actor} is the username, {target} is the audited object preceded by a space, or empty" }, { actor, target });
  if (e.path.includes(":restart")) return intl.formatMessage({ id: "web.routes.dashboard.audit_restarted", defaultMessage: "{actor} restarted{target}", description: "Dashboard: activity line for a server restart; {actor} is the username, {target} is the audited object preceded by a space, or empty" }, { actor, target });
  if (e.path.includes("/backups")) return intl.formatMessage({ id: "web.routes.dashboard.audit_backed_up", defaultMessage: "{actor} backed up{target}", description: "Dashboard: activity line for a backup; {actor} is the username, {target} is the audited object preceded by a space, or empty" }, { actor, target });
  if (e.path.includes("/users")) return intl.formatMessage({ id: "web.routes.dashboard.audit_updated_user", defaultMessage: "{actor} updated a user{target}", description: "Dashboard: activity line for a user change; {actor} is the username, {target} is the audited object preceded by a space, or empty" }, { actor, target });
  const verb =
    e.method === "DELETE"
      ? intl.formatMessage(auditVerbs.deleted)
      : e.method === "POST"
        ? intl.formatMessage(auditVerbs.created)
        : e.method === "PUT" || e.method === "PATCH"
          ? intl.formatMessage(auditVerbs.updated)
          : e.method.toLowerCase();
  return intl.formatMessage({ id: "web.routes.dashboard.audit_generic", defaultMessage: "{actor} {verb}{target}", description: "Dashboard: fallback activity line; {actor} is the username, {verb} the action word, {target} the audited object preceded by a space, or empty" }, { actor, verb, target }).trim();
}

function actionIcon(e: AuditEvent): { icon: LucideIcon; accent: string } {
  if (e.path.includes(":start")) return { icon: Play, accent: "text-success" };
  if (e.path.includes(":stop")) return { icon: Square, accent: "text-muted" };
  if (e.path.includes(":restart")) return { icon: RotateCw, accent: "text-warning" };
  if (e.path.includes("/backups")) return { icon: Archive, accent: "text-violet" };
  if (e.path.includes("/users")) return { icon: UserPlus, accent: "text-primary" };
  if (e.method === "DELETE") return { icon: Trash2, accent: "text-danger" };
  if (e.method === "POST") return { icon: Plus, accent: "text-success" };
  return { icon: Pencil, accent: "text-muted" };
}
