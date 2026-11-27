# Tasks: Dashboard UI and UX polish, screen by screen

**Input**: `specs/042-dashboard-ui-ux/` (spec.md, plan.md, research.md, data-model.md,
contracts/, quickstart.md, OPEN-DECISIONS.md)

**Tests**: the repo requires tests with every change (constitution I, web coverage floors),
so test tasks are included. Tests run in CI only (CLAUDE.md rule 8). Editing an existing
test to match this spec needs no sign-off but gets a fresh-agent peer review of the test
diff; deleting or weakening one needs valgul's written sign-off.

**Gates**: no React before valgul approves the design export (T009). Nothing is pushed until valgul says go.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: can run in parallel (different files, no dependency on an open task)
- **[Story]**: US1 Servers, US2 status pill, US3 Logs, US4 backup size, US5 resources

---

## Phase 1: Setup

- [x] T001 Re-check every `file:line` cited in `specs/042-dashboard-ui-ux/spec.md` Context and `research.md` against fresh `origin/master`; correct drift in those two files before any edit
- [x] T002 List every horizontal `Tabs.List` and every `Table.Root` with its wrapper in `web/src/` (grep), and append the two lists to `specs/042-dashboard-ui-ux/research.md` R2/R3 as the audit set

---

## Phase 2: Foundational — design pass (blocks every React task)

Run with OpenPencil `master` (app, `openpencil-mcp` or `openpencil eval -w`); never read
or edit `design.fig` with file tools. Follow the `pen-canvas-layout` and `pen-componentize`
skills; finish with `design-export`.

- [x] T003 Add design variables in `design.fig` for the new tokens of `contracts/ui-rules.md` (players/info blue, storage teal, nodes/debug slate), dark and light values
- [x] T004 Update shared components in `design.fig`: tab list (size to label, no wrap, 12 px selected), status pill (text size + dot), stat card (fixed icon colour + amber warning badge), table (single frame), secondary/toolbar button and search (12 px), slider (filled track), storage number + unit field
- [x] T005 [P] Redesign `Screen/Servers`, `Screen/Servers (Light)`, `Screen/Servers — Cluster filters open` in `design.fig` per FR-010..013 (cards, row actions incl. hover states, toolbar)
- [x] T006 [P] Redesign `Screen/Server Detail — Logs` in `design.fig` per FR-020..022: row 1 source switch + Download, row 2 search + coloured level filter with count badges + line count; also the no-game-log variant
- [x] T007 [P] Redesign `Screen/Server Detail — Backups` in `design.fig` per FR-030..033: Size column with data size and "added" second line, card "Total data size" with coverage text, old-backup row (layout per OD-8 once ruled)
- [x] T008 [P] Redesign `Screen/Server Detail — Settings · Resources` and the `Screen/Create Server — Step 3 Configure*` screens in `design.fig` per FR-040..042
- [x] T009 Run `hack/design/fig-export.ts` and commit `design.fig` + `assets/design-export/` together (done, a9eee002); hand the changed screenshots to valgul for approval (SC-007, sent 2026-11-26). STOP until approved (approved by valgul 2026-11-26: "perfect", "good for me")

---

## Phase 3: Shared web rules (blocks US1–US5 React work)

- [x] T010 Add the tokens from `contracts/ui-rules.md` to `web/src/styles/globals.css` (dark + light blocks and `--color-*` mappings next to `--color-chart-stat`)
- [x] T011 Move the `.tabs__list.w-fit` rules onto every horizontal `.tabs__list` and set the selected radius to 12 px in `web/src/styles/globals.css`; keep the Server detail main tab bar spread full width with its pill track and pill selected tab, as the app has it today (OD-10); remove the then-redundant `w-fit` classes listed by T002
- [x] T012 [P] Add a shared `.range` slider style (filled track via `--fill`, WebKit + Firefox thumbs, disabled state) to `web/src/styles/globals.css`
- [x] T013 [P] Remove the extra outer frame around the tables listed in research.md "Audit set" (Servers, Backups, NodeInventory, CaptureWidget, ShareLinks; Users if the browser shows a double frame)
- [x] T014 [US2] Rework `web/src/components/ui/PhaseChip.tsx`: text-size pill with leading dot (green running/succeeded, grey stopped/pending/suspended, amber in-transition, red failed, Asleep violet per OD-9); update `web/src/components/ui/PhaseChip.test.tsx` to the spec
- [x] T015 [US2] Remove per-call size overrides on `PhaseChip` (e.g. `web/src/routes/Servers.tsx:627`) in all call sites: `Dashboard.tsx`, `Servers.tsx`, `ServerDetail.tsx`, `Backups.tsx`, `tabs/Backups.tsx`, `components/backups/BackupRow.tsx`, `components/backups/BackupDetailDrawer.tsx`, `components/CaptureWidget.tsx`

**Checkpoint**: shared rules visible app-wide; Dashboard changes only through the pill (FR-051).

---

## Phase 4: User Story 1 — read the Servers page at a glance (P1) 🎯 MVP

**Independent test**: quickstart.md step 1.

- [x] T016 [US1] Extend `web/src/components/ui/StatCard.tsx` with accents `info`, `teal`, `slate` and a `warning` prop (amber badge + amber sub-text, icon colour unchanged); update `web/src/components/ui/StatCard.test.tsx`
- [x] T017 [US1] Set the five card accents in `web/src/routes/Servers.tsx` (Running success, Players info, vCPUs violet, Storage teal, Nodes slate) and move overcommit/unavailable states to the `warning` prop
- [x] T018 [US1] Make row actions full contrast with hover Start success / Stop danger / Restart foreground in `web/src/routes/Servers.tsx` (`ServerLifecycleActions` ~L522, `ActionButton` ~L653)
- [x] T019 [US1] Replace the hand-made Filter `div` with a real button, 12 px corners, same height as the search box in `web/src/routes/Servers.tsx` (~L300 desktop, ~L337 mobile); keep Create server a pill
- [x] T020 [P] [US1] Update `web/src/routes/Servers.test.tsx` (and split Servers tests touched) for card accents, warning badge, single frame, Filter as button with keyboard access

---

## Phase 5: User Story 2 — one clear status pill everywhere (P1)

**Independent test**: quickstart.md step 2. Implementation is T014–T015.

- [x] T021 [P] [US2] Update tests asserting pill size/class in `web/src/routes/Dashboard.test.tsx`, `web/src/routes/Servers.test.tsx`, `web/src/routes/ServerDetail.test.tsx`, `web/src/routes/Backups.test.tsx`, `web/src/routes/tabs/Backups.test.tsx` where they break

---

## Phase 6: User Story 3 — tell log source and level apart (P2)

**Independent test**: quickstart.md step 3.

- [x] T022 [US3] Split the toolbar in `web/src/routes/tabs/Logs.tsx` (~L168-206) into row 1 (source switch or plain label, Download) and row 2 (search, level filter, line count)
- [x] T023 [US3] Restyle the search box in `web/src/routes/tabs/Logs.tsx` like the Servers search (icon, sentence-case placeholder; update the `web.routes.tabs.logs.filter` message and its description)
- [x] T024 [US3] Colour the level filter with count badges and replace `LEVEL_CLASS` (`Logs.tsx:35-40`) with token classes: Info info, Debug slate, Warn warning, Error danger
- [x] T025 [P] [US3] Update `web/src/routes/tabs/Logs.test.tsx` for the two rows, badges and level colours

---

## Phase 7: User Story 4 — see how much a backup really holds (P2)

**Independent test**: quickstart.md step 4.

- [x] T026 [US4] Add optional `DataSize *resource.Quantity` (`json:"dataSize,omitempty"`) to `BackupStatus` in `operator/api/v1alpha1/backup_types.go` with the doc comment from `data-model.md`; run `make generate && make manifests`; commit deepcopy, `operator/config/crd`, `charts/gameplane/charts/gameplane-crds/crds/`, `charts/gameplane/crd-manifests/`
- [x] T027 [US4] Restic path: set `b.Status.DataSize` from `summary.TotalBytesProcessed` next to `Size` in `operator/internal/controller/backup_controller.go` (~L584)
- [x] T028 [US4] CSI path: when the snapshot completes (the game is still quiesced) read the GameServer `status.agent.diskUsedBytes` if the last heartbeat is ≤ 10 min old and set `DataSize` in `operator/internal/controller/backup_volumesnapshot.go` (`completeVolumeSnapshot`, helper in `backup_datasize.go`); never from `RestoreSize`
- [x] T029 [P] [US4] Tests: Restic dataSize assertion in `TestBackup_PopulatesSnapshotIDFromLogs` (`backup_envtest_test.go`); helper table test + CSI fresh/stale cases in new `operator/internal/controller/backup_datasize_test.go` (fake-client guard fixture)
- [x] T030 [P] [US4] E2E: in `TestBackup_DeleteForgetsSnapshot` (`test/e2e/backup_forget_e2e_test.go`, operator bucket; it waits for Succeeded, unlike `TestBackup_OperatorMaterializesJob`) assert `status.dataSize` equals `status.size` for a Restic backup. Kind has no CSI snapshot class, so the CSI path is covered by T029 and the quickstart walk
- [x] T031 [US4] Add `dataSize?: string` to the Backup status type in `web/src/types.ts` (~L784)
- [x] T032 [US4] Show `dataSize` as the main value with the "added" second line in `web/src/components/backups/BackupSize.tsx`
- [x] T033 [US4] Rename the card to "Total data size" and sum `dataSize` in `web/src/routes/tabs/Backups.tsx` (~L160), with coverage text when some backups lack it
- [x] T034 [P] [US4] Same Size display on the Backups page `web/src/routes/Backups.tsx` and `web/src/components/backups/BackupDetailDrawer.tsx`
- [x] T035 [P] [US4] Update `web/src/components/backups/BackupRow.test.tsx`, `web/src/components/backups/BackupDetailDrawer.test.tsx`, `web/src/routes/tabs/Backups.test.tsx`, `web/src/routes/Backups.test.tsx` for data size, added line and coverage text
- [x] T036 [US4] Changelog `changelog/unreleased/<PR>-backup-data-size.md` (new status field, card renamed)
- [x] T037 [US4] Old backups without `dataSize`: label per OD-8 in `web/src/components/backups/BackupSize.tsx`
- [x] T038 [US4] Exclude them from the total and show the coverage text per OD-8 in `web/src/routes/tabs/Backups.tsx`

---

## Phase 8: User Story 5 — clear sliders and boxes (P3)

**Independent test**: quickstart.md step 5.

- [x] T039 [US5] Apply `.range` and set `--fill` from value/min/max in `web/src/components/ui/ResourceInput.tsx` (L189-199); widen the number box to fit five digits; drop the unit from the CPU/memory labels (`ResourceInput.tsx` messages + callers)
- [x] T040 [P] [US5] Apply `.range` + `--fill` to the SFTP password slider in `web/src/components/sftp/SftpDialogs.tsx` (~L201)
- [x] T041 [US5] Add a storage size number + unit input (MiB/GiB/TiB, exact round-trip of unconvertible values) and use it in `web/src/routes/tabs/settings/Resources.tsx` (~L218-236) and `web/src/routes/CreateServer.tsx` (storage field, validation ~L276)
- [x] T042 [P] [US5] Update `web/src/components/ui/ResourceInput.test.tsx`, `web/src/routes/CreateServer*.test.tsx`, `web/src/routes/tabs/settings/Resources.test.tsx` for fill variable, labels and storage round-trip (`1536Mi` stays `1536Mi`)

---

## Phase 9: Polish and cross-cutting

- [x] T043 Run `npx tsc --noEmit` in `web/` and `go build ./...` in `operator/` (compile checks only)
- [x] T044 Browser smoke test at sonnet via Chrome MCP of quickstart.md steps 1–5 in dark and light, 1280 px and 390 px; screenshots to `/mnt/project-files/notes/042/`
- [x] T045 Changelog `changelog/unreleased/<PR>-dashboard-ui-polish.md` for the visual changes
- [x] T046 Fresh-agent peer review of every test diff (weakened / deleted / only replaced); sign-off card for anything weakened or deleted
- [ ] T047 Once all tasks are done and merged, archive with `spec-archive` (`specs/done_042-dashboard-ui-ux`)

---

## Dependencies and execution order

- Phase 1 → Phase 2 (design) → **valgul approval (T009)** → Phase 3 → Phases 4–8 → Phase 9.
- Operator part of US4 (T026–T030) has no design dependency and may start after Phase 1.
- US1 depends on T010 (tokens) and T013; US2 is T014–T015 (+ T021); US3 depends on T010, T011; US4 web depends on T026, T031; US5 depends on T012.
- 

## Parallel examples

- Design: T005, T006, T007, T008 after T003–T004.
- Phase 3: T012 and T013 alongside T010→T011.
- US4: T029 and T030 after T027–T028; T034 and T035 after T032–T033.
- Across stories after Phase 3: US1, US3, US5 touch different files and can run together.

## Implementation strategy

- **MVP**: Phase 2 design + Phase 3 shared rules + US1 (Servers) + US2 (pill).
- Then US3, US4, US5 as stacked PRs (one thread, one branch per PR, when valgul says go).
- **Workflow batches** (principle V): scout once per batch, haiku fix agents from the brief,
  sonnet reviews the diff; Chrome MCP smoke test at sonnet in parallel with reviews.
