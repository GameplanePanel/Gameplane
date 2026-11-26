import { describe, it, expect, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen } from "@testing-library/react";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { ModpackVersionPicker, serverVersionSwitch } from "./modpack-version-picker";
import type { RegistryVersion } from "@/types";

const VERSIONS_PATH = "/servers/test/mods/registry/projects/proj-1/versions";
const ALL_LOADERS_SUBTITLE = "Showing every version this server can run, newest first.";
const MC = "Minecraft 1.21.1 · Fabric";

const runsOn: RegistryVersion = {
  id: "1",
  name: "Pack 1.11.0",
  versionNumber: "1.11.0",
  gameVersions: ["26.1.2"],
  loaders: ["neoforge"],
  serverVersion: "neoforge-26",
  serverVersionName: "26.1.2 · NeoForge",
  files: [],
};

const sameAsServer: RegistryVersion = {
  id: "2",
  name: "Pack 1.10.4",
  versionNumber: "1.10.4",
  gameVersions: ["1.21.1"],
  loaders: ["fabric"],
  serverVersion: "latest-fabric",
  serverVersionName: "Latest · Fabric",
  files: [],
};

function mount(props: Partial<Parameters<typeof ModpackVersionPicker>[0]> = {}) {
  const onContinue = vi.fn();
  renderWithQuery(
    <ModpackVersionPicker
      open
      onOpenChange={vi.fn()}
      name="test"
      provider="ftb"
      project="proj-1"
      title="FTB Evolution 2"
      mcLabel={MC}
      onContinue={onContinue}
      allLoaders
      currentVersionId="latest-fabric"
      {...props}
    />,
  );
  return { onContinue };
}

describe("serverVersionSwitch", () => {
  it("returns the server version only when it differs from the current one", () => {
    expect(serverVersionSwitch(runsOn, "latest-fabric")).toBe("neoforge-26");
    expect(serverVersionSwitch(sameAsServer, "latest-fabric")).toBeUndefined();
    expect(serverVersionSwitch({ ...runsOn, serverVersion: undefined }, "latest-fabric")).toBeUndefined();
  });
});

describe("ModpackVersionPicker All loaders", () => {
  it("asks the API for every loader and shows the all-loaders subtitle", async () => {
    const seen: (string | null)[] = [];
    server.use(
      http.get(VERSIONS_PATH, ({ request }) => {
        seen.push(new URL(request.url).searchParams.get("allLoaders"));
        return HttpResponse.json([runsOn, sameAsServer]);
      }),
    );
    mount();
    expect(await screen.findByText(ALL_LOADERS_SUBTITLE)).toBeInTheDocument();
    expect(seen).toEqual(["true"]);
  });

  it("adds a Runs on line only to rows that need another server version", async () => {
    server.use(http.get(VERSIONS_PATH, () => HttpResponse.json([runsOn, sameAsServer])));
    mount();
    expect(await screen.findByText("Runs on 26.1.2 · NeoForge")).toBeInTheDocument();
    expect(screen.getAllByText(/^Runs on /)).toHaveLength(1);
  });

  it("keeps the filtered subtitle and no Runs on line when All loaders is off", async () => {
    const seen: (string | null)[] = [];
    server.use(
      http.get(VERSIONS_PATH, ({ request }) => {
        seen.push(new URL(request.url).searchParams.get("allLoaders"));
        return HttpResponse.json([sameAsServer]);
      }),
    );
    mount({ allLoaders: false });
    expect(await screen.findByText(`Showing versions for ${MC}, newest first.`)).toBeInTheDocument();
    expect(screen.queryByText(ALL_LOADERS_SUBTITLE)).not.toBeInTheDocument();
    expect(seen).toEqual([null]);
  });

  it("explains an empty list when the server can run no version of the pack", async () => {
    server.use(http.get(VERSIONS_PATH, () => HttpResponse.json([])));
    mount();
    expect(await screen.findByText("No versions are available for this server.")).toBeInTheDocument();
    expect(screen.getByText("This server can't run any version of this pack.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue" })).toBeDisabled();
  });
});
