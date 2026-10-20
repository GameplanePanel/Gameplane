# telemetryschema — Specification

**Status:** pre-v1 (spec 022)
**Module / package:** `github.com/GameplanePanel/gameplane/telemetryschema`
**Dependencies:** stdlib only (Go 1.26+)

## Purpose

Shared telemetry report contract used by `api` (reporter and preview) and `telemetry-receiver` (ingest). Both sides import this one package, so the categories the API sends and the receiver accepts cannot drift, and the preview an admin sees is built from the same types as the payload.

## Responsibilities

1. Define the report types: the basic tier (`version`, `servers`, `templates`) and the optional extended tier (`ext`).
2. Hold every enumeration, the band functions and the version and Kubernetes-minor patterns.
3. Decode and validate report bodies strictly, folding out-of-set values to `other`.
4. Encode reports deterministically.
5. Hold the embedded official module catalog.
6. Derive signing keys, sign report bodies and verify signatures.
7. Define the proof-of-work header, and check and solve report-ingestion challenges, so the receiver and the reporter share one definition.

## Non-goals / boundaries

- Does **not** talk to the network, the filesystem (beyond the embedded catalog) or Kubernetes. All of it is pure and unit-testable.
- Does **not** enforce the 16 KiB body limit, the `sentAt` freshness window, install-ID claims, replay protection or rate limits. Those are the receiver's checks.
- Does **not** store the signing secret or the signing key. The API stores the secret and recomputes the key on every send.
- Does **not** decide what an install reports. The API collects the values and builds a `Report`.
- Does **not** issue challenges, hold the receiver's MAC key, remember used challenges or choose a difficulty. Those are the receiver's; this package only checks and solves a challenge it is given.

## Directory & package layout

```
telemetryschema/
├── doc.go            # package comment
├── report.go         # Report, Extended, Env, Games, Features; Encode
├── enums.go          # enumerations, bands, VersionRE, K8sMinorRE, SanitizeEnum
├── decode.go         # Decode, DecodeInfo, ErrInvalidPayload, ErrUnsupportedSchema
├── catalog.go        # IsOfficial, Catalog (embeds catalog.txt)
├── catalog.txt       # official module names, one per line (generated from the chart)
├── sign.go           # NewSecret, DeriveKey, PublicKeyString, Sign, Verify, KeyFingerprint
├── pow.go            # PoWHeader, MaxPoWBits, PoWOK, SolvePoW, FormatPoW, ParsePoW
├── *_test.go         # unit tests
├── go.mod            # module, stdlib-only, no requires
└── .testcoverage.yml # 90% coverage gate
```

## External interface / contracts

The wire contract is [`specs/done_022-default-telemetry-dashboard/contracts/report-schema.md`](../specs/done_022-default-telemetry-dashboard/contracts/report-schema.md); the rules below are what this package enforces.

### Types

| Type | Fields |
|---|---|
| `Report` | `Version string`, `Servers int`, `Templates int`, `Ext *Extended` (nil on a basic-only report) |
| `Extended` | `Schema int`, `InstallID string`, `Env Env`, `Games Games`, `Features Features`, `Key string`, `SentAt time.Time` |
| `Env` | `K8s []string`, `Distro []string`, `Arch []string`, `Nodes string`, `Deployment string` (schema 2; `other` on a schema 1 report) |
| `Games` | `Official map[string]int`, `Custom int` |
| `Features` | `WakeOnConnect`, `Capture`, `Backups`, `SSO`, `AuditForwarding bool`; `Tunnels []string`; `Clusters`, `DB`, `Language string` |

JSON tags are the camelCase names in the contract (`installId`, `wakeOnConnect`, `auditForwarding`, `sentAt`, and so on).

### Enumerations

| Name | Members |
|---|---|
| `Distros` | `k3s` `rke2` `k0s` `eks` `gke` `aks` `openshift` `microk8s` `minikube` `kind` `doks` `talos` `other` |
| `Arches` | `amd64` `arm64` `arm` `ppc64le` `s390x` `riscv64` `other` |
| `Tunnels` | `frp` `tailscale` `playit` `other` |
| `DBs` | `sqlite` `postgres` |
| `Languages` | `en` |
| `NodeBands` | `1` `2-3` `4-10` `11-50` `51+` |
| `ClusterBands` | `1` `2-3` `4-10` `11+` |

`SanitizeEnum(set, v)` returns `v` when it is in `set` and `other` otherwise. `K8sMinorRE` is `^1\.[0-9]{1,3}$`; any other `env.k8s` value becomes `other`. `VersionRE` is `^[A-Za-z0-9][A-Za-z0-9._+-]{0,31}$`.

`Deployments` is `cluster`, `standalone` (FR-045).

### Bands

| Function | Input to band |
|---|---|
| `NodeBand(n)` | n ≤ 1 → `1`; 2–3 → `2-3`; 4–10 → `4-10`; 11–50 → `11-50`; over 50 → `51+` |
| `ClusterBand(n)` | n ≤ 1 → `1`; 2–3 → `2-3`; 4–10 → `4-10`; over 10 → `11+` |
| `FleetBands` | `0,1,2,5,10,25,50,100,250` (the `+Inf` bucket is implicit) |

### Decode

`Decode(body) (Report, DecodeInfo, error)` accepts exactly one JSON object and nothing after it except whitespace.

- **Rejected** (the error wraps `ErrInvalidPayload`), at every nesting level: unknown keys, duplicate keys, `null` values, wrongly typed values, keys with the wrong case, and missing required keys. `version`, `servers` and `templates` are required; `ext` is optional; every `ext` field is required.
- **Range rules:** `servers`, `templates` and `games.custom` are at least 0; `games.official` counts are at least 1; `env.arch`, `env.k8s` and `env.distro` are non-empty; `ext.schema` is 1 or 2; `ext.key` is unpadded base64url of exactly 32 bytes; `ext.sentAt` parses as RFC 3339 and is returned in UTC.
- **Folded, not rejected:** out-of-set enumeration values become `other`. `env.arch`, `env.k8s`, `env.distro` and `features.tunnels` are returned sorted, de-duplicated after folding, and never nil.
- **Catalog:** `games.official` keys outside the embedded catalog add their count to `games.custom`; the returned `Official` map holds catalog names only.
- **Schema gate:** `ext.schema` above 2 returns `ErrUnsupportedSchema`, checked before the other `ext` rules. It wraps `ErrInvalidPayload`, so one `errors.Is` check covers both.
- **Schema 1 and 2:** schema 2 requires `env.deployment` and sends `env.k8s` and `env.distro` as arrays (OD-13, OD-15). Schema 1 refuses `env.deployment` as an unknown key and sends `env.k8s` and `env.distro` as strings; they decode as one-value sets, and `Deployment` is `other` (OD-14).
- **Install ID:** when `ext.installId` is not a lowercase UUIDv4, the rest of `ext` is validated, then `DecodeInfo.ExtDropped` is set and `Ext` is nil. The basic report stays valid.

`Encode(Report)` is deterministic: struct field order is fixed, map keys and arrays are sorted, nil arrays and maps are written as `[]` and `{}`, and `sentAt` is written in UTC at second precision. `Decode(Encode(r))` returns `r` for a valid schema 2 report. Schema 1 writes `env.k8s` and `env.distro` as single values and no deployment.

### Catalog

`catalog.txt` lists the official module names, one per line, in the order of `charts/gameplane/values.yaml` → `defaultModuleSource.oci.modules`. `hack/check-telemetry-catalog.sh` (part of `make lint`) fails when the two differ. `IsOfficial(name)` is an exact, case-sensitive match; `Catalog()` returns a copy.

### Signatures

- `SignatureHeader` is `Gameplane-Telemetry-Signature`. It is required when the report has `ext` and absent otherwise.
- `NewSecret()` returns 32 random bytes (`SecretSize`). The API keeps it for the install's lifetime.
- `DeriveKey(secret, installID)` derives `seed = HKDF-SHA256(ikm = secret, salt = installID, info = "gameplane-telemetry-signing-v1", L = 32)` and returns `ed25519.NewKeyFromSeed(seed)`. The key is unique to the ID, so a new ID always means an unlinkable new key.
- `PublicKeyString(priv)` is the unpadded base64url public key carried in `ext.key`.
- `Sign(priv, body)` returns `ed25519=<base64url signature over the exact body bytes>`.
- `Verify(header, publicKey, body)` returns nil, or an error wrapping `ErrBadSignature` for a missing or malformed header, a malformed key, or a signature that does not match.
- `KeyFingerprint(publicKey)` is the hex SHA-256 of the raw public key (the receiver's claim, `key_fp`), or `""` for a malformed key.

### Proof-of-work

The wire contract is research R21 and [`contracts/receiver-http.md`](../specs/done_022-default-telemetry-dashboard/contracts/receiver-http.md). When a receiver requires proof-of-work, every report carries `Gameplane-Telemetry-PoW: <challenge>:<nonce>`.

| Name | Behavior |
|---|---|
| `PoWHeader` | `Gameplane-Telemetry-PoW`. |
| `MaxPoWBits` | 26 (OD-5): the hardest challenge an install solves. |
| `PoWOK(challenge, nonce, bits)` | `SHA-256(challenge + ":" + decimal nonce)` has at least `bits` leading zero bits. 0 or fewer bits is always satisfied. |
| `SolvePoW(ctx, challenge, bits)` | Smallest nonce for which `PoWOK` holds. One goroutine; checks `ctx` every 4,096 attempts and returns an error wrapping its error. Errors, wrapping `ErrPoWDifficulty`, when `bits` is below 0 or above `MaxPoWBits`. |
| `FormatPoW(challenge, nonce)` | The header value `<challenge>:<nonce>`. |
| `ParsePoW(header)` | Splits at the last `:`. The nonce must be plain decimal (digits only, no leading zeros, within `uint64`); the challenge must be 1 to `MaxChallengeLen` (256) bytes. Every failure wraps `ErrBadPoW`. |

### Compatibility

| Install to receiver | Result |
|---|---|
| Old install (basic only) to a new receiver | Accepted, counted as basic. |
| New install with `ext` to a receiver released before this feature | 400. The install re-POSTs basic only and suppresses `ext` for that endpoint for 7 days. |
| `ext.schema` above what the receiver supports | 400 (`ErrUnsupportedSchema`). Same fallback as the row above. |
| ID already claimed by another key | 409. The install replaces its ID, re-signs and re-POSTs once. |
| Bad signature, stale send time or replay | 403. A failed attempt with normal backoff; no fallback and no ID rotation. |

A module added to the chart catalog after a receiver was built counts as `custom` on that receiver until it is updated. A new enumeration member on an older receiver folds into `other`.

## Key invariants

- No field can hold free text: every string is matched against a pattern or an enumeration, or is the install ID, the public key or the send time.
- Every category is bounded: module names come only from the embedded catalog, and enumeration values only from their sets (or `other`).
- Player counts, names, namespaces, hostnames, addresses and custom module names cannot be represented.
- Both sides validate with the same code, so a report the API builds from this package always decodes on a receiver built from the same version.
- Stdlib only; no network or filesystem access.

## Testing & coverage

Unit tests only (`decode_test.go`, `enums_test.go`, `catalog_test.go`, `sign_test.go`, `pow_test.go`). They cover every reject case of the receiver's original decoder, every `ext` rule, the band boundaries, catalog membership, a fixed key-derivation vector, the sign and verify round trip, single-byte tampering, malformed headers and keys, and different IDs deriving different keys. `pow_test.go` pins fixed hash vectors (the hash input, the bit count and the smallest-nonce search order), the solver's difficulty and context errors, and every malformed-header case of `ParsePoW`. Coverage gate: 90% total, set in `.testcoverage.yml` and enforced by `make cover-go-check`.
