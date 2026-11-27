import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { deriveCustomThemeTokens, type CustomThemeTokens } from "./theme-derivation";

// The Go themepkg.Derive port reads the same fixture, so both implementations
// are pinned to identical output (spec 043).
interface DerivationFixture {
  comment: string;
  cases: { accent: string; surface: string; tokens: CustomThemeTokens }[];
}

const fixturePath = join(
  dirname(fileURLToPath(import.meta.url)),
  "..",
  "..",
  "..",
  "themepkg",
  "testdata",
  "derivation.json",
);
const fixture = JSON.parse(readFileSync(fixturePath, "utf8")) as DerivationFixture;

describe("theme derivation fixture (shared with themepkg)", () => {
  it("has cases", () => {
    expect(fixture.cases.length).toBeGreaterThan(0);
  });

  it.each(fixture.cases)("derives $accent on $surface", ({ accent, surface, tokens }) => {
    expect(deriveCustomThemeTokens(accent, surface)).toEqual(tokens);
  });
});
