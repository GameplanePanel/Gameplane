import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { screen } from "@testing-library/react";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { THEME_PREFS_STORAGE_KEY, DEFAULT_THEME_PREFERENCES } from "@/lib/useThemePreferences";
import type { UserThemePreferences } from "@/types";

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { ThemeSettingsPage } from "./ThemeSettings";

function seedPrefs(prefs: Partial<UserThemePreferences> = {}) {
  window.localStorage.setItem(
    THEME_PREFS_STORAGE_KEY,
    JSON.stringify({ ...DEFAULT_THEME_PREFERENCES, ...prefs }),
  );
}

beforeEach(() => {
  window.localStorage.clear();
  document.getElementById("gameplane-custom-theme-vars")?.remove();
  document.getElementById("gameplane-custom-css")?.remove();
  const html = document.documentElement;
  html.removeAttribute("data-theme-preset");
  html.removeAttribute("data-theme-type");
  html.removeAttribute("data-custom-css");
});

afterEach(() => {
  server.resetHandlers();
});

describe("ThemeSettingsPage language card", () => {
  it("places the personal Language card before the Preset theme card", async () => {
    seedPrefs();
    renderWithQuery(<ThemeSettingsPage />);

    const languageTitle = (await screen.findAllByText("Language"))[0];
    const presetTitle = screen.getAllByText("Preset theme")[0];
    expect(languageTitle.compareDocumentPosition(presetTitle) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByRole("radio", { name: /Automatic/ })).toBeInTheDocument();
  });
});
