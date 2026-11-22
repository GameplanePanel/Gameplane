# Implementation Plan: SFTP access to game servers

**Branch**: `spec-039/sftp-access` | **Date**: 2026-11-22 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/039-sftp-access/spec.md`

**Open decisions**: OD-1 to OD-6 are settled (OD-6 B: one streaming upload path and limit for SFTP and the dashboard). OD-7 A: the SFTP server lives in a new split repository `GameplanePanel/sftp`, submodule `sftp/`. No decision is open. See [OPEN-DECISIONS.md](OPEN-DECISIONS.md).

## Summary

Each cluster can run an optional SFTP endpoint (OD-3). It accepts SSH keys or a generated SFTP password (OD-2), and sends every login and file operation to the central Gameplane API (OD-1, OD-5), which applies the Files tab's permissions, writes the audit log and reaches the server's agent through its existing local proxy or remote gateway. A login as `<user>` shows a tree of the cluster's servers; `<user>+<server>` opens one server. The agent gains stat, rename and a streaming upload with resume, which the dashboard Files tab also switches to, under one Helm upload limit (OD-6). For a stopped or asleep server the operator runs a short-lived file-only pod with the server's volumes and points the `<name>-agent` Service at it, so the API and gateway reach it unchanged (OD-4); the Files tab uses it too. Details: [research.md](research.md), [data-model.md](data-model.md), [contracts/api.md](contracts/api.md).

## Technical Context

**Language/Version**: Go (api, agent, operator, sftp component: versions from `.devcontainer/versions.toml`), TypeScript strict (web), Helm 3 templates.

**Primary Dependencies**: existing chi, client-go, controller-runtime, `golang.org/x/crypto` (argon2, ssh). New in the SFTP component only: `github.com/pkg/sftp`. React 19 + TanStack Query + HeroUI v3 for the profile section.

**Storage**: SQLite / PostgreSQL through the API store; one portable migration `common/024_sftp_credentials.sql` (four tables, one audit column). Host key in a Kubernetes Secret per cluster. No CRD change: one GameServer annotation and one operator flag.

**Testing**: agent unit tests (rooted stat/rename/stream, race and escape cases), operator envtest (file-only pod lifecycle, selector switch, start ordering), API envtest (credentials, login, guard, permission matrix, audit source), SFTP component unit tests against an `httptest` API, web Vitest, chart `helm template` checks, E2E: new bucket `sftp` (protocol, permissions, stopped server, revoke) and an addition to `multicluster` (remote endpoint). CI only (rule 8).

**Target Platform**: Kubernetes (k3s to multi-node), standalone panel with remote clusters.

**Project Type**: multi-repo: Gameplane (agent, operator, api, web, chart, E2E) plus a new split component (OD-7).

**Performance Goals**: SC-002 (root of 200 servers and folders of 1,000 files under 2 s): root listing is one `GET /servers` call; folder listing one agent `list`. Streaming upload throughput bounded by the network, not buffering (no whole-file buffering anywhere).

**Constraints**: agent port stays reachable only from API, operator and gateway; no shell/exec/forwarding (FR-004); generic login failures (rule 3); no suppressions (rule 4).

**Scale/Scope**: per-user caps 4 sessions and 8 concurrent transfers (Helm `sftp.maxSessionsPerUser`, `sftp.maxTransfersPerUser`); 20 SSH keys per user.

## Constitution Check

| Principle | Status |
|---|---|
| I. E2E-tested delivery | PASS (planned): new `sftp` bucket registered in `test/e2e/buckets.sh`, `t.Parallel()`, unique names, at most 3 admin logins; remote endpoint in `multicluster`. |
| II. Design-first | PASS (planned): profile SFTP section, admin revoke on Users, cluster SFTP token dialog and the Files tab "files are starting" state are designed in `design.fig` and exported before React code. |
| III. Language best practice | PASS: `%w`, strict TS, portable migration, no suppressions. |
| IV. Spec-driven | PASS: this folder; `agent/specs.md`, `operator` docs, `api` docs and the new component's `specs.md` updated in their PRs. |
| V. Delegate | PASS (planned): Workflow scripts with explicit models starting at haiku, briefed from this folder. |
| VI. CI bears the load | PASS: compile checks only locally. |
| Kubernetes primitives first | PASS: Pod, Service selector, Secret, NetworkPolicy; no new CRD. |
| Operator authority | PASS: the API only writes an annotation; the reconciler creates, removes and orders the file-only pod. |

## Project Structure

### Documentation (this feature)

```
specs/039-sftp-access/
├── spec.md
├── OPEN-DECISIONS.md
├── plan.md
├── research.md
├── data-model.md
├── contracts/api.md
├── tasks.md
└── checklists/requirements.md
```

### Source Code

```
agent/internal/files/        stat.go, rename_linux.go, stream_linux.go (+ tests); files.go mounts them
agent/specs.md               new routes
operator/internal/controller/gameserver_files.go         file-only pod, selector switch, idle teardown (+ envtest)
operator/internal/controller/gameserver_controller.go    start ordering hook in desiredReplicas; Service selector by mode
operator/cmd/main.go         --files-idle-timeout
api/internal/db/migrations/common/024_sftp_credentials.sql
api/internal/auth/sftp.go    keys, passwords, service tokens, sessions, guard (+ tests)
api/internal/handlers/sftp.go  profile, admin, cluster token routes (+ envtest)
api/internal/handlers/sftp_login.go  /sftp/v1/login, /sftp/v1/sessions/check
api/internal/ws/dialer.go    mount files/stat, files/rename, files/stream; ensure file-only access on stopped servers
api/internal/gatewayprotocol/  stat, rename, stream as allowed operations; stream long-running
api/internal/audit/audit.go  source column
web/src/routes/ProfileSftp.tsx (or section in the profile route), Users.tsx, Clusters.tsx, tabs/Files.tsx (streaming upload with progress, files-starting state)
charts/gameplane/templates/sftp.yaml, networkpolicies.yaml (gameplane-files ingress; SFTP ingress), values.yaml
sftp/ (OD-7 A: submodule GameplanePanel/sftp)  cmd/main.go, internal/{sshserver,apiclient,vfs}, Dockerfile, specs.md
Makefile                     image-sftp; IMAGES
test/e2e/sftp_test.go, test/e2e/buckets.sh, test/e2e/multicluster_sftp_test.go
docs/sftp.md, docs/security.md, docs/architecture.md; changelog/unreleased/<PR>-sftp-access.md
```

### Delivery (stacked PRs, each green in CI before the next merges)

1. **Spec PR** (docs only): this folder.
2. **Agent PR**: stat, rename, stream + `agent/specs.md`.
3. **Operator PR**: file-only pod, selector switch, start ordering, NetworkPolicy for `gameplane-files`.
4. **API PR**: migration, credentials, SFTP login/session, guard, new file routes, ensure-files, gateway protocol, audit source.
5. **SFTP component PR** in `GameplanePanel/sftp` (OD-7 A).
6. **Design PR** then **Web PR**: profile section, admin revoke, cluster token, Files tab starting state (after design sign-off).
7. **Chart + docs + E2E PR**: `sftp.yaml`, values, submodule add/pointer, Makefile image, docs, changelog, E2E buckets.

## Complexity Tracking

| Item | Why needed | Simpler alternative rejected because |
|---|---|---|
| Service token + session token pair | SFTP server must prove it is a Gameplane endpoint for one cluster, and act as a user without holding the user's secret afterwards | Forwarding the user's SFTP password on every call keeps a secret in memory and costs an argon2 check per operation |
| File-only pod and selector switch | OD-4: files of stopped servers | Scaling the StatefulSet with a files-only flag would roll the game pod spec and fight the stop/sleep logic |
| Streaming upload route next to multipart upload for one release | OD-6: uploads above 64 MiB with resume, for SFTP and the dashboard | Raising the multipart cap still buffers through `ParseMultipartForm` and has no resume; removing multipart at once breaks API clients without notice |
| New split repository | OD-7: matches the other optional components | In-tree module conflicts with the #995 split |
