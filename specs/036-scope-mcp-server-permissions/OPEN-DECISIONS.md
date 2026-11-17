# Open decisions: spec 036 Scope MCP server permissions

Each was asked of valgul as a decision card on 2026-11-17. Only a decision valgul answers is written here as settled. OD-1 and OD-2 are settled; OD-3 is open.

## OD-1: How does an MCP session prove which Gameplane user it acts for? (SETTLED: A, valgul 2026-11-17)

Today a session has no Gameplane user: it reads with the pod's Kubernetes identity.

- A. Personal MCP token: the user creates a read-only, expiring, revocable token in the dashboard (their profile) and puts it in their MCP client config. The token never grants more than the user's current permissions. Needs a small dashboard screen (Pencil design first). (Recommended: works for local and SSO users alike, revocable per token, no browser needed in the MCP client.)
- B. Sign in from the MCP client with the user's normal login (password or SSO in a browser window, like `kubectl` OIDC plugins). No new token screen, but each client must handle a browser login and SSO-only installs need a device-login flow.
- C. Map the Kubernetes identity that opens `kubectl exec` to a Gameplane user. No Gameplane login at all, but only works for users who already have Kubernetes access, which most Gameplane users do not.

## OD-2: Where do MCP reads go, and how are remote clusters reached? (SETTLED: A, valgul 2026-11-17)

- A. Through the Gameplane API: the MCP server sends every read to the API as the user, and the API applies roles, namespace grants and per-cluster grants exactly as for the dashboard. Remote clusters work through the existing cluster registration. The MCP server needs no Kubernetes permissions of its own in this mode and can run on the user's own machine. The API gains read routes it lacks today (pod list, raw events, pod logs by pod). (Recommended: one place enforces permissions, multicluster comes for free, no rule copied into the MCP server.)
- B. MCP server keeps reading Kubernetes directly with the namespace-scoped role from Story 1, asks the API only for the user's permissions, and filters results itself. Each remote cluster needs its own MCP install that can reach the central panel. Permission rules are duplicated in the MCP server and must be kept in sync.
- C. Direct reads on the local cluster only (as B), and remote clusters are out of scope for now.

## OD-3: Does the in-cluster `kubectl exec` mode stay? (OPEN)

Today's way to start a session is `kubectl exec -i deploy/gameplane-mcp-server -- /mcp-server serve`, with no Gameplane user.

- A. Keep it as an admin-only break-glass mode for when the API is down, limited to the main and game namespaces (Story 1), with no Gameplane user check because access to the pod already needs Kubernetes rights. The per-user mode from OD-1/OD-2 becomes the normal way. (Recommended: the MCP server is for debugging, and a broken API is when it is needed most.)
- B. Remove it: the per-user mode is the only mode, the in-cluster Deployment and its role go away. Breaking change for anyone using `kubectl exec ... serve` today.
- C. Keep it, but require a Gameplane credential in it too (break-glass is lost when the API is down).
