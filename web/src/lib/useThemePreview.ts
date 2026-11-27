// In-memory preview of a catalog or installed theme (spec 043 T026). The
// preview goes through the same DOM injection as saved prefs
// (applyThemePreferences) and never writes localStorage or calls the API.
// The preview stays on across pages (valgul 2026-11-27, as in the design).
// Exit or a reload restores the saved theme: nothing is persisted, so the
// saved cache is what gets re-applied.

import { useSyncExternalStore } from "react";
import {
  DEFAULT_THEME_PREFERENCES,
  applyThemePreferences,
  isSafeModeActive,
  normalizeInstalledRender,
  readThemePreferences,
  setThemePreviewReapplier,
} from "@/lib/useThemePreferences";
import type { ThemeModeTokens } from "@/types";

export interface ThemePreviewTheme {
  /** Catalog entry the preview was opened from. */
  catalogId: number;
  /** Installed id when the theme is already installed; null otherwise. */
  installedId: number | null;
  /**
   * True when the installed copy is an older version than the previewed one:
   * Keep then updates that copy before selecting it, so the saved theme is
   * the version that was previewed.
   */
  updateOnKeep?: boolean;
  displayName: string;
  version: string;
  tokens: ThemeModeTokens;
  css: string | null;
}

let current: ThemePreviewTheme | null = null;
const listeners = new Set<() => void>();

function setCurrent(next: ThemePreviewTheme | null): void {
  current = next;
  for (const listener of listeners) listener();
}

/** Current preview, or null. Read by the banner and the detail page. */
export function getThemePreview(): ThemePreviewTheme | null {
  return current;
}

/**
 * Applies the theme in memory. Returns false, changing nothing, when safe
 * mode is active (safe mode suspends installed themes entirely).
 */
export function startThemePreview(theme: ThemePreviewTheme): boolean {
  if (!applyPreview(theme)) return false;
  setCurrent(theme);
  return true;
}

// Applies the theme over the saved base preferences (appearance mode, custom
// CSS). Returns false, changing nothing, in safe mode or on invalid tokens.
function applyPreview(theme: ThemePreviewTheme): boolean {
  if (isSafeModeActive()) return false;
  // Server tokens are validated here, so malformed data never reaches the DOM.
  const render = normalizeInstalledRender({
    tokens: theme.tokens,
    css: theme.css,
    version: theme.version,
  });
  if (render === null) return false;
  const base = readThemePreferences() ?? DEFAULT_THEME_PREFERENCES;
  applyThemePreferences({
    ...base,
    themeType: "installed",
    installedThemeId: theme.installedId,
    installedTheme: render,
  });
  return true;
}

// The OS appearance handler re-applies the preview instead of the saved theme.
setThemePreviewReapplier(() => current !== null && applyPreview(current));

/** Ends the preview and re-applies the saved theme from the local cache. */
export function exitThemePreview(): void {
  if (current === null) return;
  setCurrent(null);
  applyThemePreferences(readThemePreferences());
}

/**
 * Clears the preview state without touching the DOM. Used after Keep, when
 * the server-backed preferences are about to be re-applied on refetch.
 */
export function dropThemePreview(theme?: ThemePreviewTheme): void {
  // After Keep, only clear the preview that was kept: a newer preview started
  // while the request ran stays on.
  if (theme !== undefined && current !== theme) return;
  setCurrent(null);
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export interface UseThemePreviewResult {
  preview: ThemePreviewTheme | null;
  start: (theme: ThemePreviewTheme) => boolean;
  exit: () => void;
}

/** Subscribes to the shared preview state so the banner and pages stay in step. */
export function useThemePreview(): UseThemePreviewResult {
  const preview = useSyncExternalStore(subscribe, getThemePreview, getThemePreview);
  return { preview, start: startThemePreview, exit: exitThemePreview };
}
