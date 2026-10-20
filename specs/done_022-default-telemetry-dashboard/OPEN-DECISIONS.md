# Open Decisions

**Status**: 0 open; 16 ruled (OD-2, OD-3 and OD-4 on 2026-10-06, OD-1 and OD-5 on 2026-10-07, OD-6 on 2026-10-08, OD-7 to OD-16 on 2026-10-14).

Per CLAUDE.md rule 10, an open value MUST NOT be committed as a settled contract in code, chart defaults or docs until it is ruled here.

---

### OD-1: Default telemetry domain

**Status**: RULED (2026-10-07, user)

**Question**: Which domain (and ingest path) is the project's default telemetry destination?

**Why it matters**: FR-001 needs a concrete default. FR-002 forbids shipping a placeholder. Until this is ruled, the default stays unset, stock installs keep today's behavior, and the opt-out default (FR-003) has no effect.

**Context**: the project website has no custom domain yet. It is served from GitHub Pages at `valgulnecron.github.io/gameplane-website` (`website/src/config.ts`: `CUSTOM_DOMAIN` is undefined). GitHub Pages is static hosting, so it can't receive reports.

**Options**:
1. Register one project domain and use `telemetry.<domain>`. The website can move to the same domain later.
2. A dedicated domain used only for telemetry.

**Recommended default**: (1). A host users already know from the docs looks more trustworthy in the first-login notice and the Admin Settings destination line (FR-004, FR-017), and there is only one domain to register and renew.

**Ruling (2026-10-07, user)**: the project domain is `gameplane.net`. The default telemetry destination host is `telemetry.gameplane.net`, so `telemetry.DefaultEndpoint = "https://telemetry.gameplane.net/ingest"` (the receiver serves `POST /ingest`, `GET /v1/challenge` and `GET /v1/summary`). The website moves to `gameplane.net`, so the data-handling statement URL (OD-2) is `https://gameplane.net/telemetry/`. The website-side changes (`CUSTOM_DOMAIN`, the statement page) belong to T096 in the `website` submodule.

**Consequences**:
- `DefaultEndpoint` is set, so `telemetry-default-gate` passes and the feature PR can leave draft once the other gates are green.
- The destination copy in the designs and the docs name `telemetry.gameplane.net`; the bundled and custom sample hosts stay samples.

**Superseded interim ruling (2026-10-06, user)**: The domain is not decided yet.
- Implementation proceeds with `telemetry.DefaultEndpoint` left **blank**, as FR-002 allows.
- The feature's PR **MUST NOT merge until a domain is chosen**, recorded here as RULED, and set in `DefaultEndpoint`.

**How the block is enforced** (plan.md, Merge gate):
- The PR stays a **draft** until this decision is ruled. GitHub does not allow merging drafts, and the master ruleset has no required status checks that could block the merge instead.
- A dedicated CI job, `telemetry-default-gate`, fails while `DefaultEndpoint` is empty or isn't `https`.

**Blocks**:
- merge of the feature PR
- FR-001's default value
- the FR-008 upgrade notes
- the destination copy in the first-login notice and Admin Settings designs

---

### OD-2: Provider hosting, ownership, and data-handling statement

**Status**: RULED (2026-10-06, user)

**Question**: Who runs the default provider, and where is its data-handling statement published?

**Ruling**: The project maintainers run the default provider. The data-handling statement is a page on the project website, in the `website` submodule. It is linked from:
- the first-login notice (FR-004)
- Admin Settings → Telemetry (FR-017)
- the upgrade notes (FR-008)

**Consequences**:
- The statement page is new website work. It goes through the submodule's own PR flow, and the website's design-first rule applies only if it needs a new screen layout.
- The dashboard links to the statement through one constant, because the website URL changes once `CUSTOM_DOMAIN` is set (see OD-1).
- The statement states the OD-3 and OD-4 periods.

---

### OD-3: Aggregate retention ceiling

**Status**: RULED (2026-10-06, user)

**Question**: How long does the provider keep daily aggregates? FR-020 sets a floor of 12 months.

**Ruling**: **24 months**. `RETENTION_DAYS` defaults to `730`, the Helm value `api.telemetry.receiver.retentionDays` defaults to `730`, and the enforced minimum is 365. Two years covers year-over-year comparison, and the stated limit appears in the OD-2 statement.

---

### OD-4: Activity-record inactivity expiry

**Status**: RULED (2026-10-06, user)

**Question**: How long after an install's last report does the provider delete its activity record? An activity record holds the transformed install ID, the first-seen date, the last-seen date and the last version (FR-015).

**Ruling**: **90 days**. `ACTIVITY_EXPIRY_DAYS` defaults to `90`, the Helm value `api.telemetry.receiver.activityExpiryDays` defaults to `90`, and the enforced minimum is 31, because the expiry must stay longer than the 30-day lapsed window (US5). This keeps new and lapsed counts accurate for installs that go quiet for a season, and the period appears in the OD-2 statement.

---

### OD-5: Proof-of-work defaults

**Status**: RULED (2026-10-07, user)

**Question**: Which defaults does the receiver's proof-of-work use (FR-039, FR-040, research R21)? The user set its shape on 2026-10-07 (spec Q12): off by default and turned on by the runbook for the project's provider, with a difficulty of zero while traffic is normal or absent, rising fast with the request rate and falling slowly.

**Ruling**: the "gentler ceiling" option.

| Setting | Value | Effect |
|---|---|---|
| `INGEST_POW` | `false` | Off in the binary and in the bundled receiver; the runbook turns it on for the project's provider. |
| `INGEST_POW_TARGET_PER_MIN` | `60` | The normal challenge rate. No work is required at or below it. |
| `INGEST_POW_MIN_BITS` | `0` | No work under normal load. |
| `INGEST_POW_MAX_BITS` | `22` | The ceiling during a flood: about 4.2 million hashes, under a second on a desktop and roughly 5–8 seconds on a Raspberry Pi 4 (estimates). |
| `MaxPoWBits` (install cap) | `26` | The install refuses harder challenges (about 67 million hashes, a minute or two on a Raspberry Pi 4), so a misconfigured provider can't burn an install's CPU. |
| Curve | `ceil(2 · log2(r / T))` | 10× the normal rate gives 7 bits, 100× gives 14, 1,000× gives 20. |
| Decay | 1 bit per 5 minutes | From 22 bits back to 0 in under 2 hours. |
| Challenge lifetime | 15 minutes | Longer than a Raspberry Pi 4 needs at the 26-bit cap. |
| Challenge requests per source | 10 per minute, burst 5 | One source can't raise the difficulty for everyone. |
| Used-challenge memory | 1,000,000 entries | Filling it needs that many solved challenges within 15 minutes. |

### OD-6: Dashboard as-of day

**Status**: RULED (2026-10-08, user)

**Question**: The receiver dashboard's ranges ended on the latest complete UTC day (yesterday), so a provider saw "No reports yet" for up to a day after its first accepted report, and today's reports never showed until the day closed.

**Ruling**: the dashboard shows all data. Every range (7, 30, 90 or 365 days) ends on today, the current UTC day, which is still in progress and labelled so. The empty state appears only when the range holds no report at all. Today's lapsed-installs value stays `0` until the lifecycle job finalises the day. The public `GET /v1/summary` is unchanged and keeps the latest complete day.

---

## Amendment 2026-10-14: version trend (FR-042)

### OD-7: What "stale" means

**Status**: RULED (2026-10-14, user)

**Question**: The version trend counts installs that upgraded, downgraded or went stale. Upgrades and downgrades are detected when an install reports a different version than last time. What makes an install stale? The receiver doesn't know which release is newest today, and installs that stop reporting are already counted as lapsed.

**Options**:
1. Behind the newest seen: on a day, an install's version is lower than the newest release any install reported in the range.
2. Unchanged for N days.
3. Both: behind the newest seen release for at least N days.

**Ruling**: (1) behind the newest seen. No threshold to tune. Whether a pre-release can be the newest release is OD-10.

### OD-8: Ordering versions that aren't releases

**Status**: RULED (2026-10-14, user)

**Question**: Hand builds (`pr617`), PR images (`dev`) and edge images (`edge-<sha>`) have no order. How does the trend treat them?

**Options**:
1. Semver only: only semver versions are ordered; a change to or from anything else is "changed (unordered)".
2. Semver, with everything else sorting below every release.
3. Leave installs on non-release builds out of the trend.

**Ruling**: (1) semver only. The trend never guesses a direction, and an install on a non-semver build is never stale.

### OD-9: Where the views go

**Status**: RULED (2026-10-14, user)

**Question**: The basic "Versions" panel counts reports by version (FR-024), and "Versions by install" in the extended section already counts each install once on its latest version (FR-025). Where do these and the new trend go?

**Options**:
1. Replace the top panel with the per-install view and the trend.
2. Keep both, rename the reports panel "Reports by version", and put the trend under "Versions by install".
3. Move "Versions by install" and the trend to the top, and the reports panel down.

**Ruling**: (2) keep both and add the trend. The layout is designed first in `telemetry-receiver/telemetry-dashboard.pen`.

### OD-10: Pre-releases and the newest-release bar

**Status**: RULED (2026-10-14, user)

**Question**: Can a pre-release such as `v0.4.0-rc.1` be the "newest release" that makes other installs stale (OD-7)? Release images report their git tag (`v0.3.0`), and the repo has pre-release tags (`v0.3.0-rc.1`). Semver orders `v0.4.0-rc.1` above `v0.3.0`, so one rc tester would make every install on the newest final release stale. Moving to or from a pre-release still counts as an upgrade or downgrade either way.

**Options**:
1. Finals only: only versions without a pre-release suffix set the bar.
2. Any semver: the highest semver seen sets the bar, pre-releases included.

**Ruling**: (2) any semver. The highest semver version seen in the range sets the bar, pre-releases included. One install on `v0.4.0-rc.1` makes every install on `v0.3.0` stale for the days in the range.

---

## Amendment 2026-10-14: standalone and multicluster reporting (FR-043–FR-045)

Since the standalone panel (GameplanePanel/Gameplane#630) the API can run without a local cluster and manage only registered remote clusters. `Collect` (`api/internal/telemetry/collect.go`) counts servers, templates, games and features on the local cluster only, so a standalone panel reports 0 servers and a central panel misses its members' servers. The user asked that standalone panels and multicluster members still report through the central panel.

### OD-11: Which clusters a report counts

**Status**: RULED (2026-10-14, user)

**Question**: Should the report count servers and templates across every cluster the central panel manages?

**Options**:
1. All managed clusters: one report per central panel, with servers, templates, games and features summed over the local cluster (if any) plus every registered remote.
2. Local only: counts stay local; a standalone panel keeps reporting 0 and a multicluster install misses member servers.

**Ruling**: (1) all managed clusters.

### OD-12: A managed cluster can't be reached

**Status**: RULED (2026-10-14, user)

**Question**: When the central panel builds its daily report, a registered remote cluster may be offline or its credentials may have expired. Is the report still sent?

**Options**:
1. Send, reachable only: the report counts the clusters that answered and is sent on schedule; an offline cluster's servers are missing for that day.
2. Skip and retry: no report until every cluster answers; an always-offline cluster stops the install's telemetry.

**Ruling**: (1) send with the clusters that answered.

### OD-13: Deployment marker

**Status**: RULED (2026-10-14, user)

**Question**: Should extended reports carry a deployment marker, `cluster` or `standalone`, shown as a new environment breakdown on the dashboard? (The "telemetry marker for standalone installs" proposed on #630, which the user ruled a follow-up rather than a merge condition of #630.) The receiver rejects unknown fields, so it must learn the field first through a new extended schema version. An install that sends it to an older self-hosted receiver has its extended report refused and falls back to basic only (FR-016).

**Options**:
1. Add the marker: a new extended field and dashboard breakdown; the receiver learns it first, installs send it from the next release.
2. No marker: no schema change; standalone installs show up only through their cluster counts and `other` environment values.

**Ruling**: (1) add the marker.

### OD-14: Older reports in the deployment breakdown

**Status**: RULED (2026-10-14, user)

**Question**: Installs on releases before the marker send extended reports without it (schema 1). Their deployment is unknown. How does the breakdown show them?

**Options**:
1. Count as `other`, the rule every other environment field uses for a value that doesn't fit (FR-013). **Recommended.**
2. Leave them out: shares are computed only over installs that send the marker, and the card says how many sent it.

**Ruling**: (1) count as `other`.

### OD-15: Environment fields on a standalone or central panel

**Status**: RULED (2026-10-14, user)

**Question**: The environment fields (Kubernetes minor version, distribution, CPU architectures, node-count band) are single values describing the cluster the API runs on. Do they keep doing so now that the counts span every managed cluster (OD-11)?

**Options**:
1. Own cluster only: no change to `env`; a standalone panel sends `other` (and `["other"]` for `arch`), and a central panel with a local cluster describes that cluster. **Recommended.**
2. All managed clusters: the environment fields become per-cluster sets across members; a larger schema change and new dashboard logic.

**Ruling**: (2) all managed clusters. The Kubernetes minor version and the distribution become sorted, de-duplicated sets over the reachable managed clusters, as `arch` already is. On the dashboard an install counts once under each value it reports, so those shares can add up to more than 100%, as architectures already do. How the node-count band is reported is OD-16.

### OD-16: Node-count band across clusters

**Status**: RULED (2026-10-14, user)

**Question**: With the environment fields covering every managed cluster (OD-15), is the node-count band one band for the total, or one band per cluster? The dashboard uses it for the share of single-node installs (SC-007).

**Options**:
1. Total nodes: one band over all nodes of the reachable clusters; three single-node clusters report `2-3`, and shares still add up to 100%. **Recommended.**
2. Band per cluster: a set of bands; three single-node clusters report `1`, and shares can add up to more than 100%.

**Ruling**: (1) total nodes. `env.nodes` stays one band, over all nodes of the reachable managed clusters.
