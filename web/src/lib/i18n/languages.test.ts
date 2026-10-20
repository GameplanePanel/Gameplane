import { describe, expect, it, vi } from "vitest";
import {
  ENGLISH,
  allLanguages,
  directionFor,
  isListed,
  listedLanguages,
  loadCatalogFrom,
  parseManifests,
  type LanguageInfo,
} from "./languages";

const fr: LanguageInfo = {
  tag: "fr",
  nativeName: "Français",
  englishName: "French",
  direction: "ltr",
  completeness: 80,
  listed: true,
};

describe("parseManifests", () => {
  it("always includes English, prepended when the manifest omits it", () => {
    expect(parseManifests({})).toEqual([ENGLISH]);
    expect(parseManifests({ "/x.json": [fr] })).toEqual([ENGLISH, fr]);
  });

  it("keeps the manifest's English row when present", () => {
    const manifestEn: LanguageInfo = { ...ENGLISH, completeness: 99 };
    expect(parseManifests({ "/x.json": [manifestEn, fr] })).toEqual([manifestEn, fr]);
  });

  it("skips non-array modules and malformed rows", () => {
    const bad = { tag: "xx" } as unknown as LanguageInfo;
    expect(parseManifests({ "/x.json": undefined, "/y.json": [bad, fr] })).toEqual([ENGLISH, fr]);
  });
});

describe("manifest accessors", () => {
  it("contain English and report it as listed", () => {
    expect(allLanguages().map((row) => row.tag)).toContain("en");
    expect(isListed("en")).toBe(true);
    expect(listedLanguages().some((row) => row.tag === "en")).toBe(true);
  });

  it("report unknown tags as unlisted and default their direction to ltr", () => {
    expect(isListed("zz")).toBe(false);
    expect(directionFor("zz")).toBe("ltr");
    expect(directionFor("ar", [{ ...fr, tag: "ar", direction: "rtl" }])).toBe("rtl");
  });
});

describe("loadCatalogFrom", () => {
  it("returns the chunk for a tag with a generated catalog", async () => {
    const chunk = { "server.generic.x": [] };
    const loaders = { "/src/generated/i18n/fr.json": vi.fn(() => Promise.resolve(chunk)) };
    await expect(loadCatalogFrom(loaders, "fr")).resolves.toBe(chunk);
    expect(loaders["/src/generated/i18n/fr.json"]).toHaveBeenCalledOnce();
  });

  it("returns null when no chunk exists for the tag", async () => {
    await expect(loadCatalogFrom({}, "de")).resolves.toBeNull();
  });

  it("returns null when the chunk is not an object", async () => {
    const loaders = { "/src/generated/i18n/fr.json": () => Promise.resolve([1, 2]) };
    await expect(loadCatalogFrom(loaders, "fr")).resolves.toBeNull();
    const nullLoader = { "/src/generated/i18n/fr.json": () => Promise.resolve(null) };
    await expect(loadCatalogFrom(nullLoader, "fr")).resolves.toBeNull();
  });

  it("does not load a file whose tag is not a plain catalog key", async () => {
    const loaders = { "/src/generated/i18n/fr.json": () => Promise.resolve({}) };
    await expect(loadCatalogFrom(loaders, "../languages")).resolves.toBeNull();
  });
});
