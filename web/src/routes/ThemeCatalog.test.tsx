import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";
import type { ThemeCatalogItem, ThemeCatalogResponse, User } from "@/types";

import { ThemeCatalogPage } from "./ThemeCatalog";

const catalog = vi.fn<() => Promise<ThemeCatalogResponse>>();
const installed = vi.fn();
const install = vi.fn();
const updateInstalled = vi.fn();
const removeInstalled = vi.fn();
const catalogEntry = vi.fn();
const navigate = vi.fn();
const startPreview = vi.fn(() => true);

vi.mock("@/lib/endpoints", () => ({
  Themes: {
    catalog: () => catalog(),
    catalogEntry: (id: number) => catalogEntry(id),
    previewUrl: (id: number, mode: string) => `/themes/catalog/${id}/preview/${mode}`,
    installed: () => installed(),
    install: (body: unknown) => install(body),
    updateInstalled: (id: number) => updateInstalled(id),
    removeInstalled: (id: number) => removeInstalled(id),
  },
}));

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
  Link: ({ children, ...rest }: { children: ReactNode } & Record<string, unknown>) => <a {...rest}>{children}</a>,
}));

vi.mock("@/lib/useThemePreview", () => ({
  useThemePreview: () => ({ preview: null, start: startPreview, exit: vi.fn() }),
}));

let currentUser: User = makeUser();
vi.mock("@/lib/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/auth")>()),
  useMe: () => ({ data: currentUser }),
}));

const AURORA: ThemeCatalogItem = {
  id: 1,
  source: "official",
  name: "aurora-night",
  displayName: "Aurora Night",
  author: "Ada Reyes",
  description: "Deep blue surfaces.",
  tags: ["dark", "calm"],
  license: "MIT",
  version: "1.3.0",
  modes: ["dark"],
  status: "valid",
  lowContrast: false,
  installed: { version: "1.2.0" },
  updateAvailable: true,
};

const PAPER: ThemeCatalogItem = {
  id: 2,
  source: "official",
  name: "paper-light",
  displayName: "Paper Light",
  author: "Mira Okafor",
  description: "Warm paper tones.",
  tags: ["light"],
  license: "MIT",
  version: "1.0.3",
  modes: ["light", "dark"],
  status: "valid",
  lowContrast: false,
  installed: { version: "1.0.3" },
  updateAvailable: false,
};

const EMBER: ThemeCatalogItem = {
  id: 3,
  source: "official",
  name: "ember",
  displayName: "Ember",
  author: "Jon Vasquez",
  description: "Charcoal and amber.",
  tags: ["dark"],
  license: "MIT",
  version: "1.8.0",
  modes: ["light", "dark"],
  status: "valid",
  lowContrast: false,
  updateAvailable: false,
};

const BROKEN: ThemeCatalogItem = {
  id: 4,
  source: "community",
  name: "broken-pastel",
  displayName: "Broken Pastel",
  author: "Ines Duarte",
  description: "Three problems block installation.",
  tags: ["light"],
  license: "MIT",
  version: "0.3.0",
  modes: ["light", "dark"],
  status: "invalid",
  lowContrast: false,
  updateAvailable: false,
};

const THEMES: ThemeCatalogItem[] = [AURORA, PAPER, EMBER, BROKEN];

function cardFor(name: string): HTMLElement {
  const cards = screen.getAllByTestId("theme-card");
  const found = cards.find((card) => within(card).queryByText(name) !== null);
  if (!found) throw new Error(`no card for ${name}`);
  return found;
}

beforeEach(() => {
  currentUser = makeUser();
  catalog.mockResolvedValue({ themes: THEMES, sources: [] });
  installed.mockResolvedValue([
    {
      id: 10,
      source: "official",
      name: "aurora-night",
      displayName: "Aurora Night",
      version: "1.2.0",
      modes: ["dark"],
      updateAvailable: true,
      sourceRemoved: false,
      selected: false,
    },
    {
      id: 11,
      source: "official",
      name: "paper-light",
      displayName: "Paper Light",
      version: "1.0.3",
      modes: ["light", "dark"],
      updateAvailable: false,
      sourceRemoved: false,
      selected: false,
    },
  ]);
  install.mockResolvedValue({});
  updateInstalled.mockResolvedValue({});
  removeInstalled.mockResolvedValue(undefined);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("ThemeCatalogPage", () => {
  it("renders one card per theme with the state badges", async () => {
    renderWithQuery(<ThemeCatalogPage />);

    await screen.findByText("Aurora Night");
    expect(screen.getAllByTestId("theme-card")).toHaveLength(4);

    expect(within(cardFor("Aurora Night")).getByText("Update available")).toBeInTheDocument();
    expect(within(cardFor("Paper Light")).getByText("Installed")).toBeInTheDocument();
    expect(within(cardFor("Broken Pastel")).getByText("Invalid")).toBeInTheDocument();
  });

  it("hides Preview for invalid themes and offers Remove for installed ones", async () => {
    renderWithQuery(<ThemeCatalogPage />);
    await screen.findByText("Broken Pastel");

    expect(within(cardFor("Broken Pastel")).queryByRole("button", { name: "Preview" })).toBeNull();
    expect(within(cardFor("Broken Pastel")).getByRole("button", { name: "Install" })).toBeDisabled();
    expect(within(cardFor("Paper Light")).getByRole("button", { name: "Remove" })).toBeInTheDocument();
    expect(within(cardFor("Aurora Night")).getByRole("button", { name: "Update" })).toBeInTheDocument();
  });

  it("shows a warning for each source whose last refresh failed", async () => {
    catalog.mockResolvedValue({
      themes: THEMES,
      sources: [{ name: "community", lastError: "connection timed out after 30s" }],
    });
    renderWithQuery(<ThemeCatalogPage />);

    const banner = await screen.findByRole("status");
    expect(banner).toHaveTextContent("Source community could not be refreshed: connection timed out after 30s.");
  });

  it("shows the empty state with a link to sources for an administrator", async () => {
    catalog.mockResolvedValue({ themes: [], sources: [] });
    renderWithQuery(<ThemeCatalogPage />);

    expect(await screen.findByText("No theme sources enabled")).toBeInTheDocument();
    const button = screen.getByRole("button", { name: "Go to Theme sources" });
    button.click();
    expect(navigate).toHaveBeenCalledWith({ to: "/settings/theme-sources" });
  });

  it("hides the sources button in the empty state from users without themes:manage", async () => {
    currentUser = makeUser({ role: "viewer" });
    catalog.mockResolvedValue({ themes: [], sources: [] });
    renderWithQuery(<ThemeCatalogPage />);

    await screen.findByText("No theme sources enabled");
    expect(screen.queryByRole("button", { name: "Go to Theme sources" })).toBeNull();
  });

  it("filters by search text and clears the filters from the no-results state", async () => {
    const user = userEvent.setup();
    renderWithQuery(<ThemeCatalogPage />);
    await screen.findByText("Aurora Night");

    await user.type(screen.getByRole("searchbox"), "zzz");
    expect(await screen.findByText("No themes match your filters")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() => expect(screen.getAllByTestId("theme-card")).toHaveLength(4));
  });

  it("filters by tag from the tag chips", async () => {
    const user = userEvent.setup();
    renderWithQuery(<ThemeCatalogPage />);
    await screen.findByText("Aurora Night");

    await user.click(screen.getByRole("button", { name: "light" }));
    await waitFor(() => expect(screen.getAllByTestId("theme-card")).toHaveLength(2));
    expect(screen.queryByText("Aurora Night")).toBeNull();
  });

  it("installs a valid theme from its card", async () => {
    const user = userEvent.setup();
    renderWithQuery(<ThemeCatalogPage />);
    await screen.findByText("Ember");

    await user.click(within(cardFor("Ember")).getByRole("button", { name: "Install" }));

    await waitFor(() => expect(install).toHaveBeenCalledWith({ catalogId: 3 }));
  });

  it("updates an installed theme by mapping it to its installed id", async () => {
    const user = userEvent.setup();
    renderWithQuery(<ThemeCatalogPage />);
    await screen.findByText("Aurora Night");

    await user.click(within(cardFor("Aurora Night")).getByRole("button", { name: "Update" }));

    await waitFor(() => expect(updateInstalled).toHaveBeenCalledWith(10));
  });

  it("previews a valid theme with its tokens and never for an invalid one", async () => {
    const user = userEvent.setup();
    catalogEntry.mockResolvedValue({
      ...EMBER,
      homepage: "",
      changelog: "",
      problems: [],
      tokens: { light: {}, dark: {} },
      css: null,
    });
    renderWithQuery(<ThemeCatalogPage />);
    await screen.findByText("Ember");

    await user.click(within(cardFor("Ember")).getByRole("button", { name: "Preview" }));

    await waitFor(() => expect(startPreview).toHaveBeenCalledTimes(1));
    expect(startPreview).toHaveBeenCalledWith(
      expect.objectContaining({ catalogId: 3, displayName: "Ember", version: "1.8.0", css: null }),
    );
  });

  it("shows the error card when the catalog cannot be loaded", async () => {
    catalog.mockRejectedValue(new Error("boom"));
    renderWithQuery(<ThemeCatalogPage />);

    expect(await screen.findByText("boom")).toBeInTheDocument();
  });
});
