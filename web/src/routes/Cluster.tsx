import { Navigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useCurrentCluster } from "@/lib/cluster";
import { Clusters } from "@/lib/endpoints";
import { useInstallation } from "@/lib/useInstallation";
import { LoadingCard } from "@/components/ui/LoadingCard";

/** Preserve old bookmarks without making remembered selection the fleet default. */
export function ClusterPage() {
  const cluster = useCurrentCluster();
  const installation = useInstallation();
  const registry = useQuery({ queryKey: ["clusters"], queryFn: () => Clusters.list() });
  if (installation.isPending || registry.isPending) return <LoadingCard />;
  const available = registry.data?.items.some((item) => item.name === cluster && item.canViewInventory === true)
    && (cluster !== "local" || installation.data?.localCluster === true);
  return <Navigate to="/nodes" search={{ cluster: available ? cluster : undefined }} replace />;
}
