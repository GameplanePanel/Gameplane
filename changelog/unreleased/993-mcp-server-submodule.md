# MCP server source moved to its own repository

The optional read-only MCP server's code now lives in
[GameplanePanel/mcp](https://github.com/GameplanePanel/mcp) and is checked out
here as the `mcp-server/` submodule. Nothing changes for installs: the image,
chart values and `mcpServer.enabled` stay the same. If you build images or run
`make dev-up` from a clone, run `git submodule update --init mcp-server` first.
[PR #993](https://github.com/GameplanePanel/Gameplane/pull/993)
