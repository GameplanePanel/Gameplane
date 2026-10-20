# audit-syslog-bridge

The **audit-syslog-bridge** [optional] is a tiny, generic **HTTP-JSON → syslog** relay. It accepts an HTTP `POST` whose
body is a single JSON document and re-emits that document as an
[RFC 5424](https://www.rfc-editor.org/rfc/rfc5424) syslog message to a
configured collector (TCP, TCP over TLS, or UDP).

It exists so Gameplane's [audit webhook sink](../docs/install.md#audit-log) can
reach a syslog/SIEM endpoint, but it is deliberately **schema-agnostic**: it
forwards the received body verbatim as the syslog `MSG`, so it works for any
JSON-webhook source, not just audit events. Nothing in it is Gameplane-specific.

## Behavior

- `POST /` with a JSON body → forwarded as one syslog record. Returns `204`.
  - TCP uses RFC 6587 octet-counting framing (`<len> <msg>`); UDP sends the bare
    message as one datagram.
  - The receive time becomes the syslog `TIMESTAMP`; the body becomes `MSG`
    (collapsed to a single line).
- `GET /healthz` → `200 ok` (for Kubernetes probes).
- Other methods → `405`; empty body → `400`; a configured-but-mismatched auth
  header → `401`; a failed forward to the collector → `502`.

The forwarder reuses a lazily-dialed connection, bounds each write with a
deadline (so a hung collector can't wedge the relay), and reconnects once on a
write error. Before it reuses a TCP connection it checks that the collector
hasn't closed it, and opens a fresh one if it has. This check is best-effort: it
waits only a few milliseconds for the collector's close, so a close that
arrives later can still lose one record while the bridge returns `204` (written
to connection). Syslog has no application-level acknowledgement, so this window
can be narrowed but not closed.

Each inbound request must arrive in full (headers and body) within 15 s, and
idle keep-alive connections close after 120 s.

## Transport: prefer TCP

`SYSLOG_NETWORK=tcp` (the default) is recommended for an audit/compliance trail:

- **UDP has no delivery confirmation.** A connected-UDP write succeeds locally
  even when the collector is down, so the bridge returns `204` and the API
  counts the event `sent` while nothing actually arrives. TCP surfaces a dead
  collector as a `502` (→ API `failed`), which is the signal you want.
- **UDP caps message size.** A record whose JSON body approaches the 64 KiB
  intake limit can exceed the single-datagram ceiling and be dropped; TCP
  streams it fine.
- **TCP (and TLS) can still lose one record after a collector-side close.**
  A 204 means the record was written to the collector connection (best effort;
  the connection may close concurrently and the record be lost). If the
  collector closes a reused connection and its FIN/RST reaches the bridge only
  after the short liveness probe, the close goes unnoticed: one record is
  written into the dead connection and the bridge returns `204`. The following
  write detects the close, reconnects, and retries. If the retry succeeds, the
  bridge returns `204` and the record is delivered; if the retry fails, the
  bridge returns `502` and that record is not delivered. Syslog framing has no
  acknowledgement, so this window cannot be closed without a different transport
  (for example one with per-record acks).

`AUTH_HEADER` gates who may inject records — set it (the chart wires it from the
same Secret as the API's webhook token). Without it the relay accepts any POST.

## Configuration (environment variables)

| Var | Default | Meaning |
|---|---|---|
| `LISTEN_ADDR` | `:8514` | HTTP listen address |
| `SYSLOG_ADDR` | — (**required**) | collector `host:port` |
| `SYSLOG_NETWORK` | `tcp` | `tcp` or `udp` |
| `SYSLOG_TLS` | `false` | wrap a `tcp` connection in (verified) TLS |
| `APP_NAME` | `gameplane-audit` | RFC 5424 `APP-NAME` |
| `FACILITY` | `local0` | `kern`/`user`/`daemon`/`auth`/…/`local0`–`local7` |
| `SEVERITY` | `info` | `emerg`/`alert`/`crit`/`err`/`warning`/`notice`/`info`/`debug` |
| `SYSLOG_HOSTNAME` | OS hostname | RFC 5424 `HOSTNAME` field |
| `AUTH_HEADER` | — | if set, inbound requests must carry this exact `Authorization` header |

## Run via the Helm chart

The chart deploys this automatically when you set
`api.audit.webhook.syslogBridge.enabled=true` and a collector address — see the
[install docs](../docs/install.md#audit-log). Standalone:

```sh
docker run -e SYSLOG_ADDR=syslog.example:514 \
  ghcr.io/gameplanepanel/gameplane/audit-syslog-bridge:edge
```
