import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LANGUAGE_STORAGE_KEY, readLanguageStorage, writeLanguageStorage } from "./storage";

describe("language storage", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
  });

  it("reads nulls when nothing is stored", () => {
    expect(readLanguageStorage()).toEqual({ choice: null, applied: null });
  });

  it("reads choice and applied when stored", () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, JSON.stringify({ choice: "fr", applied: "fr" }));
    expect(readLanguageStorage()).toEqual({ choice: "fr", applied: "fr" });
  });

  it("treats bad JSON, non-objects, and wrong field types as empty", () => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "{not json");
    expect(readLanguageStorage()).toEqual({ choice: null, applied: null });

    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "42");
    expect(readLanguageStorage()).toEqual({ choice: null, applied: null });

    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "null");
    expect(readLanguageStorage()).toEqual({ choice: null, applied: null });

    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, JSON.stringify({ choice: 7, applied: ["fr"] }));
    expect(readLanguageStorage()).toEqual({ choice: null, applied: null });
  });

  it("merges a patch into the stored value", () => {
    writeLanguageStorage({ choice: "fr" });
    writeLanguageStorage({ applied: "fr" });
    expect(readLanguageStorage()).toEqual({ choice: "fr", applied: "fr" });
  });

  it("clears the choice when the patch sets it to null", () => {
    writeLanguageStorage({ choice: "de", applied: "de" });
    writeLanguageStorage({ choice: null });
    expect(readLanguageStorage()).toEqual({ choice: null, applied: "de" });
  });

  it("ignores read failures from a throwing localStorage", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(readLanguageStorage()).toEqual({ choice: null, applied: null });
  });

  it("ignores write failures from a throwing localStorage", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(() => writeLanguageStorage({ choice: "fr" })).not.toThrow();
  });
});
