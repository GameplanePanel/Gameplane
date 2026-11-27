import { useEffect, useState } from "react";
import { useLocation } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import type { MessageDescriptor } from "react-intl";
import { Eye } from "lucide-react";
import { APIError } from "@/lib/api";
import { Themes, Users } from "@/lib/endpoints";
import { useOptionalIntl } from "@/lib/i18n/format";
import {
  DEFAULT_THEME_PREFERENCES,
  readThemePreferences,
} from "@/lib/useThemePreferences";
import {
  dropThemePreview,
  exitThemePreview,
  isThemePagePath,
  useThemePreview,
  type ThemePreviewTheme,
} from "@/lib/useThemePreview";
import { themePreviewBannerMessages } from "@/components/themes/messages";

// Keep saves the previewed theme: install and select it when it is not
// installed yet, otherwise select the installed copy. Either way the server
// copy is refetched and becomes the applied theme.
async function keepTheme(theme: ThemePreviewTheme): Promise<void> {
  if (theme.installedId === null) {
    await Themes.install({ catalogId: theme.catalogId, select: true });
    return;
  }
  const prefs = readThemePreferences() ?? DEFAULT_THEME_PREFERENCES;
  await Users.updatePreferences({
    themeType: "installed",
    presetId: prefs.presetId,
    appearanceMode: prefs.appearanceMode,
    customCssEnabled: prefs.customCssEnabled,
    installedThemeId: theme.installedId,
  });
}

/**
 * Preview banner (design: "Theme preview — banner"). Mounted at the top of the
 * app shell. It shows while a theme is previewed, and the preview ends on
 * Exit, on leaving the theme pages, or on unmount. Nothing is written until Keep.
 */
export function ThemePreviewBanner() {
  const intl = useOptionalIntl();
  const location = useLocation();
  const { preview } = useThemePreview();
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);

  // Restore the saved theme when the user leaves the theme pages, and on unmount.
  useEffect(() => {
    if (preview !== null && !isThemePagePath(location.pathname)) exitThemePreview();
  }, [preview, location.pathname]);
  useEffect(() => () => exitThemePreview(), []);

  const keep = useMutation({
    mutationFn: keepTheme,
    onSuccess: () => {
      setError(null);
      dropThemePreview();
      void queryClient.invalidateQueries({ queryKey: ["me"] });
      void queryClient.invalidateQueries({ queryKey: ["installed-themes"] });
      void queryClient.invalidateQueries({ queryKey: ["theme-catalog"] });
    },
    onError: (err: unknown) => {
      setError(err instanceof APIError ? err.body || err.message : err instanceof Error ? err.message : String(err));
    },
  });

  if (preview === null) return null;
  const previewDescriptor: MessageDescriptor = themePreviewBannerMessages.previewing;
  const previewValues: Record<string, string> = { name: preview.displayName };

  return (
    <div className="sticky top-0 z-50 flex w-full items-center justify-between gap-3 border-b border-accent bg-surface px-4 py-2.5">
      <div className="flex min-w-0 items-center gap-2 text-sm text-accent">
        <Eye aria-hidden="true" className="h-4 w-4 shrink-0" />
        <span className="truncate font-medium">
          {intl.formatMessage(previewDescriptor, previewValues)}
        </span>
        {error ? (
          <span role="alert" className="truncate text-danger">
            {error}
          </span>
        ) : null}
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <button
          type="button"
          disabled={keep.isPending}
          onClick={() => {
            setError(null);
            keep.mutate(preview);
          }}
          className="rounded-full bg-accent px-3.5 py-1.5 text-sm font-medium text-accent-foreground disabled:opacity-60"
        >
          {intl.formatMessage(themePreviewBannerMessages.keep)}
        </button>
        <button
          type="button"
          onClick={() => {
            setError(null);
            exitThemePreview();
          }}
          className="rounded-full border border-border px-3.5 py-1.5 text-sm font-medium text-foreground"
        >
          {intl.formatMessage(themePreviewBannerMessages.exit)}
        </button>
      </div>
    </div>
  );
}
