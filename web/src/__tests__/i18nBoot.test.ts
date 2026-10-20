import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import html from "../../index.html?raw";

const bootScript = /<script id="i18n-boot">([\s\S]*?)<\/script>/.exec(html)?.[1];
if (!bootScript) throw new Error("i18n boot script not found in index.html");

// The boot script runs against the page's own globals (document, localStorage),
// which vitest's jsdom environment provides, so it is evaluated directly.
const runBoot = new Function(bootScript) as () => void;

describe("i18n boot script", () => {
  beforeEach(() => {
    document.documentElement.lang = "en";
    document.documentElement.dir = "ltr";
    localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
  });

  it("applies the language recorded as applied", () => {
    localStorage.setItem("gameplane-language", JSON.stringify({ choice: "fr", applied: "fr" }));
    runBoot();
    expect(document.documentElement.lang).toBe("fr");
    expect(document.documentElement.dir).toBe("ltr");
  });

  it("accepts regional tags", () => {
    localStorage.setItem("gameplane-language", JSON.stringify({ applied: "pt-BR" }));
    runBoot();
    expect(document.documentElement.lang).toBe("pt-BR");
  });

  it("stays on en when the stored value is not valid JSON", () => {
    localStorage.setItem("gameplane-language", "{garbage");
    runBoot();
    expect(document.documentElement.lang).toBe("en");
  });

  it("stays on en when the stored value is null or has no applied field", () => {
    localStorage.setItem("gameplane-language", "null");
    runBoot();
    expect(document.documentElement.lang).toBe("en");

    localStorage.setItem("gameplane-language", JSON.stringify({ choice: "fr" }));
    runBoot();
    expect(document.documentElement.lang).toBe("en");
  });

  it("ignores an applied value that is not a language tag", () => {
    localStorage.setItem("gameplane-language", JSON.stringify({ applied: "<x>" }));
    runBoot();
    expect(document.documentElement.lang).toBe("en");
  });

  it("stays on en when localStorage throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => runBoot()).not.toThrow();
    expect(document.documentElement.lang).toBe("en");
  });
});
