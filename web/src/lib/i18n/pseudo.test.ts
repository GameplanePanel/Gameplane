import { describe, expect, it } from "vitest";
import { parse, TYPE } from "@formatjs/icu-messageformat-parser";
import type { MessageFormatElement } from "@formatjs/icu-messageformat-parser";
import {
  accent,
  buildPseudoCatalog,
  pseudoLiteral,
  pseudoLocalizeAst,
  pseudoLocalizeMessage,
  pseudoManifestEntry,
} from "../../../scripts/i18n-pseudo-lib.mjs";

// shape strips literal text so two ASTs can be compared by structure only.
function shape(elements: MessageFormatElement[]): unknown {
  return elements.map((el) => {
    switch (el.type) {
      case TYPE.literal:
        return "lit";
      case TYPE.tag:
        return { tag: el.value, children: shape(el.children) };
      case TYPE.plural:
      case TYPE.select:
        return {
          type: el.type,
          value: el.value,
          options: Object.fromEntries(Object.entries(el.options).map(([k, o]) => [k, shape(o.value)])),
        };
      default:
        return { type: el.type, value: (el as { value: string }).value };
    }
  });
}

describe("pseudo-locale transform", () => {
  it("accents letters and leaves other characters alone", () => {
    expect(accent("Save 1!")).toBe("Šáṽé 1!");
  });

  it("lengthens literals by about 40% and leaves whitespace alone", () => {
    const out = pseudoLiteral("Start server");
    expect([...out].length).toBe(12 + Math.ceil(12 * 0.4));
    expect(pseudoLiteral("  ")).toBe("  ");
  });

  it("wraps the message in brackets", () => {
    const ast = pseudoLocalizeMessage("Hello");
    expect(ast[0]).toEqual({ type: TYPE.literal, value: "[" });
    expect(ast[ast.length - 1]).toEqual({ type: TYPE.literal, value: "]" });
  });

  it("keeps placeholders, plural, select, pound and rich-text tags intact", () => {
    const message =
      "Hi {name}, you have {count, plural, one {# server} other {# servers}} <b>online</b> {kind, select, a {Alpha} other {Other}}";
    const original = parse(message);
    const pseudo = pseudoLocalizeMessage(message);
    const inner = pseudo.slice(1, -1);
    expect(shape(inner)).toEqual(shape(original));
    const plural = inner.find((el) => el.type === TYPE.plural);
    expect(plural?.type === TYPE.plural && plural.options.one?.value[0]?.type).toBe(TYPE.pound);
  });

  it("accents literals nested in tags and plural options", () => {
    const ast = pseudoLocalizeMessage("<b>bold</b> {n, plural, other {items}}");
    const text = JSON.stringify(ast);
    expect(text).toContain("ƀóļð");
    expect(text).toContain("íţéɱš");
    expect(text).not.toContain('"bold"');
  });

  it("is idempotent in structure when applied to an AST directly", () => {
    const ast = pseudoLocalizeAst(parse("Plain text"));
    expect(ast).toHaveLength(3);
  });

  it("builds a catalog keyed by id, sorted", () => {
    const catalog = buildPseudoCatalog({
      "b.id": { defaultMessage: "Two" },
      "a.id": { defaultMessage: "One {x}" },
    });
    expect(Object.keys(catalog)).toEqual(["a.id", "b.id"]);
    expect(catalog["a.id"]?.some((el) => el.type === TYPE.argument)).toBe(true);
  });

  it("declares a listed left-to-right manifest row for en-XA", () => {
    expect(pseudoManifestEntry).toMatchObject({ tag: "en-XA", nativeName: "Pseudo", direction: "ltr", listed: true });
  });
});
