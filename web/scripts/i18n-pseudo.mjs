// Writes the pseudo-language "en-XA" used to spot untranslated or clipped text
// (spec 019, T099, FR-038): src/generated/i18n/en-XA.json is compiled from the
// shared English reference with accented, ~40% longer, bracketed text, keeping
// ICU placeholders, plural/select syntax and rich-text tags untouched. A
// manifest row is written to languages.pseudo.json, which languages.ts merges
// the same way as the fixture manifest.
//
// Run by `npm run i18n:pseudo`, and by vite.config.ts when VITE_I18N_PSEUDO=1.
// Release builds never run it, so the generated files never exist there and
// languages.json never contains "en-XA".
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildPseudoCatalog, pseudoManifestEntry } from "./i18n-pseudo-lib.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const outDir = path.resolve(here, "../src/generated/i18n");
const referencePath = path.resolve(here, "../../i18n/locales/en/app.json");

const reference = JSON.parse(readFileSync(referencePath, "utf8"));
const catalog = buildPseudoCatalog(reference);

mkdirSync(outDir, { recursive: true });
writeFileSync(path.join(outDir, "en-XA.json"), `${JSON.stringify(catalog)}\n`);
writeFileSync(path.join(outDir, "languages.pseudo.json"), `${JSON.stringify([pseudoManifestEntry], null, 2)}\n`);
console.log(`i18n:pseudo wrote en-XA.json (${Object.keys(catalog).length} messages) and languages.pseudo.json`);
