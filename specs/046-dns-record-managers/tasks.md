# Tasks: DNS records through provider managers (spec 046)

**Input**: `specs/046-dns-record-managers/` (spec.md, plan.md, research.md, data-model.md, contracts/, quickstart.md). All decisions OD-1 to OD-8 settled.

**Tests**: required by the constitution (Principle I: e2e for every feature, real game join) and the coverage gates in CLAUDE.md. Test tasks are listed with each story. Every test change needs valgul's sign-off before it is pushed (CLAUDE.md override 1) and a fresh-agent review of only the test diff. Tests run in CI only (CLAUDE.md rule 8).

**Format**: `- [ ] Tnnn [P] [USn] description with path`. `[P]` = different files, no dependency on an unfinished task. Paths under `dns/` are in the new repository `GameplanePanel/dns` (submodule `dns/`).

**PR stack** (one thread = one branch per PR, all target `master`): A = CRDs + operator (T005-T009, T011-T013, T017-T021, T056), B = `GameplanePanel/dns` hub + zone manager + RFC 2136 (T002, T014-T016, T022-T032, T036, T045-T049, T057), C = cloud managers (T033-T035), D = submodule + images + chart (T003, T004, T037-T040, T050), E = API (T041-T043, T051, T052), F = design (T058), G = dashboard (T010, T044, T059-T063), H = modules + gp-module (T053-T055), I = e2e + docs + changelog (T064-T070). T001 (valgul) comes first; T071 after everything merges.

## Phase 1: Setup

- [ ] T001 valgul creates the repository `GameplanePanel/dns` (default branch `master`, rulesets copied from `GameplanePanel/tunnel`); sessions cannot create repositories. Blocks every `dns/` task.
- [ ] T002 Scaffold `dns/`: `go.mod` (`module github.com/GameplanePanel/gameplane/dns`, go 1.27.0), `README.md`, `CLAUDE.md` and `AGENTS.md` modelled on `tunnel/CLAUDE.md`, `.github/workflows/ci.yaml` calling Gameplane's shared `go-component.yaml` pinned by SHA, `.testcoverage.yml` (threshold 80), `dependabot.yml`.
- [ ] T003 Add the submodule in Gameplane: `.gitmodules` entry `dns` with `component = true`, the path filters `hack/submodules.sh check` asks for, and `dns` in the split list of `hack/submodules.sh init`.
- [ ] T004 [P] Add image targets `image-dns-hub` and `image-dns-manager` to `Makefile` (pattern of `image-tunnel-frp`, line 356), and both images to `.github/workflows/publish-edge.yaml` and `.github/workflows/release.yaml` with cosign signing like the tunnel images.

## Phase 2: Foundational (blocks every story)

- [ ] T005 Add `DNSZone` (cluster-scoped, short name `dnsz`) to new `operator/api/v1alpha1/dnszone_types.go` per contracts/crds.md, with CEL rules: `hosted` required iff `kind=Hosted`, `provider` + `credentialsSecretRef` required iff `kind=Provider`, `zone` and `kind` immutable, `defaultTTL` 60..86400.
- [ ] T006 Add `DNSRecordSet` (namespaced, short name `dnsrs`) to new `operator/api/v1alpha1/dnsrecordset_types.go` per contracts/crds.md (record type rules per owner kind, max 64 records, no SOA).
- [ ] T007 Add `spec.networking.dns` (`publish` *bool default true, `publicMappings[]`) and `status.dns` (`reason`, `records[]`) to `operator/api/v1alpha1/gameserver_types.go`; condition type constant `DNSPublished`.
- [ ] T008 Add `spec.srv[]` (`service`, `protocol`, `port`, `priority`, `weight`) to `operator/api/v1alpha1/gametemplate_types.go` with CEL: port names an advertised `spec.ports[]` entry, `(service, protocol)` unique, max 8, service matches `^[a-z0-9]([a-z0-9-]{0,13}[a-z0-9])?$`.
- [ ] T009 Run `make generate && make manifests`; commit `zz_generated.deepcopy.go`, `operator/config/{crd,rbac}/`, `charts/gameplane/charts/gameplane-crds/crds/`, `charts/gameplane/crd-manifests/` (CLAUDE.md rule 7).
- [ ] T010 [P] Mirror the new GameServer/GameTemplate fields and `DNSZone`/`DNSRecordSet` types in `web/src/types.ts`.
- [ ] T011 [P] CRD validation e2e cases (bad SRV port name, duplicate service, hosted without SOA, provider without secret ref) in `test/e2e/crd_validation_e2e_test.go` (needs sign-off, test change).
- [ ] T012 Operator flag `--dns-enabled` in `operator/cmd/main.go`, wired to a `DNSEnabled bool` on `GameServerReconciler` in `operator/internal/controller/gameserver_controller.go`; when false nothing below runs (FR-026).
- [ ] T013 RBAC markers so the operator can create/update/delete `dnsrecordsets` and read `dnszones` (regenerate with `make manifests`).
- [ ] T014 [P] `dns/internal/managerapi/`: request/response types, error codes, mTLS client and server helpers per contracts/manager-api.md, with unit tests `dns/internal/managerapi/*_test.go`.
- [ ] T015 [P] `dns/internal/crd/`: minimal Go types and a controller-runtime scheme for `DNSZone` and `DNSRecordSet` (JSON-compatible with T005/T006; no import of the operator module, as `mcp-server` does).
- [ ] T016 [P] `dns/internal/owner/`: build and parse the `_gameplane-owner.<name>` TXT marker `v=1;cluster=..;ref=..;type=..` (research R6) with unit tests.

**Checkpoint**: CRDs installed, empty hub/manager libraries compile (`GOWORK=off go build ./...` in `dns/`).

## Phase 3: User Story 1 — Players connect by name without a port (P1) 🎯 MVP

**Goal**: a NodePort/LoadBalancer Minecraft Java server with `play.<zone>` gets A + SRV records with the reachable port, updated and withdrawn with the server.

**Independent test**: quickstart §1 steps 2-8 against the RFC 2136 manager and BIND.

### Operator (record orders)

- [ ] T017 [US1] `operator/internal/controller/gameserver_dns.go`: `desiredDNSRecords(gs, tmpl, endpoints, nodeIP)` implementing research R7 and R8 (table per exposure mode; publicMappings win; Tailscale and ClusterIP never; no guessed values, return a reason instead, FR-004).
- [ ] T018 [US1] Same file: create/update/delete the `DNSRecordSet` named after the GameServer with an owner reference; delete it when hostname is cleared, `publish=false`, or no record is publishable (FR-005, FR-006, FR-007a); call it from `reconcileStatus` in `operator/internal/controller/gameserver_status.go` after endpoints are computed. The ExternalDNS hint in `gameserver_controller.go` stays untouched (OD-1).
- [ ] T019 [US1] Mirror `DNSRecordSet.status.records` into `GameServer.status.dns.records` and set the `DNSPublished` condition; watch `DNSRecordSet` in the GameServer controller setup.
- [ ] T020 [P] [US1] Unit tests for `desiredDNSRecords` per exposure mode in `operator/internal/controller/gameserver_dns_test.go` (LB IP, LB hostname, NodePort, Hostport, frp, playit, tailscale, ClusterIP, publicMappings, missing node IP).
- [ ] T021 [US1] Envtest `operator/internal/controller/gameserver_dns_envtest_test.go`: record set created, follows a port override, deleted on publish=false and on GameServer delete, hint still on the Service.

### Hub

- [ ] T022 [US1] `dns/cmd/hub/main.go` + `dns/internal/hub/reconciler.go`: controller-runtime manager watching `DNSZone` and `DNSRecordSet`; finalizer `gameplane.local/dns-withdraw` on record sets.
- [ ] T023 [US1] `dns/internal/hub/route.go`: longest-suffix zone selection across `DNSZone`s; reason "Not published: no connected zone for <name>" when none (US2 scenario 3).
- [ ] T024 [US1] `dns/internal/hub/apply.go`: per record, read current records via the manager (`GET .../records`), apply the rules in spec Edge Cases: owned → update; unowned A/AAAA all matching → `Present` and still publish SRV; any unowned mismatch → `NotPublished`; shared hostname rules and ownership hand-over between servers with the same address; write marker with every create; on read failure leave records and set `Pending` (FR-010).
- [ ] T025 [US1] `dns/internal/hub/queue.go`: per-zone ordered queue with backoff and `retryAfterSeconds`; nothing is deleted while a manager is unreachable (FR-013); idempotent PUTs (FR-014).
- [ ] T026 [US1] `dns/internal/hub/status.go`: write `DNSRecordSet.status.records` and `DNSZone.status` (`ownedRecords`, `lastSyncTime`).
- [ ] T027 [P] [US1] Hub unit tests with a fake manager in `dns/internal/hub/*_test.go` (routing, conflicts, Present, shared name hand-over, withdraw order, queue while down).

### RFC 2136 manager (first provider, used by e2e)

- [ ] T028 [US1] `dns/cmd/manager/main.go`: `--provider` flag, loads zones of its type from `DNSZone`s, reads credential Secrets labelled `gameplane.io/dns-credentials=<provider>` only, serves contracts/manager-api.md on 8443 with mTLS.
- [ ] T029 [US1] `dns/internal/providers/rfc2136/`: libdns `rfc2136` adapter (research R3) for records, owned list (by marker), put, delete-with-marker-check.
- [ ] T030 [P] [US1] Adapter tests against an in-process `miekg/dns` server accepting TSIG updates in `dns/internal/providers/rfc2136/*_test.go`.

**Checkpoint**: MVP: one provider end to end.

## Phase 4: User Story 2 — Admin connects zones at different providers (P1)

**Goal**: admins add/test/edit/disconnect zones at Cloudflare, OVH, Hetzner and RFC 2136; clear errors; secrets never shown.

**Independent test**: quickstart §1 step 1 plus two zones at two providers (cloud ones need accounts).

- [ ] T031 [US2] Zone check in the hub: on `DNSZone` create/update or the test annotation, call `POST /v1/zones/{zone}/check` and map error codes to `status.state` (`dns/internal/hub/zones.go`).
- [ ] T032 [US2] Zone deletion in the hub: finalizer withdraws every owned record unless `spec.paused` ("keep records").
- [ ] T033 [P] [US2] `dns/internal/providers/cloudflare/` libdns adapter + recorded-fixture tests.
- [ ] T034 [P] [US2] `dns/internal/providers/ovh/` libdns adapter + recorded-fixture tests.
- [ ] T035 [P] [US2] `dns/internal/providers/hetzner/` adapter: confirm `libdns/hetzner` targets Hetzner's current Cloud DNS API (research R3), else write it against that API; recorded-fixture tests.
- [ ] T036 [P] [US2] `dns/specs.md`: how the hub and managers work, the protocol, ownership marker, adding a provider (SC-006).
- [ ] T037 [US2] Chart `charts/gameplane/templates/dns.yaml` per contracts/helm-values.md: hub Deployment/ServiceAccount (no Secret access) + ClusterRole; one Deployment, ServiceAccount, Role and Service per enabled manager (OD-7); NetworkPolicies; mTLS certs via `mtls.yaml` helpers; `--dns-enabled` passed to the operator.
- [ ] T038 [P] [US2] `charts/gameplane/values.yaml` + `values.schema.json` for `dns.*`, defaults off.
- [ ] T039 [P] [US2] Chart render checks in the existing chart validation (`charts/gameplane/templates/validate.yaml` pattern): manager enabled without hub enabled fails; zone serviceType values.
- [ ] T040 [US2] Submodule pointer bump `dns` in Gameplane after the B and C PRs merge (`submodule-bump` skill).
- [ ] T041 [US2] API `api/internal/handlers/dns_zones.go`: `GET/POST/PATCH/DELETE /api/v1/dns/zones`, `POST .../test` per contracts/rest-api.md; Secret created with the provider label; multi-cluster create (`clusters[]`); hosted zone single-cluster check; admin-only; audit events (FR-022, FR-025).
- [ ] T042 [US2] Mount the routes in `api/cmd/main.go` with the admin RBAC middleware.
- [ ] T043 [US2] Envtest `api/internal/handlers/dns_zones_envtest_test.go`: create, credentialsSet never leaks values, non-admin 403, delete with keep_records.
- [ ] T044 [P] [US2] Client methods in `web/src/lib/api.ts`.

## Phase 5: User Story 3 — Gameplane is the primary DNS for a zone (P2)

**Goal**: hosted zones served as a hidden primary to allowed secondaries, admin records and one-time import.

**Independent test**: quickstart §2.

- [ ] T045 [US3] `dns/internal/zone/build.go`: build the full zone (SOA + NS from `DNSZone.spec.hosted` + every routed record), serial `YYYYMMDDnn` from `DNSZone.status.serial`, journal of the last 20 changes.
- [ ] T046 [US3] `dns/internal/zone/server.go`: `miekg/dns` server on 53 TCP/UDP; AXFR and IXFR (journal, AXFR fallback) only for source in `secondaries[].address` with valid TSIG; everything else REFUSED and logged (FR-016, FR-019).
- [ ] T047 [US3] `dns/internal/zone/notify.go`: NOTIFY every secondary on serial increase (FR-017).
- [ ] T048 [US3] Hub side: for Hosted zones, send `PUT /v1/hosted/{zone}` after each batch and persist serial + journal in `DNSZone.status` (FR-018).
- [ ] T049 [P] [US3] Zone manager tests in `dns/internal/zone/*_test.go`: AXFR with and without key, IXFR from journal, serial monotonic across restart, REFUSED for normal queries.
- [ ] T050 [US3] Chart: zone manager Service on 53 (serviceType, loadBalancerIP, nodePort) in `charts/gameplane/templates/dns.yaml`.
- [ ] T051 [US3] API hosted records `GET/POST/PUT/DELETE /api/v1/dns/zones/{name}/records` and `POST .../import` (1 MiB, once, SOA/apex NS ignored, server-owned records 403) writing the zone's Admin `DNSRecordSet` in `api/internal/handlers/dns_zones.go` (FR-019a).
- [ ] T052 [US3] Envtest for hosted records and import in `api/internal/handlers/dns_zones_envtest_test.go`.

## Phase 6: User Story 4 — Module authors declare SRV services (P2)

**Goal**: templates declare SRV; validation catches bad references.

**Independent test**: `gp-module validate` passes on a valid declaration and fails naming the missing port.

- [ ] T053 [P] [US4] `gp-module/internal/validator/`: validate `spec.srv[]` (port exists and is advertised, service name, protocol, unique pair, max 8) with tests.
- [ ] T054 [P] [US4] `docs/module-authoring.md`: document `spec.srv`.
- [ ] T055 [US4] In `GameplanePanel/module`: add `srv: [{service: minecraft, protocol: tcp, port: game}]` to `minecraft-java/template.yaml` and `minecraft-proxy/template.yaml`, README notes; PR there, then pointer bump `modules` in Gameplane with its changelog entry (FR-021).
- [ ] T056 [US4] Operator uses the server's effective template revision for `spec.srv` (pinned revisions keep their own declaration) in `operator/internal/controller/gameserver_dns.go`; envtest case in `gameserver_dns_envtest_test.go`.

## Phase 7: User Story 5 — Admin sees DNS health (P3) and the dashboard

**Goal**: zone state and per-server record state visible in the dashboard.

**Independent test**: quickstart §3.

- [ ] T057 [US5] Hub health loop: `GET /v1/health` every 30 s, `ManagerUnreachable` after 2 min, back to `Connected` and drain the queue on return (`dns/internal/hub/health.go`).
- [ ] T058 [US5] Design pass in `design.fig` (CLAUDE.md rule 1, via OpenPencil): Admin Settings → DNS zones section (list, add-zone dialog per provider and Hosted, zone detail with hosted records and import), server Networking DNS card (records, states, reasons, ExternalDNS hand-over steps), "Publish to DNS" toggle; export with `hack/design/fig-export.ts`; valgul approves before T059.
- [ ] T059 [US5] DNS zones section in `web/src/routes/AdminSettings.tsx` with components in new `web/src/components/dns/` (ZoneList, AddZoneDialog, ZoneDetail, HostedRecordsTable, ImportZoneDialog), from the approved design.
- [ ] T060 [US5] `web/src/components/dns/DnsRecordsCard.tsx` on the server Networking tab, plus the publish toggle in the networking settings form.
- [ ] T061 [P] [US5] Web tests `web/src/components/dns/*.test.tsx` and `web/src/routes/AdminSettings_dns.test.tsx` (coverage gates).
- [ ] T062 [P] [US5] i18n strings for every new UI text in the shared language packs (`i18n/`), passing `check-i18n`.
- [ ] T063 [US5] Browser smoke test of the new screens (CLAUDE.md rule 13, Chrome MCP at sonnet) at 1440 and 390, light and dark.

## Phase 8: Polish & cross-cutting

- [ ] T064 E2E bucket `dns` in `test/e2e/buckets.sh` and `test/e2e/dns_e2e_test.go`: BIND fixture in `test/e2e/testdata/dns/`, RFC 2136 publish/update/withdraw, unowned-record refusal, hint kept, hosted zone AXFR to BIND + REFUSED for others + restart keeps serial, manager down keeps records (needs sign-off, test change).
- [ ] T065 E2E Minecraft Java join through the SRV record resolved via the BIND secondary, using the existing handshake client (Principle I real join) in `test/e2e/dns_e2e_test.go`.
- [ ] T066 [P] E2E tests for Cloudflare, OVH, Hetzner that `t.Skip` unless `DNS_E2E_<PROVIDER>_*` secrets are set (written, never withdrawn) in `test/e2e/dns_providers_e2e_test.go`.
- [ ] T067 [P] `docs/networking.md`: hostname section explains the hint stays and the DNS layer publishes; new `docs/dns.md` (providers, exposure modes and mapping sources, hosted-zone secondary setup, ExternalDNS hand-over); `docs/security.md` credentials and TSIG notes; `docs/architecture.md` component table (FR-027).
- [ ] T068 [P] `operator/specs.md` record-set builder section; `README.md` features list and `check-features-sync` inputs.
- [ ] T069 [P] Website docs page for DNS in `GameplanePanel/website` (design-first per website rules) and pointer bump.
- [ ] T070 Changelog entries `changelog/unreleased/<PR>-dns-*.md` in each user-visible PR (CRD fields, Helm values, API routes, modules SRV).
- [ ] T071 Run quickstart.md end to end on a dev cluster (valgul, manual walk), then archive per CLAUDE.md rule 16.

## Dependencies

- T001 → every `dns/` task. Phase 2 → all stories.
- US1 (T017-T030) is the MVP and blocks US2's API/UI only for shared types; US2 cloud managers (T033-T035) are independent of each other.
- US3 depends on the hub (T022-T026). US4 T056 depends on T017. US5 UI (T059-T061) waits for the approved design T058 and the API (T041, T051).
- E2E T064-T065 need US1 + US3 + the chart (T037, T050).

## Parallel examples

- Phase 2: T010, T011, T014, T015, T016 together after T009.
- US1: T020 and T027 and T030 (tests) alongside their implementations' review.
- US2: T033, T034, T035, T036, T038, T039, T044 together.
- US4: T053 and T054 together.

## Implementation strategy

MVP = Phase 1 + 2 + US1 (RFC 2136 only) → verify with the e2e RFC 2136 path. Then US2 (cloud managers, API), US3 (hosted zones), US4 (modules), US5 (dashboard after design), Polish. Each PR follows the PR-ready rules in project memory (green CI, no conflicts, review items fixed, full Claude review).
