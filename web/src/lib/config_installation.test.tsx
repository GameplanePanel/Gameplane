import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "@/test/server";
import { useUpdateConfigSection } from "./config";
import { useInstallation } from "./useInstallation";

function queryWrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  return { client, wrapper };
}

describe("general settings installation refresh", () => {
  it("refreshes the cached instance name immediately after saving", async () => {
    let name = "First name";
    server.use(
      http.get("/admin/installation", () => HttpResponse.json({ standalone: false, localCluster: true, version: "v0.4.1", instanceName: name })),
      http.put("/admin/config/general", async ({ request }) => {
        const body = await request.json() as { instanceName: string };
        name = body.instanceName;
        return HttpResponse.json(body);
      }),
    );
    const { client, wrapper } = queryWrapper();
    const { result, unmount } = renderHook(() => ({ installation: useInstallation(), save: useUpdateConfigSection("general") }), { wrapper });
    await waitFor(() => expect(result.current.installation.data?.instanceName).toBe("First name"));
    await act(async () => { await result.current.save.mutateAsync({ instanceName: "Saved name", externalURL: "", defaultNamespace: "games" }); });
    await waitFor(() => expect(result.current.installation.data?.instanceName).toBe("Saved name"));
    unmount();
    client.clear();
  });

  it("keeps the saved name when the general save fails", async () => {
    server.use(http.put("/admin/config/general", () => HttpResponse.json({ error: "failed" }, { status: 500 })));
    const { client, wrapper } = queryWrapper();
    client.setQueryData(["installation"], { instanceName: "Saved name" });
    const { result, unmount } = renderHook(() => useUpdateConfigSection("general"), { wrapper });
    await act(async () => { await expect(result.current.mutateAsync({ instanceName: "Unsaved name", externalURL: "", defaultNamespace: "games" })).rejects.toThrow(); });
    expect(client.getQueryData(["installation"])).toEqual({ instanceName: "Saved name" });
    expect(client.getQueryState(["installation"])?.isInvalidated).toBe(false);
    unmount();
    client.clear();
  });

  it("does not invalidate installation metadata when another section changes", async () => {
    server.use(http.put("/admin/config/telemetry", () => HttpResponse.json({ sendMetrics: false, extended: false })));
    const { client, wrapper } = queryWrapper();
    client.setQueryData(["installation"], { instanceName: "Saved name" });
    const { result, unmount } = renderHook(() => useUpdateConfigSection("telemetry"), { wrapper });
    await act(async () => { await result.current.mutateAsync({ sendMetrics: false, extended: false }); });
    expect(client.getQueryState(["installation"])?.isInvalidated).toBe(false);
    unmount();
    client.clear();
  });
});
