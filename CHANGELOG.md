# Changelog

All notable changes to Gameplane are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once it
reaches `1.0.0`. Pre-1.0 minor versions may contain breaking changes.

## [Unreleased]

### Upgrade Notes

- **Standalone panel/API:** run the central panel with Docker Compose or the
  standalone Helm profile, without a local Kubernetes cluster or operator.
  Register remote clusters explicitly and back up the credential encryption key
  with the database. See [standalone installation](docs/standalone-panel.md).
- **Remote module/template grants:** module and template permissions now apply
  to their selected cluster. Grant these permissions explicitly on remote
  clusters; a local grant no longer authorizes another cluster's catalog.

- **Telemetry is on by default for new installs, after a first-login
  notice:** a new install with a telemetry destination in effect now shares
  basic and extended data by default; the stock destination is the project's
  receiver at telemetry.gameplane.net. The first admin to sign in sees a
  notice that lists every field of both tiers and names the destination;
  nothing is sent before then. Basic is `version`, `servers` and
  `templates`. Extended adds a random install ID, the Kubernetes minor
  version, a distribution category, node architectures and a node-count
  band, server counts per official module plus one `custom` count, feature
  flags (wake-on-connect, tunnel types, packet capture, backups, single
  sign-on, audit forwarding), a cluster-count band, the database kind, the
  UI language, the install's public signing key and the send time. Each
  tier has its own toggle in **Admin Settings → Telemetry**, which also
  shows the destination, a preview of the next report and an install-ID
  reset. The data-handling statement is at
  <https://gameplane.net/telemetry/>. See
  [`docs/install.md`](docs/install.md#telemetry).
- **Installs whose basic toggle was already on start sending to the
  project's default receiver:** an install that saved **Send anonymous usage
  metrics** while no destination was configured (so it had no effect) starts
  sending basic reports to the project's default receiver
  (telemetry.gameplane.net) after upgrade.
  Extended stays off until an admin turns it on, and installs that never
  saved a choice stay off. Installs already using a custom
  `api.telemetry.endpoint` or the bundled receiver keep sending only there.
  Set `api.telemetry.enabled=false` before upgrading to prevent any
  sending.
- **Empty `api.telemetry.endpoint` now means the project default:**
  It now means the project's default receiver
  (https://telemetry.gameplane.net/ingest), so the admin toggles have an
  effect on a stock install. Set `api.telemetry.enabled=false` for a hard off (air-gapped
  clusters, privacy-sensitive installs); it overrides the endpoint, the
  bundled receiver and the admin toggles, and hides the notice.
- **The receiver's `/metrics` moved to the dashboard port and needs the
  dashboard token:** the receiver's Prometheus endpoint is no longer on
  `:8080`; it is on `:8081` and accepts only
  `Authorization: Bearer <DASHBOARD_TOKEN>`. Without
  `api.telemetry.receiver.dashboard.tokenSecretRef.name` there is no
  dashboard listener and no metrics endpoint, and the chart renders no
  `ServiceMonitor` for the receiver. Update your Prometheus scrape config
  or set the token Secret. See
  [`docs/telemetry-provider.md`](docs/telemetry-provider.md).
- **The bundled receiver now stores data:** with
  `api.telemetry.receiver.persistence.enabled` (default `true`, 1Gi) it
  keeps daily aggregates for 24 months and activity records for 90 days in
  SQLite on a PVC, and runs a single replica with a `Recreate` strategy.
  `helm upgrade --reuse-values` from 0.3.0 needs no new values; with no
  `persistence` block the receiver keeps using an `emptyDir`, as before.
- **The receiver's `DASHBOARD_TOKEN` must be at least 32 characters:** a
  receiver with `DASHBOARD_TOKEN` set to a shorter value now exits at
  startup (`DASHBOARD_TOKEN must be at least 32 characters`). Replace the
  Secret behind `api.telemetry.receiver.dashboard.tokenSecretRef` with a
  value generated from 32 random bytes, for example
  `openssl rand -base64 32`, before upgrading. Replacing the token
  invalidates browser sessions and loses no data. Receivers with no token
  (no dashboard) are unaffected. See
  [`docs/telemetry-provider.md`](docs/telemetry-provider.md#secrets).

### Added

- **Extended telemetry tier:** a second, separately consented tier with a
  random install ID, environment, game-usage and feature-adoption
  categories. Every value comes from a fixed set, a band or a number; no
  names, namespaces, hostnames, addresses, player counts or custom module
  names. Reports are signed (Ed25519, key derived from a per-install secret
  and the install ID); a receiver binds each ID to its first key and refuses
  forged, stale and replayed reports. See
  [`docs/security.md`](docs/security.md#telemetry).
- **Telemetry receiver dashboard and public summary:** a private,
  token-protected dashboard (`DASHBOARD_LISTEN_ADDR`, default `:8081`) with
  7-, 30-, 90- and 365-day views, durable daily aggregates
  (`RETENTION_DAYS`, default `730`, minimum `365`), per-install activity
  records that expire (`ACTIVITY_EXPIRY_DAYS`, default `90`, minimum `31`),
  a per-source daily ingest limit (`INGEST_SOURCE_DAILY_LIMIT`, default
  `20`), `TRUSTED_PROXY_CIDRS`, an `ID_PEPPER` secret and an optional public
  `GET /v1/summary` (`PUBLIC_SUMMARY=true`). Helm values:
  `api.telemetry.enabled`, `api.telemetry.interval`,
  `api.telemetry.receiver.{persistence,dashboard,publicSummary,pepperSecretRef,retentionDays,activityExpiryDays,ingestSourceDailyLimit,trustedProxyCIDRs}`.
- **Proof-of-work on telemetry ingestion (off by default):** a receiver with
  `INGEST_POW=true` serves `GET /v1/challenge` and requires a solved
  challenge on `/ingest` (`428 pow_required` / `pow_invalid`, `503 pow_busy`
  when its used-challenge set is full). The difficulty is zero up to
  `INGEST_POW_TARGET_PER_MIN` challenges a minute (default `60`), then rises
  with the rate between `INGEST_POW_MIN_BITS` (default `0`) and
  `INGEST_POW_MAX_BITS` (default `22`, at most `26`), and decays one bit
  every five minutes. New metrics: `gameplane_telemetry_pow_challenges_total`
  and `gameplane_telemetry_pow_bits`; `gameplane_telemetry_refused_total`
  gains the reasons `pow_required`, `pow_invalid` and `pow_busy`, and
  `gameplane_telemetry_rate_limited_total` the route `challenge`. Helm values:
  `api.telemetry.receiver.ingestPow.{enabled,targetPerMinute,minBits,maxBits}`.
  The API reporter solves a challenge when its destination offers one and
  sends without it otherwise. See
  [`docs/telemetry-provider.md`](docs/telemetry-provider.md#proof-of-work-on-ingest).
- **Provider runbook:** [`docs/telemetry-provider.md`](docs/telemetry-provider.md)
  covers deploying the receiver, TLS, keeping `:8081` private, secrets and
  rotation, retention, scraping `/metrics` and backing up `telemetry.db`.

## [0.3.0] — 2026-10-05

The first Gameplane release without a pre-release suffix, a pre-v1 release. It
contains every change made since v0.2.0-beta.8, including the changes first
previewed in v0.3.0-rc.1 and the pre-release audit fixes summarized under
0.3.0-rc.2 below. It does not claim v1 or production support; see
[`docs/roadmap.md`](docs/roadmap.md).

### Upgrade Notes

- **Network capture feature adds an emptyDir volume to every game pod:** The
  network capture feature (protocol reverse-engineering and admin-only traffic
  diagnostics) requires a pre-provisioned volume on every game pod, whether or
  not that server will ever opt into capture. This is a structural requirement:
  Kubernetes ephemeral containers can add containers to a running pod without pod
  recreation, but cannot add volumes. The capture `emptyDir` volume must exist in
  the pod template before the ephemeral sidecar is injected, so it is provisioned
  unconditionally at cluster install time.
  **Consequence: every existing GameServer will roll once on upgrade to this
  release.** Each pod will be recreated with the new template (containing the
  empty capture volume), interrupting any active player sessions. This is a
  one-time event at upgrade time and does not repeat on each server start. Plan
  your upgrade window for a time when you can tolerate a rolling restart of all
  game servers. The upgrade note for FR-001 and SC-007 (specification items
  tracking pod immutability) is updated to reflect this: non-opted-in game servers
  are no longer byte-identical between captures disabled and enabled; they carry
  an empty, unused capture volume. What they do NOT carry is the capture sidecar
  container itself (no ephemeral container, no `CAP_NET_RAW`, no network access
  change).

- **Owner-only server operations need the owner or an admin:** transferring
  ownership, editing collaborators, wiping data and deleting a server now need
  the server's owner or an admin. Operator-role users keep their other server
  permissions, but can no longer run these four operations on servers they
  don't own. Servers without a recorded owner (for example ones created with
  kubectl or GitOps) can be transferred, wiped or deleted only by an admin.
- **Playit tunnel NetworkPolicy egress widened:** the operator-managed
  NetworkPolicy for a GameServer using the `playit` tunnel provider now adds
  an unrestricted egress rule (all ports and protocols, any destination)
  alongside the DNS/advertised-ports rule every provider gets. Previously
  playit got only that rule, so it could not reach its relay (F-262).
- **Helm 3 reinstalling over leftover CRDs runs the CRD-apply hook:** a
  `helm install` onto Gameplane CRDs left behind by an uninstalled release
  runs the `crds.autoApply` hook to bring them up to date, so air-gapped
  clusters need `crds.autoApply.image` mirrored for that case. Under Helm 4,
  existing CRDs are applied natively; CRDs last applied by v0.2.0-beta.8 or
  earlier need a one-time `helm install --force-conflicts` (F-218).

### Added

- **Configurable backup/restore Job limits:** the restic backup and restore Jobs' retry limit and deadline are now Helm values (`operator.backupJobBackoffLimit`, default `2`; `operator.backupJobActiveDeadlineSeconds`, default `86400`), passed to the operator as `--backup-job-backoff-limit` / `--backup-job-active-deadline-seconds`.
- **Multicluster gateways and unified server management:** an optional
  private mTLS agent gateway (with its own Helm deployment) lets one dashboard
  and API manage authorized game servers across independent clusters.
  Dashboard, Servers, Backups and Search combine every site with optional
  location filters, and console, files, players, mods and captures are routed
  to the selected server's cluster (#542).
- **User theme customization (feature 016):** Settings → Theme & Appearance
  offers preset themes (Modern Pink, Legacy Orange) and custom color selection
  with live WCAG AA contrast validation. Custom accent and surface colors are
  persisted per user; appearance mode (light/dark) can follow the surface color.
  Custom CSS overlay allows per-user style tweaks; all customizations support
  import/export. Safe mode (`?safe-mode=1` or keyboard shortcut) suspends custom
  CSS and shows a banner without reloading. Theme bootstrap on login prevents
  flash; preferences are read/write controlled via RBAC (every user sees only
  their own) (#415).
- **Network capture sidecar (feature 003):** Admin-only, on-demand packet capture
  from running GameServer pods. Enabled per server via a Dashboard toggle, capture
  runs as an ephemeral sidecar container (no pod restart), is recorded as PCAPNG
  (Wireshark-compatible), and auto-expires after 7 days or a configurable size/duration
  limit. Download streams directly from the capture sidecar via the existing
  `<gs>-agent` Service. The capture `emptyDir` volume is pre-provisioned on every
  pod at cluster install time (the upgrade consequence above). Every capture
  session is recorded in the audit log with admin-only playback to the
  `captures:manage` role. For protocol reverse-engineering and diagnostics (#256).
- **Easy module building & authoring toolkit (feature 010):** Scaffolds, validates,
  previews, and packages custom game modules. The `gp-module` CLI supports three
  starter archetypes (`steamcmd`, `java`, `generic`) with configurable ports,
  storage, and metadata; offline validation with source-line diagnostics; dry-run
  manifest rendering (simulates `autoFromMemoryLimit`); and OCI bundle packaging
  with media-type and size-limit enforcement. The web dashboard (Modules page)
  offers a BuildModuleDialog with live validation, YAML editor, bundle download,
  and cluster installation. API endpoints: `/modules/builder/{scaffold,validate,preview,export}` (#370).
- **13 new top-steam-game modules (feature 015):** FiveM, Farming Simulator 25,
  Euro Truck Simulator 2, Mount & Blade Bannerlord, tModLoader, Team Fortress 2,
  BeamMP, Left 4 Dead 2, The Isle, ARK: Survival Evolved, Arma Reforger, Hell
  Let Loose, and Squad. Gameplane publishes four Gameplane-owned container
  images: `ghcr.io/gameplanepanel/gameplane/fivem` (FiveM + txAdmin), `farming-simulator-25`
  (Wine/Xvfb runner), `euro-truck-simulator-2` (Wine runner), and `beammp`
  (BeamMP + non-crashing diagnostic idle). All modules are signed with Cosign
  keyless signatures (#369).
- **Nuclear Option dedicated server support:** New `nuclearoption` remote-command
  protocol implemented in the agent's RCON client set, wired into console /
  players / quiesce; a Gameplane-owned, SteamCMD-based `nuclear-option`
  container image (no official image exists upstream); generic `min` / `max` /
  `minLength` / `maxLength` constraints on `ConfigField`, enforced in
  `materializeConfig` for every module, landed alongside it (#252, #255, #260).
- **Install-time configuration (feature 006):** Two Helm values for zero post-deploy
  configuration:
  - **Helm-seeded OIDC role mappings:** `api.oidc.groupsClaim`, `api.oidc.defaultRole`,
    and `api.oidc.roleMappings.{admin,operator,viewer}` seed a read-only "helm"
    OIDC provider, so OIDC-only installs need no `bootstrap-admin` run. Dashboard
    Settings → Authentication can override any role's group list (stored in
    `helmOverride.roleMappings`); changes take effect on next login (no restart).
    Reset via `DELETE /admin/config/auth/role-mappings/{role}`.
  - **Default StorageClass for game-data PVCs:** `operator.gameDataStorage.storageClassName`
    (Helm value) sets a cluster-wide default with precedence: GameServer >
    GameTemplate > install-time > cluster default. Missing StorageClass surfaces
    as `PVCProvisioningFailed` (recoverable) (#282).
- **Share link expiry options (feature 017):** Removed the hard 90-day cap that
  outlived long-running servers. Create requests now carry `expiresAt` (RFC3339
  instant) or `neverExpires: true` (not both, not neither = 400 error). Dashboard
  offers presets (15/30/60/90 days, No expiry) and custom date picker; 30 days
  is pre-selected. Past instant dates are rejected. The deprecated `expiresIn`
  field still works for one release (#392, #396).
- **Console WebSocket robustness:** Fixed 403 errors when dashboard is reached
  on non-default ports (e.g., `kubectl port-forward svc/gameplane-web 8088:80`)
  by forwarding `$http_host` instead of `$host` in the nginx config. Added guard
  on `send()` before socket open: input sent while CONNECTING is queued (max 500
  items) and flushed on open; input after reconnect is dropped (no silent replay);
  input after close is no-op. Queue tests verify behavior with fake sockets that
  throw like real browsers (#416).
- **Existing PVC for SQLite:** Helm `api.storage.existingClaim` allows installs
  to point the API database to a pre-existing PersistentVolumeClaim, enabling
  storage migration and lifecycle separation from the main Helm release (#390).
- **REST and CLI console protocols:** Agent implementation for `rest` (generic HTTP
  for games with HTTP admin APIs like FiveM txAdmin) and `cli` (local process
  execution for PTY-only games like Terraria). Both are wired to console, players,
  quiesce, lifecycle, and quick-action endpoints. The `rcon.protocol` enum is
  now `source;telnet;websocket;battleye;satisfactory;palworld;nuclearoption;rest;cli;none` (#369).
- **Security audit remediation:** Multiple fixes from a comprehensive security
  audit: share links are isolated by cluster; GameServer setting/image/service
  account/environment/tunnel changes are permission-checked; Secret and ConfigMap
  references must belong to the target server; optional network policies restrict
  audit and telemetry services to API traffic; SteamCMD image pinned to a stable
  version (#350).
- **Mod archive path confinement:** Symlink-escape and zip-slip guards hardened
  across every extraction and non-extract upload call site in the agent's mods
  handler (`ConfinePath` / `ConfineRelPath`, resolving symlinks on the deepest
  existing ancestor); an unconditional TLS-verification bypass in an e2e test
  helper was also fixed (#285, #290, #291).
- **CI hardening:** GitHub Actions workflow pins (100% to 40-hex SHAs with version
  comments), least-privilege permissions, measured timeouts, Dependabot expansion
  (Go modules, Dockerfiles, Actions), workflow-lint gates (actionlint + zizmor),
  and an AI-powered two-workflow reviewer with split trust boundaries (#292).
- **Design & visual polish:** HeroUI foundation and brand re-theme; Servers list
  and Server Detail; console, logs, files, and mods tabs; backups and settings
  pages; design alignment and visual regression testing (`test/e2e/design-vs-browser`)
  against Pencil baselines (#348, #349, #364, #371, #374, #378).

### Fixed

- **api:** SQLite DSN now automatically includes a `_pragma=busy_timeout(5000)` pragma if one is not already specified, allowing concurrent processes (API and bootstrap-admin) sharing the same database file to wait for locks instead of failing with SQLITE_BUSY immediately.
- **agent, chart:** the agent now serves Prometheus metrics on a separate,
  unauthenticated listener (`:9090`, `--metrics-addr`) instead of the mTLS
  control port (`:8090`) it used to share `/metrics` with — every agent
  scrape target was previously "down" because the control port requires a
  client cert for every route (F-216). The operator declares that listener
  as a named `metrics` containerPort (9090) on the agent sidecar, and the
  `PodMonitor` (`serviceMonitors.enabled`) now scrapes it by that name
  instead of needing a Prometheus client cert. The
  telemetry-receiver now gets its own `ServiceMonitor`, and a new
  `serviceMonitors.scrapeNamespaceSelector` value opens the `NetworkPolicy`
  exceptions (agent metrics port `9090` and the receiver's port) a
  Prometheus outside the release namespace needs to reach either target
  when `networkPolicies.enabled` is also `true` (F-217).
- **api:** the experimental PostgreSQL backend (`db.driver=postgres`, an api
  image built with `-tags postgres`) now works end to end. It has its own
  migration set, queries are rebound to Postgres placeholders, and a CI job
  runs the database tests against PostgreSQL. SQLite installs are unchanged.
  New migrations must now be portable SQL that runs on both databases
  (#519, closes #518).
- **web, api:** the Servers page now lists servers from every namespace the
  viewer may read, not only `gameplane-games`, and the namespace filter
  offers each of them. A new `GET /namespaces` route returns those
  namespaces (#498).
- **web:** catalog tag filters now intersect instead of union (AND logic), so
  selecting multiple tags shows only games matching all tags (#411).
- **web:** vertical tab list no longer renders as an oval shape (#410).
- **web:** console terminal no longer grows without bound horizontally when
  receiving long lines (#409).
- **operator:** migrated off the deprecated Fulcio certificate API (`fulcioroots.pb.go`),
  which was sunset by sigstore; image signing now uses the current `tlog.sigstore.dev` endpoint (#287).
- **chart:** the games `Namespace` now carries `helm.sh/resource-policy:
  keep`, so `helm uninstall` no longer deletes it together with every
  GameServer, data PVC and per-server Secret inside it (F-212).
- **chart:** `helm upgrade` now works for releases not named `gameplane`
  (the pre-upgrade hook targeted the wrong API Deployment) and with
  `--reuse-values` from a v0.2.0-beta.8 release (values keys added since
  then no longer break rendering), and a `helm install` over CRDs left by an
  uninstalled release now updates their schema (F-213, F-214, F-218).
- **api:** hand-written validation errors in the destinations and modules
  handlers now return 400 (bad input) or 409 (name clash) instead of 500, a
  missing share link returns 404, `201 Created` responses carry a JSON
  `Content-Type`, legacy preference timestamps are returned as RFC 3339, and
  `api/specs.md` matches the handlers (F-076, F-077, F-082, F-083, F-085,
  F-086, F-087, F-088, F-089).
- **gameaction:** a required console-action parameter sent as an empty
  string is now rejected (400) instead of silently taking its declared
  default, and the 512-character cap now counts characters rather than
  bytes (F-149, F-150, F-151).
- **api:** file, log and capture downloads and `files/write` and
  `mods/upload` uploads are now exempt from the 60 s request timeout and the
  1 MiB body-size cap that cut them off (F-074, F-075).
- **operator:** a data wipe that fails (for example on a permission error)
  now fails its Job and sets a `DataWipe=False` condition on the GameServer
  instead of being acknowledged as successful (F-054).
- **operator:** fixed the backup/restore quiesce lifecycle: a failed
  unquiesce is retried, scheduled backups quiesce by default again, deleting
  a quiesced Backup waits (bounded) for the unquiesce, and a restore now
  removes files created after the snapshot so `/data` matches it exactly
  (F-044, F-045, F-048, F-049).
- **operator:** a pinned `Module` no longer flaps between Pulling and Ready,
  a managed `GameTemplate` deleted with kubectl is recreated, and a
  GameServer's `spec.templateRef` is now immutable (the API returns 409)
  instead of wedging the StatefulSet reconcile (F-046, F-047, F-050).
- **operator:** `Module` resources in a `Failed` phase no longer rewrite
  their status on every reconcile (F-258).
- **web:** the server detail page (mods, backups, schedules, restores, quick
  actions, log download, clone) now passes the server's namespace, so
  servers outside the default games namespace no longer 404 or hit a
  same-named server there; "Shared with you" rows regain Start/Stop/Restart
  (F-116, F-130).
- **web:** errors on previously-silent mutations and unhandled promises are
  now surfaced to the user instead of failing quietly (F-126, F-137).
- **web:** closing a WebSocket now cancels a pending reconnect timer, so no
  socket is reopened after the component unmounts (F-121).
- **web:** URL safe mode (`?safe-mode=1`) now stays active across in-app
  navigation; a full reload without the parameter still turns it off
  (F-125).
- **agent, web:** an undeterminable player count is now always reported as
  unknown (previously sometimes 0) and shown as "—" instead of `-1`, and the
  agent no longer reports the game id as the game version (F-105, F-106).
- **agent:** console and log WebSockets and mod installs are no longer cut
  off at 30 s by the router timeout, and the RCON reply cap was raised from
  4096 to 16394 bytes so full multi-byte replies are accepted (F-103,
  F-104).
- **tunnel:** fixed a retry-counter overflow that made the supervisor retry
  with no backoff, and frp now proxies to the Service's own port and
  protocol, so UDP games and a remote port different from the Service port
  work (F-172, F-052).
- **tunnel:** the address playit assigns is now read from playitd and
  reported into the GameServer's `status.endpoints`, which previously stayed
  empty for playit tunnels (F-174).
- **sentinel:** a connection handed through to the game pod is no longer cut
  when the game pod becomes Ready and the sentinel shuts down; it drains
  instead. A failed listener now exits the sentinel at once instead of
  waiting for shutdown (F-179, F-180).
- **mcp-server:** `get_pod_logs` now keeps the newest tail bytes when a log
  is truncated and prefixes a truncation notice, instead of silently
  returning the oldest part (F-204).
- **api, operator:** downloading a user-stopped network capture now waits for
  the sidecar to finalize it instead of failing with 409, and a stopped
  capture that never reached a sidecar completes instead of staying Pending
  and holding the capture lock (F-259).
- **api, capture-sidecar:** deleting a capture now frees its file from the
  capture volume budget immediately instead of at pod restart, and a
  rejected concurrent start's 409 names the running capture (F-192, F-261).
- **gp-module:** the validator used by the CLI and the dashboard's module
  builder now checks the real CRD shape (`enum`, the `bool` type, required
  `spec.displayName`, `spec.version` and `ports[].name`), `preview` applies
  the default version, it warns when `gameplaneMinVersion` is newer than the
  tool, and its docs match the CLI (F-159, F-160, F-161, F-162, F-163,
  F-164, F-165, F-166, F-167, F-168).
- **web:** the dashboard's nginx now accepts request bodies up to 64 MiB,
  matching the ingress and API, so file and mod uploads over 1 MiB are no
  longer rejected with 413 (#426).
- **chart, operator:** Backup and Restore Jobs can now reach their restic
  repository when `networkPolicies.enabled` is `true`; a new
  `networkPolicies.backupEgress` value (on by default, TCP 443 and 22) opens
  that egress (#432).
- **release:** Go component images are now cross-compiled instead of built
  under emulation, so the multi-arch release build no longer times out
  (#435).
- **api, chart:** the Add node join command and the downloaded kubeconfig
  can now use a node-routable API server address set with the new
  `clusterOps.externalAddress` value, instead of the in-cluster ClusterIP
  (#464).
- **chart:** OIDC flags are now quoted, so issuer, client ID and role-mapping
  group names containing YAML-special characters render correctly, and the
  new `api.oidc.displayName` value sets the OIDC login button label (#485).
- **web:** Admin Logs now shows the timestamp of log lines that use a `time`
  field, the audit log labels module-source changes correctly, and the ban
  confirmation reads "Banning…" (#480).
- **operator:** an unparseable `spec.networking.address` now sets an
  `InvalidAddress` reason, an unparseable wake window sets an
  `IdleScheduleInvalid` condition whether or not the server is asleep, a
  stale `TunnelHostnameIgnored` condition is cleared, and an expired capture
  whose sidecar is unreachable is deleted after a bounded retry instead of
  lingering (#490).
- **mcp-server:** `list_events` now returns an error for an invalid label
  selector instead of silently ignoring it, and the tool descriptions and
  `propose_fix` backup advice use labels and names that actually exist
  (#493).

### Changed

- **Dependency updates:** Go modules (direct + indirect), web npm packages
  (React, Vite, Playwright, TanStack Router/Query, Tailwind, HeroUI, Lucide,
  Vitest, TypeScript ESLint), GitHub Actions, and container base images
  (Node 24→26 bookworm-slim, Go 1.26→1.27 alpine, nginx 1.30→1.31, restic
  0.18→0.19) bumped to latest stable releases; actionlint and zizmor pinned
  for workflow linting (#262, #263, #264, #265, #266, #267, #268, #270, #271,
  #273, #274, #275, #276, #277, #278, #279, #280, #281, #283, #298, #301, #307,
  #308, #309, #310, #311, #312, #317, #318, #320, #321, #323, #324, #328, #329,
  #334, #344, #354, #355, #356, #357, #358, #359, #379, #380, #381, #382, #387,
  #399, #400, #401, #402, #403, #404, #405, #406, #412).
- **deps:** further web npm patch updates and a refreshed Alpine base image
  digest for the tunnel images (#456, #457).
- **docs:** corrected drift found by the pre-release audit across the API
  reference (OpenAPI), agent, tunnel, svcutil, module-authoring, dependency
  and security docs (#442, #465, #468, #481, #486, #494, #495).

### Security hardening

- **api:** hardened multi-cluster request scoping.
- **operator:** hardened module bundle integrity checks.
- **web:** hardened the Admin Settings draft and managed-secret lifecycle.
- **api:** hardened ownership checks on owner-only server operations.
- **operator:** hardened network captures to apply the template-port default filter when none is given.
- **api:** hardened account removal and share-link revocation.
- **api:** hardened client IP derivation behind trusted proxies.
- **api:** hardened OIDC role re-evaluation, role-assignment auditing for every OIDC provider, and the `bootstrap-admin` break-glass reset.
- **api:** hardened role-edit guards and event-stream authorization.
- **api:** hardened cluster registration removal.
- **operator:** hardened ownership checks on restore and cleanup.
- **api:** hardened audit export fields by including reason in webhook, S3 and CSV output.
- **ci:** hardened the release signing order and the scope of the signing key.
- **api:** hardened Prometheus metrics serving with a dedicated in-cluster listener.
- **agent:** hardened file write and delete handling in the files endpoints.
- **capture-sidecar:** hardened TLS configuration defaults and capture size limits.
- **audit-syslog-bridge:** hardened `APP-NAME`/`HOSTNAME` field validation.
- **gameproto:** hardened handshake string-length and status-response validation.
- **agent:** hardened RCON error handling, TLS certificate validation, and dotfile access in file operations.
- **netguard:** hardened SSRF dial-guard policies to block cloud metadata addresses and refine reserved ranges.
- **operator:** hardened RBAC scope with cluster-wide reads and namespace-scoped writes.
- **chart:** hardened agent mTLS Secret configuration to support bring-your-own CA paths; referenced Secrets that do not exist now fail live installs or upgrades, and the API and operator mTLS checksum now covers custom Secret data to roll both on Secret rotation.
- **agent:** confined the final upload path to the data root.
- **web:** hardened dashboard permission gates for capture and mod controls.
- **operator, api, web:** hardened the module "verified" badge to reflect a recorded signature check instead of a source's current policy.
- **tunnel:** hardened relay config rendering with escaping and validation.
- **audit-syslog-bridge:** hardened collector delivery reporting and intake time bounds.
- **api:** hardened tunnel credential storage when the tunnel provider changes.
- **api:** hardened module archive extraction limits and mod-registry outbound connections.
- **deploy:** hardened the kind dev cluster bootstrap by pinning the ingress-nginx manifest to a release tag.
- **gp-module:** hardened the steamcmd preset so scaffolded templates run the game as a non-root user.
- **docs:** hardened the accuracy of the game-container posture and OIDC group-claim documentation.
- **telemetry-receiver:** hardened ingest payload validation.
- **gameaction:** hardened console action parameter validation against shell and RCON metacharacters.
- **operator:** hardened tunnel credential handling: a credentials Secret not owned by its GameServer is now refused, the tunnel is scaled to zero, and TunnelReady=False (reason TunnelCredentialRefused) reports why.

## [0.3.0-rc.2] — not tagged

The second release candidate for v0.3.0 was not tagged separately; its changes
shipped in [0.3.0](#030--2026-10-05). It covers the changes merged since
v0.3.0-rc.1, mostly fixes from the v0.3 pre-release audit. They are listed
under [0.3.0](#030--2026-10-05) above, which also holds the rc.1 changes;
the Highlights below summarize what is new since rc.1.

### Highlights

- **Agent Prometheus metrics move to a dedicated listener (port 9090):** the
  agent now serves `/metrics` on its own unauthenticated `:9090` listener
  instead of the mTLS control port, where every agent scrape target was
  reported "down"; the chart's `PodMonitor` scrapes it by name (#476).
- **Owner-only server operations now need the owner or an admin:** see
  Upgrade Notes above.
- **Helm chart fixes:** `helm upgrade` works for releases not named
  `gameplane` and with `--reuse-values` from v0.2.0-beta.8, a reinstall over
  leftover CRDs updates them (#443), and `helm uninstall` keeps the games
  Namespace and the GameServers in it (#425).
- **Playit tunnels:** the tunnel NetworkPolicy now lets playit reach its
  relay (#488), and the playit-assigned address shows up in the
  GameServer's status (#447).
- **API fixes:** validation errors return 400/409 instead of 500 (#477), a
  required console-action parameter sent empty is rejected instead of taking
  its default (#491), and large file, log and capture transfers are no longer
  cut off by the 60 s timeout and 1 MiB body cap (#444).
- **Backup, restore and wipe:** a failed data wipe is reported instead of
  acknowledged (#436), and backup/restore quiesce-lifecycle fixes stop a
  server being left quiesced; a restore now also removes files created
  after the snapshot (#454).
- **Uploads and backups:** dashboard file and mod uploads over 1 MiB are no
  longer rejected (#426), and backup and restore Jobs can reach their
  repository when NetworkPolicies are enabled (#432).
- Further security hardening across the API, operator, agent and release
  pipeline (see Security hardening above).

## [0.3.0-rc.1] — 2026-09-23

The first release candidate for v0.3.0, the first Gameplane release without a
beta suffix. It contains the changes made since v0.2.0-beta.8 up to
2026-09-23; the entries under 0.3.0 above that come from later PRs
(the pre-release audit fixes) are in 0.3.0, not in this release.
Release candidates are for testing against diverse workloads and deployment
topologies before the v1-ready v0.3.0 GA; pre-releases are
published and listed in the GitHub releases page.

### Highlights

- **Every existing GameServer pod restarts once on upgrade** due to the network
  capture feature adding an emptyDir volume; plan your upgrade window accordingly.
- **User theme customization:** presets (Modern Pink, Legacy Orange) + custom
  accent/surface colors with WCAG AA contrast guarantee, custom CSS overlay, and
  safe-mode suspension on demand. Accessed via Settings → Theme & Appearance.
- **13 new top-steam-game modules** (FiveM, Farming Simulator 25, Euro Truck
  Simulator 2, Mount & Blade Bannerlord, tModLoader, Team Fortress 2, BeamMP,
  Left 4 Dead 2, The Isle, ARK, Arma Reforger, Hell Let Loose, Squad) with
  Gameplane-owned container images (`fivem`, `farming-simulator-25`,
  `euro-truck-simulator-2`, `beammp`).
- **Network capture for protocol reverse-engineering:** admin-only, on-demand
  packet capture from running game pods as ephemeral sidecar containers,
  downloaded as PCAPNG files (Wireshark-compatible).
- **Easy module building toolkit:** `gp-module` CLI (`init`, `validate`, `preview`,
  `package`) for scaffolding, authoring, and publishing custom game modules;
  web dashboard UI with live validation and cluster installation.
- **Install-time configuration:** Helm-seeded OIDC role mappings (no
  `bootstrap-admin` needed for OIDC-only installs) and default StorageClass for
  game-data PVCs.
- **Share link expiry options:** no-expiry, 15/30/60/90-day presets, or custom
  date; lifts the old 90-day cap that outlived long-running survival servers.
- **Console fixes:** non-default port handling (port-forward scenarios) and
  guarded `send()` before socket open.
- **Existing PVC support:** Helm `api.storage.existingClaim` lets installs
  point SQLite to a pre-existing PersistentVolumeClaim.

## [0.2.0-beta.8] — 2026-08-22

A dependency sweep: all Go modules bumped to latest available versions,
web dependencies updated, and CI image pins aligned to current stable releases.

### Added

- **Wake-on-connect for idle auto-sleep:** a sentinel component (distroless Docker
  image, configured via `operator.sentinelImage`) holds advertised ports while a GameServer
  is asleep and wakes it on a genuine connection attempt. Opt-in per server via
  `spec.idle.wakeOnConnect` (default false). New CRD fields:
  `GameServer.spec.idle.wakeOnConnect` (bool) and `GamePort.wakeProtocol`
  (enum: `minecraft|terraria|generic|none`, default `generic`). New shared
  module `gameproto/` for Minecraft and Terraria handshake parsing; UDP-only
  games use a generic packets-in-window heuristic. Works across all four expose
  modes; Hostport has asymmetric limitations documented in `docs/roadmap.md`.
  Costs one small pod per sleeping server.
- **Load-balancer address pools:** GameServers can now request a specific address
  pool or explicit public IP when exposed as LoadBalancer services, via optional
  `spec.networking.addressPool` and `spec.networking.address` CRD fields. The
  operator translates these to the cluster's load-balancer flavor (configured
  via `operator.addressManager` Helm value: `metallb` | `cilium` | `none`,
  default `none`): MetalLB gets pool → `metallb.io/address-pool` annotation and
  address → `metallb.io/loadBalancerIPs`; Cilium gets pool →
  `gameplane.local/lb-pool` label (admin mirrors it in
  `CiliumLoadBalancerIPPool.spec.serviceSelector`) and address →
  `lbipam.cilium.io/ips`. A new `AddressAssignment` status condition tracks the
  assignment outcome with eight reason codes (Assigned, AssignmentPending,
  PoolNotFound, AllocationFailed, AddressInUse, ServiceNotReady,
  IgnoredForExposureMode, NoAddressManagerConfigured). Dashboard support added
  for setting and viewing pool/address. See [`docs/networking.md`](docs/networking.md).

### Security

- **Signing key rotated:** cosign key type changed from Ed25519 to ECDSA P-256
  (cross-signed for trust continuity; see `docs/key-rotation.md`).
- **Transparency logging enabled:** all published images, the Helm chart (now
  signed for the first time), and official module bundles are signed and
  recorded in the public Sigstore Rekor transparency log. Signing is now
  fail-closed in CI (missing `COSIGN_PRIVATE_KEY` fails the release).

### Changed

- **Go dependencies:** telemetry-receiver, sentinel, and mcp-server updated
  to latest go-openapi versions (`github.com/go-openapi/{jsonpointer,jsonreference,swag}`
  v0.2x → v1.x); operator updated with 50+ indirect dependency bumps (go-git
  v5.19.1 → v5.19.2); api's `modernc.org/sqlite` v1.54.0 → v1.55.0; agent and
  e2e test suite indirect dependencies (k8s.io, golang.org/x packages) brought
  current.
- **Web dependencies:** `@tanstack/react-virtual` v3.14.8 → v3.14.9,
  `lucide-react` v1.26.0 → v1.27.0, `@types/node` v26.1.1 → v26.1.2,
  `globals` v17.7.0 → v17.8.0, `jsdom` v29.1.1 → v30.0.1, `postcss` v8.5.23 →
  v8.5.25.
- **CI image pins:** `setup-envtest` release-0.19 → release-0.24, Kubernetes
  (envtest) 1.31.0 → 1.36.2 (now in sync with the Makefile), and `kind` v0.24.0 →
  v0.32.0.
- **Container image defaults:** restic/restic 0.18.1 → 0.19.1 (charts/gameplane/values.yaml operator.resticImage default) and nginxinc/nginx-unprivileged 1.30-alpine → 1.31-alpine (web/Dockerfile base image).
- **`modules/build.sh`** gained `--tlog-upload` flag (passed by the release
  pipeline for official bundles); default stays offline for air-gapped authoring.
- **Module bundles** are now signed by the pushed manifest digest. `build.sh`
  previously took the first `sha256:` token in `oras push` output, which is not
  necessarily the manifest — a layer digest printed in full would have been
  signed instead (operator verification unchanged).

### Notes

- **TypeScript held at 6.x:** `@typescript-eslint/parser@8.65.0` declares
  `peerDependencies: typescript >=4.8.4 <6.1.0`, so upgrading to TS 7.x would
  break type-aware linting. Reevaluate when `@typescript-eslint/parser` publishes
  a v8.x major that supports TS 7+.
- **ESLint held at 9.x:** `eslint-plugin-react@7.37.5` declares no `^10` peer
  range, so upgrading to ESLint 10.x would break React linting. Reevaluate when
  `eslint-plugin-react` publishes a v8+ major or a 7.x patch that widens the
  peer range.
- **Prometheus client_golang:** `telemetry-receiver`'s
  `github.com/prometheus/client_golang` v1.24.1 is at latest; no update
  available.

## [0.2.0-beta.7] — 2026-07-18

A stabilization release: a full audit of code, the live dashboard, the API, and
the docs ahead of wider external testing. Highlights below.

### Security

- **api:** the WebSocket console/logs/files/players/actions/status/mods routes
  and the mod-id / mod-update handlers now **reject a `?cluster=` selector that
  isn't the local cluster** (404). These handlers only ever act on the control
  plane's own cluster, but RBAC authorized the namespaced permission against the
  caller-supplied `?cluster=` — so a user bound only to a *registered remote*
  cluster could pass `?cluster=<remote>` to satisfy RBAC and reach same-named
  **local** servers (RCON/PTY console, mod writes, log/file reads).
  Single-cluster installs were unaffected.
- **operator:** `Expose: Hostport` now binds only *advertised* template ports to
  the node — non-advertised admin ports (RCON/query/telnet consoles) are no
  longer exposed on the node IP, matching the Service and NetworkPolicy filters.

### Fixed

- **api:** the long-lived SSE `/events` stream and the WebSocket routes are no
  longer force-closed after 60 s by the global request-timeout middleware (which
  also spammed `superfluous response.WriteHeader` into the API log every
  minute). The 60 s deadline still applies to every normal request/response
  route.
- **api:** `/modules/catalog` never emits `"sources": null` for an
  installed-but-uncatalogued module (it serializes `[]`), which previously
  crashed the entire Modules page at render.
- **web:** a `Failed` module can now be uninstalled — its Uninstall button was
  disabled, trapping the user with a broken install and no way to remove it.
- **agent:** the Source-RCON and telnet console clients gained the auth-failure
  cooldown the other protocol clients already had — a wrong RCON password is no
  longer re-sent on every poller tick (which could self-inflict a Source-engine
  `sv_rcon_maxfailures` ban). Both now wrap the shared `ErrAuth` sentinel, and
  ban/kick reasons reject all control characters, not just CR/LF.
- **operator:** `BackupSchedule.status.lastSuccessfulTime` is now populated from
  the newest successful backup (the dashboard "Last run" and `kubectl` "Last"
  columns were always blank); the `Module` Pending path clears a stale `Pulling`
  condition; `GameServer` `setPhase` stamps `observedGeneration`; and the
  ModuleSource "indexed N of M" status message no longer goes negative for
  git/http/local/upload sources.
- **chart:** the operator (and API) metrics `ServiceMonitor`s now select a
  Service that actually exists **and** carries the matching
  `app.kubernetes.io/name` label — operator metrics, the PrometheusRule alerts,
  and the Grafana dashboard were never scraped before.
- **web:** per-node pod counts show `—` instead of a fake `0/<cap>` when the
  count is unavailable (matching CPU/memory), plus assorted accessibility labels
  and a fix to the cluster page's developer-facing empty-state copy.

### Changed

- **chart:** the `defaultModuleSource` template fallback is now `oci` (matching
  the shipped `values.yaml` default) rather than `git`.

### Docs

- Refreshed stale references: the current version, the 8th (`Cluster`) CRD, the
  RBAC database tables, the OCI default module source, the `telemetry-receiver`
  and `mcp-server` components, the full `ModuleSource` type list, the
  architecture diagram's reconcilers, and the changelog link footer. Clarified
  that SQLite is the production-tested database driver and Postgres is
  experimental (build-tag-gated, work-in-progress).

## [0.2.0-beta.6] — 2026-07-17

### Changed

- **modules:** official Factorio template now uses RCON (via the operator's
  `rcon.passwordFile` pointed at the image's self-generated `config/rconpw`)
  instead of pty-only; Factorio and Palworld templates gained a `players.list`
  capability (version 1.1.0).
- **api/web/chart:** the Admin Settings → Updates **channel selector is now a
  read-only label**. The old select persisted a channel that nothing consumed
  (Gameplane is upgraded via Helm, not an in-app updater); the channel is now
  the chart's informational `updates.channel` value, passed to the API as
  `--update-channel` and served on `GET /cluster/info` as `updateChannel`.
  `PUT /admin/config/updates` is gone (400 unknown section) and any legacy
  `updates` DB row is ignored.
- **operator:** module-declared stop sequences (`capabilities.lifecycle.stop`)
  now run over telnet RCON and over pod-attach to stdin (for `consoleMode:
  pty` games with no RCON), in addition to Source RCON. **CRD change:**
  `GameTemplate.spec.rcon.protocol` first dropped the never-implemented `http`
  value, then re-widened as real clients landed — it is now
  `source;telnet;websocket;battleye;satisfactory;palworld;none` (see Added).
- **modules:** categories are now plural — declare `categories:` as a list
  (a module appears in every category's filter chip), with legacy `category:`
  accepted and normalized. All 16 official modules declare categories explicitly
  instead of the game-slug regex fallback.

### Added

- **agent:** four new remote-console protocols behind the agent's existing
  `Exec` interface, so console / players / quiesce / lifecycle / actions all
  work over them: `websocket` (Rust's Facepunch WebRcon), `battleye` (BattlEye
  RCon — DayZ), `satisfactory` (its HTTPS function API), and `palworld` (its
  REST admin API). The `rcon.protocol` enum is now
  `source;telnet;websocket;battleye;satisfactory;palworld;none`. Rust, DayZ and
  Satisfactory each gain a console (plus a players list where the protocol
  exposes one). Satisfactory's admin password isn't a server-config value the
  operator can inject — it needs a one-time in-game claim, documented in the
  module.
- **modules:** **Palworld migrates off RCON, which upstream deprecated**
  ("scheduled to stop functioning in an upcoming update"). It now speaks the
  REST admin API (`protocol: palworld`, port 8212, Basic auth against the same
  `AdminPassword`), which is strictly better than the RCON it replaces: a real
  players list, and an announce that keeps its spaces — RCON's `Broadcast`
  truncated at the first space. Kick/ban are deliberately not offered: the REST
  API moderates by `userId` while the list yields display names.
- **modules:** rich, docs-verified quick actions for ten games, including the
  first ever for Terraria and Don't Starve Together (over the new stdin
  transport) and Rust (over websocket RCON). Minecraft gains an
  `announce-restart` sequence and drops its whitelist actions — the Players tab
  already owns whitelist. Five games get none, each with the reason recorded in
  its template: Valheim's dedicated server has no stdin command input at all,
  DayZ's BattlEye is too restricted to verify, 7DTD and Garry's Mod images
  can't wire a console password, and Enshrouded has no remote console.
- **actions:** quick actions gain `commands` (a sequence run in order,
  mutually exclusive with `command`), `transport` (`rcon` | `stdin`), and
  `group` (labelled sections on the server detail page). Stdin actions run
  over pod-attach from the API, so a `consoleMode: pty` game (Terraria, Don't
  Starve Together) can carry actions — fire-and-forget, the dashboard shows
  "sent". Param validation (the console-injection guard) moved into a shared
  `gameaction` module imported by both the agent and the API, so neither
  transport is trusted blind.
- **mcp-server:** new optional component — a strictly read-only Model
  Context Protocol (MCP) server for Gameplane clusters. Lets an AI assistant
  list/get the 7 Gameplane CRDs, Pods, and Events, fetch pod logs, and get a
  `propose_fix` suggestion (YAML/kubectl as text) — never create, update,
  patch, delete, or apply. Speaks MCP (JSON-RPC 2.0) over stdio only, no
  network port; off by default via `mcpServer.enabled`. See
  `mcp-server/README.md`.
- **api:** native S3 audit sink — batched NDJSON objects to any S3-compatible
  endpoint (`api.audit.s3.*`), alongside the existing stdout/webhook/syslog
  sinks. Events are buffered and flushed when ANY of: 100 events, 1 MiB, or 5
  seconds elapsed. Works with AWS S3, MinIO, Backblaze, Wasabi, etc. Retries 3
  times on transient errors; watch `gameplane_audit_s3_events_total` for
  delivery health.
- **operator:** GameTemplate `rcon.passwordFile` — point the console agent at
  a game-managed password file inside the data volume (e.g., Factorio's
  `config/rconpw`). Precedence: `passwordSecretRef` > `passwordFile` >
  operator-generated Secret. No Secret is created or injected when using
  `passwordFile` mode; the agent reads the file on every connection.
- **api/web/rbac:** per-GameServer access control — server owners and
  collaborators get enforced per-server access on top of namespace role
  bindings. Collaborators are managed from the new Access section (server
  settings) and shared servers appear under "Shared with you"; destructive
  actions (delete, wipe, transfer, collaborator management) stay owner-only.
  (#77)
- **operator/modules:** configurable players-list command — GameTemplate
  capabilities can set the RCON command and an optional per-line entry regex
  for reading the online player list; both the Players tab and the heartbeat
  player counts honor it. (#76)
- **agent/mods:** Factorio mod-portal credentials — mod registry providers
  accept a `credentialsSecretRef` (Secret with `username`/`token` in the
  GameServer's namespace); the agent injects credentials for
  mods.factorio.com downloads, with URL redaction keeping the token out of
  logs. (#78)
- **observability:** log levels are now configurable everywhere — the API and
  agent accept `--log-level`/`GAMEPLANE_LOG_LEVEL` (debug|info|warn|error;
  unknown values degrade to info), the chart wires `api.logLevel` and the
  previously-dead `operator.logLevel` (zap: debug|info|error), and a new
  `operator.agentLogLevel` injects the level into agent sidecars (left empty
  it injects nothing, so upgrades don't roll every game pod).
- **auth:** OIDC providers can now map IdP groups to dashboard roles —
  per-provider extra scopes, a configurable groups claim, group→role
  mappings with most-privileged-wins, a default role (including `deny` to
  refuse unmatched logins), and role re-sync on every login when mappings
  are configured (the IdP becomes the source of truth; providers without
  mappings keep today's viewer-then-promote behavior).
- **api/web:** new admin-only **System Logs** viewer — `GET
  /admin/system-logs/{component}` streams the operator or API pod's logs
  (tail + optional 50s follow window; fixed namespace and label selector
  only), and the new dashboard page (Admin → System logs) renders the
  stream in a scrollback panel with component/tail/follow controls, a pod
  badge, stick-to-bottom auto-scroll, and a download button.
- **operator/modules:** game memory settings can now **size themselves to
  the container memory limit** — a configSchema field may declare
  `autoFromMemoryLimit: {percent}`, and when the user leaves it empty the
  operator computes percent% of the server's effective memory limit
  (e.g. 8Gi × 75% → `6144M`), re-tracking the limit whenever the server
  is resized. The minecraft-java module (2.7.0) adopts it for
  `MAX_MEMORY`/`INIT_MEMORY`: JVM heaps now follow the wizard's memory
  slider instead of a static `2G` default, ending both the OOM kills
  from heaps set at/above the limit and the RAM wasted below it.
  Explicit values still win, and the bundle's `gameplaneMinVersion`
  keeps older operators from silently ignoring the field.
- **telemetry:** a home for the anonymous-usage reports — the new standalone
  **`telemetry-receiver`** component (own Go module + image, like the
  audit-syslog bridge) accepts the API's daily `{version, servers,
  templates}` POST on `/ingest`, logs it structurally, and exposes
  aggregate Prometheus metrics (`gameplane_telemetry_reports_total` by
  version, fleet-size histograms). The chart can deploy it with
  `api.telemetry.receiver.enabled=true` (the API is auto-pointed at it),
  or aim the API at an external receiver via `api.telemetry.endpoint`;
  an optional shared ingest token (`api.telemetry.authSecretRef`) guards
  `/ingest` and rides the API's new `GAMEPLANE_TELEMETRY_AUTH` env.
  Previously the Admin Settings toggle worked but there was nowhere for
  the data to go.
- **auth:** identity providers are now **managed from the dashboard and
  applied live** — Admin Settings → Authentication gained an "Add
  provider" form (generic OIDC, Google preset, GitHub-via-bridge preset):
  issuer + client id go in the auth config, the client secret lands in an
  API-managed Secret (`gameplane-auth-<name>`), and the new
  `/auth/oidc/{provider}/start|callback` routes resolve providers through
  a registry that re-reads the config per request — a saved provider
  works without an API restart, and several OIDC providers can coexist.
  The login page renders one button per enabled provider and hides the
  password form when local login is disabled; disabling local now
  actually gates `/auth/login` (neutral 403). Helm-flag OIDC keeps
  working untouched as the read-only `helm` provider on its legacy
  routes (and its callback now links accounts correctly — the store was
  never attached before, so every Helm-flag OIDC login 500'd). Break-
  glass for a lockout: `bootstrap-admin --enable-local-login`.

- **e2e:** a **Terraria protocol bot** joins the Minecraft one — the opt-in
  bot bucket now also boots a real Terraria server (the same
  passivelemon/terraria-docker image the shipped module uses), completes
  the ConnectRequest → ContinueConnecting handshake with a bespoke minimal
  protocol client (`test/e2e/internal/terrabot`, self-correcting on
  version-mismatch kicks), and asserts the server answers a world-data
  request — proving the module template boots a genuinely joinable server.
  Valheim/Palworld (steamcmd, proprietary UDP) and Factorio (UDP-only, no
  control channel) remain out of scope for CI bots.
- **notifications:** a new **ntfy** sink kind — POSTs each event to an ntfy
  topic URL (ntfy.sh or self-hosted) with the headline in the `Title`
  header, `Priority: high` on failures, and an optional access token.
- **notifications:** the **Add sink form now takes the credential value
  directly** (webhook URL, ntfy topic + token, SMTP settings) instead of
  requiring a pre-created labelled Secret referenced by name. The API
  stores the value as a Secret named `gameplane-notify-<sink>` (labelled
  `gameplane.local/notification-sink=true` +
  `gameplane.local/managed-by=gameplane-api`) and wires the sink's
  `configRef` automatically; deleting the sink cleans the Secret up.
  Secrets created with kubectl/GitOps keep working and are never deleted
  through the dashboard. The cryptic "Name (DNS label)" help text is gone
  too.

- **mods:** direct **file upload** — a third "Upload file" mode on the Mods
  install page sends a local mod straight to the server (multipart to the
  agent, drop zone + progress in the dialog). The agent applies the same
  name/extension/size checks as URL installs and unpacks archives for
  extract-mode loaders; uploads are recorded as provider `upload` in the
  install manifest and are never update-checked. Because an upload carries
  no SSRF risk, it works even for modules without a download allowlist
  (e.g. installing locally built `.pak` files on a Palworld server).

- **web:** servers can now **switch game version after creation** — a new
  "Version" section in Server Settings (shown when the template declares a
  version catalog) offers the same version+loader picker as the Create
  wizard, with a default badge, a callout explaining that each loader keeps
  its own preserved mod volume, and an image-override warning with one-click
  clear. Saving restarts the server on the new version; the operator side
  (image/env swap, per-loader mod-PVC switching, unknown-id failure) was
  already fully wired and is now also covered by an end-to-end
  version-switch test against a live cluster.
- **web:** the Mods tab understands mod provenance — managed mods show a
  provider + version badge (from the agent's install manifest) while files
  placed outside the panel read "unmanaged"; a **Check updates** button runs
  the batch update check and surfaces per-mod "x.y.z available" pills with
  one-click **Update** (in-place upgrade via `replaces`) and **Update all**;
  registry installs now record their provenance so new installs are managed
  from day one; and credential-gated registry files (Factorio portal) hand
  off to the From-URL form prefilled instead of a one-click install that
  would fail.

- **agent/mods:** installed mods now carry an **install manifest**. Each mod
  volume keeps a hidden `.gameplane-mods.json` ledger recording where every
  panel-installed mod came from (registry provider, project, version, source
  URL, install time); `GET /mods` merges it into listings (`meta` per mod,
  `null` for files placed out-of-band), and `POST /mods/install` accepts
  optional `meta` (registry identity to record) and `replaces` (an existing
  mod to swap out atomically — the new file lands first, so a crash can only
  ever leave a duplicate, not a missing mod). A corrupt manifest degrades to
  "unmanaged" listings and self-heals on the next install/remove. This is the
  foundation for update detection and one-click mod upgrades in the dashboard.
- **api/mods:** batch **mod update detection** — `GET
  /servers/{name}/mods/updates` reads the server's install manifest from the
  agent and checks every managed mod against its registry provider (latest
  release compatible with the active loader + game version), returning the
  update list plus per-mod errors in one call. Results are TTL-cached and
  upstream lookups are concurrency-bounded, so page revisits don't hammer
  Modrinth & co. Upgrades ride the existing install endpoint's `replaces`
  field. A new `api-mods` e2e bucket proves the manifest round-trip
  (install with metadata → listed with metadata → in-place upgrade →
  remove) through the full API → agent → volume stack.
- **registry:** a fifth mod-registry engine — the official **Factorio mod
  portal** (`mods.factorio.com`). Templates can declare
  `capabilities.mods.registry.providers: [{provider: factorio}]` to browse
  and search the portal (keyless, catalog cached with a TTL, filtered by the
  active version's `gameVersion` major token). Portal downloads require the
  player's own factorio.com credentials, which the server must never embed
  in URLs it returns to browsers — files are flagged `requiresAuth: true`
  and the dashboard hands off to the from-URL install form instead of
  one-click install. Groundwork for the upcoming official Factorio module.

- **notifications:** the sinks configured under Admin Settings →
  Notifications now actually **deliver**. The API watches GameServer /
  Backup / Restore status transitions and pushes matching events —
  `server.unhealthy`, `server.recovered`, `backup.failed`,
  `backup.succeeded`, `restore.failed`, `restore.succeeded` — to Discord,
  Slack, SMTP, or generic-webhook sinks (previously the panel persisted
  sinks that nothing read). Sinks grew per-event filters (failures on by
  default) and a `configRef` pointing at a labelled Secret
  (`gameplane.local/notification-sink=true`) holding the webhook URL / SMTP
  credentials; a new `POST /admin/notifications/sinks/{name}/test` endpoint
  test-fires a sink synchronously. Outbound dials are SSRF-guarded
  (`netguard.IsAllowed`), delivery is best-effort with bounded retry, and
  outcomes are counted at `/metrics` as `gameplane_notify_deliveries_total`.
  See [`docs/notifications.md`](docs/notifications.md).

### Fixed

- **operator/chart:** game pods ran a **stale agent** on a floating image tag.
  The operator injects `--agent-image=…:edge` but never set a pull policy, so
  Kubernetes defaulted the agent container to `IfNotPresent` and every game pod
  reused whatever the node had cached — one live pod was running an agent 12
  days old. New agent features (the four protocols above) could never reach a
  game pod, and an unknown protocol silently fell back to Source RCON. The
  agent now inherits the chart's `image.pullPolicy`.
- **chart:** the default module catalog is now **OCI, not git**. A git source is
  single-version by construction (one ref = one `module.yaml` = one version), so
  bumping a module *replaced* its version and every install pinned to the old one
  went `VersionUnavailable`. OCI lists tags, so each release *adds* a version and
  the catalog keeps a real history you can pin and roll back to.
- **api/web:** the cluster meters told two lies — CPU/memory always read **0%**
  because the API never asked metrics-server (it now does, via the aggregated
  API, degrading to "—" where none is installed), and storage read **>100%** on a
  healthy cluster because it divided PV *provisioned* capacity by node
  *ephemeral-storage*. Storage is now presented as provisioned-of-physical with
  overcommit as an explicit state.
- **chart:** Admin → Updates showed `stable` on an `:edge` install; the channel
  now derives from `image.tag` unless set explicitly.

- **web:** file browser (list/read/write/upload/delete) now works for servers
  shared from a non-default namespace — the namespace query param was being
  inserted mid-URL, producing 400s and a silently-empty file tree.
- **web:** servers shared from other namespaces are now **navigable** —
  the detail route and namespace-aware API calls (lifecycle, console, files,
  players, and mods) carry the server's namespace through search params and
  query strings, while backups and schedules remain cluster-scoped. Player
  counts no longer render `"-1 online"` when the maximum is unknown (agent
  metric unavailable); they show just the online count instead.
- **api/web:** the Cluster page no longer presents **"Add node" and
  "Download kubeconfig" as click-to-error dead-ends** on installs where
  cluster operations are off (the default). `GET /cluster/info` now reports
  the `clusterOps` flag, and the dashboard disables both buttons with a hint
  pointing at `clusterOps.enabled` in the Helm values.
- **api/web:** the Authentication admin section can no longer save a config
  with **zero enabled identity providers** — a state that would have locked
  everyone out at their next logout. The API rejects such saves (422,
  "at least one identity provider must stay enabled") and the dashboard
  disables the toggle on the last enabled provider with an explanatory hint.
- **web:** the Create Server wizard no longer produces **unschedulable
  servers**. It set only resource *limits*, so Kubernetes defaulted
  requests to match — a 4-core limit then needed a fully-empty node and sat
  `Pending` forever on any partially-used cluster. The wizard now caps CPU /
  memory at the largest single node's capacity (shown as a hint, enforced on
  the field and the step), and sets an explicit modest CPU **request** below
  the limit (memory is still guaranteed at the limit), so a new server
  schedules onto a node with room. Defaults lowered to 2 cores / 4 GiB /
  20 GiB.
- **web:** raw byte counts in Kubernetes event messages (e.g. "Image size:
  333546371 bytes") are now shown human-readable ("318 MB") on the server
  Overview.

## [0.2.0-beta.5] — 2026-07-03

### Added

- **ci/supply-chain:** the cosign **public key is now published** — committed
  at the repo root as [`cosign.pub`](cosign.pub), exported by CI (job summary +
  artifact on every edge publish), and shipped as a release asset. Both publish
  workflows now **verify each signature right after signing** with the key
  derived from the CI secret, and fail the publish if the committed
  `cosign.pub` drifts from the signing key. README and docs gained
  `cosign verify` instructions (the key is Ed25519; offline/keyed, so
  verification takes `--insecure-ignore-tlog=true`).
- **ci:** tagged releases now get a **GitHub Release page automatically** —
  notes extracted from the tag's CHANGELOG section, prereleases flagged,
  `cosign.pub` attached, idempotent on re-runs. (beta.4's page was backfilled
  by hand.)
- **website:** Gameplane has a public marketing + docs site at
  <https://gameplanepanel.github.io/website/> — features, game
  showcase, an AMP comparison, and the full documentation set, designed
  screen-first in `design.pen` (18 frames) and built with Astro. The source
  lives in the separate
  [`gameplane-website`](https://github.com/GameplanePanel/website)
  repo, mounted here as the `website/` submodule.
- **web:** the last UI-audit parity items — a show/hide toggle on the login
  password field, user emails shown under display names on the Users page,
  port overrides editable in the Create Server wizard's Network step (the CRD
  and Settings tab already supported them), and "Back up now" relocated to a
  header button + dialog on the Backups page, matching the design.

### Changed

- **ci:** the three kind e2e jobs (core / extended / game bot) were collapsed
  into a single job running the same three suites, in the same order, against
  one cluster — they were already fully serialized by the shared concurrency
  group, so the split only cost duplicate image builds, cluster setups, and
  queue slots (~15-20 min per run). Docs-only changes no longer trigger the
  test pipeline at all (`paths-ignore`); no test was removed.

## [0.2.0-beta.4] — 2026-07-01

### Added

- **operator:** fleet metric `gameplane_gameservers{phase=…}` exported on the
  operator's `/metrics` endpoint — one gauge per GameServer lifecycle phase,
  computed from the controller cache at scrape time (so it never drifts from
  reconcile state). The bundled Grafana dashboard gains "GameServers
  running/failed" stats and a stacked "GameServers by phase" graph, and
  `prometheusRules.enabled` gains a `GameplaneGameServerFailed` alert. Until
  now the dashboard and alerts plotted only controller-runtime internals; this
  is the first domain metric.
- **operator:** companion fleet metric `gameplane_backups{phase=…}` (one gauge
  per Backup phase), with a "Backups failed" stat + stacked "Backups by phase"
  graph on the dashboard and a `GameplaneBackupFailed` alert — a silently failed
  backup is a data-loss risk, so it's worth paging on.
- **ci:** the published component images (`:edge` on every `main` push and the
  versioned images on a `v*` release) are now keyed-cosign-signed by digest
  (offline, no Rekor), matching the official module bundles. Verify with
  `cosign verify --key cosign.pub ghcr.io/gameplanepanel/gameplane/<component>`.
  Gated on `COSIGN_PRIVATE_KEY`, so publishing still works before the key is set.
  Signing and publishing run only on `main`/tags, never on PRs.
- **api:** server-side audit-log export — `GET /admin/audit/export?format=csv|json`
  (admin-only) streams the **entire** audit trail, optionally bounded by RFC3339
  `since`/`until`. Unlike the dashboard's client-side CSV (only the loaded page),
  this returns the full log in one download for compliance/archival, streamed
  row-by-row so a large table isn't buffered in memory.
- **api:** outbound **audit-log webhook push sink** — set `api.audit.webhook.url`
  and the API POSTs each audit event as JSON to that endpoint (a log aggregator's
  push API, a SIEM, or your own receiver), alongside the database. It mirrors,
  never gates: events always persist, and a slow or unreachable endpoint never
  blocks or fails a request (bounded async buffer, drop-and-count on overflow,
  surfaced at `gameplane_audit_webhook_events_total{result=sent|failed|dropped}`
  on `/metrics`). An optional `Authorization` header is Secret-sourced
  (`api.audit.webhook.authSecretRef`), never a flag.
- **audit-syslog-bridge:** a new, generic HTTP-JSON → **syslog** relay image that
  sits behind the audit webhook sink, so audit events can be forwarded to a
  syslog/SIEM collector (RFC 5424 over TCP, TCP+TLS, or UDP). Deploy the bundled
  relay with `api.audit.webhook.syslogBridge.enabled=true` +
  `…syslogBridge.syslog.addr`, and the chart auto-points the webhook at it (off
  by default). The bridge forwards the request body verbatim, so it works for
  any JSON webhook source, not just Gameplane.

### Changed

- **chart:** the bundled `defaultModuleSource` now defaults to a **git** source
  that indexes the public `gameplane-module` repository, so the official games
  appear in the Modules page out of the box — no OCI registry to publish first.
  Set `defaultModuleSource.type: oci` to pull (optionally cosign-signed) bundles
  from a registry instead. **Breaking values change:** the former top-level
  `url` / `insecure` / `modules` / `pullSecretName` / `verify` keys moved under
  `defaultModuleSource.oci.*`.
- **chart:** the official module-signing public key (ed25519) now ships in
  `defaultModuleSource.oci.verify.cosignPublicKey`, so verifying official bundles
  is just `type: oci` + `oci.verify.enabled: true` — no key to paste. Still off
  by default.

### Fixed

- **operator:** the Servers list "Node" column was always blank — nothing ever
  set the `gameplane.local/node` annotation the dashboard reads. The GameServer
  reconciler now keeps it in sync with the node the game pod is scheduled on
  (and clears it when the server is stopped), so the column shows real placement.

## [0.2.0-beta.3] — 2026-06-27

### Added

- **chart:** bundled observability — `prometheusRules.enabled` ships a
  `PrometheusRule` of operator alerts (reconcile errors, workqueue backlog,
  stuck reconcile) on the standard controller-runtime metrics, and
  `grafanaDashboards.enabled` ships a Grafana dashboard ConfigMap (reconcile
  rate/errors/latency, workqueue depth) for the Grafana sidecar to auto-import.
  Both off by default, alongside the existing `serviceMonitors`.
- **Signed module bundles.** `modules/build.sh` gains an opt-in `--sign` flag
  that keyed-cosign-signs each pushed bundle by digest (offline; no
  transparency-log upload), and the `release.yaml` workflow gains a `modules`
  job that pushes and signs the official `modules/*` bundles on every `v*` tag.
  The job is gated on a `COSIGN_PRIVATE_KEY` secret, so releases still succeed
  before a signing key is provisioned.
- **chart:** `defaultModuleSource.verify` (`enabled` / `cosignPublicKey`, off by
  default) wires the published signing key into a `gameplane-module-cosign-pub`
  Secret and sets `spec.verify.key` on the default source, so installs can
  refuse any unsigned or wrong-key bundle. See
  [`docs/module-authoring.md`](docs/module-authoring.md#signing-official-bundles).

### Changed

- **BREAKING — CRD API group renamed `gameplane.gg` → `gameplane.local`.** Drops
  the `.gg` TLD; all seven CRDs, their labels/annotations (`gameplane.local/*`),
  and RBAC now use the `gameplane.local` group, and manifests are
  `apiVersion: gameplane.local/v1alpha1`. (A CRD group must be a dotted DNS
  subdomain, so a bare `gameplane` is not valid.) No tagged release ever shipped
  the `.gg` group, so this only affects unreleased/edge installs. Recreate CRDs
  after upgrading (`kubectl apply -f charts/gameplane/crds/`) — existing
  `*.gameplane.gg` custom resources are not migrated automatically.
- **Modules moved to their own repo.** The `modules/` tree (the official game
  template bundles + `build.sh`) now lives in the standalone `gameplane-module`
  repository, vendored back here as a git submodule at the same path. Nothing
  changes at runtime — the operator still pulls bundles from an OCI registry —
  but after cloning you must run `git submodule update --init` (or clone with
  `--recurse-submodules`) before `make dev-up` / `make modules-push`.

## [0.2.0-beta.2] — 2026-06-22

### Changed

- **Renamed the project from Kestrel to Gameplane.** This is a breaking rebrand
  that spans every layer:
  - **CRD API group** `kestrel.gg` → `gameplane.local` (all seven CRDs). Existing
    clusters must be **recreated** — Helm never upgrades CRDs across a group
    change, and there is no in-place migration for `v1alpha1` objects.
  - **OCI module bundles** — media types `application/vnd.kestrel.*` →
    `application/vnd.gameplane.*` and the manifest field `kestrelMinVersion` →
    `gameplaneMinVersion`; existing bundles must be re-pushed
    (`make modules-push`).
  - **Helm chart** `kestrel` → `gameplane`, default namespaces
    `kestrel-system` / `kestrel-games` → `gameplane-system` /
    `gameplane-games`, the image registry, and every `kestrel-*` object name.
  - **Environment variables** `KESTREL_*` → `GAMEPLANE_*`.
  - **Auth cookies and CSRF header** (`kestrel_session`, `kestrel_csrf`,
    `X-Kestrel-CSRF`, …) → `gameplane_*` / `X-Gameplane-CSRF`; active sessions
    are invalidated on upgrade and users must re-login.
  - **Go module path** `github.com/kestrel-gg/kestrel` →
    `github.com/GameplanePanel/gameplane`.
- Synced `web/package.json` to the chart version (it had been left at `0.1.0`).

## [0.2.0-beta.1] — 2026-06-22

First **beta**. The control plane (operator, API, agent) and the dashboard are
feature-complete for the v1 scope and have been stabilized for external
testing. Not yet recommended for unattended production workloads — see
[Beta status & known limitations](README.md#beta-status--known-limitations).

### Highlights (the beta feature surface)

- **GameServers** — create / start / stop / restart / clone / wipe-data, with a
  guided 4-step creation wizard (template → configure → network → review),
  per-server resource limits, node placement, env vars, and a CIDR ingress
  allow-list. Provisioning sub-status surfaces image pull / install / waiting-
  for-agent / crash-loop reasons.
- **Console, logs, files, players** — RCON/PTY console with a command bar and
  resilient reconnect; pod + game-file log views with level filtering; a Monaco
  file editor; and player moderation (kick/ban/unban) plus whitelist management.
- **Backups & restore** — on-demand and scheduled backups (restic-snapshot and
  CSI volume-snapshot strategies), retention policies, and restore-as-new-server.
- **Modules** — game templates distributed as signed OCI bundles, an in-app
  registry browser (Modrinth / CurseForge / Hangar) for mods and modpacks, and
  SSRF-guarded fetching with optional cosign signature verification.
- **RBAC & audit** — admin / operator / viewer roles with a permission catalog,
  local (argon2id) and OIDC login, and a human-readable audit log.
- **Cluster** — node overview with cordon / drain and kubeconfig download.

### Fixed (stabilization for beta)

- **operator:** surface `BackupSchedule` retention-trim failures as a
  `RetentionTrimmed` status condition instead of swallowing them, and persist
  status changes even on dormant/suspended reconciles.
- **operator:** clear a `Module`'s `Pulling` condition on failure so a failed
  module no longer shows "Pulling" and "Failed" at once.
- **api:** `:restart` now reliably recycles the pod — it waits for the
  StatefulSet to drain before resuming, instead of racing the operator's
  reconcile (which left the pod untouched under load).
- **web:** fixed a login password-field accessibility bug (a `<label>` wrapping
  the "Forgot?" button mis-bound the field), a registry browser that collapsed
  expanded results on every search keystroke, and a stale audit-log assertion.
- **web:** added the design's "Audit log" quick-link to the Users header.
- **ci/test:** made the e2e `kubectl port-forward` helper resilient to a loaded
  runner (retry + longer readiness window), eliminating a cascade of flaky
  API e2e failures.

[Unreleased]: https://github.com/GameplanePanel/gameplane/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/GameplanePanel/gameplane/compare/v0.3.0-rc.1...v0.3.0
[0.3.0-rc.2]: https://github.com/GameplanePanel/gameplane/compare/v0.3.0-rc.1...v0.3.0
[0.3.0-rc.1]: https://github.com/GameplanePanel/gameplane/compare/v0.2.0-beta.8...v0.3.0-rc.1
[0.2.0-beta.8]: https://github.com/GameplanePanel/gameplane/compare/v0.2.0-beta.7...v0.2.0-beta.8
[0.2.0-beta.7]: https://github.com/GameplanePanel/gameplane/compare/v0.2.0-beta.6...v0.2.0-beta.7
[0.2.0-beta.6]: https://github.com/GameplanePanel/gameplane/compare/v0.2.0-beta.5...v0.2.0-beta.6
[0.2.0-beta.5]: https://github.com/GameplanePanel/gameplane/compare/v0.2.0-beta.4...v0.2.0-beta.5
[0.2.0-beta.4]: https://github.com/GameplanePanel/gameplane/compare/v0.2.0-beta.3...v0.2.0-beta.4
[0.2.0-beta.3]: https://github.com/GameplanePanel/gameplane/compare/v0.2.0-beta.2...v0.2.0-beta.3
[0.2.0-beta.2]: https://github.com/GameplanePanel/gameplane/compare/v0.2.0-beta.1...v0.2.0-beta.2
[0.2.0-beta.1]: https://github.com/GameplanePanel/gameplane/releases/tag/v0.2.0-beta.1
