import { describe, expect, it } from "vitest";
import { flattenNodes, filterNodes, nodeMeasurement, sortNodes, summarizeNodes } from "./nodes";
import type { FleetInventory } from "./fleet";

const inventory: FleetInventory[] = [
  { cluster: "east", name: "East", stats: {}, view: { nodes: [{ name: "same", status: "Ready", cpu: { used: 0, capacity: 4 } }] } },
  { cluster: "west", name: "West", stats: {}, view: { nodes: [{ name: "same", status: "NotReady", cpu: { used: 2, capacity: 4 } }, { name: "unknown", cpu: { capacity: 8 } }] } },
];

describe("node inventory", () => {
  it("keeps names intact and separates identities by cluster", () => {
    const rows = flattenNodes(inventory);
    expect(rows.map((row) => row.node.name)).toEqual(["same", "same", "unknown"]);
    expect(new Set(rows.map((row) => row.key)).size).toBe(3);
  });
  it("searches cluster, name, roles and labels and combines status", () => {
    expect(filterNodes(flattenNodes(inventory), "WEST", "NotReady").map((row) => row.cluster)).toEqual(["west"]);
    expect(filterNodes(flattenNodes(inventory), "", "Unknown")).toHaveLength(1);
    expect(filterNodes(flattenNodes([{ ...inventory[0], view: { nodes: [{ name: "worker", roles: ["control-plane"], labels: ["zone=north"] }] } }]), "north", "")).toHaveLength(1);
  });
  it("keeps unknown values last in either numeric sort direction", () => {
    const rows = flattenNodes(inventory);
    expect(sortNodes(rows, "cpu", "ascending").map((row) => row.cluster)).toEqual(["east", "west", "west"]);
    expect(sortNodes(rows, "cpu", "descending").map((row) => row.node.name)).toEqual(["same", "same", "unknown"]);
    expect(sortNodes(rows, "cpu", "descending")[0].cluster).toBe("west");
  });
  it("distinguishes zero from absent, negative, nonfinite and invalid capacity readings", () => {
    expect(nodeMeasurement({ used: 0, capacity: 4 })).toBe(0);
    for (const metric of [{ capacity: 4 }, { used: -1, capacity: 4 }, { used: Infinity, capacity: 4 }, { used: 1, capacity: 0 }, { used: 1, capacity: Infinity }]) expect(nodeMeasurement(metric)).toBeUndefined();
  });
  it("summarizes visible returned nodes without calling missing readings idle", () => {
    const rows = flattenNodes(inventory);
    expect(summarizeNodes(rows)).toMatchObject({ ready: 1, total: 3, clusters: 2, cpu: undefined });
    expect(summarizeNodes(rows.slice(0, 2)).cpu).toBe(25);
  });
});
