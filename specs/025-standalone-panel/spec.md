# Feature Specification: Standalone Panel and API

**Feature Branch**: `feat/standalone-panel` (contributor fork `Baylem/Gameplane`, merged as PR #630, merge commit `6ed25c1`)

**Created**: 2026-10-07

**Status**: Implemented (merged to `master` 2026-10-14). This folder was written retroactively; see [Context](#context).

**Input**: Contributor design note for PR #630, preserved verbatim in [Original design note](#original-design-note-pr-630).

## Context

PR #630 (contributor Baylem) shipped this feature with a single design note at `specs/023-standalone-panel/spec.md`. That number clashed with `specs/023-change-based-test-selection/`, so the folder was renamed to `specs/025-standalone-panel/`. The spec was then rewritten into the spec-kit template, and the plan and tasks were written after the fact. They describe what PR #630 built and do not add work. Requirements the merged code does not fully meet are listed under [Known gaps](#known-gaps) and have no tasks.

## Clarifications

### Session 2026-10-14 (valgul, on PR #630 review)

- Q: Should the panel API support overlapping processes (rolling updates, several replicas)? → A: No. Keep one API process with the `Recreate` strategy, because the audit hash chain and user-management safeguards use process-local locks. Multi-replica HA would be a separate future feature.
- Q: Must standalone installs send a telemetry marker before merge? → A: No. A telemetry marker for standalone installs is a follow-up, not part of this feature.
- Q: Must the chart enforce `--skip-crds` for the panel-only profile? → A: Not in this feature. The requirement is documented; valgul owes a follow-up issue.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Run a panel with no local cluster (Priority: P1)

An operator runs the dashboard and API on a host that is not a Kubernetes cluster, such as a VM with Docker Compose. The panel starts with no kubeconfig, no Kubernetes service account, no operator and no agent. The operator bootstraps an admin, logs in, manages panel settings, and sees empty cluster and fleet lists. Nothing survives as a fake `local` cluster, and everything persists across restarts.

**Why this priority**: This is the feature's purpose. Without it, every panel needs a local cluster.

**Independent Test**: Start the Compose profile on a host with no Kubernetes, bootstrap an admin, log in, list clusters and fleet, restart, and log in again.

**Acceptance Scenarios**:

1. **Given** a host with no Kubernetes credentials, **When** the API starts in standalone mode, **Then** it starts without consulting a kubeconfig or in-cluster credentials and registers no `local` cluster.
2. **Given** a fresh standalone panel, **When** an authenticated user lists clusters or the fleet, **Then** both lists succeed and are empty.
3. **Given** a standalone panel with an admin and settings, **When** the containers restart, **Then** the admin can log in and settings are unchanged.
4. **Given** standalone mode, **When** cluster-ops (local credential minting) is also requested, **Then** startup fails with a clear error.

---

### User Story 2 - Register, rotate and remove remote clusters (Priority: P1)

An administrator registers a remote Kubernetes cluster (which runs its own operator and agents) by pasting a self-contained kubeconfig. The panel stores the credential encrypted, checks the cluster's health on a schedule, and lets the administrator replace the kubeconfig or the agent-gateway credentials, or remove the registration.

**Why this priority**: A standalone panel is useless until it can manage remote clusters.

**Independent Test**: Register a cluster, confirm its credential is ciphertext in the database, rotate its kubeconfig, restart, and remove it.

**Acceptance Scenarios**:

1. **Given** a valid kubeconfig with a verified HTTPS server, **When** it is registered, **Then** the registration and its credential are committed together, or neither is.
2. **Given** a kubeconfig that disables TLS verification, sets a proxy, or points at loopback, metadata or another unsafe address, **When** it is registered, **Then** the request is rejected.
3. **Given** a registered cluster, **When** its kubeconfig is replaced, **Then** the registration keeps its gateway settings and grants, and the old credential is removed.
4. **Given** any registration or gateway request, **When** a response body, error or log line is produced, **Then** it contains no credential material.

---

### User Story 3 - Work on remote clusters from the dashboard (Priority: P2)

A user picks a registered cluster in the dashboard and manages workloads, modules, module sources, backup destinations and node inventory there. Every request names the selected cluster explicitly and is checked against the user's grants for that cluster. Agent operations (console, files, logs) go through the remote cluster's agent gateway.

**Why this priority**: Day-to-day use after registration. It depends on User Story 2.

**Independent Test**: With two registered clusters and a user granted on one, confirm module and inventory requests reach only the selected cluster and are refused on the other.

**Acceptance Scenarios**:

1. **Given** zero registered clusters, **When** the dashboard loads, **Then** it shows a "register a cluster" or "select a cluster" state and sends no inventory request to the panel host.
2. **Given** standalone mode, **When** a module request omits the cluster or names `local`, **Then** the API refuses it.
3. **Given** a user without a cluster-wide modules or templates grant on the selected cluster, **When** they open Modules, **Then** management controls are disabled and writes are refused.
4. **Given** a logged-out visitor, **When** they load the login page, **Then** nothing reveals whether the panel is standalone.

---

### User Story 4 - Keep the panel key safe and rotate it (Priority: P2)

An operator keeps the encryption key on storage separate from the database, or provisions it read-only from a secret store. The panel refuses an unsafe, missing or wrong key rather than starting with data it cannot read. The operator can rotate the master key offline.

**Why this priority**: Credential safety for every registered cluster depends on it.

**Independent Test**: Start with a provisioned read-only key, then with a wrong key (startup fails), then rotate to a new key and confirm every credential still decrypts.

**Acceptance Scenarios**:

1. **Given** a key file with unsafe ownership, permissions, type, size, links or ACLs, **When** the API starts, **Then** it refuses to use it.
2. **Given** a database holding credentials and a missing or wrong key, **When** the API starts, **Then** it fails closed and never generates a replacement.
3. **Given** `--panel-key-provisioned`, **When** the key file is missing, **Then** startup fails and no key is generated.
4. **Given** an offline rotation, **When** it completes, **Then** every credential is re-encrypted under the new key in one transaction, the old key file is untouched, and a process still holding the old key cannot write.

---

### User Story 5 - Install a panel-only Helm release (Priority: P3)

An operator installs a central panel on a Kubernetes cluster that will not host games, using a documented panel-only Helm profile. The default combined install and the remote operator and gateway profiles behave as before.

**Why this priority**: An alternative to Compose for operators who already run Kubernetes.

**Independent Test**: Render the chart for combined, remote-only and standalone values and check which resources each profile contains.

**Acceptance Scenarios**:

1. **Given** `api.standalone=true` and `operator.enabled=false`, **When** the chart renders, **Then** it contains no operator, agent or local-credential resources and mounts the panel key from separate storage.
2. **Given** an invalid standalone combination (operator on, gateway on, more than one API replica, conflicting key sources), **When** the chart renders, **Then** rendering fails with a clear message.
3. **Given** default values, **When** the chart renders, **Then** the combined install is unchanged apart from the documented RBAC and service-account changes.

---

### Edge Cases

- Removing the cluster the user has selected clears the selection; no other cluster is picked automatically.
- A health check against an unreachable or misbehaving cluster records a fixed status message, never the transport error text.
- A compromised remote API that sends huge responses, huge watch frames or huge object lists cannot grow the panel's memory without bound.
- A remote API or gateway that answers with a redirect does not receive or forward credentials elsewhere.
- Concurrent key publication by two processes leaves exactly one key.
- A rotation interrupted after commit can be retried safely.
- Existing Kubernetes-managed registrations are not imported into standalone storage; switching an existing deployment needs a deliberate transfer and backups.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The API MUST offer a standalone mode (`--standalone`, `GAMEPLANE_STANDALONE=true`) that stores management records in the panel database and never discovers Kubernetes credentials.
- **FR-002**: In standalone mode the API MUST NOT register a synthetic `local` cluster; cluster and fleet lists MUST succeed when empty.
- **FR-003**: Cluster registrations and labelled management credentials MUST go through one narrow storage interface; the combined install MUST keep delegating it to Kubernetes.
- **FR-004**: Standalone credentials MUST be authenticated-encrypted with a key stored outside the database, selected by `--panel-key-file` (`GAMEPLANE_PANEL_KEY_FILE`, legacy default `/data/panel.key`; deployment profiles use `/keys/panel.key`).
- **FR-005**: `--panel-key-provisioned` MUST require an existing key and MUST NOT generate one.
- **FR-006**: The key file's ownership, permissions, type and size MUST be checked before use, and a generated key MUST be published crash-durably.
- **FR-007**: A missing, replaced or wrong key MUST fail closed when credentials exist.
- **FR-008**: Master-key rotation MUST be an offline, single-transaction re-encryption with versioned authenticated ciphertext and a singleton key-state lock shared by credential writers; old and new key files stay separate, and a stale process MUST NOT write under a retired key.
- **FR-009**: Initial standalone registration MUST commit the registration and its credential atomically.
- **FR-010**: Kubeconfig rotation MUST replace the credential reference with UID and concurrency checks and preserve gateway settings and grants.
- **FR-011**: Registration and gateway credentials MUST NOT appear in response bodies, errors or logs.
- **FR-012**: Remote clients MUST require verified HTTPS, ignore forward proxies, refuse redirects and validate the destination at dial time; optional operator CIDRs narrow access, and loopback, metadata and other unsafe address classes stay blocked.
- **FR-013**: Remote responses and watch frames MUST be bounded after decompression and before decoding, and notification snapshots MUST be bounded.
- **FR-014**: The API MUST maintain remote cluster health without a central operator.
- **FR-015**: Agent operations MUST use each remote cluster's existing private gateway protocol.
- **FR-016**: In standalone mode, module requests MUST target one explicitly selected registered cluster; workload, inventory and agent requests MUST target only selected registered clusters; RBAC MUST be preserved. Authentication, users, roles and installation settings stay central.
- **FR-017**: An authenticated endpoint MUST tell the dashboard which mode it runs in, without leaking infrastructure details before login.
- **FR-018**: The dashboard MUST derive cluster choices from the API, support zero clusters, and never fabricate a local entry or send background inventory requests to the panel host.
- **FR-019**: Helm MUST expose operator enablement and standalone API configuration independently, with a documented panel-only profile that suppresses workload resources and local credentials, and MUST keep combined and remote operator/gateway profiles working.
- **FR-020**: Docker Compose MUST contain only the API and web services, with persistent database and key storage.
- **FR-021**: The panel API MUST run as a single process; Helm MUST use the `Recreate` strategy and one replica in standalone mode (Clarification 2026-10-14).

### Key Entities

- **Management object**: a stored cluster registration or labelled credential, identified by kind, namespace and name, with a UID and version for optimistic concurrency.
- **Panel key**: the 32-byte master encryption key, kept outside the database.
- **Key state**: the singleton record of the active key identity and the previous one.
- **Cluster registration**: a remote cluster's name, display name, credential references, agent-gateway settings and health status.
- **Installation capabilities**: the authenticated answer to "is this panel standalone, does it have a local cluster, which version is it".

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A standalone panel starts, bootstraps an admin, logs in, and returns empty cluster and fleet lists on a host with no Kubernetes (CI `standalone-compose` job).
- **SC-002**: After a restart, the admin, settings and every registration remain, and stored credentials are ciphertext in the database.
- **SC-003**: With a wrong or missing key and stored credentials, startup fails every time.
- **SC-004**: After rotation, 100% of stored credentials decrypt with the new key and none with the old one.
- **SC-005**: The chart renders the combined, remote-only and standalone profiles, and every invalid standalone combination fails to render (CI chart-template job).
- **SC-006**: No response body, error or log line produced by registration or gateway handlers contains credential material (handler tests).

## Assumptions

- Remote clusters run their own Gameplane operator and agents, installed with the remote operator and gateway profiles.
- Standalone mode is for fresh central panels; migration of an existing combined install is out of scope.
- Read-only mounting of a provisioned key is the deployment's job (Compose secret, Helm Secret volume); the API checks the file, not the mount.

## Known gaps

Found while documenting the merged code (2026-10-14). They are reported, not planned; no task exists for them here.

- **No `test/e2e/` coverage for standalone mode** (Constitution I). Standalone is exercised only by the Compose smoke test (`deploy/standalone/smoke.py`, CI job `standalone-compose`) and render-only chart checks (`charts/tests/check_standalone.py`). No bucket in `test/e2e/buckets.sh` runs a standalone panel, and no CI job installs the Helm panel-only profile in a cluster.
- **Design came after the first UI code** (Constitution II). The first dashboard commit `0182e16` (2026-10-07) predates the design commit `6eb7ac3` (2026-10-08). The design export is complete in the merged tree.
- **`--skip-crds` is documented, not enforced.** A panel-only Helm install without `--skip-crds` still creates the Gameplane CRDs (`charts/gameplane/examples/standalone-panel-values.yaml`, `templates/NOTES.txt`). Follow-up issue owed by valgul.
- **Single API container in Compose is documented, not enforced.** Helm enforces `api.replicas=1` (`templates/validate.yaml`); `deploy/standalone/compose.yaml` does not.
- **Gateway redirects are not followed but not rejected.** The agent-gateway client returns a 3xx to the caller (`api/internal/ws/gateway_client.go`, `CheckRedirect` returns `http.ErrUseLastResponse`) instead of failing like the Kubernetes clients; no test covers it. Credentials are not sent to the redirect target.
- **Combined installs changed in three places** that FR-019 does not mention: module and template permissions now need a cluster-wide grant on the target in every install (`api/internal/auth/perms.go`); the agent-gateway destination guard now also applies to combined installs (`api/internal/ws/gateway_client.go`); `gameplane-api-read` gains `update` on clusters and web pods stop mounting a service-account token (`charts/gameplane/templates/api.yaml`, `web.yaml`).
- **Untested paths**: the 501 from Kubernetes system logs in standalone (`api/internal/handlers/systemlogs.go`), the standalone telemetry cluster count (`api/internal/telemetry/collect.go`), and a 401 from `/admin/installation` for anonymous callers (the handler test mounts it without the session group).
- **`deploy/standalone/compose.provisioned-key.yaml` is not exercised by CI.** `smoke.py` tests provisioned-key mode with its own generated override.
- **Telemetry marker for standalone installs**: follow-up (Clarification 2026-10-14).

## Original design note (PR #630)

The contributor's note. The text is unchanged; its title line was dropped and its headings were demoted one level to sit under this heading.

### Intent

Run the panel and API independently of the operator and agents, including with Docker Compose on a host that is not a Kubernetes cluster. Start with no registered clusters and add remote clusters explicitly. Preserve the default combined Kubernetes installation.

### Design

`--standalone` (`GAMEPLANE_STANDALONE=true`) selects database-backed management storage and skips Kubernetes credential discovery. The API does not register a synthetic `local` cluster. Cluster registration metadata and labelled management credentials use a narrow storage interface; the existing installation delegates that interface to Kubernetes. Standalone secrets are authenticated-encrypted with a persistent key outside the database, selected by `--panel-key-file` (`GAMEPLANE_PANEL_KEY_FILE`, legacy default `/data/panel.key`; deployment profiles use separate storage at `/keys/panel.key`). Back up the database and key separately under different access policies. `--panel-key-provisioned` requires a protected existing key mounted read-only and never generates a missing key. File ownership, permissions, type and size are checked before use; generated key publication is crash durable. Losing or replacing a key must fail closed.

Remote workloads remain Kubernetes resources reconciled by the remote operator. The API maintains remote connectivity status without requiring a central operator. Agent operations use each remote cluster's existing private gateway protocol. Registration and gateway credentials remain central and must not appear in response bodies, errors or logs. Modules are routed to an explicit remote cluster in standalone mode; management authentication, users, roles and installation settings remain central.

Master-key rotation is an offline transaction with versioned authenticated ciphertext and a singleton key-state lock shared by credential writers. Old and new key files remain separate; a stale process cannot write under a retired key. Kubeconfig rotation changes the existing registration's credential reference with UID/concurrency checks and preserves gateway settings and grants. Standalone initial registration commits metadata and its credential atomically.

Remote clients require verified HTTPS, no forward proxy or redirects, and dial-time destination validation. Optional operator CIDRs narrow safe public/private workload access; loopback, metadata and other unsafe address classes remain blocked. Bound decompressed responses and watch frames before decoding, and use bounded notification snapshots so a compromised endpoint cannot grow a central informer cache indefinitely.

The dashboard derives cluster choices from the API and supports zero registered clusters. It must neither fabricate a local entry nor send background inventory requests to the panel host. Authenticated installation capabilities distinguish the modes without leaking infrastructure information before login.

Helm exposes independent operator enablement and standalone API configuration. A documented panel-only profile suppresses workload resources and local credentials. Docker Compose contains only API and web services with persistent database and key storage. Existing combined and remote operator/gateway profiles retain their behavior.

### Acceptance and validation

- Start without kubeconfig, Kubernetes service credentials, operator, or agent.
- Login, manage panel settings, register/remove remote clusters and survive restart.
- Empty cluster and fleet lists are successful and contain no implicit local cluster.
- Workload, module, inventory and agent requests target only explicitly selected registered clusters and preserve RBAC.
- Secret writes preserve label isolation, optimistic concurrency and encryption; malformed input fails safely.
- Chart matrix covers combined, remote-only and standalone profiles; Compose documentation includes bootstrap and persistence.
- Repository policy requires tests/lint in CI; compile locally, add regression coverage and report CI status accurately.

Existing Kubernetes-managed registrations are not silently imported into standalone storage. Switching an existing deployment requires deliberate configuration transfer and backups.
