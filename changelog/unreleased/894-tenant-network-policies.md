# Tenant network isolation

The opt-in `networkPolicies.tenantIsolation` profile restricts traffic between game
workloads in managed namespaces. The monitoring guide now distinguishes per-server probe
ingress from the legacy kubelet rule. [PR #894](https://github.com/GameplanePanel/Gameplane/pull/894)
