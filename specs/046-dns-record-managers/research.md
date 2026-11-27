# Research: spec 046 DNS records through provider managers

Each entry: Decision, Rationale, Alternatives. Entries marked (OD-n) depend on an open decision in `OPEN-DECISIONS.md` and follow its recommended option until valgul answers.

## R1: Who sends record orders?

**Decision**: The operator. For each GameServer it builds a `DNSRecordSet` (same namespace, owner reference to the GameServer) from what it already reconciles: the effective hostname (`spec.networking.hostname`, else `operator.templateHostnames`), `status.endpoints`, the effective template revision's `spec.srv`, and `spec.networking.dns`. The ExternalDNS hint code (`operator/internal/controller/gameserver_controller.go`, `externalDNSHostnameAnnotation`) is untouched (OD-1).

**Rationale**: CLAUDE.md rule 10 keeps "what this server's records are" in the operator, which already owns endpoint resolution (`gameserver_status.go`, `endpointsFromService`, `nodePortExternalIP`). A CRD is the Kubernetes-native order queue (rule 9): durable, watchable, idempotent by construction (FR-014).

**Alternatives**: the API sends orders (rejected: API never bypasses reconciliation, and cannot see allocations first); the hub reads GameServers directly (rejected: duplicates endpoint logic outside the operator).

## R2: One hub per cluster or one for the whole panel? (OD-8)

**Decision (recommended)**: One hub per cluster that runs game servers, installed by the chart with `dns.enabled=true`. `DNSZone` is cluster-scoped in that cluster. The dashboard's DNS settings page lists zones per cluster and can copy a zone (with its credentials) to several clusters at once.

**Rationale**: matches the federation model (`docs/architecture.md`, "Multi-cluster"): each target cluster runs its own operator; standalone panels have no Kubernetes of their own, so a panel-level hub would need a new storage and a reach into every remote cluster's status. Ownership markers carry the cluster ID (R6) so two clusters publishing into the same zone never touch each other's records.

**Alternatives**: one hub with the panel reading orders from every cluster through the API's client pool (one set of credentials, but status written back by a non-operator across clusters, and nothing for standalone panels without new storage).

## R3: Provider library

**Decision**: `github.com/libdns/libdns` interfaces (`RecordGetter`, `RecordAppender`, `RecordSetter`, `RecordDeleter`) with `libdns/cloudflare`, `libdns/ovh`, `libdns/hetzner` and `libdns/rfc2136`. Each manager is a thin adapter from the manager protocol (contracts/manager-api.md) to libdns.

**Rationale**: one interface for all four providers keeps managers small and makes adding a provider "new adapter + credential fields" (SC-006). Widely used (Caddy, certmagic).

**Check during build**: Hetzner moved DNS from the DNS Console API to the Cloud API in 2025; confirm the `libdns/hetzner` version targets the current API, else write the adapter against Hetzner's Cloud DNS API directly. Pin versions that are at least two weeks old.

**Alternatives**: ExternalDNS provider packages (heavy, tied to its registry model); provider SDKs directly (four different shapes).

## R4: Zone manager (hidden primary)

**Decision**: `github.com/miekg/dns` server on TCP/UDP 53 inside the pod, answering only AXFR/IXFR (and SOA queries from allowed secondaries), TSIG required (HMAC-SHA256), source address also checked against the zone's allowed list. NOTIFY sent to every secondary when the serial increases. Serial format `YYYYMMDDnn`, stored in `DNSZone.status.serial`, increased once per applied batch. IXFR served from a bounded change journal kept in `DNSZone.status` history (last 20 serials), falling back to AXFR. Exposed through its own Service (type chosen by the admin: LoadBalancer or NodePort) because secondaries outside the cluster must reach it. Everything else (normal queries) gets REFUSED (FR-019).

**Rationale**: miekg/dns is the standard Go DNS library with transfer and TSIG support; hidden primary was settled (OD-4).

**Alternatives**: running Knot/BIND as the primary and writing zone files (another daemon and config format to manage); CoreDNS with the transfer plugin (no TSIG on outgoing transfer per zone in a clean way).

## R5: Manager process shape (OD-7, settled A)

**Decision**: each enabled manager type is its own Deployment (one replica) with its own ServiceAccount; the hub is a separate Deployment whose ServiceAccount has no access to Secrets. A manager's Role can `get` Secrets in the release namespace that carry the label `gameplane.io/dns-credentials=<provider>` (enforced by the manager when it reads, since RBAC cannot filter by label; the Role is namespace-scoped and the hub cannot read any).

**Rationale**: FR-012 says credentials never reach the hub; Kubernetes gives one ServiceAccount per pod, so sidecars in the hub pod would share access. The zone manager needs its own Service on port 53. One broken manager does not restart the hub or the others (FR-013).

**Alternatives**: managers as sidecar containers in the hub pod (valgul's original wording; credentials mounted per container as Secret volumes, so adding a zone restarts the pod, and all containers share one ServiceAccount).

## R6: Ownership marker

**Decision**: for every record Gameplane creates, a companion TXT record at `_gameplane-owner.<name>` with value `v=1;cluster=<cluster-id>;ref=<namespace>/<gameserver>;type=<A|AAAA|SRV>`. A record counts as Gameplane's only if the marker exists and names this cluster and server. In hosted zones the marker is not published; ownership is the `DNSRecordSet` owner field.

**Rationale**: survives a lost cluster state (FR-007) the same way ExternalDNS's TXT registry does, and the prefix keeps the marker from clashing with ExternalDNS's own TXT records.

**Alternatives**: provider-side comments/tags (Cloudflare has comments, OVH and RFC 2136 have none).

## R7: Address and port per exposure mode

**Decision** (FR-003/FR-004), resolved by the operator from reconciled objects:

| Exposure | A/AAAA value | SRV port |
|---|---|---|
| LoadBalancer with an IP in `status.loadBalancer.ingress` | that IP (`serviceAddress`) | `Service.spec.ports[].port` (`status.endpoints[].port`) |
| LoadBalancer whose ingress is a hostname only | not published ("address is a hostname") | published with the hostname as SRV target only if the admin set the server hostname equal to that ingress hostname; otherwise not published |
| NodePort | the selected node's external IP (`nodePortExternalIP`); none known → not published | the allocated `nodePort` |
| Hostport | the node's external IP of the pod's node | the host port |
| frp / playit tunnel endpoint | the relay endpoint's IP if it is an IP, else not published (CNAME is not used) | the tunnel endpoint's public port |
| Tailscale | never published (private) | never published |
| Explicit public mapping (R8) | the mapping's address | the mapping's public port |
| ClusterIP | not published ("not reachable from outside") | not published |

**Rationale**: every value comes from a reconciled object or an explicit admin setting, never from the container port or a pool annotation (issue #1006).

## R8: NAT and forwarding edges

**Decision**: a new optional list `spec.networking.dns.publicMappings[]` (`port` name, `address`, `publicPort`) on the GameServer, set by the server owner when a router or edge forwards `address:publicPort` to the server. When set for a port, it wins over R7 for that port. Gateway route provisioning stays out of scope.

**Rationale**: the issue asks for "an explicit or authoritative mapping"; Gameplane has no NAT source today, so an explicit field is the only honest one.

## R9: Hub to manager transport

**Decision**: HTTP/JSON over mTLS on port 8443, certificates issued by the chart's existing mTLS setup (`charts/gameplane/templates/mtls.yaml`), NetworkPolicy allowing only hub → manager. Protocol in contracts/manager-api.md.

**Rationale**: same pattern as other in-cluster component links; JSON keeps the contract readable and testable without code generation.

## R10: Testing without provider accounts

**Decision**: per-provider adapter unit tests against recorded HTTP fixtures (`httptest` servers replaying the provider's documented responses); the e2e bucket `dns` runs BIND 9 in Kind as an RFC 2136 target and as the AXFR secondary of a hosted zone, and resolves the SRV record through it before a Minecraft Java join with the existing handshake client. Cloudflare/OVH/Hetzner e2e tests exist and `t.Skip` unless `DNS_E2E_<PROVIDER>_*` CI secrets are set.

**Rationale**: constitution I (real join, e2e) without paid accounts in every CI run; skipped, never withdrawn (project rule).
