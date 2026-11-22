# Implementation Plan: CurseForge and FTB modpacks for Minecraft

**Branch**: `spec-035/curseforge-modpacks` | **Date**: 2026-11-16 | **Spec**: [spec.md](./spec.md)

**Input**: Feature specification from `specs/035-curseforge-modpacks/spec.md`

## Summary

Make the Modpacks tab install CurseForge and FTB packs next to Modrinth, pinned to a chosen
version, with an Active modpack card (change version, remove), an optional backup before
every pack change, and a list of files that need a manual download. The game image does the
installing (itzg `AUTO_CURSEFORGE`, `FTBA`, `MODRINTH`). The API stops patching `spec.env`
and writes a new `GameServer.spec.modpack`; the operator renders the pack env, runs the
backup-then-apply state machine, empties the mods folder on removal, and, only when an admin
opts in, gives the game container an env var (`CF_API_KEY`) from a server-owned copy of the CurseForge key via `valueFrom.secretKeyRef`. A new keyless FTB
registry engine feeds search and versions. See [research.md](./research.md).

## Technical Context

**Language/Version**: Go 1.26 (operator, api, agent unchanged), TypeScript strict + React 19 (web)

**Primary Dependencies**: controller-runtime (operator), chi (api), TanStack Query + HeroUI v3 (web), itzg/minecraft-server image (module)

**Storage**: Kubernetes objects only (GameServer spec/status, Secrets, Backup CRs); no DB migration

**Testing**: envtest (operator, api), Go unit tests with recorded fixtures (registry engines), vitest (web), Kind E2E bucket `api-mods`

**Target Platform**: Kubernetes (k3s single node and multi-node)

**Project Type**: web service + operator + dashboard + game module

**Performance Goals**: FTB search returns within the registry browser's existing timeout (15 s client), with at most 20 detail calls per search, 4 in parallel, 10-minute cache

**Constraints**: CurseForge key never in pod spec, GameServer objects, API bodies, audit or logs (SC-003); adopted legacy packs must not roll the StatefulSet; one pack per server

**Scale/Scope**: one active pack per server; three registries; six UI surfaces (contracts/ui-modpacks-tab.md)

## Constitution Check

| Principle | Gate | Status |
|---|---|---|
| I. E2E-tested delivery | E2E in `api-mods` for CurseForge install, registry switch, remove; FTB and blocked-files tests written and bucket-excluded with a reason | PASS (planned in quickstart.md) |
| II. Design-first | Six surfaces designed in design.pen via Pencil on valgul's machine, exported, approved before React | PASS (local prompt after tasks) |
| III. Language best practice | `%w` wrapping, strict TS, no suppressions; CRD edits followed by `make generate && make manifests` with all four generated paths | PASS |
| IV. Spec-driven + `specs.md` | Update `operator/specs.md`, `api/specs.md`, `web/specs.md`, `modules/minecraft-java/specs.md` (module repo) in the same changes | PASS (tasks will carry it) |
| V. Workflows | Implementation through Workflow scripts, haiku first, tier-up review | PASS |
| VI. CI verifies | Local compile checks only | PASS |
| Core rule 10 (operator authority) | Env rendering, backup sequencing, wipe and key copies in the reconciler; API only writes intent | PASS |
| Login privacy, rule 3 | No unauthenticated surface touched | N/A |

Re-checked after Phase 1 design: no violations, Complexity Tracking empty.

## Project Structure

### Documentation (this feature)

```text
specs/035-curseforge-modpacks/
├── spec.md
├── OPEN-DECISIONS.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── api-modpack.md
│   ├── operator-modpack.md
│   └── ui-modpacks-tab.md
├── checklists/requirements.md
└── tasks.md            # /speckit-tasks
```

### Source Code

```text
operator/
├── api/v1alpha1/gametemplate_types.go     # ModpackSpec fields, ModProvider enum + modpacksOnly
├── api/v1alpha1/gameserver_types.go       # spec.modpack, status.modpack
├── internal/controller/gameserver_modpack.go          # new: env rendering, state machine, wipe, key copy
├── internal/controller/gameserver_modpack_envtest_test.go
├── internal/controller/gameserver_controller.go       # call sites, 60s requeue for CurseForge servers (managed key Secret is read, not watched)
└── config/{crd,rbac}/ + charts/gameplane/{charts/gameplane-crds/crds,crd-manifests}/  # generated
api/
├── internal/registry/ftb.go (+ _test.go, testdata/)   # new keyless engine
├── internal/registry/curseforge.go                     # modpack Versions filter, ModpackDeps comment
├── internal/registry/registry.go                       # register ftb, modsBrowse
├── internal/handlers/registry.go                       # GET/POST/DELETE modpack, providers fields
├── internal/handlers/modpack_adopt.go                  # new: legacy sweep (R6)
├── internal/handlers/modpack_missing.go                # new: MODS_NEED_DOWNLOAD.txt parser
├── internal/handlers/registry_secret.go                # share toggle
└── internal/handlers/*_envtest_test.go
web/src/
├── routes/tabs/Modpacks.tsx (+ .test.tsx)
├── components/registry-browser.tsx                     # modsBrowse filter
├── components/modpack-*.tsx                            # card, picker, confirm, missing files
├── routes/AdminSettings… (mod registries section)      # share switch
├── lib/api.ts, types.ts
i18n/locales/en/*.json                                  # new strings
netguard/                                               # no change (FTB host is public)
test/e2e/modpack_test.go + buckets.sh
docs/module-authoring.md, docs/security.md
modules/minecraft-java/{template.yaml,module.yaml,README.md,specs.md}  # module repo PR first
design.pen + assets/design-export/                      # valgul's machine
```

**Structure Decision**: existing Gameplane layout; one new operator file, three new API
files, a handful of new web components. Module change ships as its own PR in
`GameplanePanel/module`, then a pointer bump.

## Delivery order

1. CRD types + generate/manifests (operator), with envtest for rendering and state machine.
2. API: FTB engine, CurseForge modpack versions, modpack routes, adoption sweep, share toggle.
3. Module PR (needs 1 for the CRD enum; `gameplaneMinVersion` raised).
4. Design in Pencil (local prompt), approval, then web.
5. E2E, docs, `specs.md` updates.

Per project rules this is one branch and one PR in Gameplane (plus the module PR and its
pointer bump), unless valgul asks for stacked PRs.

## Decision raised by planning

- **OD-7** (SETTLED B, valgul 2026-11-16): only Remove empties the mods folder. Installing a
  different pack over an active one does not; leftovers are the operator's to delete.

## Complexity Tracking

No constitution violations.
