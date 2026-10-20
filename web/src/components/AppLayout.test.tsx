import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeServer, makeUser } from "@/test/factories";
import { setCurrentCluster } from "@/lib/cluster";
import {
  SAFE_MODE_SESSION_KEY,
  THEME_PREFS_STORAGE_KEY,
  DEFAULT_THEME_PREFERENCES,
} from "@/lib/useThemePreferences";

// TanStack Router APIs the layout (and the hero/* components it composes)
// reach into.
const routerMocks = {
  useLocation: () => ({ pathname: "/" } as ReturnType<typeof import("@tanstack/react-router").useLocation>),
  useNavigate: () => vi.fn(),
};
const navigateMock = vi.fn();

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
  Outlet: () => <div data-testid="outlet">outlet</div>,
  useLocation: () => routerMocks.useLocation(),
  useMatches: () => [],
  useNavigate: () => navigateMock,
}));

import { AppLayout } from "./AppLayout";

function fleetServers(names: string[]) {
  return {
    items: names.map((name) => ({
      target: { cluster: "local", namespace: "gameplane-games", name, uid: `${name}-uid` },
      resource: makeServer({ metadata: { name, uid: `${name}-uid` } }),
      permissions: ["servers:read"],
    })),
    partial: false,
    issues: [],
    totalReturned: names.length,
  };
}

function searchResultID(name: string) {
  return `search-result-${JSON.stringify(["local", "gameplane-games", name, `${name}-uid`])}`;
}

// HeroUI's Breadcrumbs.Item always renders `role="link"` — even the
// disabled, href-less current-page crumb (see Breadcrumbs.tsx) — so a
// breadcrumb reading "Dashboard" (root path) or a route's own label (e.g.
// "Servers") collides by accessible name with the matching sidebar nav
// link. Scope queries to the sidebar's own `<nav aria-label="Primary">`
// landmark wherever a query could ambiguously match both.
async function sidebarNav() {
  return within(await screen.findByRole("navigation", { name: "Primary" }));
}

async function sidebarRoot() {
  return within(await screen.findByRole("complementary", { name: "Sidebar" }));
}

describe("AppLayout", () => {
  it.each([true, false])("labels module ownership for the installation mode (standalone=%s)", async (standalone) => {
    const originalLocation = routerMocks.useLocation;
    routerMocks.useLocation = () => ({ pathname: "/modules" } as ReturnType<typeof import("@tanstack/react-router").useLocation>);
    server.use(http.get("/admin/installation", () => HttpResponse.json({ standalone, localCluster: !standalone })));
    const view = renderWithQuery(<AppLayout />);
    try {
      await waitFor(() => expect(view.client.getQueryData(["installation"])).toEqual({ standalone, localCluster: !standalone }));
      await screen.findByTestId("outlet");
      if (standalone) {
        await waitFor(() => expect(screen.queryByText("Central management.")).not.toBeInTheDocument());
      } else {
        expect(await screen.findByText("Central management.")).toBeInTheDocument();
      }
    } finally {
      view.unmount();
      routerMocks.useLocation = originalLocation;
    }
  });

  it.each([true, false])("opens local events only when installation capabilities report a local cluster (standalone=%s)", async (standalone) => {
    const connect = vi.fn();
    class EventStream {
      constructor(url: string) { connect(url); }
      close() {}
    }
    vi.stubGlobal("EventSource", EventStream);
    server.use(http.get("/admin/installation", () => HttpResponse.json({ standalone, localCluster: !standalone })));
    const view = renderWithQuery(<AppLayout />);
    try {
      expect(connect).not.toHaveBeenCalled();
      await waitFor(() => expect(view.client.getQueryData(["installation"])).toEqual({ standalone, localCluster: !standalone }));
      if (standalone) {
        await userEvent.click(await screen.findByRole("button", { name: /notifications/i }));
        expect(await screen.findByText("No recent activity.")).toBeInTheDocument();
        expect(connect).not.toHaveBeenCalled();
      } else {
        await waitFor(() => expect(connect).toHaveBeenCalledWith("/events"));
      }
    } finally {
      view.unmount();
      vi.unstubAllGlobals();
    }
  });

  it("renders the sidebar nav items shared by all roles", async () => {
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser({ role: "viewer" }))),
      http.get("/cluster/info", () => HttpResponse.json({ clusterName: "homelab" })),
      http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", canViewInventory: false }] })),
    );
    renderWithQuery(<AppLayout />);
    await waitFor(async () =>
      expect((await sidebarNav()).getByRole("link", { name: /Dashboard/i })).toBeInTheDocument(),
    );
    const nav = await sidebarNav();
    expect(nav.getByRole("link", { name: /Servers/i })).toBeInTheDocument();
    expect(nav.getByRole("link", { name: /Modules/i })).toBeInTheDocument();
    expect(nav.getByRole("link", { name: /Backups/i })).toBeInTheDocument();
    // Server visibility alone does not expose infrastructure administration.
    expect(nav.queryByRole("link", { name: "Cluster" })).not.toBeInTheDocument();
    expect(nav.queryByRole("link", { name: "Clusters" })).not.toBeInTheDocument();
    expect(nav.queryByRole("link", { name: /Audit log/i })).not.toBeInTheDocument();
  });

  it("an explicit inventory capability unlocks the infrastructure navigation", async () => {
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser({ role: "operator" }))),
      http.get("/clusters", () => HttpResponse.json({ items: [{ name: "east", canViewInventory: true }] })),
    );
    renderWithQuery(<AppLayout />);
    await waitFor(() =>
      expect(screen.getByRole("link", { name: /^Cluster$/i })).toBeInTheDocument(),
    );
    expect(screen.getByRole("link", { name: "Clusters" })).toHaveAttribute("href", "/clusters");
    expect(screen.queryByRole("button", { name: "Select cluster" })).not.toBeInTheDocument();
  });

  it("admin role unlocks Users / Audit / Settings nav", async () => {
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser({ role: "admin" }))),
    );
    renderWithQuery(<AppLayout />);
    await waitFor(() =>
      expect(screen.getByRole("link", { name: /Users & RBAC/i })).toBeInTheDocument(),
    );
    expect(screen.getByRole("link", { name: /Audit log/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Settings/i })).toBeInTheDocument();
  });

  it("renders the outlet for child routes", async () => {
    renderWithQuery(<AppLayout />);
    expect(await screen.findByTestId("outlet")).toBeInTheDocument();
  });

  it("notifications bell toggles a panel", async () => {
    renderWithQuery(<AppLayout />);
    const bell = await screen.findByRole("button", { name: /notifications/i });
    await userEvent.click(bell);
    expect(await screen.findByText("Recent activity")).toBeInTheDocument();
  });

  it("hamburger opens the mobile nav drawer; scrim and nav clicks close it", async () => {
    renderWithQuery(<AppLayout />);
    // Closed by default — no close button (drawer-only) in the document.
    expect(screen.queryByRole("button", { name: /close navigation/i })).not.toBeInTheDocument();

    const menuBtn = await screen.findByRole("button", { name: /open navigation/i });
    await userEvent.click(menuBtn);

    // The drawer is now open with its nav rendered. Desktop sidebar is hidden below `lg`.
    const closeBtn = await screen.findByRole("button", { name: /close navigation/i });
    expect(screen.getByRole("link", { name: /Dashboard/i })).toBeInTheDocument();

    await userEvent.click(closeBtn);
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /close navigation/i })).not.toBeInTheDocument(),
    );

    // Re-open, then close via a nav-item click instead of the X button.
    await userEvent.click(menuBtn);
    await screen.findByRole("button", { name: /close navigation/i });
    const drawerLinks = screen.getAllByRole("link", { name: /Servers/i });
    await userEvent.click(drawerLinks[drawerLinks.length - 1]);
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /close navigation/i })).not.toBeInTheDocument(),
    );
  });

  it("drawer closes when Escape is pressed (HeroUI Drawer default)", async () => {
    renderWithQuery(<AppLayout />);
    const menuBtn = await screen.findByRole("button", { name: /open navigation/i });
    await userEvent.click(menuBtn);

    // Drawer is open — close button is visible.
    await screen.findByRole("button", { name: /close navigation/i });

    // Press Escape to close (HeroUI's Drawer, built on react-aria-components
    // Modal, handles this automatically — same contract as the old Radix
    // Dialog it replaces).
    await userEvent.keyboard("{Escape}");

    // Close button should be gone, drawer is closed.
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /close navigation/i })).not.toBeInTheDocument(),
    );
  });

  it("global search filters servers by name and navigates to detail", async () => {
    server.use(
      http.get("/fleet/servers", () =>
        HttpResponse.json(fleetServers(["alpha", "beta"])),
      ),
    );
    renderWithQuery(<AppLayout />);
    const search = await screen.findByLabelText(/search servers/i);
    await userEvent.type(search, "alph");
    const result = await screen.findByTestId(searchResultID("alpha"));
    expect(result).toBeInTheDocument();
    expect(within(result).getByRole("link")).toHaveAttribute("href", "/servers/alpha?cluster=local&ns=gameplane-games");
    expect(screen.queryByTestId(searchResultID("beta"))).not.toBeInTheDocument();

    await userEvent.click(within(result).getByRole("link"));
    await waitFor(() =>
      expect(navigateMock).toHaveBeenCalledWith({ to: "/servers/$name", params: { name: "alpha" }, search: { cluster: "local", ns: "gameplane-games" } }),
    );
  });

  it("shows a shell skeleton while useMe is loading", async () => {
    // Use a promise that never resolves to simulate indefinite loading.
    server.use(
      http.get("/users/me", () => new Promise(() => {})), // Never resolves
      http.get("/cluster/info", () => new Promise(() => {})), // Never resolves
    );

    renderWithQuery(<AppLayout />);

    // Skeleton should be present with aria-busy attribute and Loading label.
    const skeleton = screen.getByLabelText("Loading");
    expect(skeleton).toHaveAttribute("aria-busy", "true");

    // "guest" text must NOT appear anywhere — no pre-auth identity leak.
    expect(screen.queryByText(/guest/i)).not.toBeInTheDocument();

    // No admin links should appear while loading (they require me data).
    expect(screen.queryByRole("link", { name: /Users & RBAC/i })).not.toBeInTheDocument();
  });

  it("transitions from skeleton to real shell when user loads", async () => {
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser({ role: "admin" }))),
      http.get("/cluster/info", () => HttpResponse.json({ clusterName: "homelab" })),
    );

    renderWithQuery(<AppLayout />);

    // Wait for real content (admin link) — indicates skeleton has been replaced.
    await waitFor(() =>
      expect(screen.getByRole("link", { name: /Users & RBAC/i })).toBeInTheDocument(),
    );

    // Real nav is present; skeleton is gone.
    expect(screen.getByRole("link", { name: /Audit log/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Settings/i })).toBeInTheDocument();
  });

  it("uses stable panel branding rather than a selected infrastructure cluster", async () => {
    setCurrentCluster("east");
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser())),
      http.get("/cluster/info", () => HttpResponse.json({ clusterName: "prod-us-east" })),
    );
    try {
      renderWithQuery(<AppLayout />);
      expect(await (await sidebarRoot()).findByText("Gameplane", { exact: true })).toBeInTheDocument();
      expect(screen.queryByText("prod-us-east")).not.toBeInTheDocument();
      expect(screen.queryByRole("button", { name: "Select cluster" })).not.toBeInTheDocument();
    } finally {
      setCurrentCluster("local");
    }
  });

  it("keeps the shell usable when infrastructure name lookup is unavailable", async () => {
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser())),
      http.get("/cluster/info", () => new HttpResponse(null, { status: 500 })),
    );
    renderWithQuery(<AppLayout />);
    expect((await sidebarRoot()).getByText("Gameplane", { exact: true })).toBeInTheDocument();
    expect((await sidebarNav()).getByRole("link", { name: "Servers" })).toHaveAttribute("href", "/servers");
  });

  it("shows role in profile footer", async () => {
    server.use(
      http.get("/users/me", () =>
        HttpResponse.json(makeUser({ role: "operator", displayName: "Alice" })),
      ),
    );
    renderWithQuery(<AppLayout />);
    // "operator"/"Alice" also appear a second time in the TopBar's user-menu
    // dropdown item, so scope to the sign-out button's own footer row (the
    // fixed sidebar's profile footer — the only one mounted while the
    // mobile drawer is closed) to keep the match unique. The HeroUI Drawer
    // does not mount its content while closed, so only the fixed sidebar's
    // button is in the DOM.
    const signOut = (await screen.findAllByRole("button", { name: /sign out/i }))[0];
    const footer = signOut.closest("div")!.parentElement!;
    expect(await within(footer).findByText("operator")).toBeInTheDocument();
    expect(within(footer).getByText("Alice")).toBeInTheDocument();
  });

  it("shows guest username when displayName is absent", async () => {
    server.use(
      http.get("/users/me", () =>
        HttpResponse.json(makeUser({ username: "bob", displayName: "" })),
      ),
    );
    renderWithQuery(<AppLayout />);
    const signOut = (await screen.findAllByRole("button", { name: /sign out/i }))[0];
    const footer = signOut.closest("div")!.parentElement!;
    expect(await within(footer).findByText("bob")).toBeInTheDocument();
  });

  it("renders avatar initials correctly", async () => {
    server.use(
      http.get("/users/me", () =>
        HttpResponse.json(makeUser({ displayName: "Alice Brown" })),
      ),
    );
    renderWithQuery(<AppLayout />);
    // Initials should be "AL" (first 2 chars uppercase). The same initials
    // also render in the desktop sidebar's profile footer (present in the
    // DOM even though it's visually hidden below `lg`), so scope to the
    // TopBar (the page's <header>, role "banner") to keep the match unique.
    // AppShellSkeleton *also* renders a <header role="banner"> (with no
    // "AL" text inside) while useMe() is loading, so an unscoped
    // findByRole("banner") can resolve to that transient skeleton header
    // before real data arrives — within() on that since-unmounted node then
    // never finds "AL" and hangs. Wait for a loaded-only element (a real
    // nav link, which the skeleton never renders) first.
    await waitFor(async () => expect((await sidebarNav()).getByRole("link", { name: /Dashboard/i })).toBeInTheDocument());
    const header = screen.getByRole("banner");
    expect(within(header).getByText("AL")).toBeInTheDocument();
  });

  it("search dropdown shows no matches message", async () => {
    server.use(
      http.get("/fleet/servers", () =>
        HttpResponse.json(fleetServers(["alpha"])),
      ),
    );
    renderWithQuery(<AppLayout />);
    const search = await screen.findByLabelText(/search servers/i);
    await userEvent.type(search, "nonexistent");
    expect(await screen.findByText("No servers match.")).toBeInTheDocument();
  });

  it("search navigates on Enter key with first match", async () => {
    server.use(
      http.get("/fleet/servers", () =>
        HttpResponse.json(fleetServers(["alpha", "beta"])),
      ),
    );
    renderWithQuery(<AppLayout />);
    const search = await screen.findByLabelText(/search servers/i);
    await userEvent.type(search, "a");
    await screen.findByTestId(searchResultID("alpha"));
    await userEvent.keyboard("{Enter}");
    await waitFor(() =>
      expect(navigateMock).toHaveBeenCalledWith({ to: "/servers/$name", params: { name: "alpha" }, search: { cluster: "local", ns: "gameplane-games" } }),
    );
  });

  it("search dropdown closes on blur", async () => {
    server.use(
      http.get("/fleet/servers", () =>
        HttpResponse.json(fleetServers(["alpha"])),
      ),
    );
    renderWithQuery(<AppLayout />);
    const search = await screen.findByLabelText(/search servers/i);
    await userEvent.click(search);
    // The matches dropdown only renders once a query is typed (empty query
    // shows nothing to blur-close), so type before looking for the match.
    await userEvent.type(search, "a");
    const result = await screen.findByTestId(searchResultID("alpha"));
    expect(result).toBeInTheDocument();
    // Blur closes dropdown after 120ms
    search.blur();
    await waitFor(
      () => expect(screen.queryByTestId(searchResultID("alpha"))).not.toBeInTheDocument(),
      { timeout: 200 },
    );
  });

  it("search clears when navigating from dropdown", async () => {
    server.use(
      http.get("/fleet/servers", () =>
        HttpResponse.json(fleetServers(["alpha"])),
      ),
    );
    renderWithQuery(<AppLayout />);
    const search = await screen.findByLabelText(/search servers/i) as HTMLInputElement;
    await userEvent.type(search, "alpha");
    expect(search.value).toBe("alpha");
    const result = await screen.findByTestId(searchResultID("alpha"));
    await userEvent.click(within(result).getByRole("link"));
    await waitFor(() => expect(search.value).toBe(""));
  });

  it("search escape key closes dropdown", async () => {
    server.use(
      http.get("/fleet/servers", () =>
        HttpResponse.json(fleetServers(["alpha"])),
      ),
    );
    renderWithQuery(<AppLayout />);
    const search = await screen.findByLabelText(/search servers/i);
    await userEvent.type(search, "a");
    const result = await screen.findByTestId(searchResultID("alpha"));
    expect(result).toBeInTheDocument();
    await userEvent.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByTestId(searchResultID("alpha"))).not.toBeInTheDocument();
    });
  });

  it("notifications open and close with bell click", async () => {
    renderWithQuery(<AppLayout />);
    const bell = await screen.findByRole("button", { name: /notifications/i });
    // Dropdown closed initially
    expect(screen.queryByText("No recent activity.")).not.toBeInTheDocument();
    await userEvent.click(bell);
    // Dropdown open
    expect(screen.getByText("No recent activity.")).toBeInTheDocument();
    await userEvent.click(bell);
    // Dropdown closed
    await waitFor(() => {
      expect(screen.queryByText("No recent activity.")).not.toBeInTheDocument();
    });
  });

  it("notifications badge shows unread count", async () => {
    renderWithQuery(<AppLayout />);
    await screen.findByRole("button", { name: /notifications/i });
    // Initially no badge (unread count is 0)
    expect(screen.queryByText(/^\d+$/)).not.toBeInTheDocument();
  });

  it("logout button navigates to login", async () => {
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      writable: true,
      value: { ...originalLocation, assign: vi.fn() },
    });
    try {
      server.use(
        http.post("/auth/logout", () => new HttpResponse(null, { status: 204 })),
      );
      renderWithQuery(<AppLayout />);
      await waitFor(async () => expect((await sidebarNav()).getByRole("link", { name: /Dashboard/i })).toBeInTheDocument());
      const logoutBtn = screen.getAllByRole("button", { name: /sign out/i })[0];
      await userEvent.click(logoutBtn);
      await waitFor(() => {
        expect(window.location.assign).toHaveBeenCalledWith("/login");
      });
    } finally {
      Object.defineProperty(window, "location", { value: originalLocation });
    }
  });

  it("sidebar nav item has active styling when exact match", async () => {
    routerMocks.useLocation = () => ({ pathname: "/servers" } as ReturnType<typeof import("@tanstack/react-router").useLocation>);
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser())),
    );
    renderWithQuery(<AppLayout />);
    // The /servers link should carry the active-state class (Sidebar marks
    // the active item with "text-primary" — HeroUI's rebuild no longer
    // relies on TanStack Router's `activeProps`/`.active` class hook, since
    // Sidebar now computes active state itself from useLocation()).
    await waitFor(async () => {
      const link = (await sidebarNav()).getByRole("link", { name: /^Servers$/i });
      expect(link.className).toContain("text-primary");
    });
  });

  it("desktop sidebar hidden on mobile", async () => {
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser())),
    );
    renderWithQuery(<AppLayout />);
    await screen.findByRole("link", { name: /Dashboard/i });
    // AppShell renders a wrapper div with data-testid="app-shell-sidebar"
    // that holds the "hidden lg:flex" classes.
    const desktopSidebar = screen.getByTestId("app-shell-sidebar");
    expect(desktopSidebar).toHaveClass("hidden");
  });

  it("system logs nav only visible with * permission", async () => {
    server.use(
      http.get("/users/me", () =>
        HttpResponse.json(makeUser({ role: "operator" })),
      ),
    );
    renderWithQuery(<AppLayout />);
    // Operator doesn't have * permission
    await waitFor(() => {
      expect(screen.queryByRole("link", { name: /System logs/i })).not.toBeInTheDocument();
    });
  });

  it("renders gameplane branding and logo", async () => {
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser())),
    );
    renderWithQuery(<AppLayout />);
    const root = await sidebarRoot();
    expect(await root.findByText("gameplane")).toBeInTheDocument();
  });

  it("breadcrumb builds from pathname", async () => {
    routerMocks.useLocation = () => ({ pathname: "/servers/alpha" } as ReturnType<typeof import("@tanstack/react-router").useLocation>);
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser())),
    );
    renderWithQuery(<AppLayout />);
    const root = await sidebarRoot();
    await waitFor(() => {
      expect(root.getByText("gameplane")).toBeInTheDocument();
    });
    // Breadcrumb should show: gameplane > alpha (server detail pages drop
    // the "Servers" ancestor crumb per design dPP50/F9pUrx).
    const breadcrumbs = screen.getAllByText(/gameplane|alpha/);
    expect(breadcrumbs.length).toBeGreaterThan(0);
  });

  it("appearance toggle switches theme and persists it", async () => {
    localStorage.clear();
    document.documentElement.classList.remove("dark", "light");
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser())),
    );
    renderWithQuery(<AppLayout />);
    await screen.findByRole("link", { name: /Dashboard/i });

    const darkBtn = screen.getAllByRole("button", { name: /^Dark$/i })[0];
    await userEvent.click(darkBtn);

    await waitFor(() => {
      expect(document.documentElement.classList.contains("dark")).toBe(true);
      expect(document.documentElement.dataset.theme).toBe("dark");
      expect(localStorage.getItem("gameplane-theme")).toBe("dark");
    });
  });

  it("redirects to /login when useMe resolves a 401", async () => {
    const originalLocation = window.location;
    Object.defineProperty(window, "location", {
      writable: true,
      value: { ...originalLocation, assign: vi.fn() },
    });
    try {
      server.use(http.get("/users/me", () => new HttpResponse("unauthorized", { status: 401 })));
      renderWithQuery(<AppLayout />);
      await waitFor(() => {
        expect(window.location.assign).toHaveBeenCalledWith("/login");
      });
    } finally {
      Object.defineProperty(window, "location", { value: originalLocation });
    }
  });

  it("persists the appearance choice through the preferences pipeline when preferences are loaded", async () => {
    window.localStorage.setItem(
      THEME_PREFS_STORAGE_KEY,
      JSON.stringify({ ...DEFAULT_THEME_PREFERENCES, appearanceMode: "system" }),
    );
    let captured: unknown = null;
    server.use(
      http.get("/users/me", () => HttpResponse.json(makeUser())),
      http.put("/users/me/preferences", async ({ request }) => {
        captured = await request.json();
        return HttpResponse.json({ ...DEFAULT_THEME_PREFERENCES, appearanceMode: "dark" });
      }),
    );
    try {
      renderWithQuery(<AppLayout />);
      await screen.findByRole("link", { name: /Dashboard/i });
      const darkBtn = screen.getAllByRole("button", { name: /^Dark$/i })[0];
      await userEvent.click(darkBtn);
      await waitFor(() => expect(captured).not.toBeNull());
      expect(captured).toMatchObject({ appearanceMode: "dark" });
    } finally {
      window.localStorage.removeItem(THEME_PREFS_STORAGE_KEY);
    }
  });

  it("shows the safe-mode banner and its Open Appearance Settings button navigates to theme settings", async () => {
    window.sessionStorage.setItem(SAFE_MODE_SESSION_KEY, "1");
    try {
      server.use(http.get("/users/me", () => HttpResponse.json(makeUser())));
      renderWithQuery(<AppLayout />);
      expect(
        await screen.findByText("Safe Mode Active: Custom CSS is suspended."),
      ).toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: /Open Appearance Settings/i }));
      expect(navigateMock).toHaveBeenCalledWith({ to: "/settings/theme" });
    } finally {
      window.sessionStorage.removeItem(SAFE_MODE_SESSION_KEY);
    }
  });

  it("dismissing the safe-mode banner hides it for the session", async () => {
    window.sessionStorage.setItem(SAFE_MODE_SESSION_KEY, "1");
    try {
      server.use(http.get("/users/me", () => HttpResponse.json(makeUser())));
      renderWithQuery(<AppLayout />);
      await screen.findByText("Safe Mode Active: Custom CSS is suspended.");
      await userEvent.click(screen.getByRole("button", { name: /^Dismiss$/i }));
      await waitFor(() => {
        expect(
          screen.queryByText("Safe Mode Active: Custom CSS is suspended."),
        ).not.toBeInTheDocument();
      });
    } finally {
      window.sessionStorage.removeItem(SAFE_MODE_SESSION_KEY);
    }
  });

  it("Ctrl+Shift+Alt+T activates safe mode via the keyboard shortcut", async () => {
    window.sessionStorage.removeItem(SAFE_MODE_SESSION_KEY);
    try {
      server.use(http.get("/users/me", () => HttpResponse.json(makeUser())));
      renderWithQuery(<AppLayout />);
      await screen.findByRole("link", { name: /Dashboard/i });
      expect(
        screen.queryByText("Safe Mode Active: Custom CSS is suspended."),
      ).not.toBeInTheDocument();
      fireEvent.keyDown(window, { key: "t", ctrlKey: true, shiftKey: true, altKey: true });
      await waitFor(() => {
        expect(
          screen.getByText("Safe Mode Active: Custom CSS is suspended."),
        ).toBeInTheDocument();
      });
      expect(window.sessionStorage.getItem(SAFE_MODE_SESSION_KEY)).toBe("1");
    } finally {
      window.sessionStorage.removeItem(SAFE_MODE_SESSION_KEY);
    }
  });

  describe("telemetry notice", () => {
    function pendingNotice() {
      const posts: { action: string }[] = [];
      server.use(
        http.get("/admin/telemetry/notice", () =>
          HttpResponse.json({ pending: true, destination: { kind: "default", host: "telemetry.example.org" } }),
        ),
        http.post("/admin/telemetry/notice", async ({ request }) => {
          posts.push((await request.json()) as { action: string });
          return new HttpResponse(null, { status: 204 });
        }),
      );
      return posts;
    }

    it("shows the notice to users with config:manage and opens Telemetry settings", async () => {
      const posts = pendingNotice();
      server.use(http.get("/users/me", () => HttpResponse.json(makeUser())));
      renderWithQuery(<AppLayout />);
      expect(await screen.findByText("Anonymous usage metrics are on for this install.")).toBeInTheDocument();
      await waitFor(() => expect(posts).toEqual([{ action: "seen" }]));
      await userEvent.click(screen.getByRole("button", { name: "Open Telemetry settings" }));
      expect(navigateMock).toHaveBeenCalledWith({ to: "/admin", search: { section: "telemetry" } });
    });

    it("does not mount the notice for users without config:manage", async () => {
      const posts = pendingNotice();
      server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ role: "viewer" }))));
      renderWithQuery(<AppLayout />);
      await screen.findByRole("link", { name: /Dashboard/i });
      expect(screen.queryByText("Anonymous usage metrics are on for this install.")).not.toBeInTheDocument();
      expect(posts).toEqual([]);
    });
  });
});
