# Open decisions: spec 046 DNS records through provider managers

Only a decision valgul answers is written here as settled. OD-1 to OD-6 were raised on 2026-11-27 with the first spec draft and asked as decision cards. OD-1 to OD-3 are settled; OD-4 to OD-6 are open. No planning or build work starts until all are.

## OD-1: How does this live next to ExternalDNS? (SETTLED: own answer, valgul 2026-11-27)

Today the operator writes the ExternalDNS hostname hint on every Service with a hostname (`docs/networking.md`). Issue #1006 asked for ExternalDNS to keep all DNS writes.

- A. Both stay. The ExternalDNS hint is unchanged; a hostname in a zone connected to Gameplane is published by Gameplane, and for that server the operator stops writing the hint so the two never fight over one name. (Recommended: no change for current ExternalDNS users, no double writers.) Moving a name that ExternalDNS already published: Gameplane never touches a record it did not create (FR-007), so it publishes the name only once the ExternalDNS record is gone. ExternalDNS removes it itself when the hint disappears if it runs with `--policy=sync`; with `upsert-only` the admin removes it. How Gameplane guides that removal is left to planning.
- B. Gameplane's DNS layer replaces the hint: the hint is removed for every server once the layer is enabled. Simpler, but breaks setups that rely on ExternalDNS for names outside connected zones.
- C. Gameplane's layer only does SRV records and leaves address records to ExternalDNS.

**Settled (valgul, thread 2026-11-27):** "hint became a real info that publish to the manager not a simple info". The hostname stops being an ExternalDNS hint: with the DNS layer enabled, a server's effective hostname is a publish order that Gameplane sends to the central DNS service, which hands it to the zone's manager. Read as: the operator no longer writes the `external-dns.alpha.kubernetes.io/hostname` annotation once the layer is enabled, and a hostname in no connected zone is reported "Not published" instead of being handed to ExternalDNS. Options A to C above are superseded.

## OD-2: Which providers ship in the first release? (SETTLED: B, valgul 2026-11-27)

- A. Cloudflare, OVH, Hetzner, plus the hosted zone manager. (Recommended: the three providers you named, and the zone manager covers any provider with secondary zones.)
- B. A, plus RFC 2136 dynamic update (BIND, PowerDNS, Knot, Technitium and others).
- C. Cloudflare only plus the zone manager first; OVH and Hetzner in a follow-up.

**Settled (valgul, decision card 2026-11-27):** B, "Those + RFC 2136": managers for Cloudflare, OVH and Hetzner, an RFC 2136 dynamic-update manager (BIND, PowerDNS, Knot, Technitium and others), plus the hosted zone manager.

## OD-3: Who may connect and change DNS zones? (SETTLED: A, valgul 2026-11-27)

Permission changes are owned by spec 039 (per-server access, in progress on branch `feat/039-per-server-access`, not yet in `specs/` on `master`). Option A needs nothing from 039; option B adds the permission inside 039's permission model and waits for it to merge.

- A. Admins only, through the existing admin role; no new permission now. (Recommended: no clash with spec 039; a finer permission can come later through 039.)
- B. A new permission `dns:write`, added together with spec 039.
- C. Admins connect zones; any user who can edit a server's networking can turn publication on or off for that server. (This is FR-006 under any option; C only adds that nothing else is delegated.)

**Settled (valgul, decision card 2026-11-27):** A, "Admins only, no new permission": zones are connected and changed through the existing admin role; no new permission, nothing needed from spec 039.

## OD-4: Does the zone manager answer public DNS queries? (OPEN)

- A. Hidden primary: it only serves zone transfers and change notices to the allowed secondaries; the public answers come from those secondaries. (Recommended: no public DNS port to expose or defend from the cluster, works behind NAT.)
- B. Public authoritative too: it also answers queries on port 53 and can be listed as an NS of the zone.
- C. Both, chosen per zone.

## OD-5: Can admins keep their own records in a hosted zone? (OPEN)

- A. Yes: admins can add, edit and delete any record type (MX, TXT, CNAME, …) in the dashboard, and can import an existing zone file once. Gameplane-owned records cannot be edited by hand. (Recommended: a primary DNS for a whole zone has to hold more than game records.)
- B. No: a hosted zone only holds SOA, NS and Gameplane's server records; other names go in a different zone.
- C. Import a zone file only, no editing in the dashboard.

## OD-6: Where do the DNS service and managers live? (OPEN)

- A. A new split repository `GameplanePanel/dns` (submodule `dns/`, own Go module, images built from the Gameplane root, like `tunnel`), holding the central service and every manager. (Recommended: same shape as other optional components; providers' SDKs stay out of the main `go.work`.) Sessions cannot create repositories, so valgul would create it.
- B. Inside the main repository, as new Go modules (`dns/` with one sub-module per manager).
- C. The central service in the main API, managers in a new split repository.
