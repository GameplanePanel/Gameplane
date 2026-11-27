import { useTestLocation, navigateTestSearch, resetTestSearch } from "@/test/routerSearch";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeBackup } from "@/test/factories";

vi.mock("@tanstack/react-router", () => ({
  useLocation: () => useTestLocation(),
  useNavigate: () => navigateTestSearch,
  Link: ({ children, to }: { children: ReactNode; to: string }) => <a href={to}>{children}</a>,
}));

import { BackupsPage } from "./Backups";

describe("BackupsPage download action", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/");
    resetTestSearch();
  });
  afterEach(() => vi.restoreAllMocks());

  it("downloads a finished restic backup from its row and marks snapshot backups", async () => {
    const clicked: string[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      clicked.push(this.getAttribute("href") ?? "");
    });
    server.use(http.get("/backups", () => HttpResponse.json({ items: [
      makeBackup({ metadata: { name: "restic-1" } }),
      makeBackup({ metadata: { name: "snap-1" }, spec: { serverRef: { name: "alpha" }, strategy: "volume-snapshot" } }),
    ] })));
    renderWithQuery(<BackupsPage />);
    await screen.findByText("snap-1");
    expect(screen.getByTitle("Snapshot backups stay inside the storage system.")).toBeInTheDocument();
    await userEvent.click(await screen.findByRole("button", { name: /^download$/i }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("Download backup");
    expect(dialog).toHaveTextContent("restic-1 · alpha");
    await userEvent.click(within(dialog).getByRole("button", { name: /^download$/i }));
    expect(clicked).toHaveLength(1);
    expect(clicked[0]).toContain("/backups/restic-1:download?format=tar.gz");
    await userEvent.click(within(dialog).getByRole("button", { name: /^cancel$/i }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });
});
