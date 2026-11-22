import { ResourceTargetProvider } from "@/lib/resourceTarget";
import type { ReactElement } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithQuery as baseRenderWithQuery } from "@/test/render";
import { ModpacksTab } from "./Modpacks";
import type {
  GameTemplate,
  ModpackActive,
  ModpackView,
  RegistryProject,
  RegistryProviderInfo,
  RegistryVersion,
} from "@/types";

// Flow tests for the env-mode (CurseForge) install, change, downgrade and
// remove paths of the Modpacks tab. Requests are answered by fetchMock.

const fetchMock = vi.fn();
let scopedControl = true;
beforeEach(() => {
  scopedControl = true;
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
const viewer = { ...operator, role: "viewer", permissions: { "*": ["servers:read"] } };

const curseforge: RegistryProviderInfo = { provider: "curseforge", available: true, modpacks: true };

const pack: RegistryProject = {
  id: "1234", slug: "atm9", title: "All The Mods 9", author: "ATM Team", downloads: 1000, provider: "curseforge",
};

function version(id: string, name: string): RegistryVersion {
  return { id, name, versionNumber: name, gameVersions: ["1.21.1"], loaders: ["neoforge"], files: [] };
}

function baseView(over: Partial<ModpackView> = {}): ModpackView {
  return {
    active: null,
    phase: "",
    message: "",
    backupName: "",
    keyShared: true,
    missingFiles: [],
    missingFilesUnknown: false,
    downloadsPath: "/data/mods-downloads",
    backupAvailable: true,
    ...over,
  };
}

const installed: ModpackActive = {
  provider: "curseforge", project: "atm9", version: "2", title: "All The Mods 9", versionName: "2.0.0", pinned: true,
};

interface Opts {
  asViewer?: boolean;
  view?: ModpackView;
  versions?: RegistryVersion[];
  providers?: RegistryProviderInfo[];
  installStatus?: number;
  installError?: string;
  posts?: unknown[];
  deletes?: string[];
}

function route(o: Opts) {
  scopedControl = !o.asViewer;
  fetchMock.mockImplementation((url: string, init?: { method?: string; body?: string }) => {
    const method = init?.method ?? "GET";
    if (url.endsWith("/users/me")) return Promise.resolve(jsonRes(o.asViewer ? viewer : operator));
    if (url.includes("/mods/registry/providers")) return Promise.resolve(jsonRes(o.providers ?? [curseforge]));
    if (url.includes("/mods/registry/search")) return Promise.resolve(jsonRes([pack]));
    if (url.includes("/versions?")) return Promise.resolve(jsonRes(o.versions ?? []));
    if (url.endsWith("/servers/s1")) return Promise.resolve(jsonRes({ metadata: { name: "s1" }, spec: {} }));
    if (url.includes("/servers/s1/modpack") && method === "POST") {
      o.posts?.push(JSON.parse(init?.body ?? "{}"));
      if (o.installError) return Promise.resolve(jsonRes({ error: o.installError }, o.installStatus ?? 409));
      return Promise.resolve(jsonRes(baseView({ phase: "BackingUp" }), 202));
    }
    if (url.includes("/servers/s1/modpack") && method === "DELETE") {
      o.deletes?.push(url);
      return Promise.resolve(jsonRes(baseView({ phase: "Applying" }), 202));
    }
    if (url.includes("/servers/s1/modpack")) return Promise.resolve(jsonRes(o.view ?? baseView()));
    return Promise.resolve(jsonRes({}));
  });
}

function curseforgeTmpl(): GameTemplate {
  return {
    metadata: { name: "t" },
    spec: {
      displayName: "T", game: "g", version: "1", image: "img",
      capabilities: {
        mods: {
          loaders: { l: { path: "mods" } },
          registry: { providers: [{ provider: "curseforge" as "modrinth", modpacks: { refEnv: "CF_MODPACK" } }] },
        },
      },
    },
  };
}

function renderTab() {
  return renderWithQuery(<ModpacksTab name="s1" tmpl={curseforgeTmpl()} />);
}

function renderWithQuery(ui: ReactElement) {
  return baseRenderWithQuery(
    <ResourceTargetProvider
      target={{ cluster: "local", name: "s1", namespace: undefined }}
      access={{
        canWrite: scopedControl, canControl: scopedControl, canConsole: scopedControl, canDelete: false,
        isOwner: false, isCollaborator: false,
        permissions: scopedControl ? ["servers:read", "servers:write"] : ["servers:read"],
      }}
    >
      {ui}
    </ResourceTargetProvider>,
  );
}

describe("Modpacks install flow (env-mode)", () => {
  it("installs a CurseForge pack: Install, picker, confirm, then POST with backup", async () => {
    const posts: unknown[] = [];
    route({ posts, versions: [version("3", "3.0.0"), version("2", "2.0.0")] });
    renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "Install…" }));
    expect(await screen.findByText("Choose a version")).toBeInTheDocument();
    await screen.findByText("3.0.0");
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    fireEvent.click(await screen.findByRole("button", { name: "Install and restart" }));
    await waitFor(() =>
      expect(posts).toEqual([
        {
          provider: "curseforge",
          project: "atm9",
          version: "3",
          title: "All The Mods 9",
          versionName: "3.0.0",
          backupFirst: true,
        },
      ]),
    );
    expect(await screen.findByText("Set modpack All The Mods 9. The server is restarting to install it.")).toBeInTheDocument();
  });

  it("does not let the picker continue without a version", async () => {
    route({ versions: [] });
    renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "Install…" }));
    expect(await screen.findByText(/No versions of this pack match/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
  });

  it("shows a 409 change_in_progress error inside the confirm dialog and keeps it open", async () => {
    route({
      versions: [version("3", "3.0.0")],
      installError: "a modpack change is already in progress; wait for it to finish",
    });
    renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "Install…" }));
    await screen.findByText("3.0.0");
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    fireEvent.click(await screen.findByRole("button", { name: "Install and restart" }));

    expect(
      await screen.findByText("a modpack change is already in progress; wait for it to finish"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Install and restart" })).toBeInTheDocument();
  });

  it("uses the change flow for a newer version of the pack already on the server", async () => {
    route({
      view: baseView({ active: installed, phase: "Applied" }),
      versions: [version("3", "3.0.0"), version("2", "2.0.0"), version("1", "1.0.0")],
    });
    renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "Change version" }));
    expect(await screen.findByRole("heading", { name: "Change version" })).toBeInTheDocument();
    await screen.findByText("3.0.0");
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("button", { name: "Change version and restart" })).toBeInTheDocument();
  });

  it("uses the downgrade flow when an older version is picked", async () => {
    route({
      view: baseView({ active: installed, phase: "Applied" }),
      versions: [version("3", "3.0.0"), version("2", "2.0.0"), version("1", "1.0.0")],
    });
    renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "Change version" }));
    fireEvent.click(await screen.findByText("1.0.0"));
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));

    expect(await screen.findByRole("button", { name: "Downgrade and restart" })).toBeInTheDocument();
  });

  it("removes the pack with backupFirst=false when no backup destination is set", async () => {
    const deletes: string[] = [];
    route({ deletes, view: baseView({ active: installed, phase: "Applied", backupAvailable: false }) });
    renderTab();

    fireEvent.click(await screen.findByRole("button", { name: "Remove" }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove modpack" }));

    await waitFor(() => expect(deletes).toHaveLength(1));
    expect(deletes[0]).toContain("/servers/s1/modpack");
    expect(deletes[0]).toContain("backupFirst=false");
  });

  it("hides a CurseForge pack whose key is not shared and says why", async () => {
    route({
      providers: [{ ...curseforge, modpacksBlockedReason: "curseforge_key_not_shared" }],
    });
    renderTab();

    expect(await screen.findByText(/need the shared CurseForge key/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Install…" })).not.toBeInTheDocument();
  });

  it("disables Install and the card actions for a read-only viewer", async () => {
    route({ asViewer: true, view: baseView({ active: installed, phase: "Applied" }) });
    renderTab();

    expect(await screen.findByRole("button", { name: "Install…" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Change version" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Remove" })).toBeDisabled();
  });
});
