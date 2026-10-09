# Standalone security follow-up implementation plan

**Goal:** Address the security review of PR #630 in separate signed commits, including key custody and rotation, without requiring a local Kubernetes cluster.

**Architecture:** Preserve SQL-backed management and explicitly selected remote workloads. Bound untrusted remote input before allocation. Keep encryption keys outside database volumes and support read-only operator-provisioned keys. Rotate master keys transactionally through an offline administrative command; rotate kubeconfigs in place through the existing cluster administration boundary.

**Spec:** [spec.md](spec.md), extended by the user's October 8 authorization of all review fixes.

## Constraints

- Preserve existing encrypted credentials and combined installations; never silently replace a missing key.
- Never expose key or credential values in logs, responses, process arguments, or Git.
- One signed, human-attributed commit per requested logical change; no amended pushed commits.
- Add behavioral regression tests first. Repository policy requires test/lint suites in CI; use compile checks locally.
- Existing isolated worktree: `D:/Codex/Gameplane/standalone-panel`, branch `feat/standalone-panel`.
- Provisioned keys are read-only inputs. Generated keys remain an available simple-deployment option. No external KMS service is required.

## Tasks

- [x] Bound automatic remote observation: limit decompressed finite responses and watch frames before buffering; bound retained remote objects; preserve cancellation and streaming. Tests cover oversize/chunked/compressed responses, watch growth, and normal observation.
- [x] Publish generated keys durably: sync key contents and containing directory before encrypted records can be committed. Test error propagation and non-overwrite behavior.
- [x] Validate existing keys: read through a file handle, enforce regular-file/size/access rules, preserve safe read-only Kubernetes secret mounts, and reject unsafe files. Add platform-specific validation with fail-closed behavior.
- [x] Support separated and provisioned key storage: retain legacy API file compatibility, add an explicit provisioned-file mode that never generates a missing key, separate generated key storage in Compose/Helm, document migration and separate protected backups, and update deployment checks.
- [x] Rotate master keys: add an offline command consuming old/new key paths, authenticated ciphertext version/key identity, atomic database re-encryption with rollback, stale-writer protection, and documented restart/recovery steps. Tests cover wrong keys, interruption, legacy data, empty stores, and SQLite/PostgreSQL.
- [x] Rotate kubeconfigs in place: authenticated `PUT /clusters/{name}/kubeconfig`, validate input, preserve registration UID/gateway/grants, guard concurrent rotation/removal/recreation, refresh the live client, never return credential values. Tests cover permissions, persistence, and races.
- [x] Make standalone registration atomic/recoverable: credentials and registration commit together; safely recover matching API-owned legacy orphans without modifying unrelated secrets. Tests cover cancellation, conflict, rollback, and retry.
- [x] Harden standalone remote connections: require verified HTTPS, block unsafe address classes at dial time, permit private workload networks, support explicit operator destination restrictions, and apply the same policy to registration, reload, rotation, and gateways. Tests cover DNS rebinding, insecure TLS/HTTP, and allowed private destinations.
- [ ] Review all changes, compile API/default and PostgreSQL builds plus dashboard, verify every commit signature, push the updated PR branch, and inspect CI. Resolve failures within the authorized scope.

## Review focus

- A compromised workload endpoint must not allocate unbounded central-process memory.
- A crash at any key/credential publication boundary must retain a usable old or new state.
- A stale running process must not write ciphertext under a retired key.
- File validation must work with read-only projected secrets and fail closed for unsafe permissions.
- Credential rotation/removal must not carry changes across registration UID replacement or remove external credentials.

## Execution notes

Independent network, key-lifecycle, and registration work may run in parallel with exclusive file ownership; shared entrypoint/deployment integration and all commits are serialized by the coordinating agent. The user's explicit request authorizes code, regression-test, design, and PR-branch updates. Existing CI-only verification and human signing requirements take precedence over generic skill test/approval defaults.

Independent review also covered Kubernetes fsGroup remount behavior, configurable
Helm rotation filenames, extended file access ACLs, unsolicited protocol upgrades,
and client-go's separate SPDY dial/header/error-stream paths. Follow-up commits
close these gaps without relaxing the permission or destination checks.

Initial CI at `999fdab0` passed actual Compose key/kubeconfig rotation and read-only
provisioned restart, chart renders and Helm lint. API tests exposed a prior
loopback health fixture, corrected to verified TLS on a local private interface;
API lint exposed missing test contexts/body cleanup and an unconfined directory
open, corrected in follow-up commits. Final-head validation remains pending.
