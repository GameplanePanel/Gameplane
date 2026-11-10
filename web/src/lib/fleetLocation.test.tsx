import { afterEach, describe, expect, it } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Link,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router";
import { useFleetLocation } from "./fleet";
import { BackupsPage } from "@/routes/Backups";
import { renderWithQuery } from "@/test/render";
import { server } from "@/test/server";

function LocationControls() {
  const [location, choose] = useFleetLocation();
  return <>
    <output data-testid="location">{location || "All clusters"}</output>
    <button onClick={() => choose("remote")}>Choose remote</button>
    <button onClick={() => choose("")}>Clear location</button>
    <Link to="/servers">Servers</Link>
  </>;
}

function mountRouter(history: ReturnType<typeof createMemoryHistory>, backupsComponent = LocationControls) {
  const root = createRootRoute({ component: () => <><Link to="/backups">Backups navigation</Link><Outlet /></> });
  const validateSearch = (search: Record<string, unknown>) => search;
  const servers = createRoute({ getParentRoute: () => root, path: "/servers", component: LocationControls, validateSearch });
  const backups = createRoute({ getParentRoute: () => root, path: "/backups", component: backupsComponent, validateSearch });
  const router = createRouter({ routeTree: root.addChildren([servers, backups]), history, defaultPendingMinMs: 0 });
  const rendered = renderWithQuery(<RouterProvider router={router} />);
  return { router, ...rendered };
}

afterEach(() => { window.history.replaceState(null, "", "/"); });

describe("fleet location follows router navigation", () => {
  it("resets the same mounted server list when its sidebar link clears the query", async () => {
    window.history.replaceState(null, "", "/servers?cluster=remote");
    const history = createMemoryHistory({ initialEntries: ["/servers?cluster=remote"] });
    mountRouter(history);
    expect(await screen.findByTestId("location")).toHaveTextContent("remote");
    await userEvent.click(screen.getByRole("link", { name: "Servers" }));
    await waitFor(() => expect(history.location.href).toBe("/servers"));
    expect(screen.getByTestId("location")).toHaveTextContent("All clusters");
  });

  it("tracks back and forward and replaces a filter without dropping other search fields", async () => {
    const history = createMemoryHistory({
      initialEntries: ["/backups?tab=schedules&cluster=local&note=keep", "/backups?tab=restores&cluster=remote&note=keep"],
      initialIndex: 1,
    });
    const { router } = mountRouter(history);
    expect(await screen.findByTestId("location")).toHaveTextContent("remote");
    act(() => history.back());
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("local"));
    act(() => history.forward());
    await waitFor(() => expect(screen.getByTestId("location")).toHaveTextContent("remote"));
    await userEvent.click(screen.getByRole("button", { name: "Clear location" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ tab: "restores", note: "keep" }));
    expect(screen.getByTestId("location")).toHaveTextContent("All clusters");
    expect(history.length).toBe(2);
    await userEvent.click(screen.getByRole("button", { name: "Choose remote" }));
    await waitFor(() => expect(router.state.location.search).toEqual({ tab: "restores", cluster: "remote", note: "keep" }));
    expect(history.length).toBe(2);
  });

  it("treats an empty or non-string location as all clusters", async () => {
    const history = createMemoryHistory({ initialEntries: ["/servers?cluster="] });
    const { router } = mountRouter(history);
    expect(await screen.findByTestId("location")).toHaveTextContent("All clusters");
    act(() => history.push("/servers?cluster=%5B%22remote%22%5D"));
    await waitFor(() => expect(router.state.location.search.cluster).toEqual(["remote"]));
    expect(screen.getByTestId("location")).toHaveTextContent("All clusters");
  });

  it("preserves Backups tab and scope when filtering, then resets and restores both on navigation", async () => {
    const scheduleScopes: Array<string | null> = [];
    const result = { items: [], scopes: [{ cluster: "local", namespace: "games" }, { cluster: "remote", namespace: "games" }], partial: false, issues: [], totalReturned: 0 };
    server.use(
      http.get("/fleet/servers", () => HttpResponse.json(result)),
      http.get("/fleet/backups", () => HttpResponse.json(result)),
      http.get("/fleet/restores", () => HttpResponse.json(result)),
      http.get("/fleet/schedules", ({ request }) => {
        scheduleScopes.push(new URL(request.url).searchParams.get("cluster"));
        return HttpResponse.json(result);
      }),
    );
    window.history.replaceState(null, "", "/backups");
    const history = createMemoryHistory({ initialEntries: ["/backups"] });
    const { router, unmount } = mountRouter(history, BackupsPage);
    try {
      await userEvent.click(await screen.findByRole("tab", { name: "Schedules" }));
      await waitFor(() => expect(router.state.location.search.tab).toBe("schedules"));
      await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
      expect(screen.queryByRole("button", { name: /Filter by phase/ })).not.toBeInTheDocument();
      await userEvent.click(screen.getByRole("button", { name: /Filter by cluster/ }));
      await userEvent.click(await screen.findByRole("option", { name: "remote" }));
      await userEvent.click(screen.getByRole("button", { name: "Apply" }));
      await waitFor(() => expect(scheduleScopes).toContain("remote"));
      expect(screen.getByRole("tab", { name: "Schedules" })).toHaveAttribute("aria-selected", "true");
      expect(router.state.location.search).toMatchObject({ tab: "schedules", cluster: "remote" });
      expect(new URL(history.location.href, "http://localhost").searchParams.get("tab")).toBe("schedules");

      await userEvent.click(screen.getByRole("link", { name: "Backups navigation" }));
      await waitFor(() => expect(router.state.location.search).toEqual({}));
      expect(screen.getByRole("tab", { name: "Backups" })).toHaveAttribute("aria-selected", "true");
      await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
      expect(screen.getByRole("button", { name: /Filter by cluster/ })).toHaveTextContent("All clusters");
      screen.getByRole("button", { name: "Clear" }).focus();
      await userEvent.keyboard("{Escape}");
      expect(screen.queryByRole("button", { name: /Filter by cluster/ })).not.toBeInTheDocument();

      act(() => history.back());
      await waitFor(() => expect(screen.getByRole("tab", { name: "Schedules" })).toHaveAttribute("aria-selected", "true"));
      await userEvent.click(screen.getByRole("button", { name: /^Filter\s*1$/ }));
      expect(screen.getByRole("button", { name: /Filter by cluster/ })).toHaveTextContent("remote");
      screen.getByRole("button", { name: "Clear" }).focus();
      await userEvent.keyboard("{Escape}");
      expect(screen.queryByRole("button", { name: /Filter by cluster/ })).not.toBeInTheDocument();
      expect(router.state.location.search).toMatchObject({ tab: "schedules", cluster: "remote" });
      act(() => history.forward());
      await waitFor(() => expect(screen.getByRole("tab", { name: "Backups" })).toHaveAttribute("aria-selected", "true"));
      await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
      expect(screen.getByRole("button", { name: /Filter by cluster/ })).toHaveTextContent("All clusters");
      screen.getByRole("button", { name: "Clear" }).focus();
      await userEvent.keyboard("{Escape}");
      expect(screen.queryByRole("button", { name: /Filter by cluster/ })).not.toBeInTheDocument();
    } finally {
      unmount();
      history.destroy();
    }
  });
});
