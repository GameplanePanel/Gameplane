import { afterEach, describe, expect, it, vi } from "vitest";
import { Modules, ModuleSources, ModuleBuilder } from "./endpoints";
import { api } from "./api";

afterEach(() => vi.unstubAllGlobals());

describe("explicit module workload targets", () => {
  it("pins module mutations, raw uploads and builder installation to a remote", async () => {
    const fetch = vi.fn(async (_path: string) => new Response("{}", { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetch);
    await Modules.install({ source: "uploaded", module: "minecraft", name: "minecraft" }, "east");
    await Modules.upgrade("minecraft", "2", "east");
    await Modules.uninstall("minecraft", "east");
    await ModuleSources.create("uploaded", { type: "upload" }, "east");
    await ModuleSources.upload("uploaded", new Blob(["bundle"]), { dryRun: true }, "east");
    await ModuleSources.removeUpload("uploaded", "minecraft", "east");
    await ModuleBuilder.installToCluster({ targetSource: "uploaded", name: "minecraft", moduleYaml: "", templateYaml: "", readmeMd: "" }, "east");
    await ModuleBuilder.archetypes("east");
    await ModuleBuilder.scaffold({ name: "minecraft" }, "east");
    await ModuleBuilder.validate({ moduleYaml: "", templateYaml: "" }, "east");
    await ModuleBuilder.preview({ templateYaml: "" }, "east");
    await ModuleBuilder.downloadArchive({ name: "minecraft", moduleYaml: "", templateYaml: "", readmeMd: "" }, "east");
    for (const call of fetch.mock.calls) {
      expect(new URL(call[0], "http://localhost").searchParams.get("cluster")).toBe("east");
    }
    expect(fetch).toHaveBeenCalledTimes(12);
  });

  it("keeps central management unscoped while allowing an explicit remote module target", async () => {
    const fetch = vi.fn(async (_path: string) => new Response("{}", { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetch);
    await api("/admin/config", { cluster: "east" });
    await Modules.catalog("east");
    await Modules.catalog();
    expect(fetch.mock.calls.map(([path]) => path)).toEqual(["/admin/config", "/modules/catalog?cluster=east", "/modules/catalog"]);
  });
});
