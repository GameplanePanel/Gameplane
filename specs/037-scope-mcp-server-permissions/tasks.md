# Tasks: Scope MCP server permissions

**Input**: Design documents from `/specs/037-scope-mcp-server-permissions/`

**Prerequisites**: plan.md, spec.md, research.md, data-model.md, contracts/, quickstart.md. All open decisions (OD-1 to OD-5) settled by valgul on 2026-11-17 and 2026-11-22.

**Tests**: Required. Constitution Principle I needs E2E coverage, and the coverage gates (api 80, web 92/76/82/92, mcp repo `.testcoverage.yml`) need unit, envtest and Vitest tests. Every test task below adds **new** test functions or files. Changing an **existing** test needs valgul's written sign-off from T002 first (CLAUDE.md override 1). Tests run in CI only (Principle VI); the only local checks are `go build ./...` (`GOWORK=off` inside `mcp-server/`) and `npx tsc --noEmit`.

**Organization**: by user story, in spec priority order: US1 and US2 (P1), US3 (P2), US4 (P3).

**Delivery** (plan.md, Structure Decision): stacked PRs from this one thread. PR A API (`feat/037-mcp-token-api`), design branch `design/037-mcp-tokens` merged into PR B dashboard (`feat/037-mcp-tokens-ui`, on A), mcp repo PR (`feat/per-user-mode` in GameplanePanel/mcp), PR C chart + docs + submodule pointer (`feat/037-mcp-scope-chart`, on B, after the mcp PR merges). Implementation runs through `Workflow` scripts (Principle V): haiku agents with an explicit `model:`, then one sonnet diff review per batch.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an unfinished task).
- **[Story]**: US1 to US4 from spec.md. Setup, Foundational and Polish tasks carry no story.
- Paths starting `mcp-server/` are in the GameplanePanel/mcp repo (repo root = `mcp-server/`).

---

## Phase 1: Setup

- [ ] T001 Confirm the docs PR for this spec folder is merged to `master`, then create the four branches from fresh `master` (Gameplane) and `main` (mcp) named in **Delivery** above; record the base SHAs in the PR A description.
- [X] T002 List every existing test that encodes behaviour this spec changes, with file, test name, assertion and the needed change, and get valgul's written sign-off on each before editing any of them. Candidates found by grep on master `73389d7a`: `mcp-server/main_test.go` (`TestRegisteredToolsAreReadOnly`, `TestToolsListAndGetHappyPath`, `TestListEventsHandler_StripsAndBounds`; expected to stay unchanged because `*kube.Client` keeps satisfying the new `Reader` interface and break-glass with no allow-list behaves as today), `mcp-server/internal/kube/client_test.go`, `test/e2e/optional_components_e2e_test.go` (subtest `mcp`, lines 342-400; comment mentions the ClusterRole), `api/internal/auth/sessions_*_test.go` (cookie-only `Authenticate`), `api/internal/rbac/rbac_test.go` and `catalog_test.go` (rule table cross-checks). If none needs a change, write "none needed" with the reason per file in `specs/037-scope-mcp-server-permissions/T002-test-signoff.md`.

---

## Phase 2: Foundational (blocks every story)

- [ ] T003 Add the portable migration `api/internal/db/migrations/common/023_mcp_tokens.sql` creating `mcp_tokens` exactly as in data-model.md (columns, FK cascade, unique `digest`, unique `(user_id, name)`, index on `user_id`); no dialect functions, timestamps bound from Go.
- [ ] T004 [P] In the mcp repo, add `mcp-server/internal/reader/reader.go` with the `Reader` interface (`ListCRD`, `GetCRD`, `ListPods`, `GetPod`, `ListEvents`, `PodLogs`, each taking an optional cluster and, where relevant, server) and change `newMCPServer`, `registerTools` and every handler in `mcp-server/main.go`, `mcp-server/tools.go`, `mcp-server/fixadvice.go` to take `reader.Reader` instead of `*kube.Client`; `*kube.Client` implements it unchanged (cluster arguments other than local return "cluster not available in break-glass mode").
- [ ] T005 [P] Add the optional `cluster` argument to every tool input, `server` to `list_pods`, `list_events` and `get_pod_logs`, in `mcp-server/tools.go`, per contracts/mcp-server-modes.md; tool names stay unchanged.

**Checkpoint**: `go build ./...` passes in `api/` and in `mcp-server/` (`GOWORK=off`).

---

## Phase 3: User Story 1 - Admin installs the MCP server without exposing unrelated workloads (P1) 🎯 MVP

**Goal**: break-glass mode can read only the release namespace and the managed game namespaces (plus the three cluster-scoped catalog kinds).

**Independent Test**: quickstart.md §1 (`kubectl auth can-i` matrix, `list_pods` with and without a namespace).

- [ ] T006 [US1] In `charts/gameplane/templates/mcp-server.yaml`, replace ClusterRole/ClusterRoleBinding `{{ .Release.Name }}-mcp-server-read` with: ClusterRole + ClusterRoleBinding `{{ .Release.Name }}-mcp-server-catalog-read` (get/list/watch on `gametemplates`, `modules`, `modulesources`); Role + RoleBinding `{{ .Release.Name }}-mcp-server-read` in `.Release.Namespace` (get/list/watch `pods`, `events`; get `pods/log`); and the same Role + RoleBinding plus `gameservers`, `backups`, `backupschedules`, `restores` in each namespace from `include "gameplane.operatorGameNamespaces" . | fromJsonArray`. No write verb anywhere; keep the `api.standalone` and `mcpServer.enabled` guards and the header comments updated.
- [ ] T007 [US1] In the same template, add env `GAMEPLANE_MCP_NAMESPACES` to the Deployment container: comma-joined release namespace + managed game namespaces (uniq).
- [ ] T008 [P] [US1] In `mcp-server/internal/kube/client.go`, add a namespace allow-list read from `GAMEPLANE_MCP_NAMESPACES` (comma-separated, trimmed, empty = unrestricted): an explicit namespace outside it returns error `namespace not in scope` before any API call; "all namespaces" for pods, events and namespaced CRDs loops over the allow-list and merges results (events then pass through the existing `boundAndSortEvents`); a listed namespace that does not exist or is forbidden is skipped and reported in the result notice, not fatal.
- [ ] T009 [P] [US1] New tests in `mcp-server/internal/kube/allowlist_test.go`: out-of-scope namespace refused with no client call; all-namespaces merges only allowed ones; unset env keeps today's behaviour; missing namespace skipped with notice.
- [ ] T010 [US1] New chart check script `charts/gameplane/tests/mcp-server-rbac.sh` (same shape as `charts/gameplane/tests/monitoring-rules.sh`, called from the chart job in `.github/workflows/ci.yaml` next to it): render with `mcpServer.enabled=true` and `operator.gameNamespaces={tenant-b}`, assert no ClusterRole grants `pods`, `events` or `pods/log`, Roles exist in the release namespace, `gameplane-games` and `tenant-b`, and no rule has a write verb.
- [ ] T011 [US1] New E2E `TestOptionalComponents_MCPNamespaceScope` in `test/e2e/optional_components_scope_e2e_test.go` (bucket `optional-components`, register in `test/e2e/buckets.sh`): `kubectl auth can-i` matrix from quickstart.md §1 for the MCP ServiceAccount, `list_pods` with no namespace returns only in-scope pods, `list_pods namespace=kube-system` returns a tool error, and the old ClusterRole `gameplane-mcp-server-read` is gone after `helm upgrade`. `t.Parallel()` only if the bucket's lifecycle test allows it (bucket comment at `buckets.sh:424`).

**Checkpoint**: US1 delivered by the mcp PR + PR C chart changes.

---

## Phase 4: User Story 2 - An MCP session only sees what its Gameplane user may see (P1)

**Goal**: personal MCP tokens, token authentication with a read-only route allow-list, the new API reads, the MCP per-user mode and the dashboard page.

**Independent Test**: quickstart.md §2.

### API (PR A)

- [ ] T012 [US2] Create `api/internal/auth/mcptokens.go`: token generation (`gpmcp_` + 43 base64url chars from `crypto/rand`), SHA-256 digest, `CreateToken(ctx, userID, name, days)` (days ∈ {7, 30, 90, 365}, name 1-64 chars trimmed, max 10 active per user, copy `session_epoch`), `ListTokens(ctx, userID)`, `RevokeToken(ctx, userID, tokenID)`, `lookupToken(ctx, raw)` (valid only if digest matches, not revoked, not expired, epoch equals user's), `touchLastUsed` (at most once a minute per token). Wrap errors with `%w`.
- [ ] T013 [US2] In `api/internal/auth/sessions.go` `Authenticate`: when there is no session cookie and the `Authorization` header is `Bearer gpmcp_…`, authenticate through `lookupToken`, load perms with `LoadPerms`, put the user plus `ViaMCPToken`/`MCPTokenID` on the context, and `SetActor(ctx, username+" (mcp)")`. Same 401/503 bodies as the cookie path. Cookie behaviour unchanged.
- [ ] T014 [US2] Add `MCPTokenGuard` in `api/internal/auth/mcptokens.go`: for token requests, allow only GET/HEAD, no WebSocket upgrade, and route patterns on the allow-list in contracts/api-mcp-token-auth.md (match on `chi.RouteContext` pattern after routing, or an equivalent path matcher kept next to the list); else 403 `server.forbidden`. Mount it in `api/cmd/main.go` right after `sessions.Authenticate` (`api/cmd/main.go:389`).
- [ ] T015 [US2] Audit in `api/internal/auth/mcptokens.go` + `api/internal/audit/`: one `mcp-session` row per (token, `X-Gameplane-MCP-Session`) per 24 hours (in-memory TTL set), and an `mcp-refused` row for every refused token request (guard, RBAC or invalid token), per data-model.md.
- [ ] T016 [US2] Create `api/internal/handlers/mcp_tokens.go` with `GET/POST /users/me/mcp-tokens`, `DELETE /users/me/mcp-tokens/{id}`, `GET /users/{id}/mcp-tokens`, `DELETE /users/{id}/mcp-tokens/{tokenId}` per contracts/api-mcp-token-auth.md (the secret only in the POST response; token requests can never reach these routes). Mount in `api/cmd/main.go` next to `MountUsers`.
- [ ] T017 [US2] Add RBAC rules in `api/internal/rbac/rbac.go`: self rules for `/users/me/mcp-tokens` (GET, POST, DELETE prefix) next to lines 192-197 with `perm: ""`; `pods` and `kube-events` segments → `servers:read` (namespaced); control-plane namespace on those two segments requires `*` on the local cluster.
- [ ] T018 [US2] Create `api/internal/handlers/pods.go` with `GET /pods`, `GET /pods/{name}`, `GET /kube-events`, `GET /servers/{name}/pods`, `GET /servers/{name}/logs/pod/tail` per contracts/api-reads.md (cluster via `resolveCluster`, namespace via `scope.Resolve` or the admin-only control-plane namespace, managedFields stripped, events newest 100 with `truncated`/`total`, log tail default 200 lines, max 5000, newest 256 KiB, `text/plain`). Mount in `api/cmd/main.go`.
- [ ] T019 [P] [US2] New unit tests `api/internal/auth/mcptokens_test.go`: format, digest-only storage, each invalid state (expired, revoked, epoch bumped, user deleted), lifetime and name validation, 10-token limit, `last_used_at` throttle, guard allow and deny (POST, WebSocket, unlisted GET), and a test that every allow-list route is registered GET-only in the router.
- [ ] T020 [P] [US2] New envtest `api/internal/handlers/mcp_tokens_envtest_test.go`: create/list/revoke as self, admin list/revoke needs `users:read`/`users:manage`, a token cannot call token routes, CSRF still required for cookie POST/DELETE, audit rows written.
- [ ] T021 [P] [US2] New envtest `api/internal/handlers/pods_envtest_test.go`: namespace-scoped `servers:read` user sees only that namespace; owner-only user reaches `/servers/{name}/pods` and log tail but not `/pods`; control-plane namespace denied for non-admin, allowed for `*`; unknown cluster → forbidden; log tail bounds; events truncation.
- [ ] T022 [US2] Add the client methods and types to `web/src/lib/api.ts` (`listMcpTokens`, `createMcpToken`, `revokeMcpToken`, `listUserMcpTokens`, `revokeUserMcpToken`).
- [ ] T023 [US2] New E2E `TestAPI_RBAC_MCPTokenReads` in `test/e2e/api_rbac_mcp_token_e2e_test.go` (bucket `api-rbac`, register in `test/e2e/buckets.sh`; uses 2 logins of the bucket budget): token user with `servers:read` in one namespace reads only that namespace through `/servers`, `/pods`, `/kube-events`; POST with the token → 403; role removal → next read 403; revoke → 401; password reset → 401. `t.Parallel()`, unique names.
- [ ] T024 [US2] Changelog `changelog/unreleased/<PR A number>-mcp-tokens-api.md` (personal MCP tokens, new read routes).

### Design and dashboard (design branch, PR B)

- [ ] T025 [US2] Design in `design.fig` (OpenPencil per CLAUDE.md rule 1, canvas layout per the `pen-canvas-layout` skill): MCP tokens page (list with rows, empty state), Create token dialog (name, expiry 7/30/90/365 default 30), one-time reveal with copy and client config snippet, revoke confirm, mobile list, and the admin "MCP tokens" dialog on Users & RBAC, per contracts/ui-mcp-tokens.md; run `pen-componentize`, then `hack/design/fig-export.ts` and commit the export in `assets/design-export/` in the same commit. Stop for valgul's design sign-off before T026.
- [ ] T026 [US2] Build `web/src/routes/McpTokens.tsx` from the approved export, register `/settings/mcp-tokens` in `web/src/router/tree.tsx`, add the user-menu entry next to Theme settings, TanStack Query for list/create/revoke.
- [ ] T027 [US2] Add the admin "MCP tokens" dialog to the user row menu in `web/src/routes/Users.tsx` (list and revoke, revoke hidden without `users:manage`).
- [ ] T028 [P] [US2] Add every new string to the English i18n pack under `i18n/` with descriptions, keys `web.routes.mcp_tokens.*` and `web.routes.users.mcp_tokens.*`.
- [ ] T029 [P] [US2] New Vitest `web/src/routes/McpTokens.test.tsx` and `web/src/routes/Users_mcptokens.test.tsx`: list states, create flow shows the secret once, copy, revoke confirm, admin dialog permission gating.
- [ ] T030 [US2] Changelog `changelog/unreleased/<PR B number>-mcp-tokens-page.md`.

### MCP per-user mode (mcp repo PR)

- [ ] T031 [US2] Create `mcp-server/internal/panel/client.go`: `reader.Reader` over HTTPS to the API with `Authorization: Bearer` from `GAMEPLANE_MCP_TOKEN`, `X-Gameplane-MCP-Session` random per process, only `http.MethodGet`, TLS verified (optional `--panel-ca` PEM), HTTPS required except loopback, maps each method to the routes in contracts/mcp-server-modes.md (GameServer list falls back to `/users/me/servers` on 403; logs of API/operator pods go to `/admin/system-logs/{component}`), API errors returned as the API's message with the token redacted.
- [ ] T032 [US2] In `mcp-server/main.go` `runServe`: with `--panel-url`/`GAMEPLANE_PANEL_URL` set, require `GAMEPLANE_MCP_TOKEN` (exit with a clear error otherwise) and use the panel client; else break-glass `kube.Client`. Print the mode to stderr on start.
- [ ] T033 [P] [US2] New tests `mcp-server/internal/panel/client_test.go` against `httptest`: each method hits the right route with the right query, only GET is ever sent (tripwire scanning the package source for other `http.Method*` constants), token never appears in errors or logs, 401/403/404 mapping, owner fallback.
- [ ] T034 [P] [US2] New test `TestPanelClientHasNoMutatingMethods` in `mcp-server/main_test.go` (new function, existing ones untouched) reflecting over `panel.Client`'s method set like `TestClientHasNoMutatingMethods`.
- [ ] T035 [US2] Update `mcp-server/README.md` and `mcp-server/specs.md`: per-user mode setup (create a token, env vars, `docker run -i` example, MCP client config), break-glass mode and its namespace scope, remove the "cluster-wide accepted tradeoff" section (FR-032).

**Checkpoint**: US2 testable end to end once PR A, PR B and the mcp PR are merged and PR C bumps the pointer.

---

## Phase 5: User Story 3 - The same checks apply on every registered cluster (P2)

**Goal**: one session covers every cluster the user has a grant for, with that grant's namespaces.

**Independent Test**: quickstart.md §3.

- [ ] T036 [US3] In `mcp-server/internal/panel/client.go`: with no `cluster` argument, call `/clusters`, run the read on each listed cluster concurrently (bounded, 4 at a time, 10 s each), return results grouped by cluster plus `unavailable: [ids]`; with no `namespace`, use `/namespaces?cluster=` for that cluster.
- [ ] T037 [US3] Shape grouped results in `mcp-server/tools.go` (`{clusters: {<id>: …}, unavailable: […]}`), keeping single-cluster output unchanged when `cluster` is given.
- [ ] T038 [P] [US3] New tests `mcp-server/internal/panel/fanout_test.go`: two clusters merged and labelled, one unreachable reported not fatal, forbidden cluster indistinguishable from unknown.
- [ ] T039 [US3] New E2E `TestMultiCluster_MCPTokenRemoteGrant` in `test/e2e/multicluster_mcp_e2e_test.go` (bucket `multicluster`, register in `test/e2e/buckets.sh`): user with a remote grant for one namespace reads it with `?cluster=<remote>` through the token, gets forbidden on an ungranted cluster with the same body as an unknown ID, and a namespace-only server grant cannot read `/cluster` data.

---

## Phase 6: User Story 4 - Admins keep a break-glass path when the panel is down (P3)

**Goal**: `kubectl exec … serve` keeps working without the API, inside the US1 scope, and is documented as admin-only.

**Independent Test**: quickstart.md §1 with the API Deployment scaled to 0.

- [ ] T040 [US4] New E2E subtest in a new function `TestOptionalComponents_MCPBreakGlassWithoutAPI` in `test/e2e/optional_components_scope_e2e_test.go`: scale `gameplane-api` to 0, open a break-glass session, `list_pods namespace=<release ns>` succeeds, `kube-system` refused, scale back.
- [ ] T041 [P] [US4] Rewrite the `mcp-server (optional)` section of `docs/security.md` (line ~1126): two modes, per-user mode enforced by the API, break-glass gated by `kubectl exec` rights and limited to the release and game namespaces, no cluster-wide grant.
- [ ] T042 [P] [US4] Add an "MCP access" paragraph to `docs/multicluster-ui.md` "User access": MCP tokens follow the same per-cluster grants.

---

## Phase 7: Polish and cross-cutting

- [ ] T043 Bump the `mcp-server` submodule pointer in PR C after the mcp PR merges (`submodule-bump` skill, argument `mcp-server`).
- [ ] T044 Changelog `changelog/unreleased/<PR C number>-mcp-namespace-scope.md` titled `Breaking: MCP server no longer has cluster-wide read access`, with the upgrade note (old ClusterRole removed; per-user mode via tokens).
- [ ] T045 [P] Update `docs/agent-architecture.md` task map with the new files (`auth/mcptokens.go`, `handlers/pods.go`, `handlers/mcp_tokens.go`, `routes/McpTokens.tsx`).
- [ ] T046 Run `/speckit-analyze` against spec, plan and this file; fix findings in the spec folder.
- [ ] T047 Walk quickstart.md on CI results (E2E buckets `api-rbac`, `multicluster`, `optional-components` green), then archive the spec folder with the `spec-archive` skill after the last PR merges.

---

## Dependencies and execution order

- **Setup**: T001 first. T002 gates any edit to an existing test file.
- **Foundational**: T003 blocks T012-T021. T004 and T005 block every mcp repo task (T008, T031-T038).
- **US1**: T006 → T007 → T010; T008 → T009; T011 needs T006-T008 merged into one install (PR C after the mcp PR).
- **US2**: T012 → T013 → T014 → T015; T016 and T017 after T012; T018 after T017; tests T019-T021 after their code; T023 after T013-T018. T025 sign-off gates T026-T029. T031 → T032 → T033-T035.
- **US3**: after T031 (T036 → T037 → T038); T039 after PR A merges.
- **US4**: T040 after T011; T041-T042 anytime after the plan.
- **Polish**: T043 after the mcp PR merges; T044-T047 last.

### PR mapping

| PR | Tasks |
|---|---|
| A API (`feat/037-mcp-token-api`) | T003, T012-T024 |
| Design (`design/037-mcp-tokens`, into B) | T025 |
| B dashboard (`feat/037-mcp-tokens-ui`, on A) | T026-T030 |
| mcp repo (`feat/per-user-mode`) | T004, T005, T008, T009, T031-T038 |
| C chart + docs + pointer (`feat/037-mcp-scope-chart`, on B) | T006, T007, T010, T011, T039-T045 |

## Parallel examples

- **Foundational**: T003 (Gameplane) with T004 + T005 (mcp repo).
- **US1**: T008 + T009 (mcp repo) alongside T006 + T007 (chart).
- **US2**: after T018, T019 + T020 + T021 together; T028 + T029 together; T033 + T034 together.
- **US3/US4**: T038 alongside T041 + T042.

## Implementation strategy

1. **MVP = US1**: the chart scope and break-glass allow-list alone close the cluster-wide blast radius (the request's first ask). It can ship as the mcp PR + a chart-only PR C if PR A/B are slower.
2. **US2** next: API first (PR A, testable by curl with a token), then dashboard (PR B), then the mcp per-user mode.
3. **US3** in the same mcp PR once the panel client exists.
4. **US4** is mostly verification and docs on top of US1.

Each batch: haiku agents with explicit `model:`, one sonnet review of the batch diff against this file and the contracts, fixes in a new workflow.
