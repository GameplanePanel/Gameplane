import { describe, it, expect, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderWithQuery } from "@/test/render";
import { RegistryBrowser } from "./registry-browser";

const SWITCH_NAME = "All loaders";

function providersHandler() {
  return http.get("/servers/test/mods/registry/providers", () =>
    HttpResponse.json([
      { provider: "modrinth", available: true, mods: true, modpacks: true },
      { provider: "ftb", available: true, mods: false, modpacks: true },
    ]),
  );
}

describe("RegistryBrowser All loaders", () => {
  it("shows the switch only for providers the caller allows", async () => {
    server.use(
      providersHandler(),
      http.get("/servers/test/mods/registry/search", () => HttpResponse.json([])),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        type="modpack"
        renderItem={() => null}
        allLoaders={false}
        onAllLoadersChange={vi.fn()}
        hasAllLoaders={(p) => p === "modrinth"}
      />,
    );
    expect(await screen.findByRole("switch", { name: SWITCH_NAME })).not.toBeChecked();
    await userEvent.click(screen.getByRole("tab", { name: "FTB" }));
    await waitFor(() => expect(screen.queryByRole("switch", { name: SWITCH_NAME })).not.toBeInTheDocument());
  });

  it("hides the switch for mod browsing even when a caller passes the props", async () => {
    server.use(
      providersHandler(),
      http.get("/servers/test/mods/registry/search", () => HttpResponse.json([])),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        type="mod"
        renderItem={() => null}
        allLoaders={true}
        onAllLoadersChange={vi.fn()}
        hasAllLoaders={() => true}
      />,
    );
    // Only Modrinth browses mods here, so no tab bar; wait for the search to settle.
    await screen.findByText("No results.");
    expect(screen.queryByRole("switch", { name: SWITCH_NAME })).not.toBeInTheDocument();
  });

  it("sends allLoaders=true on search only while the switch is on", async () => {
    const allLoadersParams: (string | null)[] = [];
    server.use(
      providersHandler(),
      http.get("/servers/test/mods/registry/search", ({ request }) => {
        allLoadersParams.push(new URL(request.url).searchParams.get("allLoaders"));
        return HttpResponse.json([]);
      }),
    );
    const onAllLoadersChange = vi.fn();
    const { rerender, client } = renderWithQuery(
      <RegistryBrowser
        name="test"
        type="modpack"
        renderItem={() => null}
        allLoaders={false}
        onAllLoadersChange={onAllLoadersChange}
        hasAllLoaders={() => true}
      />,
    );
    await waitFor(() => expect(allLoadersParams).toEqual([null]));

    await userEvent.click(await screen.findByRole("switch", { name: SWITCH_NAME }));
    expect(onAllLoadersChange).toHaveBeenLastCalledWith(true);

    rerender(
      <QueryClientProvider client={client}>
        <RegistryBrowser
          name="test"
          type="modpack"
          renderItem={() => null}
          allLoaders={true}
          onAllLoadersChange={onAllLoadersChange}
          hasAllLoaders={() => true}
        />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(allLoadersParams).toContain("true"));
    expect(screen.getByRole("switch", { name: SWITCH_NAME })).toBeChecked();
  });
});
