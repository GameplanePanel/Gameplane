// Rebuilds the English reference catalog `i18n/locales/en/app.json`: every
// message the dashboard source uses (from `formatjs extract`, which is written
// to src/generated/i18n/extracted.json and left untouched here) plus every
// server.*, condition.* and notify.* entry already in the reference. Written
// back sorted by key so the output is stable across runs. CI compares this
// file against the extraction (`i18n-check --extracted`); drift is an error.
//
// Node ESM, no dependencies. Run via `npm run i18n:extract`.
import { readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(here, "..");
const extractedPath = path.join(webRoot, "src/generated/i18n/extracted.json");
const referencePath = path.join(webRoot, "../i18n/locales/en/app.json");

const SERVER_PREFIXES = ["server.", "condition.", "notify."];

const extracted = JSON.parse(readFileSync(extractedPath, "utf8"));
const reference = JSON.parse(readFileSync(referencePath, "utf8"));

const merged = {};
for (const [id, entry] of Object.entries(reference)) {
  if (SERVER_PREFIXES.some((prefix) => id.startsWith(prefix))) {
    merged[id] = entry;
  }
}
for (const [id, entry] of Object.entries(extracted)) {
  merged[id] = { defaultMessage: entry.defaultMessage, description: entry.description };
}

const sorted = {};
for (const key of Object.keys(merged).sort()) {
  sorted[key] = merged[key];
}

writeFileSync(referencePath, JSON.stringify(sorted, null, 2) + "\n");
console.log(`i18n:extract wrote ${Object.keys(sorted).length} message(s) to i18n/locales/en/app.json`);
