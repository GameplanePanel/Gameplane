import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  api,
  APIError,
  Captures,
  Shares,
  Telemetry,
  createRequestClient,
  type CaptureStartBody,
} from "./api";
import { HttpResponse } from "msw";

// Mock the cluster module to control getCurrentCluster
vi.mock("./cluster", () => ({
  getCurrentCluster: vi.fn(() => "local"),
  setCurrentCluster: vi.fn(),
  subscribeCluster: vi.fn(),
  useCurrentCluster: vi.fn(),
}));

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
  document.cookie = "";
});

afterEach(async () => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
  // getCurrentCluster is mocked at module scope and several tests below
  // override it with mockReturnValue (not mockReturnValueOnce), which
  // persists across tests. Reset it here — globally, not just in the
  // describe block that first needed it — so a later describe block
  // (e.g. Captures.download()) that also overrides it can't leak its
  // non-"local" value into tests that never touch the cluster mock.
  const { getCurrentCluster } = await import("./cluster");
  vi.mocked(getCurrentCluster).mockReturnValue("local");
});

function jsonRes(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("api()", () => {
  it("pins a captured workload target even after the global selection changes", async () => {
    const { getCurrentCluster } = await import("./cluster");
    vi.mocked(getCurrentCluster).mockReturnValue("other-cluster");
    fetchMock.mockResolvedValue(jsonRes(200, { ok: true }));
    await api("/cluster", { cluster: "intended-cluster" });
    expect(fetchMock.mock.calls[0][0]).toBe("/cluster?cluster=intended-cluster");
    fetchMock.mockResolvedValueOnce(jsonRes(200, { ok: true }));
    await api("/servers/same:tunnel-credentials", { method: "PUT", cluster: "local", body: { provider: "frp", values: {} } });
    expect(fetchMock.mock.calls[1][0]).toBe("/servers/same:tunnel-credentials");
  });

  it("keeps central management requests independent of the workload selector", async () => {
    const { getCurrentCluster } = await import("./cluster");
    vi.mocked(getCurrentCluster).mockReturnValue("remote-prod");
    const paths = ["/auth/login", "/users/me", "/roles", "/admin/config", "/modules/catalog", "/clusters"];
    for (const path of paths) {
      fetchMock.mockResolvedValueOnce(jsonRes(200, {}));
      await api(path);
      expect(fetchMock.mock.lastCall?.[0]).toBe(path);
    }
    fetchMock.mockResolvedValueOnce(jsonRes(200, {}));
    await api("/users/2/bindings/reader/*?cluster=assigned-site", { method: "DELETE", cluster: "local" });
    expect(fetchMock.mock.lastCall?.[0]).toBe("/users/2/bindings/reader/*?cluster=assigned-site");
    fetchMock.mockResolvedValueOnce(jsonRes(200, {}));
    await api("/users/me/servers");
    expect(fetchMock.mock.lastCall?.[0]).toBe("/users/me/servers");
    fetchMock.mockResolvedValueOnce(jsonRes(200, {}));
    await api("/modules-extra");
    expect(fetchMock.mock.lastCall?.[0]).toBe("/modules-extra");
  });

  it("does not send a CSRF header on GET", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { ok: true }));
    await api("/users/me");
    const [, init] = fetchMock.mock.calls[0];
    expect(init.method).toBe("GET");
    expect(init.headers["X-Gameplane-CSRF"]).toBeUndefined();
  });

  it("sends the CSRF cookie value as header on POST", async () => {
    document.cookie = "gameplane_csrf=tok123";
    fetchMock.mockResolvedValueOnce(jsonRes(200, { ok: true }));
    await api("/auth/login", { method: "POST", body: { u: "x" } });
    const [, init] = fetchMock.mock.calls[0];
    expect(init.method).toBe("POST");
    expect(init.headers["X-Gameplane-CSRF"]).toBe("tok123");
    expect(init.body).toBe(JSON.stringify({ u: "x" }));
  });

  it("returns parsed JSON on 2xx", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { hello: "world" }));
    const r = await api<{ hello: string }>("/x");
    expect(r).toEqual({ hello: "world" });
  });

  it("returns undefined on 204", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    const r = await api("/x", { method: "DELETE" });
    expect(r).toBeUndefined();
  });

  it("throws APIError with body on 4xx", async () => {
    fetchMock.mockImplementation(
      async () => new Response("nope", { status: 401 }),
    );
    await expect(api("/users/me")).rejects.toMatchObject({
      status: 401,
      body: "nope",
    });
    await expect(api("/users/me")).rejects.toBeInstanceOf(APIError);
  });

  it("does not append cluster param when cluster is 'local'", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { ok: true }));
    await api("/servers");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers");
  });

  it("appends ?cluster= when cluster is non-local", async () => {
    const { getCurrentCluster } = await import("./cluster");
    vi.mocked(getCurrentCluster).mockReturnValueOnce("remote-prod");
    fetchMock.mockResolvedValueOnce(jsonRes(200, { ok: true }));
    await api("/servers", { cluster: "remote-prod" });
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers?cluster=remote-prod");
  });

  it("appends &cluster= when path already has query string", async () => {
    const { getCurrentCluster } = await import("./cluster");
    vi.mocked(getCurrentCluster).mockReturnValueOnce("remote-prod");
    fetchMock.mockResolvedValueOnce(jsonRes(200, { ok: true }));
    await api("/servers?namespace=games", { cluster: "remote-prod" });
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers?namespace=games&cluster=remote-prod");
  });

  it("URL-encodes the cluster id", async () => {
    const { getCurrentCluster } = await import("./cluster");
    vi.mocked(getCurrentCluster).mockReturnValueOnce("cluster@special");
    fetchMock.mockResolvedValueOnce(jsonRes(200, { ok: true }));
    await api("/servers", { cluster: "cluster@special" });
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers?cluster=cluster%40special");
  });
});

describe("withNS()", () => {
  it("returns path unchanged when namespace is undefined", async () => {
    // withNS is internal to api.ts, so we test it indirectly via Captures
    // methods that use it. Here we verify a namespace-less call.
    fetchMock.mockResolvedValueOnce(jsonRes(200, { items: [] }));
    await Captures.list("alpha");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:captures");
  });

  it("appends ?namespace= when path has no query string", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { items: [] }));
    await Captures.list("alpha", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:captures?namespace=game-ns");
  });

  it("appends &namespace= when path already has a query string", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { captureId: "cap-1" }));
    await Captures.get("alpha", "cap-123", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture?id=cap-123&namespace=game-ns");
  });

  it("URL-encodes the namespace parameter", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { items: [] }));
    await Captures.list("alpha", "ns@special/with/slashes");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:captures?namespace=ns%40special%2Fwith%2Fslashes");
  });
});

describe("withClusterParam()", () => {
  // Cluster mock is reset in the top-level afterEach above — see its
  // comment for why that must be global rather than scoped here.

  it("returns path unchanged when cluster is 'local'", async () => {
    const { getCurrentCluster } = await import("./cluster");
    vi.mocked(getCurrentCluster).mockReturnValue("local");
    fetchMock.mockResolvedValueOnce(new HttpResponse(new Blob(["data"]), {}));
    await Captures.download("alpha", "cap-123");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-file?id=cap-123");
  });

  it("appends ?cluster= when cluster is non-local", async () => {
    const { getCurrentCluster } = await import("./cluster");
    vi.mocked(getCurrentCluster).mockReturnValue("remote-prod");
    fetchMock.mockResolvedValueOnce(new HttpResponse(new Blob(["data"]), {}));
    await createRequestClient({ cluster: "remote-prod" }).Captures.download("alpha", "cap-123");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-file?id=cap-123&cluster=remote-prod");
  });

  it("appends &cluster= when path already has query string", async () => {
    const { getCurrentCluster } = await import("./cluster");
    vi.mocked(getCurrentCluster).mockReturnValue("remote-prod");
    fetchMock.mockResolvedValueOnce(new HttpResponse(new Blob(["data"]), {}));
    await createRequestClient({ cluster: "remote-prod" }).Captures.download("alpha", "cap-123", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe(
      "/servers/alpha:capture-file?id=cap-123&namespace=game-ns&cluster=remote-prod"
    );
  });

  it("URL-encodes the cluster id", async () => {
    const { getCurrentCluster } = await import("./cluster");
    vi.mocked(getCurrentCluster).mockReturnValue("cluster@special");
    fetchMock.mockResolvedValueOnce(new HttpResponse(new Blob(["data"]), {}));
    await createRequestClient({ cluster: "cluster@special" }).Captures.download("alpha", "cap-123");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-file?id=cap-123&cluster=cluster%40special");
  });
});

describe("Captures.enable()", () => {
  it("POSTs to :capture-enable without namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        name: "alpha",
        status: { capture: { enabled: true } },
      })
    );
    await Captures.enable("alpha");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-enable");
    expect(init.method).toBe("POST");
  });

  it("POSTs to :capture-enable with namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        name: "alpha",
        status: { capture: { enabled: true } },
      })
    );
    await Captures.enable("alpha", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-enable?namespace=game-ns");
  });
});

describe("Captures.disable()", () => {
  it("POSTs to :capture-disable without namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        name: "alpha",
        status: { capture: { enabled: false } },
      })
    );
    await Captures.disable("alpha");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-disable");
    expect(init.method).toBe("POST");
  });

  it("POSTs to :capture-disable with namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        name: "alpha",
        status: { capture: { enabled: false } },
      })
    );
    await Captures.disable("alpha", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-disable?namespace=game-ns");
  });
});

describe("Captures.start()", () => {
  it("POSTs to :capture-start with body, returns 202", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          captureId: "cap-12345",
          serverName: "alpha",
          phase: "Pending",
          startedAt: "",
          completedAt: "",
          createdAt: "2026-08-23T00:00:00Z",
          expiresAt: "2026-08-30T00:00:00Z",
          bytesWritten: 0,
          packetsWritten: 0,
          filter: "tcp port 25565",
        }),
        { status: 202, headers: { "Content-Type": "application/json" } }
      )
    );
    const body: CaptureStartBody = {
      maxDurationSeconds: 600,
      maxSizeBytes: 1048576,
      filter: "tcp port 25565",
      ttlSecondsAfterFinished: 3600,
    };
    await Captures.start("alpha", body);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-start");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify(body));
  });

  it("POSTs to :capture-start with namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          captureId: "cap-12345",
          serverName: "alpha",
          phase: "Pending",
          startedAt: "",
          completedAt: "",
          createdAt: "2026-08-23T00:00:00Z",
          expiresAt: "2026-08-30T00:00:00Z",
          bytesWritten: 0,
          packetsWritten: 0,
          filter: "",
        }),
        { status: 202, headers: { "Content-Type": "application/json" } }
      )
    );
    const body: CaptureStartBody = {
      maxDurationSeconds: 300,
      maxSizeBytes: 524288,
    };
    await Captures.start("alpha", body, "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-start?namespace=game-ns");
  });
});

describe("Captures.stop()", () => {
  it("POSTs to :capture-stop with captureId in body", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        captureId: "cap-12345",
        serverName: "alpha",
        phase: "Completed",
        startedAt: "2026-08-23T00:00:00Z",
        completedAt: "2026-08-23T01:00:00Z",
        createdAt: "2026-08-23T00:00:00Z",
        expiresAt: "2026-08-30T00:00:00Z",
        bytesWritten: 1024,
        packetsWritten: 100,
        filter: "tcp port 25565",
      })
    );
    await Captures.stop("alpha", "cap-12345");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-stop");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ captureId: "cap-12345" }));
  });

  it("POSTs to :capture-stop with namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        captureId: "cap-12345",
        serverName: "alpha",
        phase: "Completed",
        startedAt: "2026-08-23T00:00:00Z",
        completedAt: "2026-08-23T01:00:00Z",
        createdAt: "2026-08-23T00:00:00Z",
        expiresAt: "2026-08-30T00:00:00Z",
        bytesWritten: 1024,
        packetsWritten: 100,
        filter: "tcp port 25565",
      })
    );
    await Captures.stop("alpha", "cap-12345", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-stop?namespace=game-ns");
  });
});

describe("Captures.list()", () => {
  it("GETs from :captures without namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        captures: [],
        total: 0,
        limit: 100,
        offset: 0,
      })
    );
    await Captures.list("alpha");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:captures");
    expect(init.method).toBe("GET");
  });

  it("GETs from :captures with namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        captures: [
          {
            captureId: "cap-1",
            serverName: "alpha",
            phase: "Completed",
            startedAt: "2026-08-23T00:00:00Z",
            completedAt: "2026-08-23T01:00:00Z",
            createdAt: "2026-08-23T00:00:00Z",
            expiresAt: "2026-08-30T00:00:00Z",
            bytesWritten: 1024,
            packetsWritten: 100,
            filter: "",
          },
        ],
        total: 1,
        limit: 100,
        offset: 0,
      })
    );
    await Captures.list("alpha", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:captures?namespace=game-ns");
  });
});

describe("Captures.get()", () => {
  it("GETs from :capture?id= without namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        captureId: "cap-12345",
        serverName: "alpha",
        phase: "Completed",
        startedAt: "2026-08-23T00:00:00Z",
        completedAt: "2026-08-23T01:00:00Z",
        createdAt: "2026-08-23T00:00:00Z",
        expiresAt: "2026-08-30T00:00:00Z",
        bytesWritten: 1024,
        packetsWritten: 100,
        filter: "tcp port 25565",
      })
    );
    await Captures.get("alpha", "cap-12345");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture?id=cap-12345");
    expect(init.method).toBe("GET");
  });

  it("GETs from :capture?id= with namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        captureId: "cap-12345",
        serverName: "alpha",
        phase: "Completed",
        startedAt: "2026-08-23T00:00:00Z",
        completedAt: "2026-08-23T01:00:00Z",
        createdAt: "2026-08-23T00:00:00Z",
        expiresAt: "2026-08-30T00:00:00Z",
        bytesWritten: 1024,
        packetsWritten: 100,
        filter: "tcp port 25565",
      })
    );
    await Captures.get("alpha", "cap-12345", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture?id=cap-12345&namespace=game-ns");
  });

  it("URL-encodes the capture id", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        captureId: "cap@special/id",
        serverName: "alpha",
        phase: "Completed",
        startedAt: "2026-08-23T00:00:00Z",
        completedAt: "2026-08-23T01:00:00Z",
        createdAt: "2026-08-23T00:00:00Z",
        expiresAt: "2026-08-30T00:00:00Z",
        bytesWritten: 1024,
        packetsWritten: 100,
        filter: "tcp port 25565",
      })
    );
    await Captures.get("alpha", "cap@special/id");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture?id=cap%40special%2Fid");
  });
});

describe("Captures.remove()", () => {
  it("DELETEs from :capture?id= without namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        deleted: true,
        captureId: "cap-12345",
      })
    );
    await Captures.remove("alpha", "cap-12345");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture?id=cap-12345");
    expect(init.method).toBe("DELETE");
  });

  it("DELETEs from :capture?id= with namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        deleted: true,
        captureId: "cap-12345",
      })
    );
    await Captures.remove("alpha", "cap-12345", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture?id=cap-12345&namespace=game-ns");
  });

  it("URL-encodes the capture id", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        deleted: true,
        captureId: "cap@special/id",
      })
    );
    await Captures.remove("alpha", "cap@special/id");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture?id=cap%40special%2Fid");
  });
});

describe("Captures.download()", () => {
  it("GETs from :capture-file?id= and returns Blob without namespace", async () => {
    const blobData = new Blob(["pcapng mock data"], {
      type: "application/octet-stream",
    });
    fetchMock.mockResolvedValueOnce(
      new Response(blobData, {
        status: 200,
        headers: { "Content-Type": "application/octet-stream" },
      })
    );
    const result = await Captures.download("alpha", "cap-12345");
    expect(result).toBeInstanceOf(Blob);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-file?id=cap-12345");
    expect(init.method).toBe("GET");
  });

  it("GETs from :capture-file?id= with namespace and returns Blob", async () => {
    const blobData = new Blob(["pcapng mock data"], {
      type: "application/octet-stream",
    });
    fetchMock.mockResolvedValueOnce(
      new Response(blobData, {
        status: 200,
        headers: { "Content-Type": "application/octet-stream" },
      })
    );
    const result = await Captures.download("alpha", "cap-12345", "game-ns");
    expect(result).toBeInstanceOf(Blob);
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-file?id=cap-12345&namespace=game-ns");
  });

  it("includes cluster param on non-local cluster", async () => {
    const { getCurrentCluster } = await import("./cluster");
    vi.mocked(getCurrentCluster).mockReturnValue("remote-prod");
    const blobData = new Blob(["pcapng mock data"], {
      type: "application/octet-stream",
    });
    fetchMock.mockResolvedValueOnce(
      new Response(blobData, {
        status: 200,
        headers: { "Content-Type": "application/octet-stream" },
      })
    );
    await createRequestClient({ cluster: "remote-prod" }).Captures.download("alpha", "cap-12345");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-file?id=cap-12345&cluster=remote-prod");
  });

  it("throws APIError on non-ok response with error body", async () => {
    fetchMock.mockImplementation(
      async () => new Response("File not found", { status: 404 })
    );
    await expect(Captures.download("alpha", "cap-12345")).rejects.toMatchObject({
      status: 404,
      body: "File not found",
    });
    await expect(Captures.download("alpha", "cap-12345")).rejects.toBeInstanceOf(APIError);
  });

  it("throws APIError on non-ok response with empty body", async () => {
    fetchMock.mockImplementation(
      async () => new Response("", { status: 500 })
    );
    await expect(Captures.download("alpha", "cap-12345")).rejects.toMatchObject({
      status: 500,
      body: "",
    });
    await expect(Captures.download("alpha", "cap-12345")).rejects.toBeInstanceOf(APIError);
  });

  it("throws APIError when response.text() fails but catches gracefully", async () => {
    const failingResponse = new Response("error", { status: 500 });
    vi.spyOn(failingResponse, "text").mockRejectedValueOnce(new Error("text failed"));
    fetchMock.mockResolvedValueOnce(failingResponse);
    await expect(Captures.download("alpha", "cap-12345")).rejects.toMatchObject({
      status: 500,
      body: "",
    });
  });

  it("URL-encodes the capture id in download URL", async () => {
    const blobData = new Blob(["pcapng mock data"], {
      type: "application/octet-stream",
    });
    fetchMock.mockResolvedValueOnce(
      new Response(blobData, {
        status: 200,
        headers: { "Content-Type": "application/octet-stream" },
      })
    );
    await Captures.download("alpha", "cap@special/id");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/alpha:capture-file?id=cap%40special%2Fid");
  });
});

describe("Shares.create()", () => {
  it("POSTs to :shares with create body, returns ShareLink with token", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        id: "link-123",
        createdAt: "2026-09-07T10:00:00Z",
        expiresAt: "2026-09-14T10:00:00Z",
        canStart: true,
        token: "tok_abc123xyz",
      })
    );
    const body = { expiresIn: "7d", canStart: true };
    const result = await Shares.create("my-server", body);
    expect(result).toMatchObject({
      id: "link-123",
      token: "tok_abc123xyz",
      canStart: true,
    });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/my-server:shares");
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify(body));
  });

  it("POSTs to :shares with namespace", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        id: "link-456",
        createdAt: "2026-09-07T10:00:00Z",
        expiresAt: "2026-09-08T10:00:00Z",
        canStart: false,
        token: "tok_def456",
      })
    );
    const body = { expiresIn: "24h", canStart: false };
    await Shares.create("my-server", body, "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/my-server:shares?namespace=game-ns");
  });

  it("URL-encodes the server name", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        id: "link-789",
        createdAt: "2026-09-07T10:00:00Z",
        expiresAt: "2026-09-21T10:00:00Z",
        canStart: true,
        token: "tok_ghi789",
      })
    );
    const body = { canStart: true };
    await Shares.create("server@special/name", body);
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/server%40special%2Fname:shares");
  });
});

describe("Shares.list()", () => {
  it("GETs from :shares without namespace, returns array of ShareLinks", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, [
        {
          id: "link-1",
          createdAt: "2026-09-01T10:00:00Z",
          expiresAt: "2026-09-08T10:00:00Z",
          canStart: true,
        },
        {
          id: "link-2",
          createdAt: "2026-09-02T10:00:00Z",
          expiresAt: "2026-09-09T10:00:00Z",
          canStart: false,
        },
      ])
    );
    const result = await Shares.list("my-server");
    expect(result).toHaveLength(2);
    expect(result[0].id).toBe("link-1");
    expect(result[0].token).toBeUndefined(); // list never includes token
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/my-server:shares");
    expect(init.method).toBe("GET");
  });

  it("GETs from :shares with namespace", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, []));
    await Shares.list("my-server", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/my-server:shares?namespace=game-ns");
  });

  it("URL-encodes the server name", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, []));
    await Shares.list("server@special");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/server%40special:shares");
  });
});

describe("Shares.revoke()", () => {
  it("DELETEs /servers/{name}/shares/{id} without namespace", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await Shares.revoke("my-server", "link-123");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/my-server/shares/link-123");
    expect(init.method).toBe("DELETE");
  });

  it("DELETEs with namespace", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await Shares.revoke("my-server", "link-456", "game-ns");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/my-server/shares/link-456?namespace=game-ns");
  });

  it("URL-encodes server name and link ID", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
    await Shares.revoke("server@special", "link/with/slash");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/servers/server%40special/shares/link%2Fwith%2Fslash");
  });
});

describe("Shares.resolve()", () => {
  it("GETs /shares/{token}, returns ShareLinkPublic with address and players", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        serverName: "survival",
        status: "Running",
        address: { host: "mc.example.com", port: 25565 },
        playersOnline: 5,
      })
    );
    const result = await Shares.resolve("tok_abc123");
    expect(result).toMatchObject({
      serverName: "survival",
      status: "Running",
      address: { host: "mc.example.com", port: 25565 },
      playersOnline: 5,
    });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/shares/tok_abc123");
    expect(init.method).toBe("GET");
  });

  it("handles public resolve without address or players", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        serverName: "private-server",
        status: "Asleep",
      })
    );
    const result = await Shares.resolve("tok_def456");
    expect(result).toMatchObject({
      serverName: "private-server",
      status: "Asleep",
    });
    expect(result.address).toBeUndefined();
    expect(result.playersOnline).toBeUndefined();
  });

  it("maps 404 (invalid token) to neutral response per FR-005", async () => {
    fetchMock.mockImplementation(
      async () => new Response(JSON.stringify({ error: "not found" }), { status: 404 })
    );
    const result = await Shares.resolve("tok_invalid");
    expect(result).toMatchObject({
      serverName: "",
      status: "Unknown",
    });
  });

  it("preserves rate limits and Retry-After so public polling can retry without declaring the link invalid", async () => {
    fetchMock.mockImplementation(
      async () => new Response("rate limited", { status: 429, headers: { "Retry-After": "30" } })
    );
    await expect(Shares.resolve("tok_ratelimit")).rejects.toMatchObject({ status: 429, retryAfter: "30" });
  });

  it("passes cancellation to the public resolve request", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { serverName: "survival", status: "Starting" }));
    const controller = new AbortController();
    await Shares.resolve("tok_cancel", controller.signal);
    expect(fetchMock.mock.calls[0][1].signal).toBe(controller.signal);
  });

  it("URL-encodes the token", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        serverName: "test",
        status: "Running",
      })
    );
    await Shares.resolve("tok@special/slash");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/shares/tok%40special%2Fslash");
  });

  it("throws other non-404 errors", async () => {
    fetchMock.mockImplementation(
      async () => new Response("server error", { status: 500 })
    );
    await expect(Shares.resolve("tok_error")).rejects.toBeInstanceOf(APIError);
  });
});

describe("Shares.start()", () => {
  it("POSTs /shares/{token}/start, returns void on 202 Accepted", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 202 }));
    const result = await Shares.start("tok_abc123");
    expect(result).toBeUndefined();
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/shares/tok_abc123/start");
    expect(init.method).toBe("POST");
  });

  it("maps 404 (invalid/expired/no-permission) to void per FR-005", async () => {
    fetchMock.mockImplementation(
      async () => new Response(JSON.stringify({ error: "not found" }), { status: 404 })
    );
    const result = await Shares.start("tok_invalid");
    expect(result).toBeUndefined();
  });

  it("propagates start rate limits and Retry-After without treating them as accepted", async () => {
    fetchMock.mockImplementation(
      async () => new Response("rate limited", { status: 429, headers: { "Retry-After": "30" } })
    );
    await expect(Shares.start("tok_ratelimit")).rejects.toMatchObject({ status: 429, retryAfter: "30" });
  });

  it("URL-encodes the token", async () => {
    fetchMock.mockResolvedValueOnce(new Response(null, { status: 202 }));
    await Shares.start("tok@special");
    const [url] = fetchMock.mock.calls[0];
    expect(url).toBe("/shares/tok%40special/start");
  });

  it("throws other non-404 errors", async () => {
    fetchMock.mockImplementation(
      async () => new Response("server error", { status: 500 })
    );
    await expect(Shares.start("tok_error")).rejects.toBeInstanceOf(APIError);
  });
});

describe("Telemetry notice client", () => {
  it("GETs /admin/telemetry/notice and returns the pending notice", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonRes(200, {
        pending: true,
        destination: { kind: "bundled", host: "receiver.test" },
        fields: { basic: ["version"], extended: ["installId"] },
      })
    );
    const notice = await Telemetry.notice();
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/admin/telemetry/notice");
    expect(init.method).toBe("GET");
    expect(notice.pending).toBe(true);
    expect(notice.destination?.host).toBe("receiver.test");
  });

  it("returns only pending:false when the notice is not pending", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { pending: false }));
    expect(await Telemetry.notice()).toEqual({ pending: false });
  });

  it.each(["seen", "keep", "extended-off", "all-off"] as const)(
    "POSTs the %s action as a JSON body",
    async (action) => {
      fetchMock.mockResolvedValueOnce(new Response(null, { status: 204 }));
      await expect(Telemetry.ack(action)).resolves.toBeUndefined();
      const [url, init] = fetchMock.mock.calls[0];
      expect(url).toBe("/admin/telemetry/notice");
      expect(init.method).toBe("POST");
      expect(JSON.parse(init.body as string)).toEqual({ action });
    }
  );

  it("surfaces a 409 (notice not pending) as an APIError", async () => {
    fetchMock.mockResolvedValueOnce(new Response("telemetry notice is not pending", { status: 409 }));
    await expect(Telemetry.ack("keep")).rejects.toMatchObject({ status: 409 });
  });
});

describe("api() problem+json errors", () => {
  const problemBody = {
    type: "about:blank",
    status: 409,
    title: "Conflict",
    detail: "role \"operator\" already exists",
    messageId: "server.role.exists",
    args: { role: "operator", count: 2, nested: { ignored: true } },
  };

  function problemRes(body: string, status = 409): Response {
    return new Response(body, {
      status,
      headers: { "Content-Type": "application/problem+json" },
    });
  }

  it("parses a problem+json body into detail, messageId and args", async () => {
    fetchMock.mockResolvedValueOnce(problemRes(JSON.stringify(problemBody)));
    await expect(api("/roles")).rejects.toMatchObject({
      detail: "role \"operator\" already exists",
      messageId: "server.role.exists",
      args: { role: "operator", count: 2 },
    });
  });

  it("sets body and message to the detail sentence", async () => {
    fetchMock.mockResolvedValueOnce(problemRes(JSON.stringify(problemBody)));
    const err = await api("/roles").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(APIError);
    const apiErr = err as APIError;
    expect(apiErr.body).toBe("role \"operator\" already exists");
    expect(apiErr.message).toBe("409: role \"operator\" already exists");
    expect(apiErr.status).toBe(409);
    expect(apiErr.isHTML).toBe(false);
  });

  it("leaves messageId and args unset when the body omits them", async () => {
    fetchMock.mockResolvedValueOnce(problemRes(JSON.stringify({ detail: "plain detail" }), 400));
    const err = (await api("/roles").catch((e: unknown) => e)) as APIError;
    expect(err.detail).toBe("plain detail");
    expect(err.messageId).toBeUndefined();
    expect(err.args).toBeUndefined();
    expect(err.message).toBe("400: plain detail");
  });

  it("falls back to the raw body when problem+json is not valid JSON", async () => {
    fetchMock.mockResolvedValueOnce(problemRes("not json {", 500));
    const err = (await api("/roles").catch((e: unknown) => e)) as APIError;
    expect(err.body).toBe("not json {");
    expect(err.message).toBe("500: not json {");
    expect(err.detail).toBeUndefined();
    expect(err.messageId).toBeUndefined();
  });

  it("falls back to the raw body when problem+json has no string detail", async () => {
    fetchMock.mockResolvedValueOnce(problemRes(JSON.stringify({ messageId: "x" }), 422));
    const err = (await api("/roles").catch((e: unknown) => e)) as APIError;
    expect(err.body).toBe(JSON.stringify({ messageId: "x" }));
    expect(err.detail).toBeUndefined();
  });

  it("ignores a non-object args value", async () => {
    fetchMock.mockResolvedValueOnce(
      problemRes(JSON.stringify({ detail: "d", messageId: "m", args: ["a"] }), 400),
    );
    const err = (await api("/roles").catch((e: unknown) => e)) as APIError;
    expect(err.messageId).toBe("m");
    expect(err.args).toBeUndefined();
  });

  it("keeps plain text error bodies unchanged", async () => {
    fetchMock.mockResolvedValueOnce(new Response("boom", { status: 500, headers: { "Content-Type": "text/plain" } }));
    const err = (await api("/roles").catch((e: unknown) => e)) as APIError;
    expect(err.body).toBe("boom");
    expect(err.message).toBe("500: boom");
    expect(err.detail).toBeUndefined();
    expect(err.messageId).toBeUndefined();
    expect(err.args).toBeUndefined();
  });

  it("sends the problem+json Accept header by default", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { ok: true }));
    await api("/roles");
    const [, init] = fetchMock.mock.calls[0];
    expect((init.headers as Record<string, string>).Accept).toBe("application/json, application/problem+json");
  });

  it("lets opts.headers override the Accept header", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { ok: true }));
    await api("/roles", { headers: { Accept: "text/plain" } });
    const [, init] = fetchMock.mock.calls[0];
    expect((init.headers as Record<string, string>).Accept).toBe("text/plain");
  });
});
