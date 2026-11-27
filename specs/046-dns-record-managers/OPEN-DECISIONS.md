# Open decisions: spec 046 DNS records through provider managers

Only a decision valgul answers is written here as settled. OD-1 to OD-6 were raised on 2026-11-27 with the first spec draft and asked as decision cards. No decision is settled yet; no planning or build work starts until all are.

## OD-1: How does this live next to ExternalDNS? (OPEN)

Today the operator writes the ExternalDNS hostname hint on every Service with a hostname (`docs/networking.md`). Issue #1006 asked for ExternalDNS to keep all DNS writes.

- A. Both stay. The ExternalDNS hint is unchanged; a hostname in a zone connected to Gameplane is published by Gameplane, and for that server the operator stops writing the hint so the two never fight over one name. (Recommended: no change for current ExternalDNS users, no double writers.)
- B. Gameplane's DNS layer replaces the hint: the hint is removed for every server once the layer is enabled. Simpler, but breaks setups that rely on ExternalDNS for names outside connected zones.
- C. Gameplane's layer only does SRV records and leaves address records to ExternalDNS.

## OD-2: Which providers ship in the first release? (OPEN)

- A. Cloudflare, OVH, Hetzner, plus the hosted zone manager. (Recommended: the three providers you named, and the zone manager covers any provider with secondary zones.)
- B. A, plus RFC 2136 dynamic update (BIND, PowerDNS, Knot, Technitium and others).
- C. Cloudflare only plus the zone manager first; OVH and Hetzner in a follow-up.

## OD-3: Who may connect and change DNS zones? (OPEN)

Permission changes are owned by spec 039.

- A. Admins only, through the existing admin role; no new permission now. (Recommended: no clash with spec 039; a finer permission can come later through 039.)
- B. A new permission `dns:write`, added together with spec 039.
- C. Admins connect zones; any user who can edit a server's networking can turn publication on or off for that server. (This is FR-006 under any option; C only adds that nothing else is delegated.)

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
