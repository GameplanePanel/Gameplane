# Operator and chart security hardening

A server exposed with `Hostport` can no longer bind a port below 1024 or a port the node itself uses (Kubernetes API server, kubelet, etcd, flannel); it now shows Failed with the reason, and NodePort or LoadBalancer still work for such a game. Restore, forget, frp and Tailscale tunnel pods no longer mount a Kubernetes API token, the chart's CRD install hook can only change Gameplane's own CRDs, and `sftp.apiURL` for a remote SFTP endpoint must be an `https://` URL. [PR #1092](https://github.com/GameplanePanel/Gameplane/pull/1092)
