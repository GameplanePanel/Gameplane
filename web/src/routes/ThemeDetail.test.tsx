import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";
import type { InstalledTheme, ThemeCatalogDetail, ThemeTokens, User } from "@/types";

import { ThemeDetailPage } from "./ThemeDetail";

const catalogEntry = vi.fn<(id: number) => Promise<ThemeCatalogDetail>>();
const installed = vi.fn<() => Promise<InstalledTheme[]>>();
const install = vi.fn();
const updateInstalled = vi.fn();
const removeInstalled = vi.fn();
const startPreview = vi.fn(() => true);
const navigate = vi.fn();

vi.mock("@/lib/endpoints", () => ({
  Themes: {
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
  useParams: () => ({ id: "1" }),
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

const AURORA: ThemeCatalogDetail = {
  id: 1,
  source: "official",
  name: "aurora-night",
  displayName: "Aurora Night",
  author: "Ada Reyes",
  description: "Deep blue surfaces with a cyan accent.",
  tags: ["dark", "blue"],
  license: "MIT",
  version: "1.3.0",
  modes: ["dark"],
  status: "valid",
  lowContrast: false,
  installed: { version: "1.2.0" },
  updateAvailable: true,
  changelog: "v1.3.0 Improved contrast.\nv1.2.0 Cyan accent refresh.",
  homepage: "https://github.com/ada-reyes/aurora-night",
  problems: [],
  tokens: { dark: {} as ThemeTokens },
  css: undefined,
};

const BROKEN: ThemeCatalogDetail = {
  ...AURORA,
  id: 4,
  name: "broken-pastel",
  displayName: "Broken Pastel",
  author: "Ines Duarte",
  version: "0.3.0",
  modes: ["light", "dark"],
  status: "invalid",
  installed: undefined,
  updateAvailable: false,
  changelog: "",
  homepage: "javascript:alert(1)",
  problems: [
    { rule: "theme.yaml", detail: "missing name", line: 3 },
    { rule: "contrast", detail: "text on surface is below 4.5:1" },
  ],
  tokens: undefined,
};

const INSTALLED_AURORA: InstalledTheme = {
  id: 10,
  source: "official",
  name: "aurora-night",
  displayName: "Aurora Night",
  version: "1.2.0",
  modes: ["dark"],
  updateAvailable: true,
  sourceRemoved: false,
  selected: false,
};

beforeEach(() => {
  currentUser = makeUser();
  catalogEntry.mockImplementation(async (id: number) => (id === 4 ? BROKEN : AURORA));
  installed.mockResolvedValue([INSTALLED_AURORA]);
  install.mockResolvedValue({});
  updateInstalled.mockResolvedValue({});
  removeInstalled.mockResolvedValue(undefined);
});

afterEach(() => {
  vi.clearAllMocks();
});

describe("ThemeDetailPage", () => {
  it("shows the metadata, the installed and available versions, and the update action", async () => {
    renderWithQuery(<ThemeDetailPage />);

    expect(await screen.findByRole("heading", { name: "Aurora Night" })).toBeInTheDocument();
    expect(screen.getByText("1.3.0 (installed 1.2.0)")).toBeInTheDocument();
    expect(screen.getByText("Update from 1.2.0 to 1.3.0")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Update" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove" })).toBeInTheDocument();
    expect(screen.getByText("v1.3.0 Improved contrast.", { exact: false })).toBeInTheDocument();
  });

  it("renders a valid homepage as an external link without the scheme", async () => {
    renderWithQuery(<ThemeDetailPage />);
    await screen.findByRole("heading", { name: "Aurora Night" });

    const link = screen.getByRole("link", { name: /github\.com\/ada-reyes\/aurora-night/ });
    expect(link).toHaveAttribute("href", "https://github.com/ada-reyes/aurora-night");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
  });

  it("lists every problem of an invalid theme and disables Install and Preview", async () => {
    catalogEntry.mockResolvedValue(BROKEN);
    installed.mockResolvedValue([]);
    renderWithQuery(<ThemeDetailPage />);

    expect(await screen.findByRole("heading", { name: "Broken Pastel" })).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("2 problems found");
    expect(screen.getByText("theme.yaml:3")).toBeInTheDocument();
    expect(screen.getByText("contrast")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Preview" })).toBeDisabled();
    expect(screen.queryByRole("link", { name: /javascript/ })).toBeNull();
  });

  it("installs a theme that is not installed yet", async () => {
    const user = userEvent.setup();
    catalogEntry.mockResolvedValue({ ...AURORA, updateAvailable: false, installed: undefined });
    installed.mockResolvedValue([]);
    renderWithQuery(<ThemeDetailPage />);
    await screen.findByRole("heading", { name: "Aurora Night" });

    await user.click(screen.getByRole("button", { name: "Install" }));

    await waitFor(() => expect(install).toHaveBeenCalledWith({ catalogId: 1 }));
  });

  it("updates the installed copy", async () => {
    const user = userEvent.setup();
    renderWithQuery(<ThemeDetailPage />);
    await screen.findByRole("heading", { name: "Aurora Night" });

    await user.click(screen.getByRole("button", { name: "Update" }));

    await waitFor(() => expect(updateInstalled).toHaveBeenCalledWith(10));
  });

  it("removes the installed copy after confirmation", async () => {
    const user = userEvent.setup();
    renderWithQuery(<ThemeDetailPage />);
    await screen.findByRole("heading", { name: "Aurora Night" });

    await user.click(screen.getByRole("button", { name: "Remove" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Remove theme" }));

    await waitFor(() => expect(removeInstalled).toHaveBeenCalledWith(10));
    expect(navigate).toHaveBeenCalledWith({ to: "/settings/theme/catalog" });
  });

  it("starts an in-memory preview from the loaded tokens", async () => {
    const user = userEvent.setup();
    renderWithQuery(<ThemeDetailPage />);
    await screen.findByRole("heading", { name: "Aurora Night" });

    await user.click(screen.getByRole("button", { name: "Preview" }));

    expect(startPreview).toHaveBeenCalledWith(
      expect.objectContaining({ catalogId: 1, installedId: 10, displayName: "Aurora Night", version: "1.3.0" }),
    );
  });

  it("shows the error card when the theme cannot be loaded", async () => {
    catalogEntry.mockRejectedValue(new Error("theme not found"));
    renderWithQuery(<ThemeDetailPage />);

    expect(await screen.findByText("theme not found")).toBeInTheDocument();
  });
});
