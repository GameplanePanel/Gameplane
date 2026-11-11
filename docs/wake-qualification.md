# Qualifying simultaneous cold joins

Run the burst on dedicated staging sleepers with the same images, worlds,
storage, resource requests, worker placement and exposure used by the intended
tier. Obtain approval for the operator restart on that cluster. The driver sends
real player traffic and leaves each awakened server running. It observes
GameServers and Pods. Its only administration command is the operator rollout
explicitly selected by `--restart-operator`.

## Join objectives and evidence

These are example qualification objectives for a retry-tolerant tier. Record
the intended per-game objective in the plan before each run. The numbers are
trial inputs. No fleet capacity or game startup percentile has been established.

| Game | Completion evidence | Example objective | Observation deadline |
| --- | --- | --- | --- |
| Offline-mode Minecraft Java | Server sends Login Success (0x02) to the named player | Every client joined within 90 s | 180 s |
| Unpassworded Terraria | Nonempty WorldData (7) after player slot assignment | Every client joined within 60 s | 120 s |
| Entitled UDP game or authenticated client | Version-matched real client records a server-originated in-game artifact | Set from the tier's player retry window | Set independently to retain slow failures |

The standard client reuses the [game protocol fixtures](../test/e2e/internal/specs.md).
Its one-attempt interface also works for a restored-world canary:

```sh
cd test/e2e
go build -o join-once ./cmd/join-once
./join-once -game minecraft-java -addr mc.staging.svc:25565 -user gpcanary -deadline 25s
```

Success requires exit zero and exactly one `VERDICT<TAB>PASS<TAB>JOINED<TAB>evidence`
line. Query replies, encryption/password challenges, an accepted socket and
the sentinel's wake acknowledgement all fall short of this criterion. Each
licensed or authenticated client adapter must implement the same flags, launch
one join attempt, identify the server's post-join artifact in its verdict,
and close the client by its deadline. Keep provider tokens out of arguments,
the plan and verdicts. A missing adapter leaves that path unqualified. The
[game coverage table](game-coverage.md) records existing protocol limitations.

## Run a burst

1. Prepare at least two unsuspended, idle-asleep servers with wake-on-connect
   armed, no pending wake or lifecycle owner, zero game replicas, and one ready
   sentinel each. Use distinct servers and published endpoints. Exclude other
   player traffic and scheduled prewarming during the trial. Run from one
   client host or Pod that can reach each published address and the Kubernetes
   API. Use the game Service endpoint for a Service trial and the advertised
   node and port for a Hostport handoff. A port-forward to a game Pod bypasses
   the sentinel. Test UDP with a real client that emits enough real game
   datagrams to trigger its configured wake threshold.
2. Save a JSON array like the one below. Replace addresses and names with the
   actual sleepers. Add rows with `"path": "Hostport"` for the handoff drill.
   For UDP add `"protocol": "udp"` and `"adapter": "/opt/game-clients/join-once"`
   for an entitled, version-matched client. Exercise multiple sleepers of each
   expensive game on the approved worker pool.

```json
[
  {"namespace":"staging","name":"mc-a","game":"minecraft-java","addr":"mc-a.staging.svc:25565","path":"Service","protocol":"tcp","clients":4,"objectiveSeconds":90,"deadlineSeconds":180,"attemptSeconds":25,"retrySeconds":3},
  {"namespace":"staging","name":"terra-b","game":"terraria","addr":"terra-b.staging.svc:7777","path":"Service","protocol":"tcp","clients":4,"objectiveSeconds":60,"deadlineSeconds":120,"attemptSeconds":25,"retrySeconds":3}
]
```

3. Run from `test/e2e`. Python 3 and `kubectl` are required. The explicit
   cluster context needs reads of GameServers, StatefulSets, Deployments,
   game Pod lists/watches, Nodes and node resource metrics. The restart drill
   additionally needs patch/watch on the named operator Deployment.

```sh
python3 wake_burst.py --context staging --plan sleepers.json --client ./join-once \
  --restart-operator gameplane-system/gameplane-operator > burst.json
```

The driver releases all clients together. Every retry uses the same published
address. After a new wake token appears while clients are still trying, it
restarts the operator once and waits for rollout. The report records observed
wake intent and phase separately from the first successful game join. It
checks the original GameServer UID, a matching new request/acknowledgement,
cleared sleep and lifecycle ownership, and a single game Pod UID without game
container restarts. The Pod watch replays from the pre-burst list's resource
version. Expired watch history invalidates the lifecycle proof.

4. Archive `burst.json`, game builds, world/save sizes, node topology,
   image-cache state and storage class. The report gives successes and retries
   alongside nearest-rank p95/p99 among **successful** clients per target and
   game/path/protocol cohort. Time starts at burst release. Compare failed and
   over-objective clients with the full denominator. Repeat on fresh sleepers
   and retain individual samples; a four-client p99 is just the slowest join.
5. Match the game node names and UTC run interval to CPU/memory usage and
   node pressure in the report. Attach per-node disk throughput, I/O latency
   or pressure from the storage monitoring system for the same window.
   Metrics-server sampling periods can exceed a short boot. Missing readings
   are explicit. `joinAndRecoveryPassed` covers client objectives and the
   lifecycle checks; pressure, entitlement, and tier qualification require
   the accompanying evidence.

Run separate direct and relay trials with identical clients and worlds. Use
the relay packet-load baseline from #879 when available. Keep explicit
`spec.suspend` and lifecycle-owned backup/restore servers outside the burst.
For a tier whose first connection cannot tolerate the observed cold join,
configure the existing `spec.idle.wakeWindows` ahead of its playing hours
and verify the warm result with the same single-attempt client.

This harness does not change scheduling or start admission. A pool admission
policy needs the actual eligible worker set from #875/#876, resource requests,
storage startup pressure, and durable reservation/ownership recovery across
operator restarts. The per-server sentinel coalescing and retry policy still
applies. Hostport, hosted heavy-game UDP, operator restart timing and sustainable
fleet concurrency need measurements on that topology before closing #878.
