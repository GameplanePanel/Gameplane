# Storage Pool Inventory Implementation Plan

> For implementers: execute the agreed steps in order; parent review and CI gate production changes.

**Goal:** Report configured game-data pool capacity instead of the node system disk, with truthful provisioned-storage labels and unknown states.

**Architecture:** An optional administrator Node annotation selects an exact OpenEBS Local PV LVM thin pool. One read-only resolver serves both cluster stats and fleet inventory using the selected cluster client. Unconfigured clusters retain the existing node-ephemeral calculation.

**Tech stack:** Existing Go Kubernetes typed/dynamic clients and React/TypeScript.

**Spec:** This accepted change brief extends `api/specs.md` inventory and `web/specs.md` fleet-storage contracts. Upstream review base: `9efd6d697f8dd0ba7e5d6d41b6de4d16a8a8cc7c`.

## Global constraints

- No DB migration, registration/configuration UI, CRD change, operator fork, metrics dashboard or pool management.
- The Node annotation is administrator-owned. Readers gain no mutation or Secret permissions.
- No source-capacity number is configured or hardcoded; read the selected pool's live reported size.
- No physical-used metric is added. Existing `usedStorageBytes` remains logical Bound-PV provisioned bytes.
- Only local compile checks are allowed. Test/lint suites run in CI only; preserve user authorship/signatures and add no AI attribution.

## Frozen annotation and response contract

Node annotation key: `gameplane.local/storage-pool`. Value is one JSON object:

```json
{"provider":"openebs-lvm","namespace":"openebs","volumeGroup":"game-vg","thinPool":"game-vg_thinpool","storageClasses":["games"]}
```

All fields are required; provider is exactly `openebs-lvm`; reject unknown fields, invalid names, empty class lists, malformed JSON and duplicate/ambiguous references. Each Node selects only one pool. `thinPool` must be exactly `<volumeGroup>_thinpool`, the conventional OpenEBS name; reject other names before backend lookup even when that pool exists. The annotated Node's name is the exact LVMNode name; no arbitrary target name or URL is accepted.

Selected storage classes must be dedicated to OpenEBS LVM with `thinProvision: "yes"` and the selected VG/pool on participating nodes. This prerequisite applies to existing volumes as well as future provisioning; changing a StorageClass does not migrate existing volumes. PV metadata lacks actual thin-pool and thin-provisioning identity, so the administrator must establish this contract before opting in. Discovering LVMVolumes to verify it would expand permissions and is outside scope.

Read only `local.openebs.io/v1alpha1`, namespace from the annotation, resource `lvmnodes`, name from that Node. Resolve the exact top-level `volumeGroups[name].thinPools[name].size`, accepting valid positive Kubernetes quantity strings or integers. The actual source has no `spec`/`status` wrapper or freshness field. The controller writes only when values change: age/resourceVersion cannot prove staleness and no age heuristic is introduced.

Existing stats fields remain, plus `storageCapacitySource`:

```json
{"nodes":1,"totalStorageBytes":343597383680,"usedStorageBytes":0,"storageCapacitySource":"openebs-lvm"}
```

Per-cluster source is `node-ephemeral` or `openebs-lvm`; frontend aggregation may produce `mixed`. Missing source from an older API is interpreted as legacy node-ephemeral. No Node annotations means the existing node-ephemeral capacity and all-Bound-PV behavior. With any configured references, sum only the explicitly referenced local pools; never add unannotated nodes' system disks.

Provisioned bytes in configured mode include only Bound PVs in the configured storage classes with CSI driver `local.csi.openebs.io`, matching `openebs.io/volgroup` and actual affinity to a referenced Node. Exclude Released/unbound PVs and other classes/VGs/nodes; count each PV once. Preserve Kubernetes node-affinity OR/AND semantics and reject malformed/ambiguous metadata rather than silently invent allocation.

Configured lookup/parse failures are unavailable: cluster stats returns the existing error path; fleet omits that inventory item and reports partial coverage. Preserve authorization error handling and generic error messages. Never substitute EPHEMERAL, another cluster, a partial pool sum or zero. Named read-only LVMNode GET permission supplements existing node/PV inventory permissions. Bind a namespace-scoped Role in the annotation's namespace to the local API ServiceAccount for local inventory, or to the selected remote kubeconfig client identity in that remote cluster. No provider list/watch, Secret access or mutation permissions are added.

## Exact implementation surfaces

- New `api/internal/handlers/storage_inventory.go`: validated annotation, named dynamic lookup, pool/PV scope resolution and source metadata; no writes.
- `api/internal/handlers/cluster.go:160-179,386-440`: add response discriminator and call the shared resolver.
- `api/internal/handlers/fleet.go:567-627`: use the same resolver; retain existing per-cluster issue semantics.
- New `api/internal/handlers/storage_inventory_test.go`: handler-route regression cases using existing fixtures/fake clients; tests must compile against unchanged production code for the red CI phase.
- Web owner: `web/src/types.ts`, `web/src/lib/fleet.ts`, `web/src/lib/utils.ts`, `web/src/routes/Dashboard.tsx` and their existing tests. Keep a storage-only IEC formatter so 343597383680 bytes reads 320 GiB without changing every existing byte display. Captions distinguish pool, node-ephemeral and mixed reported capacity; numerator is provisioned, never physical usage.
- Update owning `api/specs.md`/`web/specs.md` and read-only inventory documentation with the final contract; parent owns upstream review and changelog integration; runtime RBAC and annotations remain operator-managed opt-ins.

## Review focus and regression cases

1. Correct pool versus system disk: both `/cluster/stats?cluster=remote` and `/fleet/inventory?cluster=remote` return pool 320 GiB while the node reports 100 GiB; source metadata is correct and lookup is only GET of the annotated LVMNode.
2. Configured failure: missing/forbidden pool, invalid quantity, malformed annotation and wrong provider produce errors/partial coverage with no node/home/zero fallback and no leaked upstream details.
3. Numerator scope: a matching 7 GiB Bound PV is counted; Released, wrong class/VG/node and unrelated CSI PVs are excluded. Multiple references never double-count a PV.
4. Authority/selection: cluster-wide inventory grant is required before pool reads; namespace/server grants do not probe the source. Remote selection never queries Home; unconfigured legacy behavior is unchanged.
5. Successful aggregation: both routes sum two annotated nodes' capacities and provisioned volumes, count each PV once even with multiple matching affinity terms, and exclude an unannotated node's pool, system disk and PV. Query only the two selected same-name LVMNodes.
6. Conventional naming: a valid alternate pool actually present in LVMNode is still rejected before any backend lookup, with generic unavailable/partial responses.
7. Unknowns/units: fleet does not turn omitted failures into known zero; mixed sources retain honest coverage. Storage copy says provisioned and displays IEC units; existing overcommit behavior remains meaningful.

## Upstream review and verification

- [x] Import the reviewed API/web implementation and existing regressions without the downstream deployment workflow.
- [x] Add conventional-name rejection and successful multi-node aggregation route regressions, preserving every existing test.
- [x] Observe the new naming regression fail in GitHub Actions before adding the minimal production validation.
- [x] Validate `<volumeGroup>_thinpool` before backend lookup; run only compile checks locally.
- [x] Add independent pool/PV whole-byte quantity route regressions and observe their expected red CI checkpoint.
- [x] Accept both parsed quantity representations with exact integer/range checks and preserve raw BinarySI overflow/fraction rejection; run the API compile check.
- [x] Review the combined upstream diff, resolve all actionable findings, and verify both API architectures, API lint, PostgreSQL and web unit checks in CI.
- [x] Open [upstream PR #1002](https://github.com/GameplanePanel/Gameplane/pull/1002) with its matching changelog entry and signed commits.

Red checkpoint: fork PR #15, [CI run 44039836145, amd64 job
133891614335](https://github.com/Baylem/Gameplane/actions/runs/44039836145/job/133891614335)
confirmed only `TestStoragePoolInventory_NonconventionalExistingPoolRejected`
failed: both routes returned HTTP 200 with known 320 GiB capacity and 7 GiB
provisioned instead of unavailable coverage. Both API architectures failed;
lint passed. The conventional-name guard is now implemented and the API
`go build ./...` compile check passed. Both API architectures subsequently passed in CI.

Quantity review checkpoint: at `05ed48841eca507b1fab0bca93ea300c7d746168`,
[CI run 44041788567, amd64 job
133898338197](https://github.com/Baylem/Gameplane/actions/runs/44041788567/job/133898338197)
failed only the `TestStoragePoolInventory_WholeByteQuantities` group: 16 valid
pool/PV subcases across both routes failed, including `1.5Gi`, `320.0Gi`,
`128Ti` and the MaxInt64 integer string. Both API architectures failed.
The shared conversion now checks sign/range before `Value()` and confirms exact
integer bytes with `CmpInt64`; raw BinarySI values at the parser's saturation
boundary are checked with exact rational arithmetic. Pool sizes remain positive;
PV sizes remain nonnegative. The API compile check passed; scoped re-review found
the issue addressed with no additional actionable findings.

[CI run 44042859060](https://github.com/Baylem/Gameplane/actions/runs/44042859060)
at `67b7ec3c42e77529ce50b881f22bb83faa05a31e` passed both API architectures,
API lint, PostgreSQL checks and web unit tests. Added API statement-line coverage
was 98.3%. The local TypeScript compile check also passed. Before marking the
upstream PR ready for review, require successful final CI, including end-to-end
and visual checks; their current status is tracked in the PR checks.

Image publication, deployment, pool mutation and runtime permission changes are outside
this upstream change. Test and lint results come from GitHub Actions; local
verification remains compile-only.
