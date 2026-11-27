# Contract: hub to manager protocol (spec 046)

HTTP/JSON over mTLS, port 8443, only the hub's client certificate is accepted (research R9). One manager process serves one provider type and every zone of that type in its cluster. All calls name the zone by its `DNSZone` name; the manager loads that zone's credentials itself from the Secret the `DNSZone` references (FR-012). No response ever contains credential values.

Records on the wire: `{"name": "play.example.com", "type": "A", "ttl": 300, "values": ["198.51.100.7"]}` (FQDN without trailing dot; SRV values `priority weight port target.`).

| Call | Request | Response | Notes |
|---|---|---|---|
| `GET /v1/health` | | `{"provider": "cloudflare", "version": "x.y.z"}` | hub marks zones `ManagerUnreachable` after 2 min of failures |
| `POST /v1/zones/{zone}/check` | | `{"ok": true}` or error | write test: create then delete `_gameplane-check.<zone>` TXT |
| `GET /v1/zones/{zone}/records?name=&type=` | | `{"records": [...]}` | current records for a name and type, owned or not (FR-010) |
| `GET /v1/zones/{zone}/owned?cluster=` | | `{"records": [...], "markers": [...]}` | records whose `_gameplane-owner` marker names this cluster |
| `PUT /v1/zones/{zone}/records` | `{"record": {...}, "marker": "v=1;cluster=..;ref=..;type=.."}` | `{"applied": {...}}` | create or replace one name+type, plus its marker; idempotent (FR-014) |
| `DELETE /v1/zones/{zone}/records` | `{"name": "...", "type": "...", "marker": "..."}` | `204` | manager refuses unless the marker at the provider equals the one sent (FR-007) |

Zone manager only (kind=Hosted):

| Call | Request | Response | Notes |
|---|---|---|---|
| `PUT /v1/hosted/{zone}` | full zone content `{serial, soa, ns, records[]}` | `{"serial": n}` | hub sends the whole zone after each batch; manager swaps it atomically and sends NOTIFY |

Errors: JSON `{"code": "...", "message": "..."}` with codes `credentials_rejected`, `zone_not_found`, `no_write_permission`, `rate_limited` (with `retryAfterSeconds`), `not_owned`, `provider_error`. The hub maps them to `DNSZone.status.state` and record `reason`.
