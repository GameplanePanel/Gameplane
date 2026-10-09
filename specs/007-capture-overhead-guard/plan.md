# Implementation Plan: Capture Overhead CI Regression Guard (007)

**Spec**: `specs/007-capture-overhead-guard/spec.md` · **Decisions**: `OPEN-DECISIONS.md` (OD-1 ruled; OD-2..OD-8 defaults) · **Created**: 2026-10-09

## Summary

Extend the existing Minecraft Java e2e probe with a `-mode sustained` that joins, stays connected through the Configuration and Play states for a fixed hold window, echoes KeepAlives and measures Ping Request/Pong Response round trips. A new e2e test runs 5 sustained probes in each of three phases (capture off → capture on → capture disabled) against one Minecraft GameServer, validates the capture file, writes `capture-overhead-metrics.json`, and judges regression vs noise (OD-8). A new non-blocking CI job (OD-1, OD-2) runs it and uploads the artifact. SC-002 of feature 003 stays a live-cluster manual benchmark.

## Technical context

- **Language**: Go (module `test/e2e`, build tag `e2e` for tests; probe binaries built from `test/e2e/internal/*/app.go` by `test/e2e/Dockerfile` with `GOWORK=off`, so the probe may import only what `test/e2e/go.mod` declares).
- **Existing pieces reused**:
  - Probe: `test/e2e/internal/minecraft-java/app.go` (flags, `-mode` switch), `minecraftproto/minecraft.go` (`Login`, compression-aware `reader.readPacket`), VERDICT contract in `test/e2e/internal/protocol/joindepth/`.
  - Harness: `Env.RunGameProbe` (`test/e2e/gameprobe_job.go`), `runGameBotTest` server setup (`test/e2e/gamebot_helpers_e2e_test.go`), Minecraft spec in `test/e2e/minecraft_bot_e2e_test.go`.
  - Capture: `:capture-enable` / `:capture-start` / `:capture-stop` / `:capture-file` / `:capture-disable` API routes, as driven by `TestGameServer_NetworkCaptureStartStopDownload` (`test/e2e/gameserver_e2e_test.go`), and its pcapgo validation.
  - CI: `e2e-game-bot` job steps in `.github/workflows/ci.yaml`; report wiring (`report.needs`, `NEEDS_ORDER`, `JOB_MATCHERS`, enforced by `hack/check-ci-report-coverage.sh`).
- **Protocol facts**: packet ids in OD-3 (protocol 769 / 1.21.4, from PrismarineJS minecraft-data). Unverified against a live server until the first CI run.
- **No UI, no CRD, no API or operator change.**

## Design

### Probe: sustained mode

- `minecraftproto.Hold(ctx, addr, protocol, user, hold, pingEvery)` (new): performs the existing login handshake, then:
  1. After Login Success, sends Login Acknowledged; switches to Configuration.
  2. Configuration loop: answers Select Known Packs with an empty list, echoes Keep Alive and Ping (Pong), and on Finish Configuration sends Acknowledge Finish Configuration and switches to Play. Disconnect ends the session with an error.
  3. Play loop until the hold window ends: echoes Keep Alive and Ping; answers Synchronize Player Position with Confirm Teleportation; sends a Ping Request every `pingEvery` and records the round trip on the matching Pong Response; ignores every other packet. Disconnect or read error before the window ends is a drop.
  4. Returns a `HoldResult` (keepalives received, RTT samples, packets/bytes each way, handshake latency, join duration, held duration, failure reason).
- `app.go`: `-mode sustained`, `-hold` (default 30s), `-ping-every` (default 2s). Reuses the existing ping/login warm-up retry, then holds once (OD-5). Prints one `METRICS\t<json>` line, then the usual VERDICT (JOINED with detail "held 30s, N keepalives, M rtt samples" on success; transport failure on a drop).

### Harness changes

- `GameProbe` gets a per-run Job name suffix so 15 back-to-back probes never hit `AlreadyExists`, and `ProbeResult` exposes the parsed `METRICS` JSON.
- Server setup is factored out of `runGameBotTest` into a reusable helper; the Minecraft GameTemplate spec becomes a shared value used by both tests.

### Test: `TestGameServer_CaptureOverhead_Joined`

Boot → phase 1 (5 probes) → `:capture-enable`, wait `status.capture.ready`, `:capture-start` (filter `tcp port 25565`, generous duration, `testCaptureMaxSize`, TTL 3600) → phase 2 (5 probes) → `:capture-stop`, wait Completed, download, validate (OD-6) → `:capture-disable` → phase 3 (5 probes) → restart-count check → write metrics JSON to `$GAMEPLANE_E2E_ARTIFACT_DIR` (fallback: `t.TempDir()` path logged) → judge (OD-8): `t.Errorf` only on regression, `t.Log("⚠ …")` on inconclusive. Not parallel (one Minecraft server, one capture); one admin login.

### CI

New bucket `capture-overhead`, new job `e2e-capture-overhead` (`continue-on-error: true`, ~45 min timeout, trigger per OD-2), artifact upload with `if: always()`, header comment stating it is a regression guard and not SC-002 certification (FR-008, SC-008), report wiring.

## Constitution / CLAUDE.md check

- Rule 8: local checks are compile-only; CI runs the job.
- Rule 4: no suppressions.
- E2E conventions: new test registered in `buckets.sh`; login budget 1 admin login.
- Fabricated-protocol risk: ids sourced from minecraft-data and recorded in OD-3; first CI run is the verification gate.
