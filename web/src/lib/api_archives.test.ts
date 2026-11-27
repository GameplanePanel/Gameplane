import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "@/test/server";
import { FakeXHR } from "@/test/fakeXHR";
import { APIError, backupDownloadURL, confirmRestore, uploadRestore } from "./api";

describe("backupDownloadURL", () => {
  it("builds a plain link for the local cluster", () => {
    expect(backupDownloadURL("local", undefined, "alpha-1", "tar.gz")).toBe("/backups/alpha-1:download?format=tar.gz");
  });

  it("adds the namespace and a non-local cluster", () => {
    expect(backupDownloadURL("east", "games", "alpha-1", "zip")).toBe("/backups/alpha-1:download?format=zip&namespace=games&cluster=east");
  });
});

describe("uploadRestore", () => {
  beforeEach(() => {
    FakeXHR.instances = [];
    vi.stubGlobal("XMLHttpRequest", FakeXHR);
  });
  afterEach(() => vi.unstubAllGlobals());

  const file = new File(["data"], "my world.tar.gz");

  it("posts the raw file with the query, headers and progress", async () => {
    const progress: Array<[number, number]> = [];
    const done = uploadRestore("east", "games", "alpha", file, { format: "tar.gz", template: "minecraft" }, (l, t) => progress.push([l, t]));
    const xhr = FakeXHR.instances[0];
    expect(xhr.method).toBe("POST");
    expect(xhr.url).toBe("/restores:upload?server=alpha&format=tar.gz&fileName=my+world.tar.gz&template=minecraft&namespace=games&cluster=east");
    expect(xhr.withCredentials).toBe(true);
    expect(xhr.headers["Content-Type"]).toBe("application/octet-stream");
    expect(xhr.headers["Accept"]).toContain("application/problem+json");
    expect(xhr.headers).toHaveProperty("X-Gameplane-CSRF");
    xhr.progress(2, 4);
    xhr.upload.onprogress?.({ lengthComputable: false, loaded: 1, total: 0 });
    xhr.finish(202, JSON.stringify({ restore: "restore-up-1" }));
    await expect(done).resolves.toEqual({ restore: "restore-up-1" });
    expect(progress).toEqual([[2, 4]]);
  });

  it("omits the template for a Gameplane archive and works without a progress callback", async () => {
    const done = uploadRestore("local", undefined, "alpha", file, { format: "zip" });
    const xhr = FakeXHR.instances[0];
    expect(xhr.url).toBe("/restores:upload?server=alpha&format=zip&fileName=my+world.tar.gz");
    xhr.progress(1, 4);
    xhr.finish(202, JSON.stringify({ restore: "r" }));
    await expect(done).resolves.toEqual({ restore: "r" });
  });

  it("rejects with an APIError that keeps the problem message id", async () => {
    const done = uploadRestore("local", undefined, "alpha", file, { format: "zip" });
    const xhr = FakeXHR.instances[0];
    xhr.responseHeaders["Content-Type"] = "application/problem+json";
    xhr.responseHeaders["Retry-After"] = "5";
    xhr.finish(413, JSON.stringify({ detail: "too big", messageId: "server.restore.upload.too_large" }));
    const error = await done.catch((e: unknown) => e);
    expect(error).toBeInstanceOf(APIError);
    expect((error as APIError).status).toBe(413);
    expect((error as APIError).messageId).toBe("server.restore.upload.too_large");
    expect((error as APIError).retryAfter).toBe("5");
  });

  it("rejects an unreadable or incomplete success response", async () => {
    for (const body of ["not json", JSON.stringify({}), JSON.stringify({ restore: "" })]) {
      const done = uploadRestore("local", undefined, "alpha", file, { format: "zip" });
      FakeXHR.instances.at(-1)?.finish(202, body);
      const error = await done.catch((e: unknown) => e);
      expect(error).toBeInstanceOf(APIError);
      expect((error as APIError).body).toBe("unexpected upload response");
    }
  });

  it("rejects when the connection drops or times out", async () => {
    const dropped = uploadRestore("local", undefined, "alpha", file, { format: "zip" });
    FakeXHR.instances[0].onerror?.();
    await expect(dropped).rejects.toThrow("connection lost");
    const timedOut = uploadRestore("local", undefined, "alpha", file, { format: "zip" });
    FakeXHR.instances[1].ontimeout?.();
    await expect(timedOut).rejects.toThrow("connection lost");
  });

  it("aborts the request when the signal fires", async () => {
    const controller = new AbortController();
    const done = uploadRestore("local", undefined, "alpha", file, { format: "zip", signal: controller.signal });
    controller.abort();
    expect(FakeXHR.instances[0].aborted).toBe(true);
    await expect(done).rejects.toMatchObject({ name: "AbortError" });
  });

  it("removes the abort listener once the request settles", async () => {
    const outcomes: Array<(xhr: FakeXHR) => void> = [
      (xhr) => xhr.finish(202, JSON.stringify({ restore: "r1" })),
      (xhr) => xhr.onerror?.(),
      (xhr) => xhr.ontimeout?.(),
      (xhr) => xhr.abort(),
    ];
    for (const [i, settle] of outcomes.entries()) {
      const controller = new AbortController();
      const remove = vi.spyOn(controller.signal, "removeEventListener");
      const done = uploadRestore("local", undefined, "alpha", file, { format: "zip", signal: controller.signal });
      settle(FakeXHR.instances[i]);
      await done.catch(() => undefined);
      expect(remove).toHaveBeenCalledWith("abort", expect.any(Function));
    }
  });

  it("does not start when the signal is already aborted", async () => {
    const controller = new AbortController();
    controller.abort();
    await expect(uploadRestore("local", undefined, "alpha", file, { format: "zip", signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
    expect(FakeXHR.instances).toHaveLength(0);
  });
});

describe("confirmRestore", () => {
  it("posts the generation to the restore's confirm route", async () => {
    let seen: { url: string; body: unknown } | undefined;
    server.use(http.post(/\/restores\/[^/]+:confirm(\?.*)?$/, async ({ request }) => {
      seen = { url: new URL(request.url).search, body: await request.json() };
      return new HttpResponse(null, { status: 204 });
    }));
    await confirmRestore("east", "games", "restore-up-1", 3);
    expect(seen).toEqual({ url: "?namespace=games&cluster=east", body: { generation: 3 } });
  });
});
