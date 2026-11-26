import { ResourceTargetProvider } from "@/lib/resourceTarget";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { ModpacksTab } from "./Modpacks";
import type { GameTemplate, ModpackView, RegistryProject, RegistryVersion } from "@/types";

// All loaders on the Modpacks tab: the toggle reaches the search and picker
// requests, a pack version that needs another server version shows the
// warning, and the install body carries serverVersion only then.

const fetchMock = vi.fn();
beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

function jsonRes(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const operator = {
  id: 1, username: "u", displayName: "U", email: "", role: "operator",
  permissions: { "*": ["servers:read", "servers:write"] },
};

const pack: RegistryProject = {
  id: "ftb-135", slug: "135", title: "FTB Evolution 2", author: "FTB Team", downloads: 1200000, provider: "ftb" as "modrinth",
};

// Runs on a NeoForge entry, not on the server's own Fabric entry.
const neoforgeVersion: RegistryVersion = {
  id: "1.11.0",
  name: "FTB Evolution 2 · 1.11.0",
  versionNumber: "1.11.0",
  gameVersions: ["26.1.2"],
  loaders: ["neoforge"],
  releaseType: "release",
  serverVersion: "neoforge-26",
  serverVersionName: "26.1.2 · NeoForge",
  files: [],
};

const baseView: ModpackView = {
  active: null,
  phase: "",
  message: "",
  backupName: "",
  keyShared: true,
  missingFiles: [],
  missingFilesUnknown: false,
  downloadsPath: "/data/mods-downloads",
  backupAvailable: true,
};

const posts: unknown[] = [];
const allLoadersSeen: (string | null)[] = [];

function route() {
  fetchMock.mockImplementation((url: string, init?: { method?: string; body?: string }) => {
    const method = init?.method ?? "GET";
    if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(operator));
    if (url.includes("/mods/registry/providers")) {
      return Promise.resolve(jsonRes([{ provider: "ftb", available: true, modpacks: true }]));
    }
    if (url.includes("/mods/registry/search")) return Promise.resolve(jsonRes([pack]));
    if (url.includes("/versions?")) {
      allLoadersSeen.push(new URL(url, "http://x").searchParams.get("allLoaders"));
      return Promise.resolve(jsonRes([neoforgeVersion]));
    }
    if (url.endsWith("/servers/s1")) return Promise.resolve(jsonRes({ metadata: { name: "s1" }, spec: {} }));
    if (url.includes("/servers/s1/modpack") && method === "POST") {
      posts.push(JSON.parse(init?.body ?? "{}"));
      return Promise.resolve(jsonRes({ ...baseView, phase: "BackingUp" }, 202));
    }
    if (url.includes("/servers/s1/modpack")) return Promise.resolve(jsonRes(baseView));
    return Promise.resolve(jsonRes({}));
  });
}

// The server runs its template default, a Fabric entry.
function fabricTmpl(): GameTemplate {
  return {
    metadata: { name: "t" },
    spec: {
      displayName: "T", game: "g", version: "1", image: "img",
      versions: [
        { id: "latest-fabric", displayName: "Latest · Fabric", loader: "fabric", gameVersion: "1.21.1", default: true },
      ],
      capabilities: {
        mods: {
          loaders: { fabric: { path: "mods" } },
          registry: { providers: [{ provider: "ftb" as "modrinth", modpacks: { refEnv: "FTB_MODPACK" } }] },
        },
      },
    },
  };
}

function renderTab() {
  return baseRenderWithQuery(
    <ResourceTargetProvider
      target={{ cluster: "local", name: "s1", namespace: undefined }}
      access={{
        canWrite: true, canControl: true, canConsole: true, canDelete: false,
        isOwner: false, isCollaborator: false,
        permissions: ["servers:read", "servers:write"],
      }}
    >
      <ModpacksTab name="s1" tmpl={fabricTmpl()} />
    </ResourceTargetProvider>,
  );
}

describe("Modpacks All loaders", () => {
  it("installs a pack version that needs another server version, after the warning", async () => {
    posts.length = 0;
    allLoadersSeen.length = 0;
    route();
    renderTab();

    fireEvent.click(await screen.findByRole("switch", { name: "All loaders" }));
    fireEvent.click(await screen.findByRole("button", { name: "Install…" }));

    expect(await screen.findByText("Showing every version this server can run, newest first.")).toBeInTheDocument();
    expect(await screen.findByText("Runs on 26.1.2 · NeoForge")).toBeInTheDocument();
    expect(allLoadersSeen).toContain("true");

    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByText("The server version changes")).toBeInTheDocument();
    expect(screen.getByText("The server version changes from Latest · Fabric to 26.1.2 · NeoForge.")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Install and restart" }));
    await waitFor(() =>
      expect(posts).toEqual([
        {
          provider: "ftb",
          project: "135",
          version: "1.11.0",
          title: "FTB Evolution 2",
          versionName: "FTB Evolution 2 · 1.11.0",
          backupFirst: true,
          serverVersion: "neoforge-26",
        },
      ]),
    );
  });
});
