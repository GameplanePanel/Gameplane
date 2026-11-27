// Theme export/import (FR-014, contracts/theme-export.md). Export is a
// client-side re-serialization of the loaded UserThemePreferences into a
// versioned "gameplane-theme" v1 JSON document; import validates the
// document shape client-side, then maps to the PUT payload (the server
// re-validates and applies FR-013 sanitization as the authoritative gate).

import { defineMessages, type IntlShape, type MessageDescriptor } from "react-intl";

import type {
  AppearanceMode,
  CustomColorConfig,
  ThemeExport,
  ThemeExportInstalled,
  ThemePresetId,
  ThemeType,
  UserThemePreferences,
} from "@/types";

import { englishIntl } from "@/lib/i18n/format";
import { MAX_CUSTOM_CSS_LEN } from "@/lib/theme-sanitize";

export const THEME_EXPORT_FORMAT = "gameplane-theme";
export const THEME_EXPORT_VERSION = 1;

const MESSAGES = defineMessages({
  not_json: {
    id: "web.lib.theme_export.not_json",
    defaultMessage: "invalid theme export: not valid JSON",
    description: "Theme import error: the selected text is not valid JSON",
  },
  not_object: {
    id: "web.lib.theme_export.not_object",
    defaultMessage: "invalid theme export: expected a JSON object",
    description: "Theme import error: the document is valid JSON but not an object",
  },
  bad_format: {
    id: "web.lib.theme_export.bad_format",
    defaultMessage: "invalid theme export: format must be \"{format}\"",
    description: "Theme import error: the document's format field is wrong; {format} is the expected format name, gameplane-theme",
  },
  missing_field: {
    id: "web.lib.theme_export.missing_field",
    defaultMessage: "invalid theme export: missing required field \"{field}\"",
    description: "Theme import error: a required field is missing; {field} is the field path, such as preferences.presetId",
  },
  version_not_number: {
    id: "web.lib.theme_export.version_not_number",
    defaultMessage: "invalid theme export: version must be a number",
    description: "Theme import error: the document's version field is not a number",
  },
  version_newer: {
    id: "web.lib.theme_export.version_newer",
    defaultMessage: "invalid theme export: unsupported version {version} — this theme was exported by a newer version of Gameplane; upgrade this instance before importing",
    description: "Theme import error: the document was exported by a newer Gameplane version; {version} is the document's version number",
  },
  version_must_be: {
    id: "web.lib.theme_export.version_must_be",
    defaultMessage: "invalid theme export: version must be {version}",
    description: "Theme import error: the document's version is not supported; {version} is the supported version number",
  },
  preferences_not_object: {
    id: "web.lib.theme_export.preferences_not_object",
    defaultMessage: "invalid theme export: preferences must be an object",
    description: "Theme import error: the preferences field is not an object",
  },
  theme_type_values: {
    id: "web.lib.theme_export.theme_type_values",
    defaultMessage: "invalid theme export: preferences.themeType must be \"preset\" or \"custom_colors\"",
    description: "Theme import error: themeType has an unknown value; preset and custom_colors are the value names, keep them as written",
  },
  preset_id_values: {
    id: "web.lib.theme_export.preset_id_values",
    defaultMessage: "invalid theme export: preferences.presetId must be \"pink\" or \"legacy\"",
    description: "Theme import error: presetId has an unknown value; pink and legacy are the value names, keep them as written",
  },
  appearance_mode_values: {
    id: "web.lib.theme_export.appearance_mode_values",
    defaultMessage: "invalid theme export: preferences.appearanceMode must be \"light\", \"dark\", or \"system\"",
    description: "Theme import error: appearanceMode has an unknown value; light, dark and system are the value names, keep them as written",
  },
  custom_css_enabled_boolean: {
    id: "web.lib.theme_export.custom_css_enabled_boolean",
    defaultMessage: "invalid theme export: preferences.customCssEnabled must be a boolean",
    description: "Theme import error: customCssEnabled is not true or false",
  },
  custom_colors_object: {
    id: "web.lib.theme_export.custom_colors_object",
    defaultMessage: "invalid theme export: preferences.customColors must be an object or null",
    description: "Theme import error: customColors is neither an object nor null",
  },
  custom_color_hex: {
    id: "web.lib.theme_export.custom_color_hex",
    defaultMessage: "invalid theme export: preferences.customColors.{key} must be a #RRGGBB hex color",
    description: "Theme import error: a custom color is not a #RRGGBB hex value; {key} is accent or surface",
  },
  custom_css_string: {
    id: "web.lib.theme_export.custom_css_string",
    defaultMessage: "invalid theme export: preferences.customCss must be a string or null",
    description: "Theme import error: customCss is neither a string nor null",
  },
  custom_css_too_long: {
    id: "web.lib.theme_export.custom_css_too_long",
    defaultMessage: "invalid theme export: preferences.customCss exceeds the maximum length of {max} characters",
    description: "Theme import error: the imported custom CSS is longer than the limit; {max} is the maximum number of characters",
  },
  installed_object: {
    id: "web.lib.theme_export.installed_object",
    defaultMessage: "invalid theme export: preferences.installed must be an object with source, name and version strings",
    description: "Theme import error: an installed theme export lacks its source, name or version",
  },
  installed_fallback: {
    id: "web.lib.theme_export.installed_fallback",
    defaultMessage: "This export used the installed theme {name} {version} from {source}, which is not installed here. Modern Pink is used instead.",
    description: "Theme import notice: the exported installed theme is missing on this instance, so Modern Pink is applied; {name} is the theme name, {version} its version, {source} the theme source name",
  },
});

const HEX_RE = /^#([0-9a-fA-F]{6})$/;

const THEME_TYPES: readonly ThemeType[] = ["preset", "custom_colors", "installed"];
const PRESET_IDS: readonly ThemePresetId[] = ["pink", "legacy"];
const APPEARANCE_MODES: readonly AppearanceMode[] = ["light", "dark", "system"];

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function enumValue<T extends string>(allowed: readonly T[], v: unknown): T | null {
  return typeof v === "string" && (allowed as readonly string[]).includes(v) ? (v as T) : null;
}

// utf8Len mirrors the server's byte-length accounting for the CSS cap.
function utf8Len(s: string): number {
  return new TextEncoder().encode(s).length;
}

// buildThemeExport builds the portable document from the loaded
// preferences. updatedAt is intentionally excluded (instance-local
// metadata); retained-but-inactive customs (customColors while on a preset,
// customCss while the overlay is disabled) are included so a full setup
// survives the transfer.
// installed is the source, name and version of the installed theme in use. The
// caller resolves it from the installed list (the prefs carry only ids and
// tokens, which never leave the instance). Without it, an installed selection
// exports as the base preset, since the document cannot name the theme.
export function buildThemeExport(
  prefs: UserThemePreferences,
  installed?: ThemeExportInstalled | null,
): ThemeExport {
  const exportInstalled = prefs.themeType === "installed" && installed ? installed : null;
  const themeType: ThemeType =
    prefs.themeType === "installed" && !exportInstalled ? "preset" : prefs.themeType;
  return {
    format: THEME_EXPORT_FORMAT,
    version: THEME_EXPORT_VERSION,
    preferences: {
      themeType,
      presetId: prefs.presetId,
      appearanceMode: prefs.appearanceMode,
      customColors: prefs.customColors ?? null,
      customCssEnabled: prefs.customCssEnabled,
      customCss: prefs.customCss ?? null,
      ...(exportInstalled ? { installed: exportInstalled } : {}),
    },
  };
}

// fallbackInstalledImport resolves an imported installed theme against this
// instance. When isInstalled reports the source+name as installed, the document
// is returned unchanged. Otherwise it becomes the Modern Pink preset with a
// notice. Non-installed documents pass through with a null notice.
export function fallbackInstalledImport(
  doc: ThemeExport,
  isInstalled: (ref: ThemeExportInstalled) => boolean,
  intl: IntlShape = englishIntl,
): { export: ThemeExport; notice: string | null } {
  const { installed } = doc.preferences;
  if (doc.preferences.themeType !== "installed" || !installed || isInstalled(installed)) {
    return { export: doc, notice: null };
  }
  const preferences: ThemeExport["preferences"] = {
    ...doc.preferences,
    themeType: "preset",
    presetId: "pink",
  };
  delete preferences.installed;
  const noticeDescriptor: MessageDescriptor = MESSAGES.installed_fallback;
  const noticeValues: Record<string, string> = {
    name: installed.name,
    version: installed.version,
    source: installed.source,
  };
  return {
    export: { ...doc, preferences },
    notice: intl.formatMessage(noticeDescriptor, noticeValues),
  };
}

export type ValidateThemeExportResult =
  | { ok: true; export: ThemeExport }
  | { ok: false; error: string };

// validateThemeExport parses and validates a theme export document. Returns
// a specific, field-naming error for every contract §2/§4 rejection reason;
// documents from a newer exporter (version > 1) get an upgrade prompt.
// The optional intl localizes the error text; English is the default.
export function validateThemeExport(raw: string, intl: IntlShape = englishIntl): ValidateThemeExportResult {
  const fail = (descriptor: MessageDescriptor, values?: Record<string, string>): ValidateThemeExportResult =>
    ({ ok: false, error: intl.formatMessage(descriptor, values) });
  let doc: unknown;
  try {
    doc = JSON.parse(raw);
  } catch {
    return fail(MESSAGES.not_json);
  }
  if (!isRecord(doc)) {
    return fail(MESSAGES.not_object);
  }
  if (doc.format !== THEME_EXPORT_FORMAT) {
    return fail(MESSAGES.bad_format, { format: THEME_EXPORT_FORMAT });
  }
  if (doc.version === undefined) {
    return fail(MESSAGES.missing_field, { field: "version" });
  }
  if (typeof doc.version !== "number") {
    return fail(MESSAGES.version_not_number);
  }
  if (doc.version > THEME_EXPORT_VERSION) {
    return fail(MESSAGES.version_newer, { version: String(doc.version) });
  }
  if (doc.version !== THEME_EXPORT_VERSION) {
    return fail(MESSAGES.version_must_be, { version: String(THEME_EXPORT_VERSION) });
  }
  if (doc.preferences === undefined || doc.preferences === null) {
    return fail(MESSAGES.missing_field, { field: "preferences" });
  }
  if (!isRecord(doc.preferences)) {
    return fail(MESSAGES.preferences_not_object);
  }
  const prefs = doc.preferences;

  const themeType = enumValue(THEME_TYPES, prefs.themeType);
  let installed: ThemeExportInstalled | undefined;
  if (themeType === "installed") {
    const ref = prefs.installed;
    if (!isRecord(ref) || typeof ref.source !== "string" || typeof ref.name !== "string" || typeof ref.version !== "string") {
      return fail(MESSAGES.installed_object);
    }
    installed = { source: ref.source, name: ref.name, version: ref.version };
  }
  if (themeType === null) {
    if (prefs.themeType === undefined) {
      return fail(MESSAGES.missing_field, { field: "preferences.themeType" });
    }
    return fail(MESSAGES.theme_type_values);
  }
  const presetId = enumValue(PRESET_IDS, prefs.presetId);
  if (presetId === null) {
    if (prefs.presetId === undefined) {
      return fail(MESSAGES.missing_field, { field: "preferences.presetId" });
    }
    return fail(MESSAGES.preset_id_values);
  }
  const appearanceMode = enumValue(APPEARANCE_MODES, prefs.appearanceMode);
  if (appearanceMode === null) {
    if (prefs.appearanceMode === undefined) {
      return fail(MESSAGES.missing_field, { field: "preferences.appearanceMode" });
    }
    return fail(MESSAGES.appearance_mode_values);
  }
  if (prefs.customCssEnabled === undefined) {
    return fail(MESSAGES.missing_field, { field: "preferences.customCssEnabled" });
  }
  if (typeof prefs.customCssEnabled !== "boolean") {
    return fail(MESSAGES.custom_css_enabled_boolean);
  }

  let customColors: CustomColorConfig | null = null;
  if (prefs.customColors !== undefined && prefs.customColors !== null) {
    if (!isRecord(prefs.customColors)) {
      return fail(MESSAGES.custom_colors_object);
    }
    const cc = prefs.customColors;
    if (typeof cc.accent !== "string" || !HEX_RE.test(cc.accent)) {
      return fail(MESSAGES.custom_color_hex, { key: "accent" });
    }
    if (typeof cc.surface !== "string" || !HEX_RE.test(cc.surface)) {
      return fail(MESSAGES.custom_color_hex, { key: "surface" });
    }
    customColors = { accent: cc.accent, surface: cc.surface };
  }

  let customCss: string | null = null;
  if (prefs.customCss !== undefined && prefs.customCss !== null) {
    if (typeof prefs.customCss !== "string") {
      return fail(MESSAGES.custom_css_string);
    }
    if (utf8Len(prefs.customCss) > MAX_CUSTOM_CSS_LEN) {
      return fail(MESSAGES.custom_css_too_long, { max: String(MAX_CUSTOM_CSS_LEN) });
    }
    customCss = prefs.customCss;
  }

  return {
    ok: true,
    export: {
      format: THEME_EXPORT_FORMAT,
      version: THEME_EXPORT_VERSION,
      preferences: {
        themeType,
        presetId,
        appearanceMode,
        customColors,
        customCssEnabled: prefs.customCssEnabled,
        customCss,
        ...(installed ? { installed } : {}),
      },
    },
  };
}
