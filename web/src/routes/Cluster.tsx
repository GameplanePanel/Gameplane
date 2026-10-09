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

// opMessage turns a cluster-op error into user copy — 501 means the
// operator hasn't enabled clusterOps.
function opMessage(e: unknown): string {
  if (e instanceof APIError && e.status === 501) {
    return "Cluster operations aren't enabled on this install (set clusterOps.enabled).";
  }
  if (e instanceof APIError && e.status === 403) return "You don't have permission to do that.";
  return "That operation failed. Check the API logs.";
}

export function ClusterPage() {
  const clusterId = useCurrentCluster();
  const installation = useInstallation();
  const registry = useQuery({ queryKey: ["clusters"], queryFn: () => Clusters.list(), enabled: installation.data?.standalone === true });
  const available = installation.data?.localCluster || registry.data?.items.some((cluster) => cluster.name === clusterId);
  return <><div className="px-6 pt-6"><ClusterSelector /></div>
    {installation.isPending ? <LoadingCard message="Loading installation…" />
      : installation.isError ? <ErrorCard message="Couldn't load installation capabilities." onRetry={() => void installation.refetch()} />
      : available ? <ClusterInventory key={clusterId} clusterId={clusterId} />
      : registry.isPending ? <LoadingCard message="Loading clusters…" />
      : registry.isError ? <ErrorCard message="Couldn't load registered clusters." onRetry={() => void registry.refetch()} />
      : <Card className="m-6"><CardContent className="space-y-2 p-6">
        <h2 className="font-medium">Select a workload cluster</h2>
        <p className="text-sm text-muted">Choose a registered cluster to view its node inventory. Register workload clusters on the Clusters page.</p>
      </CardContent></Card>}
  </>;
}

function ClusterInventory({ clusterId }: { clusterId: string }) {
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

  const nodes = data?.nodes ?? [];
  const [joinInfo, setJoinInfo] = useState<NodeJoinInfo | null>(null);
  const [opError, setOpError] = useState<string | null>(null);

  const addNode = useMutation({
    mutationFn: () => Cluster.addNode(clusterId),
    onSuccess: (info) => {
      setOpError(null);
      setJoinInfo(info);
    },
    onError: (e) => setOpError(opMessage(e)),
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
      setOpError(opMessage(e));
    }
  }

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title="Cluster"
        description={
          data && (data.name || data.version || nodes.length > 0)
            ? `${data.name || clusterId} · ${data.version || "—"} · ${data.ready ?? nodes.filter(n => n.status === "Ready").length}/${data.total ?? nodes.length} nodes healthy`
            : `Node inventory for selected cluster: ${clusterId}.`
        }
        actions={
          <div className="flex min-w-0 max-w-md flex-col items-start gap-2 sm:items-end">
            <div className="flex max-w-full flex-wrap items-center gap-2 sm:justify-end">
              <div title={opsDisabled ? "Cluster operations are disabled on this install." : undefined}>
                <Button
                  variant="outline"
                  onPress={() => void downloadKubeconfig()}
                  isDisabled={opsDisabled}
                >
                  <Download className="h-4 w-4" /> Download kubeconfig
                </Button>
              </div>
              <div title={opsDisabled ? "Cluster operations are disabled on this install." : undefined}>
                <Button
                  onPress={() => addNode.mutate()}
                  isDisabled={addNode.isPending || opsDisabled}
                >
                  <Plus className="h-4 w-4" /> Add node
                </Button>
              </div>
            </div>
            {opsDisabled && (
              <p className="text-xs text-muted sm:text-right">
                {clusterId !== "local" ? "Node enrollment and kubeconfig downloads are managed at this remote cluster." : <>
                  Enable <code className="font-mono">clusterOps.enabled</code> in the Helm values
                  to mint node-join tokens and kubeconfigs.
                </>}
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
              <div className="text-sm font-medium">Join a node</div>
              <button
                type="button"
                onClick={() => setJoinInfo(null)}
                className="text-xs text-muted hover:text-foreground"
              >
                Dismiss
              </button>
            </div>
            <p className="text-xs text-muted">
              Run this on the machine you want to join (token expires {joinInfo.expiresAt}):
            </p>
            <div className="flex items-center gap-2">
              <code className="flex-1 overflow-auto rounded bg-[#0b0b0d] px-3 py-2 font-mono text-xs text-foreground">
                {joinInfo.command}
              </code>
              <Button
                variant="outline"
                size="sm"
                onPress={() => void navigator.clipboard?.writeText(joinInfo.command)}
              >
                Copy
              </Button>
            </div>
          </CardContent>
        </Card>
      )}

      {isLoading && <LoadingCard message={`Loading nodes for ${clusterId}…`} />}
      {error && <ErrorCard message={`Couldn't load node inventory for ${clusterId}. Check your cluster access and connection.`} onRetry={() => void refetch()} />}
      {!isLoading && !error && nodes.length === 0 && (
        <Card>
          <CardContent className="flex flex-col items-center justify-center gap-3 p-12 text-center">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-primary/10 text-primary">
              <ServerIcon className="h-5 w-5" />
            </div>
            <div>
              <div className="font-medium">No node data yet.</div>
              <div className="pt-1 text-sm text-muted">
                No nodes are reporting yet — check the API and operator connection.
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      {canReadConfig && config?.installTimeSettings && (
        <Card>
          <CardContent className="space-y-4 p-5">
            <div className="space-y-1">
              <h3 className="text-base font-medium">Storage</h3>
              <p className="text-sm text-muted">
                Set at install time via Helm values — not editable from the dashboard.
              </p>
            </div>

            <div className="space-y-4">
              <div className="flex items-center justify-between gap-6">
                <div className="flex-1 space-y-1">
                  <div className="text-sm font-medium">Game data storage class</div>
                  <p className="text-xs text-muted">
                    StorageClass for new GameServers&apos; game-data volumes — set by the
                    operator.gameDataStorage.storageClassName Helm value and applied by the operator to
                    every new PVC; the API is passed the same value as --game-data-storage-class only to
                    report it here.{" "}
                    {config.installTimeSettings.gameDataStorageClass === "" &&
                      "Left unset, so new volumes use the cluster's default StorageClass."}
                  </p>
                </div>
                {config.installTimeSettings.gameDataStorageClass ? (
                  <div className="font-mono text-sm font-medium">
                    {config.installTimeSettings.gameDataStorageClass}
                  </div>
                ) : (
                  <div className="rounded-full bg-surface px-3 py-1 text-sm text-muted">
                    Cluster default
                  </div>
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
  const cpuKnown = node.cpu?.used !== undefined;
  const memKnown = node.memory?.used !== undefined;
  const cpuPct = pct(node.cpu?.used, node.cpu?.capacity);
  const memPct = pct(node.memory?.used, node.memory?.capacity);
  const ready = node.status === "Ready";
  const uptime = node.uptime ?? formatUptime(node.startedAt);
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
                {node.roles?.join(", ") || "worker"}
              </div>
            </div>
          </div>
          <span className={`shrink-0 whitespace-nowrap rounded-full px-2 py-0.5 text-[10px] font-mono uppercase ${
            ready
              ? "bg-success/15 text-success"
              : "bg-danger/15 text-danger"
          }`}>
            ● {node.status ?? "Unknown"}
          </span>
        </div>

        <div className="grid grid-cols-3 gap-3 text-xs">
          <Stat label="Uptime" value={uptime} />
          <Stat label="Pods" value={node.pods?.used !== undefined ? `${node.pods.used}/${node.pods.capacity ?? 0}` : node.pods?.capacity ? `of ${node.pods.capacity}` : "—"} />
          <Stat label="CPU" value={node.cpu?.capacity ? `${node.cpu.capacity} cores` : "—"} />
        </div>

        <div className="space-y-2">
          <Meter label="CPU" pct={cpuPct} unknown={!cpuKnown} accent="primary" />
          <Meter
            label="Memory"
            pct={memPct}
            unknown={!memKnown}
            sub={
              node.memory?.capacity
                ? memKnown
                  ? `${formatBytes(node.memory.used ?? 0)} / ${formatBytes(node.memory.capacity)}`
                  : `of ${formatBytes(node.memory.capacity)}`
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
