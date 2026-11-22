import { describe, it, expect, vi, beforeEach } from "vitest";
import type { ReactNode } from "react";
import { http, HttpResponse } from "msw";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { makeServer, makeTemplate, makeUser } from "@/test/factories";

// Router APIs the route reaches into. The search params stay static, so the
// assertion is on the navigation that switching to the Files tab performs.
const navigate = vi.fn();
let searchParams = {};
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, to, ...rest }: { children: ReactNode; to: string } & Record<string, unknown>) => (
    <a href={to} {...rest}>{children}</a>
  ),
  useParams: () => ({ name: "alpha" }),
  useSearch: () => searchParams,
  useNavigate: () => navigate,
}));

vi.mock("./tabs/Console", () => ({ ConsoleTab: () => <div>console-tab</div> }));
vi.mock("./tabs/Files", () => ({ FilesTab: () => <div>files-tab</div> }));
vi.mock("./tabs/Logs", () => ({ LogsTab: () => <div>logs-tab</div> }));
vi.mock("./tabs/Mods", () => ({ ModsTab: () => <div>mods-tab</div> }));
vi.mock("./tabs/Players", () => ({ PlayersTab: () => <div>players-tab</div> }));
vi.mock("./tabs/Backups", () => ({ BackupsTab: () => <div>backups-tab</div> }));
vi.mock("./tabs/Overview", () => ({ OverviewTab: () => <div>overview-tab</div> }));
vi.mock("./tabs/Settings", () => ({ SettingsTab: () => <div>settings-tab</div> }));

import { ServerDetailPage } from "./ServerDetail";

beforeEach(() => {
  searchParams = {};
  navigate.mockClear();
  server.use(
    http.get("/servers/alpha/access", ({ request, params }) => {
      const url = new URL(request.url);
      return HttpResponse.json({
        target: { cluster: url.searchParams.get("cluster") ?? "local", namespace: url.searchParams.get("namespace") ?? "gameplane-games", name: params.name },
        canWrite: true, canControl: true, canConsole: true, canDelete: true, isOwner: false, isCollaborator: false,
        permissions: ["servers:read", "servers:write", "servers:console", "captures:manage"],
      });
    }),
    http.get("/users/me", () => HttpResponse.json(makeUser({ role: "operator" }))),
  );
});

describe("ServerDetailPage modpack Open Files", () => {
  it("switches to the Files tab from the missing-files notice", async () => {
    server.use(
      http.get("/servers/alpha", () => HttpResponse.json(makeServer())),
      http.get("/templates/:name", ({ params }) =>
        HttpResponse.json(
          makeTemplate({
            metadata: { name: String(params.name) },
            spec: {
              versions: [{ id: "forge", displayName: "Forge", loader: "forge", default: true }],
              capabilities: {
                mods: {
                  loaders: { forge: { path: "modpacks" } },
                  registry: { providers: [{ provider: "curseforge", modpacks: { refEnv: "CURSEFORGE_MODPACK" } }] },
                },
              },
            },
          }),
        ),
      ),
      http.get("/mods/registry/providers", () =>
        HttpResponse.json([{ provider: "curseforge", available: true, modpacks: true }]),
      ),
      http.get("/servers/alpha/modpack", () =>
        HttpResponse.json({
          active: null,
          phase: "",
          message: "",
          backupName: "",
          keyShared: false,
          missingFiles: [
            { mod: "Example Mod", versionName: "1.0", filename: "example-1.0.jar", downloadPage: "https://www.curseforge.com/minecraft/mc-mods/example" },
          ],
          missingFilesUnknown: false,
          downloadsPath: "/data/downloads",
          backupAvailable: false,
        }),
      ),
    );
    renderWithQuery(<ServerDetailPage />);
    const modpacksTab = await screen.findByRole("tab", { name: "Modpacks" });
    await userEvent.click(modpacksTab);

    await userEvent.click(await screen.findByRole("button", { name: "Open Files" }));

    await waitFor(() => expect(screen.getByText("files-tab")).toBeInTheDocument());
    expect(navigate).toHaveBeenCalledWith(expect.objectContaining({ search: expect.objectContaining({ file: "/" }) }));
  });
});
