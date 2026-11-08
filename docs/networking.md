# Networking & Address Pools

Gameplane exposes game servers through Kubernetes Services, with optional integration with your cluster's load-balancer address manager to assign specific public addresses or address pools to each server.

## NetworkPolicy isolation and migration

NetworkPolicies are additive. An allowance in one policy cannot be narrowed by
another policy. Enforcement also requires a CNI that implements NetworkPolicy.

Modules may set `GameTemplate.spec.sentinelIsolation: true`. The operator then
labels the game and sentinel pod templates and creates two owned policies:
`<server>-sentinel-ingress` and `<server>-sentinel-egress`. They allow a sentinel
to reach only its own server's advertised container ports, with the declared
protocol. Zero advertised ports creates no allowance. Management ports must
remain `advertise: false`. The legacy chart policies exclude opted-in sources
and destinations. Templates without the field retain the existing broad
sentinel-to-game allowance; module maintainers can adopt it after validating
their proxy traffic.

Upgrade the CRDs, operator and chart together before opting in a template.
Existing game pods roll out to acquire the isolation label; StatefulSet
selectors stay unchanged. Do not claim isolation until both game and sentinel
pods have the label and the per-server policies exist. Other administrator-added
policies can still grant wider access. Disabling chart NetworkPolicies or using
a non-enforcing CNI does not provide this boundary.

Probe and API-server narrowing is a separate installation option:

```yaml
networkPolicies:
  enabled: true
  strict: true
  kubeletCIDRs: ["192.0.2.10/32"]       # Replace with observed kubelet sources.
  probePorts:
    - {protocol: TCP, port: 25565}    # Replace with all configured probe ports.
  apiServerCIDRs: ["198.51.100.20/32"] # Replace with actual API endpoints.
```

Strict mode rejects missing/empty lists, malformed CIDRs and invalid probe
ports. It never supplies broad fallback ranges. API egress is limited to the
listed addresses on TCP 443/6443. Choose addresses based on your CNI's policy
evaluation point: service DNAT or source SNAT can change which address it sees.
Include every control-plane endpoint and kubelet source that can host a server;
update these lists when nodes or endpoints change. CNI host-traffic exceptions
may still apply.

On Cilium, CIDR rules exclude node identities by default. For a self-hosted
API server on a node, configure `policyCIDRMatchMode=nodes` before using these
plain NetworkPolicy endpoint CIDRs, or provide equivalent Cilium-specific
API-server policy. The CI fixture uses nodes mode. See
[Cilium CIDR policy semantics](https://docs.cilium.io/en/stable/security/policy/layer3/#selecting-pods-or-nodes-with-cidr-ipblock)
and validate kubelet source handling for your Cilium configuration as well.

Before enabling strict mode, inventory the actual endpoints and configured
game probes, observe their traffic in the CNI, then validate game readiness,
agent heartbeats, wake requests and API/operator agent access in a staging
namespace. The default profile retains RFC1918/link-local API egress and
unrestricted probe ports for compatibility; it does not isolate those paths.
Narrowing sentinel traffic alone cannot remove those separate grants.

The dedicated CI `network-isolation` bucket installs pinned Cilium, exercises
real allowed/denied connections, and upgrades existing legacy templates.
The default kindnet fixture only verifies policy shape.

## Address Pool Overview

When a GameServer is exposed as a LoadBalancer (`spec.networking.expose: LoadBalancer`), it can request a specific external address or address pool. The operator translates this request to your cluster's load-balancer flavor so the same CRD works whether you run MetalLB, Cilium, or no address manager at all.

The address preference is optional—it only takes effect when expose mode is LoadBalancer, and it does not affect ClusterIP, NodePort, or Hostport servers.

### Flavors

Your cluster must have one of these configured via the Helm value `operator.addressManager` (see [`install.md`](install.md)):

**MetalLB**

MetalLB manages pools of available IPs. When you set `spec.networking.addressPool: my-pool` or `spec.networking.address: 203.0.113.50`, the operator writes:
- Annotation `metallb.io/address-pool: my-pool` (if requesting a pool)
- Annotation `metallb.io/loadBalancerIPs: 203.0.113.50` (if requesting a specific address)

MetalLB's controller observes these annotations and assigns an address accordingly. See [MetalLB's documentation](https://metallb.universe.tf/) for pool setup and address allocation policies.

**Cilium**

Cilium's LB IPAM manager also organizes addresses into pools. When you set a pool or address, the operator writes:
- Label `gameplane.local/lb-pool: my-pool` (if requesting a pool)
- Annotation `lbipam.cilium.io/ips: 203.0.113.50` (if requesting a specific address)

**Critical requirement**: The label `gameplane.local/lb-pool` is a Gameplane convention, not a key Cilium recognizes natively. For the pool preference to take effect, you **must** mirror it in your `CiliumLoadBalancerIPPool` resource's `spec.serviceSelector`, otherwise the pool label selects nothing and the address assignment uses the cluster's default pool.

Example `CiliumLoadBalancerIPPool` with a Gameplane pool selector:

```yaml
apiVersion: cilium.io/v2alpha1
kind: CiliumLoadBalancerIPPool
metadata:
  name: gameservers
spec:
  cidrs:
  - cidr: 203.0.113.0/24
  serviceSelector:
    matchLabels:
      gameplane.local/lb-pool: gameservers
```

This ensures that when a GameServer requests pool `gameservers`, the label routes the address assignment to this pool.

**None** (default)

When `operator.addressManager` is `none`, the operator does not mutate the Service at all. A pool or address preference is recorded on the GameServer's `AddressAssignment` condition as **unhonored** — this prevents the server from silently coming up on a default-pool address while looking assigned. The preference is visible in the condition for troubleshooting, but not applied to the Service.

## Setting an Address Pool or Specific Address

### Via CRD (kubectl)

Edit the GameServer and set one or both of `spec.networking.addressPool` and `spec.networking.address`:

```yaml
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: my-server
spec:
  templateRef:
    name: minecraft-java
  networking:
    expose: LoadBalancer
    addressPool: public-servers    # Optional: request a specific pool
    # address: 203.0.113.50        # Optional: request a specific address
```

The pool and address names come from your address manager's own configuration. For MetalLB, use the name of an `IPAddressPool` resource; for Cilium, use the name you gave to a CiliumLoadBalancerIPPool.

### Via Dashboard

1. Create or edit a server.
2. Set **Expose mode** to `LoadBalancer`.
3. Under **Networking**, fill in **Address pool** (pool name) and/or **Address** (specific IP).
4. Save.

The dashboard translates these to the CRD fields; the operator then applies them to the Service.

## Address Preference Behavior

### When Expose Mode is LoadBalancer

- `addressPool` and `address` are honored by the address manager (MetalLB or Cilium).
- The operator records `status.conditions[type=AddressAssignment]` showing whether the assignment is `Assigned`, still `AssignmentPending`, or has failed (e.g., `ServiceNotReady`, `NoAddressManagerConfigured`).
- Once assigned, `status.endpoints` lists the address and pool name.

### When Expose Mode is Not LoadBalancer

- `addressPool` and `address` are ignored by the operator.
- `status.conditions[type=AddressAssignment]` reason becomes `IgnoredForExposureMode`, explaining that the preference only applies to LoadBalancer.
- This prevents confusion if the fields are set but not in use.

### When No Address Manager is Configured

- A preference is recorded on the `AddressAssignment` condition as `NoAddressManagerConfigured`.
- The Service gets no address-pool annotations or labels.
- The server comes up on whatever address the cluster's default policy assigns.

## Troubleshooting Address Assignment

### Reading the Condition

Check the server's AddressAssignment condition:

```bash
kubectl get gs my-server -o jsonpath='{.status.conditions[?(@.type=="AddressAssignment")]}'
```

This shows the current state and a human-readable message. Common reasons:

| Reason | Meaning | Action |
|--------|---------|--------|
| `Assigned` | Address successfully assigned from the requested pool. | No action needed; the server is reachable at the assigned address. |
| `AssignmentPending` | Address requested, but the load-balancer has not assigned one yet. | Wait; address assignment can take a few seconds. If this persists, check the load-balancer's own status. |
| `ServiceNotReady` | The LoadBalancer Service does not exist yet or is not typed LoadBalancer. | Verify expose mode is set to `LoadBalancer`. Check that the service object was created: `kubectl get svc my-server`. |
| `IgnoredForExposureMode` | Pool/address was set, but expose mode is not LoadBalancer. | Change expose mode to `LoadBalancer` to honor the preference, or remove the pool/address preference. |
| `InvalidAddress` | The requested `spec.networking.address` is not a parseable IP address. Rejected before it ever reaches the address manager; no annotation or label is written. | Correct `spec.networking.address` to a valid IP literal. |
| `NoAddressManagerConfigured` | Preference set, but the cluster has no address manager configured. | Contact your cluster admin to configure `operator.addressManager` in the Helm chart, or unset the preference. |
| `PoolNotFound` | The requested pool does not exist in the cluster. For Cilium, this is derived directly from the Service condition `cilium.io/IPAMRequestSatisfied=False` (reason `no_pool`). For MetalLB, this is a best-effort detection via direct IPAddressPool lookup, so it is not guaranteed to appear — a MetalLB that stays silent leaves the condition on `AssignmentPending` instead. | Check the pool name against the address manager's own resources (`kubectl get ipaddresspool -A` for MetalLB, `kubectl get ciliumloadbalancerippool` for Cilium), then correct `spec.networking.addressPool`. |
| `AllocationFailed` | The address manager could not allocate an address from the requested pool. For MetalLB, this covers exhausted pools, addresses outside the pool's range, and other allocation errors — the specific cause is in the condition message. For Cilium, this is derived from the Service condition `cilium.io/IPAMRequestSatisfied=False` (reason `out_of_ips`). | Free an address by deleting or re-pooling another Service, widen the pool's CIDR, or point the server at a different pool. Check the condition message for the specific cause. |
| `AddressInUse` | The requested explicit address is already taken. The operator detects this directly when another GameServer anywhere in the cluster already reports that address in its `status.endpoints` (named as `namespace/name` if the conflict is in a different namespace); it is also derived best-effort from address-manager Warning events when the clash is with a non-Gameplane Service or a host on the network. | Pick a free address, or release it on the server that holds it. The condition message names the conflicting GameServer when the operator found it itself. |

### Checking the Service Metadata

Verify the operator applied the annotations/labels:

```bash
# MetalLB
kubectl get svc my-server -o jsonpath='{.metadata.annotations}' | jq '.["metallb.io/address-pool"]'

# Cilium
kubectl get svc my-server -o jsonpath='{.metadata.labels}' | jq '.["gameplane.local/lb-pool"]'
```

If the expected key is missing, the operator may not have applied it. Check:
1. **Expose mode**: Must be `LoadBalancer`.
2. **Address manager flavor**: Verify `operator.addressManager` matches your setup (e.g., if you set `cilium`, the label should appear; if `none`, it should not).
3. **Operator logs**: `kubectl logs -n gameplane-system deployment/gameplane-operator` for errors during reconciliation.

### Address Manager Setup Issues (Cilium Only)

For Cilium, verify the load-balancer pool's serviceSelector matches the label the operator sets:

```bash
# Check the pool resource
kubectl get ciliumloadbalancerippool gameservers -o yaml | grep -A 5 serviceSelector

# Verify the GameServer service has the matching label
kubectl get svc my-server -L gameplane.local/lb-pool
```

If the label is on the Service but the address is not assigned, the pool's `spec.serviceSelector` may not include it, or there are no free addresses in the pool's CIDR.

### MetalLB Pool Issues

For MetalLB, check:

```bash
# Verify the pool exists
kubectl get ipaddresspool my-pool

# Check MetalLB's speaker log for assignment failures
kubectl logs -n metallb-system -l app=metallb -c speaker
```

Common causes: pool does not exist, pool has no free addresses, or an IP conflict exists on the network.

## Live Address Changes

When you change `spec.networking.addressPool` or `spec.networking.address` on a running server (one with connected players), the following happens:

1. The operator updates the Service's annotations (for MetalLB) or labels (for Cilium) on the next reconcile to reflect the new preference.
2. The load-balancer controller observes the updated Service metadata.
3. The load-balancer then re-evaluates the address assignment based on the new preference.

**What happens to the assigned address:**

The exact behavior depends on the load-balancer implementation and is not fully determined by the operator code alone:

- **MetalLB**: When the `metallb.io/address-pool` annotation changes, MetalLB's controller may either reassign an address from the new pool or leave the current address in place (if it belongs to the new pool). The specific behavior depends on MetalLB's allocation policy and your pool configuration. Reassignment, if it occurs, causes existing player connections using the old address to drop; new join attempts use the new address.
- **Cilium**: Similarly, when the `gameplane.local/lb-pool` label changes, Cilium's LB IPAM re-evaluates the pool selection and may reassign the address. The specific behavior is configuration-dependent.

For this reason, **changing the pool on a live server should be avoided when possible**. If you must change it, do so during low-traffic windows and notify players that a brief connectivity interruption may occur.

**Verification:**

The project's e2e test `TestAddressPool_ChangePoolOnRunningServer` verifies that the pool assignment metadata is updated on the GameServer's status; it does not verify whether the actual IP address remains stable or changes, as this is load-balancer dependent. To determine the exact behavior in your deployment, test the address-change scenario against your specific load-balancer version and pool configuration before relying on it in production.

## Related

- [`install.md`](install.md) — Helm chart values for `operator.addressManager` and cluster prerequisites.
- [`operator/api/v1alpha1/gameserver_types.go`](../operator/api/v1alpha1/gameserver_types.go) — CRD field definitions for `spec.networking.addressPool` and `spec.networking.address`.
