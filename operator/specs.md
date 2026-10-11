# operator — Specification

**Status:** pre-v1 (v0.3.0)  
**Module / package:** github.com/GameplanePanel/gameplane/operator  
**Go version:** 1.26.0

## Purpose

The operator is a Kubernetes controller-runtime-based process that reconciles Gameplane Custom Resource Definitions (CRDs) into operational Kubernetes objects. It is the authoritative owner of all state transformations: a user must be able to `kubectl apply` a GameServer and achieve the same outcome as creating it via the dashboard API. The operator drives the full lifecycle of game servers, backups, restores, and module installation through a set of specialized reconcilers watching CRD status.

## Responsibilities

- **GameServer lifecycle:** reconcile GameServer CRs into StatefulSets, Services, PVCs, and pod-injected agent sidecars; manage pod affinity, RCON/console integration, file I/O, and graceful stop sequences.
- **Module installation:** pull OCI module bundles from remote or local sources, verify signatures (cosign), and materialize GameTemplate CRs; refresh source indexes periodically.
- **Backup & restore:** coordinate application-level quiesce with agent, spawn restic backup/restore Jobs, track snapshot IDs, and enforce retention policies.
- **Scheduled backups:** parse cron expressions, create Backup objects on schedule, apply retention rules (keep-daily, keep-hourly, etc.), and report schedule validity and retention status.
- **Remote cluster health:** periodically probe Kubernetes clusters reachable via kubeconfig Secrets, report connectivity and API-server health.
- **Agent orchestration:** generate per-GameServer mTLS certificates, bind RBAC to agents, expose operator-to-agent RPC for quiesce/unquiesce during backup.
- **Fleet observability:** expose Prometheus metrics summarizing GameServer and Backup phases across the entire fleet.
- **CRD codegen:** curate operator/api/v1alpha1 type definitions; `make generate && make manifests` regenerates deepcopy, RBAC, and CRD YAML whenever types change.

### Capture identity across remote gateways

The operator injects the GameServer UID into the capture sidecar and sends both
the server and NetworkCapture UIDs in capture-start requests. Status, stop and
expiry cleanup carry the same original owner and capture UIDs. This binds retained
capture bytes to their originating resources for authenticated remote downloads
and cleanup, even after sidecar restart or history eviction. Capture admission,
start/stop and expiry remain reconciler responsibilities; the gateway cannot
bypass them or operate an arbitrary pod. Existing game resources and storage are
not recreated to enable remote parity.

## Non-goals / boundaries

The operator **does not** handle:
- User authentication or authorization (RBAC is Kubernetes role-based, not application-level).
- REST API, WebSocket, or dashboard concerns (the API server, not the operator, owns the UX layer).
- Module source credential management beyond reading Secrets passed by the API (credentials are user-supplied via the API and stored as Secrets; the operator only reads them).

**House rule — "the operator is authoritative":** All business logic flows through the reconcilers. The API server writes CRs and watches status; it does **not** embed reconciliation logic. A user deleting a GameServer via `kubectl delete gameserver <name>` must trigger identical cleanup as a dashboard delete, because both routes lead to CRD deletion and the operator's cleanup — GameServer's child objects (StatefulSet, Service, PVC, etc.) are reclaimed via ownerReference garbage collection, not a finalizer (see "Finalizers" below for the two CRDs that do use one). Changes that bypass the CRD (e.g., direct edits to StatefulSets) are not supported — the operator's next reconcile will overwrite them.

## Directory & package layout

```
operator/
├── cmd/main.go
│   Entry point; parses CLI flags (metrics/probe addresses, image refs, certificate paths,
│   CIDR allowlists), constructs manager and controller-runtime objects, starts reconcilers.
│
├── api/v1alpha1/
│   CRD type definitions (gameserver_types.go, gametemplate_types.go, backup_types.go,
│   backupschedule_types.go, restore_types.go, module_types.go, modulesource_types.go,
│   cluster_types.go, networkcapture_types.go) and generated deepcopy helpers (zz_generated.deepcopy.go).
│   Edited manually; regenerate deepcopy + YAML after any type edit.
│
├── config/
│   ├── crd/              Generated CRD YAML (9 kinds). Do not hand-edit.
│   ├── rbac/             Generated ServiceAccount, Role, RoleBinding, ClusterRole,
│   │                     ClusterRoleBinding. Do not hand-edit.
│   ├── manager/          Manager deployment, StatefulSet, and RBAC references.
│   └── samples/          Sample GameServer, GameTemplate, Backup, etc. CRs for testing.
│
├── internal/
│   ├── controller/       Reconciler implementations (9 controllers + co-located envtest).
│   │   ├── gameserver_controller.go        Reconciles GameServer → StatefulSet+Service+PVC
│   │   ├── gameserver_*.go                 Split concerns: config, modcreds, rcon, restart,
│   │   │                                   wipe, version, stop_attach, node, status, extravolumes
│   │   ├── gametemplate_controller.go      Maintains template.status.inUseCount
│   │   ├── backup_controller.go            Drives Backup → restic Job + quiesce orchestration
│   │   ├── backup_volumesnapshot.go        CSI snapshot tracking for backup strategy
│   │   ├── backupschedule_controller.go    Cron scheduler + retention trimming
│   │   ├── restore_controller.go           Drives Restore → restic Job with GameServer suspend
│   │   ├── restore_volumesnapshot.go       Snapshot recovery for restore strategy
│   │   ├── modulesource_controller.go      Indexes module sources, refreshes catalog
│   │   ├── module_controller.go            Pulls OCI bundle, verifies signature, creates GameTemplate
│   │   ├── cluster_controller.go           Health checks remote clusters via kubeconfig
│   │   ├── networkcapture_controller.go    Drives NetworkCapture → sidecar injection + retention/expiry
│   │   ├── agent_certs.go                  Generates per-GameServer agent mTLS certs
│   │   ├── agent_rbac.go                   Creates per-GameServer ServiceAccount + Role
│   │   ├── metrics.go                      Prometheus collectors for GameServer/Backup phases
│   │   ├── retention.go                    Backup retention logic (keep-daily, keep-hourly, etc.)
│   │   ├── restic_summary.go               Parses restic JSON output from container logs
│   │   ├── semver.go                       Semantic versioning helpers
│   │   ├── helpers.go                      Utility functions shared across reconcilers
│   │   └── *_envtest_test.go               Co-located integration tests (envtest tier)
│   │
│   ├── agent/
│   │   Typed HTTP client for operator → agent mTLS calls during backup quiesce.
│   │   Builds TLS config from certificate paths; disabled when no mTLS material configured.
│   │
│   ├── modsrc/
│   │   Module source fetchers (bundle.go, dir.go, git.go, http.go, local.go, oci.go, upload.go).
│   │   Abstraction layer: each source type (OCI, git, HTTP, local directory) implements
│   │   a Fetcher interface; operator uses modsrc.ForSource to route to the right impl.
│   │
│   ├── oci/
│   │   OCI registry client wrapper + authentication helpers (auth.go, client.go, testregistry_test.go).
│   │   Thin layer over go-containerregistry for image pushing/pulling.
│   │
│   └── verify/
│       cosign signature verification for OCI module bundles (keyed + keyless).
│       Returns Verifier interface; operator refuses unsigned bundles if spec.verify declared.
```

## Custom Resource Definitions

Nine CRD kinds under `gameplane.local/v1alpha1`:

### Cluster-scoped (4)

| Kind | File | Purpose |
|------|------|---------|
| **GameTemplate** | `gametemplate_types.go` | Reusable blueprint for a game server (image, ports, probes, RCON protocol, mod loaders, etc.). Cluster-wide catalog; instances are created via GameServer references. Status tracks in-use count. |
| **Module** | `module_types.go` | User request to pull and install a module (OCI bundle) from a ModuleSource. Creates a child GameTemplate on success; delete cascades to the template via owner reference. |
| **ModuleSource** | `modulesource_types.go` | Registry of module bundles (OCI, git, HTTP, or local). Declares source location, refresh interval, optional credentials Secret, and optional cosign verification policy. Status lists available modules. |
| **Cluster** | `cluster_types.go` | Metadata for a remote Kubernetes cluster (displayName, kubeconfig Secret ref). Cluster-scoped so multiple control planes can discover each other. Status tracks health (Unknown/Healthy/Unhealthy), lastCheckTime, conditions. |

### Namespaced (5)

| Kind | File | Purpose |
|------|------|---------|
| **GameServer** | `gameserver_types.go` | Instance of a game server (always a single StatefulSet replica; there is no replica-count field). References a GameTemplate for defaults via `spec.templateRef`, **immutable after creation** (a repointed template would leave the StatefulSet wedged instead of switching cleanly — delete and recreate the GameServer instead); Spec declares suspend flag, stop grace period, module customizations, backup trigger, **optional capture configuration**. Status tracks phase (Pending/Starting/Running/Stopping/Suspended/Failed — `derivePhase` never assigns `Stopped`, see the "Status phases" note under GameServerReconciler), pod readiness, agent heartbeat, **capture sidecar readiness and active capture**. |
| **Backup** | `backup_types.go` | One-shot backup of a GameServer's data. Spec declares gameServer ref, optional quiesce preferences, strategy (restic or volume snapshot). Status tracks phase (Pending/Running/Succeeded/Failed), snapshot ID, restic output summary. |
| **BackupSchedule** | `backupschedule_types.go` | Recurring backup schedule for a GameServer. Spec declares cron expression, retention rules (keep-daily, keep-weekly, keep-monthly, keep-yearly), optional suspend. Status reports next firing time, last fire time, retention condition. |
| **Restore** | `restore_types.go` | One-shot restore of a Backup into a GameServer (typically a fresh copy). Spec refs the source Backup and target GameServer. `RestorePhase` declares Pending/Suspending/Running/Resuming/Succeeded/Failed, but the controller only ever assigns Pending/Suspending/Running/Succeeded/Failed — it clears the GameServer's suspend flag and sets Succeeded in the same pass, with no intermediate Resuming (`RestorePhaseResuming` is declared but never written; see F-260). Coordinates suspend → restic restore Job → resume. |
| **NetworkCapture** | `networkcapture_types.go` | Opt-in packet-capture session for a GameServer. Spec declares gameServer ref, optional pcap filter, max duration, max file size, and optional TTL. Status tracks phase (Pending/Running/Completed/Failed/Expired), start/completion times, packet/byte counts, and error messages. Owned by its parent GameServer via ownerReferences. |

**Verification:** CRD YAML in `config/crd/` generated from types via `make manifests`. All 9 kinds present and scopes correct (verified against `gameplane.local_*.yaml` files): 4 cluster-scoped (GameTemplate, Module, ModuleSource, Cluster) + 5 namespaced (GameServer, Backup, BackupSchedule, Restore, NetworkCapture).

## Reconcilers

Primary reconcilers register with the manager in `cmd/main.go` and handle CRD lifecycle:

### GameServerReconciler
- **Responsibility:** Reconcile GameServer → StatefulSet, Service, Config ConfigMap, PVC, NetworkPolicy.
- **Template recovery:** A missing template reports Failed and retries every 15 seconds. Template creation or spec changes enqueue referencing servers immediately, so a GameServer applied before its Module/template recovers without a manual edit; template status updates do not enqueue servers.
- **Key functions:**
  - **PVC StorageClass selection** — apply precedence chain at PVC creation time (only):
    - Chain: GameServer.Spec.Storage.StorageClassName (explicit override) > GameTemplate.Spec.Storage.StorageClassName (template default) > operator `--game-data-storage-class` flag (install-time default) > nil (cluster default)
    - Applied on creation only at three sites: reconcilePVC (game data volume), reconcileExtraPVCs (user-supplied extra volumes), reconcileModPVC (mod-install cache). Once a PVC exists, its `spec.storageClassName` is never mutated — PVCs are immutable per Kubernetes rules, so later changes to the flag affect only new servers.
    - Empty flag value is a strict no-op — preserves pre-feature behavior (all three sources nil → cluster default).
    - Error handling: `checkPVCProvisioningFailure` detects missing StorageClass and surfaces it as Ready condition False, reason `PVCProvisioningFailed`, with message naming the class — leaves GameServer in phase Pending (not Failed, so it is recoverable once an admin creates the StorageClass).
    - Requires `storage.k8s.io/storageclasses` get/list permission (on each reconcile to verify existence).
  - Pod spec assembly: inject agent sidecar (image + pull policy + env), mount agent certs + logs volume.
  - Config & credentials: render game startup config (template vars, port mappings, mod credentials).
  - RCON integration: create agent RBAC, mTLS certs; expose RCON port if template declares it.
  - Graceful stop: if template has Lifecycle.Stop sequence, orchestrate via agent before scaling down.
  - Node affinity: honor spec.nodeSelector and spec.affinity preferences.
  - Ingress NetworkPolicy: enforce per-template advertised ports, admit CIDRs from `--game-ingress-from-cidr`.
  - Fixed-name child cleanup: Delete fixed-name objects (`<gs>-config` Secret, `<gs>-files` Secret, `<gs>-rcon` Secret, `<gs>-auto` BackupSchedule, `<gs>-wipe` Job) only when the GameServer controls them via OwnerReference; unowned objects with these names stay in place (same rule as game-ingress NetworkPolicy). Wipe cleanup carries the verified Job UID as a delete precondition; a foreign Job cannot acknowledge a pending wipe or resume its server.
  - **Conflict requeue:** When reconcileStatefulSet or reconcileWipe encounters an apierrors.IsConflict error (concurrent update races), requeue without error logging — conflicts are expected and transient. The Module, NetworkCapture and Backup reconcilers apply the same rule to every error they return, via `requeueOnConflict` (`conflict.go`).
  - Load-balancer address management: translate spec.networking.addressPool / .address preferences onto the Service based on the cluster's address-manager flavor (MetalLB, Cilium, or none), report assignment status via the AddressAssignment condition.
  - **Network capture foundation** (**Phase 2 Foundational**): Pre-provision a `captures` emptyDir (1 GiB) unconditionally on every game pod's StatefulSet template (required because pod.spec.volumes is immutable on running pods; a rolling restart of all existing game pods is incurred once on upgrade). Extend the `<gs>-agent` Service with a second numeric ServicePort 9091 for the capture sidecar's control endpoint. RBAC markers grant `pods/ephemeralcontainers` access (get, list, watch, patch, update) and full CRUD on `networkcaptures` resources. The capture sidecar injection as an ephemeral container is implemented: `buildCaptureEphemeralContainer` (`gameserver_controller.go`) is the single source of truth for the container spec, injected eagerly by `GameServerReconciler.reconcileCapture` when `spec.capture.enabled` is set, with `NetworkCaptureReconciler.injectCaptureContainer` as an idempotent fallback for the case where its own cache is ahead of the GameServer reconciler's write.
- **Split concerns:**
  - `gameserver_config.go`: render config files, template variable substitution. The `config-init` container copies rendered config files onto the data volume on every pod start; when the template sets `security.fsGroup`, config-init makes each file and directory it copied group-writable so a non-root game sharing that fsGroup can update them.
  - `gameserver_modcreds.go`: mount mod credentials Secrets.
  - `gameserver_rcon.go`: RCON port exposure, lifecycle sequences.
  - `gameserver_restart.go`: restart action, pod deletion.
  - `gameserver_wipe.go`: data wipe sequence. Request idempotency: token-based (request token acked once Job succeeds, same token never re-runs). Wipe gate: requires spec.suspend=true (API sets this); waits for StatefulSet scaled to 0 replicas AND game pod object deleted (pod by name `<gs>-0`) before starting the destructive Job. Restart: ackWipe sets spec.suspend=false after successful Job completion to resume the server on the fresh world. Job: runs rm against the existing PVC (never deleted/recreated); carries BackoffLimit 2 and no ActiveDeadlineSeconds.
  - `gameserver_version.go`: track/propagate game version.
  - `gameserver_node.go`: node affinity, pod anti-affinity.
  - `gameserver_status.go`: phase computation from StatefulSet/Pod state + agent heartbeat, AddressAssignment condition.
  - `gameserver_stop_attach.go`: pod exec attachment for graceful stop commands.
  - `gameserver_extravolumes.go`: user-supplied additional volume mounts.
  - `gameserver_tunnel.go`: provision tunnel relay Deployment (frp, Tailscale, Playit providers), mount only the active provider's credential key (items projection with Optional:true) to prevent exposure of stale keys during a direct spec provider switch.
- **Capture configuration:**
  - **Spec fields (spec.capture):**
    - `Enabled bool`: Optional flag to enable/disable the capture sidecar injection on this GameServer. When false or omitted, no sidecar is injected and the server is unchanged. When true, the operator injects the capture sidecar as an ephemeral container into the running game pod, live and without restarting the game container.
    - `RetentionSeconds *int32`: Optional per-GameServer retention override, in seconds. Clamped to the cluster maximum (via `--capture-max-retention-seconds` flag) at API tier; defaults to cluster default (via `--capture-default-retention-seconds` flag) when omitted. Applied to all captures on this server unless overridden at capture-creation time.
  - **Status fields (status.capture):**
    - `Ready bool`: Whether the capture sidecar is currently running and listening on its :9091 port, able to accept new capture requests. True only when the ephemeral container has reached Running state and the sidecar has bound its listener; False before injection, during startup, if the container crashed, or if spec.capture.enabled is false.
    - `ActiveCapture *string`: Name of the NetworkCapture currently in Pending or Running phase for this GameServer, if any. Set by NetworkCaptureReconciler when transitioning a capture to Running; cleared when that capture transitions to a terminal phase (Completed, Failed, or Expired). Allows finding the in-progress capture without listing all NetworkCaptures in the namespace.
    - `LastCaptureTime *metav1.Time`: Timestamp of the most recent capture reaching a terminal phase (Completed or Failed) on this GameServer. Updated only on terminal transitions; not updated for Pending/Running/Expired transitions.
    - `SidecarRestarts int32`: Count of restarts observed for the capture ephemeral container. NOTE: Kubernetes does not restart ephemeral containers (RestartPolicy does not apply to them). This field is retained as a stable hook for future implementations and to surface any unexpected restarts via node-level mechanisms; expected to stay 0 in practice.
  - **Implementation status — Built:**
    - `captures` emptyDir (1 GiB) pre-provisioned unconditionally on every game pod's StatefulSet template (persistent across pod restarts, immutable per house rule). Volume is mounted only on the capture ephemeral container when capture is enabled; never on the agent or game container.
    - `<gs>-agent` Service extended with second numeric port "capture" on 9091.
    - RBAC markers on GameServerReconciler grant `pods/ephemeralcontainers` (get, list, watch, patch, update) and `networkcaptures` (full CRUD).
    - Sidecar injection as ephemeral container (conditional on spec.capture.enabled + cluster-wide `--capture-enabled` flag). Injection is live and restart-free.
    - Sidecar lifecycle reconciliation (NetworkCaptureReconciler manages Pending → Running → Completed/Failed → Expired state machine).
    - TTL-based auto-deletion (NetworkCapture transitions to Expired and is deleted once `status.completionTime + effective TTL` elapses, requeued at the exact remaining TTL rather than a blind poll; US4) — see "Retention / TTL-based expiry" under NetworkCaptureReconciler for the full mechanism, including the bounded best-effort cleanup budget that keeps a persistently unreachable sidecar from blocking the CR delete.
    - Edge cases and advanced scenarios: concurrency lock, pod-restart/eviction detection, sidecar-crash detection (US5) — see "Edge cases (Phase 7 / US5)" under NetworkCaptureReconciler.
  - **Implementation status — Planned:**
    - Dashboard UI for starting/stopping captures and browsing history (Phase 8).
- **Status phases:** `GameServerPhase` declares Pending, Starting, Running, Suspended, Stopping, Stopped, Failed, but `derivePhase` (`gameserver_status.go`) only ever assigns Pending, Starting, Running, Suspended, Stopping, or Failed — `GameServerPhaseStopped` is declared on the type but never written (mirrors `RestorePhaseResuming` above; see F-260). A stopped/suspended server reads as phase Suspended, told apart from an idle sleep by the IdleAsleep condition and status.idle, not by phase.
- **Address pool & assignment:**
  - **Inputs:** spec.networking.addressPool (pool name) and spec.networking.address (explicit IP).
  - **Manager flavor:** Selected via operator CLI flag `--address-manager`, one of `metallb` | `cilium` | `none` (default). Validated at startup; invalid flavors fail the operator.
  - **Service translation per flavor:**
    - MetalLB: addressPool → `metallb.io/address-pool` annotation; address → `metallb.io/loadBalancerIPs` annotation.
    - Cilium: addressPool → `gameplane.local/lb-pool` label (Gameplane convention; admin mirrors it in `CiliumLoadBalancerIPPool.spec.serviceSelector`); address → `lbipam.cilium.io/ips` annotation.
    - None: records the request but mutates no Service metadata; operator flags the ignored preference on AddressAssignment.
  - **Managed metadata:** All address-manager-applied annotations and labels are tracked and pruned when the preference is unset, ensuring no stale metadata lingers.
  - **AddressAssignment condition:** Reports the outcome of a pool/address request with one of nine reason codes:
    - `Assigned`: request honored; address manager assigned an address (True status).
    - `AssignmentPending`: LoadBalancer Service exists but no address assigned yet (False).
    - `ServiceNotReady`: Service doesn't exist or isn't type LoadBalancer (False).
    - `IgnoredForExposureMode`: request ignored because Expose ≠ LoadBalancer (False; the request is meaningless for ClusterIP/NodePort/Hostport).
    - `InvalidAddress`: `spec.networking.address` fails `netip.ParseAddr` (False); the value is rejected before it is ever handed to the address manager, so no manager annotation or label — including the pool annotation — is written for this request.
    - `NoAddressManagerConfigured`: request recorded but no address manager in this cluster to act on it, so default-pool address will be used — explicitly reported to avoid silent misunderstanding (False).
    - `PoolNotFound`: the address manager reported the requested pool does not exist (False). Derived best-effort from Warning events on the Service (`extractAddressFailureFromEvents`), so it is not guaranteed to fire — an address manager that emits no matching event leaves the condition on `AssignmentPending`.
    - `AllocationFailed`: the address manager reported failure to allocate an address (False) — includes exhausted pools, quota failures, or other allocation errors. Same best-effort event derivation, and the same caveat, as `PoolNotFound`.
    - `AddressInUse`: the requested explicit address is already taken (False). Two sources: a direct check (`findAddressConflict`) — this path names the conflicting server in the message and takes precedence — and, failing that, the same best-effort event derivation, which catches clashes with non-Gameplane Services or hosts on the network. `findAddressConflict` considers every other GameServer that either carries the address in `status.endpoints` ("holds" it) or merely requests it via `spec.networking.address` ("requests" it), as a single candidate set resolved by ONE ordered comparison rather than two independent checks: candidates are sorted by a strict total order that ranks an actual holder ahead of a mere requester *regardless of creation order*, falling back to `(creationTimestamp, namespace, name)` only to break ties among candidates with the same hold status — and a conflict is reported iff the minimum of that sorted set sorts strictly before the server being reconciled, in which case that minimum is named (as `namespace/name` if from a different namespace). The server's own hold status is folded into the same comparison as every candidate's, which is what keeps this from reintroducing the old livelock: a requester's own reconcile ranks the actual holder first (so the requester yields), while the holder's own reconcile — computing its own hold status fresh — ranks itself first (so it never names the requester back); only a pure holder (no explicit `spec.networking.address` of its own) is exempt from ever running this comparison in the first place (see below). This guarantees *at least one* server in any contending set is told "no conflict" (breaking a livelock two independent checks could produce: a holder and an earlier-created mere-requester permanently reporting each other) and that the reported name is deterministic regardless of List's return order — it is not *exactly* one, because a pure holder is also told "no conflict" via its own early return (see below) independently of whatever the set's minimum resolves to. "Holding" is judged independently of "requesting" — a pool-assigned server with no explicit `spec.networking.address` still counts as holding the address it was assigned, and outranks any requester for that address regardless of which was created first, which is what catches the primary real-world case: a server assigned an address from a pool racing against another server that explicitly requests that same address. Such a pure holder can never itself report a conflict — its own `spec.networking.address` is empty, so it hits `findAddressConflict`'s early return before it ever lists other servers or runs this comparison — so this precedence rule cannot produce a mutual pair (a holder that *also* requests the address in spec does run the comparison and can report, but only when it is itself outranked by a different, higher-priority holder). Two discriminators keep a `status.endpoints` match from false-positiving on an address that was never a real LoadBalancer assignment: `GameServerEndpoint.TunnelProvider` is set on every tunnel-sourced endpoint that reaches `status.endpoints` through a code path that stamps it (frp, tailscale, and both the success and validation-failure branches of playit), so only a `TunnelProvider == ""` endpoint is eligible; and `endpointsFromService` falls back to `svc.Spec.ClusterIP` as `Host` when the Service has no LoadBalancer ingress yet, so a `Host` match only counts as holding when the candidate's own `spec.networking.expose` is `LoadBalancer` — this narrows but does not fully close the false-positive, since an LB-exposed candidate still awaiting ingress assignment carries its ClusterIP as `Host`, and a requested address that happened to fall inside the Service CIDR would still read as "holding" it; `GameServerEndpoint.Pool` cannot be used to close this gap, since it is stamped only for a translated pool request, not for an explicit-address request served off a real LB ingress. A candidate with a non-nil `metadata.deletionTimestamp` is excluded from the set entirely — a terminating server can no longer legitimately hold or claim an address, so letting it win the tiebreak would permanently block a live server behind one already on its way out; a non-LoadBalancer candidate is still eligible via the requests path, since it may legitimately be requesting an address it will use once re-exposed.
    - Unset (condition absent): no pool or address requested; no condition emitted.
    - Precedence when several apply: `IgnoredForExposureMode` → `InvalidAddress` → `NoAddressManagerConfigured` → direct-conflict `AddressInUse` → event-derived failure → `ServiceNotReady` → `AssignmentPending` → `Assigned`.
  - **Endpoint population:** `endpointsFromService()` populates `GameServerEndpoint.pool` only when a pool request was actually translated (outcome == Assigned and Manager supports it); never claims a pool for the ClusterIP address shown while assignment is pending, for an ignored request, or for a cluster without an address manager.
  - **status.startedAt:** while the server is Running, `status.startedAt` follows the start time of the running game container, matched by container name (`gameContainerStartedAt` in `gameserver_status.go`). Uptime therefore resets when the game container restarts inside the same pod. When that time is unavailable, `startedAt` is set once to the current time and kept. Test: `TestGameContainerStartedAt` (`gameserver_status_test.go`).
  - **TunnelReady condition:** Reports tunnel pod readiness when `spec.networking.tunnel.enabled=true` (absent when disabled). Computed in `computeTunnelConditions` (`gameserver_status.go`) each pass reconcileStatus runs, with reasons `InvalidConfig`, `DeploymentNotReady`, `PortNotMapped` (frp, unmapped advertised ports), `NoCredentials` (tailscale/playit missing `credentialsSecretRef`), `UnknownProvider`, and `Ready`. **Credential ownership:** `reconcileTunnel` refuses to mount a `credentialsSecretRef` Secret that has no `ownerReference` matching the GameServer's name and UID (including a stale reference from a deleted same-name GameServer) (`isServerOwnedSecret`) — supported credential paths are the dashboard, the `PUT /servers/{name}:tunnel-credentials` API (which sets the ownerReference), or a manually-created Secret with an explicit ownerReference. A refused Secret is never mounted: `reconcileTunnel` still writes the tunnel Deployment, with no credential volume and zero replicas, so a tunnel already running with that Secret stops. It also writes `TunnelReady=False/TunnelCredentialRefused` (naming the Secret, never its data) directly from `reconcileTunnel` before `Reconcile` returns its error — this bypasses the normal `reconcileStatus` pass since Reconcile returns early on that error — The condition carries its own current `observedGeneration`, but `status.observedGeneration` is not advanced on a refused pass (the rest of status is not recomputed). Once the credential becomes acceptable (or the tunnel is no longer wanted), `reconcileTunnel` removes the refusal condition itself (`clearTunnelCredentialRefused`) before returning, so it does not linger if a later step (NetworkPolicy, Service, StatefulSet) fails; reconcileStatus then writes the real TunnelReady. The refusal message points at the dashboard/API, except when the refused Secret is the canonical `<server>-tunnel-auth` name, where the API answers 409 and the message says to fix the ownerReference or delete the Secret first.

### GameTemplateReconciler
- **Responsibility:** Lightweight; only maintains status.inUseCount (how many GameServers ref this template).
- **Watches:** GameServer creations/deletions to recompute count on every reconcile.

### BackupReconciler
- **Responsibility:** Drive Backup to completion: coordinate quiesce (if agent available), spawn restic Job, track snapshot ID.
- **Key functions:**
  - Quiesce orchestration: call agent Quiesce before Job, Unquiesce after completion.
  - Quiesced copies are serialized per target GameServer UID across schedules and on-demand requests. The Backup pins its target in `backup.gameplane.local/target`; the GameServer's `backup.gameplane.local/owner` stores the owning Backup name/UID. Live API reads and resourceVersion updates protect acquisition/release across reconciler restarts. Quiesce cleanup intent is recorded before calling the agent, including partial failures. Cleanup waits for live copy workers; deletion cancels the copy Job with foreground propagation first. Force-deleted owners are recovered under exclusive ownership, draining their workers and restoring saving before the next copy. Pre-upgrade quiesced Backups without a target annotation finish and release all legacy workers before new protected copies may start.
  - Orphan recovery preserves all predecessor names/UIDs in the GameServer's `backup.gameplane.local/recovering-owners` annotation, atomically with ownership transfers, so deleting a recovering Backup cannot hide an earlier worker. Normal deletion, orphan recovery, and legacy cleanup also wait for pending owned CSI captures to report ready or an explicit error; VolumeSnapshots are reclaimed by owner-reference GC after cleanup.
  - VolumeSnapshot API availability is checked before quiescing. Snapshot creation errors fail the Backup and run unquiesce cleanup immediately instead of retrying creation indefinitely with saving disabled. API/creation failures persist `backup.gameplane.local/snapshot-aborted` to allow cleanup without waiting for a failed/nonrestorable capture. Failed unquiesce remains visible and retried. A transport failure can leave a CSI snapshot object despite a Failed Backup; this does not make that Backup restorable.
  - Restic Job: mount data PVC, run restic backup to configured destination (S3, B2, rest-server, local fs). Job carries BackoffLimit and ActiveDeadlineSeconds from the reconciler's JobBackoffLimit / JobActiveDeadlineSeconds fields (operator flags --backup-job-backoff-limit / --backup-job-active-deadline-seconds, Helm operator.backupJobBackoffLimit / operator.backupJobActiveDeadlineSeconds; defaults 2 and 86400, applied when unset; spec 018 OD-027); only a Failed=True Job condition (set by jobPermanentlyFailed check) marks the Backup Failed. Pod SecurityContext takes FSGroup from the target GameTemplate's spec.security.fsGroup (fallback 65532 if template missing).
  - Snapshot tracking: parse restic JSON output from container logs to extract snapshot ID.
  - Snapshot cleanup on delete (`backup_forget.go`): restic-strategy Backups carry `BackupSnapshotFinalizer` (`gameplane.local/backup-snapshot-finalizer`, added on first reconcile, also for pre-existing non-Failed Backups; not added for volume-snapshot or Failed Backups). `finalizeDelete` runs the unquiesce stage (`BackupFinalizer`) first, then `finalizeSnapshot`: it deletes the Backup's own still-running Job (foreground, awaited), waits while a non-terminal Restore pins the Backup or its snapshot id, then runs Job `<backup>-forget` (`restic unlock || true; restic forget <id> --prune --retry-lock 10m`, backoff 3, deadline 1800s, 50m/128Mi requests and 512Mi memory limit, no PVC; the snapshot id is passed through env `FORGET_SNAPSHOT_ID` and must match `^[0-9a-f]{8,64}$`). Success sets `SnapshotForgotten=True` and a Normal `SnapshotForgotten` event; a Failed Job, a missing/incomplete repo Secret, a terminating namespace, or `maxSnapshotForgetWait` (45m, from annotation `backup.gameplane.local/forget-started-at`) sets `SnapshotForgotten=False` with a Warning `SnapshotForgetAbandoned` event and releases the Backup, leaving the snapshot in the repository. A Backup with no recorded snapshot id is released with `SnapshotForgetSkipped` when it may have written one. Backup Jobs run `restic backup /data --json --retry-lock 10m ...`. RBAC markers on `backup_controller.go` grant `backups` update/patch, `backups/status`, `backups/finalizers` and `jobs` create/update/patch/delete. Tests: `backup_forget_test.go`, `backup_forget_envtest_test.go`, `retention_terminating_test.go`; e2e `TestBackup_DeleteForgetsSnapshot` and the extended `TestBackupSchedule_RetentionTrimsPast`. Spec: `specs/done_020-backup-snapshot-forget/`.
  - Status: report phase (Pending/Running/Succeeded/Failed), error details.
- **Integrations:** Agent client (quiesce), Kubernetes Pod logs (restic output), VolumeSnapshot API (CSI backups).

### BackupScheduleReconciler
- **Responsibility:** Cron scheduler + retention enforcement.
- **Key functions:**
  - Parse spec.Schedule cron expression; report ScheduleValid condition.
  - Compute next firing time (a schedule that has never fired counts missed runs from its `metadata.creationTimestamp`, so a new schedule waits for its first real tick); create Backup CR when due. Scheduled Backups carry no ownerReference to the BackupSchedule; instead, they are tracked by the label `gameplane.local/backup-schedule: <schedule-name>` stamped at creation (same namespace). This allows Backups to persist when their BackupSchedule is deleted.
  - Retention trimming: list Backups by label, apply keep-hourly/keep-daily/keep-weekly/keep-monthly/keep-yearly rules, delete excess. Backups already being deleted (non-zero `deletionTimestamp`, still held by their snapshot finalizer) are skipped when selecting candidates and computing the keep set. Deleting a restic Backup also forgets its snapshot (see BackupReconciler, "Snapshot cleanup on delete"), so a trimmed Backup stays `Terminating` until its forget Job finishes.
  - Report RetentionTrimmed condition (success) or retention failure (TrimFailed).

### RestoreReconciler
- **Responsibility:** Drive Restore to completion: suspend GameServer, run restic restore Job, resume.
- **Key functions:**
  - Pin snapshot ID at observation (immutable during restore).
  - Restic restores pin the target GameServer name/UID on the Restore and add a cleanup finalizer before acquiring an annotation guard on that GameServer. The guard records the owning Restore name/UID and is acquired with an optimistic resourceVersion update that also sets spec.suspend=true. Other restores targeting that server wait; restores of independent targets may run concurrently. The game workload remains suspended while the guard exists, even if a power request clears spec.suspend; data-wipe reconciliation also waits for the guard to clear.
  - A nonempty pending data-wipe request blocks guard acquisition, including after wipe failure. Restore status explains this wait. An uncached read of the fixed-name wipe Job identifies a failed current request only when the current GameServer controls it, its token matches, and it reports Failed=True; DataWipe=False alone is insufficient. Confirmed failure reports that restore is blocked and asks the operator to inspect wipe Job logs and resolve the wipe failure. The restore does not cancel or acknowledge the wipe, since an in-flight wipe reconciliation could still create a writer. The wait message clears when restore acquires the target normally.
  - Suspend: wait for Suspended/Stopped status plus the actual workload: the owned StatefulSet must exist, request zero replicas, report zero replicas, and have observed its current generation. An absent StatefulSet waits for workload reconciliation; a missing template or broken workload must be repaired before restore can proceed. No live or terminating game pod or pod mounting the data PVC may remain. Nonterminal Jobs mounting that PVC must also finish before a new restore Job starts. These checks use the uncached API reader and run again immediately before creating the Job.
  - The stopped StatefulSet also receives the owning Restore guard. A successful fence write immediately requeues the Restore to repeat the workload and writer checks; only external drainage waits use the polling delay. Its mutation callback honors that guard even when the GameServer cache snapshot predates acquisition, fencing delayed scale-up writes with the StatefulSet resourceVersion. Cleanup removes both guards after workers drain.
  - Restore Job: mount snapshot (from VolumeSnapshot or restic), run restic restore. Job carries BackoffLimit and ActiveDeadlineSeconds from the reconciler's JobBackoffLimit / JobActiveDeadlineSeconds fields (operator flags --backup-job-backoff-limit / --backup-job-active-deadline-seconds, Helm operator.backupJobBackoffLimit / operator.backupJobActiveDeadlineSeconds; defaults 2 and 86400, applied when unset; spec 018 OD-027); Job failure is recognized only from a Failed=True condition. Legacy in-flight Jobs without the pinned target annotation fail the Restore and are canceled/drained; operators can start a fresh Restore after cleanup. Pod SecurityContext takes FSGroup from the target GameTemplate's spec.security.fsGroup (fallback 65532 if template missing).
  - Resume: once the restore Job succeeds and its live pods have drained, persist Succeeded before releasing the matching guard and clearing suspend in the same pass. This ordering survives interrupted status writes or operator restarts; it does not wait for the GameServer to reach Running again. Failure/deletion cancels active owned Jobs with foreground deletion and retains the guard/finalizer until Jobs and live worker pods drain, then leaves the target suspended. A missing or recreated guard owner can be recovered only after its UID's orphan Jobs and workers drain. Target recreation fails the old restore; cleanup never changes the replacement server.
  - Status: report phase (Pending/Suspending/Running/Succeeded/Failed). `RestorePhaseResuming` is declared on the type but never assigned by this reconciler (F-260).
  - Volume-snapshot restore reference handling: When a volume-snapshot restore stands up a new GameServer from the original server's spec, any references in spec.env (SecretKeyRef, ConfigMapKeyRef) and spec.networking.tunnel.credentialsSecretRef that are owned by the original server must be copied to new objects owned by the restored server, with rewritten names, because the restored server does not own the originals. Ownership of a source object is an OwnerReference to the original server (Kind, Name, UID) and is re-verified against the source object immediately before each copy, not just once during planning. An object that already exists under a copy name is accepted only if the restored server controls it (a controller OwnerReference, `controller: true`); otherwise the restore fails. The restore fails before the new GameServer is created if the original server does not own every referenced object. The references are rewritten in the new GameServer's spec when it is created, and the copies are made right after creation and re-ensured on every reconcile pass. Only an ownership refusal fails the restore; transient errors (stale cache reads, write conflicts, a referenced object not yet visible in the cache) requeue it. A restore's 10-minute deadline starts when the restore first plans the new server (before it is created) and covers both creating the server with its reference copies and waiting for it to reach Running; it is enforced even while a transient error (such as a referenced object that stays missing) keeps recurring, so the restore cannot run past the limit, and once it expires the restore fails with a deadline message.

### ModuleSourceReconciler
- **Responsibility:** Index module sources and surface available modules into status.
- **Key functions:**
  - Refresh on interval (spec.refreshInterval, default 1 hour, minimum 1 minute).
  - Fetch module metadata (module.yaml) from source (OCI, git, HTTP, local dir).
  - Report status.modules[] with name, version, digest, description.
  - Handle credentials: read the per-source-type optional Secret ref — `spec.oci.pullSecretRef` (dockerconfigjson), `spec.git.secretRef`, or `spec.http.secretRef` — and pass it to the matching fetcher. There is no top-level `spec.auth` field.
  - Report IndexFailed condition if fetch fails.

### ModuleReconciler
- **Responsibility:** Materialize Module → GameTemplate.
- **Key functions:**
  - Resolve module name/version via ModuleSource status.modules catalog.
  - Fetch OCI bundle (oras pull). The manifest body must hash to the manifest digest, and each layer body to the digest the manifest lists for it; a mismatch fails the pull (`PullFailed`). The signature check therefore covers every byte the bundle contributes.
  - Verify cosign signature if ModuleSource.spec.verify declared. On a successful install, record which digest was actually checked and under which policy in `status.verifiedDigest`/`status.verifyPolicy` — but only when the source declares a policy (key or keyless); a source with no `spec.verify` uses the always-succeeding Nop verifier, so nothing is recorded, even though `Verify()` returns nil. Both fields are cleared (left empty) on that no-policy path so a policy added to the source later doesn't make an already-installed Module look verified. The API and web dashboard use `verifiedDigest` (not the source's current policy) to decide whether the solid "verified" badge is warranted, as opposed to the softer "policy" badge for a declared-but-unchecked source.
  - Enforce `spec.digest`: a resolved bundle with a different digest fails with `DigestMismatch`. A Ready Module counts as converged only while a set `spec.digest` equals `status.appliedDigest`, so a pin added or changed after install is checked on the next reconcile.
  - The catalog's ModuleEntry.Digest describes only its LatestVersion's content, never an older pinned one, so convergence only compares `status.appliedDigest` against it when `spec.version` (or its resolved default) equals `LatestVersion`; a version pinned below latest converges without comparing the latest digest once its current generation is applied. Changing the Module's source, logical name, or other spec fields forces a fresh pull/apply even when the desired version string is unchanged.
  - Extract module.yaml + template.yaml from bundle.
  - Create GameTemplate CR with owner reference to Module (delete Module → delete template). A converged Module still confirms the owned GameTemplate exists on every reconcile — bypassing the CRD to delete it directly (e.g. `kubectl delete gametemplate`) is not supported (see the house rule above) and the next reconcile recreates it from the already-applied bundle.
  - Validate operator version against bundle's gameplaneMinVersion.
  - Report the root cause (signature mismatch, version too old, fetch failed, etc.) via the `Ready` condition set to `False` with a reason string naming the specific failure — there is no separate `InstallFailed` condition type.
  - Retry a Failed Module every `minRefreshInterval` (1 minute) via `RequeueAfter`. A spec change (new generation) or a change to its ModuleSource reconciles it at once. A same-generation retry keeps Phase=Failed while it re-pulls (no Pulling flip), and status is written only when something other than condition timestamps changes, so a Module stuck on the same failure does not rewrite its status or re-queue itself (F-258).
  - **Finalization:** Module deletion clears the ModuleFinalizer and deletes the owned GameTemplate. When the Module object deletion completes, the finalizer removal ignores NotFound errors (the Module may be deleted before the Update reaches the API server).

### ClusterStatusReconciler
- **Responsibility:** Periodic health checks on remote clusters.
- **Key functions:**
  - Guard: reserve "local" cluster name (returns Unhealthy + "NameReserved").
  - Read kubeconfig from Secret (spec.kubeconfigSecret).
  - Discover cluster version, ping API server.
  - Set status.phase (Unknown/Healthy/Unhealthy), status.message, status.lastCheckTime.
  - Report Healthy condition.
  - Requeue on interval (2 minutes). Status-only updates never trigger another probe; spec changes and annotation nudges still reconcile immediately, including after a slow or failed health check.

### NetworkCaptureReconciler
- **Status:** Implemented (Phase 2 Foundational + Phase 2 Implementation + Phase 7 US5 concurrency/failure detection + Phase 6 US4 retention/expiry); dashboard UI planned (Phase 8). Retention's file-cleanup call to the sidecar has both a real caller and a real callee end to end (sidecar `DELETE /captures/{id}` route + `CaptureClient.DeleteCaptureFile`) — see "Retention / TTL-based expiry" below, including the bounded best-effort cleanup budget that keeps a persistently unreachable sidecar from blocking the CR delete.
- **Responsibility:** Manage NetworkCapture CRD lifecycle — transition Pending → Running → Completed/Failed → Expired, inject capture sidecar, call sidecar control endpoint, monitor progress, enforce retention.
- **Key dependencies:**
  - `SidecarCaptureClient interface` — abstracted mTLS client to sidecar's `:9091` HTTP endpoints; injected for testability; production impl in `operator/internal/agent/sidecar_capture.go`. Four methods: `StartCapture`, `StopCapture`, `GetCaptureStatus`, `DeleteCaptureFile` (the last added for retention; see below).
- **Reconcile state machine:**
  - **Terminal phases (Completed, Failed):** no further lifecycle reconciliation, but still subject to the retention pass (`reconcileRetention`) described below — Kubernetes has no built-in TTL GC for custom CRDs, so this reconciler is the only enforcement of FR-007.
  - **Expired:** retried directly through `expireCapture` (file cleanup + CR delete) — reaching this phase on a still-existing object means a previous expiry attempt's CR deletion didn't land (e.g. an apiserver hiccup).
  - **Pending → Running:** Verify target GameServer exists and has `spec.capture.enabled = true`; enforce the concurrency lock (see "Edge cases" below); inject capture sidecar ephemeral container if not already present; record the observed Pod UID on the NetworkCapture (`gameplane.local/capture-pod-uid` annotation, used by the Running-phase health check below); call `SidecarClient.StartCapture()` with spec filter/maxDuration/maxSize; transition to Running; requeue every 5s to poll sidecar
  - **Running:** Before polling the sidecar, check retention (safety net, see below), then Pod health (see "Edge cases" below); if healthy, poll `SidecarClient.GetCaptureStatus()` every 5s; update `status.packetsWritten`, `status.bytesWritten`, `status.message`; once sidecar reports phase=Completed or phase=Failed, transition NetworkCapture phase and record `status.completionTime`
  - **Failure detection:** Pod-health checks (below) run before every sidecar poll; additionally, if GetCaptureStatus itself returns an error (sidecar unreachable), fail capture with reason `failed` and a descriptive message; if status.phase=Failed from sidecar, transition to Failed immediately
  - **User-requested stop:** see "Network capture stop flow" below.
- **Network capture stop flow (F-259; maintainer decision 2026-09-25):** The API never writes a capture's terminal phase. `POST :capture-stop` and `:capture-disable` only set the `gameplane.local/stop-requested` annotation (value: RFC3339 UTC time of the first request; set only if absent, so a repeat stop is a no-op). No CRD schema change. On reconcile, a NetworkCapture carrying the annotation in a non-terminal phase is handled before any other branch (`completeRequestedStop`):
  - **Pending (or not yet reconciled):** the Pending branch starts the sidecar before its own status write, so a stop request landing in between can leave the object Pending while the sidecar captures. The capture is completed directly with `message="stopped by user request"` and `SidecarStopped=True` (reason `never_started`), with no stop call, when there is evidence it never reached a sidecar: the `gameplane.local/capture-pod-uid` annotation is absent (it is persisted before `StartCapture`), the game pod `<gs>-0` is NotFound, has a different UID or is not in phase Running, or `SidecarClient.GetCaptureStatus()` returns 404 or `agent.ErrCaptureClientDisabled` (no mTLS). If the status call succeeds the sidecar is stopped exactly as in the Running case (reason `stopped`). Any other status error (timeout, 5xx, briefly unreachable pod) is returned for a requeue with backoff, bounded to 2 minutes after the stop-request time in the annotation (`pendingStopUnreachableTimeout`); after that a best-effort stop is sent and the capture completes, recording `SidecarStopFailed` if the stop fails, so an unreachable sidecar can never hold the GameServer's capture lock indefinitely.
  - **Running:** `SidecarClient.StopCapture()` is called first (the sidecar's `/stop` is synchronous and closes/flushes the PCAPNG), then the final packet/byte counts are refreshed from `GetCaptureStatus()` (best effort), the GameServer's `status.capture.activeCapture` lock is released and the capture is set to Completed with `message="stopped by user request"` and `SidecarStopped=True` (reason `stopped`). A failed sidecar stop still completes the capture and records `SidecarStopFailed=True`.
  - **Capture disabled on the GameServer:** `GameServerReconciler.stopActiveCaptures` also only sets the annotation (if absent) on every Pending/Running capture of that server and clears `status.capture.activeCapture`; it never writes a capture's phase. Completion goes through the same path above.
  - The annotation is left in place as a record; Completed/Failed/Expired captures ignore it. The watch is the plain `For(&NetworkCapture{})` with no predicates, so the annotation (metadata) update triggers a reconcile.
  - Why: before F-259 the API patched `phase=Completed` itself and the operator stopped the sidecar afterwards, so a client that downloaded as soon as it saw Completed hit the sidecar while it still held the capture as running and got a 409. Completed now always means the file is downloadable, and the API download handler only checks the phase.
  - **Rolling-upgrade fallback:** a Completed capture with `message="stopped by user request"` and no `SidecarStopped` condition (written by a pre-F-259 API or a pre-F-259 operator's capture-disable path) still gets exactly one `StopCapture` call, guarded by `SidecarStoppedCondition`.
- **Default filter (FR-003):** when `spec.filter` is nil or empty, the reconciler builds the filter it sends to `StartCapture` from the GameServer's template (`networkcapture_filter.go`): one `<tcp|udp> port <containerPort>` term per advertised port (`advertise: true`), in template order, duplicates dropped, joined with `or`. A port with no protocol is TCP. When the template advertises no port, or the template is missing, the capture goes `Failed` with a message and the sidecar is never called. The sidecar keeps refusing an empty filter, so a capture never records unfiltered traffic. A filter set on the capture is sent unchanged.
- **Ephemeral container injection:** Inject into game pod's spec.ephemeralContainers subresource with:
  - **Name:** "capture"
  - **Image:** operator CLI flag `--capture-sidecar-image` (default `ghcr.io/gameplanepanel/gameplane/capture-sidecar:dev`)
  - **TargetContainerName:** "game" (shares pid/network/ipc namespaces with the game container for packet capture)
  - **VolumeMounts:**
    - `captures` (emptyDir pre-provisioned by gameserver_controller.go) mounted at `/tmp/captures` (read-write)
    - `agent-tls` (Secret holding agent mTLS cert+key+CA) mounted at `/etc/tls` (read-only); certs passed via env vars below
  - **Environment:**
    - `TLS_CERT_FILE=/etc/tls/tls.crt` — client cert for mTLS to agent
    - `TLS_KEY_FILE=/etc/tls/tls.key` — private key for mTLS  
    - `TLS_CA_FILE=/etc/tls/ca.crt` — CA bundle for verifying agent server cert
  - **SecurityContext:**
    - `RunAsNonRoot: true` — container runs as unprivileged user (no root)
    - `AllowPrivilegeEscalation: true` — **CRITICAL:** Must be true for file capabilities (CAP_NET_RAW/CAP_NET_ADMIN via setcap on the capture binary) to work. Kernel only honors setcap'd capabilities on exec when no_new_privs is off; combined with Capabilities.Drop: ["ALL"]/Add: ["NET_RAW"] below, the container starts with zero effective capabilities but the setcap'd binary gains CAP_NET_RAW at exec time
    - `ReadOnlyRootFilesystem: true` — mounts /tmp/captures and /etc/tls as the only writable paths
    - `Capabilities: Drop: ["ALL"], Add: ["NET_RAW"]` — Drop ALL empties the process's effective capability set (no ambient capabilities are set either). Drop ALL alone would also empty the *bounding* set, which makes the kernel refuse to grant the setcap'd binary's file capability at execve (EPERM) — so NET_RAW is re-added to keep it in the bounding set. The grant itself still comes from the binary's file capability (setcap cap_net_raw+ep) at exec, never from this Add alone.
  - **No imagePullPolicy set** — inherits from pod spec (usually IfNotPresent or Always per deployment)
- **Concurrency enforcement:** see "Edge cases" below.
- **mTLS client reuse:** `operator/internal/agent/sidecar_capture.go`'s `CaptureClient` reuses the agent's existing mTLS http.Client (same CA cert/client cert/private key material) and addresses the sidecar via Service DNS: `https://<gs>-agent.<ns>.svc.cluster.local:9091`
- **Not implemented (planned for later phases):**
  - Network-partition recovery (a capture that outlives a transient loss of connectivity to the sidecar, rather than being failed outright) — no US claims this; today any sidecar-unreachable error fails the capture
  - Dashboard UI for starting/stopping captures and browsing history — Phase 8

#### Retention / TTL-based expiry (Phase 6 / US4)

- **Effective TTL resolution (`effectiveRetentionSeconds`, `clampRetention`):** a capture's TTL is `spec.ttlSecondsAfterFinished` when set, else the reconciler's `CaptureDefaultRetentionSeconds`, clamped to `CaptureMaxRetentionSeconds` either way. Those two `int32` reconciler fields are set in `operator/cmd/main.go` from the `--capture-default-retention-seconds` / `--capture-max-retention-seconds` flags (defaults 86400 / 604800); when left at their zero value (e.g. a test constructing the reconciler directly) they fall back to this file's own package constants `defaultCaptureRetentionSeconds = 86400` (24h) and `maxCaptureRetentionSeconds = 604800` (7 days) — the same values ratified in `specs/done_003-network-capture-sidecar/data-model.md` and already enforced as `NetworkCaptureSpec.TTLSecondsAfterFinished`'s own `kubebuilder:validation:Minimum=60`/`Maximum=604800` ceiling, so a value that reaches this arithmetic has, in the normal path, already been bounded by the apiserver. `clampRetention` is a defensive backstop for a hand-applied or legacy object, not the primary enforcement mechanism.
- **Expiry boundary (`captureExpiresAt`, `captureIsExpired`):** `expiresAt = anchorTime + ttlSeconds`; expired iff `expiresAt.Before(now)` — a **strict** less-than, matching data-model.md's documented transition condition `completionTime + ttl < now()`. A capture whose `now` lands exactly on `expiresAt` is not yet expired. `anchorTime` is `status.completionTime` for a terminal capture; a nil anchor (no completionTime yet) never expires (`captureExpiresAt` returns the zero `time.Time`, and `captureIsExpired` treats a zero `expiresAt` as never-expired).
- **Terminal-phase pass (`reconcileRetention`):** on every reconcile of a Completed/Failed capture, computes `expiresAt` from `status.completionTime`; if not yet expired, requeues at exactly `expiresAt.Sub(now)` — the exact remaining TTL, not a blind fixed poll, so expiry lands close to on time without a hot requeue loop. `retentionPollInterval` (60s, matching research.md's proposed interval and BackupSchedule's cadence) is used only as a backstop when `completionTime` is unexpectedly nil, which should not happen for a capture this reconciler itself transitioned to a terminal phase.
- **Running-phase safety net (`expireStuckRunningCapture`):** a capture that never reaches a terminal phase on its own (e.g. the sidecar stops reporting without actually finishing) would otherwise poll forever, since TTL is normally anchored to `completionTime`. The Running branch additionally anchors to `status.startTime` and, once that window elapses, force-stops the capture on the sidecar (best-effort — the sidecar may already be gone), clears the GameServer's active-capture pointer, sets a `RetentionStopFailed` condition if the stop call errored, and falls through to the same `expireCapture` path a terminal capture uses — this is checked *before* the pod-health/`GetCaptureStatus` poll, so a stuck-and-expired capture never reaches the sidecar for a status check first.
- **Expiry itself (`expireCapture`):** transitions `status.phase` to Expired (persisting first, stamping `completionTime` if still nil, unless already Expired — making the function idempotent for the retry-on-still-present path above), then calls `SidecarClient.DeleteCaptureFile(ctx, namespace, serverName, captureName)` (implemented end to end: sidecar `DELETE /captures/{id}` route + `CaptureClient.DeleteCaptureFile`), then `r.Delete`s the CR. File deletion is best-effort and bounded: a failure is recorded as a `FileCleanupFailed` condition and retried with backoff (30s) for up to `captureFileCleanupBudget` (5 minutes) from the first failure — e.g. while the GameServer is suspended and its `-agent` has no endpoints — after which the file is abandoned and the CR is deleted anyway rather than holding the CR delete open indefinitely on a persistently unreachable sidecar.

#### Edge cases (Phase 7 / US5)

- **Concurrency lock lives in the reconciler, not the API.** `api/internal/handlers/capture.go`'s `captureStart` handler does check `status.capture.activeCapture` / the server's own NetworkCaptures before creating a CR (`hasActiveCapture`), and rejects with HTTP 409 when it finds one — but that check is a best-effort fast path only, not the lock. Two concurrent `POST :capture-start` requests can both read "no active capture" and both pass that check before either has created its NetworkCapture CR, since there is no compare-and-swap between the API's read and its `CreateNetworkCapture` call: an HTTP handler has no way to hold a lock across the two Kubernetes API calls that would need to be atomic (list-then-create), and even if it could, only a single API replica's in-process lock would be enforced — a second API pod behind the same Service knows nothing about it. NetworkCaptureReconciler is the only place both requests' resulting CRs are guaranteed to be reconciled through a single, serialized queue (controller-runtime processes one object per workqueue item, and a List+decide+Update within one Reconcile call is effectively atomic against another Reconcile of a different object racing it into the same decision), so it is the sole point that can actually guarantee "exactly one Running capture per GameServer" rather than merely making the common case fast. On the Pending→Running transition, the reconciler lists every Pending/Running NetworkCapture for the same `spec.serverRef.name` (including its own not-yet-persisted Pending state) and keeps only the earliest-created one (by `creationTimestamp`, tie-broken by object name for entries created within the same timestamp resolution); every other one is failed immediately with condition reason `capture_already_in_progress` and never gets an ephemeral container or a sidecar `/start` call. The API's check exists purely so the ordinary case — no race — gets a fast 409 without ever creating a CR that would just be failed a moment later; it is documented in `api/internal/handlers/capture.go` as a fast path with an explicit pointer back to this section.
- **Pod restart / eviction detection.** The reconciler records the observed game Pod's UID on the NetworkCapture (`gameplane.local/capture-pod-uid` annotation) at the moment it starts the capture — not a new CRD status field, to avoid a schema/codegen change for what is pure internal bookkeeping. On every Running-phase reconcile, before trusting the sidecar's reported status, the reconciler re-fetches the game pod (`<gs>-0`): if it's gone (`NotFound`), or present but its UID no longer matches the recorded one (the StatefulSet rebuilt a pod with the same name after a delete/eviction), the capture transitions straight to Failed with condition reason `PodRestarted`, skipping the sidecar poll entirely — a dead or replaced sidecar can't answer `GetCaptureStatus` meaningfully anyway, and a same-named replacement pod would otherwise surface as a misleading "capture not found" from the sidecar client rather than the real cause. `NetworkCaptureReconciler.mapPodToNetworkCaptures` watches game Pods directly (label-based lookup, since the Pod is owned by its StatefulSet, never by a NetworkCapture, so `Owns()` would never fire) and enqueues the Running NetworkCapture immediately on the delete/recreate event rather than waiting for the next 5s poll. Either way, the terminal transition clears the parent GameServer's `status.capture.activeCapture`, same as any other Failed/Completed transition.
- **Sidecar crash, no retry.** On the same Running-phase pre-poll check, the reconciler also inspects the pod's `status.ephemeralContainerStatuses` entry for the capture container: if it reports `State.Terminated` with a non-zero exit code while the pod itself is still present and its UID unchanged (i.e. the game container is fine, only the sidecar process died), the capture is failed with condition reason `SidecarCrashed`. There is no retry path — Terminal phases (Completed/Failed/Expired) short-circuit at the top of `Reconcile` before any further work, so a crashed capture stays Failed; starting a fresh capture requires the caller to issue a new `POST :capture-start`, which is deliberate (mid-capture state on the sidecar side, e.g. a partially-written PCAPNG file, cannot be safely resumed).

### Sidecar client (operator/internal/agent/sidecar_capture.go)

- **`SidecarCaptureClient interface`** — injected into NetworkCaptureReconciler; abstracts mTLS communication with capture sidecar's `:9091` HTTP control endpoint (testable; tests inject a stub)
  - `StartCapture(ctx, namespace, serverName, captureID string, filter *string, maxDurationSeconds, maxSizeBytes int64) error` — POST `/captures/{id}/start` on sidecar
  - `StopCapture(ctx, namespace, serverName, captureID string) error` — POST `/captures/{id}/stop` on sidecar
  - `GetCaptureStatus(ctx, namespace, serverName, captureID string) (phase string, packetsWritten, bytesWritten int64, message string, err error)` — GET `/captures/{id}/status` on sidecar; polls current state during Running phase
  - `DeleteCaptureFile(ctx, namespace, serverName, captureID string) error` — added for retention (Phase 6 / US4); removes a capture's backing PCAPNG file once its TTL elapses. Implemented end to end: `DELETE /captures/{id}` on the sidecar (`capture-sidecar/internal/httpserver/handlers.go`), calling `CaptureClient.DeleteCaptureFile` (`operator/internal/agent/sidecar_capture.go`) — success on 204, treated as idempotent on 404 (already gone), a distinguishable error on 409 (still running).
- **`CaptureClient struct`** — production implementation of SidecarCaptureClient
  - `NewCaptureClient(agent *Client) *CaptureClient` — factory; reuses agent's existing mTLS http.Client (same CA cert/client cert/private key material)
  - `Disabled bool` flag — set to true if no mTLS material provided (graceful degradation). `StartCapture`, `StopCapture`, and `DeleteCaptureFile` return `nil` on Disabled=true; `GetCaptureStatus` instead returns `ErrCaptureClientDisabled` so a Pending-phase reconcile can tell "no mTLS configured" apart from a real sidecar answer.
  - URLs built from `https://<serverName>-agent.<namespace>.svc.cluster.local:9091/captures/<captureID>/start` and `.../stop` (service DNS, cluster-internal mTLS endpoint)
  - All errors wrapped with `%w` per CLAUDE.md rule 6

### Helper Reconcilers & Utilities

- **agent_certs.go:** Generate per-GameServer CA-signed mTLS server cert for agent sidecar (operator CA cert/key injected via flags).
- **agent_rbac.go:** Create per-GameServer ServiceAccount + Role for the agent sidecar (used to verify token).
- **internal/kube/capture.go:** Kubernetes client helper functions for NetworkCapture CRD operations (API-tier use):
  - `CreateNetworkCapture(ctx, ns, captureID, serverName string, filter *string, maxDuration *metav1.Duration, maxSize *resource.Quantity, ttlSecondsAfterFinished *int32) (*NetworkCapture, error)` — creates NetworkCapture CR with Pending phase; sets ownerRef for cascade delete
  - `GetNetworkCapture(ctx, ns, name string) (*NetworkCapture, error)` — fetches by namespace/name; returns nil if not found
  - `ListNetworkCaptures(ctx, ns, serverName string) ([]NetworkCapture, error)` — lists all captures in namespace; filters by spec.serverRef.name on client side
  - `DeleteNetworkCapture(ctx, ns, name string) error` — deletes NetworkCapture by namespace/name
- **metrics.go:** Prometheus collectors — GameServerCollector (count by phase), BackupCollector (count by phase).
- **retention.go:** Backup retention logic — parse keep-* rules, identify excess backups for deletion.
- **restic_summary.go:** Parse restic container logs, extract final JSON summary (snapshot ID, duration, size).
- **semver.go:** Semantic versioning utilities (compare version strings against gameplaneMinVersion).
- **helpers.go:** Shared utilities (updateCondition, upsertCondition, phase transition helpers).

## External interface / contracts

### Entry point: `cmd/main.go`

**CLI flags:**

| Flag | Type | Default | Purpose |
|------|------|---------|---------|
| `--metrics-bind-address` | string | `:8080` | Prometheus metrics endpoint address. |
| `--health-probe-bind-address` | string | `:8081` | Liveness/readiness probes address. |
| `--leader-elect` | bool | `false` | Enable leader election (multi-replica deployments). |
| `--agent-image` | string | `ghcr.io/gameplanepanel/gameplane/agent:dev` | Container image for injected agent sidecar. |
| `--agent-image-pull-policy` | string | `` | ImagePullPolicy override (Always/IfNotPresent/Never); empty leaves unset. |
| `--config-init-image` | string | `controller.DefaultConfigInitImage` | Init container for rendering config files onto data volume. |
| `--restic-image` | string | `controller.DefaultResticImage` | restic backup/restore Job image. |
| `--backup-job-backoff-limit` | int64 | 2 | BackoffLimit of restic backup/restore Jobs; must be >= 0. |
| `--backup-job-active-deadline-seconds` | int64 | 86400 | ActiveDeadlineSeconds of restic backup/restore Jobs; must be > 0. |
| `--sentinel-image` | string | `controller.DefaultSentinelImage` | Wake-on-connect sentinel container image. |
| `--tunnel-frp-image` | string | `controller.DefaultTunnelFrpImage` | frp tunnel client container image. |
| `--tunnel-tailscale-image` | string | `controller.DefaultTunnelTailscaleImage` | Tailscale tunnel client container image. |
| `--tunnel-playit-image` | string | `controller.DefaultTunnelPlayitImage` | Playit tunnel client container image. |
| `--agent-log-level` | string | `` | Log level (debug/info/warn/error) injected into agent as GAMEPLANE_LOG_LEVEL; empty skips. |
| `--module-namespace` | string | `gameplane-system` | Namespace where ModuleSource credential Secrets live. |
| `--module-local-root` | string | `` | Base directory for local-type ModuleSources; empty disables local sources. |
| `--agent-ca-bundle` | string | `` | PEM-encoded CA cert signing agent server certs (operator → agent mTLS). |
| `--agent-client-cert` | string | `` | PEM-encoded client cert for operator → agent calls. |
| `--agent-client-key` | string | `` | PEM-encoded private key for agent client cert. |
| `--agent-ca-secret-name` | string | `gameplane-agent-ca` | Name of Secret holding agent CA cert+key for signing per-GameServer certs. |
| `--agent-ca-secret-namespace` | string | `gameplane-system` | Namespace of agent CA Secret. |
| `--control-plane-namespace` | string | `gameplane-system` or `POD_NAMESPACE` env | Namespace where operator runs and where cluster kubeconfig Secrets live. |
| `--game-data-storage-class` | string | `` (cluster default), or `GAMEPLANE_GAME_DATA_STORAGE_CLASS` env | StorageClass name for game server data volumes (PVC.spec.storageClassName). Empty value is a no-op, preserving pre-feature behavior. Overridable per-GameServer or per-template; precedence chain: GameServer.spec > template.spec > this flag > cluster default. Applied on PVC creation only, never mutated on existing PVCs. |
| `--game-ingress-policy` | bool | `true` | Reconcile per-GameServer ingress NetworkPolicy. |
| `--game-ingress-from-cidr` | strings | `0.0.0.0/0` | Source CIDR(s) admitted to game ports; repeatable; canonical form enforced. |
| `--address-manager` | string | `none`, or `GAMEPLANE_ADDRESS_MANAGER` env | Load-balancer address-manager flavor (metallb, cilium, or none). Validated at startup; controls how spec.networking.addressPool / .address preferences are translated onto the Service. |
| `--metallb-namespace` | string | `metallb-system` | Namespace MetalLB's CRDs (IPAddressPool) are looked up in, for the direct pool-existence check backing the AddressAssignment condition. |
| `--capture-enabled` | bool | `false` | Enable the network capture feature cluster-wide. When false, capture capability is disabled and cannot be enabled per-GameServer. |
| `--capture-default-retention-seconds` | int64 | `86400` | Default retention period (seconds) for completed captures; applied when spec.capture.retentionSeconds is not set. 24-hour default. |
| `--capture-max-retention-seconds` | int64 | `604800` | Maximum retention period (seconds) for captures; clamps any higher retention request. 7-day default, a storage-limitation-informed constraint. |
| `--capture-default-max-duration-seconds` | int64 | `300` | Default maximum duration (seconds) for a single capture when the request does not provide an explicit `maxDuration`. 5-minute default. |
| `--capture-default-max-size-bytes` | int64 | `5368709120` | Default maximum file size (bytes) for a single capture when the request does not provide an explicit `maxSize`. 5 GiB default. |
| `--capture-sidecar-image` | string | `ghcr.io/gameplanepanel/gameplane/capture-sidecar:dev` | Container image for the network capture sidecar injected when capture is enabled. |

**Manager configuration:**
- CacheSyncTimeout: 5 minutes (extended from default 2m to tolerate slow apiservers on resource-constrained nodes).
- Scheme: includes Kubernetes types, gameplane.local/v1alpha1 types, and CSI VolumeSnapshot types.

### Codegen invariants

After any edit to `operator/api/v1alpha1/*_types.go`:

```sh
make generate && make manifests
```

Regenerates and commits atomically:
- `operator/api/v1alpha1/zz_generated.deepcopy.go` — struct deepcopy methods (includes CaptureConfiguration, CaptureStatus, NetworkCapture, NetworkCaptureList, NetworkCaptureSpec, NetworkCaptureStatus deepcopy functions).
- `operator/config/crd/gameplane.local_*.yaml` — 9 CRD manifests (includes gameplane.local_networkcaptures.yaml; gameplane.local_gameservers.yaml extended with capture spec/status schemas).
- `operator/config/rbac/*.yaml` — ServiceAccount, Roles, RoleBindings, ClusterRoles, ClusterRoleBindings (includes pods/ephemeralcontainers and networkcaptures CRUD permissions).
- `charts/gameplane/crds/*.yaml` — copy of CRDs for Helm integration (Helm `crds/` directory + pre-upgrade hook for `kubectl apply --server-side`).

Forgetting codegen leaves the YAML out of sync with types — CI's `make manifests` verify gate will catch it, but envtest runs will fail mysteriously first.

## Key invariants

1. **Operator is authoritative.** All business logic lives in reconcilers; API is a UX layer that writes CRs. Users can `kubectl apply` and get the same outcome as the dashboard.

2. **No CEL budget overruns.** CRD validation rules (XValidation) in unbounded maps/arrays must include maxProperties/maxItems + maxLength caps, or the apiserver rejects the CRD at install time and envtest panics.

3. **Codegen is mandatory after CRD type edits.** Generated deepcopy + YAML must ship in the same commit as type changes.

4. **CRDs are owned by the control plane, not Helm.** Helm's `crds/` is applied only on first install; updates come from a pre-upgrade hook running `kubectl apply --server-side --server-side-apply-manager=gameplane` on every `helm upgrade`, which also fires on a `helm install` over leftover CRDs whose `gameplane.local/crd-bundle-sha256` stamp (written by `make manifests` via `hack/sync-chart-crds.sh`) differs from the chart's (F-218). CRDs are never owned or deleted by Helm.

5. **Agent mTLS is optional but recommended.** Operator boots without `--agent-ca-bundle`/`--agent-client-cert`/`--agent-client-key` (client.Disabled=true); Agent methods silently no-op. Production installs should supply all three.

6. **Module sources are immutable once pulled.** Digest pinning (spec.digest) defeats tag moves; version pinning (spec.version) tracks a specific semver. Floating (unset version/digest) tracks latest and re-pulls on ModuleSource refresh.

7. **Backup quiesce is best-effort.** If agent unavailable, backup proceeds raw (no pause). If quiesce unsupported (agent returns ErrUnsupported), backup continues degraded (success-with-note).

8. **Remote cluster health checks are non-blocking.** A Cluster with health Unhealthy does not prevent GameServer creation on the local cluster; it surfaces the issue so operators can intervene.

9. **Capture volume is immutable and pre-provisioned.** Every game pod carries a `captures` emptyDir (1 GiB) unconditionally, because Kubernetes pod.spec.volumes is immutable on running pods. This incurs a rolling restart of all existing game pods once on upgrade, regardless of whether capture is ever used. The volume is mounted only on the capture sidecar ephemeral container (injected conditionally based on spec.capture.enabled and cluster-wide `--capture-enabled`), never on the agent or game container.

10. **Capture sidecar uses ephemeral containers, not init/sidecar containers.** Ephemeral containers are added live without restarting the game pod or agent. They have no imagePullPolicy, volumeMounts, or named containerPorts; the capture sidecar's port (9091) is exposed via the numeric TargetPort on the <gs>-agent Service. Disabling capture (spec.capture.enabled: false) does not remove the ephemeral container from running pods (Kubernetes API does not support removal); it stops accepting new captures.

11. **Capture TTL-based expiry is implemented (Phase 6 / US4), end to end.** `NetworkCaptureReconciler` reconciles the full Pending → Running → Completed/Failed → Expired lifecycle, including retention: see "Retention / TTL-based expiry" under NetworkCaptureReconciler above for the expiry arithmetic, the requeue-at-exact-remaining-TTL behavior, the Running-phase safety net for a capture that never reaches a terminal phase, and the bounded best-effort cleanup budget that keeps a persistently unreachable sidecar from blocking the CR delete. `SidecarClient.DeleteCaptureFile` (called by the retention pass) has a real production implementation on both sides: the sidecar's `DELETE /captures/{id}` route and `CaptureClient.DeleteCaptureFile`.

## Dependencies

**Direct (from go.mod):**

| Module | Version | Purpose |
|--------|---------|---------|
| k8s.io/api | v0.37.1 | Kubernetes core types (Pod, StatefulSet, Service, Job, etc.). |
| k8s.io/apimachinery | v0.37.1 | Kubernetes API machinery (metav1, runtime.Scheme, etc.). |
| k8s.io/client-go | v0.37.1 | Kubernetes client (for exec, logs, discovery). |
| sigs.k8s.io/controller-runtime | v0.25.1 | Reconciler framework (Manager, Builder, Reconciler interface). |
| github.com/GameplanePanel/gameplane/netguard | local | SSRF dial guard (permissive policy for module fetches from private registries). |
| github.com/go-git/go-git/v5 | v5.19.2 | Git operations (clone, fetch) for ModuleSources. |
| github.com/go-git/go-billy/v5 | v5.9.1 | VCS filesystem abstraction for go-git. |
| github.com/google/go-containerregistry | v0.22.1 | OCI image operations (push, pull, digest). |
| github.com/kubernetes-csi/external-snapshotter/client/v8 | v8.6.0 | VolumeSnapshot API types. |
| github.com/opencontainers/go-digest | v1.0.0 | OCI digest parsing. |
| github.com/opencontainers/image-spec | v1.1.1 | OCI image spec types. |
| github.com/prometheus/client_golang | v1.24.1 | Prometheus metrics registration & exposition. |
| github.com/robfig/cron/v3 | v3.0.1 | Cron expression parsing (BackupSchedule). |
| github.com/sigstore/cosign/v2 | v2.6.5 | cosign signature verification (module bundles). |
| github.com/sigstore/sigstore | v1.10.9 | sigstore primitives (Fulcio roots, certificate chains). |
| golang.org/x/crypto | v0.57.0 | Cryptographic primitives (TLS, X.509). |
| golang.org/x/mod | v0.41.0 | Semantic versioning (semver package). |
| oras.land/oras-go/v2 | v2.6.2 | OCI artifact pull (module bundles). |
| sigs.k8s.io/yaml | v1.6.0 | YAML marshaling (CRD manifests). |

**Indirect:** Transitively pulled by the above (go-logr, crypto libraries, etc.).

## Data & persistence

**State location:** Entirely in CRD status subresources and Kubernetes objects created by the operator.

**No external database.** All persistent state lives in:
- CRD status fields (gameserver.status.phase, backup.status.snapshotID, etc.)
- Created child objects:
  - StatefulSet, Service, PVC (GameServer)
  - ConfigMap (game startup config)
  - Secret (game credentials, RCON password, agent certs)
  - Job (backup/restore restic Jobs)
  - NetworkPolicy (per-GameServer ingress rules)
  - VolumeSnapshot (CSI backups)
  - ServiceAccount, Role, RoleBinding (agent RBAC)

**Backup data:** Stored outside the cluster (S3, B2, restic rest-server, local filesystem). Operator creates the restic Job; destination and credentials are supplied by GameTemplate.backup.* spec.

## Security considerations

1. **cosign signature verification:** ModuleSource.spec.verify declares keyed (public key Secret) or keyless (Rekor + transparency log) verification. Operator refuses to install bundles with invalid/missing signatures if verify is declared. The OCI client checks the pulled manifest and every layer against their digests before use, so the verified digest covers the installed content. Signature fetching selects the pull Secret entry matching the source registry hostname (including its port); Docker Hub's docker.io, index.docker.io and registry-1.docker.io aliases may share credentials, with an exact entry taking precedence. An unmatched registry uses anonymous access, never another registry's credentials.

2. **SSRF dial guard (netguard):** ModuleSource fetch (git clone, HTTP download) uses netguard's permissive IsAllowed policy — allows self-hosted registries on private addresses (10.0.0.0/8, etc.), but blocks obvious metadata-service endpoints (169.254.169.254). Agent module install (`capabilities.mods.install`) uses strict IsPublic policy, rejecting private IPs.

3. **Agent mTLS:** Operator → agent calls are over HTTPS with client+server certs. Server certs are per-GameServer, signed by an operator-held CA. Operator's CA cert, client cert, and private key injected via CLI flags (never in YAML).

4. **Agent RBAC:** Each GameServer gets a unique ServiceAccount + Role (verb:exec on that Pod only). Agent token is bound to that SA and verified by the operator before accepting quiesce/unquiesce calls.

5. **Operator RBAC split:** The operator's permission model separates cluster-wide reads from namespace-scoped writes, enforcing least privilege and limiting blast radius if the operator token is compromised. The ClusterRole (`manager-role`, bound as `gameplane-operator-manager`) grants get/list/watch on Gameplane CRDs, Secrets, ConfigMaps, Services, PVCs, Pods, ServiceAccounts, Roles/RoleBindings, VolumeSnapshots and NetworkPolicies; writes are restricted to the cluster-scoped CRDs (GameTemplate CRUD, Module update/patch and finalizers), to CRD `/status` subresources, and to Events. A Role in each managed namespace (`gameplane-games` by default, and any user-defined namespace) grants the operator create/update/patch/delete on GameServers, Backups, BackupSchedules, Restores, NetworkCaptures, StatefulSets, Deployments, Jobs, Services, PVCs, ConfigMaps, Secrets, ServiceAccounts, Roles/RoleBindings, NetworkPolicies and VolumeSnapshots, `update` on the GameServer, Backup, BackupSchedule and Restore finalizers, plus `pods/attach create` and `pods/ephemeralcontainers` get/patch/update. The split confines writes to managed namespaces; cluster-wide reads, including Secrets, remain. **Agent authentication direction:** The operator creates per-GameServer ServiceAccount, Roles, and RoleBindings in the managed namespace. The agent pod runs as that ServiceAccount and uses its RBAC to heartbeat (patch) its GameServer's status. The operator calls the agent sidecar via mTLS at `<name>-agent.<namespace>.svc.cluster.local:8090`; the agent does not call the operator.

6. **Network policies:** Per-GameServer ingress NetworkPolicy admits only the advertised game ports from declared CIDR(s) (default 0.0.0.0/0, customizable via `--game-ingress-from-cidr`).

7. **Finalizers:** Controllers use ownership and finalizers to ensure cleanup (e.g., Module deletion cascades to GameTemplate; a quiesced Backup's `gameplane.local/backup-finalizer` blocks deletion until the matching unquiesce is sent, so `kubectl delete` can't drop it and leave the game with auto-save off; associated Jobs are reclaimed via ownerReference GC, not a finalizer).

## Testing & coverage

**Envtest tier (controller-runtime envtest, real Kubernetes API):** Co-located `*_envtest_test.go` files in `operator/internal/controller/`. Tests create CRs, advance reconcilers, assert status fields and child objects. Covers controller logic without kind-cluster overhead.

**Unit tests:** Smaller scope, testing individual functions (e.g., cron parsing, retention logic, restic log parsing).

**Coverage gate:** `operator/.testcoverage.yml` enforces **72% total coverage** (unit + envtest merged). Excludes:
- `cmd/` — main.go + flag/signal wiring.
- `api/v1alpha1/` — mostly generated deepcopy.
- `hack/` — codegen helpers not shipped.

CI run: `make cover` generates merged profile (unit + envtest), `make cover-ratchet` shows per-module headroom.

## References

- **`docs/architecture.md`** — full system architecture, data flow, security boundaries.
- **`docs/security.md`** — auth, RBAC, threat model, pod security.
- **`docs/module-authoring.md`** — OCI bundle format, module.yaml schema, template.yaml spec.
- **`CLAUDE.md`** — rules 7 (codegen), 9 (K8s-native), 10 (operator is authoritative).
- **`Makefile`** — canonical source of build, test, lint commands.
- **`.golangci.yml`** — linter rule set (no nolint directives without cause; fix the code).
- **`.editorconfig`** — indentation: tabs in Go, 2 spaces in YAML; LF line endings.
- **`go.mod` / `go.sum`** — dependency lock files.
- **CRD YAML:** `operator/config/crd/gameplane.local_*.yaml` (generated from types).
- **RBAC YAML:** `operator/config/rbac/*.yaml` (generated, defines operator ServiceAccount + permissions).
- **Helm integration:** `charts/gameplane/crds/` + `charts/gameplane/templates/crd-apply-hook.yaml` (pre-upgrade CRD sync).
