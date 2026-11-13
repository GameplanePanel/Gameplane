import { ResourceTargetProvider } from "@/lib/resourceTarget";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { ServerActionsCard } from "./ServerActionsCard";
import type { GameServer, GameTemplate, ServerActionDecl } from "@/types";

const fetchMock = vi.fn();

beforeEach(() => vi.stubGlobal("fetch", fetchMock));
afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

function jsonRes(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

interface RunCall {
  id: string;
  params?: Record<string, string>;
}

// routeFetch answers /users/me with the given role and records POSTs to
// the action-run endpoint.
function routeFetch(role: "operator" | "viewer", runs: RunCall[]) {
  scopedControl = role === "operator";
  // Running module actions is gated on servers:write; mirror that here.
  const permissions =
    role === "operator" ? { "*": ["servers:read", "servers:write"] } : { "*": ["servers:read"] };
  fetchMock.mockImplementation((url: string, opts?: { method?: string; body?: string }) => {
    if (url.endsWith("/users/me")) {
      return Promise.resolve(
        jsonRes({ id: 1, username: "u", displayName: "U", email: "", role, permissions }),
      );
    }
    if (url.endsWith("/users/me/servers")) {
      return Promise.resolve(jsonRes({ items: [] }));
    }
    if (url.endsWith("/actions/run")) {
      runs.push(JSON.parse(opts?.body ?? "{}") as RunCall);
      return Promise.resolve(jsonRes({ ok: true, raw: "done" }));
    }
    return Promise.resolve(jsonRes({}));
  });
}

function tmpl(actions: ServerActionDecl[]): GameTemplate {
  return {
    metadata: { name: "minecraft-java" },
    spec: {
      displayName: "Minecraft",
      game: "minecraft-java",
      version: "1",
      image: "img",
      rcon: { protocol: "source" },
      capabilities: { actions },
    },
  };
}

describe("ServerActionsCard", () => {
  it("renders nothing when no actions are declared", () => {
    const { container } = renderWithQuery(<ServerActionsCard name="s1" tmpl={tmpl([])} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("runs a no-parameter action immediately", async () => {
    const runs: RunCall[] = [];
    routeFetch("operator", runs);
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "save-world", displayName: "Save world" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /save world/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    await waitFor(() => expect(runs).toEqual([{ id: "save-world" }]));
    expect(await screen.findByText("done")).toBeInTheDocument();
  });

  it("collects parameters in a dialog before running", async () => {
    const runs: RunCall[] = [];
    routeFetch("operator", runs);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "broadcast",
            displayName: "Broadcast message",
            params: [{ name: "message", type: "string", required: true }],
          },
        ])}
      />,
    );
    const open = await screen.findByRole("button", { name: /broadcast message/i });
    await waitFor(() => expect(open).not.toBeDisabled());
    fireEvent.click(open);

    const input = await screen.findByRole("textbox", { name: /message/i });
    fireEvent.change(input, { target: { value: "hello world" } });
    fireEvent.click(screen.getByRole("button", { name: "Run" }));

    await waitFor(() =>
      expect(runs).toEqual([{ id: "broadcast", params: { message: "hello world" } }]),
    );
  });

  describe("currentFrom", () => {
    const difficultyAction: ServerActionDecl = {
      id: "set-difficulty",
      displayName: "Set difficulty",
      params: [
        {
          name: "level",
          displayName: "Difficulty",
          type: "enum",
          enum: ["peaceful", "easy", "normal", "hard"],
          default: "normal",
          required: true,
          currentFrom: "difficulty",
        },
      ],
    };

    // routeStatus layers a /servers/s1/status answer over routeFetch.
    function routeStatus(readings: unknown) {
      const base = fetchMock.getMockImplementation();
      fetchMock.mockImplementation((url: string, opts?: { method?: string; body?: string }) =>
        url.endsWith("/servers/s1/status")
          ? Promise.resolve(jsonRes(readings))
          : base?.(url, opts),
      );
    }

    it("pre-fills an enum param from its status metric, matching case-insensitively", async () => {
      const runs: RunCall[] = [];
      routeFetch("operator", runs);
      routeStatus([{ id: "difficulty", value: "Easy" }]);
      renderWithQuery(<ServerActionsCard name="s1" tmpl={tmpl([difficultyAction])} />);
      const open = await screen.findByRole("button", { name: /set difficulty/i });
      await waitFor(() => expect(open).not.toBeDisabled());
      fireEvent.click(open);

      // The Select trigger is a button named by its label (see the enum test above).
      const trigger = screen.getByRole("button", { name: /difficulty/i });
      await waitFor(() => expect(trigger).toHaveTextContent("easy"));
      fireEvent.click(screen.getByRole("button", { name: "Run" }));
      await waitFor(() =>
        expect(runs).toEqual([{ id: "set-difficulty", params: { level: "easy" } }]),
      );
    });

    it("keeps the user's choice over the live reading", async () => {
      const runs: RunCall[] = [];
      routeFetch("operator", runs);
      routeStatus([{ id: "difficulty", value: "Easy" }]);
      renderWithQuery(<ServerActionsCard name="s1" tmpl={tmpl([difficultyAction])} />);
      const open = await screen.findByRole("button", { name: /set difficulty/i });
      await waitFor(() => expect(open).not.toBeDisabled());
      fireEvent.click(open);

      const trigger = screen.getByRole("button", { name: /difficulty/i });
      await waitFor(() => expect(trigger).toHaveTextContent("easy"));
      fireEvent.click(trigger);
      fireEvent.click(await screen.findByRole("option", { name: "hard" }));
      await waitFor(() => expect(trigger).toHaveTextContent("hard"));
      fireEvent.click(screen.getByRole("button", { name: "Run" }));
      await waitFor(() =>
        expect(runs).toEqual([{ id: "set-difficulty", params: { level: "hard" } }]),
      );
    });

    it("falls back to the default when the reading matches no option", async () => {
      const runs: RunCall[] = [];
      routeFetch("operator", runs);
      routeStatus([{ id: "difficulty", value: "Extreme" }]);
      renderWithQuery(<ServerActionsCard name="s1" tmpl={tmpl([difficultyAction])} />);
      await waitFor(() =>
        expect(
          fetchMock.mock.calls.some(([u]) => String(u).endsWith("/servers/s1/status")),
        ).toBe(true),
      );
      const open = await screen.findByRole("button", { name: /set difficulty/i });
      await waitFor(() => expect(open).not.toBeDisabled());
      fireEvent.click(open);

      fireEvent.click(await screen.findByRole("button", { name: "Run" }));
      await waitFor(() =>
        expect(runs).toEqual([{ id: "set-difficulty", params: { level: "normal" } }]),
      );
    });
  });

  it("disables actions for a viewer", async () => {
    routeFetch("viewer", []);
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "save-world", displayName: "Save world" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /save world/i });
    // The /users/me query resolves to viewer; the button stays disabled.
    await waitFor(() => expect(btn).toBeDisabled());
  });

  it("renders action groups with labeled sections", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          { id: "a1", displayName: "World action", group: "WORLD" },
          { id: "a2", displayName: "Server action", group: "SERVER" },
          { id: "a3", displayName: "Another world", group: "WORLD" },
        ])}
      />,
    );
    await waitFor(() => {
      expect(screen.getByText("WORLD")).toBeInTheDocument();
      expect(screen.getByText("SERVER")).toBeInTheDocument();
    });
    // Buttons present…
    expect(await screen.findByRole("button", { name: /World action/i })).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /Server action/i })).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /Another world/i })).toBeInTheDocument();
    // …and group headers render in DECLARATION order (WORLD first, since it
    // was the first group seen), not alphabetical or scrambled.
    const headers = screen.getAllByText(/^(WORLD|SERVER)$/).map((el) => el.textContent);
    expect(headers).toEqual(["WORLD", "SERVER"]);
  });

  it("shows a sent chip for actions with no output", async () => {
    const runs: RunCall[] = [];
    const permissions = { "*": ["servers:read", "servers:write"] };
    fetchMock.mockImplementation((url: string, opts?: { method?: string; body?: string }) => {
      if (url.endsWith("/users/me")) {
        return Promise.resolve(
          jsonRes({ id: 1, username: "u", displayName: "U", email: "", role: "operator", permissions }),
        );
      }
      if (url.endsWith("/actions/run")) {
        runs.push(JSON.parse(opts?.body ?? "{}") as RunCall);
        // Return no raw output (stdin style)
        return Promise.resolve(jsonRes({ ok: true }));
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        // transport: stdin makes this fire-and-forget regardless of the
        // empty body — that's what drives the "sent" chip, not the raw.
        tmpl={tmpl([{ id: "send-msg", displayName: "Send message", transport: "stdin" }])}
      />,
    );
    const btn = await screen.findByRole("button", { name: /Send message/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    // Should show "Send message sent" in the result
    await waitFor(() =>
      expect(screen.getByText("Send message sent")).toBeInTheDocument(),
    );
  });

  it("shows 'ran', not 'sent', for an rcon action that returns empty output", async () => {
    // The regression the transport-based result state fixes: an rcon command
    // (cs2 `say`, project-zomboid `servermsg`) legitimately returns nothing,
    // and that must read as "ran", not the fire-and-forget "sent" chip.
    const permissions = { "*": ["servers:read", "servers:write"] };
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) {
        return Promise.resolve(
          jsonRes({ id: 1, username: "u", displayName: "U", email: "", role: "operator", permissions }),
        );
      }
      if (url.endsWith("/actions/run")) {
        return Promise.resolve(jsonRes({ ok: true })); // empty rcon reply
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(
      // No transport + rcon protocol present → resolves to rcon.
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "broadcast", displayName: "Broadcast" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /Broadcast/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    await waitFor(() => expect(screen.getByText("Broadcast ran")).toBeInTheDocument());
    expect(screen.queryByText("Broadcast sent")).not.toBeInTheDocument();
  });

  it("shows output box for actions with output", async () => {
    const runs: RunCall[] = [];
    const permissions = { "*": ["servers:read", "servers:write"] };
    fetchMock.mockImplementation((url: string, opts?: { method?: string; body?: string }) => {
      if (url.endsWith("/users/me")) {
        return Promise.resolve(
          jsonRes({ id: 1, username: "u", displayName: "U", email: "", role: "operator", permissions }),
        );
      }
      if (url.endsWith("/actions/run")) {
        runs.push(JSON.parse(opts?.body ?? "{}") as RunCall);
        // Return some output (rcon style)
        return Promise.resolve(jsonRes({ ok: true, raw: "world saved" }));
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([{ id: "save", displayName: "Save world" }])}
      />,
    );
    const btn = await screen.findByRole("button", { name: /Save world/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    // Should show the raw output in mono font
    const output = await screen.findByText("world saved");
    expect(output).toBeInTheDocument();
    expect(output).toHaveClass("font-mono");
  });

  it("truncates long output to 200 chars", async () => {
    const permissions = { "*": ["servers:read", "servers:write"] };
    const longOutput = "x".repeat(250);
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) {
        return Promise.resolve(
          jsonRes({ id: 1, username: "u", displayName: "U", email: "", role: "operator", permissions }),
        );
      }
      if (url.endsWith("/actions/run")) {
        return Promise.resolve(jsonRes({ ok: true, raw: longOutput }));
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "cmd", displayName: "Command" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /Command/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    const output = await screen.findByText(/^x+…$/);
    expect(output.textContent).toHaveLength(201); // 200 chars + 1 ellipsis char
  });

  it("displays error message from failed action", async () => {
    const permissions = { "*": ["servers:read", "servers:write"] };
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) {
        return Promise.resolve(
          jsonRes({ id: 1, username: "u", displayName: "U", email: "", role: "operator", permissions }),
        );
      }
      if (url.endsWith("/actions/run")) {
        return Promise.reject(new Error("Connection timeout"));
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "fail", displayName: "Fail action" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /Fail action/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    const error = await screen.findByText("Connection timeout");
    expect(error).toBeInTheDocument();
    expect(error).toHaveClass("font-mono");
  });

  it("allows dismissing status messages", async () => {
    const runs: RunCall[] = [];
    routeFetch("operator", runs);
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "save", displayName: "Save" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /Save/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    const dismiss = await screen.findByRole("button", { name: "dismiss" });
    fireEvent.click(dismiss);
    await waitFor(() => expect(screen.queryByText("Save sent")).not.toBeInTheDocument());
  });

  it("handles confirm-only actions (no parameters)", async () => {
    const runs: RunCall[] = [];
    routeFetch("operator", runs);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([{ id: "restart", displayName: "Restart server", confirm: true }])}
      />,
    );
    const btn = await screen.findByRole("button", { name: /Restart server/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    // Should open a dialog even though there are no parameters
    expect(await screen.findByText("Run this action now?")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    // The dialog's onRun always calls collect(params, values), which
    // returns {} even for a param-less action (unlike the no-dialog path,
    // which omits `params` entirely) — so a confirm-only run legitimately
    // carries an empty params object, not an absent key.
    await waitFor(() => expect(runs).toEqual([{ id: "restart", params: {} }]));
  });

  it("validates required parameters before allowing submission", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "announce",
            displayName: "Announce",
            params: [{ name: "msg", type: "string", required: true }],
          },
        ])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Announce/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    fireEvent.click(openBtn);
    const runBtn = screen.getByRole("button", { name: "Run" });
    expect(runBtn).toBeDisabled(); // No input = submit disabled
    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "hello" } });
    await waitFor(() => expect(runBtn).not.toBeDisabled());
  });

  it("validates enum parameters and rejects invalid choices", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "set-mode",
            displayName: "Set mode",
            params: [{ name: "mode", type: "enum", enum: ["creative", "survival"] }],
          },
        ])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Set mode/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    fireEvent.click(openBtn);
    // HeroUI's compound Select renders its trigger as a plain button
    // (react-aria's useSelect wires it up via useMenuTrigger({type:
    // "listbox"}), which leaves the trigger's role as "button" with
    // aria-haspopup="listbox" — it never becomes role="combobox"). The
    // field's <Label htmlFor> points at the trigger's id, so the trigger's
    // accessible name is the label text ("mode"), letting it still be
    // found by label the way the other param fields are.
    const trigger = screen.getByRole("button", { name: /mode/i });
    expect(trigger).toHaveTextContent("creative"); // First enum value is default
    fireEvent.click(trigger);
    const survivalOption = await screen.findByRole("option", { name: "survival" });
    fireEvent.click(survivalOption);
    await waitFor(() => expect(trigger).toHaveTextContent("survival"));
  });

  it("validates int parameters", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "set-count",
            displayName: "Set count",
            params: [{ name: "count", type: "int", required: true }],
          },
        ])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Set count/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    fireEvent.click(openBtn);
    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "abc" } });
    expect(
      await screen.findByText("Must be a whole number"),
    ).toBeInTheDocument();
    fireEvent.change(input, { target: { value: "42" } });
    await waitFor(() => expect(screen.queryByText("Must be a whole number")).not.toBeInTheDocument());
  });

  it("rejects control characters in string parameters", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "cmd",
            displayName: "Run",
            params: [{ name: "txt", type: "string", required: true }],
          },
        ])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Run/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    fireEvent.click(openBtn);
    const input = screen.getByRole("textbox");
    // jsdom (like real browsers) runs the HTML value-sanitization algorithm
    // on single-line text inputs, which strips \n/\r before onChange ever
    // sees them — so LF can't be used to exercise this path. ESC (0x1B) is
    // still a control character but isn't stripped.
    fireEvent.change(input, { target: { value: "hello\x1bworld" } });
    expect(
      await screen.findByText("No control characters"),
    ).toBeInTheDocument();
  });

  it("handles boolean parameters with checkbox", async () => {
    const runs: RunCall[] = [];
    routeFetch("operator", runs);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "toggle",
            displayName: "Toggle feature",
            params: [{ name: "enabled", type: "bool" }],
          },
        ])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Toggle feature/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    fireEvent.click(openBtn);
    const checkbox = screen.getByRole("checkbox");
    expect(checkbox).not.toBeChecked();
    fireEvent.click(checkbox);
    expect(checkbox).toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    await waitFor(() => expect(runs).toEqual([{ id: "toggle", params: { enabled: "true" } }]));
  });

  it("cancels dialog without running action", async () => {
    const user = userEvent.setup();
    const runs: RunCall[] = [];
    routeFetch("operator", runs);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([{ id: "test", displayName: "Test", params: [{ name: "x", type: "string" }] }])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Test/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    // HeroUI v3 Button dispatches via react-aria's onPress, which
    // fireEvent.click does not trigger reliably (it fires a bare "click"
    // with no preceding pointer/mouse sequence) — userEvent.click drives
    // the full pointer sequence onPress listens for.
    await user.click(openBtn);
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    // HeroUI Modal closes, so check the dialog itself is gone — the
    // action button's own label is also "Test" and stays in the
    // document, so scope this to the dialog role rather than the text.
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    expect(runs).toHaveLength(0);
  });

  it("disables buttons while action is pending", async () => {
    const permissions = { "*": ["servers:read", "servers:write"] };
    let resolveRun: () => void;
    const runPromise = new Promise<void>((resolve) => {
      resolveRun = resolve;
    });
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) {
        return Promise.resolve(
          jsonRes({ id: 1, username: "u", displayName: "U", email: "", role: "operator", permissions }),
        );
      }
      if (url.endsWith("/actions/run")) {
        return runPromise.then(() => jsonRes({ ok: true }));
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "slow", displayName: "Slow action" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /Slow action/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    // Button becomes disabled while pending
    await waitFor(() => expect(btn).toBeDisabled());
    resolveRun!();
    await waitFor(() => expect(btn).not.toBeDisabled());
  });

  it("shows 'Actions need a live console' message when game has no rcon", async () => {
    routeFetch("operator", []);
    const noRconTemplate: GameTemplate = {
      metadata: { name: "static-game" },
      spec: {
        displayName: "Static",
        game: "static",
        version: "1",
        image: "img",
        capabilities: { actions: [{ id: "a1", displayName: "Action" }] },
      },
    };
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={noRconTemplate} />,
    );
    expect(
      await screen.findByText("Actions need a live console; this game has none."),
    ).toBeInTheDocument();
  });

  it("disables action buttons when game has no rcon", async () => {
    routeFetch("operator", []);
    const noRconTemplate: GameTemplate = {
      metadata: { name: "static-game" },
      spec: {
        displayName: "Static",
        game: "static",
        version: "1",
        image: "img",
        capabilities: { actions: [{ id: "a1", displayName: "Action" }] },
      },
    };
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={noRconTemplate} />,
    );
    const btn = await screen.findByRole("button", { name: /Action/i });
    expect(btn).toBeDisabled();
  });

  it("renders action description in button title", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "save",
            displayName: "Save world",
            description: "Saves the world to disk",
          },
        ])}
      />,
    );
    const btn = await screen.findByRole("button", { name: /Save world/i });
    // Until /users/me resolves, canRun is false and the title falls back to
    // "Requires operator role" — wait for the operator grant to land first.
    await waitFor(() => expect(btn).not.toBeDisabled());
    expect(btn).toHaveAttribute("title", "Saves the world to disk");
  });

  it("shows permission error in title when viewer role", async () => {
    routeFetch("viewer", []);
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "a1", displayName: "Action" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /Action/i });
    expect(btn).toHaveAttribute("title", "Requires operator role");
  });

  it("renders action icons correctly", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          { id: "a1", displayName: "Save", icon: "save" },
          { id: "a2", displayName: "Unknown icon", icon: "nonexistent" },
        ])}
      />,
    );
    // Save icon should render (check for svg or similar)
    const buttons = await screen.findAllByRole("button");
    expect(buttons.length).toBeGreaterThanOrEqual(2);
  });

  it("renders danger actions with danger styling", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          { id: "delete", displayName: "Delete world", danger: true },
        ])}
      />,
    );
    const btn = await screen.findByRole("button", { name: /Delete world/i });
    expect(btn.querySelector("span")).toHaveClass("text-danger");
  });

  it("drops empty optional parameters from submission", async () => {
    const runs: RunCall[] = [];
    routeFetch("operator", runs);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "cmd",
            displayName: "Run",
            params: [
              { name: "msg", type: "string", required: true },
              { name: "extra", type: "string", required: false },
            ],
          },
        ])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Run/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    fireEvent.click(openBtn);
    const inputs = screen.getAllByRole("textbox");
    fireEvent.change(inputs[0], { target: { value: "required" } });
    // Leave inputs[1] (extra) empty
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    await waitFor(() =>
      expect(runs).toEqual([{ id: "cmd", params: { msg: "required" } }]),
    );
  });

  it("handles API error responses with parsed error body", async () => {
    const permissions = { "*": ["servers:read", "servers:write"] };
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) {
        return Promise.resolve(
          jsonRes({ id: 1, username: "u", displayName: "U", email: "", role: "operator", permissions }),
        );
      }
      if (url.endsWith("/actions/run")) {
        return Promise.resolve(
          new Response(JSON.stringify({ error: "Invalid player name" }), {
            status: 400,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "ban", displayName: "Ban player" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /Ban player/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    expect(await screen.findByText("Invalid player name")).toBeInTheDocument();
  });

  it("handles API 403 permission error", async () => {
    // The client must believe it can write (servers:write granted) so the
    // button is enabled and the click actually reaches /actions/run — this
    // test is about the *server* rejecting with 403 (stale grant, revoked
    // role, etc.), not about the client-side disablement, which is covered
    // by "disables actions for a viewer" above.
    const permissions = { "*": ["servers:read", "servers:write"] };
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) {
        return Promise.resolve(
          jsonRes({ id: 1, username: "u", displayName: "U", email: "", role: "operator", permissions }),
        );
      }
      if (url.endsWith("/actions/run")) {
        return Promise.resolve(
          new Response(JSON.stringify({}), { status: 403 }),
        );
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "a1", displayName: "Action" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /Action/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    expect(
      await screen.findByText("Your role does not allow running actions."),
    ).toBeInTheDocument();
  });

  it("shows action group headers only when groups are present", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "a1", displayName: "No group" }])} />,
    );
    // When there are only ungrouped actions, no "Actions" header is shown
    await screen.findByRole("button", { name: /No group/i });
    expect(screen.queryByText("Actions")).not.toBeInTheDocument();
  });

  it("shows 'Actions' header for ungrouped when groups are also present", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          { id: "a1", displayName: "Grouped", group: "GROUP1" },
          { id: "a2", displayName: "Ungrouped" },
        ])}
      />,
    );
    expect(await screen.findByText("GROUP1")).toBeInTheDocument();
    expect(await screen.findByText("Actions")).toBeInTheDocument();
  });

  describe("transport gating without RCON", () => {
    // A pty-console game with no RCON (e.g. Terraria): actions resolve to
    // the stdin transport and are delivered over pod attach.
    const ptyTemplate = (actions: ServerActionDecl[], consoleMode: "pty" | "none" = "pty"): GameTemplate => ({
      metadata: { name: "terraria" },
      spec: {
        displayName: "Terraria",
        game: "terraria",
        version: "1",
        image: "img",
        consoleMode,
        capabilities: { actions },
      },
    });

    it("enables a stdin action on a pty game and reports it as sent", async () => {
      const runs: RunCall[] = [];
      routeFetch("operator", runs);
      renderWithQuery(
        <ServerActionsCard name="s1" tmpl={ptyTemplate([{ id: "save", displayName: "Save world" }])} />,
      );
      const btn = await screen.findByRole("button", { name: /save world/i });
      await waitFor(() => expect(btn).not.toBeDisabled());
      expect(screen.queryByText(/need a live console/i)).not.toBeInTheDocument();
      expect(screen.queryByText(/need RCON/i)).not.toBeInTheDocument();
      fireEvent.click(btn);
      await waitFor(() => expect(runs).toEqual([{ id: "save" }]));
      expect(await screen.findByText("Save world sent")).toBeInTheDocument();
    });

    it("keeps an rcon-transport action disabled on a pty game and explains why", async () => {
      routeFetch("operator", []);
      renderWithQuery(
        <ServerActionsCard
          name="s1"
          tmpl={ptyTemplate([
            { id: "save", displayName: "Save world" },
            { id: "say", displayName: "Say hi", transport: "rcon" },
          ])}
        />,
      );
      const stdinBtn = await screen.findByRole("button", { name: /save world/i });
      await waitFor(() => expect(stdinBtn).not.toBeDisabled());
      expect(screen.getByRole("button", { name: /say hi/i })).toBeDisabled();
      expect(screen.getByText("Some actions need RCON; this game has none.")).toBeInTheDocument();
    });

    it("keeps a stdin action disabled when the game has no console at all", async () => {
      routeFetch("operator", []);
      renderWithQuery(
        <ServerActionsCard
          name="s1"
          tmpl={ptyTemplate([{ id: "save", displayName: "Save world", transport: "stdin" }], "none")}
        />,
      );
      const btn = await screen.findByRole("button", { name: /save world/i });
      expect(btn).toBeDisabled();
      expect(screen.getByText("Actions need a live console; this game has none.")).toBeInTheDocument();
    });
  });

  it("displays API error without parsed error field fallback to body", async () => {
    const permissions = { "*": ["servers:read", "servers:write"] };
    fetchMock.mockImplementation((url: string) => {
      if (url.endsWith("/users/me")) {
        return Promise.resolve(
          jsonRes({ id: 1, username: "u", displayName: "U", email: "", role: "operator", permissions }),
        );
      }
      if (url.endsWith("/actions/run")) {
        return Promise.resolve(
          new Response("Internal server error", { status: 500 }),
        );
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(
      <ServerActionsCard name="s1" tmpl={tmpl([{ id: "fail", displayName: "Fail" }])} />,
    );
    const btn = await screen.findByRole("button", { name: /Fail/i });
    await waitFor(() => expect(btn).not.toBeDisabled());
    fireEvent.click(btn);
    expect(await screen.findByText("Internal server error")).toBeInTheDocument();
  });

  it("defaults to false for bool parameters when no default provided", async () => {
    const runs: RunCall[] = [];
    routeFetch("operator", runs);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "toggle",
            displayName: "Toggle",
            params: [{ name: "flag", type: "bool" }],
          },
        ])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Toggle/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    fireEvent.click(openBtn);
    const checkbox = screen.getByRole("checkbox");
    expect(checkbox).not.toBeChecked(); // Default is false
    fireEvent.click(screen.getByRole("button", { name: "Run" }));
    // collect() only drops a param whose value is the empty string; a bool's
    // value is always the literal "true"/"false" (never ""), so the default
    // is sent explicitly rather than omitted — unlike an optional string
    // param left blank (see "drops empty optional parameters" above).
    await waitFor(() =>
      expect(runs).toEqual([{ id: "toggle", params: { flag: "false" } }]),
    );
  });

  it("includes parameter with displayName in dialog labels", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "custom",
            displayName: "Custom action",
            params: [
              { name: "count", type: "int", displayName: "Item count", required: true },
            ],
          },
        ])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Custom action/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    fireEvent.click(openBtn);
    // The param is required, so ParamField appends a " *" suffix to the
    // label text — match on the displayName as a substring rather than
    // the exact label text.
    expect(await screen.findByLabelText(/Item count/)).toBeInTheDocument();
  });

  it("shows parameter description when provided", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "cmd",
            displayName: "Run command",
            params: [
              { name: "cmd", type: "string", description: "The command to execute" },
            ],
          },
        ])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Run command/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    fireEvent.click(openBtn);
    expect(await screen.findByText("The command to execute")).toBeInTheDocument();
  });

  it("falls back to generic lifecycle shortcuts when a GameServer is given and no actions are declared", async () => {
    routeFetch("operator", []);
    const onOpenConsole = vi.fn();
    const gs: GameServer = {
      metadata: { name: "s1" },
      spec: { templateRef: { name: "minecraft-java" } },
      status: { phase: "Running" },
    };
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([])}
        gs={gs}
        onOpenConsole={onOpenConsole}
      />,
    );
    expect(await screen.findByText("Quick actions")).toBeInTheDocument();
    const restart = screen.getByRole("button", { name: /Restart/i });
    const stop = screen.getByRole("button", { name: /Stop/i });
    // Running: Start is hidden, Restart/Stop are available.
    expect(screen.queryByRole("button", { name: /^Start$/i })).not.toBeInTheDocument();
    await waitFor(() => expect(restart).not.toBeDisabled());
    await waitFor(() => expect(stop).not.toBeDisabled());
    fireEvent.click(screen.getByRole("button", { name: /Open console/i }));
    expect(onOpenConsole).toHaveBeenCalled();
  });

  it.each(["Pending", "Starting"] as const)("can stop a provisioning server while %s", async (phase) => {
    routeFetch("operator", []);
    const gs: GameServer = { metadata: { name: "s1" }, spec: { templateRef: { name: "minecraft-java" } }, status: { phase } };
    renderWithQuery(<ServerActionsCard name="s1" ns="team-b" tmpl={tmpl([])} gs={gs} />);
    const stop = await screen.findByRole("button", { name: /^Stop$/i });
    expect(stop).toBeEnabled();
    expect(screen.getByRole("button", { name: /^Restart$/i })).toBeDisabled();
    fireEvent.click(stop);
    await waitFor(() => expect(fetchMock.mock.calls.filter(([url]) => String(url).includes("/servers/s1:stop?namespace=team-b"))).toHaveLength(1));
  });

  // F-126-adjacent: the generic lifecycle shortcuts' mutation had no error
  // handler — a failed Restart/Stop/Start/Wake left "Quick actions" silent.
  it("shows an error banner when a generic lifecycle shortcut fails", async () => {
    const gs: GameServer = {
      metadata: { name: "s1" },
      spec: { templateRef: { name: "minecraft-java" } },
      status: { phase: "Running" },
    };
    fetchMock.mockImplementation((url: string, opts?: { method?: string }) => {
      if (url.endsWith("/users/me")) {
        return Promise.resolve(
          jsonRes({
            id: 1,
            username: "u",
            displayName: "U",
            email: "",
            role: "operator",
            permissions: { "*": ["servers:read", "servers:write"] },
          }),
        );
      }
      if (url.endsWith("/users/me/servers")) {
        return Promise.resolve(jsonRes({ items: [] }));
      }
      if (url.includes(":restart") && opts?.method === "POST") {
        return Promise.resolve(
          new Response("restart failed: pod not ready", { status: 500 }),
        );
      }
      return Promise.resolve(jsonRes({}));
    });
    renderWithQuery(<ServerActionsCard name="s1" tmpl={tmpl([])} gs={gs} />);
    const restart = await screen.findByRole("button", { name: /Restart/i });
    await waitFor(() => expect(restart).not.toBeDisabled());
    fireEvent.click(restart);
    expect(await screen.findByText(/restart failed: pod not ready/i)).toBeInTheDocument();
  });

  it("validates negative integers", async () => {
    routeFetch("operator", []);
    renderWithQuery(
      <ServerActionsCard
        name="s1"
        tmpl={tmpl([
          {
            id: "set",
            displayName: "Set value",
            params: [{ name: "val", type: "int" }],
          },
        ])}
      />,
    );
    const openBtn = await screen.findByRole("button", { name: /Set value/i });
    await waitFor(() => expect(openBtn).not.toBeDisabled());
    fireEvent.click(openBtn);
    const input = screen.getByRole("textbox");
    fireEvent.change(input, { target: { value: "-42" } });
    // Negative integers should be valid
    await waitFor(() => expect(screen.queryByText("Must be a whole number")).not.toBeInTheDocument());
  });
});

let scopedControl = true;
function renderWithQuery(ui: ReactElement, options?: Parameters<typeof baseRenderWithQuery>[1]) {
  const props = ui.props as { name?: string; ns?: string };
  return baseRenderWithQuery(<ResourceTargetProvider target={{ cluster: "local", name: props.name ?? "s1", namespace: props.ns }} access={{ canWrite: scopedControl, canControl: scopedControl, canConsole: scopedControl, canDelete: false, isOwner: false, isCollaborator: false, permissions: scopedControl ? ["servers:read", "servers:write"] : ["servers:read"] }}>{ui}</ResourceTargetProvider>, options);
}
