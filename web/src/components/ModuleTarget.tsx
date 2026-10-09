import type { ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Card, CardContent } from "@heroui/react";
import { Clusters } from "@/lib/endpoints";
import { useCurrentCluster } from "@/lib/cluster";
import { useInstallation } from "@/lib/useInstallation";
import { ClusterSelector } from "@/components/ClusterSelector";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { useMe, can } from "@/lib/auth";

// Do not mount workload queries until the installation and target are known.
// Combined installations retain their existing local module catalog.
export function ModuleTarget({ children, loadingMessage = "Loading installation…" }: { children: (cluster: string, canManage: boolean) => ReactNode; loadingMessage?: string }) {
  const installation = useInstallation();
  const identity = useMe();
  const selected = useCurrentCluster();
  const registry = useQuery({
    queryKey: ["clusters"], queryFn: () => Clusters.list(),
    enabled: installation.data?.standalone === true,
  });
  if (installation.isPending) return <LoadingCard message={loadingMessage} />;
  if (installation.isError) return <ErrorCard message="Couldn't load installation capabilities." onRetry={() => void installation.refetch()} />;
  const renderTarget = (cluster: string) => identity.isPending ? <LoadingCard message="Loading permissions…" />
    : can(identity.data, "modules:read", "*", cluster)
      ? children(cluster, can(identity.data, "modules:manage", "*", cluster))
      : <ErrorCard message="You don't have permission to view modules on this cluster." />;
  if (!installation.data.standalone) return renderTarget("local");
  const target = registry.data?.items.find((cluster) => cluster.name === selected);
  return <div className="space-y-4">
    <ClusterSelector />
    {registry.isPending ? <LoadingCard message="Loading clusters…" />
      : registry.isError ? <ErrorCard message="Couldn't load registered clusters." onRetry={() => void registry.refetch()} />
      : target ? renderTarget(target.name)
      : <Card><CardContent className="space-y-2 p-6">
        <h2 className="font-medium">Select a workload cluster</h2>
        <p className="text-sm text-muted">Modules and their sources are managed by the operator on a registered workload cluster.</p>
        <Link to="/clusters" className="text-sm text-primary">Manage clusters</Link>
      </CardContent></Card>}
  </div>;
}
