import { describe, it, expect, vi } from "vitest";
import { http, HttpResponse } from "msw";
import { screen } from "@testing-library/react";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { ModpackVersionPicker } from "./modpack-version-picker";
import type { RegistryVersion } from "@/types";

const VERSIONS_PATH = "/servers/test/mods/registry/projects/proj-1/versions";
const MC = "Minecraft 1.21.1 · NeoForge";

const base = (id: string): RegistryVersion => ({
  id,
  name: `Pack ${id}`,
  versionNumber: id,
  gameVersions: ["1.21.1"],
  loaders: ["neoforge"],
  files: [],
});

function mount() {
  renderWithQuery(
    <ModpackVersionPicker
      open
      onOpenChange={vi.fn()}
      name="test"
      provider="curseforge"
      project="proj-1"
      title="All the Mods"
      mcLabel={MC}
      onContinue={vi.fn()}
    />,
  );
}

describe("ModpackVersionPicker release metadata", () => {
  it("shows release and beta chips and the publish date", async () => {
    server.use(
      http.get(VERSIONS_PATH, () =>
        HttpResponse.json([
          { ...base("3"), releaseType: "release", publishedAt: "2026-11-14T10:00:00Z" },
          { ...base("2"), releaseType: "beta", publishedAt: "2026-10-24T10:00:00Z" },
        ]),
      ),
    );
    mount();
    expect(await screen.findByText("Release")).toBeInTheDocument();
    expect(screen.getByText("Beta")).toBeInTheDocument();
    expect(screen.getByText(/Published .*2026/, { selector: "span" })).toBeInTheDocument();
  });

  it("renders neither chip nor a publish line without metadata", async () => {
    server.use(http.get(VERSIONS_PATH, () => HttpResponse.json([base("1")])));
    mount();
    await screen.findByRole("radio");
    expect(screen.queryByText("Release")).not.toBeInTheDocument();
    expect(screen.queryByText("Beta")).not.toBeInTheDocument();
    expect(screen.queryByText(/Published/)).not.toBeInTheDocument();
    expect(screen.getByText("neoforge 1.21.1")).toBeInTheDocument();
  });
});
