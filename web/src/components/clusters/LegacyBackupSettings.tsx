import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Clusters } from "@/lib/endpoints";
import { useCurrentCluster } from "@/lib/cluster";
import { useInstallation } from "@/lib/useInstallation";
import { useOptionalIntl } from "@/lib/i18n/format";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { ClusterSection } from "./ClusterSection";

/** Preserve saved /admin?section=backups URLs without issuing workload requests. */
export function LegacyBackupSettings() {
  const intl = useOptionalIntl();
  const selected = useCurrentCluster();
  const installation = useInstallation();
  const registry = useQuery({ queryKey: ["clusters"], queryFn: () => Clusters.list() });
  if (installation.isPending || registry.isPending) return <LoadingCard message={intl.formatMessage({ id: "web.cluster_detail.loading", defaultMessage: "Loading cluster…", description: "Cluster detail loading state" })} />;
  if (installation.isError) return <ErrorCard message={intl.formatMessage({ id: "web.routes.cluster.couldn_t_load_installation_capabilities", defaultMessage: "Couldn't load installation capabilities.", description: "Cluster: error message shown when the installation capabilities cannot be fetched; a retry button appears below it" })} onRetry={() => void installation.refetch()} />;
  if (registry.isError) return <ErrorCard message={intl.formatMessage({ id: "web.routes.cluster.couldn_t_load_registered_clusters", defaultMessage: "Couldn't load registered clusters.", description: "Cluster: error message shown when the list of registered clusters fails to load; a retry button appears below it" })} onRetry={() => void registry.refetch()} />;
  // The legacy combined-installation screen always owned local destinations.
  // Only standalone installations used the remembered workload selection.
  const targetId = !installation.data.standalone && installation.data.localCluster
    ? "local"
    : installation.data.standalone && selected !== "local" ? selected : "";
  const target = registry.data.items.find((cluster) => cluster.name === targetId);
  return <ClusterSection title={intl.formatMessage({ id: "web.cluster_configuration.backups_moved", defaultMessage: "Backup destinations moved", description: "Legacy admin backup destination deep link title" })} subtitle={intl.formatMessage({ id: "web.cluster_configuration.backups_moved_description", defaultMessage: "Backup destinations belong to each cluster. Open the cluster's Configuration tab to manage them.", description: "Legacy backup settings guidance" })}>
    {target ? <Link to="/clusters/$clusterId" params={{ clusterId: target.name }} search={{ tab: "configuration" }} className="text-sm text-primary">{intl.formatMessage({ id: "web.cluster_configuration.open_named_configuration", defaultMessage: "Open {name} configuration", description: "Legacy backup settings link to selected cluster configuration" }, { name: target.displayName || target.name })}</Link> : <Link to="/clusters" className="text-sm text-primary">{intl.formatMessage({ id: "web.cluster_configuration.choose_cluster", defaultMessage: "Choose a cluster", description: "Legacy backup settings link to cluster directory when no valid target exists" })}</Link>}
  </ClusterSection>;
}
