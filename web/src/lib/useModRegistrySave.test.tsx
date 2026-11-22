import { describe, expect, it } from "vitest";
import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { server } from "@/test/server";
import type { AllConfig, ModRegistriesCfg } from "./config";
import { useModRegistrySave } from "./useModRegistrySave";

function renderSave(initial: AllConfig = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  client.setQueryData(["config"], initial);
  return { client, ...renderHook(() => useModRegistrySave(), { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> }) };
}

describe("immediate mod registry persistence", () => {
  it("stores a key and its configuration in order while retaining another provider's newer change", async () => {
    const calls: string[] = [];
    const nexus = { provider: "nexus" as const, configRef: "nexus-external" };
    let saved: ModRegistriesCfg | undefined;
    server.use(
      http.get("/admin/config", () => HttpResponse.json({ modRegistries: { registries: [nexus] } })),
      http.put("/admin/registries/steam/secret", async ({ request }) => {
        calls.push("secret");
        expect(await request.json()).toEqual({ apiKey: "steam-key" });
        return HttpResponse.json({ name: "gameplane-modreg-steam", keys: ["apiKey"] });
      }),
      http.put("/admin/config/modRegistries", async ({ request }) => {
        calls.push("config");
        saved = await request.json() as ModRegistriesCfg;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    const { result, client } = renderSave({ modRegistries: { registries: [] } });
    await act(() => result.current.mutateAsync({ kind: "set", provider: "steam", apiKey: "steam-key" }));
    expect(calls).toEqual(["secret", "config"]);
    expect(saved?.registries).toEqual([nexus, { provider: "steam", configRef: "gameplane-modreg-steam" }]);
    expect(client.getQueryData<AllConfig>(["config"])?.modRegistries).toEqual(saved);
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
  });

  it.each(["secret", "config"] as const)("reports a %s write failure without reporting success", async (stage) => {
    const calls: string[] = [];
    server.use(
      http.get("/admin/config", () => HttpResponse.json({})),
      http.put("/admin/registries/steam/secret", () => {
        calls.push("secret");
        return stage === "secret" ? HttpResponse.text("secret failed", { status: 500 }) : HttpResponse.json({ name: "gameplane-modreg-steam", keys: ["apiKey"] });
      }),
      http.put("/admin/config/modRegistries", () => {
        calls.push("config");
        return HttpResponse.text("config failed", { status: 500 });
      }),
    );
    const { result, client } = renderSave();
    await act(async () => { await expect(result.current.mutateAsync({ kind: "set", provider: "steam", apiKey: "key" })).rejects.toThrow(`${stage} failed`); });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.isSuccess).toBe(false);
    expect(calls).toEqual(stage === "secret" ? ["secret"] : ["secret", "config"]);
    expect(client.getQueryData<AllConfig>(["config"])?.modRegistries).toBeUndefined();
  });

  it("does not write when the fresh configuration read fails", async () => {
    let writes = 0;
    server.use(
      http.get("/admin/config", () => HttpResponse.text("read failed", { status: 500 })),
      http.put("/admin/registries/steam/secret", () => { writes++; return HttpResponse.json({}); }),
      http.put("/admin/config/modRegistries", () => { writes++; return new HttpResponse(null, { status: 204 }); }),
    );
    const { result } = renderSave();
    await act(async () => { await expect(result.current.mutateAsync({ kind: "set", provider: "steam", apiKey: "key" })).rejects.toThrow("read failed"); });
    expect(writes).toBe(0);
    await waitFor(() => expect(result.current.isError).toBe(true));
  });

  it("rejects a changed provider before writing its key", async () => {
    let writes = 0;
    server.use(
      http.get("/admin/config", () => HttpResponse.json({ modRegistries: { registries: [{ provider: "steam", configRef: "new-owner-secret" }] } })),
      http.put("/admin/registries/steam/secret", () => { writes++; return HttpResponse.json({}); }),
    );
    const { result } = renderSave();
    await act(async () => { await expect(result.current.mutateAsync({ kind: "set", provider: "steam", apiKey: "key" })).rejects.toThrow("Configuration changed."); });
    expect(writes).toBe(0);
  });

  it.each(["gameplane-modreg-steam", "external-secret"])("removes a row before cleaning up only the owned Secret: %s", async (configRef) => {
    const calls: string[] = [];
    const expected = { provider: "steam" as const, configRef };
    server.use(
      http.get("/admin/config", () => HttpResponse.json({ modRegistries: { registries: [expected] } })),
      http.put("/admin/config/modRegistries", async ({ request }) => {
        calls.push("config");
        expect(await request.json()).toEqual({ registries: [] });
        return new HttpResponse(null, { status: 204 });
      }),
      http.delete("/admin/registries/steam/secret", () => { calls.push("delete"); return new HttpResponse(null, { status: 204 }); }),
    );
    const { result } = renderSave();
    await act(() => result.current.mutateAsync({ kind: "remove", provider: "steam", expected }));
    expect(calls).toEqual(configRef === "external-secret" ? ["config"] : ["config", "delete"]);
  });

  it("reports cleanup failure while keeping the successfully removed row absent", async () => {
    const expected = { provider: "steam" as const, configRef: "gameplane-modreg-steam" };
    server.use(
      http.get("/admin/config", () => HttpResponse.json({ modRegistries: { registries: [expected] } })),
      http.put("/admin/config/modRegistries", () => new HttpResponse(null, { status: 204 })),
      http.delete("/admin/registries/steam/secret", () => HttpResponse.text("cleanup failed", { status: 500 })),
    );
    const { result, client } = renderSave();
    await act(async () => { await expect(result.current.mutateAsync({ kind: "remove", provider: "steam", expected })).rejects.toThrow("cleanup failed"); });
    expect(client.getQueryData<AllConfig>(["config"])?.modRegistries).toEqual({ registries: [] });
    await waitFor(() => expect(result.current.isError).toBe(true));
  });
});
