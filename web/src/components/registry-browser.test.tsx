import { describe, it, expect, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { RegistryBrowser, providerLabel, compactNum } from "./registry-browser";
import type { RegistryProject, ModRegistryProvider } from "@/types";

const mockProject = (overrides: Partial<RegistryProject> = {}): RegistryProject => ({
  id: "proj-1",
  title: "Example Mod",
  slug: "example-mod",
  provider: "modrinth",
  downloads: 1500,
  iconUrl: "https://example.com/icon.png",
  ...overrides,
});

describe("RegistryBrowser", () => {
  it("keeps each retained result's provider for actions while a provider switch is pending", async () => {
    let finishSearch!: () => void;
    const pending = new Promise<void>((resolve) => { finishSearch = resolve; });
    const searches: string[] = [];
    const install = vi.fn();
    server.use(
      http.get("/servers/test/mods/registry/providers", () => HttpResponse.json([
        { provider: "modrinth", available: true, mods: true, modpacks: true },
        { provider: "curseforge", available: true, mods: true, modpacks: true },
      ])),
      http.get("/servers/test/mods/registry/search", async ({ request }) => {
        const provider = new URL(request.url).searchParams.get("provider") ?? "";
        searches.push(provider);
        if (provider === "curseforge") {
          await pending;
          return HttpResponse.json([mockProject({ id: "same-id", title: "New provider project", provider: "curseforge" })]);
        }
        return HttpResponse.json(Array.from({ length: 24 }, (_, i) => mockProject({ id: `old-${i}`, title: `Old project ${i}` })));
      }),
    );
    renderWithQuery(<RegistryBrowser name="test" type="modpack" renderItem={(p, provider) => (
      <button onClick={() => install({ id: p.id, provider })}>{p.title}</button>
    )} />);
    await screen.findByRole("button", { name: "Old project 0" });
    await userEvent.click(screen.getByRole("tab", { name: "CurseForge" }));
    await waitFor(() => expect(searches).toContain("curseforge"));
    expect(screen.getByRole("button", { name: "Load more" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Old project 0" }));
    expect(install).toHaveBeenLastCalledWith({ id: "old-0", provider: "modrinth" });
    await act(async () => finishSearch());
    await userEvent.click(await screen.findByRole("button", { name: "New provider project" }));
    expect(install).toHaveBeenLastCalledWith({ id: "same-id", provider: "curseforge" });
    expect(searches).toEqual(["modrinth", "curseforge"]);
  });

  it("renders no provider UI when only one provider is available", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([{ provider: "modrinth", available: true, mods: true, modpacks: false }]),
      ),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        type="mod"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );
    await waitFor(() => {
      expect(screen.queryByRole("button", { name: /modrinth/i })).not.toBeInTheDocument();
    });
  });

  it("renders provider switcher when multiple providers available", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
          { provider: "curseforge", available: true, mods: true, modpacks: true },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", () =>
        HttpResponse.json([mockProject()]),
      ),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );
    await waitFor(() => {
      expect(screen.getByRole("tab", { name: "Modrinth" })).toBeInTheDocument();
      expect(screen.getByRole("tab", { name: "CurseForge" })).toBeInTheDocument();
    });
  });

  it("filters providers by type (modpacks only)", async () => {
    // The provider switcher only renders once more than one *usable*
    // provider remains after filtering (RegistryBrowser gates it on
    // `available.length > 1`) — so a second modpack-capable provider is
    // needed here to actually exercise the switcher and prove curseforge
    // (modpacks: false) got filtered out rather than the switcher being
    // hidden entirely for having only one usable entry.
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: true },
          { provider: "curseforge", available: true, mods: true, modpacks: false },
          { provider: "thunderstore", available: true, mods: true, modpacks: true },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", () =>
        HttpResponse.json([mockProject()]),
      ),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        type="modpack"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );
    await waitFor(() => {
      expect(screen.getByRole("tab", { name: "Modrinth" })).toBeInTheDocument();
      expect(screen.queryByRole("tab", { name: "CurseForge" })).not.toBeInTheDocument();
    });
  });

  it("shows 'not available' message when no usable providers", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([]),
      ),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );
    await waitFor(() => {
      // The component renders a curly apostrophe ("isn’t"); match on the
      // surrounding text only so this doesn't depend on that character.
      expect(
        screen.getByText(/In-app browse isn.t available/),
      ).toBeInTheDocument();
    });
  });

  it("loads and displays results from default provider", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", () =>
        HttpResponse.json([mockProject({ title: "First Mod" })]),
      ),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );
    expect(await screen.findByText("First Mod")).toBeInTheDocument();
  });

  it("debounces search input", async () => {
    let searchCalls = 0;
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", () => {
        searchCalls++;
        return HttpResponse.json([mockProject()]);
      }),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );

    // Initial load
    await screen.findByText("Example Mod");
    const initialCalls = searchCalls;

    // Type fast - should debounce
    const input = screen.getByRole("textbox");
    await userEvent.type(input, "abc", { delay: 50 });

    // Wait for debounce
    await waitFor(() => {
      expect(searchCalls).toBeGreaterThan(initialCalls);
    });
  });

  it("disables sort when searching", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", () =>
        HttpResponse.json([mockProject()]),
      ),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );

    const sortTrigger = screen.getByRole("button", { name: /sort/i });
    expect(sortTrigger).not.toHaveAttribute("aria-disabled", "true");
    expect(sortTrigger).not.toBeDisabled();

    const input = screen.getByRole("textbox");
    await userEvent.type(input, "test");

    // Sort should become disabled while searching
    await waitFor(() => {
      expect(sortTrigger).toBeDisabled();
    });
  });

  it("renders category chips only for modrinth", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", () =>
        HttpResponse.json([mockProject()]),
      ),
    );
    const categories = [
      { value: "magic", label: "Magic" },
      { value: "tech", label: "Tech" },
    ];
    renderWithQuery(
      <RegistryBrowser
        name="test"
        categories={categories}
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "All" })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Magic" })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Tech" })).toBeInTheDocument();
    });
  });

  it("filters by category", async () => {
    let lastRequest: Record<string, string> | null = null;
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", ({ request }) => {
        lastRequest = Object.fromEntries(new URL(request.url).searchParams);
        return HttpResponse.json([mockProject()]);
      }),
    );
    const categories = [
      { value: "magic", label: "Magic" },
    ];
    renderWithQuery(
      <RegistryBrowser
        name="test"
        categories={categories}
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );

    await screen.findByText("Example Mod");
    const magicBtn = screen.getByRole("button", { name: "Magic" });
    await userEvent.click(magicBtn);

    await waitFor(() => {
      expect(lastRequest?.category).toBe("magic");
    });
  });

  it("shows 'No results' when search finds nothing", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", () =>
        HttpResponse.json([]),
      ),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText(/No results/)).toBeInTheDocument();
    });
  });

  it("shows error on search failure", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", () =>
        HttpResponse.json({ error: "Invalid search query" }, { status: 400 }),
      ),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );

    await waitFor(() => {
      expect(screen.getByText(/Invalid search query/)).toBeInTheDocument();
    });
  });

  it("handles pagination with Load more button", async () => {
    const projects = Array.from({ length: 30 }, (_, i) =>
      mockProject({ title: `Mod ${i + 1}` }),
    );
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", ({ request }) => {
        const url = new URL(request.url);
        const offset = parseInt(url.searchParams.get("offset") ?? "0");
        const page = projects.slice(offset, offset + 24);
        return HttpResponse.json(page);
      }),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );

    // First page loads
    await screen.findByText("Mod 1");
    expect(screen.getByRole("button", { name: /Load more/i })).toBeInTheDocument();

    // Click load more
    await userEvent.click(screen.getByRole("button", { name: /Load more/i }));

    // Second page should load
    await waitFor(() => {
      expect(screen.getByText("Mod 25")).toBeInTheDocument();
    });
  });

  it("keeps previous results while fetching new search", async () => {
    let isSlowRequest = false;
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", async () => {
        if (isSlowRequest) {
          await new Promise((r) => setTimeout(r, 100));
        }
        return HttpResponse.json([mockProject({ title: "Result" })]);
      }),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );

    await screen.findByText("Result");

    // Trigger a slow search
    isSlowRequest = true;
    const input = screen.getByRole("textbox");
    await userEvent.type(input, "new");

    // Old results should still be visible (keepPreviousData)
    expect(screen.getByText("Result")).toBeInTheDocument();
  });

  it("falls back to first available provider when pick is no longer available", async () => {
    // Handlers must be registered before the first render fires the
    // providers query — registering them afterward (as this test
    // previously did) leaves that first request unhandled, which under
    // this suite's onUnhandledFrame:"error" config errors it out and
    // the component never gets data to recover from since react-query
    // doesn't retry (retry: false) or auto-refetch on a later server.use.
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
          { provider: "curseforge", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", () =>
        HttpResponse.json([mockProject()]),
      ),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );

    // Initial render with both providers
    await screen.findByText("Example Mod");

    // Update MSW handler to return only one provider
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
    );

    // Should still render without crashing (MSW handler update triggers refetch)
    expect(screen.getByText("Example Mod")).toBeInTheDocument();
  });

  it("renders renderItem for each project with provider", async () => {
    const renderItem = vi.fn((p) => <div key={p.id}>{p.title}</div>);
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", () =>
        HttpResponse.json([
          mockProject({ id: "1", title: "Mod 1" }),
          mockProject({ id: "2", title: "Mod 2" }),
        ]),
      ),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={renderItem}
      />,
    );

    // RegistryProject's display field is `title`, not `name` — mockProject
    // never sets `name`, so asserting on it could never match.
    await waitFor(() => {
      expect(renderItem).toHaveBeenCalledWith(
        expect.objectContaining({ id: "1", title: "Mod 1" }),
        "modrinth",
      );
      expect(renderItem).toHaveBeenCalledWith(
        expect.objectContaining({ id: "2", title: "Mod 2" }),
        "modrinth",
      );
    });
  });

  it("switches providers and resets pagination", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
          { provider: "curseforge", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", ({ request }) => {
        const url = new URL(request.url);
        const provider = url.searchParams.get("provider") as ModRegistryProvider;
        return HttpResponse.json([
          mockProject({ provider, title: `${provider} Result` }),
        ]);
      }),
    );
    renderWithQuery(
      <RegistryBrowser
        name="test"
        renderItem={(p) => <div>{p.title}</div>}
      />,
    );

    await screen.findByText("modrinth Result");

    // Switch provider
    await userEvent.click(screen.getByRole("tab", { name: "CurseForge" }));

    await waitFor(() => {
      expect(screen.getByText("curseforge Result")).toBeInTheDocument();
    });
  });

  it("renders category chips for a non-modrinth category-filterable provider (hangar)", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([{ provider: "hangar", available: true, mods: true, modpacks: false }]),
      ),
      http.get("/servers/test/mods/registry/search", () =>
        HttpResponse.json([mockProject({ provider: "hangar" })]),
      ),
    );
    const categories = [{ value: "utility", label: "Utility" }];
    renderWithQuery(
      <RegistryBrowser name="test" categories={categories} renderItem={(p) => <div>{p.title}</div>} />,
    );

    await waitFor(() => {
      expect(screen.getByRole("button", { name: "All" })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Utility" })).toBeInTheDocument();
    });
  });

  it("disables category chips for a provider that can't filter by category", async () => {
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([{ provider: "curseforge", available: true, mods: true, modpacks: false }]),
      ),
      http.get("/servers/test/mods/registry/search", ({ request }) => {
        expect(new URL(request.url).searchParams.get("category")).toBeFalsy();
        return HttpResponse.json([mockProject({ provider: "curseforge" })]);
      }),
    );
    const categories = [{ value: "utility", label: "Utility" }];
    renderWithQuery(
      <RegistryBrowser name="test" categories={categories} renderItem={(p) => <div>{p.title}</div>} />,
    );

    await screen.findByText("Example Mod");
    // Disabled chips are not exposed as clickable buttons (no role="button",
    // no onClick), but the label still renders with aria-disabled + a title
    // explaining why.
    const chip = screen.getByText("Utility");
    expect(chip.closest("[aria-disabled]")).toHaveAttribute(
      "title",
      "Category filtering isn't available for CurseForge.",
    );
    expect(screen.queryByRole("button", { name: "Utility" })).not.toBeInTheDocument();
  });

  it("switching provider tabs re-queries search with the new provider param", async () => {
    const seenProviders: string[] = [];
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([
          { provider: "modrinth", available: true, mods: true, modpacks: false },
          { provider: "hangar", available: true, mods: true, modpacks: false },
        ]),
      ),
      http.get("/servers/test/mods/registry/search", ({ request }) => {
        const provider = new URL(request.url).searchParams.get("provider") ?? "";
        seenProviders.push(provider);
        return HttpResponse.json([mockProject({ provider: provider as ModRegistryProvider })]);
      }),
    );
    renderWithQuery(
      <RegistryBrowser name="test" renderItem={(p) => <div>{p.title}</div>} />,
    );

    await waitFor(() => expect(seenProviders).toContain("modrinth"));

    await userEvent.click(screen.getByRole("tab", { name: "Hangar" }));

    await waitFor(() => expect(seenProviders).toContain("hangar"));
  });

  it("toggling a category chip applies and clears the filter on the search request", async () => {
    let lastCategory: string | null = null;
    server.use(
      http.get("/servers/test/mods/registry/providers", () =>
        HttpResponse.json([{ provider: "modrinth", available: true, mods: true, modpacks: false }]),
      ),
      http.get("/servers/test/mods/registry/search", ({ request }) => {
        lastCategory = new URL(request.url).searchParams.get("category");
        return HttpResponse.json([mockProject()]);
      }),
    );
    const categories = [{ value: "utility", label: "Utility" }];
    renderWithQuery(
      <RegistryBrowser name="test" categories={categories} renderItem={(p) => <div>{p.title}</div>} />,
    );

    await screen.findByText("Example Mod");
    const utilityChip = screen.getByRole("button", { name: "Utility" });
    await userEvent.click(utilityChip);
    await waitFor(() => expect(lastCategory).toBe("utility"));

    // Clicking "All" clears it back out.
    await userEvent.click(screen.getByRole("button", { name: "All" }));
    await waitFor(() => expect(lastCategory).toBeFalsy());
  });
});

describe("providerLabel", () => {
  it("returns display name for known providers", () => {
    expect(providerLabel("modrinth")).toBe("Modrinth");
    expect(providerLabel("curseforge")).toBe("CurseForge");
    expect(providerLabel("thunderstore")).toBe("Thunderstore");
    expect(providerLabel("factorio")).toBe("Factorio Mod Portal");
  });

  it("capitalizes unknown providers", () => {
    expect(providerLabel("unknown")).toBe("Unknown");
    expect(providerLabel("custom-provider")).toBe("Custom-provider");
  });
});

describe("compactNum", () => {
  it("formats small numbers", () => {
    expect(compactNum(0)).toBe("0");
    expect(compactNum(100)).toBe("100");
    expect(compactNum(999)).toBe("999");
  });

  it("formats thousands", () => {
    expect(compactNum(1000)).toBe("1K");
    expect(compactNum(1500)).toBe("1.5K");
    expect(compactNum(999999)).toBe("1M");
  });

  it("formats millions", () => {
    expect(compactNum(1000000)).toBe("1M");
    expect(compactNum(1500000)).toBe("1.5M");
  });
});
