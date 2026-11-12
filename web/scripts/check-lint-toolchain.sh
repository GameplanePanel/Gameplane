#!/usr/bin/env bash
# Run from web/ after npm ci, using the dashboard's installed tools and lint configs.
set -euo pipefail

# npm query checks installed packages by their real name, including aliases.
npm query '[name=typescript]' | jq -e 'map(.version) | unique
  | (length == 1 and (.[0] | split(".")[0] | tonumber) >= 7)
    or error("Expected one native TypeScript version, resolved \(.)")' >/dev/null

probe_dir=$(mktemp -d src/lint-probe.XXXXXX)
trap 'rm -r -- "$probe_dir"' EXIT
probe="$probe_dir/Probe.tsx"
cat >"$probe" <<'TSX'
import { useState } from "react";
declare function save(): Promise<void>;
save();
void save();
export async function submit() { await save(); }
export function Label({ enabled }: { enabled: boolean }) {
  if (enabled) useState(false);
  return <span>Untranslated</span>;
}
TSX

node_modules/.bin/eslint --format json "$probe" >"$probe_dir/eslint.json" || test "$?" -eq 1
jq -e '.[0].messages | map(select(.severity == 2).ruleId)
  | contains(["react-hooks/rules-of-hooks", "formatjs/no-literal-string-in-jsx"])
    or error("Expected Hooks and FormatJS errors, got \(.)")' "$probe_dir/eslint.json" >/dev/null

node_modules/.bin/oxlint --format json "$probe" >"$probe_dir/oxlint.json" || test "$?" -eq 1
# Only the bare save() on line 3 should be reported.
jq -e '[.diagnostics[] | select(.code == "typescript(no-floating-promises)" and .severity == "error")
  | .labels[0].span.line] | . == [3]
    or error("Expected a floating promise only on line 3, got \(.)")' "$probe_dir/oxlint.json" >/dev/null
