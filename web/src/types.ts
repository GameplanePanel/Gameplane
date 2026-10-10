// Minimal TypeScript surface mirroring the Gameplane CRDs. Kept lean —
// we use `unknown` for nested structures the UI doesn't yet render.

export interface ObjectMeta {
  name: string;
  uid?: string;
  namespace?: string;
  creationTimestamp?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
}

// A single user-supplied input for a declared server action.
export interface ActionParamDecl {
  name: string;
  displayName?: string;
  description?: string;
  type: "string" | "int" | "bool" | "enum";
  default?: string;
  enum?: string[];
  required?: boolean;
  // Status metric id whose live value pre-fills this param in the dialog.
  currentFrom?: string;
}

// A module-declared operator action surfaced as a button on the server
// detail page (spec.capabilities.actions[]). `command` is the agent's
// concern; the UI renders displayName/params and POSTs id+params.
export interface ServerActionDecl {
  id: string;
  displayName: string;
  description?: string;
  icon?: string;
  command?: string;
  params?: ActionParamDecl[];
  confirm?: boolean;
  danger?: boolean;
  commands?: string[];
  transport?: "rcon" | "stdin";
  group?: string;
}

// A module-declared live metric (spec.capabilities.status.metrics[]).
export interface StatusMetricDecl {
  id: string;
  displayName: string;
  command?: string;
  regex?: string;
  unit?: string;
}

// Install policy for the mods capability (spec.capabilities.mods.install).
export interface ModInstallPolicy {
  allowedHosts: string[];
  maxSizeMB?: number;
}

// spec.capabilities.mods.loaders[<loader>] — the mods directory for one
// loader/server-type, selected by the active version's loader.
export interface ModLoaderDecl {
  path: string;
  displayName?: string;
  extensions?: string[];
}

// spec.capabilities.mods.registry — selects a built-in external mod
// registry the dashboard can browse/search. When present (and the server's
// game supports it) the Mods tab offers a "Search registry" mode; absent
// means install-by-URL only.
export interface ModRegistryDecl {
  providers: ModProviderDecl[];
}

// The closed set of built-in registry engines (mirrors ModProvider.Provider's
// +kubebuilder:validation:Enum server-side). curseforge, steam, and nexus
// need an admin-configured API key and stay hidden from the Mods browser
// until one is set (see RegistryProviderInfo.available); the rest are keyless.
export type ModRegistryProvider =
  | "modrinth"
  | "thunderstore"
  | "curseforge"
  | "hangar"
  | "factorio"
  | "steam"
  | "spigot"
  | "github"
  | "umod"
  | "nexus";

// One declared registry provider on a template.
export interface ModProviderDecl {
  provider: ModRegistryProvider;
  community?: string;
  // Required (>0) when provider is "steam" — facets Workshop browse/search
  // to this Steam app id. Ignored otherwise.
  steamAppID?: number;
  // Required (>0) when provider is "curseforge" — the numeric CurseForge
  // game this provider searches (e.g. 432 for Minecraft, 83374 for ARK:
  // Survival Ascended). Ignored otherwise.
  curseforgeGameID?: number;
  // Required when provider is "github" — the one repository whose Releases
  // stand in for versions. Ignored otherwise.
  github?: { owner: string; repo: string };
  modpacks?: ModpackDecl;
}

// registry.providers[].modpacks — enables the Modpacks tab for that
// provider. refEnv set = env-mode install (e.g. Minecraft/itzg
// MODRINTH_MODPACK); empty = deps-mode (resolve + install the pack's deps).
export interface ModpackDecl {
  refEnv?: string;
  env?: { name: string; value?: string }[];
}

// GET /servers/{name}/mods/registry/providers — drives the provider switch.
export interface RegistryProviderInfo {
  provider: string;
  available: boolean;
  modpacks: boolean;
}

// spec.capabilities.mods.idList — declares that this game's server
// downloads its own mods given a list of provider-native ids (ARK:
// Survival Ascended's CurseForge ids appended to its launch string,
// Project Zomboid's semicolon-separated MOD_IDS, generic Steam Workshop id
// lists) rather than the agent dropping files into a mods directory. Its
// presence on a template is the dashboard's signal to render the
// id-managed mods editor instead of the file-based install/list UI.
// Mirrors ModIDListSpec (operator/api/v1alpha1/gametemplate_types.go); the
// dashboard only needs to know this exists, not env/separator/format/mode
// (those drive the operator's projection, not the UI).
export interface ModIDListDecl {
  env: string;
  separator?: string;
  format?: string;
  mode?: "replace" | "append";
}

// spec.capabilities.mods — declares the mod directory and (optionally)
// the URL-install policy. A template uses either a single `path` (legacy)
// or a per-loader `loaders` map keyed by GameVersion.loader; install is
// offered only when `install` is set; `registry` enables in-app browse.
// `idList` is mutually exclusive with path/loaders in practice — see
// ModIDListDecl.
export interface ModsCapability {
  path?: string;
  loaders?: Record<string, ModLoaderDecl>;
  extensions?: string[];
  install?: ModInstallPolicy;
  registry?: ModRegistryDecl;
  idList?: ModIDListDecl;
}

// One provider-native mod id + display label — GameServer.spec.mods.ids,
// GET/PUT /servers/{name}/mods/ids. Mirrors api/internal/handlers/
// mod_ids.go's ModID DTO (and operator/api/v1alpha1/gameserver_types.go's
// ModRef). `name` is a display label only, never projected into the game.
export interface ModID {
  id: string;
  name?: string;
}

// spec.capabilities — only the surfaces the dashboard renders are typed;
// players/quiesce are agent-side and left opaque here.
export interface GameCapabilities {
  players?: unknown;
  quiesce?: unknown;
  actions?: ServerActionDecl[];
  status?: { metrics?: StatusMetricDecl[] };
  mods?: ModsCapability;
}

// Registry identity of an installed mod, recorded in the agent's install
// manifest at install time and echoed back in listings. provider "upload"
// marks direct uploads (never update-checked).
export interface ModMeta {
  provider: string;
  projectId?: string;
  projectName?: string;
  versionId?: string;
  versionNumber?: string;
  gameVersion?: string;
  loader?: string;
  sourceUrl?: string;
  installedAt?: string;
}

// One installed mod file, as returned by GET /servers/{name}/mods.
// meta is null/absent for unmanaged files (placed outside the panel).
export interface InstalledMod {
  name: string;
  size: number;
  modTime?: string;
  meta?: ModMeta | null;
}

// One available upgrade from GET /servers/{name}/mods/updates.
export interface ModUpdate {
  name: string;
  provider: string;
  projectId: string;
  projectName?: string;
  installedVersionId: string;
  installedVersionNumber?: string;
  latestVersionId: string;
  latestVersionNumber?: string;
  file: RegistryFile;
}

export interface ModUpdatesResponse {
  checkedAt: string;
  updates: ModUpdate[];
  errors?: { name: string; error: string }[];
}

// One reading from GET /servers/{name}/status (the agent resolves each
// declared metric to a value; empty when the command didn't match).
export interface StatusReading {
  id: string;
  displayName?: string;
  value: string;
  unit?: string;
}

// A Kubernetes probe. Only the timing fields are edited per-server; the
// action (httpGet / tcpSocket / exec) and anything else is carried through
// unchanged via the index signature.
export interface Probe {
  initialDelaySeconds?: number;
  periodSeconds?: number;
  timeoutSeconds?: number;
  failureThreshold?: number;
  successThreshold?: number;
  [key: string]: unknown;
}

export interface ProbeSet {
  readiness?: Probe;
  liveness?: Probe;
  startup?: Probe;
}

export type ProbeKind = "readiness" | "liveness" | "startup";

// One entry in a template's version catalog (spec.versions[]). Selecting it
// (GameServer.spec.version = id) pins the image and, when `loader` keys into
// capabilities.mods.loaders, that loader's per-(version+loader) mod volume.
// `env` is operator-side only and intentionally not surfaced here.
export interface GameVersion {
  id: string;
  displayName: string;
  image?: string;
  loader?: string;
  default?: boolean;
  // Clean upstream version token (e.g. "1.21.4") passed to a mod registry
  // to filter results; distinct from `id` (a Gameplane selector).
  gameVersion?: string;
}

// A search hit from the mod-registry browse endpoint, normalized across
// providers (GET /servers/{name}/mods/registry/search).
export interface RegistryProject {
  id: string;
  slug?: string;
  title: string;
  description?: string;
  author?: string;
  iconUrl?: string;
  downloads?: number;
  pageUrl?: string;
  provider: ModRegistryProvider;
}

// A downloadable artifact of a RegistryVersion; downloadUrl is handed to
// the existing install endpoint. requiresAuth marks files the portal only
// serves with the user's own credentials (e.g. Factorio's username+token
// query params) — the UI offers a from-URL handoff instead of one-click
// install.
export interface RegistryFile {
  filename: string;
  downloadUrl: string;
  size?: number;
  primary?: boolean;
  requiresAuth?: boolean;
}

// One release of a RegistryProject (newest first), already filtered to the
// active loader + game version by the API.
export interface RegistryVersion {
  id: string;
  name?: string;
  versionNumber?: string;
  gameVersions?: string[];
  loaders?: string[];
  files: RegistryFile[];
}

export interface GameTemplate {
  metadata: ObjectMeta;
  spec: {
    displayName: string;
    game: string;
    categories?: string[];
    version: string;
    description?: string;
    icon?: string;
    accentColor?: string;
    image: string;
    versions?: GameVersion[];
    logPath?: string;
    consoleMode?: "rcon" | "pty" | "none";
    // authentication: whether the console requires a password (default true).
    // false means no password Secret is minted (e.g. Nuclear Option's loopback port).
    rcon?: { protocol?: string; port?: number; authentication?: boolean };
    probes?: ProbeSet;
    capabilities?: GameCapabilities;
    configSchema?: Array<{
      name: string;
      displayName?: string;
      description?: string;
      type: "string" | "int" | "bool" | "enum" | "password";
      default?: string;
      enum?: string[];
      required?: boolean;
      target?: "env" | "file";
      autoFromMemoryLimit?: { percent: number };
      // Bounds mirror the CRD ConfigField: min/max apply to int fields,
      // minLength/maxLength to string and password fields (bytes).
      min?: number;
      max?: number;
      minLength?: number;
      maxLength?: number;
    }>;
  };
  status?: { inUseCount?: number };
}

export type GameServerPhase =
  | "Pending" | "Starting" | "Running" | "Stopping" | "Stopped" | "Suspended" | "Failed";

export type Expose = "ClusterIP" | "NodePort" | "LoadBalancer" | "Hostport";

export interface SecretKeyRef {
  name: string;
  key: string;
  optional?: boolean;
}

export interface EnvVar {
  name: string;
  value?: string;
  valueFrom?: {
    secretKeyRef?: SecretKeyRef;
  };
}

export interface ResourceRequirements {
  requests?: Partial<Record<"cpu" | "memory", string>>;
  limits?: Partial<Record<"cpu" | "memory", string>>;
}

export interface Toleration {
  key?: string;
  operator?: "Exists" | "Equal";
  value?: string;
  effect?: "NoSchedule" | "PreferNoSchedule" | "NoExecute";
  tolerationSeconds?: number;
}

export interface PortOverride {
  name: string;
  servicePort?: number;
  nodePort?: number;
}

export interface RemotePortMapping {
  name: string;
  remotePort: number;
}

export interface FrpTunnelSpec {
  serverAddr: string;
  serverPort?: number;
  remotePorts: RemotePortMapping[];
}

export interface TailscaleTunnelSpec {
  hostname?: string;
  tags?: string[];
}

export interface PlayitTunnelSpec {
  tunnelName?: string;
}

export interface GameServerTunnel {
  enabled?: boolean;
  provider: "frp" | "tailscale" | "playit";
  credentialsSecretRef?: { name: string };
  frp?: FrpTunnelSpec;
  tailscale?: TailscaleTunnelSpec;
  playit?: PlayitTunnelSpec;
}

export interface GameServerNetworking {
  expose?: Expose;
  hostname?: string;
  serviceAnnotations?: Record<string, string>;
  portOverrides?: PortOverride[];
  sourceRanges?: string[];
  // Load-balancer address pool to allocate from; honored only for
  // expose: LoadBalancer, and only when the cluster runs a supported
  // address manager (MetalLB or Cilium LB-IPAM).
  addressPool?: string;
  // Specific IP to request from that pool. Format is validated
  // operator-side, not by the CRD.
  address?: string;
  tunnel?: GameServerTunnel;
}

export interface GameServerStorage {
  size?: string;
  storageClassName?: string;
  mountPath?: string;
}

// Idle auto-sleep configuration (GameServer.spec.idle). Named rather than
// inlined so the settings form can pass a whole draft around without
// restating the shape.
export interface IdleSpec {
  enabled?: boolean;
  // 5–1440, default 30 (bounds mirror the CRD).
  afterMinutes?: number;
  // Five-field cron expressions, max 8.
  wakeWindows?: string[];
  // Whether to arm the wake sentinel while asleep: a small pod holds the
  // server's advertised ports and wakes it when a player joins.
  wakeOnConnect?: boolean;
}

// GameServer.spec.capture — opt-in flag for the network-capture sidecar,
// plus an optional per-server override of the cluster's default retention
// window. Mirrors CaptureConfiguration (data-model.md; operator/api/
// v1alpha1/gameserver_types.go). Disabling does NOT remove an already
// -injected ephemeral capture container — see CaptureStatus.ready below.
export interface CaptureConfiguration {
  enabled?: boolean;
  // Seconds; 1..604800 (cluster max). Omit to use the cluster default (86400).
  retentionSeconds?: number;
}

// GameServer.status.capture — observed state of the capture sidecar for
// this server. Mirrors the API's captureStatusResp (api/internal/handlers/
// capture.go) and the CRD's status.capture.{...}; on the wire
// activeCapture/lastCaptureTime are JSON `null` (not an omitted key) when
// unset, so both are typed nullable rather than optional.
export interface CaptureStatus {
  ready: boolean;
  activeCapture: string | null;
  lastCaptureTime: string | null;
  sidecarRestarts: number;
}

export type CapturePhase = "Pending" | "Running" | "Completed" | "Failed" | "Expired";

// A single packet capture, as returned by the capture-start/-stop/list/get
// endpoints (api/internal/handlers/capture.go's captureStartResp/
// captureStopResp/captureItem/captureGetResp — merged into one shape here
// since the same object is passed through the dashboard's capture widget
// regardless of which endpoint produced it). Not every field is present on
// every response: maxDurationSeconds/maxSizeBytes/ttlSecondsAfterFinished
// are start-only, stoppingReason is stop-only, expiresAt appears once the
// capture has completed. startedAt/completedAt are "" (not null) until set
// — the API's formatOptionalTime never emits null.
export interface NetworkCapture {
  captureId: string;
  phase: CapturePhase;
  serverName: string;
  filter: string;
  maxDurationSeconds?: number;
  maxSizeBytes?: number;
  ttlSecondsAfterFinished?: number;
  createdAt: string;
  startedAt: string;
  completedAt: string;
  bytesWritten: number;
  packetsWritten: number;
  expiresAt?: string;
  stoppingReason?:
    | "user_requested"
    | "max_duration_reached"
    | "max_size_reached"
    | "pod_restarted"
    | "error";
}

// GET /servers/{name}:captures response envelope.
export interface NetworkCaptureList {
  captures: NetworkCapture[];
  total: number;
  limit: number;
  offset: number;
}

export interface InlineBackupPolicy {
  schedule: string;
  repoRef: { name: string; key: string };
  retention?: {
    keepLast?: number;
    keepHourly?: number;
    keepDaily?: number;
    keepWeekly?: number;
    keepMonthly?: number;
    keepYearly?: number;
  };
  suspend?: boolean;
}

export interface GameServerEndpoint {
  name: string;
  host: string;
  port: number;
  protocol?: string;
  // Tailscale endpoints are tailnet-private only, not reachable from the internet.
  private?: boolean;
  tunnelProvider?: string;
  // Address pool the load-balancer address was allocated from; set by the
  // reconciler, never by the user.
  pool?: string;
}

export interface GameServer {
  metadata: ObjectMeta & { resourceVersion?: string };
  spec: {
    templateRef: { name: string };
    suspend?: boolean;
    // Seconds the operator waits for the template's stop sequence to run
    // over RCON before scaling the pod down (soft-stop). Range 0–600,
    // default 30. No effect when the template declares no stop sequence.
    stopGracePeriodSeconds?: number;
    image?: string;
    // Selects a GameTemplate.spec.versions[].id (image + per-loader mod
    // volume). Omit to use the template's default version.
    version?: string;
    config?: Record<string, string>;
    env?: EnvVar[];
    probes?: ProbeSet;
    resources?: ResourceRequirements;
    storage?: GameServerStorage;
    networking?: GameServerNetworking;
    backupPolicy?: InlineBackupPolicy;
    nodeSelector?: Record<string, string>;
    tolerations?: Toleration[];
    affinity?: Record<string, unknown>;
    serviceAccountName?: string;
    // Selected mods for a game whose server installs mods by id (see
    // GameTemplate.spec.capabilities.mods.idList). Absent/undefined for
    // file-drop games.
    mods?: { ids?: ModID[] };
    // Idle auto-sleep. Opt-in: when enabled, the operator scales the server
    // to zero after afterMinutes of zero reported players, and brings it back
    // on a wakeWindows cron tick or an explicit :wake.
    idle?: IdleSpec;
    // Opt-in packet-capture sidecar. See CaptureConfiguration's doc comment
    // for the enable/disable asymmetry (disable does not remove a running
    // ephemeral container).
    capture?: CaptureConfiguration;
  };
  status?: {
    phase?: GameServerPhase;
    endpoints?: GameServerEndpoint[];
    agent?: {
      lastHeartbeat?: string;
      // null/absent means "unknown" (agent couldn't query the game, or
      // the heartbeat is stale and the API blanked it).
      playersOnline?: number | null;
      playersMax?: number;
      gameVersion?: string;
      // Resource usage the agent reads from its own cgroup + a statfs of
      // the data volume. null/absent means "unknown" (unreadable source,
      // or a stale heartbeat the API blanked) — render "—", not a zero.
      cpuMillicores?: number | null;
      cpuLimitMillicores?: number | null;
      memoryBytes?: number | null;
      memoryLimitBytes?: number | null;
      diskUsedBytes?: number | null;
      diskTotalBytes?: number | null;
      // Set by the API when the heartbeat is older than the freshness
      // window — the reported values are no longer current.
      stale?: boolean;
    };
    // Observed state of idle auto-sleep. `asleep` is the operator's own
    // marker and is distinct from spec.suspend, the user's power switch —
    // both surface as phase Suspended, so this field is the only thing that
    // tells an automatic sleep apart from a deliberate stop.
    idle?: {
      emptySince?: string;
      asleep?: boolean;
      asleepSince?: string;
      lastWakeTime?: string;
      // One short phrase from the operator explaining the current state —
      // why idle time is or isn't accruing. Render it as given.
      reason?: string;
    };
    // Ready / Progressing / Healthy. The operator refines Progressing's
    // message while Starting (e.g. "pulling the game image") — the
    // dashboard surfaces it as a provisioning sub-status.
    conditions?: Array<{
      type: string;
      status: string;
      reason?: string;
      message?: string;
      lastTransitionTime?: string;
    }>;
    startedAt?: string;
    // Observed state of the capture sidecar for this server.
    capture?: CaptureStatus;
  };
}

// A Kubernetes Event about a server's pod/StatefulSet/GameServer, as
// returned by GET /servers/{name}/events. Mirrors the API's PodEvent DTO.
export interface ServerEvent {
  id: string;
  time: string;
  type: string; // Normal | Warning
  reason: string;
  message: string;
  source: string;
  object: string;
  count: number;
}

export interface Backup {
  metadata: ObjectMeta;
  // strategy: restic-snapshot writes to a restic repo; volume-snapshot takes a
  // CSI snapshot and is restored by provisioning a NEW server from it.
  spec: { serverRef: { name: string }; strategy?: "restic-snapshot" | "volume-snapshot" };
  status?: {
    phase?: "Pending" | "Running" | "Succeeded" | "Failed";
    startTime?: string;
    completionTime?: string;
    snapshotID?: string;
    size?: string;
  };
}

export interface BackupSchedule {
  metadata: ObjectMeta;
  spec: {
    serverRef: { name: string };
    schedule: string;
    suspend?: boolean;
    retention?: {
      keepLast?: number;
      keepHourly?: number;
      keepDaily?: number;
      keepWeekly?: number;
      keepMonthly?: number;
      keepYearly?: number;
    };
  };
  status?: {
    lastSuccessfulTime?: string;
    nextScheduleTime?: string;
  };
}

export type RestorePhase =
  | "Pending" | "Suspending" | "Running" | "Resuming" | "Succeeded" | "Failed";

export interface Restore {
  metadata: ObjectMeta;
  spec: {
    backupRef: { name: string };
    serverRef: { name: string };
  };
  status?: {
    phase?: RestorePhase;
    snapshotID?: string;
    startTime?: string;
    completionTime?: string;
    message?: string;
  };
}

// BackupDestination is a labelled Kubernetes Secret holding restic repo
// credentials. The API only ever returns a redacted projection — the
// password is never shipped to the browser; `hasPassword` confirms it
// is stored. Mutating the password requires re-POSTing both fields.
export interface BackupDestination {
  name: string;
  url: string;
  hasPassword: boolean;
  createdAt?: string;
}

// Role names are open-ended now that custom roles exist. BuiltinRole is
// kept for the few places that special-case the seeded roles (e.g. the
// role badge colors).
export type UserRole = string;
export type BuiltinRole = "admin" | "operator" | "viewer";

// "pending" = local account that has never set a password (e.g. created
// before the admin attached one); "oidc" = bound to an external IdP via
// the oidc_links table; "local" = password-backed local account.
export type UserProvider = "local" | "oidc" | "pending";

export interface User {
  id: number;
  username: string;
  displayName: string;
  email: string;
  role: UserRole;
  provider?: UserProvider;
  createdAt?: string;
  // Effective permission set keyed by namespace ("*" = cluster-wide; a "*"
  // permission means all). Present on /users/me; drives can()-based UI
  // gating. Absent elsewhere.
  permissions?: Record<string, string[]>;
  // Cluster-scoped permission structure: cluster id → namespace → sorted
  // permission names. Preserves cluster and namespace dimensions. When
  // present, can() uses this to mirror the backend's cluster-aware checks.
  // Omitted (omitempty) when there are no bindings. Present on /users/me.
  permissionsByCluster?: Record<string, Record<string, string[]>>;
  // Styling/theme preferences embedded by GET /users/me (absent on other
  // user payloads) — see UserThemePreferences below.
  preferences?: UserThemePreferences | null;
}

// User theme customization (specs/done_016-user-theme-customization): client-side
// representation of the user_preferences row (data-model.md §2.3).

// Base mode only — custom CSS is an overlay (customCssEnabled), not a mode.
export type ThemeType = "preset" | "custom_colors";
export type ThemePresetId = "pink" | "legacy";
export type AppearanceMode = "light" | "dark" | "system";

export interface CustomColorConfig {
  accent: string; // #RRGGBB
  surface: string; // #RRGGBB
}

export interface UserThemePreferences {
  themeType: ThemeType;
  presetId: ThemePresetId;
  appearanceMode: AppearanceMode;
  // null when unset (the wire format sends JSON null, not an omitted key);
  // retained across preset/overlay changes, cleared only by reset (FR-012).
  customColors?: CustomColorConfig | null;
  // Overlay on/off; independent of themeType. Toggling never deletes customCss.
  customCssEnabled: boolean;
  customCss?: string | null;
  updatedAt?: string;
}

// Portable export document (FR-014, data-model.md §2.4; full schema in
// contracts/theme-export.md).
export interface ThemeExport {
  format: "gameplane-theme";
  version: 1;
  preferences: Omit<UserThemePreferences, "updatedAt">;
}

// A named set of catalog permissions.
export interface Role {
  name: string;
  description: string;
  builtin: boolean;
  permissions: string[];
}

// One grantable permission and whether it is namespaced.
export interface Permission {
  key: string;
  label: string;
  namespaced: boolean;
}

// Permissions grouped by resource, for the permission picker.
export interface PermissionGroup {
  resource: string;
  label: string;
  permissions: Permission[];
}

// A user's role grant in a namespace ("*" = cluster-wide).
export interface RoleBinding {
  roleName: string;
  namespace: string;
  /** Omitted by older API responses for local bindings. */
  cluster?: string;
}

export type ExtendedUser = User;

export interface List<T> {
  items: T[];
}

export interface ClusterStats {
  nodes?: number;
  totalStorageBytes?: number;
  usedStorageBytes?: number;
}

export interface ClusterNode {
  name: string;
  roles?: string[];
  status?: "Ready" | "NotReady" | string;
  uptime?: string;
  startedAt?: string;
  pods?: { used?: number; capacity?: number };
  cpu?: { used?: number; capacity?: number };
  memory?: { used?: number; capacity?: number };
  labels?: string[];
}

export interface ClusterView {
  nodes?: ClusterNode[];
  version?: string;
  name?: string;
  ready?: number;
  total?: number;
}

export interface ClusterInfo {
  clusterName?: string;
  version?: string; // Kubernetes server version
  gameplaneVersion?: string; // Gameplane control-plane build
  clusterOps?: boolean; // node-join / kubeconfig minting enabled on this install
  updateChannel?: string; // informational chart updates.channel label
}

export interface NodeJoinInfo {
  command: string;
  token: string;
  caCertHash: string;
  endpoint: string;
  expiresAt: string;
}

export interface ClusterRegistry {
  name: string;
  displayName: string;
  phase: "Healthy" | "Unhealthy" | "Unknown" | "";
  message?: string;
  serverVersion?: string;
  lastCheckTime?: string;
  /** Explicit selected-cluster inventory capability; absent on older APIs. */
  canViewInventory?: boolean;
}

export interface LoginProvider {
  name?: string; // route slug for /auth/oidc/{name}/start ("helm" = the Helm-flag provider)
  kind: "local" | "oidc" | string;
  label: string;
}

export interface LoginProvidersResp {
  providers: LoginProvider[];
}

export interface LoginResponse {
  user: User;
  csrf: string;
}

export interface PlayerCapabilities {
  kick: boolean;
  ban: boolean;
  unban: boolean;
  whitelist?: boolean;
}

export interface PlayerEntry {
  steamId: string;
  faction?: string;
  displayName?: string;
}

export interface PlayersResp {
  /** -1 when the agent cannot determine the player count. */
  online: number;
  /** -1 when the agent cannot determine the player cap. */
  max: number;
  players: string[];
  /** Structured roster (Nuclear Option): Steam ID, optional faction and resolved display name. Other games omit it. */
  entries?: PlayerEntry[];
  asOf: string;
  capabilities: PlayerCapabilities;
}

export interface BannedPlayer {
  name: string;
  reason?: string;
  source?: string;
}

export interface AuditEvent {
  id: number;
  ts: string;
  actor: string;
  method: string;
  path: string;
  target?: string;
  status: number;
  ip?: string;
}

export interface AuditVerifyResult {
  ok: boolean;
  firstBadId?: number;
  checked: number;
  message: string;
}

// Module catalog types — shape of /modules and /modules/* responses.

export type ModuleSourceType = "oci" | "git" | "http" | "local" | "upload";

export interface OCISourceSpec {
  url: string;
  modules: Array<{ name: string }>;
  insecure?: boolean;
  pullSecretRef?: { name: string };
}

export interface GitSourceSpec {
  url: string;
  ref?: string;
  subPath?: string;
  secretRef?: { name: string };
}

export interface HTTPSourceSpec {
  url: string;
  secretRef?: { name: string };
  insecure?: boolean;
}

export interface LocalSourceSpec {
  path?: string;
}

// Keyless (Fulcio) cosign verification: the signing certificate must carry
// this OIDC issuer and SAN identity.
export interface ModuleVerifyKeyless {
  issuer: string;
  identity: string;
}

// Cosign signature policy mirroring ModuleSource.spec.verify. Exactly one of
// key/keyless is set. The CRD restricts verify to OCI sources via CEL.
export interface ModuleVerifySpec {
  // Keyed verification: a Secret holding the cosign public key (cosign.pub).
  key?: { name: string };
  keyless?: ModuleVerifyKeyless;
  // Also require Sigstore transparency-log (Rekor) inclusion evidence. Only
  // meaningful with `key` — keyless always enforces the log. Off by default so
  // air-gapped clusters keep verifying offline-signed bundles.
  requireTransparencyLog?: boolean;
}

// Discriminated union mirroring ModuleSourceSpec on the CRD: exactly
// the nested config matching `type` is set (upload needs none).
export interface ModuleSourceSpec {
  type?: ModuleSourceType;
  oci?: OCISourceSpec;
  git?: GitSourceSpec;
  http?: HTTPSourceSpec;
  local?: LocalSourceSpec;
  allow?: string[];
  refreshInterval?: string;
  // Only meaningful for OCI sources (CEL-enforced on the CRD).
  verify?: ModuleVerifySpec;
}

export interface ModuleEntry {
  name: string;
  displayName?: string;
  summary?: string;
  game?: string;
  icon?: string;
  reference?: string;
  versions?: string[];
  latestVersion?: string;
  digest?: string;
}

// SourceRef names one ModuleSource offering a catalog entry, with its
// type so the UI can badge where the module comes from.
export interface SourceRef {
  name: string;
  type: ModuleSourceType;
}

export interface ModuleSource {
  metadata: ObjectMeta;
  spec: ModuleSourceSpec;
  status?: {
    lastSync?: string;
    modules?: ModuleEntry[];
    conditions?: Array<{
      type: string;
      status: string;
      reason?: string;
      message?: string;
      lastTransitionTime?: string;
    }>;
  };
}

export type ModulePhase = "Pending" | "Pulling" | "Ready" | "Failed";

export interface Module {
  metadata: ObjectMeta;
  spec: {
    source: { name: string };
    name: string;
    version?: string;
  };
  status?: {
    phase?: ModulePhase;
    appliedVersion?: string;
    appliedDigest?: string;
    appliedTemplate?: string;
    previousVersion?: string;
    previousDigest?: string;
    lastError?: string;
    conditions?: Array<{
      type: string;
      status: string;
      reason?: string;
      message?: string;
    }>;
  };
}

// CatalogEntry is the merged-view row served by /modules/catalog. The
// API joins each ModuleSource.status.modules with the live Module CRs
// to produce one row per logical module name.
export interface CatalogEntry {
  name: string;
  displayName?: string;
  summary?: string;
  game?: string;
  categories?: string[];
  icon?: string;
  sources: SourceRef[];
  versions?: string[];
  latestVersion?: string;
  digest?: string;
  installed: boolean;
  installedVersion?: string;
  installedFrom?: string; // ModuleSource name
  moduleName?: string;    // Module CR name (= GameTemplate name)
  phase?: ModulePhase;
  lastError?: string;
  appliedDigest?: string;    // digest of the installed bundle
  // verifiedDigest is set only when the operator actually ran cosign
  // verification for this install (Module.status.verifiedDigest); empty if
  // the source had no verify policy at install time, or the module predates
  // this field. verifyPolicy is the mode ("keyed"/"keyless") that produced
  // it, empty whenever verifiedDigest is.
  verifiedDigest?: string;
  verifyPolicy?: string;
  previousVersion?: string;  // rollback target (operator-owned)
  previousDigest?: string;
  // pinnedVersion is the Module's desired spec.version (what it's pinned to);
  // differs from installedVersion when a pin failed to apply. Empty = latest.
  pinnedVersion?: string;
  // reason is the Ready-condition reason (e.g. "VersionUnavailable") so the
  // UI can act on WHY a module failed.
  reason?: string;
}

// ShareLink mirrors the list/create response shapes from the API's shareResp
// (api/internal/handlers/shares.go). The token is only present in create
// responses; list/revoke never include it.
export interface ShareLink {
  id: string;
  createdAt: string; // RFC3339 timestamp
  expiresAt: string | null; // RFC3339 timestamp, or null = never expires
  revokedAt?: string | null; // RFC3339 timestamp, or null = active
  canStart: boolean;
  token?: string; // only in create response
}

// ShareLinkCreateRequest is the request body for POST /servers/{name}:shares.
// Callers send exactly one of `expiresAt` or `neverExpires: true` (OD-1/OD-5).
export interface ShareLinkCreateRequest {
  /** @deprecated for one release per OD-1/OD-5; prefer `expiresAt`/`neverExpires`. */
  expiresIn?: string; // e.g. "24h", "7d"; undefined = default (7 days)
  expiresAt?: string; // RFC3339 instant; the absolute replacement for expiresIn
  neverExpires?: boolean; // true = create a link with no expiry
  canStart: boolean;
}

// ShareAddr is the public address + port pair on the public resolve response.
export interface ShareAddr {
  host: string;
  port: number;
}

// ShareLinkPublic mirrors the sharePublicResp response shape from the API
// (api/internal/handlers/shares.go), returned by GET /shares/{token} without
// authentication. Contains only the minimal public view: server name, status,
// address (if exposed), and player count (if exposed). Never reveals cluster,
// namespace, version, user names, or other internal metadata.
export interface ShareLinkPublic {
  serverName: string;
  status: string; // e.g. "Running", "Asleep", "Failed"
  address?: ShareAddr; // undefined if no public endpoint is exposed
  playersOnline?: number; // undefined if not exposed by the game
}
