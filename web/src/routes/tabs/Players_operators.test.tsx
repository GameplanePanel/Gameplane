import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { renderWithQuery } from "@/test/render";
import { makePlayers } from "@/test/factories";
import { server } from "@/test/server";
import { PlayersTab } from "./Players";

const supported = makePlayers({ capabilities: { kick: false, ban: false, unban: false, operators: true } });

function renderPlayers(permissions = ["servers:read"], access = { isOwner: false, isCollaborator: false }) {
  return renderWithQuery(
    <ResourceTargetProvider target={{ cluster: "remote", namespace: "team", name: "alpha" }} access={{ canWrite: false, canControl: false, canConsole: false, canDelete: false, ...access, permissions }}>
      <PlayersTab name="alpha" ns="team" />
    </ResourceTargetProvider>,
  );
}

describe("configured operators", () => {
  it("shows offline identities to readers and refreshes the list in the selected scope", async () => {
    let refreshed = false;
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.json(supported)),
      http.get("/servers/alpha/players/operators", ({ request }) => {
        const url = new URL(request.url);
        expect(url.searchParams.get("cluster")).toBe("remote");
        expect(url.searchParams.get("namespace")).toBe("team");
        return HttpResponse.json(refreshed ? [{ name: "NewOperator" }] : [{ name: "OfflineOperator", uuid: "operator-uuid" }, { name: "", uuid: "uuid-only" }]);
      }),
    );
    renderPlayers();
    const list = await screen.findByRole("region", { name: "Operators" });
    expect(await within(list).findByText("OfflineOperator")).toBeInTheDocument();
    expect(within(list).getByText("operator-uuid")).toBeInTheDocument();
    expect(within(list).getByText("uuid-only")).toBeInTheDocument();
    const card = screen.getAllByText("Operators").find((element) => element.closest('[data-slot="card"]'))!.closest('[data-slot="card"]') as HTMLElement;
    expect(within(card).getByText("2")).toBeInTheDocument();
    expect(within(list).queryByRole("button")).not.toBeInTheDocument();
    refreshed = true;
    await userEvent.click(screen.getByRole("button", { name: "Refresh players" }));
    expect(await within(list).findByText("NewOperator")).toBeInTheDocument();
    expect(within(list).queryByText("OfflineOperator")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /Operators \(1\)/ }));
    expect(screen.queryByRole("region", { name: "Operators" })).not.toBeInTheDocument();
  });

  it("distinguishes loading operators from an empty list", async () => {
    let release!: () => void;
    const ready = new Promise<void>((resolve) => { release = resolve; });
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.json(supported)),
      http.get("/servers/alpha/players/operators", async () => { await ready; return HttpResponse.json([]); }),
    );
    renderPlayers();
    expect(await screen.findByText("Loading operators…")).toBeInTheDocument();
    expect(screen.queryByText("No operators configured.")).not.toBeInTheDocument();
    await act(async () => { release(); await ready; });
    expect(await screen.findByText("No operators configured.")).toBeInTheDocument();
  });

  it("replaces stale identities with an error and can retry through Refresh", async () => {
    let failed = false;
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.json(supported)),
      http.get("/servers/alpha/players/operators", () => failed ? HttpResponse.text("operator list unavailable", { status: 502 }) : HttpResponse.json([{ name: "OfflineOperator" }])),
    );
    renderPlayers();
    await screen.findByText("OfflineOperator");
    failed = true;
    await userEvent.click(screen.getByRole("button", { name: "Refresh players" }));
    expect(await screen.findByText("Could not load operators. Refresh to try again.")).toBeInTheDocument();
    expect(screen.queryByText("OfflineOperator")).not.toBeInTheDocument();
    expect(screen.queryByText("No operators configured.")).not.toBeInTheDocument();
    failed = false;
    await userEvent.click(screen.getByRole("button", { name: "Refresh players" }));
    expect(await screen.findByText("OfflineOperator")).toBeInTheDocument();
  });

  it("loads operators independently when the initial live RCON snapshot fails", async () => {
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.text("upstream unavailable", { status: 502 })),
      http.get("/servers/alpha/players/operators", () => HttpResponse.json([{ name: "OfflineOperator" }])),
    );
    renderPlayers();
    expect(await screen.findByText("OfflineOperator")).toBeInTheDocument();
    expect(screen.getByText("Player information is unavailable.")).toBeInTheDocument();
  });

  it.each([false, undefined])("does not query a known unsupported capability (%s)", async (operators) => {
    let requests = 0;
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.json(makePlayers({ capabilities: { kick: false, ban: false, unban: false, operators } }))),
      http.get("/servers/alpha/players/operators", () => { requests++; return HttpResponse.json([]); }),
    );
    renderPlayers();
    await screen.findByText("2 / 20 online");
    expect(requests).toBe(0);
    expect(screen.queryByText("Operators")).not.toBeInTheDocument();
  });

  it.each([401, 403])("does not probe after a denied snapshot (%s)", async (status) => {
    let requests = 0;
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.text("denied", { status })),
      http.get("/servers/alpha/players/operators", () => { requests++; return HttpResponse.json([]); }),
    );
    renderPlayers();
    await screen.findByText("Player information is unavailable.");
    expect(requests).toBe(0);
    expect(screen.queryByText("Operators")).not.toBeInTheDocument();
  });

  it.each([404, 501])("hides an unsupported operator probe (%s)", async (status) => {
    let requests = 0;
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.text("upstream unavailable", { status: 502 })),
      http.get("/servers/alpha/players/operators", () => { requests++; return HttpResponse.text("unsupported", { status }); }),
    );
    renderPlayers();
    await waitFor(() => expect(requests).toBe(1));
    await waitFor(() => expect(screen.queryByText("Loading operators…")).not.toBeInTheDocument());
    expect(screen.queryByText("Operators")).not.toBeInTheDocument();
    expect(screen.queryByText("Could not load operators. Refresh to try again.")).not.toBeInTheDocument();
  });

  it("does not read operator identities without server read permission", async () => {
    let requests = 0;
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.json(supported)),
      http.get("/servers/alpha/players/operators", () => { requests++; return HttpResponse.json([]); }),
    );
    renderPlayers([]);
    await screen.findByText("2 / 20 online");
    expect(requests).toBe(0);
    expect(screen.queryByText("Operators")).not.toBeInTheDocument();
  });

  it.each(["owner", "collaborator"])("shows operators to a scoped %s without a server read role", async (role) => {
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.json(supported)),
      http.get("/servers/alpha/players/operators", () => HttpResponse.json([{ name: "OfflineOperator" }])),
    );
    renderPlayers([], { isOwner: role === "owner", isCollaborator: role === "collaborator" });
    expect(await screen.findByText("OfflineOperator")).toBeInTheDocument();
  });

  it.each([404, 501])("allows an explicit refresh to discover operators after an unsupported response (%s)", async (status) => {
    let available = false;
    let requests = 0;
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.json(supported)),
      http.get("/servers/alpha/players/operators", () => {
        requests++;
        return available ? HttpResponse.json([{ name: "OfflineOperator" }]) : HttpResponse.text("unsupported", { status });
      }),
    );
    renderPlayers();
    await waitFor(() => expect(requests).toBe(1));
    await waitFor(() => expect(screen.queryByText("Loading operators…")).not.toBeInTheDocument());
    expect(screen.queryByText("Operators")).not.toBeInTheDocument();
    available = true;
    await userEvent.click(screen.getByRole("button", { name: "Refresh players" }));
    expect(await screen.findByText("OfflineOperator")).toBeInTheDocument();
    expect(requests).toBe(2);
  });
});
