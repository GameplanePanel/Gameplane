# Implementation Plan: Scope MCP server permissions

**Branch**: `spec-037/scope-mcp-server-permissions` | **Date**: 2026-11-22 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/037-scope-mcp-server-permissions/spec.md`

**Open decisions**: planning raised OD-4 (token lifetimes, settled: 7, 30, 90 or 365 days) and OD-5 (who may create tokens, settled: every signed-in user) in [OPEN-DECISIONS.md](OPEN-DECISIONS.md). Everything else in this plan follows the settled OD-1 to OD-3.

## Summary

The MCP server gets two modes. The normal **per-user mode** runs on the user's machine, authenticates to the Gameplane API with a personal MCP token (OD-1) and sends every read through the API as that user (OD-2), so roles, namespace grants, owner fallback and per-cluster grants apply exactly as in the dashboard, on every registered cluster. The in-cluster **break-glass mode** (`kubectl exec … serve`, OD-3) keeps reading Kubernetes directly, but the chart's cluster-wide ClusterRole is replaced by namespaced Roles in the release namespace and the managed game namespaces, plus a small ClusterRole for the three cluster-scoped catalog kinds. The API gains token authentication behind a read-only route allow-list, four read routes the tools need, token management routes and audit rows. The dashboard gains a personal "MCP tokens" page, designed in `design.fig` first. Details: [research.md](research.md).

## Technical Context

**Language/Version**: Go (api, mcp-server: versions from `.devcontainer/versions.toml`), TypeScript strict (web), Helm 3 templates (chart)

**Primary Dependencies**: chi, client-go, controller-runtime `GetConfig` (mcp-server break-glass), `github.com/modelcontextprotocol/go-sdk` (mcp-server), React 19 + TanStack Query + HeroUI v3 (web). No new third-party dependency.

**Storage**: SQLite / PostgreSQL through the API store; one new portable migration `common/023_mcp_tokens.sql` ([data-model.md](data-model.md)).

**Testing**: Go unit tests (API token routes and read routes in `api/internal/handlers/mcp_tokens_test.go` and `pods_test.go` with a fake clientset), mcp repo unit tests (`httptest` panel, fake clientset break-glass), web Vitest, chart `helm template` checks, E2E in `test/e2e/` buckets `api-rbac`, `multicluster`, `optional-components`. CI only (rule 8).

**Target Platform**: Kubernetes (k3s to multi-node), standalone panel; MCP server per-user mode on Linux/macOS/Windows hosts via the published container image.

**Project Type**: multi-repo web service (Gameplane API + dashboard + chart) plus a split component (`GameplanePanel/mcp`, submodule `mcp-server/`).

**Performance Goals**: a token-authenticated read adds one indexed digest lookup and the existing permission load (same cost as a cookie session). `last_used_at` written at most once a minute per token.

**Constraints**: MCP server stays stdio-only and read-only (FR-030, FR-031); tokens never stored or logged in clear; refusals match dashboard wording (FR-014, login privacy rule 3).

**Scale/Scope**: up to 10 active tokens per user; tool fan-out bounded by registered clusters (tens).

## Constitution Check

| Principle | Status |
|---|---|
| I. E2E-tested delivery | PASS (planned): E2E for break-glass scope (`optional-components`), token reads and revocation (`api-rbac`), remote grant (`multicluster`). New tests use `t.Parallel()`, unique names, and fit the per-bucket login budget (2 extra logins in `api-rbac`). |
| II. Design-first | PASS (planned): MCP tokens page and admin dialog designed in `design.fig`, exported with `hack/design/fig-export.ts`, before any React code. |
| III. Language best practice | PASS: `%w` wrapping, strict TS, no suppressions; migration portable per README. |
| IV. Spec-driven | PASS: spec, ODs, plan, research, data model, contracts, quickstart in this folder; `mcp-server/specs.md` updated in the mcp PR; `api` docs updated with routes. |
| V. Delegate | PASS (planned): implementation through Workflow scripts with explicit models starting at haiku, briefed from this folder. |
| VI. CI bears the load | PASS: only compile checks locally; CI in both repos verifies. |
| Kubernetes primitives first | PASS: Role/RoleBinding/ClusterRole only; no new CRD. |
| Operator authority | PASS: reads only; nothing bypasses reconciliation. |

Re-checked after Phase 1 design: no new violations.

## Project Structure

### Documentation (this feature)

```text
specs/037-scope-mcp-server-permissions/
├── spec.md
├── OPEN-DECISIONS.md
├── plan.md               # this file
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── api-mcp-token-auth.md
│   ├── api-reads.md
│   ├── mcp-server-modes.md
│   └── ui-mcp-tokens.md
├── checklists/requirements.md
└── tasks.md              # /speckit-tasks
```

### Source Code

```text
GameplanePanel/mcp (submodule mcp-server/)
├── main.go                 # serve: choose per-user (panel) or break-glass (kube) mode
├── tools.go, fixadvice.go  # handlers take a Reader; new cluster/server args; fan-out
├── internal/reader/        # Reader interface shared by both clients
├── internal/kube/          # break-glass client + namespace allow-list
├── internal/panel/         # NEW: GET-only HTTPS client to the Gameplane API
├── README.md, specs.md     # both modes, no cluster-wide wording
└── *_test.go               # tripwires extended to panel client

GameplanePanel/Gameplane
├── api/internal/auth/mcptokens.go        # NEW: token create/lookup/revoke, guard
├── api/internal/auth/sessions.go         # Authenticate: bearer fallback
├── api/internal/handlers/mcp_tokens.go   # NEW: /users/me/mcp-tokens, /users/{id}/mcp-tokens
├── api/internal/handlers/pods.go         # NEW: /pods, /pods/{name}, /kube-events, /servers/{name}/pods, /servers/{name}/logs/pod/tail
├── api/internal/rbac/rbac.go             # rules for pods, kube-events, mcp-tokens
├── api/internal/db/migrations/common/023_mcp_tokens.sql
├── api/cmd/main.go                       # mount routes + guard
├── web/src/routes/McpTokens.tsx (+ test) # NEW personal page; Users.tsx admin dialog
├── web/src/lib/api.ts, router/tree.tsx
├── i18n/ packs                           # new strings
├── design.fig + assets/design-export/    # new screens
├── charts/gameplane/templates/mcp-server.yaml  # namespaced Roles, catalog ClusterRole, env
├── docs/security.md, docs/multicluster-ui.md   # MCP sections
├── test/e2e/                             # new tests + buckets.sh
└── changelog/unreleased/<PR>-mcp-*.md
```

**Structure Decision**: work splits by repository and by reviewable unit, delivered as stacked PRs (one thread, valgul's multi-PR rule for specs):

1. **Gameplane PR A, API** (`feat/037-mcp-token-api`): migration, token auth + guard, token routes, read routes, RBAC rules, audit, unit tests, E2E `api-rbac`, changelog.
2. **Design** (`design/037-mcp-tokens`, merged into PR B): `design.fig` screens + export.
3. **Gameplane PR B, dashboard** (on A): MCP tokens page, admin dialog, i18n, Vitest.
4. **mcp PR** (`feat/per-user-mode` in GameplanePanel/mcp): Reader, panel client, break-glass allow-list, tools, docs, tests.
5. **Gameplane PR C, chart + docs + pointer** (on B, after the mcp PR merges): chart RBAC and env, `docs/security.md`, `docs/multicluster-ui.md`, submodule bump, E2E `optional-components` and `multicluster`, changelog (breaking note: cluster-wide MCP role removed).

## Complexity Tracking

| Item | Why needed | Simpler alternative rejected because |
|---|---|---|
| Two MCP server modes | OD-3 keeps break-glass for when the API is down | One mode loses either per-user checks or debugging while the API is broken |
| Token route allow-list in addition to RBAC | Makes "a token can only read" hold even if a GET route is added later with side effects | Method-only guard would admit console WebSocket upgrades and capture downloads |
