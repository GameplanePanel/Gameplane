# Feature Specification: Capture Overhead CI Regression Guard

**Feature Branch**: `007-capture-overhead-guard`

**Created**: 2026-08-24

**Status**: Planned (plan.md, tasks.md)

**Amendments**: 2026-10-09 — gating ruled non-blocking (OPEN-DECISIONS.md OD-1; supersedes SC-005's "block" wording); Unresolved Questions 1–5 answered by OD-2..OD-6; references to the `003-network-capture-sidecar` branch and `main` are stale (003 shipped as `specs/done_003-network-capture-sidecar/`, default branch `master`, trigger per OD-2); `gameproto` `ParsePacket` and `tshark`/`capinfos` do not exist here, so capture validation follows OD-6; "KeepAlive RTT" is measured as defined in OD-3.

**Input**: User description: "A CI regression guard that detects gross performance overhead from running a network capture on a live game server"

## Critical Framing: What This Feature Does and Does Not Do

**IMPORTANT: Read this section first.** This feature is NOT a certification of SC-002 ("Zero Perceptible Player-Experience Impact") from feature 003 (Network Capture Sidecar).

### What SC-002 Is (Out of Scope for This Feature)

SC-002 is a **live-cluster-only manual benchmark** documented in `specs/done_003-network-capture-sidecar/sc-002-benchmark.md`. It validates that players on a **REAL network** with **REAL player load** experience no observable increase in packet loss or latency when a capture is running. SC-002 requires:
- A real Kubernetes cluster (not kind) with real network conditions
- Real or realistically-simulated game clients
- Stable baseline measurements taken in the same environment
- Comparison against a human-assessed threshold for "perceptible impact"

**SC-002 remains a manual, live-cluster procedure.** No automated CI test will ever certify it.

### What This Feature DOES (Gross Regression Detection Only)

This feature adds a **CI regression guard** that runs **on the GitHub Actions shared runner** in a **kind cluster** and catches **GROSS regressions** — capture overhead so severe that it breaks:
- The ability to join the game at all (probe fails to handshake)
- The game pod's stability (restart, eviction, or crash)
- Capture file validity or correctness (filter not applied, corrupted output)

The "gross" bar is intentionally wide: this guard is designed to catch **regressions, not to measure absolute overhead**. It answers "did the capture sidecar break something fundamental?" not "did average RTT increase by 2%?".

### Why Kind's Network Is Too Noisy for a Perf Gate

A shared GitHub Actions runner runs 10s of other jobs concurrently. The kind cluster (all pods on one VM) adds CPU steal variance, container networking adds 0–5% scheduling jitter, and pod-to-pod traffic crosses veth interfaces (not a real network). Automated assertions on latency deltas would fail ~20% of the time due to environmental noise, not sidecar overhead. **A flaky perf gate is worse than no gate** — the team learns to re-run until green and trust in the whole CI erodes.

---

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Detect gross regression where capture breaks joinability (Priority: P1)

A maintainer pushes code to the `003-network-capture-sidecar` branch. CI automatically runs a regression test that starts a lightweight game server (Minecraft, bot-fast bucket) with capture enabled, then tries to join it with a bot. If the bot cannot join (or captures garbage packets), the CI job fails and the PR is blocked, preventing regressions from merging.

**Why this priority**: Joinability is the minimum bar. A capture sidecar that prevents the game server from accepting connections is a showstopper bug. Catching this in CI before merge prevents a broken feature from shipping.

**Independent Test**: Can be tested independently by booting a GameServer with capture enabled, running the game-bot probe against it in both sustained-load mode and normal join mode, and verifying that the bot successfully handshakes and the capture file is valid. The test passes when the join succeeds and the file contains expected traffic.

**Acceptance Scenarios**:

1. **Given** a GameServer created with capture enabled and running in the kind cluster, **When** the game-bot probe connects in normal mode, **Then** the handshake completes and the probe exits 0 (success).
2. **Given** a GameServer with an active capture sidecar, **When** the bot probe connects in sustained-load mode and holds the connection for 30 seconds while responding to KeepAlive packets, **Then** the connection does not drop and the probe reports "connection held for full duration" before exiting 0.
3. **Given** a completed capture file from the sustained-load test, **When** the file is parsed with standard packet analysis tools, **Then** the file is valid PCAPNG and contains packets matching the configured filter expression (non-empty and filter-correct).

---

### User Story 2 - Detect gross regression in throughput or packet loss (Priority: P1)

The CI regression test includes an A/B probe design: the same bot is run multiple times in alternating capture-off and capture-on phases. Differences in pass/fail counts between phases reveal if capture degrades joinability or stability in aggregate.

**Why this priority**: While absolute latency numbers are too noisy, binary outcomes (join succeeded vs. failed) are not. If capture causes 80% of joins to fail while baseline achieves 100% success, that is a regression worth catching.

**Independent Test**: Can be tested independently by running 5 probe attempts with capture disabled, then 5 with capture enabled, then 5 with capture disabled again, comparing pass counts across phases. The test passes when the off/on/off pass rates are similar (no phase showing a significant drop in success).

**Acceptance Scenarios**:

1. **Given** a GameServer and capture-off baseline phase of 5 join attempts, **When** 4 or more succeed, **Then** record the pass count (baseline established).
2. **Given** a capture-enabled phase of 5 join attempts on the same server, **When** pass count is >= baseline pass count, **Then** capture did not degrade joinability and the phase passes.
3. **Given** a second capture-off phase of 5 join attempts, **When** pass count is >= original baseline, **Then** joinability did not regress after capture was disabled (sidecar injection/removal did not leave server in broken state).

---

### User Story 3 - Record throughput/loss metrics as non-blocking CI artifacts (Priority: P2)

The CI job records packet-level metrics (handshake latency, join duration, KeepAlive RTT samples) in a structured JSON artifact. A human can read the artifact post-merge to spot trends (e.g., "capture overhead increased by 15% over the last month"), but the metrics do not block the merge.

**Why this priority**: Human-in-the-loop trend detection is more reliable than automated thresholds on a noisy runner. Storing metrics as artifacts enables retrospective analysis and trend detection without false positives.

**Independent Test**: Can be tested independently by verifying that the CI job completes and outputs a valid JSON file containing probe metrics (handshake latency, KeepAlive RTT min/max/avg, packet counts, join duration) indexed by phase (capture-off phase 1, capture-on phase 1, etc.). The metrics file is valid JSON and all expected fields are present.

**Acceptance Scenarios**:

1. **Given** a completed CI regression run, **When** the job's artifacts are downloaded, **Then** a `capture-overhead-metrics.json` file is present.
2. **Given** the metrics file, **When** it is parsed as JSON, **Then** it contains entries for each of 3 phases (off/on/off) with keys: probe_phase, capture_enabled, handshake_latency_ms, keepalive_rtt_min/max/avg_ms, join_duration_sec, packets_sent/received, packet_loss_pct.
3. **Given** the metrics file, **When** a human reads the trends across phases, **Then** they can see whether capture-on phases show consistent delta patterns vs. capture-off phases without re-running the test.

---

### Edge Cases

- What happens when a probe fails to reach KeepAlive exchange (server accepts connection but fails to negotiate)? The probe fails early and is reported as "transport failure" (exit code 3) rather than as a join success; the failure is recorded in the metrics artifact and does NOT block the CI job (non-gating), but is flagged for manual review.
- What happens on a heavily loaded CI runner where CPU steal is >50%? Both baseline and capture phases may show higher latency jitter; the A/B comparison still holds (if both degrade together, the delta is near zero and we still see no regression from capture).
- What happens if the probe times out waiting for a KeepAlive response? The probe logs the timeout, records it as a failed KeepAlive, and exits. If timeouts are sporadic (1 in 5 runs), they are treated as environmental noise (runner CPU steal); if consistent (4 in 5 runs in capture-on phase but 0 in 5 in capture-off phase), that is a regression.
- What happens if the capture file is empty or filtered so aggressively that no packets match? The probe records an empty file as a validation failure (file is valid PCAPNG but has 0 packets). A capture filter that is too strict is a misconfiguration, not a regression in the sidecar itself, so this failure is noted but does not block merge; it triggers manual review of the filter configuration.

---

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: The CI regression test MUST boot a lightweight game server (Minecraft Java from `bot-fast` bucket) with capture enabled in a kind cluster, then run the existing game-bot probe against it in a "sustained-load" mode (new probe mode, not a new binary; see FR-002).
- **FR-002**: The game-bot probe MUST support a `-mode sustained` flag (in addition to the existing normal join mode) that:
  - Connects to the server once
  - Waits for and responds to KeepAlive packets from the server for a bounded window (30 seconds, configurable)
  - Periodically samples the round-trip time (RTT) for each KeepAlive exchange
  - Logs structured JSON metrics (handshake latency, KeepAlive RTT samples, join duration, packet counts)
  - Exits 0 if the connection is held for the full duration; exits non-zero if dropped before the window expires
  - Outputs one `VERDICT` line (per existing e2e contract) naming the depth reached and whether sustained mode succeeded
- **FR-003**: The CI regression job MUST run an A/B test with three phases in sequence:
  - **Phase 1 (capture-off)**: Run 5 probe attempts against a GameServer with capture disabled; record pass count and metrics
  - **Phase 2 (capture-on)**: Run 5 probe attempts against the same GameServer with capture enabled; record pass count and metrics
  - **Phase 3 (capture-off again)**: Run 5 probe attempts after capture is disabled; record pass count and metrics
  - Each phase runs in isolation: after phase 1 completes, disable capture and wait 10 seconds before phase 2; after phase 2 completes, disable capture and wait 10 seconds before phase 3
- **FR-004**: The regression job MUST capture packets during phase 2 (capture-on). The job MUST verify the capture file is valid PCAPNG, is non-empty, and contains only packets matching the filter expression (game server's advertised port, or a default filter applied at capture start).
- **FR-005**: The regression job MUST output a structured JSON metrics artifact (`capture-overhead-metrics.json`) containing:
  - Per-phase entries: phase name, capture_enabled flag, pass_count (integer 0–5), metrics for all probes in that phase
  - Per-probe metrics: handshake_latency_ms, join_duration_sec, packets_sent, packets_received, packet_loss_pct, keepalive_rtt_samples_ms (array), server_tick_rate_ticks_per_sec (if obtainable from RCON, else null)
  - Summary: overall pass count across all 15 probes, count of sustained-mode timeouts, any validation failures (empty capture file, invalid PCAPNG, filter mismatch)
- **FR-006**: The regression job MUST be **non-gating** — it MUST NOT block PR merge if it fails due to environmental noise (e.g., runner CPU steal, transient pod scheduling jitter). Instead:
  - Run the job to completion and output full metrics
  - If all 5 phase-2 probes fail but phase 1 and 3 both have 4+ passes, flag the job with "⚠️ INCONCLUSIVE (possible environmental noise)" and allow merge to proceed; log a warning comment on the PR
  - If phase 2 pass count is significantly lower than phase 1 and 3 (e.g., 1 pass in phase 2 vs 4+ in others), block with "❌ REGRESSION DETECTED (capture-enabled phase shows >50% increase in failure rate)" and require manual review
- **FR-007**: The regression job MUST complete in approximately **30 minutes** (15 probes × ~2 minutes per probe, plus setup/teardown). If the job runs longer, the CI run time cost exceeds the value of the signal (see Risk section). The CI pipeline (`.github/workflows/ci.yaml` or equivalent) MAY run the regression job in its own parallel job slot rather than sequentially in an existing test job.
- **FR-008**: The regression job MUST document (in a code comment or CI job description) that:
  - This guard catches **gross regressions** (joinability breaks, pod crashes, capture file corruption) but does NOT measure absolute overhead
  - **SC-002 (zero perceptible impact) remains a live-cluster manual benchmark**, documented in `specs/done_003-network-capture-sidecar/sc-002-benchmark.md`
  - The in-cluster A/B test uses noisy network conditions (veth, runner CPU steal) so absolute latency numbers are not meaningful; paired-difference comparison is the signal
  - Flaky environmental factors (transient pod failures) are expected and the job handles them with non-gating + manual review flagging, not automated assertions
- **FR-009**: The probe's "sustained-load" mode MUST use KeepAlive packets as the in-protocol application-layer round-trip signal. This reuses the existing Minecraft wire protocol (no new wire-format fabrication) and provides a reliable measurement of whether the server is responsive, without introducing a new tool (e.g., no `mineflyer`, no Node, no npm). The probe MUST document which protocol(s) support KeepAlive-based sustained measurement vs. probes that can only join-and-disconnect (and therefore cannot measure sustained overhead).
- **FR-010**: The regression job MUST verify the capture file against the real Minecraft wire format by:
  - Using existing game-protocol parsing in `gameproto/minecraft.go` or `gameproto/terraria.go` to parse captured packets
  - Confirming that packets are well-formed wire-protocol messages (no truncation, no corruption)
  - Reporting parse errors as "⚠️ capture file contains malformed packets: [details]" (warning, not failure; see Risk section on fabricated protocol details)
  - Documenting any uncertainty about whether the server image's actual wire format matches the template's expectations (see Unresolved Questions section)
- **FR-011**: If server tick rate is obtainable from RCON, the regression job MUST sample it after phase 1 completes and record it in the metrics artifact. If tick rate is not available (e.g., vanilla Minecraft with no `/tps` command, or the RCON integration is not yet ready), the job MUST NOT fail; instead, it MUST record `server_tick_rate_ticks_per_sec: null` and note in the artifact "tick rate unavailable (check server type in template)". See Unresolved Questions.
- **FR-012**: Capture must be reusable with other game modules (not Minecraft-only in design), but only Minecraft Java from `bot-fast` must be included in the initial CI job. Support for Terraria, Garry's Mod, or other `bot-fast` games is future work (may be added incrementally post-v1).

### Key Entities

- **RegressionProbe**: A configured run of the game-bot probe in sustained-load mode against a GameServer; captures metadata: phase (off/on/off), probe attempt number (1–5), connect timestamp, disconnect timestamp, handshake latency, KeepAlive RTT samples, packet counts, success/failure reason.
- **CaptureFile**: The output PCAPNG file from a capture-enabled phase. Must be valid (parseable by tshark/capinfos), non-empty, and filter-correct (only game-server packets). Validation is performed using existing `gameproto` wire-format parsers.
- **RegressionMetrics**: Structured JSON artifact output by the CI job, containing all per-probe metrics, per-phase summary stats, overall pass counts, and human-readable interpretation (regression vs. noise).
- **A/B Comparison**: The comparison of pass counts and latency deltas across the three phases (off/on/off). Success is determined by this comparison, not by absolute thresholds on individual latencies.

---

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A regression probe running in sustained-load mode against a GameServer with capture enabled connects, exchanges >=1 KeepAlive packet, records RTT samples, and exits 0 (success) when the connection is held for the full 30-second window without drops.
- **SC-002**: A capture file generated during a regression run is valid PCAPNG (readable by tshark/capinfos without errors), is non-empty (contains >= 1 packet), and contains only packets matching the configured filter (game-server ports or default filter).
- **SC-003**: A complete regression run with 3 phases (off/on/off, 5 probes each) produces a valid `capture-overhead-metrics.json` artifact containing per-phase summary stats, per-probe metrics, and an overall pass-count comparison.
- **SC-004**: An A/B comparison across phases shows no statistically significant increase in failure rate or latency in the capture-on phase (phase 2) compared to the baseline phases (phase 1 and 3), accounting for environmental noise (±1 probe failure tolerance, ±15% RTT jitter tolerance).
- **SC-005**: When the regression job encounters environmental noise (e.g., a single transient probe timeout), it records the event in the metrics artifact, flags the job with a ⚠️ notice, and allows merge to proceed (non-gating). Only when phase 2 pass count is >50% lower than phase 1/3 baseline does the job block with a ❌ and require manual review.
- **SC-006**: The regression job completes in approximately 30 minutes or less (15 probes × ~2 min per probe, plus overhead). If runtime exceeds 40 minutes on three consecutive runs, the design is revised to reduce the number of probes or the probe timeout.
- **SC-007**: Captured packets are validated against the real Minecraft wire format (using existing `gameproto/minecraft.go` parsers) to detect format drift or corruption. Parse errors are logged with details but do NOT fail the job (see FR-010 Risk note on fabricated protocol knowledge).
- **SC-008**: The regression job's CI workflow comment and documentation clearly state that this is a **regression guard**, not a **performance certification**. SC-002 (zero perceptible impact on real networks) is a separate live-cluster manual benchmark.

---

## Assumptions

- **Assumption 1**: The existing game-bot probe (`test/e2e/internal/minecraft-java/minecraftproto/` and `/probe/minecraft-java` binary) can be extended with a `-mode sustained` flag without major refactoring. The mode reuses the existing Minecraft wire-protocol implementation; no new protocol parsing is introduced.
- **Assumption 2**: KeepAlive packets occur frequently enough (typically every 20–30 seconds in vanilla Minecraft) to provide multiple RTT samples during a 30-second sustained-load window. If a server type (e.g., heavily modded, or a non-Paper variant) does not send KeepAlive, the sustained mode times out waiting for it and fails; this is treated as a server-template misconfiguration, not a capture regression.
- **Assumption 3**: The GitHub Actions runner's CPU and network conditions are noisy but statistically similar across back-to-back test runs. An A/B comparison (capture-off vs. capture-on vs. capture-off) averages out transient jitter better than absolute thresholds. See Risk section.
- **Assumption 4**: Capture filter expressions are correctly configured at capture start (default filter is game-server port). A filter that is too aggressive (e.g., capturing only port 1234 when the server listens on 25565) is a misconfiguration, not a sidecar bug. The regression job uses the default filter and does not test custom filters.
- **Assumption 5**: The `gameproto` package (Minecraft, Terraria wire-format parsers) is maintained and available at build time. If support is needed for other `bot-fast` games later, their protocol parsers must exist in `gameproto/` before the regression job can validate their captures.
- **Assumption 6**: The regression job is run as part of CI on every commit to the `003-network-capture-sidecar` branch (or merged into main after feature 003 ships). It is NOT run on every commit to every branch (overhead + cost).
- **Assumption 7**: The live-cluster manual SC-002 benchmark (documented in `specs/done_003-network-capture-sidecar/sc-002-benchmark.md`) remains separate from this CI guard. A maintainer MUST run SC-002 on a real cluster before release to validate zero perceptible player impact; this CI guard is not a substitute.

---

## Unresolved Questions

The following questions MUST be answered before implementation; they do not block the spec but affect the implementation plan:

1. **Server Type and Tick Rate Availability**: The `modules/minecraft-java/template.yaml` uses the `itzg/minecraft-server` image. Does this image run vanilla, Paper, Spigot, or another variant by default? Does it expose `/tps` command via RCON? Can tick rate be reliably sampled during a regression run, or MUST the metrics artifact record `tick_rate: null`?
   - **Action**: Read the itzg/minecraft-server image docs and template configuration; verify RCON `/tps` availability. If unavailable, mark server_tick_rate as "not implemented in v1".

2. **KeepAlive Packet Frequency**: Minecraft's KeepAlive interval varies by server configuration (default ~20 seconds, but configurable). Is 30 seconds a long enough sustained window to capture >= 1 KeepAlive exchange reliably, or does it risk timing out?
   - **Action**: Test against a real itzg container; confirm KeepAlive arrives within 25 seconds. Adjust sustained window timeout if needed (e.g., 45 seconds for safety).

3. **Capture Filter Verification**: The regression job uses a default filter (e.g., "port 25565"). How does the regression job verify that only matching packets are in the file? Does `gameproto/minecraft.go` parse individual PCAPNG packets and check their src/dst ports?
   - **Action**: Determine whether `gameproto` or a separate filter-validation helper will parse the PCAPNG file and assert all packets match the filter. If no existing tool does this, document it as a future enhancement and proceed with "file is non-empty" as the validation in v1.

4. **CI Pipeline Integration**: Where should the regression job be placed in `.github/workflows/ci.yaml`? Should it:
   - Run on every commit to `003-network-capture-sidecar` branch only?
   - Run on every commit to `main` (once 003 is merged)?
   - Run in a separate `ci-capture-overhead.yaml` workflow triggered by label or manual dispatch?
   - **Action**: Decide placement and trigger conditions based on CI cost tolerance and merge process requirements.

5. **Probe Timeout and Retry Strategy**: If a sustained-load probe times out (KeepAlive doesn't arrive in 30 seconds), should the job retry that probe, or count it as a failure immediately?
   - **Action**: Define retry strategy (0 retries = fail immediately, or 1 retry = transient tolerance). Recommend: no retries, treat timeouts as failures but flag them in the artifact as "possible environmental transience".

---

## Out of Scope

- **Absolute overhead measurement**: This feature does not measure "the capture sidecar adds X% to game latency" in absolute terms. Absolute measurements on a shared CI runner are too noisy. See SC-002 Benchmark Procedure in feature 003 for that.
- **Automated threshold tuning**: No machine-learning or statistical model tunes the regression gate dynamically. Thresholds (pass-count delta >50%, latency jitter ±15%) are hardcoded based on operational experience.
- **Per-game regression testing in CI**: Only Minecraft Java from `bot-fast` is tested initially. Terraria, Garry's Mod, and other games may be added incrementally, but are NOT part of this spec.
- **Real-time capture streaming or live metrics**: The regression job produces offline metrics after the test completes. Real-time dashboarding of capture overhead is future work.
- **Capture filter custom expression testing**: The regression job tests only the default filter. Custom filter expressions (e.g., "only packets from 10.0.0.5") are not regression-tested in CI; they are tested manually by the operator (see feature 003 User Story 1).
- **Cross-version or cross-image regression testing**: The regression job tests a single server image (itzg/minecraft-server, fixed version per `template.yaml`). Testing against different versions or images is out of scope for v1.

---

## Architectural Constraints & Design Decisions

The following design decisions are established and not open for re-discussion:

1. **Reuse Existing Go Bot and Wire Protocol Parsers**. The regression guard uses the existing game-bot probe (`test/e2e/internal/minecraft-java/`) and `gameproto/` parsers. NO new runtime dependencies (Node.js, mineflayer, npm) are introduced. This keeps the probe binary small, the CI runner disk usage minimal, and leverages existing tested code.

2. **A/B Comparison, Not Absolute Thresholds**. The guard compares off/on/off phases rather than asserting absolute latency/packet-loss numbers. This makes the guard usable on a noisy CI runner without constant false positives.

3. **Non-Gating With Human Flagging**. The regression job is non-gating by default (fails → ⚠️ warning, not ❌ block). Only severe regressions (>50% failure rate in capture-on phase) block merge. This prevents flaky perf tests from eroding CI trust, while still catching gross bugs.

4. **KeepAlive as the Overhead Signal**. The sustained-load probe uses KeepAlive packet exchanges to measure server responsiveness during capture, reusing the in-protocol round-trip already built into Minecraft. No synthetic traffic or custom wire format is added.

5. **Metrics As Artifacts, Not Assertions**. Raw probe metrics (per-phase stats, per-probe RTTs, packet counts) are output to a JSON artifact for human inspection. The metrics do not directly trigger pass/fail conditions; the A/B comparison logic (phase counts) does. This separation allows for later refinement of pass/fail logic without re-running probes.

---

## Risk Analysis: Why This Design Accepts Non-Gating

### The Core Risk: Flaky Perf Gates Destroy CI Trust

**Problem**: GitHub Actions runners experience CPU steal, memory pressure, and network jitter that vary by minute and by runner. Latency can swing ±20% run-to-run on the same code, purely from environmental factors.

**Consequence**: If the regression gate asserts `|RTT_delta| < 5%`, the gate fails 15–20% of the time due to noise, not regressions. Developers learn to re-run the job until it passes and stop trusting the gate.

**Solution**: This spec accepts a non-gating design (emit metrics, flag ⚠️, allow merge) for the first iteration. A future iteration can tighten the gate once operational experience establishes noise floors per runner type.

### Secondary Risk: KeepAlive Protocol Knowledge Is Fragile

**Problem**: Feature 003's research and implementation have already involved two instances of fabricated protocol knowledge (Factorio join sequence, GameMod version negotiation — cited in project memory as Source Connect Evidence and Factorio Protocol Undocumented). Extending the probe with KeepAlive sampling adds another protocol surface.

**Consequence**: If the KeepAlive implementation is wrong (e.g., misinterprets the opcode, reads the wrong field for timestamp), the regression guard measures nothing useful and could mask real regressions.

**Solution**: FR-010 requires validation of captured packets against the existing `gameproto/minecraft.go` wire-format parsers. If captured packets fail to parse, the job logs warnings and allows merge (non-gating) but flags the issue for manual review. This prevents a broken guard from silently shipping.

### Tertiary Risk: 30-Minute CI Time Cost

**Problem**: 15 probes × ~2 minutes per probe = 30 minutes of wall-clock time (or ~5–10 minutes if run in parallel, at cost to runner concurrency quota).

**Consequence**: Every commit to `003-network-capture-sidecar` adds 30 minutes to CI time, multiplied across all contributors. Over a month with 20 commits, that's 10 hours of total runner time.

**Solution**: Run the regression job in its own dedicated CI job slot (not sequentially in an existing job). Use GitHub Actions' `jobs.<job_id>.runs-on` to pin it to a specific runner type or label, so it doesn't compete with other tests. If runtime exceeds 40 minutes on three consecutive runs, reduce probe count (e.g., 3 phases × 3 probes instead of 5) or split `bot-fast` games across multiple jobs (Minecraft in one, Terraria in another).

---

## Rationale for Non-Gating Design

This feature takes a deliberately conservative approach to avoid repeating past mistakes:

1. **The E2E Flake Problem**: Feature 001 (Game Protocol E2E Coverage) documented multiple e2e test flakes that clear on re-run due to runner variance. Adding a perf gate with tight thresholds repeats the same mistake.

2. **The Fabricated Protocol Problem**: Feature 003 (Network Capture Sidecar) has already had to pause and verify protocol details against real servers twice. A regression guard that depends on correctness of probe code is only useful if the probe is thoroughly verified.

3. **The Decomposition Principle**: From the CLAUDE.md constitution, "finish the project for a v1 release" means ship a working feature, not ship a perfect observability system. v1 ships a working capture sidecar; a refined regression guard is post-v1 work.

4. **Live-Cluster Validation**: SC-002 (the real correctness bar) is a live-cluster manual benchmark. A CI guard is a safety net, not the source of truth. Accepting non-gating acknowledges this hierarchy.

---

## Success Definition

This spec succeeds when:

1. ✅ The regression job runs on every commit to the `003-network-capture-sidecar` branch without manual intervention
2. ✅ The job produces a valid `capture-overhead-metrics.json` artifact with per-phase and per-probe metrics
3. ✅ The job detects a gross regression if code is broken (e.g., capture sidecar crashes, join always fails)
4. ✅ The job does NOT block merge on transient noise (e.g., a single probe timeout, ±10% RTT variance)
5. ✅ A human can read the metrics artifact and spot trends (e.g., "capture overhead increased steadily over 5 commits")
6. ✅ No automated thresholds are hard-coded in a way that causes >5% false-positive failures
7. ✅ A maintainer running SC-002 on a live cluster independently reports zero perceptible player impact, confirming the CI guard's signal is valid (post-v1 validation step)

---

## Appendix: Protocol Verification Strategy

### Why This Matters

Feature 003's implementation has already had to verify protocol details against real servers (Factorio, GMod) to correct fabricated assumptions. A regression guard that re-parses captured packets adds protocol surface that can drift.

### Verification Approach (Per FR-010)

1. **At Regression Job Start**: Verify that `gameproto/minecraft.go` exists and its `ParsePacket` function can parse sample Minecraft packets.
2. **After Capture Completes**: Use `gameproto/minecraft.go` to iterate over all packets in the capture file:
   - Parse each packet; log any parse failures as warnings
   - Count parse failures; if >10% of packets fail to parse, flag as "⚠️ possible protocol drift" and note in artifact
3. **No Automatic Fix-Up**: Do not attempt to re-sync the protocol parser against the capture. Capturing well-formed packets is the signal; if >10% of packets are malformed, that is either:
   - Evidence the server image has an unexpected wire-format variant (check `template.yaml`'s image version)
   - Evidence the capture filter captured non-Minecraft traffic (misconfiguration)
   - Evidence of a real protocol bug (rare)
   All three are manual-review items, not automatic regressions.

### Rationale

This is deliberately conservative. A CI guard that fails on protocol-parsing errors teaches developers to ignore the guard. Better to emit a warning, allow merge, and flag for human review.
