// Update and Remove on the Theme settings page (spec 043, T045 / T044).
import { afterEach, beforeEach, describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { DEFAULT_THEME_PREFERENCES, THEME_PREFS_STORAGE_KEY } from "@/lib/useThemePreferences";
import type { InstalledTheme } from "@/types";

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { ThemeSettingsPage } from "./ThemeSettings";

const aurora: InstalledTheme = {
  id: 7, source: "official", name: "aurora-night", displayName: "Aurora Night", version: "1.2.0",
  modes: ["dark"], updateAvailable: true, sourceRemoved: false, selected: false,
};

beforeEach(() => {
  window.localStorage.clear();
  window.localStorage.setItem(THEME_PREFS_STORAGE_KEY, JSON.stringify({ ...DEFAULT_THEME_PREFERENCES }));
});

afterEach(() => {
  server.resetHandlers();
});

describe("ThemeSettingsPage installed theme actions", () => {
  it("Update posts to the update endpoint", async () => {
    let updated = false;
    server.use(
      http.get("/users/me/themes", () => HttpResponse.json([aurora])),
      http.post("/users/me/themes/7/update", () => {
        updated = true;
        return HttpResponse.json({ ...aurora, version: "1.3.0", updateAvailable: false, tokens: {}, css: null });
      }),
    );
    renderWithQuery(<ThemeSettingsPage />);

    const card = await screen.findByTestId("installed-themes-card");
    await userEvent.click(within(card).getByRole("button", { name: "Update" }));

    await waitFor(() => expect(updated).toBe(true));
  });

  it("Remove asks for confirmation, deletes, and shows the fallback notice when the selected theme went", async () => {
    let deleted = false;
    server.use(
      http.get("/users/me/themes", () => HttpResponse.json([{ ...aurora, selected: true }])),
      http.delete("/users/me/themes/7", () => {
        deleted = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<ThemeSettingsPage />);

    const card = await screen.findByTestId("installed-themes-card");
    await userEvent.click(within(card).getByRole("button", { name: "Remove" }));
    expect(await screen.findByText("Remove theme")).toBeInTheDocument();
    expect(deleted).toBe(false);

    await userEvent.click(screen.getByRole("button", { name: "Remove theme" }));

    await waitFor(() => expect(deleted).toBe(true));
    expect(await screen.findByRole("status")).toHaveTextContent("Aurora Night was removed, you are back on Modern Pink");
  });

  it("Remove does not delete when the confirmation is cancelled", async () => {
    let deleted = false;
    server.use(
      http.get("/users/me/themes", () => HttpResponse.json([aurora])),
      http.delete("/users/me/themes/7", () => {
        deleted = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    renderWithQuery(<ThemeSettingsPage />);

    const card = await screen.findByTestId("installed-themes-card");
    await userEvent.click(within(card).getByRole("button", { name: "Remove" }));
    await userEvent.click(await screen.findByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(screen.queryByRole("button", { name: "Remove theme" })).toBeNull());
    expect(deleted).toBe(false);
  });
});
