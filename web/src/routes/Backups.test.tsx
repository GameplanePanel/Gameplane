import { useTestLocation, navigateTestSearch, resetTestSearch } from "@/test/routerSearch";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeBackup, makeSchedule, makeRestore } from "@/test/factories";

vi.mock("@tanstack/react-router", () => ({
  useLocation: () => useTestLocation(),
  useNavigate: () => navigateTestSearch,
  Link: ({ children, to, params, search }: { children: ReactNode; to: string; params?: { clusterId: string }; search?: Record<string, string> }) => (
    <a href={params && search ? to.replace("$clusterId", params.clusterId) + "?" + new URLSearchParams(search) : to}>{children}</a>
  ),
}));

import { BackupsPage } from "./Backups";

describe("BackupsPage", () => {
  // Each test starts with the default Backups route search.
  beforeEach(() => {
    window.history.replaceState(null, "", "/");
    resetTestSearch();
  });

  it("renders the Backups tab and loads rows", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({ metadata: { name: "alpha-2026-05-07" } }),
            makeBackup({ metadata: { name: "alpha-2026-05-06" } }),
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    await screen.findByText("alpha-2026-05-07");
    expect(screen.getByText("alpha-2026-05-06")).toBeInTheDocument();
  });

  it("switches to the Schedules tab and shows rows", async () => {
    server.use(
      http.get("/schedules", () =>
        HttpResponse.json({
          items: [makeSchedule({ metadata: { name: "alpha-daily" } })],
        }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    const schedTab = screen.getByRole("tab", { name: /Schedules/i });
    await userEvent.click(schedTab);
    await waitFor(() =>
      expect(screen.getByText("alpha-daily")).toBeInTheDocument(),
    );
  });

  it("runs a snapshot from the 'Back up now' header dialog", async () => {
    type BackupBody = {
      kind?: string;
      spec?: { serverRef?: { name?: string }; repoRef?: { name?: string; key?: string } };
    };
    // No initializer: TS pins a `= null` variable to `null` inside the
    // closure-assigned pattern, so declare it open and assert truthiness.
    let posted: BackupBody | undefined;
    server.use(
      http.post("/backups", async ({ request }) => {
        posted = (await request.json()) as BackupBody;
        return HttpResponse.json(
          makeBackup({ metadata: { name: "alpha-manual-1" } }),
        );
      }),
    );
    renderWithQuery(<BackupsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Back up now/i }));

    const dialog = await screen.findByRole("dialog");
    const serverButton = within(dialog).getByRole("button", { name: /Server/i });
    await userEvent.click(serverButton);
    const option = await screen.findByRole("option", { name: /^alpha · local \/ gameplane-games$/ });
    await userEvent.click(option);

    // Enabled only once the destination auto-selects from the query.
    const run = within(dialog).getByRole("button", { name: /Run snapshot/i });
    await waitFor(() => expect(run).toBeEnabled());
    await userEvent.click(run);

    await waitFor(() => expect(posted).toBeTruthy());
    expect(posted?.kind).toBe("Backup");
    expect(posted?.spec?.serverRef?.name).toBe("alpha");
    expect(posted?.spec?.repoRef).toEqual({ name: "default", key: "repo" });
    // The dialog closes once the snapshot is accepted.
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("switches to the Restores tab", async () => {
    renderWithQuery(<BackupsPage />);
    const tab = screen.getByRole("tab", { name: /Restores/i });
    await userEvent.click(tab);
    // Empty restore list — page should still render the panel header
    // without crashing.
    await waitFor(() => expect(tab).toHaveAttribute("aria-selected", "true"));
  });

  it("filters backups by server name", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({ metadata: { name: "alpha-1" }, spec: { serverRef: { name: "alpha" } } }),
            makeBackup({ metadata: { name: "beta-1" }, spec: { serverRef: { name: "beta" } } }),
          ],
        }),
      ),
      http.get("/schedules", () =>
        HttpResponse.json({ items: [] }),
      ),
      http.get("/restores", () =>
        HttpResponse.json({ items: [] }),
      ),
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            { metadata: { name: "alpha" } },
            { metadata: { name: "beta" } },
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    await screen.findByText("alpha-1");
    // BackupFilters' server select defaults to "All servers" (a filter with
    // no selection means "show everything"), unlike the "Back up now" dialog's
    // forced-choice "Select a server…".
    await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
    const serverSelect = screen.getByRole("button", { name: /Filter by server/i });
    await userEvent.click(serverSelect);
    const betaOption = await screen.findByRole("option", { name: /^beta · local \/ gameplane-games$/ });
    await userEvent.click(betaOption);
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    // Only beta-1 should be visible
    expect(screen.getByText("beta-1")).toBeInTheDocument();
    expect(screen.queryByText("alpha-1")).not.toBeInTheDocument();
  });

  it("filters backups by phase", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({ metadata: { name: "backup-1" }, status: { phase: "Succeeded" } }),
            makeBackup({ metadata: { name: "backup-2" }, status: { phase: "Failed" } }),
          ],
        }),
      ),
      http.get("/schedules", () =>
        HttpResponse.json({ items: [] }),
      ),
      http.get("/restores", () =>
        HttpResponse.json({ items: [] }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    await screen.findByText("backup-1");
    // Phase select defaults to "All phases" (same "no selection = show
    // everything" convention as the server filter).
    await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
    const phaseSelect = screen.getByRole("button", { name: /Filter by phase/i });
    await userEvent.click(phaseSelect);
    const succeededOption = await screen.findByRole("option", { name: "Succeeded" });
    await userEvent.click(succeededOption);
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(screen.getByText("backup-1")).toBeInTheDocument();
    expect(screen.queryByText("backup-2")).not.toBeInTheDocument();
  });

  it("searches backups by name", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({ metadata: { name: "alpha-search-test" }, spec: { serverRef: { name: "alpha" } } }),
            makeBackup({ metadata: { name: "beta-other" }, spec: { serverRef: { name: "beta" } } }),
          ],
        }),
      ),
      http.get("/schedules", () =>
        HttpResponse.json({ items: [] }),
      ),
      http.get("/restores", () =>
        HttpResponse.json({ items: [] }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    await screen.findByText("alpha-search-test");
    // Search for "search"
    const searchInput = screen.getByPlaceholderText(/search/i) as HTMLInputElement;
    await userEvent.type(searchInput, "search");
    expect(screen.getByText("alpha-search-test")).toBeInTheDocument();
    expect(screen.queryByText("beta-other")).not.toBeInTheDocument();
  });

  it("shows empty state when no backups match filters", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({
          items: [
            makeBackup({ metadata: { name: "alpha-1" }, spec: { serverRef: { name: "alpha" } } }),
          ],
        }),
      ),
      http.get("/schedules", () =>
        HttpResponse.json({ items: [] }),
      ),
      http.get("/restores", () =>
        HttpResponse.json({ items: [] }),
      ),
      http.get("/servers", () =>
        HttpResponse.json({
          items: [{ metadata: { name: "beta" } }],
        }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    await screen.findByText("alpha-1");
    // Filter to beta server (which has no backups)
    await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
    const serverSelect = screen.getByRole("button", { name: /Filter by server/i });
    await userEvent.click(serverSelect);
    const betaOption = await screen.findByRole("option", { name: /^beta · local \/ gameplane-games$/ });
    await userEvent.click(betaOption);
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(screen.getByText(/No backups match the current filters/)).toBeInTheDocument();
  });

  it("shows error when BackupNow dialog fails to create backup", async () => {
    server.use(
      http.post("/backups", () =>
        HttpResponse.text("backup failed", { status: 500 }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Back up now/i }));
    const dialog = await screen.findByRole("dialog");
    const serverButton = within(dialog).getByRole("button", { name: /Server/i });
    await userEvent.click(serverButton);
    const option = await screen.findByRole("option", { name: /^alpha · local \/ gameplane-games$/ });
    await userEvent.click(option);
    const run = within(dialog).getByRole("button", { name: /Run snapshot/i });
    await waitFor(() => expect(run).toBeEnabled());
    await userEvent.click(run);
    await waitFor(() => expect(screen.getByText(/backup failed/)).toBeInTheDocument());
  });

  it("disables BackupNow button when no destination configured", async () => {
    server.use(
      http.get("/backup-destinations", () =>
        HttpResponse.json({ items: [] }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    await userEvent.click(screen.getByRole("button", { name: /Back up now/i }));
    const dialog = await screen.findByRole("dialog");
    const run = within(dialog).getByRole("button", { name: /Run snapshot/i });
    await waitFor(() => expect(run).toBeDisabled());
    expect(within(dialog).queryByText(/No backup destinations configured/)).not.toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: /Server/i }));
    await userEvent.click(await screen.findByRole("option", { name: /^alpha · local \/ gameplane-games$/ }));
    expect(await within(dialog).findByText(/No backup destinations configured/)).toBeInTheDocument();
    const link = await within(dialog).findByRole("link", { name: "Manage destinations" });
    expect(link).toHaveAttribute("href", "/clusters/local?tab=configuration&ns=gameplane-games");
  });

  it("toggles schedule suspend status", async () => {
    const toggleHandler = vi.fn(() => {
      // patchSpec does GET then PUT; return a valid schedule with suspend toggled
      return HttpResponse.json(
        makeSchedule({
          metadata: { name: "alpha-daily" },
          spec: { serverRef: { name: "alpha" }, schedule: "0 3 * * *", suspend: true },
        }),
      );
    });
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({ items: [] }),
      ),
      http.get("/schedules", () =>
        HttpResponse.json({
          items: [
            makeSchedule({
              metadata: { name: "alpha-daily" },
              spec: { serverRef: { name: "alpha" }, schedule: "0 3 * * *", suspend: false },
            }),
          ],
        }),
      ),
      http.get("/restores", () =>
        HttpResponse.json({ items: [] }),
      ),
      // patchSpec's GET-before-PUT read step targets the single-schedule
      // route, not the list route above — without this, the request falls
      // through to the base handler and the mutation never reaches
      // toggleHandler in time.
      http.get("/schedules/alpha-daily", () =>
        HttpResponse.json(
          makeSchedule({
            metadata: { name: "alpha-daily" },
            spec: { serverRef: { name: "alpha" }, schedule: "0 3 * * *", suspend: false },
          }),
        ),
      ),
      http.put("/schedules/alpha-daily", toggleHandler),
    );
    renderWithQuery(<BackupsPage />);
    const schedTab = screen.getByRole("tab", { name: /Schedules/i });
    await userEvent.click(schedTab);
    await screen.findByText("alpha-daily");
    // HeroUI Switch renders as a switch element
    const switchControl = screen.getByRole("switch", { name: /Schedule active/i });
    await userEvent.click(switchControl);
    await waitFor(() => expect(toggleHandler).toHaveBeenCalled());
  });

  it("shows delete confirmation dialog for schedule", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({ items: [] }),
      ),
      http.get("/schedules", () =>
        HttpResponse.json({
          items: [makeSchedule({ metadata: { name: "alpha-daily" } })],
        }),
      ),
      http.get("/restores", () =>
        HttpResponse.json({ items: [] }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    const schedTab = screen.getByRole("tab", { name: /Schedules/i });
    await userEvent.click(schedTab);
    await screen.findByText("alpha-daily");
    const deleteBtn = screen.getByRole("button", { name: /Delete/i });
    await userEvent.click(deleteBtn);
    expect(await screen.findByText(/Delete schedule/)).toBeInTheDocument();
  });

  it("filters restores by server and phase", async () => {
    server.use(
      http.get("/backups", () =>
        HttpResponse.json({ items: [] }),
      ),
      http.get("/schedules", () =>
        HttpResponse.json({ items: [] }),
      ),
      http.get("/restores", () =>
        HttpResponse.json({
          items: [
            makeRestore({
              metadata: { name: "restore-1" },
              spec: { serverRef: { name: "alpha" }, backupRef: { name: "backup-1" } },
              status: { phase: "Succeeded" },
            }),
            makeRestore({
              metadata: { name: "restore-2" },
              spec: { serverRef: { name: "beta" }, backupRef: { name: "backup-2" } },
              status: { phase: "Failed" },
            }),
          ],
        }),
      ),
      http.get("/servers", () =>
        HttpResponse.json({
          items: [
            { metadata: { name: "alpha" } },
            { metadata: { name: "beta" } },
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    const restoresTab = screen.getByRole("tab", { name: /Restores/i });
    await userEvent.click(restoresTab);
    await screen.findByText("restore-1");
    // Filter by alpha server (RestoresTabPanel reuses BackupFilters, so
    // same "All servers" default as the Backups tab's server filter).
    await userEvent.click(screen.getByRole("button", { name: /^Filter$/ }));
    const serverSelect = screen.getByRole("button", { name: /Filter by server/i });
    await userEvent.click(serverSelect);
    const alphaOption = await screen.findByRole("option", { name: /^alpha · local \/ gameplane-games$/ });
    await userEvent.click(alphaOption);
    await userEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(screen.getByText("restore-1")).toBeInTheDocument();
    expect(screen.queryByText("restore-2")).not.toBeInTheDocument();
  });

  it("shows empty restores state", async () => {
    server.use(
      http.get("/restores", () =>
        HttpResponse.json({ items: [] }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    const restoresTab = screen.getByRole("tab", { name: /Restores/i });
    await userEvent.click(restoresTab);
    expect(await screen.findByText(/No restores have been run/)).toBeInTheDocument();
  });

  it("searches restores by name", async () => {
    server.use(
      http.get("/restores", () =>
        HttpResponse.json({
          items: [
            makeRestore({
              metadata: { name: "search-restore-1" },
              spec: { serverRef: { name: "alpha" }, backupRef: { name: "backup-1" } },
            }),
            makeRestore({
              metadata: { name: "other-restore" },
              spec: { serverRef: { name: "beta" }, backupRef: { name: "backup-2" } },
            }),
          ],
        }),
      ),
    );
    renderWithQuery(<BackupsPage />);
    const restoresTab = screen.getByRole("tab", { name: /Restores/i });
    await userEvent.click(restoresTab);
    await screen.findByText("search-restore-1");
    const searchInput = screen.getByPlaceholderText(/search/i) as HTMLInputElement;
    await userEvent.type(searchInput, "search");
    expect(screen.getByText("search-restore-1")).toBeInTheDocument();
    expect(screen.queryByText("other-restore")).not.toBeInTheDocument();
  });

  it("deletes a schedule", async () => {
    const deleteHandler = vi.fn(() =>
      HttpResponse.json({})
    );
    server.use(
      http.get("/schedules", () =>
        HttpResponse.json({
          items: [makeSchedule({ metadata: { name: "alpha-daily" } })],
        }),
      ),
      http.delete("/schedules/alpha-daily", deleteHandler),
    );
    renderWithQuery(<BackupsPage />);
    const schedTab = screen.getByRole("tab", { name: /Schedules/i });
    await userEvent.click(schedTab);
    await screen.findByText("alpha-daily");
    const deleteBtn = screen.getByRole("button", { name: /Delete/i });
    await userEvent.click(deleteBtn);
    await screen.findByText(/Delete schedule/);
    const typeInput = screen.getByRole("textbox") as HTMLInputElement;
    await userEvent.type(typeInput, "alpha-daily");
    const confirmBtn = screen.getByRole("button", { name: /Delete$/ });
    await userEvent.click(confirmBtn);
    await waitFor(() => expect(deleteHandler).toHaveBeenCalled());
  });

  it("lists each cluster's backup destinations on the Destinations tab", async () => {
    const requested: string[] = [];
    server.use(
      http.get("/backup-destinations", ({ request }) => {
        requested.push(new URL(request.url).searchParams.get("namespace") ?? "");
        return HttpResponse.json({ items: [{ name: "nightly-backups", url: "s3:s3.example.com/gameplane", hasPassword: true }] });
      }),
    );
    renderWithQuery(<BackupsPage />);
    const tab = screen.getByRole("tab", { name: /Destinations/i });
    await userEvent.click(tab);
    await waitFor(() => expect(tab).toHaveAttribute("aria-selected", "true"));
    expect(await screen.findByRole("region", { name: "local" })).toBeInTheDocument();
    expect(await screen.findByText("nightly-backups")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Add destination/i })).toBeInTheDocument();
    expect(requested).toContain("gameplane-games");
  });

  it("shows only the cluster picked in the cluster filter on the Destinations tab", async () => {
    server.use(http.get("/backup-destinations", () => HttpResponse.json({ items: [] })));
    await navigateTestSearch({ search: () => ({ tab: "destinations", cluster: "east" }) });
    renderWithQuery(<BackupsPage />);
    expect(await screen.findByRole("region", { name: "east" })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "local" })).not.toBeInTheDocument();
    expect(await screen.findByText(/No backup destinations configured/i)).toBeInTheDocument();
  });

  it("says no clusters are available when the fleet lists none", async () => {
    server.use(http.get("/fleet/:kind", () => HttpResponse.json({ items: [], partial: false, issues: [], totalReturned: 0, scopes: [] })));
    await navigateTestSearch({ search: () => ({ tab: "destinations" }) });
    renderWithQuery(<BackupsPage />);
    expect(await screen.findByText("No clusters available.")).toBeInTheDocument();
  });
});
