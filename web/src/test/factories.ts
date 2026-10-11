// Test data factories — produce fully-populated objects matching the
// types in src/types.ts. Each factory accepts a partial override so tests
// can pin only the fields they care about.

import type {
  GameServer,
  GameTemplate,
  Backup,
  BackupSchedule,
  BackupDestination,
  BannedPlayer,
  User,
  AuditEvent,
  ClusterView,
  ClusterInfo,
  ClusterStats,
  CatalogEntry,
  Module,
  ModuleSource,
  PlayerEntry,
  PlayersResp,
  Restore,
  NetworkCapture,
} from "@/types";
import type { AllConfig } from "@/lib/config";
import type { FileEntry } from "@/lib/endpoints";

// Built-in role permission sets, mirroring the server seed, so makeUser
// produces a permission set consistent with the requested role and
// can()-based UI gating behaves in tests as it would against the API.
const builtinPerms: Record<string, string[]> = {
  admin: ["*"],
  operator: [
    "servers:read", "servers:write", "servers:console",
    "backups:read", "backups:write", "backups:restore",
    "schedules:read", "schedules:write",
    "templates:read", "templates:write",
    "modules:read", "destinations:read", "cluster:read", "roles:read",
  ],
  viewer: [
    "servers:read", "backups:read", "schedules:read", "templates:read",
    "modules:read", "destinations:read", "cluster:read", "roles:read",
  ],
};

// Built-in roles that explicitly do NOT get captures:manage (admin gets it via
// the wildcard "*"; operator/viewer are denied it per FR-005/SC-005).

export function makeUser(over: Partial<User> = {}): User {
  const role = over.role ?? "admin";
  return {
    id: 1,
    username: "admin",
    displayName: "Admin",
    email: "admin@example.com",
    role,
    provider: "local",
    createdAt: "2026-01-01T00:00:00Z",
    permissions: { "*": builtinPerms[role] ?? [] },
    ...over,
  };
}

// Override allows pinning only the nested fields a test cares about (a
// partial metadata/spec/status), unlike a flat Partial<GameServer> which
// would demand a complete spec. The nested objects are deep-merged over
// the defaults below.
type ServerOverride = {
  metadata?: Partial<GameServer["metadata"]>;
  spec?: Partial<GameServer["spec"]>;
  status?: Partial<GameServer["status"]>;
};

export function makeServer(over: ServerOverride = {}): GameServer {
  return {
    metadata: {
      name: "alpha",
      namespace: "gameplane-games",
      creationTimestamp: "2026-04-01T00:00:00Z",
      ...(over.metadata ?? {}),
    },
    spec: {
      templateRef: { name: "minecraft-vanilla" },
      ...(over.spec ?? {}),
    },
    status: {
      phase: "Running",
      agent: { playersOnline: 3, playersMax: 20, lastHeartbeat: "2026-05-07T11:59:30Z" },
      startedAt: "2026-05-07T09:00:00Z",
      ...(over.status ?? {}),
    },
  };
}

// Override allows pinning only the nested fields a test cares about (a
// partial spec/metadata/status), unlike a flat Partial<GameTemplate>
// which would demand a complete spec. The nested objects are deep-merged
// over the defaults below.
type TemplateOverride = {
  metadata?: Partial<GameTemplate["metadata"]>;
  spec?: Partial<GameTemplate["spec"]>;
  status?: Partial<NonNullable<GameTemplate["status"]>>;
};

export function makeTemplate(over: TemplateOverride = {}): GameTemplate {
  return {
    metadata: { name: "minecraft-vanilla", ...(over.metadata ?? {}) },
    spec: {
      displayName: "Minecraft (Vanilla)",
      game: "minecraft",
      version: "1.21",
      image: "ghcr.io/gameplanepanel/gameplane/minecraft:1.21",
      consoleMode: "rcon",
      rcon: { protocol: "source" },
      logPath: "/data/logs/latest.log",
      ...(over.spec ?? {}),
    },
    status: { inUseCount: 1, ...(over.status ?? {}) },
  };
}

export function makeBackup(over: Partial<Backup> = {}): Backup {
  return {
    metadata: { name: "alpha-2026-05-07", namespace: "gameplane-games" },
    spec: { serverRef: { name: "alpha" } },
    status: {
      phase: "Succeeded",
      startTime: "2026-05-07T03:00:00Z",
      completionTime: "2026-05-07T03:01:30Z",
      size: "120 MiB",
      snapshotID: "abcd1234ef",
    },
    ...over,
  };
}

export function makeSchedule(over: Partial<BackupSchedule> = {}): BackupSchedule {
  return {
    metadata: { name: "alpha-daily", namespace: "gameplane-games" },
    spec: { serverRef: { name: "alpha" }, schedule: "0 3 * * *", retention: { keepLast: 7 } },
    status: {
      lastSuccessfulTime: "2026-05-07T03:01:30Z",
      nextScheduleTime: "2026-05-08T03:00:00Z",
    },
    ...over,
  };
}

export function makeRestore(over: Partial<Restore> = {}): Restore {
  return {
    metadata: { name: "restore-alpha-1", namespace: "gameplane-games" },
    spec: {
      backupRef: { name: "alpha-2026-05-07" },
      serverRef: { name: "alpha" },
    },
    status: {
      phase: "Succeeded",
      snapshotID: "abcd1234ef",
      startTime: "2026-05-07T04:00:00Z",
      completionTime: "2026-05-07T04:02:00Z",
    },
    ...over,
  };
}

export function makeDestination(over: Partial<BackupDestination> = {}): BackupDestination {
  return {
    name: "default",
    url: "s3:s3.amazonaws.com/gameplane-backups",
    hasPassword: true,
    createdAt: "2026-04-01T00:00:00Z",
    ...over,
  };
}

export function makeAudit(over: Partial<AuditEvent> = {}): AuditEvent {
  return {
    id: 1,
    ts: "2026-05-07T12:00:00Z",
    actor: "admin",
    method: "POST",
    path: "/api/v1/servers",
    target: "alpha",
    status: 201,
    ip: "10.0.0.1",
    ...over,
  };
}

export function makeClusterView(over: Partial<ClusterView> = {}): ClusterView {
  return {
    name: "gameplane-prod",
    version: "v1.31.0",
    ready: 3,
    total: 3,
    nodes: [
      {
        name: "node-1",
        roles: ["control-plane", "worker"],
        status: "Ready",
        startedAt: "2026-05-01T00:00:00Z",
        cpu: { used: 2, capacity: 8 },
        memory: { used: 4_000_000_000, capacity: 16_000_000_000 },
        pods: { used: 12, capacity: 110 },
      },
    ],
    ...over,
  };
}

export function makeClusterStats(over: Partial<ClusterStats> = {}): ClusterStats {
  return { nodes: 3, totalStorageBytes: 1_000_000_000_000, usedStorageBytes: 250_000_000_000, ...over };
}

export function makeClusterInfo(over: Partial<ClusterInfo> = {}): ClusterInfo {
  return { clusterName: "homelab-01", version: "v1.31.0", clusterOps: true, updateChannel: "stable", ...over };
}

export function makeCatalog(over: Partial<CatalogEntry> = {}): CatalogEntry {
  return {
    name: "minecraft-vanilla",
    displayName: "Minecraft (Vanilla)",
    summary: "Run a vanilla Minecraft server",
    game: "minecraft",
    sources: [{ name: "upstream", type: "oci" }],
    versions: ["1.21", "1.20", "1.19"],
    latestVersion: "1.21",
    installed: false,
    ...over,
  };
}

export function makeModule(over: Partial<Module> = {}): Module {
  return {
    metadata: { name: "minecraft-vanilla" },
    spec: {
      source: { name: "upstream" },
      name: "minecraft-vanilla",
      version: "1.21",
    },
    status: {
      phase: "Ready",
      appliedVersion: "1.21",
      appliedTemplate: "minecraft-vanilla",
    },
    ...over,
  };
}

export function makeModuleSource(over: Partial<ModuleSource> = {}): ModuleSource {
  return {
    metadata: { name: "upstream" },
    spec: {
      type: "oci",
      oci: {
        url: "ghcr.io/gameplanepanel/gameplane/modules",
        modules: [{ name: "minecraft-vanilla" }],
      },
    },
    status: { lastSync: "2026-05-07T00:00:00Z", modules: [] },
    ...over,
  };
}

export function makePlayers(over: Partial<PlayersResp> = {}): PlayersResp {
  return {
    online: 2,
    max: 20,
    players: ["alice", "bob"],
    asOf: "2026-05-07T12:00:00Z",
    capabilities: { kick: true, ban: true, unban: true, whitelist: true },
    ...over,
  };
}

export function makePlayerEntry(over: Partial<PlayerEntry> = {}): PlayerEntry {
  return {
    steamId: "76561198000000001",
    faction: "Boscali",
    displayName: "Pilot_Vance",
    ...over,
  };
}

export function makeBannedPlayer(over: Partial<BannedPlayer> = {}): BannedPlayer {
  return {
    name: "griefer-1",
    reason: "broke spawn",
    source: "console",
    ...over,
  };
}

export function makeFileEntry(over: Partial<FileEntry> = {}): FileEntry {
  return {
    name: "server.properties",
    path: "/data/server.properties",
    size: 412,
    dir: false,
    mode: "0644",
    modTime: "2026-05-07T12:00:00Z",
    ...over,
  };
}

export function makeConfig(over: Partial<AllConfig> = {}): AllConfig {
  return {
    general: {
      instanceName: "Gameplane (mock)",
      externalURL: "https://gameplane.local",
      defaultNamespace: "gameplane-games",
    },
    auth: { providers: [{ name: "local", kind: "local", enabled: true }] },
    notifications: { sinks: [] },
    telemetry: { sendMetrics: false, extended: false },
    ...over,
  };
}

export function makeCapture(over: Partial<NetworkCapture> = {}): NetworkCapture {
  return {
    captureId: "cap-12345",
    phase: "Completed",
    serverName: "alpha",
    filter: "tcp port 25565",
    maxDurationSeconds: 3600,
    maxSizeBytes: 104857600, // 100 MB
    ttlSecondsAfterFinished: 86400, // 24 hours
    createdAt: "2026-08-23T00:00:00Z",
    startedAt: "2026-08-23T00:00:00Z",
    completedAt: "2026-08-23T01:00:00Z",
    bytesWritten: 1024,
    packetsWritten: 100,
    expiresAt: "2026-08-30T00:00:00Z",
    ...over,
  };
}
