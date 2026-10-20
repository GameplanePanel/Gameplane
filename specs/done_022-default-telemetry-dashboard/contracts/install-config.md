# Contract: install-time configuration (Helm values, API flags and environment)

**Requirements**: FR-001, FR-002, FR-005, FR-006, FR-009, FR-033; SC-004. **Research**: R3, R12, R13, R15, R17.

## Helm values (`charts/gameplane/values.yaml`)

```yaml
api:
  telemetry:
    # NEW. false = hard off: the API never sends telemetry, whatever the admin
    # toggles say. This is the single setting for air-gapped and
    # privacy-sensitive clusters (FR-005, SC-004).
    enabled: true
    # Empty = the project's default provider (https://telemetry.gameplane.net/ingest,
    # compiled into the API; OD-1 ruled 2026-10-07). Set a URL to send reports to your
    # own receiver instead (FR-005, FR-006).
    endpoint: ""
    authSecretRef: { name: "", key: token }   # unchanged
    # NEW, testing only. Spacing between reports. Don't change in production.
    interval: 24h
    receiver:
      enabled: false        # unchanged; auto-wires the API when endpoint is empty
      replicas: 1           # must stay 1 while persistence is enabled (the template fails otherwise)
      resources: {}
      persistence:          # NEW (FR-020)
        enabled: true
        size: 1Gi
        storageClassName: ""
      dashboard:            # NEW (FR-022, FR-028)
        tokenSecretRef: { name: "", key: token }   # empty name = no dashboard listener
        # Extra NetworkPolicy peers allowed to reach the dashboard port
        # (port-forward works without any).
        ingressFrom: []
      publicSummary:
        enabled: false      # NEW (FR-031)
      pepperSecretRef: { name: "", key: pepper }   # NEW (R5), optional
      retentionDays: 730          # NEW (OD-3: 24 months)
      activityExpiryDays: 90      # NEW (OD-4)
      ingestSourceDailyLimit: 20  # NEW (R6)
      trustedProxyCIDRs: []       # NEW (R6)
      ingestPow:                  # NEW (FR-039, R21, OD-5)
        enabled: false            # the project's provider turns it on (runbook)
        targetPerMinute: 60
        minBits: 0
        maxBits: 22
```

All new keys are read with `hasKey` or `dig` guards (the F-214 precedent), so `helm upgrade --reuse-values` from beta.8 needs no new values and adds no storage: an absent `persistence` block counts as disabled (an `emptyDir`), and other absent keys fall back to the defaults above. `retentionDays` below 365 or `activityExpiryDays` below 31 fails rendering, so a bad value never reaches a crash-looping pod. Likewise, `ingestPow.targetPerMinute` below 1, or `ingestPow.maxBits` below `minBits` or above 26, fails rendering.

## How values render

| Values | API arguments and environment | Destination kind |
|---|---|---|
| `enabled: false` | `--telemetry-disabled` | `disabled` |
| `endpoint: ""`, `receiver.enabled: true` | `--telemetry-endpoint=http://gameplane-telemetry-receiver.<ns>.svc:8080/ingest`, `GAMEPLANE_TELEMETRY_BUNDLED=true` | `bundled` |
| `endpoint: "https://…"` | `--telemetry-endpoint=https://…` | `custom` |
| `endpoint: ""`, `receiver.enabled: false` | *(no endpoint argument)* | `default` (OD-1 ruled 2026-10-07) |
| `defaultModuleSource.enabled: true` | `--official-module-source=<defaultModuleSource.name>` | — |
| `interval` set | `GAMEPLANE_TELEMETRY_INTERVAL=<interval>` | — |

`enabled: false` takes precedence over both the endpoint and the receiver. The receiver itself is still deployed if `receiver.enabled` is true.

## API flags and environment (`api/cmd/main.go`)

| Flag | Env | Default | Change |
|---|---|---|---|
| `--telemetry-endpoint` | `GAMEPLANE_TELEMETRY_ENDPOINT` | `""` | **Semantics change**: empty now means "use `telemetry.DefaultEndpoint`" (https://telemetry.gameplane.net/ingest, OD-1 ruled 2026-10-07) rather than "off". |
| `--telemetry-disabled` | `GAMEPLANE_TELEMETRY_DISABLED` | `false` | New. |
| — | `GAMEPLANE_TELEMETRY_BUNDLED` | `false` | New. Set only by the chart. |
| `--telemetry-interval` | `GAMEPLANE_TELEMETRY_INTERVAL` | `24h` | New. Minimum `1m`; lower values are a startup error. |
| `--official-module-source` | `GAMEPLANE_OFFICIAL_MODULE_SOURCE` | `""` | New. Empty means every module counts as `custom`. |
| — | `GAMEPLANE_TELEMETRY_AUTH` | `""` | Unchanged. |

`telemetry.DefaultEndpoint` (`api/internal/telemetry`) is the only place the project URL lives (FR-002). It must use `https`; a non-https value fails startup (FR-009).

## Receiver chart rendering (`templates/telemetry-receiver.yaml`)

- **Workload.** The Deployment keeps `replicas: {{ $r.replicas }}`, adds `strategy: Recreate`, and fails rendering when `replicas > 1 && persistence.enabled`.
- **Volumes.** When `persistence.enabled`, a PVC `gameplane-telemetry-receiver-data` is mounted at `/data`. Otherwise an `emptyDir` is mounted there and the template adds a NOTES warning. In both cases the root filesystem stays read-only.
- **Environment.** `DATA_DIR=/data`, `DASHBOARD_TOKEN` and `ID_PEPPER` from their `secretKeyRef`s when named, `PUBLIC_SUMMARY`, `RETENTION_DAYS`, `ACTIVITY_EXPIRY_DAYS`, `INGEST_SOURCE_DAILY_LIMIT`, `TRUSTED_PROXY_CIDRS`, and `INGEST_POW`, `INGEST_POW_TARGET_PER_MIN`, `INGEST_POW_MIN_BITS` and `INGEST_POW_MAX_BITS`.
- **Service.** Adds port `dashboard` (8081 → 8081) when the dashboard token is named.
- **NetworkPolicy.** Port 8080 admits only the API pod. Port 8081 admits `dashboard.ingressFrom` peers, plus the Prometheus namespace when `serviceMonitors.scrapeNamespaceSelector` is set; that rule moves from 8080 to 8081, because `/metrics` now lives there. With no peers configured, port-forward still works.
- **ServiceMonitor** (`templates/servicemonitors.yaml`). It renders only when `dashboard.tokenSecretRef.name` is set, and scrapes port `dashboard`, path `/metrics`, with `bearerTokenSecret` pointing at that Secret. When `serviceMonitors.enabled` and the receiver are on without a token, NOTES prints a warning and no ServiceMonitor is rendered.

## Test and dev installs (research R17)

`deploy/kind/e2e.sh` and `deploy/kind/up.sh` must pass `--set api.telemetry.receiver.enabled=true`, so that no test or dev install ever reaches the project provider. The CI `helm template` checks (`.github/workflows/ci.yaml:775-804`) gain assertions for:

- the `disabled`, `bundled` and `custom` renderings above
- `--reuse-values` compatibility with beta.8
