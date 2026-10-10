import { ResourceTargetProvider } from "@/lib/resourceTarget";
import type { ReactElement } from "react";
import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { makePlayers, makePlayerEntry } from "@/test/factories";
import { PlayersTab } from "./Players";

describe("PlayersTab", () => {
  it("shows the online count and player list", async () => {
    server.use(
      http.get("/servers/alpha/players", () =>
        HttpResponse.json(makePlayers({ online: 2, max: 20, players: ["alice", "bob"] })),
      ),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    expect(await screen.findByText(/2 \/ 20 online/)).toBeInTheDocument();
    expect(screen.getByText("alice")).toBeInTheDocument();
    expect(screen.getByText("bob")).toBeInTheDocument();
  });

  it("shows loading state initially", () => {
    server.use(
      http.get("/servers/alpha/players", async () => {
        await new Promise((r) => setTimeout(r, 50));
        return HttpResponse.json(makePlayers());
      }),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    expect(screen.getByText(/Loading/i)).toBeInTheDocument();
  });

  it("offers a refresh button", async () => {
    let callCount = 0;
    server.use(
      http.get("/servers/alpha/players", () => {
        callCount++;
        return HttpResponse.json(makePlayers());
      }),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    await screen.findByText(/online/);
    expect(callCount).toBe(1);
    const btn = screen.getByTitle(/Refresh/i);
    await userEvent.click(btn);
    await waitFor(() => expect(callCount).toBeGreaterThan(1));
  });

  it("renders empty state when no players online", async () => {
    server.use(
      http.get("/servers/alpha/players", () =>
        HttpResponse.json(makePlayers({ online: 0, players: [] })),
      ),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    expect(await screen.findByText(/0 \/ 20 online/)).toBeInTheDocument();
  });

  it("hides unknown max player count (renders -1 as just online count)", async () => {
    server.use(
      http.get("/servers/alpha/players", () =>
        HttpResponse.json(makePlayers({ online: 5, max: -1, players: [] })),
      ),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    expect(await screen.findByText(/5 online/)).toBeInTheDocument();
    expect(screen.queryByText("-1")).not.toBeInTheDocument();
    expect(screen.queryByText(/5 \/ -1/)).not.toBeInTheDocument();
  });

  it("renders an unknown player count (-1) as — and 'Player count unknown'", async () => {
    server.use(
      http.get("/servers/alpha/players", () =>
        HttpResponse.json(makePlayers({ online: -1, max: -1, players: [] })),
      ),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    expect(await screen.findByText("Player count unknown")).toBeInTheDocument();
    // The Whitelisted/Banned stat tiles also render "—" while their own
    // queries are pending, so scope to the Online tile specifically instead
    // of asserting on the page as a whole.
    const onlineCard = screen.getByText("Online").closest('[data-slot="card"]') as HTMLElement;
    expect(within(onlineCard).getByText("—")).toBeInTheDocument();
    expect(screen.queryByText("-1")).not.toBeInTheDocument();
    expect(screen.queryByText(/-1 online/)).not.toBeInTheDocument();
  });

  it("lists the whitelist and adds an entry", async () => {
    const added: string[] = [];
    server.use(
      http.get("/servers/alpha/players", () => HttpResponse.json(makePlayers())),
      http.get("/servers/alpha/players/whitelist", () =>
        HttpResponse.json(["alice", "carol"]),
      ),
      http.post("/servers/alpha/players/whitelist/add", async ({ request }) => {
        const b = (await request.json()) as { name?: string };
        if (b.name) added.push(b.name);
        return HttpResponse.json({ ok: true });
      }),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    // The collapsed Whitelist section advertises the count from the query.
    const toggle = await screen.findByText(/Whitelist \(2\)/);
    await userEvent.click(toggle);
    const input = await screen.findByPlaceholderText(/Add player to whitelist/i);
    await userEvent.type(input, "dave");
    const addButton = screen.getByRole("button", { name: /Add/i });
    await userEvent.click(addButton);
    await waitFor(() => expect(added).toContain("dave"));
  });

  it("shows the display name and faction for a structured entry", async () => {
    const entry = makePlayerEntry();
    server.use(
      http.get("/servers/alpha/players", () =>
        HttpResponse.json(makePlayers({ online: 1, players: [entry.steamId], entries: [entry] })),
      ),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    expect(await screen.findByText("Pilot_Vance")).toBeInTheDocument();
    expect(screen.getByText("Boscali")).toBeInTheDocument();
    expect(screen.queryByText(entry.steamId)).not.toBeInTheDocument();
  });

  it("falls back to the raw Steam ID when an entry has no display name", async () => {
    const entry = makePlayerEntry({ displayName: undefined });
    server.use(
      http.get("/servers/alpha/players", () =>
        HttpResponse.json(makePlayers({ online: 1, players: [entry.steamId], entries: [entry] })),
      ),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    expect(await screen.findByText(entry.steamId)).toBeInTheDocument();
    expect(screen.queryByText("unknown")).not.toBeInTheDocument();
  });

  it("sends the Steam ID, not the display name, when kicking a named row", async () => {
    const entry = makePlayerEntry();
    const kicked: string[] = [];
    server.use(
      http.get("/servers/alpha/players", () =>
        HttpResponse.json(makePlayers({ online: 1, players: [entry.steamId], entries: [entry] })),
      ),
      http.post("/servers/alpha/players/kick", async ({ request }) => {
        const b = (await request.json()) as { name?: string };
        if (b.name) kicked.push(b.name);
        return HttpResponse.json({ ok: true });
      }),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    await screen.findByText("Pilot_Vance");
    await userEvent.click(screen.getByRole("button", { name: "Kick" }));
    const kickButtons = await screen.findAllByRole("button", { name: "Kick" });
    await userEvent.click(kickButtons[kickButtons.length - 1]);
    await waitFor(() => expect(kicked).toEqual([entry.steamId]));
  });

  it("shows the Steam ID next to the display name in the kick confirmation", async () => {
    const entry = makePlayerEntry();
    server.use(
      http.get("/servers/alpha/players", () =>
        HttpResponse.json(makePlayers({ online: 1, players: [entry.steamId], entries: [entry] })),
      ),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    await screen.findByText("Pilot_Vance");
    await userEvent.click(screen.getByRole("button", { name: "Kick" }));
    expect(await screen.findByText(entry.steamId)).toBeInTheDocument();
  });

  it("shows the raw Steam ID once when the row has no display name", async () => {
    const entry = makePlayerEntry({ displayName: undefined });
    server.use(
      http.get("/servers/alpha/players", () =>
        HttpResponse.json(makePlayers({ online: 1, players: [entry.steamId], entries: [entry] })),
      ),
    );
    renderWithQuery(<PlayersTab name="alpha" />);
    await screen.findByText(entry.steamId);
    await userEvent.click(screen.getByRole("button", { name: "Kick" }));
    await screen.findByPlaceholderText(/Reason/i);
    expect(screen.getAllByText(entry.steamId)).toHaveLength(2);
  });
});

function renderWithQuery(ui: ReactElement, options?: Parameters<typeof baseRenderWithQuery>[1]) {
  const props = ui.props as { name?: string; ns?: string };
  return baseRenderWithQuery(<ResourceTargetProvider target={{ cluster: "local", name: props.name ?? "alpha", namespace: props.ns }} access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: true, isCollaborator: false, permissions: ["*"] }}>{ui}</ResourceTargetProvider>, options);
}
