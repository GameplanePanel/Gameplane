# Personal MCP tokens and read-only pod and event routes

Users can create personal MCP tokens in the API (`/users/me/mcp-tokens`) for a read-only MCP client. Each token lasts 7, 30, 90 or 365 days, can be revoked at any time, and never grants more than its owner's permissions. Administrators can list and revoke a user's tokens. The API also gains read routes for pods, Kubernetes events and pod logs: `/pods`, `/pods/{name}`, `/kube-events`, `/servers/{name}/pods` and `/servers/{name}/logs/pod/tail`. No upgrade step is needed. [PR #0000](https://github.com/GameplanePanel/Gameplane/pull/0000)
