# Relay flow metrics

The Tailscale supervisor exposes flow, admission and queue counters by protocol. The UDP
benchmark compares direct and relayed traffic at the same offered load and reports loss,
round-trip latency and jitter. [PR #905](https://github.com/GameplanePanel/Gameplane/pull/905)
