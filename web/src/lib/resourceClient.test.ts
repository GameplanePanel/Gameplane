import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createRequestClient, api } from "./api";
import { createResourceClient } from "./endpoints";
import { setCurrentCluster } from "./cluster";

const fetchMock = vi.fn();
const json = (body: unknown = {}) => new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });
beforeEach(() => { vi.stubGlobal("fetch", fetchMock); fetchMock.mockImplementation(() => Promise.resolve(json())); });
afterEach(() => { vi.unstubAllGlobals(); fetchMock.mockReset(); setCurrentCluster("local"); });
const path = (index = -1) => new URL(fetchMock.mock.calls.at(index)![0], "http://test");

describe("immutable resource transport", () => {
  it("pins same-name reads and writes to independent cluster/namespace clients", async () => {
    const scope = { cluster: "remote-a", namespace: "games-a" };
    const first = createResourceClient(scope);
    const second = createResourceClient({ cluster: "remote-b", namespace: "games-b" });
    scope.cluster = "changed"; scope.namespace = "changed";
    setCurrentCluster("unrelated");
    await first.Servers.get("same");
    expect(path().searchParams.get("cluster")).toBe("remote-a");
    expect(path().searchParams.get("namespace")).toBe("games-a");
    await second.Servers.lifecycle("same", "start");
    expect(path().searchParams.get("cluster")).toBe("remote-b");
    expect(path().searchParams.get("namespace")).toBe("games-b");
    await first.Servers.create({ name: "new", templateRef: { name: "minecraft" } });
    expect(path().searchParams.get("namespace")).toBe("games-a");
  });
  it("retains a captured namespace through an asynchronous read-modify-write", async () => {
    let resolveRead!: (response: Response) => void;
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { resolveRead = resolve; }));
    const client = createResourceClient({ cluster: "remote-a", namespace: "games-a" });
    const pending = client.Schedules.patchSpec("daily", { suspend: true });
    setCurrentCluster("remote-b");
    resolveRead(json({ metadata: { name: "daily", namespace: "games-a" }, spec: { schedule: "0 * * * *" } }));
    await pending;
    expect(fetchMock).toHaveBeenCalledTimes(2);
    for (const [url] of fetchMock.mock.calls) {
      const parsed = new URL(url, "http://test");
      expect(parsed.searchParams.get("cluster")).toBe("remote-a");
      expect(parsed.searchParams.get("namespace")).toBe("games-a");
    }
    expect(fetchMock.mock.lastCall![1].method).toBe("PUT");
  });
  it("keeps legacy local calls independent of remembered infrastructure selection", async () => {
    setCurrentCluster("remote-a");
    await api("/servers/same");
    expect(path().searchParams.has("cluster")).toBe(false);
    const client = createRequestClient({ cluster: "local", namespace: "games" });
    expect(client.url("/servers/same")).toBe("/servers/same?namespace=games");
  });
  it("rejects conflicting or duplicated selectors before any request", async () => {
    const client = createRequestClient({ cluster: "a", namespace: "games" });
    expect(() => createRequestClient({ cluster: "" })).toThrow();
    for (const url of ["https://elsewhere/servers", "//elsewhere/servers", "/servers/x?cluster=b", "/servers/x?cluster=a&cluster=a", "/servers/x?namespace=other", "/servers/x?namespace=games&namespace=games"]) {
      expect(() => client.url(url)).toThrow();
    }
    expect(() => client.api("/servers/x", { cluster: "b" })).toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(client.url("/servers/x?cluster=a&namespace=games")).toBe("/servers/x?cluster=a&namespace=games");
  });
  it("preserves explicit administration assignment targets and aggregate filters", async () => {
    const client = createRequestClient({ cluster: "site", namespace: "games" });
    for (const url of ["/fleet/servers", "/fleet/backups?cluster=filter", "/admin/config", "/roles", "/clusters", "/shares/token", "/users/4/bindings/reader/*?cluster=assignment"]) {
      expect(client.url(url)).toBe(url);
      await client.api(url);
      expect(fetchMock.mock.lastCall![0]).toBe(url);
    }
    expect(client.url("/cluster")).toBe("/cluster?cluster=site");
    expect(client.url("/modules-other")).toBe("/modules-other?cluster=site");
  });
  it("scopes module catalogs to explicit workload targets and preserves the local default", async () => {
    setCurrentCluster("unrelated");
    const client = createRequestClient({ cluster: "site", namespace: "games" });
    expect(client.url("/modules/catalog")).toBe("/modules/catalog?cluster=site");
    await client.api("/modules/catalog");
    expect(fetchMock.mock.lastCall![0]).toBe("/modules/catalog?cluster=site");
    const local = createRequestClient({ cluster: "local", namespace: "games" });
    expect(local.url("/modules/catalog")).toBe("/modules/catalog");
    await api("/modules/catalog");
    expect(fetchMock.mock.lastCall![0]).toBe("/modules/catalog");
  });
  it("cancels JSON and raw reads and scopes stream/download paths", async () => {
    const controller = new AbortController();
    const client = createResourceClient({ cluster: "site", namespace: "games" }).withSignal(controller.signal);
    await client.Servers.get("same");
    expect(fetchMock.mock.lastCall![1].signal).toBe(controller.signal);
    fetchMock.mockResolvedValueOnce(new Response("contents"));
    expect(await client.Files.read("same", "site.txt")).toBe("contents");
    expect(fetchMock.mock.lastCall![1].signal).toBe(controller.signal);
    for (const url of [client.Files.downloadURL("same", "site.txt"), client.Logs.fileStreamPath("same"), client.Logs.podStreamPath("same"), client.Logs.downloadURL("same")]) {
      const parsed = new URL(url, "http://test");
      expect(parsed.searchParams.get("cluster")).toBe("site");
      expect(parsed.searchParams.get("namespace")).toBe("games");
    }
    fetchMock.mockResolvedValueOnce(new Response("capture"));
    await client.Captures.download("same", "capture-1");
    expect(fetchMock.mock.lastCall![1].signal).toBe(controller.signal);
    controller.abort();
    expect(fetchMock.mock.lastCall![1].signal.aborted).toBe(true);
  });
  it("combines caller cancellation with the resource lifetime", async () => {
    const lifetime = new AbortController(), request = new AbortController();
    const client = createRequestClient({ cluster: "site" }, lifetime.signal);
    await client.api("/servers/same", { signal: request.signal });
    const signal = fetchMock.mock.lastCall![1].signal as AbortSignal;
    request.abort(); expect(signal.aborted).toBe(true);
    const raw = new AbortController();
    await client.raw("/servers/same/files", { signal: raw.signal });
    lifetime.abort(); expect(fetchMock.mock.lastCall![1].signal.aborted).toBe(true);
  });
});
