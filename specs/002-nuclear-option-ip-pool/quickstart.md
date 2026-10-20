# Quickstart & Validation Guide: Nuclear Option Module & IP Pool Override

**Phase 1 Validation for Spec 002**

This guide provides runnable validation scenarios that prove each success criterion (SC-001 through SC-008) has been met. It covers both the Nuclear Option game module deployment and the load-balancer IP pool override feature.

---

## Reading Map

- **Success Criteria Summary**: See "What PASS Looks Like" checklist below; maps SC-001..SC-008 to observable proof.
- **Implementation Detail**: Refer to `spec.md` (Functional Requirements, Key Entities, Assumptions); this guide focuses on *validation*, not implementation code.
- **Data Model & Join Coverage**: Reference `specs/done_001-gameprotocol-e2e-coverage/data-model.md` for JoinDepth, CoverageRecord, and the vocabulary used in `docs/game-coverage.md`.
- **Test Buckets**: Reference `test/e2e/buckets.sh` for bucket names and e2e test organization.

---

## Prerequisites

This validation has two independent tracks. **Start with Track B (IP Pool Override)** — it requires no game binary and no special setup beyond your cluster's address manager. Once Track B is validated, proceed to Track A (Nuclear Option Module), which requires the dedicated server binary and real test infrastructure.

### Track B Prerequisites: Load-Balancer IP Pool Override

**Cluster Requirements**:
1. A Kubernetes cluster (kind via `make dev-up`, or a live remote k3s cluster) with a configured load-balancer address manager:
   - **MetalLB** (e.g., from `make dev-up`, which auto-installs MetalLB v0.13+)
   - **Cilium** (native Cilium IPAM / LB IPAM)
   - Other CNCF-standard address managers compatible with Service `status.loadBalancer.ingress` status reporting and annotation/label-driven pool hints

2. **Cluster State**:
   - Gameplane operator and API installed (via `make dev-install` for kind, or `helm upgrade --install` for remote)
   - At least two named address pools configured in the address manager (e.g., `pool-us-east` and `pool-us-west` for MetalLB, or equivalent for Cilium)
   - Default pool configured and active

3. **Validation Setup**:
   - `kubectl` access to the cluster
   - Ability to inspect Service annotations/labels and `status.loadBalancer` fields
   - For MetalLB: ability to read Events on a Service
   - For Cilium: ability to read Service Conditions

### Track A Prerequisites: Nuclear Option Module

**Unverified Preconditions** (must be confirmed before proceeding):

1. **Game Binary Availability** — Read `spec.md` "Verification Required Before Implementation, Claim 1". Confirm:
   - Nuclear Option dedicated server (Steam app 3930080) is publicly downloadable without base-game ownership
   - Native Linux binary (`NuclearOptionServer.x86_64`) exists and runs on Linux
   - You have or can generate a test account to download it
   - Expected: SteamCMD download via `+login anonymous +app_update 3930080 validate +quit`

2. **Network Footprint** — Read `spec.md` "Claim 2". Confirm:
   - Dedicated server listens on UDP 7777 (game), UDP 7778 (query), TCP 7779 (remote-command)
   - Server startup expects flags like `-ServerRemoteCommands 7779`
   - **Note**: Remote-command port has NO AUTHENTICATION — it must never be exposed outside the pod

3. **Remote-Command Protocol** — Read `spec.md` "Claim 3". Confirm:
   - Request format: 4-byte little-endian length + UTF-8 JSON body
   - Response format: 4-byte status code (e.g., 2000 = Success) + 4-byte body length + UTF-8 JSON body
   - Supported commands exist: `get-player-list`, `kick-player`, `banlist-add`, `banlist-remove`, `send-chat-message`, `set-next-mission`

4. **Readiness Signal** — Read `spec.md` "Claim 4". Confirm the log line or signal that indicates "accepting players":
   - Expected log marker: `[DedicatedServerManager] Waiting for Players before loading next map`
   - Alternative (if above does not exist): readiness derived from successful join probe

5. **Log Accessibility** — Read `spec.md` "Claim 5". Confirm:
   - Server logs are written to a pod-accessible location (e.g., `/game/logs/` or within the working directory)
   - Format is readable (plain text, JSON, or structured)
   - Agent pod can read logs via volume mount or log tail

**Cluster & Test Infrastructure**:
- A test Kubernetes cluster with sufficient resources:
  - **Local kind cluster** (`make dev-up`): May be too small for heavy games (spec notes 8–16 GB RAM requirement); useful for **lightweight validation only**
  - **Remote kubelab cluster** (`CLUSTER=remote REMOTE_KUBECONFIG=~/kubelab.yaml`): Preferred for real e2e validation; has more resources
  - Minimum for Nuclear Option test: 8 GB RAM, 2–4 CPU cores, 30 GB storage allocated to the test pod
- Gameplane operator, API, and agent installed and operational
- Real-time access to pod logs and kubectl exec
- A real game client binary (Nuclear Option) or a protocol probe test ready to join

---

## Track B Validation: Load-Balancer IP Pool Override (Independent of Nuclear Option)

Complete this track first. The IP pool feature works standalone and requires no game binary.

### B.1: Verify Address Manager & Pools Are Configured

**Objective**: Confirm the cluster's address manager is installed and has multiple named pools ready.

#### For MetalLB:

```bash
# Confirm MetalLB is installed
kubectl get deployment -n metallb-system -o name
# Expected: deployment.apps/metallb-controller, deployment.apps/metallb-speaker (or similar)

# List configured address pools (MetalLB ConfigMap)
kubectl get configmap -n metallb-system -o yaml | grep -A 100 "address-pools:"
# Expected: at least two pool entries with names like "pool-us-east" and "pool-us-west"
# Example structure:
#   address-pools:
#   - name: pool-us-east
#     protocol: layer2
#     addresses:
#     - 192.0.2.100-192.0.2.110
#   - name: pool-us-west
#     protocol: layer2
#     addresses:
#     - 192.0.2.200-192.0.2.210

# Confirm pool addresses are reachable and assigned (layer2 needs them reachable on the network)
```

**PASS Criteria**:
- Two or more named pools exist
- Each pool has a distinct address range
- MetalLB controller is running

#### For Cilium:

```bash
# Confirm Cilium is installed
kubectl get daemonset -n kube-system cilium -o name
# Expected: daemonset.apps/cilium (or similar)

# List LB IP pools (Cilium CiliumLoadBalancerIPPool)
kubectl get ciliumloadbalancerippool -o yaml
# Expected: at least two CiliumLoadBalancerIPPool objects with names like "pool-us-east" and "pool-us-west"

# Confirm Cilium IPAM is enabled
kubectl get ciliumnode -o yaml | grep loadbalancer
```

**PASS Criteria**:
- Two or more LB IP pools exist as Cilium CRDs
- Each pool has distinct IP ranges configured
- Cilium control plane is operational

### B.2: Create a GameServer Without Pool Preference (SC-006: Backward Compatibility)

**Objective**: Establish baseline: servers without pool preference behave unchanged.

#### Setup:

```bash
# Create a simple test GameServer (any lightweight game, e.g., terraria-tiny)
# Use the dashboard or REST API to create a GameServer:
#   - Game: terraria (or similar lightweight module)
#   - Exposure: LoadBalancer
#   - Pool Preference: (leave empty)
#   - Name: test-no-pool

# Alternatively, via kubectl:
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: test-no-pool
  namespace: default
spec:
  template:
    name: terraria  # lightweight for quick validation
  networking:
    exposeMode: LoadBalancer
    # poolPreference intentionally absent
EOF
```

#### Validation:

```bash
# Wait for Service to be created (watch status)
kubectl get svc test-no-pool -w
# Expected: Service of type LoadBalancer created

# Check assigned address (should come from default pool, no special annotation)
kubectl get svc test-no-pool -o yaml | grep -A 5 "status:"
# Expected: status.loadBalancer.ingress[0].ip or .hostname contains an address from the default pool

# Measure time from creation to address assignment
# Record: time_to_address (should be < 30s per SC-008)
```

**PASS Criteria (SC-006)**:
- Service is created without pool-specific annotation/label
- Address is assigned from the default pool
- Behavior is identical to pre-pool-override behavior (no regression)

### B.3: Create a GameServer WITH Pool Preference (SC-002: Pool Assignment)

**Objective**: Verify that a requested pool is honored.

#### Setup:

```bash
# Create a GameServer with pool preference set to "pool-us-east"
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: test-with-pool-east
  namespace: default
spec:
  template:
    name: terraria
  networking:
    exposeMode: LoadBalancer
    poolPreference: pool-us-east  # REQUEST a specific pool
EOF

# Also create one requesting "pool-us-west" for comparison
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: test-with-pool-west
  namespace: default
spec:
  template:
    name: terraria
  networking:
    exposeMode: LoadBalancer
    poolPreference: pool-us-west
EOF
```

#### Validation:

```bash
# Wait for both Services to be created and assigned addresses
kubectl get svc test-with-pool-east test-with-pool-west -w

# Capture assigned addresses
ADDR_EAST=$(kubectl get svc test-with-pool-east -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
ADDR_WEST=$(kubectl get svc test-with-pool-west -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

echo "test-with-pool-east assigned: $ADDR_EAST (should be in pool-us-east range)"
echo "test-with-pool-west assigned: $ADDR_WEST (should be in pool-us-west range)"

# Verify addresses fall within the requested pool ranges
# For MetalLB pools:
#   - pool-us-east addresses must fall in 192.0.2.100-192.0.2.110
#   - pool-us-west addresses must fall in 192.0.2.200-192.0.2.210
# Adjust the expected ranges based on your cluster's actual pool configuration
```

**PASS Criteria (SC-002)**:
- `test-with-pool-east` is assigned an address from the pool-us-east range
- `test-with-pool-west` is assigned an address from the pool-us-west range
- Time to assignment is < 30s (SC-008)
- Pool assignment works 100% of the time (no random fallbacks)

### B.4: Trigger Pool Misconfiguration Errors (SC-003: Error Clarity)

**Objective**: Verify that each type of pool misconfiguration surfaces a specific, actionable error within 30 seconds.

#### Scenario B.4a: Nonexistent Pool Name

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: test-error-pool-not-found
  namespace: default
spec:
  template:
    name: terraria
  networking:
    exposeMode: LoadBalancer
    poolPreference: nonexistent-pool-xyz  # Pool does not exist
EOF

# Wait up to 30s and check status
for i in {1..6}; do
  sleep 5
  STATUS=$(kubectl get gameserver test-error-pool-not-found -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}')
  echo "[$i*5s] Status: $STATUS"
  if echo "$STATUS" | grep -qi "pool.*not found\|nonexistent"; then
    echo "✓ PASS: Pool-not-found error surfaced within 30s"
    break
  fi
done
# Expected message: "Pool 'nonexistent-pool-xyz' not found in cluster" or similar
```

**PASS Criteria**:
- Status message appears within 30s
- Message names the specific pool that was not found
- Message is actionable (tells operator to choose a different pool)

#### Scenario B.4b: Pool Exhausted (No Available Addresses)

```bash
# This scenario requires a pool with a very small address range or one already in use.
# For testing, you can temporarily edit the MetalLB ConfigMap to limit pool-us-east to a single address,
# then create enough GameServers to exhaust it:

# Create servers until the pool is exhausted
for i in {1..5}; do
  kubectl apply -f - <<EOF
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: test-exhaust-$i
  namespace: default
spec:
  template:
    name: terraria
  networking:
    exposeMode: LoadBalancer
    poolPreference: pool-us-east  # Use the limited pool
EOF
done

# The last one or two should fail with pool exhausted
kubectl get gameserver test-exhaust-5 -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
# Expected: "Address pool 'pool-us-east' is exhausted; no addresses available"
```

**PASS Criteria**:
- When all addresses in a pool are consumed, the status shows a clear "exhausted" message within 30s
- Message is not a hanging "Pending" state
- Operator can understand the fix (free an address or use a different pool)

#### Scenario B.4c: Requested Address Already in Use

```bash
# First, get the address assigned to test-with-pool-east
EXISTING_ADDR=$(kubectl get svc test-with-pool-east -o jsonpath='{.status.loadBalancer.ingress[0].ip}')

# Try to create a new server requesting that same address
# (Requires a GameServer CRD field for explicit address request; check spec FR-015)
# If the field is implemented as addressPreference:
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: test-error-address-in-use
  namespace: default
spec:
  template:
    name: terraria
  networking:
    exposeMode: LoadBalancer
    addressPreference: $EXISTING_ADDR  # Request an address already in use
EOF

# Check status within 30s
kubectl get gameserver test-error-address-in-use -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
# Expected: "Address $EXISTING_ADDR is already in use by test-with-pool-east; choose a different address or pool"
```

**PASS Criteria**:
- Address-in-use error surfaces within 30s
- Error names the specific address and the conflicting server
- Operator can see a link or reference to the conflicting server

#### Scenario B.4d: Wrong Exposure Mode (LoadBalancer Required)

```bash
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: test-error-wrong-mode
  namespace: default
spec:
  template:
    name: terraria
  networking:
    exposeMode: ClusterIP  # Not LoadBalancer
    poolPreference: pool-us-east  # Pool preference is incompatible here
EOF

# Check status
kubectl get gameserver test-error-wrong-mode -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
# Expected: "Pool preference is ignored when exposure mode is not 'Load Balancer'" or similar warning
```

**PASS Criteria**:
- Dashboard or status shows a warning that pool preference is ignored
- No address is assigned (ClusterIP has none)
- Message is clear about why the setting has no effect

### B.5: Verify Address Visibility in Dashboard (SC-008: Timely Address Display)

**Objective**: Confirm the assigned address is visible in the dashboard within 30 seconds.

#### Setup Dashboard Port-Forward:

```bash
# Terminal 1: Port-forward the API service
kubectl port-forward -n gameplane-system svc/gameplane-api 8000:8000

# Terminal 2: Start the Vite dev server
cd web
npm run dev  # Listens on http://localhost:5173 (proxies API calls to :8000)
```

#### Validation:

1. Open dashboard at `http://localhost:5173`
2. Navigate to **Servers** page
3. Open the detail page for `test-with-pool-east`
4. Check **Networking** section:
   - Should show "Assigned Address" with the IP from pool-us-east
   - Should show "Pool" field with value "pool-us-east"
   - Should appear within 30s of server creation

**PASS Criteria (SC-008)**:
- Address is displayed in the dashboard within 30s
- Pool name is displayed if applicable
- Address matches the actual assigned Service IP

---

## Track A Validation: Nuclear Option Module (Requires Game Binary)

Complete Track B first. Track A requires the dedicated server binary and a test infrastructure capable of running it.

### A.1: Verify Unverified Preconditions (Before Implementation)

Before proceeding, confirm the five unverified claims from `spec.md`:

#### Claim 1: Dedicated Server Availability & Linux Build

```bash
# Attempt to download the server binary
steamcmd +force_install_dir ./nuclear_server +login anonymous +app_update 3930080 validate +quit

# Expected:
#   - Download succeeds without requiring the base game (app 2168680)
#   - File exists: ./nuclear_server/NuclearOptionServer.x86_64
#   - File is executable (not a Windows-only build or Proton layer)

# Test basic startup:
cd nuclear_server
./RunServer.sh  # May fail waiting for config, but should execute
# Expected: Logs appear (even if it immediately fails on missing config)

# **UNVERIFIED**: If download fails or binary is unavailable, the entire module cannot ship.
# Fallback: Document the licensing blocker and defer the module to a future release.
```

**Result**: Document in the quickstart whether Claim 1 is **VERIFIED** or **BLOCKED**.

#### Claim 2: Network Ports

```bash
# Start the server (if Claim 1 passed)
# Capture network state before and after startup
netstat -tulnp | grep NuclearOptionServer

# Expected output (after server starts):
#   udp  0  0 0.0.0.0:7777  0.0.0.0:*  <pid>/NuclearOptionServer
#   udp  0  0 0.0.0.0:7778  0.0.0.0:*  <pid>/NuclearOptionServer
#   tcp  0  0 0.0.0.0:7779  0.0.0.0:*  <pid>/NuclearOptionServer  (only if -ServerRemoteCommands flag is set)

# **UNVERIFIED**: If ports differ from 7777/7778/7779, update the module template accordingly.
```

**Result**: Document the actual ports. Expected: **VERIFIED** as 7777/7778/7779.

#### Claim 3: Remote-Command Protocol Format

```bash
# Start the server with remote-command port enabled
./RunServer.sh -ServerRemoteCommands 7779

# From another terminal, test the protocol
# Send a get-player-list request (should be empty at startup)
(
  # Build request: length prefix + JSON body
  JSON='{"name":"get-player-list","arguments":[]}'
  LEN=$(printf '%s' "$JSON" | wc -c)
  # Little-endian 4-byte length
  printf "\\x$(printf '%02x' $((LEN & 0xFF)))\\x$(printf '%02x' $(((LEN >> 8) & 0xFF)))\\x$(printf '%02x' $(((LEN >> 16) & 0xFF)))\\x$(printf '%02x' $(((LEN >> 24) & 0xFF)))"
  printf '%s' "$JSON"
) | nc -q 1 localhost 7779 | od -A x -t x1z

# Expected response:
#   - First 4 bytes: little-endian status code (e.g., 0xd0 0x07 0x00 0x00 = 2000 = Success)
#   - Next 4 bytes: little-endian body length
#   - Remaining bytes: UTF-8 JSON response body (e.g., {"players": []})

# **UNVERIFIED**: If response format differs, rework the moderation command implementation (FR-007–FR-012).
```

**Result**: Document the exact protocol. Expected: **VERIFIED** as length-prefixed JSON with status code 2000 for success.

#### Claim 4: Readiness Signal

```bash
# Start the server and observe logs
./RunServer.sh | tee server.log

# Watch for the readiness marker
grep "Waiting for Players before loading next map" server.log
# Expected: log line appears within ~1–2 minutes of startup

# **UNVERIFIED**: If this line does NOT appear, readiness must be inferred from a successful join probe (slower, ~10–30s overhead).
```

**Result**: Document readiness detection. Expected: **VERIFIED** as log line within ~60s.

#### Claim 5: On-Disk Log Location & Format

```bash
# After server starts, find log files
find ~/nuclear_server -name "*.log" -o -name "logs" -type d

# Expected: Log files in a path like:
#   ~/nuclear_server/logs/
#   ~/nuclear_server/Saved/Logs/  (varies by game)

# Test readability
tail -f ~/nuclear_server/logs/*.log
# Expected: Text or JSON logs, human-readable or parseable

# **UNVERIFIED**: If logs are inaccessible or binary, document this as a limitation.
```

**Result**: Document actual log location. Expected: **VERIFIED** with path and format.

---

### A.2: Deploy Nuclear Option Module to Catalog (SC-001: Deployability)

**Objective**: Add the Nuclear Option module to the modules/ submodule and verify it appears in the dashboard catalog.

#### Setup:

Assuming the five unverified claims have been verified (or marked as blockers), create the module:

```bash
# Fetch/initialize the gameplane-module submodule
git submodule update --init modules

# Create a new module directory
mkdir -p modules/nuclear-option

# Create module.yaml with discovered configuration
cat > modules/nuclear-option/module.yaml <<'EOF'
name: "Nuclear Option"
displayName: "Nuclear Option"
version: "1.0"
description: "Nuclear Option dedicated server"
icon: "icon.png"  # Optional: provide an icon
template: "template.yaml"
# Ports and resources configured in template.yaml
EOF

# Create template.yaml with server resource defaults and configuration schema
# (See spec FR-002, FR-003 for required fields: CPU 2–4, RAM 8–16GB, storage 30GB)
cat > modules/nuclear-option/template.yaml <<'EOF'
apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
metadata:
  name: nuclear-option
spec:
  image: "ghcr.io/gameplanepanel/gameplane/nuclear-option:latest"  # Build this image
  resources:
    requests:
      cpu: "2"
      memory: "8Gi"
      storage: "30Gi"
    limits:
      cpu: "4"
      memory: "16Gi"
  configurationSchema:
    serverName:
      type: "string"
      maxLength: 64
      description: "Display name for the server"
    password:
      type: "string"
      description: "Server password (optional)"
    maxPlayers:
      type: "integer"
      minimum: 1
      maximum: 64
      description: "Maximum concurrent players"
    missionRotation:
      type: "array"
      items:
        type: "string"
      description: "List of available missions"
  ports:
    - name: "game"
      port: 7777
      protocol: "UDP"
      description: "Game join port"
    - name: "query"
      port: 7778
      protocol: "UDP"
      description: "Server query port"
    - name: "rcon"
      port: 7779
      protocol: "TCP"
      description: "Remote console (internal only, never expose externally)"
EOF

# Create README.md documenting the module
cat > modules/nuclear-option/README.md <<'EOF'
# Nuclear Option Module

Nuclear Option dedicated server for Gameplane.

## Configuration

- **serverName**: Display name (1–64 characters)
- **password**: Optional server password
- **maxPlayers**: 1–64 players
- **missionRotation**: Array of mission names from the available mission set

## Remote Console

The remote console (TCP 7779) supports commands:
- `get-player-list`: List connected players with Steam IDs and factions
- `kick-player`: Disconnect a player by Steam ID
- `banlist-add` / `banlist-remove`: Manage the ban list
- `send-chat-message`: Broadcast to all players
- `set-next-mission`: Advance to the next mission in rotation

**Security**: The remote-command port has NO AUTHENTICATION. It must never be exposed outside the pod.

## Status & Logs

Readiness is indicated by the log line:
```
[DedicatedServerManager] Waiting for Players before loading next map
```

Logs are written to: `/game/logs/` (or per-game variation confirmed during development)

## Known Limitations

- Mods are not supported (unmodded server only)
- Mission authoring must be done externally
- Address reassignment requires a pod restart

EOF

# Create icon.png if available (optional, but recommended for the dashboard)
# For now, you can skip this or use a placeholder

# Push the module to the registry
cd ../..  # Back to repo root
make modules-push MODULE_REGISTRY=localhost:5001
```

#### Validation:

```bash
# Wait for the operator to index the module (watch for ~10s)
kubectl get module -n gameplane-system -w
# Expected: `nuclear-option` CRD appears

# Navigate to the dashboard Modules page
# Expected: Nuclear Option appears in the catalog with its description and icon

# Click "Deploy"
# Expected: Server configuration form appears with fields for serverName, password, maxPlayers, etc.
```

**PASS Criteria (SC-001, first part: deployability)**:
- Module is listed in the dashboard catalog
- Configuration form matches the schema defined in template.yaml
- Server creation button works (ServiceServer CRD is submitted)

### A.3: Wait for Server Boot & Readiness (SC-001, SC-008)

**Objective**: Deploy a server, wait for it to boot, and confirm readiness within expected time.

#### Setup:

```bash
# Create a GameServer via the dashboard or kubectl
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: nuclear-test-1
  namespace: default
spec:
  template:
    name: nuclear-option
  config:
    serverName: "Test Server"
    password: "testpass"
    maxPlayers: 16
    missionRotation:
      - "Mission_Classic"
      - "Mission_Casual"
  networking:
    exposeMode: LoadBalancer
    # poolPreference: (optional, for Track B validation)
EOF

# Watch the pod startup
kubectl get pod -l gameplane.io/gameserver=nuclear-test-1 -w
# Expected: Pod transitions from Pending → Running (~2–5 minutes depending on image download)
```

#### Validation:

```bash
# Once pod is Running, check the readiness marker in logs
READINESS_TIME=$(kubectl logs -l gameplane.io/gameserver=nuclear-test-1 | \
  grep -m1 "Waiting for Players before loading next map" | \
  date -f - +%s 2>/dev/null || echo "NOT_FOUND")

if [ "$READINESS_TIME" != "NOT_FOUND" ]; then
  echo "✓ PASS: Server reached accepting-players state at $READINESS_TIME"
else
  echo "✗ FAIL: Readiness marker not found in logs (check log location, Claim 5)"
fi

# Dashboard check: Navigate to server detail page
# Expected: Server status shows "Accepting Players" or "Ready"
# Expected: Assigned address is visible in Networking section
```

**PASS Criteria (SC-001, SC-008)**:
- Pod reaches Running state
- Readiness marker appears in logs within 3–5 minutes
- Dashboard shows status as "Accepting Players" within 30s of readiness
- Assigned address is visible in Networking section

### A.4: Execute a Real Player Join (SC-001, SC-005: Join-Coverage)

**Objective**: Confirm a real game client can join the server (or a test probe can complete the join handshake).

#### Setup:

This step requires either:
1. A real Nuclear Option game client (manual join by a human)
2. A protocol probe test (automated bot)

**Option A: Manual Real-Client Join** (recommended for initial validation):

```bash
# Get the server's public address
PUBLIC_ADDR=$(kubectl get svc nuclear-test-1 -o jsonpath='{.status.loadBalancer.ingress[0].ip}')
echo "Server address: $PUBLIC_ADDR:7777"

# Connect using the real game client (on another machine with the game installed)
# Launch the game and connect to $PUBLIC_ADDR:7777
# Expected: Server appears in server browser and join succeeds
# Expected: Player appears in the server's player list
```

**Option B: Protocol Probe Test** (automated, recorded for CI):

```bash
# Create a protocol probe test in test/e2e/ that:
# 1. Dials the server on UDP 7777
# 2. Sends the first join handshake packet (client hello / server list request)
# 3. Receives a server response packet proving the server is listening and responding
# 4. Completes the full login sequence (if documented) and observes a post-join artifact

# Example probe structure (see test/e2e/gameprobe/ for full implementations):
# test/e2e/gameprobe/nuclear-option/probe.go:
#   - Dial server on UDP 7777
#   - Send client handshake (to be documented from real-client capture)
#   - Assert server responds with expected packet type
#   - Upgrade to PARTIAL or JOINED based on depth of handshake completed

# Run the test locally (if permitted) or via CI:
# make test-e2e-bucket BUCKET=bot-fast  (if assigned to bot-fast, unlikely for heavy game)
# make test-e2e-bucket BUCKET=bot-heavy  (if deferred to bot-heavy)
```

**PASS Criteria (SC-001, SC-005)**:
- Real client join succeeds within 5 minutes from deployment
- Client receives a successful connection acknowledgment from the server
- Player appears in the server's player list (visible via Remote Console: `get-player-list`)
- **For covered-in-ci**: Test is authored, verifies JOINED depth, passes on every CI run (added to bot-fast bucket)
- **For covered-deferred**: Test is authored, verifies JOINED depth, passes on manual runs (added to bot-heavy bucket with lastVerified date)
- **For blocked-doc or out-of-scope-by-design**: Protocol is documented as inaccessible with specific reason (see game-coverage.md data model)

### A.5: Test Moderation Commands (SC-004: Remote Console)

**Objective**: Execute moderation commands and verify they execute within 5 seconds.

#### Setup:

First, ensure at least one player is connected to the test server (from A.4).

#### Validation:

```bash
# Connect to the remote-command port (TCP 7779) via port-forward
kubectl port-forward pod/nuclear-test-1-xyz 7779:7779 &
PF_PID=$!

# Give port-forward time to establish
sleep 2

# Test 1: Get player list
echo "Testing: get-player-list"
(
  JSON='{"name":"get-player-list","arguments":[]}'
  LEN=$(printf '%s' "$JSON" | wc -c)
  printf "\\x$(printf '%02x' $((LEN & 0xFF)))\\x$(printf '%02x' $(((LEN >> 8) & 0xFF)))\\x$(printf '%02x' $(((LEN >> 16) & 0xFF)))\\x$(printf '%02x' $(((LEN >> 24) & 0xFF)))"
  printf '%s' "$JSON"
) | nc -q 1 localhost 7779 | hexdump -C
# Expected: Status code 2000 + JSON body with player array

# Test 2: Broadcast message
echo "Testing: send-chat-message"
(
  JSON='{"name":"send-chat-message","arguments":["Hello from admin"]}'
  LEN=$(printf '%s' "$JSON" | wc -c)
  printf "\\x$(printf '%02x' $((LEN & 0xFF)))\\x$(printf '%02x' $(((LEN >> 8) & 0xFF)))\\x$(printf '%02x' $(((LEN >> 16) & 0xFF)))\\x$(printf '%02x' $(((LEN >> 24) & 0xFF)))"
  printf '%s' "$JSON"
) | nc -q 1 localhost 7779 | hexdump -C
# Expected: Status code 2000 + confirmation JSON

# Test 3: Set next mission
echo "Testing: set-next-mission"
(
  JSON='{"name":"set-next-mission","arguments":["Mission_Casual"]}'
  LEN=$(printf '%s' "$JSON" | wc -c)
  printf "\\x$(printf '%02x' $((LEN & 0xFF)))\\x$(printf '%02x' $(((LEN >> 8) & 0xFF)))\\x$(printf '%02x' $(((LEN >> 16) & 0xFF)))\\x$(printf '%02x' $(((LEN >> 24) & 0xFF)))"
  printf '%s' "$JSON"
) | nc -q 1 localhost 7779 | hexdump -C
# Expected: Status code 2000 + mission change confirmation

# If a player is connected, they should see the message in-game within ~5 seconds

kill $PF_PID
```

**PASS Criteria (SC-004)**:
- `get-player-list` returns the expected list of connected players
- `send-chat-message` delivers the message in-game within 5s
- `set-next-mission` advances the mission rotation
- `kick-player` and `banlist-add` / `banlist-remove` work similarly
- All commands return status code 2000 on success
- All commands execute within 5 seconds of issuing the request

### A.6: Configuration Validation (SC-007: Invalid Config Detection)

**Objective**: Verify that invalid server configurations are rejected quickly.

#### Test Cases:

```bash
# Test 1: Server name too long (exceeds 64 characters)
cat <<'EOF' | kubectl apply -f - 2>&1
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: nuclear-test-invalid-name
  namespace: default
spec:
  template:
    name: nuclear-option
  config:
    serverName: "This is an extremely long server name that exceeds the 64-character limit specified in the schema and should be rejected"
    maxPlayers: 16
EOF
# Expected: Validation error appears within 10s (before pod creation)

# Test 2: Invalid max-players value (out of range)
cat <<'EOF' | kubectl apply -f - 2>&1
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: nuclear-test-invalid-players
  namespace: default
spec:
  template:
    name: nuclear-option
  config:
    serverName: "Test"
    maxPlayers: 128  # Max is 64
EOF
# Expected: Validation error within 10s

# Test 3: Invalid mission name
cat <<'EOF' | kubectl apply -f - 2>&1
apiVersion: gameplane.local/v1alpha1
kind: GameServer
metadata:
  name: nuclear-test-invalid-mission
  namespace: default
spec:
  template:
    name: nuclear-option
  config:
    serverName: "Test"
    maxPlayers: 16
    missionRotation:
      - "NonExistent_Mission"  # Not in available missions
EOF
# Expected: Validation error within 10s (or pod enters error state within 10s with clear error message)

# Test 4: Malformed JSON in configMap (if applicable)
# Check GameServer status for validation errors
kubectl get gameserver nuclear-test-invalid-name -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'
# Expected: Clear message like "Server name must be 1–64 characters"
```

**PASS Criteria (SC-007)**:
- Invalid config is rejected by CRD validation or caught by startup probe within 10s
- Error message names the specific field and the constraint (e.g., "maxPlayers must be between 1 and 64")
- Server does not enter a crash-loop or hung state
- Operator can read the error and fix it

### A.7: Backup & Restore Round-Trip (FR-013)

**Objective**: Verify that the server's configuration, mission state, and ban list survive a backup/restore cycle.

#### Setup:

```bash
# Create a backup of the running server
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: Backup
metadata:
  name: nuclear-test-backup-1
  namespace: default
spec:
  gameServerRef:
    name: nuclear-test-1
  destination:
    type: "S3"  # or local file, depending on your setup
    # Configure S3 credentials or local path
EOF

# Wait for the backup to complete
kubectl get backup nuclear-test-backup-1 -w
# Expected: Status becomes "Completed" within a few minutes
```

#### Validation:

```bash
# Verify backup contents (should include config, mission state, ban list, logs)
# Restore to a new server
cat <<'EOF' | kubectl apply -f -
apiVersion: gameplane.local/v1alpha1
kind: Restore
metadata:
  name: nuclear-test-restore-1
  namespace: default
spec:
  backupRef:
    name: nuclear-test-backup-1
  gameServerRef:
    name: nuclear-test-1-restored
EOF

# Wait for the restore to complete
kubectl get restore nuclear-test-restore-1 -w
# Expected: Status becomes "Completed"

# Verify the restored server has the same configuration
ORIGINAL_CONFIG=$(kubectl get gs nuclear-test-1 -o jsonpath='{.spec.config}')
RESTORED_CONFIG=$(kubectl get gs nuclear-test-1-restored -o jsonpath='{.spec.config}')

if [ "$ORIGINAL_CONFIG" = "$RESTORED_CONFIG" ]; then
  echo "✓ PASS: Configuration restored correctly"
else
  echo "✗ FAIL: Configuration does not match"
fi
```

**PASS Criteria (FR-013)**:
- Backup completes within reasonable time
- Restore creates a new server with identical configuration
- Mission state and ban list are preserved
- Backup uses Gameplane's existing backup framework (no Nuclear-Option-specific tooling required)

### A.8: Verify Join-Coverage Status in docs/game-coverage.md (SC-005)

**Objective**: Confirm the Nuclear Option row in docs/game-coverage.md is well-formed and the join-coverage verifier passes.

#### Update game-coverage.md:

Based on the join protocol validation (A.4), add or update the Nuclear Option row:

```markdown
| `nuclear-option` | Nuclear Option | covered-in-ci | JOINED | TestGameServer_NuclearOptionBot_Joined | bot-fast | 2026-08-21 | — | — |
```

Or, if the test is deferred or blocked:

```markdown
| `nuclear-option` | Nuclear Option | covered-deferred | JOINED | TestGameServer_NuclearOptionBot_Joined | bot-heavy | 2026-08-21 | — | — |
```

Or:

```markdown
| `nuclear-option` | Nuclear Option | blocked-doc | QUERY | TestGameServer_NuclearOptionBot_Query | bot-heavy | — | Remote-command protocol wire format not documented; packet capture required | documentation |
```

#### Validation:

```bash
# Run the join-coverage verifier
test/e2e/joincoverage.sh verify
# Expected: All 16 checks pass with message:
#   "buckets OK: <N> tests, all in exactly one bucket"

# If any check fails, fix the issue:
# - Check 6: Covered modules must have a test function (ensure test name is correct)
# - Check 7: Test names must be valid (ensure function exists in test/e2e/*_test.go)
# - Check 8: Covered modules must have a bucket (ensure bucket is listed)
# - Check 9: Bucket name must be valid (ensure it's one of the known buckets)
# - Check 10: Covered-in-ci cannot use bot-heavy (move to bot-fast if in CI)
# - Check 11: Covered modules must have a Last Verified date
# - Check 13: Blocked-doc must name a concrete artifact (e.g., "wire format", "packet capture")
```

**PASS Criteria (SC-005)**:
- Nuclear Option row exists in docs/game-coverage.md
- Status is one of: `covered-in-ci`, `covered-deferred`, `blocked-doc`, `out-of-scope-by-design`
- Depth matches status: `covered-*` requires `JOINED`
- Test name is valid (exists in test/e2e)
- Bucket name is valid and consistent with status (covered-in-ci → bot-fast, covered-deferred → bot-heavy)
- `test/e2e/joincoverage.sh verify` passes all 16 checks
- If blocked-doc: blocker field names a concrete artifact (protocol spec, packet capture, etc.)
- If out-of-scope-by-design: blocker class is "architectural"

---

## What PASS Looks Like: Success Criteria Checklist

This checklist maps each SC-001 through SC-008 to the concrete observable that proves it.

| Success Criterion | Observable Proof | Validation Step |
|---|---|---|
| **SC-001**: Deploy & join within 5 min | Real player joins; player appears in player list (Remote Console `get-player-list` command shows the player) | A.4: Player join test; A.5: Remote Console verification |
| **SC-002**: 100% of pool assignments honored | GameServer requests pool-us-east → Service receives address in range 192.0.2.100-110; GameServer requests pool-us-west → Service receives address in range 192.0.2.200-210 (no false failures or fallbacks) | B.3: Multiple pool requests all succeed |
| **SC-003**: Misconfiguration errors within 30s | Status message appears on GameServer within 30s; message names specific problem (pool not found, exhausted, address in use, mode incompatible) | B.4a–B.4d: Four error scenarios each surface distinct message within 30s |
| **SC-004**: Moderation commands execute within 5s | `get-player-list` returns player array; `send-chat-message` appears in-game; `set-next-mission` changes mission; `kick-player` disconnects; commands all respond within 5s (status code 2000) | A.5: Protocol tests of each command type |
| **SC-005**: Join-coverage status recorded & verified | Nuclear Option row in docs/game-coverage.md; status is one of covered-in-ci/covered-deferred/blocked-doc/out-of-scope-by-design; `test/e2e/joincoverage.sh verify` passes all 16 checks | A.8: docs/game-coverage.md row verification and joincoverage.sh gate |
| **SC-006**: Backward compatibility (no pool) | GameServer without poolPreference receives address from default pool; behavior identical to pre-feature; no regression | B.2: Server without pool preference behaves unchanged |
| **SC-007**: Invalid config rejected within 10s | Config validation rejects malformed JSON, bad field types, out-of-range values within 10s; error message names field and constraint; no crash-loop | A.6: Invalid config test cases |
| **SC-008**: Address visible in dashboard within 30s | Dashboard Networking section displays assigned address within 30s of Service receiving it; pool name visible if set | B.5: Dashboard address display; A.3: Server boot & readiness |

---

## Troubleshooting

### Track B (IP Pool Override)

**Symptom**: Pool assignment silently does nothing (server gets an address but not from the requested pool).

**Likely Causes**:
1. **MetalLB vs. Cilium annotation/label asymmetry**: MetalLB uses `metallb.universe.tf/address-pool` annotation; Cilium uses `cilium.io/address-pool` label or CiliumLoadBalancerIPPool reference. If the operator is writing the wrong one for your address manager, pools are ignored.
   - **Check**: Inspect the Service YAML:
     ```bash
     kubectl get svc <name> -o yaml | grep -E "(metallb|cilium|address-pool)"
     ```
   - **Fix**: Verify the operator code writes the correct annotation/label for your cluster's address manager.

2. **Pool names do not match**: Pool name in GameServer CRD does not match actual pool name in MetalLB ConfigMap or Cilium CRD.
   - **Check**: List actual pools:
     ```bash
     # MetalLB
     kubectl get configmap -n metallb-system -o yaml | grep "name:"
     # Cilium
     kubectl get ciliumloadbalancerippool -o yaml | grep "name:"
     ```
   - **Fix**: Use the exact pool name as it appears in the address manager.

**Symptom**: Pool assignment works for the first server but hangs for the second.

**Likely Causes**:
1. **Pool exhausted** (all addresses taken): See B.4b for reproducing and verifying the error message.
2. **Service stuck in "Pending"**: Address manager is not responding or has crashed.
   - **Check**: Inspect MetalLB speaker/controller logs:
     ```bash
     kubectl logs -n metallb-system deployment/metallb-controller
     ```
   - **Fix**: Restart the address manager or check for configuration errors.

### Track A (Nuclear Option Module)

**Symptom**: Server pod stays in "Pending" or "CrashLoopBackOff".

**Likely Causes**:
1. **Image not found**: Container image does not exist or is not reachable.
   - **Check**:
     ```bash
     kubectl describe pod -l gameplane.io/gameserver=nuclear-test-1
     ```
   - **Fix**: Build and push the nuclear-option image to the registry.

2. **Insufficient resources**: Cluster does not have 8+ GB RAM or 30 GB storage available.
   - **Check**:
     ```bash
     kubectl top nodes
     kubectl get pvc
     ```
   - **Fix**: Free resources or use a larger cluster (kubelab recommended).

**Symptom**: Server pod runs but remote-command port (TCP 7779) is not reachable.

**Likely Causes**:
1. **Port not exposed**: Server is not starting with the `-ServerRemoteCommands 7779` flag.
   - **Check**: Server logs for startup flags.
   - **Fix**: Verify the template.yaml includes the correct startup command.

2. **Firewall blocking**: NetworkPolicy or pod security policy blocks TCP 7779.
   - **Check**:
     ```bash
     kubectl get networkpolicy
     ```
   - **Fix**: Ensure the agent sidecar or pod can reach the game container's port.

**Symptom**: Remote-command response is asymmetric to request (request format correct, response unreadable).

**Likely Causes**:
1. **Protocol implementation bug**: The wire format does not match the actual server protocol (most common implementation error per spec).
   - **Check**: Capture a real RCON interaction with the standalone server outside the pod.
   - **Fix**: Verify response format (status code as 4-byte little-endian, body length as 4-byte little-endian, JSON body).

2. **Byte order mismatch**: Status code or length are network-endian instead of little-endian.
   - **Fix**: Verify the exact byte order with a real server.

**Symptom**: Pod starts but `Waiting for Players` log line never appears.

**Likely Causes**:
1. **Different readiness marker**: Server uses a different log line (Claim 4 unverified).
   - **Check**: Read full server logs:
     ```bash
     kubectl logs -l gameplane.io/gameserver=nuclear-test-1 | head -100
     ```
   - **Fix**: Find the actual readiness marker and update the startup probe.

2. **Server hangs or crashes silently**: No logs appear at all.
   - **Check**: `kubectl describe pod` for exit codes or restart loops.
   - **Fix**: Verify the server binary is executable and config file is created.

**Symptom**: Steam download fails or the binary is unavailable.

**Likely Causes**:
1. **Claim 1 not verified**: Dedicated server binary is not downloadable without base-game ownership, or no Linux build exists.
   - **Fix**: This is a blocking blocker (spec BLOCKING RISK). Document the licensing/platform constraint and defer the module.

**Symptom**: CRD does not update on `helm upgrade`.

**Likely Causes**:
1. **Helm CRD update limitation**: Helm's native `crds/` directory only updates on fresh install, not on upgrade.
   - **Check**: Gameplane installs a pre-upgrade hook that applies CRDs server-side.
   - **Fix**: Ensure the hook ran:
     ```bash
     kubectl get job -n gameplane-system | grep crd-apply
     ```
   - **Manual fallback**:
     ```bash
     kubectl apply --server-side -f charts/gameplane/crd-manifests/
     ```

---

## Local Development Workflow (make dev-up vs. Remote kubelab)

### Using make dev-up (kind cluster, lightweight)

**Good for**: Quick validation of IP pool logic, API changes, small tests.

```bash
make dev-up          # Creates kind cluster with MetalLB + local registry
make dev-install     # Installs Gameplane chart
make web-dev         # Starts Vite dashboard (http://localhost:5173)

# Validate Track B (IP pool override)
# Run validation scenarios B.2–B.5 directly

# Track A (Nuclear Option module) is NOT recommended on kind due to resource constraints
# Use kubelab for A-track testing
```

### Using Remote kubelab (k3s, higher capacity)

**Good for**: Full end-to-end validation including heavy games, real protocol tests.

```bash
# Set cluster target to kubelab
export CLUSTER=remote
export REMOTE_KUBECONFIG=~/kubelab.yaml

# Redeploy to kubelab
make dev-install

# Port-forward the API
kubectl --kubeconfig ~/kubelab.yaml port-forward -n gameplane-system svc/gameplane-api 8000:8000

# Start web dev
cd web && npm run dev

# Run Track A validation scenarios (real game deployment + join)
```

### Running E2E Suite

**CI**: All tests run on GitHub Actions (`go test -run ...` via buckets.sh).

**Local** (if permitted by project policy):

```bash
# Full e2e suite (requires a kind cluster)
# **NOTE**: Project policy states NOT to run tests locally; submit to CI instead.

# For learning only, the command would be:
make test-e2e         # Runs all buckets (operator, api-auth, ..., bot-fast)
make test-e2e-keep    # Re-run against existing cluster (faster iterations)

# Heavy games (bot-heavy bucket) never run in default CI:
make test-e2e-bucket BUCKET=bot-heavy
```

---

## Summary: Validation by Phase

1. **Phase 1 Quickstart** (this document):
   - Verify unverified claims (Claim 1–5)
   - Validate Track B (IP pool override) independently
   - Validate Track A (Nuclear Option module) if preconditions are met
   - Record success/blockers for each SC-001..SC-008

2. **Phase 2 Implementation**:
   - Build the module image with correct resource defaults, config schema, startup flags
   - Implement pool-preference logic in the operator (write annotation/label based on cluster's address manager)
   - Implement remote-command proxy in the agent (parse protocol, route to game container port)
   - Implement configuration validation in the operator (schema enforcement)
   - Write e2e tests: bot test for join protocol, operator/api tests for pool assignment and error handling

3. **Phase 3 Integration**:
   - Run full e2e suite on CI
   - Confirm all SC pass on GitHub Actions
   - Update docs/game-coverage.md with join-coverage status
   - Run `test/e2e/joincoverage.sh verify` as a gate before shipping

---

## References

- **Spec**: `specs/002-nuclear-option-ip-pool/spec.md` (Success Criteria, User Stories, Requirements)
- **Data Model**: `specs/done_001-gameprotocol-e2e-coverage/data-model.md` (JoinDepth, CoverageRecord, test vocabulary)
- **Verifier**: `test/e2e/joincoverage.sh` (join-coverage gate)
- **Buckets**: `test/e2e/buckets.sh` (e2e test organization)
- **Coverage Table**: `docs/game-coverage.md` (module join-protocol status)
- **Makefile**: `Makefile` (dev-up, dev-install, test-e2e targets)
- **CLAUDE.md**: `CLAUDE.md` (project rules: e2e CI only, no local tests, Pool Preference unverified, etc.)

---

**Document Revision**: Phase 1 Quickstart  
**Last Updated**: 2026-08-21  
**Status**: Ready for validation
