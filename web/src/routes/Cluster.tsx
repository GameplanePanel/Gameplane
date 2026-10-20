import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Download, Plus, Server as ServerIcon, AlertCircle } from "lucide-react";
import { Button, Card, CardContent, Alert } from "@heroui/react";
import { Meter } from "@/components/ui/Meter";
import { PageHeader } from "@/components/ui/PageHeader";
import { formatBytes, formatUptime } from "@/lib/utils";
import { APIError, api } from "@/lib/api";
import type { ClusterNode, NodeJoinInfo } from "@/types";
import { Cluster, Clusters } from "@/lib/endpoints";
import { useMe, can } from "@/lib/auth";
import type { AllConfig } from "@/lib/config";
import { useCurrentCluster } from "@/lib/cluster";
import { ClusterSelector } from "@/components/ClusterSelector";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { useInstallation } from "@/lib/useInstallation";
import { useOptionalIntl } from "@/lib/i18n/format";
import { defineMessages, type MessageDescriptor } from "react-intl";

// Helm key shown as code in the cluster-ops note; a machine value, not copy.
const CLUSTER_OPS_KEY = "clusterOps.enabled";

const opErrorMessages = defineMessages({
  notEnabled: { id: "web.routes.cluster.op_not_enabled", defaultMessage: "Cluster operations aren't enabled on this install (set clusterOps.enabled).", description: "Cluster: error shown when the operator has cluster operations turned off (HTTP 501)" },
  forbidden: { id: "web.routes.cluster.op_forbidden", defaultMessage: "You don't have permission to do that.", description: "Cluster: error shown when the user lacks permission for a cluster operation (HTTP 403)" },
  failed: { id: "web.routes.cluster.op_failed", defaultMessage: "That operation failed. Check the API logs.", description: "Cluster: generic error shown when a cluster operation fails" },
});

// opMessage turns a cluster-op error into user copy — 501 means the
// operator hasn't enabled clusterOps.
function opMessage(e: unknown): MessageDescriptor {
  if (e instanceof APIError && e.status === 501) return opErrorMessages.notEnabled;
  if (e instanceof APIError && e.status === 403) return opErrorMessages.forbidden;
  return opErrorMessages.failed;
}

// Node status is a machine value from the API; Ready/NotReady show as labels,
// anything else as received, and a missing status shows as Unknown.
const nodeStatusMessages = defineMessages({
  ready: { id: "web.routes.cluster.status_ready", defaultMessage: "Ready", description: "Cluster: node status label for a node that is ready" },
  notReady: { id: "web.routes.cluster.status_not_ready", defaultMessage: "NotReady", description: "Cluster: node status label for a node that is not ready" },
  unknown: { id: "web.routes.cluster.status_unknown", defaultMessage: "Unknown", description: "Cluster: node status shown when the node reports none" },
});

function nodeStatusLabel(intl: ReturnType<typeof useOptionalIntl>, status: string | undefined): string {
  if (!status) return intl.formatMessage(nodeStatusMessages.unknown);
  if (status === "Ready") return intl.formatMessage(nodeStatusMessages.ready);
  if (status === "NotReady") return intl.formatMessage(nodeStatusMessages.notReady);
  return status;
}

export function ClusterPage() {
  const intl = useOptionalIntl();
  const clusterId = useCurrentCluster();
  const installation = useInstallation();
  const registry = useQuery({ queryKey: ["clusters"], queryFn: () => Clusters.list(), enabled: installation.data?.standalone === true });
  const available = installation.data?.localCluster || registry.data?.items.some((cluster) => cluster.name === clusterId);
  return <><div className="px-6 pt-6"><ClusterSelector /></div>
    {installation.isPending ? <LoadingCard message={intl.formatMessage({ id: "web.routes.cluster.loading_installation", defaultMessage: "Loading installation…", description: "Cluster: loading text shown in place of the node inventory while the installation capabilities are being fetched" })} />
      : installation.isError ? <ErrorCard message={intl.formatMessage({ id: "web.routes.cluster.couldn_t_load_installation_capabilities", defaultMessage: "Couldn't load installation capabilities.", description: "Cluster: error message shown when the installation capabilities cannot be fetched; a retry button appears below it" })} onRetry={() => void installation.refetch()} />
      : available ? <ClusterInventory key={clusterId} clusterId={clusterId} />
      : registry.isPending ? <LoadingCard message={intl.formatMessage({ id: "web.routes.cluster.loading_clusters", defaultMessage: "Loading clusters…", description: "Cluster: loading text shown while the list of registered clusters is being fetched, before a cluster can be chosen" })} />
      : registry.isError ? <ErrorCard message={intl.formatMessage({ id: "web.routes.cluster.couldn_t_load_registered_clusters", defaultMessage: "Couldn't load registered clusters.", description: "Cluster: error message shown when the list of registered clusters fails to load; a retry button appears below it" })} onRetry={() => void registry.refetch()} />
      : <Card className="m-6"><CardContent className="space-y-2 p-6">
        <h2 className="font-medium">{intl.formatMessage({ id: "web.routes.cluster.select_a_workload_cluster", defaultMessage: "Select a workload cluster", description: "Cluster: heading of the empty-state card shown when no workload cluster is selected" })}</h2>
        <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.cluster.choose_a_registered_cluster_to_view_its", defaultMessage: "Choose a registered cluster to view its node inventory. Register workload clusters on the Clusters page.", description: "Cluster: hint under the empty-state heading saying a registered cluster must be picked to see its nodes and that clusters are registered on the Clusters page" })}</p>
      </CardContent></Card>}
  </>;
}

function ClusterInventory({ clusterId }: { clusterId: string }) {
  const intl = useOptionalIntl();
  const { data: me } = useMe();
  const canReadConfig = clusterId === "local" && can(me, "config:read");

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["cluster", clusterId],
    queryFn: ({ signal }) => Cluster.view(clusterId, signal),
    refetchInterval: 15_000,
  });
  const { data: info } = useQuery({
    queryKey: ["cluster-info", clusterId],
    queryFn: ({ signal }) => Cluster.info(clusterId, signal),
  });
  const { data: config } = useQuery({
    queryKey: ["config"],
    queryFn: () => api<AllConfig>("/admin/config"),
    enabled: canReadConfig,
  });
  // Only an explicit false disables the buttons — an older API (or a
  // failed info fetch) leaves them active, and the 501 opMessage still
  // catches the disabled case as a backstop.
  const opsDisabled = clusterId !== "local" || info?.clusterOps === false;
  const disabledHint = opsDisabled
    ? intl.formatMessage({ id: "web.routes.cluster.ops_disabled_hint", defaultMessage: "Cluster operations are disabled on this install.", description: "Cluster: tooltip on the node action buttons when cluster operations are off" })
    : undefined;
  const emptyValue = intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" });

  const nodes = data?.nodes ?? [];
  const [joinInfo, setJoinInfo] = useState<NodeJoinInfo | null>(null);
  const [opError, setOpError] = useState<string | null>(null);

  const addNode = useMutation({
    mutationFn: () => Cluster.addNode(clusterId),
    onSuccess: (info) => {
      setOpError(null);
      setJoinInfo(info);
    },
    onError: (e) => setOpError(intl.formatMessage(opMessage(e))),
  });

  async function downloadKubeconfig() {
    setOpError(null);
    try {
      const blob = await Cluster.kubeconfig(clusterId);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "gameplane-kubeconfig.yaml";
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (e) {
      setOpError(intl.formatMessage(opMessage(e)));
    }
  }

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title={intl.formatMessage({ id: "web.routes.cluster.cluster", defaultMessage: "Cluster", description: "Cluster: page title of the node inventory view at the top of the page; keep it short" })}
        description={
          data && (data.name || data.version || nodes.length > 0)
            ? intl.formatMessage({ id: "web.routes.cluster.summary", defaultMessage: "{name} · {version} · {ready}/{total} nodes healthy", description: "Cluster: page subtitle; {name} is the cluster name, {version} the Kubernetes version, {ready} and {total} the node counts" }, { name: data.name || clusterId, version: data.version || emptyValue, ready: data.ready ?? nodes.filter(n => n.status === "Ready").length, total: data.total ?? nodes.length })
            : intl.formatMessage({ id: "web.routes.cluster.node_inventory_for", defaultMessage: "Node inventory for selected cluster: {clusterId}.", description: "Cluster: page subtitle shown before node data loads; {clusterId} is the cluster id" }, { clusterId })
        }
        actions={
          <div className="flex min-w-0 max-w-md flex-col items-start gap-2 sm:items-end">
            <div className="flex max-w-full flex-wrap items-center gap-2 sm:justify-end">
              <div title={disabledHint}>
                <Button
                  variant="outline"
                  onPress={() => void downloadKubeconfig()}
                  isDisabled={opsDisabled}
                >
                  <Download className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.cluster.download_kubeconfig", defaultMessage: "Download kubeconfig", description: "Cluster: button that downloads the cluster's kubeconfig file; keep it short; kubeconfig is a technical term, usually left untranslated" })}</Button>
              </div>
              <div title={disabledHint}>
                <Button
                  onPress={() => addNode.mutate()}
                  isDisabled={addNode.isPending || opsDisabled}
                >
                  <Plus className="h-4 w-4" /> {intl.formatMessage({ id: "web.routes.cluster.add_node", defaultMessage: "Add node", description: "Cluster: button that creates a node-join token so a new machine can be added to the cluster; keep it short" })}</Button>
              </div>
            </div>
            {opsDisabled && (
              <p className="text-xs text-muted sm:text-right">
                {clusterId !== "local"
                  ? intl.formatMessage({ id: "web.routes.cluster.remote_managed_note", defaultMessage: "Node enrollment and kubeconfig downloads are managed at this remote cluster.", description: "Cluster: note under the action buttons on a remote cluster" })
                  : intl.formatMessage({ id: "web.routes.cluster.enable_helm_note", defaultMessage: "Enable {setting} in the Helm values to mint node-join tokens and kubeconfigs.", description: "Cluster: note under the action buttons when operations are disabled; {setting} is the Helm key, shown as code" }, { setting: <code className="font-mono">{CLUSTER_OPS_KEY}</code> })}
              </p>
            )}
          </div>
        }
      />

      {opError && (
        <Alert status="danger" className="flex items-center gap-3">
          <Alert.Indicator>
            <AlertCircle className="h-5 w-5 shrink-0 text-danger" />
          </Alert.Indicator>
          <Alert.Content>{opError}</Alert.Content>
        </Alert>
      )}

      {joinInfo && (
        <Card>
          <CardContent className="space-y-2 p-4">
            <div className="flex items-center justify-between">
              <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.cluster.join_a_node", defaultMessage: "Join a node", description: "Cluster: heading of the card that shows the command to run on a machine that should join the cluster as a node" })}</div>
              <button
                type="button"
                onClick={() => setJoinInfo(null)}
                className="text-xs text-muted hover:text-foreground"
              >{intl.formatMessage({ id: "web.routes.cluster.dismiss", defaultMessage: "Dismiss", description: "Cluster: button that closes the join-a-node card; keep it short" })}</button>
            </div>
            <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.cluster.run_this_on_the_machine_you_want_to", defaultMessage: "Run this on the machine you want to join (token expires {expiresAt}):", description: "Cluster: sentence with expiresAt in p" }, { expiresAt: joinInfo.expiresAt })}</p>
            <div className="flex items-center gap-2">
              <code className="flex-1 overflow-auto rounded bg-[#0b0b0d] px-3 py-2 font-mono text-xs text-foreground">
                {joinInfo.command}
              </code>
              <Button
                variant="outline"
                size="sm"
                onPress={() => void navigator.clipboard?.writeText(joinInfo.command)}
              >{intl.formatMessage({ id: "web.routes.cluster.copy", defaultMessage: "Copy", description: "Cluster: button that copies the join command to the clipboard; keep it short" })}</Button>
            </div>
          </CardContent>
        </Card>
      )}

      {isLoading && <LoadingCard message={intl.formatMessage({ id: "web.routes.cluster.loading_nodes_for", defaultMessage: "Loading nodes for {clusterId}…", description: "Cluster: message of LoadingCard; {clusterId} is the cluster id" }, { clusterId })} />}
      {error && <ErrorCard message={intl.formatMessage({ id: "web.routes.cluster.nodes_load_failed", defaultMessage: "Couldn't load node inventory for {clusterId}. Check your cluster access and connection.", description: "Cluster: error card message when node inventory fails to load; {clusterId} is the cluster id" }, { clusterId })} onRetry={() => void refetch()} />}
      {!isLoading && !error && nodes.length === 0 && (
        <Card>
          <CardContent className="flex flex-col items-center justify-center gap-3 p-12 text-center">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-primary/10 text-primary">
              <ServerIcon className="h-5 w-5" />
            </div>
            <div>
              <div className="font-medium">{intl.formatMessage({ id: "web.routes.cluster.no_node_data_yet", defaultMessage: "No node data yet.", description: "Cluster: empty-state heading shown when no node information has arrived for the cluster; the line below it says what to check" })}</div>
              <div className="pt-1 text-sm text-muted">{intl.formatMessage({ id: "web.routes.cluster.no_nodes_are_reporting_yet_check_the", defaultMessage: "No nodes are reporting yet — check the API and operator connection.", description: "Cluster: hint under the empty-state heading asking the user to check the API and operator connection because no nodes report yet" })}</div>
            </div>
          </CardContent>
        </Card>
      )}

      {canReadConfig && config?.installTimeSettings && (
        <Card>
          <CardContent className="space-y-4 p-5">
            <div className="space-y-1">
              <h3 className="text-base font-medium">{intl.formatMessage({ id: "web.routes.cluster.storage", defaultMessage: "Storage", description: "Cluster: section heading for the storage settings that are fixed at install time; shown only to users who can read the cluster configuration" })}</h3>
              <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.cluster.set_at_install_time_via_helm_values_not", defaultMessage: "Set at install time via Helm values — not editable from the dashboard.", description: "Cluster: hint under the storage heading explaining that these values come from Helm values at install time and cannot be edited in the dashboard" })}</p>
            </div>

            <div className="space-y-4">
              <div className="flex items-center justify-between gap-6">
                <div className="flex-1 space-y-1">
                  <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.cluster.game_data_storage_class", defaultMessage: "Game data storage class", description: "Cluster: label of the setting that names the storage class used for new game server data volumes; storage class is a technical term, usually left untranslated" })}</div>
                  <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.cluster.storageclass_for_new_gameservers_game", defaultMessage: "StorageClass for new GameServers' game-data volumes — set by the operator.gameDataStorage.storageClassName Helm value and applied by the operator to every new PVC; the API is passed the same value as --game-data-storage-class only to report it here.", description: "Cluster: explanation under the game data storage class label; a Helm value sets it and it applies to new volumes; StorageClass is a technical term, usually left untranslated" })} {config.installTimeSettings.gameDataStorageClass === "" &&
                      intl.formatMessage({ id: "web.routes.cluster.storage_class_unset", defaultMessage: "Left unset, so new volumes use the cluster's default StorageClass.", description: "Cluster: note under the StorageClass description when no StorageClass is set" })}
                  </p>
                </div>
                {config.installTimeSettings.gameDataStorageClass ? (
                  <div className="font-mono text-sm font-medium">
                    {config.installTimeSettings.gameDataStorageClass}
                  </div>
                ) : (
                  <div className="rounded-full bg-surface px-3 py-1 text-sm text-muted">{intl.formatMessage({ id: "web.routes.cluster.cluster_default", defaultMessage: "Cluster default", description: "Cluster: badge shown instead of a storage class name when none is set, meaning new volumes use the cluster's default; keep it short" })}</div>
                )}
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {!error && nodes.map((n) => <NodeCard key={n.name} node={n} />)}
      </div>
    </div>
  );
}

function NodeCard({ node }: { node: ClusterNode }) {
  const intl = useOptionalIntl();
  const cpuKnown = node.cpu?.used !== undefined;
  const memKnown = node.memory?.used !== undefined;
  const cpuPct = pct(node.cpu?.used, node.cpu?.capacity);
  const memPct = pct(node.memory?.used, node.memory?.capacity);
  const ready = node.status === "Ready";
  const uptime = node.uptime ?? formatUptime(node.startedAt, intl);
  const emptyValue = intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" });
  return (
    <Card>
      <CardContent className="p-4 space-y-4">
        <div className="flex items-start justify-between gap-3">
          <div className="flex min-w-0 flex-1 items-start gap-3">
            <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-surface">
              <ServerIcon className="h-4 w-4 text-muted" />
            </div>
            <div className="min-w-0 flex-1">
              <div className="break-all font-mono text-sm font-semibold">{node.name}</div>
              <div className="pt-0.5 text-[11px] text-muted">
                {node.roles?.join(", ") || intl.formatMessage({ id: "web.routes.cluster.role_worker", defaultMessage: "worker", description: "Cluster: role label shown for a node that reports no Kubernetes roles" })}
              </div>
            </div>
          </div>
          <span className={`shrink-0 whitespace-nowrap rounded-full px-2 py-0.5 text-[10px] font-mono uppercase ${
            ready
              ? "bg-success/15 text-success"
              : "bg-danger/15 text-danger"
          }`}>
            {intl.formatMessage({ id: "web.routes.cluster.node_status_badge", defaultMessage: "● {status}", description: "Cluster: node status badge; {status} is the node status, or Unknown when none is reported" }, { status: nodeStatusLabel(intl, node.status) })}
          </span>
        </div>

        <div className="grid grid-cols-3 gap-3 text-xs">
          <Stat label={intl.formatMessage({ id: "web.routes.cluster.uptime", defaultMessage: "Uptime", description: "Cluster: label of the uptime figure on a node card, showing how long the node has been running; keep it short" })} value={uptime} />
          <Stat label={intl.formatMessage({ id: "web.routes.cluster.pods", defaultMessage: "Pods", description: "Cluster: label of the pod figure on a node card; the value shows pods in use out of the node's pod capacity, or only the capacity when usage is unknown" })} value={node.pods?.used !== undefined ? intl.formatMessage({ id: "web.routes.cluster.pods_used_of_capacity", defaultMessage: "{used}/{capacity}", description: "Cluster: node pod count as used/capacity; {used} and {capacity} are numbers" }, { used: node.pods.used, capacity: node.pods.capacity ?? 0 }) : node.pods?.capacity ? intl.formatMessage({ id: "web.routes.cluster.pods_of_capacity", defaultMessage: "of {capacity}", description: "Cluster: node pod capacity shown when usage is unknown; {capacity} is the pod limit" }, { capacity: node.pods.capacity }) : emptyValue} />
          <Stat label={intl.formatMessage({ id: "web.routes.cluster.cpu", defaultMessage: "CPU", description: "Cluster: label for CPU on a node card, used both for the core count figure and for the CPU usage bar; CPU is a technical term, usually left untranslated" })} value={node.cpu?.capacity ? intl.formatMessage({ id: "web.routes.cluster.cpu_cores", defaultMessage: "{cores} cores", description: "Cluster: node CPU capacity; {cores} is the core count" }, { cores: node.cpu.capacity }) : emptyValue} />
        </div>

        <div className="space-y-2">
          <Meter label={intl.formatMessage({ id: "web.routes.cluster.cpu", defaultMessage: "CPU", description: "Cluster: label for CPU on a node card, used both for the core count figure and for the CPU usage bar; CPU is a technical term, usually left untranslated" })} pct={cpuPct} unknown={!cpuKnown} accent="primary" />
          <Meter
            label={intl.formatMessage({ id: "web.routes.cluster.memory", defaultMessage: "Memory", description: "Cluster: label of the memory usage bar on a node card, showing memory in use against the node's total memory" })}
            pct={memPct}
            unknown={!memKnown}
            sub={
              node.memory?.capacity
                ? memKnown
                  ? intl.formatMessage({ id: "web.routes.cluster.memory_used_of_capacity", defaultMessage: "{used} / {capacity}", description: "Cluster: node memory as used / capacity; both are formatted byte sizes" }, { used: formatBytes(node.memory.used ?? 0), capacity: formatBytes(node.memory.capacity) })
                  : intl.formatMessage({ id: "web.routes.cluster.memory_of_capacity", defaultMessage: "of {capacity}", description: "Cluster: node memory capacity shown when usage is unknown; {capacity} is a formatted byte size" }, { capacity: formatBytes(node.memory.capacity) })
                : undefined
            }
            accent="violet"
          />
        </div>

        {node.labels && node.labels.length > 0 && (
          <div className="flex flex-wrap gap-1">
            {node.labels.map((l) => (
              <span key={l} className="rounded bg-surface px-2 py-0.5 text-[10px] font-mono text-muted">
                {l}
              </span>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-md border border-border bg-surface/60 p-2">
      <div className="text-[10px] uppercase tracking-wider text-muted">{label}</div>
      <div className="pt-0.5 font-mono text-xs text-foreground">{value}</div>
    </div>
  );
}

function pct(u?: number, cap?: number): number {
  if (u === undefined || !cap) return 0;
  return (u / cap) * 100;
}
