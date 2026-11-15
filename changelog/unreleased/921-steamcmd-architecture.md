# SteamCMD image and worker architecture

The SteamCMD base build targets `linux/amd64`. Game templates can require a worker architecture with `spec.requiredNodeArchitecture`. On mixed clusters, set the `kubernetes.io/arch: amd64` node selector on affected GameServers that still use older templates. [Affected games and upgrade steps](../../containers/README.md#platform-support)
