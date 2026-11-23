# Breaking: MCP server no longer reads every namespace

The break-glass MCP server (`mcpServer.enabled`) no longer has a cluster-wide
ClusterRole for Pods, Events and pod logs. The chart removes the old
`<release>-mcp-server-read` ClusterRole and ClusterRoleBinding on upgrade. Break-glass
reads now cover only the release namespace and the managed game namespaces, through
namespaced Roles. A ClusterRole remains for GameTemplate, Module and ModuleSource only.
After upgrading, use a personal MCP token (`/users/me/mcp-tokens`) for per-user reads
across clusters. See [`docs/security.md`](docs/security.md#mcp-server-optional).
[PR #1046](https://github.com/GameplanePanel/Gameplane/pull/1046)
