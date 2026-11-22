import { useTestLocation, navigateTestSearch, resetTestSearch } from "@/test/routerSearch";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeBackup, makeSchedule, makeServer, makeRestore } from "@/test/factories";

vi.mock("@tanstack/react-router", () => ({
  useLocation: () => useTestLocation(),
  useNavigate: () => navigateTestSearch,
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
}));

import { BackupsPage } from "./Backups";

// HeroUI Select renders as a button. This finds the server select button,
// either in an open dialog (for backups) or on the page (for schedules).
function serverSelect(): HTMLElement {
  try {
    const dialog = screen.getByRole("dialog");
    const serverButton = within(dialog).getByRole("button", { name: /Server/i });
    return serverButton;
  } catch {
    // Not in a dialog; find the button on the page with "Select a server" text
    const allButtons = screen.getAllByRole("button");
    const match = allButtons.find((b) => within(b).queryByText(/Select a server/i));
    if (!match) throw new Error("server select not found");
    return match;
  }
}

describe("BackupsPage flows", () => {
  beforeEach(() => resetTestSearch());
  it("disables 'Run snapshot' and explains when no destination is configured", async () => {
    server.use(http.get("/backup-destinations", () => HttpResponse.json({ items: [] })));
    renderWithQuery(<BackupsPage />);
    // "Back up now" lives in the page header and opens the snapshot dialog.
    await userEvent.click(screen.getByRole("button", { name: /Back up now/i }));
    await userEvent.click(serverSelect());
    await userEvent.click(await screen.findByRole("option", { name: /^alpha · local \/ gameplane-games$/ }));
    expect(await screen.findByText(/No backup destinations configured/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Run snapshot/i })).toBeDisabled();
  });

  it("shows a destination lookup error without claiming the server has no destinations", async () => {
    server.use(http.get("/backup-destinations", () => HttpResponse.text("Destination lookup failed", { status: 403 })));
    renderWithQuery(<BackupsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Back up now/i }));
    await userEvent.click(serverSelect());
    await userEvent.click(await screen.findByRole("option", { name: /^alpha · local \/ gameplane-games$/ }));
    const dialog = within(screen.getByRole("dialog"));
    expect(await dialog.findByText("Destination lookup failed")).toBeInTheDocument();
    expect(dialog.queryByText(/No backup destinations configured/i)).not.toBeInTheDocument();
    expect(dialog.queryByRole("link", { name: "Manage destinations" })).not.toBeInTheDocument();
    expect(dialog.getByRole("button", { name: /Run snapshot/i })).toBeDisabled();
  });

  it("runs a snapshot for the selected server", async () => {
    let posted: unknown = null;
    server.use(
      http.get("/servers", () => HttpResponse.json({ items: [makeServer({ metadata: { name: "alpha" } })] })),
      http.post("/backups", async ({ request }) => {
        posted = await request.json();
        return HttpResponse.json(makeBackup({ metadata: { name: "alpha-new" } }));
      }),
    );
    renderWithQuery(<BackupsPage />);
    await screen.findByText("alpha-2026-05-07"); // default backup row
    await userEvent.click(screen.getByRole("button", { name: /Back up now/i }));
    await userEvent.click(serverSelect());
    const option = await screen.findByRole("option", { name: /^alpha · local \/ gameplane-games$/ });
    await userEvent.click(option);
    const run = screen.getByRole("button", { name: /Run snapshot/i });
    await waitFor(() => expect(run).toBeEnabled());
    await userEvent.click(run);
    await waitFor(() => expect(posted).not.toBeNull());
    // Success closes the dialog again.
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("filters the backup list by search text", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({ metadata: { name: "keep-me" }, spec: { serverRef: { name: "alpha" } } }),
            makeBackup({ metadata: { name: "hide-me" }, spec: { serverRef: { name: "beta" } } }),
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    await screen.findByText("keep-me");
    await userEvent.type(screen.getByPlaceholderText(/Search by name or server/i), "keep");
    await waitFor(() => expect(screen.queryByText("hide-me")).toBeNull());
    expect(screen.getByText("keep-me")).toBeInTheDocument();
  });

  it("shows the empty restores state on the Restores tab", async () => {
    server.use(http.get("/restores", () => HttpResponse.json({ items: [] })));
    renderWithQuery(<BackupsPage />);
    await userEvent.click(screen.getByRole("tab", { name: /^Restores$/i }));
    expect(await screen.findByText(/No restores have been run/i)).toBeInTheDocument();
  });

  it("lists restores with their target and phase", async () => {
    server.use(
      http.get("/restores", () =>
        HttpResponse.json({
          items: [makeRestore({ metadata: { name: "restore-1" }, spec: { backupRef: { name: "b1" }, serverRef: { name: "alpha" } } })],
        }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    await userEvent.click(screen.getByRole("tab", { name: /^Restores$/i }));
    expect(await screen.findByText("restore-1")).toBeInTheDocument();
  });

  it("opens the schedule form for a chosen server", async () => {
    server.use(
      http.get("/servers", () => HttpResponse.json({ items: [makeServer({ metadata: { name: "alpha" } })] })),
    );
    renderWithQuery(<BackupsPage />);
    await userEvent.click(screen.getByRole("tab", { name: /^Schedules$/i }));
    const select = serverSelect();
    await userEvent.click(select);
    const alphaOption = await screen.findByRole("option", { name: /^alpha · local \/ gameplane-games$/ });
    await userEvent.click(alphaOption);
    expect(await screen.findByText(/Schedule \(cron\)/i)).toBeInTheDocument();
  });

  it("prompts before deleting a schedule and toggles suspend", async () => {
    let patched: unknown = null;
    server.use(
      http.get("/schedules", () =>
        HttpResponse.json({ items: [makeSchedule({ metadata: { name: "alpha-daily" } })] }),
      ),
      http.get("/schedules/:name", ({ params }) =>
        HttpResponse.json(makeSchedule({ metadata: { name: String(params.name) } })),
      ),
      http.put("/schedules/:name", async ({ request }) => {
        patched = await request.json();
        return HttpResponse.json(makeSchedule({ metadata: { name: "alpha-daily" } }));
      }),
    );
    renderWithQuery(<BackupsPage />);
    await userEvent.click(screen.getByRole("tab", { name: /^Schedules$/i }));
    await screen.findByText("alpha-daily");

    // Toggle the "active" switch → patchSpec (read-modify-write PUT).
    const switchControl = screen.getByRole("switch", { name: /Schedule active/i });
    await userEvent.click(switchControl);
    await waitFor(() => expect(patched).not.toBeNull());

    // Delete prompts a confirm dialog.
    await userEvent.click(screen.getByRole("button", { name: /^Delete$/i }));
    expect(await screen.findByText(/Delete schedule\?/i)).toBeInTheDocument();
  });
});
