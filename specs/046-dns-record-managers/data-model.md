# Data model: spec 046

All new state lives in Kubernetes resources (group `gameplane.local/v1alpha1`). Field-level schema: [contracts/crds.md](contracts/crds.md).

## DNSZone (cluster-scoped)

One zone Gameplane may publish into, in this cluster.

| Field | Notes |
|---|---|
| `spec.zone` | FQDN without trailing dot, unique across DNSZones in the cluster |
| `spec.kind` | `Provider` or `Hosted` |
| `spec.provider` | `cloudflare`, `ovh`, `hetzner`, `rfc2136` (Kind=Provider) |
| `spec.credentialsSecretRef` | name of a Secret in the release namespace labelled `gameplane.io/dns-credentials=<provider>` (Kind=Provider) |
| `spec.defaultTTL` | seconds, default 300, 60 to 86400 |
| `spec.hosted` | Kind=Hosted: `soa` (mname, rname, refresh, retry, expire, minimum), `nameservers[]` (the secondaries' names, become NS records), `secondaries[]` (`address` CIDR, `tsigSecretRef`) |
| `spec.paused` | stop all changes, keep records ("keep records" disconnect option) |
| `status.state` | `Connected`, `ManagerUnreachable`, `CredentialsRejected`, `ZoneNotFound`, `NoWritePermission`, `Pending` |
| `status.message`, `status.lastSyncTime`, `status.ownedRecords` | FR-023 |
| `status.serial`, `status.journal[]` | Kind=Hosted: current serial and the last 20 changes for IXFR |

Lifecycle: created by the API (admin) → hub asks the manager to check access (write test: create and delete `_gameplane-check.<zone>` TXT) → `Connected`. Deletion has a finalizer: the hub removes every owned record first unless `spec.paused` was set with "keep records".

## DNSRecordSet (namespaced)

The desired records of one owner, and what happened to them.

| Field | Notes |
|---|---|
| `spec.owner` | `{kind: GameServer, name}` or `{kind: Admin}` (hosted-zone records the admin added, OD-5) |
| `spec.zone` | set for Admin record sets only; GameServer sets are routed by the hub (longest suffix) |
| `spec.records[]` | `name` (FQDN), `type` (A, AAAA, SRV for servers; any type for Admin), `ttl`, `values[]`; SRV values as `priority weight port target` |
| `status.records[]` | per record: `state` (`Published`, `Present`, `Pending`, `NotPublished`), `reason`, `zone`, `appliedValues[]`, `lastAppliedTime` |
| `status.observedGeneration` | |

GameServer record sets are created, updated and deleted only by the operator (owner reference to the GameServer). The hub holds a finalizer so records are withdrawn before the object disappears (FR-005). Admin record sets are created by the API and live in the release namespace.

## GameServer additions

- `spec.networking.dns.publish` (bool pointer, default true; FR-006).
- `spec.networking.dns.publicMappings[]` (`port`, `address`, `publicPort`; research R8).
- `status.dns.records[]`: copy of the matching `DNSRecordSet.status.records` for the dashboard (FR-024), plus `status.dns.reason` when nothing is sent (for example "Not published: no connected zone for play.other.net", "Not published: public port unknown").
- Condition `DNSPublished` (True when every desired record is Published or Present).

## GameTemplate addition

- `spec.srv[]`: `service` (RFC 6335 service name, 1-15 chars, letters, digits, hyphens), `protocol` (`tcp`/`udp`), `port` (name of an advertised `spec.ports[]` entry), `priority` (0-65535, default 0), `weight` (0-65535, default 0). Validated by CEL in the CRD and by `gp-module validate` (FR-020).

## State transitions of one record

```text
(desired) -> Pending -> Published
                    \-> Present        (unowned A/AAAA already matches, FR-007 edge case)
                    \-> NotPublished   (conflict, no zone, unknown port/address, shared-name rule)
Published -> Pending (value changed) -> Published
Published -> withdrawn (removed from spec or owner deleted; marker removed too)
any -> Pending (manager unreachable / provider rejects; queued, nothing deleted, FR-013)
```

## Relationships

- GameServer 1 → 0..1 DNSRecordSet (owner reference).
- DNSRecordSet records → DNSZone by longest suffix of `name` (hub).
- DNSZone (Provider) → 1 credentials Secret; DNSZone (Hosted) → n TSIG Secrets.
- Hosted zone content = SOA + NS from `DNSZone.spec.hosted` + every DNSRecordSet record routed to it.
