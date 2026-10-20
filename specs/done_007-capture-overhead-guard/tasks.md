---
description: "Task list for Feature 007: Capture Overhead CI Regression Guard"
---

# Tasks: Capture Overhead CI Regression Guard

**Input**: `spec.md`, `plan.md`, `OPEN-DECISIONS.md` (OD-1 ruled 2026-10-09; OD-2..OD-8 defaults).

**Local verification (rule 8)**: compile checks only (`go build ./...`, `go vet` is not run locally; test files are checked by CI). The new job's first CI run is the verification gate for the protocol ids in OD-3.

**Format**: `T### [P?] [Story] Description` with repo-relative paths.

---

## Phase 1: Spec housekeeping

- [X] T001 Record OD-1..OD-8 in `specs/done_007-capture-overhead-guard/OPEN-DECISIONS.md` and add an amendment note to `spec.md` pointing the stale branch names (`003-network-capture-sidecar`, `main`), the FR-006/SC-005 gating conflict, `gameproto` `ParsePacket` and `tshark`/`capinfos` references at the decisions that supersede them.

## Phase 2: Probe sustained mode (US1, FR-002, FR-009)

- [X] T002 [US1] Add `Hold` and `HoldResult` to `test/e2e/internal/minecraft-java/minecraftproto/minecraft.go`: Login Acknowledged, Configuration loop (Select Known Packs, Keep Alive, Ping, Finish Configuration, Disconnect), Play loop (Keep Alive, Ping, Synchronize Player Position → Confirm Teleportation, periodic Ping Request / Pong Response RTT, Disconnect), packet/byte counters, typed failure reasons (drop, timeout, disconnect). Packet ids per OD-3.
- [X] T003 [US1] Unit-test `Hold` in `test/e2e/internal/minecraft-java/minecraftproto/minecraft_test.go` against an in-process fake server: full hold with KeepAlives and pongs, compression on, server disconnect mid-hold (drop), server that never finishes configuration (timeout).
- [X] T004 [US1] Add `-mode sustained`, `-hold`, `-ping-every` to `test/e2e/internal/minecraft-java/app.go`; print one `METRICS\t<json>` line then VERDICT; a drop maps to a transport-failure exit (3).
- [X] T005 [P] [US1] Document sustained mode and the KeepAlive/RTT semantics (OD-3) in `test/e2e/internal/minecraft-java/spec.md` (FR-009: which protocols support sustained measurement).

## Phase 3: Harness (US1, US2)

- [X] T006 [US2] `test/e2e/gameprobe_job.go`: per-run Job name suffix on `GameProbe` and parsed `METRICS` JSON on `ProbeResult`.
- [X] T007 [US2] `test/e2e/gamebot_helpers_e2e_test.go` + `test/e2e/minecraft_bot_e2e_test.go`: factor GameServer creation out of `runGameBotTest` into a reusable helper and share the Minecraft spec; existing test behaviour unchanged.
- [X] T008 [US2] `test/e2e/go.mod`: require + replace `gameproto` (`../../gameproto`) for capture validation in the test (not the probe).

## Phase 4: Regression test (US1, US2, US3; FR-001, FR-003..FR-005, FR-010, FR-011)

- [X] T009 [US2] New `test/e2e/capture_overhead_e2e_test.go` with `TestGameServer_CaptureOverhead_Joined`: three phases × 5 sustained probes, capture enable/start/stop/download/disable, capture validation (OD-6), restart-count check, `capture-overhead-metrics.json` (FR-005 fields; tick rate null per OD-4; `sidecar_present` per OD-7), verdict per OD-8.
- [X] T010 [US2] Register the test in a new `capture-overhead` bucket in `test/e2e/buckets.sh` (`bucket_names`, `list_bucket`), with a comment on why it is not in `bot-fast` (OD-1/OD-2).
- [X] T011 [P] Remove the stale "not yet built" capture-route comments in `test/e2e/test_helpers_e2e_test.go` and `test/e2e/gameserver_e2e_test.go`.

## Phase 5: CI (US3; FR-006..FR-008, SC-008)

- [X] T012 [US3] `.github/workflows/ci.yaml`: job `e2e-capture-overhead` (copy of `e2e-game-bot` steps, `continue-on-error: true`, trigger per OD-2, artifact upload with `if: always()`, FR-008 header comment); add a `capture_overhead` path filter output to the `changes` job; wire the job into `report.needs`, `NEEDS_ORDER` and `JOB_MATCHERS`.

## Phase 6: Verification

- [X] T013 First CI run of `e2e-capture-overhead`: confirm the sustained probe holds against `itzg/minecraft-server` 1.21.4 (OD-3 gate), the artifact uploads, and runtime stays within SC-006 (≤ ~40 min). Record the run link here.
  - Verified 2026-10-09 on commit dbbd0c0, run https://github.com/GameplanePanel/Gameplane/actions/runs/37876049652 (job `e2e capture overhead (kind)`, 12m45s): `TestGameServer_CaptureOverhead_Joined` PASS in 603.65s, verdict `ok`. Every sustained probe held 30s (`VERDICT PASS JOINED held 30s, 1 keepalives, 14 rtt samples`). The capture had 1126 packets, 0 outside the filter, 15 streams and 0 classify failures. Average RTT was off-1 0.36 ms, on 0.30 ms and off-2 0.33 ms. Artifact `capture-overhead-metrics` uploaded.
- [X] T014 Fix `specs/done_003-network-capture-sidecar/sc-002-benchmark.md` line ~300, which describes 007 with thresholds 007 does not use, to point at OD-8.
