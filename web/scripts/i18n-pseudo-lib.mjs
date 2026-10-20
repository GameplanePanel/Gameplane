// Pseudo-localization transform for the en-XA test language (spec 019, T099,
// FR-038). Pure functions only; scripts/i18n-pseudo.mjs does the file I/O.
//
// The transform works on the parsed ICU AST, so placeholders, plural and
// select syntax, the pound sign and rich-text tags are never touched: only
// literal text is accented and lengthened.
import { parse, TYPE } from "@formatjs/icu-messageformat-parser";

const ACCENTS = {
  a: "á", b: "ƀ", c: "ç", d: "ð", e: "é", f: "ƒ", g: "ĝ", h: "ĥ", i: "í", j: "ĵ",
  k: "ķ", l: "ļ", m: "ɱ", n: "ñ", o: "ó", p: "þ", q: "ǫ", r: "ŕ", s: "š", t: "ţ",
  u: "ú", v: "ṽ", w: "ŵ", x: "ẋ", y: "ý", z: "ž",
  A: "Á", B: "Ɓ", C: "Ç", D: "Ð", E: "É", F: "Ƒ", G: "Ĝ", H: "Ĥ", I: "Í", J: "Ĵ",
  K: "Ķ", L: "Ļ", M: "Ṁ", N: "Ñ", O: "Ó", P: "Þ", Q: "Ǫ", R: "Ŕ", S: "Š", T: "Ţ",
  U: "Ú", V: "Ṽ", W: "Ŵ", X: "Ẋ", Y: "Ý", Z: "Ž",
};

// Padding repeats this cycle so lengthened text stays readable as pseudo text.
const PADDING = "~·";

export const PSEUDO_TAG = "en-XA";
export const LENGTH_FACTOR = 0.4;

// accent replaces ASCII letters with accented look-alikes.
export function accent(text) {
  let out = "";
  for (const ch of text) out += ACCENTS[ch] ?? ch;
  return out;
}

// pseudoLiteral accents a run of literal text and appends padding so it is
// about 40% longer. Whitespace-only runs are left alone.
export function pseudoLiteral(text) {
  if (text.trim() === "") return text;
  const extra = Math.ceil([...text].length * LENGTH_FACTOR);
  let pad = "";
  for (let i = 0; i < extra; i += 1) pad += PADDING[i % PADDING.length];
  return accent(text) + pad;
}

function transformElements(elements) {
  return elements.map((el) => {
    switch (el.type) {
      case TYPE.literal:
        return { ...el, value: pseudoLiteral(el.value) };
      case TYPE.tag:
        return { ...el, children: transformElements(el.children) };
      case TYPE.plural:
      case TYPE.select: {
        const options = {};
        for (const [key, option] of Object.entries(el.options)) {
          options[key] = { ...option, value: transformElements(option.value) };
        }
        return { ...el, options };
      }
      default:
        return el;
    }
  });
}

// pseudoLocalizeAst returns the pseudo-localized AST for one parsed message,
// wrapped in square brackets.
export function pseudoLocalizeAst(elements) {
  return [
    { type: TYPE.literal, value: "[" },
    ...transformElements(elements),
    { type: TYPE.literal, value: "]" },
  ];
}

// pseudoLocalizeMessage parses an ICU message and returns its compiled AST in
// the same shape `formatjs compile --ast` writes.
export function pseudoLocalizeMessage(message) {
  return pseudoLocalizeAst(parse(message));
}

// buildPseudoCatalog maps every id of the English reference ({id: {defaultMessage}})
// to its pseudo-localized AST.
export function buildPseudoCatalog(reference) {
  const catalog = {};
  for (const id of Object.keys(reference).sort()) {
    catalog[id] = pseudoLocalizeMessage(reference[id].defaultMessage);
  }
  return catalog;
}

// pseudoManifestEntry is the language-manifest row merged in only when
// VITE_I18N_PSEUDO=1.
export const pseudoManifestEntry = {
  tag: PSEUDO_TAG,
  nativeName: "Pseudo",
  englishName: "Pseudo-locale",
  direction: "ltr",
  completeness: 100,
  listed: true,
};
