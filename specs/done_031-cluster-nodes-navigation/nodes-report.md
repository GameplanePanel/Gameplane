# Shared Nodes implementation report

Implemented `NodesPage`, shared `NodeInventory`, target-keyed `NodeEnrollment`, and pure flatten/filter/sort/summary helpers. Fleet queries preserve explicit cluster scope and the global authorized scope list. Inventory uses composite cluster/node identities, responsive table or stacked rows, search/status/cluster filters, all seven sortable columns, capacity-weighted summaries, unknown metrics, and visible partial/error coverage.

Enrollment from All clusters requires a target choice before any mutation. Remote targets show guidance; local joins require installation capabilities, cluster-management permission, and enabled operations. Route or target changes discard commands and pending UI results. Older APIs retain the existing capability fallback. No direct local capability request occurs before a local workload is confirmed.

Regression sources were written before implementation. No local test or lint suite was run. Root is running the permitted TypeScript container compile; its initial compile identified React Intl descriptor inference errors, resolved using the existing `MessageDescriptor` registry pattern. Final compile and browser verification are recorded by the integration owner.

## Legacy coverage migration

- `routes/Cluster.test.tsx` inventory cases moved to `components/nodes/NodeInventory.test.tsx`: late local response isolation, node fields/roles/labels/status/uptime, unknown versus zero, multiple rows, filtered summaries, empty/error/retry behavior.
- Enrollment cases moved to `components/nodes/NodeEnrollment.test.tsx`: command generation, 501/403/500 handling, retry clears errors, copy/dismiss, disabled capabilities, old API fallback, local-result disposal, remote guidance, standalone and permissions.
- Download/storage cases moved by the configuration agent to `components/clusters/ClusterConfiguration.test.tsx`.
- Old grid/card selectors and the single-cluster version subtitle were replaced by table assertions and filtered summaries. Node roles are displayed only when reported, rather than inventing a worker role. Cluster version belongs to the detail Overview.
- `lib/nodes.test.ts` covers identities, combined search/status, numeric ordering with unknown values last, invalid/zero measurements, and weighted summaries.

Migrated E2E sources: `cluster.spec.ts`, `multicluster-ui.spec.ts`, `shell.spec.ts`, `screenshots.spec.ts`, `errorHandling.spec.ts`, and live `dataScreens.spec.ts` / `login-and-shell.spec.ts`. They cover Nodes navigation, all/filtered clusters, responsive bounds, local enrollment versus per-cluster configuration, legacy `/cluster` redirect, unchanged remembered selection, and inventory failure coverage. API/cache/privacy tests retain `/cluster` where it denotes the backend endpoint. E2E suites remain CI-only.

No commits were made by this task owner.
