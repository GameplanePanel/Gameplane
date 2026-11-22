# Contract: MCP server modes and tools (GameplanePanel/mcp)

## Modes

| Mode | Started by | Identity | Reach |
|---|---|---|---|
| Per-user (normal) | `GAMEPLANE_PANEL_URL=… GAMEPLANE_MCP_TOKEN=… mcp-server serve` on the user's machine (or `docker run -i … serve` with those env vars) | The token's Gameplane user | Every registered cluster and namespace that user may read, through the API |
| Break-glass (admin) | `kubectl exec -i deploy/gameplane-mcp-server -n <release ns> -- /mcp-server serve` | The pod's ServiceAccount; no Gameplane user | Local cluster, release namespace + managed game namespaces only (`GAMEPLANE_MCP_NAMESPACES`) |

`serve` with `GAMEPLANE_PANEL_URL` set and no token exits with a clear error. The server writes which mode it runs in to stderr on start.

## Tools (names unchanged; new optional `cluster` argument on every tool)

| Tool | Per-user mode API call | Break-glass |
|---|---|---|
| `list_gameplane_resources` | `/{servers|templates|backups|schedules|restores}`, `/modules`, `/modules/sources`; for `GameServer` without namespace permission, falls back to `/users/me/servers` | as today, within the allow-list |
| `get_gameplane_resource` | `/{kind path}/{name}` | as today, within the allow-list |
| `list_pods` | `/pods` or `/servers/{server}/pods` (new optional `server` argument) | as today, within the allow-list |
| `get_pod` | `/pods/{name}` | as today, within the allow-list |
| `list_events` | `/kube-events` or `/servers/{server}/events` | as today, within the allow-list |
| `get_pod_logs` | `/servers/{server}/logs/pod/tail` (new `server` argument required in this mode), or `/admin/system-logs/{component}` for API/operator pods | as today, within the allow-list |
| `propose_fix` | reads through the same calls, so it never sees more than the user | as today |

- No `cluster` → per-user mode calls `/clusters`, runs the call on each, and returns `{cluster: [...]}` groups plus `unavailable: [clusterIDs]`. Break-glass ignores `cluster` other than the local one and says so.
- No `namespace` → per-user mode uses the namespaces the user may read on that cluster (from `/namespaces`); break-glass loops over its allow-list.
- Refusals are returned as tool errors with the API's message (`not authenticated`, `permission denied`, `namespace not in scope`, `cluster not found or not permitted`).

## Invariants (tested)

- Tool handlers hold only a `Reader` interface; both implementations expose only read-shaped exported methods (`TestClientHasNoMutatingMethods` extended to the panel client).
- The panel client issues only `http.MethodGet`; a test fails if any other method appears in it.
- Tokens are read only from the environment and never logged; error messages redact the `Authorization` header.
- Transport stays stdio only.
