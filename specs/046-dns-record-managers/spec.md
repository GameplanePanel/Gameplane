# Feature Specification: DNS records through provider managers

**Feature Branch**: `spec-046/dns-record-managers`

**Created**: 2026-11-27

**Status**: Draft (OD-1 to OD-4 settled, OD-5 and OD-6 open, see `OPEN-DECISIONS.md`). Not v1 scope unless valgul says so.

**Input**: User description: "https://github.com/GameplanePanel/Gameplane/issues/1006 since this would need to have multiple connector the idea is a centralised api that receive order from the main app and then send it to a "manager" sidecar this manager sidecar would be capable of communicating to one type of dns to create the record so we would have one for cloudflare, ovh, hetzner, etc... and also support zone transfer with a specific manager that would hold a full zone and be the "main" dns for the zone"

## Background

Today a server's `spec.networking.hostname` (or the template default from `operator.templateHostnames`) is only an ExternalDNS hint: the operator writes `external-dns.alpha.kubernetes.io/hostname` on the Service and never creates a DNS record itself (`docs/networking.md`, "A Hostname as the Server's Address"). Operators without ExternalDNS keep records by hand, and nothing publishes SRV records, so players of games such as Minecraft Java must type a port whenever the public port is not the game's default.

Issue #1006 asks for template-defined SRV records (`_minecraft._tcp` on the `game` port, with priority and weight), published from the port players really reach (Service port, NodePort, or a NAT/forwarding-edge mapping), updated when that changes and withdrawn when publication stops. It proposes ExternalDNS as the writer.

This feature keeps the issue's record rules but has Gameplane write the records itself, through its own DNS layer:

- A central **DNS service** receives record orders from Gameplane ("this server needs these records") and is the only place that knows which zone and which provider each name belongs to.
- Each DNS provider is reached through a **provider manager**: a small sidecar that speaks to exactly one kind of DNS provider (one for Cloudflare, one for OVH, one for Hetzner, and so on). Adding a provider means adding a manager, not changing the central service.
- One special manager, the **zone manager**, holds a full zone itself and is the primary DNS for it. Other DNS servers (a provider's secondary DNS, or the operator's own) copy the zone from it by zone transfer.

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Players connect by name without a port (Priority: P1)

An admin connects a DNS zone (for example `example.com` at Cloudflare) once. A server owner sets the hostname `play.example.com` on a Minecraft Java server whose template declares an SRV service `_minecraft._tcp` on its `game` port. Gameplane creates the address record for `play.example.com` and the SRV record `_minecraft._tcp.play.example.com. SRV 0 0 31234 play.example.com.`, where `31234` is the port players actually reach. Players type `play.example.com` and join.

**Why this priority**: This is the outcome issue #1006 asks for, and it is the reason for every other part of the feature.

**Independent Test**: With one provider manager and one connected zone, create a NodePort Minecraft Java server with a hostname in that zone; query the provider's DNS and see the address and SRV records with the allocated NodePort; join with a client using only the hostname.

**Acceptance Scenarios**:

1. **Given** a connected zone and a server with a hostname in it whose template declares an SRV service, **When** the server's external endpoint is known, **Then** the address and SRV records are created at the zone's provider within 2 minutes and the server's Networking view shows them as "Published".
2. **Given** a published server, **When** its public port changes (port override, new NodePort, pool reallocation, or a changed NAT mapping), **Then** the SRV record is updated to the new port and no record with the old port remains.
3. **Given** a published server, **When** its hostname is cleared, publication is turned off, or the server is deleted, **Then** every record Gameplane created for it is removed, and records Gameplane did not create are left untouched.
4. **Given** a server whose public port cannot be determined (for example a ClusterIP Service with no external mapping, or a NAT edge with no known mapping), **When** Gameplane reconciles it, **Then** no SRV record is published (an existing one is withdrawn) and the server shows "Not published: public port unknown" with the reason.

---

### User Story 2 - Admin connects zones at different providers (Priority: P1)

An admin opens the DNS settings, adds a zone, picks its provider (Cloudflare, OVH, Hetzner, …) and enters the provider credentials. Gameplane checks it can read and write the zone and shows the zone as "Connected" or the exact error ("credentials rejected", "zone not found", "no write permission"). Several zones at several providers can be connected at the same time; each hostname goes to the zone with the longest matching suffix.

**Why this priority**: Without a connected zone nothing can be published, and supporting several providers is the main reason for the manager design.

**Independent Test**: Connect two zones at two different providers, set hostnames in each on two servers, and check each provider holds only its own records.

**Acceptance Scenarios**:

1. **Given** valid credentials, **When** the admin adds a zone, **Then** the zone shows "Connected" after a write test that creates and deletes a temporary record.
2. **Given** wrong credentials or a zone the credentials cannot reach, **When** the admin adds the zone, **Then** it shows the provider's error in plain words and nothing is published to it.
3. **Given** a hostname that matches no connected zone, **When** a server uses it, **Then** the server shows "Not published: no connected zone for play.other.net"; no ExternalDNS hint is written for it (OD-1).
4. **Given** provider credentials, **When** anyone reads them back through the dashboard or API, **Then** only "set" or "not set" is shown; the secret value never leaves the panel.

---

### User Story 3 - Gameplane is the primary DNS for a zone (Priority: P2)

An admin who wants no provider API at all (or a provider Gameplane has no manager for) adds a zone of type "Hosted by Gameplane". The zone manager holds the whole zone: its SOA and NS records, the server records Gameplane creates, and any other records the admin adds (OD-5). The admin lists the secondary DNS servers allowed to copy the zone; on every change the zone manager tells them and they copy the new version by zone transfer.

**Why this priority**: It covers every DNS host that supports secondary zones, without writing one manager per provider, but it needs more setup than a provider manager.

**Independent Test**: Create a hosted zone, add one allowed secondary, publish a server record, and check the secondary serves the new record after the change notice, while a server not on the allowed list is refused a transfer.

**Acceptance Scenarios**:

1. **Given** a hosted zone with an allowed secondary, **When** a server record changes, **Then** the zone's serial increases, the secondary is told, and it serves the change within 2 minutes.
2. **Given** a transfer request from an address or key not on the zone's allowed list, **When** it reaches the zone manager, **Then** it is refused and the refusal is logged.
3. **Given** the zone manager restarts, **When** it comes back, **Then** the zone content and serial are the same as before the restart.

---

### User Story 4 - Module authors declare SRV services (Priority: P2)

A module author adds an SRV declaration to a game template: service name (`minecraft`), protocol (`tcp` or `udp`), the named port it points to, and optional priority and weight. `gp-module validate` rejects a declaration that names a port the template does not have, or an invalid service name.

**Why this priority**: SRV records are only useful for games whose clients look them up; the declaration belongs with the game, not with each server.

**Independent Test**: Add an SRV declaration to a test template; validate passes; change the port name to a missing one; validate fails with a message naming the missing port.

**Acceptance Scenarios**:

1. **Given** a template with a valid SRV declaration, **When** it is validated, **Then** it passes and the declaration is part of the template revision servers use.
2. **Given** a declaration naming a port the template lacks, **When** it is validated or applied, **Then** it is rejected with the missing port name.
3. **Given** a server pinned to an older template revision, **When** the template's SRV declaration changes in a newer revision, **Then** the server keeps publishing from its own effective revision until it moves to the new one.

---

### User Story 5 - Admin sees DNS health (Priority: P3)

An admin sees, for each zone, whether its manager is reachable, when it last synced, and how many records Gameplane owns there; for each server, which records are published, pending or refused, and why.

**Why this priority**: Failures (expired credentials, a provider outage, a removed manager) must be visible before players notice, but publishing works without this view.

**Independent Test**: Stop a provider manager; within 2 minutes its zone shows "Manager unreachable" and affected servers show "Pending", and records are not deleted because of the outage.

**Acceptance Scenarios**:

1. **Given** a manager that stops answering, **When** 2 minutes pass, **Then** its zone shows "Manager unreachable" and no records are removed because of it.
2. **Given** the manager returns, **When** it reconnects, **Then** pending changes are applied in order and the zone shows "Connected".

### Edge Cases

- Two servers ask for the same hostname: the first owner keeps it; the second shows "Not published: name owned by server X". A second server may add an SRV record under that hostname only if it has a different service and protocol and its own external address is the address the hostname already resolves to; otherwise its SRV record is not published and it shows "Not published: play.example.com points to server X's address".
- A record with the same name and type already exists at the provider and was not created by Gameplane: Gameplane never overwrites or deletes it; the server shows "Not published: record exists and is not owned by Gameplane".
- A hostname changes from a name in zone A to a name in zone B: records are created in B and removed from A; if B fails, A's records are still removed and the server shows the B error.
- A zone is disconnected while servers still use it: Gameplane asks before removing, then removes every record it owns in that zone (or leaves them, if the admin chooses "keep records").
- Provider rate limits or partial failures: changes are retried with backoff. While the provider accepts changes, no SRV record points to a port the server no longer uses for longer than 5 minutes (SC-002). While the provider rejects changes or its manager is unreachable, the old record cannot be changed: the change stays queued (FR-013), the server shows "Pending: provider not accepting changes since <time>" and the zone shows its error, until the change is applied.
- Several clusters (multi-cluster panel): each cluster's servers publish through the same zones; ownership is tracked per server across clusters so one cluster cannot remove another's records.
- A sleeping server (wake-on-connect): records stay published while asleep, because the sentinel keeps the port.
- IPv6: an AAAA record is published when the endpoint has an IPv6 address; both A and AAAA when it has both.
- Existing ExternalDNS users: once the DNS layer is enabled, the operator stops writing the ExternalDNS hint on every Service (OD-1). Records ExternalDNS already made are not Gameplane's (FR-007): ExternalDNS removes them itself under `--policy=sync`, otherwise the admin removes them; until then the server shows "Not published: record exists and is not owned by Gameplane". The upgrade note says so.

## Requirements *(mandatory)*

### Functional Requirements

**Records and endpoints**

- **FR-001**: Gameplane MUST publish an address record (A and/or AAAA) for a server's effective hostname (server setting, else template default) when the hostname is in a connected zone and the server's external address is known.
- **FR-002**: Gameplane MUST publish one SRV record per SRV declaration in the server's effective template revision, pointing at the server's hostname, with the declared priority and weight (default 0 and 0) and the port players reach.
- **FR-003**: The SRV port MUST come from the server's effective external mapping, per issue #1006: the Service port (including overrides and pool allocation) for an externally reachable LoadBalancer without upstream translation; the actual NodePort for NodePort exposure; the public port of an explicit or authoritative mapping for a NAT or forwarding edge. It MUST NOT be inferred from the container port, a pool annotation alone, or a Service port under NodePort or NAT exposure. A ClusterIP alone is not external reachability.
- **FR-004**: When the external address or port is unknown or unsupported, Gameplane MUST withhold (or withdraw) the affected record and report the reason on the server. It MUST never publish a guessed value.
- **FR-005**: Gameplane MUST update its records when the hostname, external address, external port, template revision or SRV declaration changes, and MUST remove them when the hostname is cleared, publication is turned off for the server, or the server is deleted.
- **FR-006**: Server owners MUST be able to turn DNS publication off per server; it is on by default when the hostname is in a connected zone.
- **FR-007**: Gameplane MUST only change or remove records it created, and MUST mark ownership so it can tell them apart after a restart or a lost database (ownership marker kept at the provider, or in the hosted zone).
- **FR-007a**: With the DNS layer enabled, the server's effective hostname MUST be a publish order to the central DNS service, not an ExternalDNS hint: the operator MUST stop writing `external-dns.alpha.kubernetes.io/hostname` on game Services (OD-1). This is a breaking change for installs that relied on the hint and MUST get a `Breaking:` changelog entry and an upgrade note.
- **FR-008**: The address the server advertises (`status.endpoints[].host`) and the assigned Service address (`serviceAddress`) MUST keep their current meaning; DNS publication reads them and does not change them.

**Central DNS service and managers**

- **FR-009**: A central DNS service MUST receive record orders from Gameplane (desired records per server), route each to the manager of the zone it belongs to (longest suffix match), track ownership and state, and report results back to the server's status.
- **FR-010**: Each provider manager MUST serve exactly one provider type and expose the same small set of operations to the central service: check zone access, list owned records, create, update and delete a record. A new provider is added by shipping a new manager, without changing the central service or the dashboard beyond adding the provider's credential fields.
- **FR-011**: The first release MUST include managers for Cloudflare, OVH, Hetzner and RFC 2136 dynamic update (BIND, PowerDNS, Knot, Technitium and others), plus the hosted zone manager (OD-2).
- **FR-012**: Managers MUST hold provider credentials only for their own zones, receive them from the cluster secret store, and never return them to the central service, the API or the dashboard.
- **FR-013**: The central service MUST keep working when one manager is down: other zones publish normally; changes for the down zone are queued and applied when it returns; nothing is deleted because a manager is unreachable.
- **FR-014**: Applying the same order twice MUST give the same records (orders are idempotent), so retries and restarts never create duplicates.

**Hosted zones (zone manager)**

- **FR-015**: Admins MUST be able to create a zone hosted by Gameplane, with its SOA values and NS records.
- **FR-016**: The zone manager MUST serve full and incremental zone transfers to the secondaries on the zone's allowed list, authenticated by shared transfer key, and MUST refuse and log any other transfer request.
- **FR-017**: The zone manager MUST send a change notice to every allowed secondary when the zone serial increases, and MUST increase the serial on every change.
- **FR-018**: Hosted zone content and serial MUST survive a restart of the zone manager.
- **FR-019**: The zone manager MUST be a hidden primary: it serves zone transfers and change notices only to the zone's allowed secondaries and MUST NOT answer public DNS queries; the zone's NS records name the secondaries (OD-4). Whether admins can add records of their own to a hosted zone follows OD-5.

**Templates**

- **FR-020**: Game templates MUST be able to declare SRV services (service name, protocol tcp or udp, named port, optional priority and weight). Validation (`gp-module validate` and the operator) MUST reject declarations naming a missing port or an invalid service name.
- **FR-021**: The official Minecraft Java module MUST declare `_minecraft._tcp` on its `game` port. Other official modules get SRV declarations only where their game clients look SRV records up.

**Administration and visibility**

- **FR-022**: Admins MUST be able to add, test, edit and disconnect zones (provider or hosted) in the dashboard and through the API; only admins may do this, through the existing admin role, with no new permission (OD-3).
- **FR-023**: Each zone MUST show its state (Connected, Manager unreachable, Credentials rejected, …), last successful sync time, and the number of records Gameplane owns in it.
- **FR-024**: Each server's Networking view MUST list its DNS records with state (Published, Pending, Not published) and the reason for any record not published.
- **FR-025**: Every zone change (add, edit, disconnect, credential change) and every record change MUST be written to the audit log with who or what caused it.
- **FR-026**: The whole DNS layer MUST be optional: an install that does not enable it behaves exactly as today, including the ExternalDNS hostname hint. Enabling it replaces the hint (FR-007a).
- **FR-027**: Documentation MUST list supported providers, supported exposure modes and mapping sources, the hosted-zone transfer setup, and how to move from ExternalDNS (the hint is replaced, existing ExternalDNS records must be removed before Gameplane publishes the name).

### Key Entities

- **DNS zone**: a domain Gameplane may publish into. Name, kind (provider or hosted), provider type, reference to its credentials, state, last sync, owner cluster(s).
- **Provider manager**: a running connector for one provider type; serves one or more zones of that type; reports its health.
- **Zone manager**: the manager that holds hosted zones itself; holds SOA, NS, records, serial, and the allowed secondaries with their transfer keys.
- **Record order**: the desired set of records for one server (name, type, value, TTL), with the server it belongs to.
- **Owned record**: a record Gameplane created, with its zone, owner server and last applied value.
- **SRV declaration**: part of a template revision; service, protocol, named port, priority, weight.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A player can join a Minecraft Java server on a non-default public port by typing only its hostname, for each supported exposure mode (LoadBalancer, NodePort, documented NAT mapping).
- **SC-002**: After a server's public port or hostname changes, the published records match the new values within 2 minutes in 95% of changes, and no record with the old value remains after 5 minutes.
- **SC-003**: Across create, change and delete of 100 servers, Gameplane never changes or removes a record it did not create.
- **SC-004**: An admin connects a zone at a supported provider in under 5 minutes, with a clear error for each failed check.
- **SC-005**: A secondary DNS server serves a hosted-zone change within 2 minutes of the change.
- **SC-006**: Adding a new provider needs only a new manager plus its credential form; no change to record logic or other managers.
- **SC-007**: With the DNS layer disabled, existing installs behave exactly as before (same Service annotations, same endpoints).

## Assumptions

- Not v1 scope (project rule: new specs are built after v1 unless valgul says otherwise).
- Default TTL is 300 seconds for records Gameplane creates; admins may change it per zone.
- Record types needed by Gameplane itself are A, AAAA and SRV; CNAME is not used for server names.
- Provider credentials live in Kubernetes Secrets in the namespace where the managers run, like other Gameplane secrets.
- The public port for NAT or forwarding edges comes only from mapping sources Gameplane already knows (tunnels, explicit per-server public port settings); gateway route provisioning stays out of scope, as in issue #1006.
- The feature does not depend on issue #999 or #1001 (spec 036 port pools); it reads whatever port mapping results.
- Permission changes are owned by spec 039 (per-server access, in progress on branch `feat/039-per-server-access`, not yet in `specs/` on `master`); this spec adds no permission (OD-3).
- DNSSEC signing of hosted zones is out of scope for the first release.
