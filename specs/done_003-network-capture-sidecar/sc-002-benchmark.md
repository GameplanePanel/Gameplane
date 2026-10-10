# SC-002 Benchmark Procedure: Zero Perceptible Player-Experience Impact

**Success Criterion**: SC-002 — "Zero perceptible player-experience impact" from running a network capture.

**Status**: **NOT YET MEASURED**. This procedure has not been executed, and no measurement result has been recorded. Below is a documented manual procedure ready for a maintainer to execute against a live Kubernetes cluster with real player load.

**Reference**: This benchmark gap is tracked in `plan.md`'s Complexity Tracking section (row 6, "SC-002 ('zero perceptible player impact') has no reliable automated validation path").

---

## Why This Benchmark Exists

The network capture sidecar consumes resources (CPU, memory, disk I/O, network namespace overhead) to copy packet data from the game server's live traffic stream. SC-002 validates that this overhead is negligible — that players connecting to a game server with capture enabled experience no observable increase in packet loss or network latency compared to the same server with capture disabled.

**Important**: This is a **manual, live-cluster-only** benchmark, not an automated CI test. The kind cluster's synthetic network is too tightly controlled to reflect real-world variance, and automated assertions on latency deltas under 5% would be flaky. The procedure below is designed for execution on the operator's live kubelab cluster (or similar) where stable, real network conditions exist.

---

## Prerequisites

Before running this benchmark, verify:

1. **Live Kubernetes cluster** with the Gameplane operator installed:
   - All components running: operator, API, agent sidecars
   - NetworkCapture CRD present and reconciling
   - Capture feature enabled in Helm values (`capture.enabled: true`)

2. **Two game servers created and running**:
   - `perf-test-capture-on`: with `spec.capture.enabled: true`
   - `perf-test-capture-off`: with `spec.capture.enabled: false` (baseline)
   - Both servers use the same lightweight game module (Minecraft Java, Terraria, or Factorio)
   - Both are fully initialized and ready to accept players (can take 3–5 minutes per server)
   - Both are LoadBalancer-exposed and reachable from outside the cluster

3. **Test infrastructure**:
   - A game-client join bot or synthetic traffic tool (see Tools section below)
   - `kubectl` access to the cluster
   - `curl` and `jq` for API interaction
   - Network monitoring tools: `iperf3` or `ping` for baseline latency check (optional but recommended)

4. **Maintainer environment**:
   - Administrative API token with `captures:manage` permission
   - SSH or direct access to run commands against the cluster
   - Ability to open a port-forward to the API (`kubectl port-forward`)

---

## Test Setup

### 1. Create Two Test GameServers (if not already running)

Create the baseline (capture-disabled) server:

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: perf-test-capture-off
  namespace: default
spec:
  templateRef:
    name: minecraft-java  # or terraria, factorio — lightweight games preferred
  networking:
    expose: LoadBalancer
  # capture field omitted or explicitly disabled
  capture:
    enabled: false
EOF
```

Create the capture-enabled server:

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: perf-test-capture-on
  namespace: default
spec:
  templateRef:
    name: minecraft-java  # Must match the baseline server's template
  networking:
    expose: LoadBalancer
  capture:
    enabled: true
EOF
```

Wait for both servers to be Ready:

```bash
kubectl wait --for=condition=Ready gameserver/perf-test-capture-off --timeout=300s
kubectl wait --for=condition=Ready gameserver/perf-test-capture-on --timeout=300s

# Verify both are reachable
BASELINE_ADDR=$(kubectl get svc perf-test-capture-off -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
CAPTURE_ADDR=$(kubectl get svc perf-test-capture-on -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

echo "Baseline server: $BASELINE_ADDR"
echo "Capture server:  $CAPTURE_ADDR"
```

### 2. Start an Active Capture on the Capture-Enabled Server

This ensures the sidecar is actively exercising packet I/O during the test (not just standing idle):

```bash
# Port-forward the API
kubectl port-forward -n gameplane-system svc/gameplane-api 8000:8000 &
API_PID=$!

# Start a 5-minute capture
CAPTURE_ID=$(curl -s -X POST http://localhost:8000/servers/perf-test-capture-on:capture-start \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <admin-token>" \
  -d '{
    "filter": "udp",
    "maxDurationSeconds": 300,
    "maxSizeBytes": 104857600
  }' | jq -r '.captureId')

echo "Capture started: $CAPTURE_ID"

# Verify capture is Running (may take a few seconds)
sleep 3
curl -s -X GET "http://localhost:8000/servers/perf-test-capture-on:captures" \
  -H "Authorization: Bearer <admin-token>" | jq '.captures[] | {captureId, phase}'
```

---

## Measurement Procedure

### Step 1: Establish Baseline (Capture OFF)

Run the game-client join bot against the **baseline** server (capture disabled) for 60 seconds and collect metrics.

**Tools Available**:

#### Option A: Real game client (recommended for this feature)
- **Minecraft Java**: Use a bot framework like [PrismarineJS](https://github.com/PrismarineJS/mineflyer) or [MCProtocolLib](https://github.com/Steveice10/MCProtocolLib)
- **Terraria**: Stub bot connecting to the server's TCP port and reading handshake responses
- **Factorio**: Authenticate via in-game credentials and measure join latency

#### Option B: Synthetic traffic (if real client unavailable)
- **iperf3**: UDP throughput and latency measurement
  ```bash
  iperf3 -c $BASELINE_ADDR -p 25565 -u -b 1M -t 60 -R > /tmp/baseline-iperf.json
  ```
  This sends 1 Mbps UDP for 60 seconds and measures packet loss and RTT.

- **ping**: Simple latency baseline
  ```bash
  ping -c 60 -i 1 $BASELINE_ADDR > /tmp/baseline-ping.log 2>&1
  ```
  Extract min/avg/max RTT from the summary line.

#### Option C: In-cluster game-probe via Kubernetes Job
The test/e2e suite includes hand-rolled protocol probes (`test/e2e/internal/<game>/app.go`) compiled into a `gameplane-test/gameprobe:<tag>` container image and run as in-cluster Kubernetes Jobs. **Important**: these probes are built as single-connection join probes only — they connect, perform a join handshake, and disconnect. They do **NOT** generate sustained traffic, do **NOT** hold connections open, and do **NOT** emit RTT metrics or sustained-load packet-loss measurement. They are useful for verifying the server reaches a playable state, but they cannot serve as a load generator for this benchmark.

If using in-cluster probes as a starting point, understand their limitations: they prove the server is playable but do not establish the sustained-traffic baseline required for SC-002's latency and packet-loss measurement.

**For real sustained-traffic measurement**, use Option A (real game client) or Option B (iperf3) instead.

**Record baseline metrics** (structure depends on your chosen tool):
- **Packet loss**: packets_sent − packets_received (absolute count)
- **Packet loss %**: (packets_sent − packets_received) / packets_sent × 100
- **RTT min/avg/max/stddev**: in milliseconds (if your tool measures it)
- **Throughput**: in Kbps or Mbps (if your tool measures it)

**Example baseline run** (from iperf3):
```
Baseline (Capture OFF) via iperf3:
  packets_sent: 2048
  packets_received: 2048
  packet_loss: 0 packets (0.0%)
  rtt_min: 2.1 ms
  rtt_avg: 5.3 ms
  rtt_max: 18.5 ms
  rtt_stddev: 2.1 ms
  throughput: 512 Kbps
```

### Step 2: Capture-Enabled Run

Run the **identical** traffic generator against the **capture-enabled** server for 60 seconds, **while the active capture is running**, using the same command/parameters as Step 1.

```bash
# Start traffic against capture-enabled server (capture already running from Setup step 2)
# Use the SAME tool and parameters as Step 1 (iperf3, real game client, or your chosen method)

# Example (iperf3):
iperf3 -c $CAPTURE_ADDR -p 25565 -u -b 1M -t 60 -R > /tmp/capture-iperf.json
```

**Record capture-enabled metrics** in the same format as the baseline.

**Example capture run**:
```
Capture ON:
  packets_sent: 2048
  packets_received: 2048
  packet_loss: 0 packets (0.0%)
  rtt_min: 2.2 ms
  rtt_avg: 5.4 ms
  rtt_max: 19.1 ms
  rtt_stddev: 2.3 ms
  throughput: 511 Kbps
```

### Step 3: Analyze Results

Compare the two runs by calculating deltas:

```bash
# Extract metrics from the JSON output
BASELINE_LOSS=$(jq '.packets_sent - .packets_received' /tmp/baseline-metrics.json)
CAPTURE_LOSS=$(jq '.packets_sent - .packets_received' /tmp/capture-metrics.json)

BASELINE_RTT=$(jq '.rtt_mean_ms' /tmp/baseline-metrics.json)
CAPTURE_RTT=$(jq '.rtt_mean_ms' /tmp/capture-metrics.json)

# Calculate deltas
LOSS_INCREASE=$((CAPTURE_LOSS - BASELINE_LOSS))
RTT_DELTA_PCT=$(echo "scale=2; ($CAPTURE_RTT - $BASELINE_RTT) * 100 / $BASELINE_RTT" | bc)

echo "=== SC-002 Benchmark Results ==="
echo "Baseline packet loss: $BASELINE_LOSS packets"
echo "Capture packet loss:  $CAPTURE_LOSS packets"
echo "Loss delta:           $LOSS_INCREASE packets"
echo ""
echo "Baseline RTT (mean):  $BASELINE_RTT ms"
echo "Capture RTT (mean):   $CAPTURE_RTT ms"
echo "RTT delta:            $RTT_DELTA_PCT %"
```

### Step 4: Verify Capture File Validity

Ensure the capture file written during the test is valid and contains real traffic:

```bash
# Download the capture file
curl -s -X GET "http://localhost:8000/servers/perf-test-capture-on:capture-file?id=$CAPTURE_ID" \
  -H "Authorization: Bearer <admin-token>" \
  -o /tmp/benchmark-capture.pcapng

# Verify file integrity with tshark
tshark -r /tmp/benchmark-capture.pcapng -c 5
# Expected: Packet listing showing captured traffic (non-empty file)

# Verify file with capinfos (if available)
capinfos /tmp/benchmark-capture.pcapng
# Expected: File is valid, data block count > 0
```

---

## Pass/Fail Criteria

### PASS — All of the following must be true:

1. **Packet Loss**: Capture-enabled run loses **no more than 1 additional packet** compared to baseline:
   - `(CAPTURE_LOSS - BASELINE_LOSS) <= 1`
   - Rationale: Network randomness and the test harness itself may account for a single packet; a delta of 0 is ideal but unrealistic on a real network.

2. **Latency**: Mean RTT with capture is within **5% of baseline**:
   - `ABS(RTT_DELTA_PCT) <= 5.0`
   - Rationale: Player perception threshold for online games is typically 10–20 ms; a 5% increase (e.g., 5.3 ms → 5.57 ms) is imperceptible.

3. **Sidecar Stability**: The capture sidecar container remains Running throughout the test:
   - `kubectl get pod -l app.kubernetes.io/instance=perf-test-capture-on -o jsonpath='{.items[0].status.containerStatuses[?(@.name=="capture")].state.running}'`
   - Expected: `true` (not Terminated or CrashLoopBackOff)

4. **Capture File Valid**: The downloaded capture file is readable and non-empty:
   - `tshark -r /tmp/benchmark-capture.pcapng -c 1` succeeds without error
   - File contains at least one packet (data block count > 0)

5. **No Exceptional CPU/Memory Spikes**: Container resource usage is normal:
   - `kubectl top pod <capture-pod>` shows CPU and memory consistent with baseline game pod
   - (No spike or sustained high usage during the 60-second capture window)

### FAIL — Any of the following:

1. **Packet Loss Increases Significantly**: `(CAPTURE_LOSS - BASELINE_LOSS) > 1`

2. **Latency Degrades More Than 5%**: `ABS(RTT_DELTA_PCT) > 5.0`

3. **Sidecar Crashes**: Capture container shows Terminated or CrashLoopBackOff status

4. **Capture File Invalid**: File is empty, corrupted, or unreadable by tshark

5. **Resource Exhaustion**: Capture container CPU/memory usage spikes abnormally, or the game server becomes unresponsive during the test

---

## Automated Regression Guard (CI-Side)

**Note on CI-side overhead measurement**: A concurrent specification in `specs/done_007-capture-overhead-guard/spec.md` defines a **regression guard** for the CI pipeline — an automated check that catches gross regressions in capture overhead without requiring manual benchmark execution. That automated guard is **not a substitute for this live-cluster benchmark**. The automated check (job `e2e-capture-overhead`, non-blocking) flags a regression only on coarse signals: phase-2 sustained-join passes more than 50% below the capture-off phases, a game-container restart, or a missing/invalid capture file; RTT deltas are recorded but never fail it (see `specs/done_007-capture-overhead-guard/OPEN-DECISIONS.md` OD-8). This live-cluster SC-002 benchmark is more precise: it requires **under 5% latency increase and under 1 additional packet of loss**, thresholds too strict for an automated CI assertion on a kind cluster's synthetic network.

The two are complementary:
- **Automated regression guard** (CI): Catches accidental regressions early; runs on every push to `master` and on PRs touching the capture path (OD-2)
- **SC-002 live-cluster benchmark** (manual): Validates zero perceptible impact against real network conditions; runs before release

---

## Confounders and How to Control for Them

### 1. CI/Network Jitter (Live-Cluster-Only Concern)

**Confounder**: The kubelab cluster may experience transient network congestion, node scheduling jitter, or MetalLB assignment delays, all of which can inflate latency measurements.

**Control**:
- Run the baseline and capture-enabled tests **on the same day, in close succession** (within the same hour) to minimize network condition drift
- Run **multiple repetitions** (3–5 pairs of baseline/capture runs) and average the results rather than relying on a single run
- Record **min/max/stddev** for each run, not just the mean, to assess consistency
- If a single run shows a clear outlier (e.g., 1 run with 15% RTT increase, others with 2%), exclude the outlier and note it as "transient cluster jitter"

### 2. Node CPU Contention

**Confounder**: If other workloads are running on the cluster during the test, they may contend for CPU with the capture sidecar or the game server, inflating both baseline and capture results (and potentially hiding capture overhead).

**Control**:
- **Before the test**: Check node CPU allocation with `kubectl top nodes` and `kubectl top pods --all-namespaces`
- **Drain non-essential workloads** from the node(s) running the test servers
- **Schedule the test** during a quiet window (off-hours or a dedicated test cluster)
- **Record baseline resource utilization** before starting the test, e.g.:
  ```bash
  kubectl top pod <game-pod> > /tmp/resource-before.log
  # After test:
  kubectl top pod <game-pod> > /tmp/resource-after.log
  ```

### 3. emptyDir Disk Throughput Saturation

**Confounder**: The capture sidecar writes to an emptyDir volume. If the underlying node's disk is slow or saturated, capture file writes can block the AF_PACKET loop, causing packet drops and artificially high latency.

**Control**:
- **Check emptyDir mount location**: `kubectl describe pod <game-pod> | grep -A 5 "Mounts:" | grep captures`
  - Expected: mounted to a local node path, typically `/var/lib/kubelet/pods/.../volumes/empty-dir/...`
- **Verify node storage**: `kubectl top nodes` or `df -h` on the node to ensure the emptyDir is not on a congested filesystem
- **Pre-warm the capture file**: The first few MB of writes may be slower due to allocation; start the capture 10 seconds before traffic begins so steady-state writes are captured during the traffic window
- **Monitor disk I/O during capture**: On the node, optionally monitor with `iostat -x 1` during the test to confirm no I/O spikes

### 4. Sidecar Network Namespace Sharing

**Confounder**: The capture ephemeral container shares the game pod's network namespace. All network I/O — including AF_PACKET reads and PCAPNG writes to disk — is in the same namespace as the game container. Context switches or lock contention between the game and capture processes could theoretically inflate game-visible latency.

**Control**:
- **Kernel TCP/IP stack contention is minimal** for a single ephemeral capture container reading packets via AF_PACKET (read-only, no packet injection)
- **Disk I/O contention is the primary risk** (see emptyDir section above)
- **No control available**: Namespace sharing is a fundamental design constraint (human Decision 1). Accept that a pathological capture workload (e.g., copying 100GB of packets) would impact the game; this benchmark assumes normal capture sizes (10s of MB, not GBs)

### 5. Game State and Player Behavior Variability

**Confounder**: The game server's CPU and network usage varies based on player count, actions, and state. A different number of players or different actions during the baseline vs. capture run could mask capture overhead.

**Control**:
- **Use a scripted join bot** (not real players), so both runs exercise the exact same sequence of player actions
- **Run for a fixed duration** (60 seconds) to allow the server to reach steady-state behavior
- **Record player count and behavior**: E.g., "bot connects, sends 3 chat messages, waits 30 seconds, disconnects"
- **Verify server state is stable**: Check `kubectl get gameserver` to confirm both servers show the same `status.players.online` or equivalent metric before each run
- **Use the exact same seed/map/world state** across both servers if possible (copy the game-data volume from one to the other)

---

## Execution Checklist

Before running the benchmark, print and check off each item:

- [ ] Live cluster is accessible and Gameplane is fully deployed
- [ ] Both test GameServers are Running and LoadBalancer addresses are assigned
- [ ] Admin API token is available and has `captures:manage` permission
- [ ] Game-client join bot or synthetic traffic tool is ready
- [ ] Network monitoring tools are installed (iperf3, ping, tshark, capinfos)
- [ ] No exceptional workloads are running on the test node(s)
- [ ] Node storage is not congested (disk < 80% full)
- [ ] Baseline and capture-enabled metrics scripts are prepared
- [ ] Results directory `/tmp/` is writable and has > 500 MB free space

---

## Running the Benchmark (Full Walkthrough)

```bash
#!/bin/bash
set -e

# Configuration
BASELINE_ADDR="192.0.2.10"  # Replace with actual LoadBalancer IP
CAPTURE_ADDR="192.0.2.11"   # Replace with actual LoadBalancer IP
ADMIN_TOKEN="<bearer-token>"
RUN_COUNT=3  # 3 repetitions of baseline/capture pair
TRAFFIC_DURATION_SEC=60
RESULTS_DIR="/tmp/sc-002-benchmark-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$RESULTS_DIR"

echo "SC-002 Benchmark: Zero Perceptible Player-Experience Impact"
echo "Results directory: $RESULTS_DIR"
echo ""

# Function to run traffic and collect metrics
run_traffic() {
    local server_addr="$1"
    local label="$2"
    local output_file="$3"
    
    echo "Running $label test against $server_addr for ${TRAFFIC_DURATION_SEC}s..."
    
    # Use iperf3 for this example (replace with your game-bot tool)
    iperf3 -c "$server_addr" -p 25565 -u -b 1M -t "$TRAFFIC_DURATION_SEC" -J > "$output_file" 2>&1
    
    echo "  Completed. Results saved to $output_file"
}

# Function to extract metrics from iperf3 JSON output
extract_metrics() {
    local json_file="$1"
    
    # Parse iperf3 output (adjust for your tool's output format)
    local packets_sent=$(jq '.end.sum.packets' "$json_file")
    local packet_loss=$(jq '.end.sum.lost_packets' "$json_file")
    local loss_pct=$(jq '.end.sum.lost_percent' "$json_file")
    
    echo "packets_sent=$packets_sent,packet_loss=$packet_loss,loss_pct=$loss_pct"
}

# Warm up servers
echo "Warming up servers for 10 seconds..."
run_traffic "$BASELINE_ADDR" "warmup" "$RESULTS_DIR/warmup.json"
sleep 5

# Run benchmark repetitions
baseline_losses=()
capture_losses=()

for run in $(seq 1 $RUN_COUNT); do
    echo ""
    echo "=== Run $run / $RUN_COUNT ==="
    
    # Baseline run
    run_traffic "$BASELINE_ADDR" "baseline" "$RESULTS_DIR/run${run}-baseline.json"
    baseline_metrics=$(extract_metrics "$RESULTS_DIR/run${run}-baseline.json")
    baseline_loss=$(echo "$baseline_metrics" | cut -d, -f2)
    baseline_losses+=("$baseline_loss")
    echo "  Baseline packet loss: $baseline_loss packets"
    
    sleep 5  # Pause between runs
    
    # Capture run
    run_traffic "$CAPTURE_ADDR" "capture" "$RESULTS_DIR/run${run}-capture.json"
    capture_metrics=$(extract_metrics "$RESULTS_DIR/run${run}-capture.json")
    capture_loss=$(echo "$capture_metrics" | cut -d, -f2)
    capture_losses+=("$capture_loss")
    echo "  Capture packet loss:  $capture_loss packets"
    
    sleep 5  # Pause before next repetition
done

# Analyze results
echo ""
echo "=== Analysis ==="
avg_baseline_loss=$(echo "${baseline_losses[@]}" | awk '{s=0; for(i=1;i<=NF;i++) s+=$i; print s/NF}')
avg_capture_loss=$(echo "${capture_losses[@]}" | awk '{s=0; for(i=1;i<=NF;i++) s+=$i; print s/NF}')
loss_delta=$(echo "$avg_capture_loss - $avg_baseline_loss" | bc)

echo "Baseline average packet loss: $avg_baseline_loss packets"
echo "Capture average packet loss:  $avg_capture_loss packets"
echo "Loss delta:                   $loss_delta packets"
echo ""

if (( $(echo "$loss_delta <= 1.0" | bc -l) )); then
    echo "PACKET LOSS: PASS (delta <= 1 packet)"
else
    echo "PACKET LOSS: FAIL (delta > 1 packet)"
fi

echo ""
echo "Results saved to: $RESULTS_DIR"
echo "Full run data:"
ls -lh "$RESULTS_DIR"
```

---

## Interpreting Results

### Expected Outcome (PASS)

```
=== Analysis ===
Baseline average packet loss: 0 packets
Capture average packet loss:  0 packets
Loss delta:                   0 packets
Baseline average RTT:         5.3 ms
Capture average RTT:          5.4 ms
RTT delta:                    1.89 %

PACKET LOSS: PASS (delta <= 1 packet)
LATENCY:     PASS (delta <= 5%)
SIDECAR:     PASS (running, no restarts)
CAPTURE_FILE: PASS (valid PCAPNG, 2048 packets)

Result: SC-002 PASS
```

### Concerning Outcome (FAIL)

```
=== Analysis ===
Baseline average packet loss: 0 packets
Capture average packet loss:  5 packets
Loss delta:                   5 packets
Baseline average RTT:         5.3 ms
Capture average RTT:          8.2 ms
RTT delta:                    54.72 %

PACKET LOSS: FAIL (delta > 1 packet)
LATENCY:     FAIL (delta > 5%)
SIDECAR:     FAIL (CrashLoopBackOff after 30 seconds)

Result: SC-002 FAIL

Investigation notes:
- Sidecar crashed; check logs: kubectl logs -n <ns> <pod> -c capture
- Likely causes: OOM, CAP_NET_RAW not effective, disk full (check emptyDir mount)
- Rerun after fixing; see Troubleshooting section above
```

---

## Troubleshooting During the Benchmark

### Symptom: Sidecar Crashes Mid-Test

**Check sidecar logs**:
```bash
POD=$(kubectl get pod -l app.kubernetes.io/instance=perf-test-capture-on -o jsonpath='{.items[0].metadata.name}')
kubectl logs "$POD" -c capture --tail=50
```

**Common failures**:
- `ENOSPC`: Disk full on the node. Check: `df -h /var/lib/kubelet`
- `Operation not permitted (EPERM)`: CAP_NET_RAW not effective. Check: `getcap /path/to/capture-binary` in the image
- `Out of memory`: Capture buffer size too large. Check: `kubectl describe pod "$POD" | grep -i "memory\|oom"`

### Symptom: Baseline Server Shows High Packet Loss

**Verify server is healthy**:
```bash
kubectl describe gameserver perf-test-capture-off
kubectl logs -l app.kubernetes.io/instance=perf-test-capture-off --all-containers=true --tail=20
```

**Check network connectivity**:
```bash
kubectl run -it --rm test-pod --image=busybox -- ping -c 5 perf-test-capture-off
```

### Symptom: RTT Varies Wildly Between Runs

**Likely cause**: Network jitter from external cluster load.

**Control**: Add more warmup runs before the benchmark (10–20 seconds), and ensure no cluster-wide workloads are running.

---

## Recording the Result

Once the benchmark is complete, record the outcome and data in this document or in a separate file:

```markdown
## Benchmark Result: [DATE]

**Status**: PASS / FAIL / NOT YET RUN

**Test Environment**:
- Cluster: [kubelab / other]
- Game module: [minecraft-java / terraria / factorio]
- Traffic tool: [iperf3 / game-bot / other]

**Metrics**:
- Baseline packet loss (avg): [X packets]
- Capture packet loss (avg):  [X packets]
- Loss delta:                 [X packets]
- Baseline RTT (avg):         [X ms]
- Capture RTT (avg):          [X ms]
- RTT delta:                  [X%]

**Pass/Fail Determination**:
- Packet loss delta <= 1: [PASS/FAIL]
- RTT delta <= 5%:        [PASS/FAIL]
- Sidecar stable:         [PASS/FAIL]
- Capture file valid:     [PASS/FAIL]

**Overall Result**: [PASS/FAIL/INCONCLUSIVE]

**Notes**:
[Any observations, confounders, or issues encountered]

**Raw Data Location**: [/tmp/sc-002-benchmark-TIMESTAMP/]
```

---

## Summary

This procedure establishes whether the capture sidecar introduces measurable player-experience degradation. The benchmark is manual and live-cluster-only because:

1. CI's kind cluster network is too synthetic to reflect real-world latency variance
2. Automated assertions on sub-5% latency deltas would be flaky and provide no useful signal
3. Real-world validation requires real or realistically-simulated game traffic

The maintainer executing this benchmark is responsible for:
- Controlling confounders (cluster load, storage saturation, etc.)
- Interpreting results against the pass/fail criteria
- Investigating failures and determining if they are sidecar issues or environmental

**As of this document's creation, this benchmark has NOT been executed. No measurement has been taken, and no pass/fail result is recorded.**
