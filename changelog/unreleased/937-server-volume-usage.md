# Per-server storage usage

The agent reports storage scoped to the server data directory. It uses native directory counters on kernel CephFS and a bounded background scan on other filesystems. The dashboard labels requested storage separately when measured capacity is unavailable.
