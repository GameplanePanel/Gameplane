# Standalone panel and API

## Intent

Run the panel and API independently of the operator and agents, including with Docker Compose on a host that is not a Kubernetes cluster. Start with no registered clusters and add remote clusters explicitly. Preserve the default combined Kubernetes installation.

## Design

`--standalone` (`GAMEPLANE_STANDALONE=true`) selects database-backed management storage and skips Kubernetes credential discovery. The API does not register a synthetic `local` cluster. Cluster registration metadata and labelled management credentials use a narrow storage interface; the existing installation delegates that interface to Kubernetes. Standalone secrets are authenticated-encrypted with a persistent key outside the database, selected by `--panel-key-file` (`GAMEPLANE_PANEL_KEY_FILE`, legacy default `/data/panel.key`; deployment profiles use separate storage at `/keys/panel.key`). Back up the database and key separately under different access policies. `--panel-key-provisioned` requires a protected existing key mounted read-only and never generates a missing key. File ownership, permissions, type and size are checked before use; generated key publication is crash durable. Losing or replacing a key must fail closed.

Remote workloads remain Kubernetes resources reconciled by the remote operator. The API maintains remote connectivity status without requiring a central operator. Agent operations use each remote cluster's existing private gateway protocol. Registration and gateway credentials remain central and must not appear in response bodies, errors or logs. Modules are routed to an explicit remote cluster in standalone mode; management authentication, users, roles and installation settings remain central.

Master-key rotation is an offline transaction with versioned authenticated ciphertext and a singleton key-state lock shared by credential writers. Old and new key files remain separate; a stale process cannot write under a retired key. Kubeconfig rotation changes the existing registration's credential reference with UID/concurrency checks and preserves gateway settings and grants. Standalone initial registration commits metadata and its credential atomically.

Remote clients require verified HTTPS, no forward proxy or redirects, and dial-time destination validation. Optional operator CIDRs narrow safe public/private workload access; loopback, metadata and other unsafe address classes remain blocked. Bound decompressed responses and watch frames before decoding, and use bounded notification snapshots so a compromised endpoint cannot grow a central informer cache indefinitely. See [security follow-up plan](security-plan.md) for implementation and validation coverage.

The dashboard derives cluster choices from the API and supports zero registered clusters. It must neither fabricate a local entry nor send background inventory requests to the panel host. Authenticated installation capabilities distinguish the modes without leaking infrastructure information before login.

Helm exposes independent operator enablement and standalone API configuration. A documented panel-only profile suppresses workload resources and local credentials. Docker Compose contains only API and web services with persistent database and key storage. Existing combined and remote operator/gateway profiles retain their behavior.

## Acceptance and validation

- Start without kubeconfig, Kubernetes service credentials, operator, or agent.
- Login, manage panel settings, register/remove remote clusters and survive restart.
- Empty cluster and fleet lists are successful and contain no implicit local cluster.
- Workload, module, inventory and agent requests target only explicitly selected registered clusters and preserve RBAC.
- Secret writes preserve label isolation, optimistic concurrency and encryption; malformed input fails safely.
- Chart matrix covers combined, remote-only and standalone profiles; Compose documentation includes bootstrap and persistence.
- Repository policy requires tests/lint in CI; compile locally, add regression coverage and report CI status accurately.

Existing Kubernetes-managed registrations are not silently imported into standalone storage. Switching an existing deployment requires deliberate configuration transfer and backups.
