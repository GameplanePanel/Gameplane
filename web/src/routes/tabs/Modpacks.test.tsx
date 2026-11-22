import { ResourceTargetProvider } from "@/lib/resourceTarget";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { ModpacksTab } from "./Modpacks";
import type { GameTemplate, ModpackInstallBody, ModpackView, RegistryProject, RegistryVersion } from "@/types";

const fetchMock = vi.fn();
beforeEach(() => { scopedControl = true; vi.stubGlobal("fetch", fetchMock); });
afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

function jsonRes(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const operator = {
  id: 1, username: "u", displayName: "U", email: "", role: "operator",
  permissions: { "*": ["servers:read", "servers:write"] },
};
const viewer = { ...operator, role: "viewer", permissions: { "*": ["servers:read"] } };

type Modpacks = { refEnv?: string; env?: { name: string; value?: string }[] };

interface Routes {
  me?: typeof operator;
  packs?: RegistryProject[];
  deps?: { filename: string; downloadUrl: string }[];
  providers?: { provider: string; available: boolean; modpacks: boolean }[];
  onModpack?: (body: ModpackInstallBody, provider: string | null) => void;
  onInstallMod?: (body: { url: string; name?: string }) => void;
  versions?: RegistryVersion[];
}

function route(r: Routes) {
  scopedControl = r.me?.role !== "viewer";
  fetchMock.mockImplementation((url: string, opts?: { method?: string; body?: string }) => {
    const method = opts?.method ?? "GET";
    if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(r.me ?? operator));
    if (url.includes("/mods/registry/providers"))
      return Promise.resolve(jsonRes(r.providers ?? [{ provider: "modrinth", available: true, modpacks: true }]));
    if (url.includes("/mods/registry/search")) return Promise.resolve(jsonRes(r.packs ?? []));
    if (url.includes("/versions?")) return Promise.resolve(jsonRes(r.versions ?? []));
    if (url.includes("/mods/registry/projects/") && url.includes("/modpack"))
      return Promise.resolve(jsonRes(r.deps ?? []));
    if (url.endsWith("/servers/s1")) return Promise.resolve(jsonRes({ metadata: { name: "s1" }, spec: {} }));
    if (url.includes("/modpack") && method === "POST") {
      const provider = new URL("http://x" + url.slice(url.indexOf("/"))).searchParams.get("provider");
      r.onModpack?.(JSON.parse(opts?.body ?? "{}"), provider);
      return Promise.resolve(jsonRes(modpackView({ phase: "BackingUp" }), 202));
    }
    if (url.includes("/servers/s1/modpack")) return Promise.resolve(jsonRes(modpackView()));
    if (url.includes("/mods/install")) {
      const body = JSON.parse(opts?.body ?? "{}");
      r.onInstallMod?.(body);
      return Promise.resolve(jsonRes({ name: body.name ?? "x", size: 1 }));
    }
    return Promise.resolve(jsonRes({}));
  });
}

function tmpl(modpacks: Modpacks, provider = "modrinth"): GameTemplate {
  return {
    metadata: { name: "t" },
    spec: {
      displayName: "T", game: "g", version: "1", image: "img",
      capabilities: {
        mods: { loaders: { l: { path: "mods" } }, registry: { providers: [{ provider: provider as "modrinth", modpacks }] } },
      },
    },
  };
}

const providersOf = (provider: string) => [{ provider, available: true, modpacks: true }];

const pack: RegistryProject = {
  id: "cobblemon", slug: "cobblemon", title: "Cobblemon", author: "cobblemon",
  downloads: 9_100_000, provider: "modrinth",
};

const mrVersions: RegistryVersion[] = [
  { id: "v2", name: "2.0", versionNumber: "2.0", gameVersions: ["1.21.1"], loaders: ["fabric"], files: [] },
  { id: "v1", name: "1.0", versionNumber: "1.0", gameVersions: ["1.21.1"], loaders: ["fabric"], files: [] },
];
function modpackView(over: Partial<ModpackView> = {}): ModpackView {
  return { active: null, phase: "", message: "", backupName: "", keyShared: false, missingFiles: [],
    missingFilesUnknown: false, downloadsPath: "downloads/", backupAvailable: true, ...over };
}
// Install… -> pick newest version -> Continue -> confirm.
async function installNewest() {
  fireEvent.click(await screen.findByRole("button", { name: "Install…" }));
  await screen.findByText("2.0");
  fireEvent.click(screen.getByRole("button", { name: "Continue" }));
  fireEvent.click(await screen.findByRole("button", { name: "Install and restart" }));
}

describe("ModpacksTab", () => {
  it("env-mode: installs by pinning the chosen version", async () => {
    const calls: ModpackInstallBody[] = [];
    const providers: (string | null)[] = [];
    route({ packs: [pack], versions: mrVersions, onModpack: (b, p) => { calls.push(b); providers.push(p); } });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);

    await screen.findByText("Cobblemon");
    await installNewest();
    await waitFor(() =>
      expect(calls).toEqual([
        { provider: "modrinth", project: "cobblemon", version: "v2", title: "Cobblemon", versionName: "2.0", backupFirst: true },
      ]),
    );
    expect(providers).toEqual([null]); // provider moved from the query string into the body
    expect(await screen.findByText("Set modpack Cobblemon. The server is restarting to install it.")).toBeInTheDocument();
  });

  it("env-mode: does not show active modpack", async () => {
    route({ packs: [] });
    renderWithQuery(
      <ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />,
    );
    expect(screen.queryByText("Active modpack:")).not.toBeInTheDocument();
  });

  it("deps-mode: resolves and installs each dependency", async () => {
    const installed: Array<{ url: string; name?: string }> = [];
    route({
      providers: providersOf("thunderstore"),
      packs: [{ ...pack, title: "MegaPack", id: "packer-MegaPack" }],
      deps: [
        { filename: "a.zip", downloadUrl: "https://cdn/a.zip" },
        { filename: "b.zip", downloadUrl: "https://cdn/b.zip" },
      ],
      onInstallMod: (b) => installed.push(b),
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({}, "thunderstore")} />);

    fireEvent.click(await screen.findByRole("button", { name: /install/i }));
    await waitFor(() => expect(installed).toHaveLength(2));
    expect(installed[0]).toEqual({ url: "https://cdn/a.zip", name: "a.zip" });
    expect(await screen.findByText(/Installed MegaPack — 2 mods/)).toBeInTheDocument();
  });

  it("disables install for viewers", async () => {
    route({ me: viewer, packs: [pack] });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);
    await screen.findByText("Cobblemon");
    expect(screen.getByRole("button", { name: /install/i })).toBeDisabled();
  });

  it("filters by category chip and changes sort", async () => {
    const urls: string[] = [];
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers"))
        return Promise.resolve(jsonRes(providersOf("modrinth")));
      if (url.includes("/mods/registry/search")) {
        urls.push(url);
        return Promise.resolve(jsonRes([pack]));
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);
    await screen.findByText("Cobblemon");

    fireEvent.click(screen.getByRole("button", { name: "Tech" }));
    await waitFor(() => expect(urls.some((u) => u.includes("category=technology"))).toBe(true));

    // HeroUI v3's Select trigger is a role=button (aria-haspopup=listbox);
    // its options render in a portal-mounted listbox once opened — a plain
    // fireEvent.change on a hidden native <select> never reaches it.
    fireEvent.click(screen.getByRole("button", { name: /sort/i }));
    fireEvent.click(screen.getByRole("option", { name: /recently updated/i }));
    await waitFor(() => expect(urls.some((u) => u.includes("sort=updated"))).toBe(true));
    expect(urls.every((u) => u.includes("type=modpack"))).toBe(true);
  });

  it("switches providers", async () => {
    const urls: string[] = [];
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers"))
        return Promise.resolve(
          jsonRes([
            { provider: "modrinth", available: true, modpacks: true },
            { provider: "thunderstore", available: true, modpacks: true },
          ]),
        );
      if (url.includes("/mods/registry/search")) {
        urls.push(url);
        return Promise.resolve(jsonRes([pack]));
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);
    await screen.findByText("Cobblemon");
    await waitFor(() => expect(urls.some((u) => u.includes("provider=modrinth"))).toBe(true));

    // Provider switcher is a HeroUI Tabs list, not buttons — the accessible
    // role for each provider is "tab".
    fireEvent.click(screen.getByRole("tab", { name: "Thunderstore" }));
    await waitFor(() => expect(urls.some((u) => u.includes("provider=thunderstore"))).toBe(true));
  });

  it("pages with load more", async () => {
    const page1 = Array.from({ length: 24 }, (_, i) => ({ ...pack, id: `p${i}`, title: `Pack ${i}` }));
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers"))
        return Promise.resolve(jsonRes(providersOf("modrinth")));
      if (url.includes("/mods/registry/search")) {
        const off = new URL("http://x" + url.slice(url.indexOf("/"))).searchParams.get("offset");
        return Promise.resolve(jsonRes(off ? [{ ...pack, id: "last", title: "Last Pack" }] : page1));
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);
    await screen.findByText("Pack 0");
    fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
    expect(await screen.findByText("Last Pack")).toBeInTheDocument();
  });

  it("shows the unavailable state when no provider is usable", async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      // No usable provider → the browser shows the unavailable state.
      if (url.includes("/mods/registry/providers")) return Promise.resolve(jsonRes([]));
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({})} />);
    expect(await screen.findByText(/isn’t available/)).toBeInTheDocument();
  });

  it("surfaces a search error", async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers"))
        return Promise.resolve(jsonRes(providersOf("modrinth")));
      if (url.includes("/mods/registry/search")) return Promise.resolve(jsonRes({ error: "boom" }, 502));
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({})} />);
    expect(await screen.findByText("boom")).toBeInTheDocument();
  });

  it("env-mode: surfaces install error", async () => {
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers"))
        return Promise.resolve(jsonRes(providersOf("modrinth")));
      if (url.includes("/mods/registry/search")) return Promise.resolve(jsonRes([pack]));
      if (url.includes("/versions?")) return Promise.resolve(jsonRes(mrVersions));
      if (url.includes("/modpack") && opts?.method === "POST")
        return Promise.resolve(jsonRes({ error: "failed to restart server" }, 500));
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);

    await screen.findByText("Cobblemon");
    await installNewest();
    expect(await screen.findByText("failed to restart server")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install and restart" })).toBeInTheDocument(); // dialog stays open
  });

  it("deps-mode: surfaces install error for first mod", async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers"))
        return Promise.resolve(jsonRes(providersOf("thunderstore")));
      if (url.includes("/mods/registry/search"))
        return Promise.resolve(jsonRes([{ ...pack, title: "MegaPack", id: "packer-MegaPack" }]));
      if (url.includes("/mods/registry/projects"))
        return Promise.resolve(
          jsonRes([
            { filename: "a.zip", downloadUrl: "https://cdn/a.zip" },
            { filename: "b.zip", downloadUrl: "https://cdn/b.zip" },
          ]),
        );
      if (url.includes("/mods/install"))
        return Promise.resolve(jsonRes({ error: "network timeout" }, 500));
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({}, "thunderstore")} />);

    fireEvent.click(await screen.findByRole("button", { name: /install/i }));
    expect(await screen.findByText("network timeout")).toBeInTheDocument();
  });

  it("dismisses the error banner when dismiss is clicked", async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers")) return Promise.resolve(jsonRes(providersOf("thunderstore")));
      if (url.includes("/mods/registry/search")) return Promise.resolve(jsonRes([{ ...pack, id: "packer-Mega", title: "MegaPack" }]));
      if (url.includes("/mods/registry/projects"))
        return Promise.resolve(jsonRes([{ filename: "a.zip", downloadUrl: "https://cdn/a.zip" }]));
      if (url.includes("/mods/install")) return Promise.resolve(jsonRes({ error: "install failed" }, 500));
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({}, "thunderstore")} />);

    await screen.findByText("MegaPack");
    fireEvent.click(await screen.findByRole("button", { name: /install/i }));
    expect(await screen.findByText("install failed")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "dismiss" }));
    await waitFor(() => expect(screen.queryByText("install failed")).not.toBeInTheDocument());
  });

  it("install button stays disabled while one is being installed", async () => {
    let delayResolve: () => void;
    const delayed = new Promise<void>((resolve) => { delayResolve = resolve; });
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers")) return Promise.resolve(jsonRes(providersOf("thunderstore")));
      if (url.includes("/mods/registry/search"))
        return Promise.resolve(jsonRes([pack, { ...pack, id: "pack2", title: "Pack 2" }]));
      if (url.includes("/mods/registry/projects")) return delayed.then(() => jsonRes([]));
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({}, "thunderstore")} />);

    await screen.findByText("Cobblemon");
    const buttons = screen.getAllByRole("button", { name: /install/i });
    expect(buttons).toHaveLength(2);
    fireEvent.click(buttons[0]);
    await waitFor(() => expect(buttons[0]).toHaveTextContent("Installing…"));
    expect(buttons[1]).toBeDisabled();
    delayResolve!();
  });

  it("env-mode: the confirm button is disabled while the install is submitted", async () => {
    let delayResolve: () => void;
    const delayed = new Promise<void>((resolve) => { delayResolve = resolve; });
    route({ packs: [pack], versions: mrVersions });
    const base = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) =>
      url.includes("/servers/s1/modpack") && opts?.method === "POST"
        ? delayed.then(() => jsonRes(modpackView({ phase: "BackingUp" }), 202))
        : base(url, opts));
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);

    await screen.findByText("Cobblemon");
    await installNewest();
    expect(await screen.findByRole("button", { name: "Working…" })).toBeDisabled();
    delayResolve!();
  });

  it("shows active modpack when none is set", async () => {
    route({ packs: [] });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);

    // The active modpack banner should not appear
    expect(screen.queryByText("Active modpack:")).not.toBeInTheDocument();
  });

  it("handles APIError with JSON error message", async () => {
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers"))
        return Promise.resolve(jsonRes(providersOf("modrinth")));
      if (url.includes("/mods/registry/search")) return Promise.resolve(jsonRes([pack]));
      if (url.includes("/versions?")) return Promise.resolve(jsonRes(mrVersions));
      if (url.includes("/modpack") && opts?.method === "POST")
        return Promise.resolve(jsonRes({ error: "specific error from API" }, 500));
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);

    await screen.findByText("Cobblemon");
    await installNewest();
    expect(await screen.findByText("specific error from API")).toBeInTheDocument();
  });

  it("deps-mode: handles 403 error with role message", async () => {
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers")) return Promise.resolve(jsonRes(providersOf("thunderstore")));
      if (url.includes("/mods/registry/search")) return Promise.resolve(jsonRes([pack]));
      if (url.includes("/mods/registry/projects"))
        return Promise.resolve(jsonRes([{ filename: "a.zip", downloadUrl: "https://cdn/a.zip" }]));
      if (url.includes("/mods/install")) return Promise.resolve(new Response("forbidden", { status: 403 }));
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({}, "thunderstore")} />);
    await screen.findByText("Cobblemon");
    fireEvent.click(await screen.findByRole("button", { name: /install/i }));
    expect(await screen.findByText("Your role does not allow managing this server.")).toBeInTheDocument();
  });

  it("env-mode: handles 403 error with role message", async () => {
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
      if (url.includes("/mods/registry/providers"))
        return Promise.resolve(jsonRes(providersOf("modrinth")));
      if (url.includes("/mods/registry/search")) return Promise.resolve(jsonRes([pack]));
      if (url.includes("/versions?")) return Promise.resolve(jsonRes(mrVersions));
      if (url.includes("/modpack") && opts?.method === "POST")
        return Promise.resolve(new Response("forbidden", { status: 403 }));
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);

    await screen.findByText("Cobblemon");
    await installNewest();
    expect(await screen.findByText("Your role does not allow managing this server.")).toBeInTheDocument();
  });

  it("shows success banner for env-mode install", async () => {
    route({ packs: [pack], versions: mrVersions });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);

    await screen.findByText("Cobblemon");
    await installNewest();
    expect(await screen.findByText(/Set modpack Cobblemon/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "dismiss" }));
    await waitFor(() => expect(screen.queryByText(/Set modpack Cobblemon/)).not.toBeInTheDocument());
  });

  it("shows success banner with mod count for deps-mode install", async () => {
    route({
      providers: providersOf("thunderstore"),
      packs: [{ ...pack, title: "Single Mod", id: "packer-Single" }],
      deps: [{ filename: "mod.zip", downloadUrl: "https://cdn/mod.zip" }],
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({}, "thunderstore")} />);

    fireEvent.click(await screen.findByRole("button", { name: /install/i }));
    expect(await screen.findByText(/Installed Single Mod — 1 mod\./)).toBeInTheDocument();
  });

  it("pluralizes mod count in success message", async () => {
    route({
      providers: providersOf("thunderstore"),
      packs: [{ ...pack, title: "Multi", id: "packer-Multi" }],
      deps: [
        { filename: "a.zip", downloadUrl: "https://cdn/a.zip" },
        { filename: "b.zip", downloadUrl: "https://cdn/b.zip" },
        { filename: "c.zip", downloadUrl: "https://cdn/c.zip" },
      ],
    });
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({}, "thunderstore")} />);

    fireEvent.click(await screen.findByRole("button", { name: /install/i }));
    expect(await screen.findByText(/Installed Multi — 3 mods\./)).toBeInTheDocument();
  });

  it("env-mode: shows the working state while submitting", async () => {
    let delayResolve: () => void;
    const delayed = new Promise<void>((resolve) => { delayResolve = resolve; });
    route({ packs: [pack], versions: mrVersions });
    const base = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) =>
      url.includes("/servers/s1/modpack") && opts?.method === "POST"
        ? delayed.then(() => jsonRes(modpackView({ phase: "BackingUp" }), 202))
        : base(url, opts));
    renderWithQuery(<ModpacksTab name="s1" tmpl={tmpl({ refEnv: "MODRINTH_MODPACK" })} />);

    await screen.findByText("Cobblemon");
    await installNewest();
    expect(await screen.findByRole("button", { name: "Working…" })).toBeInTheDocument();
    delayResolve!();
  });
});

let scopedControl = true;
function renderWithQuery(ui: ReactElement, options?: Parameters<typeof baseRenderWithQuery>[1]) {
  const props = ui.props as { name?: string; ns?: string };
  return baseRenderWithQuery(<ResourceTargetProvider target={{ cluster: "local", name: props.name ?? "s1", namespace: props.ns }} access={{ canWrite: scopedControl, canControl: scopedControl, canConsole: scopedControl, canDelete: false, isOwner: false, isCollaborator: false, permissions: scopedControl ? ["servers:read", "servers:write"] : ["servers:read"] }}>{ui}</ResourceTargetProvider>, options);
}
