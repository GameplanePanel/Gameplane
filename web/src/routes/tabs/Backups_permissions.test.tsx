import { afterEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import { focusManager, onlineManager } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { makeBackup } from "@/test/factories";
import { BackupsTab } from "./Backups";

const readPermissions = ["backups:read", "schedules:read", "destinations:read"];

function view(permissions: string[], cluster = "east", namespace = "games") {
  return <ResourceTargetProvider target={{ cluster, namespace, name: "alpha" }} access={{ canWrite: false, canControl: false, canConsole: false, canDelete: false, isOwner: false, isCollaborator: false, permissions }}><BackupsTab name="alpha" ns={namespace} /></ResourceTargetProvider>;
}

afterEach(() => {
  vi.useRealTimers();
  focusManager.setFocused(undefined);
  onlineManager.setOnline(true);
});

describe("BackupsTab scoped read access", () => {
  it("does not request any protected lists without their permissions", async () => {
    const requests: string[] = [];
    for (const path of ["backups", "schedules", "restores", "backup-destinations"]) {
      server.use(http.get(`/${path}`, () => {
        requests.push(path);
        return HttpResponse.json({ items: [] });
      }));
    }
    const { client } = renderWithQuery(view(["servers:read"]));
    expect(screen.getByText("You do not have permission to view backups in this cluster and namespace.")).toBeInTheDocument();
    expect(screen.getByText("You do not have permission to view schedules in this cluster and namespace.")).toBeInTheDocument();
    expect(screen.getByText("You do not have permission to view restores in this cluster and namespace.")).toBeInTheDocument();
    await act(() => client.invalidateQueries());
    expect(requests).toEqual([]);
    expect(screen.getByRole("button", { name: /Back up now/i })).toBeDisabled();
  });

  it("uses the schedule permission independently and keeps the exact resource scope", async () => {
    const requests: URL[] = [];
    server.use(http.get("/schedules", ({ request }) => {
      requests.push(new URL(request.url));
      return HttpResponse.json({ items: [] });
    }));
    renderWithQuery(view(["schedules:read"], "west", "other-games"));
    expect(await screen.findByText("No schedules yet.")).toBeInTheDocument();
    expect(requests).toHaveLength(1);
    expect(requests[0].searchParams.get("cluster")).toBe("west");
    expect(requests[0].searchParams.get("namespace")).toBe("other-games");
    expect(screen.queryByText("You do not have permission to view schedules in this cluster and namespace.")).not.toBeInTheDocument();
    expect(screen.getByText("You do not have permission to view restores in this cluster and namespace.")).toBeInTheDocument();
  });

  it.each([
    ["backups", 401], ["backups", 403],
    ["schedules", 401], ["schedules", 403],
    ["restores", 401], ["restores", 403],
  ] as const)("stops automatic requests after %s returns %i until access changes", async (kind, status) => {
    let requests = 0;
    let denied = true;
    server.use(http.get(`/${kind}`, () => {
      requests++;
      return denied ? HttpResponse.text("Forbidden", { status }) : HttpResponse.json({ items: [] });
    }));
    const rendered = renderWithQuery(view(readPermissions));
    const message = `You do not have permission to view ${kind} in this cluster and namespace.`;
    expect(await screen.findByText(message)).toBeInTheDocument();
    expect(requests).toBe(1);
    vi.useFakeTimers();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(16000);
      focusManager.setFocused(false);
      focusManager.setFocused(true);
      onlineManager.setOnline(false);
      onlineManager.setOnline(true);
      await rendered.client.invalidateQueries();
      await vi.advanceTimersByTimeAsync(1000);
    });
    vi.useRealTimers();
    rendered.rerender(view([...readPermissions]));
    expect(requests).toBe(1);
    denied = false;
    rendered.rerender(view([...readPermissions, "servers:write"]));
    await waitFor(() => expect(requests).toBe(2));
    await waitFor(() => expect(screen.queryByText(message)).not.toBeInTheDocument());
  });

  it("hides cached backup rows when access is revoked", async () => {
    let requests = 0;
    server.use(http.get("/backups", () => {
      requests++;
      return HttpResponse.json({ items: [makeBackup({ metadata: { name: "private-backup" }, spec: { serverRef: { name: "alpha" } } })] });
    }));
    const rendered = renderWithQuery(view(readPermissions));
    expect(await screen.findByText("private-backup")).toBeInTheDocument();
    rendered.rerender(view(["schedules:read"]));
    expect(screen.queryByText("private-backup")).not.toBeInTheDocument();
    await act(() => rendered.client.invalidateQueries());
    expect(requests).toBe(1);
  });
});
