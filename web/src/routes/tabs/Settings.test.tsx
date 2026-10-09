import { ResourceTargetProvider } from "@/lib/resourceTarget";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { SettingsTab } from "./Settings";
import type { GameServer } from "@/types";

type FetchInit = Parameters<typeof fetch>[1];

const navigateMock = vi.fn();
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigateMock,
}));

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
  navigateMock.mockReset();
});
afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});


function gs(overrides: Partial<GameServer> = {}): GameServer {
  return {
    metadata: { name: "mc-survival", resourceVersion: "100" },
    spec: { templateRef: { name: "minecraft-java" } },
    status: { phase: "Running" },
    ...overrides,
  };
}

function jsonRes(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// The Templates.get query fires whenever templateRef is set; default it.
function stubTemplate() {
  fetchMock.mockImplementation(async (url: string) => {
    if (url.startsWith("/templates/")) {
      return jsonRes({
        metadata: { name: "minecraft-java" },
        spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" },
      });
    }
    throw new Error(`unexpected fetch: ${url}`);
  });
}

describe("SettingsTab", () => {
  it("preserves share creation and its one-time token across server status refreshes", async () => {
    const link = { id: "share-id", token: "one-time-token", createdAt: new Date().toISOString(), expiresAt: null, canStart: false };
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) return jsonRes({ metadata: { name: "minecraft-java" }, spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" } });
      if (url.includes(":shares")) return jsonRes(init?.method === "POST" ? link : []);
      throw new Error(`unexpected fetch: ${url}`);
    });
    const subject = (snapshot: GameServer) => <ResourceTargetProvider
      target={{ cluster: "local", name: "mc-survival" }}
      access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: true, isCollaborator: false, permissions: ["*"] }}>
      <SettingsTab gs={snapshot} name="mc-survival" />
    </ResourceTargetProvider>;
    const view = baseRenderWithQuery(subject(gs({ status: { phase: "Starting" } })));
    await userEvent.click(screen.getByRole("tab", { name: "Share links" }));
    await userEvent.click(screen.getAllByRole("button", { name: "Create link" })[0]);
    const createDialog = await screen.findByRole("dialog", { name: "Create share link for mc-survival" });
    await userEvent.click(within(createDialog).getByRole("button", { name: /30 days/ }));
    await userEvent.click(screen.getByRole("option", { name: "No expiry" }));
    view.rerender(<QueryClientProvider client={view.client}>{subject(gs({ metadata: { name: "mc-survival", resourceVersion: "101" }, status: { phase: "Running" } }))}</QueryClientProvider>);
    expect(screen.getByRole("dialog", { name: "Create share link for mc-survival" })).toBeVisible();
    expect(within(createDialog).getByRole("button", { name: /No expiry/ })).toBeVisible();
    await userEvent.click(within(createDialog).getByRole("button", { name: "Create link" }));
    await screen.findByRole("dialog", { name: "Share link created" });
    view.rerender(<QueryClientProvider client={view.client}>{subject(gs({ metadata: { name: "mc-survival", resourceVersion: "102" }, status: { phase: "Running", startedAt: new Date().toISOString() } }))}</QueryClientProvider>);
    const createdDialog = screen.getByRole("dialog", { name: "Share link created" });
    expect(createdDialog).toBeVisible();
    expect(within(createdDialog).getByText(/\/share\/one-time-token$/)).toBeVisible();
    await userEvent.click(within(createdDialog).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Share link created" })).not.toBeInTheDocument());
  });

  it("requires reload without writing to a replacement server UID", async () => {
    let writes = 0;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) return jsonRes({ metadata: { name: "minecraft-java" }, spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" } });
      if (init?.method === "PUT") writes++;
      return jsonRes(gs({ metadata: { name: "mc-survival", uid: "replacement", resourceVersion: "200" } }));
    });
    renderWithQuery(<SettingsTab gs={gs({ metadata: { name: "mc-survival", uid: "original", resourceVersion: "100" } })} name="mc-survival" />);
    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), { target: { value: "original server edit" } });
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await screen.findByText(/changed since you opened this page/i);
    expect(writes).toBe(0);
    expect(screen.getByPlaceholderText(/Long-standing/)).toHaveValue("original server edit");
    fireEvent.click(screen.getByRole("button", { name: /^Reload$/ }));
    await waitFor(() => expect(screen.getByPlaceholderText(/Long-standing/)).toHaveValue(""));
  });

  it("locks every settings editor until both the latest GET and PUT finish", async () => {
    let finishGet!: (response: Response) => void;
    let finishPut!: (response: Response) => void;
    const get = new Promise<Response>((resolve) => { finishGet = resolve; });
    const put = new Promise<Response>((resolve) => { finishPut = resolve; });
    let submitted: GameServer | undefined;
    fetchMock.mockImplementation((url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) return Promise.resolve(jsonRes({ metadata: { name: "minecraft-java" }, spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" } }));
      if (init?.method === "PUT") { submitted = JSON.parse(init.body as string) as GameServer; return put; }
      return get;
    });
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);
    const description = screen.getByPlaceholderText(/Long-standing/);
    fireEvent.change(description, { target: { value: "submitted" } });
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(description).toBeDisabled());
    expect(description.closest("fieldset")).toHaveAttribute("inert");
    // A portalled control or delayed editor callback may still dispatch a
    // change outside the disabled subtree. The parent must reject it too.
    fireEvent.change(description, { target: { value: "late edit" } });
    expect(description).toHaveValue("submitted");
    fireEvent.click(screen.getByRole("tab", { name: /Environment/i }));
    expect(screen.getByRole("button", { name: /Add variable/i })).toBeDisabled();
    await act(async () => finishGet(jsonRes(gs())));
    await waitFor(() => expect(submitted).toBeDefined());
    expect(screen.getByRole("button", { name: /Add variable/i })).toBeDisabled();
    await act(async () => finishPut(jsonRes(submitted)));
    await waitFor(() => expect(screen.getByRole("button", { name: /Add variable/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("tab", { name: /^General$/i }));
    expect(screen.getByPlaceholderText(/Long-standing/)).toHaveValue("submitted");
  });

  it("starts clean and reports dirty after editing description", async () => {
    stubTemplate();
    const onDirty = vi.fn();
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" onDirtyChange={onDirty} />);

    await waitFor(() => expect(onDirty).toHaveBeenCalledWith(false));

    const textarea = screen.getByPlaceholderText(/Long-standing/);
    fireEvent.change(textarea, { target: { value: "Hello world" } });

    await waitFor(() => expect(onDirty).toHaveBeenLastCalledWith(true));
    expect(screen.getByText("Unsaved changes")).toBeInTheDocument();
  });

  it("adds, removes, and toggles env-var rows", async () => {
    stubTemplate();
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);
    fireEvent.click(screen.getByRole("tab", { name: /Environment/i }));

    fireEvent.click(screen.getByRole("button", { name: /Add variable/i }));
    const nameInput = screen.getByPlaceholderText("VAR_NAME");
    fireEvent.change(nameInput, { target: { value: "FOO" } });
    fireEvent.change(screen.getByPlaceholderText("value"), { target: { value: "bar" } });

    fireEvent.click(screen.getByRole("button", { name: /Add from secret/i }));
    expect(screen.getAllByPlaceholderText("VAR_NAME")).toHaveLength(2);

    // Remove the literal row.
    const removeButtons = screen.getAllByLabelText("Remove");
    fireEvent.click(removeButtons[0]);
    expect(screen.getAllByPlaceholderText("VAR_NAME")).toHaveLength(1);
  });

  it("flags duplicate env names as invalid", async () => {
    stubTemplate();
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);
    fireEvent.click(screen.getByRole("tab", { name: /Environment/i }));

    fireEvent.click(screen.getByRole("button", { name: /Add variable/i }));
    fireEvent.click(screen.getByRole("button", { name: /Add variable/i }));
    const inputs = screen.getAllByPlaceholderText("VAR_NAME");
    fireEvent.change(inputs[0], { target: { value: "FOO" } });
    fireEvent.change(inputs[1], { target: { value: "FOO" } });

    expect(screen.getAllByText("Duplicate name")).toHaveLength(2);
  });

  it("PUTs merged object that preserves unknown spec fields and uses latest resourceVersion", async () => {
    let putBody: unknown = null;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) {
        return jsonRes({
          metadata: { name: "minecraft-java" },
          spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" },
        });
      }
      if (url === "/servers/mc-survival" && (!init || init.method === "GET")) {
        // Latest server-side state, with a NEW resourceVersion and an
        // operator-added annotation the UI never saw.
        return jsonRes({
          metadata: {
            name: "mc-survival",
            resourceVersion: "200",
            annotations: { "gameplane.local/managed-by-operator": "true" },
          },
          spec: { templateRef: { name: "minecraft-java" } },
          status: { phase: "Running" },
        });
      }
      if (url === "/servers/mc-survival" && init?.method === "PUT") {
        putBody = JSON.parse(init.body as string);
        return jsonRes(putBody);
      }
      throw new Error(`unexpected fetch: ${url} ${init?.method ?? "GET"}`);
    });

    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);

    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), {
      target: { value: "test desc" },
    });

    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));

    await waitFor(() => expect(putBody).not.toBeNull());

    const body = putBody as GameServer;
    // Latest resourceVersion preserved.
    expect(body.metadata.resourceVersion).toBe("200");
    // Operator-managed annotation preserved.
    expect(body.metadata.annotations?.["gameplane.local/managed-by-operator"]).toBe("true");
    // User-edited annotation applied.
    expect(body.metadata.annotations?.["gameplane.local/description"]).toBe("test desc");
  });

  it("keeps concurrent stop and version edits when saving a description", async () => {
    let putBody: GameServer | null = null;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) return jsonRes({ metadata: { name: "minecraft-java" }, spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" } });
      if (url === "/servers/mc-survival" && init?.method === "PUT") {
        putBody = JSON.parse(init.body as string) as GameServer;
        return jsonRes(putBody);
      }
      if (url === "/servers/mc-survival") return jsonRes(gs({ spec: { templateRef: { name: "minecraft-java" }, suspend: true, version: "1.21" } }));
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<SettingsTab gs={gs({ spec: { templateRef: { name: "minecraft-java" }, suspend: false, version: "1.20" } })} name="mc-survival" />);
    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), { target: { value: "new description" } });
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(putBody).not.toBeNull());
    const body = putBody as GameServer | null;
    expect(body?.spec).toEqual({ templateRef: { name: "minecraft-java" }, suspend: true, version: "1.21" });
  });

  it("offers reload without writing when the same description changed concurrently", async () => {
    let writes = 0;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) return jsonRes({ metadata: { name: "minecraft-java" }, spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" } });
      if (init?.method === "PUT") writes++;
      return jsonRes(gs({ metadata: { name: "mc-survival", annotations: { "gameplane.local/description": "someone else's edit" } } }));
    });
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);
    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), { target: { value: "my edit" } });
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(screen.getByText(/changed since you opened this page/i)).toBeInTheDocument());
    expect(writes).toBe(0);
  });

  it("cannot save invalid game config after switching sections and recovers after correction", async () => {
    let writes = 0;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) return jsonRes({
        metadata: { name: "minecraft-java" },
        spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x", configSchema: [{ name: "MAX_PLAYERS", displayName: "Max players", type: "int", default: "16" }] },
      });
      if (init?.method === "PUT") { writes++; return jsonRes(JSON.parse(init.body as string)); }
      if (url === "/servers/mc-survival") return jsonRes(gs());
      throw new Error(`unexpected fetch: ${url}`);
    });
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);
    fireEvent.click(await screen.findByRole("tab", { name: /Game configuration/i }));
    fireEvent.change(screen.getByLabelText(/Max players/), { target: { value: "1.5" } });
    fireEvent.click(screen.getByRole("tab", { name: /^General$/i }));
    const save = screen.getByRole("button", { name: /Save changes/i });
    expect(save).toBeDisabled();
    fireEvent.click(save);
    expect(writes).toBe(0);
    fireEvent.click(screen.getByRole("tab", { name: /Game configuration/i }));
    fireEvent.change(screen.getByLabelText(/Max players/), { target: { value: "20" } });
    fireEvent.click(screen.getByRole("tab", { name: /^General$/i }));
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => expect(writes).toBe(1));
  });

  it("waits for the template before allowing a draft to save", async () => {
    let resolveTemplate!: (response: Response) => void;
    const pendingTemplate = new Promise<Response>((resolve) => { resolveTemplate = resolve; });
    fetchMock.mockImplementation(() => pendingTemplate);
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);
    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), { target: { value: "edited" } });
    expect(screen.getByRole("button", { name: /Save changes/i })).toBeDisabled();
    await act(async () => resolveTemplate(jsonRes({ metadata: { name: "minecraft-java" }, spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" } })));
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
  });

  it("retains an editor validation failure on navigation and resets editor text on discard", async () => {
    stubTemplate();
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);
    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), { target: { value: "edited" } });
    fireEvent.click(screen.getByRole("tab", { name: /Network capture/i }));
    fireEvent.change(screen.getByLabelText("Retention window value"), { target: { value: "8" } });
    fireEvent.click(screen.getByRole("tab", { name: /^General$/i }));
    expect(screen.getByRole("button", { name: /Save changes/i })).toBeDisabled();
    fireEvent.click(screen.getByRole("tab", { name: /Network capture/i }));
    fireEvent.change(screen.getByLabelText("Retention window value"), { target: { value: "8" } });
    fireEvent.click(screen.getByRole("button", { name: /Discard/i }));
    expect(screen.getByLabelText("Retention window value")).toHaveValue("");
    expect(screen.queryByText(/Exceeds the cluster maximum/i)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: /^General$/i }));
    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), { target: { value: "new edit" } });
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
  });

  it("surfaces a reload prompt on 409 conflict", async () => {
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) {
        return jsonRes({
          metadata: { name: "minecraft-java" },
          spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" },
        });
      }
      if (url === "/servers/mc-survival" && (!init || init.method === "GET")) {
        return jsonRes(gs());
      }
      if (init?.method === "PUT") {
        return jsonRes({ message: "conflict" }, 409);
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);

    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), {
      target: { value: "x" },
    });
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));

    await waitFor(() =>
      expect(screen.getByText(/changed since you opened this page/i)).toBeInTheDocument(),
    );
  });

  it("requires typing the server name to delete from the danger zone", async () => {
    stubTemplate();
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);
    fireEvent.click(screen.getByRole("tab", { name: /Danger zone/i }));

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const confirmBtn = screen.getByRole("button", { name: /^Delete server$/i });
    expect(confirmBtn).toBeDisabled();

    const phraseInput = screen.getByRole("textbox");
    fireEvent.change(phraseInput, { target: { value: "wrong" } });
    expect(confirmBtn).toBeDisabled();

    fireEvent.change(phraseInput, { target: { value: "mc-survival" } });
    expect(confirmBtn).toBeEnabled();

    fetchMock.mockImplementationOnce(async () => new Response(null, { status: 204 }));
    await act(async () => {
      fireEvent.click(confirmBtn);
    });
    await waitFor(() => expect(navigateMock).toHaveBeenCalledWith({ to: "/servers" }));
  });

  it("clears dirty flag after successful save", async () => {
    const onDirty = vi.fn();
    stubTemplate();
    let putCalled = false;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) {
        return jsonRes({
          metadata: { name: "minecraft-java" },
          spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" },
        });
      }
      if (url === "/servers/mc-survival" && (!init || init.method === "GET")) {
        return jsonRes(gs());
      }
      if (url === "/servers/mc-survival" && init?.method === "PUT") {
        putCalled = true;
        const body = JSON.parse(init.body as string);
        return jsonRes(body);
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" onDirtyChange={onDirty} />);
    await waitFor(() => expect(onDirty).toHaveBeenCalledWith(false));
    onDirty.mockClear();

    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), {
      target: { value: "test" },
    });
    await waitFor(() => expect(onDirty).toHaveBeenCalledWith(true));

    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() => {
      expect(putCalled).toBe(true);
      expect(onDirty).toHaveBeenCalledWith(false);
    });
    expect(screen.getByText(/Saved/i)).toBeInTheDocument();
  });

  it("shows error message on save failure (non-409)", async () => {
    stubTemplate();
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) {
        return jsonRes({
          metadata: { name: "minecraft-java" },
          spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" },
        });
      }
      if (url === "/servers/mc-survival" && (!init || init.method === "GET")) {
        return jsonRes(gs());
      }
      if (init?.method === "PUT") {
        return jsonRes({ error: "internal server error" }, 500);
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);

    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), {
      target: { value: "x" },
    });
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));

    await waitFor(() =>
      expect(screen.getByText(/internal server error/i)).toBeInTheDocument(),
    );
  });

  it("disables save button when section is invalid", async () => {
    stubTemplate();
    const { unmount } = renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);

    // General section is safe, but let's switch to Lifecycle and make it invalid
    fireEvent.click(screen.getByRole("tab", { name: /Lifecycle/i }));

    // Make a change first
    fireEvent.change(screen.getAllByRole("textbox")[0], {
      target: { value: "30" },
    });

    // render() doesn't replace the previous tree, it mounts a second one
    // alongside it — unmount first, or every subsequent screen query below
    // (starting with "Lifecycle") is ambiguous across both instances.
    unmount();

    // Now create invalid grace period state
    renderWithQuery(
      <SettingsTab
        gs={{
          ...gs(),
          spec: { ...gs().spec, stopGracePeriodSeconds: 999 },
        }}
        name="mc-survival"
      />,
    );

    fireEvent.click(screen.getByRole("tab", { name: /Lifecycle/i }));
    const saveBtn = screen.getByRole("button", { name: /Save changes/i });
    // Section is invalid (grace period > 600), so save should be disabled
    expect(saveBtn).toBeDisabled();
  });

  it("clears error when editing after error", async () => {
    stubTemplate();
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) {
        return jsonRes({
          metadata: { name: "minecraft-java" },
          spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" },
        });
      }
      if (url === "/servers/mc-survival" && (!init || init.method === "GET")) {
        return jsonRes(gs());
      }
      if (init?.method === "PUT") {
        return jsonRes({ error: "something failed" }, 500);
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);

    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), {
      target: { value: "x" },
    });
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));

    await waitFor(() =>
      expect(screen.getByText(/something failed/i)).toBeInTheDocument(),
    );

    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), {
      target: { value: "y" },
    });

    // Error should remain until successful save or reset
    expect(screen.getByText(/something failed/i)).toBeInTheDocument();
  });

  it("discard button is disabled when not dirty", () => {
    stubTemplate();
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);

    const discardBtn = screen.getByRole("button", { name: /Discard/i });
    expect(discardBtn).toBeDisabled();
  });

  it("discard button resets draft to baseline", async () => {
    const onDirty = vi.fn();
    stubTemplate();
    renderWithQuery(
      <SettingsTab
        gs={gs({ metadata: { name: "mc-survival", annotations: { "gameplane.local/description": "old" } } })}
        name="mc-survival"
        onDirtyChange={onDirty}
      />,
    );

    await waitFor(() => expect(onDirty).toHaveBeenCalledWith(false));

    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), {
      target: { value: "new" },
    });
    await waitFor(() => expect(onDirty).toHaveBeenCalledWith(true));

    const discardBtn = screen.getByRole("button", { name: /Discard/i });
    fireEvent.click(discardBtn);

    await waitFor(() => expect(onDirty).toHaveBeenCalledWith(false));
    // Verify the field is reset back to old
    const textarea = screen.getByPlaceholderText(/Long-standing/) as HTMLTextAreaElement;
    expect(textarea.value).toBe("old");
  });

  it("reload button fetches fresh server and resets local state", async () => {
    stubTemplate();
    let fetchCount = 0;
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) {
        return jsonRes({
          metadata: { name: "minecraft-java" },
          spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" },
        });
      }
      if (url === "/servers/mc-survival" && (!init || init.method === "GET")) {
        fetchCount++;
        if (fetchCount === 1) {
          return jsonRes(gs({ metadata: { name: "mc-survival", annotations: {} } }));
        }
        return jsonRes(gs({ metadata: { name: "mc-survival", annotations: { "gameplane.local/description": "reloaded" } } }));
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    const onDirty = vi.fn();
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" onDirtyChange={onDirty} />);

    await waitFor(() => expect(onDirty).toHaveBeenCalledWith(false));
    onDirty.mockClear();

    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), {
      target: { value: "edited" },
    });
    await waitFor(() => expect(onDirty).toHaveBeenCalledWith(true));

    // Simulate 409 conflict. save.mutate's mutationFn does a GET (fetch
    // latest to merge onto) *then* a PUT — mockImplementationOnce only
    // swaps out a single call, so it intercepted the GET and left the PUT
    // falling through to the earlier fetchCount-based mock (which has no
    // PUT branch, so it threw "unexpected fetch"). Use a persistent
    // override so both the mutation's internal GET and its PUT — plus the
    // GET the "Reload" button issues afterward — are handled.
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) {
        return jsonRes({
          metadata: { name: "minecraft-java" },
          spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" },
        });
      }
      if (url === "/servers/mc-survival" && init?.method === "PUT") {
        return jsonRes({ error: "conflict" }, 409);
      }
      if (url === "/servers/mc-survival" && (!init || init.method === "GET")) {
        return jsonRes(gs({ metadata: { name: "mc-survival", annotations: { "gameplane.local/description": "reloaded" } } }));
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() =>
      expect(screen.getByText(/changed since you opened this page/i)).toBeInTheDocument(),
    );

    const reloadBtn = screen.getByRole("button", { name: /Reload/i });
    fireEvent.click(reloadBtn);

    await waitFor(() => {
      expect(screen.queryByText(/changed since you opened this page/i)).not.toBeInTheDocument();
      expect(onDirty).toHaveBeenCalledWith(false);
    });
  });

  // F-137: reload() had no try/catch — a failed GET during the conflict
  // banner's "Reload" rejected silently (uncaught in promise), no message.
  it("shows an error when Reload's fetch fails", async () => {
    stubTemplate();
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);
    await waitFor(() => screen.getByPlaceholderText(/Long-standing/));

    // The save mutation's own GET (fetch latest to merge onto, before the
    // PUT) must succeed here so the conflict banner actually appears — the
    // 502 below is installed only for the *next* GET, which is the one the
    // "Reload" button issues.
    fetchMock.mockImplementation(async (url: string, init?: FetchInit) => {
      if (url.startsWith("/templates/")) {
        return jsonRes({
          metadata: { name: "minecraft-java" },
          spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" },
        });
      }
      if (url === "/servers/mc-survival" && init?.method === "PUT") {
        return jsonRes({ error: "conflict" }, 409);
      }
      if (url === "/servers/mc-survival" && (!init || init.method === "GET")) {
        return jsonRes(gs({ metadata: { name: "mc-survival", annotations: { "gameplane.local/description": "reloaded" } } }));
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    fireEvent.change(screen.getByPlaceholderText(/Long-standing/), {
      target: { value: "edited" },
    });
    await waitFor(() => expect(screen.getByRole("button", { name: /Save changes/i })).toBeEnabled());
    fireEvent.click(screen.getByRole("button", { name: /Save changes/i }));
    await waitFor(() =>
      expect(screen.getByText(/changed since you opened this page/i)).toBeInTheDocument(),
    );

    // Only the Reload button's own GET should fail — install the 502 for
    // just that one upcoming call.
    fetchMock.mockImplementationOnce(async (url: string, init?: FetchInit) => {
      if (url === "/servers/mc-survival" && (!init || init.method === "GET")) {
        return jsonRes({ error: "server unreachable" }, 502);
      }
      throw new Error(`unexpected fetch during reload: ${url}`);
    });

    fireEvent.click(screen.getByRole("button", { name: /Reload/i }));

    // The conflict banner is replaced by an error, not silently swallowed.
    await waitFor(() => {
      expect(screen.getByText(/server unreachable/i)).toBeInTheDocument();
    });
  });

  it("filters out version section when template has no versions", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/templates/")) {
        return jsonRes({
          metadata: { name: "minecraft-java" },
          spec: { displayName: "Minecraft", game: "minecraft-java", version: "1.0", image: "x" },
          // No versions array
        });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);

    // Version tab should not be in the nav
    expect(screen.queryByRole("tab", { name: /^Version$/i })).not.toBeInTheDocument();
  });

  it("includes version section when template has versions", async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.startsWith("/templates/")) {
        return jsonRes({
          metadata: { name: "minecraft-java" },
          spec: {
            displayName: "Minecraft",
            game: "minecraft-java",
            version: "1.0",
            image: "x",
            versions: [
              { id: "1.19", label: "1.19" },
              { id: "1.20", label: "1.20" },
            ],
          },
        });
      }
      throw new Error(`unexpected fetch: ${url}`);
    });

    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);

    // Version tab should be in the nav
    await waitFor(() => {
      expect(screen.getByRole("tab", { name: /^Version$/i })).toBeInTheDocument();
    });
  });

  it("navigates to Share links section between Access and Danger tabs", async () => {
    stubTemplate();
    renderWithQuery(<SettingsTab gs={gs()} name="mc-survival" />);

    // Share links tab should exist
    const shareLinksTab = screen.getByRole("tab", { name: /Share links/i });
    expect(shareLinksTab).toBeInTheDocument();

    // Click to navigate to Share links section
    fireEvent.click(shareLinksTab);

    // Section should render
    await waitFor(() => {
      expect(screen.getByText(/Let people without a Gameplane account/i)).toBeInTheDocument();
    });

    // Verify it's between Access and Danger by checking tab order
    const tabs = screen.getAllByRole("tab");
    const accessIdx = tabs.findIndex((t) => t.textContent?.includes("RBAC"));
    const shareLinksIdx = tabs.findIndex((t) => t.textContent?.includes("Share links"));
    const dangerIdx = tabs.findIndex((t) => t.textContent?.includes("Danger"));
    expect(accessIdx).toBeLessThan(shareLinksIdx);
    expect(shareLinksIdx).toBeLessThan(dangerIdx);
  });
});

function renderWithQuery(ui: ReactElement, options?: Parameters<typeof baseRenderWithQuery>[1]) {
  const props = ui.props as { name?: string; ns?: string };
  return baseRenderWithQuery(<ResourceTargetProvider target={{ cluster: "local", name: props.name ?? "alpha", namespace: props.ns }} access={{ canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: true, isCollaborator: false, permissions: ["*"] }}>{ui}</ResourceTargetProvider>, options);
}
