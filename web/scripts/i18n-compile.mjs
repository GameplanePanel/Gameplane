// Compiles every shared locale catalog into an AST-format JSON chunk under
// src/generated/i18n/<tag>.json. The dashboard loads these chunks at runtime
// via import.meta.glob (src/lib/i18n/languages.ts).
//
// Only entries the shared Go checker accepts are compiled (i18n-check
// --valid-out). Invalid, unknown and missing entries are left out, so the
// dashboard shows English for them. The filtered packs come from
// GAMEPLANE_I18N_VALID when a build already has them (the web Docker stage
// copies them in). Otherwise this script runs the checker to write them to
// ../i18n/.valid, which needs Go on PATH.
//
// Node ESM, no dependencies beyond the local @formatjs/cli binary. Skips
// silently when the shared locales directory is absent.
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, rmSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const webRoot = path.resolve(here, "..");
const localesDir = path.resolve(webRoot, "../i18n/locales");
const outDir = path.join(webRoot, "src/generated/i18n");
const formatjsBin = path.join(webRoot, "node_modules/.bin/formatjs");
const i18nDir = path.resolve(webRoot, "../i18n");
const generatedValidDir = path.join(i18nDir, ".valid");

if (!existsSync(localesDir)) {
  process.exit(0);
}

let validDir = process.env.GAMEPLANE_I18N_VALID ? path.resolve(process.env.GAMEPLANE_I18N_VALID) : "";
if (validDir === "") {
  // Exit status 1 means the checker reported findings. Those still come with
  // the filtered packs, and CI fails on them separately. Other failures stop
  // the build.
  rmSync(generatedValidDir, { recursive: true, force: true });
  const run = spawnSync("go", ["run", "./cmd/i18n-check", "--valid-out", generatedValidDir, "locales"], {
    cwd: i18nDir,
    stdio: "inherit",
  });
  if (run.error || (run.status !== 0 && run.status !== 1)) {
    console.error("i18n:compile could not write the filtered language packs");
    process.exit(1);
  }
  validDir = generatedValidDir;
}

mkdirSync(outDir, { recursive: true });

for (const entry of readdirSync(validDir, { withFileTypes: true })) {
  if (!entry.isDirectory()) continue;
  const tag = entry.name;
  const source = path.join(validDir, tag, "app.json");
  if (!existsSync(source)) continue;
  const target = path.join(outDir, `${tag}.json`);
  execFileSync(formatjsBin, ["compile", source, "--ast", "--out-file", target], { stdio: "inherit" });
  console.log(`i18n:compile ${tag} -> src/generated/i18n/${tag}.json`);
}
