# Quickstart: validating feature 022

**Feature**: [spec.md](spec.md) | **Contracts**: [contracts/](contracts/)

CI is the verification authority (constitution VI, CLAUDE.md rule 8). Locally, run only compile checks. The scenarios below are what the `telemetry` and `upgrade` E2E buckets automate (research R17). They are written so that a reviewer can also walk through them by hand on a dev Kind cluster.

## Local compile checks (the only local verification)

```sh
cd telemetryschema && go build ./... && cd ..
cd telemetry-receiver && go build ./... && cd ..
cd api && go build ./... && cd ..
cd web && npx tsc --noEmit && cd ..
helm template gameplane charts/gameplane --set api.telemetry.enabled=false | grep -- --telemetry-disabled
```

## CI runs

| What | Where |
|---|---|
| Unit and coverage for `telemetryschema` (gate 90), `telemetry-receiver` (gate 70) and `api` (gate 80) | `go test` matrix (`ci.yaml` module lists) |
| API handler and migration integration | `make test-integration` (envtest) |
| Web components | vitest (`web` job) |
| Chart renderings | `helm template` checks |
| End to end | `make test-e2e-bucket BUCKET=telemetry`, and `BUCKET=upgrade` |
| Catalog drift | `hack/check-telemetry-catalog.sh` in `make lint` |

## Manual walk-through on a dev cluster

**Prerequisites**:

- `git submodule update --init`
- `make dev-up` (with `up.sh` deploying the bundled receiver, per [install-config.md](contracts/install-config.md))
- a dashboard token Secret:

```sh
kubectl -n gameplane-system create secret generic telemetry-dashboard \
  --from-literal=token="$(openssl rand -hex 24)"
helm upgrade gameplane charts/gameplane --reuse-values \
  --set api.telemetry.receiver.dashboard.tokenSecretRef.name=telemetry-dashboard \
  --set api.telemetry.receiver.publicSummary.enabled=true \
  --set api.telemetry.interval=2m
```

### S1: a fresh install defaults on and is gated by the notice (US1; FR-003, FR-004)

1. Sign in to the dashboard as the bootstrap admin.
   - **Expect** the telemetry banner, naming the in-cluster receiver host and listing the basic and extended fields.
2. Before you sign in, `GET /admin/telemetry` returns `consent.source = "default"` and `status.lastOutcome = "never"`. No report arrives while nobody has seen the notice.
3. Leave the banner showing for one interval.
   - **Expect** `lastOutcome = "ok"` and the receiver's `/api/v1/views` showing `extended.installs.active1d = 1`.

### S2: the tiers and the install ID (US3; FR-012, FR-018, FR-019)

1. Admin Settings → Telemetry shows the destination kind `bundled`, both switches, the install ID, the preview and the status line.
2. Turn off the extended switch and save.
   - **Expect** the install ID disappears from the preview, and the next report's `ext_reports` count doesn't increase.
3. Turn it back on.
   - **Expect** a new ID.
4. Choose "Reset ID".
   - **Expect** a new ID, and the next report counted as a new install.
5. Compare the preview JSON with what the receiver counted.
   - **Expect** them to agree field for field (SC-006).

### S3: at most one accepted report per interval across restarts (FR-007, SC-005)

```sh
kubectl -n gameplane-system rollout restart deploy/gameplane-api
```

- **Expect**: over the next 3 intervals, `daily_basic.reports` increases by 3 ± 1, and never by more than 1 within a single interval.

### S4: the private dashboard and the refusal (US4; FR-022, FR-023, FR-027)

```sh
kubectl -n gameplane-system port-forward svc/gameplane-telemetry-receiver 8081:8081
```

1. Open `http://localhost:8081/`.
   - **Expect** a redirect to the login page, which contains no figures.
2. Sign in with the token.
   - **Expect** the overview for 30 days. 7, 90 and 365 also work.
3. `curl -s localhost:8081/api/v1/views`
   - **Expect** `401 {"error":"unauthorized"}`.
4. `curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TOKEN" localhost:8081/metrics`
   - **Expect** `200`, and `401` without the header (spec Q8).
5. Rotate the Secret and restart the receiver.
   - **Expect** the old session is rejected and the data is intact (FR-028).
6. On a receiver with no data:
   - **Expect** the empty state, with no zero-filled charts.

### S5: the public summary (US6; FR-029–FR-032)

```sh
kubectl -n gameplane-system port-forward svc/gameplane-telemetry-receiver 8080:8080
curl -si localhost:8080/v1/summary
```

- **Expect** exactly the five keys from [receiver-http.md](contracts/receiver-http.md), plus `Cache-Control: public, max-age=3600` and `Access-Control-Allow-Origin: *`.
- With `publicSummary.enabled=false`: **expect** `404`.
- `curl -si localhost:8080/metrics`: **expect** `404`. Metrics live only on the dashboard port (FR-030).

### S6: operator disable and redirect (US2; FR-005, SC-003, SC-004)

1. `helm upgrade … --reuse-values --set api.telemetry.enabled=false`
   - **Expect** the settings section shows "disabled by the operator", the switches are disabled, `PUT /admin/config/telemetry` returns `409`, and the receiver gets no reports over 3 intervals.
2. `--set api.telemetry.enabled=true --set api.telemetry.endpoint=http://<other-receiver>/ingest`
   - **Expect** reports arrive only at the other receiver, and the settings show the kind `custom`.

### S7: an old receiver still gets basic reports (FR-016, SC-013)

Point `api.telemetry.endpoint` at a Deployment running `ghcr.io/valgulnecron/gameplane/telemetry-receiver:v0.2.0-beta.8`.

- **Expect** the API logs one 400 followed by an accepted basic-only POST, and later reports go basic-only without a 400 first.

### S8: upgrades preserve the existing choice (US1 scenarios 5 and 6; FR-003)

This is automated in the `upgrade` bucket. A beta.8 install with no saved choice, upgraded to this version:

- **Expect** `consent.source = "legacy"`, both tiers off, and `GET /admin/telemetry/notice` returning `pending: false`.
- If `sendMetrics` was true before the upgrade: **expect** basic on and extended off.

### S9: forged reports are refused, and a claimed ID heals itself (US7; FR-035–FR-038, SC-014, SC-015)

This is automated in the `telemetry` bucket with a test-only signing client built on `telemetryschema`.

1. Take the install's current ID from `GET /admin/telemetry` and POST a report with that ID, signed by a different key.
   - **Expect** `409 {"error":"id_claimed"}`, and no change to `/api/v1/views`.
2. POST an extended report with no signature header, then a correctly signed report with one body byte changed, then a byte-for-byte resend of an accepted report.
   - **Expect** `403` with `bad_signature`, `bad_signature` and `replay` respectively, and no change to any figure.
3. Reset the install ID, and immediately claim the new ID with the test client's own key before the install reports.
   - **Expect** the install's next attempt to get a 409, replace its ID and have the resend accepted. `GET /admin/telemetry` then shows a new `installId` and a set `status.lastIdRotationAt`.

### S10: proof-of-work follows the request rate (FR-039–FR-041, SC-016)

This is automated in the `telemetry` bucket, against a custom receiver deployed with `INGEST_POW=true`, `INGEST_POW_MIN_BITS=8` (so solving really happens) and `INGEST_POW_TARGET_PER_MIN=60`.

1. Point the API at that receiver and wait one interval.
   - **Expect** `gameplane_telemetry_pow_challenges_total` to rise and the report to be accepted (`lastOutcome = "ok"`).
2. POST `/ingest` with no `Gameplane-Telemetry-PoW` header, then with a used or tampered one.
   - **Expect** `428` with `pow_required`, then `pow_invalid`, and no figure changed.
3. Unit tests cover the curve: at or below the normal rate a challenge carries `INGEST_POW_MIN_BITS`; at 1,000 times the rate it carries at least 20 bits; the difficulty falls one bit per 5 minutes after the flood stops.
4. Start the receiver with a 31-character `DASHBOARD_TOKEN`.
   - **Expect** it to exit at startup with an error naming the 32-character minimum.

## Merge gate (OD-1, ruled 2026-10-07)

OD-1 is ruled: the domain is telemetry.gameplane.net. The merge gate is satisfied. The CI job `telemetry-default-gate` keeps enforcing that the default stays an https URL.

## Release checklist

- **OD-2.** The data-handling statement page on the project website is published, states 24-month retention (OD-3) and 90-day activity expiry (OD-4), and is linked from the notice, the settings and the upgrade notes.
- **FR-008 upgrade notes.** They are written in `CHANGELOG.md`, `docs/install.md` and the website telemetry page.
