import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  CUSTOM_CSS_ELEMENT_ID,
  CUSTOM_THEME_VARS_ELEMENT_ID,
  DEFAULT_THEME_PREFERENCES,
  INSTALLED_THEME_CSS_ELEMENT_ID,
  SAFE_MODE_SESSION_KEY,
  THEME_PREFS_STORAGE_KEY,
  resetUrlSafeModeForTests,
  writeThemePreferences,
} from "@/lib/useThemePreferences";
import {
  exitThemePreview,
  getThemePreview,
  startThemePreview,
  type ThemePreviewTheme,
} from "@/lib/useThemePreview";
import type { ThemeTokens, UserThemePreferences } from "@/types";

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

const PREVIEW: ThemePreviewTheme = {
  catalogId: 12,
  installedId: null,
  displayName: "Aurora Night",
  version: "1.2.0",
  tokens: { dark: TOKENS },
  css: ".aurora-probe { color: red; }",
};

const SAVED: UserThemePreferences = {
  ...DEFAULT_THEME_PREFERENCES,
  appearanceMode: "dark",
  customCssEnabled: false,
  customCss: null,
};

function resetDom(): void {
  for (const id of [CUSTOM_THEME_VARS_ELEMENT_ID, INSTALLED_THEME_CSS_ELEMENT_ID, CUSTOM_CSS_ELEMENT_ID]) {
    document.getElementById(id)?.remove();
  }
  const root = document.documentElement;
  for (const attr of ["data-theme", "data-theme-type", "data-theme-preset", "data-custom-css"]) {
    root.removeAttribute(attr);
  }
  root.classList.remove("dark", "light");
}

beforeEach(() => {
  window.localStorage.clear();
  window.sessionStorage.clear();
  resetUrlSafeModeForTests();
  resetDom();
});

afterEach(() => {
  exitThemePreview();
  resetDom();
  vi.resetModules();
});

describe("startThemePreview", () => {
  it("applies the catalog theme in memory through the installed injection path", () => {
    writeThemePreferences(SAVED);

    expect(startThemePreview(PREVIEW)).toBe(true);

    expect(getThemePreview()?.displayName).toBe("Aurora Night");
    const root = document.documentElement;
    expect(root.dataset.themeType).toBe("installed");
    expect(root.dataset.theme).toBe("dark");
    const vars = document.getElementById(CUSTOM_THEME_VARS_ELEMENT_ID);
    expect(vars?.textContent).toContain('html[data-theme-type="installed"][data-theme]');
    expect(vars?.textContent).toContain("--accent: #ff00aa;");
    expect(document.getElementById(INSTALLED_THEME_CSS_ELEMENT_ID)?.textContent).toBe(
      ".aurora-probe { color: red; }",
    );
  });

  it("writes nothing to localStorage", () => {
    writeThemePreferences(SAVED);
    const before = window.localStorage.getItem(THEME_PREFS_STORAGE_KEY);

    startThemePreview(PREVIEW);

    expect(window.localStorage.getItem(THEME_PREFS_STORAGE_KEY)).toBe(before);
  });

  it("is blocked in safe mode and leaves the DOM untouched", () => {
    writeThemePreferences(SAVED);
    window.sessionStorage.setItem(SAFE_MODE_SESSION_KEY, "1");

    expect(startThemePreview(PREVIEW)).toBe(false);

    expect(getThemePreview()).toBeNull();
    expect(document.getElementById(INSTALLED_THEME_CSS_ELEMENT_ID)).toBeNull();
    expect(document.documentElement.dataset.themeType).toBeUndefined();
  });
});

describe("exitThemePreview", () => {
  it("restores the saved preferences exactly", () => {
    writeThemePreferences(SAVED);
    startThemePreview(PREVIEW);

    exitThemePreview();

    expect(getThemePreview()).toBeNull();
    const root = document.documentElement;
    expect(root.dataset.themeType).toBe("preset");
    expect(root.dataset.themePreset).toBe("pink");
    expect(document.getElementById(CUSTOM_THEME_VARS_ELEMENT_ID)).toBeNull();
    expect(document.getElementById(INSTALLED_THEME_CSS_ELEMENT_ID)).toBeNull();
    expect(window.localStorage.getItem(THEME_PREFS_STORAGE_KEY)).toBe(JSON.stringify(SAVED));
  });

  it("is a no-op when no preview is active", () => {
    writeThemePreferences(SAVED);
    document.documentElement.dataset.themeType = "custom_colors";

    exitThemePreview();

    expect(document.documentElement.dataset.themeType).toBe("custom_colors");
  });
});

describe("reload", () => {
  it("restores the saved theme after a reload, since nothing was persisted", async () => {
    writeThemePreferences(SAVED);
    startThemePreview(PREVIEW);

    // A reload gives a fresh module instance; only localStorage survives.
    vi.resetModules();
    const prefs = await import("@/lib/useThemePreferences");
    const preview = await import("@/lib/useThemePreview");

    expect(preview.getThemePreview()).toBeNull();
    prefs.applyThemePreferences(prefs.readThemePreferences());
    expect(document.documentElement.dataset.themeType).toBe("preset");
    expect(document.getElementById(INSTALLED_THEME_CSS_ELEMENT_ID)).toBeNull();
  });
});
