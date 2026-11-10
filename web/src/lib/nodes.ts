import type { ClusterNode } from "@/types";
import type { FleetInventory } from "./fleet";

export interface InventoryNode {
  key: string;
  cluster: string;
  clusterName: string;
  node: ClusterNode;
}
export type NodeSortColumn = "name" | "cluster" | "status" | "cpu" | "memory" | "pods" | "uptime";
export type NodeSortDirection = "ascending" | "descending";

export function flattenNodes(inventory: FleetInventory[]): InventoryNode[] {
  return inventory.flatMap((item) => (item.view.nodes ?? []).map((node) => ({
    key: JSON.stringify([item.cluster, node.name]), cluster: item.cluster,
    clusterName: item.name || item.cluster, node,
  })));
}

export function nodeStatus(node: ClusterNode): string {
  return node.status === "Ready" || node.status === "NotReady" ? node.status : "Unknown";
}

export function validNodeNumber(value: number | undefined): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0;
}

export function nodeMeasurement(metric?: { used?: number; capacity?: number }): number | undefined {
  if (!validNodeNumber(metric?.used) || !validNodeNumber(metric?.capacity) || metric.capacity === 0) return undefined;
  const percent = metric.used / metric.capacity * 100;
  return Number.isFinite(percent) ? percent : undefined;
}

export function filterNodes(rows: InventoryNode[], search: string, status: string): InventoryNode[] {
  const query = search.trim().toLocaleLowerCase();
  return rows.filter(({ cluster, clusterName, node }) => (!status || nodeStatus(node) === status) &&
    (!query || [node.name, cluster, clusterName, ...(node.roles ?? []), ...(node.labels ?? [])].some((value) => value.toLocaleLowerCase().includes(query))));
}

function uptimeSeconds(node: ClusterNode): number | undefined {
  if (node.uptime) {
    // The legacy API can supply an elapsed duration instead of startedAt.
    const matches = [...node.uptime.matchAll(/(\d+(?:\.\d+)?)\s*(d|h|m|s)/g)];
    if (matches.length && node.uptime.replace(/(\d+(?:\.\d+)?)\s*(d|h|m|s)/g, "").trim() === "") {
      const factors: Record<string, number> = { d: 86400, h: 3600, m: 60, s: 1 };
      return matches.reduce((sum, match) => sum + Number(match[1]) * factors[match[2]], 0);
    }
  }
  const started = node.startedAt ? Date.parse(node.startedAt) : NaN;
  return Number.isFinite(started) && started <= Date.now() ? (Date.now() - started) / 1000 : undefined;
}

export function sortNodes(rows: InventoryNode[], column: NodeSortColumn, direction: NodeSortDirection): InventoryNode[] {
  const value = (row: InventoryNode): string | number | undefined => {
    if (column === "name") return row.node.name;
    if (column === "cluster") return row.clusterName;
    if (column === "status") return nodeStatus(row.node) === "Unknown" ? undefined : nodeStatus(row.node);
    if (column === "cpu" || column === "memory") return nodeMeasurement(row.node[column]);
    if (column === "pods") return validNodeNumber(row.node.pods?.used) && Number.isInteger(row.node.pods.used) ? row.node.pods.used : undefined;
    return uptimeSeconds(row.node);
  };
  return [...rows].sort((a, b) => {
    const left = value(a), right = value(b);
    // Missing readings stay last in both directions, separate from measured zero.
    if (left === undefined || right === undefined) return left === right ? a.key.localeCompare(b.key) : left === undefined ? 1 : -1;
    const comparison = typeof left === "number" && typeof right === "number" ? left - right : String(left).localeCompare(String(right), undefined, { numeric: true });
    return (comparison || a.key.localeCompare(b.key)) * (direction === "ascending" ? 1 : -1);
  });
}

export function summarizeNodes(rows: InventoryNode[]) {
  const usage = (kind: "cpu" | "memory") => {
    if (!rows.length || rows.some(({ node }) => nodeMeasurement(node[kind]) === undefined)) return undefined;
    const used = rows.reduce((sum, { node }) => sum + (node[kind]?.used ?? 0), 0);
    const capacity = rows.reduce((sum, { node }) => sum + (node[kind]?.capacity ?? 0), 0);
    const percent = used / capacity * 100;
    return Number.isFinite(percent) ? percent : undefined;
  };
  return { ready: rows.filter(({ node }) => node.status === "Ready").length, total: rows.length,
    clusters: new Set(rows.map((row) => row.cluster)).size, cpu: usage("cpu"), memory: usage("memory") };
}
