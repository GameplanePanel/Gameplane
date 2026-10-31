# Monitoring games and relays

Gameplane exports the existing agent CPU, process memory, disk and player gauges,
optional game performance measurements, and relay health. Prometheus and Grafana
are optional. A monitor resource alone does not prove discovery or network access.

## Complete setup

Install the Prometheus Operator and its CRDs first. Apply
[`examples/monitoring/gameplane-values.yaml`](examples/monitoring/gameplane-values.yaml)
to the Gameplane release and deploy
[`examples/monitoring/prometheus.yaml`](examples/monitoring/prometheus.yaml), or
copy that resource's selectors into your existing Prometheus configuration.

The examples agree on the `monitoring: gameplane` monitor/rule label, discover
both `gameplane-system` and `gameplane-games`, and place Prometheus in `monitoring`.
Change all three files/settings together when renaming namespaces. With
kube-prometheus-stack, configure `prometheus.prometheusSpec.serviceMonitorSelector`,
`podMonitorSelector`, their namespace selectors and `ruleSelector` to match the
example. Do not leave the chart's default release-label selector pointing at a
different Helm release. The Grafana sidecar must watch ConfigMaps labeled
`grafana_dashboard: "1"` in the Gameplane release namespace.

The chart enables rules and both Grafana dashboards in this example. Agent
metrics use plain HTTP port 9090; tunnel metrics share the health listener on
8091. Neither monitor uses the agent's mTLS control port or a client certificate.
Check Prometheus **Status → Targets** for agent and tunnel targets with `up=1`,
then query `gameplane_agent_players_online` and `gameplane_tunnel_ready`.
Unknown player counts are absent even when scraping works.

With the operator and its NetworkPolicies enabled, the chart requires a nonempty
`serviceMonitors.scrapeNamespaceSelector`. Missing configuration fails with a
message naming the setting. If another system owns the policies, set
`serviceMonitors.externallyManagedPolicy: true`; that flag opens no traffic.
When upgrading an installation that already enabled monitors without a selector,
set the selector or the explicit external-policy option in the same Helm upgrade.
Supply your own ingress policies selecting `app.kubernetes.io/name: gameplane-game`
on TCP 9090 and `app.kubernetes.io/name: gameplane-tunnel` on TCP 8091, with a
namespace selector for the scraper. Restricted Prometheus egress policies must
also allow those destinations, DNS and API discovery.

For enforced isolation, configure `networkPolicies.strict: true`, actual
`kubeletCIDRs` and `apiServerCIDRs`, and the game modules' exact `probePorts`.
The legacy broad kubelet allowance can otherwise admit other private-address
sources on metrics ports. Keep metrics ports out of the game probe allowance;
the dedicated scrape policies grant their access. The CI monitoring bucket
uses Cilium and actual node/API addresses, tests a scraper in a separate
namespace, revokes/restores the scrape policy, and checks an unapproved namespace
cannot reach agent metrics.

## Declaring numeric game measurements

Reuse a module's existing `capabilities.status.metrics[]` entry. This opt-in
example describes an output such as `TPS: 19.5`:

```yaml
capabilities:
  status:
    metrics:
      - id: tps
        displayName: TPS
        command: stats
        regex: 'TPS: (?P<value>[0-9.]+)'
        unit: TPS                 # presentation only
        performance:
          signal: tick_rate
          unit: per_second        # machine-readable input unit
          degradedThreshold: "18"
```

`signal` is one of `tick_rate`, `tick_duration`, or `frame_rate`. Rates require
`per_second`; durations accept `seconds` or `milliseconds`. Values and thresholds
are converted to hertz or seconds. The optional threshold is a finite nonnegative
decimal string, up to nine integer and six fractional digits, for portable CRD
handling. Rates below the threshold or durations above it trigger the degradation
rule after five minutes. No default threshold or game-specific tick rate is
invented. Modules must choose a read-only command and a meaningful threshold.
No external module is opted in by this change.

The source polls immediately and then 30 seconds after each completed cycle.
There are at most 16 declared entries, 16 distinct commands per cycle, 1024 bytes
per command, 4096 bytes per regex and 64 KiB of accepted response text. Identical
commands are executed once and parsed for each reading. Each command waits at
most three seconds and the whole cycle at most ten seconds. The legacy RCON
interface cannot cancel every protocol operation: one outstanding call may
finish later, but it blocks further background calls until it returns, its late
result is discarded, and no queue or growing set of workers is created.
Underlying transports retain their own deadlines and response limits. Source
RCON bounds dialing to five seconds, authentication to ten seconds (including
all preamble packets), and a command exchange to 30 seconds after authentication,
including continuously arriving fragments, and accepts at most 8 MiB of
aggregate response text for large administrative commands. Its 400-ms fragment
idle window never extends the absolute exchange deadline. Exceeding either
bound discards the incomplete response and closes the connection. This transport
cap is separate from the status collector's smaller 64-KiB acceptance limit.

Both protected `/status` routes and Prometheus share the same source. Templates
with valid performance declarations use the interval cache for all their status
readouts; templates without them retain the five-second on-demand cache. Scrapes
never execute RCON or wait on an RCON command. Samples expire 75 seconds after
the command completed. Failed queries, invalid/negative/nonfinite values and
stale samples have no numeric value series. A genuine zero is retained.

The metric families are `gameplane_agent_performance_value`,
`gameplane_agent_performance_available`,
`gameplane_agent_performance_sample_timestamp_seconds`, and optional
`gameplane_agent_performance_degraded_threshold`. Labels are the bounded metric
ID, closed signal and base unit; threshold adds `direction=below|above`.
PodMonitor adds namespace, Pod, server and component identity. Display strings,
commands, responses and errors never become labels. Unsupported declarations
produce no performance series. Failed supported declarations emit availability
zero; value and timestamp remain absent.

## Relays and infrastructure

Tunnel gauges read the same snapshot as readiness: `gameplane_tunnel_ready`,
`gameplane_tunnel_connection_supported`, `gameplane_tunnel_connection_known`, `gameplane_tunnel_connected`,
`gameplane_tunnel_forwards_registered`, `gameplane_tunnel_endpoint_assigned`,
and `gameplane_tunnel_observation_timestamp_seconds`. `connected` and observation
timestamp are absent when their evidence is unknown or stale. Readiness and
registration flags mean *confirmed*, so zero does not invent a disconnected
provider. The rules distinguish an unavailable relay from unknown health.
Pinned Playit cannot confirm live connectivity: its support gauge is zero and
the informational unknown-health alert excludes it. Its known/connected metrics
still represent unknown evidence, without claiming a healthy relay. Supported
providers raise the info alert when their observations become unknown or stale;
an observed tsnet failure is unavailable and raises the warning-level outage
alert. Between Tailscale attempts and while a replacement joins, the supervisor
knows that no forwarding attempt is serving, so that unavailable state remains
known throughout backoff. It carries no provider observation timestamp. Fresh
observations resume when forwarding is established and retain their normal
staleness limit. See [relay-health.md](relay-health.md).
Tailscale, frp and Playit all use the same metrics path. Metrics contain no
credentials, login URLs, proxy names or provider error messages.

Container CPU throttling and working-set pressure require kubelet/cAdvisor
scrapes. OOM/restart, Pending Pod and node pressure rules require kube-state-metrics.
Install/configure those collectors (kube-prometheus-stack normally provides
them); the minimal Prometheus example demonstrates Gameplane discovery and
does not deploy them or Grafana. A missing upstream collector leaves those
series absent; it does not mean zero pressure. Agent process RSS is not total
container memory. These rules filter the games namespace and use `max` to
deduplicate replicated sources. The existing fleet rules likewise collapse
duplicate operator replicas instead of adding their cache-derived counts.

The Workloads dashboard shows scrape health, relay readiness/known state,
supported game measurements, container pressure, restarts and Pending Pods.
Missing measurements remain gaps. CI renders and evaluates the shipped alert
rules with promtool, including outage/recovery, supported zero versus unknown,
pressure, throttling, OOM, Pending/node pressure and duplicate operator readings.
