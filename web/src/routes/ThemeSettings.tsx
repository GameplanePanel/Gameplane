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
import { cn } from "@/lib/utils";
import { errorText } from "@/lib/errors";
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

// Dark-mode accent/surface pair per preset, per contracts/theme-ui.md §3.1
// and the lWvcv swatch fills.
const PRESETS: Array<{
  id: ThemePresetId;
  name: string;
  desc: string;
  accent: string;
  surface: string;
}> = [
  { id: "pink",   name: "Modern Pink",   desc: "Modern HeroUI brand", accent: "#FF4FA3", surface: "#1C1A20" },
  { id: "legacy", name: "Legacy Orange", desc: "Original Gameplane",  accent: "#F97316", surface: "#171717" },
];

// Accent palette swatches (lWvcv ccSwatch fills) and surface tone options
// (contracts/theme-ui.md §3.2). The three dark surfaces keep the derived
// base in dark mode; Crisp Light flips it to light.
const ACCENT_SWATCHES: Array<{ name: string; hex: string }> = [
  { name: "Blue",    hex: "#3B82F6" },
  { name: "Emerald", hex: "#10B981" },
  { name: "Purple",  hex: "#8B5CF6" },
  { name: "Amber",   hex: "#F59E0B" },
  { name: "Cyan",    hex: "#06B6D4" },
  { name: "Rose",    hex: "#F43F5E" },
  { name: "Orange",  hex: "#F97316" },
];

const SURFACE_TONES: Array<{ name: string; hex: string }> = [
  { name: "Dark Slate",  hex: "#1E293B" },
  { name: "Midnight",    hex: "#0F172A" },
  { name: "Charcoal",    hex: "#171717" },
  { name: "Crisp Light", hex: "#F8FAFC" },
];

const APPEARANCE_MODES: Array<{ value: AppearanceMode; label: string }> = [
  { value: "light",  label: "Light" },
  { value: "dark",   label: "Dark" },
  { value: "system", label: "System" },
];

const CSS_PLACEHOLDER = "/* Example: customize typography or borders */\n:root {\n  --radius: 14px;\n}";

// utf8Len mirrors the sanitizer's byte accounting for the editor counter.
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

function editorErrorMessage(css: string, result: Extract<SanitizeResult, { ok: false }>): string {
  const err = result.error;
  if (err.includes("@import")) {
    const hit = findLine(css, /@import/i);
    return hit
      ? `External resource loads are not allowed (line ${hit.line}: \`${hit.text}\`). Paste the content inline instead.`
      : "External resource loads are not allowed (@import). Paste the content inline instead.";
  }
  if (err.includes("external url()")) {
    const hit = findLine(css, /url\(\s*["']?(?:https?:)?\/\//i);
    return hit
      ? `External resource loads are not allowed (line ${hit.line}: \`${hit.text}\`). Paste the content inline instead.`
      : "External resource loads are not allowed (external url()). Paste the content inline instead.";
  }
  if (err.includes("unbalanced braces")) {
    return "Syntax error: unbalanced braces — every '{' must have a matching '}'.";
  }
  if (err.includes("maximum length")) {
    return `Custom CSS exceeds the ${MAX_CUSTOM_CSS_LEN.toLocaleString("en-US")}-byte limit.`;
  }
  if (err.includes("HTML delimiter")) {
    return "HTML <style> and <script> tags are not allowed in custom CSS.";
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
  const cssError = cssResult.ok ? null : editorErrorMessage(cssDraft, cssResult);

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
      setCopyNote("Copied to clipboard");
    } catch {
      setCopyNote("Clipboard unavailable — select the text and copy it manually.");
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
    () => (importText.trim() === "" ? null : validateThemeExport(importText)),
    [importText],
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
    onError: (err) => setResetError(errorText(err, "Reset failed")),
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
        title="Theme & Appearance"
        description="Personal theme presets, colors, custom CSS, and theme portability."
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
              <Card.Content className="text-sm text-muted">Loading theme preferences…</Card.Content>
            </Card>
          ) : (
            <>
              {/* Preset theme (contracts/theme-ui.md §3.1) */}
              <ThemeCard title="Preset theme" subtitle="Choose the base preset for your dashboard.">
                <div className="space-y-2">
                  <Label className="text-xs">Choose a preset theme:</Label>
                  <RadioGroup
                    aria-label="Preset theme"
                    value={draft.themeType === "custom_colors" ? "custom_colors" : draft.presetId}
                    onChange={pickBase}
                    className="grid gap-3 sm:grid-cols-3"
                  >
                    {PRESETS.map((p) => (
                      <Radio key={p.id} value={p.id}>
                        <Radio.Content className="flex w-full items-start gap-3 rounded-lg border border-border px-4 py-3 transition-colors hover:bg-surface/60 data-[selected]:border-primary data-[selected]:bg-primary/5">
                          <Radio.Control className="mt-1 shrink-0" />
                          <div className="min-w-0 flex-1">
                            <div className="text-sm font-medium">{p.name}</div>
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
                            <div className="mt-1.5 text-xs text-muted">{p.desc}</div>
                          </div>
                        </Radio.Content>
                      </Radio>
                    ))}
                    <Radio value="custom_colors">
                      <Radio.Content className="flex h-full w-full items-start gap-3 rounded-lg border border-border px-4 py-3 transition-colors hover:bg-surface/60 data-[selected]:border-primary data-[selected]:bg-primary/5">
                        <Radio.Control className="mt-1 shrink-0" />
                        <div className="min-w-0 flex-1">
                          <div className="text-sm font-medium">Custom colors</div>
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
                          <div className="mt-1.5 text-xs text-muted">Your saved accent and surface</div>
                        </div>
                      </Radio.Content>
                    </Radio>
                  </RadioGroup>
                </div>
                <p className="flex items-center gap-2 text-xs text-muted">
                  <Info className="h-3.5 w-3.5 shrink-0" />
                  Your custom colors and CSS are kept and can be re-applied later.
                </p>
              </ThemeCard>

              {/* Appearance mode */}
              <ThemeCard title="Appearance mode" subtitle="Light, dark, or follow your system preference.">
                <div
                  role="group"
                  aria-label="Appearance mode"
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
                      {label}
                    </button>
                  ))}
                </div>
                {colorsActive && (
                  <p id="appearance-mode-note" className="flex items-center gap-2 text-xs text-muted">
                    <Info className="h-3.5 w-3.5 shrink-0" />
                    Set by your surface color
                  </p>
                )}
              </ThemeCard>

              {/* Custom colors (§3.2) */}
              <ThemeCard title="Custom colors" subtitle="Accent color and surface tone for the dashboard.">
                <div className={cn("space-y-4 transition-opacity", !colorsActive && "opacity-50")}>
                  <div className="space-y-1.5">
                    <Label className="text-xs">Primary accent</Label>
                    <div className="flex flex-wrap gap-2.5" role="group" aria-label="Primary accent">
                      {ACCENT_SWATCHES.map((s) => {
                        const selected = effectiveColors.accent.toLowerCase() === s.hex.toLowerCase();
                        return (
                          <button
                            key={s.hex}
                            type="button"
                            aria-label={s.name}
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
                        aria-label="Primary accent color picker"
                        disabled={!colorsActive}
                        value={effectiveColors.accent.toLowerCase()}
                        onChange={(e) => pickAccent(e.target.value)}
                        className="h-8 w-8 cursor-pointer rounded-md border border-border bg-transparent p-0 disabled:cursor-not-allowed"
                      />
                      <input
                        type="text"
                        inputMode="text"
                        aria-label="Primary accent hex value"
                        disabled={!colorsActive}
                        defaultValue={effectiveColors.accent}
                        key={effectiveColors.accent}
                        placeholder="#RRGGBB"
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
                    >
                      Preview button
                    </span>
                    <span className={cn("text-xs", guardOk ? "text-muted" : "text-danger")}>
                      Contrast {accentSurfaceRatio.toFixed(1)}:1 — {guardOk ? "Passes" : "Fails"} WCAG AA
                    </span>
                  </div>

                  <div className="space-y-2">
                    <Label className="text-xs">Surface tone</Label>
                    <RadioGroup
                      aria-label="Surface tone"
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
                            <span>{t.name}</span>
                          </Radio.Content>
                        </Radio>
                      ))}
                    </RadioGroup>
                    <div className="flex items-center gap-2 pt-1">
                      <input
                        type="color"
                        aria-label="Surface tone color picker"
                        disabled={!colorsActive}
                        value={effectiveColors.surface.toLowerCase()}
                        onChange={(e) => pickSurface(e.target.value)}
                        className="h-8 w-8 cursor-pointer rounded-md border border-border bg-transparent p-0 disabled:cursor-not-allowed"
                      />
                      <input
                        type="text"
                        inputMode="text"
                        aria-label="Surface tone hex value"
                        disabled={!colorsActive}
                        defaultValue={effectiveColors.surface}
                        key={effectiveColors.surface}
                        placeholder="#RRGGBB"
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
                      <div className="mb-1 font-medium text-warning-soft-foreground">Contrast guard</div>
                      <p className="text-warning-soft-foreground">
                        The selected accent and surface combination fails WCAG AA (
                        {accentSurfaceRatio.toFixed(1)}:1). Choose a darker accent or a darker surface tone.
                      </p>
                    </div>
                  </div>
                )}
              </ThemeCard>

              {/* Custom CSS overlay (§3.3) */}
              <ThemeCard title="Custom CSS overlay" subtitle="A stylesheet layered on top of the active base theme.">
                <div className="flex items-center justify-between gap-4">
                  <Label className="text-sm">Enable custom CSS overlay</Label>
                  <Switch
                    aria-label="Enable custom CSS overlay"
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
                <p className="text-xs text-muted">
                  Your rules are applied on top of the active base theme and win over it; elements you
                  don&apos;t target follow the base theme.
                </p>
                <div className="space-y-1.5">
                  <label className="block text-xs text-muted" htmlFor="theme-custom-css">
                    Custom CSS
                  </label>
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
                      {cssBytes.toLocaleString("en-US")} / {MAX_CUSTOM_CSS_LEN.toLocaleString("en-US")}
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
                    <div className="mb-1 font-medium text-warning-soft-foreground">Custom CSS recovery</div>
                    <p className="text-warning-soft-foreground">
                      Custom CSS modifies application appearance directly. If the interface becomes
                      unusable, recover via the <code className="font-mono">?safe-mode=1</code> URL
                      parameter, the safe-mode keyboard shortcut, or the &lsquo;Sign in with safe
                      mode&rsquo; link on the login page.
                    </p>
                  </div>
                </div>
              </ThemeCard>

              {/* Export / Import (§3.4) */}
              <ThemeCard
                title="Export / Import"
                subtitle="Copy or download your saved theme profile, or apply a gameplane-theme v1 document."
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Button variant="outline" size="sm" onPress={() => void copyExport()}>
                    <Copy className="mr-1.5 h-4 w-4" />
                    Copy to clipboard
                  </Button>
                  <Button variant="outline" size="sm" onPress={downloadExport}>
                    <Download className="mr-1.5 h-4 w-4" />
                    Download gameplane-theme.json
                  </Button>
                </div>
                {copyNote && (
                  <p className="text-xs text-muted" role="status">
                    {copyNote}
                  </p>
                )}

                <div className="space-y-1.5">
                  <label className="block text-xs text-muted" htmlFor="theme-import-json">
                    Paste theme JSON
                  </label>
                  <textarea
                    id="theme-import-json"
                    className="min-h-[96px] w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-[13px] text-fg placeholder:text-muted focus:border-primary focus:outline-hidden"
                    value={importText}
                    onChange={(e) => setImportText(e.target.value)}
                    placeholder="Paste a gameplane-theme v1 document…"
                    spellCheck={false}
                  />
                  <p className="text-[11px] text-muted">Accepts gameplane-theme v1 documents.</p>
                </div>

                <div className="flex items-center gap-3">
                  <input
                    ref={fileInputRef}
                    type="file"
                    accept=".json,application/json"
                    className="hidden"
                    aria-label="Theme export file"
                    onChange={(e) => void onFilePicked(e)}
                  />
                  <Button variant="outline" size="sm" onPress={() => fileInputRef.current?.click()}>
                    <Upload className="mr-1.5 h-4 w-4" />
                    Choose file…
                  </Button>
                  <span className="text-xs text-muted">Accepts gameplane-theme v1 (.json)</span>
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
                    <div className="text-sm font-medium">Import preview</div>
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
                      <span>
                        Preset:{" "}
                        {PRESETS.find((p) => p.id === importPreview.preferences.presetId)?.name}
                      </span>
                    </div>
                    <div className="text-xs text-muted">
                      Custom CSS:{" "}
                      {importPreview.preferences.customCss
                        ? `${utf8Len(importPreview.preferences.customCss).toLocaleString("en-US")} bytes`
                        : "none"}
                    </div>
                    <div className="flex justify-end">
                      <Button
                        variant="primary"
                        size="sm"
                        isDisabled={isUpdating}
                        onPress={applyImport}
                      >
                        Apply import
                      </Button>
                    </div>
                  </div>
                )}

                <p className="flex items-center gap-2 text-xs text-muted">
                  <Info className="h-3.5 w-3.5 shrink-0" />
                  Sharing galleries and organization-enforced themes are not part of this feature.
                </p>
              </ThemeCard>

              {/* Actions row: destructive Reset on the left, Save on the right. */}
              <div className="flex items-center justify-between gap-3">
                <Button variant="danger" onPress={() => { setResetError(null); setConfirmingReset(true); }}>
                  <RotateCcw className="mr-1.5 h-4 w-4" />
                  Reset to Defaults
                </Button>
                <div className="flex items-center gap-3">
                  {lastError?.where === "save" && (
                    <span role="alert" className="text-xs text-danger">{lastError.message}</span>
                  )}
                  {resetError && <span className="text-xs text-danger">{resetError}</span>}
                  {lastDone?.where === "save" && <span className="text-xs text-success">Saved</span>}
                  {lastDone?.where === "import" && (
                    <span className="text-xs text-success">Import applied</span>
                  )}
                  <Button
                    variant="primary"
                    isDisabled={!dirty || isUpdating || cssError !== null}
                    onPress={save}
                  >
                    {isUpdating ? "Saving…" : "Save"}
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
        title="Reset to Defaults?"
        description="This deletes your custom colors and custom CSS and restores the selected preset. Continue?"
        confirmLabel="Reset"
        destructive
        busy={reset.isPending}
        onConfirm={() => reset.mutate()}
      />
    </div>
  );
}
