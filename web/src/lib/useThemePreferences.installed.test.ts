import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  CUSTOM_CSS_ELEMENT_ID,
  CUSTOM_THEME_VARS_ELEMENT_ID,
  INSTALLED_THEME_CSS_ELEMENT_ID,
  SAFE_MODE_SESSION_KEY,
  applyThemePreferences,
  installedThemeModes,
  installedThemeTokens,
  normalizeThemePreferences,
  resetUrlSafeModeForTests,
  themePreferencesEqual,
} from "@/lib/useThemePreferences";
import { enforceUnauthenticatedTheme } from "@/lib/enforceUnauthenticatedTheme";
import type { InstalledThemeRender, ThemeTokens, UserThemePreferences } from "@/types";

function tokensWithAccent(accent: string): ThemeTokens {
  return {
    background: "#101820",
    foreground: "#f5f5f5",
    surface: "#1b2633",
    "surface-secondary": "#223044",
    "surface-tertiary": "#2a3a52",
    overlay: "#000000",
    accent,
    "accent-foreground": "#ffffff",
    "accent-soft": "#3a1030",
    "accent-soft-foreground": "#ffb3e6",
    muted: "#8899aa",
    border: "#334155",
    focus: "#00aaff",
    link: "#7dd3fc",
  };
}

const LIGHT = tokensWithAccent("#0066ff");
const DARK = tokensWithAccent("#ff00aa");

const BOTH_MODES: InstalledThemeRender = {
  tokens: { light: LIGHT, dark: DARK },
  css: ".installed-probe { color: red; }",
  version: "1.0.0",
};

const DARK_ONLY: InstalledThemeRender = {
  tokens: { dark: DARK },
  css: null,
  version: "1.0.0",
};

function prefs(overrides: Partial<UserThemePreferences>): UserThemePreferences {
  return {
    themeType: "installed",
    presetId: "pink",
    appearanceMode: "dark",
    customColors: null,
    customCssEnabled: false,
    customCss: null,
    installedThemeId: 7,
    installedTheme: BOTH_MODES,
    ...overrides,
  };
}

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
  resetDom();
});

describe("normalizeThemePreferences (installed)", () => {
  it("keeps the installed id and valid render data", () => {
    const out = normalizeThemePreferences({
      themeType: "installed",
      presetId: "pink",
      appearanceMode: "dark",
      installedThemeId: 7,
      installedTheme: { tokens: { dark: DARK }, css: ".x{}", version: "1.0.0" },
    });

    expect(out?.themeType).toBe("installed");
    expect(out?.installedThemeId).toBe(7);
    expect(out?.installedTheme).toEqual({ tokens: { dark: DARK }, css: ".x{}", version: "1.0.0" });
  });

  it("falls back to the preset when the installed id is missing", () => {
    const out = normalizeThemePreferences({ themeType: "installed", presetId: "pink", appearanceMode: "dark" });

    expect(out?.themeType).toBe("preset");
    expect(out).not.toHaveProperty("installedThemeId");
  });

  it("drops render data with an invalid token value so no raw value reaches the DOM", () => {
    const out = normalizeThemePreferences({
      themeType: "installed",
      presetId: "pink",
      appearanceMode: "dark",
      installedThemeId: 7,
      installedTheme: { tokens: { dark: { ...DARK, accent: "red; } body { display:none" } }, css: null, version: "1.0.0" },
    });

    expect(out?.themeType).toBe("installed");
    expect(out?.installedTheme).toBeNull();
  });

  it("omits installed fields on non-installed payloads", () => {
    const out = normalizeThemePreferences({ themeType: "preset", presetId: "pink", appearanceMode: "dark" });

    expect(out).not.toHaveProperty("installedThemeId");
    expect(out).not.toHaveProperty("installedTheme");
  });
});

describe("themePreferencesEqual (installed)", () => {
  it("detects a different installed theme", () => {
    expect(themePreferencesEqual(prefs({}), prefs({ installedThemeId: 8 }))).toBe(false);
  });

  it("detects changed render data for the same id", () => {
    expect(themePreferencesEqual(prefs({}), prefs({ installedTheme: DARK_ONLY }))).toBe(false);
  });
});

describe("installed render order and modes", () => {
  it("mounts tokens, then the theme css, then the user overlay last", () => {
    applyThemePreferences(
      prefs({ customCssEnabled: true, customCss: "body { margin: 0; }" }),
    );

    const ids = Array.from(document.head.querySelectorAll("style")).map((el) => el.id);
    const vars = ids.indexOf(CUSTOM_THEME_VARS_ELEMENT_ID);
    const installed = ids.indexOf(INSTALLED_THEME_CSS_ELEMENT_ID);
    const overlay = ids.indexOf(CUSTOM_CSS_ELEMENT_ID);
    expect(vars).toBeGreaterThanOrEqual(0);
    expect(installed).toBeGreaterThan(vars);
    expect(overlay).toBeGreaterThan(installed);
    expect(ids[ids.length - 1]).toBe(CUSTOM_CSS_ELEMENT_ID);
  });

  it("uses the tokens of the active mode when both modes exist", () => {
    applyThemePreferences(prefs({ appearanceMode: "light" }));

    const vars = document.getElementById(CUSTOM_THEME_VARS_ELEMENT_ID)?.textContent ?? "";
    expect(vars).toContain('html[data-theme-type="installed"][data-theme]');
    expect(vars).toContain(`--accent: ${LIGHT.accent};`);
    expect(document.documentElement.dataset.theme).toBe("light");
  });

  it("pins a single-mode theme to its only mode and uses its tokens", () => {
    applyThemePreferences(prefs({ appearanceMode: "light", installedTheme: DARK_ONLY }));

    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    const vars = document.getElementById(CUSTOM_THEME_VARS_ELEMENT_ID)?.textContent ?? "";
    expect(vars).toContain(`--accent: ${DARK.accent};`);
  });

  it("reports the modes a render carries and the single-mode fallback", () => {
    expect(installedThemeModes(BOTH_MODES)).toEqual(["light", "dark"]);
    expect(installedThemeModes(DARK_ONLY)).toEqual(["dark"]);
    expect(installedThemeTokens(DARK_ONLY, "light")).toEqual(DARK);
    expect(installedThemeTokens(BOTH_MODES, "light")).toEqual(LIGHT);
  });

  it("removes the theme css element when the render carries none", () => {
    applyThemePreferences(prefs({ installedTheme: DARK_ONLY }));

    expect(document.getElementById(INSTALLED_THEME_CSS_ELEMENT_ID)).toBeNull();
  });
});

describe("installed safe mode", () => {
  it("renders Modern Pink and mounts neither installed element", () => {
    window.sessionStorage.setItem(SAFE_MODE_SESSION_KEY, "1");

    applyThemePreferences(prefs({ customCssEnabled: true, customCss: "body { margin: 0; }" }));

    const root = document.documentElement;
    expect(root.dataset.themeType).toBe("preset");
    expect(root.dataset.themePreset).toBe("pink");
    expect(document.getElementById(CUSTOM_THEME_VARS_ELEMENT_ID)).toBeNull();
    expect(document.getElementById(INSTALLED_THEME_CSS_ELEMENT_ID)).toBeNull();
    expect(document.getElementById(CUSTOM_CSS_ELEMENT_ID)).toBeNull();
  });
});

describe("unauthenticated surfaces", () => {
  it("removes the installed theme css element", () => {
    applyThemePreferences(prefs({}));
    expect(document.getElementById(INSTALLED_THEME_CSS_ELEMENT_ID)).not.toBeNull();

    const dispose = enforceUnauthenticatedTheme();
    try {
      expect(document.getElementById(INSTALLED_THEME_CSS_ELEMENT_ID)).toBeNull();
      expect(document.getElementById(CUSTOM_THEME_VARS_ELEMENT_ID)).toBeNull();
      expect(document.documentElement.dataset.themeType).toBe("preset");
    } finally {
      dispose();
    }
  });
});
