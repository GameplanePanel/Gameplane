// Writes the fixture language used by the i18n E2E specs (spec 019, T046):
// a listed language "zz" with 0% completeness and an empty compiled catalog,
// so the specs can switch languages and see the English fallback text.
//
// Run by `npm run i18n:fixtures`, and by vite.config.ts when
// VITE_I18N_FIXTURES=1. Release builds never run it, so the generated files
// never exist there and languages.json never contains "zz".
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const outDir = path.resolve(here, "../src/generated/i18n");

const fixtureManifest = [
  {
    tag: "zz",
    nativeName: "Fixture 0%",
    englishName: "Fixture",
    direction: "ltr",
    completeness: 0,
    listed: true,
  },
];

mkdirSync(outDir, { recursive: true });
writeFileSync(path.join(outDir, "languages.fixtures.json"), `${JSON.stringify(fixtureManifest, null, 2)}\n`);
writeFileSync(path.join(outDir, "zz.json"), "{}\n");
console.log("i18n:fixtures wrote languages.fixtures.json and zz.json");
