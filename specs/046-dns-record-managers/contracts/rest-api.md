# Contract: panel API routes (spec 046)

Mounted in `api/cmd/main.go`, all behind the existing admin role check (OD-3), all with `?cluster=` like other per-cluster resources, all audited (FR-025, `api/internal/audit`). The API only creates and edits `DNSZone`, credential Secrets and Admin `DNSRecordSet`s; it never calls a manager.

| Method + path | Body / result | Notes |
|---|---|---|
| `GET /api/v1/dns/zones` | `[{name, zone, kind, provider, state, message, lastSyncTime, ownedRecords, credentialsSet}]` | `credentialsSet` is a bool; secret values are never returned |
| `POST /api/v1/dns/zones` | `{zone, kind, provider, credentials{...}, defaultTTL, hosted{...}, clusters[]}` | creates the Secret then the DNSZone in each listed cluster; 409 if the zone exists there; a Hosted zone can exist in one cluster only |
| `GET /api/v1/dns/zones/{name}` | zone + status | |
| `PATCH /api/v1/dns/zones/{name}` | `{defaultTTL?, credentials?, hosted?, paused?}` | `credentials` replaces the Secret data |
| `POST /api/v1/dns/zones/{name}/test` | `{state, message}` | sets an annotation the hub reacts to by re-running the check |
| `DELETE /api/v1/dns/zones/{name}?keep_records=false` | | `keep_records=true` sets `paused` first so the hub leaves records |
| `GET /api/v1/dns/zones/{name}/records` | `[{name, type, ttl, values, owner, state}]` | hosted zones: every record; provider zones: Gameplane-owned only |
| `POST /api/v1/dns/zones/{name}/records` | `{name, type, ttl, values}` | hosted zones only (OD-5); written to the zone's Admin DNSRecordSet |
| `PUT` / `DELETE /api/v1/dns/zones/{name}/records/{id}` | | 403 `owned_by_gameplane` on a server-owned record |
| `POST /api/v1/dns/zones/{name}/import` | zone file text, 1 MiB max | hosted zones, only while the zone has no Admin records yet (one-time import); SOA and apex NS lines ignored; returns parsed count and rejected lines |

Server records need no new route: `GET` of a GameServer already returns `status.dns`. Turning publication off is the existing server update with `spec.networking.dns.publish=false` (permission: whoever may edit the server's networking today).
