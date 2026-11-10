import { describe, it, expect, vi } from "vitest";
import type { ReactNode } from "react";
import { screen, within } from "@testing-library/react";
import { renderWithQuery } from "@/test/render";
import { buildCrumbs, Breadcrumbs } from "./Breadcrumbs";

// Mock TanStack Router
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
  useLocation: () => ({ pathname: "/" } as ReturnType<typeof import("@tanstack/react-router").useLocation>),
}));

describe("buildCrumbs", () => {
  it("keeps fleet nodes separate from cluster details", () => {
    expect(buildCrumbs("/nodes")).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Nodes", to: "/nodes" },
    ]);
    expect(buildCrumbs("/clusters/remote-demo")).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Clusters", to: "/clusters" },
      { label: "remote-demo", to: "/clusters/remote-demo" },
      { label: "Overview" },
    ]);
  });

  it.each(["nodes", "configuration"])("includes the active cluster %s tab", (tab) => {
    const crumbs = buildCrumbs("/clusters/east", undefined, tab);
    expect(crumbs.at(-1)?.label).toBe(tab === "nodes" ? "Nodes" : "Configuration");
    expect(crumbs.at(-2)).toEqual({ label: "east", to: "/clusters/east" });
  });
  it("returns home and dashboard crumb for root path", () => {
    const crumbs = buildCrumbs("/");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Dashboard" },
    ]);
  });

  it("builds breadcrumbs for a single route segment", () => {
    const crumbs = buildCrumbs("/servers");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Servers", to: "/servers" },
    ]);
  });

  it("builds breadcrumbs for multiple route segments", () => {
    // Server detail pages drop the "Servers" ancestor crumb per design
    // (dPP50, F9pUrx) — see the comment in buildCrumbs.
    const crumbs = buildCrumbs("/servers/my-server");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "my-server", to: "/servers/my-server" },
    ]);
  });

  it("maps known labels from the label map", () => {
    const crumbs = buildCrumbs("/admin/audit");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Settings", to: "/admin" },
      { label: "Audit log", to: "/admin/audit" },
    ]);
  });

  it("uses path segment as label for unmapped routes", () => {
    const crumbs = buildCrumbs("/unknown/path");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "unknown", to: "/unknown" },
      { label: "path", to: "/unknown/path" },
    ]);
  });

  it("handles /modules route", () => {
    const crumbs = buildCrumbs("/modules");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Modules", to: "/modules" },
    ]);
  });

  it("handles /cluster route", () => {
    const crumbs = buildCrumbs("/cluster");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Cluster", to: "/cluster" },
    ]);
  });

  it("handles /users route", () => {
    const crumbs = buildCrumbs("/users");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Users & RBAC", to: "/users" },
    ]);
  });

  it("handles /backups route", () => {
    const crumbs = buildCrumbs("/backups");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Backups", to: "/backups" },
    ]);
  });

  it("handles /admin/logs route", () => {
    const crumbs = buildCrumbs("/admin/logs");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Settings", to: "/admin" },
      { label: "System logs", to: "/admin/logs" },
    ]);
  });

  it("handles /new route", () => {
    const crumbs = buildCrumbs("/new");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "New", to: "/new" },
    ]);
  });

  it("ignores trailing slashes", () => {
    const crumbs = buildCrumbs("/servers/");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Servers", to: "/servers" },
    ]);
  });

  it("collapses /settings/theme into a single Settings crumb, matching /admin", () => {
    const crumbs = buildCrumbs("/settings/theme");
    expect(crumbs).toEqual([
      { label: "gameplane", to: "/" },
      { label: "Settings", to: "/settings/theme" },
    ]);
  });
});

describe("Breadcrumbs component", () => {
  it("renders home link and dashboard crumb", () => {
    const crumbs = buildCrumbs("/");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    const nav = screen.getByRole("navigation");
    expect(nav).toBeInTheDocument();

    const homeLink = screen.getByRole("link", { name: /gameplane/i });
    expect(homeLink).toHaveAttribute("href", "/");

    const dashboardCrumb = within(nav).getByText("Dashboard");
    expect(dashboardCrumb).toBeInTheDocument();
  });

  it("renders navigation links for non-final crumbs", () => {
    // Uses /admin/audit rather than a server detail path: server detail
    // pages drop the "Servers" ancestor crumb (dPP50, F9pUrx), leaving no
    // middle crumb to exercise here.
    const crumbs = buildCrumbs("/admin/audit");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    // Find links by their href attribute
    const homeLink = screen.getByRole("link", { name: /gameplane/i });
    expect(homeLink).toHaveAttribute("href", "/");

    const settingsLink = screen.getByRole("link", { name: /settings/i });
    expect(settingsLink).toHaveAttribute("href", "/admin");

    // Last item should not be a link
    const auditLogText = screen.getByText("Audit log");
    expect(auditLogText.closest("a")).not.toBeInTheDocument();
  });

  it("marks the last crumb with aria-current='page'", () => {
    const crumbs = buildCrumbs("/servers/my-server");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    const nav = screen.getByRole("navigation");
    const lastCrumb = within(nav).getByText("my-server");

    // Last item should have aria-current='page'
    expect(lastCrumb).toHaveAttribute("aria-current", "page");
  });

  it("renders the dashboard crumb with aria-current when on root", () => {
    const crumbs = buildCrumbs("/");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    const nav = screen.getByRole("navigation");
    const dashboardCrumb = within(nav).getByText("Dashboard");

    // Dashboard (last) should have aria-current='page'
    expect(dashboardCrumb).toHaveAttribute("aria-current", "page");
  });

  it("renders custom route labels from label map", () => {
    const crumbs = buildCrumbs("/admin/audit");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    expect(screen.getByRole("link", { name: /gameplane/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /settings/i })).toBeInTheDocument();
    expect(screen.getByText("Audit log")).toBeInTheDocument();
  });

  it("renders unmapped labels as-is", () => {
    const crumbs = buildCrumbs("/unknown/path");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    expect(screen.getByRole("link", { name: /gameplane/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /unknown/i })).toBeInTheDocument();
    expect(screen.getByText("path")).toBeInTheDocument();
  });

  it("renders all crumbs in order", () => {
    const crumbs = buildCrumbs("/admin/audit");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    const nav = screen.getByRole("navigation");
    expect(within(nav).getByText("gameplane")).toBeInTheDocument();
    expect(within(nav).getByText("Settings")).toBeInTheDocument();
    expect(within(nav).getByText("Audit log")).toBeInTheDocument();
  });

  it("accepts custom crumb items", () => {
    const customCrumbs = [
      { label: "Home", to: "/" },
      { label: "Custom", to: "/custom" },
      { label: "Page" },
    ];
    renderWithQuery(<Breadcrumbs items={customCrumbs} />);

    const homeLink = screen.getByRole("link", { name: /home/i });
    expect(homeLink).toHaveAttribute("href", "/");

    const customLink = screen.getByRole("link", { name: /custom/i });
    expect(customLink).toHaveAttribute("href", "/custom");

    expect(screen.getByText("Page")).toBeInTheDocument();
  });

  it("handles empty items array gracefully", () => {
    renderWithQuery(<Breadcrumbs items={[]} />);

    const nav = screen.getByRole("navigation");
    expect(nav).toBeInTheDocument();
  });

  it("renders chevron separators between crumbs", () => {
    const crumbs = buildCrumbs("/servers/my-server");
    const { container } = renderWithQuery(<Breadcrumbs items={crumbs} />);

    // Check that SVG chevron icons are present (LucideReact renders SVG)
    const chevrons = container.querySelectorAll("svg");
    // Should have chevrons separating the items
    expect(chevrons.length).toBeGreaterThan(0);
  });

  it("does not render href on last crumb", () => {
    const crumbs = buildCrumbs("/servers/my-server");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    const lastCrumb = screen.getByText("my-server");
    expect(lastCrumb.closest("a")).not.toBeInTheDocument();
  });

  it("renders href on non-last crumbs", () => {
    // Uses /admin/audit rather than a server detail path: server detail
    // pages drop the "Servers" ancestor crumb (dPP50, F9pUrx), leaving no
    // middle crumb to exercise here.
    const crumbs = buildCrumbs("/admin/audit");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    const homeLink = screen.getByRole("link", { name: /gameplane/i });
    expect(homeLink).toHaveAttribute("href", "/");

    const settingsLink = screen.getByRole("link", { name: /settings/i });
    expect(settingsLink).toHaveAttribute("href", "/admin");
  });

  it("handles single crumb without navigation", () => {
    const singleCrumb = [{ label: "Home" }];
    renderWithQuery(<Breadcrumbs items={singleCrumb} />);

    expect(screen.getByText("Home")).toBeInTheDocument();
    expect(screen.getByText("Home")).toHaveAttribute("aria-current", "page");
  });

  it("renders link with to property on non-last item", () => {
    // Uses /admin/audit rather than a server detail path: server detail
    // pages drop the "Servers" ancestor crumb (dPP50, F9pUrx), leaving no
    // middle crumb to exercise here.
    const crumbs = buildCrumbs("/admin/audit");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    const settingsLink = screen.getByRole("link", { name: /settings/i });
    expect(settingsLink).toHaveAttribute("href", "/admin");
  });

  it("maintains label map translations in breadcrumbs", () => {
    const crumbs = buildCrumbs("/users");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    expect(screen.getByText("Users & RBAC")).toBeInTheDocument();
  });

  it("handles complex nested paths", () => {
    const crumbs = buildCrumbs("/admin/audit");
    renderWithQuery(<Breadcrumbs items={crumbs} />);

    // Verify structure
    const nav = screen.getByRole("navigation");
    expect(within(nav).getByText("gameplane")).toBeInTheDocument();
    expect(within(nav).getByText("Settings")).toBeInTheDocument();
    expect(within(nav).getByText("Audit log")).toBeInTheDocument();

    // Verify last item has aria-current
    const auditLog = within(nav).getByText("Audit log");
    expect(auditLog).toHaveAttribute("aria-current", "page");
  });
});
