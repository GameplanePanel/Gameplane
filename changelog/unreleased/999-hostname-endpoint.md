# LoadBalancer servers show their hostname as their address

After upgrading, every LoadBalancer server that already has
`spec.networking.hostname` set reports that hostname as its address in
`status.endpoints` and the dashboard, in place of the load balancer's IP, once
the load balancer has published one. Check that each such hostname resolves to
its server before upgrading, or clear it on servers where it doesn't; players
would otherwise be shown a name that doesn't reach the server. The assigned
address stays in the new `status.endpoints[].serviceAddress`. Servers without a
hostname, and ClusterIP or NodePort servers, are unchanged. See
[A hostname as the server's address](../../docs/networking.md#a-hostname-as-the-servers-address).
[PR #999](https://github.com/GameplanePanel/Gameplane/pull/999)
