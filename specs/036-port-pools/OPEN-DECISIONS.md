# Decisions

Settled by the maintainer in the review of #1001; spec.md records the rules.

- **Colliding explicit overrides are refused.** A `servicePort` must respect
  an existing reservation for the same protocol and port in the pool. The
  requester reports the conflict and keeps its previous valid allocation, if
  any; the holder is unchanged. Creation age never lets an override displace
  another server.
- **A valid active allocation is kept on exhaustion.** The Service stays,
  every port it publishes stays reserved, and selectors, sleep and wake
  routing, relays and endpoints keep following it while the change is
  reported as pending and retried. A new server with no allocation stays
  unpublished. Only an uncontested allocation that still fits the effective
  workload is kept.
- **Existing allocations are kept when a pool shrinks.** The reduced range
  governs new and replacement allocations only. Ports outside it stay
  assigned and reserved across restarts, and are reported. Retiring them is a
  deliberate migration; the edge keeps forwarding them until then.
- **Capacity is reserved before a disruptive rollout.** A revision that
  can't use the current mapping reserves its replacement durably (the pending
  record) before the rollout is admitted and the workload drained. The old
  ports stay reserved until the switch. Without capacity, the server keeps
  its current revision and working configuration, reports the pending
  change, and continues normal lifecycle handling. This uses the existing
  revision machinery; it is not a general rollback framework.
