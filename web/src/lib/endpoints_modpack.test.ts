import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ModRegistries, Servers } from "./endpoints";

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
  fetchMock.mockImplementation(
    async () => new Response(JSON.stringify({}), { status: 200, headers: { "Content-Type": "application/json" } }),
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

describe("modpack client", () => {
  it("modpack GETs the server modpack view", async () => {
    await Servers.modpack("srv");
    const c = last();
    expect(c.url).toBe("/servers/srv/modpack");
    expect(c.init.method ?? "GET").toBe("GET");
  });

  it("installModpack POSTs the body", async () => {
    await Servers.installModpack("srv", { provider: "curseforge", project: "42", version: "7", backupFirst: true });
    const c = last();
    expect(c.url).toBe("/servers/srv/modpack");
    expect(c.init.method).toBe("POST");
    expect(JSON.parse(String(c.init.body))).toEqual({
      provider: "curseforge",
      project: "42",
      version: "7",
      backupFirst: true,
    });
  });

  it("removeModpack DELETEs with backupFirst in the query", async () => {
    await Servers.removeModpack("srv", true);
    const c = last();
    expect(c.url).toBe("/servers/srv/modpack?backupFirst=true");
    expect(c.init.method).toBe("DELETE");
  });

  it("modpackVersions encodes the project and asks for type=modpack", async () => {
    await Servers.modpackVersions("srv", "a/b c", "curseforge");
    expect(last().url).toBe(
      "/servers/srv/mods/registry/projects/a%2Fb%20c/versions?provider=curseforge&type=modpack",
    );
  });

  it("getShare and putShare hit the CurseForge share endpoint", async () => {
    await ModRegistries.getShare();
    expect(last().url).toBe("/admin/registries/curseforge/share");
    await ModRegistries.putShare(true);
    const c = last();
    expect(c.url).toBe("/admin/registries/curseforge/share");
    expect(c.init.method).toBe("PUT");
    expect(JSON.parse(String(c.init.body))).toEqual({ share: true });
  });
});
