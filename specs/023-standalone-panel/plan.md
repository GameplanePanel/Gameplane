# Standalone Panel Implementation Plan

**Goal:** Deliver an upstream issue and pull request implementing independent panel/API installation.

**Architecture:** Keep management storage separate from workload clients. Use database storage for standalone management records, Kubernetes for existing combined installs, and explicit registry clients for remote workloads.

**Tech stack:** Go, SQLite/PostgreSQL, React/TypeScript, Helm, Docker Compose.

**Spec:** [spec.md](spec.md)

## Constraints

Preserve default deployments, RBAC and credential labels. Never expose credentials. No central operator or implicit local cluster in standalone mode. Tests and lint execute in CI; compile checks may execute locally. Use signed human-attributed commits.

## Tasks

- [x] Storage: add narrow Secrets/Clusters methods to kube.Client and separate Registry.Management from Registry.Default. Implement encrypted SQL persistence and remote registration reload/health checks; add persistence, conflict and isolation tests.
- [x] API: skip Kubernetes setup in standalone mode, integrate management storage into credential consumers and cluster/fleet/gateway discovery, dispatch standalone modules remotely, handle unavailable local-only features, add zero-cluster and authorization regressions.
- [x] Dashboard: derive available clusters from discovery, preserve empty-install registration flow, route standalone module operations to explicit remotes, add regression tests.
- [x] Deployment: operator.enabled, standalone API profile, no local workload resources/credentials, Docker Compose and installation docs, chart matrix checks.
- [x] Review: inspect full diff for nil management clients, credential leakage, stale registration races, implicit local fallback and namespace/cluster authorization confusion. Compile and submit signed branch, then inspect CI and address failures.

## Review focus

Restart with encrypted credentials; lost encryption key; removal during client reload; zero or unreachable remotes; remote-only permission grants; unchanged combined installation. Tests should assert these behaviors at their owning layer.
