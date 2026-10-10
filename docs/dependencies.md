# Dependency report

This is a component-by-component inventory of Gameplane's third-party
dependencies and why each one is there. It covers all 16 Go modules that
share `go.work` (`netguard`, `gameaction`, `gameproto`, `telemetryschema`,
`svcutil`, `operator`, `api`, `agent`, `audit-syslog-bridge` [optional],
`telemetry-receiver` [optional], `sentinel` [optional],
`capture-sidecar` [optional], `tunnel` [optional], `gp-module`,
`mcp-server` [optional], `test/e2e`), the
`web/` npm package, and the build/CI toolchain that isn't a code dependency
but is invoked by `make`/GitHub Actions.

Only **direct** dependencies (the `require` block entries without
`// indirect` in each `go.mod`, and `dependencies`/`devDependencies` in
`web/package.json`) get individual justifications; indirect/transitive
dependencies are mentioned only where the tree itself is notable (e.g. the
`k8s.io/*` and `sigstore`/`cosign` trees pull in a large indirect closure).
Every "why" below was verified against real import/call sites — where a
dependency's purpose couldn't be pinned to a call site, that's stated
plainly rather than guessed.

**How to regenerate/verify this report:** `cat <module>/go.mod` for the
direct `require` block, then `grep -rn "<import path>" <module>/ --include="*.go"`
to find call sites (or `go mod why -m <path>` from inside the module). For
`web/`, check `web/package.json` against `grep -rln "<package>" web/src`.
Toolchain versions come from `Makefile`, `.github/workflows/*.yaml`, and
`.devcontainer/post-create.sh`.

Accurate as of **2026-09-02**, repo state `v0.3.0`; the `gameproto`,
`svcutil`, `sentinel`, `capture-sidecar`, `tunnel`, and `gp-module` sections
were added and verified on **2026-09-26**.

> **Note:** The dependency versions listed in the table below reflect the state at v0.3.0. For the most current versions, refer to [CHANGELOG.md](../CHANGELOG.md).

## At a glance

| Component | Language | Direct deps | Why it exists |
|---|---|---|---|
| `netguard/` | Go | 0 (stdlib only) | Shared SSRF dial-guard used by the operator and agent |
| `gameaction/` | Go | 0 (stdlib only) | Shared console-injection guard + command-template renderer used by the agent and API |
| `gameproto/` | Go | 0 (stdlib only) | Shared Minecraft/Terraria handshake classifier used by sentinel |
| `telemetryschema/` | Go | 0 (stdlib only) | Shared telemetry report contract (types, strict decoder, official module catalog, report signing) used by the API reporter and the telemetry receiver |
| `svcutil/` | Go | 0 (stdlib only) | Shared env-parsing + graceful-shutdown helpers (no active importers yet) |
| `operator/` | Go | 19 | controller-runtime reconciler for the 8 CRDs; module OCI pull/verify, git/OCI fetch, restic-backup scheduling |
| `api/` | Go | 17 | chi REST + WebSocket gateway: auth (local + OIDC), RBAC, K8s client, SQLite/Postgres persistence, notifications |
| `agent/` | Go | 6 | In-pod sidecar: RCON/WebRcon console, chi HTTP surface, heartbeat status patch, disk usage stats |
| `audit-syslog-bridge/` | Go | 0 (stdlib only) | Standalone HTTP-JSON → syslog relay for the audit webhook sink |
| `telemetry-receiver/` | Go | 1 | Standalone collector for the API's opt-in anonymous usage telemetry |
| `sentinel/` | Go | 2 | Wake-on-connect listener holding ports for sleeping pods |
| `capture-sidecar/` | Go | 3 | Ephemeral AF_PACKET packet capture sidecar with BPF filtering |
| `tunnel/` | Go | 0 (stdlib only) | Relay client supervisor for frp/Tailscale/playit |
| `gp-module/` | Go | 2 | Module authoring CLI: scaffold, offline validate, dry-run preview, OCI package |
| `mcp-server/` | Go | 5 | Standalone, strictly read-only MCP server for AI-assisted cluster diagnostics |
| `test/e2e/` | Go | 4 | kind + Helm + real-component end-to-end suite |
| `web/` | TypeScript/React | 18 runtime + 26 dev | Dashboard: routing, data fetching, UI primitives, console/file editors, tests |

## Shared Go modules

### netguard

`netguard/go.mod` has **no `require` block at all** — it imports only the
Go standard library (`context`, `net`, `net/http`, `errors`, `strings`,
`syscall`, `time`). It is a dial-time SSRF guard: `IsAllowed` (permissive,
for admin-configured infrastructure) and `IsPublic` (strict, for user-supplied
targets) both work by inspecting the resolved IP in a `net.Dialer.Control`
hook, so no client library is needed — just `net` and `syscall` for the
dial-time hook itself.

**Callers and policies:**
- **Operator** (`operator/internal/modsrc/`): module-source HTTP/git fetches via `IsAllowed`.
- **API gateway** (`api/internal/notify/{notify,deliver}.go` and `api/internal/steam/resolver.go`): admin-configured notification sinks via `IsAllowed`; Steam name resolution via `IsPublic`.
- **Agent** (`agent/internal/mods/mods.go` and `agent/internal/rcon/websocket.go`): user-supplied mod downloads via `IsPublic`; loopback WebSocket RCON via `IsAllowed`.

It is a local `replace` module (`replace github.com/GameplanePanel/gameplane/netguard => ../netguard` in every importer's `go.mod`), not a published module — each Dockerfile that builds an importer must `COPY netguard/` into the build context alongside the component's own source.

### gameaction

`gameaction/go.mod` also has **no `require` block** — stdlib only
(`fmt`, `strconv`, `strings`, `text/template`). It validates raw user
input against a module's declared action parameters (`Resolve`, rejecting
control characters and enforcing type/length/enum constraints — the
console-injection guard) and renders a `text/template`-based command
string from the sanitized params (`Compile`/`Render`).

Imported by:
- `agent/internal/actions/actions.go` — the RCON quick-action transport.
- `api/internal/ws/actions.go` — the stdin pod-attach quick-action transport.

Both importers call `Resolve` independently before rendering, since each
is its own trust boundary (per the package doc comment). Also a local
`replace` module; same Dockerfile `COPY` requirement as netguard.

### gameproto

`gameproto/go.mod` has **no `require` block at all** — it imports only the
Go standard library (`bufio`, `bytes`, `encoding/binary`, `encoding/json`,
`errors`, `fmt`, `io`, `strconv`, `strings`). It is a wire-protocol parser that
classifies Minecraft (Java) and Terraria connection handshakes from the
first bytes off the wire, so a proxy can decide whether a connection is a
real client join before waking a sleeping pod.

Imported by:
- `sentinel/` — to hold a client's connection open while the sentinel wakes the target `GameServer`'s pod, replaying the buffered handshake once the pod is ready.

Also a local `replace` module (`replace github.com/GameplanePanel/gameplane/gameproto => ../gameproto` in `sentinel/go.mod`); same Dockerfile `COPY` requirement as netguard.

### telemetryschema

`telemetryschema/go.mod` has **no `require` block at all** — it imports only the
Go standard library (`bytes`, `crypto/ed25519`, `crypto/hkdf`, `crypto/rand`,
`crypto/sha256`, `embed`, `encoding/base64`, `encoding/hex`, `encoding/json`,
`errors`, `fmt`, `io`, `math`, `regexp`, `slices`, `sort`, `strings`, `time`). It
is the shared telemetry report contract: the basic and extended report types,
the fixed category enumerations and band functions, the strict decoder, the
embedded official-module catalog, and Ed25519 report signing.

Imported by (spec 022, in progress):
- `telemetry-receiver/` — to decode and validate reports (`Decode`) and to verify signatures (`Verify`).
- `api/` — to build, encode and sign the reporter's payload and the admin preview.

Both importers use a local `replace` module (`replace github.com/GameplanePanel/gameplane/telemetryschema => ../telemetryschema`); the same Dockerfile `COPY` requirement as netguard applies.

### svcutil

`svcutil/go.mod` has **no `require` block at all** — stdlib only
(`log/slog`, `os`, `net/http`, `context`, `errors`, `time`). It provides
shared environment-variable parsing (`Or`, `OrInt`, `ParseLogLevel`) and a
graceful HTTP shutdown helper (`RunHTTP`), meant to cut down on
copy-pasted startup/shutdown boilerplate across the repo's standalone Go
binaries.

It has **no active importers yet** (see `svcutil/specs.md` for adoption
status) — it ships as an available shared module ahead of the other
services being migrated onto it.

## Services

### operator

controller-runtime operator reconciling GameServer, GameTemplate, Backup,
BackupSchedule, Restore, Module, and ModuleSource. Direct deps from
`operator/go.mod` (excluding the local `netguard` replace, covered above):

| Dependency | Version | Why | Status |
|---|---|---|---|
| `sigs.k8s.io/controller-runtime` | v0.24.1 | Core reconciler framework — every controller in `internal/controller/*` embeds `client.Client`; `cmd/main.go` builds the manager via `ctrl.NewManager` | direct-runtime | <!-- doc-versions: dependency -->
| `k8s.io/api` | v0.37.1 | Core/apps/batch typed API objects used across `api/v1alpha1/*_types.go` and controllers to build Pod/PVC/Job/Service/RBAC objects | direct-runtime | <!-- doc-versions: dependency -->
| `k8s.io/apimachinery` | v0.37.1 | `metav1.ObjectMeta`, `runtime.Scheme`, `types.NamespacedName`, etc. — pervasive across CRD types and controllers | direct-runtime | <!-- doc-versions: dependency -->
| `k8s.io/client-go` | v0.37.1 | `internal/controller/cluster_controller.go`, `gameserver_stop_attach.go` (pod exec/attach via `kubernetes.Clientset`), `cmd/main.go` scheme/clientset setup | direct-runtime | <!-- doc-versions: dependency -->
| `github.com/go-git/go-git/v5` | v5.19.2 | `internal/modsrc/git.go` — clones a ModuleSource's git repo/ref (HTTP or SSH) in-memory to discover module directories | direct-runtime |
| `github.com/go-git/go-billy/v5` | v5.9.1 | `internal/modsrc/git.go` — in-memory filesystem (`memfs`) backing the git clone above, avoiding disk writes for an admin-configured but externally-controlled fetch | direct-runtime |
| `golang.org/x/crypto` | v0.55.0 | `internal/modsrc/git.go` — SSH key/host-key handling for git-over-SSH ModuleSource cloning, alongside `go-git`'s SSH transport | direct-runtime | <!-- doc-versions: dependency -->
| `github.com/google/go-containerregistry` | v0.22.0 | `internal/verify/verify.go` — `name.NewDigest` + `remote.WithAuth/WithContext` resolve and authenticate the image ref being cosign-verified | direct-runtime | <!-- doc-versions: dependency -->
| `oras.land/oras-go/v2` | v2.6.2 | `internal/oci/client.go` — pulls Module OCI bundle artifacts from the registry (`registry/remote.Repository` + retry transport) | direct-runtime |
| `github.com/opencontainers/image-spec` | v1.1.1 | `internal/oci/client.go` — `ocispec.Manifest`/`ocispec.Descriptor` types for parsing OCI manifests when pulling module bundles | direct-runtime |
| `github.com/sigstore/cosign/v2` | v2.6.5 | `internal/verify/verify.go` — `cosign.VerifyImageSignatures`/`CheckOpts` verify Module OCI bundle signatures, keyed (against `cosign.pub`) or keyless (Fulcio/Rekor) | direct-runtime |
| `github.com/sigstore/sigstore` | v1.10.9 | `internal/verify/verify.go` — `pkg/fulcioroots` supplies Fulcio root certs for keyless cosign verification | direct-runtime |
| `golang.org/x/mod` | v0.40.0 | `semver.Compare`/`semver.IsValid` in `internal/controller/semver.go`, `module_controller.go`, `internal/modsrc/oci.go`, `internal/oci/client.go` — sorts/validates Module version tags | direct-runtime | <!-- doc-versions: dependency -->
| `sigs.k8s.io/yaml` | v1.6.0 | `internal/modsrc/bundle.go` parses `module.yaml`; `internal/controller/module_controller.go` parses the bundled `template.yaml` into JSON-tagged Go structs | direct-runtime |
| `github.com/robfig/cron/v3` | v3.0.1 | `internal/controller/backupschedule_controller.go` — parses a BackupSchedule's cron expression and computes the next run time | direct-runtime |
| `github.com/kubernetes-csi/external-snapshotter/client/v8` | v8.0.0 | `internal/controller/backup_volumesnapshot.go`/`backup_controller.go` — the CSI `VolumeSnapshot` types backing the volume-snapshot Backup/Restore strategy | direct-runtime |
| `github.com/prometheus/client_golang` | v1.24.1 | `internal/controller/metrics.go` — custom GameServer-phase and Backup Prometheus collectors registered with controller-runtime's metrics registry | direct-runtime |
| `github.com/go-logr/logr` | v1.4.3 | No non-test import found; only used in `internal/controller/helpers_envtest_test.go` (`logr.Discard()`). Runtime logging goes through controller-runtime's zap wrapper, which satisfies the `logr.Logger` interface without a direct import | **test-only** |
| `github.com/opencontainers/go-digest` | v1.0.0 | Only imported by `internal/oci/testregistry_test.go` (a fake OCI registry double for tests) | **test-only** |

Also declared as a `tool` directive (Go 1.24+ syntax, not a runtime
dependency): `sigs.k8s.io/controller-tools/cmd/controller-gen` — invoked by
`make generate`/`make manifests` to regenerate `zz_generated.deepcopy.go`
and the CRD/RBAC YAML.

The indirect closure is unusually large (~150 entries) because
`sigstore/cosign` and `go-git` each pull in substantial trees of their own
(TUF, Rekor, PKCS11, credential helpers, etc.) — normal for those
libraries, not a sign of dependency sprawl in Gameplane's own code.

### api

chi-based REST + WebSocket gateway. Direct deps from `api/go.mod`
(excluding the local `netguard`/`gameaction` replaces, covered above):

| Dependency | Version | Why | Status |
|---|---|---|---|
| `github.com/go-chi/chi/v5` | v5.3.2 | `cmd/main.go` router setup and nearly every file under `internal/handlers/` and `internal/ws/` — the HTTP router/middleware stack for the whole REST surface | direct-runtime |
| `github.com/coder/websocket` | v1.8.15 | `internal/ws/attach.go`, `podlogs.go`, `dialer.go` — upgrades HTTP to WebSocket for exec/attach terminal streaming and pod-log tailing, and dials the outbound WS proxy path | direct-runtime |
| `github.com/coreos/go-oidc/v3` | v3.20.0 | `internal/auth/oidc.go` — `oidc.NewProvider` + `IDTokenVerifier` for OIDC discovery and ID-token verification in the login flow | direct-runtime |
| `golang.org/x/oauth2` | v0.36.0 | `internal/auth/oidc.go` — `oauth2.Config` drives the OIDC authorization-code exchange | direct-runtime | <!-- doc-versions: dependency -->
| `golang.org/x/crypto` | v0.55.0 | `internal/auth/password.go` — `argon2.IDKey` hashes/verifies local-login passwords (argon2id), plus a constant-time dummy-verify path to avoid a user-enumeration timing leak | direct-runtime | <!-- doc-versions: dependency -->
| `k8s.io/api`, `k8s.io/apimachinery`, `k8s.io/client-go` | v0.37.0 | `internal/kube/*.go` (client wrapper, kubeconfig loading, watches, exec streaming), `internal/handlers/*.go`, `internal/notify/sinks.go`, `internal/rbac/rbac.go` — reading/writing CRDs and core objects | direct-runtime | <!-- doc-versions: dependency -->
| `sigs.k8s.io/controller-runtime` | v0.24.1 | `cmd/main.go` — only `ctrl.GetConfig()` is used, to load the kubeconfig/in-cluster `*rest.Config` at startup; not the manager/reconciler machinery (that's the operator's job — see rule "the operator is authoritative"). `envtest.Environment` in `internal/handlers/suite_envtest_test.go` is test-only | direct-runtime (minimal) | <!-- doc-versions: dependency -->
| `sigs.k8s.io/yaml` | v1.6.0 | `internal/handlers/module_upload.go` — parses `module.yaml` metadata and `template.yaml` from uploaded module bundles | direct-runtime |
| `github.com/minio/minio-go/v7` | v7.3.0 | `internal/audit/s3.go` — S3-compatible client (`minio.New`, `PutObject`) backing the S3 audit-log sink | direct-runtime |
| `github.com/prometheus/client_golang` | v1.24.1 | `cmd/metrics.go` (`promhttp` handler at `/metrics` on the separate metrics listener); `promauto` counters in `internal/notify/notify.go`, `internal/audit/s3.go`, `internal/audit/audit.go` | direct-runtime |
| `modernc.org/sqlite` | v1.57.0 | `internal/db/db.go` — blank-imported to register the `"sqlite"` `database/sql` driver, the default persistence backend | direct-runtime |
| `github.com/jackc/pgx/v5` | v5.11.0 | `internal/db/db_postgres.go` (behind `//go:build postgres`) — wraps the `pgx/v5/stdlib` connector (rebinding `?` placeholders to `$n`) when built with `-tags postgres`; the alternative persistence backend | direct-runtime (opt-in build tag) |
| `golang.org/x/mod` | v0.40.0 | `internal/handlers/semver.go` — `semver.Compare` orders module versions for the version-switch/update-detection UI | direct-runtime | <!-- doc-versions: dependency -->
| `golang.org/x/sync` | v0.22.0 | `internal/registry/registry.go` — `singleflight.Group` collapses concurrent DB + live-Secret registry-credential lookups into one in-flight call | direct-runtime | <!-- doc-versions: dependency -->
| `github.com/go-jose/go-jose/v4` | v4.0.2 | No non-test import found; used only in `internal/auth/oidc_issuer_test.go`/`oidc_more_test.go` to build fake JWKS/signed JWTs for testing the OIDC verifier | **test-only** |
| `github.com/prometheus/client_model` | v0.6.2 | No non-test import found; used in `internal/audit/audit_webhook_test.go`/`s3_test.go` (`dto "prometheus/client_model/go"`) to assert counter values in tests | **test-only** | <!-- doc-versions: dependency -->

**Confirmed policy**: `k8s.io/metrics` is not imported anywhere in `api/`
(`grep -rn "k8s.io/metrics" api/` returns nothing but the explanatory
comments). `internal/handlers/cluster.go` reads the metrics-server
aggregated API (`/apis/metrics.k8s.io/v1beta1/nodes`) via
`h.k.Typed.CoreV1().RESTClient().Get().AbsPath(...).DoRaw(ctx)` — a raw
REST call over the existing typed client-go clientset — specifically to
avoid adding a whole extra module for one read-only GET. See "Notable
policies" below.

### agent

In-pod sidecar handling console/RCON, files, logs, players, heartbeat,
and quiesce. Direct deps from `agent/go.mod` (excluding the local
`netguard`/`gameaction` replaces, covered above):

| Dependency | Version | Why | Status |
|---|---|---|---|
| `github.com/go-chi/chi/v5` | v5.3.2 | `cmd/main.go` — router for the agent's whole HTTP surface; every feature package (console, logs, files, players, status, actions, quiesce, lifecycle, mods) exposes `Mount(r chi.Router, ...)` onto it | direct-runtime |
| `github.com/coder/websocket` | v1.8.15 | Dual role: server-side WS upgrade for `internal/console/console.go` (RCON console stream) and `internal/logs/logs.go` (live log tail); client-side outbound dial in `internal/rcon/websocket.go` implementing the Rust dedicated server's WebRcon protocol | direct-runtime |
| `k8s.io/apimachinery` | v0.37.1 | `internal/heartbeat/heartbeat.go` only — `metav1.Now()`/`PatchOptions{}`, `types.MergePatchType` to build the JSON merge-patch that updates `status.agent` | direct-runtime | <!-- doc-versions: dependency -->
| `k8s.io/client-go` | v0.37.1 | `internal/heartbeat/heartbeat.go` only — `rest.InClusterConfig()` then `dynamic.NewForConfig(...).Resource(gvr).Namespace(...).Patch(...)` to patch the owning GameServer's status every tick; only the `dynamic` + `rest` subpackages are used, no typed clientset | direct-runtime | <!-- doc-versions: dependency -->
| `github.com/prometheus/client_golang` | v1.24.1 | `cmd/main.go` — serves `promhttp.Handler()` at `/metrics`; only the `promhttp` subpackage, no custom collectors | direct-runtime |
| `golang.org/x/sys` | v0.47.0 | `internal/usage/usage.go` — `unix.Statfs`/`unix.Statfs_t` to report the game data volume's disk usage over heartbeat. **Not** used for a PTY: `internal/console/console.go`'s doc comment states the agent's console is RCON-only by design ("no real PTY"); a `GameTemplate.spec.consoleMode: "pty"` game is bridged instead through `api/internal/ws/attach.go` against the Kubernetes pod-attach API, with no agent involvement at all | direct-runtime | <!-- doc-versions: dependency -->

Note `k8s.io/apimachinery`/`k8s.io/client-go` are at v0.37.1 here, as in <!-- doc-versions: dependency -->
`operator` and `sentinel`; `api`, `mcp-server` and `test/e2e` are still on v0.37.0. <!-- doc-versions: dependency -->
Each Go module in the workspace resolves its own client-go version
independently (there's no shared root `go.mod`), so patch versions can
drift between modules as Dependabot bumps them one module at a time.

### audit-syslog-bridge

`audit-syslog-bridge/go.mod` has **no `require` block** — stdlib only
(`crypto/subtle`, `crypto/tls`, `log/slog`, `net`, `net/http`, etc.). It's
a small, schema-agnostic HTTP-JSON → syslog (RFC 5424) relay: it accepts
one JSON POST body, frames it as an RFC 5424/6587 message, and forwards it
over a lazily-dialed TCP/TCP+TLS/UDP connection (`main.go`'s `forwarder`
type). Sits behind the API's audit webhook sink
(`api.audit.webhook.syslogBridge.enabled`). No third-party library needed
because the entire job is HTTP-in, hand-formatted-string-out.

### telemetry-receiver

`telemetry-receiver/go.mod` has exactly one direct dependency:

| Dependency | Version | Why |
|---|---|---|
| `github.com/prometheus/client_golang` | v1.24.1 | `main.go` — `prometheus.NewRegistry()` + `CounterVec`/`Histogram` for `gameplane_telemetry_reports_total`/`_servers`/`_templates`, exposed via `promhttp.HandlerFor` at `/metrics` |

Deliberately kept to "stdlib + client_golang only" (per the package doc
comment) so it can be deployed standalone on a public host collecting
reports from many installs, independent of Gameplane's release cadence.
It validates the API's daily `{version, servers, templates}` telemetry
POST, logs it structurally, and aggregates it into the metrics above —
never storing raw per-install reports.

### mcp-server

Strictly read-only Model Context Protocol server for AI-assisted cluster
diagnostics. Direct deps from `mcp-server/go.mod`:

| Dependency | Version | Why |
|---|---|---|
| `github.com/modelcontextprotocol/go-sdk` | v1.8.0 | `main.go`/`tools.go` — `mcp.NewServer`, `mcp.AddTool`, `mcp.StdioTransport` implement the MCP (JSON-RPC 2.0) protocol itself; every registered tool (`list_gameplane_resources`, `get_gameplane_resource`, `list_pods`, `get_pod`, `list_events`, `get_pod_logs`, `propose_fix`) is built against this SDK's types |
| `k8s.io/client-go` | v0.37.0 | `internal/kube/client.go` — `kubernetes.NewForConfig`/`dynamic.NewForConfig` build the typed and dynamic clientsets, kept as unexported fields on `Client` so no exported method can reach a mutating verb | <!-- doc-versions: dependency -->
| `k8s.io/api` | v0.37.0 | `internal/kube/client.go` — `corev1` types for the typed Pod/Event reads (`ListPods`, `GetPod`, `ListEvents`, `PodLogs`) | <!-- doc-versions: dependency -->
| `k8s.io/apimachinery` | v0.37.0 | `internal/kube/client.go` — `unstructured.Unstructured(List)`, `runtime.Scheme`, `schema.GroupVersionResource` back the dynamic-client reads of 7 of Gameplane's 9 CRDs (not Cluster or NetworkCapture) (redeclared GVK/GVR locally rather than importing the operator module's generated types, to stay standalone) | <!-- doc-versions: dependency -->
| `sigs.k8s.io/controller-runtime` | v0.25.1 | `main.go` — only `ctrl.GetConfig()`, to load the kubeconfig (in-cluster, falling back to `KUBECONFIG`/`~/.kube/config`) that builds the `kube.Client` above | <!-- doc-versions: dependency -->

The read-only guarantee has two layers: the handler boundary (only
List/Get-shaped methods are exported from `internal/kube`, and
`tools.go`'s handlers receive only that `*kube.Client`) and RBAC (a
`get`/`list`/`watch`-only ClusterRole), which is the layer that stops
mutation by any code in the process. The go-sdk itself has no bearing
on that guarantee; it's purely the protocol transport.

### sentinel

Wake-on-connect proxy that holds a listener open for a sleeping
`GameServer`'s port, classifies the first bytes of an inbound connection
via `gameproto`, and triggers a pod wake before replaying the buffered
handshake. Direct deps from `sentinel/go.mod` (excluding the local
`gameproto` replace, covered above):

| Dependency | Version | Why |
|---|---|---|
| `k8s.io/apimachinery` | v0.37.1 | `main.go` — typed/unstructured plumbing for reading the target `GameServer`'s status and idle/wake fields | <!-- doc-versions: dependency -->
| `k8s.io/client-go` | v0.37.1 | `main.go` — in-cluster client used to read `GameServer` status and patch it to trigger a wake | <!-- doc-versions: dependency -->

### capture-sidecar

Ephemeral, on-demand AF_PACKET packet capture container that runs
alongside a `GameServer` pod with `CAP_NET_RAW` (see
[`docs/security.md`](security.md) for the capability/PodSecurity
trade-off). Direct deps from `capture-sidecar/go.mod`:

| Dependency | Version | Why |
|---|---|---|
| `github.com/gopacket/gopacket` | v1.7.2 | `internal/capture/writer.go`/`afpacket.go` — `AF_PACKET` socket capture via `gopacket/afpacket`, plus pcap-format frame writing via `pcapgo`/`layers` |
| `github.com/packetcap/go-pcap` | v0.0.0-20260731105150-c86974bbfbcd | `internal/capture/filter.go` — compiles the tcpdump-style filter expression to BPF without requiring libpcap at runtime | <!-- doc-versions: dependency -->
| `golang.org/x/net` | v0.59.0 | `internal/capture/filter.go`/`afpacket.go` — `x/net/bpf`: assembling the classic-BPF program attached to the AF_PACKET socket | <!-- doc-versions: dependency -->

### tunnel

`tunnel/go.mod` has **no `require` block at all** — stdlib only
(`os/exec`, `encoding/json`, `net/http`, `context`, etc.). It's the relay
client supervisor: it launches and monitors the `frp`, `tailscaled`, or
`playitd` client binary as a subprocess, configured by env vars
(`GAMESERVER_NAME`, `TUNNEL_TYPE`, etc.) that the operator renders from
`spec.networking.tunnel`. For `playit`, `playit_reporter.go` polls
`playitd`'s local Unix-socket IPC (newline-delimited JSON `get_state`
frames) for the assigned public addresses, then PATCHes the `GameServer`
resource's `status.tunnelEndpoints` directly against the Kubernetes API
server (`https://kubernetes.default.svc`, using the pod's service-account
token) with a stdlib `net/http` client — nothing is reported to the
agent. The `frp` and Tailscale addresses are computed by the operator
itself rather than polled by the supervisor. No third-party library is
needed because process supervision, JSON/Unix-socket IPC, and HTTP status
patching are all stdlib jobs.

### gp-module

Module authoring CLI (`init`, `validate`, `preview`, `package`) used by
module authors to scaffold, offline-validate, dry-run preview, and OCI
package a game module before publishing it. Direct deps from
`gp-module/go.mod`:

| Dependency | Version | Why |
|---|---|---|
| `gopkg.in/yaml.v3` | v3.0.1 | `internal/common/yaml_ast.go`, `internal/validator/`, `internal/scaffold/`, `internal/preview/`, `internal/packager/` — parses/round-trips `module.yaml`/`template.yaml` while preserving comments and key order for scaffold output |
| `k8s.io/apimachinery` | v0.37.0 | `internal/preview/memory.go` — `resource.Quantity` parsing, to preview a template's resource requests/limits the same way the operator would | <!-- doc-versions: dependency -->

## Frontend

### web

React 18 + TypeScript strict + Vite dashboard (`web/package.json` version
`0.3.0`, matching the repo version):

**Framework / core**
- `react` `^19.2.8`, `react-dom` `^19.2.8` — base UI framework, rendered via `createRoot` in `src/main.tsx`, used throughout every component.

**Routing / data fetching**
- `@tanstack/react-router` `^1.170.32` — file-based route tree; `createRouter`/`RouterProvider` in `src/main.tsx`, tree built in `src/router/tree.tsx`, `Link`/`useNavigate` used across `src/routes/*.tsx`.
- `@tanstack/react-query` `^5.102.8` — server-state cache; `QueryClient`/`QueryClientProvider` in `src/main.tsx`, `useQuery`/`useMutation` used pervasively across routes and `src/lib/` — the dominant data-fetching layer.
- `@tanstack/react-virtual` `^3.14.10` — `useVirtualizer` in `src/routes/tabs/Logs.tsx`, virtualizes the log-line list.
- `@tanstack/router-devtools` (dev) — **no import found anywhere in `src/`**. Installed but apparently unused; flag as a candidate for removal or an intentionally-deferred addition.

**UI primitives (HeroUI v3)**
- `@heroui/react` `^3.2.4` — foundational component library (Button, Input, Select, Modal, Table, etc.); composed and themed in `src/components/ui/` (Gameplane's composition layer over HeroUI).
- `@heroui/styles` `^3.2.4` — companion style package with semantic token infrastructure for light/dark modes and brand customization; `@heroui/styles` is imported in `src/main.tsx` for global theme setup.
- `clsx` `^2.1.1` + `tailwind-merge` `^3.6.0` — combined into the `cn()` helper in `src/lib/utils.ts`, used app-wide for conditional/merged Tailwind classes and with HeroUI's theme tokens.
- `lucide-react` `^1.37.0` — icon set, imported in roughly 35 files (`AppLayout`, `Dashboard`, `Login`, dialogs, etc.).
- `react-aria` — peer dependency of HeroUI, providing accessibility primitives (via `react-aria-components`) used by HeroUI atoms.

**Editors / terminals**
- `@monaco-editor/react` `^4.7.0` — the `Editor` component in `src/routes/tabs/Files.tsx` (in-browser file editor) and `Placement.tsx`; given its own `manualChunks` bundle in `vite.config.ts`.
- `@xterm/xterm` `^6.0.0` + `@xterm/addon-fit` `^0.11.0` — `Terminal`/`FitAddon` in `src/routes/tabs/useConsoleTerminal.ts`, driving the live server console (`Console.tsx`); also chunked separately in `vite.config.ts`. <!-- doc-versions: dependency -->

**Styling / build**
- `tailwindcss` `^4.3.3`, `autoprefixer` `^10.5.4`, `postcss` `^8.5.26` (dev) — `postcss.config.js` wires Tailwind + autoprefixer; `tailwind.config.ts` defines the design tokens used throughout `src/styles` and Tailwind classes app-wide.
- `vite` `^8.2.2`, `@vitejs/plugin-react` `^6.1.1` (dev) — `vite.config.ts` drives the dev server (with the API/WS proxy to the in-cluster API used by `make web-dev`), production build, and is the engine behind `vitest.config.ts`.
- `typescript` `^6.0.3` (dev) — strict-mode compile per `tsconfig.json`; `npm run build` is `tsc -b && vite build`.

**Lint** (dev, all wired directly in `eslint.config.js`)
- `eslint` `^9.39.5`, `@eslint/js` `^10.0.1`, `@typescript-eslint/eslint-plugin` `^8.68.0`, `@typescript-eslint/parser` `^8.68.0`, `eslint-plugin-react` `^7.37.5`, `eslint-plugin-react-hooks` `^7.1.1`, `globals` `^17.11.0` — flat-config ESLint enforcing `strict`, `@typescript-eslint/no-explicit-any: error`, `@typescript-eslint/no-floating-promises: error`, run via `npm run lint`.

**Testing — unit tier (Vitest)**
- `vitest` `^4.1.11`, `@vitest/coverage-v8` `^4.1.10` (dev) — `vitest.config.ts` (jsdom environment, coverage thresholds), run via `npm test`/`test:cover`.
- `jsdom` `^30.0.1` (dev) — the test environment itself.
- `@testing-library/react` `^16.3.3`, `@testing-library/user-event` `^14.6.6`, `@testing-library/jest-dom` `^7.0.1`, `@testing-library/dom` `^10.4.1` (dev) — `render`/`screen`, interaction simulation, and DOM matchers used across the co-located `*.test.tsx` files.
- `msw` `^2.15.0` — dual role: unit-test request mocking (`src/test/server.ts`/`handlers.ts`, used via `setupServer` in most `*.test.tsx` files) **and** browser-mode mocking for Playwright's mock tier (`src/test/browser-msw.ts` uses `msw/browser`'s `setupWorker`, loaded by `main.tsx` when `VITE_E2E_MOCK` is set).
- `vitest-websocket-mock` `^0.7.0` (dev) — `src/test/ws.ts`, mocks the WebSocket used by `src/lib/ws.ts`/console streaming in unit tests. <!-- doc-versions: dependency -->

**Testing — e2e tier (Playwright)**
- `@playwright/test` `^1.62.1` (dev) — `playwright.config.ts` defines mock vs. live run modes (`GAMEPLANE_E2E_TARGET`), driven by `make test-web-e2e-mock`/`test-web-e2e-live`. Live mode hits a real kind cluster via a `kubectl port-forward` globalSetup; mock mode reuses the same `msw` handlers as the unit tier.

**Types**
- `@types/react` `^19.2.18`, `@types/react-dom` `^19.2.5`, `@types/node` `^26.4.0` (dev) — type definitions consumed implicitly by TypeScript, no direct import.

## Test-only

### test/e2e

kind + Helm + real-component end-to-end suite (build tag `e2e`), run
against a throwaway kind cluster per CI bucket or a reused
cluster/kubelab via `GAMEPLANE_E2E_REUSE_CLUSTER`. Direct deps from
`test/e2e/go.mod`:

| Dependency | Version | Why |
|---|---|---|
| `github.com/coder/websocket` | v1.8.15 | `api_ws_e2e_test.go` — a real WebSocket client dialing the API's WS endpoints (console/logs) end-to-end, exercising the same protocol the dashboard's `web/src/lib/ws.ts` uses |
| `k8s.io/client-go` | v0.37.0 | Used across most `*_e2e_test.go` files to build a real cluster client, apply/watch objects, and assert on cluster state after driving the API/dashboard | <!-- doc-versions: dependency -->
| `k8s.io/apimachinery` | v0.37.0 | 28 files reference it — `metav1`, `types`, `runtime` types used throughout the assertions and helper builders (`env.go`, `test_helpers_e2e_test.go`) | <!-- doc-versions: dependency -->
| `k8s.io/api` | v0.37.0 | Core/apps typed objects used to construct and inspect Pods/Deployments/etc. during the e2e flows | <!-- doc-versions: dependency -->

The `k8s.io/*` trio here is on v0.37.0, like `api` and `mcp-server` <!-- doc-versions: dependency -->
(`operator`, `agent` and `sentinel` are on v0.37.1); each module in the workspace resolves its own <!-- doc-versions: dependency -->
client-go version independently (there's no shared root `go.mod`), so
patch versions can drift between modules.

## Toolchain / build-time

These aren't code dependencies of any component; they're invoked by
`Makefile` targets and GitHub Actions workflows.

- **`sigs.k8s.io/controller-tools/cmd/controller-gen`** (`tool` directive in `operator/go.mod`, pulling in `sigs.k8s.io/controller-tools v0.20.1` indirect) — `make generate`/`make manifests` regenerate `zz_generated.deepcopy.go` and the CRD/RBAC YAML. <!-- doc-versions: dependency -->
- **`github.com/vladopajic/go-test-coverage/v2@v2.18.9`** — pinned in `Makefile`'s `GO_TEST_COVERAGE_PKG`, run via `go run` (no install step) by `make cover-go-check` and CI's `go` job to enforce each module's `.testcoverage.yml` threshold.
- **`github.com/wadey/gocovmerge@v0.0.0-20160331181800-b5bfa59ec0ad`** — pinned in `Makefile`'s `GOCOVMERGE_PKG`, merges each module's `unit.out` + `envtest.out` coverage profiles before the threshold gate. <!-- doc-versions: dependency -->
- **`sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.19`** — installed by `make envtest-bin`; downloads the K8s 1.36.2 envtest binaries (`kube-apiserver`, `etcd`) that back `operator`'s and `api`'s `-tags=envtest` integration tests.
- **`golangci-lint` v2.12.2** — pinned in `.devcontainer/post-create.sh` (built from source, not the release binary, because the release binary is built with an older Go than the repo's `go 1.26.0` toolchain wants). Configured by `.golangci.yml` (bodyclose, errcheck, gosec, govet, ineffassign, staticcheck, unused, misspell, gofmt, goimports, revive, unparam, nilerr, noctx, errorlint, contextcheck). **Not currently invoked by CI** — `ci.yaml`'s `go` job runs only `go vet`, not `golangci-lint run`; `make lint-go` exists for local/devcontainer use and is called out in `docs/contributing.md` as a pre-PR step, but nothing in `.github/workflows/` gates on it. Same for the web job: it runs `npm run build` and `npm run test:cover`, not `npm run lint` — ESLint isn't CI-gated either.
- **`oras` CLI ≥ 1.2.0** (pinned `1.2.0` in the devcontainer; `oras-project/setup-oras@v2.0.1` action in `release.yaml`) — `modules/build.sh` pushes each `modules/<name>/` directory as an OCI module bundle (`make modules-push`); this is a *different* thing from `operator`'s `oras.land/oras-go/v2` **library** dependency, which is the operator's own pull-side client, not the CLI.
- **`cosign` CLI v3.1.2** (`sigstore/cosign-installer@v4.1.2`) — used in `publish-edge.yaml`/`release.yaml` to sign published container images, the Helm chart, and module bundles against `COSIGN_PRIVATE_KEY`, recording each signature in the public Rekor transparency log, and to round-trip-verify against the derived public key before publishing. Signatures use the classic format (`--new-bundle-format=false --use-signing-config=false`), which is what keeps them readable by the operator's `sigstore/cosign/v2` library; cosign's newer Rekor v2 flow additionally requires an RFC3161 timestamp authority that keyed signing cannot satisfy. `modules/build.sh` gates the same behaviour behind its own `--tlog-upload` flag so local and air-gapped authoring stays offline. Distinct from `operator`'s `sigstore/cosign/v2` **library** dependency, which only ever *verifies* (never signs) at reconcile time.
- **`kind`** v0.32.0 (devcontainer pin) / `helm/kind-action@v1.14.0` (`install_only: true` in CI) — spins up the ephemeral clusters for `make dev-up`, `make test-e2e`, and every `e2e-*` CI job. <!-- doc-versions: dependency -->
- **`helm`** (`azure/setup-helm@v5.0.1` in CI; `kubectl-helm-minikube` devcontainer feature, `helm: latest`) — chart lint (`helm lint`), chart render sanity-check, `helm template`, and `helm upgrade --install` in `deploy/kind/*.sh`/`Makefile`'s `dev-install`.
- **`docker buildx bake`** (`containers/docker-bake.hcl`, `docker/bake-action@v6`) — builds the operator/api/agent e2e images concurrently with a shared GitHub Actions layer cache (`.github/actions/e2e-images`); also builds the `e2e-gameprobe` headless bot image used only by the game-bot CI job.
- **`docker/build-push-action@v6` + `docker/metadata-action@v5`** — the actual multi-arch (`linux/amd64,linux/arm64`) image builds and GHCR tagging in `publish-edge.yaml` (main → `:edge`) and `release.yaml` (`v*` tags → versioned images + `oci://ghcr.io/<owner>/charts` Helm push).

The Helm chart itself (`charts/gameplane/Chart.yaml`) declares **no chart
`dependencies:`** — it's a single chart with no subcharts.

## Notable policies

- **No `k8s.io/metrics` module.** `api/` deliberately avoids adding
  `k8s.io/metrics` as a dependency for the one place it needs
  metrics-server data (`internal/handlers/cluster.go`'s node CPU/mem
  panel): it issues a raw `GET /apis/metrics.k8s.io/v1beta1/nodes` through
  the existing typed client-go clientset's `RESTClient()` instead. Adding a
  new module for a single read-only GET was judged not worth the extra
  dependency surface — the same reasoning that keeps `netguard`,
  `gameaction`, `audit-syslog-bridge`, and `telemetry-receiver` this lean.
- **Persistence is driver-selectable, but Postgres needs a rebuild.**
  `api/internal/db` registers `modernc.org/sqlite` (pure-Go, default)
  unconditionally, and compiles in `github.com/jackc/pgx/v5`'s `stdlib`
  driver only under the `postgres` build tag (`db_postgres.go`); without
  that tag, `db_nopostgres.go` stands in and `Open(ctx, "postgres", dsn)`
  returns `"postgres support not compiled in — rebuild with -tags
  postgres"`. Confirmed: `api/Dockerfile` (and thus the published
  `ghcr.io/.../api` image) does **not** pass `-tags postgres` — this is
  intentional and documented, not an oversight: `charts/gameplane/values.yaml`'s
  `api.db.driver` comment explicitly tells an operator who wants Postgres [experimental]
  to set `dsn` *and* rebuild the image with `-tags postgres` themselves.
  So `pgx/v5` is a real, live dependency, but it ships dormant in the
  default binary.
- **netguard's two policies, three importers.** Both `IsAllowed` (permissive,
  for admin-configured infrastructure) and `IsPublic` (strict, for user-supplied
  targets) are used by operator (module sources), API gateway
  (notification sinks via `IsAllowed`; Steam resolver and module registry via `IsPublic`), and agent
  (mod downloads via `IsPublic`; loopback WebSocket RCON via `IsAllowed`). The
  split prevents private-registry access from being re-opened (if strict rules
  apply to the operator) or agent SSRF from being introduced (if permissive rules
  apply to the agent).
- **Local `replace` modules need explicit Docker `COPY`s.** `netguard` and
  `gameaction` are both resolved via `replace ... => ../netguard` /
  `../gameaction` directives, not published modules. Every Dockerfile that
  builds an importer (`operator/Dockerfile`, `api/Dockerfile`,
  `agent/Dockerfile`) must `COPY` those directories into the build context;
  a Dockerfile edit that forgets this breaks the build with an unresolved
  module error, not a subtle bug — but it's a manual step to remember when
  adding a new local module.
- **Signing has two independent code paths that must be kept in sync.**
  Verification (`operator`'s `sigstore/cosign/v2` **library**, checked at
  Module-reconcile time against `cosign.pub`) and signing (the `cosign` **CLI**
  v3.0.6, `sigstore/cosign-installer@v4.1.2` in CI) both use classic cosign
  signature format (`--new-bundle-format=false --use-signing-config=false`) for
  mutual compatibility. A bump to either side needs to preserve that format
  compatibility and the ability to sign with `--tlog-upload` (Rekor logging).
- **Lint is configured but not CI-enforced.** `.golangci.yml` and
  `web/eslint.config.js` are both real, actively-maintained rule sets (see
  "Fix, don't silence" in `CLAUDE.md`), but neither `golangci-lint run` nor
  `npm run lint` currently appears in `.github/workflows/ci.yaml` — only
  `go vet` (Go) and `npm run build`/`test:cover` (web) are CI-gated. Lint
  is a devcontainer/local (`make lint`) and `docs/contributing.md`
  pre-PR-checklist step today, not an automated CI gate.
- **Workspace-wide `k8s.io/*` versions.** The six client-go consumers
  are one patch apart: `operator`, `agent` and `sentinel` are on
  `k8s.io/*` v0.37.1, `api`, `mcp-server` and `test/e2e` on v0.37.0. <!-- doc-versions: dependency --> Because `go.work`
  links independent modules rather than a single root `go.mod`, Dependabot
  bumps them one module at a time and patch versions drift between bumps.
