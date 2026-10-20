# tunnel — Specification

**Status:** pre-v1 (v0.3.0)  
**Module / command:** `github.com/GameplanePanel/gameplane/tunnel`  
**Dependencies:** stdlib only

## Purpose

Relay client supervisor that configures and supervises a third-party tunnel process (frp, Tailscale, or playit). The supervisor runs as a single-replica Deployment per GameServer, reads provider-specific configuration and credentials via environment variables and a mounted Secret, renders provider-specific config files, spawns the relay binary, and supervises it with exponential backoff until context cancellation or an unrecoverable error. The tunnel pod never sleeps — it holds the public relay address across the full GameServer lifecycle so connection attempts can trigger wake-on-connect.

## Responsibilities

1. Parse environment variables to determine the tunnel provider (frp, tailscale, or playit), backing service address, and provider-specific config.
2. Read provider-specific credentials from the mounted Secret at `/etc/gameplane/tunnel-auth` (read-only).
3. Render provider-specific config files at fixed paths (`/tmp/gameplane-tunnel-frpc.toml` for frp, `/tmp/gameplane-tunnel-tailscaled.json` for tailscale, `/tmp/gameplane-tunnel-playit-auth` for playit).
4. Build provider-specific relay command-line arguments, referencing only fixed config file paths (never passing secrets via argv).
5. Spawn the relay binary (frpc, tailscaled, or playitd) and supervise it until context cancellation or unrecoverable failure.
6. On transient failure (exit code other than 126/127, not a permission error), apply exponential backoff (2^n seconds, capped at 300 seconds) and restart.
7. On unrecoverable failure (exit code 126/127 or "permission denied" error), exit immediately.
8. Forward SIGTERM for graceful shutdown, waiting up to 10 seconds before SIGKILL.
9. (Playit only) Poll playitd's IPC control socket for the assigned relay addresses and patch them into the GameServer's `status.tunnelEndpoints`, which the operator validates and merges into `status.endpoints` (see "Playit Address Reporting").
10. (Tailscale only, when `TAILSCALE_TAGS` holds valid tags) Register the device once with `tailscale up --advertise-tags` over tailscaled's socket, requesting the tags at registration. If the tailnet refuses them, log the error; only if the backend is not `Running` afterwards, attempt one untagged registration, which can also fail and is then logged (see "Tailscale Tag Application").

## Non-goals / boundaries

- Does **not** run the game server itself — that is the operator and game container's job.
- Does **not** create or manage the relay infrastructure — frp/Tailscale/playit accounts and credentials are pre-configured.
- Does **not** authenticate or authorize clients connecting to the relay — relay access control is the provider's responsibility.
- Does **not** validate or filter traffic — the relay process handles all inbound connections and port forwarding.
- Does **not** modify the game pod or game container — the tunnel runs as a separate Deployment.
- Does **not** report metrics or logs to external systems — logs flow to the pod's stdout/stderr for the cluster to capture.
- Does **not** support runtime configuration changes — config is read once at startup and cannot be updated without redeploying.
- Does **not** implement Tailscale, frp, or playit protocols — delegates to their official binaries.

## Directory & package layout

```
tunnel/
├── main.go              # Entry point; config loading; credential reading; config rendering; relay spawning and supervision; tailscale tagged registration
├── playit_reporter.go   # playit only: polls playitd's IPC socket for assigned addresses; patches status.tunnelEndpoints
├── playit_reporter_test.go # Fake playitd IPC server + httptest API server tests for the reporter
├── main_test.go         # Config parsing; credential reading; config rendering per provider; command building; backoff; error classification; supervision lifecycle
├── Dockerfile.frp       # Image build for frp provider (sets Version via -ldflags)
├── Dockerfile.playit    # Image build for playit provider (one image per provider)
├── Dockerfile.tailscale # Image build for tailscale provider
├── go.mod              # Dependencies: stdlib only (no external deps, so no go.sum)
└── .testcoverage.yml    # 70% coverage gate
```

Single executable module; no subdirectories or packages.

## External Interface / Contracts

### Environment Variables

| Variable | Type | Required | Description |
|---|---|---|---|
| `GAMESERVER_NAME` | string | yes | Name of the GameServer resource (used in logs and for playit tunnel naming) |
| `GAMESERVER_NAMESPACE` | string | yes | Kubernetes namespace of the GameServer |
| `TUNNEL_TYPE` | enum (frp\|tailscale\|playit) | yes | Relay provider to use |
| `BACKING_SERVICE_DNS` | string | yes | DNS name of the game pod Service, format `<gs-name>.<namespace>.svc` |
| `FRP_SERVER_ADDR` | string | if frp | Hostname or IP of the frp server |
| `FRP_SERVER_PORT` | int | no (default 7000) | frp server port; validated 1–65535 |
| `BACKING_SERVICE_PORT` | string | if frp | Port mappings for frp, format `name:localPort:remotePort:protocol,...` (e.g., `game:34197:30000:udp`); localPort and protocol come from the GameTemplate port, remotePort from `spec.networking.tunnel.frp.remotePorts` (F-052) |
| `TAILSCALE_HOSTNAME` | string | if tailscale | MagicDNS hostname in the tailnet; fatal if unset for tailscale provider |
| `TAILSCALE_TAGS` | string | no | Comma-separated ACL tags requested for the Tailscale device when it registers. Each entry is trimmed, empty entries are dropped, and a bare name gets the `tag:` prefix. Each tag must then be `tag:` followed by a letter and only letters, digits or `-` (tailscale's `tailcfg.CheckTag` rule); otherwise the whole value is logged and ignored, and the device registers untagged. Never written into tailscaled's declarative config: its alpha0 config has no field for ACL tags (`ipn.ConfigVAlpha`), and its loader rejects unknown JSON fields, so a `tags` key would make tailscaled refuse to start. Tags are requested through a one-off `tailscale up --advertise-tags` instead (see "Tailscale Tag Application"). The tailnet's ACL must grant `tagOwners` for the tags to the auth key's owner. If it doesn't, the error is logged and the tunnel runs untagged rather than crash-looping. |
| `BACKING_SERVICE_PORTS` | string | if tailscale or playit | Container ports for non-frp providers, format `name:port,name:port` |
| `PLAYIT_TUNNEL_NAME` | string | if playit | Label/name for the playit tunnel; fatal if unset for playit provider |

**Validation**: At startup, `loadConfig` enforces mutual requirement rules: frp mandates `FRP_SERVER_ADDR` and `BACKING_SERVICE_PORT`; tailscale mandates `TAILSCALE_HOSTNAME` and `BACKING_SERVICE_PORTS`; playit mandates `PLAYIT_TUNNEL_NAME` and `BACKING_SERVICE_PORTS`. Invalid `FRP_SERVER_PORT` values (non-numeric, out of range 1–65535) are fatal. Unknown `TUNNEL_TYPE` values are fatal.

### Credentials & Secrets

The operator mounts a read-only Secret volume at `/etc/gameplane/tunnel-auth` containing provider-specific credential keys:

| Provider | Key Name | Example Usage | Notes |
|---|---|---|---|
| frp | `token` | Auth token for the frpc client | Read by `readCredentials` and embedded in rendered `/tmp/gameplane-tunnel-frpc.toml` |
| tailscale | `authKey` | Auth key (one-time or reusable) for device registration | Read and embedded in rendered `/tmp/gameplane-tunnel-tailscaled.json` |
| playit | `secretKey` | API secret key for playitd authentication | Read and written to `/tmp/gameplane-tunnel-playit-auth` for playitd's `--secret-path` flag |

Whitespace (leading/trailing newlines, spaces) is trimmed from credential values before use. Missing credentials are fatal errors and prevent relay startup.

### Rendered Config Paths

| Provider | Path | Format | Ownership |
|---|---|---|---|
| frp | `/tmp/gameplane-tunnel-frpc.toml` | TOML | Rendered by `renderFrpConfig`; read by frpc process |
| tailscale | `/tmp/gameplane-tunnel-tailscaled.json` | JSON | Rendered by `renderTailscaleConfig`; read by tailscaled via `--config` flag. Holds `version` and `hostname`, plus `authKey` without tags, or `"locked": false` with tags |
| tailscale (tags only) | `/tmp/gameplane-tunnel-tailscale-authkey` | Raw text (auth key) | Written by `renderTailscaleConfig` when tags are requested; read by `tailscale up --auth-key=file:<path>` |
| playit | `/tmp/gameplane-tunnel-playit-auth` | Raw text (secret key) | Rendered by `renderPlayitConfig`; read by playitd via `--secret-path` flag |

All files are written with mode `0o600` (read/write by owner only) via `os.WriteFile` — see `renderFrpConfig`, `renderTailscaleConfig`, `renderPlayitConfig` in `main.go`. The rendered config file (and, for tailscale, the auth key file) is removed by a deferred `os.Remove` when the supervisor's `run` returns (context cancellation or an unrecoverable error); it is not removed on SIGKILL or on each relay restart inside the backoff loop. tailscaled's `--state` file (`/tmp/tailscale.state`) is never removed by the process. All of these files live under `/tmp` in the pod's ephemeral, per-pod filesystem, so any that remain are discarded when the pod terminates and its filesystem is reclaimed.

### Relay Binaries and Command-Line Arguments

| Provider | Binary | Arguments |
|---|---|---|
| frp | `/usr/local/bin/frpc` | `-c /tmp/gameplane-tunnel-frpc.toml` (config file flag) |
| tailscale | `/usr/local/bin/tailscaled` | `--tun=userspace-networking` (hardened pod context), `--state=/tmp/tailscale.state`, `--config=/tmp/gameplane-tunnel-tailscaled.json`, `--socket=/tmp/gameplane-tunnel-tailscaled.sock` |
| playit | `/usr/local/bin/playitd` | `--secret-path /tmp/gameplane-tunnel-playit-auth`, `--socket-path /tmp/gameplane-tunnel-playitd.sock`, `--platform-docker` |

All binary paths are fixed constants, not configurable. Secrets are never passed via command-line arguments (gosec G204 compliance).

### Playit Address Reporting (F-174)

playit.gg assigns a tunnel's public address server-side, so the supervisor learns it at runtime (`playit_reporter.go`) and reports it back:

1. `playitd` is started with `--socket-path /tmp/gameplane-tunnel-playitd.sock`. Its Linux default, `/run/playit/playitd.sock`, can't be created by the non-root distroless image. `playitd` chmods the socket to `0660` under its own uid (65532), which the supervisor shares.
2. A goroutine started by `run` polls that socket with playitd's IPC protocol (playit-agent v1.0.10, `packages/playit-ipc`). The protocol is newline-delimited JSON over a Unix stream socket. The server sends `{"message_kind":"hello","data":{"protocol":{"ipc_version":2,...}}}`. The client then sends `{"ipc_version":2,"request_id":1,"request":{"type":"get_state"}}`. The reply is `{"message_kind":"response","data":{...,"response":{"type":"state","data":{"state":"running","data":{"tunnels":[{"display_address","destination","is_disabled",...}]}}}}}`. Any IPC version other than 2 is treated as an error.
3. `TunnelState` carries no tunnel name, so each enabled tunnel is mapped to a `BACKING_SERVICE_PORTS` name by matching the port in its `destination` (the local origin, `ip:port`). With exactly one advertised port and one enabled tunnel, the two are paired even if the ports differ. A `display_address` without a port (an SRV-style playit hostname) is reported with the backing port number. HTTPS tunnels and unmatched tunnels are skipped. The list is capped at 32 entries (the CRD's `MaxItems`).
4. When the mapped set differs from the last one reported, it's merge-patched into `status.tunnelEndpoints` on `PATCH https://kubernetes.default.svc/apis/gameplane.local/v1alpha1/namespaces/<ns>/gameservers/<name>/status`. The request uses the pod's projected ServiceAccount token and CA from `/var/run/secrets/kubernetes.io/serviceaccount`. The first report after startup is always sent, and an empty set is sent as `null` so stale addresses from a previous pod are cleared. The operator validates those entries (`validatePlayitEndpoints`) and merges them into `status.endpoints`, the same place frp/tailscale endpoints land.
5. Timing: while the socket is down, playitd isn't `running` yet, or a patch fails, the reporter backs off from 1s, doubling up to 30s. Once a set has been reported, it re-polls every 30s so address changes are picked up. It stops when `run`'s context ends. If the ServiceAccount CA can't be read (not in a cluster) or `BACKING_SERVICE_PORTS` doesn't parse, the reporter logs and is skipped. The relay still runs.

### Tailscale Tag Application

Tailscale ACL tags can only be requested when a device registers. tailscaled's declarative `--config` file has no field for them (see the `TAILSCALE_TAGS` row above). `tailscale set` has no `--advertise-tags` flag, and changing the tags of a registered node needs a fresh login. So when `TAILSCALE_TAGS` holds valid tags, the supervisor lets tailscaled start unauthenticated and registers it itself. Flags below are confirmed against tailscale v1.102.4, the version `Dockerfile.tailscale` ships.

1. `renderTailscaleConfig` writes the config with `version`, `hostname` and `"locked": false`, and no `authKey`. The auth key goes to `/tmp/gameplane-tunnel-tailscale-authkey` (mode 0600). `locked` must be false: `ipn.ConfigVAlpha.Locked` defaults to true, and a locked config makes tailscaled reject every CLI prefs change.
2. `tailscaled` is started as usual, with `--socket=/tmp/gameplane-tunnel-tailscaled.sock`. Its Linux default, `/run/tailscale/tailscaled.sock`, can't be created by the non-root distroless image; this is the same reasoning as `playitSocketPath`. The `tailscale` CLI, bundled in `Dockerfile.tailscale` alongside `tailscaled`, reaches it over the same socket via `--socket=`. With no auth key, tailscaled waits in `NeedsLogin`.
3. `run` starts one goroutine, `startTailscaleRegistrar`, which calls `registerTailscaleOnce` once per supervisor lifetime, in parallel with the relay supervision loop. It is not rerun on a relay restart: the login and granted tags persist in tailscaled's state file (`/tmp/tailscale.state`, `--state` in `buildCommand`).
4. `waitForTailscaled` polls `tailscale --socket=<socket> status --json --peers=false` every `tailscaleReadyPollInterval` (2 seconds), bounded by `tailscaleReadyTimeout` (2 minutes). It waits until the socket answers with a settled `BackendState`: `NeedsLogin`, `NeedsMachineAuth`, `Stopped` or `Running`. Transient states (`NoState`, `Starting`) keep it polling.
5. If the node is already `Running` and `Self.Tags` is exactly the requested set, as with a reused state file, nothing more happens.
6. Otherwise it runs `tailscale --socket=<socket> up --reset --auth-key=file:/tmp/gameplane-tunnel-tailscale-authkey --hostname=<hostname> --advertise-tags=<tags>` once, bounded by `tailscaleUpTimeout` (2 minutes). The pure `tailscaleUpArgs` builds this argv. `--auth-key=file:` makes the CLI read the key from the file, so the key never appears in argv. `--reset` resets every setting not named on the command line to its default. Without it, `tailscale up` on a node with saved prefs refuses to run unless every non-default setting is repeated.
7. If that fails, most commonly because the tailnet's ACL doesn't grant `tagOwners` for the tags to the auth key's owner, the error is logged. If the node is not `Running` afterwards, `tailscale up` runs once more without `--advertise-tags`, so the tunnel registers untagged. Every failure, including the readiness timeout, is logged and none stops the supervisor or the relay process. An ACL problem only the tailnet admin can fix never becomes a crash loop.

The CLI is run by `execTailscaleCLI` with `exec.CommandContext` tied to `run`'s context. The binary path is the literal `/usr/local/bin/tailscale`, and nothing goes through a shell. The argv is appended to `cmd.Args`, so gosec's G204 check sees only literal arguments. The actual guarantee is that argv is built only by `tailscaleStatusArgs` and `tailscaleUpArgs`: every value is a single `--flag=value` element, the tags are validated by `parseTailscaleTags`, and the auth key is never in argv.

Without tags, none of this runs. The auth key goes into the config and tailscaled logs in by itself, as before.

## Key Invariants

1. **Tunnel pod never sleeps.** The tunnel Deployment persists for the full GameServer lifecycle, even when the server idles or suspends. The pod must remain running to hold the relay address so a connection attempt can trigger wake-on-connect.

2. **Credentials are never passed via command-line.** All three providers use file-based credential delivery: frp and tailscale embed credentials in rendered config files, playitd reads a secret file via `--secret-path`. This guards against argv scraping and satisfies gosec's G204 (subprocess argument constant verification).

3. **Exponential backoff is capped and unshed.** On transient failure, retry delays follow 2^n seconds (1s, 2s, 4s, ..., 256s) capped at 300 seconds (5 minutes). No jitter is added because each pod runs a single replica against its own relay connection; jitter spreads out a thundering herd, not a singleton.

4. **Exit codes 126/127 and permission errors are unrecoverable.** Exit code 126 (permission denied) and 127 (command not found) indicate misconfiguration, missing binaries, or permission issues. Any error message containing "permission denied" is treated as unrecoverable. All other exit codes trigger backoff retry.

5. **SIGTERM is forwarded for graceful shutdown.** When the pod is terminated, SIGTERM is sent to the relay child process. The relay has 10 seconds (cmd.WaitDelay) to shut down cleanly before SIGKILL is sent. Relay logs (stdout/stderr) flow through the pod's container logs.

6. **Config file paths are compile-time constants.** To pass gosec's subprocess-argument verification, all config file paths are defined as package-level constants, not variables. This ensures `buildCommand` can pass `exec.CommandContext` with only literal or constant-resolved arguments.

7. **Operator image selection per provider.** The operator injects provider-specific tunnel images via flags (`--tunnel-frp-image`, `--tunnel-tailscale-image`, `--tunnel-playit-image`), each with a `dev` default. Each pod runs exactly one provider's binary, selected by `TUNNEL_TYPE`.

## Known Gaps

- **Tailscale doesn't forward tailnet traffic to the game (F-173):** `BACKING_SERVICE_DNS` and `BACKING_SERVICE_PORTS` are read and validated for presence when `TUNNEL_TYPE=tailscale`, but neither reaches `renderTailscaleConfig` or the `tailscaled` process. The rendered config carries only `version`, `hostname`, and `authKey` or `locked`. ACL tags are requested at registration through `tailscale up`; see "Tailscale Tag Application". The tunnel pod registers as a tailnet device, but nothing bridges an inbound tailnet connection to the backing Service — tailscaled's declarative `--config` file has no serve/forward field, and `tailscale serve` (the real mechanism) only proxies TCP/HTTP(S) to a local `127.0.0.1` target, never a remote host, and has no UDP support. Fixing this is an architecture decision (a local proxy + headless `tailscale serve`, TCP-only; a subnet route via `advertiseRoutes`, which needs tailnet ACL auto-approval and widens reachability into cluster-internal networking; or replacing subprocess `tailscaled` with an embedded `tsnet` listener, which would end this module's stdlib-only dependency policy), tracked pending a decision in `specs/done_018-v0-3-release-readiness/OPEN-DECISIONS.md`.
- **Single-use Tailscale auth keys and tag retries:** when `tags` is set, the supervisor logs in with `tailscale up`. If that tagged login is refused (missing `tagOwners`), or a restarted pod reuses a state file with different tags, the untagged fallback `up` reuses the same auth key; a single-use (ephemeral, non-reusable) key has already been consumed, so the fallback fails too. The failure is logged and the supervisor does not crash-loop, but the device stays logged out until a fresh key is stored. Use a reusable auth key when setting `tags`.

## CRD Integration

The tunnel configuration is anchored in the `GameServer` CRD at `spec.networking.tunnel`:

| Field | Type | Applies To | Notes |
|---|---|---|---|
| `enabled` | bool | All providers | Must be true to create a tunnel Deployment |
| `provider` | enum (frp\|tailscale\|playit) | All providers | Selects tunnel binary and config rendering strategy |
| `credentialsSecretRef` | SecretNameRef (optional) | All providers | Required if enabled=true (enforced by kubebuilder validation) |
| `frp` | FrpTunnelSpec (optional) | frp only | `serverAddr` (required), `serverPort` (optional, default 7000), `remotePorts[]` (required, min 1 item) |
| `tailscale` | TailscaleTunnelSpec (optional) | tailscale only | `hostname` (optional, defaults to GameServer name), `tags[]` (optional, max 8) |
| `playit` | PlayitTunnelSpec (optional) | playit only | `tunnelName` (optional, defaults to GameServer name) |

The operator's `reconcileTunnel` reconciler materializes these fields into the tunnel Deployment's environment variables and Secret mount.

## Operator Integration

**Image Flags** (set at operator startup via `cmd/main.go`):
- `--tunnel-frp-image` (default: `ghcr.io/gameplanepanel/gameplane/tunnel-frp:dev`)
- `--tunnel-tailscale-image` (default: `ghcr.io/gameplanepanel/gameplane/tunnel-tailscale:dev`)
- `--tunnel-playit-image` (default: `ghcr.io/gameplanepanel/gameplane/tunnel-playit:dev`)

**Deployment Structure** (per GameServer):
- Name: `<gameserver-name>-tunnel`
- Replicas: 1 (never zero during sleep); 0 while the operator refuses the credentials Secret (`TunnelReady=False`, reason `TunnelCredentialRefused`), so a tunnel already running with it stops
- Container name: `tunnel`
- Security context: uid 65532 (nonroot), runAsNonRoot=true, allowPrivilegeEscalation=false, ALL capabilities dropped
- Mounts: read-only Secret at `/etc/gameplane/tunnel-auth` (if credentialsSecretRef is set; the operator refuses to mount a Secret with no `ownerReference` to the GameServer — see operator/specs.md's TunnelReady condition)

**RBAC** (playit provider only):
- ServiceAccount: `<gameserver-name>-tunnel`
- Role: `<gameserver-name>-tunnel-tunnel` (immutable naming)
- Permissions: `gameplane.local/gameservers/status patch` (narrow to the specific GameServer by resourceNames)

The playit provider alone needs RBAC because it must patch the GameServer's status subresource to report discovered addresses. frp and tailscale use static/pre-configured addresses and do not need this grant.

**NetworkPolicy** (all providers):
- A per-server egress policy (`<gameserver-name>-tunnel-egress`) admits outbound traffic from the tunnel pod to:
  - DNS (UDP/TCP port 53, all destinations)
  - Relay control plane ports (provider-specific: TCP 7000 for frp, TCP 443 + UDP 41641 for tailscale, playit: unrestricted egress (all ports, all protocols, any destination))
  - Container advertised ports (inbound relay traffic forwarded to the game)

Without this policy, the default-deny egress rule in the games namespace would silently drop relay connections.

## Supervision and Failure Handling

### Backoff Strategy

```
retry 1: 1 second   (2^0)
retry 2: 2 seconds  (2^1)
retry 3: 4 seconds  (2^2)
retry 4: 8 seconds  (2^3)
...
retry N: min(2^(N-1), 300) seconds
```

Capping at 300 seconds (5 minutes) prevents arbitrarily long waits. No jitter or randomization (unnecessary for a singleton pod).

### Exit Code Classification

- **Unrecoverable (fatal):** Exit code 126, 127, or any error message containing "permission denied". Pod logs the error and exits immediately.
- **Transient (retry):** All other exit codes and errors. Pod computes backoff delay, sleeps, and spawns the relay binary again.
- **Graceful shutdown:** Context cancelled (e.g., pod termination). Pod sends SIGTERM to child, waits 10 seconds, then SIGKILL if needed. No retry.

### Graceful Shutdown (SIGTERM → 10s grace → SIGKILL)

When the pod receives SIGTERM (e.g., during cluster shutdown or pod deletion):
1. The signal handler cancels the context.
2. The main loop observes `ctx.Done()` and stops spawning new relay processes.
3. The currently-running relay process receives SIGTERM (via cmd.Cancel, which calls `cmd.Process.Signal(syscall.SIGTERM)`).
4. The relay process has 10 seconds (cmd.WaitDelay) to shut down.
5. If the relay doesn't exit within 10 seconds, exec.Cmd automatically sends SIGKILL.
6. The pod exits cleanly once the child is dead.

## Dependencies

**Internal:** None  
**External:** Go stdlib only (bufio, bytes, context, crypto/tls, crypto/x509, encoding/json, errors, fmt, io, log, net, net/http, net/url, os, os/exec, os/signal, path/filepath, reflect, sort, strconv, strings, sync, syscall, time). The playit status patch uses plain `net/http` against the API server rather than client-go, keeping the module free of third-party dependencies.  
**Go version:** 1.26+

No third-party dependencies. The operator provides provider-specific binaries (frpc, tailscaled, playitd) in each container image.

## Security Considerations

1. **Secret mounting (read-only):** Credentials are mounted from Kubernetes Secrets into the pod at `/etc/gameplane/tunnel-auth` as a read-only VolumeMount. The credential files are accessed only after a path-containment check via `filepath.Rel` to ensure the resolved path does not escape the mount directory, providing defense-in-depth against directory traversal.

2. **Credentials never on argv:** All three providers accept secrets via file-based config (embedded in TOML/JSON for frp/tailscale, separate `--secret-path` file for playitd). This ensures secrets don't appear in `/proc/<pid>/cmdline`.

3. **Hardened pod security context:** UID 65532 (nonroot), runAsNonRoot=true, allowPrivilegeEscalation=false, all capabilities dropped. No privileged escalation or host access.

4. **Narrow RBAC for playit:** Only playit needs a Kubernetes grant (patch gameservers/status), and the Role is scoped to the single GameServer by resourceNames. frp and tailscale run without RBAC grants.

5. **Network egress policy:** The tunnel pod's outbound traffic is controlled by a per-server NetworkPolicy. Without it, the games namespace's default-deny-egress would block relay connections.

6. **Image provider isolation:** Each provider's tunnel binary (frpc, tailscaled, playitd) runs in its own container image. A vulnerability in one provider's binary doesn't affect others; operators can patch images independently.

7. **No credential validation at tunnel pod level:** The tunnel supervisor does not validate frp tokens, Tailscale auth keys, or playit secret keys. Validation happens at the provider's server. Invalid credentials manifest as authentication failures on the provider side.

## Testing & Coverage

**Test structure:**

- **Config loading:** `TestLoadConfigFrp`, `TestLoadConfigTailscale`, `TestLoadConfigPlayit` verify environment variable parsing and provider-specific validation (required fields per provider, default values).
- **Config validation errors:** `TestLoadConfigMissingRequired`, `TestLoadConfigFrpMissingAddress`, `TestLoadConfigInvalidFrpPort`, `TestLoadConfigTailscaleMissingHostname`, `TestLoadConfigPlayitMissingTunnelName` verify that missing or invalid required fields are fatal with clear error messages.
- **Credential reading:** `TestReadCredentialsSuccess`, `TestReadCredentialsMissingFile`, `TestReadCredentialsKeyNames`, `TestReadCredentialsUnknownTunnelType` verify credential path isolation (defense-in-depth `filepath.Rel` check) and per-provider key names.
- **Config rendering:** `TestRenderFrpConfig`, `TestRenderFrpConfigInvalidPortMapping`, `TestRenderFrpConfigMultiplePorts`, `TestRenderTailscaleConfig`, `TestRenderTailscaleConfigNoHostname`, `TestRenderTailscaleConfigWithTagsOmitsTags`, `TestRenderTailscaleConfigNoTags`, `TestRenderTailscaleConfigInvalidTagsRegistersUntagged`, `TestRenderTailscaleConfigLogTagsNotesRegistration`, `TestRenderTailscaleConfigLogTagsSilentWhenUnset` and `TestRenderPlayitConfig` verify the TOML/JSON/text output for each provider. The tailscale tests also check that no `tags` key is ever written into tailscaled's config. With valid tags, `authKey` is left out, `"locked": false` is written, and the key goes to a 0600 file. Without tags or with invalid tags, the config keeps its original shape and no key file is written. The log line names `tailscale up --advertise-tags` and the `tagOwners` requirement.
- **Command building:** `TestBuildCommandFrp`, `TestBuildCommandPlayit`, `TestBuildCommandTailscale`, `TestBuildCommandUnknownType` verify that command-line arguments are correctly assembled and that secrets never appear in argv.
- **Tailscale tagged registration:** `TestParseTailscaleTags` (table-driven) checks that tags are trimmed, empty entries dropped and bare names given the `tag:` prefix, and that invalid tags are rejected: empty names, a leading digit, spaces, other prefixes, flag-like values and shell metacharacters. `TestTailscaleUpArgs` and `TestTailscaleStatusArgs` check the exact argv. The auth key never appears in it. `TestRegisterTailscaleOnceRequestsTags` checks that exactly one tagged `tailscale up` runs from `NeedsLogin`. `TestRegisterTailscaleOnceWaitsThroughStarting` checks that a transient state is polled past and that a node already `Running` with the same tags is skipped. `TestRegisterTailscaleOnceRunningWithOtherTags` checks that a node with different tags is re-registered. `TestRegisterTailscaleOnceFallsBackUntagged` checks that the tagged failure is logged with the `tagOwners` hint and followed by the exact untagged argv. `TestRegisterTailscaleOnceNoFallbackWhenStillRunning` checks that no second login runs on a node that is still up. `TestStartTailscaleRegistrarReturnsAfterFailure` checks that `wait()` returns after failures, so `run`'s shutdown is never blocked. `TestRegisterTailscaleOnceStopsOnCancel` checks that cancellation ends the wait without running `up`.
- **Exponential backoff:** `TestExponentialBackoff`, `TestExponentialBackoffCap` verify delay calculations and the 300-second cap.
- **Error classification:** `TestIsUnrecoverable` verifies that exit codes 126/127 and "permission denied" errors are unrecoverable, and that other errors trigger retry.
- **Supervision lifecycle:** `TestRunContextCancellation`, `TestRunTransientFailureBacksOffThenCancels`, `TestRunRenderConfigFailure`, `TestRunReadCredentialsFailure`, `TestRunPlayitConfigDispatch` verify clean context cancellation, backoff retry, and error path handling.
- **Playit address reporting** (`playit_reporter_test.go`): a fake playitd IPC server on a Unix socket speaks the v1.0.10 wire format. `TestQueryPlayitState*` cover running/starting/error lifecycles, skipped event frames, protocol errors (wrong IPC version, missing hello, service error, mismatched request id, blank frame), a missing socket and a context timeout. `TestParseBackingPorts` and `TestPlayitEndpoints*` cover the port-name mapping, the single-port fallback, portless addresses, dedup and the 32-entry cap. `TestKubeStatusPatcher*` and `TestNewInClusterStatusPatcher` run against an `httptest` TLS server and check the merge-patch path, headers, body and `null` clear. `TestRunPlayitReporter*` check backoff, patching only on change, the initial empty clear, and shutdown on context end.
- **Process execution:** `TestRunCommandSuccess`, `TestRunCommandNonZeroExit`, `TestRunCommandStartError`, `TestRunCommandContextCancellation` verify command spawning, exit code propagation, and graceful SIGTERM shutdown with 10-second grace period.

**Test doubles:**

- **`withCredentialsDir`:** Temporary directory helper that repoints the package-level `credentialsDir` var at `t.TempDir()` for the duration of a test, allowing credential reads to be exercised without touching the real mount point.
- **`stubTailscaleCLI` / `fakeTailscale`:** `stubTailscaleCLI` points the package-level `runTailscaleCLI` var at a fake `func(ctx, args...) ([]byte, error)` for the duration of a test. This is the same seam pattern as `credentialsDir`. `fakeTailscale` scripts the `status --json` replies and `up` results and records each `up` argv, so `waitForTailscaled` and `registerTailscaleOnce` can be tested without running a real `tailscale` binary.
- **Real system binaries in some tests:** `TestRunCommandContextCancellation` uses the real `sleep` command (no relay binary needed) to verify that context cancellation triggers SIGTERM forwarding, allowing the child to exit cleanly within the 10-second grace period.

**Coverage gate:** 70% per `.testcoverage.yml`. Uncovered paths include error cases in relay binary spawning (operator-level validation and pod constraints already catch most configuration issues before the tunnel binary runs) and platform-specific signal handling edge cases.

**Coverage rationale:** The tunnel supervisor's primary responsibility is configuration management and child process supervision. Tests cover all config parsing, credential reading, config rendering, command building, and supervision loop paths. The relay binaries themselves (frpc, tailscaled, playitd) are third-party and tested by their own projects. The tunnel pod's job is to correctly invoke them and restart on failure; actual relay functionality is end-to-end tested at the e2e suite level.

## References

- **Architecture:** `docs/tunnels.md` (tunnel provider setup, wake-on-connect interaction, troubleshooting)
- **CRD & operator integration:** `operator/internal/controller/gameserver_tunnel.go` (deployment creation, env var composition), `operator/internal/controller/tunnel_rbac.go` (RBAC for playit), `operator/api/v1alpha1/gameserver_types.go` (CRD fields)
- **Consumers:** Operator (operator/internal/controller/gameserver_controller.go invokes planTunnel and reconcileTunnel), API (api/internal/handlers may report tunnel endpoints to dashboard)
- **Related specs:** `operator/specs.md` (CRD reconciliation), `api/specs.md` (endpoint reporting)
- **CLAUDE.md:** "K8s-native by default" (rule 9) and "Operator is authoritative" (rule 10)
