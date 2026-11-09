import { describe, expect, it, vi } from "vitest";
import { act, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeClusterInfo, makeUser } from "@/test/factories";
import { NodeEnrollment } from "./NodeEnrollment";

describe("NodeEnrollment", () => {
  it("requires explicit cluster selection before creating a join command from All clusters", async () => {
    const join = vi.fn(() => HttpResponse.json({ command: "local-command", expiresAt: "later" }));
    server.use(http.post("/cluster/nodes:join", join));
    renderWithQuery(<NodeEnrollment />);
    await waitFor(() => expect(screen.getByRole("button", { name: "Add node" })).toBeEnabled());
    await userEvent.click(screen.getByRole("button", { name: "Add node" }));
    expect(join).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "Enrollment cluster" }));
    await userEvent.click(await screen.findByRole("option", { name: "local" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Generate join command" })).toBeEnabled());
    await userEvent.click(screen.getByRole("button", { name: "Generate join command" }));
    expect(await screen.findByText("local-command")).toBeInTheDocument();
    expect(join).toHaveBeenCalledTimes(1);
  });
  it.each([[501, /aren't enabled/], [403, /don't have permission/], [500, /operation failed/]] as const)("preserves enrollment error handling for %s", async (status, message) => {
    server.use(http.post("/cluster/nodes:join", () => new HttpResponse("failed", { status })));
    renderWithQuery(<NodeEnrollment clusterId="local" />);
    await waitFor(() => expect(screen.getByRole("button", { name: "Add node" })).toBeEnabled());
    await userEvent.click(screen.getByRole("button", { name: "Add node" }));
    expect(await screen.findByText(message)).toBeInTheDocument();
  });
  it("copies and dismisses the join command and clears an earlier failure on success", async () => {
    let calls = 0;
    const clipboard = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText: clipboard } });
    server.use(http.post("/cluster/nodes:join", () => ++calls === 1 ? new HttpResponse("failed", { status: 500 }) : HttpResponse.json({ command: "local-command", expiresAt: "later" })));
    renderWithQuery(<NodeEnrollment clusterId="local" />);
    const add = screen.getByRole("button", { name: "Add node" });
    await waitFor(() => expect(add).toBeEnabled());
    await userEvent.click(add);
    await screen.findByText(/operation failed/);
    await userEvent.click(add);
    await screen.findByText("local-command");
    expect(screen.queryByText(/operation failed/)).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(clipboard).toHaveBeenCalledWith("local-command");
    await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    expect(screen.queryByText("local-command")).not.toBeInTheDocument();
  });
  it("does not show a late local command after changing the explicit target", async () => {
    let release: () => void = () => {};
    const pending = new Promise<void>((resolve) => { release = resolve; });
    server.use(http.post("/cluster/nodes:join", async () => { await pending; return HttpResponse.json({ command: "secret-local-command", expiresAt: "later" }); }));
    const { rerender, client } = renderWithQuery(<NodeEnrollment clusterId="local" />);
    await waitFor(() => expect(screen.getByRole("button", { name: "Add node" })).toBeEnabled());
    await userEvent.click(screen.getByRole("button", { name: "Add node" }));
    rerender(<QueryClientProvider client={client}><NodeEnrollment clusterId="remote" /></QueryClientProvider>);
    await act(async () => { release(); await pending; });
    expect(screen.queryByText("secret-local-command")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add node" })).toBeDisabled();
    expect(screen.getByText(/managed at this remote cluster/)).toBeInTheDocument();
  });
  it("never requests local capabilities or joins on standalone installations", async () => {
    const info = vi.fn(() => HttpResponse.json(makeClusterInfo()));
    server.use(http.get("/admin/installation", () => HttpResponse.json({ standalone: true, localCluster: false })), http.get("/clusters", () => HttpResponse.json({ items: [] })), http.get("/cluster/info", info));
    renderWithQuery(<NodeEnrollment clusterId="local" />);
    await screen.findByText(/No local workload cluster/);
    expect(info).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Add node" })).toBeDisabled();
  });
  it("gates disabled capabilities and missing management permissions", async () => {
    server.use(http.get("/cluster/info", () => HttpResponse.json(makeClusterInfo({ clusterOps: false }))));
    const { unmount } = renderWithQuery(<NodeEnrollment clusterId="local" />);
    await screen.findByText(/clusterOps.enabled/);
    expect(screen.getByRole("button", { name: "Add node" })).toBeDisabled();
    unmount();
    server.use(http.get("/users/me", () => HttpResponse.json(makeUser({ role: "viewer" }))));
    renderWithQuery(<NodeEnrollment clusterId="local" />);
    await screen.findByText(/permission to enroll nodes/);
    expect(screen.getByRole("button", { name: "Add node" })).toBeDisabled();
  });
  it("retains the older API capability fallback", async () => {
    server.use(http.get("/cluster/info", () => HttpResponse.error()));
    renderWithQuery(<NodeEnrollment clusterId="local" />);
    await waitFor(() => expect(screen.getByRole("button", { name: "Add node" })).toBeEnabled());
  });
});
