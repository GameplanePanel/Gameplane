import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Button } from "@heroui/react";
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
  useThemePreview,
  type ThemePreviewTheme,
} from "@/lib/useThemePreview";
import { themePreviewBannerMessages } from "@/components/themes/messages";

// Keep saves the previewed theme: install and select it when it is not
// installed yet, otherwise select the installed copy (updated first when it is
// older than the previewed version). Either way the server copy is refetched
// and becomes the applied theme.
async function keepTheme(theme: ThemePreviewTheme): Promise<void> {
  if (theme.installedId === null) {
    await Themes.install({ catalogId: theme.catalogId, select: true });
    return;
  }
  if (theme.updateOnKeep) await Themes.updateInstalled(theme.installedId);
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
 * app shell. It shows while a theme is previewed and stays on while the user
 * browses other pages, so the theme can be judged across the dashboard
 * (valgul 2026-11-27: "Keep it across pages (design)"). The preview ends on
 * Exit, Keep, a reload, or unmount. Nothing is written until Keep.
 */
export function ThemePreviewBanner() {
  const intl = useOptionalIntl();
  const { preview } = useThemePreview();
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);

  // Restore the saved theme when the app shell unmounts (sign-out).
  useEffect(() => () => exitThemePreview(), []);

  const keep = useMutation({
    mutationFn: keepTheme,
    onSuccess: (_data, kept) => {
      setError(null);
      dropThemePreview(kept);
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
        <Button
          variant="primary"
          size="sm"
          isDisabled={keep.isPending}
          onPress={() => {
            setError(null);
            keep.mutate(preview);
          }}
          className="rounded-full px-3.5"
        >
          {intl.formatMessage(themePreviewBannerMessages.keep)}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onPress={() => {
            setError(null);
            exitThemePreview();
          }}
          className="rounded-full px-3.5"
        >
          {intl.formatMessage(themePreviewBannerMessages.exit)}
        </Button>
      </div>
    </div>
  );
}
