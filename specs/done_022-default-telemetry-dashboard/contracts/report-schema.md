# Contract: Telemetry report schema

**Owner**: `telemetryschema/` (research R1). The API builds reports with this package and the receiver decodes them with it.
**Requirements**: FR-010, FR-011, FR-013, FR-016, FR-035–FR-038.

## Body

The body is one JSON object. Keys are case-sensitive. Duplicate keys, unknown keys, `null` values and trailing content are all rejected. The body may be at most 16 KiB, which is unchanged.

### Basic tier (unchanged)

```json
{ "version": "0.3.0", "servers": 3, "templates": 7 }
```

### Basic and extended tiers

```json
{
  "version": "0.3.0",
  "servers": 3,
  "templates": 7,
  "ext": {
    "schema": 1,
    "installId": "3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10",
    "env": { "k8s": "1.31", "distro": "k3s", "arch": ["amd64"], "nodes": "1" },
    "games": { "official": { "minecraft-java": 2, "terraria": 1 }, "custom": 0 },
    "features": {
      "wakeOnConnect": true, "tunnels": ["playit"], "capture": false,
      "backups": true, "sso": false, "auditForwarding": false,
      "clusters": "1", "db": "sqlite", "language": "en"
    },
    "key": "pV0mY3Qn8wK2b7cXlR4sTt9uJ1eHaZ6dFgQmNoP5rSk",
    "sentAt": "2026-10-06T09:12:44Z"
  }
}
```

Sent with the header:

```text
Gameplane-Telemetry-Signature: ed25519=<base64url Ed25519 signature over the exact body bytes>
```

## Field rules

| Path | Type | Allowed values | When the value doesn't fit |
|---|---|---|---|
| `version` | string | `^[A-Za-z0-9][A-Za-z0-9._+-]{0,31}$` | The receiver buckets it as `invalid`. |
| `servers`, `templates` | integer | ≥ 0 | 400 |
| `ext` | object | optional | — |
| `ext.schema` | integer | `1` or `2` (`2` adds `ext.env.deployment`, FR-045) | A higher value gets 400, which triggers the API's basic-only fallback. |
| `ext.installId` | string | lowercase UUIDv4 | Once `ext` passes the structural checks, the receiver ignores it and counts the report as basic. A structurally broken `ext` is a 400 whatever the ID. |
| `ext.env.k8s` | string | `1.<minor>`, where minor is 1–3 digits | `other` |
| `ext.env.distro` | string | `k3s` `rke2` `k0s` `eks` `gke` `aks` `openshift` `microk8s` `minikube` `kind` `doks` `talos` `other` | `other` |
| `ext.env.arch` | string[] | non-empty, sorted, unique, drawn from `amd64` `arm64` `arm` `ppc64le` `s390x` `riscv64` `other` | Unknown members become `other`. An empty array gets 400. |
| `ext.env.nodes` | string | `1` `2-3` `4-10` `11-50` `51+` | `other` |
| `ext.env.k8s`, `ext.env.distro` in schema 2 | string[] | non-empty, sorted, unique; members follow the schema 1 rules above, one per value across the reachable managed clusters (OD-15) | Unknown members become `other`; a panel with no reachable cluster sends `["other"]`. An empty array gets 400. |
| `ext.env.deployment` | string | `cluster` `standalone`. Required in schema 2, refused (unknown key, 400) in schema 1. | `other` |
| `ext.games.official` | object | keys are module names from the embedded catalog, values are integers ≥ 1 | Unknown keys are added to `custom`. A non-positive value gets 400. |
| `ext.games.custom` | integer | ≥ 0 | 400 |
| `ext.features.wakeOnConnect`, `capture`, `backups`, `sso`, `auditForwarding` | boolean | — | 400 |
| `ext.features.tunnels` | string[] | sorted, unique, drawn from `frp` `tailscale` `playit` `other` (may be empty) | Unknown members become `other`. |
| `ext.features.clusters` | string | `1` `2-3` `4-10` `11+` | `other` |
| `ext.features.db` | string | `sqlite` `postgres` | `other` |
| `ext.features.language` | string | `en` (spec 019 extends this list) | `other` |
| `ext.key` | string | canonical base64url (unpadded, no line breaks) encoding of a 32-byte Ed25519 public key | 400 |
| `ext.sentAt` | string | RFC 3339 UTC | 400 if it doesn't parse. 403 `stale` if it is outside `[now − 36h, now + 1h]`. |

## Signature and claim (FR-035–FR-038, research R20)

**Key derivation** (install side, `telemetryschema.DeriveKey`):

```text
seed = HKDF-SHA256(ikm = signing_secret, salt = installId,
                   info = "gameplane-telemetry-signing-v1", L = 32)
priv = ed25519.NewKeyFromSeed(seed)
```

- `signing_secret` is 32 random bytes created once on the install. It never leaves the install.
- The key is unique to the ID, so a new ID always means a new key.

**Signing**: `Gameplane-Telemetry-Signature: ed25519=<base64url(ed25519.Sign(priv, body))>`.
- `body` is the exact bytes POSTed.
- The header is required whenever `ext` is present, and absent on basic-only reports.

**Verification** (receiver, `telemetryschema.Verify`, then claim lookup). Checks run in this order, and the first failure wins:

| Check | Failure |
|---|---|
| Header present and well-formed, and the signature verifies with `ext.key` over the body | 403 `{"error":"bad_signature"}` |
| `sentAt` within `[now − 36h, now + 1h]` | 403 `{"error":"stale"}` |
| The ID is unclaimed (it gets claimed: `key_fp = SHA-256(key)`), or it is claimed with the same `key_fp` | 409 `{"error":"id_claimed"}` |
| `sentAt` is later than the ID's last accepted `sentAt` | 403 `{"error":"replay"}` |

A refused report changes no stored data.

## Band functions (exported by `telemetryschema`)

| Function | Input → band |
|---|---|
| `NodeBand(n)` | 1 → `1`; 2–3 → `2-3`; 4–10 → `4-10`; 11–50 → `11-50`; over 50 → `51+` |
| `ClusterBand(n)` | n counts the local cluster plus registered clusters. 1 → `1`; 2–3 → `2-3`; 4–10 → `4-10`; over 10 → `11+` |
| `FleetBands` | `0,1,2,5,10,25,50,100,250,+Inf`, the same buckets as today's Prometheus histograms |

## Privacy guarantees enforced by the schema

- No field can hold free text. Every string is matched against a pattern or an enumeration, or is the install ID, the public key, or the send time.
- Module names can come only from the embedded catalog.
- Player counts, names, namespaces, hostnames, addresses, and custom module names cannot be represented in the schema.

## Catalog

`telemetryschema/catalog.txt` lists one official module name per line, generated from `charts/gameplane/values.yaml` → `defaultModuleSource.oci.modules`. `hack/check-telemetry-catalog.sh` fails CI when the two differ (research R15).

## Compatibility

| Install → receiver | Result |
|---|---|
| Old install (basic only) → new receiver | Accepted, counted as basic. |
| New install with `ext` → a receiver released before this feature (≤ v0.3.0) | 400. The install re-POSTs basic only, which is accepted, and suppresses `ext` for that endpoint for 7 days. |
| New install with `ext.schema` N → receiver that supports a lower schema | 400. Same fallback as the row above. |
| Install on schema 1 (no `deployment`) → receiver that supports schema 2 | Accepted. The deployment breakdown counts it as `other` (OD-14). |
| Install whose ID is claimed by another key → new receiver | 409. The install replaces its ID, re-signs and re-POSTs once (FR-037). |
| Bad signature, stale send time or replay → new receiver | 403. Counts as a failed attempt with normal backoff. No fallback and no ID rotation. |
