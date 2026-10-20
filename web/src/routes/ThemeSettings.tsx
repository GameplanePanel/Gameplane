// Theme & Appearance settings page (specs/done_016-user-theme-customization,
// contracts/theme-ui.md; assets/design-export lWvcv). One scrollable page of
// stacked cards inside the standard app shell: Preset theme, Appearance
// mode, Custom colors, Custom CSS overlay, Export / Import. Edits apply to
// the DOM live for preview (base attributes via applyThemePreferences,
// custom-color tokens via #gameplane-custom-theme-vars); Save persists the
// draft through useThemePreferences, and Reset to Defaults is the only
// action that deletes stored custom colors/CSS (FR-012).

import { useCallback, useEffect, useMemo, useRef, useState, type ChangeEvent, type ReactNode } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button, Card, Label, RadioGroup, Radio, Switch } from "@heroui/react";
import {
  AlertTriangle,
  CircleAlert,
  Copy,
  Download,
  Info,
  Palette,
  RotateCcw,
  Upload,
} from "lucide-react";
import { PageHeader } from "@/components/ui/PageHeader";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { SettingsNav } from "@/components/ui/SettingsNav";
import { LanguagePicker } from "@/components/settings/LanguagePicker";
import { cn } from "@/lib/utils";
import { errorTextIntl } from "@/lib/errors";
import { can, useMe } from "@/lib/auth";
import { Users } from "@/lib/endpoints";
import {
  applyThemePreferences,
  DEFAULT_THEME_PREFERENCES,
  readThemePreferences,
  themePreferencesEqual,
  useThemePreferences,
  writeThemePreferences,
} from "@/lib/useThemePreferences";
import {
  contrastRatio,
  deriveCustomThemeTokens,
  passesContrastGuard,
} from "@/lib/theme-derivation";
import { MAX_CUSTOM_CSS_LEN, sanitizeCustomCss, type SanitizeResult } from "@/lib/theme-sanitize";
import { buildThemeExport, validateThemeExport } from "@/lib/theme-export";
import type { AppearanceMode, CustomColorConfig, ThemePresetId, UserThemePreferences } from "@/types";
import { defineMessages, type IntlShape, type MessageDescriptor } from "react-intl";
import { useFormat, type FormatApi } from "@/lib/i18n/format";
import { useOptionalIntl } from "@/lib/i18n/format";

const PRESET_MESSAGES = defineMessages({
  pinkName: { id: "web.routes.theme_settings.preset_modern_pink", defaultMessage: "Modern Pink", description: "Theme settings: name of the Modern Pink preset theme" },
  pinkDesc: { id: "web.routes.theme_settings.preset_modern_pink_desc", defaultMessage: "Modern HeroUI brand", description: "Theme settings: one-line description of the Modern Pink preset theme" },
  legacyName: { id: "web.routes.theme_settings.preset_legacy_orange", defaultMessage: "Legacy Orange", description: "Theme settings: name of the Legacy Orange preset theme" },
  legacyDesc: { id: "web.routes.theme_settings.preset_legacy_orange_desc", defaultMessage: "Original Gameplane", description: "Theme settings: one-line description of the Legacy Orange preset theme" },
});

// Display names for the accent swatches, surface tones and appearance modes.
const LABELS = defineMessages({
  accentBlue: { id: "web.routes.theme_settings.accent_blue", defaultMessage: "Blue", description: "Theme settings: name of the Blue accent swatch (also its accessible name)" },
  accentEmerald: { id: "web.routes.theme_settings.accent_emerald", defaultMessage: "Emerald", description: "Theme settings: name of the Emerald accent swatch (also its accessible name)" },
  accentPurple: { id: "web.routes.theme_settings.accent_purple", defaultMessage: "Purple", description: "Theme settings: name of the Purple accent swatch (also its accessible name)" },
  accentAmber: { id: "web.routes.theme_settings.accent_amber", defaultMessage: "Amber", description: "Theme settings: name of the Amber accent swatch (also its accessible name)" },
  accentCyan: { id: "web.routes.theme_settings.accent_cyan", defaultMessage: "Cyan", description: "Theme settings: name of the Cyan accent swatch (also its accessible name)" },
  accentRose: { id: "web.routes.theme_settings.accent_rose", defaultMessage: "Rose", description: "Theme settings: name of the Rose accent swatch (also its accessible name)" },
  accentOrange: { id: "web.routes.theme_settings.accent_orange", defaultMessage: "Orange", description: "Theme settings: name of the Orange accent swatch (also its accessible name)" },
  surfaceDarkSlate: { id: "web.routes.theme_settings.surface_dark_slate", defaultMessage: "Dark Slate", description: "Theme settings: name of the Dark Slate surface tone" },
  surfaceMidnight: { id: "web.routes.theme_settings.surface_midnight", defaultMessage: "Midnight", description: "Theme settings: name of the Midnight surface tone" },
  surfaceCharcoal: { id: "web.routes.theme_settings.surface_charcoal", defaultMessage: "Charcoal", description: "Theme settings: name of the Charcoal surface tone" },
  surfaceCrispLight: { id: "web.routes.theme_settings.surface_crisp_light", defaultMessage: "Crisp Light", description: "Theme settings: name of the Crisp Light surface tone" },
  modeLight: { id: "web.routes.theme_settings.mode_light", defaultMessage: "Light", description: "Theme settings: label of the Light appearance mode" },
  modeDark: { id: "web.routes.theme_settings.mode_dark", defaultMessage: "Dark", description: "Theme settings: label of the Dark appearance mode" },
  modeSystem: { id: "web.routes.theme_settings.mode_system", defaultMessage: "System", description: "Theme settings: label of the System appearance mode, which follows the OS setting" },
});

// Dark-mode accent/surface pair per preset, per contracts/theme-ui.md §3.1
// and the lWvcv swatch fills.
const PRESETS: Array<{
  id: ThemePresetId;
  name: MessageDescriptor;
  desc: MessageDescriptor;
  accent: string;
  surface: string;
}> = [
  { id: "pink",   name: PRESET_MESSAGES.pinkName,   desc: PRESET_MESSAGES.pinkDesc, accent: "#FF4FA3", surface: "#1C1A20" },
  { id: "legacy", name: PRESET_MESSAGES.legacyName, desc: PRESET_MESSAGES.legacyDesc, accent: "#F97316", surface: "#171717" },
];

// Accent palette swatches (lWvcv ccSwatch fills) and surface tone options
// (contracts/theme-ui.md §3.2). The three dark surfaces keep the derived
// base in dark mode; Crisp Light flips it to light.
const ACCENT_SWATCHES: Array<{ name: MessageDescriptor; hex: string }> = [
  { name: LABELS.accentBlue, hex: "#3B82F6" },
  { name: LABELS.accentEmerald, hex: "#10B981" },
  { name: LABELS.accentPurple, hex: "#8B5CF6" },
  { name: LABELS.accentAmber, hex: "#F59E0B" },
  { name: LABELS.accentCyan, hex: "#06B6D4" },
  { name: LABELS.accentRose, hex: "#F43F5E" },
  { name: LABELS.accentOrange, hex: "#F97316" },
];

const SURFACE_TONES: Array<{ name: MessageDescriptor; hex: string }> = [
  { name: LABELS.surfaceDarkSlate, hex: "#1E293B" },
  { name: LABELS.surfaceMidnight, hex: "#0F172A" },
  { name: LABELS.surfaceCharcoal, hex: "#171717" },
  { name: LABELS.surfaceCrispLight, hex: "#F8FAFC" },
];

const APPEARANCE_MODES: Array<{ value: AppearanceMode; label: MessageDescriptor }> = [
  { value: "light",  label: LABELS.modeLight },
  { value: "dark",   label: LABELS.modeDark },
  { value: "system", label: LABELS.modeSystem },
];

const CSS_PLACEHOLDER = "/* Example: customize typography or borders */\n:root {\n  --radius: 14px;\n}";

// utf8Len mirrors the sanitizer's byte accounting for the editor counter.
// presetLabel returns the display name of a preset id, or undefined when unknown.
function presetLabel(intl: IntlShape, id: ThemePresetId): string | undefined {
  const preset = PRESETS.find((p) => p.id === id);
  return preset ? intl.formatMessage(preset.name) : undefined;
}

function utf8Len(s: string): number {
  return new TextEncoder().encode(s).length;
}

const HEX_COLOR_RE = /^#[0-9a-fA-F]{6}$/;

// ---------------------------------------------------------------------------
// Custom CSS editor validation (contracts/theme-ui.md §3.3): the sanitizer
// decides, the editor copy quotes the offending line like the T1wkiT design.
// ---------------------------------------------------------------------------

function findLine(css: string, re: RegExp): { line: number; text: string } | null {
  const lines = css.split("\n");
  for (let i = 0; i < lines.length; i++) {
    if (re.test(lines[i] ?? "")) return { line: i + 1, text: (lines[i] ?? "").trim() };
  }
  return null;
}

function editorErrorMessage(
  css: string,
  result: Extract<SanitizeResult, { ok: false }>,
  fmt: FormatApi,
  intl: IntlShape,
): string {
  const err = result.error;
  if (err.includes("@import")) {
    const hit = findLine(css, /@import/i);
    return hit
      ? intl.formatMessage({ id: "web.routes.theme_settings.editor_error_external_line", defaultMessage: "External resource loads are not allowed (line {line}: `{text}`). Paste the content inline instead.", description: "Theme settings: custom CSS error for an @import or external url() on a known line; {line} is the line number and {text} the offending source line" }, { line: String(hit.line), text: hit.text })
      : intl.formatMessage({ id: "web.routes.theme_settings.editor_error_external_import", defaultMessage: "External resource loads are not allowed (@import). Paste the content inline instead.", description: "Theme settings: custom CSS error for an @import rule when its line cannot be located" });
  }
  if (err.includes("external url()")) {
    const hit = findLine(css, /url\(\s*["']?(?:https?:)?\/\//i);
    return hit
      ? intl.formatMessage({ id: "web.routes.theme_settings.editor_error_external_line", defaultMessage: "External resource loads are not allowed (line {line}: `{text}`). Paste the content inline instead.", description: "Theme settings: custom CSS error for an @import or external url() on a known line; {line} is the line number and {text} the offending source line" }, { line: String(hit.line), text: hit.text })
      : intl.formatMessage({ id: "web.routes.theme_settings.editor_error_external_url", defaultMessage: "External resource loads are not allowed (external url()). Paste the content inline instead.", description: "Theme settings: custom CSS error for an external url() when its line cannot be located" });
  }
  if (err.includes("unbalanced braces")) {
    return intl.formatMessage({ id: "web.routes.theme_settings.editor_error_unbalanced_braces", defaultMessage: "Syntax error: unbalanced braces — every {open} must have a matching {close}.", description: "Theme settings: custom CSS syntax error; {open} and {close} are the brace characters { and }" }, { open: "'{'", close: "'}'" });
  }
  if (err.includes("maximum length")) {
    return intl.formatMessage({ id: "web.routes.theme_settings.editor_error_max_length", defaultMessage: "Custom CSS exceeds the {max}-byte limit.", description: "Theme settings: custom CSS error when the text is longer than the limit; {max} is the formatted byte limit" }, { max: fmt.number(MAX_CUSTOM_CSS_LEN) });
  }
  if (err.includes("HTML delimiter")) {
    return intl.formatMessage({ id: "web.routes.theme_settings.editor_error_html_delimiter", defaultMessage: "HTML {style} and {script} tags are not allowed in custom CSS.", description: "Theme settings: custom CSS error for HTML style or script tags; {style} and {script} are the literal tags" }, { style: "<style>", script: "<script>" });
  }
  return err;
}

// ---------------------------------------------------------------------------

function ThemeCard({ title, subtitle, children }: { title: string; subtitle: string; children: ReactNode }) {
  return (
    <Card className="space-y-4">
      <Card.Header className="space-y-2 pb-2">
        <div className="font-medium text-base">{title}</div>
        <div className="text-xs text-muted">{subtitle}</div>
      </Card.Header>
      <Card.Content className="space-y-4">{children}</Card.Content>
    </Card>
  );
}

export function ThemeSettingsPage() {
  const intl = useOptionalIntl();
  const fmt = useFormat();
  const { data: me } = useMe();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  // Save/import both persist through useThemePreferences; lastError/lastDone
  // route the hook's feedback inline (there is no toast layer — THEME_ERROR_EVENT
  // also fires for a future toast to pick up).
  const whereRef = useRef<"save" | "import" | null>(null);
  const [lastError, setLastError] = useState<{ where: "save" | "import"; message: string } | null>(null);
  const [lastDone, setLastDone] = useState<{ where: "save" | "import" } | null>(null);

  const onError = useCallback((message: string) => {
    setLastError({ where: whereRef.current ?? "save", message });
    setLastDone(null);
  }, []);

  const { preferences, updatePreferences, isUpdating } = useThemePreferences(me, { onError });

  // Draft being edited/previewed. Initialized once from the reconciled
  // preferences; on a profile with no prefs anywhere it falls back to the
  // pink/system defaults.
  const [draft, setDraft] = useState<UserThemePreferences | null>(null);
  const noPrefsAnywhere = me !== undefined && me.preferences == null && readThemePreferences() === null;
  const ready = preferences !== null || noPrefsAnywhere;
  // Initializes the draft the first time preferences become available (the
  // false -> true transition of `ready`). Setting state during render here
  // follows React's "adjust state when a prop changes" pattern instead of an
  // effect; `wasReady` makes the transition one-shot, same as the old ref.
  const [wasReady, setWasReady] = useState(false);
  if (ready && !wasReady) {
    setWasReady(true);
    setDraft(
      preferences
        ? { ...preferences, customColors: preferences.customColors ? { ...preferences.customColors } : null }
        : { ...DEFAULT_THEME_PREFERENCES },
    );
  }

  const update = (patch: Partial<UserThemePreferences>) => {
    setLastError(null);
    setLastDone(null);
    setDraft((d) => (d ? { ...d, ...patch } : d));
  };

  // Leaving without saving restores the last persisted state to the DOM.
  useEffect(() => {
    return () => {
      const saved = readThemePreferences();
      applyThemePreferences(saved);
    };
  }, []);

  // After a successful import the draft follows the server's stored profile
  // (which may retain customs the document omitted, FR-012). Keyed on the
  // completion object, so the sync runs once per import, a later ["me"]
  // refetch can't clobber subsequent edits, and the "Import applied"
  // indicator survives until the next edit.
  const [importText, setImportText] = useState("");
  const [syncedDone, setSyncedDone] = useState(lastDone);
  if (lastDone !== syncedDone) {
    setSyncedDone(lastDone);
    if (lastDone?.where === "import") {
      setImportText("");
      if (preferences) {
        setDraft({ ...preferences, customColors: preferences.customColors ? { ...preferences.customColors } : null });
      }
    }
  }

  // Record a completion only when the mutation settled without an error
  // report (onError runs before this effect's re-render on failure).
  useEffect(() => {
    if (isUpdating || !whereRef.current) return;
    const where = whereRef.current;
    whereRef.current = null;
    setLastDone((cur) => (lastError ? cur : { where }));
  }, [isUpdating, lastError]);

  const runUpdate = (patch: Partial<UserThemePreferences>, where: "save" | "import") => {
    whereRef.current = where;
    setLastError(null);
    setLastDone(null);
    updatePreferences(patch);
  };

  const dirty = useMemo(
    () => draft !== null && (preferences === null || !themePreferencesEqual(draft, preferences)),
    [draft, preferences],
  );

  // --- Custom colors -------------------------------------------------------

  const draftAccent = draft?.customColors?.accent ?? ACCENT_SWATCHES[0].hex;
  const draftSurface = draft?.customColors?.surface ?? SURFACE_TONES[0].hex;
  const effectiveColors: CustomColorConfig = useMemo(
    () => ({ accent: draftAccent, surface: draftSurface }),
    [draftAccent, draftSurface],
  );
  const derived = useMemo(
    () => deriveCustomThemeTokens(effectiveColors.accent, effectiveColors.surface),
    [effectiveColors],
  );
  const accentSurfaceRatio = contrastRatio(effectiveColors.accent, effectiveColors.surface);
  const guardOk = passesContrastGuard(effectiveColors.accent, effectiveColors.surface);

  const pickAccent = (hex: string) =>
    update({ themeType: "custom_colors", customColors: { accent: hex, surface: effectiveColors.surface } });
  const pickSurface = (hex: string) =>
    update({ themeType: "custom_colors", customColors: { accent: effectiveColors.accent, surface: hex } });

  // Base-theme selection (contracts/theme-ui.md §3.1): the third radio card
  // activates the custom-colors base; either preset restores "preset". The
  // custom-colors card's controls stay visible but disabled until the
  // custom base is active (§3.2).
  const colorsActive = draft?.themeType === "custom_colors";
  const pickBase = (value: string) => {
    if (value === "custom_colors") {
      update({
        themeType: "custom_colors",
        customColors: draft?.customColors ?? {
          accent: ACCENT_SWATCHES[0].hex,
          surface: SURFACE_TONES[0].hex,
        },
      });
    } else {
      update({ presetId: value as ThemePresetId, themeType: "preset" });
    }
  };

  // --- Custom CSS ------------------------------------------------------------

  const cssDraft = draft?.customCss ?? "";
  const cssBytes = utf8Len(cssDraft);
  const cssResult = useMemo(() => sanitizeCustomCss(cssDraft), [cssDraft]);
  const cssError = cssResult.ok ? null : editorErrorMessage(cssDraft, cssResult, fmt, intl);

  // Live preview: the draft applies to the DOM as it changes; applyThemePreferences
  // mounts the custom-color vars and re-appends #gameplane-custom-css last in <head>.
  // A stylesheet the sanitizer rejects is never previewed (FR-013): the last
  // saved overlay stays in place instead, so e.g. an @import is never fetched.
  const savedCss = preferences?.customCss ?? null;
  useEffect(() => {
    if (!draft) return;
    applyThemePreferences(cssResult.ok ? draft : { ...draft, customCss: savedCss });
  }, [draft, cssResult.ok, savedCss]);

  // --- Export ----------------------------------------------------------------

  // The export reflects the saved profile state (contract theme-export.md §3),
  // including retained-but-inactive customs — not the unsaved draft.
  const exportSource = preferences ?? draft ?? DEFAULT_THEME_PREFERENCES;
  const exportJson = useMemo(() => JSON.stringify(buildThemeExport(exportSource), null, 2), [exportSource]);

  const [copyNote, setCopyNote] = useState<string | null>(null);
  const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    return () => {
      if (copyTimerRef.current) clearTimeout(copyTimerRef.current);
    };
  }, []);

  const copyExport = async () => {
    try {
      await navigator.clipboard.writeText(exportJson);
      setCopyNote(intl.formatMessage({ id: "web.routes.theme_settings.copied_to_clipboard", defaultMessage: "Copied to clipboard", description: "Theme settings: status note after the export JSON was copied" }));
    } catch {
      setCopyNote(intl.formatMessage({ id: "web.routes.theme_settings.clipboard_unavailable", defaultMessage: "Clipboard unavailable — select the text and copy it manually.", description: "Theme settings: status note when the clipboard write failed" }));
    }
    if (copyTimerRef.current) clearTimeout(copyTimerRef.current);
    copyTimerRef.current = setTimeout(() => setCopyNote(null), 3000);
  };

  const downloadExport = () => {
    if (typeof URL.createObjectURL !== "function") return;
    const blob = new Blob([exportJson], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "gameplane-theme.json";
    a.click();
    URL.revokeObjectURL(url);
  };

  // --- Import ----------------------------------------------------------------

  const importResult = useMemo(
    () => (importText.trim() === "" ? null : validateThemeExport(importText, intl)),
    [importText, intl],
  );
  const importPreview = importResult?.ok ? importResult.export : null;
  const importClientError = importResult && !importResult.ok ? importResult.error : null;

  const fileInputRef = useRef<HTMLInputElement>(null);
  const onFilePicked = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    e.target.value = ""; // re-picking the same file must re-trigger onChange
    if (!file) return;
    setImportText(await file.text());
  };

  const applyImport = () => {
    if (!importPreview) return;
    const p = importPreview.preferences;
    runUpdate(
      {
        themeType: p.themeType,
        presetId: p.presetId,
        appearanceMode: p.appearanceMode,
        customColors: p.customColors ?? null,
        customCssEnabled: p.customCssEnabled,
        customCss: p.customCss ?? null,
      },
      "import",
    );
  };

  // --- Reset to Defaults (FR-012: the only action that deletes customs) ------

  const [confirmingReset, setConfirmingReset] = useState(false);
  const [resetError, setResetError] = useState<string | null>(null);
  const reset = useMutation({
    mutationFn: () =>
      Users.resetPreferences({
        presetId: draft?.presetId ?? "pink",
        appearanceMode: draft?.appearanceMode ?? "system",
      }),
    onSuccess: (result) => {
      setConfirmingReset(false);
      setResetError(null);
      setLastError(null);
      setLastDone(null);
      writeThemePreferences(result);
      applyThemePreferences(result);
      setDraft({ ...result, customColors: result.customColors ?? null });
      void queryClient.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (err) => setResetError(errorTextIntl(intl, err, intl.formatMessage({ id: "web.routes.theme_settings.reset_failed", defaultMessage: "Reset failed", description: "Theme settings: fallback error shown when resetting the theme fails" }))),
  });

  const save = () => {
    if (!draft || cssError) return;
    runUpdate(
      {
        themeType: draft.themeType,
        presetId: draft.presetId,
        appearanceMode: draft.appearanceMode,
        customColors: draft.customColors ?? null,
        customCssEnabled: draft.customCssEnabled,
        customCss: draft.customCss ?? null,
      },
      "save",
    );
  };

  return (
    <div className="space-y-6 p-6">
      <PageHeader
        title={intl.formatMessage({ id: "web.routes.theme_settings.theme_appearance", defaultMessage: "Theme & Appearance", description: "Theme settings: page title, Theme & Appearance, at the top of the screen; keep it short" })}
        description={intl.formatMessage({ id: "web.routes.theme_settings.personal_theme_presets_colors_custom", defaultMessage: "Personal theme presets, colors, custom CSS, and theme portability.", description: "Theme settings: description under the page title: personal presets, colors, custom CSS and theme import and export" })}
      />

      <div className="grid gap-6 lg:grid-cols-[220px_1fr]">
        {/* Theme is open to every user; the admin sections only show for
            users who can open /admin (config:manage). */}
        <SettingsNav
          active="theme"
          showAdminSections={can(me, "config:manage")}
          onSelect={(key) => void navigate({ to: "/admin", search: { section: key } })}
        />

        <div className="space-y-6">
          {!draft ? (
            <Card>
              <Card.Content className="text-sm text-muted">{intl.formatMessage({ id: "web.routes.theme_settings.loading_theme_preferences", defaultMessage: "Loading theme preferences…", description: "Theme settings: loading text shown while the saved theme preferences are fetched" })}</Card.Content>
            </Card>
          ) : (
            <>
              {/* Language (spec 019 T044): personal, saved to the account */}
              <LanguagePicker />

              {/* Preset theme (contracts/theme-ui.md §3.1) */}
              <ThemeCard title={intl.formatMessage({ id: "web.routes.theme_settings.preset_theme", defaultMessage: "Preset theme", description: "Theme settings: title of the preset theme card, also the accessible name of its preset choice group; keep it short" })} subtitle={intl.formatMessage({ id: "web.routes.theme_settings.choose_the_base_preset_for_your", defaultMessage: "Choose the base preset for your dashboard.", description: "Theme settings: subtitle under the preset theme title: choose the base preset for the dashboard" })}>
                <div className="space-y-2">
                  <Label className="text-xs">{intl.formatMessage({ id: "web.routes.theme_settings.choose_a_preset_theme", defaultMessage: "Choose a preset theme:", description: "Theme settings: label above the preset choices in the preset theme card" })}</Label>
                  <RadioGroup
                    aria-label={intl.formatMessage({ id: "web.routes.theme_settings.preset_theme", defaultMessage: "Preset theme", description: "Theme settings: title of the preset theme card, also the accessible name of its preset choice group; keep it short" })}
                    value={draft.themeType === "custom_colors" ? "custom_colors" : draft.presetId}
                    onChange={pickBase}
                    className="grid gap-3 sm:grid-cols-3"
                  >
                    {PRESETS.map((p) => (
                      <Radio key={p.id} value={p.id}>
                        <Radio.Content className="flex w-full items-start gap-3 rounded-lg border border-border px-4 py-3 transition-colors hover:bg-surface/60 data-[selected]:border-primary data-[selected]:bg-primary/5">
                          <Radio.Control className="mt-1 shrink-0" />
                          <div className="min-w-0 flex-1">
                            <div className="text-sm font-medium">{intl.formatMessage(p.name)}</div>
                            <div className="mt-2 flex items-center gap-2">
                              <span
                                aria-hidden
                                className="h-6 w-10 rounded-md border border-border"
                                style={{ backgroundColor: p.accent }}
                              />
                              <span
                                aria-hidden
                                className="h-6 w-10 rounded-md border border-border"
                                style={{ backgroundColor: p.surface }}
                              />
                            </div>
                            <div className="mt-1.5 text-xs text-muted">{intl.formatMessage(p.desc)}</div>
                          </div>
                        </Radio.Content>
                      </Radio>
                    ))}
                    <Radio value="custom_colors">
                      <Radio.Content className="flex h-full w-full items-start gap-3 rounded-lg border border-border px-4 py-3 transition-colors hover:bg-surface/60 data-[selected]:border-primary data-[selected]:bg-primary/5">
                        <Radio.Control className="mt-1 shrink-0" />
                        <div className="min-w-0 flex-1">
                          <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.theme_settings.custom_colors", defaultMessage: "Custom colors", description: "Theme settings: name of the custom colors option and the title of its card; keep it short" })}</div>
                          <div className="mt-2 flex items-center gap-2">
                            <span
                              aria-hidden
                              className="flex h-6 w-10 items-center justify-center rounded-md border border-border bg-surface"
                            >
                              <Palette className="h-4 w-4 text-muted" />
                            </span>
                            <span
                              aria-hidden
                              className="h-6 w-10 rounded-md border border-border"
                              style={{ backgroundColor: effectiveColors.surface }}
                            />
                          </div>
                          <div className="mt-1.5 text-xs text-muted">{intl.formatMessage({ id: "web.routes.theme_settings.your_saved_accent_and_surface", defaultMessage: "Your saved accent and surface", description: "Theme settings: caption under the colour preview, naming the accent and surface colors you saved" })}</div>
                        </div>
                      </Radio.Content>
                    </Radio>
                  </RadioGroup>
                </div>
                <p className="flex items-center gap-2 text-xs text-muted">
                  <Info className="h-3.5 w-3.5 shrink-0" />{intl.formatMessage({ id: "web.routes.theme_settings.your_custom_colors_and_css_are_kept_and", defaultMessage: "Your custom colors and CSS are kept and can be re-applied later.", description: "Theme settings: note under the preset choices: custom colors and CSS are kept and can be re-applied later" })}</p>
              </ThemeCard>

              {/* Appearance mode */}
              <ThemeCard title={intl.formatMessage({ id: "web.routes.theme_settings.appearance_mode", defaultMessage: "Appearance mode", description: "Theme settings: title of the appearance mode card, also its accessible group name; keep it short" })} subtitle={intl.formatMessage({ id: "web.routes.theme_settings.light_dark_or_follow_your_system", defaultMessage: "Light, dark, or follow your system preference.", description: "Theme settings: subtitle under the appearance mode title: light, dark, or follow the system preference" })}>
                <div
                  role="group"
                  aria-label={intl.formatMessage({ id: "web.routes.theme_settings.appearance_mode", defaultMessage: "Appearance mode", description: "Theme settings: title of the appearance mode card, also its accessible group name; keep it short" })}
                  aria-describedby={colorsActive ? "appearance-mode-note" : undefined}
                  className="inline-flex self-start rounded-lg border border-border bg-surface/40 p-1"
                >
                  {APPEARANCE_MODES.map(({ value, label }) => (
                    <button
                      key={value}
                      type="button"
                      aria-pressed={draft.appearanceMode === value}
                      disabled={colorsActive}
                      onClick={() => update({ appearanceMode: value })}
                      className={cn(
                        "rounded-md px-4 py-1.5 text-sm transition-colors",
                        "disabled:cursor-not-allowed disabled:opacity-50",
                        draft.appearanceMode === value
                          ? "bg-surface text-fg shadow-sm"
                          : "text-muted hover:text-fg",
                      )}
                    >
                      {intl.formatMessage(label)}
                    </button>
                  ))}
                </div>
                {colorsActive && (
                  <p id="appearance-mode-note" className="flex items-center gap-2 text-xs text-muted">
                    <Info className="h-3.5 w-3.5 shrink-0" />{intl.formatMessage({ id: "web.routes.theme_settings.set_by_your_surface_color", defaultMessage: "Set by your surface color", description: "Theme settings: note shown when custom colors are active, saying the appearance mode is set by the surface color" })}</p>
                )}
              </ThemeCard>

              {/* Custom colors (§3.2) */}
              <ThemeCard title={intl.formatMessage({ id: "web.routes.theme_settings.custom_colors", defaultMessage: "Custom colors", description: "Theme settings: name of the custom colors option and the title of its card; keep it short" })} subtitle={intl.formatMessage({ id: "web.routes.theme_settings.accent_color_and_surface_tone_for_the", defaultMessage: "Accent color and surface tone for the dashboard.", description: "Theme settings: subtitle under the custom colors title: accent color and surface tone for the dashboard" })}>
                <div className={cn("space-y-4 transition-opacity", !colorsActive && "opacity-50")}>
                  <div className="space-y-1.5">
                    <Label className="text-xs">{intl.formatMessage({ id: "web.routes.theme_settings.primary_accent", defaultMessage: "Primary accent", description: "Theme settings: label of the primary accent swatches, also their accessible group name; keep it short" })}</Label>
                    <div className="flex flex-wrap gap-2.5" role="group" aria-label={intl.formatMessage({ id: "web.routes.theme_settings.primary_accent", defaultMessage: "Primary accent", description: "Theme settings: label of the primary accent swatches, also their accessible group name; keep it short" })}>
                      {ACCENT_SWATCHES.map((s) => {
                        const selected = effectiveColors.accent.toLowerCase() === s.hex.toLowerCase();
                        return (
                          <button
                            key={s.hex}
                            type="button"
                            aria-label={intl.formatMessage(s.name)}
                            aria-pressed={selected}
                            disabled={!colorsActive}
                            onClick={() => pickAccent(s.hex)}
                            className={cn(
                              "h-8 w-8 rounded-md border border-border transition-shadow",
                              "disabled:cursor-not-allowed",
                              selected && "ring-2 ring-foreground ring-offset-2 ring-offset-background",
                            )}
                            style={{ backgroundColor: s.hex }}
                          />
                        );
                      })}
                    </div>
                    <div className="flex items-center gap-2 pt-1">
                      <input
                        type="color"
                        aria-label={intl.formatMessage({ id: "web.routes.theme_settings.primary_accent_color_picker", defaultMessage: "Primary accent color picker", description: "Theme settings: accessible name of the colour picker for the primary accent" })}
                        disabled={!colorsActive}
                        value={effectiveColors.accent.toLowerCase()}
                        onChange={(e) => pickAccent(e.target.value)}
                        className="h-8 w-8 cursor-pointer rounded-md border border-border bg-transparent p-0 disabled:cursor-not-allowed"
                      />
                      <input
                        type="text"
                        inputMode="text"
                        aria-label={intl.formatMessage({ id: "web.routes.theme_settings.primary_accent_hex_value", defaultMessage: "Primary accent hex value", description: "Theme settings: accessible name of the text field where the primary accent is typed as a hex value" })}
                        disabled={!colorsActive}
                        defaultValue={effectiveColors.accent}
                        key={effectiveColors.accent}
                        placeholder={intl.formatMessage({ id: "web.routes.theme_settings.rrggbb", defaultMessage: "#RRGGBB", description: "Theme settings: grey placeholder showing the hex colour format in the hex fields, usually left untranslated" })}
                        maxLength={7}
                        className="w-28 rounded-md border border-border bg-surface px-2 py-1 font-mono text-xs text-fg placeholder:text-muted focus:border-primary focus:outline-hidden disabled:cursor-not-allowed disabled:opacity-50"
                        onBlur={(e) => {
                          const v = e.target.value.trim();
                          if (HEX_COLOR_RE.test(v)) pickAccent(v);
                          else e.target.value = effectiveColors.accent;
                        }}
                        onKeyDown={(e) => {
                          if (e.key !== "Enter") return;
                          const v = e.currentTarget.value.trim();
                          if (HEX_COLOR_RE.test(v)) pickAccent(v);
                        }}
                      />
                    </div>
                  </div>

                  <div className="flex flex-wrap items-center gap-3">
                    <span
                      className="rounded-md px-3 py-1.5 text-sm font-medium"
                      style={{ backgroundColor: derived.accent, color: derived["accent-foreground"] }}
                    >{intl.formatMessage({ id: "web.routes.theme_settings.preview_button", defaultMessage: "Preview button", description: "Theme settings: sample button drawn in the chosen colors to preview contrast; its text is only a sample label" })}</span>
                    <span className={cn("text-xs", guardOk ? "text-muted" : "text-danger")}>{intl.formatMessage({ id: "web.routes.theme_settings.contrast_result", defaultMessage: "Contrast {ratio}:1 — {verdict} WCAG AA", description: "Theme settings: accent/surface contrast result; {ratio} is the contrast ratio to one decimal place and {verdict} is Passes or Fails" }, { ratio: accentSurfaceRatio.toFixed(1), verdict: guardOk ? intl.formatMessage({ id: "web.routes.theme_settings.verdict_passes", defaultMessage: "Passes", description: "Theme settings: contrast verdict when the WCAG AA check passes" }) : intl.formatMessage({ id: "web.routes.theme_settings.verdict_fails", defaultMessage: "Fails", description: "Theme settings: contrast verdict when the WCAG AA check fails" }) })}</span>
                  </div>

                  <div className="space-y-2">
                    <Label className="text-xs">{intl.formatMessage({ id: "web.routes.theme_settings.surface_tone", defaultMessage: "Surface tone", description: "Theme settings: label of the surface tone choices, also their accessible group name; keep it short" })}</Label>
                    <RadioGroup
                      aria-label={intl.formatMessage({ id: "web.routes.theme_settings.surface_tone", defaultMessage: "Surface tone", description: "Theme settings: label of the surface tone choices, also their accessible group name; keep it short" })}
                      value={
                        SURFACE_TONES.find((t) => t.hex.toLowerCase() === effectiveColors.surface.toLowerCase())
                          ?.hex ?? effectiveColors.surface
                      }
                      onChange={(hex) => pickSurface(hex)}
                      isDisabled={!colorsActive}
                      orientation="horizontal"
                      className="flex flex-wrap gap-2"
                    >
                      {SURFACE_TONES.map((t) => (
                        <Radio key={t.hex} value={t.hex}>
                          <Radio.Content className="flex items-center gap-2 rounded-md border border-border px-3 py-2 text-sm transition-colors hover:bg-surface/60 data-[selected]:border-primary data-[selected]:bg-primary/5">
                            <Radio.Control />
                            <span
                              aria-hidden
                              className="h-4 w-4 rounded-sm border border-border"
                              style={{ backgroundColor: t.hex }}
                            />
                            <span>{intl.formatMessage(t.name)}</span>
                          </Radio.Content>
                        </Radio>
                      ))}
                    </RadioGroup>
                    <div className="flex items-center gap-2 pt-1">
                      <input
                        type="color"
                        aria-label={intl.formatMessage({ id: "web.routes.theme_settings.surface_tone_color_picker", defaultMessage: "Surface tone color picker", description: "Theme settings: accessible name of the colour picker for the surface tone" })}
                        disabled={!colorsActive}
                        value={effectiveColors.surface.toLowerCase()}
                        onChange={(e) => pickSurface(e.target.value)}
                        className="h-8 w-8 cursor-pointer rounded-md border border-border bg-transparent p-0 disabled:cursor-not-allowed"
                      />
                      <input
                        type="text"
                        inputMode="text"
                        aria-label={intl.formatMessage({ id: "web.routes.theme_settings.surface_tone_hex_value", defaultMessage: "Surface tone hex value", description: "Theme settings: accessible name of the text field where the surface tone is typed as a hex value" })}
                        disabled={!colorsActive}
                        defaultValue={effectiveColors.surface}
                        key={effectiveColors.surface}
                        placeholder={intl.formatMessage({ id: "web.routes.theme_settings.rrggbb", defaultMessage: "#RRGGBB", description: "Theme settings: grey placeholder showing the hex colour format in the hex fields, usually left untranslated" })}
                        maxLength={7}
                        className="w-28 rounded-md border border-border bg-surface px-2 py-1 font-mono text-xs text-fg placeholder:text-muted focus:border-primary focus:outline-hidden disabled:cursor-not-allowed disabled:opacity-50"
                        onBlur={(e) => {
                          const v = e.target.value.trim();
                          if (HEX_COLOR_RE.test(v)) pickSurface(v);
                          else e.target.value = effectiveColors.surface;
                        }}
                        onKeyDown={(e) => {
                          if (e.key !== "Enter") return;
                          const v = e.currentTarget.value.trim();
                          if (HEX_COLOR_RE.test(v)) pickSurface(v);
                        }}
                      />
                    </div>
                  </div>
                </div>

                {colorsActive && !guardOk && (
                  <div role="alert" className="flex gap-3 rounded-md border border-warning/40 bg-warning/10 p-3">
                    <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-warning-soft-foreground" />
                    <div className="text-xs">
                      <div className="mb-1 font-medium text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.theme_settings.contrast_guard", defaultMessage: "Contrast guard", description: "Theme settings: heading of the warning shown when the accent and surface colors fail the WCAG AA contrast check" })}</div>
                      <p className="text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.theme_settings.the_selected_accent_and_surface", defaultMessage: "The selected accent and surface combination fails WCAG AA ({toFixed}:1). Choose a darker accent or a darker surface tone.", description: "Theme settings: sentence with toFixed in p" }, { toFixed: accentSurfaceRatio.toFixed(1) })}</p>
                    </div>
                  </div>
                )}
              </ThemeCard>

              {/* Custom CSS overlay (§3.3) */}
              <ThemeCard title={intl.formatMessage({ id: "web.routes.theme_settings.custom_css_overlay", defaultMessage: "Custom CSS overlay", description: "Theme settings: title of the custom CSS overlay card; keep it short" })} subtitle={intl.formatMessage({ id: "web.routes.theme_settings.a_stylesheet_layered_on_top_of_the", defaultMessage: "A stylesheet layered on top of the active base theme.", description: "Theme settings: subtitle under the custom CSS overlay title: a stylesheet layered on top of the active base theme" })}>
                <div className="flex items-center justify-between gap-4">
                  <Label className="text-sm">{intl.formatMessage({ id: "web.routes.theme_settings.enable_custom_css_overlay", defaultMessage: "Enable custom CSS overlay", description: "Theme settings: label and accessible name of the switch that turns the custom CSS overlay on" })}</Label>
                  <Switch
                    aria-label={intl.formatMessage({ id: "web.routes.theme_settings.enable_custom_css_overlay", defaultMessage: "Enable custom CSS overlay", description: "Theme settings: label and accessible name of the switch that turns the custom CSS overlay on" })}
                    isSelected={draft.customCssEnabled}
                    onChange={(v) => update({ customCssEnabled: v })}
                  >
                    <Switch.Content>
                      <Switch.Control>
                        <Switch.Thumb />
                      </Switch.Control>
                    </Switch.Content>
                  </Switch>
                </div>
                <p className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.theme_settings.your_rules_are_applied_on_top_of_the", defaultMessage: "Your rules are applied on top of the active base theme and win over it; elements you don't target follow the base theme.", description: "Theme settings: note under the overlay switch: your rules are applied over the base theme and win; elements you do not target follow the base theme" })}</p>
                <div className="space-y-1.5">
                  <label className="block text-xs text-muted" htmlFor="theme-custom-css">{intl.formatMessage({ id: "web.routes.theme_settings.custom_css", defaultMessage: "Custom CSS", description: "Theme settings: label of the custom CSS text area" })}</label>
                  <textarea
                    id="theme-custom-css"
                    className="min-h-[140px] w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-[13px] text-fg placeholder:text-muted focus:border-primary focus:outline-hidden"
                    value={cssDraft}
                    onChange={(e) => update({ customCss: e.target.value })}
                    placeholder={CSS_PLACEHOLDER}
                    spellCheck={false}
                  />
                  <div className="flex items-center justify-between">
                    <span className="text-xs text-muted">
                      {intl.formatMessage({ id: "web.routes.theme_settings.css_bytes_of_limit", defaultMessage: "{used} / {max}", description: "Theme settings: custom CSS byte counter; {used} is the bytes in use and {max} the byte limit, both already formatted" }, { used: fmt.number(cssBytes), max: fmt.number(MAX_CUSTOM_CSS_LEN) })}
                    </span>
                  </div>
                </div>
                {cssError && (
                  <p role="alert" className="flex items-start gap-2 text-xs text-danger">
                    <CircleAlert className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
                    {cssError}
                  </p>
                )}
                <div className="flex gap-3 rounded-md border border-warning/40 bg-warning/10 p-3">
                  <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-warning-soft-foreground" />
                  <div className="text-xs">
                    <div className="mb-1 font-medium text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.theme_settings.custom_css_recovery", defaultMessage: "Custom CSS recovery", description: "Theme settings: heading of the warning box about recovering the interface if custom CSS makes it unusable" })}</div>
                    <p className="text-warning-soft-foreground">{intl.formatMessage({ id: "web.routes.theme_settings.custom_css_modifies_application", defaultMessage: "Custom CSS modifies application appearance directly. If the interface becomes unusable, recover via the", description: "Theme settings: opening sentence of the custom CSS warning, ending with the words recover via the, followed by the safe-mode parameter" })} <code className="font-mono">{intl.formatMessage({ id: "web.routes.theme_settings.safe_mode_1", defaultMessage: "?safe-mode=1", description: "Theme settings: URL parameter shown in monospace in the custom CSS warning (technical term, usually left untranslated)" })}</code> {intl.formatMessage({ id: "web.routes.theme_settings.url_parameter_the_safe_mode_keyboard", defaultMessage: "URL parameter, the safe-mode keyboard shortcut, or the ‘Sign in with safe mode’ link on the login page.", description: "Theme settings: end of the custom CSS warning naming the recovery options: the URL parameter, the safe-mode keyboard shortcut, or the sign-in link on the login page" })}</p>
                  </div>
                </div>
              </ThemeCard>

              {/* Export / Import (§3.4) */}
              <ThemeCard
                title={intl.formatMessage({ id: "web.routes.theme_settings.export_import", defaultMessage: "Export / Import", description: "Theme settings: title of the export and import card; keep it short" })}
                subtitle={intl.formatMessage({ id: "web.routes.theme_settings.copy_or_download_your_saved_theme", defaultMessage: "Copy or download your saved theme profile, or apply a gameplane-theme v1 document.", description: "Theme settings: subtitle under the export and import title: copy or download the saved theme, or apply a theme document" })}
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Button variant="outline" size="sm" onPress={() => void copyExport()}>
                    <Copy className="mr-1.5 h-4 w-4" />{intl.formatMessage({ id: "web.routes.theme_settings.copy_to_clipboard", defaultMessage: "Copy to clipboard", description: "Theme settings: button that copies the theme export to the clipboard; keep it short" })}</Button>
                  <Button variant="outline" size="sm" onPress={downloadExport}>
                    <Download className="mr-1.5 h-4 w-4" />{intl.formatMessage({ id: "web.routes.theme_settings.download_gameplane_theme_json", defaultMessage: "Download gameplane-theme.json", description: "Theme settings: button that downloads the theme export as a gameplane-theme.json file; keep it short, the file name is usually left untranslated" })}</Button>
                </div>
                {copyNote && (
                  <p className="text-xs text-muted" role="status">
                    {copyNote}
                  </p>
                )}

                <div className="space-y-1.5">
                  <label className="block text-xs text-muted" htmlFor="theme-import-json">{intl.formatMessage({ id: "web.routes.theme_settings.paste_theme_json", defaultMessage: "Paste theme JSON", description: "Theme settings: label of the text area where a theme document is pasted to import it" })}</label>
                  <textarea
                    id="theme-import-json"
                    className="min-h-[96px] w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-[13px] text-fg placeholder:text-muted focus:border-primary focus:outline-hidden"
                    value={importText}
                    onChange={(e) => setImportText(e.target.value)}
                    placeholder={intl.formatMessage({ id: "web.routes.theme_settings.paste_a_gameplane_theme_v1_document", defaultMessage: "Paste a gameplane-theme v1 document…", description: "Theme settings: grey placeholder inside the paste area showing that a gameplane-theme v1 document goes here; format name, usually left untranslated" })}
                    spellCheck={false}
                  />
                  <p className="text-[11px] text-muted">{intl.formatMessage({ id: "web.routes.theme_settings.accepts_gameplane_theme_v1_documents", defaultMessage: "Accepts gameplane-theme v1 documents.", description: "Theme settings: hint under the paste area saying it accepts gameplane-theme v1 documents" })}</p>
                </div>

                <div className="flex items-center gap-3">
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept=".json,application/json"
                    className="hidden"
                    aria-label={intl.formatMessage({ id: "web.routes.theme_settings.theme_export_file", defaultMessage: "Theme export file", description: "Theme settings: accessible name of the hidden file picker that chooses a theme JSON file" })}
                    onChange={(e) => void onFilePicked(e)}
                  />
                  <Button variant="outline" size="sm" onPress={() => fileInputRef.current?.click()}>
                    <Upload className="mr-1.5 h-4 w-4" />{intl.formatMessage({ id: "web.routes.theme_settings.choose_file", defaultMessage: "Choose file…", description: "Theme settings: button that opens the file picker to choose a theme file; keep it short" })}</Button>
                  <span className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.theme_settings.accepts_gameplane_theme_v1_json", defaultMessage: "Accepts gameplane-theme v1 (.json)", description: "Theme settings: hint beside the choose-file button saying the file must be a gameplane-theme v1 JSON document" })}</span>
                </div>

                {importClientError && (
                  <p role="alert" className="flex items-start gap-2 text-xs text-danger">
                    <CircleAlert className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
                    {importClientError}
                  </p>
                )}
                {lastError?.where === "import" && (
                  <p role="alert" className="flex items-start gap-2 text-xs text-danger">
                    <CircleAlert className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
                    {lastError.message}
                  </p>
                )}

                {importPreview && (
                  <div className="space-y-2 rounded-md border border-border bg-surface/40 p-4">
                    <div className="text-sm font-medium">{intl.formatMessage({ id: "web.routes.theme_settings.import_preview", defaultMessage: "Import preview", description: "Theme settings: heading of the preview box that shows what an import will apply" })}</div>
                    <div className="flex items-center gap-2 text-sm">
                      <span
                        aria-hidden
                        className="h-5 w-5 rounded-sm border border-border"
                        style={{
                          backgroundColor:
                            importPreview.preferences.customColors?.accent ??
                            PRESETS.find((p) => p.id === importPreview.preferences.presetId)?.accent,
                        }}
                      />
                      <span>{intl.formatMessage({ id: "web.routes.theme_settings.preset", defaultMessage: "Preset:", description: "Theme settings: label before the preset name in the import preview; the preset name follows it" })} {presetLabel(intl, importPreview.preferences.presetId)}
                      </span>
                    </div>
                    <div className="text-xs text-muted">{intl.formatMessage({ id: "web.routes.theme_settings.custom_css_2", defaultMessage: "Custom CSS:", description: "Theme settings: label before the custom CSS summary in the import preview; the size or none follows it" })} {importPreview.preferences.customCss
                        ? intl.formatMessage({ id: "web.routes.theme_settings.import_css_bytes", defaultMessage: "{size} bytes", description: "Theme settings: import preview size of the imported custom CSS; {size} is the formatted byte count" }, { size: fmt.number(utf8Len(importPreview.preferences.customCss)) })
                        : intl.formatMessage({ id: "web.routes.theme_settings.import_css_none", defaultMessage: "none", description: "Theme settings: import preview value when the imported document has no custom CSS" })}
                    </div>
                    <div className="flex justify-end">
                      <Button
                        variant="primary"
                        size="sm"
                        isDisabled={isUpdating}
                        onPress={applyImport}
                      >{intl.formatMessage({ id: "web.routes.theme_settings.apply_import", defaultMessage: "Apply import", description: "Theme settings: button that applies the imported theme; keep it short" })}</Button>
                    </div>
                  </div>
                )}

                <p className="flex items-center gap-2 text-xs text-muted">
                  <Info className="h-3.5 w-3.5 shrink-0" />{intl.formatMessage({ id: "web.routes.theme_settings.sharing_galleries_and_organization", defaultMessage: "Sharing galleries and organization-enforced themes are not part of this feature.", description: "Theme settings: note at the bottom saying sharing galleries and organisation-enforced themes are not part of this feature" })}</p>
              </ThemeCard>

              {/* Actions row: destructive Reset on the left, Save on the right. */}
              <div className="flex items-center justify-between gap-3">
                <Button variant="danger" onPress={() => { setResetError(null); setConfirmingReset(true); }}>
                  <RotateCcw className="mr-1.5 h-4 w-4" />{intl.formatMessage({ id: "web.routes.theme_settings.reset_to_defaults", defaultMessage: "Reset to Defaults", description: "Theme settings: button that opens the reset-to-defaults confirmation; keep it short" })}</Button>
                <div className="flex items-center gap-3">
                  {lastError?.where === "save" && (
                    <span role="alert" className="text-xs text-danger">{lastError.message}</span>
                  )}
                  {resetError && <span className="text-xs text-danger">{resetError}</span>}
                  {lastDone?.where === "save" && <span className="text-xs text-success">{intl.formatMessage({ id: "web.routes.theme_settings.saved", defaultMessage: "Saved", description: "Theme settings: status text beside the save button confirming the theme was saved; keep it short" })}</span>}
                  {lastDone?.where === "import" && (
                    <span className="text-xs text-success">{intl.formatMessage({ id: "web.routes.theme_settings.import_applied", defaultMessage: "Import applied", description: "Theme settings: status text confirming the imported theme was applied; keep it short" })}</span>
                  )}
                  <Button
                    variant="primary"
                    isDisabled={!dirty || isUpdating || cssError !== null}
                    onPress={save}
                  >
                    {isUpdating ? intl.formatMessage({ id: "web.routes.theme_settings.saving", defaultMessage: "Saving…", description: "Theme settings: Save button label while the save request is in flight" }) : intl.formatMessage({ id: "web.routes.theme_settings.save", defaultMessage: "Save", description: "Theme settings: button label that saves the theme draft" })}
                  </Button>
                </div>
              </div>
            </>
          )}
        </div>
      </div>

      <ConfirmDialog
        open={confirmingReset}
        onOpenChange={(open) => {
          if (!open) setConfirmingReset(false);
        }}
        title={intl.formatMessage({ id: "web.routes.theme_settings.reset_to_defaults_2", defaultMessage: "Reset to Defaults?", description: "Theme settings: title of the reset-to-defaults confirmation dialog" })}
        description={intl.formatMessage({ id: "web.routes.theme_settings.this_deletes_your_custom_colors_and", defaultMessage: "This deletes your custom colors and custom CSS and restores the selected preset. Continue?", description: "Theme settings: description in the reset confirmation: deletes custom colors and custom CSS, restores the selected preset, and asks whether to continue" })}
        confirmLabel={intl.formatMessage({ id: "web.routes.theme_settings.reset", defaultMessage: "Reset", description: "Theme settings: confirm button of the reset-to-defaults dialog; keep it short" })}
        destructive
        busy={reset.isPending}
        onConfirm={() => reset.mutate()}
      />
    </div>
  );
}
