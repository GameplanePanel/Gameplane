# Research: Standalone Panel and API

Retroactive. Each entry records a choice PR #630 made, the reason given in its code, docs or review, and the alternative it passed over. Except for R8, which valgul decided on PR #630, the alternatives are inferred while documenting; PR #630 did not record them. There are no open `NEEDS CLARIFICATION` items.

## R1. Where management records live in standalone mode

- **Decision**: Two tables in the panel database (`management_objects`, `management_key_state`), behind the same interface the combined install backs with Kubernetes Secrets and Cluster CRs.
- **Rationale**: The panel already owns a database that survives restarts; one interface keeps handlers identical in both modes.
- **Alternatives considered**: A local embedded Kubernetes API (k3s, kine) on the panel host, which reintroduces the cluster the feature removes; a separate key-value store, which is one more service to back up.

## R2. Credential encryption and key custody

- **Decision**: AES-256-GCM with a 32-byte key in a file outside the database, authenticated over the record identity, with a versioned `gpk1.<keyID>.` header. Deployment profiles put the key on a separate volume (`/keys`), or provision it read-only.
- **Rationale**: A stolen database backup alone reveals no credentials; binding the record identity stops ciphertext from being swapped between records.
- **Alternatives considered**: Storing the key in the database (defeats separation); deriving it from a passphrase at startup (needs an interactive or secret input on every restart).

## R3. Key file validation and publication

- **Decision**: Check type, size, link count, owner, mode and extended ACLs on the opened inode; publish a generated key with fsync, hard link and directory fsync under a lock file; fail closed on any doubt.
- **Rationale**: A key that another user can read or replace silently breaks the separation in R2; a key lost in a crash makes every credential unreadable.
- **Alternatives considered**: Trusting the mount, which offers no guarantee on Compose bind mounts or reused volumes.

## R4. Master-key rotation

- **Decision**: Offline command `api rotate-panel-key` that re-encrypts every record in one transaction and updates the singleton key-state row; every credential writer locks that row and checks the active key ID.
- **Rationale**: One transaction means no mixed-key state; the row lock stops a stale process from writing under a retired key.
- **Alternatives considered**: Online rotation with dual-key reads, which needs coordination across processes the single-process design (R8) does not have.

## R5. Reaching remote clusters safely

- **Decision**: Validate kubeconfigs (HTTPS, verified TLS, no proxy, no userinfo or query), refuse redirects, resolve once and dial only validated IPs, block loopback, metadata and non-global classes, optionally narrow by operator CIDRs, and bound every response and watch frame.
- **Rationale**: A registration administrator can point the panel anywhere; the panel must not become an SSRF pivot or be exhausted by a hostile endpoint.
- **Alternatives considered**: Reusing `netguard.IsAllowed` alone, which does not cover redirects, proxies or response size.

## R6. Health and notifications without a central operator

- **Decision**: A 30 s health poller writes a fixed status projection; a 5 s per-cluster notification poller takes bounded snapshots.
- **Rationale**: Informers would cache unbounded remote state centrally; polling with fixed budgets keeps memory bounded.
- **Alternatives considered**: Running an operator next to the panel, which the feature exists to avoid.

## R7. Dashboard mode detection

- **Decision**: An authenticated `GET /admin/installation` returning `standalone`, `localCluster` and `version`.
- **Rationale**: Core rule 3 forbids exposing deployment details before login.
- **Alternatives considered**: A public capabilities endpoint (leaks mode), or inferring mode from the cluster list (ambiguous when it is empty).

## R8. Process model

- **Decision**: One API process, `Recreate` strategy, one replica (valgul, 2026-10-14).
- **Rationale**: The audit hash chain and user-management safeguards use process-local locks.
- **Alternatives considered**: Database-coordinated locks with a shared key for rolling updates and replicas; deferred to a possible future HA feature.

## R9. Deployment profiles

- **Decision**: Helm `operator.enabled` and `api.standalone` with render-time guards and a panel-only example; Docker Compose with only `gameplane-api` and `web`.
- **Rationale**: Compose covers hosts without Kubernetes; the Helm profile covers operators who already run Kubernetes but want a central panel.
- **Alternatives considered**: Skipping CRDs from values, which Helm cannot do (the `crds/` directory installs before templates), so `--skip-crds` is documented instead (follow-up issue owed).
