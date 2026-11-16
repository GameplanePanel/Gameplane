# LoadBalancer servers show their hostname as their address

A LoadBalancer server with `spec.networking.hostname` set now reports that
hostname as its address in `status.endpoints` and the dashboard, once the
load balancer has published an address, in place of the load balancer's IP.
Players see the DNS name they should use. Servers without a hostname, and
ClusterIP or NodePort servers, are unchanged.
