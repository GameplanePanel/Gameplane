import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Card, Chip, Input, ListBox, ListBoxItem, Select, Table } from "@heroui/react";
import { ArrowDown, ArrowUp, ArrowUpDown, Cpu, Layers, Search, Server, MemoryStick } from "lucide-react";
import { defineMessages, type MessageDescriptor } from "react-intl";
import { FleetCoverage } from "@/components/FleetScope";
import { PageHeader } from "@/components/ui/PageHeader";
import { StatCard } from "@/components/ui/StatCard";
import { Meter } from "@/components/ui/Meter";
import { Fleet } from "@/lib/fleet";
import { filterNodes, flattenNodes, nodeMeasurement, nodeStatus, sortNodes, summarizeNodes, validNodeNumber, type InventoryNode, type NodeSortColumn, type NodeSortDirection } from "@/lib/nodes";
import { useMediaQuery } from "@/lib/media";
import { useOptionalIntl } from "@/lib/i18n/format";
import { formatBytes, formatUptime } from "@/lib/utils";
import { NodeEnrollment } from "./NodeEnrollment";

const messages: Record<string, MessageDescriptor> = defineMessages({
  title: { id: "web.nodes.title", defaultMessage: "Nodes", description: "Nodes inventory page and embedded tab heading" },
  description: { id: "web.nodes.description", defaultMessage: "Monitor nodes and resource usage across your clusters.", description: "Nodes inventory description" },
  clusterDescription: { id: "web.nodes.cluster_description", defaultMessage: "Node inventory for {cluster}.", description: "Nodes inventory description fixed to one cluster" },
  ready: { id: "web.nodes.ready", defaultMessage: "Ready nodes", description: "Nodes: summary count of ready nodes" },
  reporting: { id: "web.nodes.reporting", defaultMessage: "Reporting clusters", description: "Nodes: number of clusters represented by visible nodes" },
  cpu: { id: "web.nodes.cpu", defaultMessage: "CPU", description: "Nodes: CPU usage column and summary label" },
  memory: { id: "web.nodes.memory", defaultMessage: "Memory", description: "Nodes: memory usage column and summary label" },
  pods: { id: "web.nodes.pods", defaultMessage: "Pods", description: "Nodes: pod usage column label" },
  uptime: { id: "web.nodes.uptime", defaultMessage: "Uptime", description: "Nodes: uptime column label" },
  node: { id: "web.nodes.node", defaultMessage: "Node", description: "Nodes: node identity column label" },
  cluster: { id: "web.nodes.cluster", defaultMessage: "Cluster", description: "Nodes: cluster identity column label" },
  status: { id: "web.nodes.status", defaultMessage: "Status", description: "Nodes: status column label" },
  visible: { id: "web.nodes.visible", defaultMessage: "Matching nodes", description: "Nodes: scope of the filtered summary" },
  returned: { id: "web.nodes.returned", defaultMessage: "Returned nodes only", description: "Nodes: incomplete coverage summary qualifier" },
  measured: { id: "web.nodes.measured", defaultMessage: "Capacity-weighted usage", description: "Nodes: CPU and memory summary uses summed usage and capacity" },
  unknownMetric: { id: "web.nodes.unknown_metric", defaultMessage: "Measurement unavailable", description: "Nodes: absent or incomplete resource usage summary" },
  unavailable: { id: "web.nodes.unavailable", defaultMessage: "Inventory unavailable", description: "Nodes: inventory cannot provide a reliable count" },
  search: { id: "web.nodes.search", defaultMessage: "Search nodes", description: "Nodes: accessible search field label" },
  searchPlaceholder: { id: "web.nodes.search_placeholder", defaultMessage: "Search nodes, clusters or labels…", description: "Nodes: search field placeholder" },
  filterCluster: { id: "web.nodes.filter_cluster", defaultMessage: "Filter by cluster", description: "Nodes: cluster filter accessible label" },
  filterStatus: { id: "web.nodes.filter_status", defaultMessage: "Filter by status", description: "Nodes: status filter accessible label" },
  allClusters: { id: "web.nodes.all_clusters", defaultMessage: "All clusters", description: "Nodes: option to include every authorized cluster" },
  allStatuses: { id: "web.nodes.all_statuses", defaultMessage: "All statuses", description: "Nodes: option to include every node status" },
  statusReady: { id: "web.nodes.status_ready", defaultMessage: "Ready", description: "Nodes: healthy node status" },
  statusNotReady: { id: "web.nodes.status_not_ready", defaultMessage: "Not ready", description: "Nodes: unhealthy node status" },
  statusUnknown: { id: "web.nodes.status_unknown", defaultMessage: "Unknown", description: "Nodes: unreported or unrecognized status filter" },
  coverage: { id: "web.nodes.coverage", defaultMessage: "Node results", description: "Nodes: resource label in fleet coverage notice" },
  retry: { id: "web.nodes.retry", defaultMessage: "Retry", description: "Nodes: retries failed inventory loading" },
  loading: { id: "web.nodes.loading", defaultMessage: "Loading nodes…", description: "Nodes: initial inventory loading state" },
  noData: { id: "web.nodes.no_data", defaultMessage: "No node data is available for this scope.", description: "Nodes: no returned data when inventory is incomplete or failed" },
  noMatch: { id: "web.nodes.no_match", defaultMessage: "No nodes match these filters.", description: "Nodes: search and status filters matched no nodes" },
  empty: { id: "web.nodes.empty", defaultMessage: "No nodes are reporting in this scope.", description: "Nodes: complete empty inventory" },
  list: { id: "web.nodes.list", defaultMessage: "Node inventory", description: "Nodes: accessible table and mobile list name" },
  sort: { id: "web.nodes.sort", defaultMessage: "Sort by {column}", description: "Nodes: accessible column sort button label" },
  sortBy: { id: "web.nodes.sort_by", defaultMessage: "Sort nodes by", description: "Nodes: accessible mobile sort column selector" },
  ascending: { id: "web.nodes.ascending", defaultMessage: "Ascending", description: "Nodes: ascending sort direction" },
  descending: { id: "web.nodes.descending", defaultMessage: "Descending", description: "Nodes: descending sort direction" },
  cores: { id: "web.nodes.cores", defaultMessage: "{used} / {capacity} cores", description: "Nodes: measured CPU usage and total core capacity" },
  memoryUsage: { id: "web.nodes.memory_usage", defaultMessage: "{used} / {capacity}", description: "Nodes: memory usage and capacity, both formatted sizes" },
  ofCapacity: { id: "web.nodes.of_capacity", defaultMessage: "of {capacity}", description: "Nodes: capacity shown when usage is unreported" },
  podUsage: { id: "web.nodes.pod_usage", defaultMessage: "{used} / {capacity}", description: "Nodes: pod count and capacity; either missing value is an em dash" },
  percent: { id: "web.nodes.percent", defaultMessage: "{value}%", description: "Nodes: resource usage percentage" },
  readyCount: { id: "web.nodes.ready_count", defaultMessage: "{ready} / {total}", description: "Nodes: ready count out of visible returned nodes" },
  count: { id: "web.nodes.count", defaultMessage: "{count, plural, one {# node} other {# nodes}}", description: "Nodes: count of visible returned rows" },
  missingInventory: { id: "web.nodes.missing_inventory", defaultMessage: "Node inventory was not reported.", description: "Nodes: a returned cluster omitted its node inventory" },
});

const columns: NodeSortColumn[] = ["name", "cluster", "status", "cpu", "memory", "pods", "uptime"];
const columnMessage = { name: messages.node, cluster: messages.cluster, status: messages.status, cpu: messages.cpu, memory: messages.memory, pods: messages.pods, uptime: messages.uptime };

export function NodeInventory({ clusterId = "", onClusterChange, embedded = false }: { clusterId?: string; onClusterChange?: (value: string) => void; embedded?: boolean }) {
  const intl = useOptionalIntl();
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState("");
  const [sortColumn, setSortColumn] = useState<NodeSortColumn>("name");
  const [sortDirection, setSortDirection] = useState<NodeSortDirection>("ascending");
  const mobile = useMediaQuery("(max-width: 767px)");
  const inventory = useQuery({ queryKey: ["fleet", "inventory", clusterId], queryFn: ({ signal }) => Fleet.inventory({ cluster: clusterId || undefined }, signal), refetchInterval: 15_000 });
  // Keep the all-cluster scope list alive while filtering so capped or unavailable
  // clusters remain selectable. Identical all-cluster queries share their cache.
  const scopes = useQuery({ queryKey: ["fleet", "inventory", ""], queryFn: ({ signal }) => Fleet.inventory({}, signal), enabled: !embedded, staleTime: 30_000 });
  const selectedItems = useMemo(() => (inventory.data?.items ?? []).filter((item) => !clusterId || item.cluster === clusterId), [inventory.data, clusterId]);
  const missingInventory = selectedItems.filter((item) => !Array.isArray(item.view.nodes));
  const partial = inventory.data?.partial === true || missingInventory.length > 0;
  const issues = [...inventory.data?.issues ?? [], ...missingInventory.map((item) => ({ cluster: item.cluster, code: "unavailable", message: intl.formatMessage(messages.missingInventory) }))];
  const rows = useMemo(() => flattenNodes(selectedItems), [selectedItems]);
  const filtered = useMemo(() => filterNodes(rows, search, status), [rows, search, status]);
  const visible = useMemo(() => sortNodes(filtered, sortColumn, sortDirection), [filtered, sortColumn, sortDirection]);
  const summary = summarizeNodes(filtered);
  const unknownCounts = inventory.isPending || !!inventory.error || partial && !rows.length;
  const choices = [...new Set([clusterId, ...[scopes.data, inventory.data].flatMap((data) => [
    ...(data?.items.map((item) => item.cluster) ?? []), ...(data?.scopes?.map((scope) => scope.cluster) ?? []), ...(data?.issues.map((issue) => issue.cluster) ?? []),
  ])].filter(Boolean))].sort();
  const names = new Map([...(scopes.data?.items ?? []), ...(inventory.data?.items ?? [])].map((item) => [item.cluster, item.name || item.cluster]));
  const statusLabel = (value: string) => intl.formatMessage(value === "Ready" ? messages.statusReady : value === "NotReady" ? messages.statusNotReady : messages.statusUnknown);
  const empty = inventory.isPending ? messages.loading : inventory.error || partial ? messages.noData : search || status ? messages.noMatch : messages.empty;
  const sort = (column: NodeSortColumn) => { setSortColumn(column); setSortDirection(column === sortColumn && sortDirection === "ascending" ? "descending" : "ascending"); };
  const percentage = (value: number | undefined) => value === undefined || inventory.error ? "—" : intl.formatMessage(messages.percent, { value: String(Math.round(value)) });
  const qualifier = intl.formatMessage(unknownCounts ? messages.unavailable : partial ? messages.returned : messages.visible);

  return <div className={embedded ? "min-w-0 space-y-5" : "min-w-0 space-y-6 p-4 sm:p-6"}>
    <PageHeader title={intl.formatMessage(messages.title)} description={intl.formatMessage(clusterId ? messages.clusterDescription : messages.description, { cluster: names.get(clusterId) || clusterId })} actions={<NodeEnrollment clusterId={clusterId} />} />
    <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
      <StatCard label={intl.formatMessage(messages.ready)} value={unknownCounts ? "—" : intl.formatMessage(messages.readyCount, { ready: summary.ready, total: summary.total })} sub={qualifier} icon={<Server className="h-5 w-5" />} accent="success" />
      <StatCard label={intl.formatMessage(messages.reporting)} value={unknownCounts ? "—" : summary.clusters} sub={qualifier} icon={<Layers className="h-5 w-5" />} />
      <StatCard label={intl.formatMessage(messages.cpu)} value={percentage(summary.cpu)} sub={intl.formatMessage(summary.cpu === undefined ? messages.unknownMetric : partial ? messages.returned : messages.measured)} icon={<Cpu className="h-5 w-5" />} />
      <StatCard label={intl.formatMessage(messages.memory)} value={percentage(summary.memory)} sub={intl.formatMessage(summary.memory === undefined ? messages.unknownMetric : partial ? messages.returned : messages.measured)} icon={<MemoryStick className="h-5 w-5" />} accent="violet" />
    </div>
    <div className="flex min-w-0 flex-col gap-3 md:flex-row md:items-center">
      <div className="relative min-w-0 flex-1"><Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" /><Input aria-label={intl.formatMessage(messages.search)} placeholder={intl.formatMessage(messages.searchPlaceholder)} value={search} onChange={(event) => setSearch(event.target.value)} className="w-full pl-9" /></div>
      {!embedded && <NodeSelect label={intl.formatMessage(messages.filterCluster)} value={clusterId} onChange={(value) => onClusterChange?.(value)} choices={[{ value: "", label: intl.formatMessage(messages.allClusters) }, ...choices.map((value) => ({ value, label: names.get(value) || value }))]} />}
      <NodeSelect label={intl.formatMessage(messages.filterStatus)} value={status} onChange={setStatus} choices={[{ value: "", label: intl.formatMessage(messages.allStatuses) }, ...["Ready", "NotReady", "Unknown"].map((value) => ({ value, label: statusLabel(value) }))]} />
    </div>
    <FleetCoverage partial={partial} issues={issues} error={inventory.error} label={intl.formatMessage(messages.coverage)} />
    {!!inventory.error && <Button variant="outline" onPress={() => void inventory.refetch()}>{intl.formatMessage(messages.retry)}</Button>}
    {mobile ? <>
      <div className="flex items-center gap-2"><NodeSelect label={intl.formatMessage(messages.sortBy)} value={sortColumn} onChange={(value) => setSortColumn(value as NodeSortColumn)} choices={columns.map((value) => ({ value, label: intl.formatMessage(columnMessage[value]) }))} /><Button variant="outline" aria-label={intl.formatMessage(sortDirection === "ascending" ? messages.ascending : messages.descending)} onPress={() => setSortDirection(sortDirection === "ascending" ? "descending" : "ascending")}>{sortDirection === "ascending" ? <ArrowUp className="h-4 w-4" /> : <ArrowDown className="h-4 w-4" />}</Button></div>
      <div role="list" aria-label={intl.formatMessage(messages.list)} className="space-y-3">
        {!visible.length && <Card className="p-8 text-center text-sm text-muted">{intl.formatMessage(empty)}</Card>}
        {visible.map((row) => <Card key={row.key} role="listitem" className="min-w-0 space-y-3 p-4">
          <div className="flex min-w-0 items-start justify-between gap-2"><NodeIdentity row={row} /><NodeStatus row={row} /></div>
          <p className="break-all text-xs text-muted">{row.clusterName}{row.clusterName !== row.cluster ? ` · ${row.cluster}` : ""}</p>
          <div className="grid grid-cols-2 gap-4"><NodeMetric row={row} kind="cpu" /><NodeMetric row={row} kind="memory" /></div>
          <div className="flex justify-between gap-3 text-xs"><span>{intl.formatMessage(messages.pods)} <NodePods row={row} /></span><span>{intl.formatMessage(messages.uptime)} {nodeUptime(row, intl)}</span></div>
        </Card>)}
      </div>
    </> : <div className="overflow-hidden rounded-lg border border-border bg-card">
      <Table.Root className="bg-transparent"><Table.ScrollContainer><Table.Content aria-label={intl.formatMessage(messages.list)} keyboardNavigationBehavior="arrow" className="min-w-[880px]">
        <Table.Header>{columns.map((column) => <Table.Column id={column} key={column} isRowHeader={column === "name"}>
          <button type="button" className="inline-flex items-center gap-1 py-2 text-xs font-semibold" aria-label={intl.formatMessage(messages.sort, { column: intl.formatMessage(columnMessage[column]) })} aria-description={sortColumn === column ? intl.formatMessage(sortDirection === "ascending" ? messages.ascending : messages.descending) : undefined} onClick={() => sort(column)}>{intl.formatMessage(columnMessage[column])}{sortColumn === column ? sortDirection === "ascending" ? <ArrowUp className="h-3 w-3" /> : <ArrowDown className="h-3 w-3" /> : <ArrowUpDown className="h-3 w-3 text-muted" />}</button>
        </Table.Column>)}</Table.Header>
        <Table.Body renderEmptyState={() => <div className="py-10 text-center text-sm text-muted">{intl.formatMessage(empty)}</div>}>
          {visible.map((row) => <Table.Row key={row.key}>
            <Table.Cell className="max-w-xs"><NodeIdentity row={row} /></Table.Cell>
            <Table.Cell className="max-w-40"><div className="break-all text-sm">{row.clusterName}</div>{row.clusterName !== row.cluster && <div className="break-all font-mono text-xs text-muted">{row.cluster}</div>}</Table.Cell>
            <Table.Cell><NodeStatus row={row} /></Table.Cell>
            <Table.Cell className="min-w-36"><NodeMetric row={row} kind="cpu" /></Table.Cell>
            <Table.Cell className="min-w-40"><NodeMetric row={row} kind="memory" /></Table.Cell>
            <Table.Cell className="whitespace-nowrap font-mono text-xs"><NodePods row={row} /></Table.Cell>
            <Table.Cell className="whitespace-nowrap font-mono text-xs">{nodeUptime(row, intl)}</Table.Cell>
          </Table.Row>)}
        </Table.Body>
      </Table.Content></Table.ScrollContainer></Table.Root>
    </div>}
    <p className="text-xs text-muted">{unknownCounts ? intl.formatMessage(messages.unavailable) : intl.formatMessage(messages.count, { count: visible.length })}{partial && !unknownCounts ? ` · ${intl.formatMessage(messages.returned)}` : ""}</p>
  </div>;
}

function NodeSelect({ label, value, onChange, choices }: { label: string; value: string; onChange: (value: string) => void; choices: { value: string; label: string }[] }) {
  return <Select aria-label={label} value={value} onChange={(next) => onChange(String(next ?? ""))} className="w-full md:w-48 md:shrink-0"><Select.Trigger><Select.Value /><Select.Indicator className="ml-auto h-4 w-4" /></Select.Trigger><Select.Popover><ListBox aria-label={label}>{choices.map((choice) => <ListBoxItem id={choice.value} key={choice.value} textValue={choice.label}>{choice.label}</ListBoxItem>)}</ListBox></Select.Popover></Select>;
}

function NodeIdentity({ row }: { row: InventoryNode }) {
  return <div className="min-w-0"><div className="break-all font-mono text-sm font-medium">{row.node.name}</div>{row.node.roles?.length ? <div className="mt-1 break-words text-xs text-muted">{row.node.roles.join(", ")}</div> : null}{row.node.labels?.length ? <div className="mt-1 flex flex-wrap gap-1">{row.node.labels.map((label) => <span className="break-all rounded bg-surface px-1 py-0.5 text-[10px] text-muted" key={label}>{label}</span>)}</div> : null}</div>;
}

function NodeStatus({ row }: { row: InventoryNode }) {
  const intl = useOptionalIntl();
  const status = nodeStatus(row.node);
  return <Chip size="sm" color={status === "Ready" ? "success" : status === "NotReady" ? "danger" : "default"} className="shrink-0 whitespace-nowrap">{status === "Unknown" && row.node.status?.trim() ? row.node.status : intl.formatMessage(status === "Ready" ? messages.statusReady : status === "NotReady" ? messages.statusNotReady : messages.statusUnknown)}</Chip>;
}

function NodeMetric({ row, kind }: { row: InventoryNode; kind: "cpu" | "memory" }) {
  const intl = useOptionalIntl();
  const metric = row.node[kind];
  const percent = nodeMeasurement(metric);
  const capacity = validNodeNumber(metric?.capacity) && metric.capacity > 0 ? metric.capacity : undefined;
  const sub = capacity !== undefined ? percent !== undefined
    ? intl.formatMessage(kind === "cpu" ? messages.cores : messages.memoryUsage, { used: kind === "cpu" ? intl.formatNumber(metric?.used ?? 0, { maximumFractionDigits: 2 }) : formatBytes(metric?.used ?? 0), capacity: kind === "cpu" ? intl.formatNumber(capacity) : formatBytes(capacity) })
    : intl.formatMessage(messages.ofCapacity, { capacity: kind === "cpu" ? intl.formatNumber(capacity) : formatBytes(capacity) }) : undefined;
  return <Meter label={intl.formatMessage(kind === "cpu" ? messages.cpu : messages.memory)} pct={percent ?? 0} unknown={percent === undefined} sub={sub} accent={kind === "cpu" ? "primary" : "violet"} />;
}

function NodePods({ row }: { row: InventoryNode }) {
  const intl = useOptionalIntl();
  const pods = row.node.pods;
  const used = validNodeNumber(pods?.used) && Number.isInteger(pods.used) ? pods.used : undefined;
  const capacity = validNodeNumber(pods?.capacity) && Number.isInteger(pods.capacity) && pods.capacity > 0 ? pods.capacity : undefined;
  return <>{used === undefined && capacity === undefined ? "—" : intl.formatMessage(messages.podUsage, { used: used ?? "—", capacity: capacity ?? "—" })}</>;
}

function nodeUptime(row: InventoryNode, intl: ReturnType<typeof useOptionalIntl>) {
  if (row.node.uptime?.trim()) return row.node.uptime;
  const started = row.node.startedAt ? Date.parse(row.node.startedAt) : NaN;
  return Number.isFinite(started) && started <= Date.now() ? formatUptime(row.node.startedAt, intl) : "—";
}
