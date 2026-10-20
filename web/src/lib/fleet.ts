import { useQueries, useQuery } from "@tanstack/react-query";
import { useCallback, useMemo } from "react";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { defineMessages } from "react-intl";
import { api } from "@/lib/api";
import { Templates } from "@/lib/endpoints";
import { assignGameCodesForTemplates } from "@/lib/gameIcon";
import { useOptionalIntl } from "@/lib/i18n/format";
import type { Backup, BackupSchedule, ClusterNode, ClusterStats, ClusterView, GameServer, GameTemplate, Restore } from "@/types";

export interface FleetTarget {
  cluster: string;
  namespace: string;
  name: string;
  uid: string;
}

export interface FleetAccess {
  canWrite: boolean;
  canControl: boolean;
  canConsole: boolean;
  canDelete: boolean;
  isOwner: boolean;
  isCollaborator: boolean;
}

export interface FleetItem<T> {
  target: FleetTarget;
  resource: T;
  access?: FleetAccess;
  permissions: string[];
}

export interface FleetIssue {
  cluster: string;
  namespace?: string;
  code: string;
  message: string;
}

export interface FleetResult<T> {
  items: T[];
  partial: boolean;
  issues: FleetIssue[];
  totalReturned: number;
  scopes?: { cluster: string; namespace?: string }[];
}

export interface FleetInventory {
  cluster: string;
  name: string;
  view: ClusterView;
  stats: ClusterStats;
}

export interface FleetPlacement { cluster: string; namespace: string; templates: GameTemplate[] }

export interface FleetFilter { cluster?: string; namespace?: string }

export function useFleetLocation(): readonly [string, (value: string) => void] {
  const search = useLocation().search;
  const navigate = useNavigate();
  const location = typeof search.cluster === "string" ? search.cluster : "";
  const choose = useCallback((value: string) => {
    void navigate({
      to: ".",
      replace: true,
      search: (previous: Record<string, unknown>) => ({ ...previous, cluster: value || undefined }),
    });
  }, [navigate]);
  return [location, choose];
}

function path(kind: string, filter: FleetFilter): string {
  const params = new URLSearchParams();
  if (filter.cluster) params.set("cluster", filter.cluster);
  if (filter.namespace) params.set("namespace", filter.namespace);
  const query = params.toString();
  return `/fleet/${kind}${query ? `?${query}` : ""}`;
}

function read<T>(kind: string, filter: FleetFilter = {}, signal?: AbortSignal) {
  // Fleet APIs own their scope; a remembered infrastructure selection cannot narrow them.
  return api<FleetResult<T>>(path(kind, filter), { cluster: "local", signal });
}

export const Fleet = {
  placements: (filter?: FleetFilter, signal?: AbortSignal) => read<FleetPlacement>("placements", filter, signal),
  servers: (filter?: FleetFilter, signal?: AbortSignal) => read<FleetItem<GameServer>>("servers", filter, signal),
  backups: (filter?: FleetFilter, signal?: AbortSignal) => read<FleetItem<Backup>>("backups", filter, signal),
  schedules: (filter?: FleetFilter, signal?: AbortSignal) => read<FleetItem<BackupSchedule>>("schedules", filter, signal),
  restores: (filter?: FleetFilter, signal?: AbortSignal) => read<FleetItem<Restore>>("restores", filter, signal),
  inventory: (filter?: FleetFilter, signal?: AbortSignal) => read<FleetInventory>("inventory", filter, signal),
};

export function useFleetServers(filter: FleetFilter = {}) {
  return useQuery({ queryKey: ["fleet", "servers", filter.cluster ?? "", filter.namespace ?? ""],
    queryFn: ({ signal }) => Fleet.servers(filter, signal), refetchInterval: 5_000 });
}

const MESSAGES = defineMessages({
  results_unavailable: {
    id: "web.lib.fleet.results_unavailable",
    defaultMessage: "Server results unavailable.",
    description: "Fleet view issue shown for a cluster or namespace whose server list failed to load",
  },
});

export function useFleetServerSelection(cluster: string, namespaces: Set<string>) {
  const intl = useOptionalIntl();
  const selected = useMemo(() => namespaces.size ? [...namespaces].sort() : [undefined], [namespaces]);
  const results = useQueries({ queries: selected.map((namespace) => ({
    queryKey: ["fleet", "servers", cluster, namespace ?? ""],
    queryFn: ({ signal }: { signal: AbortSignal }) => Fleet.servers({ cluster: cluster || undefined, namespace }, signal),
    refetchInterval: 5_000,
  })) });
  const items = new Map<string, FleetItem<GameServer>>();
  const issues: FleetIssue[] = [];
  let partial = false;
  for (const [index, result] of results.entries()) {
    for (const item of result.data?.items ?? []) items.set(targetKey(item.target), item);
    issues.push(...result.data?.issues ?? []);
    if (result.error) issues.push({ cluster, namespace: selected[index], code: "unavailable", message: intl.formatMessage(MESSAGES.results_unavailable) });
    partial ||= result.data?.partial === true || !!result.error;
  }
  return {
    data: { items: [...items.values()], issues, partial, totalReturned: items.size },
    isLoading: results.some((result) => result.isLoading),
    error: results.every((result) => !!result.error) ? results[0]?.error : undefined,
  };
}

export function targetKey(target: FleetTarget): string {
  return JSON.stringify([target.cluster, target.namespace, target.name, target.uid]);
}

export function targetSearch(target: FleetTarget): { cluster: string; ns: string } {
  return { cluster: target.cluster, ns: target.namespace };
}

export function targetLabel(target: Pick<FleetTarget, "cluster" | "namespace">): string {
  return `${target.cluster} / ${target.namespace}`;
}

export type Located<T> = T & { fleetTarget: FleetTarget; fleetAccess?: FleetAccess; fleetPermissions: string[] };

export function located<T>(item: FleetItem<T>): Located<T> {
  return { ...item.resource, fleetTarget: item.target, fleetAccess: item.access, fleetPermissions: item.permissions };
}

export function resourceTarget(resource: { metadata: { name: string; namespace?: string; uid?: string }; fleetTarget?: FleetTarget }): FleetTarget {
  return resource.fleetTarget ?? { cluster: "local", namespace: resource.metadata.namespace ?? "gameplane-games", name: resource.metadata.name, uid: resource.metadata.uid ?? "" };
}

export function sumNodeUsage(nodes: ClusterNode[], key: "cpu" | "memory") {
  const complete = nodes.length > 0 && nodes.every((node) =>
    typeof node[key]?.used === "number" && Number.isFinite(node[key]?.used) &&
    typeof node[key]?.capacity === "number" && (node[key]?.capacity ?? 0) > 0);
  if (!complete) return { known: false, used: 0, capacity: 0, pct: 0 };
  const used = nodes.reduce((sum, node) => sum + (node[key]?.used ?? 0), 0);
  const capacity = nodes.reduce((sum, node) => sum + (node[key]?.capacity ?? 0), 0);
  return { known: true, used, capacity, pct: (used / capacity) * 100 };
}

export function inventoryTotals(items: FleetInventory[]) {
  const nodes = items.flatMap((item) => (item.view.nodes ?? []).map((node) => ({ ...node, name: `${item.cluster} / ${node.name}` })));
  const storageKnown = items.length > 0 && items.every((item) =>
    typeof item.stats.usedStorageBytes === "number" && typeof item.stats.totalStorageBytes === "number" && item.stats.totalStorageBytes > 0);
  return {
    nodes,
    ready: items.reduce((sum, item) => sum + (item.view.ready ?? 0), 0),
    total: items.reduce((sum, item) => sum + (item.view.total ?? item.view.nodes?.length ?? 0), 0),
    usedStorageBytes: storageKnown ? items.reduce((sum, item) => sum + (item.stats.usedStorageBytes ?? 0), 0) : undefined,
    totalStorageBytes: storageKnown ? items.reduce((sum, item) => sum + (item.stats.totalStorageBytes ?? 0), 0) : undefined,
  };
}

export function useFleetPlacements() {
  return useQuery({ queryKey: ["fleet", "placements"], queryFn: ({ signal }) => Fleet.placements({}, signal), staleTime: 30_000 });
}

export function templateKey(cluster: string, name: string): string {
  return JSON.stringify([cluster, name]);
}

export function useFleetGameCodes(items: FleetItem<GameServer>[] = []) {
  const clusters = [...new Set(items.map((item) => item.target.cluster))].sort();
  const queries = useQueries({ queries: clusters.map((cluster) => ({
    queryKey: ["templates", cluster], queryFn: ({ signal }: { signal: AbortSignal }) => Templates.list(cluster, signal), staleTime: 30_000,
  })) });
  const gameCodes = new Map<string, string>();
  const byName = new Map<string, GameTemplate>();
  const templatesByCluster = new Map<string, GameTemplate[]>();
  for (const [index, query] of queries.entries()) {
    const templates = query.data?.items ?? [];
    templatesByCluster.set(clusters[index], templates);
    for (const [name, code] of assignGameCodesForTemplates(templates)) gameCodes.set(templateKey(clusters[index], name), code);
    for (const template of templates) byName.set(templateKey(clusters[index], template.metadata.name), template);
  }
  return { gameCodes, byName, templatesByCluster };
}

export function playerCoverage(servers: GameServer[]) {
  const unknown = servers.filter((server) => {
    if (["Stopped", "Suspended"].includes(server.status?.phase ?? "")) return false;
    const players = server.status?.agent?.playersOnline;
    return typeof players !== "number" || !Number.isFinite(players) || players < 0;
  }).length;
  return { known: servers.length === 0 || unknown < servers.length, unknown };
}
