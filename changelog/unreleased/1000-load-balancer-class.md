# Choose a server's load-balancer implementation

A LoadBalancer server can set `spec.networking.loadBalancerClass` to pick
which load-balancer implementation handles its Service, for a cluster that
runs more than one. Changing it replaces the Service, since Kubernetes
doesn't allow changing a Service's class in place.
