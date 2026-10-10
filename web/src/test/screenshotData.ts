// Rich, deterministic data set for screenshot/demo purposes, built from
// existing factories to keep shapes in sync. Used when
// localStorage.getItem("gameplane-e2e-dataset") === "screenshots".

import type {
  GameServer,
  GameTemplate,
  User,
  AuditEvent,
  ClusterView,
  ServerEvent,
  InstalledMod,
  RegistryProject,
} from "@/types";
import type { AllConfig } from "@/lib/config";
import {
  makeAudit,
  makeClusterStats,
  makeClusterView,
  makeConfig,
  makeRestore,
  makeSchedule,
  makeServer,
  makeTemplate,
  makeUser,
} from "./factories";

// ============================================================================
// GameTemplates: 8 official templates with short descriptions
// ============================================================================

export const screenshotTemplates: GameTemplate[] = [
  makeTemplate({
    metadata: { name: "minecraft-java" },
    spec: {
      displayName: "Minecraft Java Edition",
      game: "minecraft",
      version: "1.21",
      description: "Official Minecraft Java Edition server",
      image: "ghcr.io/gameplanepanel/gameplane/minecraft:1.21",
      versions: [
        { id: "1.21", displayName: "1.21 (Vanilla)", default: true, gameVersion: "1.21" },
      ],
      rcon: { protocol: "minecraft" },
      capabilities: {
        status: {
          metrics: [
            { id: "world-seed", displayName: "World seed" },
            { id: "difficulty", displayName: "Difficulty" },
          ],
        },
        actions: [
          {
            id: "set-time",
            displayName: "Set time",
            icon: "clock",
            group: "WORLD",
          },
          {
            id: "set-weather",
            displayName: "Set weather",
            icon: "cloud",
            group: "WORLD",
          },
          {
            id: "save-world",
            displayName: "Save world",
            description: "Flush the world to disk now.",
            icon: "save",
            group: "WORLD",
          },
          {
            id: "broadcast",
            displayName: "Broadcast message",
            description: "Send a chat message to everyone on the server.",
            icon: "megaphone",
            group: "SERVER",
          },
          {
            id: "reload-config",
            displayName: "Reload config",
            description: "Reapply server.properties without a restart.",
            icon: "refresh-cw",
            group: "SERVER",
          },
          {
            id: "announce-restart",
            displayName: "Announce restart",
            icon: "rotate-ccw",
            danger: true,
            group: "SERVER",
          },
          {
            id: "toggle-pvp",
            displayName: "Toggle PvP",
            description: "Enable or disable player-versus-player combat.",
            icon: "gamepad-2",
            group: "PLAYERS",
          },
        ],
        mods: {
          path: "mods",
          registry: {
            providers: [{ provider: "modrinth", modpacks: {} }],
          },
        },
      },
    },
  }),
  makeTemplate({
    metadata: { name: "satisfactory" },
    spec: {
      displayName: "Satisfactory",
      game: "satisfactory",
      version: "1.0",
      description: "Satisfactory dedicated server",
      image: "ghcr.io/gameplanepanel/gameplane/satisfactory:1.0",
    },
  }),
  makeTemplate({
    metadata: { name: "valheim-default" },
    spec: {
      displayName: "Valheim",
      game: "valheim",
      version: "0.218",
      description: "Norse exploration and survival game",
      image: "ghcr.io/gameplanepanel/gameplane/valheim:0.218",
      capabilities: {
        mods: {
          path: "BepInEx/plugins",
          extensions: [".dll", ".zip"],
          install: { allowedHosts: ["thunderstore.io", "gcdn.thunderstore.io"] },
          registry: { providers: [{ provider: "thunderstore", community: "valheim" }] },
        },
      },
    },
  }),
  makeTemplate({
    metadata: { name: "terraria-vanilla" },
    spec: {
      displayName: "Terraria",
      game: "terraria",
      version: "1.4.4",
      description: "2D sandbox with mining and exploration",
      image: "ghcr.io/gameplanepanel/gameplane/terraria:1.4.4",
    },
  }),
  makeTemplate({
    metadata: { name: "rust-vanilla" },
    spec: {
      displayName: "Rust",
      game: "rust",
      version: "2026.01",
      description: "Survival multiplayer game",
      image: "ghcr.io/gameplanepanel/gameplane/rust:2026.01",
    },
  }),
  makeTemplate({
    metadata: { name: "palworld-default" },
    spec: {
      displayName: "Palworld",
      game: "palworld",
      version: "0.3.5",
      description: "Pokemon-like survival crafting MMO",
      image: "ghcr.io/gameplanepanel/gameplane/palworld:0.3.5",
    },
  }),
  makeTemplate({
    metadata: { name: "factorio-vanilla" },
    spec: {
      displayName: "Factorio",
      game: "factorio",
      version: "1.1.105",
      description: "Industrial automation and logistics",
      image: "ghcr.io/gameplanepanel/gameplane/factorio:1.1.105",
    },
  }),
  makeTemplate({
    metadata: { name: "cs2-competitive" },
    spec: {
      displayName: "Counter-Strike 2",
      game: "cs2",
      version: "2026.02",
      description: "Competitive tactical first-person shooter",
      image: "ghcr.io/gameplanepanel/gameplane/cs2:2026.02",
    },
  }),
  makeTemplate({
    metadata: { name: "ark-ascended" },
    spec: {
      displayName: "ARK: Survival Ascended",
      game: "ark",
      version: "1.30.2",
      description: "Dinosaur survival game",
      image: "ghcr.io/gameplanepanel/gameplane/ark:1.30.2",
    },
  }),
  makeTemplate({
    metadata: { name: "minecraft-modlist" },
    spec: {
      displayName: "Minecraft (Mod List)",
      game: "minecraft",
      version: "1.21",
      description: "Minecraft server with mod list support",
      image: "ghcr.io/gameplanepanel/gameplane/minecraft:1.21",
      capabilities: {
        mods: {
          path: "mods",
          idList: { env: "MODLIST_FILE" },
          registry: {
            providers: [{ provider: "modrinth", modpacks: {} }],
          },
        },
      },
    },
  }),
  makeTemplate({
    metadata: { name: "minecraft-modded" },
    spec: {
      displayName: "Minecraft (Modded)",
      game: "minecraft",
      version: "1.21",
      description: "Minecraft server with mod support (Fabric/Forge)",
      image: "ghcr.io/gameplanepanel/gameplane/minecraft:1.21",
      versions: [
        { id: "1.21", displayName: "1.21 (Vanilla)", default: true, gameVersion: "1.21" },
        { id: "1.21-fabric", displayName: "1.21 (Fabric)", loader: "fabric" },
        { id: "1.21-forge", displayName: "1.21 (Forge)", loader: "forge" },
        { id: "1.20.4", displayName: "1.20.4", gameVersion: "1.20.4" },
      ],
      capabilities: {
        mods: {
          path: "mods",
          extensions: [".jar"],
          install: { allowedHosts: ["modrinth.com", "cdn.modrinth.com", "github.com"] },
          loaders: {
            fabric: { path: "mods" },
            forge: { path: "mods" },
          },
          registry: {
            // Two providers (design GayoL, specs/014h) — test-server-09's
            // mods/registry/providers mock handler exposes both so the
            // Mods browse screen can capture the provider tabs + category
            // pills; hangar is additive alongside the pre-existing modrinth.
            providers: [{ provider: "modrinth", modpacks: {} }, { provider: "hangar" }],
          },
        },
      },
    },
  }),
];

// ============================================================================
// GameServers: 5 servers with diverse phases and namespaces
// ============================================================================

export const screenshotServers: GameServer[] = [
  makeServer({
    metadata: {
      name: "mc-survival",
      namespace: "gameplane-games",
      // The screenshot session (user 1) owns this server and exercises share-link dialogs.
      annotations: { "gameplane.local/node": "kubelab-control", "gameplane.local/owner-id": "1", "gameplane.local/owner": "admin" },
    },
    spec: {
      templateRef: { name: "minecraft-java" },
      // Matches design frame OC804 (Settings · Game configuration). The
      // password is the API's redaction marker, as the real API returns it.
      config: {
        DIFFICULTY: "Expert",
        MAX_PLAYERS: "8",
        PVP: "false",
        SERVER_PASSWORD: "__gameplane_redacted__",
        MOTD: "Welcome to Gameplane",
      },
      idle: {
        enabled: true,
        afterMinutes: 30,
        wakeWindows: ["0 17 * * *", "0 9 * * 6,0"],
        wakeOnConnect: true,
      },
      capture: { enabled: true },
    },
    status: {
      phase: "Running",
      agent: {
        gameVersion: "1.21.4-fabric",
        playersOnline: 0,
        playersMax: 20,
        lastHeartbeat: new Date(Date.now() - 15 * 1000).toISOString(),
        cpuMillicores: 0,
        cpuLimitMillicores: 2000,
        memoryBytes: 1_520_000_000,
        memoryLimitBytes: 4_000_000_000,
        diskUsedBytes: 3_770_000_000,
        diskTotalBytes: 29_000_000_000,
      },
      endpoints: [
        {
          name: "frp",
          host: "mc.frp.gameplane.dev",
          port: 25565,
          tunnelProvider: "FRP",
        },
        {
          name: "external",
          host: "172.18.255.203",
          port: 25565,
          pool: "pool-us-west",
        },
        {
          name: "cluster",
          host: "10.107.129.42",
          port: 30812,
        },
      ],
      startedAt: new Date(Date.now() - 185 * 1000).toISOString(),
    },
  }),
  makeServer({
    metadata: {
      name: "mc-test",
      namespace: "gameplane-games",
      annotations: { "gameplane.local/node": "kubelab-worker-2" },
    },
    spec: { templateRef: { name: "minecraft-java" } },
    status: {
      phase: "Failed",
      agent: {
        playersOnline: null,
        playersMax: 0,
        cpuMillicores: 0,
        cpuLimitMillicores: 2000,
        memoryBytes: 0,
        memoryLimitBytes: 4_000_000_000,
      },
      startedAt: undefined,
    },
  }),
  makeServer({
    metadata: {
      name: "user-server-test",
      namespace: "gameplane-games",
    },
    spec: { templateRef: { name: "satisfactory" }, suspend: true },
    status: {
      phase: "Suspended",
      idle: {
        asleep: true,
        asleepSince: new Date(Date.now() - 21600 * 1000).toISOString(),
      },
      agent: {
        playersOnline: null,
        playersMax: 0,
      },
      startedAt: undefined,
    },
  }),
  makeServer({
    metadata: {
      name: "test-server-09",
      namespace: "default",
      annotations: { "gameplane.local/node": "node-01" },
    },
    spec: { templateRef: { name: "minecraft-modded" }, version: "1.21-fabric" },
    status: {
      phase: "Running",
      agent: {
        playersOnline: 4,
        playersMax: 20,
        lastHeartbeat: "2026-09-06T10:15:30Z",
        cpuMillicores: 1680,
        cpuLimitMillicores: 4000,
        memoryBytes: 6_200_000_000,
        memoryLimitBytes: 8_000_000_000,
        diskUsedBytes: 15_600_000_000,
        diskTotalBytes: 50_000_000_000,
      },
      endpoints: [
        {
          name: "main",
          host: "test-server-09.gameplane-demo.local",
          port: 25565,
          protocol: "tcp",
        },
      ],
      startedAt: "2026-09-03T14:20:00Z",
    },
  }),
  makeServer({
    metadata: {
      // Distinct from the "mc-survival" entry above: this fixture
      // pre-dates that rename (commit 07ed2333) and was itself the
      // "primary mock server" the rename was targeting under its old
      // name. Renaming both to "mc-survival" duplicated the fixture
      // (same namespace/name), which double-rendered its row/card and
      // its <Link to="/servers/$name"> in the Servers list — restore
      // the pre-rename name here to keep the fixture set unique.
      name: "test-server-01",
      namespace: "gameplane-games",
      annotations: { "gameplane.local/node": "kubelab-control" },
    },
    spec: {
      templateRef: { name: "minecraft-modded" },
      idle: {
        enabled: true,
        afterMinutes: 30,
        wakeWindows: ["0 17 * * *", "0 9 * * 6,0"],
        wakeOnConnect: true,
      },
      capture: { enabled: true },
    },
    status: {
      phase: "Running",
      agent: {
        gameVersion: "1.21.4-fabric",
        playersOnline: 0,
        playersMax: 20,
        lastHeartbeat: new Date(Date.now() - 15 * 1000).toISOString(),
        cpuMillicores: 0,
        cpuLimitMillicores: 2000,
        memoryBytes: 1_520_000_000,
        memoryLimitBytes: 4_000_000_000,
        diskUsedBytes: 3_770_000_000,
        diskTotalBytes: 29_000_000_000,
      },
      endpoints: [
        {
          name: "frp",
          host: "mc.frp.gameplane.dev",
          port: 25565,
          tunnelProvider: "FRP",
        },
        {
          name: "external",
          host: "172.18.255.203",
          port: 25565,
          pool: "pool-us-west",
        },
        {
          name: "cluster",
          host: "10.107.129.42",
          port: 30812,
        },
      ],
      startedAt: new Date(Date.now() - 185 * 1000).toISOString(),
    },
  }),
  makeServer({
    metadata: {
      name: "test-server-02",
      namespace: "default",
      annotations: { "gameplane.local/node": "node-01" },
    },
    spec: { templateRef: { name: "minecraft-modded" }, version: "1.21-fabric" },
    status: {
      phase: "Running",
      agent: {
        playersOnline: 4,
        playersMax: 20,
        lastHeartbeat: "2026-09-06T10:15:30Z",
        cpuMillicores: 1680,
        cpuLimitMillicores: 4000,
        memoryBytes: 6_200_000_000,
        memoryLimitBytes: 8_000_000_000,
        diskUsedBytes: 15_600_000_000,
        diskTotalBytes: 50_000_000_000,
      },
      endpoints: [
        {
          name: "main",
          host: "test-server-02.gameplane-demo.local",
          port: 25565,
          protocol: "tcp",
        },
      ],
      startedAt: "2026-09-03T14:20:00Z",
    },
  }),
  makeServer({
    metadata: {
      name: "test-server-capture-disabled",
      namespace: "default",
      annotations: { "gameplane.local/node": "node-01" },
    },
    spec: { templateRef: { name: "minecraft-modded" }, capture: { enabled: false } },
    status: {
      phase: "Running",
      agent: {
        playersOnline: 0,
        playersMax: 20,
        lastHeartbeat: "2026-09-06T10:15:30Z",
        cpuMillicores: 0,
        cpuLimitMillicores: 4000,
        memoryBytes: 1_520_000_000,
        memoryLimitBytes: 4_000_000_000,
        diskUsedBytes: 3_770_000_000,
        diskTotalBytes: 29_000_000_000,
      },
      endpoints: [
        {
          name: "main",
          host: "test-server-capture-disabled.gameplane-demo.local",
          port: 25565,
          protocol: "tcp",
        },
      ],
      startedAt: "2026-09-03T14:20:00Z",
    },
  }),
  makeServer({
    metadata: {
      name: "test-server-no-shares",
      namespace: "default",
      annotations: { "gameplane.local/node": "node-01" },
    },
    spec: { templateRef: { name: "minecraft-modded" } },
    status: {
      phase: "Running",
      agent: {
        playersOnline: 0,
        playersMax: 20,
        lastHeartbeat: "2026-09-06T10:15:30Z",
        cpuMillicores: 0,
        cpuLimitMillicores: 4000,
        memoryBytes: 1_520_000_000,
        memoryLimitBytes: 4_000_000_000,
        diskUsedBytes: 3_770_000_000,
        diskTotalBytes: 29_000_000_000,
      },
      endpoints: [
        {
          name: "main",
          host: "test-server-no-shares.gameplane-demo.local",
          port: 25565,
          protocol: "tcp",
        },
      ],
      startedAt: "2026-09-03T14:20:00Z",
    },
  }),
  makeServer({
    metadata: { name: "test-server-04", namespace: "gameplane-demo" },
    spec: { templateRef: { name: "rust-vanilla" } },
    status: {
      phase: "Failed",
      agent: { playersOnline: null, playersMax: 128, lastHeartbeat: undefined },
    },
  }),
  makeServer({
    metadata: {
      name: "ark-island",
      namespace: "gameplane-demo",
      annotations: { "gameplane.local/node": "node-01" },
    },
    spec: { templateRef: { name: "minecraft-modlist" } },
    status: {
      phase: "Running",
      agent: {
        playersOnline: 0,
        playersMax: 70,
        lastHeartbeat: "2026-09-06T10:15:30Z",
        cpuMillicores: 0,
        cpuLimitMillicores: 4000,
        memoryBytes: 1_520_000_000,
        memoryLimitBytes: 4_000_000_000,
        diskUsedBytes: 3_770_000_000,
        diskTotalBytes: 29_000_000_000,
      },
      endpoints: [
        {
          name: "main",
          host: "ark-island.gameplane-demo.local",
          port: 27015,
          protocol: "tcp",
        },
      ],
      startedAt: "2026-09-03T14:20:00Z",
    },
  }),
];

// ============================================================================
// Cluster Nodes: 3 nodes matching 12 vCPUs cluster core capacity
// ============================================================================

export const screenshotNodes = [
  {
    name: "kubelab-control",
    roles: ["control-plane", "worker"],
    status: "Ready" as const,
    startedAt: "2026-08-01T00:00:00Z",
    cpu: { used: 0.5, capacity: 4 },
    memory: { used: 2_000_000_000, capacity: 8_000_000_000 },
    pods: { used: 12, capacity: 110 },
  },
  {
    name: "kubelab-worker-1",
    roles: ["worker"],
    status: "Ready" as const,
    startedAt: "2026-08-10T12:00:00Z",
    cpu: { used: 1.2, capacity: 4 },
    memory: { used: 4_000_000_000, capacity: 8_000_000_000 },
    pods: { used: 18, capacity: 110 },
  },
  {
    name: "kubelab-worker-2",
    roles: ["worker"],
    status: "Ready" as const,
    startedAt: "2026-07-20T08:15:00Z",
    cpu: { used: 0.8, capacity: 4 },
    memory: { used: 3_000_000_000, capacity: 8_000_000_000 },
    pods: { used: 15, capacity: 110 },
  },
];

export function screenshotClusterView(): ClusterView {
  return makeClusterView({
    name: "gameplane-demo",
    version: "v1.31.0",
    ready: 3,
    total: 3,
    nodes: screenshotNodes,
  });
}

export function screenshotClusterStats() {
  return makeClusterStats({
    nodes: 3,
    totalStorageBytes: 86 * 1024 ** 3,
    usedStorageBytes: 77 * 1024 ** 3,
  });
}

// ============================================================================
// Kubernetes Events: realistic lifecycle events for mc-survival
// ============================================================================

export const screenshotEvents: ServerEvent[] = [
  {
    id: "evt-001",
    time: new Date(Date.now() - 170 * 1000).toISOString(),
    type: "Normal",
    reason: "Pulled",
    message: 'Successfully pulled image "itzg/minecraft-server:java21" ... 321 MB',
    source: "kubelet",
    object: "mc-survival",
    count: 1,
  },
  {
    id: "evt-002",
    time: new Date(Date.now() - 175 * 1000).toISOString(),
    type: "Normal",
    reason: "Created",
    message: "Container created",
    source: "kubelet",
    object: "mc-survival",
    count: 1,
  },
  {
    id: "evt-003",
    time: new Date(Date.now() - 178 * 1000).toISOString(),
    type: "Normal",
    reason: "Started",
    message: "Container started",
    source: "kubelet",
    object: "mc-survival",
    count: 1,
  },
  {
    id: "evt-004",
    time: new Date(Date.now() - 180 * 1000).toISOString(),
    type: "Normal",
    reason: "Scheduled",
    message: "Successfully assigned gameplane-games/mc-survival-0 to kubelab-control",
    source: "default-scheduler",
    object: "mc-survival",
    count: 1,
  },
  {
    id: "evt-005",
    time: new Date(Date.now() - 182 * 1000).toISOString(),
    type: "Normal",
    reason: "SuccessfulCreate",
    message: "Create Pod mc-survival-0 ...",
    source: "statefulset-controller",
    object: "mc-survival",
    count: 1,
  },
  {
    id: "evt-001",
    time: "2026-09-02T15:35:00Z",
    type: "Normal",
    reason: "Scheduled",
    message: 'Successfully assigned gameplane-demo/test-server-04 to node-01',
    source: "default-scheduler",
    object: "test-server-04",
    count: 1,
  },
  {
    id: "evt-002",
    time: "2026-09-02T15:36:15Z",
    type: "Normal",
    reason: "Pulling",
    message: 'Pulling image "ghcr.io/gameplanepanel/gameplane/rust:2026.01"',
    source: "kubelet",
    object: "test-server-04",
    count: 1,
  },
  {
    id: "evt-003",
    time: "2026-09-02T15:37:30Z",
    type: "Normal",
    reason: "Pulled",
    message: 'Successfully pulled image "ghcr.io/gameplanepanel/gameplane/rust:2026.01" in 1m15s',
    source: "kubelet",
    object: "test-server-04",
    count: 1,
  },
  {
    id: "evt-004",
    time: "2026-09-02T15:37:45Z",
    type: "Normal",
    reason: "Created",
    message: 'Created container rust-server',
    source: "kubelet",
    object: "test-server-04",
    count: 1,
  },
  {
    id: "evt-005",
    time: "2026-09-02T15:37:50Z",
    type: "Normal",
    reason: "Started",
    message: 'Started container rust-server',
    source: "kubelet",
    object: "test-server-04",
    count: 1,
  },
  {
    id: "evt-006",
    time: "2026-09-02T15:40:22Z",
    type: "Warning",
    reason: "ImagePullBackOff",
    message: 'Back-off pulling image "ghcr.io/gameplanepanel/gameplane/rust:2026.01"',
    source: "kubelet",
    object: "test-server-04",
    count: 3,
  },
  {
    id: "evt-007",
    time: "2026-09-02T15:41:45Z",
    type: "Warning",
    reason: "CrashLoopBackOff",
    message: 'Back-off restarting failed container rust-server',
    source: "kubelet",
    object: "test-server-04",
    count: 12,
  },
];

// ============================================================================
// Audit Events: 15 diverse audit events across actors and methods
// ============================================================================

export const screenshotAuditEvents: AuditEvent[] = [
  makeAudit({
    id: 101,
    ts: "2026-09-02T14:22:15Z",
    actor: "test-user-01",
    method: "POST",
    path: "/api/v1/servers",
    target: "mc-survival",
    status: 201,
    ip: "<internal>",
  }),
  makeAudit({
    id: 102,
    ts: "2026-09-02T14:25:03Z",
    actor: "admin-demo",
    method: "PUT",
    path: "/api/v1/servers/mc-survival",
    target: "mc-survival",
    status: 200,
    ip: "<internal>",
  }),
  makeAudit({
    id: 103,
    ts: "2026-09-02T14:31:42Z",
    actor: "operator-01",
    method: "POST",
    path: "/api/v1/servers/mc-survival:start",
    target: "mc-survival",
    status: 202,
    ip: "<internal>",
  }),
  makeAudit({
    id: 104,
    ts: "2026-09-02T14:35:18Z",
    actor: "test-user-01",
    method: "GET",
    path: "/api/v1/servers/mc-survival",
    target: "mc-survival",
    status: 200,
    ip: "<internal>",
  }),
  makeAudit({
    id: 105,
    ts: "2026-09-02T14:42:09Z",
    actor: "admin-demo",
    method: "POST",
    path: "/api/v1/backups",
    target: "mc-survival",
    status: 201,
    ip: "<internal>",
  }),
  makeAudit({
    id: 106,
    ts: "2026-09-02T15:01:33Z",
    actor: "operator-01",
    method: "POST",
    path: "/api/v1/servers/test-server-02:restart",
    target: "test-server-02",
    status: 202,
    ip: "<internal>",
  }),
  makeAudit({
    id: 107,
    ts: "2026-09-02T15:05:21Z",
    actor: "test-user-01",
    method: "DELETE",
    path: "/api/v1/backups/mc-survival-nightly-0713",
    target: "mc-survival-nightly-0713",
    status: 204,
    ip: "<internal>",
  }),
  makeAudit({
    id: 108,
    ts: "2026-09-02T15:12:44Z",
    actor: "admin-demo",
    method: "PATCH",
    path: "/api/v1/users/2",
    target: "operator-01",
    status: 200,
    ip: "<internal>",
  }),
  makeAudit({
    id: 109,
    ts: "2026-09-02T15:18:07Z",
    actor: "admin-demo",
    method: "POST",
    path: "/api/v1/admin/config/backups",
    target: undefined,
    status: 204,
    ip: "<internal>",
  }),
  makeAudit({
    id: 110,
    ts: "2026-09-02T15:24:56Z",
    actor: "operator-01",
    method: "GET",
    path: "/api/v1/servers",
    target: undefined,
    status: 200,
    ip: "<internal>",
  }),
  makeAudit({
    id: 111,
    ts: "2026-09-02T15:31:28Z",
    actor: "test-user-01",
    method: "POST",
    path: "/api/v1/servers/mc-survival/players/kick",
    target: "Player-01",
    status: 200,
    ip: "<internal>",
  }),
  makeAudit({
    id: 112,
    ts: "2026-09-02T15:38:15Z",
    actor: "admin-demo",
    method: "POST",
    path: "/api/v1/servers",
    target: "test-server-03",
    status: 201,
    ip: "<internal>",
  }),
  makeAudit({
    id: 113,
    ts: "2026-09-02T15:40:02Z",
    actor: "operator-01",
    method: "PUT",
    path: "/api/v1/schedules/test-server-01-daily",
    target: "test-server-01-daily",
    status: 200,
    ip: "<internal>",
  }),
  makeAudit({
    id: 114,
    ts: "2026-09-02T15:43:19Z",
    actor: "test-user-01",
    method: "GET",
    path: "/api/v1/cluster",
    target: undefined,
    status: 200,
    ip: "<internal>",
  }),
  makeAudit({
    id: 115,
    ts: "2026-09-02T15:45:33Z",
    actor: "admin-demo",
    method: "POST",
    path: "/api/v1/admin/config/auth",
    target: undefined,
    status: 204,
    ip: "<internal>",
  }),
];

// ============================================================================
// Users: diverse roles and demo accounts
// ============================================================================

export const screenshotUsers: User[] = [
  makeUser({
    id: 1,
    username: "admin",
    displayName: "admin",
    email: "admin@gameplane-demo.local",
    role: "admin",
  }),
  makeUser({
    id: 2,
    username: "operator-01",
    displayName: "Server Operator",
    email: "operator@gameplane-demo.local",
    role: "operator",
  }),
  makeUser({
    id: 3,
    username: "viewer-01",
    displayName: "Demo Viewer",
    email: "viewer@gameplane-demo.local",
    role: "viewer",
  }),
  makeUser({
    id: 4,
    username: "test-user-01",
    displayName: "Test User",
    email: "test@gameplane-demo.local",
    role: "operator",
  }),
];

// ============================================================================
// Schedules and Restores
// ============================================================================

export const screenshotSchedules = [
  makeSchedule({
    metadata: { name: "test-server-01-daily", namespace: "default" },
    spec: {
      serverRef: { name: "mc-survival" },
      schedule: "0 3 * * *",
      retention: { keepLast: 7 },
    },
  }),
  makeSchedule({
    metadata: { name: "test-server-02-weekly", namespace: "default" },
    spec: {
      serverRef: { name: "test-server-02" },
      schedule: "0 2 * * 0",
      retention: { keepLast: 4 },
    },
  }),
];

export const screenshotRestores = [
  makeRestore({
    metadata: { name: "restore-test-server-01-1", namespace: "default" },
    spec: {
      backupRef: { name: "mc-survival-nightly-0713" },
      serverRef: { name: "mc-survival" },
    },
  }),
];

// ============================================================================
// Config / Settings
// ============================================================================

export function screenshotConfig(): AllConfig {
  return makeConfig({
    general: {
      instanceName: "My Gameplane Cluster",
      externalURL: "https://gameplane-demo.local",
      defaultNamespace: "default",
    },
    modRegistries: {
      registries: [{ provider: "curseforge" }, { provider: "steam" }],
    },
    installTimeSettings: {
      gameDataStorageClass: "fast-nvme",
    },
  });
}

// Variant of screenshotConfig() with a Helm-seeded OIDC provider (role
// mappings on all three roles) plus a dashboard override on the admin
// mapping. Used only by the slice4 Admin Settings — Authentication tests
// that need the OIDC-configured state (uMiwd, R65Xyx, Rwnu3, XL5ZU,
// vStkb, uw0dB), selected per-test via the "e2e_admin_config_variant=oidc"
// cookie (see buildScreenshotHandlers() in handlers.ts) — a page.route
// override of GET /admin/config doesn't work here because MSW's Service
// Worker (msw/browser, src/test/browser-msw.ts) answers that request
// itself; Playwright can't intercept a request already resolved inside a
// Service Worker's fetch handler. Kept out of the shared screenshotConfig()
// because most other tests (and the "no OIDC mappings yet" variant below)
// need the plain, not-yet-configured state instead.
export function screenshotConfigWithOidc(): AllConfig {
  const base = screenshotConfig();
  return {
    ...base,
    auth: {
      providers: [{ name: "local", kind: "local", enabled: true }],
      helmOverride: {
        roleMappings: {
          admin: ["ops-leads"],
        },
      },
    },
    installTimeSettings: {
      gameDataStorageClass: "fast-nvme",
      oidcHelmProvider: {
        groupsClaim: "groups",
        defaultRole: "viewer",
        roleMappings: {
          admin: ["ops-leads"],
          operator: ["ops-team"],
          viewer: ["everyone"],
        },
      },
    },
  };
}

// Variant of screenshotConfig() with a Helm-seeded OIDC provider that
// declares no role mappings at all, and no dashboard override either —
// the "no OIDC mappings yet" empty state. Backs both nNGDX (the empty-state
// banner) and BV5ei (the resulting "Not configured" provenance badges),
// selected via the "e2e_admin_config_variant=oidc-empty-mappings" cookie
// — see screenshotConfigWithOidc()'s comment on why a cookie, not
// page.route, is the override mechanism here.
export function screenshotConfigOidcEmptyMappings(): AllConfig {
  const base = screenshotConfig();
  return {
    ...base,
    installTimeSettings: {
      gameDataStorageClass: "fast-nvme",
      oidcHelmProvider: { groupsClaim: "groups", defaultRole: "viewer" },
    },
  };
}

// Variant of screenshotConfig() with no game-data StorageClass set at
// install time — the "cluster default" empty state for Cluster Settings
// (dxdEi), selected via the "e2e_admin_config_variant=empty-storage-class"
// cookie — see screenshotConfigWithOidc()'s comment on why a cookie, not
// page.route, is the override mechanism here.
export function screenshotConfigEmptyStorageClass(): AllConfig {
  const base = screenshotConfig();
  return {
    ...base,
    installTimeSettings: { gameDataStorageClass: "" },
  };
}

// Variant of screenshotConfig() with only CurseForge configured (no Steam).
// Backs the Wj0V4 (Admin Settings — Mod registries) screenshot, selected via
// the "e2e_admin_config_variant=registries-curseforge" cookie — see
// screenshotConfigWithOidc()'s comment on why a cookie, not page.route, is
// the override mechanism here.
export function screenshotConfigCurseforgeOnly(): AllConfig {
  const base = screenshotConfig();
  return {
    ...base,
    modRegistries: {
      registries: [{ provider: "curseforge" }],
    },
  };
}

// ============================================================================
// System Log Lines for Admin — System Logs screen (control-plane logs)
// ============================================================================

export const screenshotSystemLogLines = [
  '{"level":"info","ts":"2026-09-06T12:00:01Z","msg":"starting gameplane-api","version":"v0.2.0-beta.8"}',
  '{"level":"info","ts":"2026-09-06T12:00:02Z","msg":"connected to database","driver":"sqlite"}',
  '{"level":"info","ts":"2026-09-06T12:00:03Z","msg":"listening","addr":":8080"}',
  '{"level":"info","ts":"2026-09-06T12:01:15Z","msg":"request","method":"GET","path":"/api/v1/servers","status":200,"duration_ms":4}',
  '{"level":"info","ts":"2026-09-06T12:01:22Z","msg":"request","method":"POST","path":"/api/v1/auth/login","status":200,"duration_ms":112}',
  '{"level":"warn","ts":"2026-09-06T12:03:47Z","msg":"slow query","table":"audit_events","duration_ms":340}',
  '{"level":"info","ts":"2026-09-06T12:05:00Z","msg":"reconciled gameserver","name":"mc-survival","phase":"Running"}',
];

// ============================================================================
// Game Server Log Lines (exact lines matching Pencil design kPmoo)
// ============================================================================

export const screenshotLogLines = [
  "[17:50:57] [Server thread/INFO]: Starting minecraft server version 1.21.4",
  "[17:50:57] [Server thread/INFO]: Preparing level 'world'",
  "[17:51:06] [Worker-Main-2/INFO]: Preparing spawn area: 2%",
  "[17:51:07] [Worker-Main-2/INFO]: Preparing spawn area: 26%",
  "[17:51:08] [Worker-Main-2/INFO]: Preparing spawn area: 52%",
  "[17:51:09] [Worker-Main-2/INFO]: Preparing spawn area: 78%",
  "[17:51:11] [Server thread/INFO]: Done (14.618s)! For help, type 'help'",
  "[17:51:11] [Server thread/INFO]: RCON running on 0.0.0.0:25575",
  "[17:52:11] [Server thread/INFO]: Server empty for 60 seconds, pausing",
];

// ============================================================================
// Console Output Lines for RCON/PTY Console WebSocket demonstrations
// (Empty array so terminal shows only "— connected —" matching Pencil design Xn5ns)
// ============================================================================

export const screenshotConsoleOutput: string[] = [];

// ============================================================================
// Installed Mods for test-server-02 (Minecraft, Fabric)
// ============================================================================

export const screenshotEmptyMods: InstalledMod[] = [];

export const screenshotInstalledMods: InstalledMod[] = [
  {
    name: "fabric-api-0.100.8+1.21.jar",
    size: 1_482_240,
    modTime: "2026-08-30T19:12:00Z",
    meta: {
      provider: "modrinth",
      projectId: "P7dR8mSH",
      projectName: "Fabric API",
      versionNumber: "0.100.8+1.21",
      installedAt: "2026-08-30T19:12:00Z",
    },
  },
  {
    name: "sodium-fabric-0.5.11+mc1.21.jar",
    size: 212_992,
    modTime: "2026-08-30T19:14:00Z",
    meta: {
      provider: "modrinth",
      projectId: "AANobbMI",
      projectName: "Sodium",
      versionNumber: "0.5.11+mc1.21",
      installedAt: "2026-08-30T19:14:00Z",
    },
  },
  {
    name: "lithium-fabric-0.13.0+mc1.21.jar",
    size: 356_352,
    modTime: "2026-09-01T08:05:00Z",
    meta: {
      provider: "modrinth",
      projectId: "gvQqBUqZ",
      projectName: "Lithium",
      versionNumber: "0.13.0+mc1.21",
      installedAt: "2026-09-01T08:05:00Z",
    },
  },
];

// ============================================================================
// Thunderstore Registry Projects for Valheim mod browser
// ============================================================================

export const screenshotRegistryProjects: RegistryProject[] = [
  {
    id: "denikson-BepInExPack_Valheim",
    slug: "BepInExPack_Valheim",
    title: "BepInExPack_Valheim",
    description: "BepInEx pack for Valheim with mod manager integration",
    author: "denikson",
    downloads: 14_200_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/denikson/BepInExPack_Valheim/",
    provider: "thunderstore",
  },
  {
    id: "ValheimModding-Jotunn",
    slug: "Jotunn",
    title: "Jotunn",
    description: "A library that provides intuitive and modular systems for modding Valheim",
    author: "ValheimModding",
    downloads: 12_800_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/ValheimModding/Jotunn/",
    provider: "thunderstore",
  },
  {
    id: "ValheimModding-HookGenPatcher",
    slug: "HookGenPatcher",
    title: "HookGenPatcher",
    description: "Automatic Unity Networking patching for game object systems",
    author: "ValheimModding",
    downloads: 10_500_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/ValheimModding/HookGenPatcher/",
    provider: "thunderstore",
  },
  {
    id: "Grantapher-ValheimPlus",
    slug: "ValheimPlus",
    title: "ValheimPlus",
    description: "Comprehensive quality-of-life mod with farming, building, and exploration enhancements",
    author: "Grantapher",
    downloads: 9_800_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/Grantapher/ValheimPlus/",
    provider: "thunderstore",
  },
  {
    id: "RandyKnapp-EpicLoot",
    slug: "EpicLoot",
    title: "EpicLoot",
    description: "Adds an advanced loot system with rare item drops and crafting",
    author: "RandyKnapp",
    downloads: 8_200_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/RandyKnapp/EpicLoot/",
    provider: "thunderstore",
  },
  {
    id: "RandyKnapp-EquipmentAndQuickSlots",
    slug: "EquipmentAndQuickSlots",
    title: "EquipmentAndQuickSlots",
    description: "Adds equipment slots and quick-slot hotbar for better inventory management",
    author: "RandyKnapp",
    downloads: 7_100_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/RandyKnapp/EquipmentAndQuickSlots/",
    provider: "thunderstore",
  },
  {
    id: "Advize-PlantEverything",
    slug: "PlantEverything",
    title: "PlantEverything",
    description: "Allows planting of all Valheim plants for better farming",
    author: "Advize",
    downloads: 6_400_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/Advize/PlantEverything/",
    provider: "thunderstore",
  },
  {
    id: "Azumatt-AzuCraftyBoxes",
    slug: "AzuCraftyBoxes",
    title: "AzuCraftyBoxes",
    description: "Adds convenient crafting interface boxes that can be placed anywhere",
    author: "Azumatt",
    downloads: 5_600_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/Azumatt/AzuCraftyBoxes/",
    provider: "thunderstore",
  },
  {
    id: "Smoothbrain-Sailing",
    slug: "Sailing",
    title: "Sailing",
    description: "Overhauls sailing mechanics with smoother controls and navigation features",
    author: "Smoothbrain",
    downloads: 4_300_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/Smoothbrain/Sailing/",
    provider: "thunderstore",
  },
  {
    id: "Smoothbrain-Farming",
    slug: "Farming",
    title: "Farming",
    description: "Expands farming with new crops and enhanced growth mechanics",
    author: "Smoothbrain",
    downloads: 3_800_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/Smoothbrain/Farming/",
    provider: "thunderstore",
  },
  {
    id: "ishid4-BetterArchery",
    slug: "BetterArchery",
    title: "BetterArchery",
    description: "Improves bow mechanics with crosshair and better damage calculations",
    author: "ishid4",
    downloads: 2_200_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/ishid4/BetterArchery/",
    provider: "thunderstore",
  },
  {
    id: "blaxxun-Groups",
    slug: "Groups",
    title: "Groups",
    description: "Adds player groups and cross-server communication features",
    author: "blaxxun",
    downloads: 1_500_000,
    pageUrl: "https://thunderstore.io/c/valheim/p/blaxxun/Groups/",
    provider: "thunderstore",
  },
];

// Author/downloads values are pinned to assets/design-export/json/tY6RD.json's
// "Card <title>" nodes (Server Detail — Modpacks) so the screenshot capture
// (slice2b.spec.ts's tY6RD test) matches the design frame's copy exactly —
// title/description/id/slug/pageUrl/provider are unaffected.
export const screenshotModrinthProjects: RegistryProject[] = [
  {
    id: "fabulously-optimized",
    slug: "fabulously-optimized",
    title: "Fabulously Optimized",
    description: "Fast, beautiful, and convenient modpack for Minecraft Java Edition",
    author: "robotkoer",
    downloads: 14_200_000,
    pageUrl: "https://modrinth.com/modpack/fabulously-optimized",
    provider: "modrinth",
  },
  {
    id: "vanilla-perfected",
    slug: "vanilla-perfected",
    title: "Vanilla Perfected",
    description: "Enhanced vanilla experience with quality-of-life improvements",
    author: "demonjoeTV",
    downloads: 2_000_000,
    pageUrl: "https://modrinth.com/modpack/vanilla-perfected",
    provider: "modrinth",
  },
  {
    id: "optifabric",
    slug: "optifabric",
    title: "OptiFabric",
    description: "Use OptiFine shaders with Fabric mods",
    author: "Creamer",
    downloads: 1_500_000,
    pageUrl: "https://modrinth.com/modpack/optifabric",
    provider: "modrinth",
  },
  {
    id: "remarkably-optimized",
    slug: "remarkably-optimized",
    title: "Remarkably Optimized",
    description: "Optimized performance with essential mods",
    author: "ThatOneJay",
    downloads: 1_100_000,
    pageUrl: "https://modrinth.com/modpack/remarkably-optimized",
    provider: "modrinth",
  },
  {
    id: "fps-modpack",
    slug: "fps-modpack",
    title: "FPS Modpack",
    description: "Maximize performance with optimization mods",
    author: "MIRRONAKE",
    downloads: 1_100_000,
    pageUrl: "https://modrinth.com/modpack/fps-modpack",
    provider: "modrinth",
  },
  {
    id: "fresh-smooth",
    slug: "fresh-smooth",
    title: "Fresh & Smooth",
    description: "Balanced modpack for smooth gameplay",
    author: "EndarPacks",
    downloads: 864_300,
    pageUrl: "https://modrinth.com/modpack/fresh-smooth",
    provider: "modrinth",
  },
];

// ============================================================================
// Helper for tests/e2e to conditionally swap handler sets
// ============================================================================

// configSchema served ONLY by the single-template GET (see handlers.ts), so
// Settings > Game configuration renders (design frames OC804/QFEg9) while the
// Create wizard's template list — whose captures have no "Template
// configuration" block — stays unchanged.
export const screenshotTemplateConfigSchemas: Record<
  string,
  NonNullable<GameTemplate["spec"]["configSchema"]>
> = {
  "minecraft-java": [
    { name: "WORLD_NAME", displayName: "World name", type: "string", default: "Gameplane" },
    { name: "DIFFICULTY", displayName: "Difficulty", type: "enum", enum: ["Classic", "Expert", "Master", "Journey"], default: "Classic" },
    { name: "MAX_PLAYERS", displayName: "Max players", type: "int", default: "16", min: 1, max: 255 },
    { name: "PVP", displayName: "PvP", type: "bool", default: "true" },
    { name: "SERVER_PASSWORD", displayName: "Server password", type: "password" },
    { name: "MOTD", displayName: "MOTD", type: "string", target: "file" },
  ],
};

export function getScreenshotData() {
  return {
    templates: screenshotTemplates,
    templateConfigSchemas: screenshotTemplateConfigSchemas,
    servers: screenshotServers,
    nodes: screenshotNodes,
    clusterView: screenshotClusterView,
    clusterStats: screenshotClusterStats,
    events: screenshotEvents,
    auditEvents: screenshotAuditEvents,
    users: screenshotUsers,
    schedules: screenshotSchedules,
    restores: screenshotRestores,
    config: screenshotConfig,
    logLines: screenshotLogLines,
    installedMods: screenshotInstalledMods,
    registryProjects: screenshotRegistryProjects,
  };
}
