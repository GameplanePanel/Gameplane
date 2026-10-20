# mcp-server — Specification

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/mcp-server`

## Purpose

Optional, strictly **read-only** Model Context Protocol (MCP) server that exposes cluster state from a Gameplane deployment. Lets an AI assistant read 7 of Gameplane's 9 CRDs (not Cluster or NetworkCapture), Pods, Events, and pod logs, and receive suggested fixes as plain text (YAML and/or `kubectl` commands) for a human operator to review and apply. Speaks MCP (JSON-RPC 2.0) over stdio, not a network port.

## Responsibilities

1. **Expose read-only MCP tools** — 7 tools listed below, all List/Get-shaped or text-generating.
2. **Guarantee read-only operation** — through three independent enforcement layers (see Key Invariants).
3. **Index 7 of Gameplane's 9 CRDs** — GameServer, GameTemplate, Backup, BackupSchedule, Restore, Module, ModuleSource (not Cluster or NetworkCapture), by name and optional label selector.
4. **Expose core Kubernetes resources** — Pods, Events, and pod logs, scoped to namespace (or all namespaces).
5. **Generate fix suggestions** — the `propose_fix` tool matches symptoms against heuristic keyword rules and returns suggested diagnostics and remediation steps as plain text.
6. **Support two subcommands** — `idle` (the default, long-lived Deployment process) and `serve` (per-client MCP session over stdio).

## Non-goals / boundaries

- **No mutations** — never creates, updates, patches, deletes, or applies anything. Guarded structurally and by RBAC.
- **No HTTP port** — stdio-only transport, no listening socket.
- **No LLM calls** — fix suggestions are deterministic, keyword-matched heuristic text.
- **No config management** — reads Kubernetes objects as-is; does not validate them, enforce schemas, or trigger reconciliation.
- For run instructions and Helm values, see `mcp-server/README.md`.

## Directory & package layout

```
mcp-server/
├── main.go                   # Entry point; idle/serve subcommands; newMCPServer
├── tools.go                  # Tool registration; 7 handler functions; readOnlyTool helper
├── fixadvice.go              # Heuristic advice text; buildFixSuggestion; matchFixRules
├── main_test.go              # Tool-set registration tests; client invariant tripwire test
├── internal/kube/
│   ├── client.go             # Read-only Client struct; List/Get methods; CRDKinds registry
│   └── client_test.go        # Fake client tests
├── go.mod                    # Dependencies
├── go.sum
├── .testcoverage.yml         # Coverage gate (70%)
└── README.md                 # Run instructions; RBAC blast radius; transport details
```

## External interface / contracts

### Subcommands

- **`idle`** (default, no args) — blocks indefinitely until signaled (SIGTERM/SIGINT). Performs no cluster access. Used as the Helm Deployment's container process so a live pod exists for `kubectl exec` to attach to.
- **`serve`** — builds a Kubernetes client (via `ctrl.GetConfig()`, supports in-cluster or kubeconfig), runs one MCP session over stdio until the client disconnects. Paired with `kubectl exec -i deploy/gameplane-mcp-server -n <ns> -- /mcp-server serve`.

### Registered tools (7 total)

From `tools.go` `registeredToolNames`:

| Tool | Input | Output |
|---|---|---|
| `list_gameplane_resources` | `kind` (e.g., "GameServer"), optional `namespace`, optional `labelSelector` | Unstructured list of CRD objects as JSON |
| `get_gameplane_resource` | `kind`, optional `namespace`, `name` | Single Gameplane CRD object as JSON |
| `list_pods` | Optional `namespace`, optional `labelSelector` | Core Pod list as JSON |
| `get_pod` | `namespace`, `name` | Single Pod (spec+status) as JSON |
| `list_events` | Optional `namespace`, optional `fieldSelector`, optional `labelSelector` | Core Event list as JSON |
| `get_pod_logs` | `namespace`, `pod`, optional `container`, optional `tailLines` (capped 5000), optional `previous` | Log text (up to 256 KiB of the newest bytes; when truncated, prefixed with a notice) |
| `propose_fix` | Optional `kind`/`namespace`/`name`, required `symptom` (free text) | Suggested diagnostics + fix text (never applies anything) |

Pod logs read the complete finite Kubernetes tail response while retaining only a 256 KiB rolling buffer. Long lines can make the requested tail exceed 4 MiB; the returned suffix still includes the actual final bytes. Request cancellation and stream read failures return errors instead of presenting partial output as the newest logs.

**Artifact scope:**
- **7 of Gameplane's 9 CRDs** (not Cluster or NetworkCapture): GameServer, GameTemplate, Backup, BackupSchedule, Restore, Module, ModuleSource (4 namespaced, 3 cluster-scoped; verified against `CRDKinds` in `internal/kube/client.go`).
- **Core resources**: `v1` Pods, Events, `pods/log` subresource.
- **No write verbs**: no Create/Update/Patch/Delete/Apply anywhere.

### Transport

**MCP over stdio only**. The server uses `mcp.StdioTransport` and never opens a listening socket. A client connects by spawning the process with stdin/stdout/stderr piped to the MCP JSON-RPC stream.

## Key invariants

The read-only guarantee rests on **two enforcing layers** (the handler boundary and RBAC) plus a test tripwire. Only RBAC stops mutation by any code in the process:

### 1. Handler boundary (`kube.Client`)

**Source**: `mcp-server/internal/kube/client.go` (the `Client` struct, `New`, `NewFrom`), `mcp-server/main.go` (package doc comment and `runServe`).

- The `kube.Client` struct (in package `internal/kube`) holds two unexported fields:
  - `typed kubernetes.Interface` — typed clientset (supports all verbs)
  - `dynamic dynamic.Interface` — dynamic client (supports all verbs)
- The **only exported methods on `Client`** are:
  - `ListCRD`, `GetCRD` (CRD reads)
  - `ListPods`, `GetPod` (Pod reads)
  - `ListEvents` (Event reads)
  - `PodLogs` (log reads)
- Every MCP tool handler lives in `package main` (`tools.go`, `fixadvice.go`) and receives `*kube.Client` as input.
- **What the boundary enforces**: a caller that holds only a `*kube.Client` cannot reach its unexported clientsets, so no tool handler can call `Create`, `Update`, `Delete`, `Patch`, or `Apply` through it.
- **What it does not enforce**: the boundary is on the `*kube.Client`, not on `package main`. `runServe` loads the `*rest.Config` itself (`ctrl.GetConfig()`) and passes it to `kube.New`. Keeping every handler on the `*kube.Client`, and having no mutating call site anywhere in the module, is a code convention checked in review, not a compile-time guarantee. Layer 2 is what stops mutation.

### 2. RBAC backstop (authoritative)

**Source**: `charts/gameplane/templates/mcp-server.yaml` lines 22–36.

The Helm chart installs a `ClusterRole` with **only read verbs**:

```yaml
rules:
  - apiGroups: ["gameplane.local"]
    resources: [gameservers, gametemplates, backups, backupschedules, restores, modules, modulesources]
    verbs: [get, list, watch]
  - apiGroups: [""]
    resources: [pods, events]
    verbs: [get, list, watch]
  - apiGroups: [""]
    resources: [pods/log]
    verbs: [get]
```

- **No create/update/patch/delete verbs anywhere**.
- The ServiceAccount (`gameplane-mcp-server`) is bound to this role; any attempt to mutate (even if code somehow tried) is rejected by the API server with 403 Forbidden.
- This is the **authoritative** layer and the only one that stops mutation by any code in the process: the API server rejects a write from this ServiceAccount whatever client makes it. It covers the in-cluster ServiceAccount; a standalone run against a kubeconfig has that kubeconfig user's permissions instead.

### 3. Test tripwire (visible guarantee)

**Source**: `mcp-server/main_test.go` lines 28–50 and 68–104.

Two tests catch regressions:

- **`TestClientHasNoMutatingMethods`** (lines 40–50): reflects over `kube.Client`'s method set; asserts no exported method name starts with Create, Update, Delete, Patch, or Apply. Catches a future mistake in `internal/kube/client.go` immediately.
- **`TestRegisteredToolsAreReadOnly`** (lines 73–104): connects a real MCP client over in-memory transports; asserts:
  1. Every registered tool name does not contain a mutating verb (case-insensitive substring search).
  2. Every tool carries `Annotations.ReadOnlyHint = true`.
  3. No tool has `DestructiveHint = true`.
  4. The tool set matches `registeredToolNames` exactly (from `tools.go` line 27).

This test catches accidental registration of a mutating tool before it ships.

**Note**: These tests are **visible guarantees** (for auditing), not enforcement. Layer 1 keeps tool handlers on read-only methods; layer 2 (RBAC) is the layer that stops mutation.

## Dependencies

From `go.mod` (verified):

| Dependency | Version | Purpose |
|---|---|---|
| `github.com/modelcontextprotocol/go-sdk` | v1.8.0 | MCP protocol implementation (Server, tools, stdio transport) |
| `k8s.io/api` | v0.37.0 | Kubernetes API types (corev1) |
| `k8s.io/apimachinery` | v0.37.0 | Kubernetes meta types, label/field selectors, dynamic client |
| `k8s.io/client-go` | v0.37.0 | Kubernetes typed clientset, dynamic client, rest.Config |
| `sigs.k8s.io/controller-runtime` | v0.25.1 | `ctrl.GetConfig()` for kubeconfig loading |

**Why these versions?** Pinned to match the operator's dependencies for consistency across the workspace (via `go.work`).

## Security considerations

1. **Read-only blast radius**: The ClusterRole grants get/list/watch cluster-wide (all namespaces), not just `gameplane-games`. Pod logs in any namespace may contain application secrets (API keys, connection strings); this is an accepted tradeoff on an opt-in component.
2. **No listening port**: No network surface to expose; reachable only via `kubectl exec`.
3. **Deployment model**: Opt-in via `mcpServer.enabled=true` in the Helm chart. Admin responsibility to decide if this is acceptable for their cluster.
4. **See `docs/security.md`** for the full threat model and per-cluster decisions (especially if cohabiting with unrelated sensitive workloads).

## Testing & coverage

### Coverage gate

- **Threshold**: 70% (from `.testcoverage.yml`).
- **Measured against**: package-level aggregate (not per-file).
- **What's covered**: tool registration, read-only invariant tripwires (main_test.go), CRD/Pod/Event/log client helpers against fake clientsets, propose_fix heuristic advice text.
- **Uncovered**: main()/run() process wiring (signal handling, stdio transport) — not unit-testable.

### Test commands

Run via the workspace root Makefile:

```sh
make test-go           # Runs all Go tests, including mcp-server
make cover             # Full coverage report with gate checks
cd mcp-server && go test ./...    # Isolated run
```

### Key test cases

From `main_test.go`:

- `TestClientHasNoMutatingMethods` — lint tripwire on Client's exported method set.
- `TestRegisteredToolsAreReadOnly` — over-the-wire check: tool names, ReadOnlyHint, exact registry.
- `TestToolsListAndGetHappyPath` — happy-path tests for list/get tools using fake clientsets.
- `TestProposeFixTool` — propose_fix heuristic matching and fallback advice.
- `TestRunIdleReturnsOnCancel` — idle subcommand cancellation.

## References

- **`mcp-server/README.md`** — run instructions, transport details, RBAC blast radius, tool descriptions.
- **`charts/gameplane/templates/mcp-server.yaml`** — Helm template (ServiceAccount, ClusterRole, Deployment).
- **`docs/security.md`** — threat model, mcp-server section, admin decision guidance.
- **`go.work`** — workspace that links all Go modules (operator, api, agent, mcp-server, etc.).
- **Model Context Protocol spec** — https://modelcontextprotocol.io/

