# MCP server keeps the unavailable-cluster warning on long event lists

When `list_events` runs across clusters with a personal MCP token and returns more than 100 events, the result now still says which clusters could not be read, after the truncation notice. Before, the truncation notice replaced that warning. [PR #1081](https://github.com/GameplanePanel/Gameplane/pull/1081)
