import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";
import type { GameTemplate } from "@/types";

const navigate = vi.fn();
let search: { template?: string; cluster?: string; ns?: string } = {};
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
  useSearch: () => search,
}));

import {
  CreateServerWizard,
  nodeCaps,
  nonEmptyPortOverrides,
  parseSourceRanges,
} from "./CreateServer";
import { gameCategory } from "@/lib/games";

interface FetchInit {
  method?: string;
  headers?: Record<string, string>;
  body?: string;
}

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  fetchMock.mockReset();
  navigate.mockReset();
  search = {};
  vi.unstubAllGlobals();
});

function jsonRes(status: number, body: unknown): Response {
  const list = body as { items?: GameTemplate[] };
  if (Array.isArray(list?.items)) body = { items: [{ cluster: "local", namespace: "gameplane-games", templates: list.items }], partial: false, issues: [], totalReturned: 1 };
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

// The wizard fires several GETs on mount (templates + cluster node caps),
// so ordered once-mocks are fragile — an extra GET shifts the queue. This
// routes by URL/method instead, resolving each call correctly regardless
// of order: /cluster → node caps, POST → the create response, else the
// template list.
function routeFetch(opts: { templates?: GameTemplate[]; create?: Response } = {}) {
  fetchMock.mockImplementation((url: string, init?: FetchInit) => {
    if (url.includes("/cluster")) return Promise.resolve(jsonRes(200, { nodes: [] }));
    if ((init?.method ?? "GET") === "POST") {
      return Promise.resolve(opts.create ?? jsonRes(201, { metadata: { name: "mc-test" } }));
    }
    return Promise.resolve(jsonRes(200, { items: opts.templates ?? [template()] }));
  });
}

function template(overrides: Partial<GameTemplate["spec"]> = {}): GameTemplate {
  return {
    metadata: { name: "minecraft-java" },
    spec: {
      displayName: "Minecraft Java",
      game: "minecraft",
      version: "1.21",
      image: "itzg/minecraft-server",
      ...overrides,
    },
  };
}

async function pickTemplate(t: GameTemplate) {
  fireEvent.click(await screen.findByRole("button", { name: new RegExp(t.spec.displayName, "i") }));
  fireEvent.click(screen.getByRole("button", { name: /Continue to Configure/i }));
}

function versionedTemplate(): GameTemplate {
  return template({
    versions: [
      { id: "1.21.4-paper", displayName: "1.21.4 Paper", loader: "paper", default: true },
      { id: "1.21.4-forge", displayName: "1.21.4 Forge", loader: "forge" },
    ],
  });
}

describe("CreateServerWizard", () => {
  it("blocks Continue with a reason when the name is invalid", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { items: [template()] }));
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());

    const nameInput = screen.getByPlaceholderText("mc-hardcore");
    fireEvent.change(nameInput, { target: { value: "MC TEST" } });

    const continueBtn = screen.getByRole("button", { name: /Continue to Network/i });
    expect(continueBtn).toBeDisabled();
    expect(screen.getByTestId("step-reason").textContent).toMatch(/lowercase letters/);
  });

  it("blocks Continue when a required template config field is missing", async () => {
    const t = template({
      configSchema: [
        { name: "VERSION", type: "string", required: true },
      ],
    });
    fetchMock.mockResolvedValueOnce(jsonRes(200, { items: [t] }));
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(t);

    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), {
      target: { value: "mc-test" },
    });

    const continueBtn = screen.getByRole("button", { name: /Continue to Network/i });
    expect(continueBtn).toBeDisabled();
    expect(screen.getByTestId("step-reason").textContent).toMatch(/VERSION is required/);
  });

  it("renders an inline alert when the API returns 409", async () => {
    routeFetch({ create: new Response("name taken", { status: 409 }) });

    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());

    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), {
      target: { value: "mc-test" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    const alert = await screen.findByTestId("create-error");
    expect(alert.textContent).toMatch(/A server named mc-test already exists/);
    expect(navigate).not.toHaveBeenCalled();
  });

  it("pre-selects the template from the ?template= search param", async () => {
    search = { template: "minecraft-java" };
    fetchMock.mockResolvedValueOnce(jsonRes(200, { items: [template()] }));
    renderWithQuery(<CreateServerWizard />);

    // Without pre-selection, the name input (step 2) is never reachable; its
    // presence means step 1 auto-advanced past template selection isn't
    // needed — the template is already chosen, so Continue is enabled.
    const continueBtn = await screen.findByRole("button", { name: /Continue to Configure/i });
    await waitFor(() => expect(continueBtn).toBeEnabled());
    // The preview reflects the chosen template name.
    expect(screen.getAllByText(/Minecraft Java/i).length).toBeGreaterThan(0);
  });

  it("does not pre-select when no ?template= is present", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { items: [template()] }));
    renderWithQuery(<CreateServerWizard />);

    await screen.findByRole("button", { name: new RegExp(template().spec.displayName, "i") });
    const continueBtn = screen.getByRole("button", { name: /Continue to Configure/i });
    expect(continueBtn).toBeDisabled();
  });

  it("sends nodeSelector when nodePlacement is 'pin'", async () => {
    routeFetch();

    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());

    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), {
      target: { value: "mc-test" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Pin to node/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    expect(body.kind).toBe("GameServer");
    expect(body.spec.nodeSelector).toEqual({ "gameplane.local/pinned": "true" });
  });

  it("sends requests equal to limits (Guaranteed QoS)", async () => {
    routeFetch();

    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());

    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), {
      target: { value: "mc-test" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    // Defaults from the wizard state: 2 cores / 4Gi.
    expect(body.spec.resources.requests).toEqual({ cpu: "2", memory: "4Gi" });
    expect(body.spec.resources.limits).toEqual({ cpu: "2", memory: "4Gi" });
  });

  it("sends requests below the limits when reserving less (Burstable QoS)", async () => {
    routeFetch();

    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());

    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), {
      target: { value: "mc-test" },
    });
    expect(screen.queryByLabelText("Reserved CPU value")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("switch", { name: "Reserve less than the limit" }));
    // The reservation starts at the limits.
    expect(screen.getByLabelText("Reserved CPU value")).toHaveValue(2);
    expect(screen.getByLabelText("Reserved memory value")).toHaveValue(4);

    const cpu = screen.getByLabelText("Reserved CPU value");
    fireEvent.change(cpu, { target: { value: "0.5" } });
    fireEvent.blur(cpu);
    const memory = screen.getByLabelText("Reserved memory value");
    fireEvent.change(memory, { target: { value: "1" } });
    fireEvent.blur(memory);

    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    expect(screen.getByText("Reserved CPU")).toBeInTheDocument();
    expect(screen.getByText("500m")).toBeInTheDocument();
    expect(screen.getByText("Reserved memory")).toBeInTheDocument();
    expect(screen.getByText("1Gi")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    expect(body.spec.resources.requests).toEqual({ cpu: "500m", memory: "1Gi" });
    expect(body.spec.resources.limits).toEqual({ cpu: "2", memory: "4Gi" });
  });

  it("lowering a limit below the reservation lowers the reservation too", async () => {
    routeFetch();

    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());

    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), {
      target: { value: "mc-test" },
    });
    await userEvent.click(screen.getByRole("switch", { name: "Reserve less than the limit" }));

    const cpuLimit = screen.getByLabelText("CPU cores value");
    fireEvent.change(cpuLimit, { target: { value: "1" } });
    fireEvent.blur(cpuLimit);
    const memoryLimit = screen.getByLabelText("Memory (GiB) value");
    fireEvent.change(memoryLimit, { target: { value: "2" } });
    fireEvent.blur(memoryLimit);
    expect(screen.getByLabelText("Reserved CPU value")).toHaveValue(1);
    expect(screen.getByLabelText("Reserved memory value")).toHaveValue(2);

    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    expect(body.spec.resources.requests).toEqual({ cpu: "1", memory: "2Gi" });
    expect(body.spec.resources.limits).toEqual({ cpu: "1", memory: "2Gi" });
  });

  it("keeps the 4-step flow when the template declares no versions", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { items: [template()] }));
    renderWithQuery(<CreateServerWizard />);
    fireEvent.click(await screen.findByRole("button", { name: /Minecraft Java/i }));
    expect(screen.getByRole("button", { name: /Continue to Configure/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Continue to Version/i })).not.toBeInTheDocument();
  });

  it("inserts a Version step and pre-selects the default for versioned templates", async () => {
    fetchMock.mockResolvedValueOnce(jsonRes(200, { items: [versionedTemplate()] }));
    renderWithQuery(<CreateServerWizard />);
    fireEvent.click(await screen.findByRole("button", { name: /Minecraft Java/i }));
    // The step after Template is Version, not Configure.
    fireEvent.click(screen.getByRole("button", { name: /Continue to Version/i }));
    expect(screen.getByText("1.21.4 Paper")).toBeInTheDocument();
    expect(screen.getByText("Default")).toBeInTheDocument();
    // The default seeds a valid selection, so Configure is reachable.
    expect(screen.getByRole("button", { name: /Continue to Configure/i })).toBeEnabled();
  });

  it("includes the selected version in the create body", async () => {
    routeFetch({ templates: [versionedTemplate()] });

    renderWithQuery(<CreateServerWizard />);
    fireEvent.click(await screen.findByRole("button", { name: /Minecraft Java/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Version/i }));
    // Switch from the default (paper) to forge.
    fireEvent.click(screen.getByRole("button", { name: /1\.21\.4 Forge/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Configure/i }));
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    expect(body.spec.version).toBe("1.21.4-forge");
  });

  it("filters templates by search text and category", async () => {
    const minecraftTemplate: GameTemplate = {
      metadata: { name: "minecraft-java" },
      spec: {
        displayName: "Minecraft Java",
        game: "minecraft",
        categories: ["Sandbox"],
        version: "1.21",
        image: "itzg/minecraft-server",
      },
    };
    const valheimTemplate: GameTemplate = {
      metadata: { name: "valheim" },
      spec: {
        displayName: "Valheim",
        game: "valheim",
        categories: ["Survival"],
        version: "1.0",
        image: "x",
      },
    };
    fetchMock.mockResolvedValue(jsonRes(200, { items: [minecraftTemplate, valheimTemplate] }));
    renderWithQuery(<CreateServerWizard />);

    // Both templates visible by default.
    expect(await screen.findByRole("button", { name: /Minecraft Java/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Valheim/i })).toBeInTheDocument();

    // Search narrows to matching templates.
    fireEvent.change(screen.getByLabelText("Search templates"), { target: { value: "valheim" } });
    expect(screen.queryByRole("button", { name: /Minecraft Java/i })).toBeNull();
    expect(screen.getByRole("button", { name: /Valheim/i })).toBeInTheDocument();

    // Category pill filters by the declared category (Sandbox).
    fireEvent.change(screen.getByLabelText("Search templates"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Sandbox" }));
    expect(screen.getByRole("button", { name: /Minecraft Java/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Valheim/i })).toBeNull();
  });

  it("renders all category chips reachable on step 1 (Bug 1: category overflow)", async () => {
    const templates: GameTemplate[] = [
      {
        metadata: { name: "minecraft" },
        spec: { displayName: "Minecraft", game: "minecraft", categories: ["Sandbox"], version: "1.21", image: "x" },
      },
      {
        metadata: { name: "terraria" },
        spec: { displayName: "Terraria", game: "terraria", categories: ["Sandbox"], version: "1.0", image: "x" },
      },
      {
        metadata: { name: "valheim" },
        spec: { displayName: "Valheim", game: "valheim", categories: ["Survival"], version: "1.0", image: "x" },
      },
      {
        metadata: { name: "7days" },
        spec: { displayName: "7 Days to Die", game: "7days", categories: ["Survival"], version: "1.0", image: "x" },
      },
      {
        metadata: { name: "ark" },
        spec: { displayName: "ARK", game: "ark", categories: ["Adventure"], version: "1.0", image: "x" },
      },
      {
        metadata: { name: "gmod" },
        spec: { displayName: "Garry's Mod", game: "gmod", categories: ["Sandbox"], version: "1.0", image: "x" },
      },
    ];
    fetchMock.mockResolvedValue(jsonRes(200, { items: templates }));
    renderWithQuery(<CreateServerWizard />);

    // "All" renders unconditionally (even before the template query
    // resolves), so awaiting it wouldn't prove the derived category chips
    // are up. Await a chip that only exists once the templates have loaded.
    await screen.findByRole("button", { name: "Sandbox" });
    expect(screen.getByRole("button", { name: "All" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Survival" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Adventure" })).toBeInTheDocument();

    // Verify they're all clickable and the page doesn't have overflow clipping
    // by checking that they have aria-pressed attribute (indicating they're interactive buttons)
    const categoryButtons = screen.getAllByRole("button", { pressed: false });
    expect(categoryButtons.length).toBeGreaterThanOrEqual(3);

    // Regression guard for Bug 1: the row must wrap rather than scroll
    // behind a clipped overflow container. If `flex-wrap` is dropped or
    // `overflow-x-auto` comes back, this fails even though jsdom does no
    // layout, because it's asserting on the classes directly.
    const chipRow = screen.getByRole("button", { name: "Sandbox" }).closest("div.flex");
    expect(chipRow?.className).toContain("flex-wrap");
    expect(chipRow?.className).not.toContain("overflow-x-auto");
  });

  it("displays the preview panel inside the modal without clipping on step 1 (Bug 2: preview overflow)", async () => {
    const minecraftTemplate: GameTemplate = {
      metadata: { name: "minecraft-java" },
      spec: {
        displayName: "Minecraft Java Edition",
        game: "minecraft",
        version: "1.21",
        image: "itzg/minecraft-server",
      },
    };
    fetchMock.mockResolvedValue(jsonRes(200, { items: [minecraftTemplate] }));
    renderWithQuery(<CreateServerWizard />);

    // Pre-select the template to show the preview reflecting a real
    // selection — once selected, the preview header shows the template's
    // own name instead of the unselected "Pick a template" placeholder.
    fireEvent.click(await screen.findByRole("button", { name: /Minecraft Java/i }));

    // The template name should appear in the preview header and not be cut off.
    const previewElements = screen.getAllByText(/Minecraft Java Edition/i);
    expect(previewElements.length).toBeGreaterThan(0);
    // The preview's static footer content is always present.
    expect(screen.getByText(/Memory tip/i)).toBeInTheDocument();

    // Regression guard for Bug 2: the preview panel and its grid column
    // must be min-w-0, or the panel escapes its column and gets clipped by
    // the modal's overflow-hidden instead of shrinking to fit.
    const preview = screen.getByText(/Memory tip/i).closest("aside");
    expect(preview?.className).toContain("min-w-0");

    // Verify the modal doesn't have overflow by checking the outer container
    const modal = screen.getByRole("dialog");
    expect(modal).toBeTruthy();
    if (modal) {
      // The modal must have overflow-hidden to bound content and prevent clipping.
      // We check className since jsdom doesn't resolve Tailwind computed styles.
      // This assertion catches regressions if overflow-hidden is removed.
      expect(modal.className).toContain("overflow-hidden");
    }
  });
});

describe("parseSourceRanges", () => {
  it("splits on newlines and commas, trimming blanks", () => {
    expect(parseSourceRanges("203.0.113.0/24\n10.0.0.0/8")).toEqual([
      "203.0.113.0/24",
      "10.0.0.0/8",
    ]);
    expect(parseSourceRanges(" 1.2.3.0/24 , , 5.6.7.0/24 ")).toEqual([
      "1.2.3.0/24",
      "5.6.7.0/24",
    ]);
    expect(parseSourceRanges("")).toEqual([]);
  });
});

describe("nonEmptyPortOverrides", () => {
  it("drops rows with a blank or whitespace-only name", () => {
    expect(
      nonEmptyPortOverrides([
        { name: "game", nodePort: 30005 },
        { name: "" },
        { name: "  " },
      ]),
    ).toEqual([{ name: "game", nodePort: 30005 }]);
    expect(nonEmptyPortOverrides([])).toEqual([]);
  });
});

describe("nodeCaps", () => {
  it("returns the largest single node's CPU cores and memory GiB", () => {
    const caps = nodeCaps([
      { cpu: { capacity: 4 }, memory: { capacity: 14656061440 } }, // ~13.6 GiB
      { cpu: { capacity: 8 }, memory: { capacity: 8 * 1024 ** 3 } },
    ]);
    expect(caps.maxCpu).toBe(8);
    // Largest memory node is the ~13.6 GiB one → floor to 13 GiB.
    expect(caps.maxMemGi).toBe(13);
  });
  it("is {0,0} when node data is unavailable (no cap)", () => {
    expect(nodeCaps([])).toEqual({ maxCpu: 0, maxMemGi: 0 });
  });
});

describe("CreateServerWizard networking", () => {
  it("sends sourceRanges when LoadBalancer + CIDRs are set", async () => {
    routeFetch();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));

    fireEvent.click(screen.getByRole("button", { name: /LoadBalancer/i }));
    fireEvent.change(screen.getByLabelText("IP allow-list"), {
      target: { value: "203.0.113.0/24\n10.0.0.0/8" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    expect(body.spec.networking.sourceRanges).toEqual(["203.0.113.0/24", "10.0.0.0/8"]);
  });

  it("sends portOverrides when a named override is added", async () => {
    routeFetch();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));

    fireEvent.click(screen.getByRole("button", { name: /Add override/i }));
    fireEvent.change(screen.getByPlaceholderText("game"), { target: { value: "game" } });
    fireEvent.change(screen.getByPlaceholderText("30000-32767"), { target: { value: "30005" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    // The review summary lists the named override.
    expect(screen.getByText("Port overrides")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    expect(body.spec.networking.portOverrides).toEqual([{ name: "game", nodePort: 30005 }]);
  });

  it("omits portOverrides when the only row is left blank", async () => {
    routeFetch();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));

    // An added-but-never-named row must not pollute the payload.
    fireEvent.click(screen.getByRole("button", { name: /Add override/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    expect(screen.queryByText("Port overrides")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    expect(body.spec.networking.portOverrides).toBeUndefined();
  });
});

describe("gameCategory", () => {
  it.each([
    ["minecraft", "Sandbox"],
    ["terraria", "Sandbox"],
    ["valheim", "Survival"],
    ["palworld", "Survival"],
    ["cs2", "Shooter"],
    ["something-unknown", "Other"],
  ])("maps %s -> %s", (game, want) => {
    expect(gameCategory(game)).toBe(want);
  });
});

describe("CreateServerWizard review", () => {
  it("Edit links jump back to the matching step", async () => {
    fetchMock.mockResolvedValue(jsonRes(200, { items: [template()] }));
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));

    // Template / Configuration / Network sections each expose an Edit link.
    const edits = screen.getAllByRole("button", { name: "Edit" });
    expect(edits.length).toBe(3);
    // Configuration is the second section → jumps back to the Configure step.
    fireEvent.click(edits[1]);
    expect(screen.getByPlaceholderText("mc-hardcore")).toBeInTheDocument();
  });

  it("bounds the Template-config preview so it can't escape the wizard", async () => {
    const t = template({
      configSchema: [{ name: "MOTD", displayName: "Server MOTD", type: "string", default: "hi" }],
    });
    fetchMock.mockResolvedValue(jsonRes(200, { items: [t] }));
    const { container } = renderWithQuery(<CreateServerWizard />);
    await pickTemplate(t);
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    // Editing a field is what populates state.config (defaults alone stay out),
    // which is what makes the Template-config <pre> render on the review step.
    fireEvent.change(screen.getByLabelText("Server MOTD"), { target: { value: "a long custom motd" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));

    const configPre = Array.from(container.querySelectorAll("pre")).find((p) =>
      p.textContent?.includes('"MOTD"'),
    );
    expect(configPre).toBeTruthy();
    // Bounded height + own scroll → it stays inside the card instead of
    // overflowing the wizard and becoming unreachable.
    expect(configPre?.className).toContain("overflow-auto");
    expect(configPre?.className).toMatch(/max-h-/);
  });

  it("masks password-type config values on the review step", async () => {
    const t = template({
      configSchema: [
        { name: "MOTD", displayName: "Server MOTD", type: "string" },
        { name: "ADMIN_PASSWORD", displayName: "Admin password", type: "password" },
      ],
    });
    fetchMock.mockResolvedValue(jsonRes(200, { items: [t] }));
    const { container } = renderWithQuery(<CreateServerWizard />);
    await pickTemplate(t);
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.change(screen.getByLabelText("Server MOTD"), { target: { value: "hello" } });
    fireEvent.change(screen.getByLabelText("Admin password"), { target: { value: "hunter2" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));

    const configPre = Array.from(container.querySelectorAll("pre")).find((p) =>
      p.textContent?.includes('"MOTD"'),
    );
    expect(configPre).toBeTruthy();
    expect(configPre?.textContent).toContain('"MOTD": "hello"');
    expect(configPre?.textContent).toContain('"ADMIN_PASSWORD": "********"');
    expect(configPre?.textContent).not.toContain("hunter2");
  });

  it("shows Cancel on step 1 and closes the wizard back to the server list", async () => {
    fetchMock.mockResolvedValue(jsonRes(200, { items: [template()] }));
    renderWithQuery(<CreateServerWizard />);
    const cancel = await screen.findByRole("button", { name: "Cancel" });
    fireEvent.click(cancel);
    // Design W8idqY: closing the create-server wizard returns to the server
    // list, not the dashboard.
    expect(navigate).toHaveBeenCalledWith({ to: "/servers" });
  });

  it("closes via the header Close button back to the server list", async () => {
    fetchMock.mockResolvedValue(jsonRes(200, { items: [template()] }));
    renderWithQuery(<CreateServerWizard />);
    const close = await screen.findByRole("button", { name: "Close" });
    fireEvent.click(close);
    expect(navigate).toHaveBeenCalledWith({ to: "/servers" });
  });

  // F-137: both Close and Cancel passed nav(...) straight to onPress,
  // leaving the returned promise unhandled. A rejecting navigate must not
  // throw out of the click handler.
  it("does not throw when navigate rejects on Cancel", async () => {
    navigate.mockRejectedValueOnce(new Error("navigation aborted"));
    fetchMock.mockResolvedValue(jsonRes(200, { items: [template()] }));
    renderWithQuery(<CreateServerWizard />);
    const cancel = await screen.findByRole("button", { name: "Cancel" });
    fireEvent.click(cancel);
    await waitFor(() => expect(navigate).toHaveBeenCalledWith({ to: "/servers" }));
  });

  it("sends addressPool and address when LoadBalancer + pool/address are set", async () => {
    routeFetch();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));

    fireEvent.click(screen.getByRole("button", { name: /LoadBalancer/i }));
    fireEvent.change(screen.getByPlaceholderText("pool-us-west"), {
      target: { value: "us-west-pool" },
    });
    fireEvent.change(screen.getByPlaceholderText("203.0.113.50"), {
      target: { value: "203.0.113.42" },
    });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    expect(body.spec.networking.addressPool).toBe("us-west-pool");
    expect(body.spec.networking.address).toBe("203.0.113.42");
  });

  it("omits addressPool and address when left empty", async () => {
    routeFetch();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));

    fireEvent.click(screen.getByRole("button", { name: /LoadBalancer/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    expect(body.spec.networking.addressPool).toBeUndefined();
    expect(body.spec.networking.address).toBeUndefined();
  });

  it("shows address preference ignored alert when pool is set with non-LoadBalancer expose", async () => {
    routeFetch();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));

    // NodePort is the default, so don't click LoadBalancer
    fireEvent.change(screen.getByPlaceholderText("pool-us-west"), {
      target: { value: "us-west-pool" },
    });

    expect(screen.getByText(/Address preference ignored/i)).toBeInTheDocument();
    expect(screen.getByText(/Address pool and requested address only take effect when Expose/i)).toBeInTheDocument();
  });

  it("omits addressPool and address from payload when only whitespace is entered", async () => {
    routeFetch();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate(template());
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));

    fireEvent.click(screen.getByRole("button", { name: /LoadBalancer/i }));
    fireEvent.change(screen.getByPlaceholderText("pool-us-west"), {
      target: { value: "   " },
    });
    fireEvent.change(screen.getByPlaceholderText("203.0.113.50"), {
      target: { value: "  \t  " },
    });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit).method === "POST")!;
    const body = JSON.parse((postCall[1] as FetchInit).body as string);
    expect(body.spec.networking.addressPool).toBeUndefined();
    expect(body.spec.networking.address).toBeUndefined();
  });
});

describe("explicit creation location", () => {
  it("does not replace an unavailable requested local site with a remote site", async () => {
    search = { cluster: "local", template: "minecraft-java" };
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ items: [{ cluster: "remote", namespace: "games", templates: [template()] }], partial: false, issues: [], totalReturned: 1 }), { status: 200, headers: { "Content-Type": "application/json" } }));
    renderWithQuery(<CreateServerWizard />);
    expect(await screen.findByText("No available location")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Continue to Configure/i })).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.every(([, options]) => !options?.method || options.method === "GET")).toBe(true);
  });
  it("selects the requested namespace's actual template without consulting a global selector", async () => {
    search = { cluster: "remote", ns: "second" };
    const first = template({ displayName: "First-site template" });
    const second = template({ displayName: "Second-site template" });
    fetchMock.mockImplementation((url: string) => Promise.resolve(new Response(JSON.stringify(url.startsWith("/fleet/placements") ? { items: [{ cluster: "remote", namespace: "first", templates: [first] }, { cluster: "remote", namespace: "second", templates: [second] }], partial: false, issues: [], totalReturned: 2 } : { nodes: [] }), { status: 200, headers: { "Content-Type": "application/json" } })));
    renderWithQuery(<CreateServerWizard />);
    expect(await screen.findByRole("button", { name: /Second-site template/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /First-site template/i })).not.toBeInTheDocument();
  });
});
