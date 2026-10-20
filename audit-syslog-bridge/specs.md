# audit-syslog-bridge — Specification

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/audit-syslog-bridge`

## Purpose

Optional, schema-agnostic HTTP-JSON → RFC 5424 syslog relay. Sits behind the API's audit webhook sink (`api.audit.webhook.syslogBridge.enabled`) to forward audit events to a syslog/SIEM collector, but works for *any* JSON webhook source — nothing is Gameplane-specific.

## Responsibilities

- Listen on HTTP, accept `POST /` with a JSON body, forward it as one RFC 5424 syslog record
- Encode the received timestamp, hostname, app-name, and collapse the JSON body to a single-line MSG field
- Frame the message for the wire: RFC 6587 octet-counting (`<len> <msg>`) for TCP (reliable stream framing), bare message for UDP (one datagram)
- Maintain a lazily-dialed, reused syslog connection; before reusing a TCP connection, check that the collector has not closed it and redial if it has; reconnect once on write error
- Bound each write with a deadline (default 5s) to prevent a hung collector from blocking the HTTP handler indefinitely
- Provide `/healthz` for Kubernetes probes
- Enforce optional bearer-token auth on POST /

## Non-goals / boundaries

NOT Gameplane-specific — forwards the received JSON body verbatim as the syslog MSG, so it does not parse or understand the Gameplane audit schema. Works equally well as a generic webhook-to-syslog relay for any source. See README.md for config/run instructions.

## Directory & package layout

Single flat package (`main`): `main.go` (relay + config + server logic), `bridge_test.go` (unit tests covering HTTP handler, syslog framing, config validation, TCP/UDP forwarding, auth, reconnection, write-deadline enforcement), `go.mod` (workspace-linked, stdlib-only), `Dockerfile`, `README.md`.

## External interface / contracts

**HTTP endpoints:**
- `POST /` — forward JSON body as syslog record
  - Request: any Content-Type, JSON body up to 64 KiB
  - Response: `204 No Content` (record written to collector connection; best effort, may be lost if collector closes concurrently); `400 Bad Request` if body empty/unreadable; `401 Unauthorized` if `AUTH_HEADER` is set and does not match; `405 Method Not Allowed` for GET/other; `502 Bad Gateway` if forward to collector fails
- `GET /healthz` — Kubernetes probe
  - Response: `200 OK` with body "ok"

**Transports:**
- TCP (RFC 6587 octet-counting framing): `<decimal-length> <message>`, reliable for audit trails
- UDP: bare message, one datagram per record
- TLS: wraps TCP in verified TLS 1.2+; `SYSLOG_TLS=true` (requires `SYSLOG_NETWORK=tcp`)

**Auth:**
- Optional bearer token: if `AUTH_HEADER` environment variable is set, the HTTP request `Authorization` header must match exactly (constant-time comparison); absence of the env var = any POST accepted

## Key invariants

- Forwards the received JSON body verbatim (schema-agnostic; does not parse, validate, or understand audit events)
- Prefers TCP over UDP for audit/compliance trails (UDP has no delivery confirmation; TCP surfaces a dead collector as a 502)
- Connection reuse: lazily dials once, then reuses; before each TCP write a short read probe (5 ms) checks whether the collector has closed the connection, and the bridge redials if it has; on write error, closes and reconnects once before surfacing the error. The probe is best-effort, not a delivery guarantee: a collector close whose FIN/RST lands after the probe goes unnoticed, and one record may then be written into the dead connection (reported as `204`: written to connection, best effort); the next write detects the close and reconnects, returning `204` on successful retry or `502` if the retry fails. Syslog framing has no acknowledgement, so this cannot be closed without a different transport
- Write deadline per frame (5s default) prevents a collector that accepts but does not drain from blocking indefinitely and wedging the handler behind the connection mutex
- RFC 5424 compliance: formats message as `<PRI>1 TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA MSG`; collapses embedded newlines/CRs to spaces so each syslog record is one line; PROCID, MSGID, STRUCTURED-DATA are "-"
- `APP_NAME` and `SYSLOG_HOSTNAME` are validated at startup the same way `FACILITY`/`SEVERITY` are: each must be 1*max PRINTUSASCII (RFC 5424 %d33-126, no spaces or control bytes), APP-NAME capped at 48 bytes and HOSTNAME at 255 bytes, or `newServer` rejects the config before the process starts serving. An empty value is accepted and rendered as the RFC 5424 nil value `-`; when `SYSLOG_HOSTNAME` is empty the OS hostname is used instead, and an OS hostname that fails the same check (or can't be read) falls back to `-`

## Dependencies

**Go 1.26** (workspace-linked to `go.work` alongside `operator/`, `api/`, `agent/`, etc.)

**Imports:** stdlib only (`crypto/tls`, `net`, `net/http`, `log/slog`, `sync`, `time`, `context`, etc.). No external dependencies.

## Security considerations

- **Auth boundary:** optional bearer token (`AUTH_HEADER` env var) gates who may inject records (constant-time comparison; absence = open)
- **TLS transport:** `SYSLOG_TLS=true` wraps the TCP connection in verified TLS 1.2+, protecting audit events in transit
- **Trust boundary:** API ← audit-syslog-bridge ← SIEM/syslog collector. The API rate-limits webhook submissions; the bridge forwards verbatim and is stateless, so it does not amplify or replay events
- **DoS mitigation:** inbound body capped at 64 KiB; each request must arrive in full (headers and body) within 15 s (`http.Server.ReadTimeout`, with a 10 s header bound) and idle keep-alive connections close after 120 s; the per-frame write deadline bounds the collector side, so a misbehaving client/collector cannot wedge the process

## Testing & coverage

**Coverage gate:** 70% (`.testcoverage.yml`). Tests cover HTTP handler (methods, auth, empty body), RFC 5424 framing, APP-NAME/HOSTNAME/facility/severity validation, TCP/UDP forward, connection reuse, reconnect after the collector closes the connection (plain TCP and TLS), reconnect-after-write-failure, write deadline enforcement, forward-failure 502 (including a collector that has gone away), the intake read bound, graceful shutdown on context cancel, and env-var defaults. Uncovered: `main()`/`run()` process signal handling (ListenAndServe + SIGTERM), which is not unit-testable; ~30% gap is acceptable and noted in the gate comment.

## References

- `audit-syslog-bridge/README.md` — behavior table, config env-vars, transport tradeoffs, run instructions
- `docs/security.md` — audit integrity, threat model, pre-auth privacy (login page anonymity is separate; the bridge sits behind auth)
- `docs/install.md#audit-log` — Helm values to enable syslog-bridge, auth-header Secret wiring
- API audit webhook sink (`api/internal/audit/audit.go`) — `WebhookSink` calls `POST http://gameplane-audit-syslog-bridge.<namespace>.svc:8514/` with audit events
