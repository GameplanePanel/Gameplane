import { describe, it, expect, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { TELEMETRY_STATEMENT_URL } from "@/lib/links";
import { TelemetryNotice } from "./TelemetryNotice";

type Dest = { kind: string; host: string | null };

// Serves a pending notice until any non-"seen" ack lands, recording bodies.
function setup(dest: Dest | null = { kind: "default", host: "telemetry.example.org" }, ackStatus = 204) {
  const bodies: { action: string }[] = [];
  let pending = true;
  server.use(
    http.get("/admin/telemetry/notice", () =>
      HttpResponse.json(pending ? { pending: true, destination: dest ?? undefined } : { pending: false }),
    ),
    http.post("/admin/telemetry/notice", async ({ request }) => {
      const body = (await request.json()) as { action: string };
      bodies.push(body);
      if (body.action !== "seen") {
        if (ackStatus !== 204) return new HttpResponse(null, { status: ackStatus });
        pending = false;
      }
      return new HttpResponse(null, { status: 204 });
    }),
  );
  return bodies;
}

describe("TelemetryNotice", () => {
  it("renders nothing while the notice is not pending (default handler)", async () => {
    const { container } = renderWithQuery(<TelemetryNotice onOpenSettings={() => {}} />);
    await waitFor(() => expect(container).toBeEmptyDOMElement());
    expect(screen.queryByText(/Anonymous usage metrics/)).not.toBeInTheDocument();
  });

  it("renders the pending notice and posts seen exactly once", async () => {
    const bodies = setup();
    const { client, rerender } = renderWithQuery(<TelemetryNotice onOpenSettings={() => {}} />);
    expect(await screen.findByText("Anonymous usage metrics are on for this install.")).toBeInTheDocument();
    expect(screen.getByText("telemetry.example.org")).toBeInTheDocument();
    expect(screen.getByText(/the Gameplane project's telemetry service/)).toBeInTheDocument();
    await waitFor(() => expect(bodies).toEqual([{ action: "seen" }]));
    rerender(
      <QueryClientProvider client={client}>
        <TelemetryNotice onOpenSettings={() => {}} />
      </QueryClientProvider>,
    );
    await new Promise((r) => setTimeout(r, 50));
    expect(bodies).toEqual([{ action: "seen" }]);
  });

  it.each([
    ["Keep sharing", "keep"],
    ["Turn off extended", "extended-off"],
    ["Turn off all", "all-off"],
  ])("%s posts %s and hides the banner", async (name, action) => {
    const bodies = setup();
    renderWithQuery(<TelemetryNotice onOpenSettings={() => {}} />);
    await userEvent.click(await screen.findByRole("button", { name }));
    await waitFor(() =>
      expect(screen.queryByText("Anonymous usage metrics are on for this install.")).not.toBeInTheDocument(),
    );
    expect(bodies).toContainEqual({ action });
  });

  it("invalidates the telemetry and admin-config queries after an action", async () => {
    setup();
    const { client } = renderWithQuery(<TelemetryNotice onOpenSettings={() => {}} />);
    const spy = vi.spyOn(client, "invalidateQueries");
    await userEvent.click(await screen.findByRole("button", { name: "Keep sharing" }));
    await waitFor(() => {
      expect(spy).toHaveBeenCalledWith({ queryKey: ["telemetry"] });
      expect(spy).toHaveBeenCalledWith({ queryKey: ["admin-config"] });
    });
  });

  it("shows an error and keeps the banner when the ack fails", async () => {
    setup(undefined, 409);
    renderWithQuery(<TelemetryNotice onOpenSettings={() => {}} />);
    await userEvent.click(await screen.findByRole("button", { name: "Turn off all" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not save your choice. Try again.");
    expect(screen.getByText("Anonymous usage metrics are on for this install.")).toBeInTheDocument();
  });

  it("names the bundled receiver", async () => {
    setup({ kind: "bundled", host: "gameplane-telemetry-receiver.gameplane.svc" });
    renderWithQuery(<TelemetryNotice onOpenSettings={() => {}} />);
    expect(await screen.findByText("gameplane-telemetry-receiver.gameplane.svc")).toBeInTheDocument();
    expect(screen.getByText(/Reports stay inside your cluster/)).toBeInTheDocument();
  });

  it("names a custom destination", async () => {
    setup({ kind: "custom", host: "metrics.corp.example" });
    renderWithQuery(<TelemetryNotice onOpenSettings={() => {}} />);
    expect(await screen.findByText("metrics.corp.example")).toBeInTheDocument();
    expect(screen.getByText(/destination your operator configured/)).toBeInTheDocument();
  });

  it("omits the destination line when the response has no host", async () => {
    setup({ kind: "default", host: null });
    renderWithQuery(<TelemetryNotice onOpenSettings={() => {}} />);
    await screen.findByText("Anonymous usage metrics are on for this install.");
    expect(screen.queryByText(/Reports are sent to/)).not.toBeInTheDocument();
  });

  it("links to Telemetry settings and the data-handling statement", async () => {
    setup();
    const onOpenSettings = vi.fn();
    renderWithQuery(<TelemetryNotice onOpenSettings={onOpenSettings} />);
    await userEvent.click(await screen.findByRole("button", { name: "Open Telemetry settings" }));
    expect(onOpenSettings).toHaveBeenCalledOnce();
    const link = screen.getByRole("link", { name: "Data-handling statement" });
    expect(link).toHaveAttribute("href", TELEMETRY_STATEMENT_URL);
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
  });

  it("tolerates a failed seen post", async () => {
    setup();
    server.use(http.post("/admin/telemetry/notice", () => new HttpResponse(null, { status: 500 })));
    renderWithQuery(<TelemetryNotice onOpenSettings={() => {}} />);
    expect(await screen.findByText("Anonymous usage metrics are on for this install.")).toBeInTheDocument();
  });

  it("renders the banner when destination is absent", async () => {
    setup(null);
    renderWithQuery(<TelemetryNotice onOpenSettings={() => {}} />);
    expect(await screen.findByText("Anonymous usage metrics are on for this install.")).toBeInTheDocument();
    expect(screen.queryByText(/Reports are sent to/)).not.toBeInTheDocument();
  });
});
