// Theme preview banner (spec 043, T026/T029): shows while a theme is
// previewed; Keep installs or selects it (updating an older copy first), Exit
// ends the preview, and a failed Keep shows the server message.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { act } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { resetUrlSafeModeForTests } from "@/lib/useThemePreferences";
import {
  exitThemePreview,
  getThemePreview,
  startThemePreview,
  type ThemePreviewTheme,
} from "@/lib/useThemePreview";
import type { ThemeTokens } from "@/types";
import { ThemePreviewBanner } from "./ThemePreviewBanner";

const TOKENS: ThemeTokens = {
  background: "#101820",
  foreground: "#f5f5f5",
  surface: "#1b2633",
  "surface-secondary": "#223044",
  "surface-tertiary": "#2a3a52",
  overlay: "#000000",
  accent: "#ff00aa",
  "accent-foreground": "#ffffff",
  "accent-soft": "#3a1030",
  "accent-soft-foreground": "#ffb3e6",
  muted: "#8899aa",
  border: "#334155",
  focus: "#00aaff",
  link: "#7dd3fc",
};

const NOT_INSTALLED: ThemePreviewTheme = {
  catalogId: 12,
  installedId: null,
  displayName: "Aurora Night",
  version: "1.2.0",
  tokens: { dark: TOKENS },
  css: null,
};

const OLDER_COPY: ThemePreviewTheme = {
  ...NOT_INSTALLED,
  installedId: 7,
  updateOnKeep: true,
};

function preview(theme: ThemePreviewTheme): void {
  act(() => {
    expect(startThemePreview(theme)).toBe(true);
  });
}

beforeEach(() => {
  window.localStorage.clear();
  window.sessionStorage.clear();
  resetUrlSafeModeForTests();
});

afterEach(() => {
  act(() => exitThemePreview());
  server.resetHandlers();
});

describe("ThemePreviewBanner", () => {
  it("renders nothing while no theme is previewed", () => {
    renderWithQuery(<ThemePreviewBanner />);
    expect(screen.queryByText(/Previewing/)).toBeNull();
    expect(screen.queryByRole("button", { name: "Keep" })).toBeNull();
  });

  it("Keep installs and selects a theme that is not installed, then ends the preview", async () => {
    let body: unknown = null;
    server.use(
      http.post("/users/me/themes", async ({ request }) => {
        body = await request.json();
        return HttpResponse.json({ id: 9 }, { status: 201 });
      }),
    );
    renderWithQuery(<ThemePreviewBanner />);
    preview(NOT_INSTALLED);

    expect(screen.getByText("Previewing Aurora Night")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Keep" }));

    await waitFor(() => expect(getThemePreview()).toBeNull());
    expect(body).toEqual({ catalogId: 12, select: true });
    expect(screen.queryByText("Previewing Aurora Night")).toBeNull();
  });

  it("Keep updates an older installed copy before selecting it", async () => {
    const calls: string[] = [];
    let prefs: Record<string, unknown> | null = null;
    server.use(
      http.post("/users/me/themes/7/update", () => {
        calls.push("update");
        return HttpResponse.json({ id: 7 });
      }),
      http.put("/users/me/preferences", async ({ request }) => {
        calls.push("preferences");
        prefs = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(prefs);
      }),
    );
    renderWithQuery(<ThemePreviewBanner />);
    preview(OLDER_COPY);

    await userEvent.click(screen.getByRole("button", { name: "Keep" }));

    await waitFor(() => expect(getThemePreview()).toBeNull());
    expect(calls).toEqual(["update", "preferences"]);
    expect(prefs).toMatchObject({ themeType: "installed", installedThemeId: 7 });
  });

  it("shows the server message when Keep fails and keeps the preview on", async () => {
    server.use(
      http.post("/users/me/themes", () => new HttpResponse("source disabled", { status: 409 })),
    );
    renderWithQuery(<ThemePreviewBanner />);
    preview(NOT_INSTALLED);

    await userEvent.click(screen.getByRole("button", { name: "Keep" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("source disabled");
    expect(getThemePreview()).toEqual(NOT_INSTALLED);
  });

  it("Exit preview ends the preview without calling the API", async () => {
    renderWithQuery(<ThemePreviewBanner />);
    preview(NOT_INSTALLED);

    await userEvent.click(screen.getByRole("button", { name: "Exit preview" }));

    expect(getThemePreview()).toBeNull();
    expect(screen.queryByText("Previewing Aurora Night")).toBeNull();
  });
});
