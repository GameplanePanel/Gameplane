// Theme preferences plumbing (specs/done_016-user-theme-customization, research.md
// R-05): local cache, DOM application, profile reconciliation, and the
// optimistic PUT mutation with offline retry. The inline boot script in
// index.html mirrors the cache format and DOM attributes — keep the storage
// keys, element ids, and attribute values in sync with it.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { defineMessages } from "react-intl";
import { APIError } from "@/lib/api";
import { useOptionalIntl } from "@/lib/i18n/format";
import { Users, type UserPreferencesUpdate } from "@/lib/endpoints";
import { customThemeTokensToCss, deriveCustomThemeTokens, surfaceAppearance } from "@/lib/theme-derivation";
import type {
  AppearanceMode,
  CustomColorConfig,
  ThemePresetId,
  ThemeType,
  User,
  UserThemePreferences,
} from "@/types";

export const THEME_PREFS_STORAGE_KEY = "gameplane-theme-prefs";
export const SAFE_MODE_SESSION_KEY = "gameplane-safe-mode";
export const CUSTOM_CSS_ELEMENT_ID = "gameplane-custom-css";
export const CUSTOM_THEME_VARS_ELEMENT_ID = "gameplane-custom-theme-vars";
// Pre-serialized custom-color tokens for the index.html boot script, which
// cannot run the derivation itself; kept in step with the prefs cache.
export const THEME_VARS_CSS_STORAGE_KEY = "gameplane-theme-vars-css";
// D4: the resolved light/dark mode for the active custom-colors surface,
// cached so the index.html boot script never has to duplicate the
// relative-luminance math in ES5 — one source of truth (surfaceAppearance).
export const CUSTOM_MODE_STORAGE_KEY = "gameplane-theme-custom-mode";
// Dispatched on window when a preferences update fails, so a toast layer can
// listen without this plumbing owning any UI (the toast ships in a later
// task of this feature).
export const THEME_ERROR_EVENT = "gameplane-theme-error";

// Pink preset + system appearance, per data-model.md §2.1 defaults.
export const DEFAULT_THEME_PREFERENCES: UserThemePreferences = {
  themeType: "preset",
  presetId: "pink",
  appearanceMode: "system",
  customColors: null,
  customCssEnabled: false,
  customCss: null,
};

const HEX_COLOR_RE = /^#([0-9a-fA-F]{6})$/;

// F-125: set once `?safe-mode=1` has been seen during this page load, so URL
// safe mode survives TanStack client-side navigation (which drops the query
// string, e.g. the banner's "Open Appearance Settings" link). Deliberately
// in-memory only: a full reload re-evaluates this module and clears it, so
// removing the parameter and reloading restores normal behavior
// (contracts/theme-ui.md §4). The sessionStorage flag below is a separate,
// pre-existing entry point (keyboard shortcut, login-page safe-mode link).
let urlSafeModeSeen = false;

const THEME_MESSAGES = defineMessages({
  update_failed: {
    id: "web.lib.use_theme_preferences.update_failed",
    defaultMessage: "Theme update failed",
    description: "Error shown when saving theme preferences fails for an unknown reason",
  },
});

/** Test-only: clears the in-memory URL safe-mode latch, simulating a fresh page load. */
export function resetUrlSafeModeForTests(): void {
  urlSafeModeSeen = false;
}

/**
 * Safe mode (FR-009) suspends only the custom CSS overlay; the base theme
 * still applies. Entry points: the `?safe-mode=1` URL parameter (kept for
 * the rest of the page load, across in-app navigation), or the
 * `gameplane-safe-mode` sessionStorage flag set by the keyboard shortcut
 * and the login-page safe-mode link.
 */
export function isSafeModeActive(): boolean {
  if (typeof window === "undefined") return false;
  try {
    if (new URLSearchParams(window.location.search).get("safe-mode") === "1") {
      urlSafeModeSeen = true;
    }
  } catch {
    // Malformed query string — treat the URL entry point as inactive.
  }
  if (urlSafeModeSeen) return true;
  try {
    const flag = window.sessionStorage.getItem(SAFE_MODE_SESSION_KEY);
    return flag === "1" || flag === "true";
  } catch {
    return false;
  }
}

/**
 * Validates and coerces an unknown payload (localStorage cache or API
 * response) into a well-formed UserThemePreferences, defaulting invalid
 * fields. Returns null when the payload is not an object at all — callers
 * then treat it as "no prefs".
 */
export function normalizeThemePreferences(input: unknown): UserThemePreferences | null {
  if (typeof input !== "object" || input === null) return null;
  const raw = input as Record<string, unknown>;

  const themeType: ThemeType = raw.themeType === "custom_colors" ? "custom_colors" : "preset";
  const presetId: ThemePresetId = raw.presetId === "legacy" ? "legacy" : "pink";
  const appearanceMode: AppearanceMode =
    raw.appearanceMode === "light" || raw.appearanceMode === "dark" ? raw.appearanceMode : "system";

  let customColors: CustomColorConfig | null = null;
  if (typeof raw.customColors === "object" && raw.customColors !== null) {
    const colors = raw.customColors as Record<string, unknown>;
    // Invalid hex never reaches the DOM: drop the whole pair back to null.
    if (
      typeof colors.accent === "string" &&
      typeof colors.surface === "string" &&
      HEX_COLOR_RE.test(colors.accent) &&
      HEX_COLOR_RE.test(colors.surface)
    ) {
      customColors = { accent: colors.accent, surface: colors.surface };
    }
  }

  const customCss =
    typeof raw.customCss === "string" && raw.customCss.length > 0 ? raw.customCss : null;

  return {
    themeType,
    presetId,
    appearanceMode,
    customColors,
    customCssEnabled: raw.customCssEnabled === true,
    customCss,
    ...(typeof raw.updatedAt === "string" ? { updatedAt: raw.updatedAt } : {}),
  };
}

/** Content comparison for drift detection; updatedAt is metadata, ignored. */
export function themePreferencesEqual(
  a: UserThemePreferences,
  b: UserThemePreferences,
): boolean {
  return (
    a.themeType === b.themeType &&
    a.presetId === b.presetId &&
    a.appearanceMode === b.appearanceMode &&
    a.customCssEnabled === b.customCssEnabled &&
    a.customCss === b.customCss &&
    a.customColors?.accent === b.customColors?.accent &&
    a.customColors?.surface === b.customColors?.surface
  );
}

/** Reads and normalizes the cached prefs; null when absent or unreadable. */
export function readThemePreferences(): UserThemePreferences | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(THEME_PREFS_STORAGE_KEY);
    if (!raw) return null;
    return normalizeThemePreferences(JSON.parse(raw));
  } catch {
    // Storage blocked or corrupt cache — behave as "no cached prefs".
    return null;
  }
}

export function writeThemePreferences(prefs: UserThemePreferences): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(THEME_PREFS_STORAGE_KEY, JSON.stringify(prefs));
    if (prefs.themeType === "custom_colors" && prefs.customColors) {
      const tokens = deriveCustomThemeTokens(prefs.customColors.accent, prefs.customColors.surface);
      window.localStorage.setItem(THEME_VARS_CSS_STORAGE_KEY, customThemeTokensToCss(tokens));
      window.localStorage.setItem(
        CUSTOM_MODE_STORAGE_KEY,
        surfaceAppearance(prefs.customColors.surface),
      );
    } else {
      window.localStorage.removeItem(THEME_VARS_CSS_STORAGE_KEY);
      window.localStorage.removeItem(CUSTOM_MODE_STORAGE_KEY);
    }
  } catch {
    // Storage blocked — preferences still apply for this session.
  }
}

function resolveAppearanceMode(prefs: UserThemePreferences | null): "dark" | "light" {
  // D4: custom colors override appearanceMode entirely — the whole page's
  // light/dark follows the surface's own brightness, not the stored mode.
  if (prefs?.themeType === "custom_colors" && prefs.customColors) {
    return surfaceAppearance(prefs.customColors.surface);
  }
  const mode = prefs?.appearanceMode ?? "system";
  if (mode !== "system") return mode;
  // Mirrors the boot script: an unavailable matchMedia keeps the markup's
  // dark default rather than flipping to light.
  if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  return "dark";
}

export interface ApplyThemePreferencesOptions {
  /**
   * false suppresses the custom CSS overlay (logout, /login, /share/:token).
   * Only the mounted element is removed — the stored stylesheet is never
   * deleted (disable-vs-reset, contracts/theme-tokens-v2.md §5.6).
   */
  overlay?: boolean;
}

/**
 * Mounts (or removes, when colors is null) the derived custom-color tokens in
 * <style id="gameplane-custom-theme-vars"> (contracts/theme-tokens-v2.md §4).
 * applyThemePreferences re-appends the custom CSS overlay afterwards, so an
 * enabled overlay still wins over these tokens.
 */
export function applyCustomThemeVars(colors: CustomColorConfig | null | undefined): void {
  if (typeof document === "undefined") return;
  const existing = document.getElementById(CUSTOM_THEME_VARS_ELEMENT_ID);
  if (!colors) {
    existing?.remove();
    return;
  }
  const tokens = deriveCustomThemeTokens(colors.accent, colors.surface);
  const el = existing ?? document.createElement("style");
  el.id = CUSTOM_THEME_VARS_ELEMENT_ID;
  el.textContent = customThemeTokensToCss(tokens);
  document.head.appendChild(el);
}

/**
 * Applies preferences to the DOM: the dark/light class + data-theme,
 * data-theme-preset, data-theme-type, data-custom-css, and the
 * #gameplane-custom-css overlay mounted as the LAST child of document.head
 * (after the preset tokens and #gameplane-custom-theme-vars) so user rules
 * win at equal specificity. Safe mode suspends only the overlay.
 */
export function applyThemePreferences(
  prefs: UserThemePreferences | null,
  options: ApplyThemePreferencesOptions = {},
): void {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  const resolved = resolveAppearanceMode(prefs);

  root.classList.remove("dark", "light");
  root.classList.add(resolved);
  root.dataset.theme = resolved;
  root.dataset.themePreset = prefs?.presetId ?? "pink";
  root.dataset.themeType = prefs?.themeType ?? "preset";
  applyCustomThemeVars(prefs?.themeType === "custom_colors" ? prefs.customColors : null);

  const css = prefs?.customCssEnabled === true ? prefs.customCss : null;
  const inject =
    (options.overlay ?? true) && typeof css === "string" && css.length > 0 && !isSafeModeActive();

  root.dataset.customCss = inject ? "on" : "off";
  const existing = document.getElementById(CUSTOM_CSS_ELEMENT_ID);
  if (inject && typeof css === "string") {
    const el = existing ?? document.createElement("style");
    el.id = CUSTOM_CSS_ELEMENT_ID;
    el.textContent = css;
    document.head.appendChild(el); // (re)append keeps it last in <head>
  } else if (existing) {
    existing.remove();
  }
}

/** Removes only the mounted overlay element and flips data-custom-css=off. */
export function unmountCustomCssOverlay(): void {
  if (typeof document === "undefined") return;
  document.getElementById(CUSTOM_CSS_ELEMENT_ID)?.remove();
  document.documentElement.dataset.customCss = "off";
}

// Builds the PUT body (FR-012 retention): optional customs are omitted when
// unset so the server keeps stored values — never send an explicit null.
function toUpdateBody(prefs: UserThemePreferences): UserPreferencesUpdate {
  const body: UserPreferencesUpdate = {
    themeType: prefs.themeType,
    presetId: prefs.presetId,
    appearanceMode: prefs.appearanceMode,
    customCssEnabled: prefs.customCssEnabled,
  };
  if (prefs.customColors) body.customColors = prefs.customColors;
  if (prefs.customCss) body.customCss = prefs.customCss;
  return body;
}

// Network-level failures surface from fetch as a raw TypeError (never an
// APIError, which means the server answered — validation/auth failures are
// not worth an automatic retry).
function isConnectivityError(err: unknown): boolean {
  if (typeof navigator !== "undefined" && navigator.onLine === false) return true;
  return !(err instanceof APIError);
}

export interface UseThemePreferencesOptions {
  /**
   * Called with a human-readable message when an update fails after the
   * optimistic state was reverted. The toast UI is a later task; until then
   * failures also dispatch THEME_ERROR_EVENT on window.
   */
  onError?: (message: string) => void;
}

export interface UseThemePreferencesResult {
  /** Effective preferences (backend-reconciled cache), null before the profile loads. */
  preferences: UserThemePreferences | null;
  /**
   * Optimistically applies the patch to the DOM and localStorage, then PUTs
   * it. On failure the previous state is restored and the error is reported
   * via onError / THEME_ERROR_EVENT; a connectivity failure is queued and
   * retried when the browser fires `online`, then re-reconciled from the
   * server response.
   */
  updatePreferences: (patch: Partial<UserThemePreferences>) => void;
  isUpdating: boolean;
  /** Message of the last failed update, if any. */
  error: string | null;
}

interface UpdateVars {
  body: UserPreferencesUpdate;
  previous: UserThemePreferences | null;
}

/**
 * Owns the user's theme preferences for a session: reconciles the backend
 * copy from useMe() over the localStorage cache (backend wins on drift,
 * e.g. a change made on another device), keeps the DOM attributes and the
 * custom CSS overlay in sync, and persists mutations.
 */
export function useThemePreferences(
  me?: User,
  { onError }: UseThemePreferencesOptions = {},
): UseThemePreferencesResult {
  const [preferences, setPreferences] = useState<UserThemePreferences | null>(() =>
    readThemePreferences(),
  );
  const [error, setError] = useState<string | null>(null);
  const intl = useOptionalIntl();
  const pendingRetryRef = useRef<UpdateVars | null>(null);
  // Pulled into its own binding (rather than the optional-chain expression
  // inline) so the memo has a single, stable dependency to key off.
  const rawPrefs = me?.preferences;
  const mePreferences = useMemo(
    () => (rawPrefs ? normalizeThemePreferences(rawPrefs) : null),
    [rawPrefs],
  );

  // Profile reconciliation (research.md R-05 §3): the backend copy wins on
  // drift. Adopting the new value into `preferences` happens here, during
  // render, via React's "adjust state when a prop changes" pattern (comparing
  // against the last-seen mePreferences) rather than in an effect — only the
  // DOM/localStorage side effects below stay in the effect.
  // Starting from null (rather than mePreferences) makes the first non-null
  // profile get adopted on mount, e.g. right after login when ["me"] is
  // already cached.
  const [prevMePreferences, setPrevMePreferences] = useState<UserThemePreferences | null>(null);
  if (mePreferences !== prevMePreferences) {
    setPrevMePreferences(mePreferences);
    if (mePreferences) {
      setPreferences(mePreferences);
    }
  }
  useEffect(() => {
    if (!mePreferences) return;
    const cached = readThemePreferences();
    if (!cached || !themePreferencesEqual(cached, mePreferences)) {
      writeThemePreferences(mePreferences);
    }
    applyThemePreferences(mePreferences);
  }, [mePreferences]);

  // Re-resolve the base theme when the OS preference flips while in
  // "system". D4: skip subscribing entirely while a custom-colors theme is
  // active — resolveAppearanceMode already ignores appearanceMode in that
  // case, so this is a pure no-op guard against re-render churn, not a
  // correctness fix (belt-and-suspenders with WP-lib 1e).
  useEffect(() => {
    if (preferences?.appearanceMode !== "system") return;
    if (preferences?.themeType === "custom_colors" && preferences.customColors) return;
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handleChange = () => applyThemePreferences(readThemePreferences());
    mq.addEventListener("change", handleChange);
    return () => mq.removeEventListener("change", handleChange);
  }, [preferences?.appearanceMode, preferences?.themeType, preferences?.customColors]);

  const reportError = useCallback(
    (message: string) => {
      setError(message);
      onError?.(message);
      if (typeof window !== "undefined") {
        window.dispatchEvent(new CustomEvent<string>(THEME_ERROR_EVENT, { detail: message }));
      }
    },
    [onError],
  );

  const mutation = useMutation({
    mutationFn: (vars: UpdateVars) => Users.updatePreferences(vars.body),
    onSuccess: (server) => {
      pendingRetryRef.current = null;
      setError(null);
      const next = normalizeThemePreferences(server) ?? readThemePreferences();
      if (!next) return;
      writeThemePreferences(next);
      setPreferences(next);
      applyThemePreferences(next);
    },
    onError: (err: unknown, vars) => {
      // Revert the optimistic application.
      setPreferences(vars.previous);
      if (vars.previous) {
        writeThemePreferences(vars.previous);
        applyThemePreferences(vars.previous);
      }
      const message =
        err instanceof APIError
          ? err.body || intl.formatMessage({
              id: "web.lib.use_theme_preferences.request_failed",
              defaultMessage: "Request failed ({status})",
              description: "Error shown when saving theme preferences fails with an HTTP error that has no message; {status} is the HTTP status code",
            }, { status: String(err.status) })
          : err instanceof Error
            ? err.message
            : intl.formatMessage(THEME_MESSAGES.update_failed);
      reportError(message);
      // Offline resilience: queue the PUT and replay it when connectivity
      // returns; onSuccess then re-reconciles cache and DOM from the
      // authoritative response.
      if (isConnectivityError(err)) {
        pendingRetryRef.current = vars;
      }
    },
  });

  // Replay a queued PUT once the browser reports connectivity again.
  const mutateRef = useRef(mutation.mutate);
  useEffect(() => {
    mutateRef.current = mutation.mutate;
  }, [mutation.mutate]);
  useEffect(() => {
    if (typeof window === "undefined") return;
    const handleOnline = () => {
      const pending = pendingRetryRef.current;
      if (!pending) return;
      pendingRetryRef.current = null;
      mutateRef.current(pending);
    };
    window.addEventListener("online", handleOnline);
    return () => window.removeEventListener("online", handleOnline);
  }, []);

  const updatePreferences = useCallback(
    (patch: Partial<UserThemePreferences>) => {
      const current = readThemePreferences() ?? preferences ?? DEFAULT_THEME_PREFERENCES;
      const next: UserThemePreferences = {
        ...current,
        ...patch,
        customColors:
          patch.customColors !== undefined ? patch.customColors : current.customColors,
        customCss: patch.customCss !== undefined ? patch.customCss : current.customCss,
      };
      // Optimistic: DOM + cache first…
      setPreferences(next);
      writeThemePreferences(next);
      applyThemePreferences(next);
      // …then the authoritative PUT.
      mutation.mutate({ body: toUpdateBody(next), previous: current });
    },
    [preferences, mutation.mutate],
  );

  return {
    preferences,
    updatePreferences,
    isUpdating: mutation.isPending,
    error,
  };
}
