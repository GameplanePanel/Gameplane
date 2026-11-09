import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Button } from "@heroui/react";
import { Download } from "lucide-react";
import { api, APIError } from "@/lib/api";
import { Cluster, Clusters } from "@/lib/endpoints";
import { can, useMe } from "@/lib/auth";
import { getCurrentCluster, setCurrentCluster } from "@/lib/cluster";
import { useInstallation } from "@/lib/useInstallation";
import { useOptionalIntl } from "@/lib/i18n/format";
import type { AllConfig } from "@/lib/config";
import type { ClusterRegistry } from "@/types";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { ErrorCard } from "@/components/ui/ErrorCard";
import { LoadingCard } from "@/components/ui/LoadingCard";
import { BackupDestinationsPanel } from "./BackupDestinationsPanel";
import { ClusterSection } from "./ClusterSection";

const CLUSTER_OPS_KEY = "clusterOps.enabled";

export function ClusterConfiguration({ cluster }: { cluster: ClusterRegistry }) {
  return <ConfigurationTarget key={cluster.name} cluster={cluster} />;
}

function ConfigurationTarget({ cluster }: { cluster: ClusterRegistry }) {
  const intl = useOptionalIntl();
  const identity = useMe();
  const installation = useInstallation();
  const client = useQueryClient();
  const navigate = useNavigate();
  const active = useRef(true);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);
  const [removeOpen, setRemoveOpen] = useState(false);
  const isLocal = cluster.name === "local" && installation.data?.localCluster === true;
  const canManage = can(identity.data, "cluster:manage");
  const canReadConfig = isLocal && can(identity.data, "config:read");
  const info = useQuery({ queryKey: ["cluster-info", cluster.name], queryFn: ({ signal }) => Cluster.info(cluster.name, signal), enabled: isLocal && cluster.canViewInventory === true });
  const config = useQuery({ queryKey: ["config"], queryFn: ({ signal }) => api<AllConfig>("/admin/config", { signal }), enabled: canReadConfig });
  // Older APIs omit clusterOps. Preserve server-side 501 as the backstop.
  const opsDisabled = !isLocal || !canManage || info.data?.clusterOps === false;
  const download = useMutation({
    mutationFn: () => Cluster.kubeconfig(cluster.name),
    onSuccess: (blob) => {
      if (!active.current) return;
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      try {
        anchor.href = url;
        anchor.download = "gameplane-kubeconfig.yaml";
        document.body.appendChild(anchor);
        anchor.click();
      } finally {
        anchor.remove();
        URL.revokeObjectURL(url);
      }
    },
  });
  const remove = useMutation({
    mutationFn: () => Clusters.remove(cluster.name),
    onSuccess: async () => {
      if (getCurrentCluster() === cluster.name) setCurrentCluster("");
      await client.invalidateQueries({ queryKey: ["clusters"] });
      await client.invalidateQueries({ queryKey: ["fleet"] });
      if (active.current) { setRemoveOpen(false); await navigate({ to: "/clusters" }); }
    },
  });

  if (installation.isPending || identity.isPending) return <LoadingCard message={intl.formatMessage({ id: "web.cluster_configuration.loading", defaultMessage: "Loading cluster configuration…", description: "Cluster configuration loading state" })} />;
  const installationFailure = <ErrorCard message={intl.formatMessage({ id: "web.routes.cluster.couldn_t_load_installation_capabilities", defaultMessage: "Couldn't load installation capabilities.", description: "Cluster: error message shown when the installation capabilities cannot be fetched; a retry button appears below it" })} onRetry={() => void installation.refetch()} />;
  if (installation.isError && (!installation.data || (installation.error instanceof APIError && installation.error.status === 401))) return installationFailure;
  if (identity.isError && (!identity.data || (identity.error instanceof APIError && identity.error.status === 401))) return <ErrorCard message={intl.formatMessage({ id: "web.cluster_configuration.permissions_error", defaultMessage: "Couldn't load your permissions.", description: "Cluster configuration: identity loading failure" })} onRetry={() => void identity.refetch()} />;

  const errorText = download.error instanceof APIError && download.error.status === 501
    ? intl.formatMessage({ id: "web.routes.cluster.op_not_enabled", defaultMessage: "Cluster operations aren't enabled on this install (set clusterOps.enabled).", description: "Cluster: error shown when the operator has cluster operations turned off (HTTP 501)" })
    : download.error instanceof APIError && download.error.status === 403
      ? intl.formatMessage({ id: "web.routes.cluster.op_forbidden", defaultMessage: "You don't have permission to do that.", description: "Cluster: error shown when the user lacks permission for a cluster operation (HTTP 403)" })
      : intl.formatMessage({ id: "web.routes.cluster.op_failed", defaultMessage: "That operation failed. Check the API logs.", description: "Cluster: generic error shown when a cluster operation fails" });

  return <div className="space-y-6">
    {installation.isError && installationFailure}
    <ClusterSection title={intl.formatMessage({ id: "web.cluster_configuration.access", defaultMessage: "Access", description: "Cluster configuration access section" })} subtitle={intl.formatMessage({ id: "web.cluster_configuration.access_description", defaultMessage: "Download credentials for Kubernetes access when this cluster supports it.", description: "Cluster access section description" })}>
      <Button variant="outline" isDisabled={opsDisabled || download.isPending} onPress={() => download.mutate()}><Download className="h-4 w-4" />{intl.formatMessage({ id: "web.routes.cluster.download_kubeconfig", defaultMessage: "Download kubeconfig", description: "Cluster: button that downloads the cluster's kubeconfig file; keep it short; kubeconfig is a technical term, usually left untranslated" })}</Button>
      {!isLocal ? <p className="text-sm text-muted">{intl.formatMessage({ id: "web.cluster_configuration.remote_access", defaultMessage: "Kubeconfig downloads and install-time storage settings are managed at this remote cluster.", description: "Cluster configuration: unsupported remote access and storage guidance" })}</p>
        : !canManage ? <p className="text-sm text-muted">{intl.formatMessage({ id: "web.cluster_configuration.access_permission", defaultMessage: "Cluster management permission is required to download a kubeconfig.", description: "Cluster access permission hint" })}</p>
          : info.data?.clusterOps === false && <p className="text-sm text-muted">{intl.formatMessage({ id: "web.cluster_configuration.enable_ops", defaultMessage: "Enable {setting} in the Helm values to download kubeconfigs.", description: "Disabled local kubeconfig capability hint" }, { setting: <code>{CLUSTER_OPS_KEY}</code> })}</p>}
      {download.error && <p role="alert" className="text-sm text-danger">{errorText}</p>}
    </ClusterSection>
    {canReadConfig && config.isPending && <LoadingCard message={intl.formatMessage({ id: "web.cluster_configuration.loading_storage", defaultMessage: "Loading storage settings…", description: "Cluster storage loading state" })} />}
    {canReadConfig && config.isError && <ErrorCard message={intl.formatMessage({ id: "web.cluster_configuration.storage_error", defaultMessage: "Couldn't load storage settings.", description: "Cluster storage read failure" })} onRetry={() => void config.refetch()} />}
    {canReadConfig && config.data?.installTimeSettings && <StorageSection settings={config.data.installTimeSettings} />}
    <BackupDestinationsPanel clusterId={cluster.name} />
    {cluster.name !== "local" && canManage && <ClusterSection className="border border-danger/30" title={intl.formatMessage({ id: "web.cluster_configuration.remove_registration", defaultMessage: "Remove registration", description: "Cluster removal section title" })} subtitle={intl.formatMessage({ id: "web.routes.clusters.removes_this_panel_s_connection_to_the", defaultMessage: "Removes this panel's connection to the cluster. Running workloads remain on the cluster.", description: "Clusters: text in the remove-cluster confirmation dialog saying the panel's connection is removed while running workloads stay on the cluster" })}>
      <Button variant="danger" onPress={() => { remove.reset(); setRemoveOpen(true); }} aria-label={intl.formatMessage({ id: "web.routes.clusters.remove_cluster_named", defaultMessage: "Remove cluster {name}", description: "Clusters: accessible label of the Remove button; {name} is the cluster display name" }, { name: cluster.displayName || cluster.name })}>{intl.formatMessage({ id: "web.cluster_configuration.remove_registration", defaultMessage: "Remove registration", description: "Cluster removal section title" })}</Button>
    </ClusterSection>}
    <ConfirmDialog open={removeOpen} onOpenChange={(open) => { if (!open && !remove.isPending) { setRemoveOpen(false); remove.reset(); } }} title={intl.formatMessage({ id: "web.routes.clusters.remove_title", defaultMessage: "Remove {name}?", description: "Clusters: title of the remove-cluster confirmation; {name} is the cluster name" }, { name: cluster.displayName || cluster.name })}
      description={<div className="space-y-3"><p>{intl.formatMessage({ id: "web.routes.clusters.removes_this_panel_s_connection_to_the", defaultMessage: "Removes this panel's connection to the cluster. Running workloads remain on the cluster.", description: "Clusters: text in the remove-cluster confirmation dialog saying the panel's connection is removed while running workloads stay on the cluster" })}</p>{remove.error && <p role="alert" className="text-danger">{intl.formatMessage({ id: "web.routes.clusters.couldn_t_remove_the_cluster", defaultMessage: "Couldn't remove the cluster registration. Try again.", description: "Clusters: error message in the remove-cluster dialog shown when removing the registration fails; the user can try again" })}</p>}</div>}
      confirmLabel={intl.formatMessage({ id: "web.routes.clusters.remove_cluster", defaultMessage: "Remove cluster", description: "Clusters: confirm button in the remove-cluster dialog, shown in a destructive style; keep it short" })} destructive busy={remove.isPending} onConfirm={() => cluster.name !== "local" && canManage && remove.mutate()} />
  </div>;
}

function StorageSection({ settings }: { settings: NonNullable<AllConfig["installTimeSettings"]> }) {
  const intl = useOptionalIntl();
  return <ClusterSection title={intl.formatMessage({ id: "web.routes.cluster.storage", defaultMessage: "Storage", description: "Cluster: section heading for the storage settings that are fixed at install time; shown only to users who can read the cluster configuration" })} subtitle={intl.formatMessage({ id: "web.routes.cluster.set_at_install_time_via_helm_values_not", defaultMessage: "Set at install time via Helm values — not editable from the dashboard.", description: "Cluster: hint under the storage heading explaining that these values come from Helm values at install time and cannot be edited in the dashboard" })}>
    <div className="flex flex-wrap items-start justify-between gap-4"><div className="min-w-0 flex-1 space-y-1"><div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.cluster.game_data_storage_class", defaultMessage: "Game data storage class", description: "Cluster: label of the setting that names the storage class used for new game server data volumes; storage class is a technical term, usually left untranslated" })}</div><p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.cluster.storageclass_for_new_gameservers_game", defaultMessage: "StorageClass for new GameServers' game-data volumes — set by the operator.gameDataStorage.storageClassName Helm value and applied by the operator to every new PVC; the API is passed the same value as --game-data-storage-class only to report it here.", description: "Cluster: explanation under the game data storage class label; a Helm value sets it and it applies to new volumes; StorageClass is a technical term, usually left untranslated" })} {settings.gameDataStorageClass === "" && intl.formatMessage({ id: "web.routes.cluster.storage_class_unset", defaultMessage: "Left unset, so new volumes use the cluster's default StorageClass.", description: "Cluster: note under the StorageClass description when no StorageClass is set" })}</p></div><span className="break-all rounded bg-surface px-3 py-1 font-mono text-sm">{settings.gameDataStorageClass || intl.formatMessage({ id: "web.routes.cluster.cluster_default", defaultMessage: "Cluster default", description: "Cluster: badge shown instead of a storage class name when none is set, meaning new volumes use the cluster's default; keep it short" })}</span></div>
  </ClusterSection>;
}
