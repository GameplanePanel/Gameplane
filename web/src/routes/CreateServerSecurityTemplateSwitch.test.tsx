import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, fireEvent, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithQuery } from "@/test/render";
import { makeUser } from "@/test/factories";
import type { GameTemplate } from "@/types";

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

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function template(name: string, displayName: string): GameTemplate {
  return { metadata: { name }, spec: { displayName, game: name, version: "1", image: `example/${name}` } };
}

function routeFetch() {
  const templates = [template("minecraft-java", "Minecraft Java"), template("terraria", "Terraria")];
  fetchMock.mockImplementation((url: string, init?: FetchInit) => {
    if (url.includes("/users/me")) return Promise.resolve(json(200, makeUser()));
    if (url.includes("/cluster")) return Promise.resolve(json(200, { nodes: [] }));
    if ((init?.method ?? "GET") === "POST") return Promise.resolve(json(201, { metadata: { name: "mc-test" } }));
    return Promise.resolve(json(200, { items: [{ cluster: "local", namespace: "gameplane-games", templates }], partial: false, issues: [], totalReturned: 2 }));
  });
}

function createdBody(): { spec: Record<string, unknown> } {
  const postCall = fetchMock.mock.calls.find((c) => (c[1] as FetchInit | undefined)?.method === "POST")!;
  return JSON.parse((postCall[1] as FetchInit).body as string);
}

describe("CreateServerWizard — security overrides and template changes", () => {
  it("drops overrides chosen for another template when the owner picks a new one", async () => {
    routeFetch();
    const user = userEvent.setup();
    renderWithQuery(<CreateServerWizard />);

    fireEvent.click(await screen.findByRole("button", { name: /Minecraft Java/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Configure/i }));
    await waitFor(() => expect(screen.queryByText("Read-only")).not.toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: /^Advanced security/ }));
    await user.click(screen.getByRole("button", { name: /Run-as user/ }));
    await user.click(await screen.findByRole("option", { name: "Custom value" }));
    const input = screen.getByRole("spinbutton", { name: "Run-as user value" });
    fireEvent.change(input, { target: { value: "2000" } });
    fireEvent.blur(input);

    fireEvent.click(screen.getByRole("button", { name: /^Back$/ }));
    fireEvent.click(await screen.findByRole("button", { name: /Terraria/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Configure/i }));
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(createdBody().spec).not.toHaveProperty("security");
  });

  it("keeps overrides when the owner re-picks the same template", async () => {
    routeFetch();
    const user = userEvent.setup();
    renderWithQuery(<CreateServerWizard />);

    fireEvent.click(await screen.findByRole("button", { name: /Minecraft Java/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Configure/i }));
    await waitFor(() => expect(screen.queryByText("Read-only")).not.toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: /^Advanced security/ }));
    await user.click(screen.getByRole("button", { name: /Run-as user/ }));
    await user.click(await screen.findByRole("option", { name: "Custom value" }));
    const input = screen.getByRole("spinbutton", { name: "Run-as user value" });
    fireEvent.change(input, { target: { value: "2000" } });
    fireEvent.blur(input);

    fireEvent.click(screen.getByRole("button", { name: /^Back$/ }));
    fireEvent.click(await screen.findByRole("button", { name: /Minecraft Java/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Configure/i }));
    fireEvent.change(screen.getByPlaceholderText("mc-hardcore"), { target: { value: "mc-test" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue to Network/i }));
    fireEvent.click(screen.getByRole("button", { name: /Continue to Review/i }));
    fireEvent.click(screen.getByRole("button", { name: /Create server/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(createdBody().spec.security).toEqual({ runAsUser: { value: 2000 } });
  });
});
