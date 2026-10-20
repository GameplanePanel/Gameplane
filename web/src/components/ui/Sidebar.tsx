import { useLocation, Link } from "@tanstack/react-router";
import type { LucideIcon } from "lucide-react";
import { ShieldCheck, X, LogOut, Palette } from "lucide-react";
import {
  Drawer,
} from "@heroui/react";
import type { User } from "@/types";
import { cn } from "@/lib/utils";
import { AppearanceToggle, type AppearanceMode } from "./AppearanceToggle";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface SidebarNavItem {
  to: string;
  label: string;
  icon: LucideIcon;
}

export interface SidebarNavGroup {
  label: string;
  items: SidebarNavItem[];
}

export interface SidebarProps {
  navItems: SidebarNavGroup[];
  clusterName?: string;
  user?: User;
  variant: "fixed" | "drawer";
  isOpen?: boolean;
  onClose?: () => void;
  onNavigate?: () => void;
  onLogout: () => void | Promise<void>;
  theme?: AppearanceMode;
  onThemeChange?: (mode: AppearanceMode) => void;
  /** Called when the footer "Customize theme" button is pressed (navigates to /settings/theme). */
  onCustomizeTheme?: () => void;
  /** D4: true while a custom-colors theme is active — disables the footer AppearanceToggle. */
  isCustomColorsActive?: boolean;
}

export function Sidebar({
  navItems,
  clusterName,
  user,
  variant,
  isOpen,
  onClose,
  onNavigate,
  onLogout,
  theme = "system",
  onThemeChange,
  onCustomizeTheme,
  isCustomColorsActive = false,
}: SidebarProps) {
  const intl = useOptionalIntl();
  const { pathname } = useLocation();

  // Compute which nav item is active (exact vs prefix matching per contract).
  const isActive = (to: string): boolean => {
    // /settings/theme sits outside /admin but belongs to the Settings
    // surface, so it lights up the Settings item.
    if (to === "/admin" && pathname.startsWith("/settings/")) return true;
    const exact = to === "/" || navItems.some((group) =>
      group.items.some((o) => o.to !== to && o.to.startsWith(to + "/"))
    );
    if (exact) return to === pathname;
    return pathname === to || pathname.startsWith(to + "/");
  };

  const sidebarContent = (
    <aside
      aria-label={intl.formatMessage({ id: "web.components.ui.sidebar.sidebar", defaultMessage: "Sidebar", description: "Sidebar: screen-reader name of the side navigation panel; not visible on screen" })}
      className={cn(
        "flex flex-col border-r border-border bg-surface/60",
        variant === "fixed" ? "h-full w-[260px] shrink-0" : "w-full h-full"
      )}>
      {/* Header */}
      <div className="flex items-center gap-2 px-5 py-4 border-b border-border">
        <div className="flex h-8 w-8 items-center justify-center rounded-md bg-primary/15">
          <ShieldCheck className="h-4 w-4 text-primary" />
        </div>
        <div className="min-w-0 flex-1 leading-tight">
          <div className="font-mono text-base font-semibold text-fg">{intl.formatMessage({ id: "web.components.ui.sidebar.gameplane", defaultMessage: "gameplane", description: "Sidebar: product name shown at the top of the side navigation, with the cluster name beneath it; keep the name as written" })}</div>
          <div className="text-[11px] text-muted truncate">{clusterName || intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</div>
        </div>
        {variant === "drawer" && onClose && (
          <button
            type="button"
            aria-label={intl.formatMessage({ id: "web.components.ui.sidebar.close_navigation", defaultMessage: "Close navigation", description: "Sidebar: icon-only button that closes the navigation drawer on small screens; screen-reader name, not visible on screen" })}
            onClick={onClose}
            // Auto-focus so the drawer's own Escape-to-dismiss handling
            // (attached to the modal content, not a global listener) has
            // focus inside the overlay to bubble the keydown from.
            autoFocus
            className="rounded-md p-1.5 text-muted hover:bg-border/60 hover:text-fg"
          >
            <X className="h-4 w-4" />
          </button>
        )}
      </div>

      {/* Nav */}
      {/* Distinct accessible names per variant: the fixed sidebar and the
          mobile drawer both render this markup, and — unlike a raw CSS
          attribute selector — an assistive tech's accessibility tree (and
          Playwright's getByRole) can see both landmarks at once whenever
          the drawer is open, so sharing one name would give two
          indistinguishable "Primary" navigation landmarks. */}
      <nav
        className="flex-1 overflow-auto px-3 py-2 scrollbar-thin"
        aria-label={variant === "drawer" ? intl.formatMessage({ id: "web.components.ui.sidebar.mobile_navigation", defaultMessage: "Mobile navigation", description: "Sidebar: accessible name of the navigation landmark in the mobile drawer" }) : intl.formatMessage({ id: "web.components.ui.sidebar.primary", defaultMessage: "Primary", description: "Sidebar: accessible name of the main navigation landmark" })}
      >
        {navItems.map((group) => (
          group.items.length > 0 && (
            <div key={group.label}>
              <div className="px-3 pb-2 pt-3 text-[10px] font-semibold uppercase tracking-widest text-muted">
                {group.label}
              </div>
              <ul className="flex flex-col gap-0.5">
                {group.items.map(({ to, label, icon: Icon }) => (
                  <li key={to}>
                    <Link
                      to={to}
                      onClick={onNavigate}
                      aria-current={isActive(to) ? "page" : undefined}
                      className={cn(
                        "group flex items-center gap-3 rounded-md px-3 py-2 text-sm text-muted transition-colors",
                        "hover:bg-border/60 hover:text-fg",
                        isActive(to) && "bg-primary/10 text-primary"
                      )}
                    >
                      <Icon className="h-[18px] w-[18px] shrink-0" />
                      <span>{label}</span>
                    </Link>
                  </li>
                ))}
              </ul>
              {navItems.findIndex((g) => g === group) < navItems.length - 1 && group.items.length > 0 && (
                <div className="h-3" />
              )}
            </div>
          )
        ))}
      </nav>

      {/* Footer */}
      <div className="border-t border-border px-3 py-3 space-y-2" aria-label={intl.formatMessage({ id: "web.components.ui.sidebar.sidebar_footer", defaultMessage: "Sidebar footer", description: "Sidebar: screen-reader name of the footer area at the bottom of the side navigation; not visible on screen" })}>
        {/* Appearance toggle row */}
        {(onThemeChange || onCustomizeTheme) && (
          <div className="flex items-center justify-center gap-2">
            {onThemeChange && (
              <AppearanceToggle
                value={theme}
                onChange={onThemeChange}
                disabled={isCustomColorsActive}
              />
            )}
            {onCustomizeTheme && (
              <button
                type="button"
                aria-label={intl.formatMessage({ id: "web.components.ui.sidebar.customize_theme", defaultMessage: "Customize theme", description: "Sidebar: icon-only button for changing the theme; its tooltip and screen-reader name read Customize theme; keep it short" })}
                title={intl.formatMessage({ id: "web.components.ui.sidebar.customize_theme", defaultMessage: "Customize theme", description: "Sidebar: icon-only button for changing the theme; its tooltip and screen-reader name read Customize theme; keep it short" })}
                onClick={() => {
                  onNavigate?.();
                  onCustomizeTheme();
                }}
                className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full text-muted transition-colors hover:bg-border/60 hover:text-fg"
              >
                <Palette className="h-4 w-4" />
              </button>
            )}
          </div>
        )}

        {/* User info + logout row */}
        <div className="flex items-center gap-3 rounded-md px-2 py-1.5">
          <div className="flex h-8 w-8 items-center justify-center rounded-full bg-primary/20 font-mono text-xs text-primary shrink-0">
            {(user?.displayName || user?.username || "G").slice(0, 2).toUpperCase()}
          </div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm text-fg">
              {user?.displayName || user?.username || intl.formatMessage({ id: "web.components.ui.sidebar.guest", defaultMessage: "guest", description: "Sidebar: name shown for a user with no display name or username" })}
            </div>
            <div className="truncate text-[11px] text-muted">
              {user?.role ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}
            </div>
          </div>
          <button
            aria-label={intl.formatMessage({ id: "web.components.ui.sidebar.sign_out", defaultMessage: "Sign out", description: "Sidebar: icon-only button that signs the user out; screen-reader name, not visible on screen; keep it short" })}
            className="rounded p-1 text-muted hover:bg-border/60 hover:text-fg transition-colors"
            onClick={() => void onLogout()}
          >
            <LogOut className="h-4 w-4" />
          </button>
        </div>
      </div>
    </aside>
  );

  // Render as drawer if variant is "drawer"
  if (variant === "drawer") {
    return (
      <Drawer.Root isOpen={isOpen ?? false} onOpenChange={(open) => { if (!open) onClose?.(); }}>
        <Drawer.Backdrop>
          <Drawer.Content placement="left">
            <Drawer.Dialog className="w-[280px] max-w-[85vw] p-0">
              <Drawer.Body className="p-0">
                {sidebarContent}
              </Drawer.Body>
            </Drawer.Dialog>
          </Drawer.Content>
        </Drawer.Backdrop>
      </Drawer.Root>
    );
  }

  // Render as fixed sidebar
  return sidebarContent;
}
