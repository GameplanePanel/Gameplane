# Audit syslog bridge, tunnel clients and capture sidecar moved to their own repositories

The code for the audit-syslog-bridge, the tunnel clients (frp, Tailscale,
playit) and the capture sidecar now lives in
[GameplanePanel/audit-syslog-bridge](https://github.com/GameplanePanel/audit-syslog-bridge),
[GameplanePanel/tunnel](https://github.com/GameplanePanel/tunnel) and
[GameplanePanel/capture-sidecar](https://github.com/GameplanePanel/capture-sidecar),
checked out here as submodules at the same paths. Nothing changes for
installs: images, image names and chart values stay the same. If you build
images from a clone, run `hack/submodules.sh init` first (`make images` and
`make dev-up` also do it for you). [PR #995](https://github.com/GameplanePanel/Gameplane/pull/995)
