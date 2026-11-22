import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";
import type { GameTemplate, User } from "@/types";

const navigate = vi.fn();
vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => navigate,
  useSearch: () => ({}),
}));

import { CreateServerWizard } from "./CreateServer";

interface FetchInit {
  method?: string;
  body?: string;
}

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  fetchMock.mockReset();
  navigate.mockReset();
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

// An RFC 7807 problem body, the shape the API uses for refusals that carry
// a stable messageId and interpolation args (see lib/api.ts parseProblem).
// Without an IntlProvider the server.* catalog is not loaded, so the page falls
// back to the English detail carried in the problem body.
function problemRes(status: number, messageId: string, args: Record<string, string>, detail: string): Response {
  return new Response(
    JSON.stringify({ title: "Refused", status, detail, messageId, args }),
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

interface RouteOpts {
  me?: User;
  create?: Response;
}

// Order-independent router: the wizard fires several GETs on mount (the
// signed-in user, templates and cluster node caps), so each call is routed
// by URL and method rather than by queue position.
function routeFetch(opts: RouteOpts = {}) {
  fetchMock.mockImplementation((url: string, init?: FetchInit) => {
    if (url.includes("/users/me")) return Promise.resolve(jsonRes(200, opts.me ?? makeUser()));
    if (url.includes("/cluster")) return Promise.resolve(jsonRes(200, { nodes: [] }));
    if ((init?.method ?? "GET") === "POST") {
      return Promise.resolve(opts.create ?? jsonRes(201, { metadata: { name: "mc-test" } }));
    }
    return Promise.resolve(jsonRes(200, { items: [template()] }));
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

async function pickTemplate() {
  fireEvent.click(await screen.findByRole("button", { name: /Minecraft Java/i }));
  fireEvent.click(screen.getByRole("button", { name: /Continue to Configure/i }));
}

// Waits until the signed-in user has loaded. The Advanced section shows a
// "Read-only" chip while the user is unknown, so its absence means the
// permission check has resolved.
async function waitForPermissions() {
  await waitFor(() => expect(screen.queryByText("Read-only")).not.toBeInTheDocument());
}

function fillName() {
  fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), {
    target: { value: "mc-test" },
  });
}

function openAdvanced(user: ReturnType<typeof userEvent.setup>) {
  return user.click(screen.getByRole("button", { name: /^Advanced security/ }));
}

// Chooses "Custom value" in a setting's dropdown, types the number, and
// commits it the way the owner does (blur).
async function setCustomId(user: ReturnType<typeof userEvent.setup>, setting: RegExp, label: string, value: string) {
  await user.click(screen.getByRole("button", { name: setting }));
  await user.click(await screen.findByRole("option", { name: "Custom value" }));
  const input = screen.getByRole("spinbutton", { name: label });
  fireEvent.change(input, { target: { value } });
  fireEvent.blur(input);
}

function continueToReview() {
  fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
  fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
}

function createdBody(): { spec: Record<string, unknown> } {
  const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit | undefined)?.method === "POST")!;
  return JSON.parse((postCall[1] as FetchInit).body as string);
}

describe("CreateServerWizard — Advanced security on Configure", () => {
  it("shows the Advanced security section collapsed by default", async () => {
    routeFetch();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate();
    fillName();

    const toggle = screen.getByRole("button", { name: /^Advanced security/ });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    // Collapsed: no setting controls are mounted.
    expect(screen.queryByRole("button", { name: /Run-as user/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Filesystem group/ })).not.toBeInTheDocument();
  });

  it("expands to show one control per setting when the owner opens it", async () => {
    routeFetch();
    const user = userEvent.setup();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate();
    fillName();

    await openAdvanced(user);

    expect(screen.getByRole("button", { name: /^Advanced security/ })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("button", { name: /Run-as user/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Run-as group/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Filesystem group/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Volume ownership check/ })).toBeInTheDocument();
  });

  it("is read-only when the user lacks servers:security", async () => {
    routeFetch({ me: makeUser({ role: "viewer", permissions: { "*": ["servers:read"] } }) });
    const user = userEvent.setup();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate();
    fillName();

    await waitFor(() => expect(fetchMock.mock.calls.some((c) => String(c[0]).includes("/users/me"))).toBe(true));
    expect(await screen.findByText("Read-only")).toBeInTheDocument();

    await openAdvanced(user);

    expect(screen.getByText(/Changing them needs the servers:security permission/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Run-as user/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Filesystem group/ })).not.toBeInTheDocument();
  });

  it("is editable when the user holds servers:security", async () => {
    routeFetch();
    const user = userEvent.setup();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate();
    fillName();
    await waitForPermissions();

    await openAdvanced(user);

    expect(screen.queryByText("Read-only")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Run-as user/ })).toBeEnabled();
  });

  it("flags an out-of-range value inline and does not accept it", async () => {
    routeFetch();
    const user = userEvent.setup();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate();
    fillName();
    await waitForPermissions();
    await openAdvanced(user);

    await user.click(screen.getByRole("button", { name: /Run-as user/ }));
    await user.click(await screen.findByRole("option", { name: "Custom value" }));
    const input = screen.getByRole("spinbutton", { name: "Run-as user value" });
    fireEvent.change(input, { target: { value: "4294967296" } });
    fireEvent.blur(input);

    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("Enter a whole number from 0 to 4294967295.")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("step-reason")).toHaveTextContent("Fix the Advanced security values before continuing."));
    expect(screen.getByRole("button", { name: /Continue to Network/i })).toBeDisabled();
  });

  it("omits security from the create body when no override is set", async () => {
    routeFetch();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate();
    fillName();
    continueToReview();
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(createdBody().spec).not.toHaveProperty("security");
  });

  it("sends only the overrides that were set, in the create body", async () => {
    routeFetch();
    const user = userEvent.setup();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate();
    fillName();
    await waitForPermissions();
    await openAdvanced(user);

    await setCustomId(user, /Run-as user/, "Run-as user value", "1000");
    await user.click(screen.getByRole("button", { name: /Filesystem group/ }));
    await user.click(await screen.findByRole("option", { name: "Use image default" }));

    continueToReview();
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(createdBody().spec.security).toEqual({
      runAsUser: { value: 1000 },
      fsGroup: { useImageDefault: true },
    });
  });

  it("shows the refusal title when the API refuses root values on this cluster", async () => {
    routeFetch({
      create: problemRes(403, "server.security.root_disabled", { setting: "runAsUser" }, "root values (user 0, group 0) are not allowed on this cluster"),
    });
    const user = userEvent.setup();
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate();
    fillName();
    await waitForPermissions();
    await openAdvanced(user);
    await setCustomId(user, /Run-as user/, "Run-as user value", "0");

    continueToReview();
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    const alert = await screen.findByTestId("create-error");
    expect(alert.textContent).toMatch(/Not created: security settings refused/);
    expect(alert.textContent).toMatch(/root values/);
    expect(navigate).not.toHaveBeenCalled();
  });

  it("uses the same refusal title for a forbidden security write", async () => {
    routeFetch({
      create: problemRes(403, "server.security.forbidden", {}, "changing security settings needs the servers:security permission"),
    });
    renderWithQuery(<CreateServerWizard />);
    await pickTemplate();
    fillName();
    continueToReview();
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    const alert = await screen.findByTestId("create-error");
    expect(alert.textContent).toMatch(/Not created: security settings refused/);
    expect(navigate).not.toHaveBeenCalled();
  });
});
