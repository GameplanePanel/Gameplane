# Contract: CRDs and CRD additions (spec 046)

Group `gameplane.local`, version `v1alpha1`. Types in `operator/api/v1alpha1/`; `make generate && make manifests` after every edit (CLAUDE.md rule 7). Meaning of each field: [data-model.md](../data-model.md).

## DNSZone (cluster-scoped, short name `dnsz`)

```yaml
apiVersion: gameplane.local/v1alpha1
kind: DNSZone
metadata:
  name: example-com            # DNS-label form of spec.zone
spec:
  zone: example.com            # required, RFC 1123, immutable
  kind: Provider               # Provider | Hosted, immutable
  provider: cloudflare         # required when kind=Provider: cloudflare|ovh|hetzner|rfc2136
  credentialsSecretRef: dns-example-com   # required when kind=Provider
  defaultTTL: 300              # 60..86400
  paused: false
  hosted:                      # required when kind=Hosted, forbidden otherwise (CEL)
    soa: {mname: ns1.example.com, rname: hostmaster.example.com, refresh: 3600, retry: 600, expire: 1209600, minimum: 300}
    nameservers: [ns1.secondary.example.net, ns2.secondary.example.net]
    secondaries:
      - address: 203.0.113.10/32
        tsigSecretRef: dns-tsig-example-com   # keys: name, algorithm (hmac-sha256), secret (base64)
status:
  state: Connected
  message: ""
  lastSyncTime: "2026-11-27T12:00:00Z"
  ownedRecords: 12
  serial: 2026112703           # hosted only
  journal: []                  # hosted only, last 20 serial changes
  conditions: []
```

Credential Secret keys per provider (manager reads them, nobody else):

| Provider | Keys |
|---|---|
| cloudflare | `apiToken` (Zone.DNS edit on the zone) |
| ovh | `endpoint`, `applicationKey`, `applicationSecret`, `consumerKey` |
| hetzner | `apiToken` |
| rfc2136 | `server` (host:port), `tsigName`, `tsigAlgorithm`, `tsigSecret` |

## DNSRecordSet (namespaced, short name `dnsrs`)

```yaml
apiVersion: gameplane.local/v1alpha1
kind: DNSRecordSet
metadata:
  name: mc-survival            # = GameServer name for server record sets
  namespace: games
  ownerReferences: [{kind: GameServer, name: mc-survival, controller: true}]
  finalizers: [gameplane.local/dns-withdraw]   # added by the hub
spec:
  owner: {kind: GameServer, name: mc-survival} # or {kind: Admin}
  zone: ""                                     # Admin sets only
  records:
    - {name: play.example.com, type: A, ttl: 300, values: ["198.51.100.7"]}
    - {name: _minecraft._tcp.play.example.com, type: SRV, ttl: 300, values: ["0 0 31234 play.example.com."]}
status:
  observedGeneration: 3
  records:
    - {name: play.example.com, type: A, zone: example.com, state: Published, reason: "", appliedValues: ["198.51.100.7"], lastAppliedTime: "..."}
```

Validation: `type` in A, AAAA, SRV for `owner.kind=GameServer`; A, AAAA, CNAME, MX, TXT, SRV, CAA, NS (below apex only) for `Admin`; max 64 records; SOA never allowed (the hub owns it).

## GameServer additions

```yaml
spec:
  networking:
    dns:
      publish: true                       # default true
      publicMappings:
        - {port: game, address: 203.0.113.5, publicPort: 40000}
status:
  dns:
    reason: ""
    records: [...]                        # mirror of DNSRecordSet.status.records
  conditions:
    - type: DNSPublished
```

## GameTemplate addition

```yaml
spec:
  srv:
    - {service: minecraft, protocol: tcp, port: game, priority: 0, weight: 0}
```

CEL: `port` must name an entry of `spec.ports` with `advertise: true`; `(service, protocol)` unique; at most 8 entries.
