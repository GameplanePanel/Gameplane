# Cluster details implementation report

Implemented Task 3 against the approved Overview, Nodes and Configuration Pencil baseline.

- Added `/clusters/$clusterId` page adapter with URL-owned tabs and authoritative registration resolution. Standalone rejects nonexistent local targets. Nodes mounts the shared `NodeInventory` only for `canViewInventory === true`; no remembered cluster mutation occurs.
- Added cluster Overview identity/connectivity and explicit server/node links. Transient registration or installation refresh failures retain loaded tab content and drafts while showing Retry; authentication failure blocks it.
- Added keyed Configuration with local-only kubeconfig download, local/config:read storage settings, destination management and a separate confirmed registration removal section. Late credential responses cannot initiate a download after leaving that target. Successful removal clears only a matching remembered selection, invalidates registry/fleet data and returns to Clusters.
- Extracted destination CRUD with explicit cluster and `gameplane-games` namespace, corresponding permission checks and query keys. Deletion still requires the exact destination name; its failures remain in the confirmation and clear on dismissal. Target changes discard passwords and mutation errors; pending requests retain their captured target.
- Updated directory actions to Open cluster, View servers and capability-gated View nodes. Removed direct directory removal and the inventory-or-manager-only page denial; the backend's registry remains the access boundary.
- Removed backup destinations from panel Settings navigation. `/admin?section=backups` preserves the local Configuration destination on combined installations; standalone uses a valid remembered remote. Missing targets link to the cluster directory. This compatibility view issues no destination request and never assumes a local standalone workload.

Regression sources preserve the previous download/storage and destination assertions in their new owning component tests. Added route-versus-storage targeting, unavailable/unauthorized registration, destination namespace grants, wrong-cluster mutation denial, pending request isolation, download disposal and registration removal coverage. Existing standalone module assertions remain; node inventory/enrollment coverage is owned by the Nodes task.

The root task is preserving discoverability for destination/config-only roles in the existing registry authorization and updating router/sidebar integration. No new endpoint or remote cluster operation was introduced here.

Validation: no local tests or lint suites were run. This checkout has no local dependency installation; `npx tsc --noEmit` could not invoke TypeScript (npx selected the unrelated `tsc` package). Root's Docker `tsc -b` identified unsupported `exact` role-query options in the new tests; those options were removed. Await root's repeat compilation and CI for final verification. No commits made by this subtask.
