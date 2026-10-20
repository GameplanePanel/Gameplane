import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";

import { ModulesPage } from "./Modules";
import type { CatalogEntry } from "@/types";

// Modules.tsx calls Modules.catalog/install/upgrade/uninstall — stub
// the entire endpoints module so the test asserts on the wire calls
// without spinning up fetch mocks.
const catalog = vi.fn();
const install = vi.fn();
const upgrade = vi.fn();
const uninstall = vi.fn();
const removeUpload = vi.fn();
const listSources = vi.fn();
const archetypes = vi.fn();
vi.mock("@/lib/endpoints", () => ({
  Modules: {
    catalog: () => catalog(),
    install: (body: unknown) => install(body),
    upgrade: (name: string, version: string) => upgrade(name, version),
    uninstall: (name: string) => uninstall(name),
  },
  ModuleSources: {
    list: () => listSources(),
    removeUpload: (source: string, module: string) => removeUpload(source, module),
  },
  ModuleBuilder: {
    archetypes: () => archetypes(),
    scaffold: vi.fn(),
    validate: vi.fn(),
    preview: vi.fn(),
    installToCluster: vi.fn(),
    downloadArchive: vi.fn(),
  },
}));

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children, ...rest }: { children: ReactNode } & Record<string, unknown>) => (
    <a {...rest}>{children}</a>
  ),
}));

const MINECRAFT: CatalogEntry = {
  name: "minecraft-java",
  displayName: "Minecraft (Java)",
  summary: "Vanilla / Paper / Forge",
  game: "minecraft-java",
  sources: [{ name: "default", type: "oci" }],
  versions: ["1.1.0", "1.0.0"],
  latestVersion: "1.1.0",
  installed: false,
};

const VALHEIM_INSTALLED: CatalogEntry = {
  name: "valheim",
  displayName: "Valheim",
  sources: [{ name: "default", type: "oci" }],
  versions: ["0.9.0"],
  latestVersion: "0.9.0",
  installed: true,
  installedVersion: "0.9.0",
  installedFrom: "default",
  moduleName: "valheim",
  phase: "Ready",
};

const TERRARIA_UPGRADE: CatalogEntry = {
  name: "terraria",
  displayName: "Terraria",
  sources: [{ name: "default", type: "oci" }],
  versions: ["1.1.0", "1.0.0"],
  latestVersion: "1.1.0",
  installed: true,
  installedVersion: "1.0.0",
  installedFrom: "default",
  moduleName: "terraria",
  phase: "Ready",
};

const CUSTOM_UPLOADED: CatalogEntry = {
  name: "custom-game",
  displayName: "Custom Game",
  summary: "Custom uploaded module",
  game: "custom",
  sources: [{ name: "uploads", type: "upload" }],
  versions: ["1.0.0"],
  latestVersion: "1.0.0",
  installed: false,
};

function renderPage() {
  return renderWithQuery(<ModulesPage />);
}

afterEach(() => {
  catalog.mockReset();
  install.mockReset();
  upgrade.mockReset();
  uninstall.mockReset();
  removeUpload.mockReset();
  listSources.mockReset();
  archetypes.mockReset();
});

// Provide default mock for listSources to avoid hanging
vi.mocked(listSources).mockResolvedValue({
  items: [{ metadata: { name: "default" }, spec: { type: "oci" } }],
});

describe("ModulesPage", () => {
  it("renders the catalog with installation state per entry", async () => {
    catalog.mockResolvedValue({ items: [MINECRAFT, VALHEIM_INSTALLED, TERRARIA_UPGRADE] });
    renderPage();

    expect(await screen.findByText("Minecraft (Java)")).toBeInTheDocument();
    expect(screen.getByText("Valheim")).toBeInTheDocument();
    expect(screen.getByText("Terraria")).toBeInTheDocument();

    // Minecraft is not installed → version label shows the latest tag.
    expect(screen.getByText(/v1\.1\.0$/)).toBeInTheDocument();
    // Valheim is installed → "v0.9.0 installed".
    expect(screen.getByText(/v0\.9\.0 installed/)).toBeInTheDocument();
    // Terraria has an upgrade available.
    expect(screen.getByText(/v1\.0\.0 installed.*v1\.1\.0 available/)).toBeInTheDocument();
  });

  it("filters the catalog by game category (multi-select)", async () => {
    const minecraftEntry = { ...MINECRAFT, categories: ["Sandbox", "Survival"] };
    const valheimEntry = { ...VALHEIM_INSTALLED, game: "valheim", categories: ["Survival"] };
    catalog.mockResolvedValue({
      items: [minecraftEntry, valheimEntry],
    });
    renderPage();
    expect(await screen.findByText("Minecraft (Java)")).toBeInTheDocument();
    expect(screen.getByText("Valheim")).toBeInTheDocument();

    // Both declared categories should have chips. Click Sandbox to filter.
    const sandboxBtn = screen.getByRole("button", { name: "Sandbox" });
    expect(sandboxBtn).toBeInTheDocument();
    await userEvent.click(sandboxBtn);
    expect(screen.getByText("Minecraft (Java)")).toBeInTheDocument();
    expect(screen.queryByText("Valheim")).toBeNull();

    // Click Survival to show only modules with both Sandbox AND Survival
    const survivalBtn = screen.getByRole("button", { name: "Survival" });
    await userEvent.click(survivalBtn);
    expect(screen.getByText("Minecraft (Java)")).toBeInTheDocument();
    expect(screen.queryByText("Valheim")).not.toBeInTheDocument();
  });

  it("shows modules matching all of the selected categories (multi-select AND)", async () => {
    const minecraftEntry = { ...MINECRAFT, categories: ["Sandbox", "Survival"] };
    const valheimEntry = { ...VALHEIM_INSTALLED, game: "valheim", categories: ["Survival"] };
    const terraria = { ...TERRARIA_UPGRADE, game: "terraria", categories: ["Sandbox", "Modded"] };
    catalog.mockResolvedValue({
      items: [minecraftEntry, valheimEntry, terraria],
    });
    renderPage();
    expect(await screen.findByText("Minecraft (Java)")).toBeInTheDocument();
    expect(screen.getByText("Valheim")).toBeInTheDocument();
    expect(screen.getByText("Terraria")).toBeInTheDocument();

    // Click Sandbox: shows Minecraft and Terraria (both have Sandbox)
    await userEvent.click(screen.getByRole("button", { name: "Sandbox" }));
    expect(screen.getByText("Minecraft (Java)")).toBeInTheDocument();
    expect(screen.queryByText("Valheim")).not.toBeInTheDocument();
    expect(screen.getByText("Terraria")).toBeInTheDocument();

    // Click Survival: now shows only Minecraft (must have BOTH Sandbox AND Survival)
    await userEvent.click(screen.getByRole("button", { name: "Survival" }));
    expect(screen.getByText("Minecraft (Java)")).toBeInTheDocument();
    expect(screen.queryByText("Valheim")).not.toBeInTheDocument();
    expect(screen.queryByText("Terraria")).not.toBeInTheDocument();

    // Click Survival again to deselect it: shows Minecraft and Terraria (only Sandbox required)
    await userEvent.click(screen.getByRole("button", { name: "Survival" }));
    expect(screen.getByText("Minecraft (Java)")).toBeInTheDocument();
    expect(screen.queryByText("Valheim")).not.toBeInTheDocument();
    expect(screen.getByText("Terraria")).toBeInTheDocument();

    // Click Sandbox to deselect it: shows all (no filters)
    await userEvent.click(screen.getByRole("button", { name: "Sandbox" }));
    expect(screen.getByText("Minecraft (Java)")).toBeInTheDocument();
    expect(screen.getByText("Valheim")).toBeInTheDocument();
    expect(screen.getByText("Terraria")).toBeInTheDocument();
  });

  it("opens the install dialog and POSTs /modules with the chosen version", async () => {
    catalog.mockResolvedValue({ items: [MINECRAFT] });
    install.mockResolvedValue({});
    renderPage();

    await screen.findByText("Minecraft (Java)");
    await userEvent.click(screen.getByRole("button", { name: /install/i }));

    // Dialog appears with "Install minecraft-java" title.
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    // Confirm install.
    const confirms = screen.getAllByRole("button", { name: /^install$/i });
    await userEvent.click(confirms[confirms.length - 1]);

    await waitFor(() => expect(install).toHaveBeenCalledTimes(1));
    expect(install.mock.calls[0][0]).toMatchObject({
      source: "default",
      module: "minecraft-java",
      name: "minecraft-java",
      version: "1.1.0",
    });
  });

  it("upgrades an installed module to the latest version", async () => {
    catalog.mockResolvedValue({ items: [TERRARIA_UPGRADE] });
    upgrade.mockResolvedValue({});
    renderPage();

    await screen.findByText("Terraria");
    await userEvent.click(screen.getByRole("button", { name: /upgrade/i }));

    await waitFor(() => expect(upgrade).toHaveBeenCalledWith("terraria", "1.1.0"));
  });

  it("uninstalls after confirmation", async () => {
    catalog.mockResolvedValue({ items: [VALHEIM_INSTALLED] });
    uninstall.mockResolvedValue(undefined);
    renderPage();

    await screen.findByText("Valheim");
    await userEvent.click(screen.getByRole("button", { name: /uninstall/i }));
    // Confirm dialog button (label "Uninstall" again, in the dialog).
    const buttons = await screen.findAllByRole("button", { name: /uninstall/i });
    await userEvent.click(buttons[buttons.length - 1]);

    await waitFor(() => expect(uninstall).toHaveBeenCalledWith("valheim"));
  });

  it("shows the upload action only when an upload-type source exists", async () => {
    catalog.mockResolvedValue({ items: [MINECRAFT] });
    listSources.mockResolvedValue({
      items: [
        { metadata: { name: "default" }, spec: { type: "oci" } },
        { metadata: { name: "uploads" }, spec: { type: "upload" } },
      ],
    });
    renderPage();

    await screen.findByText("Minecraft (Java)");
    const uploadBtn = await screen.findByRole("button", { name: /upload module/i });
    await userEvent.click(uploadBtn);
    expect(await screen.findByText(/Choose a bundle archive/)).toBeInTheDocument();
  });

  it("hides the upload action without upload sources", async () => {
    catalog.mockResolvedValue({ items: [MINECRAFT] });
    listSources.mockResolvedValue({
      items: [{ metadata: { name: "default" }, spec: { type: "oci" } }],
    });
    renderPage();
    await screen.findByText("Minecraft (Java)");
    expect(screen.queryByRole("button", { name: /upload module/i })).not.toBeInTheDocument();
  });

  it("shows 'Remove upload' button only on upload-type entries", async () => {
    catalog.mockResolvedValue({ items: [MINECRAFT, CUSTOM_UPLOADED] });
    renderPage();

    await screen.findByText("Minecraft (Java)");

    // There should be exactly one "Remove upload" button (for CUSTOM_UPLOADED)
    expect(screen.getByRole("button", { name: /remove upload/i })).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: /remove upload/i })).toHaveLength(1);
  });

  it("removes an uploaded module after confirmation", async () => {
    catalog.mockResolvedValue({ items: [CUSTOM_UPLOADED] });
    removeUpload.mockResolvedValue(undefined);
    renderPage();

    await screen.findByText("Custom Game");
    await userEvent.click(screen.getByRole("button", { name: /remove upload/i }));

    // Confirm dialog button (label "Remove upload" in the dialog).
    const buttons = await screen.findAllByRole("button", { name: /remove upload/i });
    await userEvent.click(buttons[buttons.length - 1]);

    await waitFor(() =>
      expect(removeUpload).toHaveBeenCalledWith("uploads", "custom-game"),
    );
  });

  it("filters by source name", async () => {
    const mc = { ...MINECRAFT, sources: [{ name: "default", type: "oci" }] };
    const val = { ...VALHEIM_INSTALLED, sources: [{ name: "community", type: "git" }] };
    catalog.mockResolvedValue({ items: [mc, val] });
    renderPage();

    await screen.findByText("Minecraft (Java)");
    expect(screen.getByText("Valheim")).toBeInTheDocument();

    // Click the "community" source filter
    await userEvent.click(screen.getByRole("button", { name: "community" }));
    expect(screen.queryByText("Minecraft (Java)")).not.toBeInTheDocument();
    expect(screen.getByText("Valheim")).toBeInTheDocument();
  });

  it("searches modules by display name", async () => {
    const mc = { ...MINECRAFT, displayName: "Minecraft (Java)" };
    const val = { ...VALHEIM_INSTALLED, displayName: "Valheim" };
    catalog.mockResolvedValue({ items: [mc, val] });
    renderPage();

    await screen.findByText("Minecraft (Java)");
    const input = screen.getByRole("textbox", { name: /search modules/i });
    await userEvent.type(input, "valheim");
    expect(screen.queryByText("Minecraft (Java)")).not.toBeInTheDocument();
    expect(screen.getByText("Valheim")).toBeInTheDocument();
  });

  it("searches case-insensitively", async () => {
    const mc = { ...MINECRAFT, displayName: "Minecraft (Java)" };
    catalog.mockResolvedValue({ items: [mc] });
    renderPage();

    await screen.findByText("Minecraft (Java)");
    const input = screen.getByRole("textbox", { name: /search modules/i });
    await userEvent.type(input, "MINECRAFT");
    expect(screen.getByText("Minecraft (Java)")).toBeInTheDocument();
  });

  it("shows empty message when all modules are filtered out", async () => {
    catalog.mockResolvedValue({ items: [MINECRAFT] });
    renderPage();

    await screen.findByText("Minecraft (Java)");
    const input = screen.getByRole("textbox", { name: /search modules/i });
    await userEvent.type(input, "nonexistent");
    expect(
      screen.getByText(/No modules match the current filter/),
    ).toBeInTheDocument();
    expect(screen.queryByText("Minecraft (Java)")).not.toBeInTheDocument();
  });

  it("lets an empty catalog open the source editor through Manage sources", async () => {
    catalog.mockResolvedValue({ items: [] });
    listSources.mockResolvedValue({ items: [] });
    renderPage();

    await waitFor(() =>
      expect(
        screen.getByText(/No modules in any catalog yet/),
      ).toBeInTheDocument(),
    );
    await userEvent.click(screen.getByRole("button", { name: "Manage sources" }));
    expect(await screen.findByRole("button", { name: "Add source" })).toBeEnabled();
  });

  it("shows install error on the page", async () => {
    catalog.mockResolvedValue({ items: [MINECRAFT] });
    install.mockRejectedValue(new Error("network timeout"));
    const { container } = renderPage();

    await screen.findByText("Minecraft (Java)");
    await userEvent.click(screen.getByRole("button", { name: /install/i }));
    const confirms = screen.getAllByRole("button", { name: /^install$/i });
    await userEvent.click(confirms[confirms.length - 1]);

    // The install error surfaces both inside the still-open InstallDialog
    // (local validation-style error) and in the page-level banner — the
    // dialog is portaled to document.body, so scope to the render's own
    // container to assert on the page banner specifically and avoid a
    // "multiple elements" match against the dialog's copy.
    await waitFor(() =>
      expect(within(container).getByText(/network timeout/)).toBeInTheDocument(),
    );
  });

  it("shows upgrade error on the page", async () => {
    catalog.mockResolvedValue({ items: [TERRARIA_UPGRADE] });
    upgrade.mockRejectedValue(new Error("permission denied"));
    renderPage();

    await screen.findByText("Terraria");
    await userEvent.click(screen.getByRole("button", { name: /upgrade/i }));

    await waitFor(() =>
      expect(screen.getByText(/permission denied/)).toBeInTheDocument(),
    );
  });

  it("shows uninstall error on the page", async () => {
    catalog.mockResolvedValue({ items: [VALHEIM_INSTALLED] });
    uninstall.mockRejectedValue(new Error("servers still running"));
    renderPage();

    await screen.findByText("Valheim");
    await userEvent.click(screen.getByRole("button", { name: /uninstall/i }));
    const buttons = await screen.findAllByRole("button", { name: /uninstall/i });
    await userEvent.click(buttons[buttons.length - 1]);

    await waitFor(() =>
      expect(screen.getByText(/servers still running/)).toBeInTheDocument(),
    );
  });

  it("clears page error when install succeeds", async () => {
    catalog.mockResolvedValue({ items: [MINECRAFT] });
    install.mockRejectedValueOnce(new Error("first attempt failed"))
      .mockResolvedValueOnce({});
    const { container } = renderPage();

    await screen.findByText("Minecraft (Java)");
    await userEvent.click(screen.getByRole("button", { name: /install/i }));
    let confirms = screen.getAllByRole("button", { name: /^install$/i });
    await userEvent.click(confirms[confirms.length - 1]);

    // Same duplicate-rendering issue as "shows install error on the page":
    // scope to the render's own container so this matches only the
    // page-level banner, not the InstallDialog's portaled copy.
    await waitFor(() =>
      expect(within(container).getByText(/first attempt failed/)).toBeInTheDocument(),
    );

    // Try again; error should clear on success
    await userEvent.click(screen.getByRole("button", { name: /install/i }));
    confirms = screen.getAllByRole("button", { name: /^install$/i });
    await userEvent.click(confirms[confirms.length - 1]);

    await waitFor(() =>
      expect(within(container).queryByText(/first attempt failed/)).not.toBeInTheDocument(),
    );
  });

  it("filters 'All categories' clears the category set", async () => {
    // Minecraft is Sandbox-only; Valheim is Survival-only, so selecting
    // Survival hides Minecraft (matchesAllCategories requires all selected tags).
    // Clicking the "All categories" chip itself — not re-toggling Survival
    // — must reset the filter regardless of what's currently selected.
    const mc = { ...MINECRAFT, categories: ["Sandbox"] };
    const val = { ...VALHEIM_INSTALLED, categories: ["Survival"] };
    catalog.mockResolvedValue({ items: [mc, val] });
    renderPage();

    await screen.findByText("Minecraft (Java)");
    await userEvent.click(screen.getByRole("button", { name: "Survival" }));
    await waitFor(() => {
      expect(screen.queryByText("Minecraft (Java)")).not.toBeInTheDocument();
    });

    // Click "All categories" to clear the filter (not a re-toggle).
    await userEvent.click(screen.getByRole("button", { name: "All categories" }));
    await waitFor(() => {
      expect(screen.getByText("Minecraft (Java)")).toBeInTheDocument();
    });
  });

  it("disables buttons while mutations are pending", async () => {
    catalog.mockResolvedValue({ items: [MINECRAFT, VALHEIM_INSTALLED] });
    install.mockImplementation(() => new Promise(() => {})); // never resolves
    renderPage();

    await screen.findByText("Minecraft (Java)");
    const installBtns = screen.getAllByRole("button", { name: /install/i });
    await userEvent.click(installBtns[0]);
    const confirms = screen.getAllByRole("button", { name: /^install$/i });
    await userEvent.click(confirms[confirms.length - 1]);

    // While the mutation is pending, other action buttons should be disabled.
    // The uninstall button for Valheim should be disabled during pending
    // install — the pending InstallDialog stays open the whole time (it
    // never resolves), and Radix marks the rest of the page aria-hidden
    // while it's open, so this must opt in to hidden elements to find it.
    await waitFor(() => {
      const uninstallBtn = screen.getByRole("button", { name: /uninstall/i, hidden: true });
      expect(uninstallBtn).toBeDisabled();
    }, { timeout: 2000 });
  });

  it("opens BuildModuleDialog when clicking Create module", async () => {
    catalog.mockResolvedValue({ items: [MINECRAFT] });
    archetypes.mockResolvedValue({
      archetypes: [
        {
          id: "steamcmd",
          title: "SteamCMD Dedicated Server",
          description: "Dedicated game server installed and managed via SteamCMD (Valve UDP ports, save volume, non-root user)",
          defaultImage: "cm2network/steamcmd:root@sha256:4d830b0475b8719f96b9978ba57404434bb3da3f260388d75cfb373cf5889ea8",
          defaultPorts: [
            { name: "game", containerPort: 27015, protocol: "UDP", advertise: true },
            { name: "query", containerPort: 27016, protocol: "UDP", advertise: true },
          ],
          defaultStorage: {
            size: "20Gi",
            mountPath: "/serverdata",
          },
          defaultEnv: [
            { name: "STEAMAPPID", value: "0" },
            { name: "SERVER_NAME", value: "Game Server" },
          ],
          configSchema: [
            {
              name: "SERVER_PASSWORD",
              displayName: "Server Password",
              description: "Password required for players to join the server",
              type: "password",
              required: false,
            },
            {
              name: "MAX_PLAYERS",
              displayName: "Maximum Players",
              description: "Maximum allowed concurrent players",
              type: "int",
              default: "16",
              min: 1,
              max: 128,
            },
          ],
          capabilities: {
            lifecycle: {
              stop: ["quit"],
            },
          },
          defaultCategories: ["Survival", "Co-op"],
        },
      ],
    });
    renderPage();

    await screen.findByText("Minecraft (Java)");
    const createBtn = screen.getByRole("button", { name: /Create module/i });
    expect(createBtn).toBeInTheDocument();

    await userEvent.click(createBtn);
    expect(await screen.findByText("Create game module")).toBeInTheDocument();
    expect(await screen.findByText("SteamCMD Dedicated Server")).toBeInTheDocument();
  });
});
