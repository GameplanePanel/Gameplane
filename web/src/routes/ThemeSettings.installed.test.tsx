// Installed themes on the Theme settings page (spec 043, T040/T038): the
// installed themes appear as preset options and save with their id.
import { afterEach, beforeEach, describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { DEFAULT_THEME_PREFERENCES, THEME_PREFS_STORAGE_KEY } from "@/lib/useThemePreferences";
import type { InstalledTheme, InstalledThemeDetail } from "@/types";

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { ThemeSettingsPage } from "./ThemeSettings";

const TOKENS = {
  background: "#101010", foreground: "#f5f5f5", surface: "#1a1a1a", "surface-secondary": "#222222",
  "surface-tertiary": "#2a2a2a", overlay: "#000000", accent: "#00aaff", "accent-foreground": "#000000",
  "accent-soft": "#0a2a3a", "accent-soft-foreground": "#9fdcff", muted: "#888888", border: "#333333",
  focus: "#00aaff", link: "#66ccff",
};

const aurora: InstalledTheme = {
  id: 7, source: "official", name: "aurora-night", displayName: "Aurora Night", version: "1.2.0",
  modes: ["dark"], updateAvailable: true, sourceRemoved: false, selected: false,
};
const paper: InstalledTheme = {
  id: 8, source: "official", name: "paper-light", displayName: "Paper Light", version: "1.0.3",
  modes: ["light", "dark"], updateAvailable: false, sourceRemoved: false, selected: false,
};

beforeEach(() => {
  window.localStorage.clear();
  document.getElementById("gameplane-custom-theme-vars")?.remove();
  document.getElementById("gameplane-installed-theme-css")?.remove();
  window.localStorage.setItem(THEME_PREFS_STORAGE_KEY, JSON.stringify({ ...DEFAULT_THEME_PREFERENCES }));
});

afterEach(() => {
  server.resetHandlers();
});

describe("ThemeSettingsPage installed themes", () => {
  it("lists installed themes as preset options and summarizes them", async () => {
    server.use(http.get("/users/me/themes", () => HttpResponse.json([aurora, paper])));
    renderWithQuery(<ThemeSettingsPage />);

    expect(await screen.findByRole("radio", { name: /Aurora Night/ })).toBeInTheDocument();
    expect(screen.getByRole("radio", { name: /Paper Light/ })).toBeInTheDocument();
    expect(screen.getByTestId("installed-themes-summary")).toHaveTextContent("2 themes installed · 1 update available");
  });

  it("shows no installed radios when the installed list fails to load", async () => {
    server.use(http.get("/users/me/themes", () => new HttpResponse("boom", { status: 500 })));
    renderWithQuery(<ThemeSettingsPage />);

    expect(await screen.findByRole("radio", { name: /Modern Pink/ })).toBeInTheDocument();
    expect(screen.queryByRole("radio", { name: /Aurora Night/ })).toBeNull();
    expect(screen.queryByTestId("installed-themes-card")).toBeNull();
  });

  it("saves the picked installed theme with its id", async () => {
    let body: unknown = null;
    const detail: InstalledThemeDetail = { ...aurora, tokens: { dark: TOKENS }, css: null };
    server.use(
      http.get("/users/me/themes", () => HttpResponse.json([aurora, paper])),
      http.get("/users/me/themes/7", () => HttpResponse.json(detail)),
      http.put("/users/me/preferences", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ ...DEFAULT_THEME_PREFERENCES, themeType: "installed", installedThemeId: 7 });
      }),
    );
    renderWithQuery(<ThemeSettingsPage />);

    await userEvent.click(await screen.findByRole("radio", { name: /Aurora Night/ }));
    await waitFor(() => expect(screen.getByRole("radio", { name: /Aurora Night/ })).toBeChecked());
    await userEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(body).toMatchObject({ themeType: "installed", installedThemeId: 7 }));
    expect(within(screen.getByTestId("installed-themes-card")).getByText("Aurora Night")).toBeInTheDocument();
  });
});
