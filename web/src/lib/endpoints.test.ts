import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Audit, Auth, Clusters, Files, Logs, Players, Servers, Templates, Users, createResourceClient } from "./endpoints";

// Mock the cluster module so we don't depend on actual localStorage in tests
vi.mock("./cluster", () => ({
  getCurrentCluster: vi.fn(() => "local"),
  setCurrentCluster: vi.fn(),
  subscribeCluster: vi.fn(),
  useCurrentCluster: vi.fn(),
}));

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
  fetchMock.mockImplementation(
    async () =>
      new Response(JSON.stringify({ items: [] }), {
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
  headers?: Record<string, string>;
  body?: BodyInit;
}

function called(): { url: string; init: FetchInit } {
  const last = fetchMock.mock.calls.at(-1);
  expect(last).toBeDefined();
  return { url: last![0] as string, init: last![1] as FetchInit };
}

describe("endpoints", () => {
  it("Servers.list hits /servers GET", async () => {
    await Servers.list();
    expect(called().url).toBe("/servers");
    expect(called().init.method).toBe("GET");
  });

  it("Servers.lifecycle hits the verb path with POST", async () => {
    await Servers.lifecycle("mc-survival", "restart");
    expect(called().url).toBe("/servers/mc-survival:restart");
    expect(called().init.method).toBe("POST");
  });

  it("Servers.clone POSTs the new name to the clone verb path", async () => {
    await Servers.clone("mc-survival", "mc-survival-copy");
    const c = called();
    expect(c.url).toBe("/servers/mc-survival:clone");
    expect(c.init.method).toBe("POST");
    expect(c.init.body).toBe(JSON.stringify({ newName: "mc-survival-copy" }));
  });

  it("Servers.create POSTs JSON body", async () => {
    await Servers.create({
      name: "x",
      templateRef: { name: "minecraft-java" },
    });
    const c = called();
    expect(c.url).toBe("/servers");
    expect(c.init.method).toBe("POST");
    expect(typeof c.init.body).toBe("string");
  });

  it("Templates.get encodes the name", async () => {
    await Templates.get("valheim");
    expect(called().url).toBe("/templates/valheim");
  });

  it("Players endpoints carry server name and action", async () => {
    await Players.snapshot("s1");
    expect(called().url).toBe("/servers/s1/players");
    await Players.banned("s1");
    expect(called().url).toBe("/servers/s1/players/banned");
    await Players.moderate("s1", "kick", { name: "alice" });
    expect(called().url).toBe("/servers/s1/players/kick");
    expect(called().init.method).toBe("POST");
  });

  it("Audit.page sends limit and before in querystring", async () => {
    await Audit.page(50, 0);
    expect(called().url).toBe("/admin/audit?limit=50");
    await Audit.page(50, 999);
    expect(called().url).toBe("/admin/audit?limit=50&before=999");
  });

  it("Auth.login POSTs to /auth/login", async () => {
    await Auth.login({ username: "u", password: "p" });
    expect(called().url).toBe("/auth/login");
    expect(called().init.method).toBe("POST");
  });

  it("Users.remove DELETEs by id", async () => {
    await Users.remove(42);
    expect(called().url).toBe("/users/42");
    expect(called().init.method).toBe("DELETE");
  });

  describe("Files", () => {
    it("list GETs /files/list with path query", async () => {
      fetchMock.mockImplementation(
        async () =>
          new Response(JSON.stringify([]), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      );
      await Files.list("mc-survival", "/world");
      expect(called().url).toBe(
        "/servers/mc-survival/files/list?path=%2Fworld",
      );
      expect(called().init.method ?? "GET").toBe("GET");
    });

    it("read GETs /files/read and returns the raw body as text", async () => {
      fetchMock.mockImplementation(
        async () =>
          new Response("hello\n", {
            status: 200,
            headers: { "Content-Type": "text/plain" },
          }),
      );
      const text = await Files.read("mc-survival", "/server.properties");
      expect(text).toBe("hello\n");
      expect(called().url).toBe(
        "/servers/mc-survival/files/read?path=%2Fserver.properties",
      );
    });

    it("write POSTs octet-stream body to /files/write", async () => {
      fetchMock.mockImplementation(async () => new Response(null, { status: 204 }));
      await Files.write("mc-survival", "/server.properties", "k=v\n");
      const c = called();
      expect(c.url).toBe(
        "/servers/mc-survival/files/write?path=%2Fserver.properties",
      );
      expect(c.init.method).toBe("POST");
      expect(c.init.headers?.["Content-Type"]).toBe("application/octet-stream");
      expect(c.init.body).toBe("k=v\n");
    });

    it("create POSTs encoded octet-stream content with namespace and credentials", async () => {
      fetchMock.mockImplementation(async () => new Response(null, { status: 204 }));
      await Files.create("mc-survival", "/new #?.txt", "", "team-c");
      const c = called();
      expect(c.url).toBe("/servers/mc-survival/files/create?path=%2Fnew%20%23%3F.txt&namespace=team-c");
      expect(c.init.method).toBe("POST");
      expect(c.init.headers?.["Content-Type"]).toBe("application/octet-stream");
      expect(c.init.body).toBe("");
      expect(fetchMock.mock.calls.at(-1)?.[1]?.credentials).toBe("include");
    });

    it.each([409, 404])("create fails closed on %s without retrying write", async (status) => {
      fetchMock.mockImplementation(async () => new Response("create failed", { status }));
      await expect(Files.create("mc-survival", "/existing.txt", "")).rejects.toThrow("create failed");
      expect(fetchMock).toHaveBeenCalledTimes(1);
      expect(called().url).toBe("/servers/mc-survival/files/create?path=%2Fexisting.txt");
    });

    it("create preserves the captured remote cluster and sends the CSRF token", async () => {
      fetchMock.mockImplementation(async () => new Response(null, { status: 204 }));
      document.cookie = "gameplane_csrf=create-token; path=/";
      try {
        const client = createResourceClient({ cluster: "remote", namespace: "team-c" });
        await client.Files.create("mc-survival", "/new.txt", "");
        expect(called().url).toBe("/servers/mc-survival/files/create?path=%2Fnew.txt&namespace=team-c&cluster=remote");
        expect(called().init.headers?.["X-Gameplane-CSRF"]).toBe("create-token");
      } finally {
        document.cookie = "gameplane_csrf=; Max-Age=0; path=/";
      }
    });

    it("mkdir POSTs to /files/mkdir with empty body", async () => {
      fetchMock.mockImplementation(async () => new Response(null, { status: 204 }));
      await Files.mkdir("mc-survival", "/mods-disabled");
      const c = called();
      expect(c.url).toBe(
        "/servers/mc-survival/files/mkdir?path=%2Fmods-disabled",
      );
      expect(c.init.method).toBe("POST");
      expect(c.init.body).toBeUndefined();
    });

    it("remove DELETEs and forwards the recursive flag for directories", async () => {
      fetchMock.mockImplementation(async () => new Response(null, { status: 204 }));
      await Files.remove("mc-survival", "/world");
      expect(called().url).toBe("/servers/mc-survival/files/delete?path=%2Fworld");
      expect(called().init.method).toBe("DELETE");

      await Files.remove("mc-survival", "/world", true);
      expect(called().url).toBe(
        "/servers/mc-survival/files/delete?path=%2Fworld&recursive=true",
      );
    });

    it("upload POSTs multipart form-data to /files/upload", async () => {
      fetchMock.mockImplementation(async () => new Response(null, { status: 204 }));
      const file = new File(["bytes"], "ops.json", { type: "application/json" });
      await Files.upload("mc-survival", "/", [file]);
      const c = called();
      expect(c.url).toBe("/servers/mc-survival/files/upload?path=%2F");
      expect(c.init.method).toBe("POST");
      expect(c.init.body).toBeInstanceOf(FormData);
    });

    it("downloadURL builds a path-encoded GET URL", () => {
      expect(Files.downloadURL("mc-survival", "/world/level.dat")).toBe(
        "/servers/mc-survival/files/download?path=%2Fworld%2Flevel.dat",
      );
    });

    it("list appends namespace param last when ns is set", async () => {
      fetchMock.mockImplementation(
        async () =>
          new Response(JSON.stringify([]), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      );
      await Files.list("mc-survival", "/", "team-a");
      const url = called().url;
      expect(url).toBe("/servers/mc-survival/files/list?path=%2F&namespace=team-a");
      expect(url).not.toContain("files?namespace=");
    });

    it("read appends namespace param last when ns is set", async () => {
      fetchMock.mockImplementation(
        async () =>
          new Response("content", {
            status: 200,
            headers: { "Content-Type": "text/plain" },
          }),
      );
      await Files.read("mc-survival", "/config.yml", "team-b");
      const url = called().url;
      expect(url).toBe("/servers/mc-survival/files/read?path=%2Fconfig.yml&namespace=team-b");
      expect(url).not.toContain("files?namespace=");
    });

    it("write appends namespace param last when ns is set", async () => {
      fetchMock.mockImplementation(async () => new Response(null, { status: 204 }));
      await Files.write("mc-survival", "/server.properties", "k=v", "team-c");
      const url = called().url;
      expect(url).toBe("/servers/mc-survival/files/write?path=%2Fserver.properties&namespace=team-c");
      expect(url).not.toContain("files?namespace=");
    });

    it("mkdir appends namespace param last when ns is set", async () => {
      fetchMock.mockImplementation(async () => new Response(null, { status: 204 }));
      await Files.mkdir("mc-survival", "/mods", "team-d");
      const url = called().url;
      expect(url).toBe("/servers/mc-survival/files/mkdir?path=%2Fmods&namespace=team-d");
      expect(url).not.toContain("files?namespace=");
    });

    it("remove appends namespace param last when ns is set", async () => {
      fetchMock.mockImplementation(async () => new Response(null, { status: 204 }));
      await Files.remove("mc-survival", "/temp", false, "team-e");
      const url = called().url;
      expect(url).toBe("/servers/mc-survival/files/delete?path=%2Ftemp&namespace=team-e");
      expect(url).not.toContain("files?namespace=");
    });

    it("upload appends namespace param last when ns is set", async () => {
      fetchMock.mockImplementation(async () => new Response(null, { status: 204 }));
      const file = new File(["bytes"], "mod.jar", { type: "application/octet-stream" });
      await Files.upload("mc-survival", "/mods", [file], "team-f");
      const url = called().url;
      expect(url).toBe("/servers/mc-survival/files/upload?path=%2Fmods&namespace=team-f");
      expect(url).not.toContain("files?namespace=");
    });

    it("downloadURL appends namespace param last when ns is set", () => {
      const url = Files.downloadURL("mc-survival", "/world.zip", "team-g");
      expect(url).toBe("/servers/mc-survival/files/download?path=%2Fworld.zip&namespace=team-g");
      expect(url).not.toContain("files?namespace=");
    });
  });

  describe("Logs", () => {
    it("downloadURL builds a server-encoded GET URL", () => {
      expect(Logs.downloadURL("mc survival")).toBe(
        "/servers/mc%20survival/logs/download",
      );
    });
  });

  describe("withNS query-append branch", () => {
    it("removeMod appends &namespace= when ns is set on a query-bearing path", async () => {
      await Servers.removeMod("mc-survival", "MyMod", "other");
      const url = called().url;
      expect(url).toContain("/servers/mc-survival/mods");
      expect(url).toContain("?name=MyMod");
      expect(url).toContain("&namespace=other");
      expect(url).not.toContain("?namespace=");
    });

    it("removeMod excludes namespace when ns is not set", async () => {
      await Servers.removeMod("mc-survival", "MyMod");
      const url = called().url;
      expect(url).toBe("/servers/mc-survival/mods?name=MyMod");
      expect(url).not.toContain("namespace=");
    });

    it("searchRegistry appends &namespace= when ns is set on a pre-existing query string", async () => {
      await Servers.searchRegistry("mc-survival", {}, "other");
      const url = called().url;
      expect(url).toContain("/servers/mc-survival/mods/registry/search");
      expect(url).toContain("?limit=24");
      expect(url).toContain("&namespace=other");
      expect(url).not.toContain("?namespace=");
    });

    it("searchRegistry excludes namespace when ns is not set", async () => {
      await Servers.searchRegistry("mc-survival", {});
      const url = called().url;
      expect(url).toContain("/servers/mc-survival/mods/registry/search?limit=24");
      expect(url).not.toContain("namespace=");
    });
  });

  describe("Clusters", () => {
    it("list GETs /clusters", async () => {
      fetchMock.mockImplementation(
        async () =>
          new Response(JSON.stringify({ items: [] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
      );
      await Clusters.list();
      expect(called().url).toBe("/clusters");
      expect(called().init.method).toBe("GET");
    });
  });
});
