import { describe, expect, it, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { RegisterClusterDialog } from "./RegisterClusterDialog";

const NAMESPACE_LABEL = "Control-plane namespace (optional)";
const NAMESPACE_ERROR = /Use a lowercase DNS label/;

async function fillRequiredFields() {
  await userEvent.type(screen.getByLabelText("Name"), "east");
  await userEvent.type(screen.getByLabelText("Display name"), "East");
  await userEvent.type(screen.getByLabelText("Kubeconfig"), "apiVersion: v1");
}

describe("RegisterClusterDialog control-plane namespace", () => {
  it("pre-fills gameplane and omits it from the body because it is the API default", async () => {
    const bodies: Record<string, unknown>[] = [];
    server.use(http.post("/clusters", async ({ request }) => {
      bodies.push((await request.json()) as Record<string, unknown>);
      return HttpResponse.json({ name: "east" }, { status: 201 });
    }));
    renderWithQuery(<RegisterClusterDialog open onOpenChange={vi.fn()} />);
    const namespace = screen.getByLabelText(NAMESPACE_LABEL);
    expect(namespace).toHaveValue("gameplane");
    expect(namespace).toHaveAttribute("aria-invalid", "false");
    expect(screen.getByText(/Leave gameplane unless it was installed elsewhere/)).toBeInTheDocument();
    await fillRequiredFields();
    await userEvent.click(screen.getByRole("button", { name: "Register" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).toEqual({ name: "east", displayName: "East", kubeconfig: "apiVersion: v1" });
  });

  it("flags an invalid namespace, blocks registration and recovers once corrected", async () => {
    const creates = vi.fn(() => HttpResponse.json({ name: "east" }, { status: 201 }));
    server.use(http.post("/clusters", creates));
    renderWithQuery(<RegisterClusterDialog open onOpenChange={vi.fn()} />);
    await fillRequiredFields();
    const namespace = screen.getByLabelText(NAMESPACE_LABEL);
    await userEvent.clear(namespace);
    await userEvent.type(namespace, "Bad_Name");
    expect(await screen.findByText(NAMESPACE_ERROR)).toBeInTheDocument();
    expect(namespace).toHaveAttribute("aria-invalid", "true");
    expect(namespace).toHaveAttribute("aria-describedby", "cluster-control-plane-namespace-error");
    expect(screen.getByRole("button", { name: "Register" })).toBeDisabled();
    await userEvent.clear(namespace);
    await userEvent.type(namespace, "team-a");
    await waitFor(() => expect(screen.queryByText(NAMESPACE_ERROR)).not.toBeInTheDocument());
    expect(namespace).not.toHaveAttribute("aria-invalid", "true");
    expect(namespace).toHaveAttribute("aria-describedby", "cluster-control-plane-namespace-help");
    expect(screen.getByRole("button", { name: "Register" })).toBeEnabled();
    expect(creates).not.toHaveBeenCalled();
  });

  it("rejects a namespace longer than 63 characters", async () => {
    renderWithQuery(<RegisterClusterDialog open onOpenChange={vi.fn()} />);
    await fillRequiredFields();
    const namespace = screen.getByLabelText(NAMESPACE_LABEL);
    await userEvent.clear(namespace);
    await userEvent.type(namespace, "a".repeat(64));
    expect(await screen.findByText(NAMESPACE_ERROR)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Register" })).toBeDisabled();
  });

  it("sends a valid custom namespace as controlPlaneNamespace", async () => {
    const bodies: Record<string, unknown>[] = [];
    server.use(http.post("/clusters", async ({ request }) => {
      bodies.push((await request.json()) as Record<string, unknown>);
      return HttpResponse.json({ name: "east" }, { status: 201 });
    }));
    renderWithQuery(<RegisterClusterDialog open onOpenChange={vi.fn()} />);
    await fillRequiredFields();
    const namespace = screen.getByLabelText(NAMESPACE_LABEL);
    await userEvent.clear(namespace);
    await userEvent.type(namespace, "  team-a  ");
    await userEvent.click(screen.getByRole("button", { name: "Register" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0].controlPlaneNamespace).toBe("team-a");
  });

  it("omits controlPlaneNamespace when the field is blank", async () => {
    const bodies: Record<string, unknown>[] = [];
    server.use(http.post("/clusters", async ({ request }) => {
      bodies.push((await request.json()) as Record<string, unknown>);
      return HttpResponse.json({ name: "east" }, { status: 201 });
    }));
    renderWithQuery(<RegisterClusterDialog open onOpenChange={vi.fn()} />);
    await fillRequiredFields();
    const namespace = screen.getByLabelText(NAMESPACE_LABEL);
    await userEvent.clear(namespace);
    expect(screen.queryByText(NAMESPACE_ERROR)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Register" })).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: "Register" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]).not.toHaveProperty("controlPlaneNamespace");
  });
});
