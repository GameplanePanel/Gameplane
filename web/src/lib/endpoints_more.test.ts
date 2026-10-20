import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  Auth,
  AuthProviders,
  BackupDestinations,
  Backups,
  Cluster,
  Logs,
  ModRegistries,
  Modules,
  ModuleSources,
  Notifications,
  Players,
  Restores,
  Roles,
  Schedules,
  Servers,
  Templates,
  Users,
  Audit,
} from "./endpoints";
import type { ModID } from "@/types";

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
  fetchMock.mockImplementation(
    async () =>
      new Response(JSON.stringify({ items: [], spec: {} }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
  );
});

afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

interface FetchInit {
  method?: string;
  body?: BodyInit;
}

function last(): { url: string; init: FetchInit } {
  const c = fetchMock.mock.calls.at(-1);
  expect(c).toBeDefined();
  return { url: c![0] as string, init: (c![1] ?? {}) as FetchInit };
}

// Asserts the most recent fetch hit `url` with `method` (GET when omitted).
async function expectCall(p: Promise<unknown>, url: string, method = "GET") {
  await p;
  const c = last();
  expect(c.url).toBe(url);
  expect(c.init.method ?? "GET").toBe(method);
}

describe("Servers endpoints (uncovered)", () => {
  it("get / update / remove", async () => {
    await expectCall(Servers.get("s1"), "/servers/s1");
    await expectCall(Servers.update("s1", {} as never), "/servers/s1", "PUT");
    await expectCall(Servers.remove("s1"), "/servers/s1", "DELETE");
  });
  it("wipeData / transfer carry confirm/userId bodies", async () => {
    await Servers.wipeData("s1", "s1");
    expect(last().url).toBe("/servers/s1:wipe-data");
    expect(last().init.body).toBe(JSON.stringify({ confirm: "s1" }));
    await Servers.transfer("s1", 7);
    expect(last().url).toBe("/servers/s1:transfer");
    expect(last().init.body).toBe(JSON.stringify({ userId: 7 }));
  });
  it("status / runAction / mods family", async () => {
    await expectCall(Servers.status("s1"), "/servers/s1/status");
    await expectCall(Servers.runAction("s1", { id: "save" }), "/servers/s1/actions/run", "POST");
    await expectCall(Servers.mods("s1"), "/servers/s1/mods");
    await expectCall(Servers.installMod("s1", { url: "u" }), "/servers/s1/mods/install", "POST");
    await expectCall(Servers.removeMod("s1", "a b"), "/servers/s1/mods?name=a%20b", "DELETE");
  });
});

describe("Templates / Cluster", () => {
  it("Templates.list", async () => {
    await expectCall(Templates.list(), "/templates");
  });
  it("Cluster info / stats / view / addNode", async () => {
    await expectCall(Cluster.info(), "/cluster/info");
    await expectCall(Cluster.stats(), "/cluster/stats");
    await expectCall(Cluster.view(), "/cluster");
    await expectCall(Cluster.addNode(), "/cluster/nodes:join", "POST");
  });
  it("Cluster.kubeconfig POSTs and returns a blob", async () => {
    fetchMock.mockImplementation(async () => new Response("kubeconfig-bytes", { status: 200 }));
    const blob = await Cluster.kubeconfig();
    // (instanceof Blob is unreliable across the undici/jsdom realm boundary;
    // assert on the content instead.)
    expect(await blob.text()).toBe("kubeconfig-bytes");
    expect(last().url).toBe("/cluster/kubeconfig");
    expect(last().init.method).toBe("POST");
  });
});

describe("Backups / Schedules / Restores / Destinations", () => {
  it("Backups list/get/create/remove", async () => {
    await expectCall(Backups.list(), "/backups");
    await expectCall(Backups.get("b1"), "/backups/b1");
    await expectCall(Backups.create({ serverRef: { name: "s1" } }), "/backups", "POST");
    await expectCall(Backups.remove("b1"), "/backups/b1", "DELETE");
  });
  it("Schedules list/create/remove and patchSpec read-modify-write", async () => {
    await expectCall(Schedules.list(), "/schedules");
    await expectCall(
      Schedules.create({ serverRef: { name: "s1" }, schedule: "0 3 * * *", repoRef: { name: "r", key: "repo" } }),
      "/schedules",
      "POST",
    );
    // patchSpec GETs then PUTs; the last call is the PUT.
    await expectCall(Schedules.patchSpec("sc1", { suspend: true }), "/schedules/sc1", "PUT");
    await expectCall(Schedules.remove("sc1"), "/schedules/sc1", "DELETE");
  });
  it("Restores list/create", async () => {
    await expectCall(Restores.list(), "/restores");
    await expectCall(
      Restores.create({ backupRef: { name: "b1" }, serverRef: { name: "s1" } }),
      "/restores",
      "POST",
    );
  });
  it("BackupDestinations list/upsert/remove", async () => {
    await expectCall(BackupDestinations.list(), "/backup-destinations");
    await expectCall(
      BackupDestinations.upsert({ name: "d1", url: "s3:x", password: "pw" }),
      "/backup-destinations",
      "POST",
    );
    await expectCall(BackupDestinations.remove("d1"), "/backup-destinations/d1", "DELETE");
  });
});

describe("Users / Roles / Auth", () => {
  it("Users me/list/create/update/resetPassword/bindings", async () => {
    await expectCall(Users.me(), "/users/me");
    await expectCall(Users.list(), "/users");
    await expectCall(Users.create({ username: "u", role: "viewer" }), "/users", "POST");
    await expectCall(Users.update(3, { role: "operator" }), "/users/3", "PATCH");
    await expectCall(Users.resetPassword(3, "longenoughpw1"), "/users/3/reset-password", "POST");
    await expectCall(Users.bindings(3), "/users/3/bindings");
    await expectCall(
      Users.addBinding(3, { roleName: "operator", namespace: "team-a" }),
      "/users/3/bindings",
      "POST",
    );
    await expectCall(Users.removeBinding(3, "operator", "team-a"), "/users/3/bindings/operator/team-a?cluster=local", "DELETE");
    await expectCall(Users.removeBinding(3, "cluster-reader", "*", "remote-east"), "/users/3/bindings/cluster-reader/*?cluster=remote-east", "DELETE");
  });
  it("Roles list/catalog/create/update/remove", async () => {
    await expectCall(Roles.list(), "/roles");
    await expectCall(Roles.catalog(), "/roles/permissions");
    await expectCall(Roles.create({ name: "support", permissions: ["servers:read"] }), "/roles", "POST");
    await expectCall(Roles.update("support", { permissions: [] }), "/roles/support", "PATCH");
    await expectCall(Roles.remove("support"), "/roles/support", "DELETE");
  });
  it("Auth logout / providers / oidcStartURL", async () => {
    await expectCall(Auth.logout(), "/auth/logout", "POST");
    await expectCall(Auth.providers(), "/auth/providers");
    expect(Auth.oidcStartURL()).toBe("/auth/oidc/start");
  });
});

describe("Modules / ModuleSources / Logs", () => {
  it("Modules catalog/list/install/upgrade/uninstall", async () => {
    await expectCall(Modules.catalog(), "/modules/catalog");
    await expectCall(Modules.list(), "/modules");
    await expectCall(Modules.install({ source: "up", module: "mc" }), "/modules", "POST");
    await expectCall(Modules.upgrade("m1", "1.2"), "/modules/m1", "PATCH");
    await expectCall(Modules.uninstall("m1"), "/modules/m1", "DELETE");
  });
  it("ModuleSources list/create/update/remove/removeUpload", async () => {
    await expectCall(ModuleSources.list(), "/modules/sources");
    await expectCall(
      ModuleSources.create("up", { type: "oci" } as never),
      "/modules/sources",
      "POST",
    );
    await expectCall(ModuleSources.update("up", { type: "oci" } as never), "/modules/sources/up", "PUT");
    await expectCall(ModuleSources.remove("up"), "/modules/sources/up", "DELETE");
    await expectCall(ModuleSources.removeUpload("up", "mc"), "/modules/sources/up/upload/mc", "DELETE");
  });
  it("ModuleSources.upload POSTs the bundle (dry-run query)", async () => {
    const file = new Blob(["bundle"]);
    await ModuleSources.upload("up", file, { dryRun: true });
    expect(last().url).toBe("/modules/sources/up/upload?dryRun=true");
    expect(last().init.method).toBe("POST");
    await ModuleSources.upload("up", file);
    expect(last().url).toBe("/modules/sources/up/upload");
  });
  it("Logs stream paths are server-encoded", () => {
    expect(Logs.fileStreamPath("a b")).toBe("/ws/servers/a%20b/logs?tail=500");
    expect(Logs.podStreamPath("a b")).toBe("/ws/servers/a%20b/logs/pod?from=start");
  });
});

describe("Servers mods endpoints (uncovered branches)", () => {
  it("modUpdates GETs the update check endpoint", async () => {
    await expectCall(Servers.modUpdates("s1"), "/servers/s1/mods/updates");
  });

  it("modVersions with provider includes provider query param", async () => {
    await Servers.modVersions("s1", "project1", "modrinth");
    expect(last().url).toBe(
      "/servers/s1/mods/registry/projects/project1/versions?provider=modrinth"
    );
  });

  it("modVersions without provider omits provider query param", async () => {
    await Servers.modVersions("s1", "project1");
    expect(last().url).toBe("/servers/s1/mods/registry/projects/project1/versions");
  });

  it("modpackDeps with provider includes provider query param", async () => {
    await Servers.modpackDeps("s1", "packname", "curseforge");
    expect(last().url).toBe(
      "/servers/s1/mods/registry/projects/packname/modpack?provider=curseforge"
    );
  });

  it("modpackDeps without provider omits provider query param", async () => {
    await Servers.modpackDeps("s1", "packname");
    expect(last().url).toBe("/servers/s1/mods/registry/projects/packname/modpack");
  });

  it("installModpack with provider includes provider query param", async () => {
    await Servers.installModpack("s1", { ref: "1.0" }, "modrinth");
    expect(last().url).toBe("/servers/s1/modpack?provider=modrinth");
    expect(last().init.method).toBe("POST");
  });

  it("installModpack without provider omits provider query param", async () => {
    await Servers.installModpack("s1", { ref: "1.0" });
    expect(last().url).toBe("/servers/s1/modpack");
    expect(last().init.method).toBe("POST");
  });

  it("modIDs GETs the ids list endpoint", async () => {
    await expectCall(Servers.modIDs("s1"), "/servers/s1/mods/ids");
  });

  it("setModIDs PUTs the ids list", async () => {
    const ids: ModID[] = [{ id: "123" }, { id: "456" }];
    await expectCall(Servers.setModIDs("s1", ids), "/servers/s1/mods/ids", "PUT");
  });

  it("registryProviders GETs the providers endpoint", async () => {
    await expectCall(Servers.registryProviders("s1"), "/servers/s1/mods/registry/providers");
  });

  it("getMyServers GETs /users/me/servers", async () => {
    await expectCall(Servers.getMyServers(), "/users/me/servers");
  });

  it("setCollaborators PUTs to the collaborators endpoint with ns", async () => {
    await Servers.setCollaborators("s1", "games", { userIds: [1, 2] });
    expect(last().url).toBe("/servers/s1:collaborators?namespace=games");
    expect(last().init.method).toBe("PUT");
  });

  it("events GETs the server events", async () => {
    await expectCall(Servers.events("s1"), "/servers/s1/events");
  });

  it("searchRegistry encodes project names", async () => {
    fetchMock.mockImplementation(async () =>
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    await Servers.searchRegistry("s1", { q: "my mod" });
    // URLSearchParams encodes spaces as +, which is valid for query parameters
    expect(last().url).toContain("q=my+mod");
  });

  it("searchRegistry respects all optional parameters", async () => {
    await Servers.searchRegistry("s1", {
      q: "fabric",
      provider: "modrinth",
      type: "mod",
      sort: "popularity",
      category: "magic",
      limit: 50,
      offset: 10,
    });
    const url = last().url;
    expect(url).toContain("q=fabric");
    expect(url).toContain("provider=modrinth");
    expect(url).toContain("type=mod");
    expect(url).toContain("sort=popularity");
    expect(url).toContain("category=magic");
    expect(url).toContain("limit=50");
    expect(url).toContain("offset=10");
  });

  it("searchRegistry with default limit only includes limit=24 when not specified", async () => {
    await Servers.searchRegistry("s1", {});
    expect(last().url).toContain("limit=24");
  });
});

describe("Servers mods namespace variants", () => {
  it("modUpdates with namespace appends &namespace=", async () => {
    await Servers.modUpdates("s1", "team-a");
    expect(last().url).toBe("/servers/s1/mods/updates?namespace=team-a");
  });

  it("installMod with namespace appends &namespace=", async () => {
    await expectCall(
      Servers.installMod("s1", { url: "http://example.com/mod.jar" }, "team-a"),
      "/servers/s1/mods/install?namespace=team-a",
      "POST"
    );
  });

  it("registryProviders with namespace appends &namespace=", async () => {
    await Servers.registryProviders("s1", "team-a");
    expect(last().url).toBe("/servers/s1/mods/registry/providers?namespace=team-a");
  });

  it("modIDs with namespace appends &namespace=", async () => {
    await Servers.modIDs("s1", "team-a");
    expect(last().url).toBe("/servers/s1/mods/ids?namespace=team-a");
  });
});

describe("Auth.oidcStartURL variants", () => {
  it("returns legacy path when no name provided", () => {
    expect(Auth.oidcStartURL()).toBe("/auth/oidc/start");
  });

  it("returns legacy path for 'helm' provider name", () => {
    expect(Auth.oidcStartURL("helm")).toBe("/auth/oidc/start");
  });

  it("returns provider-specific path when name is non-helm", () => {
    expect(Auth.oidcStartURL("okta")).toBe("/auth/oidc/okta/start");
    expect(Auth.oidcStartURL("azure")).toBe("/auth/oidc/azure/start");
  });

  it("URL-encodes provider names", () => {
    expect(Auth.oidcStartURL("provider@example")).toBe("/auth/oidc/provider%40example/start");
  });
});

describe("Audit export filters", () => {
  it("exportCsv with no filters includes format param only", async () => {
    fetchMock.mockImplementation(
      async () => new Response("test", { status: 200, headers: { "Content-Type": "text/csv" } })
    );
    await Audit.exportCsv({});
    expect(last().url).toBe("/admin/audit/export?format=csv");
  });

  it("exportCsv with all filters includes all params", async () => {
    fetchMock.mockImplementation(
      async () => new Response("test", { status: 200, headers: { "Content-Type": "text/csv" } })
    );
    await Audit.exportCsv({ actor: "admin", method: "POST", status: "200" });
    const url = last().url;
    expect(url).toContain("format=csv");
    expect(url).toContain("actor=admin");
    expect(url).toContain("method=POST");
    expect(url).toContain("status=200");
  });

  it("exportCsv omits empty filter values", async () => {
    fetchMock.mockImplementation(
      async () => new Response("test", { status: 200, headers: { "Content-Type": "text/csv" } })
    );
    await Audit.exportCsv({ actor: "alice", method: "", status: "404" });
    const url = last().url;
    expect(url).toContain("actor=alice");
    expect(url).toContain("status=404");
    expect(url).not.toContain("method=");
  });
});

describe("Players moderate action branches", () => {
  it("moderate kick action", async () => {
    await Players.moderate("s1", "kick", { name: "player1", reason: "spam" });
    expect(last().url).toBe("/servers/s1/players/kick");
    expect(last().init.method).toBe("POST");
  });

  it("moderate ban action", async () => {
    await Players.moderate("s1", "ban", { name: "player1", reason: "griefing" });
    expect(last().url).toBe("/servers/s1/players/ban");
    expect(last().init.method).toBe("POST");
  });

  it("moderate unban action", async () => {
    await Players.moderate("s1", "unban", { name: "player1" });
    expect(last().url).toBe("/servers/s1/players/unban");
    expect(last().init.method).toBe("POST");
  });

  it("whitelist GETs the list", async () => {
    await expectCall(Players.whitelist("s1"), "/servers/s1/players/whitelist");
  });

  it("whitelistAdd POSTs a player name", async () => {
    await Players.whitelistAdd("s1", "newplayer");
    expect(last().url).toBe("/servers/s1/players/whitelist/add");
    expect(last().init.method).toBe("POST");
  });

  it("whitelistRemove POSTs to remove endpoint", async () => {
    await Players.whitelistRemove("s1", "oldplayer");
    expect(last().url).toBe("/servers/s1/players/whitelist/remove");
    expect(last().init.method).toBe("POST");
  });

  it("whitelist with namespace appends &namespace=", async () => {
    await Players.whitelist("s1", "team-ns");
    expect(last().url).toBe("/servers/s1/players/whitelist?namespace=team-ns");
  });

  it("whitelistAdd with namespace appends &namespace=", async () => {
    await Players.whitelistAdd("s1", "player1", "team-ns");
    expect(last().url).toBe("/servers/s1/players/whitelist/add?namespace=team-ns");
  });
});

describe("AuthProviders and Notifications secrets", () => {
  it("AuthProviders.putSecret encodes provider name", async () => {
    await AuthProviders.putSecret("oidc@example", { clientSecret: "secret123" });
    expect(last().url).toContain("/admin/auth/providers/oidc%40example/secret");
    expect(last().init.method).toBe("PUT");
  });

  it("AuthProviders.deleteSecret encodes provider name", async () => {
    await AuthProviders.deleteSecret("oidc@example");
    expect(last().url).toContain("/admin/auth/providers/oidc%40example/secret");
    expect(last().init.method).toBe("DELETE");
  });

  it("Notifications.putSecret encodes sink name", async () => {
    await Notifications.putSecret("discord-alerts", { kind: "discord", url: "https://webhook" });
    expect(last().url).toContain("/admin/notifications/sinks/discord-alerts/secret");
    expect(last().init.method).toBe("PUT");
  });

  it("Notifications.deleteSecret encodes sink name", async () => {
    await Notifications.deleteSecret("discord-alerts");
    expect(last().url).toContain("/admin/notifications/sinks/discord-alerts/secret");
    expect(last().init.method).toBe("DELETE");
  });

  it("Notifications.test encodes sink name", async () => {
    await Notifications.test("slack-notifier");
    expect(last().url).toContain("/admin/notifications/sinks/slack-notifier/test");
    expect(last().init.method).toBe("POST");
  });
});

describe("ModRegistries secrets", () => {
  it("ModRegistries.putSecret encodes provider", async () => {
    await ModRegistries.putSecret("curseforge", "api-key-123");
    expect(last().url).toBe("/admin/registries/curseforge/secret");
    expect(last().init.method).toBe("PUT");
  });

  it("ModRegistries.deleteSecret encodes provider", async () => {
    await ModRegistries.deleteSecret("nexus");
    expect(last().url).toBe("/admin/registries/nexus/secret");
    expect(last().init.method).toBe("DELETE");
  });
});

describe("Language endpoints (spec 019)", () => {
  it("Users.setLanguage sends a PUT with the tag", async () => {
    await expectCall(Users.setLanguage("fr"), "/users/me/language", "PUT");
    expect(last().init.body).toBe(JSON.stringify({ language: "fr" }));
  });

  it("Users.setLanguage(null) clears the saved choice", async () => {
    await expectCall(Users.setLanguage(null), "/users/me/language", "PUT");
    expect(last().init.body).toBe(JSON.stringify({ language: null }));
  });

  it("Auth.locale reads the public instance default", async () => {
    await expectCall(Auth.locale(), "/auth/locale");
  });
});
