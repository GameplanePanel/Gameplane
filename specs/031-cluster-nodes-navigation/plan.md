# Cluster and node navigation implementation plan

**Goal:** Implement the approved cluster details and all-cluster Nodes experience and run the branch locally.

**Architecture:** Reuse existing fleet inventory and cluster endpoints. One shared NodeInventory component serves the Nodes page and cluster detail Nodes tab. Extract target-bound configuration panels from legacy Cluster and AdminSettings screens.

**Tech Stack:** React 19, TypeScript, TanStack Query/Router, HeroUI, react-intl, Vitest/MSW regression sources, Docker/Vite mock demo.

**Spec:** [spec.md](spec.md)

## Global constraints

- Design visual changes in Pencil before implementing them; preserve theme tokens and export touched designs.
- No local test or lint suites; compile/build and interactive inspection only, suites in CI.
- Reuse existing endpoints; extend registration metadata discovery only for existing destination/local configuration permissions. No new dependency required.
- Every workload operation captures its explicit cluster; standalone never invents local.
- Use human Git author and signed/sign-off commits; no AI attribution.
- Branch `feat/cluster-nodes-navigation`, push to `Baylem/Gameplane`, never merge.

## Review focus

- Duplicate node names in different clusters must not collapse or cross-target links.
- Unavailable/truncated clusters remain filterable and coverage is visibly incomplete.
- Missing/invalid metrics differ from measured zero, including sort behavior.
- Cluster changes while an enrollment/destination request is pending cannot leak results or drafts.
- Remote-only permissions, standalone emptiness and unsupported operations retain their boundaries.

## Task 1: Canonical Pencil designs

- [x] Update Sidebar and Settings nav and cluster/location terminology.
- [x] Design fleet Nodes desktop/mobile, cluster Overview / Nodes / Configuration and directory entry paths.
- [x] Inspect screenshots and layout bounds; export touched roots as PNG and JSON, update manifest.
- [x] Save design.pen through Pen and verify its Git status.

## Task 2: Shared nodes inventory

Files: new `web/src/pages/Nodes.tsx`, `web/src/components/nodes/NodeInventory.tsx`, `web/src/components/nodes/NodeEnrollment.tsx`, `web/src/lib/nodes.ts`, colocated tests.

Interfaces:
- `NodesPage()` reads useFleetLocation and renders `NodeInventory`.
- `NodeInventory({clusterId?, onClusterChange?, embedded?})`: omitted/empty cluster means all; fixed embedded cluster has no global filter. `onClusterChange(value: string)` updates the Nodes URL. Export for ClusterDetail.
- `NodeEnrollment({clusterId?: string})`: explicit target selection for all-cluster action, local-only join, permission checks, keyed results on selection changes.
- Fleet `inventory(filter?: {cluster?: string}, signal?: AbortSignal)` and result scopes/issues are existing APIs.

- [x] Write regression sources before UI implementation for duplicate names, all/default and exact-cluster requests, search/status/sort, partial inventory, unknown vs zero and target-bound enrollment.
- [x] Implement flatten/filter/sort helpers without rewriting node names.
- [x] Translate approved design to shared responsive table, summaries, controls, coverage, retry and empty states.
- [x] Reuse target-bound Add node and operation error behavior; preserve disabled/remote semantics and copy command behavior.
- [x] Compile new sources; report validation without running suites.

## Task 3: Cluster details and configuration

Files: new `web/src/pages/ClusterDetail.tsx`, `web/src/components/clusters/ClusterConfiguration.tsx`, `web/src/components/clusters/BackupDestinationsPanel.tsx`, update `web/src/routes/Clusters.tsx`, `web/src/routes/Cluster.tsx`, `web/src/routes/AdminSettings.tsx`, `web/src/components/ui/SettingsNav.tsx`, colocated tests.

Interfaces:
- `ClusterDetailPage()` reads `/clusters/$clusterId` and search `tab?: overview|nodes|configuration`; explicit registry identity only.
- `ClusterConfiguration({cluster: ClusterRegistry})` preserves local capability/permission limits and separate removal confirmation.
- `BackupDestinationsPanel({clusterId: string})` captures exact target for CRUD and queries `['backup-destinations', clusterId]`.
- Consume shared `NodeInventory({clusterId, embedded:true})` from task 2.

- [x] Write/relocate behavioral tests for configuration, enrollment/download errors, destination target/permissions, delete failure, explicit standalone selection and stale response isolation.
- [x] Implement cluster detail shell, overview and shared Nodes tab.
- [x] Extract kubeconfig/storage/destination controls; remove the legacy controls from old screens.
- [x] Make Clusters entries open details and filtered Nodes; move removal to configuration.
- [x] Remove backup destination Settings nav and preserve legacy deep links through a clear owning-cluster link/redirect.
- [x] Keep Cluster.tsx compatibility export/redirect thin; preserve old regressions in new owning components.

## Task 4: Navigation, terminology, integration and delivery

Files: `web/src/router/tree.tsx`, `web/src/components/AppLayout.tsx`, `web/src/components/ui/Breadcrumbs.tsx`, `web/src/components/FleetScope.tsx`, other user-facing location strings/tests, English catalog, docs, browser mock data if needed.

- [x] Add `/nodes?cluster=` and `/clusters/$clusterId?tab=` routes without incorrect local inventory guards; preserve `/cluster` links.
- [x] Change sidebar Cluster to Nodes and route resource drill-down links to Nodes with explicit filters. Keep central-management banner off cluster detail/configuration pages.
- [x] Replace cluster-related Location labels, using Cluster / namespace for combined placement choices; retain non-cluster uses.
- [x] Regenerate English catalog with existing extraction workflow; update relevant docs and tests.
- [x] Compile/build, obtain independent diff review and fix material findings.
- [x] Signed commit and verify signature; push feature branch to personal fork; inspect applicable CI.
- [x] Run branch demo in Docker with coherent multi-cluster fixtures and inspect desktop/mobile Nodes, filters, details/configuration and navigation in browser.
- [ ] Deliver branch URL, local URL, verification results and remaining limitations.
