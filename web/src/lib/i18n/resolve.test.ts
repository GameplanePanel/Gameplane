import { describe, expect, it } from "vitest";
import { matchListed, resolveLanguage } from "./resolve";

const listed = ["en", "fr", "de", "pt-BR"];

describe("matchListed", () => {
  it("matches exactly, case-insensitively, and returns the listed spelling", () => {
    expect(matchListed("FR", listed)).toBe("fr");
    expect(matchListed("pt-br", listed)).toBe("pt-BR");
  });

  it("falls back from a regional tag to its base language", () => {
    expect(matchListed("fr-CA", listed)).toBe("fr");
    expect(matchListed("de-AT", listed)).toBe("de");
  });

  it("returns null for unlisted, empty, or blank tags", () => {
    expect(matchListed("ja", listed)).toBeNull();
    expect(matchListed("ja-JP", listed)).toBeNull();
    expect(matchListed("", listed)).toBeNull();
    expect(matchListed("   ", listed)).toBeNull();
  });
});

describe("resolveLanguage", () => {
  it("prefers the user choice over every other step", () => {
    expect(
      resolveLanguage({
        userChoice: "de",
        browserChoice: "fr",
        navigatorLanguages: ["fr"],
        instanceDefault: "fr",
        listed,
      }),
    ).toBe("de");
  });

  it("uses the browser choice when there is no user choice", () => {
    expect(
      resolveLanguage({ browserChoice: "fr", navigatorLanguages: ["de"], instanceDefault: "de", listed }),
    ).toBe("fr");
  });

  it("walks navigator languages in order, with regional before base", () => {
    expect(resolveLanguage({ navigatorLanguages: ["ja", "fr-CA", "de"], listed })).toBe("fr");
    expect(resolveLanguage({ navigatorLanguages: ["pt-BR"], listed })).toBe("pt-BR");
  });

  it("uses the instance default when no earlier step matches", () => {
    expect(resolveLanguage({ navigatorLanguages: ["ja"], instanceDefault: "de", listed })).toBe("de");
  });

  it("skips unlisted candidates at every step", () => {
    expect(
      resolveLanguage({
        userChoice: "ja",
        browserChoice: "ko",
        navigatorLanguages: ["zh-CN"],
        instanceDefault: "it",
        listed,
      }),
    ).toBe("en");
  });

  it("falls back to en when nothing is provided", () => {
    expect(resolveLanguage({ listed: [] })).toBe("en");
    expect(resolveLanguage({ userChoice: null, browserChoice: null, instanceDefault: null, listed })).toBe("en");
    expect(resolveLanguage({ userChoice: "", listed })).toBe("en");
  });
});
