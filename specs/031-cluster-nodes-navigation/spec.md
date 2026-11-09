# Cluster and node navigation

## Intent

Implement the user's approved discussion: keep Clusters, replace the ambiguous singular Cluster navigation with Nodes, show all authorized nodes by default with a cluster filter, give each cluster Overview / Nodes / Configuration, and move cluster-owned configuration out of panel settings. Use Cluster wherever the product currently calls a Kubernetes cluster a Location.

## Design

Pencil `design.pen` is the visual source. Retain the established sidebar, typography, theme tokens and HeroUI controls. Clusters remains a registration/connectivity directory. Each entry opens cluster details and links directly to its nodes. The Nodes sidebar entry always opens the all-cluster inventory, independently of remembered infrastructure selection.

Nodes has a title and description, filter-responsive summary counts, search, Cluster and Status filters, and a sortable table with Node, Cluster, Status, CPU, Memory, Pods and Uptime. On narrow screens use compact stacked node rows without horizontal page overflow. Preserve node roles/labels in secondary information. Cluster detail Nodes reuses the same inventory component, fixed to that route's cluster. No duplicated inventory implementation.

The search and filter toolbar follows Backups: a compact search input and one Filter button. Its badge counts applied Cluster and Status filters, excluding search and the fixed cluster in an embedded inventory. The popover stages changes until Apply; Clear resets the draft choices, and dismissal discards unapplied changes. Search continues to filter immediately. This refines the existing design at the user's request without changing inventory or sorting behavior.

Cluster details use explicit route identity with tabs Overview, Nodes and Configuration. Overview shows registration identity, API connectivity, version and links to servers/nodes. Configuration contains Access (download kubeconfig when supported), Storage (read-only install-time class when available), Backup destinations (existing CRUD), and a separate removal section. Registration removal retains confirmation and leaves workloads intact. Add node remains in Nodes; from All clusters it requires an explicit target before minting any join command. Only the local cluster supports current join/kubeconfig operations.

Panel settings retains panel-owned sections. Remove backup destinations from its navigation; preserve old deep links with a clear path to the owning cluster. Preserve the old /cluster URL as a compatibility redirect. Navigation and breadcrumbs identify the active cluster and tab, and cluster IDs in URLs are encoded by the router.

## Data and authorization

- Reuse `/fleet/inventory`, `Clusters.list`, and existing cluster/configuration/destination endpoints. No new endpoint or workload capability is implied. Preserve access to relocated settings by allowing registration metadata discovery for existing destination grants on their target clusters and local installation configuration grants; this never grants node inventory or remote configuration access.
- Default Nodes requests all authorized inventory; a cluster filter requests that exact backend cluster, including clusters omitted by a combined limit.
- Node identity is the pair of cluster registration ID and node name. Same names in different clusters remain separate.
- Filter choices retain authorized scopes, unavailable clusters and the selected cluster. Partial/error data is visible, never presented as a complete healthy/empty fleet.
- Missing CPU, memory or pod measurements render unknown; measured zero remains zero. Summaries follow filters and disclose partial coverage.
- Check inventory using the registration's canViewInventory and the fleet API's authorization; do not hide remote-only grants behind a local/global inventory guard.
- Mutations retain the route or explicitly captured cluster throughout asynchronous work. Switching clusters discards sensitive drafts/results. Never infer a local workload on standalone installs.
- Destination permissions and requests are target-bound. Download, enrollment and install-time storage keep their existing local/permission/capability limitations.

## Terminology

Change user-facing cluster/location vocabulary in navigation, filters, creation, status/error text, search and accessible labels. A placement containing both cluster and namespace is labeled Cluster / namespace where needed. Browser window.location, storage paths, HTTP Location headers and unrelated geographic meanings stay unchanged. Refresh English i18n source and affected tests/docs.

## Alternatives considered

1. Rename only: removes the naming collision but leaves configuration scattered and nodes restricted to one remembered cluster.
2. Nest every node visit under Clusters: clear ownership but adds steps for frequent monitoring.
3. Chosen: direct fleet Nodes plus cluster detail tabs, sharing the inventory view. This follows the user's all-clusters requirement and retains quick access.

## Validation and delivery

Add behavioral regressions for filtering, duplicate identity, unknown and partial metrics, route-bound mutations and capability restrictions. Repository policy permits local compile/build checks and interactive browser inspection, but test/lint suites run only in CI. Preserve existing regression assertions when relocating them. Commit with the user's unattended signing configuration and human attribution, push the feature branch to Baylem/Gameplane, and run its demo in Docker locally for review. No merge or upstream publication is requested.
