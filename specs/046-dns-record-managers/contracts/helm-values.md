# Contract: Helm values (spec 046)

Off by default (FR-026). With `dns.enabled=false` the chart renders nothing new and the operator builds no `DNSRecordSet`s.

```yaml
dns:
  enabled: false
  hub:
    image: {repository: ghcr.io/gameplanepanel/dns-hub, tag: ""}   # tag defaults to the chart appVersion
    resources: {}
  managers:                       # one Deployment per enabled manager (OD-7 option A)
    cloudflare: {enabled: false}
    ovh: {enabled: false}
    hetzner: {enabled: false}
    rfc2136: {enabled: false}
    zone:
      enabled: false
      serviceType: LoadBalancer   # LoadBalancer | NodePort; port 53 TCP+UDP for secondaries only
      loadBalancerIP: ""
      nodePort: null
  image: {repository: ghcr.io/gameplanepanel/dns-manager, tag: ""}
```

Rendered objects: hub Deployment + ServiceAccount (no Secret access) + ClusterRole for DNSZone/DNSRecordSet and their status; per enabled manager a Deployment, ServiceAccount, Role (`get` Secrets in the release namespace), Service on 8443; zone manager Service on 53; NetworkPolicies (hub → managers 8443 only; zone manager 53 ingress from anywhere, filtered in-process by TSIG + CIDR; managers egress to their provider API or RFC 2136 server); mTLS certificates via the existing `mtls.yaml` helpers. The operator gets a flag `--dns-enabled` from `dns.enabled`.
