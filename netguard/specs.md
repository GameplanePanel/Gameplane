# netguard — Specification

**Status:** pre-v1 (v0.3.0)  
**Module / package:** github.com/GameplanePanel/gameplane/netguard

## Purpose

Shared SSRF/egress dial-guard restricting outbound connections so they cannot be aimed at cloud instance-metadata endpoints or other cluster-internal SSRF targets. Enforced at dial time to defeat DNS rebinding past a name-based allowlist. Used by:
- **Operator reconciliation** (module-source HTTP/git fetches)
- **API gateway** (notification sinks, Steam resolver)
- **Agent sidecar** (mod downloads, WebSocket RCON)

via two deliberately different policies that must remain separately selectable:
- **`IsAllowed`** (operator & API, permissive): admin-configured infrastructure (private registries, notification sinks, loopback RCON).
- **`IsPublic`** (agent & API Steam resolver, strict): user-supplied targets (mod URLs, Steam names).

## Responsibilities

- Evaluate IP addresses against two distinct trust policies (`IsAllowed`, `IsPublic`) that reflect the caller's confidence in the target.
- Provide drop-in replacements for `net.Dialer` and `http.Client` that enforce a policy at dial time via `Control` hooks.
- Detect cloud-metadata service hostnames and block them by name for clarity.
- Offer a pre-flight check (`CheckHostAllowed`) for transports where the dial is buried and cannot be instrumented (e.g., git over SSH).
- Block reserved/special-use ranges (test networks, CGNAT, NAT64/6to4 translation prefixes) that can reach cluster-internal hosts.

## Non-goals / boundaries

- Does not provide high-level API validation or sanitization; callers (operator modsrc handler, agent mod installer) own that.
- Does not intercept or modify DNS; the policy runs on resolved IPs only.
- Does not attempt to detect poisoned DNS responses; host-key pinning and TLS validation remain the caller's responsibility.
- Does not provide telemetry, logging, or audit trails; callers wrap the error if needed.

## Directory & package layout

```
netguard/
├── netguard.go          # policy functions (IsAllowed, IsPublic), factories, dial guards
├── netguard_test.go     # table tests for address policies, metadata hostname detection, Dialer/HTTPClient enforcement
├── go.mod              # (module, stdlib-only)
└── .testcoverage.yml   # 91% coverage gate
```

Single package; no subdirectories or internal structure.

## External interface / contracts

### Policy functions

- **`IsAllowed(ip net.IP) bool`** — operator policy (permissive). Allows globally routable and private (RFC1918 / ULA) addresses; loopback; disallows unspecified, multicast, link-local, and NAT64/6to4 prefixes that can wrap link-local addresses. Designed for self-hosted infrastructure (private registries on 10.0.0.0/8 or loopback).
- **`IsPublic(ip net.IP) bool`** — agent policy (strict). Allows only globally routable unicast addresses; disallows loopback, private (RFC1918 / ULA), link-local, multicast, unspecified, and reserved/special-use ranges (test networks, RFC 6598 CGNAT, NAT64/6to4, documentation blocks).

### Metadata detection

- **`HostIsMetadata(host string) bool`** — reports whether `host` is a known cloud instance-metadata hostname (currently: `metadata.google.internal`, `metadata`). Case-insensitive; trailing dot ignored.

### Factories

- **`Dialer(timeout time.Duration, allow func(net.IP) bool) *net.Dialer`** — returns a `net.Dialer` with `Timeout` set and a `Control` hook enforcing the `allow` policy at dial time. Resolving the address, binding to the local port, and establishing the connection all proceed normally; the hook runs on the resolved remote IP and returns `ErrBlockedAddr` if the policy rejects it.
- **`HTTPClient(timeout time.Duration, allow func(net.IP) bool) *http.Client`** — returns an `http.Client` with the guarded dialer. Disables `Proxy` (a forward proxy would hide the real destination IP) and uses a custom `Transport` with conservative connection pools (`MaxIdleConns: 2`). No `CheckRedirect` is set; callers that re-validate redirect hosts supply their own.

### Pre-flight check

- **`CheckHostAllowed(ctx context.Context, host string, allow func(net.IP) bool) error`** — resolves `host` (literal IP or DNS name), checks it against `allow`, and returns `ErrBlockedAddr` if blocked or metadata hostname. Returns DNS lookup errors as-is. Intended for transports (git over SSH, cgo tools) where the dial is not instrumented; the residual TOCTOU window is bounded by any host-key pinning those transports already require.

### Error

- **`ErrBlockedAddr`** — sentinel error, `errors.Is`-matchable, returned when a dial or host check is rejected by the policy.

## Key invariants

- **Two policies must remain separate.** Collapsing `IsAllowed` and `IsPublic` would either re-open the SSRF the agent guards against (if strict rules apply to the operator) or break self-hosted registries (if permissive rules apply to the agent). Tests (`TestIsPublic`) explicitly assert the split via a `policy split broken` check.
- **Dial-time enforcement.** The `Control` hook runs after name resolution but before the connection is established, so it sees the real destination IP and defeats DNS rebinding past a name-based allowlist.
- **No proxy bypass.** `HTTPClient` sets `Proxy: nil` to ensure the dial guard is authoritative; a forward proxy in the pod environment would hide the destination.
- **IPv4-mapped and IPv4-compatible IPv6 handling.** The `normalize()` helper converts IPv4-mapped (`::ffff:a.b.c.d`) and IPv4-compatible (deprecated, but still covered: `::a.b.c.d`) IPv6 addresses to their 4-byte form so IPv4 reserved-prefix checks apply uniformly.
- **Metadata hosts by name and IP.** Cloud metadata addresses (link-local 169.254.169.254 / metadata.google.internal) are blocked by address via the policy and by name via `HostIsMetadata` for clearer error messages.

## Dependencies

**Internal:** None.  
**External:** stdlib only (`context`, `errors`, `net`, `net/http`, `strings`, `syscall`, `time`). No external packages listed in `go.mod`.

## Security considerations

- **SSRF prevention.** Both policies block unroutable addresses and ranges that can be exploited to reach cluster-internal services or cloud metadata endpoints. `IsAllowed` (operator) permits private addresses because self-hosted registries are legitimate internal endpoints; `IsPublic` (agent) forbids them because untrusted mod URLs should not be allowed to reach the cluster.
- **RFC 6598 CGNAT.** Several Kubernetes setups (EKS custom networking, GKE, Cilium/Calico) use the RFC 6598 CGNAT range (100.64.0.0/10) for node/pod addressing. Blocking it in `IsPublic` prevents a malicious mod URL from reaching cluster-internal hosts via that range.
- **NAT64 / 6to4 translation prefixes.** The `64:ff9b::/96` (NAT64 well-known) and `2002::/16` (6to4) IPv6 prefixes can wrap IPv4 addresses, including link-local (169.254.0.0/16). Both policies block them defensively.
- **Metadata hostname whitelisting.** `HostIsMetadata` catches known cloud providers; the address-based check catches metadata endpoints by IP. Together they provide defense in depth.

## Testing & coverage

- **Test structure:** Table-driven tests (`TestIsAllowed`, `TestIsPublic`) cover boundary cases: public IPs, RFC1918, loopback, link-local, multicast, unspecified, IPv4-mapped IPv6, NAT64/6to4, and reserved ranges.
- **Policy split verification:** `TestIsPublic` includes a check that the two policies genuinely differ (e.g., `IsAllowed(10.0.0.1) = true` but `IsPublic(10.0.0.1) = false`).
- **Metadata detection:** `TestHostIsMetadata` verifies case-insensitivity and trailing-dot stripping.
- **Dial-time enforcement:** `TestHTTPClientGuardsDial` verifies that the `Control` hook rejects blocked addresses. `TestHTTPClientNoProxy` asserts that `Proxy` is `nil`.
- **Pre-flight check:** `TestCheckHostAllowed` exercises both IP literals and DNS names with both policies, including metadata hostnames.
- **Coverage gate:** 91% (`.testcoverage.yml`). The small remaining gap is in error branches (`dialControl` and `CheckHostAllowed` error paths for malformed addresses and DNS failures).

## References

- **`docs/architecture.md`** — overview of netguard in the operator/agent's security boundaries.
- **`docs/security.md`** — threat model and SSRF defense rationale.
- **`operator/internal/modsrc/{git,http}.go`** — module-source fetches via `IsAllowed`.
- **`agent/internal/rcon/websocket.go`** — loopback WebSocket RCON via `IsAllowed`.
- **`agent/internal/mods/mods.go`** — mod downloads via `IsPublic`.
- **`api/internal/notify/{notify,deliver}.go`** — admin-configured notification sinks via `IsAllowed`.
- **`api/internal/steam/resolver.go`** — Steam name resolution via `IsPublic`.
- **`api/internal/registry/registry.go`** — module registry queries (Modrinth, Thunderstore, etc.) via `IsPublic`.
- **`go.work`** — workspace linking netguard to operator, agent, and other Go modules.
