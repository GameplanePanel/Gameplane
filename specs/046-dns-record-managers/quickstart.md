# Quickstart: validate spec 046

Manual walk on a dev cluster (`make dev-up`), after the implementation PRs are merged. The automated version is the e2e bucket `dns` (`make test-e2e-bucket BUCKET=dns`, CI only).

## Prerequisites

- Values: `dns.enabled=true`, `dns.managers.rfc2136.enabled=true`, `dns.managers.zone.enabled=true` (plus `cloudflare`, `ovh` or `hetzner` if you have an account and a test zone).
- A BIND 9 pod in the cluster serving `test.lan` with dynamic updates allowed for a TSIG key, and able to act as secondary (the e2e bucket's fixture, `test/e2e/testdata/dns/`).
- The Minecraft Java module with its `_minecraft._tcp` declaration (FR-021).

## 1. Provider zone through RFC 2136 (US1, US2)

1. Settings → DNS zones → Add zone: `test.lan`, provider RFC 2136, server and TSIG from the fixture. Expect **Connected** (write test passed).
2. Create a Minecraft Java server, Expose NodePort, hostname `play.test.lan`.
3. Within 2 minutes the server's Networking card shows the A and SRV records as **Published**. `dig @<bind> SRV _minecraft._tcp.play.test.lan` returns `0 0 <nodePort> play.test.lan.`; `dig A play.test.lan` returns the node's external IP.
4. Join with a client (or the e2e handshake client) using only `play.test.lan`.
5. Change the port with a port override. The SRV record follows; the old port is gone after 5 minutes at most.
6. The Service still carries `external-dns.alpha.kubernetes.io/hostname: play.test.lan` (OD-1).
7. Turn **Publish to DNS** off: records and their `_gameplane-owner` markers disappear; the hint stays.
8. Add an A record for `taken.test.lan` by hand in BIND, set it as a server's hostname: the server shows **Not published: record exists and is not owned by Gameplane**; the hand record is untouched.

## 2. Hosted zone (US3)

1. Add zone `hosted.lan`, kind Hosted, SOA defaults, nameserver = BIND, secondary = BIND pod CIDR with a TSIG key.
2. Configure BIND as secondary of `hosted.lan` pointing at the zone manager Service.
3. Add an MX record in the zone detail page; publish a server at `mc.hosted.lan`.
4. BIND serves both within 2 minutes; `dig @<zone-manager> A mc.hosted.lan` is REFUSED (hidden primary); an AXFR from another pod without the key is refused and logged.
5. Restart the zone manager pod: serial and content unchanged.
6. Import a zone file into a fresh hosted zone once; a second import is refused.

## 3. Failure and visibility (US5)

1. Scale the RFC 2136 manager to 0: within 2 minutes `test.lan` shows **Manager unreachable**, servers show **Pending**, nothing is deleted.
2. Scale back: pending changes apply, zone shows **Connected**.
3. Audit log lists the zone add, credential change and record changes.

## 4. Disabled layer

Install with `dns.enabled=false`: no new pods, no `DNSRecordSet`s, Services and `status.endpoints` identical to before (SC-007).
