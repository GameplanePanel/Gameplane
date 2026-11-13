# Quickstart: validating feature 030

**Feature**: [spec.md](spec.md) | **Contracts**: [contracts/](contracts/)

CI is the verification authority (constitution VI, CLAUDE.md rule 8). Locally, run only compile checks. The scenarios below are what the E2E tests automate (research R16); they are written so a reviewer can also walk them on a dev Kind cluster. Shapes, error codes and field names are in the contracts and are not repeated here.

## Local compile checks (the only local verification)

```sh
cd operator && go build ./... && cd ..
cd api && go build ./... && cd ..
cd gp-module && go build ./... && cd ..
cd mcp-server && go build ./... && cd ..
cd test/e2e && go build -tags e2e ./... && cd ..   # the E2E suite's helpers; test files compile in CI
cd web && npx tsc --noEmit && cd ..
```

## CI runs

| What | Where |
|---|---|
| Resolver table tests, StatefulSet gate, backup/restore group, status, root-override start refusal, restricted-mode effective values | `go test` and envtest for `operator` (gate 72) |
| Create, update, clone, permission, root rule, config section and its ConfigMap mirror, audit rows (with the risky marker) | `make test-integration` (envtest, `api` gate 80) |
| Template checks | `gp-module` unit tests (gate 80); the module repo's validator tests (`test-validate-py.sh`) |
| Dashboard section, wizard, settings, admin switch | vitest (`web` gates) |
| i18n key coverage | `i18n-check` in `make lint` |
| End to end | `make test-e2e-bucket BUCKET=operator` (`TestGameServer_SecurityOverrideAppliesOnRestart`, including the OD-4 refusal, and `TestAPI_SecurityOverride_PermissionAndRootRule`; both live in the `operator` bucket) |
| CRD sync | `make lint` drift checks on the four generated locations |

## Manual walk-through on a dev cluster

**Prerequisites**:

- `git submodule update --init modules` and `make dev-up`, then `make images && make dev-load && make dev-install` with this branch's operator and API.
- A game template with a security block. The E2E busybox fixture (`test/e2e/test_helpers_e2e_test.go:42`) is enough: add `security: {runAsUser: 1000, runAsGroup: 1000, fsGroup: 1000}` to it and apply it.
- Two sign-ins: the bootstrap admin, and a user bound to the built-in `operator` role (holds `servers:write`, not `servers:security`).
- `GET /admin/config` shows no `security` section (setting off).

### S1: no override means no change (US1; FR-001, SC-001)

1. Create a server from the template without `spec.security` and wait for Running.
2. `kubectl exec` into the game container and run `id`.
   - **Expect** uid 1000, gid 1000; the data volume's group is 1000.
3. `GET` the server.
   - **Expect** `status.security.desired` and `applied` both showing 1000 with `source: ModuleDefault`, the ownership check `OnlyWhenWrong` with `BuiltInDefault`, and `pending: false`.
4. Repeat with a template that sets no `security`.
   - **Expect** the pod has no `securityContext` on the game container and no `fsGroup`; every id has `source: ImageDefault`.

### S2: override at create time (US2; FR-002, FR-003)

1. As admin, create a server with `runAsUser { value: 2000 }` (see [crd-gameserver-security.md](contracts/crd-gameserver-security.md)); leave the rest unset.
   - **Expect** `id` shows uid 2000 and gid 1000; `desired.runAsUser` is `Override`, the others `ModuleDefault`.
2. Create a server with `runAsGroup { useImageDefault: true }` while the setting is off.
   - **Expect** 403 `server.security.root_disabled`; no object exists.
3. Create with `runAsUser { value: 4294967296 }`.
   - **Expect** 422 `server.security.invalid`.

### S3: change applies on the next restart, never by itself (US3; FR-008, SC-004)

1. On the running server from S2, save `runAsUser { value: 3000 }` (as admin).
   - **Expect** the pod UID and restart count do not change; `id` still shows 2000; `status.security.applied.runAsUser` is 2000, `desired` is 3000, `pending: true`.
2. Use the Restart action.
   - **Expect** a new pod; `id` shows 3000; `applied` equals `desired`; `pending: false`.
3. Remove the override and restart.
   - **Expect** uid 1000 again, `source: ModuleDefault`.
4. Edit the template's default (as admin) and let the template rollout run.
   - **Expect** a server with an override keeps its overridden value; others follow the new default (US3 scenario 3).

### S4: permission (US4; FR-010, SC-003)

1. As the `operator`-role user, create a server with any `spec.security`.
   - **Expect** 403 `server.security.forbidden`; nothing created.
2. As that user, `PUT` an existing server with the same `spec.security` it already has plus an unrelated change.
   - **Expect** 200 (unchanged block needs no permission).
3. As that user, `PUT` without the `security` member on a server that has one.
   - **Expect** 200 and the stored override is still there.
4. As that user, clone a server that has an override.
   - **Expect** 403 `server.security.forbidden`; as admin, 201.
5. Add `servers:security` to a custom role for the namespace and repeat step 1.
   - **Expect** 201.
6. Open the server's Advanced section as the `operator`-role user.
   - **Expect** the values, sources and pending marker are visible and read-only.

### S5: the root setting (FR-005, FR-005a; OD-2, OD-4)

1. As admin, `PUT /admin/config/security {"allowRootOverrides": true}` ([cluster-setting.md](contracts/cluster-setting.md)).
2. Create a server with `fsGroup { value: 0 }`, then one with `runAsUser { useImageDefault: true }`.
   - **Expect** both accepted.
3. Turn the setting off (`PUT /admin/config/security {"allowRootOverrides": false}`) and save an unrelated change to the first server.
   - **Expect** 200 and the override remains; a new server with a root value is refused again. `kubectl get configmap gameplane-security-settings -n <control-plane namespace> -o yaml` shows `allowRootOverrides: "false"`.

### S5a: turning the setting off with root-override servers (FR-005a; OD-4)

Prerequisite: S5 step 2 left two servers holding root overrides (`fsGroup { value: 0 }` and `runAsUser { useImageDefault: true }`) and the setting is on. Stop the first one (Suspend), keep the second running, then turn the setting off.

1. Note the second server's pod UID and restart count, then `PUT /admin/config/security {"allowRootOverrides": false}`.
   - **Expect** the running server's pod UID and restart count do not change; no server restarts or stops. Its `SecurityAllowed` condition is `False` with reason `RootOverrideDisallowed` and a message that it will not start again until the override is cleared or the setting is on.
2. Start the stopped first server (Resume or Start).
   - **Expect** it stays at zero replicas; its status shows `SecurityAllowed=False`, reason `RootOverrideDisallowed`, naming `fsGroup`; the dashboard shows the reason and what clears it.
3. As admin, remove the override from the stopped server.
   - **Expect** the server starts on the next reconcile, `SecurityAllowed` is `True`.
4. Suspend the second server, then turn the setting back on, and start it.
   - **Expect** it starts with its override; `SecurityAllowed` is `True`. Repeat step 2 with the setting on first: **expect** a stopped server holding a root override starts as soon as the setting is flipped on (no other action).
5. Delete the ConfigMap `gameplane-security-settings` while a stopped server holds a root override.
   - **Expect** the setting reads as off (fail closed): the server does not start; restarting the API recreates the ConfigMap from the stored setting.

### S5b: restricted workload mode (OD-5)

Prerequisite: the operator runs with `--restricted-workloads` (chart value) and the setting is on.

1. Create a server with `runAsUser { useImageDefault: true }`, then start it.
   - **Expect** the server is reported failed with the existing restricted-mode message about the missing positive run-as user or filesystem group; no pod is created.
2. Replace the override with `runAsUser { value: 2000 }`.
   - **Expect** the server leaves the failed state and starts.
3. With a server already running, save `runAsUser { useImageDefault: true }`.
   - **Expect** the running pod is untouched (same pod UID, `pending: true`); the next start would fail with the same message.

### S6: ownership check and the upgrade (FR-001, FR-002a, SC-006; research R8)

1. Before installing this branch, run a server whose module sets `fsGroup` and record the pod UID.
2. Upgrade to this branch.
   - **Expect** the pod is not restarted; `status.security.pending` is true with `applied.volumeOwnershipCheck` `EveryStart` and `desired` `OnlyWhenWrong`.
3. Restart the server.
   - **Expect** the pod spec carries `fsGroupChangePolicy: OnRootMismatch`; `pending: false`.
4. Set `volumeOwnershipCheck: EveryStart` on the server and restart.
   - **Expect** `fsGroupChangePolicy: Always`.
5. With a world of 100,000 or more files (SC-006), compare the time from restart to Ready with the earlier release.
   - **Expect** at least as fast.

### S7: backup and restore use the applied group (FR-011)

1. With a pending `fsGroup` change on a stopped server (change saved, not yet started), create a Backup.
   - **Expect** the backup job's pod `fsGroup` equals `applied.fsGroup`, not `desired`.

### S8: risky values (FR-006a; OD-6 settled C)

1. Install a module that marks `runAsUser` risky with a reason (Palworld after the module PR).
2. In the dashboard, set a run-as user override.
   - **Expect** a confirmation showing the reason; cancelling stores nothing; confirming stores the override.
3. Send the same override directly to the API as a `servers:security` holder, with no confirmation parameter.
   - **Expect** 200: the API refuses nothing on the risky flag. The audit row for the change lists `"risky":["runAsUser"]` in its reason (and no reason text).

### S9: audit (FR-010, SC-005)

1. After S2 to S5 and S8, open the audit page.
   - **Expect** one row per change with actor, time, path, target and the before and after in the reason, one row per refusal, and one row for each flip of the cluster setting ([audit-events.md](contracts/audit-events.md)); `GET /admin/audit/verify` reports an intact chain.

### S10: read-only MCP (FR scope; research R15)

1. Point the MCP server at the cluster and `get` a GameServer.
   - **Expect** `spec.security` and `status.security` are present; the server exposes no write tool.

### S11: module authoring (FR-013)

1. Run `gp-module validate` on a template with a `risky` entry whose reason is 201 characters.
   - **Expect** an error; with 200 characters, no error. `gp-module preview` lists the risky settings.
