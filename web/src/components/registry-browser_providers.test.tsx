import { describe, it, expect } from "vitest";
import { http, HttpResponse } from "msw";
import { screen } from "@testing-library/react";
import { server } from "@/test/server";
import { renderWithQuery } from "@/test/render";
import { RegistryBrowser } from "./registry-browser";

function seed(providers: Record<string, unknown>[]) {
  server.use(
    http.get("/servers/test/mods/registry/providers", () => HttpResponse.json(providers)),
    http.get("/servers/test/mods/registry/search", () => HttpResponse.json([])),
  );
}

const renderItem = () => null;

describe("RegistryBrowser provider filtering", () => {
  it("hides providers with modsBrowse:false from the mod browser", async () => {
    seed([
      { provider: "modrinth", available: true, mods: true, modpacks: true },
      { provider: "curseforge", available: true, mods: true, modpacks: true, modsBrowse: false },
    ]);
    renderWithQuery(<RegistryBrowser name="test" type="mod" renderItem={renderItem} />);
    await screen.findByPlaceholderText(/search/i);
    expect(screen.queryByRole("tab", { name: "CurseForge" })).not.toBeInTheDocument();
  });

  it("still shows providers whose modsBrowse is undefined", async () => {
    seed([
      { provider: "modrinth", available: true, mods: true, modpacks: true },
      { provider: "curseforge", available: true, mods: true, modpacks: true },
    ]);
    renderWithQuery(<RegistryBrowser name="test" type="mod" renderItem={renderItem} />);
    expect(await screen.findByRole("tab", { name: "CurseForge" })).toBeInTheDocument();
  });

  it("hides a blocked provider from the modpack switch and shows the hint", async () => {
    seed([
      { provider: "modrinth", available: true, mods: true, modpacks: true },
      {
        provider: "curseforge",
        available: true,
        mods: true,
        modpacks: true,
        modpacksBlockedReason: "curseforge_key_not_shared",
      },
    ]);
    renderWithQuery(<RegistryBrowser name="test" type="modpack" renderItem={renderItem} />);
    expect(await screen.findByText(/need the shared CurseForge key/i)).toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "CurseForge" })).not.toBeInTheDocument();
  });

  it("does not show the hint on the mod browser", async () => {
    seed([
      { provider: "curseforge", available: true, mods: true, modpacks: true, modpacksBlockedReason: "curseforge_key_not_shared" },
    ]);
    renderWithQuery(<RegistryBrowser name="test" type="mod" renderItem={renderItem} />);
    await screen.findByPlaceholderText(/search/i);
    expect(screen.queryByText(/need the shared CurseForge key/i)).not.toBeInTheDocument();
  });
});
