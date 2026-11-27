// Shared data for the theme catalog and detail pages (spec 043, T029/T030).
// Install, update and remove go through the Themes client; each success
// refreshes the installed list, the catalog and the signed-in user (whose
// preferences the server may reset when the selected theme is removed).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Themes } from "@/lib/endpoints";
import { APIError } from "@/lib/api";
import type { InstalledTheme } from "@/types";

export const THEME_CATALOG_KEY = "theme-catalog";
export const INSTALLED_THEMES_KEY = "installed-themes";

// Server errors carry a plain-English (or localized server.* id) body; show it
// as is, the same way the modules pages do.
export function themeErrorText(err: unknown): string {
  if (err instanceof APIError) return err.body || err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}

// Catalog entries do not carry the installed row id, so match on source + name.
export function findInstalled(
  installed: readonly InstalledTheme[] | undefined,
  theme: { source: string; name: string },
): InstalledTheme | undefined {
  return installed?.find((t) => t.source === theme.source && t.name === theme.name);
}

// useThemeActions loads the signed-in user's installed themes (failing soft:
// an error leaves installed undefined, so no installed badges are shown) and
// exposes the install, update and remove mutations. onError receives the
// message to show on the page, or null once an action succeeds.
export function useThemeActions(onError: (message: string | null) => void) {
  const qc = useQueryClient();
  const installedQuery = useQuery({
    queryKey: [INSTALLED_THEMES_KEY],
    queryFn: () => Themes.installed(),
  });

  const refresh = async () => {
    await Promise.all([
      qc.invalidateQueries({ queryKey: [INSTALLED_THEMES_KEY] }),
      qc.invalidateQueries({ queryKey: [THEME_CATALOG_KEY] }),
      qc.invalidateQueries({ queryKey: ["me"] }),
    ]);
  };

  const fail = (err: Error) => onError(themeErrorText(err));

  const install = useMutation({
    mutationFn: (catalogId: number) => Themes.install({ catalogId }),
    onSuccess: async () => {
      onError(null);
      await refresh();
    },
    onError: fail,
  });

  const update = useMutation({
    mutationFn: (installedId: number) => Themes.updateInstalled(installedId),
    onSuccess: async () => {
      onError(null);
      await refresh();
    },
    onError: fail,
  });

  const remove = useMutation({
    mutationFn: (installedId: number) => Themes.removeInstalled(installedId),
    onSuccess: async () => {
      onError(null);
      await refresh();
    },
    onError: fail,
  });

  return {
    installed: installedQuery.data,
    install,
    update,
    remove,
    busy: install.isPending || update.isPending || remove.isPending,
  };
}
