# Open Decisions: Capture Overhead CI Regression Guard (007)

**Status**: OD-1 ruled by the maintainer 2026-10-09. OD-2 through OD-8 are implementation defaults chosen 2026-10-09 to resolve the spec's "Unresolved Questions" (spec.md § Unresolved Questions 1–5) and its internal contradictions; each is marked **DEFAULT** until the maintainer confirms or overrides it.

| ID | Question | Status |
|----|----------|--------|
| OD-1 | Gating: does the job ever block a merge? | **RULED** 2026-10-09 |
| OD-2 | CI placement and trigger | DEFAULT |
| OD-3 | What "KeepAlive RTT" measures | DEFAULT |
| OD-4 | Server tick rate | DEFAULT |
| OD-5 | Probe retry strategy | DEFAULT |
| OD-6 | Capture-file validation scope | DEFAULT |
| OD-7 | Phase 3 sidecar state | DEFAULT (constraint, not a choice) |
| OD-8 | Regression / inconclusive thresholds | DEFAULT |

---

## OD-1: Gating (RULED 2026-10-09)

**Conflict**: FR-006 says the job is non-gating; SC-005 says it "blocks with ❌" when phase 2 passes drop by more than 50%.

**Ruling (maintainer, 2026-10-09, project thread decision card "Never block")**: the job **never blocks a merge**. It runs as its own CI job with job-level `continue-on-error: true`. A detected regression fails the job (red, but the workflow and the PR stay mergeable) and is recorded in `capture-overhead-metrics.json` and the job summary with ❌; environmental noise is recorded with ⚠ and the job passes. SC-005's "block" wording is superseded by this ruling.

## OD-2: CI placement and trigger (DEFAULT)

Spec Q4; spec text naming the `003-network-capture-sidecar` branch and `main` is stale (003 merged as `specs/done_003-network-capture-sidecar/`; the default branch is `master`).

**Default**: a new job `e2e-capture-overhead` in `.github/workflows/ci.yaml`, fed by a new e2e bucket `capture-overhead` in `test/e2e/buckets.sh` (not `bot-fast`, whose matrix legs are blocking). It runs on every push to `master`, and on pull requests only when the diff touches the capture path or the probe: `capture-sidecar/**`, `operator/internal/controller/networkcapture*`, `api/internal/handlers/capture*`, `test/e2e/internal/minecraft-java/**`, `test/e2e/capture_overhead_e2e_test.go`.

## OD-3: What "KeepAlive RTT" measures (DEFAULT)

FR-002/FR-009 ask for "KeepAlive RTT". Minecraft KeepAlive is **server-initiated**: the client only echoes the id, so the client cannot time a round trip from it.

**Default**: sustained mode echoes every KeepAlive (Configuration and Play states) to keep the session alive and counts them, and measures RTT with the Play-state **Ping Request / Pong Response** pair (serverbound `0x24`, clientbound `0x38`, `i64` id, protocol 769 / 1.21.4), sending one ping every 2 seconds during the hold window. The metrics field keeps the spec's name `keepalive_rtt_samples_ms` for continuity; its value is the Ping Request → Pong Response round trip. `keepalives_received` is recorded separately.

Packet ids are taken from PrismarineJS `minecraft-data` `data/pc/1.21.4/protocol.json` (version 769), not from memory: Login Acknowledged `0x03` (serverbound, Login); Configuration clientbound Disconnect `0x02`, Finish Configuration `0x03`, Keep Alive `0x04`, Ping `0x05`, Select Known Packs `0x0e`; Configuration serverbound Acknowledge Finish Configuration `0x03`, Keep Alive `0x04`, Pong `0x05`, Select Known Packs `0x07`; Play clientbound Disconnect `0x1d`, Keep Alive `0x27`, Login `0x2c`, Ping `0x37`, Pong Response `0x38`, Start Configuration `0x70`; Play serverbound Confirm Teleportation `0x00`, Keep Alive `0x1a`, Ping Request `0x24`, Pong `0x2b`. The first CI run against the real `itzg/minecraft-server` 1.21.4 container is the verification gate (spec § Secondary Risk).

## OD-4: Server tick rate (DEFAULT)

Spec Q1: vanilla 1.21.4 has no `/tps`. **Default**: record `server_tick_rate_ticks_per_sec: null` with the note "tick rate unavailable (check server type in template)", as FR-011 allows.

## OD-5: Probe retry strategy (DEFAULT)

Spec Q5, following its recommendation: the join step keeps the probe's existing ping/login retry loop (server warm-up), but the 30-second hold is **not retried**; a dropped or timed-out hold counts as a failed probe and is flagged "possible environmental transience" in the artifact.

## OD-6: Capture-file validation scope (DEFAULT)

Spec Q3 and FR-010/SC-002/SC-007 cite `gameproto` `ParsePacket` and `tshark`/`capinfos`. Neither exists here: `gameproto` only classifies the handshake (`MinecraftClassifier.Classify`), and CI runners have no tshark. **Default**: read the file with gopacket `pcapgo.NewNgReader` (already used by `TestGameServer_NetworkCaptureStartStopDownload`), require ≥1 packet, require every TCP packet to have source or destination port 25565 (the explicit capture filter), and reassemble each client→server stream's first bytes through `MinecraftClassifier.Classify`, recording classification failures as warnings (not failures), per SC-007.

## OD-7: Phase 3 sidecar state (constraint)

`:capture-disable` only flips `status.capture.ready`; the ephemeral `capture` container cannot be removed from a running pod. Phase 3 therefore runs with an **idle** sidecar present, while phase 1 has none. This is recorded in the artifact (`sidecar_present`) and is the behaviour FR-003 phase 3 can test.

## OD-8: Regression and inconclusive thresholds (DEFAULT)

From SC-004/SC-005 and FR-006: **regression (❌, job fails)** when phase-2 passes are more than 50% below the lower of phase 1 and phase 3 (e.g. 2/5 vs 5/5), when the game container's restart count increased, or when the capture file is missing, empty, unreadable or contains packets outside the filter. **Inconclusive (⚠, job passes)** when phase 1 or phase 3 itself has fewer than 4/5 passes, or on any isolated probe failure that does not meet the regression bar. RTT deltas are recorded but never fail the job (±15% jitter tolerance is reported, not enforced).
