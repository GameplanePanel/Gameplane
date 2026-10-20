# Feature Specification: Default telemetry destination, extended telemetry, and telemetry dashboard

**Feature Branch**: `feat/default-telemetry-dashboard` (not yet created)

**Created**: 2026-10-06

**Status**: Draft (clarified); amended 2026-10-14 with the version trend (US5, FR-042), and again on 2026-10-14 with standalone and multicluster reporting (FR-043–FR-045)

**Input**: User description: "Right now telemetry is only something someone can add. i want to add a default to a domain (not yet defined) and also build a dashboard to show the data provided by the telemetry provider". Follow-ups: "also could we extend the telemetry?" and "right now there is no verif for data sent. so i want a way for server to 'claim' the id with a key derivated from stable local value to sign the data so no one can impersonatte other id"

## Clarifications

### Session 2026-10-06

- Q1: Once a default destination exists, is telemetry opt-in or opt-out? → A: **Opt-out for new installs.** Fresh installs report unless an admin turns it off, and an admin notice on first login explains this. Existing installs keep their saved choice. Installs that never saved a choice stay off.
- Q2: Who is the dashboard for, and where does it live? → A: **A private, access-controlled page served by the receiver itself**, for whoever operates a provider (the project's or a self-hosted one). Alongside it, a **small public API** exposes only headline counts and none of the other anonymized data.
- Q3: What should extended telemetry add? → A: **All four categories:**
  - environment (Kubernetes version, distribution, CPU architecture, node-count band)
  - game usage (servers per official catalog module, with everything else counted as "custom")
  - feature adoption (which optional features are in use)
  - a random anonymous install ID
- Q4: How do admins consent to extended data? → A: **Two tiers with separate toggles, both on by default for new installs.** The first-login notice covers both.
- Q5: What local value is the signing key derived from? → A: **A random secret created once on the install and kept in its database**, combined with the install ID. Resetting the ID therefore yields a new, unlinkable key. Nothing derived from the cluster's own identity is used.
- Q7: What happens on save when basic is turned off while extended is on? → A: **Extended is turned off with it**, and saving succeeds. The dashboard also disables the extended switch whenever basic is off.
- Q8: Where do the provider's operational metrics live? → A: **Behind the dashboard credential, on the private port.** No aggregate is readable from the provider's public port except the public summary.
- Q9: Is running the project's receiver in scope? → A: **A runbook only.** It documents deployment, TLS, secrets, the public summary and backups. No infrastructure code.
- Q10: Where is the provider dashboard designed? → A: **In its own Pencil file**, `telemetry-receiver/telemetry-dashboard.pen`, which starts from a copy of the HeroUI design. It follows the pink theme the main app and website share (`web/src/styles/globals.css` tokens, Geist and JetBrains Mono), dark first (user direction, 2026-10-07).
- Q6: What happens when a report's signature is wrong, or its ID is already claimed by another key? → A: **The provider refuses it and counts nothing.** When the refusal says the ID belongs to another key, the install automatically replaces its ID (and with it the key) and resends.

### Session 2026-10-07

- Q11: Per-source limits can be outrun by anyone holding many IPv6 /64s. What protects the dashboard credential? → A: **The provider refuses to start with a dashboard credential shorter than 32 characters**, so guessing it stays infeasible however many sources an attacker controls. Per-source limits remain a cost, not a bound (security review of 0819cec1).
- Q12: Should sending a report cost the sender work, as Anubis does for browsers? → A: **Yes, for report ingestion only (not the dashboard login), as an Anubis-style proof-of-work.** It is off by default, including in the bundled receiver, and the project's provider turns it on through the runbook. When it is on, the difficulty follows the request rate: none while traffic is normal or absent, rising fast as requests flood in, and falling slowly once they stop. Default values are ruled in OD-5.

### Session 2026-10-14 (amendment: version trend)

The Versions panel showed "pr617 6" for one install that had sent 6 daily reports, which reads like 6 installs. The user asked for the current version per install, with a trend of installs that upgrade, go stale or downgrade over time, and chose to amend this spec rather than start a new one.

- Q13: What does "stale" mean? → A: **Behind the newest release seen.** On a given day, an install is stale when its version is lower than the newest release any install reported in the selected range (OD-7).
- Q14: How are versions that aren't releases (`pr617`, `dev`, `edge-<sha>`) ordered? → A: **Semver only.** Only semver versions are ordered. A change to or from anything else counts as "changed (unordered)", never as an upgrade or downgrade, and such an install is never stale (OD-8).
- Q15: Where do the views go? → A: **Keep both, add the trend.** The basic reports-by-version panel stays and is renamed "Reports by version". The trend goes under "Versions by install" in the extended section (OD-9).
- Q16: Can a pre-release (`v0.4.0-rc.1`) set the newest-release bar for stale? → A: **Any semver.** The highest semver version seen in the range sets the bar, pre-releases included, so one install on `v0.4.0-rc.1` makes installs on `v0.3.0` stale (OD-10).

### Session 2026-10-14 (amendment: standalone and multicluster reporting)

Since the standalone panel (GameplanePanel/Gameplane#630) the API can run without a local cluster and manage only registered remote clusters. Reports counted the local cluster only, so a standalone panel reported 0 servers and a central panel missed its members' servers. The user asked that standalone panels and multicluster members still report through the central panel.

- Q17: Which clusters does a report count? → A: **All managed clusters.** The central panel sends one report, with servers, templates, games and features summed over its local cluster (if any) plus every registered remote cluster (OD-11).
- Q18: What if a managed cluster can't be reached? → A: **Send with the clusters that answered.** The report goes out on schedule; an unreachable cluster's servers are missing for that day (OD-12).
- Q19: Should reports say whether the install is a cluster install or a standalone panel? → A: **Add a marker.** Extended reports carry `cluster` or `standalone`, shown as an environment breakdown (OD-13).

## Context

Today an install sends anonymous usage reports (`{version, servers, templates}`, about once a day) only if **two** gates are both open:

1. **Whether**: an admin turns on **Admin Settings → Telemetry → Send anonymous usage metrics** (off by default).
2. **Where**: the operator configures a destination at install time, either an external receiver address or the bundled in-cluster receiver. Out of the box there is no destination, so the reporter never runs, even when the admin toggle is on.

So telemetry only works when someone adds a destination. This feature makes five changes:

- It gives gate 2 a default: the project-operated telemetry provider at https://telemetry.gameplane.net/ingest (OPEN-DECISIONS OD-1, ruled 2026-10-07).
- It turns telemetry on by default for **new** installs (Q1).
- It adds an **extended** tier of richer, still name-free data, behind its own toggle (Q3, Q4).
- It adds a private dashboard and a minimal public summary API on the provider (Q2).
- It makes extended reports verifiable. Each install signs them with a key tied to its install ID, and the provider binds each ID to the first key that uses it, so no one can report under another install's ID (Q5, Q6).

The receiver currently keeps only in-memory counters that reset on restart, stores nothing durable, and rejects any report field beyond the original three. Supporting this feature means it must:

- keep durable **aggregate** history
- keep a minimal per-install activity record for unique-install counts
- accept the extended report while still accepting basic reports from older installs

Raw reports are still never stored.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - New installs report by default, with an informed opt-out (Priority: P1)

An operator installs Gameplane fresh with default settings. When an admin first signs in, a notice explains three things:

- anonymous usage metrics are on, in two tiers: **basic** and **extended**
- exactly what each tier sends, including that extended includes a random install ID
- where the data goes

The notice lets the admin turn off extended only, or turn off everything. If the admin leaves telemetry on, reports reach the project's default telemetry provider, and nobody has to configure anything.

**Why this priority**: This is the core ask. A stock install produces adoption signal without setup, and the notice keeps the opt-out informed.

**Independent Test**: Fresh install pointed at a test provider standing in for the default domain. Sign in as admin, confirm the notice, leave telemetry on, and confirm that a report arrives and is accepted. Repeat, turning off extended only, and confirm that only the basic report arrives. Repeat, turning off everything, and confirm that nothing arrives.

**Acceptance Scenarios**:

1. **Given** a fresh install with no telemetry destination configured, **When** the first admin signs in, **Then** a notice states that basic and extended usage metrics are on, lists every field each tier sends, names the destination, and offers one-step actions to turn off extended only or everything.
2. **Given** a fresh install where an admin has seen the notice and left both tiers on, **When** time passes, **Then** the first report, basic plus extended, reaches the default provider within 1 hour of the notice being shown, and reports continue about once a day.
3. **Given** a fresh install where no admin has signed in yet, **When** time passes, **Then** nothing is sent.
4. **Given** a fresh install where an admin turns everything off before the first report, **When** time passes, **Then** nothing is ever sent.
5. **Given** an install that existed before this feature and never saved a telemetry choice, **When** it is upgraded, **Then** both tiers stay off and no notice claims telemetry is on.
6. **Given** an install that existed before this feature with basic telemetry already on, **When** it is upgraded, **Then** basic stays on and extended stays off until an admin turns it on.
7. **Given** an install whose operator configured a custom destination or the bundled receiver before this feature, **When** it is upgraded, **Then** it keeps reporting only to that destination and never to the default provider.

---

### User Story 2 - Operators can redirect or hard-disable telemetry at install time (Priority: P1)

An operator running an air-gapped or privacy-sensitive cluster sets one install-time option that guarantees the install never contacts any telemetry destination, whatever an admin later toggles. Another operator points reports at their own receiver instead of the project default.

**Why this priority**: Telemetry that is on by default is only acceptable if operators can refuse it before any data leaves. A hard off-switch protects air-gapped clusters, regulated environments, and anyone who doesn't want the install contacting a project-run service. It has to ship with the default, not after it.

**Independent Test**: Install with telemetry explicitly disabled, sign in as admin, and observe for a full reporting cycle that no notice claims telemetry is on and no outbound telemetry connection is made. Repeat with a custom destination and confirm that reports arrive there only.

**Acceptance Scenarios**:

1. **Given** an install with telemetry explicitly disabled at install time, **When** an admin opens Admin Settings → Telemetry, **Then** both toggles are unavailable and a message says the operator has disabled telemetry for this install.
2. **Given** an install with telemetry explicitly disabled, **When** any amount of time passes, **Then** no connection is made to any telemetry destination, and the US1 first-login notice is not shown.
3. **Given** an install with a custom destination, **When** reports are sent, **Then** they go only to the custom destination, and the US1 notice names that destination instead of the project default.

---

### User Story 3 - Admins see where data goes and exactly what is sent (Priority: P2)

At any time, an admin opens Admin Settings → Telemetry. They see:

- the destination host
- the two tier toggles
- the current install ID, with a way to reset it
- a literal preview of the next report, including every extended field when that tier is on
- while telemetry is on, when the last report was sent and whether it succeeded

**Why this priority**: Informed opt-out depends on admins being able to see the destination and exact payload at any time, not only in the one-time notice. The pipeline works without this, so it ranks P2.

**Independent Test**: Open the Telemetry settings on installs with the default, custom, bundled, and disabled destinations, each with extended on and off. Confirm that each shows the correct destination and a preview identical to what the provider receives.

**Acceptance Scenarios**:

1. **Given** the default destination, **When** an admin views Telemetry settings, **Then** the default provider's host is shown, labelled as the Gameplane project's telemetry service.
2. **Given** telemetry is on, **When** an admin views Telemetry settings, **Then** the last successful report time and the outcome of the last attempt are shown. Failures appear as a short, non-technical status, such as "Last attempt failed, will retry".
3. **Given** the preview, **When** the next report is sent, **Then** the payload the provider receives has the same fields and values as the preview at that moment.
4. **Given** extended telemetry is on, **When** an admin resets the install ID, **Then** the preview immediately shows a new ID, and the old ID is never sent again.
5. **Given** extended telemetry is turned off, **When** an admin views Telemetry settings, **Then** no install ID is shown or kept. Turning extended back on creates a new ID.

---

### User Story 4 - Provider operator views aggregate telemetry on a private dashboard (Priority: P2)

A provider operator opens the provider's private dashboard. This is the project maintainers for the default provider, or a self-hoster for their own receiver. After proving access, they see, for a chosen time range:

- how many installs reported
- which Gameplane versions they run
- how large their fleets are

**Why this priority**: The project needs the collected data to be readable before it can prioritize work with it. It ranks P2 because it depends on data from US1 existing.

**Independent Test**: Feed a provider a known set of synthetic basic reports across several days, open the dashboard with valid access, and confirm that every figure matches the expected aggregates. Open it without access and confirm that no figure is revealed.

**Acceptance Scenarios**:

1. **Given** reports from multiple days, **When** an authorized viewer picks a range of 7, 30, 90, or 365 days, **Then** the dashboard shows a per-day count of reports received.
2. **Given** reports from several versions, **When** an authorized viewer looks at version adoption, **Then** it shows each version's share of reports over the range. Versions outside the top 10 are grouped as "Other", and malformed versions appear as "Invalid".
3. **Given** reports with varying server and template counts, **When** an authorized viewer looks at fleet size, **Then** it shows the distribution in the same size bands the provider already uses (0, 1, 2, 5, 10, 25, 50, 100, 250, 250+) and the median per install.
4. **Given** no reports in the selected range, **When** an authorized viewer opens the dashboard, **Then** an empty state explains that no data has been received yet. It shows no zero-filled charts that could look like real data.
5. **Given** the provider has restarted, **When** an authorized viewer opens the dashboard, **Then** all history within the retention period is still shown.
6. **Given** a viewer without valid access, **When** they open the dashboard or any of its data, **Then** access is refused with a generic message. Nothing is revealed: no figures, no date ranges, no version names, and no hint about whether any data exists.

---

### User Story 5 - Extended telemetry shows how Gameplane is actually used (Priority: P3)

Using extended data, the provider operator sees on the dashboard:

- unique active installs, with new and lapsed installs
- which Kubernetes versions, distributions, architectures, and cluster sizes installs run on
- which official games are most run
- which optional features are actually used

These answer questions that basic counts can't, such as "how many installs are single-node homelabs on arm64?" or "is anyone using relay tunnels?".

**Why this priority**: It turns adoption counts into prioritization signal. The basic pipeline and dashboard (US1–US4) are useful without it, so it ranks P3.

**Independent Test**: Feed a provider a synthetic population of installs with known IDs, environments, games, and features over several weeks, including ID resets, upgrades, and installs that stop reporting. Confirm that every extended figure on the dashboard matches the expected values.

**Acceptance Scenarios**:

1. **Given** extended reports over a range, **When** an authorized viewer looks at installs, **Then** the dashboard shows:
   - unique active installs for today (in progress) and the trailing 7 and 30 days
   - new installs (first seen) per period
   - lapsed installs (not seen for 30 days) per period
2. **Given** extended reports, **When** an authorized viewer looks at environment, **Then** it shows each install's share by Kubernetes minor version, distribution, CPU architecture, and node-count band.
3. **Given** extended reports, **When** an authorized viewer looks at game usage, **Then** it shows, for each official module, the number of installs running it and the total servers using it, plus a single "custom" figure for all non-official modules.
4. **Given** extended reports, **When** an authorized viewer looks at feature adoption, **Then** it shows the share of installs using each tracked feature.
5. **Given** some installs share only basic data, **When** an authorized viewer looks at any extended figure, **Then** the dashboard states how many of the reports in the range included extended data, so coverage is clear.
6. **Given** an install that upgraded within the range, **When** an authorized viewer looks at version adoption by install, **Then** the install is counted on its latest reported version.
7. **Given** installs whose extended reports change version within the range, **When** an authorized viewer looks at the version trend, **Then** it shows per day how many installs upgraded, downgraded, or changed between versions that can't be ordered (FR-042).
8. **Given** installs active on several semver versions within the range, **When** an authorized viewer looks at the version trend, **Then** it shows per day how many active installs were stale, that is, on a version lower than the newest release seen in the range (FR-042, OD-7, OD-10).
9. **Given** an install that only ever reports a version that isn't semver (for example `pr617`), **When** an authorized viewer looks at the version trend, **Then** that install is never counted as upgraded, downgraded or stale, and a change of its version counts only as "changed (unordered)".
10. **Given** extended reports from cluster installs and standalone panels, **When** an authorized viewer looks at environment, **Then** it also shows each install's share by deployment, cluster or standalone (FR-045).

---

### User Story 6 - Anyone can read headline counts from a public summary API (Priority: P3)

A website, README badge, or curious user asks the provider's public summary API how many installs are reporting. They get headline counts only. Version adoption, fleet sizes, environment, games, features, and per-day history stay private to the dashboard.

**Why this priority**: It lets the project show adoption publicly (badges, website) without exposing the detailed anonymized data. The private dashboard and data collection are useful without it, so it ranks P3.

**Independent Test**: Feed a provider known reports, call the public summary without credentials, and confirm that it returns exactly the allowed headline counts and nothing else. Disable the public summary and confirm that it is unavailable.

**Acceptance Scenarios**:

1. **Given** the public summary is enabled, **When** anyone requests it without credentials, **Then** it returns only the values listed in FR-029.
2. **Given** the public summary, **When** its response is inspected, **Then** it contains no version names, fleet-size data, server or template totals, environment, game, or feature data, per-day series, or install IDs.
3. **Given** the provider operator has not enabled the public summary, **When** anyone requests it, **Then** it is unavailable.
4. **Given** heavy repeated polling of the public summary, **When** requests keep arriving, **Then** the provider keeps serving reports and the dashboard normally.

---

### User Story 7 - No one can report under another install's ID (Priority: P3)

Someone learns an install's ID, for example from a screenshot of Admin Settings, and sends forged reports under it to skew that install's version, environment or feature figures. The provider rejects them, because the ID was claimed by the real install's key and the forger can't produce that key.

**Why this priority**: Unique-install and per-install figures are only as trustworthy as the IDs behind them. The pipeline and dashboard work without this, so it ranks P3, but it has to ship with the extended tier, because a published ID would otherwise be forgeable from day one.

**Independent Test**: Against a provider that has accepted signed reports from an install, send reports carrying the same ID but signed with a different key, unsigned, with an altered body, or with an old send time. Confirm that every one is refused and no figure changes. Then have a fresh install's ID claimed by a different key first, and confirm that the install replaces its ID and its next report is accepted.

**Acceptance Scenarios**:

1. **Given** an install ID claimed by the install's key, **When** a report arrives with that ID signed by any other key, **Then** the provider refuses it and no aggregate or activity record changes.
2. **Given** a signed report, **When** any byte of its body is changed in transit, **Then** the provider refuses it.
3. **Given** an extended report without a signature, with a send time outside the accepted window, or replaying a report already accepted, **When** it arrives, **Then** the provider refuses it.
4. **Given** an install whose ID was claimed by someone else first, **When** its report is refused for that reason, **Then** the install replaces its ID and key, resends once, the resent report is accepted as a new install, and Admin Settings shows that the ID was replaced.
5. **Given** an install that resets its ID, **When** its next report arrives, **Then** it carries a new key, and the provider can't tell that the old and new IDs belong to the same install.
6. **Given** a basic-only report, **When** it arrives, **Then** no signature is required, because it carries no ID to impersonate.

---

### Edge Cases

- **Admin enabled the toggle before this feature, with no destination configured.** That consent was saved while it had no effect. After upgrade, basic reports start going to the default provider. Extended stays off (US1 scenario 6). The upgrade notes MUST call this out (FR-008).
- **New install vs. existing install.** An install counts as new only if it has no prior Gameplane state when it first starts on this version. Reinstalling over existing persisted state counts as existing, so an operator's or admin's previous choice is never silently flipped to on.
- **Install with no admin sign-in.** A headless or automated install where no admin ever signs in sends nothing, because the notice is never shown (FR-004).
- **Install ID reset.** The install appears to the provider as one new install, and its old ID lapses after 30 days. Dashboard install counts can briefly be off by one per reset. This is accepted and documented on the dashboard.
- **Duplicated ID.** A cluster restored from a backup, or cloned, can share an ID with its source. Such installs are counted as one until an admin resets the ID. This is an accepted limitation.
- **Unrecognized values.** An unrecognized Kubernetes distribution, an unknown architecture, or a version that doesn't parse is reported as "other". Raw strings are never sent.
- **Look-alike module names.** A module that shares a name with an official catalog module, but comes from any other source, counts as "custom". Its name is never sent.
- **Older provider.** A self-hosted provider older than this feature rejects extended reports. The install still delivers its basic report to it (FR-016).
- **Older installs.** The provider keeps accepting basic-only reports from installs older than this feature.
- **Managed cluster offline.** A registered cluster that can't be reached while a report is built is left out of that day's counts, and the report is still sent (FR-044). The install's server count drops for that day and returns when the cluster answers again.
- **Standalone panel with no clusters yet.** It reports 0 servers and templates, as a cluster install with no servers does, and its deployment marker says `standalone` (FR-045).
- **No route to the internet** (air-gapped cluster or restrictive egress). Delivery fails quietly. No user-facing error appears beyond the US3 status line, retries are bounded so a failure never becomes a request storm, and the rest of the product is unaffected.
- **Frequent restarts.** An install whose control plane restarts more often than once a day must still report about daily, and must never send more than one report per day (FR-007). Repeated restarts must not inflate counts.
- **Default provider unreachable or rejecting reports.** The install backs off and retries at the next cycle. Reports are never queued and replayed in bulk.
- **Spoofed or flooded reports.** The default provider accepts reports from anyone on the internet without a shared secret. Signing (FR-035–FR-038) stops anyone from reporting under an ID that another key has claimed. It can't stop someone from inventing new IDs and keys, so fabricated installs and basic reports remain possible. The provider limits how many reports a single source can submit. When the provider turns on proof-of-work (FR-039), every report also costs its sender work that grows with the request rate, so a flood becomes expensive however many addresses it comes from; it deters cheap bulk fabrication, not a well-resourced attacker. Dashboard and public-summary figures are presented as approximate and self-reported.
- **ID seen before its first report.** If someone copies an ID from the Admin Settings preview and claims it before the install's first report, the install's report is refused as already claimed. The install then replaces its ID automatically (FR-037), and the forger is left holding an ID nobody uses.
- **Install clock wrong.** A report whose send time falls outside the accepted window is refused. The install keeps its ID, backs off as for any failed delivery, and Admin Settings shows the failure. The ID is never rotated for this reason.
- **Claim expiry.** When an ID's activity record expires after 90 days without reports (OD-4), its claim goes with it. An install that returns after that is treated as new, and its own key claims the ID again.
- **Restored database.** Restoring the install's database from a backup restores the same secret and ID, and so the same key, and reports keep being accepted. Losing the database makes the install new (Edge Cases: new vs. existing install), with a new ID and secret.
- **Malicious values.** Malicious version strings, and malicious values in any other field, fall into "Invalid" or "other" buckets. Hostile input can't create unbounded dashboard categories or inject content into the dashboard.
- **Default domain decided.** A build MUST NOT ship with a placeholder destination (FR-002). OD-1 is ruled (2026-10-07), and the default is set to https://telemetry.gameplane.net/ingest.
- **Explicit disable conflicts with the admin toggles.** The operator's install-time disable always wins over both tiers.
- **Lost or leaked dashboard access.** The provider operator can replace the dashboard access credential without losing any stored data.

## Requirements *(mandatory)*

### Functional Requirements

**Default destination & consent**

- **FR-001**: When the operator has not configured a telemetry destination, the system MUST use the project's default telemetry provider as the destination.
- **FR-002**: The default destination MUST be defined in exactly one authoritative place, and MUST NOT ship as a placeholder or unresolvable value. OD-1 is ruled (2026-10-07), and the default is set to https://telemetry.gameplane.net/ingest.
- **FR-003**: Telemetry MUST have two tiers with independent admin controls: **basic** and **extended**. Extended can only be on while basic is on, and turning basic off turns extended off. The tier defaults are:
  - On a **new** install with a destination in effect, both tiers MUST default to **on**.
  - On an install that existed before this feature, a saved basic choice MUST be kept, and if no choice was ever saved, basic MUST stay **off**.
  - Extended MUST default to **off** on every pre-existing install.
- **FR-004**: On a new install where telemetry defaults to on, the system MUST show the first admin who signs in a notice. The notice:
  - lists the exact fields of both tiers
  - states that extended includes a random install ID
  - names the destination host
  - offers one-step actions to turn off extended only, or everything

  No report MUST be sent before an admin has been shown this notice. Once dismissed, the notice MUST NOT reappear for that admin.
- **FR-005**: An operator MUST be able to set, at install time, either a custom destination (which replaces the default) or an explicit **disabled** state. When disabled, no telemetry of either tier is ever sent, whatever the admin toggles say, and the FR-004 notice is not shown.
- **FR-006**: Destinations the operator configured before this feature, whether a custom destination or the bundled in-cluster receiver, MUST keep working unchanged after upgrade, and MUST NOT also send to the default provider.
- **FR-007**: While telemetry is on, an install MUST send about one report per day of uptime, survive control-plane restarts without skipping days, and send no more than one report in any 24-hour window.
- **FR-008**: The release that introduces this feature MUST state the following in its upgrade notes and install documentation:
  - that new installs share basic and extended data by default
  - exactly what each tier contains
  - which existing installs are affected (those whose basic toggle was already on)
  - how to disable telemetry at install time and from the dashboard
- **FR-009**: Reports to the default provider MUST travel only over an encrypted connection with a verified server identity.

**Payload & privacy**

- **FR-010**: The **basic** report MUST stay exactly the current three fields: version, server count, and template count.
- **FR-011**: The **extended** report MUST add the following, and only these, sent only while the extended tier is on:
  1. **Install ID**: a random identifier (FR-012).
  2. **Environment**:
     - Kubernetes minor version
     - distribution category (from a fixed published list, plus "other")
     - CPU architecture(s) of the nodes
     - node-count band: 1, 2–3, 4–10, 11–50, 50+
     - deployment: cluster or standalone (FR-045)
  3. **Game usage**: the server count for each module from the official catalog, plus one combined count for all other modules, labelled "custom".
  4. **Feature adoption**:
     - wake-on-connect in use (yes/no)
     - relay tunnel types in use
     - packet capture enabled (yes/no)
     - any server with backups configured (yes/no)
     - single sign-on enabled (yes/no)
     - audit forwarding in use (yes/no)
     - registered-cluster band: 1, 2–3, 4–10, 10+
     - database backend category
     - default dashboard language
  5. **Proof of identity**: the install's public signing key and the time the report was sent. The report is signed (FR-035).
- **FR-012**: The install ID MUST be random and not derived from any cluster, host, network, or user attribute. It MUST be kept only on the install. An admin MUST be able to reset it at any time. It MUST be deleted when the extended tier is turned off, and a new one created if the tier is turned back on. The install's signing secret (FR-035) MUST likewise be kept only on the install, and MUST never be sent, displayed or exported through any Gameplane interface.
- **FR-013**: Every extended value MUST come from a fixed, published set of categories or bands, except per-module server counts (non-negative integers), the install ID, the public signing key and the send time. Values that don't fit MUST be sent as "other". Neither tier MUST carry names, namespaces, hostnames, addresses, player counts, custom module names, or free text.
- **FR-014**: The provider MUST NOT store raw reports or source network addresses. It MAY keep source addresses transiently, and only to enforce per-source request limits. It MUST NOT store any extended attribute (environment, games, features) linked to an install ID. Extended attributes exist on the provider only as aggregates.
- **FR-015**: For unique-install counting, the provider MAY keep one **activity record** per install ID, holding only:
  - a one-way transformed form of the ID
  - a fingerprint of the key that claimed the ID (FR-036)
  - the first-seen date
  - the last-seen date
  - the send time of the latest accepted report, used only to refuse replays (FR-036)
  - the last reported version

  It MUST delete the record once the install has been inactive for the period set in OD-4.
- **FR-016**: The provider MUST keep accepting basic-only reports. An install whose provider rejects extended reports MUST still deliver its basic report to that provider.

**Admin transparency**

- **FR-017**: Admin Settings → Telemetry MUST show the destination host currently in effect: the project default, a custom destination, the bundled receiver, or disabled by the operator.
- **FR-018**: Admin Settings → Telemetry MUST show a preview of the next report's exact field values, including every extended field and the install ID when that tier is on. While telemetry is on, it MUST also show the time of the last successful report and the outcome of the last attempt.
- **FR-019**: Admin Settings → Telemetry MUST present basic and extended as separate controls, and MUST offer an install-ID reset while extended is on. When extended is on, no product copy may claim that no identifying data is sent. Instead, it MUST state that a random install ID is included. While basic is off, the extended control MUST be disabled and shown as off.

**Provider storage**

- **FR-020**: The provider MUST keep **daily aggregates** (UTC) for at least 12 months, and they MUST survive provider restarts.
  - **From basic data**, per day:
    - reports accepted
    - count per sanitized version
    - server and template distributions by size band
    - server and template totals
  - **From extended data**, per day:
    - unique active installs, and new installs
    - per-category counts for each environment field
    - installs and servers per official module, plus "custom"
    - per-feature adoption counts
    - the number of reports that included extended data
    - active installs per reported version
    - installs that upgraded, downgraded, or changed version without an order (FR-042)
- **FR-021**: The provider MUST limit how many reports a single source can submit per day, so that one source can't distort the figures by flooding.

**Private dashboard**

- **FR-022**: The provider MUST serve a private dashboard of the FR-020 aggregates for 7-, 30-, 90-, and 365-day ranges, viewable only by viewers who present the provider operator's access credential.
- **FR-023**: Unauthenticated access to the dashboard or its data MUST be refused with a generic message that reveals no figures, dates, categories, or whether any data exists.
- **FR-024**: The dashboard MUST show these **basic** views:
  - reports per day as a trend
  - version adoption by reports, titled "Reports by version", with the top 10 versions plus "Other" and "Invalid" (OD-9)
  - fleet-size distributions and medians for servers and templates
  - total servers and templates today (in progress)
  - a "data as of" timestamp
- **FR-025**: The dashboard MUST show these **extended** views:
  - unique active installs (today in progress, trailing 7 and 30 days)
  - new and lapsed installs per period
  - version adoption by install
  - the version trend (FR-042), placed under version adoption by install (OD-9)
  - environment breakdowns
  - game usage by installs and by servers, including "custom"
  - feature adoption shares
- **FR-026**: The dashboard MUST label basic figures as approximate and self-reported. Every extended view MUST show what share of reports in the range included extended data.
- **FR-027**: The dashboard MUST show a clear empty state when there is no data in the selected range, rather than zero-valued charts.
- **FR-028**: The provider operator MUST be able to replace the dashboard access credential without losing stored data. Until a credential is configured, the dashboard MUST stay unavailable rather than open.

**Public summary API**

- **FR-029**: The provider MUST offer a public summary that needs no credentials and returns **only**:
  - reports received on the latest complete day
  - reports received over the trailing 30 days
  - total reports received since collection began
  - unique active installs over the trailing 30 days (from extended data)
  - a "data as of" date
- **FR-030**: The public summary MUST NOT expose any other aggregate, any category breakdown, any per-day series, or any install ID. No other unauthenticated endpoint of the provider may expose aggregates either. Operational metrics MUST require the dashboard credential (FR-022).
- **FR-031**: The public summary MUST be off unless the provider operator enables it. The project's default provider enables it.
- **FR-032**: The public summary MUST be rate-limited per source, and safe for clients to cache, so that public polling can't degrade report ingestion or the dashboard.

**Self-hosted parity**

- **FR-033**: The bundled in-cluster receiver and standalone self-hosted receivers MUST offer the same storage, private dashboard, and public summary as the default provider, under the same defaults.
- **FR-034**: The official module catalog used for game-usage reporting MUST be the same published list the install already trusts as its official module source. Any module the install did not get from that source counts as "custom".

**Report authenticity**

- **FR-035**: Every extended report MUST be signed. The signing key MUST be derived from a random secret created once on the install and kept in its database, combined with the current install ID. The key is therefore unique to that ID, and an ID reset yields a new key that can't be linked to the old one. The secret and private key MUST never leave the install. The report carries the public key and its send time, and the signature MUST cover the whole report as sent.
- **FR-036**: The provider MUST bind an install ID to the key of the first validly signed report that uses it (the **claim**). From then on it MUST accept reports for that ID only when they are signed by the same key. It MUST refuse, without changing any aggregate or activity record, any extended report that is:
  - unsigned, or carries a signature that doesn't verify against the whole report
  - signed with a send time more than 36 hours old or more than 1 hour in the future
  - signed by a key other than the one that claimed its ID
  - sent no later than the latest report already accepted for its ID (a replay)
- **FR-037**: When the provider refuses a report because its ID is claimed by another key, the install MUST replace its ID, and so its key, and resend once in the same attempt. Admin Settings MUST show when the ID was last replaced for this reason. A refusal for any other reason MUST NOT trigger a replacement.
- **FR-038**: A claim MUST last exactly as long as the ID's activity record, and expire with it (FR-015, OD-4). Basic-only reports carry no ID and need no signature.

**Report cost and credential strength**

- **FR-039**: A provider MUST be able to require proof-of-work for report ingestion. When it does, every report MUST present a single-use challenge issued by the provider within the last 15 minutes, with a solution of the difficulty that challenge states. The provider MUST refuse, without changing any aggregate or activity record, a report whose challenge is missing, forged, expired, already used or unsolved. The difficulty MUST follow recent challenge demand: none at or below a configured normal rate, rising from the very next challenge when demand exceeds it, and falling back slowly once demand subsides. Proof-of-work MUST be off unless the provider operator enables it; the bundled receiver keeps it off by default, and the project's default provider enables it (OD-2 runbook). Defaults are OD-5.
- **FR-040**: When its provider offers challenges, the install MUST obtain and solve one before each report. When the provider doesn't offer them, the install MUST send without proof-of-work, so older and self-hosted providers keep working. The install MUST refuse to solve a challenge harder than a fixed cap (OD-5) and record the attempt as failed. When the provider refuses a report for its proof-of-work, the install MUST obtain a new challenge and resend once in the same attempt. Solving MUST NOT block the API's request handling.
- **FR-041**: The provider MUST refuse to start when a dashboard credential is set but shorter than 32 characters.

**Version trend** (amendment 2026-10-14)

- **FR-042**: The provider MUST derive a daily version trend from extended reports only, because basic reports carry no install ID. For each day in the range it shows:
  - **upgraded**: installs whose first non-duplicate report that day carries a semver version higher than their activity record's last version
  - **downgraded**: the same, with a lower semver version
  - **changed (unordered)**: installs whose version changed where either side isn't semver (OD-8)
  - **stale**: installs active that day on a semver version lower than the newest release reported by any install in the range (OD-7). The newest release is the highest semver version seen, pre-releases included (OD-10). Installs on a version that isn't semver are never stale.

  An install's first report (no activity record yet) is never a change. Version changes MUST be stored only as daily counts, never linked to an install ID (FR-014). The trend starts on the day the provider gains this feature, with no backfill.

**Standalone and multicluster reporting** (amendment 2026-10-14)

- **FR-043**: An install MUST send one report for everything its panel manages. Server and template counts (FR-010) and per-module game counts MUST be summed, and the cluster-derived features combined (wake-on-connect and backups: true when any cluster has them; relay tunnels: the union of the providers in use), over the local cluster, when there is one, plus every registered remote cluster (OD-11). A standalone panel, which has no local cluster, counts its registered clusters only. A registered cluster's servers count in the report of the panel that manages them. The basic report keeps exactly its three fields (FR-010).
- **FR-044**: When a registered cluster can't be listed while a report is built (unreachable, or its credentials rejected), the install MUST leave that cluster out of the counts and still send the report on schedule (OD-12). The report MUST NOT be delayed, retried or skipped because of it, and the clusters band (FR-011) MUST still count every registered cluster.
- **FR-045**: The extended report MUST carry a **deployment** marker in its environment: `standalone` when the panel runs without a local cluster, `cluster` otherwise (OD-13). It is a new extended schema version. The provider MUST accept both the new and the previous schema, and MUST keep a daily per-category count for deployment like the other environment fields (FR-020). A provider older than this amendment refuses the new schema, and the install falls back to basic reports for it (FR-016). Reports on the previous schema carry no marker and count as `other` in the breakdown (OD-14). The other environment fields cover every reachable managed cluster (OD-15): the Kubernetes minor version and the distribution become sets of values, as CPU architectures already are, and a standalone panel with no reachable cluster sends `other` for each. The node-count band stays one value, over all nodes of the reachable managed clusters (OD-16).

### Key Entities

- **Basic report**: one anonymous sample from one install, carrying version, server count, and template count. Unchanged by this feature.
- **Extended report**: the basic report plus an install ID, environment categories, per-official-module server counts with a "custom" total, feature-adoption flags and bands, the public signing key and the send time. It is signed (FR-035) and sent only while the extended tier is on.
- **Install ID**: a random identifier kept only on the install. An admin can reset it, and it's deleted when extended is turned off. The install also replaces it automatically when it turns out to be claimed by another key (FR-037).
- **Install signing secret**: random bytes created once on the install and kept in its database. Combined with the install ID, they derive the signing key. They are never sent or shown.
- **Telemetry destination**: where an install's reports go. Exactly one of: project default, operator-specified custom destination, bundled in-cluster receiver, or disabled.
- **Telemetry consent**: the admin's on/off choice for each tier. Basic and extended default to on for new installs. For existing installs, basic keeps its saved choice (or stays off if none was saved), and extended stays off. Consent is evaluated before every report, and an operator's disabled destination overrides both tiers.
- **Telemetry notice acknowledgement**: per admin, whether the FR-004 notice has been shown and dismissed. It gates the first report on new installs.
- **Delivery status**: per install, the last attempt time, the last successful report time, and the last outcome. Shown to admins and never sent anywhere.
- **Daily aggregate**: per calendar day (UTC) on the provider. Holds the basic and extended tallies listed in FR-020. It's the only form in which report contents are kept.
- **Version change**: a daily count of installs whose reported version went up, went down, or changed without an order (FR-042). Never linked to an ID.
- **Activity record**: per install ID on the provider. Holds the transformed ID, the fingerprint of the key that claimed it, the first-seen and last-seen dates, the latest accepted send time, and the last version. It expires after inactivity (OD-4), and the claim expires with it.
- **Dashboard access credential**: the provider operator's secret that unlocks the private dashboard. It can be replaced without touching stored data.
- **Public summary**: a view derived from daily aggregates and activity records, limited to the values listed in FR-029.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: On a fresh install with default settings, the first report is accepted by the default provider within 1 hour of the first admin seeing the notice, with zero telemetry configuration steps.
- **SC-002**: Turning telemetry off takes one action from the first-login notice and one action from Admin Settings, either for extended only or for everything. After turning off extended, no further report carries an install ID or any extended field. After turning off everything, the install makes zero connections to any telemetry destination over a 48-hour observation.
- **SC-003**: Over a 48-hour observation, an install with telemetry disabled at install time, or an upgraded install that never saved a choice, makes zero connections to any telemetry destination.
- **SC-004**: An operator can disable or redirect telemetry by changing exactly one install-time setting.
- **SC-005**: Over 7 days of operation that includes at least 3 control-plane restarts, an install with telemetry on produces 7 ± 1 reports, never more than one in any 24-hour window.
- **SC-006**: In 100% of sampled reports, the Admin Settings preview matches the payload the provider receives, field for field.
- **SC-007**: An authorized viewer can find the following within 1 minute of opening the dashboard, without any other tool:
  - unique active installs over 30 days
  - the most common version
  - the median fleet size
  - the five most-run official games
  - the share of single-node installs
- **SC-008**: For a synthetic population with known truth, including restarts, upgrades, and installs that stop reporting, the dashboard's 30-day unique-active-install count is within 1% of the true count.
- **SC-009**: After a provider restart, 100% of daily aggregates and activity records within their retention periods are still present.
- **SC-010**: With 12 months of history at 10,000 reports per day, the dashboard shows the 365-day view within 3 seconds.
- **SC-011**: Every response from the public summary contains only the values listed in FR-029. An unauthenticated request to the private dashboard reveals none of its figures.
- **SC-012**: An audit of everything the provider keeps finds only:
  - dates, category labels, counts, and band tallies
  - activity records holding a transformed ID, a key fingerprint, two dates, one send time, and a version

  It finds no raw reports, network addresses, untransformed IDs, full public keys, or extended attributes linked to any ID.
- **SC-013**: An install on this version that reports to a provider older than this feature still has 100% of its basic reports accepted.
- **SC-014**: In a test of every forgery case in US7 scenarios 1–3 against a claimed ID, 100% of forged reports are refused and 0 figures change.
- **SC-015**: An install whose ID was claimed by another key has a report accepted under a new ID within the same delivery attempt in 100% of tested cases.
- **SC-016**: With proof-of-work on, a challenge issued while demand is at or below the normal rate requires no work, and a challenge issued while demand is 1,000 times the normal rate requires at least 2^20 hash attempts on average (FR-039).
- **SC-017**: For a synthetic population with known upgrades, downgrades, unordered changes and installs left on older releases, every daily upgraded, downgraded, unordered and stale count on the version trend matches the expected value exactly.
- **SC-018**: A central panel managing a local cluster and two registered clusters, one of them unreachable, sends its report on schedule, and the report's server and template counts equal the sum over the two reachable clusters. A standalone panel's extended report says `standalone`, and the provider's deployment breakdown counts it once.

## Assumptions

- The default provider is operated by the Gameplane project. Its maintainers run it at https://telemetry.gameplane.net/ingest, and its data-handling statement is a page on the project website at https://gameplane.net/telemetry/ (OD-1 and OD-2, ruled 2026-10-07).
- "Telemetry provider" means the receiving service, whether that is the project-run default or a self-hosted or bundled receiver. All of them speak the same report format.
- Unique-install figures are available only for installs that share extended data, because only extended reports carry an install ID. Basic-only installs are counted as reports per day.
- The install ID is pseudonymous rather than anonymous: it links one install's reports over time. That is why it's confined to the extended tier, never linked to stored extended attributes (FR-014), resettable, and deleted when extended is turned off (FR-012). Signing protects existing IDs from impersonation. Fabricated new installs can only be limited by per-source limits, because there's no way to tell a real install from an invented one without a project-issued secret.
- The provider accepts unauthenticated reports from the public internet, because a shared secret can't be distributed to every install. Optional token-protected ingest for self-hosted receivers keeps working.
- The private dashboard uses a single operator-held access credential. Multi-user accounts and roles on the provider are out of scope.
- The public summary is aimed at badges and website counters. It's read-only and needs no credentials.
- Daily aggregates are kept for 24 months (OD-3, ruled; FR-020 floor 12 months). Activity records expire after 90 days of inactivity (OD-4, ruled).
- Game-usage and feature-adoption values come from what the control plane already knows about its own resources and settings. No data is collected from inside game servers or from players.
- The first-login notice (US1), the Admin Settings changes (US3), and the provider's private dashboard (US4, US5) are designed in Pencil before they're built (constitution Principle II). The notice and settings are designed in `design.pen`. The provider dashboard is designed in its own Pencil file, `telemetry-receiver/telemetry-dashboard.pen`, which starts from a copy of the HeroUI design, and its snapshot is exported to `telemetry-receiver/design-export/`. Opt-out, tier, disabled, custom-destination, restart, ID-reset, old-provider, public-summary, forged-report and ID-conflict paths get E2E coverage (constitution Principle I).
- Changes to provider behavior update `telemetry-receiver/specs.md` and its README in the same change, and reporter changes update `api/specs.md` (constitution Principle IV). Two receiver invariants change:
  - "stores nothing persistent" becomes "stores daily aggregates and expiring activity records only, never raw reports"
  - "exactly three fields" becomes "basic or extended report, both strictly validated"
- Changing telemetry from opt-in to opt-out for new installs, and adding the extended tier, reverses the documented "off by default" and "no identifying data" promises. Every doc and UI string that makes those promises (install docs, chart comments, receiver README, website telemetry page, the Admin Settings subtitle) is updated in the same change.
- Out of scope:
  - player counts or any per-player data
  - per-server details beyond the per-module counts
  - custom module names
  - free-text fields
  - telemetry from game agents
  - exporting raw data
  - alerting on telemetry
  - buying or provisioning the domain
