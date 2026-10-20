import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Modules } from "./endpoints";

const fetchMock = vi.fn();

beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
  fetchMock.mockImplementation(
    async () =>
      new Response(JSON.stringify({ "template.displayName": "Jeu" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
  );
});

afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
});

describe("Modules.locales", () => {
  it("GETs the module's translations for a tag", async () => {
    const got = await Modules.locales("mc", "fr");
    expect(got).toEqual({ "template.displayName": "Jeu" });
    const call = fetchMock.mock.calls.at(-1);
    expect(call).toBeDefined();
    expect(call![0]).toBe("/modules/mc/locales/fr");
    expect(((call![1] ?? {}) as { method?: string }).method ?? "GET").toBe("GET");
  });

  it("encodes the tag", async () => {
    await Modules.locales("mc", "pt BR/x");
    const call = fetchMock.mock.calls.at(-1);
    expect(call![0]).toBe("/modules/mc/locales/pt%20BR%2Fx");
  });
});
