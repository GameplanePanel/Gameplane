# Implementation Plan: DNS records through provider managers

**Branch**: `spec-046/dns-plan` | **Date**: 2026-11-27 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/046-dns-record-managers/spec.md` (OD-1 to OD-6 settled). Planning raised OD-7 and OD-8, both settled by valgul 2026-11-27 (managers as their own pods, one hub per game cluster).

## Summary

Gameplane publishes a server's address record and its template-declared SRV records itself. The operator turns each GameServer into a desired record set (a new namespaced `DNSRecordSet` resource) from the endpoints it already reconciles. A new **DNS hub** (the central DNS service) watches `DNSZone` and `DNSRecordSet` resources, picks the zone by longest suffix, checks the names it does not own, and sends create, update and delete calls to the **manager** that serves the zone: one manager per provider type (Cloudflare, OVH, Hetzner, RFC 2136) plus the **zone manager**, a hidden primary that holds hosted zones and serves AXFR/IXFR with NOTIFY and TSIG to allowed secondaries. Results flow back through `DNSRecordSet.status` into `GameServer.status.dns`, which the API and dashboard read. The ExternalDNS hint stays on every Service (OD-1). Hub and managers live in the new split repository `GameplanePanel/dns` (submodule `dns/`, OD-6); CRD types, operator changes, API routes, dashboard and chart live in Gameplane.

## Technical Context

**Language/Version**: Go 1.27 (hub, managers, operator, API); TypeScript strict + React 19 (dashboard).

**Primary Dependencies**: controller-runtime (operator, hub); `github.com/libdns/libdns` with `libdns/cloudflare`, `libdns/ovh`, `libdns/hetzner`, `libdns/rfc2136` for provider managers (research R3); `github.com/miekg/dns` for the zone manager (AXFR, IXFR, NOTIFY, TSIG; research R4); existing `api/internal/{audit,rbac}`; HeroUI v3 + TanStack Query (dashboard).

**Storage**: Kubernetes resources only: `DNSZone` (cluster-scoped), `DNSRecordSet` (namespaced), credential and TSIG Secrets in the release namespace. Hosted zone content and serial live in those resources, so a zone manager restart loses nothing (FR-018). No panel database change.

**Testing**: unit tests in `dns/` with fake providers (recorded HTTP fixtures per provider); envtest for the operator's record-set builder and the hub reconciler; e2e bucket `dns` on Kind with an in-cluster BIND secondary (RFC 2136 target and AXFR secondary). Cloud provider e2e tests are written and skipped unless their credentials are present in CI (never withdrawn).

**Target Platform**: Kubernetes (k3s single node and multi-node), amd64 and arm64 images.

**Project Type**: Kubernetes operator + controller + web dashboard (existing monorepo) plus one new split component repository.

**Performance Goals**: records match a change within 2 minutes in 95% of changes (SC-002); hosted-zone secondaries serve a change within 2 minutes (SC-005).

**Constraints**: never touch a record Gameplane did not create (FR-007); never publish a guessed port (FR-004); credentials never leave the manager that uses them (FR-012); disabled layer = no behaviour change (FR-026).

**Scale/Scope**: up to a few hundred servers and a few dozen zones per cluster; 5 managers in the first release.

## Constitution Check

*GATE: checked before Phase 0 and again after Phase 1.*

| Principle | Status | How |
|---|---|---|
| I. E2E-tested delivery | Pass | New e2e bucket `dns`: hosted zone + BIND secondary AXFR, RFC 2136 publish/update/withdraw, Minecraft Java join through SRV lookup with a real handshake client. Cloud provider e2e tests skip without credentials. |
| II. Design-first UI | Pass | Admin "DNS zones" settings page, zone detail with hosted records, and the server Networking DNS card are designed in `design.fig` and exported before any React work. |
| III. Language best practice | Pass | Go errors wrapped with `%w`; CRD edits followed by `make generate && make manifests`; no lint suppressions. |
| IV. Spec-driven | Pass | Spec merged (#1125), plan here, tasks next. `dns/specs.md` written with the component; `operator/specs.md` and `api` docs updated in the same changes. |
| V. Delegation | Pass | Implementation goes through workflows per CLAUDE.md rule 13. |
| VI / VII (security, simplicity) | Pass with note | New component holds third-party credentials: per-manager ServiceAccount, Secrets read only by managers, hub has no Secret access, mTLS between hub and managers. See Complexity Tracking for the separate controller. |

Post-design re-check: still passes; the only deviation is recorded below.

## Project Structure

### Documentation (this feature)

```text
specs/046-dns-record-managers/
├── spec.md
├── OPEN-DECISIONS.md
├── plan.md              # this file
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── crds.md          # DNSZone, DNSRecordSet, GameServer/GameTemplate additions
│   ├── manager-api.md   # hub <-> manager protocol
│   ├── rest-api.md      # panel API routes for zones and hosted records
│   └── helm-values.md   # dns.* chart values
├── checklists/requirements.md
└── tasks.md             # /speckit-tasks
```

### Source Code

```text
operator/api/v1alpha1/
├── dnszone_types.go            # new: DNSZone (cluster-scoped)
├── dnsrecordset_types.go       # new: DNSRecordSet (namespaced)
├── gameserver_types.go         # + spec.networking.dns, status.dns
└── gametemplate_types.go       # + spec.srv[]
operator/internal/controller/
├── gameserver_dns.go           # builds the desired DNSRecordSet from status.endpoints
└── gameserver_dns_envtest_test.go
operator/config/{crd,rbac}/, charts/gameplane/charts/gameplane-crds/crds/, charts/gameplane/crd-manifests/   # generated

dns/                            # SUBMODULE GameplanePanel/dns (own go.mod, not in go.work)
├── cmd/hub/                    # central DNS service (controller-runtime)
├── cmd/manager/                # one binary, --provider=cloudflare|ovh|hetzner|rfc2136|zone
├── internal/hub/               # zone routing, ownership, conflict checks, queue, status
├── internal/managerapi/        # shared protocol types + mTLS client/server
├── internal/providers/{cloudflare,ovh,hetzner,rfc2136}/   # libdns adapters
├── internal/zone/              # hidden primary: zone build, serial, AXFR/IXFR, NOTIFY, TSIG
├── Dockerfile.hub, Dockerfile.manager
└── specs.md

api/internal/handlers/dns_zones.go        # admin zone CRUD, test, hosted records, import
api/internal/handlers/dns_zones_envtest_test.go
web/src/routes/settings.dns.tsx           # admin zones page (after design)
web/src/routes/settings.dns.$zone.tsx     # zone detail + hosted records
web/src/components/server/DnsRecordsCard.tsx
charts/gameplane/templates/dns.yaml       # hub, managers, RBAC, NetworkPolicies, mTLS
modules (GameplanePanel/module): minecraft-java, minecraft-proxy template.yaml + srv
gp-module/                                # validate srv declarations
test/e2e/dns_e2e_test.go                  # bucket "dns"
docs/networking.md, docs/dns.md, website docs page
```

**Structure Decision**: CRD types stay in `operator/api/v1alpha1` (the single CRD source and chart sync path). The hub and managers are a split component like `tunnel`: own repository and Go module, images built from the Gameplane root. The hub reads the two CRDs through its own minimal Go types (as `mcp-server` does) so it does not import the operator module.

## Phases

- **Phase 0 (research.md)**: R1 where orders come from, R2 hub placement per cluster (OD-8), R3 provider library, R4 zone manager library and hidden-primary setup, R5 manager process shape (OD-7), R6 ownership marker, R7 address and port resolution per exposure mode, R8 NAT/forwarding mapping source, R9 hub-manager transport, R10 testing without provider accounts.
- **Phase 1**: `data-model.md`, `contracts/*`, `quickstart.md`.
- **Phase 2** (`/speckit-tasks`): stacked PRs, in order: (A) CRD types + operator record-set builder; (B) `GameplanePanel/dns` hub + zone manager + RFC 2136 manager, then submodule + chart; (C) Cloudflare, OVH, Hetzner managers; (D) API routes + audit; (E) design pass; (F) dashboard; (G) modules SRV declarations + gp-module validation; (H) e2e bucket, docs, changelog.

## Complexity Tracking

| Deviation | Why needed | Simpler alternative rejected because |
|---|---|---|
| A reconciler outside `operator/internal/controller/` (the DNS hub) holds DNS business logic, against CLAUDE.md core rule 10's "business logic lives in reconcilers" in the operator | OD-6 puts the central DNS service in its own repository; it is still a Kubernetes reconciler of CRDs, and the operator stays the authority for what a server's records are | Putting the hub in the operator would pull four provider SDKs into the operator image and give the operator provider credentials, which FR-012 keeps inside managers |
| A new split repository and 2 images | OD-6 (settled) | n/a |
