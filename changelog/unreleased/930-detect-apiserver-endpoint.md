# Agent heartbeats reach a public API server endpoint

The chart's `allow-agent-to-apiserver` and `allow-sentinel-to-apiserver` policies only allowed
private ranges by default, so on a k3s node with a public IP every agent heartbeat timed out
and servers stayed "Starting" with no CPU, memory or disk figures. When
`networkPolicies.apiServerCIDRs` is unset, the chart now also allows the `kubernetes` Service
addresses it detects at install or upgrade time. [PR #930](https://github.com/GameplanePanel/Gameplane/pull/930)
