import { Menu, UserCircle, LogOut, Palette } from "lucide-react";
import type { ReactNode } from "react";
import { useNavigate } from "@tanstack/react-router";
import {
  Button,
  Avatar,
  Dropdown,
  DropdownTrigger,
  DropdownPopover,
  DropdownMenu,
  DropdownItem,
  DropdownSection,
  Separator,
} from "@heroui/react";
import { Auth } from "@/lib/endpoints";
import type { User } from "@/types";
import type { JSX } from "react";
import { useOptionalIntl } from "@/lib/i18n/format";

export interface TopBarProps {
  /** Breadcrumb navigation slot — typically <Breadcrumbs items={...} /> */
  breadcrumbs: ReactNode;
  /** Cluster selector slot — typically <ClusterSelector /> */
  clusterSelector: ReactNode;
  /** Search slot — typically <GlobalSearch /> */
  search: ReactNode;
  /** Notifications panel slot — typically <NotificationsPanel /> */
  notifications: ReactNode;
  /** Current page title for mobile display (derived from last breadcrumb) */
  mobileTitle?: string;
  /** Current user for display in the avatar menu */
  user?: User;
  /** Called when hamburger button is clicked (mobile navigation trigger) */
  onMenuClick: () => void;
}

/**
 * TopBar is a pure layout component that composes the application header.
 * It accepts four `ReactNode` slots for its major sections (breadcrumbs, cluster
 * selector, search, notifications) and renders them alongside the mobile hamburger
 * button and user avatar dropdown menu. Each slot component owns its own data fetching.
 */
export function TopBar({
  breadcrumbs,
  clusterSelector,
  search,
  notifications,
  mobileTitle,
  user,
  onMenuClick,
}: TopBarProps): JSX.Element {
  const intl = useOptionalIntl();
  const name = user?.displayName || user?.username || intl.formatMessage({ id: "web.components.ui.top_bar.guest", defaultMessage: "guest", description: "Top bar: name shown for a user with no display name or username" });
  const initials = name.slice(0, 2).toUpperCase();
  const navigate = useNavigate();

  const handleLogout = async () => {
    try {
      await Auth.logout().catch(() => {});
    } finally {
      location.assign("/login");
    }
  };

  return (
    <header className="flex h-16 items-center justify-between gap-2 border-b border-border bg-background px-3 sm:gap-4 sm:px-6">
      {/* Left: hamburger + breadcrumbs (desktop only) + mobile title (mobile only) */}
      <div className="flex min-w-0 items-center gap-2">
        <Button
          isIconOnly
          variant="ghost"
          size="sm"
          aria-label={intl.formatMessage({ id: "web.components.ui.top_bar.open_navigation", defaultMessage: "Open navigation", description: "Top bar: icon-only button that opens the navigation on small screens; screen-reader name, not visible on screen" })}
          onClick={onMenuClick}
          className="shrink-0 lg:hidden"
        >
          <Menu className="h-5 w-5" />
        </Button>
        {/* Mobile title — shown below lg breakpoint */}
        {mobileTitle && (
          <div className="min-w-0 truncate font-mono text-base font-bold text-foreground lg:hidden">
            {mobileTitle}
          </div>
        )}
        {/* Desktop breadcrumbs — hidden below lg breakpoint */}
        <div className="hidden min-w-0 text-sm text-muted lg:block">{breadcrumbs}</div>
      </div>

      {/* Right: cluster selector, search, notifications, user menu */}
      <div className="flex shrink-0 items-center gap-2 sm:gap-3">
        {/* Cluster context stays reachable on mobile as well as desktop. */}
        <div className="flex min-w-0">{clusterSelector}</div>
        {/* Search — hidden below lg breakpoint */}
        <div className="hidden lg:flex">{search}</div>
        {/* Notifications — hidden below lg breakpoint */}
        <div className="hidden lg:flex">{notifications}</div>

        {/* User avatar dropdown menu */}
        <Dropdown>
          <DropdownTrigger
            aria-label={intl.formatMessage({ id: "web.components.ui.top_bar.user_menu", defaultMessage: "User menu", description: "Top bar: screen-reader name of the user avatar dropdown menu; not visible on screen" })}
            className="cursor-pointer"
          >
            <Avatar
              size="sm"
              color="default"
            >
              <Avatar.Fallback className="bg-accent text-accent-foreground">{initials}</Avatar.Fallback>
            </Avatar>
          </DropdownTrigger>
          <DropdownPopover placement="bottom end" className="rounded-md">
            <div className="flex items-center gap-2 px-2 py-2">
              <UserCircle className="h-4 w-4 text-muted" />
              <div className="flex flex-col">
                <span className="font-semibold text-foreground">{name}</span>
                <span className="text-xs text-muted">{user?.role ?? intl.formatMessage({ id: "web.common.empty_value", defaultMessage: "—", description: "Shown in place of a value that is not set" })}</span>
              </div>
            </div>
            <DropdownMenu className="w-48">
              <DropdownSection>
                <Separator className="my-1" />
                <DropdownItem
                  key="theme"
                  onClick={() => void navigate({ to: "/settings/theme" })}
                >
                  <div className="flex items-center gap-2">
                    <Palette className="h-4 w-4" />{intl.formatMessage({ id: "web.components.ui.top_bar.theme_appearance", defaultMessage: "Theme & Appearance", description: "Top bar: user menu item that opens the Theme and Appearance settings; keep it short" })}</div>
                </DropdownItem>
                <DropdownItem
                  key="logout"
                  onClick={() => void handleLogout()}
                  className="text-danger"
                >
                  <div className="flex items-center gap-2">
                    <LogOut className="h-4 w-4" />{intl.formatMessage({ id: "web.components.ui.top_bar.sign_out", defaultMessage: "Sign out", description: "Top bar: user menu item that signs the user out; keep it short" })}</div>
                </DropdownItem>
              </DropdownSection>
            </DropdownMenu>
          </DropdownPopover>
        </Dropdown>
      </div>
    </header>
  );
}
