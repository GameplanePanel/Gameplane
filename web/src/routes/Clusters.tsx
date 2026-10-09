import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button, Card, CardContent } from "@heroui/react";
import { Network } from "lucide-react";
import { PageHeader } from "@/components/ui/PageHeader";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { Clusters } from "@/lib/endpoints";
import { useCurrentCluster } from "@/lib/cluster";
import { useClusterSelection } from "@/lib/useClusterSelection";
import { useMe, can } from "@/lib/auth";
import { cn, formatRelative } from "@/lib/utils";
import { RegisterClusterDialog } from "@/components/RegisterClusterDialog";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";

export function ClustersPage() {
  const { data: me } = useMe();
  const client = useQueryClient();
  const [registerOpen, setRegisterOpen] = useState(false);
  const [removeTarget, setRemoveTarget] = useState<string | null>(null);
  const remove = useMutation({
    mutationFn: (name: string) => Clusters.remove(name),
    onSuccess: async () => {
      setRemoveTarget(null);
      await client.invalidateQueries({ queryKey: ["clusters"] });
      await client.invalidateQueries({ queryKey: ["fleet"] });
    },
  });
  const currentCluster = useCurrentCluster();
  const selectCluster = useClusterSelection();
  const navigate = useNavigate();
  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["clusters"],
    queryFn: () => Clusters.list(),
    refetchInterval: 30_000,
  });
  const clusters = data?.items ?? [];
  const openCluster = async (id: string, destination: "/servers" | "/cluster") => {
    if (destination === "/servers") { await navigate({ to: "/servers", search: { cluster: id } }); return; }
    await selectCluster(id);
    await navigate({ to: destination });
  };

  return (
    <div className="space-y-6 p-4 sm:p-6">
      <PageHeader title="Clusters" description="Administration of registered locations, connectivity and node inventory."
        actions={can(me, "cluster:manage") && <Button onPress={() => setRegisterOpen(true)}>Register cluster</Button>} />
      {registerOpen && <RegisterClusterDialog open onOpenChange={setRegisterOpen} />}
      {remove.error && <ErrorCard message="Couldn't remove the cluster registration. Try again." />}
      <ConfirmDialog open={!!removeTarget} onOpenChange={(open) => !open && setRemoveTarget(null)} title={`Remove ${removeTarget ?? ""}?`}
        description="Removes this panel's connection to the cluster. Running workloads remain on the cluster."
        confirmLabel="Remove cluster" destructive busy={remove.isPending} onConfirm={() => removeTarget && remove.mutate(removeTarget)} />
      {!isLoading && !error && !can(me, "cluster:manage") && !clusters.some((item) => item.canViewInventory) ? (
        <ErrorCard message="Cluster administration requires inventory or cluster-management access." />
      ) : isLoading ? (
        <LoadingCard message="Loading clusters…" />
      ) : error ? (
        <ErrorCard message="Couldn't load registered clusters. Try again." onRetry={() => void refetch()} />
      ) : clusters.length === 0 ? (
        <Card><CardContent className="space-y-2 p-6">
          <h2 className="font-medium">No clusters available</h2>
          <p className="text-sm text-muted">{can(me, "cluster:manage") ? "Register a workload cluster to deploy and manage game servers. This panel can run without a local cluster." : "Ask your administrator for access to a registered cluster."}</p>
        </CardContent></Card>
      ) : (
        <>
          <p className="text-sm text-muted">{clusters.length} {clusters.length === 1 ? "cluster" : "clusters"} available · Status reports Kubernetes API connectivity, not game or gateway health.</p>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            {clusters.map((cluster) => (
              <Card key={cluster.name}>
                <CardContent className="flex h-full flex-col gap-4 p-5">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <h2 className="flex items-center gap-2 break-words font-semibold"><Network className="h-4 w-4 shrink-0 text-muted" />{cluster.displayName || cluster.name}</h2>
                      <p className="mt-1 break-all font-mono text-xs text-muted">{cluster.name}</p>
                    </div>
                    {cluster.name === currentCluster && <span className="rounded-full bg-primary/10 px-2 py-1 text-xs text-primary">Selected</span>}
                  </div>
                  <div className="space-y-2 text-sm">
                    <p className={cn("font-medium", cluster.phase === "Healthy" ? "text-success" : cluster.phase === "Unhealthy" ? "text-danger" : "text-muted")}>
                      API: {cluster.phase || "Unknown"}
                    </p>
                    {cluster.name === "local" && <p className="text-muted">Central cluster</p>}
                    {cluster.serverVersion && <p className="text-muted">Kubernetes {cluster.serverVersion}</p>}
                    {cluster.lastCheckTime && <p className="text-muted">Last checked {formatRelative(cluster.lastCheckTime)}</p>}
                    {cluster.message && <p className="break-words text-muted">{cluster.message}</p>}
                  </div>
                  <div className="mt-auto flex flex-wrap gap-2">
                    <Button onPress={() => void openCluster(cluster.name, "/servers")} aria-label={`View servers in ${cluster.displayName || cluster.name}`}>View servers</Button>
                    {cluster.canViewInventory === true && <Button variant="outline" onPress={() => void openCluster(cluster.name, "/cluster")} aria-label={`View nodes in ${cluster.displayName || cluster.name}`}>View nodes</Button>}
                    {cluster.name !== "local" && can(me, "cluster:manage") && <Button variant="outline" onPress={() => setRemoveTarget(cluster.name)} aria-label={`Remove cluster ${cluster.displayName || cluster.name}`}>Remove</Button>}
                  </div>
                </CardContent>
              </Card>
            ))}
          </div>
        </>
      )}
    </div>
  );
}
