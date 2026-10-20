import { ChevronDown, Check, Network } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useCurrentCluster } from "@/lib/cluster";
import { useClusterSelection } from "@/lib/useClusterSelection";
import { Clusters } from "@/lib/endpoints";
import type { ClusterRegistry } from "@/types";
import { cn } from "@/lib/utils";
import {
  Dropdown,
  DropdownTrigger,
  DropdownPopover,
  DropdownMenu,
  DropdownItem,
  DropdownSection,
} from "@heroui/react";
import { useOptionalIntl } from "@/lib/i18n/format";

function getPhaseColor(phase: ClusterRegistry["phase"]): string {
  switch (phase) {
    case "Healthy":
      return "bg-success";
    case "Unhealthy":
      return "bg-danger";
    case "Unknown":
    default:
      return "bg-muted";
  }
}

function getDisplayName(cluster: ClusterRegistry | undefined | null, selectedId: string, selectLabel: string): string {
  if (!cluster) return selectedId || selectLabel;
  return cluster.displayName || cluster.name || "local";
}

export function ClusterSelector() {
  const intl = useOptionalIntl();
  const currentClusterId = useCurrentCluster();
  const selectCluster = useClusterSelection();
  const navigate = useNavigate();
  const { data, isLoading, error } = useQuery({
    queryKey: ["clusters"],
    queryFn: () => Clusters.list(),
    refetchInterval: 30_000,
  });

  const clusters = data?.items ?? [];
  const currentCluster = clusters.find((c) => c.name === currentClusterId);
  const selectLabel = intl.formatMessage({ id: "web.components.cluster_selector.select_cluster_label", defaultMessage: "Select cluster", description: "Cluster selector: label shown when no cluster is selected" });
  const displayName = data && !currentCluster && currentClusterId === "local"
    ? selectLabel : getDisplayName(currentCluster, currentClusterId, selectLabel);
  const phase = currentCluster?.phase ?? "Unknown";
  const phaseColor = getPhaseColor(phase);

  const handleViewClusters = (): void => {
    void navigate({ to: "/clusters" });
  };

  return (
    <Dropdown>
      {/* DropdownTrigger itself renders the real `<button>` (react-aria-components'
          Button primitive); a raw `<button>` child here would nest buttons
          (invalid HTML, breaks React's hydration and much of testing-library's
          querying), so the trigger content is a plain `<div>` and the a11y
          label moves onto DropdownTrigger, which forwards it to that button. */}
      <DropdownTrigger
        aria-label={intl.formatMessage({ id: "web.components.cluster_selector.select_cluster", defaultMessage: "Select cluster", description: "Cluster selector: screen-reader label for the cluster switcher button; not visible on screen" })}
        className={cn(
          "flex max-w-[42vw] items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1.5 text-sm sm:max-w-64",
          "text-fg hover:bg-surface transition-colors cursor-pointer",
        )}
      >
        <>
          <span className={cn("h-2 w-2 shrink-0 rounded-full", phaseColor)} aria-hidden="true" />
          <span className="hidden text-muted sm:inline">{intl.formatMessage({ id: "web.components.cluster_selector.cluster", defaultMessage: "Cluster:", description: "Cluster selector: label before the current cluster's name on the switcher button; hidden on small screens" })}</span>
          <span className="truncate">{displayName}</span>
          <ChevronDown className="h-3.5 w-3.5 text-muted shrink-0" />
        </>
      </DropdownTrigger>

      <DropdownPopover placement="bottom start">
        <DropdownMenu aria-label={intl.formatMessage({ id: "web.components.cluster_selector.cluster_options", defaultMessage: "Cluster options", description: "Cluster selector: screen-reader name of the dropdown menu that lists clusters and actions; not visible on screen" })} className="min-w-[200px]">
          {isLoading || error ? (
            <DropdownItem isDisabled>
              <span className="text-sm text-muted">
                {isLoading ? intl.formatMessage({ id: "web.components.cluster_selector.loading", defaultMessage: "Loading…", description: "Cluster selector: text while the cluster list loads" }) : intl.formatMessage({ id: "web.components.cluster_selector.error_loading", defaultMessage: "Error loading clusters", description: "Cluster selector: text shown when the cluster list fails to load" })}
              </span>
            </DropdownItem>
          ) : clusters.length === 0 ? (
            <DropdownItem onPress={handleViewClusters} textValue={intl.formatMessage({ id: "web.components.cluster_selector.manage_clusters", defaultMessage: "Manage clusters", description: "Cluster selector: typeahead text of the manage-clusters item" })}>
              <span className="text-sm text-muted">{intl.formatMessage({ id: "web.components.cluster_selector.no_clusters_available_manage_clusters", defaultMessage: "No clusters available · Manage clusters", description: "Cluster selector: dropdown message shown when no clusters exist, followed by the words 'Manage clusters' in the same text" })}</span>
            </DropdownItem>
          ) : (
            <>
              <DropdownSection aria-label={intl.formatMessage({ id: "web.components.cluster_selector.clusters", defaultMessage: "Clusters", description: "Cluster selector: screen-reader name of the dropdown section that holds the cluster choices; not visible on screen" })}>
                {clusters.map((cluster) => (
                  <DropdownItem
                    key={cluster.name}
                    onPress={() => void selectCluster(cluster.name)}
                    textValue={cluster.displayName || cluster.name}
                    className="flex items-center gap-2"
                  >
                    <div className="flex items-center gap-2 flex-1">
                      <span
                        className={cn("h-2 w-2 rounded-full", getPhaseColor(cluster.phase))}
                      />
                      <span>{cluster.displayName || cluster.name}</span>
                    </div>
                    {cluster.name === currentClusterId && (
                      <Check className="h-3.5 w-3.5 text-primary shrink-0" />
                    )}
                  </DropdownItem>
                ))}
              </DropdownSection>

              <DropdownSection aria-label={intl.formatMessage({ id: "web.components.cluster_selector.actions", defaultMessage: "Actions", description: "Cluster selector: screen-reader name of the dropdown section that holds the 'View all clusters' action; not visible on screen" })}>
                <DropdownItem
                  key="view-clusters"
                  onPress={handleViewClusters}
                  textValue={intl.formatMessage({ id: "web.components.cluster_selector.view_all_clusters_text", defaultMessage: "View all clusters", description: "Cluster selector: typeahead text of the view-all-clusters item" })}
                  className="flex items-center gap-2"
                >
                  <Network className="h-3.5 w-3.5 text-muted shrink-0" />
                  <span>{intl.formatMessage({ id: "web.components.cluster_selector.view_all_clusters", defaultMessage: "View all clusters", description: "Cluster selector: dropdown menu item that navigates to the clusters view; keep it short" })}</span>
                </DropdownItem>
              </DropdownSection>
            </>
          )}
        </DropdownMenu>
      </DropdownPopover>
    </Dropdown>
  );
}
