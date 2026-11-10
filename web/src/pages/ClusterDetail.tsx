import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Button, Tab, Tabs } from "@heroui/react";
import { defineMessages } from "react-intl";
import { Clusters } from "@/lib/endpoints";
import { APIError } from "@/lib/api";
import { useInstallation } from "@/lib/useInstallation";
import { useOptionalIntl } from "@/lib/i18n/format";
import { formatRelative } from "@/lib/utils";
import type { ClusterRegistry } from "@/types";
import { PageHeader } from "@/components/ui/PageHeader";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { NodeInventory } from "@/components/nodes/NodeInventory";
import { ClusterConfiguration } from "@/components/clusters/ClusterConfiguration";
import { ClusterSection } from "@/components/clusters/ClusterSection";

const tabs = defineMessages({
  overview: { id: "web.cluster_detail.overview", defaultMessage: "Overview", description: "Cluster detail overview tab" },
  nodes: { id: "web.cluster_detail.nodes", defaultMessage: "Nodes", description: "Cluster detail nodes tab" },
  configuration: { id: "web.cluster_detail.configuration", defaultMessage: "Configuration", description: "Cluster detail configuration tab" },
});
type ClusterTab = keyof typeof tabs;
const unauthorized = (error: unknown) => error instanceof APIError && error.status === 401;

export function ClusterDetailPage() {
  const { clusterId } = useParams({ from: "/app-layout/clusters/$clusterId" });
  const { tab = "overview" } = useSearch({ from: "/app-layout/clusters/$clusterId" });
  const intl = useOptionalIntl();
  const navigate = useNavigate();
  const installation = useInstallation();
  const registry = useQuery({ queryKey: ["clusters"], queryFn: () => Clusters.list(), refetchInterval: 30_000 });
  if (installation.isPending || registry.isPending) return <LoadingCard message={intl.formatMessage({ id: "web.cluster_detail.loading", defaultMessage: "Loading cluster…", description: "Cluster detail loading state" })} />;
  const installationFailure = <ErrorCard message={intl.formatMessage({ id: "web.routes.cluster.couldn_t_load_installation_capabilities", defaultMessage: "Couldn't load installation capabilities.", description: "Cluster: error message shown when the installation capabilities cannot be fetched; a retry button appears below it" })} onRetry={() => void installation.refetch()} />;
  if (installation.isError && (!installation.data || unauthorized(installation.error))) return installationFailure;
  const registryFailure = <ErrorCard message={intl.formatMessage({ id: "web.routes.cluster.couldn_t_load_registered_clusters", defaultMessage: "Couldn't load registered clusters.", description: "Cluster: error message shown when the list of registered clusters fails to load; a retry button appears below it" })} onRetry={() => void registry.refetch()} />;
  if (registry.isError && (!registry.data || unauthorized(registry.error))) return registryFailure;
  const cluster = registry.data?.items.find((item) => item.name === clusterId && (item.name !== "local" || installation.data?.localCluster));
  if (!cluster) return <div className="space-y-4 p-6"><ErrorCard message={intl.formatMessage({ id: "web.cluster_detail.unavailable", defaultMessage: "Cluster unavailable", description: "Cluster detail requested registration is absent or inaccessible" })} /><Button variant="outline" onPress={() => void navigate({ to: "/clusters" })}>{intl.formatMessage({ id: "web.cluster_detail.back_to_clusters", defaultMessage: "Back to clusters", description: "Return to cluster directory" })}</Button></div>;
  const selected = tab in tabs ? tab as ClusterTab : "overview";
  const clusterName = cluster.displayName || cluster.name;
  return <div className="min-w-0 space-y-6 p-4 sm:p-6">
    {installation.isError && installationFailure}
    {registry.isError && registryFailure}
    <PageHeader title={clusterName} description={intl.formatMessage({ id: "web.cluster_detail.description", defaultMessage: "Cluster connectivity, nodes and configuration.", description: "Cluster detail page description" })} actions={<Button variant="outline" onPress={() => void navigate({ to: "/servers", search: { cluster: cluster.name } })}>{intl.formatMessage({ id: "web.routes.clusters.view_servers", defaultMessage: "View servers", description: "Clusters: button on a cluster card that opens the server list for that cluster; keep it short; its screen-reader name also includes the cluster name" })}</Button>} />
    <Tabs selectedKey={selected} onSelectionChange={(key) => void navigate({ to: "/clusters/$clusterId", params: { clusterId }, search: { tab: String(key) as ClusterTab } })}>
      <Tabs.List aria-label={intl.formatMessage({ id: "web.cluster_detail.tabs", defaultMessage: "Cluster detail tabs", description: "Accessible cluster tab navigation label" })}>{(Object.keys(tabs) as ClusterTab[]).map((key) => <Tab key={key} id={key}>{intl.formatMessage(tabs[key])}</Tab>)}</Tabs.List>
    </Tabs>
    {selected === "overview" && <ClusterOverview cluster={cluster} />}
    {selected === "nodes" && (cluster.canViewInventory === true ? <div className="space-y-4"><div className="flex flex-wrap items-center justify-between gap-3"><p className="text-sm text-muted">{intl.formatMessage({ id: "web.cluster_detail.fixed_cluster", defaultMessage: "Cluster: {name}", description: "Fixed cluster scope label above embedded nodes" }, { name: clusterName })}</p><Button variant="outline" onPress={() => void navigate({ to: "/nodes", search: {} })}>{intl.formatMessage({ id: "web.cluster_detail.all_nodes", defaultMessage: "View all nodes", description: "Link from cluster nodes to fleet nodes" })}</Button></div><NodeInventory key={cluster.name} clusterId={cluster.name} embedded /></div> : <ErrorCard message={intl.formatMessage({ id: "web.cluster_detail.inventory_denied", defaultMessage: "You don't have permission to view node inventory on this cluster.", description: "Cluster node inventory access denied" })} />)}
    {selected === "configuration" && <ClusterConfiguration key={cluster.name} cluster={cluster} />}
  </div>;
}

function ClusterOverview({ cluster }: { cluster: ClusterRegistry }) {
  const intl = useOptionalIntl();
  const navigate = useNavigate();
  const empty = intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" });
  const phase = cluster.phase === "Healthy" ? intl.formatMessage({ id: "web.routes.clusters.phase_healthy", defaultMessage: "Healthy", description: "Clusters: API status label for a cluster the panel reports as healthy" }) : cluster.phase === "Unhealthy" ? intl.formatMessage({ id: "web.routes.clusters.phase_unhealthy", defaultMessage: "Unhealthy", description: "Clusters: API status label for a cluster the panel reports as unhealthy" }) : cluster.phase || intl.formatMessage({ id: "web.routes.clusters.phase_unknown", defaultMessage: "Unknown", description: "Clusters: API status shown when the cluster reports no phase" });
  return <div className="grid gap-6 lg:grid-cols-2"><ClusterSection title={intl.formatMessage({ id: "web.cluster_detail.identity", defaultMessage: "Cluster identity", description: "Cluster overview identity section" })}>
    <dl className="space-y-4 text-sm"><div><dt className="text-muted">{intl.formatMessage({ id: "web.cluster_detail.registration_id", defaultMessage: "Registration ID", description: "Cluster registration identifier label" })}</dt><dd className="break-all pt-1 font-mono">{cluster.name}</dd></div><div><dt className="text-muted">{intl.formatMessage({ id: "web.cluster_detail.kubernetes_version", defaultMessage: "Kubernetes version", description: "Cluster Kubernetes version label" })}</dt><dd className="pt-1 font-mono">{cluster.serverVersion || empty}</dd></div></dl>
    {cluster.name === "local" && <p className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.clusters.central_cluster", defaultMessage: "Central cluster", description: "Clusters: note on the cluster card of the built-in local cluster that the panel itself runs in" })}</p>}
    {cluster.canViewInventory === true && <Button variant="outline" onPress={() => void navigate({ to: "/nodes", search: { cluster: cluster.name } })}>{intl.formatMessage({ id: "web.routes.clusters.view_nodes", defaultMessage: "View nodes", description: "Clusters: button on a cluster card that opens the node inventory for that cluster; shown only when the user can view inventory; keep it short" })}</Button>}
  </ClusterSection><ClusterSection title={intl.formatMessage({ id: "web.cluster_detail.connectivity", defaultMessage: "API connectivity", description: "Cluster overview connectivity section" })} subtitle={intl.formatMessage({ id: "web.cluster_detail.connectivity_description", defaultMessage: "Status reports Kubernetes API connectivity, not game or gateway health.", description: "Cluster health scope explanation" })}>
    <p className={cluster.phase === "Healthy" ? "font-medium text-success" : cluster.phase === "Unhealthy" ? "font-medium text-danger" : "font-medium text-muted"}>{phase}</p>
    {cluster.message && <p className="break-words text-sm text-muted">{cluster.message}</p>}
    <dl className="text-sm"><dt className="text-muted">{intl.formatMessage({ id: "web.routes.clusters.last_checked", defaultMessage: "Last checked", description: "Clusters: label before the time of the last health check on a cluster card; the relative time follows it" })}</dt><dd className="pt-1">{cluster.lastCheckTime ? formatRelative(cluster.lastCheckTime, intl) : empty}</dd></dl>
  </ClusterSection></div>;
}
