import { NodeInventory } from "@/components/nodes/NodeInventory";
import { useFleetLocation } from "@/lib/fleet";

export function NodesPage() {
  const [clusterId, onClusterChange] = useFleetLocation();
  return <NodeInventory clusterId={clusterId} onClusterChange={onClusterChange} />;
}
