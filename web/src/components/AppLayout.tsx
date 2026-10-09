import { Outlet, useLocation, useMatches, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Archive,
  LayoutDashboard,
  Network,
  Package,
  ScrollText,
  Server,
  Settings,
  Terminal,
  Users,
} from "lucide-react";
import { APIError } from "@/lib/api";
import { Clusters, Auth } from "@/lib/endpoints";
import { useMe, can } from "@/lib/auth";
import { useInstallation } from "@/lib/useInstallation";
import type { User } from "@/types";
import { useEffect, useState } from "react";
import { AppShell } from "@/components/ui/AppShell";
import { Sidebar, type SidebarNavGroup } from "@/components/ui/Sidebar";
import { TopBar } from "@/components/ui/TopBar";
import { Breadcrumbs, buildCrumbs } from "@/components/ui/Breadcrumbs";
import { GlobalSearch } from "@/components/ui/GlobalSearch";
import { NotificationsPanel } from "@/components/ui/NotificationsPanel";
import { AppLoadingSkeleton } from "@/components/ui/AppLoadingSkeleton";
import type { AppearanceMode } from "@/components/ui/AppearanceToggle";
import { useDelayedLoading } from "@/lib/useDelayedLoading";
import {
  applyThemePreferences,
  DEFAULT_THEME_PREFERENCES,
  isSafeModeActive,
  SAFE_MODE_SESSION_KEY,
  unmountCustomCssOverlay,
  useThemePreferences,
} from "@/lib/useThemePreferences";
import { SafeModeBanner } from "@/components/ui/SafeModeBanner";
import { TelemetryNotice } from "@/components/ui/TelemetryNotice";

// The localStorage key the theme boot script in index.html reads before
// React mounts — must stay in sync (see index.html and theme-tokens.md).
// The prefs cache (gameplane-theme-prefs, owned by useThemePreferences) is
// authoritative once it exists; this legacy key (light/dark/system only)
// remains for backwards compatibility.
// Note: HeroUI's own `useTheme()` hook hardcodes a different key
// ("heroui-theme"), which would silently diverge from the boot script, so
// theme state is owned here rather than via that hook (deviation from the
// contract's preferred approach, flagged for the maintainer).
const THEME_STORAGE_KEY = "gameplane-theme";

function readStoredTheme(): AppearanceMode {
  try {
    const saved = localStorage.getItem(THEME_STORAGE_KEY);
    if (saved === "light" || saved === "dark" || saved === "system") return saved;
  } catch {
    // localStorage unavailable — fall back to system default.
  }
  return "system";
}

function useAppearance(
  me: User | undefined,
): [AppearanceMode, (mode: AppearanceMode) => void, boolean] {
  const { preferences, updatePreferences } = useThemePreferences(me);
  const [legacyTheme, setLegacyTheme] = useState<AppearanceMode>(readStoredTheme);

  const theme: AppearanceMode = preferences?.appearanceMode ?? legacyTheme;
  // D4: the sidebar footer toggle is meaningless (and disabled) while a
  // custom-colors theme is active — light/dark follows the surface color.
  const isCustomColorsActive =
    preferences?.themeType === "custom_colors" && !!preferences.customColors;

  const setTheme = (mode: AppearanceMode) => {
    setLegacyTheme(mode);
    try {
      localStorage.setItem(THEME_STORAGE_KEY, mode);
    } catch {
      // localStorage unavailable — theme still applies for this session.
    }
    if (preferences) {
      // Persist through the preferences pipeline (optimistic DOM apply +
      // PUT) so the choice syncs across devices; the hook also owns the
      // system-mode media-query subscription.
      updatePreferences({ appearanceMode: mode });
    } else {
      applyThemePreferences({ ...DEFAULT_THEME_PREFERENCES, appearanceMode: mode });
    }
  };

  return [theme, setTheme, isCustomColorsActive];
}

export function AppLayout() {
  const { data: me, error, isLoading } = useMe();
  const installation = useInstallation();
  const { data: registry } = useQuery({ queryKey: ["clusters"], queryFn: () => Clusters.list() });
  const canViewInventory = registry?.items.some((item) => item.canViewInventory === true) === true;
  const canManageInfrastructure = can(me, "cluster:manage") || canViewInventory;
  const { pathname } = useLocation();
  const matches = useMatches();
  const navigate = useNavigate();
  const [theme, setTheme, isCustomColorsActive] = useAppearance(me);
  // Below `lg`, the fixed sidebar becomes an off-canvas drawer toggled by
  // the TopBar's hamburger button. Desktop (`lg`+) keeps the always-on
  // sidebar and never mounts the drawer.
  const [drawerOpen, setDrawerOpen] = useState(false);
  // Safe mode (FR-009, contracts/theme-ui.md §4): ?safe-mode=1 in the URL,
  // the Ctrl+Shift+Alt+T shortcut, or the sessionStorage flag planted by the
  // safe-mode login link. Suspends only the custom CSS overlay.
  const [safeMode, setSafeMode] = useState(() => isSafeModeActive());
  const [safeModeBannerDismissed, setSafeModeBannerDismissed] = useState(false);
  const showSkeleton = useDelayedLoading(isLoading);

  useEffect(() => {
    if (error instanceof APIError && error.status === 401) {
      location.assign("/login");
    }
  }, [error]);

  // The keyboard safe-mode entry point: plant the same sessionStorage flag
  // as the login-page link so safe mode (and the banner) survive reloads,
  // then activate it for the current view.
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      if (!e.ctrlKey || !e.shiftKey || !e.altKey || e.repeat) return;
      if (e.key.toLowerCase() !== "t") return;
      try {
        window.sessionStorage.setItem(SAFE_MODE_SESSION_KEY, "1");
      } catch {
        // sessionStorage blocked — safe mode still applies for this view.
      }
      setSafeMode(true);
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  // Suspension is overlay-only: unmountCustomCssOverlay flips
  // data-custom-css="off" and removes the mounted <style> element — the
  // stored stylesheet in preferences/localStorage is never deleted.
  useEffect(() => {
    if (safeMode) unmountCustomCssOverlay();
  }, [safeMode]);

  // The custom CSS overlay is authenticated-only surface: unmount it when
  // the layout unmounts (navigation to /login or /share/:token) as well as
  // on logout. The stored stylesheet itself is never deleted.
  useEffect(() => () => unmountCustomCssOverlay(), []);

  if (showSkeleton) return <AppLoadingSkeleton />;

  const onLogout = async () => {
    unmountCustomCssOverlay();
    await Auth.logout().catch(() => {});
    location.assign("/login");
  };

  // Sidebar is deliberately router-free (its tests mock the router module),
  // so the footer's customize-theme button reports through this callback.
  const openThemeSettings = () => {
    void navigate({ to: "/settings/theme" });
  };

  const openTelemetrySettings = () => {
    void navigate({ to: "/admin", search: { section: "telemetry" } });
  };

  const navItems: SidebarNavGroup[] = [
    {
      label: "General",
      items: [
        { to: "/", label: "Dashboard", icon: LayoutDashboard },
        { to: "/servers", label: "Servers", icon: Server },
        { to: "/modules", label: "Modules", icon: Package },
        { to: "/backups", label: "Backups", icon: Archive },
      ],
    },
    {
      label: "Admin",
      items: [
        ...(canManageInfrastructure ? [{ to: "/clusters", label: "Clusters", icon: Network }] : []),
        ...(canViewInventory
          ? [{ to: "/cluster", label: "Cluster", icon: Server }]
          : []),
        ...(can(me, "users:manage")
          ? [{ to: "/users", label: "Users & RBAC", icon: Users }]
          : []),
        ...(can(me, "audit:read")
          ? [{ to: "/admin/audit", label: "Audit log", icon: ScrollText }]
          : []),
        // System logs match the API's /admin/system-logs guard: the admin
        // wildcard permission, not a narrower named one.
        ...(can(me, "*")
          ? [{ to: "/admin/logs", label: "System logs", icon: Terminal }]
          : []),
        ...(can(me, "config:manage")
          ? [{ to: "/admin", label: "Settings", icon: Settings }]
          : []),
      ],
    },
  ];

  // The splat route under this layout (see router/tree.tsx) is the 404 page;
  // its crumb is fixed instead of echoing the unknown URL segments.
  const onNotFound = matches.some((m) => m.routeId.endsWith("$"));
  const crumbs = onNotFound
    ? [{ label: "gameplane", to: "/" }, { label: "Page not found" }]
    : buildCrumbs(pathname);
  // Extract the last breadcrumb label as the mobile title
  const mobileTitle = crumbs.length > 0 ? crumbs[crumbs.length - 1].label : "";
  const centralManagement = ["/users", "/admin", "/settings", ...(installation.data?.standalone === false ? ["/modules"] : [])].some(
    (prefix) => pathname === prefix || pathname.startsWith(prefix + "/"),
  );

  return (
    <>
      {/* Safe-mode banner: sticky at the top of the viewport so it stays
          reachable while recovering from broken custom CSS. Dismiss only
          hides it — safe mode remains active for the session. */}
      {safeMode && !safeModeBannerDismissed && (
        <div className="sticky top-0 z-50">
          <SafeModeBanner
            onOpenSettings={openThemeSettings}
            onDismiss={() => setSafeModeBannerDismissed(true)}
          />
        </div>
      )}
      <AppShell
        sidebar={
          <Sidebar
            navItems={navItems}
            clusterName={centralManagement ? "Central management" : "Gameplane"}
            user={me}
            variant="fixed"
            onLogout={onLogout}
            theme={theme}
            onThemeChange={setTheme}
            onCustomizeTheme={openThemeSettings}
            isCustomColorsActive={isCustomColorsActive}
          />
        }
        topBar={
          <TopBar
            breadcrumbs={<Breadcrumbs items={crumbs} />}
            clusterSelector={null}
            search={<GlobalSearch />}
            notifications={<NotificationsPanel enabled={installation.data?.localCluster === true} />}
            mobileTitle={mobileTitle}
            user={me}
            onMenuClick={() => setDrawerOpen(true)}
          />
        }
      >
        {/* First-login telemetry notice (spec 022 FR-004): admins only; the
            API also answers {pending:false} to callers without config:manage. */}
        {can(me, "config:manage") && <TelemetryNotice onOpenSettings={openTelemetrySettings} />}
        {centralManagement && (
          <div role="note" className="border-b border-border bg-surface px-4 py-3 text-sm text-muted sm:px-6">
            <strong className="font-medium text-foreground">Central management.</strong>{" "}
            This catalog, account or installation setting belongs to the central Gameplane installation. Server actions use each server’s location.
          </div>
        )}
        <Outlet />
      </AppShell>

      {/* Mobile off-canvas drawer — rendered outside AppShell so it never
          nests inside the <main> landmark; HeroUI's Drawer portals its
          content regardless, but keeping it a sibling here avoids
          confusing the accessibility tree in source order too. */}
      <Sidebar
        navItems={navItems}
        clusterName={centralManagement ? "Central management" : "Gameplane"}
        user={me}
        variant="drawer"
        isOpen={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        onNavigate={() => setDrawerOpen(false)}
        onLogout={onLogout}
        theme={theme}
        onThemeChange={setTheme}
        onCustomizeTheme={openThemeSettings}
        isCustomColorsActive={isCustomColorsActive}
      />
    </>
  );
}
