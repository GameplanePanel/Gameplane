# Open decisions: spec 037 Scope MCP server permissions

Only a decision valgul answers is written here as settled. OD-1 to OD-3 were asked as decision cards and settled on 2026-11-17. OD-4 and OD-5 were raised by planning on 2026-11-22: both are settled. No decision is open.

## OD-1: How does an MCP session prove which Gameplane user it acts for? (SETTLED: A, valgul 2026-11-17)

Today a session has no Gameplane user: it reads with the pod's Kubernetes identity.

- A. Personal MCP token: the user creates a read-only, expiring, revocable token in the dashboard (their profile) and puts it in their MCP client config. The token never grants more than the user's current permissions. Needs a small dashboard screen (design.fig design first). (Recommended: works for local and SSO users alike, revocable per token, no browser needed in the MCP client.)
- B. Sign in from the MCP client with the user's normal login (password or SSO in a browser window, like `kubectl` OIDC plugins). No new token screen, but each client must handle a browser login and SSO-only installs need a device-login flow.
- C. Map the Kubernetes identity that opens `kubectl exec` to a Gameplane user. No Gameplane login at all, but only works for users who already have Kubernetes access, which most Gameplane users do not.

## OD-2: Where do MCP reads go, and how are remote clusters reached? (SETTLED: A, valgul 2026-11-17)

- A. Through the Gameplane API: the MCP server sends every read to the API as the user, and the API applies roles, namespace grants and per-cluster grants exactly as for the dashboard. Remote clusters work through the existing cluster registration. The MCP server needs no Kubernetes permissions of its own in this mode and can run on the user's own machine. The API gains read routes it lacks today (pod list, raw events, pod logs by pod). (Recommended: one place enforces permissions, multicluster comes for free, no rule copied into the MCP server.)
- B. MCP server keeps reading Kubernetes directly with the namespace-scoped role from Story 1, asks the API only for the user's permissions, and filters results itself. Each remote cluster needs its own MCP install that can reach the central panel. Permission rules are duplicated in the MCP server and must be kept in sync.
- C. Direct reads on the local cluster only (as B), and remote clusters are out of scope for now.

## OD-3: Does the in-cluster `kubectl exec` mode stay? (SETTLED: A, valgul 2026-11-17)

Today's way to start a session is `kubectl exec -i deploy/gameplane-mcp-server -- /mcp-server serve`, with no Gameplane user.

- A. Keep it as an admin-only break-glass mode for when the API is down, limited to the main and game namespaces (Story 1), with no Gameplane user check because access to the pod already needs Kubernetes rights. The per-user mode from OD-1/OD-2 becomes the normal way. (Recommended: the MCP server is for debugging, and a broken API is when it is needed most.)
- B. Remove it: the per-user mode is the only mode, the in-cluster Deployment and its role go away. Breaking change for anyone using `kubectl exec ... serve` today.
- C. Keep it, but require a Gameplane credential in it too (break-glass is lost when the API is down).

## OD-4: How long can a personal MCP token live? (SETTLED: B, valgul 2026-11-22)

Every token expires (spec FR-016); this sets the choices in the Create token dialog. Planning also proposes at most 10 active tokens per user.

- A. 7, 30 or 90 days, default 30. (Recommended: a leaked token stops working within a quarter at most, and 30 days is a short enough default for a credential that reads logs.)
- B. 7, 30, 90 or 365 days, default 30. Fewer renewals for long-lived assistant setups, but a leaked token can work for a year.
- C. A custom date up to an admin-set maximum (Admin Settings, default 90 days). Most flexible; adds an admin setting and its design.

## OD-5: Who may create personal MCP tokens? (SETTLED: A, valgul 2026-11-22)

The per-user MCP mode needs no Helm switch, because it runs on the user's machine and talks to the API.

- A. Every signed-in user, always. The token can only read what the user can already read in the dashboard. (Recommended: no new setting, and a token never widens access.)
- B. Every signed-in user, but only when an admin turns on "Allow MCP tokens" in Admin Settings (default off). Adds a setting, its design, and a clear disabled state on the tokens page.
- C. Only users with a new permission `mcp:tokens` granted through roles. Adds a catalog permission and role edits.
