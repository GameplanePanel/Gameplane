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

function getDisplayName(cluster: ClusterRegistry | undefined | null, selectedId: string): string {
  if (!cluster) return selectedId;
  return cluster.displayName || cluster.name || "local";
}

export function ClusterSelector() {
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
  const displayName = data && !currentCluster && currentClusterId === "local"
    ? "Select cluster" : getDisplayName(currentCluster, currentClusterId);
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
        aria-label="Select cluster"
        className={cn(
          "flex max-w-[42vw] items-center gap-1.5 rounded-full border border-border bg-card px-3 py-1.5 text-sm sm:max-w-64",
          "text-fg hover:bg-surface transition-colors cursor-pointer",
        )}
      >
        <>
          <span className={cn("h-2 w-2 shrink-0 rounded-full", phaseColor)} aria-hidden="true" />
          <span className="hidden text-muted sm:inline">Cluster:</span>
          <span className="truncate">{displayName}</span>
          <ChevronDown className="h-3.5 w-3.5 text-muted shrink-0" />
        </>
      </DropdownTrigger>

      <DropdownPopover placement="bottom start">
        <DropdownMenu aria-label="Cluster options" className="min-w-[200px]">
          {isLoading || error ? (
            <DropdownItem isDisabled>
              <span className="text-sm text-muted">
                {isLoading ? "Loading…" : "Error loading clusters"}
              </span>
            </DropdownItem>
          ) : clusters.length === 0 ? (
            <DropdownItem onPress={handleViewClusters} textValue="Manage clusters">
              <span className="text-sm text-muted">No clusters available · Manage clusters</span>
            </DropdownItem>
          ) : (
            <>
              <DropdownSection aria-label="Clusters">
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

              <DropdownSection aria-label="Actions">
                <DropdownItem
                  key="view-clusters"
                  onPress={handleViewClusters}
                  textValue="View all clusters"
                  className="flex items-center gap-2"
                >
                  <Network className="h-3.5 w-3.5 text-muted shrink-0" />
                  <span>View all clusters</span>
                </DropdownItem>
              </DropdownSection>
            </>
          )}
        </DropdownMenu>
      </DropdownPopover>
    </Dropdown>
  );
}
